// Package mysql 工具-MySQL：连接会话建立（直连 / SSH 隧道）、元数据查询与 SQL 执行。
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/ssh"

	"infra-ops/common/crypto"
	"infra-ops/common/sshx"
	"infra-ops/model"
	"infra-ops/store/repo"
	"infra-ops/store/setting"
)

const (
	defaultPort         = 3306
	defaultCharset      = "utf8mb4"
	connectTimeout      = 10 * time.Second
	defaultQueryTimeout = 30 * time.Second
	maxQueryTimeout     = 300 * time.Second
	defaultRowLimit     = 1000
	maxRowLimit         = 5000
)

// Deps 处理器依赖（装配处一次性注入）。
type Deps struct {
	Repo      *repo.MySQLRepo
	AIRepo    *repo.MySQLAIRepo
	LogRepo   *repo.MySQLLogRepo
	AILogRepo *repo.AILogRepo // 统一 AI 调用记录（写 ai_logs，menu=mysql）
	HostRepo  *repo.HostRepo
	CredRepo  *repo.CredentialRepo
	CryptoS   *crypto.Service
	SSHC      *sshx.Client
	Settings  *setting.SettingsRepo
}

// Handler MySQL 工具处理器。
type Handler struct {
	repo         *repo.MySQLRepo
	aiRepo       *repo.MySQLAIRepo
	logRepo      *repo.MySQLLogRepo
	hostRepo     *repo.HostRepo
	credRepo     *repo.CredentialRepo
	cryptoS      *crypto.Service
	sshC         *sshx.Client
	settings     *setting.SettingsRepo
	aiLogRepo    *repo.AILogRepo      // 统一 AI 调用记录（写 ai_logs，menu=mysql）
	modes        *modeRegistry        // 门禁判断 1 的状态：连接当前的读写模式
	transfers    *transferProgress    // 导出 / 导入的进度（前端轮询取）
	catalogTasks *catalogTaskRegistry // 语义目录生成的后台任务（每连接保留最近一个，前端轮询查进度 / 停止）
	queryCancels *queryCancelRegistry // 正在执行的 SQL（每连接一条）：「取消」按钮的中断入口
}

// NewHandler 创建 MySQL 工具处理器。
func NewHandler(d Deps) *Handler {
	return &Handler{
		repo:         d.Repo,
		aiRepo:       d.AIRepo,
		logRepo:      d.LogRepo,
		hostRepo:     d.HostRepo,
		credRepo:     d.CredRepo,
		cryptoS:      d.CryptoS,
		sshC:         d.SSHC,
		settings:     d.Settings,
		aiLogRepo:    d.AILogRepo,
		modes:        newModeRegistry(),
		transfers:    newTransferProgress(),
		catalogTasks: newCatalogTaskRegistry(),
		queryCancels: newQueryCancelRegistry(),
	}
}

// session 一次请求期间持有的数据库会话；走隧道时附带 SSH 连接与本地转发监听。
type session struct {
	db      *sql.DB
	sshConn *ssh.Client
	ln      net.Listener
}

func (s *session) Close() {
	if s.db != nil {
		s.db.Close()
	}
	if s.ln != nil {
		s.ln.Close()
	}
	if s.sshConn != nil {
		s.sshConn.Close()
	}
}

// ---------------- 会话建立 ----------------

// password 解密连接密码。
func (h *Handler) password(conn *model.MySQLConn) (string, error) {
	if len(conn.EncryptedSecret) == 0 {
		return "", nil
	}
	pw, err := h.cryptoS.Decrypt(conn.EncryptedSecret)
	if err != nil {
		return "", fmt.Errorf("解密连接密码失败")
	}
	return string(pw), nil
}

// open 建立数据库会话。选中跳板机时先建 SSH 隧道，再经本地转发端口连库。
func (h *Handler) open(conn *model.MySQLConn) (*session, error) {
	if conn.Port == 0 {
		conn.Port = defaultPort
	}
	target := net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port))
	addr := target
	s := &session{}

	if conn.SSHHostID > 0 {
		sshConn, ln, err := h.startTunnel(conn.SSHHostID, target)
		if err != nil {
			return nil, err
		}
		s.sshConn, s.ln = sshConn, ln
		addr = ln.Addr().String()
	}

	pw, err := h.password(conn)
	if err != nil {
		s.Close()
		return nil, err
	}
	cfg := gomysql.NewConfig()
	cfg.User = conn.Username
	cfg.Passwd = pw
	cfg.Net = "tcp"
	cfg.Addr = addr
	cfg.DBName = conn.DefaultSchema
	cfg.Timeout = connectTimeout
	cfg.ParseTime = true
	cfg.Loc = time.Local
	cfg.Params = map[string]string{"charset": charsetOf(conn)}
	if conn.DefaultSchema == "" {
		// 未指定库时去掉结尾的 "/"，否则 DSN 解析为库名空串
		cfg.DBName = ""
	}

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("初始化连接失败: %w", err)
	}
	// 隧道场景下连接池里的陈旧连接会指向已关闭的转发端口，收缩池子降低踩中概率
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Minute)
	s.db = db

	// sql.Open 是惰性的，不探活的话后端第一个查询才暴露连不上，
	// 报出来会变成「读取表概览失败: dial tcp …」这类跑偏的提示。这里先探一次，统一成「连接失败」。
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("连接失败: %w", err)
	}
	return s, nil
}

func charsetOf(conn *model.MySQLConn) string {
	if strings.TrimSpace(conn.Charset) == "" {
		return defaultCharset
	}
	return strings.TrimSpace(conn.Charset)
}

// startTunnel 复用「主机管理 + 凭据管理」建立 SSH 连接，并在本机随机端口做转发。
func (h *Handler) startTunnel(hostID int64, target string) (*ssh.Client, net.Listener, error) {
	host, err := h.hostRepo.GetByID(hostID)
	if err != nil || host == nil {
		return nil, nil, fmt.Errorf("跳板机不存在（host_id=%d）", hostID)
	}
	cred, err := h.credRepo.GetByID(host.CredentialID)
	if err != nil || cred == nil {
		return nil, nil, fmt.Errorf("跳板机 %s 未配置可用凭据", host.Name)
	}
	secret, err := h.cryptoS.Decrypt(cred.EncryptedSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("解密跳板机凭据失败")
	}
	dialCfg := sshx.DialConfig{
		Addr:     net.JoinHostPort(host.IP, strconv.Itoa(host.Port)),
		Username: cred.Username,
	}
	switch cred.Type {
	case "private_key":
		dialCfg.PrivateKey = secret
	case "password":
		dialCfg.Password = string(secret)
	default:
		dialCfg.Password = string(secret)
	}

	client, err := h.sshC.Dial(dialCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH 隧道建立失败（%s）: %w", host.Name, err)
	}
	ln, err := sshx.LocalForward(client, target)
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("本地转发端口监听失败: %w", err)
	}
	return client, ln, nil
}

// ---------------- 连通性 ----------------

// ping 探活并返回服务端版本。
func ping(ctx context.Context, db *sql.DB) (string, error) {
	if err := db.PingContext(ctx); err != nil {
		return "", err
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return "", err
	}
	return version, nil
}

// ---------------- 元数据 ----------------

// listDatabases 列出全部库（含系统库，由前端折叠展示）。
func listDatabases(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

const tablesSQL = `SELECT TABLE_NAME, TABLE_TYPE, IFNULL(ENGINE,''), IFNULL(TABLE_ROWS,0), IFNULL(TABLE_COMMENT,'')
	FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`

// listTables 列出库内表与视图。
func listTables(ctx context.Context, db *sql.DB, schema string) ([]model.MySQLTable, error) {
	rows, err := db.QueryContext(ctx, tablesSQL, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MySQLTable{}
	for rows.Next() {
		var t model.MySQLTable
		if err := rows.Scan(&t.Name, &t.Type, &t.Engine, &t.Rows, &t.Comment); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const columnsSQL = `SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, IFNULL(COLUMN_KEY,''), COLUMN_DEFAULT, IFNULL(EXTRA,''), IFNULL(COLUMN_COMMENT,'')
	FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`

// listColumns 列出表字段。COLUMN_DEFAULT 需区分「NULL」与「无默认值」，故用 sql.NullString。
func listColumns(ctx context.Context, db *sql.DB, schema, table string) ([]model.MySQLColumn, error) {
	rows, err := db.QueryContext(ctx, columnsSQL, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MySQLColumn{}
	for rows.Next() {
		var c model.MySQLColumn
		var nullable string
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &nullable, &c.Key, &def, &c.Extra, &c.Comment); err != nil {
			return nil, err
		}
		c.Nullable = strings.EqualFold(nullable, "YES")
		if def.Valid {
			v := def.String
			c.Default = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const indexesSQL = `SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME
	FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
	ORDER BY INDEX_NAME, SEQ_IN_INDEX`

// listIndexes 列出索引，同一索引的多列按顺序合并。
func listIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]model.MySQLIndex, error) {
	rows, err := db.QueryContext(ctx, indexesSQL, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MySQLIndex{}
	idx := map[string]int{}
	for rows.Next() {
		var name, col string
		var nonUnique int
		if err := rows.Scan(&name, &nonUnique, &col); err != nil {
			return nil, err
		}
		pos, ok := idx[name]
		if !ok {
			out = append(out, model.MySQLIndex{Name: name, Unique: nonUnique == 0})
			pos = len(out) - 1
			idx[name] = pos
		}
		out[pos].Columns = append(out[pos].Columns, col)
	}
	return out, rows.Err()
}

// ---------------- 表详情 ----------------

// tableStatusSQL 表概览。时间列用 DATE_FORMAT 转字符串：驱动开了 parseTime，
// 直接扫 DATETIME 会得到 time.Time，扫进 string 会报错。
const tableStatusSQL = `SELECT TABLE_NAME, IFNULL(ENGINE,''), IFNULL(TABLE_COLLATION,''),
	IFNULL(TABLE_ROWS,0), IFNULL(DATA_LENGTH,0), IFNULL(INDEX_LENGTH,0), IFNULL(TABLE_COMMENT,''),
	IFNULL(DATE_FORMAT(CREATE_TIME,'%Y-%m-%d %H:%i:%s'),''),
	IFNULL(DATE_FORMAT(UPDATE_TIME,'%Y-%m-%d %H:%i:%s'),'')
	FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`

// tableStatus 读表概览；表不存在返回 nil。
func tableStatus(ctx context.Context, db *sql.DB, schema, table string) (*model.MySQLTableStatus, error) {
	st := &model.MySQLTableStatus{}
	err := db.QueryRowContext(ctx, tableStatusSQL, schema, table).Scan(
		&st.Name, &st.Engine, &st.Collation, &st.Rows, &st.DataLength, &st.IndexLength,
		&st.Comment, &st.CreatedAt, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return st, nil
}

// quoteIdent 反引号包裹标识符（内部反引号翻倍）。
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// showCreateTable 取建表 / 建视图语句。
// 视图返回的列数与表不同（4 列），故按列动态扫描后取第 2 列。
// 取不到时把原因作为第二个返回值带回，不阻断表详情的其余内容。
func showCreateTable(ctx context.Context, db *sql.DB, schema, table string) (string, string, error) {
	rows, err := db.QueryContext(ctx, "SHOW CREATE TABLE "+quoteIdent(schema)+"."+quoteIdent(table))
	if err != nil {
		return "", err.Error(), nil
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return "", err.Error(), nil
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err.Error(), nil
		}
		return "", "未取到建表语句", nil
	}
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err.Error(), nil
	}
	if len(vals) < 2 {
		return "", "建表语句格式异常", nil
	}
	return stringifyValue(vals[1]), "", nil
}

// stringifyValue 把驱动返回的原始值转为字符串（[]byte / nil / 其他）。
func stringifyValue(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

// ---------------- SQL 分类与执行 ----------------

// SQL 类型 —— 门禁的「判断 2」。只有 DQL 与 SESSION 视为只读，其余一律需过确认。
// 字面量统一取自 model，避免门禁与「使用记录」的统计各维护一套取值。
const (
	sqlDQL     = model.SQLTypeDQL     // 纯查询
	sqlSession = model.SQLTypeSession // 会话控制（SET / USE / BEGIN …），不碰数据
	sqlDML     = model.SQLTypeDML     // 数据变更
	sqlDDL     = model.SQLTypeDDL     // 结构变更
	sqlOther   = model.SQLTypeOther   // 其余（存储过程 / 权限 / 未知），按最严处理
)

// 会话运行模式 —— 门禁的「判断 1」。连接之后由使用者在工作台自行切换。
const (
	ModeRead  = "read"
	ModeWrite = "write"
)

var (
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineCommentRe  = regexp.MustCompile(`(?m)^[ \t]*(--|#).*$`)
	wordRe         = regexp.MustCompile(`[A-Za-z_]+`)
	cteDMLRe       = regexp.MustCompile(`(?i)\b(UPDATE|DELETE|INSERT|REPLACE)\b`)
)

// keywordType 首关键词 → SQL 类型。
var keywordType = map[string]string{
	"SELECT": sqlDQL, "SHOW": sqlDQL, "DESC": sqlDQL, "DESCRIBE": sqlDQL,
	"EXPLAIN": sqlDQL, "HELP": sqlDQL, "TABLE": sqlDQL, "VALUES": sqlDQL,
	"SET": sqlSession, "USE": sqlSession, "BEGIN": sqlSession, "START": sqlSession,
	"COMMIT": sqlSession, "ROLLBACK": sqlSession, "SAVEPOINT": sqlSession,
	"RELEASE": sqlSession, "XA": sqlSession,
	"INSERT": sqlDML, "UPDATE": sqlDML, "DELETE": sqlDML, "REPLACE": sqlDML,
	"CREATE": sqlDDL, "ALTER": sqlDDL, "DROP": sqlDDL, "TRUNCATE": sqlDDL,
	"RENAME": sqlDDL, "COMMENT": sqlDDL,
}

// sqlRisk 类型风险等级：0 直接放行，越大越危险（DML 1 / DDL·OTHER 2）。
func sqlRisk(sqlType string) int {
	switch sqlType {
	case sqlDQL, sqlSession:
		return 0
	case sqlDML:
		return 1
	default:
		return 2
	}
}

// statementInfo 单条语句的分类结果。
type statementInfo struct {
	Kind string `json:"kind"`
	Type string `json:"type"`
}

// sqlAnalysis 整段输入的静态判定（可能含多条语句）。
type sqlAnalysis struct {
	Statements []statementInfo
	Kind       string // 风险最高那条的首关键词，用于展示
	Type       string // 风险最高的类型
	IsWrite    bool   // 含非只读语句
}

// RequireConfirm 是否需要过写门禁（判断 1 × 判断 2 之后的结论）。
func (a sqlAnalysis) RequireConfirm() bool { return sqlRisk(a.Type) > 0 }

// classifySQL 单条语句分类。
// WITH 需再看是否内嵌写操作（CTE 可后接 UPDATE / DELETE / INSERT）。
func classifySQL(sqlText string) (kind, sqlType string) {
	s := blockCommentRe.ReplaceAllString(sqlText, " ")
	s = lineCommentRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if s == "" {
		return "", sqlOther
	}
	kind = strings.ToUpper(wordRe.FindString(s))
	if kind == "WITH" {
		if m := strings.ToUpper(cteDMLRe.FindString(s)); m != "" {
			return m, sqlDML
		}
		return kind, sqlDQL
	}
	if t, ok := keywordType[kind]; ok {
		return kind, t
	}
	return kind, sqlOther
}

// splitStatements 把输入切成独立语句，注释一并剥除。
//
// 分号只在「非字符串、非反引号标识符、非注释」状态下才算分隔符，
// 否则 SELECT ';' 与注释里的分号会被误切。
// `--` 注释要求其后为空白（MySQL 规则），否则 a--b 里的内容会被当注释吃掉而漏判。
func splitStatements(text string) []string {
	out := []string{}
	var buf strings.Builder
	r := []rune(text)
	n := len(r)
	for i := 0; i < n; i++ {
		ch := r[i]

		isHash := ch == '#'
		isDash := ch == '-' && i+1 < n && r[i+1] == '-' &&
			(i+2 >= n || r[i+2] == ' ' || r[i+2] == '\t' || r[i+2] == '\r' || r[i+2] == '\n')
		if isHash || isDash {
			j := i
			for j < n && r[j] != '\n' {
				j++
			}
			buf.WriteByte(' ')
			i = j - 1
			continue
		}
		if ch == '/' && i+1 < n && r[i+1] == '*' {
			j := i + 2
			for j+1 < n && !(r[j] == '*' && r[j+1] == '/') {
				j++
			}
			if j+1 < n {
				j += 2
			} else {
				j = n
			}
			buf.WriteByte(' ')
			i = j - 1
			continue
		}
		// 字符串与反引号标识符：整段原样保留，内部的分号不算分隔符
		if ch == '\'' || ch == '"' || ch == '`' {
			quote := ch
			j := i + 1
			for j < n {
				c := r[j]
				if c == '\\' && quote != '`' {
					j += 2
					continue
				}
				if c == quote {
					// 单/双引号里的连写引号是转义（'' 表示一个 '）
					if quote != '`' && j+1 < n && r[j+1] == quote {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			if j > n {
				j = n
			}
			buf.WriteString(string(r[i:j]))
			i = j - 1
			continue
		}
		if ch == ';' {
			if s := strings.TrimSpace(buf.String()); s != "" {
				out = append(out, s)
			}
			buf.Reset()
			continue
		}
		buf.WriteRune(ch)
	}
	if s := strings.TrimSpace(buf.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// analyzeStatements 逐条分类，并取风险最高的一条作为整体判定。
// 这样 `SELECT 1; DROP TABLE t` 不会被首关键词 SELECT 蒙混过关。
func analyzeStatements(stmts []string) sqlAnalysis {
	an := sqlAnalysis{Statements: make([]statementInfo, 0, len(stmts))}
	for _, raw := range stmts {
		kind, sqlType := classifySQL(raw)
		// 每条语句都必须占位：Statements 与 stmts 一一对应（execStatements 按下标取用）；
		// 无首关键词的怪语句（纯数字 / 纯符号 / 纯中文）归 sqlOther，按最严风险参与判定
		an.Statements = append(an.Statements, statementInfo{Kind: kind, Type: sqlType})
		if len(an.Statements) == 1 || sqlRisk(sqlType) > sqlRisk(an.Type) {
			an.Kind, an.Type = kind, sqlType
		}
	}
	if len(an.Statements) == 0 {
		// 全是空语句 / 纯注释：保守按需确认处理
		an.Kind, an.Type = "", sqlOther
	}
	an.IsWrite = an.RequireConfirm()
	return an
}

// analyzeSQL 切分 + 分类，返回可执行语句与整体判定。
func analyzeSQL(text string) ([]string, sqlAnalysis) {
	stmts := splitStatements(text)
	return stmts, analyzeStatements(stmts)
}

// queryRows 执行只读语句并拉取结果集，超过 limit 即截断。
func queryRows(ctx context.Context, db *sql.DB, sqlText string, limit int) ([]model.MySQLResultColumn, [][]interface{}, bool, error) {
	rows, err := db.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	types, _ := rows.ColumnTypes()
	outCols := []model.MySQLResultColumn{}
	for i, name := range cols {
		t := ""
		if i < len(types) {
			t = types[i].DatabaseTypeName()
		}
		outCols = append(outCols, model.MySQLResultColumn{Name: name, Type: t})
	}

	outRows := [][]interface{}{}
	truncated := false
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		// Next() 已消费一行，此时行数已达上限说明还有更多 → 标记截断
		if len(outRows) >= limit {
			truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		row := make([]interface{}, len(cols))
		for i, v := range vals {
			row[i] = normalizeValue(v)
		}
		outRows = append(outRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return outCols, outRows, truncated, nil
}

// execStatements 逐条执行切分后的语句，聚合为一次结果。
// 只读语句产出结果集（取最后一条），写类语句累加影响行数。
func execStatements(ctx context.Context, db *sql.DB, stmts []string, an sqlAnalysis, limit int) (*model.MySQLQueryResult, error) {
	if limit <= 0 || limit > maxRowLimit {
		limit = defaultRowLimit
	}
	start := time.Now()
	res := &model.MySQLQueryResult{
		Columns:        []model.MySQLResultColumn{},
		Rows:           [][]interface{}{},
		StatementKind:  an.Kind,
		SQLType:        an.Type,
		IsWrite:        an.IsWrite,
		StatementCount: len(an.Statements),
		Statements:     []model.MySQLStatementResult{},
	}

	for i, text := range stmts {
		info := an.Statements[i]
		t0 := time.Now()
		item := model.MySQLStatementResult{Kind: info.Kind, Type: info.Type}

		if info.Type == sqlDQL {
			cols, rws, truncated, err := queryRows(ctx, db, text, limit)
			if err != nil {
				return nil, err
			}
			res.Columns, res.Rows, res.Truncated = cols, rws, truncated
		} else {
			r, err := db.ExecContext(ctx, text)
			if err != nil {
				return nil, err
			}
			// 写类语句不产出结果集，清掉上一条只读语句残留的行
			res.Columns, res.Rows, res.Truncated = []model.MySQLResultColumn{}, [][]interface{}{}, false
			item.AffectedRows, _ = r.RowsAffected()
			res.AffectedRows += item.AffectedRows
			if id, err := r.LastInsertId(); err == nil && id > 0 {
				res.LastInsertID = id
			}
		}

		item.ElapsedMs = time.Since(t0).Milliseconds()
		res.Statements = append(res.Statements, item)
	}

	res.RowCount = len(res.Rows)
	res.ElapsedMs = time.Since(start).Milliseconds()
	return res, nil
}

// normalizeValue 把驱动原始值转为 JSON 友好形式：[]byte → string，时间 → 本地格式串。
func normalizeValue(v interface{}) interface{} {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	default:
		return v
	}
}

// queryTimeout 归一化请求超时。
func queryTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultQueryTimeout
	}
	if d > maxQueryTimeout {
		return maxQueryTimeout
	}
	return d
}
