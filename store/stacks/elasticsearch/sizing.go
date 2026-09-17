package elasticsearch

import (
	"strconv"
	"strings"
)

// sizing.go：规格档位（内部预设）——把原来「每个节点手工填 JVM 堆 + GC 参数」的繁琐配置，
// 收敛为三档预设，由平台按「档位 × 角色」直接给出该角色容器的堆内存，用户不再需要理解
// -Xms/-Xmx、compressed oops、GC 参数这些细节。
//
// 为什么堆必须由平台显式给定，而不能交给 ES 自动推导：
//
//	ES 8.7+ 默认按「节点角色 + 容器可见内存」自动定堆（官方口径：多数生产环境推荐默认值）。
//	但冷热温模式是**一机多容器**——同一台宿主可同时跑 master / coordinator / hot / warm / cold
//	多个容器。容器未设内存硬限时每个容器都看到整机内存，于是各自按整机内存的 ~50% 定堆，
//	叠起来必然超配（3 个容器 = 1.5 倍整机内存），结果是 swap 与 GC 抖动。
//	所以这里按角色显式给堆，并天然满足两条官方硬约束：
//	  1. 堆 ≤ 宿主内存 50%（其余留给 Lucene 的页缓存；ES 官方 heap-size 文档）——
//	     **仅约束数据节点**，master / 纯协调不存分片，按「堆 + 4G 开销」给即可（见 esSizingRole）；
//	  2. 堆 ≤ 31G（HotSpot compressed oops 上限，越界后指针变宽反而更慢）。
//
// GC 与其它 JVM 参数一律不再暴露：ES 官方口径「默认 JVM/GC 参数已适配绝大多数场景，
// 除非有明确证据不要改动」，因此 ES_JAVA_OPTS 只写 -Xms/-Xmx 与一条显式的 -XX:+UseG1GC
// （G1 是 ES 默认 GC，显式声明只为日志排查时一眼可见）。
//
// 档位口径（2026-09-16 用户拍板 v1.4，同日补回性能基准）：档位定义「每角色的 JVM 参数
// + 基本数量（Ref）+ 单 hot 节点性能基准」。按数据量推「该给多少台」没有准数，
// 机器数量与硬件规格最终由用户按自己手上的资源决定，档位数值只是参考不是要求。
//
// 三档定位：
//
//	mini     小额尝鲜 —— 每角色各 1 台（master/协调/hot/warm/cold 各 1），POC / 联调 / 开发测试；
//	standard 标准使用 —— 默认档，生产常规给堆，承载业务初期到中期的生产用量；
//	full     大力出奇迹 —— hot 层堆顶到 31G，面向大规模日志 / APM / 安全分析。
const (
	// esSizingMini 小额尝鲜档。
	esSizingMini = "mini"
	// esSizingStandard 标准使用档（默认）。
	esSizingStandard = "standard"
	// esSizingFull 大力出奇迹档（hot 拉满）。
	esSizingFull = "full"

	// esRoleCluster 集群（非冷热温）模式的逻辑角色：节点同时承担 master+data，
	// 按「数据节点」规格取堆（与冷热温 data_hot 同档）。
	esRoleCluster = "cluster"
)

// esSizingRole 某一档位下单个角色的规格：JVM 堆 + 内存建议 + 基本数量。
//
// 内存建议 MemGB 的取值口径**按角色分两类**（这是最容易套错的地方，2026-09-16 修）：
//   - 数据节点（hot/warm/cold）：内存 = 堆 × 2。ES 官方 heap-size 口径是「堆 ≤ 宿主内存 50%」，
//     另一半不是浪费，是留给 Lucene 的页缓存（段文件读全靠它，命中率高不高直接决定查询延迟）。
//   - master / 纯协调：内存 = 堆 + 4G。这两类**不存分片**，没有 Lucene 页缓存需求，
//     只需覆盖 JVM 堆外（metaspace/线程栈/netty direct buffer）与系统开销，
//     再按「2 × 堆」给就是纯虚胖（曾把满载档纯协调写成 16g 堆配 64G 内存）。
//
// master 与纯协调**合并为一行**（Role 仍为 "master"）：master 候选本身隐式协调，
// 两角色规格取原纯协调（较重）的一档，取大不取小。部署注入时 coordinator 角色的堆
// 复用这一行（见 roleHeaps），脚本侧 HEAP_COORDINATOR 变量不变。
type esSizingRole struct {
	Role    string `json:"role"`     // master（含协调）/ data_hot / data_warm / data_cold
	Label   string `json:"label"`    // 展示名（数据角色与 plan.go cwhRoleLabel 同一词表）
	HeapGB  int    `json:"heap_gb"`  // 本档该角色容器的 JVM 堆（GB，Xms=Xmx）
	MemGB   int    `json:"mem_gb"`   // 建议宿主内存（GB），口径见上
	MemRule string `json:"mem_rule"` // 内存口径短标注（界面在内存列后缀显示）
	Ref     string `json:"ref"`      // 基本数量（各档位的最基本台数：mini 各 ×1，生产 master ×3 · 协调 ×2，数据层按需 n）
}

// 内存口径短标注与整句说明（随 Extras 一并下发，界面直接展示，避免「数字看着吓人但没说为什么」）。
const (
	esMemRuleData      = "堆 × 2"
	esMemRuleStateless = "堆 + 4G"
	esMemNote          = "内存口径：数据层 = 堆 × 2（另一半留给 Lucene 页缓存，ES 官方建议堆 ≤ 宿主内存 50%）；" +
		"master / 纯协调不存分片、无页缓存需求，= 堆 + 4G 系统与堆外开销。同机混部时，宿主内存按各容器建议内存之和取。"
)

// withMemRule 给角色补上内存口径短标注（按角色分类，界面逐行展示）。
func (r esSizingRole) withMemRule() esSizingRole {
	if r.Role == "master" {
		r.MemRule = esMemRuleStateless
	} else {
		r.MemRule = esMemRuleData
	}
	return r
}

// esSizingProfile 一个规格档位：每角色的 JVM 参数（堆）+ 内存建议 + 基本数量，
// 外加单 hot 节点的性能基准（2026-09-16 用户要求恢复）。
//
// 性能基准是**单 hot 节点口径**（一个 hot 节点能扛的日增/写入/QPS 量级），集群能力 ≈ n × 该值，
// 由用户按自己打算给多少台数据节点自行换算——界面不做集群级推算（按数据量推台数没有准数，
// 做过 n 换算芯片又撤掉）。这些是量级参考而非承诺，上线前以真实数据压测校准。
type esSizingProfile struct {
	Key          string         `json:"key"`
	Label        string         `json:"label"`          // 小额尝鲜 / 标准使用 / 大力出奇迹
	Summary      string         `json:"summary"`        // 一句话定位
	Fit          string         `json:"fit"`            // 适用场景（参考）
	HotDailyGB   string         `json:"hot_daily_gb"`   // 单 hot 节点日增量级
	HotWriteMBps string         `json:"hot_write_mbps"` // 单 hot 节点持续写入吞吐
	QueryQPS     string         `json:"query_qps"`      // 单 hot 节点查询量级
	Note         string         `json:"note"`           // 取舍说明（参考）
	MemNote      string         `json:"mem_note"`       // 内存口径整句说明（三档同源，esSizingTable 统一补）
	Roles        []esSizingRole `json:"roles"`
}

// esSizingProfiles 三档预设表：堆内存的唯一事实源（部署注入与界面展示同源，不会漂移）。
//
// 规格数值沿用既有口径不动（用户明确「别乱动」）；master/协调合并行取原纯协调值。
// 参考数量：mini = 每角色各 1 台（用户口径「11111」）；生产档 master ×3（奇数）· 协调 ×2，
// 数据层一律「按需 n」——数量由用户决定，档位不写死也不建议。
var esSizingProfiles = map[string]esSizingProfile{
	esSizingMini: {
		Key:          esSizingMini,
		Label:        "小额尝鲜",
		Summary:      "每角色各 1 台的最小分层，成本最低",
		Fit:          "POC 验证 / 接口联调 / 开发测试环境",
		HotDailyGB:   "5–20 GB / 节点",
		HotWriteMBps: "10–20 MB/s（约 1–2 万条/秒）",
		QueryQPS:     "30–100 QPS 起",
		Note:         "基本数量：master / 协调 / hot / warm / cold 各 1 台。任一节点单点无冗余，不要用于生产。",
		Roles: []esSizingRole{
			{Role: "master", Label: "master / 协调", HeapGB: 2, MemGB: 6, Ref: "各 ×1"},
			{Role: "data_hot", Label: cwhRoleLabel("data_hot"), HeapGB: 6, MemGB: 12, Ref: "×1"},
			{Role: "data_warm", Label: cwhRoleLabel("data_warm"), HeapGB: 4, MemGB: 8, Ref: "×1"},
			{Role: "data_cold", Label: cwhRoleLabel("data_cold"), HeapGB: 2, MemGB: 6, Ref: "×1"},
		},
	},
	esSizingStandard: {
		Key:          esSizingStandard,
		Label:        "标准使用",
		Summary:      "默认档：生产常规给堆，分层齐备",
		Fit:          "中型业务主搜索集群 / 日志与指标平台",
		HotDailyGB:   "30–80 GB / 节点",
		HotWriteMBps: "20–40 MB/s（约 2–4 万条/秒）",
		QueryQPS:     "每 hot 节点约 100–400 QPS",
		Note:         "基本数量：master ×3（奇数）· 协调 ×2；数据层按需 n。机器数量与硬件由你按实际资源决定。",
		Roles: []esSizingRole{
			{Role: "master", Label: "master / 协调", HeapGB: 8, MemGB: 12, Ref: "master ×3 · 协调 ×2"},
			{Role: "data_hot", Label: cwhRoleLabel("data_hot"), HeapGB: 16, MemGB: 32, Ref: "按需 n"},
			{Role: "data_warm", Label: cwhRoleLabel("data_warm"), HeapGB: 8, MemGB: 16, Ref: "按需 n"},
			{Role: "data_cold", Label: cwhRoleLabel("data_cold"), HeapGB: 4, MemGB: 8, Ref: "按需 n"},
		},
	},
	esSizingFull: {
		Key:          esSizingFull,
		Label:        "大力出奇迹",
		Summary:      "hot 层拉满：单容器堆顶到 31G（compressed oops 上限）",
		Fit:          "大规模日志 / APM / 安全分析，写入持续高位",
		HotDailyGB:   "80–230 GB / 节点",
		HotWriteMBps: "40–80 MB/s（约 4–8 万条/秒）",
		QueryQPS:     "每 hot 节点约 150–650 QPS",
		Note:         "hot 单容器堆已到 31G 上限，单节点规格无法再加大——要更高容量/吞吐就加数据节点数量。基本数量：master ×3 · 协调 ×2，数据层按需 n。",
		Roles: []esSizingRole{
			{Role: "master", Label: "master / 协调", HeapGB: 16, MemGB: 20, Ref: "master ×3 · 协调 ×2"},
			{Role: "data_hot", Label: cwhRoleLabel("data_hot"), HeapGB: 31, MemGB: 64, Ref: "按需 n"},
			{Role: "data_warm", Label: cwhRoleLabel("data_warm"), HeapGB: 16, MemGB: 32, Ref: "按需 n"},
			{Role: "data_cold", Label: cwhRoleLabel("data_cold"), HeapGB: 8, MemGB: 16, Ref: "按需 n"},
		},
	},
}

// esSizingKeys 档位顺序（界面展示与测试共用）。
var esSizingKeys = []string{esSizingMini, esSizingStandard, esSizingFull}

// esSizingNormalize 归一化档位取值：未知/空/历史实例缺失一律回落默认档 standard。
// 历史实例（本次改造前部署）参数里没有 sizing，扩容时按 standard 处理，不会中断变更。
func esSizingNormalize(v string) string {
	k := strings.ToLower(strings.TrimSpace(v))
	if _, ok := esSizingProfiles[k]; ok {
		return k
	}
	return esSizingStandard
}

// esSizingLabel 档位展示名（未知档位回落默认档的展示名）。
func esSizingLabel(v string) string { return esSizingProfiles[esSizingNormalize(v)].Label }

// esRoleHeap 取「档位 × 角色」的堆内存字符串（如 "16g"）。
// 集群模式角色（esRoleCluster）映射到数据节点档位；coordinator 复用合并后的 master 行；
// 未知角色按数据-hot 兜底（角色已由 validateCWHRoles 白名单校验，此处仅为防御性兜底：
// 宁大勿小，避免 ES 以 1g 默认堆启动）。
func esRoleHeap(profile, role string) string {
	p := esSizingProfiles[esSizingNormalize(profile)]
	if r, ok := p.roleHeaps()[normalizeSizingRole(role)]; ok {
		return strconv.Itoa(r) + "g"
	}
	r, _ := p.roleHeaps()["data_hot"]
	return strconv.Itoa(r) + "g"
}

// normalizeSizingRole 把「集群模式角色」归一为规格表里的数据节点角色。
func normalizeSizingRole(role string) string {
	if role == esRoleCluster {
		return "data_hot"
	}
	return role
}

// roleHeaps 该档位「角色 → 堆（GB）」视图。
// 规格表里 master 一行同时覆盖 master 与 coordinator 两个部署角色（取大不取小）。
func (p esSizingProfile) roleHeaps() map[string]int {
	out := make(map[string]int, len(p.Roles)+1)
	for _, r := range p.Roles {
		out[r.Role] = r.HeapGB
		if r.Role == "master" {
			out["coordinator"] = r.HeapGB
		}
	}
	return out
}

// esSizingTable 三档完整规格表（按 esSizingKeys 顺序），供蓝图 Extras 下发给前端；
// 与部署注入（esRoleHeap）同源，前端不需要再维护一份数字。
//
// 内存口径标注（每行 mem_rule + 整句 mem_note）在这里统一补齐：写表的人只需要填数字，
// 口径解释不会漏、也不会三档写得不一致。
func esSizingTable() []esSizingProfile {
	out := make([]esSizingProfile, 0, len(esSizingKeys))
	for _, k := range esSizingKeys {
		p, ok := esSizingProfiles[k]
		if !ok {
			continue
		}
		p.MemNote = esMemNote
		roles := make([]esSizingRole, 0, len(p.Roles))
		for _, r := range p.Roles {
			roles = append(roles, r.withMemRule())
		}
		p.Roles = roles
		out = append(out, p)
	}
	return out
}
