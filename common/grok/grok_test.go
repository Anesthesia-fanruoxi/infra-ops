package grok

import (
	"strings"
	"testing"
)

// fieldMap 把解析结果拍平成 map，断言更方便。
func fieldMap(m *Match) map[string]string {
	out := map[string]string{}
	if m == nil {
		return out
	}
	for _, f := range m.Fields {
		out[f.Name] = f.Value
	}
	return out
}

func mustParse(t *testing.T, patterns []string, defs map[string]string, text string) *Match {
	t.Helper()
	m, err := Compile(patterns, defs)
	if err != nil {
		t.Fatalf("编译失败: %v", err)
	}
	return m.Parse(text)
}

// TestCommonLogFormats 三种最常见日志形态各跑一遍，锁住内置 pattern 库的可用性。
func TestCommonLogFormats(t *testing.T) {
	// nginx combined
	line := `127.0.0.1 - - [10/Oct/2026:14:20:31 +0800] "GET /api/v1/orders?id=42 HTTP/1.1" 200 1234 "-" "curl/8.4.0"`
	m := mustParse(t, []string{`%{COMBINEDAPACHELOG}`}, nil, line)
	if m == nil || m.Partial {
		t.Fatalf("nginx 日志应整行命中，得到 %+v", m)
	}
	f := fieldMap(m)
	for k, want := range map[string]string{
		"clientip": "127.0.0.1", "verb": "GET", "request": "/api/v1/orders?id=42",
		"httpversion": "1.1", "response": "200", "bytes": "1234",
		"agent": `"curl/8.4.0"`, "timestamp": "10/Oct/2026:14:20:31 +0800",
	} {
		if f[k] != want {
			t.Errorf("字段 %s = %q，期望 %q", k, f[k], want)
		}
	}

	// Java 应用日志（含 ISO8601 毫秒与方括号上下文）
	java := `2026-10-10 14:20:31.123 ERROR [order-service,trace=9f2c] com.example.OrderController - create order failed: timeout`
	m2 := mustParse(t, []string{
		`%{TIMESTAMP_ISO8601:ts} %{LOGLEVEL:level} \[%{DATA:ctx}\] %{JAVACLASS:logger} - %{GREEDYDATA:message}`,
	}, nil, java)
	f2 := fieldMap(m2)
	if f2["ts"] != "2026-10-10 14:20:31.123" || f2["level"] != "ERROR" || f2["logger"] != "com.example.OrderController" {
		t.Errorf("Java 日志解析结果不符: %+v", f2)
	}
	if f2["message"] != "create order failed: timeout" {
		t.Errorf("message 取错: %q", f2["message"])
	}

	// syslog（内置 pattern 里带命名子组的复合定义）
	sys := `<134>Oct 10 14:20:31 web-01 nginx: upstream timed out`
	m3 := mustParse(t, []string{`%{SYSLOGBASE} %{GREEDYDATA:message}`}, nil, sys)
	f3 := fieldMap(m3)
	if f3["timestamp"] != "Oct 10 14:20:31" || f3["logsource"] != "web-01" || f3["program"] != "nginx" {
		t.Errorf("syslog 解析结果不符: %+v", f3)
	}
}

// TestPartialMatch 整行不中但局部命中时必须标 Partial —— 这是排错的核心信号。
func TestPartialMatch(t *testing.T) {
	m := mustParse(t, []string{`%{WORD:first}`}, nil, "hello world")
	if m == nil || !m.Partial {
		t.Fatalf("应返回部分命中，得到 %+v", m)
	}
	if got := fieldMap(m)["first"]; got != "hello" {
		t.Errorf("部分命中字段 = %q，期望 hello", got)
	}
	// 完全不沾边 → nil
	if got := mustParse(t, []string{`%{IP:ip}`}, nil, "nothing here"); got != nil {
		t.Errorf("毫无匹配时应返回 nil，得到 %+v", got)
	}
}

// TestMultiPatternOrder 多 pattern 按顺序取首个整行命中。
func TestMultiPatternOrder(t *testing.T) {
	m := mustParse(t, []string{`%{IP:ip}`, `%{WORD:word}`}, nil, "abc")
	if m == nil || m.PatternIdx != 1 || m.Partial {
		t.Fatalf("应命中第二条 pattern，得到 %+v", m)
	}
}

// TestTypeSuffix 类型后缀做数值规范化；转换失败不谎称类型。
func TestTypeSuffix(t *testing.T) {
	m := mustParse(t, []string{`%{NUMBER:bytes:int} %{NUMBER:rate:float} %{WORD:tag:int}`}, nil, "1234 0.250 abc")
	f := map[string]Field{}
	for _, x := range m.Fields {
		f[x.Name] = x
	}
	if f["bytes"].Value != "1234" || f["bytes"].Type != "int" {
		t.Errorf("bytes 应为 int 1234: %+v", f["bytes"])
	}
	if f["rate"].Value != "0.25" || f["rate"].Type != "float" {
		t.Errorf("rate 应为 float 0.25: %+v", f["rate"])
	}
	if f["tag"].Type != "" || f["tag"].Value != "abc" {
		t.Errorf("非数值不该标类型: %+v", f["tag"])
	}
}

// TestDuplicateFieldLastWins 同名字段后者覆盖、位置保持首次出现。
func TestDuplicateFieldLastWins(t *testing.T) {
	m := mustParse(t, []string{`%{WORD:a} %{WORD:b} %{WORD:a}`}, nil, "x y z")
	if len(m.Fields) != 2 {
		t.Fatalf("同名字段应合并为一条，得到 %d 条: %+v", len(m.Fields), m.Fields)
	}
	if m.Fields[0].Name != "a" || m.Fields[0].Value != "z" {
		t.Errorf("a 应被后者覆盖且保持首位: %+v", m.Fields)
	}
}

// TestCustomDefinitions 自定义定义优先于内置同名项。
func TestCustomDefinitions(t *testing.T) {
	defs := map[string]string{"MYID": `[A-Z]{2}-\d{4}`, "WORD": `[a-z]+`}
	m := mustParse(t, []string{`%{MYID:id} %{WORD:w}`}, defs, "AB-1234 abc")
	f := fieldMap(m)
	if f["id"] != "AB-1234" || f["w"] != "abc" {
		t.Errorf("自定义定义未生效: %+v", f)
	}
}

// TestCompileErrors 未定义 / 循环引用 / RE2 不支持的语法都必须编译期报错且信息可读。
func TestCompileErrors(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		defs     map[string]string
		want     string
	}{
		{"未定义", []string{`%{NOPE:x}`}, nil, "未定义的 pattern"},
		{"循环引用", []string{`%{A:x}`}, map[string]string{"A": `%{B}`, "B": `%{A}`}, "循环引用"},
		{"后顾不支持", []string{`(?<=x)y`}, nil, "RE2"},
		{"空 pattern", []string{"  "}, nil, "至少填写一条"},
	}
	for _, tc := range cases {
		_, err := Compile(tc.patterns, tc.defs)
		if err == nil {
			t.Errorf("%s：应报错", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s：错误信息 %q 未包含 %q", tc.name, err.Error(), tc.want)
		}
	}
}

// TestCacheSameResult 缓存不许改变语义（同 key 多次取到同一 Matcher）。
func TestCacheSameResult(t *testing.T) {
	p := []string{`%{WORD:a}`}
	m1, err1 := Cached(p, nil)
	m2, err2 := Cached(p, nil)
	if err1 != nil || err2 != nil || m1 != m2 {
		t.Fatalf("缓存未命中或结果不一致: %v %v %p %p", err1, err2, m1, m2)
	}
	if got := fieldMap(m1.Parse("ok"))["a"]; got != "ok" {
		t.Errorf("缓存结果解析异常: %q", got)
	}
}

// TestGatewayLogFormat 线上真实网关日志（nginx 自定义 log_format 输出，由用户样例还原）：
// 竖线、方括号、` - ` 与「紧贴的下一个方括号」（[0.000,-][Content-Type: -]，中间没有空格）
// 都要逐字符对上。这条 fixture 是从一个真实缺陷里长出来的——模型按格式定义里的变量顺序
// 在 method 与 uri 之间凭空补了一个 ` - `，结果整行不匹配，而引擎本身完全正确。
func TestGatewayLogFormat(t *testing.T) {
	line := `[2026-10-13T14:36:05+08:00,1791873365532] [-] ` +
		`[GET /static/js/chunks/goToNextError.SyntaxError.js.map - 184 200] ` +
		`[3781,3519] [0.000,-][Content-Type: -] [Gw-Sign: -]`
	pattern := `\[%{TIMESTAMP_ISO8601:ts},%{POSINT:timestamp}\] \[%{DATA:client_ip}\] ` +
		`\[%{WORD:method} %{DATA:uri} - %{POSINT:request_length} %{POSINT:status}\] ` +
		`\[%{POSINT:bytes_sent},%{POSINT:body_bytes_sent}\] ` +
		`\[%{NUMBER:request_time},%{DATA:upstream_response_time}\]` +
		`\[Content-Type: %{DATA:content_type}\] \[Gw-Sign: %{DATA:gw_sign}\]`

	m := mustParse(t, []string{pattern}, nil, line)
	if m == nil || m.Partial {
		t.Fatalf("网关日志应整行命中，得到 %+v", m)
	}
	f := fieldMap(m)
	for k, want := range map[string]string{
		"ts": "2026-10-13T14:36:05+08:00", "timestamp": "1791873365532", "client_ip": "-",
		"method": "GET", "uri": "/static/js/chunks/goToNextError.SyntaxError.js.map",
		"request_length": "184", "status": "200", "bytes_sent": "3781", "body_bytes_sent": "3519",
		"request_time": "0.000", "upstream_response_time": "-", "content_type": "-", "gw_sign": "-",
	} {
		if f[k] != want {
			t.Errorf("字段 %s = %q，期望 %q", k, f[k], want)
		}
	}

	// 同一行的「错误 pattern」（method 与 uri 之间多了一个样例里没有的 ` - `）必须不命中：
	// 生成侧的本地硬校验就是靠这个事实拦下模型自造的分布符。
	bad := `\[%{TIMESTAMP_ISO8601:ts},%{POSINT:timestamp}\] \[%{DATA:client_ip}\] ` +
		`\[%{DATA:method} - %{DATA:uri} - %{POSINT:request_length} %{POSINT:status}\] ` +
		`\[%{POSINT:bytes_sent},%{POSINT:body_bytes_sent}\]`
	if got := mustParse(t, []string{bad}, nil, line); got != nil {
		t.Errorf("自造分隔符的 pattern 不该命中（连部分命中都不该有），得到 %+v", got)
	}
}

// TestQuotedString RE2 版 QUOTEDSTRING 必须能吃转义引号（Logstash 原版用原子组，这里改写过）。
func TestQuotedString(t *testing.T) {
	m := mustParse(t, []string{`%{QS:s}`}, nil, `"he said \"hi\""`)
	if m == nil || m.Partial {
		t.Fatalf("转义引号应整行命中: %+v", m)
	}
	if got := fieldMap(m)["s"]; got != `"he said \"hi\""` {
		t.Errorf("引号串取值不符: %q", got)
	}
}
