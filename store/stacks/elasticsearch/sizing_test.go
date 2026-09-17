package elasticsearch

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// sizing_test.go：规格档位预设表的自检。
//
// 这里断言的不是「某个具体数字」，而是**官方硬约束与档位设计不变量**——
// 数字将来按实测调整时，只要仍然满足这些约束，测试就不会拦；
// 一旦有人写出「堆 > 宿主内存 50%」或「堆 > 31G」这类会真出事的值，立刻失败。

// esSizingRoleKeys 部署期会取堆的全部角色（与 validateCWHRoles 白名单同源）。
// coordinator 在规格表里与 master 合并为一行（roleHeaps 复用），但堆取值必须可用。
var esSizingRoleKeys = []string{"master", "coordinator", "data_hot", "data_warm", "data_cold"}

// esSizingRowKeys 规格表必须出现的行（master 与协调已合并为一行）。
var esSizingRowKeys = []string{"master", "data_hot", "data_warm", "data_cold"}

func TestSizingProfilesComplete(t *testing.T) {
	if len(esSizingProfiles) != 3 {
		t.Fatalf("应有 3 档规格，实际 %d", len(esSizingProfiles))
	}
	for _, k := range esSizingKeys {
		p, ok := esSizingProfiles[k]
		if !ok {
			t.Fatalf("档位 %s 未定义", k)
		}
		if p.Key != k || p.Label == "" || p.Summary == "" || p.Fit == "" || p.Note == "" {
			t.Fatalf("档位 %s 元信息不完整: %+v", k, p)
		}
		// 单 hot 节点性能基准必须一并给出（缺一即为文档不完整）
		for name, v := range map[string]string{
			"HotDailyGB": p.HotDailyGB, "HotWriteMBps": p.HotWriteMBps, "QueryQPS": p.QueryQPS,
		} {
			if strings.TrimSpace(v) == "" {
				t.Fatalf("档位 %s 缺 %s", k, name)
			}
		}
		got := map[string]bool{}
		for _, r := range p.Roles {
			got[r.Role] = true
			// master 行是合并行（展示名自定义）；数据角色展示名与 cwhRoleLabel 同一词表
			wantLabel := cwhRoleLabel(r.Role)
			if r.Role == "master" {
				wantLabel = "master / 协调"
			}
			if r.Label != wantLabel {
				t.Fatalf("档位 %s 角色 %s 展示名 %q，期望 %q", k, r.Role, r.Label, wantLabel)
			}
			if r.HeapGB <= 0 || r.MemGB <= 0 || strings.TrimSpace(r.Ref) == "" {
				t.Fatalf("档位 %s 角色 %s 规格不完整: %+v", k, r.Role, r)
			}
		}
		for _, rk := range esSizingRowKeys {
			if !got[rk] {
				t.Fatalf("档位 %s 缺角色行 %s", k, rk)
			}
		}
	}
}

// sizingMemViolation 内存口径违规检查：返回空串＝合规，否则返回原因。
// 抽成函数是为了让它同时服务于「正向断言当前表」与「反向验证历史错误值」两个测试。
func sizingMemViolation(r esSizingRole) string {
	if r.HeapGB > 31 {
		return fmt.Sprintf("角色 %s 堆 %dG 越过 31G compressed oops 上限", r.Role, r.HeapGB)
	}
	if r.Role == "master" { // master 行是合并行，内存口径按「不存分片」给
		if r.MemGB < r.HeapGB+4 {
			return fmt.Sprintf("角色 %s（不存分片）内存 %dG 不足以覆盖堆 %dG + 4G 系统开销", r.Role, r.MemGB, r.HeapGB)
		}
		if r.MemGB > r.HeapGB+12 {
			return fmt.Sprintf("角色 %s（不存分片）内存 %dG 相对堆 %dG 虚胖（无 Lucene 页缓存需求，不该按 2×堆 给）",
				r.Role, r.MemGB, r.HeapGB)
		}
		return ""
	}
	if r.MemGB < r.HeapGB*2 {
		return fmt.Sprintf("角色 %s 内存 %dG 小于堆 %dG 的 2 倍（官方建议堆 ≤ 宿主内存 50%%，另一半留给 Lucene 页缓存）",
			r.Role, r.MemGB, r.HeapGB)
	}
	if r.MemGB > r.HeapGB*2+8 {
		return fmt.Sprintf("角色 %s 内存 %dG 相对堆 %dG 明显过配（超过 2 倍堆 + 8G）", r.Role, r.MemGB, r.HeapGB)
	}
	return ""
}

// TestSizingHeapRespectsOfficialLimits 内存口径按角色分两类，且堆不越 31G。
//
//   - 数据节点（hot/warm/cold）：ES 官方 heap-size 口径是「堆 ≤ 宿主内存 50%」，
//     另一半留给 Lucene 页缓存 ⇒ 内存至少 2 × 堆；同时不允许再往上虚胖（≤ 2×堆 + 8G）。
//   - master / 纯协调：不存分片、无页缓存需求 ⇒ 内存 = 堆 + 4G 起的系统/堆外开销即可，
//     给到 2×堆 就是虚胖（曾把满载档纯协调写成 16g 堆配 64G 内存，用户一眼看出不对）。
func TestSizingHeapRespectsOfficialLimits(t *testing.T) {
	for _, k := range esSizingKeys {
		for _, r := range esSizingProfiles[k].Roles {
			if msg := sizingMemViolation(r); msg != "" {
				t.Fatalf("档位 %s 违规：%s", k, msg)
			}
		}
	}
}

// TestSizingMemRuleReverseCheck 反向验证：上面的判据必须真能拦下历史虚胖值。
// 剥掉判据（或把 master/协调 又改回「2×堆」口径）时，本用例会如期 FAIL。
func TestSizingMemRuleReverseCheck(t *testing.T) {
	bad := []esSizingRole{
		{Role: "master", MemGB: 64, HeapGB: 16},   // 合并行若按 2×堆 给内存（历史虚胖值）
		{Role: "master", MemGB: 32, HeapGB: 8},    // 同上：8g 堆配 32G（4×）
		{Role: "data_hot", MemGB: 64, HeapGB: 16}, // 标准 hot 旧值：16g 堆配 64G（4×，过配）
		{Role: "data_warm", MemGB: 32, HeapGB: 8}, // 标准 warm 旧值同上
		{Role: "data_hot", MemGB: 24, HeapGB: 16}, // 内存不足堆的 2 倍（页缓存被挤）
		{Role: "master", MemGB: 10, HeapGB: 8},    // 不存分片但内存未留够 4G 开销
		{Role: "data_hot", MemGB: 64, HeapGB: 33}, // 堆越过 31G
	}
	for _, r := range bad {
		if msg := sizingMemViolation(r); msg == "" {
			t.Fatalf("反向验证失败：%+v 应被判违规，判据却放行", r)
		}
	}
	// 合规样例不能被误伤
	good := []esSizingRole{
		{Role: "master", MemGB: 20, HeapGB: 16},
		{Role: "master", MemGB: 12, HeapGB: 8},
		{Role: "data_hot", MemGB: 64, HeapGB: 31},
		{Role: "data_hot", MemGB: 32, HeapGB: 16},
		{Role: "data_cold", MemGB: 6, HeapGB: 2},
	}
	for _, r := range good {
		if msg := sizingMemViolation(r); msg != "" {
			t.Fatalf("合规样例被误判：%+v → %s", r, msg)
		}
	}
}

// TestSizingMemRuleAnnotations 内存口径标注必须补齐且与角色分类一致（界面直接展示这一行）。
func TestSizingMemRuleAnnotations(t *testing.T) {
	for _, p := range esSizingTable() {
		if p.MemNote == "" {
			t.Fatalf("档位 %s 缺内存口径整句说明", p.Key)
		}
		for _, r := range p.Roles {
			want := esMemRuleData
			if r.Role == "master" {
				want = esMemRuleStateless
			}
			if r.MemRule != want {
				t.Fatalf("档位 %s 角色 %s 内存口径标注 = %q，期望 %q", p.Key, r.Role, r.MemRule, want)
			}
		}
	}
}

// TestSizingMonotonic 档位越高，同一角色的内存/堆不得回退（避免「大力出奇迹比标准使用还小」）。
func TestSizingMonotonic(t *testing.T) {
	prev := map[string]esSizingRole{}
	for _, k := range esSizingKeys {
		for _, r := range esSizingProfiles[k].Roles {
			if p, ok := prev[r.Role]; ok {
				if r.MemGB < p.MemGB || r.HeapGB < p.HeapGB {
					t.Fatalf("角色 %s 在档位 %s（%dG/%dG 堆）低于上一档（%dG/%dG 堆）",
						r.Role, k, r.MemGB, r.HeapGB, p.MemGB, p.HeapGB)
				}
			}
			prev[r.Role] = r
		}
	}
}

// TestSizingHeapNaming 堆字符串口径：必须是 <n>g，且集群模式角色映射到数据节点档位。
func TestSizingHeapNaming(t *testing.T) {
	for _, k := range esSizingKeys {
		for _, role := range append(append([]string{}, esSizingRoleKeys...), esRoleCluster) {
			h := esRoleHeap(k, role)
			if !strings.HasSuffix(h, "g") {
				t.Fatalf("档位 %s 角色 %s 堆 %q 不带 g 后缀", k, role, h)
			}
			n, err := strconv.Atoi(strings.TrimSuffix(h, "g"))
			if err != nil || n <= 0 {
				t.Fatalf("档位 %s 角色 %s 堆 %q 非法", k, role, h)
			}
		}
	}
	// 集群模式（节点即数据节点）应与 data_hot 同档
	for _, k := range esSizingKeys {
		if a, b := esRoleHeap(k, esRoleCluster), esRoleHeap(k, "data_hot"); a != b {
			t.Fatalf("档位 %s 集群模式堆 %s 应等于 data_hot 的 %s", k, a, b)
		}
	}
	// 角色大小关系：hot ≥ warm ≥ cold ≥ master（分层存储的基本取舍）
	for _, k := range esSizingKeys {
		hot := esSizingProfiles[k].roleHeaps()["data_hot"]
		warm := esSizingProfiles[k].roleHeaps()["data_warm"]
		cold := esSizingProfiles[k].roleHeaps()["data_cold"]
		if hot < warm || warm < cold {
			t.Fatalf("档位 %s 堆应满足 hot ≥ warm ≥ cold，实际 %d/%d/%d", k, hot, warm, cold)
		}
	}
}

// TestSizingMergedMasterCoordinator master 与纯协调合并为一行（2026-09-16 用户拍板）：
// 合并行取两角色中较重的一档（原纯协调值），部署注入时 coordinator 必须复用该堆——
// 若有人把 coordinator 从 roleHeaps 映射里删掉，esRoleHeap 会兜底到 data_hot（虚大），此处拦截。
func TestSizingMergedMasterCoordinator(t *testing.T) {
	for _, k := range esSizingKeys {
		p := esSizingProfiles[k]
		master := p.roleHeaps()["master"]
		if master == 0 {
			t.Fatalf("档位 %s 缺 master 合并行", k)
		}
		if got := p.roleHeaps()["coordinator"]; got != master {
			t.Fatalf("档位 %s coordinator 堆 %d 应复用 master 合并行的 %d", k, got, master)
		}
		if got, want := esRoleHeap(k, "coordinator"), esRoleHeap(k, "master"); got != want {
			t.Fatalf("档位 %s esRoleHeap(coordinator)=%s 与 master=%s 不一致", k, got, want)
		}
	}
}

// TestSizingNormalize 未知/空档位回落默认档（历史实例参数里没有 sizing）。
func TestSizingNormalize(t *testing.T) {
	cases := map[string]string{
		"":         esSizingStandard,
		"  ":       esSizingStandard,
		"STANDARD": esSizingStandard,
		" full ":   esSizingFull,
		"MINI":     esSizingMini,
		"large":    esSizingStandard,
		"小额尝鲜":     esSizingStandard,
	}
	for in, want := range cases {
		if got := esSizingNormalize(in); got != want {
			t.Fatalf("归一化 %q = %q，期望 %q", in, got, want)
		}
	}
	if got := esRoleHeap("不存在的档位", "data_hot"); got != esRoleHeap(esSizingStandard, "data_hot") {
		t.Fatalf("未知档位应回落默认档堆，实际 %s", got)
	}
	// 未知角色兜底为数据节点规格（宁大勿小）
	if got, want := esRoleHeap(esSizingStandard, "未知角色"), esRoleHeap(esSizingStandard, "data_hot"); got != want {
		t.Fatalf("未知角色应兜底 data_hot 堆 %s，实际 %s", want, got)
	}
	if got := esSizingLabel("standard"); got != "标准使用" {
		t.Fatalf("档位展示名 = %q", got)
	}
}

// TestSizingTableOrder Extras 下发顺序固定为 mini → standard → full（界面按此顺序渲染卡片）。
func TestSizingTableOrder(t *testing.T) {
	tab := esSizingTable()
	if len(tab) != 3 {
		t.Fatalf("下发档位数 = %d", len(tab))
	}
	for i, k := range esSizingKeys {
		if tab[i].Key != k {
			t.Fatalf("第 %d 档 = %s，期望 %s", i, tab[i].Key, k)
		}
	}
}

// TestSizingRefIsReferenceOnly 参考数量口径（2026-09-16 用户拍板）：
// 档位只定义每角色 JVM 参数 + 参考数量，数据层一律「按需 n」，不得写死具体台数——
// 按数据量推台数没有准数（曾写过「满载 6×hot」的参考拓扑与逐档容量估算，均已删）。
func TestSizingRefIsReferenceOnly(t *testing.T) {
	for _, k := range esSizingKeys {
		for _, r := range esSizingProfiles[k].Roles {
			if r.Role == "master" {
				continue // master 行合并展示「master ×3 · 协调 ×2」或 mini「各 ×1」
			}
			if k == esSizingMini {
				if r.Ref != "×1" {
					t.Fatalf("尝鲜档数据角色 %s 参考数量 = %q，期望 ×1（每角色各 1 台）", r.Role, r.Ref)
				}
				continue
			}
			if r.Ref != "按需 n" {
				t.Fatalf("档位 %s 数据角色 %s 参考数量 = %q，应为「按需 n」（数量由用户决定）", k, r.Role, r.Ref)
			}
		}
	}
}
