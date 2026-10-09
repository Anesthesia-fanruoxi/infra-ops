package model

// RedisConn 工具-Redis：一个可连接的 Redis 实例配置。
// SSHHostID 为 0 表示直连；大于 0 表示先经该主机建立 SSH 隧道再连（内网实例走跳板机场景）。
type RedisConn struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"` // ACL 用户名（Redis 6+ 的 AUTH user pass），可空
	EncryptedSecret []byte `json:"-"`        // 密码密文（永不下发前端）
	DefaultDB       int    `json:"default_db"`
	SSHHostID       int64  `json:"ssh_host_id"`
	Remark          string `json:"remark"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// RedisConnView Redis 连接列表展示视图（不含任何密码材料）。
type RedisConnView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	DefaultDB   int    `json:"default_db"`
	SSHHostID   int64  `json:"ssh_host_id"`
	SSHHostName string `json:"ssh_host_name"` // 跳板机名称（联表填充，仅展示）
	Remark      string `json:"remark"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// RedisKeyItem 扫描结果里的一条 key。
type RedisKeyItem struct {
	Key  string `json:"key"`
	Type string `json:"type"` // string / list / set / zset / hash / stream / none
	TTL  int64  `json:"ttl"`  // 秒；-1 永久；-2 已不存在
}

// RedisScanResult 一次 key 扫描的返回，三种调用（首次 / 更多 / 全部）共用同一结构。
//
// 扫描是「有游标的会话」而不是「一次查完」：SCAN 每轮只遍历一部分槽位，
// 返回的游标要带回下一次调用。ScanID 就是这份会话的凭据。
type RedisScanResult struct {
	ScanID   string         `json:"scan_id"`
	Prefix   string         `json:"prefix"`  // 使用者输入的原始匹配串
	Pattern  string         `json:"pattern"` // 实际下发的匹配串（转义后的前缀 + *）
	DB       int            `json:"db"`
	Keys     []RedisKeyItem `json:"keys"`
	Scanned  int64          `json:"scanned"`  // 本会话累计遍历过的 key 数量
	Found    int64          `json:"found"`    // 本会话累计命中（去重后）
	Returned int64          `json:"returned"` // 本会话累计返回给前端的条数
	HasMore  bool           `json:"has_more"` // 还有更多未取（游标未归零）
	Done     bool           `json:"done"`     // 游标已归零：本次匹配彻底扫完
	LimitHit bool           `json:"limit_hit"`
	MaxKeys  int            `json:"max_keys"` // 「展示全部」的条数上限
	ElapsedMs int64         `json:"elapsed_ms"`
}

// RedisDBInfo 一个库的概况。Keys / Expires 为 -1 表示未知（INFO keyspace 没给出该库）。
type RedisDBInfo struct {
	Index   int   `json:"index"`
	Keys    int64 `json:"keys"`
	Expires int64 `json:"expires"`
}

// RedisEntry 值预览里的一行；列名按类型在前端变化（zset 是「分数 / 成员」、hash 是「字段 / 值」…）。
type RedisEntry struct {
	Rank  string `json:"rank"`  // list 序号 / zset 分数 / stream ID
	Field string `json:"field"` // hash 字段名，其余类型为空
	Value string `json:"value"`
}

// RedisKeyDetail 单个 key 的只读详情。
type RedisKeyDetail struct {
	Key       string       `json:"key"`
	Type      string       `json:"type"`
	TTL       int64        `json:"ttl"`
	Size      int64        `json:"size"`      // 字符串长度 / 元素个数
	Preview   string       `json:"preview"`   // string 类型的值（超长截断）
	Entries   []RedisEntry `json:"entries"`   // 其余类型的结构化预览
	Truncated bool         `json:"truncated"` // 预览被截断
	ElapsedMs int64        `json:"elapsed_ms"`
}
