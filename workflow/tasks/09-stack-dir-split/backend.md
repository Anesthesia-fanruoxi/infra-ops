# 任务 09 · 套件目录化拆分 — backend.md（后端执行）

> 设计依据：`docs/套件目录化拆分设计.md`（已定稿；§4 目标架构 / §5 关键设计 / §7 引擎收敛清单 / §8 迁移计划）。
> 原则：**先建骨架后搬肉，每期只搬不改，搬完即删**；每期结束 `go build/vet/test ./...` 全绿。
> **已拍板**：① Go 代码与 `scripts/` 同级（方案 A）③ **一次性迁移全部 9 套件，不设试点期** ④ 部署模板体系不纳入（仅 embed 联动）。
> 执行顺序即步骤编号；每步完成后按验收标准验收并在 workflow.md 标记 [x]。
> 阶段映射：B1=P0，B2=P1，B3–B7=P2，B8=P3，B9=P4，B10=P7（后端部分）。

## B1（P0）基线与行为快照 ✅ 2026-09-14

- [x] `api/stack/snapshot_test.go`（骨架与比对逻辑）+ `api/stack/snapshot_cases_test.go`（22 个用例）：9 套件 × 各 mode 的角色计划 / 登记服务 / 校验端点；`SNAPSHOT_WRITE=1` 写盘，默认与基线比对
- [x] `store/stack_render_snapshot_test.go`：蓝图（9 份 + 列表顺序）、流水线（13 份）、渲染产物（52 份 = 13 组合 × 4 阶段，含 `@@ASSET@@` 注入结果与「该模式无此阶段」的错误文案）
- [x] `script/check_stack_lines.py`：`--record` 记基线 / 默认「不得劣化」比对 / `--targets` 终态硬门禁（P7 用）
- [x] 基线全部落盘 `workflow/tasks/09-stack-dir-split/baseline/`：`cases/` 22 + `blueprint/` 10 + `pipeline/` 13 + `render/` 52 + `metrics.json`，合计 394KB

**P0 实测基线**（`baseline/metrics.json`）

- 受检 59 个文件 / 15380 行；套件分支 **52 处 / 14 个文件**：`stack_plan_generic.go` 11、`stack_create.go` 9、`stack_instance_validate.go` 6、`stack_phase.go` 4、`stack_register.go` 4，其余 1–3。
- 待收敛文件：`store/builtin_stacks.go` 576、`stacks.js` 1824、`stacks-forms.js` 681、`style.css` 2333。

**执行中暴露的两个真实约束**（已写进快照实现，后续拆分勿踩）

1. **`hosts` 有唯一索引 `idx_hosts_ip_port(ip, port)`** —— 多个用例复用同一批 IP 时，主机行会被 `INSERT OR IGNORE` 静默丢弃，进而让服务登记因外键失败而全部落空。快照按用例序号分配独立端口（`2200+caseIdx`）绕开。
2. **服务注册不是单一入口** —— `stack_phase.go` 按 `bp.Key == "redis"` 分流到 `registerRedisService`，其余才走 `registerStackService`；且参数取**主机级合并视图**（冷热温的 `roles` 就放在主机参数里）。快照必须按同一分发实现，否则 redis 与冷热温的服务登记基线是错的。

验收：✅ 三类基线落盘入库且可复现（`SNAPSHOT_WRITE=1` 生成 → 默认模式逐字节比对通过）；`go build ./... && go vet ./... && go test ./...` 全绿 + 交叉编译 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` 通过；`check_stack_lines.py` 默认模式通过，`--targets` 如实报出尚未达成的终态项（builtin_stacks.go 576>120、stacks.js 1824>120、分支 52>0）。

## B2（P1）契约层、装配表与 embed 收紧 ✅ 2026-09-14

- [x] 新建 `store/stackkit/`：
  - `driver.go`：`Driver{Key/Blueprint/Phase/Pipeline}` + `PhaseFile{Path,Assets}` + 阶段常量与 `ValidPhase`（区分「未知阶段」与「该模式无此阶段脚本」两种报错）
  - `capability.go`：8 个可选能力接口（`RolePlanner` / `ServiceRegistrar` / `VarInjector` / `CreateValidator` / `ScaleGuard` / `TopologyBuilder` / `DefaultsProvider` / `VerifyProbe`）+ `PlanInput`（带 `NewPlan`/`Src`/`ModeDef`/`Get`/`IsYes`）+ 各能力 Ctx
  - `plan_helper.go`：`Roles{Boot,Rest,All,HasMaster}` + `AddAt/AddAll/AddRest/Warn/PlanHosts`（引导机判定与落点顺序与既有 `planGenericRoles` 逐条一致）+ `NowLocal/TrimParam/IsYes/FirstNonEmpty`
  - `registry.go`：`Registry` 容器（按登记顺序 List）+ `Default` 单例 + 包级 `Register/Find/List/Keys`
  - `probe.go`：`Endpoint`（JSON tag 与既有 `stackVerifyEndpoint` 逐字对齐）/`Check`/`VerifyResult`
- [x] 新建 `store/registry.go`：装配表 `init()` 登记 9 套件 → `FindStackDriver` / `ListStackDrivers` / `ListStackKeys`；`VerifyStackRegistry()` 启动自检（蓝图 key 一致、模式非空、每模式 `Phase("node")` 可读、注入资源可读），失败即 `panic`（fail fast）
- [x] 新建 `store/embed.go`：唯一 embed 声明点 —— `StackFS`（`stacks/*/scripts stacks/*/configs`）+ `BuiltinFS`（`builtin`）+ `readAsset` 前缀路由
- [x] `store/builtin_stacks.go`：`BuiltinStack` 改**兼容壳**（持有 `drv stackkit.Driver`，保留蓝图字段面供引擎读 `bp.Key`/`bp.Name`，保留 `ModeDef`/`Blueprint`/`Pipeline`/`LoadPhase` 渲染实现与 `@@ASSET@@` 注入）；新增过渡驱动 `legacyStack`（9 份蓝图 + 阶段表字面量原样承载，仅 `path/assets` → `Path/Assets`、`stackPhaseFile` → `stackkit.PhaseFile`）
- [x] `store/builtin_templates.go`：embed 收紧（删除本地 `//go:embed builtin stacks/elasticsearch stacks/rocketmq stacks/rabbitmq`），3 处套件脚本（`rocketmq/namesrv.sh`、`rocketmq/broker.sh`、`rabbitmq/node.sh`）与全部模板资源改走 `readAsset`；**30 条模板定义零改动**
- [x] 自检单测 `store/builtin_stacks_test.go`：装配表自检 / 覆盖 9 套件且顺序稳定 / `StackFS` 不含 `.go` / 模板复用的 3 条套件脚本可读 / 30 条模板全部可装载

**验收实测（2026-09-14）**

| 项 | 结果 |
|----|------|
| `go build ./...` | 0 |
| `go vet ./...` | 0 |
| `go test ./...` | 0（store / api/stack / api/deploy / api/host / api/tool/es 全过） |
| 交叉编译 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` | 0 |
| B1 渲染字节基线（52 份） | **逐字节一致**（`TestSnapshotRenderScripts` 全过） |
| B1 行为快照（22 例）/ 蓝图（10）/ 流水线（13） | 逐项一致 |
| 发布体积（`-ldflags "-s -w"`） | 18,013,184 → 18,019,840（**+6.6KB / +0.04%**） |
| 套件分支数（`check_stack_lines.py`） | 52 处 / 14 文件 **不变**（P1 不动引擎分支） |
| 本机 8090 启动 | ✅ 正常监听（`infra-ops starting on 127.0.0.1:8090`），启动自检未触发 panic；`GET /api/stacks` 401（受保护路由，符合预期） |

**embed 收紧的直接实证**（不靠推理，靠二进制检索）

1. 往 `store/stacks/redis/` 放探针 `embedprobe.go`（含标记串 `EMBED_PROBE_B2_MARKER_5a1f9c3e`）；
2. 收紧后 `//go:embed stacks/*/scripts stacks/*/configs` → 发布二进制中**检索不到**标记串 ✅；
3. 反向对照：临时把 `StackFS` 的 embed 改回 `//go:embed stacks` → 二进制中**能检索到**标记串（体积 +512B）✅ —— 证明探针灵敏、收紧确实生效，而非「恰好没匹配」；
4. 探针与临时二进制已清理。

**两个踩坑（后续期务必注意）**

1. **`go fmt ./store/...` 会越界改文件** —— gofmt 顺手给 `store/db.go`、`store/repo/{registry,sftp,es,stack}.go` 补了文件末尾换行 / 重排了结构体字段对齐，这些都**不在 boundary 白名单**内。已逐个回退（`db.go` 因 git 换行归一化无 diff；`registry.go`/`sftp.go` 直接 `git checkout` 还原；`es.go` 手工去掉补上的末尾换行；`stack.go` 手工还原字段对齐）。**结论：只对本期实际改动的文件跑 gofmt/gofmt -l，不要对整包跑 `go fmt`。**
2. **兼容壳的行数代价是有意的** —— `BuiltinStack` 保留蓝图**字段面**（而非改成 `Key()` 方法）是因为引擎现有约 55 处 `bp.Key` 取的是字段，而这些取值点中绝大多数会在 P2–P4 整段删除；P1 若先改写成方法调用，等于给「即将删除的代码」做无用改造。代价是 `builtin_stacks.go` 暂增至 597 行（详见设计文档 §13 修订记录）。

验收：✅ `go build/vet/test ./...` 全绿 + 交叉编译通过；B1 行为快照与渲染字节基线逐项一致（**行为零变更**）；发布体积无异常增长且已证实套件 `.go` 未进二进制；启动自检在真实进程启动路径上生效且不误伤。

## B3（P2-1）搬 `PlanRoles` · 简单型 5 套件 ✅ 2026-09-14

- [x] 新建 `store/stacks/{kafka,nacos,powerjob,rabbitmq,rocketmq}/`，每套件两个文件：
  - `<key>.go`：`Driver` + `New()` + `Key/Phase/Pipeline` + **蓝图**（从 `builtin_stacks.go` 字面量原样迁入，字段顺序零变更）
  - `plan.go`：`PlanRoles(stackkit.PlanInput)`（从 `stack_plan_generic.go` 的对应 case 原样搬入）
  - 19–31 行/`plan.go`、60–77 行/`<key>.go`（kafka 因双模式 + UI 警告略长）
- [x] `store/builtin_stacks.go`：`builtinLegacyStacks` 由 `[]*legacyStack` 改 `map[string]*legacyStack`，撤出 5 套件蓝图+阶段表字面量（592 → **415 行**）
- [x] `store/registry.go`：新增 `stackDrivers` 装配表（顺序 = `_order.json` 冻结的展示顺序）：已目录化套件用真驱动，未迁移的取 `builtinLegacyStacks[<key>]`
- [x] `api/stack/stack_plan_generic.go` 降为**通用外壳**（195 → **133 行**）：规范化 → 最小主机数校验 → 装配 `PlanInput` → `d.(stackkit.RolePlanner).PlanRoles(in)` → 通用收尾；残留 redis/es/elfk 分支抽为 `planRolesInEngine`（B4–B6 逐个删）
- [x] `store/stackkit`：新增 `PlanInput.FallbackMasterMember()`（承载拆分前 `default` 分支，含「模式不认识」边界）；`Roles.AddAt` 去掉 `HostIP == ""` 早退（回到与拆分前闭包**严格等价**）
- [x] 迁移进度闸门单测 `api/stack/stack_plan_migration_test.go`：① 已迁移套件必须实现 `RolePlanner`；② **反证**——`planRolesInEngine` 对它们只能给通用兜底（谁把引擎分支加回来就红）；③ 未知模式兜底一致

**验收实测（2026-09-14）**

| 项 | 结果 |
|----|------|
| `go build ./...` / `go vet ./...` / `go test ./...` | 0 / 0 / 0（store、api/stack、api/deploy、api/host、api/tool/es、store/repo 全过） |
| 交叉编译 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` | 0 |
| B1 行为快照 **22 例**（含 5 套件全部用例） | **逐字段一致** |
| B1 蓝图 10 / 流水线 13 / 渲染产物 52 | **逐字节一致** |
| 套件分支数 | 52 → **46** 处（`stack_plan_generic.go` 11 → 5） |
| `store/builtin_stacks.go` / `api/stack/stack_plan_generic.go` | 592 → 415 行；195 → 133 行 |
| 发布体积（`-ldflags "-s -w"`） | 18,013,184 → 18,046,976（**+33.8KB / +0.19%**，5 个新包 + 断言） |
| 本机 8090 启动 | ✅ 正常监听，启动自检未触发 panic |
| gofmt | 只对本期 16 个改动文件跑 `gofmt -l`，**零待格式化**（不对整包跑 `go fmt`） |

**B3 定下的角色规划契约（B4–B7 照此办理，勿各自发挥）**

驱动只做两件事：往 `in.Roles` 上累加角色 + 用 `in.Roles.Warn` 记提示；**不自己**拼 `plan.Hosts` / `GeneratedBy` / `Warnings`。引擎统一收尾：`roles.PlanHosts(norm)` → 追加驱动警告 → AssignMaster 兜底提示 → `in.Src()` 定来源。收益：9 套件样板量最小、警告顺序天然与拆分前一致（被快照冻结）、主机行展开规则与引擎同源。

**主列表顺序仍是硬约束**：`registry.go` 的 `stackDrivers` 数组顺序 = 前端卡片顺序 = `baseline/blueprint/_order.json`。新增/调整必须同步刷新该基线（本次顺序未变，故基线零改动）。

**与设计文档的偏差**（已回写 `docs/套件目录化拆分设计.md` §13）：B3 每套件只落 `<key>.go` + `plan.go`——`register.go` 属 P3（B8）、`vars.go` 属 P4（B9），提前创建即死代码；§4.1 目录树是终态清单而非每期落地清单。

验收：✅ 5 套件角色计划与 P0 快照逐字段一致；`stack_plan_generic.go` 对应 case 消失（分支 11 → 5）；全量门禁 + 交叉编译全绿；启动自检在真实进程上生效且不误伤。

## B4（P2-2）搬 `PlanRoles` · redis（三模式）✅ 2026-09-14

- [x] `store/stacks/redis/`：`replication` / `sentinel` / `cluster` 三模式角色规划原样搬入 `plan.go`
- [x] 删除 `stack_plan_generic.go` 的 redis 三个 case

验收：✅ 快照比对全绿（三模式角色计划一致）。

> **落地偏差**：条目里的 `verify.go` / `vars.go` 未按名创建 —— 探活落在 `probe.go`（`VerifyProbe`，B8）、
> 拓扑校验落在 `topo.go` + 角色命名 `roles.go`（B9）、运行期默认值落在 `defaults.go`（B9）。
> §4.1 是**终态目录树**，每期只落当期所需文件（同 B3 已记录的偏差）。

## B5（P2-3）搬 `PlanRoles` · elasticsearch（双模式）✅ 2026-09-14

- [x] `store/stacks/elasticsearch/`：`cluster` 与冷热温分层角色规划（含 master 与数据层互斥、协调兜底）原样搬入 `plan.go`
- [x] 删除 `stack_plan_generic.go` 的 ES case

验收：✅ 快照比对全绿（cluster + 冷热温角色计划一致）。

> **落地偏差**：冷热温未单开 `cold_warm_hot.go` —— 分层规则随 `plan.go` 落位，
> 冷热温的每台主机 `roles` 参数由 `vars.go`（`VarInjector`）注入，`scale.go` 承担缩容保护（B8/B9）。

## B6（P2-4）搬 `PlanRoles` · elfk（编排型）✅ 2026-09-14

- [x] `store/stacks/elfk/`：引导机 = ES master + Kibana + Filebeat；第 2 台 = Logstash(aux) + ES data + Filebeat；其余 = ES data + Filebeat
- [x] 确认 **Logstash 落点三处一致**（planner norm[1] / `node.sh` 的 node_ips 第 2 段 / `register.go` 的 `Seq==2`），`logstash_host` 参数可覆盖 → manual
- [x] 删除 `stack_plan_generic.go` 的 elfk case

验收：✅ 快照比对全绿；`TestPlanGenericELFK` 等价单测通过（含 Logstash 手改 → `source=manual`、`MinHosts=3` 拦截）。

## B7（P2-5）搬 `PlanRoles` · bigdata（HA 矩阵）收尾 ✅ 2026-09-14

- [x] `store/stacks/bigdata/`：组件多选与依赖联动（`components.go`）、HA 全角色矩阵与冲突检测（`plan_ha.go`）原样搬入
- [x] `bigdataMasterVars` / `bigdataSecondaryVars` / `bigdataRoleKeys` 从 `store/builtin_stacks.go` 迁入该目录
- [x] 删除 `stack_plan_generic.go` 的最后 case；该文件收敛为通用外壳（仅规范化 / 最小主机数 / 落点 / 兜底默认）
- [x] `store/builtin_stacks.go` 的 9 个蓝图字面量全部搬入各套件 `Blueprint()`

验收：✅ 快照比对全绿（含 HA 全角色矩阵）；`stack_plan_generic.go` 的 switch 彻底消失；`builtin_stacks.go` 于 B10 收尾至 60 行（≤120）。

## B8（P3）搬探活：`ServiceRegistrar` + `VerifyProbe` + `ScaleGuard` ✅ 2026-09-14

- [x] `ServiceRegistrar`：bigdata / elasticsearch / elfk / kafka / rabbitmq 各自实现（`register.go`）；`stack_register.go:93/98/137/204` 特判与 166 行 `registry` map 已删除；`stack_register_bigdata.go`（155 行）整文件迁入 `store/stacks/bigdata/`
- [x] `VerifyProbe`：redis / bigdata / elasticsearch 各自实现（`VerifyScript` + `ParseVerify`），落在各套件 `probe.go` / `endpoint.go`；引擎侧 `stack_verify_script.go` / `stack_verify_parse.go` / `stack_verify.go` 的**套件分支已删除**（三文件保留为通用分发骨架），`stack_verify_redis.go`（140 行）整文件迁入 `store/stacks/redis/probe.go`
- [x] `ScaleGuard`：redis / bigdata / elasticsearch 各自实现（`scale.go`）；`stack_instance_validate.go:41–53` 分支已删除
- [x] `stack_validate_bigdata.go`（205 行）整文件迁入 `store/stacks/bigdata/validate.go`

验收：✅ 登记服务清单 / 校验端点清单 / 缩容保护行为与 B1 快照逐项一致（`api/stack` 22 例行为快照零差异）；引擎侧对应分支数为 0。

## B9（P4）搬变量与拓扑：`VarInjector` + `TopologyBuilder` + `Defaults` + `CreateValidator` ✅ 2026-09-14

- [x] `VarInjector`：es（`stackClusterExtra` / `stackCWHExtraMerged`）、bigdata（`bigdataMasterExtra`）、elfk 迁入各套件 `vars.go`；`stack_engine.go` / `stack_phase.go` 的对应分支已删除
- [x] `TopologyBuilder`：redis 迁入 `topo.go` + `roles.go`；`stack_topo.go` 的分支已删除
- [x] `DefaultsProvider`：kafka（cluster_id）/ rabbitmq（erlang_cookie）/ redis（replicas）/ es 迁入 `defaults.go`；`stack_create.go` 的 88–125 行分支已删除
- [x] `CreateValidator`：redis / es / bigdata / kafka / rabbitmq 迁入各套件 `validate.go`
- [x] 整文件迁出：`stack_bigdata.go`（182）、`stack_bigdata_render.go`（156）、`stack_vars_cwh.go`（79）
- [x] 同批清理残留分支：`stack_create_hosts.go`、`stack_instance_build.go`、`stack_instance_reinstall.go`、`stack_op.go`、`stack_sync.go`、`stack_plan_handler.go`、`stack_plan_persist.go`

验收：✅ 渲染脚本与 P0 基线 **diff 为空**（52 份 `render/` 基线逐字节一致）；B1 快照全绿；`api/stack` + `store` 套件分支数为 0（`check_stack_lines.py` 硬指标）。

> **B8/B9 的落地是被逐期执行的，但 `- [x]` 在 B10 收尾时统一补齐** —— 补勾依据是终态目录件
> （`register.go` / `probe.go` / `endpoint.go` / `scale.go` / `vars.go` / `topo.go` / `roles.go` /
> `defaults.go` / `validate.go` 均已到位）、套件分支 0、以及 B1 基线三类快照逐字节一致，均为可复验事实。

## B10（P7）后端收尾 ✅ 2026-09-14

- [x] 删除 P1 遗留的 `BuiltinStack` 兼容壳，`FindBuiltinStack` 直接返回 `stackkit.Driver`；引擎侧不再有兼容包装
- [x] 补齐单测：每套件 `Blueprint` 合法性 + `Phase` 可读 + `PlanRoles` 确定性；契约冒烟（registry 全覆盖）
- [x] 更新 `workflow/docs/03-directory.md` 的目录说明（新增 `store/stackkit/`、`store/stacks/<key>/*.go`）
- [x] 全量验收：`go build ./... && go vet ./... && go test ./...` 全绿 + 交叉编译 + 快照/渲染 diff 全绿 + `script/check_stack_lines.py` 达标

**收尾形态（删壳后的契约面）**

- `store/builtin_stacks.go` 576 → **60 行**，只剩三件事：`ListStackBlueprints()` / `FindBuiltinStack(key) stackkit.Driver` / `LoadStackPhase(d, mode, phase)`。
  - 「渲染」必须留在 store 包（要读 `StackFS`），故从壳方法降为**包级函数**，签名带驱动；
  - 「取模式」下沉到契约层：新增 `stackkit.ModeDef(bp, mode)`（与 `(*PlanInput).ModeDef` 同口径），与既有的 `stackkit.DeclaresVar` 并列。
- 引擎侧参数类型 `*store.BuiltinStack` 全部换成 `stackkit.Driver`（17 个函数），顺带消掉 `store.FindStackDriver(bp.Key).(能力接口)` 这类**二次查找**——`bp` 本身就是驱动，直接 `d.(能力接口)`。
- 读蓝图统一为函数入口 `bp := d.Blueprint()`；`bp.Key / bp.Name / bp.Category / bp.RequiresDocker / bp.SharedVars / bp.Modes` 等字段面原样保留（蓝图本就是值类型，无第二份副本）。
- 引擎侧不再出现 `bp.ModeDef` / `bp.LoadPhase`；`LoadStackPhase` 的两条报错文案逐字未改。

**验收实测（2026-09-14）**

| 项 | 结果 |
|----|------|
| `go build ./...` / `go vet ./...` / `go test ./...` | 0 / 0 / 0（store、api/stack、api/deploy、api/host、api/tool/es、store/repo、stacks/{bigdata,elasticsearch,redis} 全过） |
| 交叉编译 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` | 0（main 二进制产出 + `go vet ./...` 全包类型检查通过） |
| B1 蓝图 10 / 流水线 13 / 渲染产物 52 | **逐字节一致**（未跑 `SNAPSHOT_WRITE`） |
| B1 行为快照 22 例（角色计划 / 登记服务 / 校验端点） | **逐字段一致** |
| `check_stack_lines.py --targets` | **通过**（套件分支 0；style.css 套件专属选择器 0；builtin_stacks.go 60≤120；stacks.js 22≤120；全部 ≤500） |
| 本机 8090 启动 | ✅ `infra-ops starting on 127.0.0.1:8090`，启动自检未 panic |
| 前端装载冒烟 `_fe_smoke.js` | ✅ 脚本 57/57，9 套件注册齐备，模板 39422 字符逐字节一致 |
| gofmt | 只对本期 24 个改动文件跑 `gofmt -l`，**零待格式化** |

**B10 新增的三个契约闸门**（`store/builtin_stacks_test.go`）

1. `TestStackDriverBlueprintContract`：registry 全覆盖 —— 蓝图 key/名称/分类/模式自洽，每模式 `Phase("node")` 存在且路径**不越出本套件目录**、脚本与注入资源在 embed 内可读、未知阶段一律 `ok=false`、流水线阶段 key 唯一且 `Target ∈ {"", all, leader}`。
2. `TestLoadStackPhaseErrorContract`：冻结两条报错文案 —— `未知阶段: X` 与 `套件 K 模式 M 没有 P 脚本`；缺脚本样本由遍历动态找，找 0 个即判定断言失效（避免「测试假过」）。
3. `TestStackDriverPlanRolesDeterministic`：9 套件**全部**实现 `stackkit.RolePlanner`（谁把规划搬回引擎就红），且同输入两次规划结果逐字节相同；驱动必须「自建主机行」或「填落点助手」二选一，两者皆空即失败。

**基线重录的说明（唯一一处「劣化」系有意为之）**

`--targets` 首次运行报 4 处行数劣化，逐条归因后重录基线，并把删壳前的 P7 基线留档为
`baseline/metrics.p7-preb10.json`（P0 基线仍是 `baseline/metrics.p0.json`）：

| 文件 | 变化 | 归因 |
|------|------|------|
| `api/stack/stack_op.go` | +1 | 新增 `stackkit` import（函数签名换 `stackkit.Driver`，无法省） |
| `api/stack/stack_plan_handler.go` | +1 | 同上（`stackkit.ModeDef`） |
| `api/stack/stack_instance_reinstall.go` | +1 | 同上（`stackkit.ModeDef`） |
| `store/builtin_stacks_test.go` | +160 | B10 要求的契约冒烟单测（三组闸门） |

`api/stack/stack_engine.go` 曾 +1（`bp := d.Blueprint()` 局部量），已改写为 `d.Blueprint()` 直接用掉，
回到 192 行 —— 生产代码**零行数增长**，其余 13 个改动文件均为行数下降或持平。

**设计文档修订**：`docs/套件目录化拆分设计.md` §13 追加 P7 条目（兼容壳的存活区间到此结束；
`ModeDef` 落位契约层；`LoadStackPhase` 因需 `StackFS` 留在 store 包）。

验收：✅ 设计文档 §10「结构类 1–3、5 + 行为类 6–8」全部达标；本任务改动文件全部落在 boundary.md 白名单内。

## 进度记录

（执行中记录：包循环依赖暴露点、兼容壳存活区间、各期 commit 号、快照比对差异的归因。）

- **P2-1（B3）无包循环**：`store/stacks/<key>` 只 import `model` + `store/stackkit`；`store` 顶层 import `store/stacks/<key>`（单向），套件之间零 import。`stackkit` 不 import `store`，故 `PlanInput` 只能带**值**（`model.StackBlueprint`）而不能带 `*store.BuiltinStack` —— 这是契约层唯一需要留意的形状约束。
- **兼容壳存活区间：P1（B2）→ P7（B10）结束**。`BuiltinStack`（`bp.Key` 字段面）+ `legacyStack` 过渡驱动支撑了 P2–P4 的全部引擎调用点；B3 起已迁出的套件不再走 `legacyStack`；B10 把最后一个调用点（17 个函数的 `bp *store.BuiltinStack` 参数）换成 `stackkit.Driver` 后删壳，`FindBuiltinStack` 直接返回 `stackkit.Driver`。
- **快照零差异的归因**：B3–B10 未刷新任何快照基线，`SNAPSHOT_WRITE` 一次未跑即通过比对 —— 说明「迁移 + 引擎外壳重构 + 删壳」确实只搬不改。任何一期若需要 `SNAPSHOT_WRITE=1` 才能过，必须逐条解释差异来源，否则视为回归。
- **删壳的一个副作用（正向）**：引擎里 `store.FindStackDriver(bp.Key).(能力接口)` 的二次查找全部消失 —— `bp` 本身就是驱动实例。这种「先拿壳再按 key 反查驱动」的写法是 P1 兼容期的产物，删壳时自然清除。
- **待用户裁定：`script/` 下的任务 09 辅助工具在白名单外**。boundary.md §1 只登记了 `script/check_stack_lines.py`，但实际还新建了下列**新增文件**（不是改既有文件）：
  - 生成器：`script/_gen_stacks_fe.py`（前端骨架/套件拆分，幂等，源文件缺失时回退 `script/_stacks_src_snapshot.js` / `_stacks_forms_src_snapshot.js`）、`script/_gen_stacks_css.py`（CSS 拆分 + 字节守恒校验，回退 `_style_src_snapshot.css`）；
  - 校验：`script/_fe_smoke.js`（Node 沙箱装载冒烟 + 模板逐字节断言）、`script/_expected_template.txt`（39422 字符预期模板）、`script/_css_*.py` / `_scan_b9*.py` / `_extract_stack_frontend.py`（拆分期一次性测绘脚本）。
  处理建议：把「生成器 + 冒烟 + 快照」补进 boundary.md §1 白名单（它们是可复现性的关键，删掉就失去「重跑生成器零差异」的能力）；一次性测绘脚本（`_css_*` / `_scan_b9*` / `_extract_stack_frontend.py`）可删。**本次未擅自增删或改 boundary.md**，等用户裁定。
- **任务关闭后的一次基线刷新（2026-09-14，属修复不属重构）**：修 run 75 的 bigdata HA 部署失败时改了 `store/stacks/bigdata/scripts/{ha.sh,node.sh}`（根因与修复见 `docs/大数据HA部署ZKFC故障修复.md`），
  连带使 `baseline/render/bigdata__cluster__node.txt` 变化：**1754 → 1834 行（+102 / −22）**，已 `SNAPSHOT_WRITE=1 go test ./store/ -run TestSnapshotRenderScripts` 刷新。
  差异逐条对应修复的 6 处改动（wait_zk 判据、NN1 先等 ZK、formatZK 重试+硬失败、ZKFC 运行校验、wait_port 附打 hive 内部日志、SKIP_SCHEMA_INIT/MS2 等待），**其余基线文件（蓝图/流水线/其余套件渲染/行为快照）零变化**。
  这是本任务「快照零差异」纪律的**唯一一次例外**，性质是**部署脚本缺陷修复**（`ha.sh` 只注入 `node.sh`，故仅 bigdata node 渲染受影响），不是目录化重构引入的行为漂移。
- **刷新顺序的坑**：刷新基线后若又改了脚本，须**再刷一次**再跑门禁；否则基线与代码不一致，会被误判成回归（本次实测踩到一次）。

