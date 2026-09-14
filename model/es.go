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
