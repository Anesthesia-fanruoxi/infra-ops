package stack

// 任务 09（套件目录化拆分）B1 行为快照基线。
//
// 目的：在目录化拆分**之前**，把 9 个套件 × 各 mode 的「角色计划 / 登记服务 / 校验端点」
// 固化成基线文件；拆分每一期都必须与基线逐字段一致（设计文档 §8 P2/P3 验收）。
//
// 用法：
//
//	go test ./api/stack/ -run TestSnapshot                  # 与基线比对（默认）
//	SNAPSHOT_WRITE=1 go test ./api/stack/ -run TestSnapshot # 生成 / 刷新基线
//
// 基线目录：workflow/tasks/09-stack-dir-split/baseline/cases/
//
// 注意：GeneratedAt 为运行时刻，落盘前统一清空；服务与安装记录的 updated_at 不参与快照。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/repo"
)

// snapshotRoot 基线目录（测试工作目录为 api/stack，故上溯两级到仓库根）。
const snapshotRoot = "../../workflow/tasks/09-stack-dir-split/baseline"

// snapCase 一个快照用例：固定输入 → 固定的角色计划 / 服务登记 / 校验端点。
type snapCase struct {
	Name         string
	Stack        string
	Mode         string
	Op           string // 默认 create
	Hosts        []model.StackRunHost
	Params       map[string]string
	Masters      map[string]string
	ManualKeys   []string
	ManualMaster bool
	Bigdata      bool // 走 planBigdataRoles（输入取自 hosts[0].ParamsJSON）
	PlanOnly     bool // 仅比对角色计划（负例：期望报错，不登记服务/端点）
}

// snHost 构造一台成员主机；paramsJSON 仅大数据与冷热温需要。
func snHost(id int64, ip, role string, seq int, paramsJSON string) model.StackRunHost {
	return model.StackRunHost{
		HostID: id, HostIP: ip, HostName: "sn" + strings.ReplaceAll(ip, ".", "-"),
		Role: role, Seq: seq, ParamsJSON: paramsJSON,
	}
}

// snapHosts 返回用例主机的副本，主机 ID 按用例序号整体偏移。
// 必要性：host_services 的唯一键是 (host_id, service_name)，若各用例复用同一批 ID，
// 不同套件的服务登记会互相覆盖/串味，快照就不再是该用例的纯行为。
func snapHosts(caseIdx int, c snapCase) []model.StackRunHost {
	base := int64(caseIdx+1) * 100
	out := make([]model.StackRunHost, len(c.Hosts))
	for i, h := range c.Hosts {
		h.HostID = base + int64(h.Seq)
		out[i] = h
	}
	return out
}

// bdParams 大数据主机参数（算法只读 hosts[0].ParamsJSON）。
func bdParams(components string, ha bool, masters string) string {
	if masters == "" {
		masters = "{}"
	}
	return fmt.Sprintf(`{"components":%q,"ha":%q,"masters":%s}`, components, fmt.Sprintf("%t", ha), masters)
}

// esParams 冷热温主机参数（每台勾选的角色）。
func esParams(roles string) string {
	return fmt.Sprintf(`{"roles":%q}`, roles)
}

// ---------- 快照产物结构 ----------

type serviceSnap struct {
	HostID   int64  `json:"host_id"`
	Name     string `json:"service_name"`
	URL      string `json:"url"`
	Web      bool   `json:"web"`
	Instance int64  `json:"instance_id"`
}

type installSnap struct {
	HostID       int64  `json:"host_id"`
	TemplateID   int64  `json:"template_id"`
	TemplateName string `json:"template_name"`
	TaskID       int64  `json:"task_id"`
}

type endpointSnap struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	URL       string `json:"url"`
	Role      string `json:"role,omitempty"`
}

type caseSnap struct {
	Case      string          `json:"case"`
	StackKey  string          `json:"stack_key"`
	Mode      string          `json:"mode"`
	Op        string          `json:"op"`
	Plan      *model.RolePlan `json:"role_plan,omitempty"`
	PlanError string          `json:"plan_error,omitempty"`
	Services  []serviceSnap   `json:"services"`
	Installs  []installSnap   `json:"installs"`
	Endpoints []endpointSnap  `json:"endpoints"`
}

// ---------- 快照数据库 ----------

// 服务登记与安装台账落在 SQLite（host_services / host_installs 对 hosts / credentials
// 有外键约束），故为快照准备一个临时库。
func openSnapshotDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// SNAPSHOT_DB 仅供排查：指定一个固定目录，测试结束后可打开该库核对主机/服务登记行
	if p := os.Getenv("SNAPSHOT_DB"); p != "" {
		dir = p
	}
	if err := store.Open(filepath.Join(dir, "snapshot.db")); err != nil {
		t.Fatalf("打开快照库失败: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("快照库迁移失败: %v", err)
	}
	t.Cleanup(store.Close)
	seedSnapshotHosts(t)
}

// seedSnapshotHosts 预置一条凭据与全部用例的主机行（外键依赖）。
// 注意：hosts 上有唯一索引 idx_hosts_ip_port(ip, port)，而多个用例会复用同一批 IP
// 来对照不同模式，因此按用例序号分配独立端口，避免主机行被唯一约束静默丢弃。
func seedSnapshotHosts(t *testing.T) {
	t.Helper()
	credName := "snapshot-credential"
	_, err := store.DB.Exec(
		`INSERT OR IGNORE INTO credentials(id,name,type,username,encrypted_secret) VALUES(1,?,'password','root',x'00')`, credName)
	if err != nil {
		t.Fatalf("预置凭据失败: %v", err)
	}
	for ci, c := range snapCases {
		port := 2200 + ci
		for _, h := range snapHosts(ci, c) {
			name := fmt.Sprintf("snap-%s-%d", c.Name, h.HostID)
			if _, err := store.DB.Exec(
				`INSERT INTO hosts(id,name,ip,port,credential_id) VALUES(?,?,?,?,1)`,
				h.HostID, name, h.HostIP, port); err != nil {
				t.Fatalf("预置主机失败(%s/%d): %v", c.Name, h.HostID, err)
			}
		}
	}
}

// ---------- 快照计算 ----------

func buildCaseSnap(t *testing.T, h *stackHandler, caseIdx int, c snapCase) caseSnap {
	t.Helper()
	op := c.Op
	if op == "" {
		op = "create"
	}
	out := caseSnap{Case: c.Name, StackKey: c.Stack, Mode: c.Mode, Op: op,
		Services: []serviceSnap{}, Installs: []installSnap{}, Endpoints: []endpointSnap{}}

	d := store.FindBuiltinStack(c.Stack)
	if d == nil {
		t.Fatalf("套件不存在: %s", c.Stack)
	}
	caseHosts := snapHosts(caseIdx, c)

	var plan model.RolePlan
	var err error
	if c.Bigdata {
		plan, err = planBigdataRoles(caseHosts, PlanOptions{Op: op, Masters: c.Masters, ManualKeys: c.ManualKeys})
	} else {
		plan, err = planGenericRoles(d, caseHosts, PlanOptions{
			Op: op, Mode: c.Mode, Params: c.Params, ManualMaster: c.ManualMaster,
		})
	}
	if err != nil {
		out.PlanError = err.Error()
		return out
	}
	plan.GeneratedAt = "" // 运行时刻，不参与比对
	out.Plan = &plan

	if c.PlanOnly {
		return out
	}

	// 服务登记与安装台账。注册分发与 stack_phase.go 的调用逐字一致：
	// 单一入口依套件能力（ServiceRegistrar）分发，未实现即走引擎通用角色投影；
	// 参数取「运行级 + 主机级」合并视图（运行期主机 ParamsJSON 本就是合并后的整体视图，
	// 冷热温的 roles 就在这里）。
	hosts := append([]model.StackRunHost{}, caseHosts...)
	sort.SliceStable(hosts, func(i, j int) bool { return hosts[i].Seq < hosts[j].Seq })
	for i := range hosts {
		hp := mergeParamMaps(c.Params, parseJSONMap(hosts[i].ParamsJSON))
		h.registerStackService(d, &hosts[i], c.Mode, hp, 9000, hosts)
	}
	for _, hh := range hosts {
		svcs, serr := h.tplRepo.HostServices(hh.HostID)
		if serr != nil {
			t.Fatalf("读取服务失败(%s/%d): %v", c.Name, hh.HostID, serr)
		}
		for _, s := range svcs {
			out.Services = append(out.Services, serviceSnap{
				HostID: s.HostID, Name: s.ServiceName, URL: s.URL, Web: s.Web, Instance: s.InstanceID,
			})
		}
		ins, ierr := h.tplRepo.HostInstalls(hh.HostID)
		if ierr != nil {
			t.Fatalf("读取安装台账失败(%s/%d): %v", c.Name, hh.HostID, ierr)
		}
		for _, it := range ins {
			out.Installs = append(out.Installs, installSnap{
				HostID: it.HostID, TemplateID: it.TemplateID, TemplateName: it.TemplateName, TaskID: it.TaskID,
			})
		}
	}
	sort.SliceStable(out.Services, func(i, j int) bool {
		if out.Services[i].HostID != out.Services[j].HostID {
			return out.Services[i].HostID < out.Services[j].HostID
		}
		return out.Services[i].Name < out.Services[j].Name
	})
	sort.SliceStable(out.Installs, func(i, j int) bool {
		if out.Installs[i].HostID != out.Installs[j].HostID {
			return out.Installs[i].HostID < out.Installs[j].HostID
		}
		return out.Installs[i].TemplateName < out.Installs[j].TemplateName
	})

	// 校验接入端点（与运行期一致：实例携带角色计划与成员参数）
	inst := &model.StackInstance{
		ID: 9000, StackKey: c.Stack, StackName: d.Blueprint().Name, Mode: c.Mode,
		ParamsJSON: jsonOrEmpty(c.Params),
	}
	if b := encodeRolePlan(&plan); b != "" {
		inst.RolePlanJSON = b
	}
	for _, hh := range hosts {
		inst.Hosts = append(inst.Hosts, model.StackInstanceHost{
			HostID: hh.HostID, HostName: hh.HostName, HostIP: hh.HostIP,
			Role: hh.Role, Seq: hh.Seq, ParamsJSON: hh.ParamsJSON, Status: "active",
		})
	}
	for _, ep := range stackVerifyEndpoints(inst, c.Params, nil) {
		out.Endpoints = append(out.Endpoints, endpointSnap{
			Name: ep.Name, Component: ep.Component, URL: ep.URL, Role: ep.Role,
		})
	}
	return out
}

func jsonOrEmpty(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// TestSnapshotCases 角色计划 / 登记服务 / 校验端点三类行为的基线比对。
func TestSnapshotCases(t *testing.T) {
	h := &stackHandler{tplRepo: repo.NewDeployRepo()}
	openSnapshotDB(t)
	write := os.Getenv("SNAPSHOT_WRITE") == "1"
	dir := filepath.Join(snapshotRoot, "cases")
	if write {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("创建基线目录失败: %v", err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		t.Fatalf("基线目录不存在（先跑 SNAPSHOT_WRITE=1 生成）: %v", err)
	}

	for ci, c := range snapCases {
		ci, c := ci, c
		t.Run(c.Name, func(t *testing.T) {
			got := buildCaseSnap(t, h, ci, c)
			b, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			b = append(b, '\n')
			path := filepath.Join(dir, c.Name+".json")
			if write {
				if werr := os.WriteFile(path, b, 0o644); werr != nil {
					t.Fatalf("写入基线失败: %v", werr)
				}
				return
			}
			want, rerr := os.ReadFile(path)
			if rerr != nil {
				t.Fatalf("读取基线失败（补生成请跑 SNAPSHOT_WRITE=1）: %v", rerr)
			}
			if string(want) != string(b) {
				t.Fatalf("行为与基线不一致（case=%s）\n%s", c.Name, firstDiff(string(want), string(b)))
			}
		})
	}
}

// firstDiff 输出首个差异行及其上下文，便于定位。
func firstDiff(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			lo := i - 3
			if lo < 0 {
				lo = 0
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "第 %d 行起差异：\n", i+1)
			for j := lo; j <= i && j < len(wl); j++ {
				fmt.Fprintf(&sb, "  基线 %d | %s\n", j+1, wl[j])
			}
			for j := lo; j <= i && j < len(gl); j++ {
				fmt.Fprintf(&sb, "  当前 %d | %s\n", j+1, gl[j])
			}
			return sb.String()
		}
	}
	return "(内容相同)"
}
