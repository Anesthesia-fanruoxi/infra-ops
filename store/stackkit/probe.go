package stackkit

import "infra-ops/model"

// 探活通用模型。字段与既有 api/stack 的 JSON 契约逐字对齐——这些结构直接回给前端，
// 改名 / 改 tag 即破坏接口，属边界外改动。

// Endpoint 套件成员的访问入口（探活结果回显）。
type Endpoint struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	URL       string `json:"url"`
	Role      string `json:"role,omitempty"`
}

// Check 单台主机上的一项探活结论。
type Check struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail"`
}

// VerifyResult 单机探活结果的通用模型：套件负责填 Checks / LiveRole，
// 多机聚合、成败汇总与文案（notes / client_hint）由引擎统一生成。
type VerifyResult struct {
	OK        bool       `json:"ok"`
	Role      string     `json:"role,omitempty"`
	LiveRole  string     `json:"live_role,omitempty"`
	Error     string     `json:"error,omitempty"`
	Checks    []Check    `json:"checks"`
	Endpoints []Endpoint `json:"endpoints,omitempty"`
}

// ProbeCtx 单台主机探活上下文（ProbePlugin 的入参）。
type ProbeCtx struct {
	Mode string
	// Role 计划角色（未实测时的期望角色）。
	Role string
	// HostIP 当前主机 IP。
	HostIP string
	// Params 该主机的参数合并视图（含主机级变量与全量 masters 投影）。
	Params map[string]string
	// Hosts 本次运行的全部主机（确定性落点判定用，按 Seq 升序）。
	Hosts []model.StackRunHost
}

// ProbeOutcome 套件探活解析产物。
type ProbeOutcome struct {
	// Checks 追加到该主机结果里的检查项（容器 / 实例 / 角色 / 复制 / 集群 / 哨兵 …）。
	Checks []Check
	// LiveRole 实测角色。
	LiveRole string
	// LiveRoleFromOutput 标记 LiveRole 是否来自「实测输出」：
	// true  → 原样采用（即使为空，表示确实没探到角色，如 redis 的 INFO replication 缺失）；
	// false → 引擎回落为该主机的期望角色（Role），与拆分前「非 redis 套件」的处理一致。
	LiveRoleFromOutput bool
	// OK 显式覆盖本主机是否通过；nil 表示由引擎按 Checks 全体判定（ChecksOK）。
	OK *bool
}

// ProbePlugin 探活能力：期望容器清单 + 脚本尾片段 + 输出解析 + 汇总提示。
//
// 调用约定（引擎侧）：
//   - Containers 返回期望容器名：非空 → 引擎把「容器/实例检查项」的整体渲染交给 ParseSuite；
//     为空 → 引擎自行做 compose 状态检查，再用 ParseSuite 追加套件专属检查项。
//   - Summary 返回该套件的探活说明与客户端命令（替代引擎里的套件 switch）。
type ProbePlugin interface {
	Containers(ctx ProbeCtx) []string
	// ScriptTail 追加到通用探活脚本尾部的片段（空 = 无）。
	ScriptTail(ctx ProbeCtx) string
	ParseSuite(ctx ProbeCtx, raw string, containers []string) ProbeOutcome
	Summary(ctx ProbeCtx, hosts []ProbeHostRow) (notes []string, clientHint string)
}

// ProbeHostRow 汇总阶段的单主机视图（避免 stackkit 依赖 api 层结构）。
type ProbeHostRow struct {
	HostID   int64
	HostName string
	HostIP   string
	Role     string
	LiveRole string
	OK       bool
	Checks   []Check
}

// EndpointCtx 接入端点生成上下文（一台主机一次）。
type EndpointCtx struct {
	Mode   string
	Host   model.StackInstanceHost
	Params map[string]string
	// InstName 实例名（通用兜底端点的名称）。
	InstName string
	// Components 解析后的组件列表（bigdata 用；其余套件可忽略）。
	Components []string
}

// EndpointProvider 接入端点能力：替代 stackVerifyEndpoints 的套件分支。
//
// 未实现该接口的套件由引擎给出通用兜底端点（tcp://<ip>），因此引擎侧只需一次能力断言。
type EndpointProvider interface {
	Endpoints(ctx EndpointCtx) []Endpoint
}
