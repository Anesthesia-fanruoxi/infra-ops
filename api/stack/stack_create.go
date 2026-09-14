package stack

import (
	"encoding/json"
	"fmt"
	"strings"

	"infra-ops/api/deploy"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/stackkit"
)

// createAndRun 建单并启动套件运行：按 op 分支做参数合并、拓扑/角色校验、主机装配与角色物化。
func (h *stackHandler) createAndRun(req stackRunReq, remoteIP string) (int64, error) {
	op := strings.TrimSpace(req.Op)
	if op == "" {
		op = "create"
	}
	switch op {
	case "create", "reinstall", "scale_out", "scale_in", "add_component", "uninstall", "remove_component":
	default:
		return 0, fmt.Errorf("不支持的操作: %s", op)
	}

	var inst *model.StackInstance
	if op != "create" {
		if req.InstanceID <= 0 {
			return 0, fmt.Errorf("缺少集群实例")
		}
		full, err := h.repo.GetInstanceFull(req.InstanceID)
		if err != nil || full == nil {
			return 0, fmt.Errorf("集群实例不存在")
		}
		busy, err := h.repo.InstanceHasRunning(full.ID)
		if err != nil {
			return 0, err
		}
		if busy {
			return 0, fmt.Errorf("该集群有正在执行的流程，请稍后再试")
		}
		inst = full
		req.StackKey = inst.StackKey
		req.Mode = inst.Mode
	}

	d := store.FindBuiltinStack(req.StackKey)
	if d == nil {
		return 0, fmt.Errorf("套件不存在")
	}
	bp := d.Blueprint()
	if strings.TrimSpace(req.Mode) == "" {
		return 0, fmt.Errorf("请选择模式")
	}
	mode := stackkit.ModeDef(bp, req.Mode)
	if mode == nil {
		return 0, fmt.Errorf("不支持的模式: %s", req.Mode)
	}
	ids := deploy.DedupInt64(req.HostIDs)
	if (op == "uninstall" || op == "remove_component") && len(ids) == 0 && inst != nil {
		for _, h := range activeInstanceHosts(inst) {
			ids = append(ids, h.HostID)
		}
	}
	if len(ids) == 0 && op != "reinstall" {
		if op == "uninstall" && inst != nil {
			_ = h.repo.UpdateInstance(inst.ID, "", "uninstalled", "", "")
			return 0, nil
		}
		return 0, fmt.Errorf("请选择主机")
	}

	params := req.Params
	if params == nil {
		params = map[string]string{}
	}
	reqComponents := strings.TrimSpace(params["components"])
	if inst != nil {
		base := map[string]string{}
		_ = json.Unmarshal([]byte(inst.ParamsJSON), &base)
		for k, v := range base {
			if strings.TrimSpace(params[k]) == "" {
				params[k] = v
			}
		}
	}

	if op == "create" {
		if err := validateStackTopology(d, mode, len(ids), req.MasterHostID, ids, params); err != nil {
			return 0, err
		}
	}
	// 套件专属的创建/加装/卸载前置校验（ES 冷热温角色组合；大数据底座的落点、组件依赖与增量）。
	// 引擎只把请求上下文交给驱动，逐条报错文案与校验顺序由套件给出（stackkit.CreateValidator）。
	if op == "create" || op == "add_component" || op == "remove_component" {
		masterID := req.MasterHostID
		if op == "add_component" {
			masterID = instanceMasterHostID(inst)
		}
		if v, ok := d.(stackkit.CreateValidator); ok {
			if err := v.ValidateCreate(stackkit.ValidateCtx{
				Op: op, Mode: req.Mode, Params: params, ReqComponents: reqComponents,
				HostIDs: ids, MasterID: masterID, HostParams: req.HostParams,
				Instance: inst, LookupHost: h.hostLookup,
			}); err != nil {
				return 0, err
			}
		}
	}
	// 创建期套件默认值（Kafka 集群 ID / RabbitMQ Erlang Cookie 未填则自动生成，就地写入 params）。
	if op == "create" {
		if dp, ok := d.(stackkit.DefaultsProvider); ok {
			if err := dp.Defaults(op, req.Mode, params); err != nil {
				return 0, err
			}
		}
	}
	if op == "remove_component" && bp.Category != "platform" {
		return 0, fmt.Errorf("仅组合套件支持按组件卸载")
	}
	if op == "reinstall" && inst != nil {
		switch inst.Status {
		case "failed", "partial", "uninstalled":
		default:
			return 0, fmt.Errorf("仅失败、部分成功或已卸载的集群可重新安装")
		}
	}
	if op == "uninstall" && inst != nil && inst.Status == "uninstalled" {
		return 0, fmt.Errorf("集群已卸载")
	}

	// 清单式角色计划套件（大数据底座）：缩容保护优先读已物化角色计划
	// （缺失时惰性生成并落库，docs/角色物化设计.md §3.3）
	if op == "scale_in" && inst != nil && suiteSelfPlans(bp.Key) {
		_, _ = h.loadRolePlan(inst)
	}

	hosts, err := h.buildHostSetForOp(op, d, mode, inst, req, ids, params)
	if err != nil {
		return 0, err
	}

	// 部署前物化角色计划（docs/角色物化设计.md §3.1 时机矩阵，全部套件适用）。
	// create/add_component 生成或重规划；scale_out/scale_in 增量刷新（落点冻结、全员角色伸缩）；
	// reinstall 只读沿用。bigdata 计划的全量 masters 投影注入参数，使运行期任何子集主机上的
	// 推导都与 Plan 逐字节一致（41 类不一致的根治机制）；通用套件预览落库供抽屉/缩容保护共读。
	var pendingPlan *model.RolePlan
	if op == "create" || op == "add_component" || op == "reinstall" || op == "scale_out" || op == "scale_in" {
		plan, perr := h.planForStackOp(op, d, mode, inst, params, hosts, ids, req.MasterHostID)
		if perr != nil {
			return 0, perr
		}
		if plan != nil {
			applyPlanMasters(params, plan)
			applyPlanToHosts(hosts, plan)
			pendingPlan = plan
			if op != "create" && req.InstanceID > 0 {
				if b := encodeRolePlan(plan); b != "" {
					_ = h.repo.SetInstanceRolePlan(req.InstanceID, b)
				}
			}
		}
	}

	if op == "create" {
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = bp.Name + "-" + mode.Label
		}
		compList := []string{}
		// 组件型套件（蓝图声明了 components 参数）：实例以组件清单为部署单元，落库供加装/卸载比对
		if stackkit.DeclaresVar(bp, "components") {
			compList = parseComponentsCSV(params["components"])
		}
		comps, _ := json.Marshal(compList)
		taskParams, _ := json.Marshal(params)
		id, cerr := h.repo.CreateInstance(&model.StackInstance{
			Name: name, StackKey: bp.Key, StackName: bp.Name, Mode: req.Mode,
			Category: bp.Category, Status: "deploying",
			ParamsJSON: string(taskParams), ComponentsJSON: string(comps),
		})
		if cerr != nil {
			return 0, fmt.Errorf("创建集群实例失败: %w", cerr)
		}
		if pendingPlan != nil {
			if b := encodeRolePlan(pendingPlan); b != "" {
				_ = h.repo.SetInstanceRolePlan(id, b)
			}
		}
		req.InstanceID = id
	}

	// 记录运行前置状态：失败时回滚，避免实例被永久留在 deploying（如加装失败本不影响既有集群）
	if op != "create" && inst != nil && strings.TrimSpace(inst.Status) != "" {
		params["prev_instance_status"] = inst.Status
	}
	taskParams, _ := json.Marshal(params)
	runID, err := h.repo.CreateRun(&model.StackRun{
		InstanceID: req.InstanceID, Op: op,
		StackKey: bp.Key, StackName: bp.Name, Mode: req.Mode, ParamsJSON: string(taskParams),
	}, hosts)
	if err != nil {
		return 0, fmt.Errorf("创建套件运行失败: %w", err)
	}
	if op != "create" {
		// 显式覆盖过参数时一并持久化到实例，后续扩容/重装继续沿用（如内网镜像仓库前缀）
		persist := ""
		if len(req.Params) > 0 {
			if merged, merr := json.Marshal(params); merr == nil {
				persist = string(merged)
			}
		}
		_ = h.repo.UpdateInstance(req.InstanceID, "", "deploying", persist, "")
	}
	h.auditRepo.Create(&model.AuditLog{
		Action: "stack.run", TargetType: "stack_run", TargetID: runID,
		Detail:   fmt.Sprintf("op=%s stack=%s mode=%s hosts=%d instance=%d", op, bp.Name, req.Mode, len(hosts), req.InstanceID),
		RemoteIP: remoteIP,
	})
	go h.execute(runID)
	return runID, nil
}
