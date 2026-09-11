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
	ClusterName string      `json:"cluster_name"`
	Version     string      `json:"version"`
	Status      string      `json:"status"`
	TimedOut    bool        `json:"timed_out"`
	Nodes       int         `json:"nodes"`
	DataNodes   int         `json:"data_nodes"`
	ActiveShards int       `json:"active_shards"`
	ActivePrim  int         `json:"active_primary_shards"`
	Relocating  int         `json:"relocating_shards"`
	Initializing int       `json:"initializing_shards"`
	Unassigned  int         `json:"unassigned_shards"`
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
	Index      string `json:"index"`
	Health     string `json:"health"`
	Status     string `json:"status"`
	Primaries  string `json:"pri"`
	Replicas   string `json:"rep"`
	DocsCount  string `json:"docs_count"`
	DocsDeleted string `json:"docs_deleted"`
	StoreSize  string `json:"store_size"`
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
	Total  int64         `json:"total"`
	From   int           `json:"from"`
	Size   int           `json:"size"`
	Took   int           `json:"took"`
	TimedOut bool        `json:"timed_out"`
	Hits   []ESSearchHit `json:"hits"`
}