package mysql

import (
	"errors"
	"strings"
	"testing"

	"infra-ops/common/aiopenai"
	"infra-ops/model"
)

func TestUnmarshalModelJSON(t *testing.T) {
	type row struct {
		Table string `json:"table"`
	}
	cases := []struct {
		name  string
		in    string
		want  string
		isErr bool
	}{
		{"裸数组", `[{"table":"t1"}]`, "t1", false},
		{"围栏包裹", "```json\n[{\"table\":\"t1\"}]\n```", "t1", false},
		{"围栏无语言", "```\n[{\"table\":\"t1\"}]\n```", "t1", false},
		{"前后带解释", "好的，结果如下：\n[{\"table\":\"t1\"}]\n希望有帮助", "t1", false},
		{"对象包数组", `{"list":[{"table":"t1"}]}`, "", false},
		{"空内容", "", "", true},
		{"非 JSON", "抱歉我无法完成", "", true},
		{"结构不完整", `[{"table":"t1"`, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "对象包数组" {
				var obj struct {
					List []row `json:"list"`
				}
				if err := unmarshalModelJSON(c.in, &obj); err != nil || len(obj.List) != 1 {
					t.Fatalf("对象解析失败: %v", err)
				}
				return
			}
			var out []row
			err := unmarshalModelJSON(c.in, &out)
			if c.isErr {
				if err == nil {
					t.Fatalf("期望报错，实际通过：%+v", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if out[0].Table != c.want {
				t.Errorf("table = %q, want %q", out[0].Table, c.want)
			}
		})
	}
}

func TestKeywords(t *testing.T) {
	got := keywords("查一下订单表 order_info 里最近的金额")
	for _, k := range []string{"订单", "order_info", "金额"} {
		if !contains(got, k) {
			t.Errorf("缺少关键词 %q，实际 %v", k, got)
		}
	}
	// 单字不成词、长度 < 2 的英文串不成词
	if contains(keywords("a 的"), "a") {
		t.Error("单字符不应作为关键词")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func entry(name, purpose string, notes ...string) model.MySQLAICatalogEntry {
	e := model.MySQLAICatalogEntry{SchemaName: "shop", TableName: name, Purpose: purpose}
	for _, n := range notes {
		e.Columns = append(e.Columns, model.MySQLAIColumn{Name: n, Note: n})
	}
	return e
}

func TestPickTables(t *testing.T) {
	many := []model.MySQLAICatalogEntry{
		entry("t_user", "统计用户订单金额的汇总表", "用户", "订单金额"),
		entry("t_order", "订单主表", "订单金额", "下单时间"),
		entry("zd_mark", "零散标记", "标记"),
	}
	// 未超上限：全量返回
	if got := pickTables(many, "随便", 10); len(got) != 3 {
		t.Errorf("未超上限应全量返回，实际 %d", len(got))
	}
	// 表名直中问题必须置顶：这里 t_user 的用途与字段几乎复读了整句问法，
	// 单靠用词重合它会排第一，只有「表名直中」这条规则在，zd_mark 才会胜出。
	// （名字刻意不用 t_ 前缀：前缀剥离是另一条次级规则，会盖住主规则的验证）
	got := pickTables(many, "统计用户的订单金额，顺便看下 zd_mark", 1)
	if len(got) != 1 || got[0].TableName != "zd_mark" {
		t.Fatalf("表名直中应压过用词重合，实际 %v", names(got))
	}
	// 无表名命中时按用途/字段用词相关度排序
	got = pickTables(many, "统计每个用户的订单金额", 1)
	if len(got) != 1 || got[0].TableName != "t_user" {
		t.Errorf("应以「统计用户订单金额」这句用途命中的 t_user 为先，实际 %v", names(got))
	}
}

func names(list []model.MySQLAICatalogEntry) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.TableName)
	}
	return out
}

func TestGuardGeneration(t *testing.T) {
	// —— 只读模式：只放行单条 DQL ——
	for _, s := range []string{
		"SELECT `shop`.`t_order`.`id` FROM `shop`.`t_order` LIMIT 10",
		"SHOW TABLES",
		"WITH x AS (SELECT 1 AS a) SELECT * FROM x",
		"EXPLAIN SELECT 1",
	} {
		if err := guardGeneration(s, ModeRead); err != nil {
			t.Errorf("只读模式误拦查询：%s -> %v", s, err)
		}
	}
	for _, s := range []string{
		"UPDATE `shop`.`t_order` SET `a`=1",
		"DELETE FROM `shop`.`t_order`",
		"DROP TABLE `shop`.`t_order`",
		"SELECT 1; DROP TABLE `shop`.`t_order`",
		"SELECT 1; DELETE FROM `shop`.`t_order`",
		"WITH x AS (SELECT 1) DELETE FROM `shop`.`t_order`",
		"SELECT * FROM `shop`.`t_order` FOR UPDATE",
		"SELECT * INTO OUTFILE '/tmp/x' FROM `shop`.`t_order`",
	} {
		if err := guardGeneration(s, ModeRead); err == nil {
			t.Errorf("只读模式写操作未被拦下：%s", s)
		}
	}
	// 只读模式的拒绝文案必须同时说清「为什么」与「怎么办」，
	// 否则使用者不知道要去切模式，只会以为是功能坏了。
	err := guardGeneration("DROP TABLE `shop`.`t_order`", ModeRead)
	if err == nil || !strings.Contains(err.Error(), "只读模式") || !strings.Contains(err.Error(), "可写模式") {
		t.Errorf("只读模式的拒绝文案没给出切换指引：%v", err)
	}

	// —— 可写模式：写语句与多语句都放行（真正确认在执行侧门禁）——
	for _, s := range []string{
		"SELECT 1",
		"UPDATE `shop`.`t_order` SET `a`=1 WHERE `id`=2",
		"DELETE FROM `shop`.`t_order` WHERE `id`=2",
		"ALTER TABLE `shop`.`t_order` ADD COLUMN `c` INT",
		"DROP TABLE `shop`.`t_order`",
		"CREATE TABLE `shop`.`t_x` (`id` INT); INSERT INTO `shop`.`t_x` VALUES (1)",
	} {
		if err := guardGeneration(s, ModeWrite); err != nil {
			t.Errorf("可写模式误拦写语句：%s -> %v", s, err)
		}
	}
	// 与模式无关：判成 DQL、执行时不会弹确认的「带副作用查询」两种模式都不放行
	for _, s := range []string{
		"SELECT * FROM `shop`.`t_order` FOR UPDATE",
		"SELECT * FROM `shop`.`t_order` LOCK IN SHARE MODE",
		"SELECT * INTO OUTFILE '/tmp/x' FROM `shop`.`t_order`",
	} {
		if err := guardGeneration(s, ModeWrite); err == nil {
			t.Errorf("可写模式下带副作用的查询也应拦下：%s", s)
		}
	}
	if err := guardGeneration("   ", ModeWrite); err == nil {
		t.Error("空语句应报错")
	}
}

func TestSQLSystemPrompt(t *testing.T) {
	read, write := sqlSystemPrompt(ModeRead), sqlSystemPrompt(ModeWrite)
	if !strings.Contains(read, "只读模式") {
		t.Errorf("只读提示词没有声明只读：%s", read)
	}
	if !strings.Contains(write, "可写模式") {
		t.Errorf("可写提示词没有声明可写：%s", write)
	}
	if strings.Contains(write, "绝不生成") {
		t.Errorf("可写提示词不该再禁写：%s", write)
	}
	// 分模式只换第 1 条规则，输出契约、时间换算与「不加未提条件」规则必须两套都在
	for _, p := range []string{read, write} {
		for _, must := range []string{"只输出 JSON", "反引号", "不要编造不存在的表或字段",
			"当前时间", "CURDATE", "[int 时间戳]", "毫秒", "is_delete"} {
			if !strings.Contains(p, must) {
				t.Errorf("提示词缺少共用规则 %q：%s", must, p)
			}
		}
	}
}

func TestTableFingerprint(t *testing.T) {
	tbl := model.MySQLTable{Name: "t_order", Type: "BASE TABLE", Comment: "订单主表"}
	cols := []model.MySQLColumn{{Name: "id", Type: "bigint", Comment: "主键"}}
	base := tableFingerprint(tbl, cols)
	if base != tableFingerprint(tbl, cols) {
		t.Error("指纹应当稳定")
	}
	if base == tableFingerprint(tbl, []model.MySQLColumn{{Name: "id", Type: "bigint", Comment: "订单ID"}}) {
		t.Error("字段注释变了指纹应当改变")
	}
	if base == tableFingerprint(tbl, []model.MySQLColumn{{Name: "uid", Type: "bigint", Comment: "主键"}}) {
		t.Error("字段名变了指纹应当改变")
	}
	tbl2 := tbl
	tbl2.Comment = "订单表"
	if base == tableFingerprint(tbl2, cols) {
		t.Error("表注释变了指纹应当改变")
	}
}

func TestBatchIndexes(t *testing.T) {
	briefs := make([]tableBrief, 0, 20)
	for i := 0; i < 20; i++ {
		briefs = append(briefs, tableBrief{Table: "t", Columns: []columnBrief{{Name: "a", Type: "int", Comment: "列"}}})
	}
	all := make([]int, 0, 20)
	for i := range briefs {
		all = append(all, i)
	}
	got := batchIndexes(briefs, all)
	if len(got) != 2 {
		t.Fatalf("20 张小表应按 15/批 切成 2 批，实际 %d", len(got))
	}
	if len(got[0]) != catalogBatchTables || len(got[1]) != 20-catalogBatchTables {
		t.Errorf("批次大小不符：%d / %d", len(got[0]), len(got[1]))
	}
	if b := batchIndexes(briefs, nil); len(b) != 0 {
		t.Errorf("无待生成表时不应发请求，实际 %d 批", len(b))
	}

	// 超长表单批放不下时单独成批
	huge := []tableBrief{
		{Table: "big", Columns: []columnBrief{{Name: "c", Type: "text", Comment: strings.Repeat("长", catalogBatchChars)}}},
		{Table: "small", Columns: []columnBrief{{Name: "c", Type: "int", Comment: "短"}}},
	}
	b := batchIndexes(huge, []int{0, 1})
	if len(b) != 2 {
		t.Errorf("超长表应单独成批，实际 %v", b)
	}
}

func TestBuildCatalogEntry(t *testing.T) {
	brief := tableBrief{
		Table: "t_order",
		Columns: []columnBrief{
			{Name: "id", Type: "bigint", Comment: "主键ID"},
			{Name: "amount", Type: "decimal(10,2)", Comment: ""},
			{Name: "create_time", Type: "datetime", Comment: "创建时间"},
			{Name: "uid", Type: "bigint", Comment: "用户ID"},
			{Name: "created_at", Type: "bigint", Comment: "创建时间戳", Unit: "ms"},
		},
	}

	// notes 数量对齐：按下标回填；字段以库里为准，模型编造的字段无从进入
	aligned := catalogReply{Table: "t_order", Purpose: "订单主表",
		Notes: []string{"主键", "订单金额", "创建时间", "用户ID", "创建时间戳"}}
	e := buildCatalogEntry("shop", brief, "fp1", aligned)
	if e.TableName != "t_order" || e.SchemaName != "shop" || e.Fingerprint != "fp1" {
		t.Fatalf("基本信息不对：%+v", e)
	}
	if len(e.Columns) != 5 {
		t.Fatalf("字段数应严格等于库里的 5 个，实际 %d（%+v）", len(e.Columns), e.Columns)
	}
	if e.Columns[1].Note != "订单金额" || e.Columns[3].Note != "用户ID" || e.Columns[4].Note != "创建时间戳" {
		t.Errorf("模型说明未按序回填：%+v", e.Columns)
	}
	// 真实列类型与时间戳单位随目录标记（生成 SQL 时做时间自适应）
	if e.Columns[2].Type != "datetime" || e.Columns[0].Type != "bigint" {
		t.Errorf("字段类型未随目录标记：%+v", e.Columns)
	}
	if e.Columns[4].Unit != "ms" {
		t.Errorf("时间戳单位未随目录标记：%+v", e.Columns)
	}

	// notes 数量不符（漏答 / 多答）：整表退回原注释，避免错位把 A 的说明挂到 B 上
	bad := catalogReply{Table: "t_order", Purpose: "订单主表", Notes: []string{"主键", "订单金额"}}
	e2 := buildCatalogEntry("shop", brief, "fp1", bad)
	if e2.Columns[1].Note != "" || e2.Columns[2].Note != "创建时间" {
		t.Errorf("数量不符时应整表退回原注释：%+v", e2.Columns)
	}
}

func TestCatalogContextTypeHints(t *testing.T) {
	e := model.MySQLAICatalogEntry{
		SchemaName: "ysh_app", TableName: "app_channel_api_record", Purpose: "API 渠道采量记录",
		Columns: []model.MySQLAIColumn{
			{Name: "id", Type: "bigint unsigned", Note: "主键"},
			{Name: "create_time", Type: "datetime", Note: "创建时间"},
			{Name: "pay_at", Type: "int(11)", Note: ""},
			{Name: "stat_date", Type: "int unsigned", Note: "统计日期"},
			{Name: "is_delete", Type: "tinyint(1)", Note: "逻辑删除"},
			{Name: "candidate_id", Type: "int", Note: "候选人"},
			{Name: "created_at", Type: "bigint(20)", Unit: "ms", Note: "创建时间戳"},
			{Name: "updated_at", Type: "int", Unit: "s", Note: "更新时间"},
		},
	}
	got := catalogContext([]model.MySQLAICatalogEntry{e})
	// 日期时间类型原样标，模型据此选字符串字面量
	if !strings.Contains(got, "create_time [datetime]") {
		t.Errorf("datetime 列未标类型：\n%s", got)
	}
	// 整数时间戳：名字含 time / 说明含日期都要认出来
	if !strings.Contains(got, "pay_at [int 时间戳]") || !strings.Contains(got, "stat_date [int 时间戳]") {
		t.Errorf("整数时间列未标时间戳：\n%s", got)
	}
	// 有探测单位的时间戳列标出秒 / 毫秒，生成 SQL 时按单位换算
	if !strings.Contains(got, "created_at [int 时间戳·毫秒]") || !strings.Contains(got, "updated_at [int 时间戳·秒]") {
		t.Errorf("时间戳单位未标出：\n%s", got)
	}
	// 与时间无关的列不占上下文；int 名字含 date 子串也不误标（candidate_id）
	if strings.Contains(got, "id [") || strings.Contains(got, "is_delete [") || strings.Contains(got, "candidate_id [") {
		t.Errorf("普通列不该有类型标记：\n%s", got)
	}

	// 旧目录没有类型标记：保持原样输出，不误标
	old := entry("t_order", "订单主表", "create_time")
	if strings.Contains(catalogContext([]model.MySQLAICatalogEntry{old}), "create_time [") {
		t.Errorf("无类型标记时不应猜测类型")
	}
}

func TestTimestampUnit(t *testing.T) {
	cases := []struct {
		v    int64
		want string
	}{
		{1731024000, "s"},     // 2024-11 秒级（10 位）
		{1731024000000, "ms"}, // 毫秒级（13 位）
		{100000000000, "ms"},  // 1e11 起必为毫秒（毫秒到 1973 年，秒级要 5138 年才够 12 位）
		{99999999999, "s"},    // 边界之下按秒（11 位）
		{100000000, "s"},      // 1e8 起为秒（1973 年）
		{99999999, ""},        // 小值判不了（可能是时长等非时间戳）
		{0, ""},
		{-1, ""},
	}
	for _, c := range cases {
		if got := timestampUnit(c.v); got != c.want {
			t.Errorf("timestampUnit(%d) = %q, want %q", c.v, got, c.want)
		}
	}
}

// ---------------- 使用记录装配 ----------------

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"abc", 5, "abc"},
		{"abcde", 5, "abcde"},
		{"abcdef", 3, "abc…"},
		{"中文测试", 2, "中文…"},
	}
	for _, c := range cases {
		if got := truncateRunes(c.in, c.n); got != c.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestAccumulateUsage(t *testing.T) {
	var u model.MySQLAIUsage
	accumulateUsage(&u, &aiopenai.Result{Usage: aiopenai.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, FromAPI: true}})
	accumulateUsage(&u, &aiopenai.Result{Usage: aiopenai.Usage{PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23, FromAPI: true}})
	if u.Calls != 2 || u.PromptTokens != 30 || u.CompletionTokens != 5 || u.TotalTokens != 35 {
		t.Errorf("多批用量未正确累加：%+v", u)
	}
	if u.Source != model.TokenSourceAPI {
		t.Errorf("全部来自接口时应标 api，实际 %q", u.Source)
	}

	// 只要有一批是估算的，整体就按估算标注（不假称精确）
	accumulateUsage(&u, &aiopenai.Result{Usage: aiopenai.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}})
	if u.TotalTokens != 37 || u.Source != model.TokenSourceEstimated {
		t.Errorf("混入估算值后应整体标 estimated：%+v", u)
	}

	// 调用失败时没有结果可用，不能改动任何累计值
	before := u
	accumulateUsage(&u, nil)
	if u != before {
		t.Errorf("nil 结果不应改变累计值：%+v -> %+v", before, u)
	}
}

func TestAITraceAssembly(t *testing.T) {
	h := &Handler{}
	tr := h.beginTrace(aiTraceParams{
		Kind: model.AILogKindSQL, Conn: &model.MySQLConn{ID: 7, Name: "cs"},
		Schema: "demo", Mode: "read", Model: "mimo", BaseURL: "https://x/v1",
		TableCount: 3, Question: "查订单",
	})
	if tr.row.ConnID != 7 || tr.row.ConnName != "cs" || tr.row.Kind != model.AILogKindSQL {
		t.Errorf("连接信息未写入记录：%+v", tr.row)
	}
	if tr.row.Status != model.LogStatusOK || tr.row.Model != "mimo" {
		t.Errorf("初始状态不符：%+v", tr.row)
	}

	// 接口回了 usage：原样记下，来源标 api，并采用服务端回显的模型名
	tr.fill(&aiopenai.Result{Content: "{}", Model: "mimo-0715",
		Usage: aiopenai.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, FromAPI: true}})
	if tr.row.Model != "mimo-0715" || tr.row.TokenSource != model.TokenSourceAPI || tr.row.TotalTokens != 15 {
		t.Errorf("用量未记录：%+v", tr.row)
	}
	if u := tr.usage(1); u.Calls != 1 || u.TotalTokens != 15 || u.Source != model.TokenSourceAPI {
		t.Errorf("下发给前端的用量不符：%+v", u)
	}

	// 标失败只改状态与原因，已记下的用量必须留着
	tr.fail(errors.New("AI 服务返回 500"))
	if tr.status != model.LogStatusError || tr.row.TotalTokens != 15 {
		t.Errorf("失败标记破坏了已记内容：status=%s row=%+v", tr.status, tr.row)
	}
	// 原因只记第一次的（首个错误更接近根因）
	tr.fail(errors.New("解析模型返回失败"))
	if tr.reason != "AI 服务返回 500" {
		t.Errorf("失败原因被覆盖：%q", tr.reason)
	}

	// 接口没回 usage 时必须标 estimated
	tr2 := h.beginTrace(aiTraceParams{Kind: model.AILogKindCatalog})
	tr2.fill(&aiopenai.Result{Content: "x", Usage: aiopenai.Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4}})
	if tr2.row.TokenSource != model.TokenSourceEstimated {
		t.Errorf("接口没回 usage 时应标 estimated，实际 %q", tr2.row.TokenSource)
	}
}

// ---------------- 跨库上下文与全库目录 ----------------

func TestMergeCatalogEntries(t *testing.T) {
	current := []model.MySQLAICatalogEntry{entry("t_order", "订单主表")}
	all := []model.MySQLAICatalogEntry{
		{SchemaName: "shop", TableName: "t_order", Purpose: "订单主表"},
		{SchemaName: "shop", TableName: "t_user", Purpose: "用户表"},
		{SchemaName: "erp", TableName: "t_dept", Purpose: "部门表"},
		{SchemaName: "erp", TableName: "t_staff", Purpose: "员工表"},
	}
	got := mergeCatalogEntries(current, all, "shop")
	// 期望：当前库条目在前（含 current 里多出的表），其余库按原次序补足，无重复
	var seq []string
	for _, e := range got {
		seq = append(seq, e.SchemaName+"."+e.TableName)
	}
	want := []string{"shop.t_order", "shop.t_user", "erp.t_dept", "erp.t_staff"}
	if strings.Join(seq, ",") != strings.Join(want, ",") {
		t.Fatalf("合并次序不符：%v", seq)
	}
	// 目录为空时返回空，由调用方判定报错
	if got := mergeCatalogEntries(nil, nil, "shop"); len(got) != 0 {
		t.Errorf("空目录应返回空切片，实际 %d", len(got))
	}
}

func TestIsSystemSchema(t *testing.T) {
	for _, s := range []string{"information_schema", "PERFORMANCE_SCHEMA", "mysql", "Sys", " sys "} {
		if !isSystemSchema(s) {
			t.Errorf("%q 应判为系统库", s)
		}
	}
	for _, s := range []string{"shop", "erp", "mydb", "mysql_audit"} {
		if isSystemSchema(s) {
			t.Errorf("%q 不应判为系统库", s)
		}
	}
}

func TestNormalizePickedSchemas(t *testing.T) {
	// 弹框勾选：去空白、去重、剔除系统库，保持勾选顺序
	got := normalizePickedSchemas([]string{" shop ", "shop", "mysql", "", "erp", "  sys"})
	want := []string{"shop", "erp"}
	if len(got) != len(want) {
		t.Fatalf("清洗结果不符：%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个库不符：%v", i, got)
		}
	}
	if got := normalizePickedSchemas(nil); len(got) != 0 {
		t.Errorf("nil 应返回空切片，实际 %v", got)
	}
	// 全是系统库时清空 → 调用方据此报错，而不是拿系统库去生成
	if got := normalizePickedSchemas([]string{"mysql", "sys"}); len(got) != 0 {
		t.Errorf("全系统库应清空，实际 %v", got)
	}
}

func TestAddUsage(t *testing.T) {
	total := model.MySQLAIUsage{Calls: 1, PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
	addUsage(&total, model.MySQLAIUsage{Calls: 2, PromptTokens: 20, CompletionTokens: 3, TotalTokens: 23, Source: "api"})
	if total.Calls != 3 || total.PromptTokens != 30 || total.TotalTokens != 35 || total.Source != "" {
		t.Errorf("多库用量累加不符：%+v", total)
	}
	// 任一库是估算的，全库总量就按估算标注
	addUsage(&total, model.MySQLAIUsage{Calls: 1, TotalTokens: 4, Source: "estimated"})
	if total.Source != "estimated" {
		t.Errorf("混入估算后应整体标 estimated：%+v", total)
	}
}
