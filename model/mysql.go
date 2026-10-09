package model

// MySQLConn 工具-MySQL：一个可连接的 MySQL 实例配置。
// SSHHostID 为 0 表示直连；大于 0 表示先经该主机建立 SSH 隧道再连库。
type MySQLConn struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	EncryptedSecret []byte `json:"-"` // 密码密文（永不下发前端）
	DefaultSchema   string `json:"default_schema"`
	Charset         string `json:"charset"`
	ReadOnly        bool   `json:"read_only"`
	SSHHostID       int64  `json:"ssh_host_id"`
	Remark          string `json:"remark"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// MySQLConnView MySQL 连接列表展示视图（不含任何密码材料）。
type MySQLConnView struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	HasPassword   bool   `json:"has_password"`
	DefaultSchema string `json:"default_schema"`
	Charset       string `json:"charset"`
	ReadOnly      bool   `json:"read_only"`
	SSHHostID     int64  `json:"ssh_host_id"`
	SSHHostName   string `json:"ssh_host_name"` // 跳板机名称（联表填充，仅展示）
	Remark        string `json:"remark"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// MySQLTable 库中的一张表或视图（information_schema.TABLES 摘要）。
type MySQLTable struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // BASE TABLE / VIEW
	Engine  string `json:"engine"`
	Rows    int64  `json:"rows"` // 估算行数，视图/非 InnoDB 可能为 0
	Comment string `json:"comment"`
}

// MySQLColumn 表字段定义（information_schema.COLUMNS）。
type MySQLColumn struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"` // 完整类型，如 varchar(64) / int unsigned
	Nullable bool    `json:"nullable"`
	Key      string  `json:"key"`     // PRI / UNI / MUL / ''
	Default  *string `json:"default"` // NULL 与「无默认值」区分由前端展示为 NULL / -
	Extra    string  `json:"extra"`   // auto_increment / on update CURRENT_TIMESTAMP ...
	Comment  string  `json:"comment"`
}

// MySQLIndex 表索引（information_schema.STATISTICS 聚合）。
type MySQLIndex struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}

// MySQLTableStatus 表概览（表详情顶部的统计条）。
type MySQLTableStatus struct {
	Name        string `json:"name"`
	Engine      string `json:"engine"`
	Collation   string `json:"collation"`
	Rows        int64  `json:"rows"`         // 估算行数（InnoDB 为统计值）
	DataLength  int64  `json:"data_length"`  // 数据长度（字节）
	IndexLength int64  `json:"index_length"` // 索引长度（字节）
	Comment     string `json:"comment"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// MySQLTableDetail 表详情：一次请求取回概览 + 字段 + 索引 + 建表语句。
type MySQLTableDetail struct {
	Status  MySQLTableStatus `json:"status"`
	Columns []MySQLColumn    `json:"columns"`
	Indexes []MySQLIndex     `json:"indexes"`
	DDL     string           `json:"ddl"`       // SHOW CREATE TABLE 原文
	DDLErr  string           `json:"ddl_error"` // 无权限等取不到 DDL 时的说明（不阻断其余内容）
}

// MySQLAIColumn AI 归纳出的单个字段说明。
// Type 为生成目录时标记的真实列类型（如 datetime / int）：生成 SQL 时据此把「今天 / 上周」
// 这类相对时间自适应换算成日期字面量或 Unix 时间戳，不再靠模型猜。旧目录无此字段，为空。
// Unit 为 int 时间戳列的探测结果（ms / s）：建目录时抽一条真实值判断存的是秒还是毫秒，
// 生成 SQL 时按对应单位换算；探测不出（空表 / 超时）留空，按秒级兜底。
type MySQLAIColumn struct {
	Name string `json:"name"`
	Note string `json:"note"`
	Type string `json:"type,omitempty"`
	Unit string `json:"unit,omitempty"`
}

// MySQLAICatalogEntry 表语义目录条目：AI 依据表/字段注释归纳出的用途与字段含义。
type MySQLAICatalogEntry struct {
	SchemaName  string          `json:"schema_name"`
	TableName   string          `json:"table_name"`
	Purpose     string          `json:"purpose"`
	Columns     []MySQLAIColumn `json:"columns"`
	Fingerprint string          `json:"fingerprint"`
	GeneratedAt string          `json:"generated_at"`
}

// MySQLAICatalogState 一次目录生成的执行结果。
type MySQLAICatalogState struct {
	Schema    string       `json:"schema"`
	Total     int          `json:"total"`     // 库内表总数
	Generated int          `json:"generated"` // 本次新生成
	Cached    int          `json:"cached"`    // 命中缓存跳过
	Stale     int          `json:"stale"`     // 结构变化需重生成
	Failed    int          `json:"failed"`    // 生成失败
	Batches   int          `json:"batches"`   // 实际请求模型的批次数
	Message   string       `json:"message"`
	Usage     MySQLAIUsage `json:"usage"` // 本次生成累计的 token 用量（逐批相加）
}

// MySQLAIUsage 一次（或一轮）模型调用的 token 用量。
//
// Source 为 estimated 时数值是按字符估算的近似值（接口没回 usage），
// 界面上必须把来源一并显示出来，否则会被当成接口给的精确数字。
type MySQLAIUsage struct {
	Calls            int    `json:"calls"` // 本轮发起的模型调用次数（语义目录逐批请求，故可能 > 1）
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	Source           string `json:"source"` // api / estimated
	ElapsedMs        int64  `json:"elapsed_ms"`
}

// MySQLAISQLResult 自然语言生成 SQL 的结果。
type MySQLAISQLResult struct {
	SQL            string       `json:"sql"`
	Tables         []string     `json:"tables"`          // SQL 用到的表
	Explain        string       `json:"explain"`         // 一句话说明
	UsedCatalog    int          `json:"used_catalog"`    // 本次送进上下文的表数量
	SQLType        string       `json:"sql_type"`        // DQL / SESSION / DML / DDL / OTHER
	StatementCount int          `json:"statement_count"` // 生成语句的条数
	ConfirmLevel   string       `json:"confirm_level"`   // 追加到编辑器后执行时的确认档位
	Mode           string       `json:"mode"`            // 生成时的会话模式（read / write）
	Usage          MySQLAIUsage `json:"usage"`           // 本次 token 用量
}

// MySQLExplainTable 执行计划分析时随上下文送出的相关表结构（information_schema 元信息）。
// 只有字段 / 索引 / 行数大小这类结构信息，不含任何业务行数据。
type MySQLExplainTable struct {
	Schema      string   `json:"schema"`
	Name        string   `json:"name"`
	Engine      string   `json:"engine"`
	Rows        int64    `json:"rows"`         // 估算行数（information_schema.TABLES，非实时精确值）
	DataLength  int64    `json:"data_length"`  // 数据长度（字节）
	IndexLength int64    `json:"index_length"` // 索引长度（字节）
	Columns     []string `json:"columns"`      // 每列一行：名 类型 [NOT NULL] [KEY] 注释
	Indexes     []string `json:"indexes"`      // 每索引一行：名 [UNIQUE] (列…) 基数≈N
	Note        string   `json:"note"`         // 取不到或截断时的说明
}

// MySQLExplainDigest 执行计划的文本摘要 + 相关表结构统计：AI 分析的输入，同时回传前端供对照。
type MySQLExplainDigest struct {
	RowCount  int                 `json:"row_count"` // 计划总行数
	Truncated bool                `json:"truncated"` // 行数过多时 lines 只含前若干行
	Lines     []string            `json:"lines"`     // 每行一个计划节点（列名=值 拼接）
	Tables    []MySQLExplainTable `json:"tables"`    // 计划涉及的表结构 / 索引 / 行数大小（元信息）
}

// MySQLAIExplainResult AI 分析 EXPLAIN 执行计划的结果。
type MySQLAIExplainResult struct {
	Summary     string   `json:"summary"`
	Findings    []string `json:"findings"`
	Suggestions []string `json:"suggestions"`
	// 建议新增 / 调整索引的建索引语句：仅回传展示供复制，服务端不会自动执行（可能为空数组）
	IndexStatements []string `json:"index_statements"`
	// 语义不变的局部小改版本（保留原 EXPLAIN 前缀；无可优化空间时为空字符串）
	OptimizedSQL string             `json:"optimized_sql"`
	Digest       MySQLExplainDigest `json:"digest"`
	Usage        MySQLAIUsage       `json:"usage"`
}

// MySQLResultColumn 结果集列元数据。
type MySQLResultColumn struct {
	Name string `json:"name"`
	Type string `json:"type"` // 数据库类型名，如 VARCHAR / BIGINT / DATETIME
}

// MySQLStatementResult 多语句执行时，单条语句的执行摘要。
type MySQLStatementResult struct {
	Kind         string `json:"kind"`          // 首关键词，如 SELECT / UPDATE / DROP
	Type         string `json:"type"`          // DQL / SESSION / DML / DDL / OTHER
	AffectedRows int64  `json:"affected_rows"` // 仅写类语句有意义
	ElapsedMs    int64  `json:"elapsed_ms"`
}

// MySQLModeState 会话运行模式：连接之后由使用者在工作台自行切换。
type MySQLModeState struct {
	Mode        string `json:"mode"`         // 当前模式：read / write
	DefaultMode string `json:"default_mode"` // 连接配置里的默认模式
}

// MySQLQueryResult SQL 执行的统一结果：查询返回列 + 行，写操作返回影响行数。
type MySQLQueryResult struct {
	Columns        []MySQLResultColumn    `json:"columns"`
	Rows           [][]interface{}        `json:"rows"`
	RowCount       int                    `json:"row_count"`
	Truncated      bool                   `json:"truncated"` // 命中行数上限被截断
	ElapsedMs      int64                  `json:"elapsed_ms"`
	AffectedRows   int64                  `json:"affected_rows"`
	LastInsertID   int64                  `json:"last_insert_id"`
	IsWrite        bool                   `json:"is_write"`
	StatementKind  string                 `json:"statement_kind"`  // 风险最高那条的首关键词
	SQLType        string                 `json:"sql_type"`        // DQL / SESSION / DML / DDL / OTHER
	Mode           string                 `json:"mode"`            // 执行时的会话模式
	StatementCount int                    `json:"statement_count"` // 本次输入的语句条数
	Statements     []MySQLStatementResult `json:"statements"`      // 逐条执行摘要
	// 仅单条 EXPLAIN 执行计划（EXPLAIN SELECT ... 类）的结果为 true：
	// 前端据此显示「AI 分析」入口，其余语句结果一律不给分析入口。
	CanAnalyze bool `json:"can_analyze"`
}

// ---------------- 使用记录：SQL 执行记录 ----------------

// AI 调用记录的取值口径（菜单 / 类型 / 状态 / token 来源 / 结构）在 model/ai_log.go。

// SQL 语句类型 —— 门禁「判断 2」的分类结果，只有 DQL 与 SESSION 视为只读。
// 放在 model 里是为了让门禁分类（api/tool/mysql）与使用记录统计（store/repo）
// 共用同一套字面量，不出现两份各自维护的取值表。
const (
	SQLTypeDQL     = "DQL"     // 纯查询
	SQLTypeSession = "SESSION" // 会话控制（SET / USE / BEGIN …），不碰数据
	SQLTypeDML     = "DML"     // 数据变更
	SQLTypeDDL     = "DDL"     // 结构变更
	SQLTypeOther   = "OTHER"   // 其余（存储过程 / 权限 / 未知），按最严处理
)

// MySQLSQLLog 一次 SQL 执行的记录 —— 描述「人在工具里执行了这句 SQL」这件事本身，
// 不标注语句从哪来（手写、从别处粘贴还是借鉴 AI 结果都不区分），
// 也不与 AI 调用记录做任何关联。
type MySQLSQLLog struct {
	ID             int64  `json:"id"`
	ConnID         int64  `json:"conn_id"`
	ConnName       string `json:"conn_name"`
	SchemaName     string `json:"schema"`
	SQL            string `json:"sql"`
	StatementCount int    `json:"statement_count"`
	SQLType        string `json:"sql_type"`       // DQL / SESSION / DML / DDL / OTHER
	StatementKind  string `json:"statement_kind"` // 风险最高那条的首关键词
	Mode           string `json:"mode"`           // 执行时的会话模式
	Confirmed      bool   `json:"confirmed"`      // 是否经过二次确认才放行
	RowCount       int    `json:"row_count"`      // 返回行数
	AffectedRows   int64  `json:"affected_rows"`  // 写语句影响行数
	ElapsedMs      int64  `json:"elapsed_ms"`
	Status         string `json:"status"` // ok / error
	Error          string `json:"error"`
	CreatedAt      string `json:"created_at"`
}

// MySQLLogFilter SQL 执行记录的查询条件。
type MySQLLogFilter struct {
	ConnID   int64  // 0 = 全部连接
	Status   string // ok / error / 空=全部
	SQLType  string // DQL / DML / ...
	Page     int
	PageSize int
}

// Offset 分页偏移。
func (f MySQLLogFilter) Offset() int {
	if f.Page < 1 {
		return 0
	}
	return (f.Page - 1) * f.PageSize
}

// MySQLSQLTypeStat 按语句类型聚合的执行次数。
type MySQLSQLTypeStat struct {
	SQLType string `json:"sql_type"`
	Runs    int    `json:"runs"`
}

// MySQLSQLLogSummary SQL 执行记录的全量汇总（不受分页影响）。
type MySQLSQLLogSummary struct {
	Runs         int                `json:"runs"`
	OK           int                `json:"ok"`
	Failed       int                `json:"failed"`
	QueryRuns    int                `json:"query_runs"` // DQL / SESSION
	WriteRuns    int                `json:"write_runs"` // DML / DDL / OTHER
	RowsReturned int64              `json:"rows_returned"`
	RowsAffected int64              `json:"rows_affected"`
	AvgElapsedMs int64              `json:"avg_elapsed_ms"`
	FirstAt      string             `json:"first_at"`
	LastAt       string             `json:"last_at"`
	ByType       []MySQLSQLTypeStat `json:"by_type"`
}
