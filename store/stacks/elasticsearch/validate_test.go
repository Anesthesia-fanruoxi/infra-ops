package elasticsearch

import (
	"strconv"
	"strings"
	"testing"
)

// validate_test.go：冷热温角色组合校验用例（原 api/stack/stack_cwh_validate_test.go 整体迁入，
// 用例与断言逐条保留）。
func TestValidateCWHRoles(t *testing.T) {
	auth := func(hosts map[string]map[string]string) (string, error) { return validateCWHRoles(hosts) }
	wr := func(roles ...string) map[string]string {
		return map[string]string{"roles": strings.Join(roles, ",")}
	}
	hosts := func(m map[int64]string) map[string]map[string]string {
		out := map[string]map[string]string{}
		for id, roles := range m {
			out[strconv.FormatInt(id, 10)] = wr(roles)
		}
		return out
	}

	cases := []struct {
		name    string
		hosts   map[string]map[string]string
		wantErr string // 期望错误子串；空表示成功
	}{
		{
			name:  "全角色合法 master 3奇数",
			hosts: hosts(map[int64]string{1: "master", 2: "master", 3: "master", 4: "coordinator", 5: "coordinator", 6: "data_hot", 7: "data_warm", 8: "data_cold"}),
		},
		{
			name:    "master 与数据层互斥（单机混合）",
			hosts:   hosts(map[int64]string{1: "master,data_hot", 2: "coordinator"}),
			wantErr: "master 不能与",
		},
		{
			name:    "缺 master 候选",
			hosts:   hosts(map[int64]string{1: "coordinator", 2: "coordinator", 3: "data_hot"}),
			wantErr: "至少需要 1 台 master",
		},
		{
			name:    "协调与数据层共存拒绝",
			hosts:   hosts(map[int64]string{1: "master", 2: "coordinator,data_hot"}),
			wantErr: "纯协调",
		},
		{
			name:    "角色重复拒绝",
			hosts:   hosts(map[int64]string{1: "master", 2: "data_hot,data_hot"}),
			wantErr: "重复选择",
		},
		{
			name:    "未知角色拒绝",
			hosts:   hosts(map[int64]string{1: "master", 2: "data_hot", 3: "funny_role"}),
			wantErr: "未知角色",
		},
		{
			name:    "合成单主机 master 混数据",
			hosts:   hosts(map[int64]string{1: "master,data_hot"}),
			wantErr: "master 不能与",
		},
		{
			name:  "master 4 台偶数给出提示",
			hosts: hosts(map[int64]string{1: "master", 2: "master", 3: "master", 4: "master", 5: "coordinator"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := auth(tc.hosts)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("期望成功却返回错误: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望错误包含 %q 却成功", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("错误文案不符: 期望包含 %q，实际 %q", tc.wantErr, err.Error())
			}
		})
	}
}
