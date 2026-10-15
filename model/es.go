package model

// ESConn 工具-Elasticsearch：一个可连接的 ES 集群配置。
type ESConn struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	Insecure        bool   `json:"insecure"`
	AuthType        string `json:"auth_type"` // anonymous | basic
	Username        string `json:"username"`
	EncryptedSecret []byte `json:"-"`
	Remark          string `json:"remark"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// ESConnView ES 连接列表展示视图（不返回任何密码材料）。
type ESConnView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Insecure    bool   `json:"insecure"`
	AuthType    string `json:"auth_type"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	Remark      string `json:"remark"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ESOverview 集群概览（版本 + 健康）。
type ESOverview struct {
	ClusterName  string `json:"cluster_name"`
	Version      string `json:"version"`
	Status       string `json:"status"`
	TimedOut     bool   `json:"timed_out"`
	Nodes        int    `json:"nodes"`
	DataNodes    int    `json:"data_nodes"`
	ActiveShards int    `json:"active_shards"`
	ActivePrim   int    `json:"active_primary_shards"`
	Relocating   int    `json:"relocating_shards"`
	Initializing int    `json:"initializing_shards"`
	Unassigned   int    `json:"unassigned_shards"`
}

// ESNode ES 集群节点信息。
type ESNode struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Roles         []string `json:"roles"`
	CPUPercent    int      `json:"cpu_percent"`
	HeapPercent   int      `json:"heap_percent"`
	DiskTotal     int64    `json:"disk_total"`
	DiskAvailable int64    `json:"disk_available"`
	DocsCount     int64    `json:"docs_count"`
}

// ESIndex 索引概要（来自 _cat/indices）。
type ESIndex struct {
	Index       string `json:"index"`
	Health      string `json:"health"`
	Status      string `json:"status"`
	Primaries   string `json:"pri"`
	Replicas    string `json:"rep"`
	DocsCount   string `json:"docs_count"`
	DocsDeleted string `json:"docs_deleted"`
	StoreSize   string `json:"store_size"`
}

// ESSearchHit ES 搜索结果中的一条命中。
type ESSearchHit struct {
	Index  string      `json:"index"`
	ID     string      `json:"id"`
	Score  interface{} `json:"score"`
	Source interface{} `json:"source"`
}

// ESSearchResult ES 查询结果。
type ESSearchResult struct {
	Total    int64         `json:"total"`
	From     int           `json:"from"`
	Size     int           `json:"size"`
	Took     int           `json:"took"`
	TimedOut bool          `json:"timed_out"`
	Hits     []ESSearchHit `json:"hits"`
}

// ESViewField 数据视图的单个合并字段（跨索引合并语义见 docs/ES控制台设计.md §5.5）。
type ESViewField struct {
	Path         string   `json:"path"`
	Types        []string `json:"types"` // 多类型即 type_conflict
	Searchable   bool     `json:"searchable"`
	Aggregatable bool     `json:"aggregatable"`
	TypeConflict bool     `json:"type_conflict"`
	Partial      bool     `json:"partial"` // 仅部分成员存在
	SubFields    []string `json:"sub_fields,omitempty"`
	Analyzer     string   `json:"analyzer,omitempty"`
	Format       string   `json:"format,omitempty"`
}

// ESView 数据视图：对齐 Kibana 三要素（名称 / 索引匹配 / 时间字段），字段表本地持久化 + 异步同步。
type ESView struct {
	ID           int64  `json:"id"`
	ConnID       int64  `json:"conn_id"`
	Name         string `json:"name"`
	IndexPattern string `json:"index_pattern"`
	TimeField    string `json:"time_field"`
	FieldsJSON   string `json:"-"`           // []ESViewField 快照
	StatsJSON    string `json:"-"`           // 命中概览快照
	SyncStatus   string `json:"sync_status"` // idle | syncing | failed
	SyncError    string `json:"sync_error"`
	SyncedAt     string `json:"synced_at"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// ESFieldCache 一个 ES 连接的「系统自带字段」缓存（生成 logstash 配置时删哪些字段用）。
//
// 为什么落库、而不是生成时现查：
//   - 这份清单要从集群里探测（读版本 → 该版本的 ECS 组件模板 → 再核对集群里真实存在哪些），
//     每次生成配置都打一遍集群既慢、又让一个纯本地的拼装动作依赖集群在线；
//   - 它随版本变化得很慢（集群升级才会变），所以启动时探测一次、之后按周期刷新就够。
//
// Fields 与 Extra 分开存的原因不同：前者是探测出来的（会被下一轮覆盖），后者是人工登记的
// （offset / logYear / logMillis 这类 ECS 之外的采集器字段），**任何一轮探测都不许动它**。
type ESFieldCache struct {
	ID        int64  `json:"id"`
	ConnID    int64  `json:"conn_id"`
	ESVersion string `json:"es_version"` // 探测时的集群版本，如 8.13.0
	// Fields 自动发现：候选池 ∩ 该版本 ECS 定义里存在 ∩ 集群里真实存在
	Fields []string `json:"fields"`
	// Extra 人工补充：ECS 之外的采集器自定义字段，界面上可编辑
	Extra []string `json:"extra"`
	// Status ok | no_ecs（该版本没有 ECS 组件模板，退化为候选池）| failed
	Status   string `json:"status"`
	Error    string `json:"error"`
	SyncedAt string `json:"synced_at"`
	// UpdatedAt 只随 Extra 变化（探测覆盖的是 Fields，不算「人工改动」）
	UpdatedAt string `json:"updated_at"`
}
