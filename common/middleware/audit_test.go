package middleware

import "testing"

func TestParseResult(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		code    int
		message string
	}{
		{"同步成功", `{"code":0,"message":"ok","data":null}`, 0, "ok"},
		{"同步业务失败", `{"code":400,"message":"参数错误","data":null}`, 400, "参数错误"},
		{"SSE 成功（final 帧）",
			"event: delta\ndata: {\"field\":\"sql\",\"text\":\"SE\"}\n\nevent: final\ndata: {\"sql\":\"SELECT 1\"}\n\n", 0, "ok"},
		{"SSE 失败（error 帧）",
			"event: delta\ndata: {\"field\":\"sql\",\"text\":\"x\"}\n\nevent: error\ndata: {\"message\":\"AI 服务报错：rate limited\"}\n\n", 500, "AI 服务报错：rate limited"},
		{"SSE 截断无终帧", "event: delta\ndata: {\"field\":\"sql\",\"text\":\"x\"}\n\n", 0, ""},
		{"非 JSON 非 SSE", "hello world", 0, ""},
		{"空体", "", 0, ""},
	}
	for _, c := range cases {
		code, message := parseResult([]byte(c.body))
		if code != c.code || message != c.message {
			t.Errorf("%s: got (%d,%q)，期望 (%d,%q)", c.name, code, message, c.code, c.message)
		}
	}
}
