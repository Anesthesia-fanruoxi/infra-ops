package api

import "testing"

func TestExtractSelfReportedName(t *testing.T) {
	cases := []struct {
		name, out, want string
	}{
		{"无标记", "hello world\ninstall done", ""},
		{"标准标记", "xx\ninfra-ops:set-name=node-3\n", "node-3"},
		{"带前导空格", "  infra-ops:set-name=web-01.corp.local", "web-01.corp.local"},
		{"多次出现取最后", "infra-ops:set-name=a\ninfra-ops:set-name=b", "b"},
		{"带引号", `infra-ops:set-name="db-1"`, "db-1"},
		{"超长拒绝", "infra-ops:set-name=" + string(make([]byte, 100)), ""},
	}
	for _, c := range cases {
		if got := extractSelfReportedName(c.out); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
