package stackkit

import "infra-ops/model"

// 本文件定义套件的可选能力接口，以及各能力共用的上下文结构。
//
// 调用约定（引擎侧）：能力一律用类型断言调用，未实现即走引擎通用默认；
//
//	if p, ok := d.(stackkit.RolePlanner); ok { plan, err = p.PlanRoles(in) }
//
// 接口名与方法名在任务 09 内保持稳定；上下文字段按 B3–B9 的搬迁需要细化，
// 但「谁负责落库 / 谁负责拼接」的边界不变：套件只做声明与计算，副作用（写库、SSH）留在引擎。

// PlanInput 角色规划输入。Hosts 由引擎按 Seq 升序规范化后传入。
type PlanInput struct {
	// Blueprint 当前套件蓝图（取 Name / Modes 做最小主机数校验与提示文案）。
	Blueprint model.StackBlueprint
	// Op create / add_component / scale_out / scale_in / reinstall / replan。
	Op string
	// Mode 套件模式 key。
	Mode string
	// Params 套件参数（enable_ui / db_host / replicas / components / ha 等影响角色展开）。
	Params map[string]string
	// Masters 权威落点输入（key ∈ 角色键集合）；缺省键自动落位。
	Masters map[string]string
	// ManualKeys 用户显式指定的 masters 键（用于来源标注，跨重规划继承）。
	ManualKeys []string
	// ManualMaster 通用套件：用户是否显式指定了主/引导节点。
	ManualMaster bool
	// Hosts 本次操作的全部成员（含既有成员），按 Seq 升序。
	Hosts []model.StackRunHost
	// Roles 落点助手（由引擎用 NewRoles(Hosts) 构造）。
	Roles *Roles
}

// NewPlan 生成带基础字段的空计划（Op / GeneratedAt / Rev 与既有 planner 一致）。
func (in PlanInput) NewPlan() model.RolePlan {
	return model.RolePlan{Op: in.Op, GeneratedAt: NowLocal(), Rev: 1}
}

// Src 通用套件的落点来源标注：用户显式指定主节点 → manual，否则 auto。
func (in PlanInput) Src() string {
	if in.ManualMaster {
		return "manual"
	}
	return "auto"
}

// ModeDef 取当前模式定义；不存在返回 nil。
func (in PlanInput) ModeDef() *model.StackMode {
	for i := range in.Blueprint.Modes {
		if in.Blueprint.Modes[i].Key == in.Mode {
			return &in.Blueprint.Modes[i]
		}
	}
	return nil
}

// Get 取参数（去首尾空白）。
func (in PlanInput) Get(key string) string { return TrimParam(in.Params, key) }

// IsYes 参数是否为真值（yes / true / 1）。
func (in PlanInput) IsYes(key string) bool { return IsYes(in.Get(key)) }

// FirstNonEmpty 取首个非空值。
func (in PlanInput) FirstNonEmpty(vals ...string) string { return FirstNonEmpty(vals...) }

// FallbackMasterMember 引擎通用兜底落点：引导机 = 主节点、其余 = 成员节点。
//
// 与拆分前 planGenericRoles 的 default 分支逐字一致；套件在「模式不认识」时也走它，
// 保证「未知模式」这一边界的行为与拆分前完全相同（不因目录化而改变）。
func (in PlanInput) FallbackMasterMember() {
	in.Roles.AddAt(in.Blueprint.Key, "master", "主节点", "", in.Src(), in.Roles.Boot)
	in.Roles.AddRest(in.Blueprint.Key, "member", "成员节点")
}

// RolePlanner 角色规划：替代 planGenericRoles 的套件分支，全部 9 个套件实现。
//
// 返回值的两种形态（引擎据此决定是否做通用收尾）：
//
//   - **落点式**（除 bigdata 外的 8 套件）：只往 in.Roles 上 AddAt/AddAll/AddRest + Warn，
//     返回的 plan 仅带 Op/GeneratedAt/Rev（in.NewPlan()）。引擎统一收尾：
//     in.Roles.PlanHosts(Hosts) → 追加驱动警告 → AssignMaster 兜底提示 → in.Src() 定来源。
//     好处：9 套件样板量最小、警告顺序天然与拆分前一致（被快照冻结）。
//   - **清单式**（bigdata）：驱动自建 plan.Hosts / plan.Injected / plan.Masters / plan.Warnings，
//     引擎检测到 len(plan.Hosts) > 0 即认为计划由驱动全权负责，不再改写。
type RolePlanner interface {
	PlanRoles(in PlanInput) (model.RolePlan, error)
}

// SelfPlanning 标记接口：套件的角色计划是**清单式**（自建主机行、自算 Injected/Masters），
// 需要「落点冻结」式扩缩容语义（scale_out/scale_in 先把全量 masters 投影注入成员参数再重算，
// 保证落点不变）。目前只有 bigdata；引擎据此分流而不再判 key。
type SelfPlanning interface {
	SelfPlans() bool
}

// RegisterCtx 服务登记上下文。
type RegisterCtx struct {
	// Key 套件 key（安装台账「套件:<key>/<mode>」命名用）。
	Key string
	// Name 套件展示名（通用角色登记的服务名前缀，如「Kafka 主节点」）。
	Name string
	Mode string
	// Host 当前登记的主机。
	Host model.StackRunHost
	// Hosts 本次运行的全部主机。
	Hosts []model.StackRunHost
	// Params 该主机的参数合并视图（含 roles 等主机级变量）。
	Params map[string]string
	// InstanceID 集群实例 ID。
	InstanceID int64
	// Emit 服务入口出口：套件只声明，落库由引擎统一执行。
	Emit func(model.HostService)
	// MarkInstall 安装台账出口（审计用）：套件只给台账名，落库由引擎执行。
	MarkInstall func(name string)
}

// RoleService 按运行期主机角色给出通用服务入口（主 / 工作 / 节点三档）。
//
// 与拆分前 registerStackService 的 default 分支逐字一致：三档的名称、URL、web 均不变，
// 是「未实现 ServiceRegistrar 的套件」以及「套件自身无专属规则时的回落」共用实现。
func RoleService(ctx RegisterCtx) model.HostService {
	ip := ctx.Host.HostIP
	name := ctx.Name + " 节点"
	switch ctx.Host.Role {
	case "master":
		name = ctx.Name + " 主节点"
	case "worker":
		name = ctx.Name + " 工作节点"
	}
	return model.HostService{
		HostID: ctx.Host.HostID, HostIP: ip, ServiceName: name,
		URL: "http://" + ip, Web: false, TemplateID: 0, InstanceID: ctx.InstanceID,
	}
}

// RegisterByRole 通用服务登记：按运行期角色落一条入口 + 记安装台账。
// 引擎在套件未实现 ServiceRegistrar 时调用它；套件也可在无专属规则时复用。
func RegisterByRole(ctx RegisterCtx) {
	ctx.Emit(RoleService(ctx))
	ctx.MarkInstall("套件:" + ctx.Key + "/" + ctx.Mode)
}

// ServiceRegistrar 服务登记：替代 registerStackService 的 key 特判（bigdata / es / elfk / kafka）。
type ServiceRegistrar interface {
	RegisterServices(ctx RegisterCtx)
}

// VarsCtx 变量注入上下文（B9）：引擎已按通用规则算出 per-host 注入表，套件再按自身规则增补或整体替换。
type VarsCtx struct {
	Mode string
	Op   string
	// Existing 存量主机（扩容/加装时非空）。
	Existing []model.StackRunHost
	// Hosts 本次运行主机（含存量 + 新增，按 Seq 升序）。
	Hosts []model.StackRunHost
	// Plan 已物化角色计划（可能为 nil；清单式套件的唯一事实源）。
	Plan *model.RolePlan
	// Extra 引擎通用注入表（key = hostID）；套件可就地增补。
	Extra map[int64]map[string]string
}

// VarInjector 变量注入：替代引擎里的套件分支（冷热温以 roles 重算 / 大数据主从与 HA XML 块）。
//
// 两种使用形态（都允许）：
//   - 就地增补 ctx.Extra 并返回 nil —— 适合「在通用变量之上追加」的套件（大数据底座）；
//   - 返回非 nil 表 —— 整体替换（冷热温以每台主机 roles 为权威重算）。
type VarInjector interface {
	ExtraVars(ctx VarsCtx) map[int64]map[string]string
}

// ValidateCtx 创建校验上下文。
type ValidateCtx struct {
	Op     string
	Mode   string
	Params map[string]string
	// ReqComponents 请求原始 components 参数（未经实例参数合并），
	// 供「加装/卸载组件」按用户提交的增量集合校验。
	ReqComponents string
	// Hosts 本次所选主机（已标注 master 角色与 Seq，含合并后的主机参数）。
	Hosts []model.StackRunHost
	// HostParams 逐主机原始参数（hostID 字符串 → 参数表）；冷热温的 roles 在这里。
	HostParams map[string]map[string]string
	// HostIDs 本次所选主机 ID（与原顺序一致）。
	HostIDs  []int64
	MasterID int64
	Instance *model.StackInstance
	// LookupHost 按 ID 解析主机（HostIP / HostName）。
	//
	// 惰性回调：套件在真正需要主机 IP 时才调用，从而与拆分前「查库时机」一致
	// （缺主机时仍报同一句错误、仍在同一校验位置暴露），引擎不再预先解析。
	LookupHost func(id int64) (model.StackRunHost, bool)
}

// CreateValidator 创建前置校验：承接原 stack_create.go 的套件专属校验（含 add/remove 组件分支）。
// Params 可就地写入（如加装组件后合并 components / masters）。
type CreateValidator interface {
	ValidateCreate(ctx ValidateCtx) error
}

// ScaleCtx 缩容保护上下文。
type ScaleCtx struct {
	Mode      string
	Instance  *model.StackInstance
	Active    []model.StackInstanceHost
	RemoveIDs []int64
	// Remain 缩容后剩余主机数。
	Remain   int
	MasterID int64
}

// ScaleGuard 缩容保护：替代 stack_instance_validate.go 的套件角色保护分支。
type ScaleGuard interface {
	ScaleGuard(ctx ScaleCtx) error
}

// ScaleInGuard 缩容装配后的套件专属保护（可选能力）。
//
// 与 ScaleGuard 的分工只在调用时机：
//   - ScaleGuard 在 validateScaleIn 内、通用成员/下限校验之后调用（全部套件通用入口）；
//   - ScaleInGuard 在其后、缩容主机集合装配完成时调用，用于「按模式」的补充保护
//     （如 ES 冷热温不允许移除最后一台 master 候选主机，否则集群失去选举与仲裁能力）。
//
// 两者都在写库前执行，缺一不可；拆成两个接口是为了让校验顺序与拆分前逐字一致。
type ScaleInGuard interface {
	ScaleInGuard(ctx ScaleCtx) error
}

// DrainCtx 缩容软下线上下文。
type DrainCtx struct {
	Mode string
	// Removed 本次移除的主机（含 Seq / ParamsJSON）。
	Removed []model.StackRunHost
}

// ScaleInDrainer 缩容软下线（可选能力）：套件声明「本模式缩容前需先在存活 leader 上执行
// scale_in 阶段脚本做软下线」，并给出与该模式相关的脚本渲染参数；ok=false 表示无需软下线
// （直接停服）。
//
// 分工：套件只按 Removed 给出**模式相关**参数（如待排空的节点名）；「在存活 leader 上执行」
// 由引擎负责（leader 选取、self_ip 注入、脚本加载、渲染与 SSH 执行留在引擎）。
type ScaleInDrainer interface {
	DrainParams(ctx DrainCtx) (map[string]string, bool)
}

// RoleNamer 运行期主机角色命名：引擎先按「是否主节点 + AssignMaster」给出
// master / worker / node，再给套件一次改名机会（如 redis 主从把 worker 叫 replica）。
// 返回空串表示「沿用引擎通用命名」。
type RoleNamer interface {
	HostRole(mode string, hostID, masterID int64, assignMaster bool) string
}

// BootstrapHostResolver 引导主机解析：引擎通用规则是「主角色主机」；
// 套件可覆盖（如大数据底座的集群初始化落在 NameNode 所在主机，可能与主节点分离）。
// 返回空串表示沿用通用规则。
type BootstrapHostResolver interface {
	BootstrapHostIP(hosts []model.StackRunHost) string
}

// InstanceVarsCtx 实例级参数补全上下文（探活 / 接入端点生成前调用，就地写入 Params）。
type InstanceVarsCtx struct {
	// Instance 集群实例（只读）。
	Instance *model.StackInstance
	// Params 待补全的参数视图（套件就地写入）。
	Params map[string]string
	// Hosts 实例在役成员（按 Seq 升序）。
	Hosts []model.StackRunHost
}

// InstanceVarInjector 实例级变量补全：角色计划缺失时由套件按自身规则推导权威参数。
// 目前只有大数据底座（HA 角色落点矩阵），引擎据此不再判 key。
type InstanceVarInjector interface {
	InjectInstanceVars(ctx InstanceVarsCtx)
}

// TopoCtx 拓扑校验上下文。
type TopoCtx struct {
	Mode     string
	Hosts    int
	Replicas int
	MasterID int64
	HostIDs  []int64
	// Params 套件参数（redis 集群从 replicas 读副本倍数；通用路径不消费）。
	Params map[string]string
}

// TopologyBuilder 拓扑校验：替代 stack_topo.go 的 redis 特判。
//
// 注：设计文档原拟返回 model.StackTopology，但 model 包为只读且无该类型；
// 故沿用既有「校验即返回 error」的形态（行为等价，B9 搬迁时不再改形）。
type TopologyBuilder interface {
	ValidateTopology(ctx TopoCtx) error
}

// DefaultsProvider 参数默认值补全：替代原 stack_create.go 的 cluster_id / erlang_cookie 注入、
// stack_phase.go 的引导阶段 replicas 兜底。就地写入 params（与既有实现一致）；返回错误则中止。
//
// op 为本次运行类型（create / bootstrap / scale_out …）：套件据此只在**该套件原本生效的时点**
// 补全，从而保持「创建期实例参数原样落库、运行期再兜底」这一既有行为不被改写。
type DefaultsProvider interface {
	Defaults(op, mode string, params map[string]string) error
}

// AssetProvisioner 离线资产声明（可选能力）：套件按模式与参数声明部署所需的离线物料
// （如 Hive HA 需要 MySQL Connector/J 驱动）。引擎在执行部署阶段前把已就绪的资产
// 经 SFTP 分发到目标机（{{home_dir}} 等占位按主机参数渲染）；资产未上传时跳过分发，
// 脚本内建下载兜底，不阻塞部署。副作用（落盘、SSH）仍在引擎，套件只声明。
type AssetProvisioner interface {
	RequiredAssets(mode string, params map[string]string) []model.StackAssetNeed
}

// VerifyProbe 旧版探活接口（B2 草案）。已被 probe.go 的 ProbePlugin 取代：
// 后者额外覆盖「期望容器清单 / 脚本尾片段 / 汇总提示」，才能把引擎里的套件分支清零。
// 保留类型别名仅为兼容，勿在新代码中使用。
type VerifyProbe = ProbePlugin
