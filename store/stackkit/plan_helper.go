package stackkit

import (
	"fmt"
	"strings"
	"time"

	"infra-ops/model"
)

// NowLocal 角色计划 / 探活结果的生成时间戳。
// 格式与既有 nowLocal() 逐字节一致（本机时区渲染 + 固定 +08:00 后缀），不得擅自改成 RFC3339。
func NowLocal() string { return time.Now().Format("2006-01-02T15:04:05+08:00") }

// TrimParam 取参数并去首尾空白。
func TrimParam(params map[string]string, key string) string { return strings.TrimSpace(params[key]) }

// IsYes 判断取值是否为真值（yes / true / 1，忽略大小写）。
func IsYes(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "true", "1":
		return true
	}
	return false
}

// FirstNonEmpty 取首个去空白后非空的值。
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// Roles 角色落点累加器：把「原 switch 各 case 里的 addAt / addAll / addRest」几乎原样搬进套件目录。
//
// Boot / Rest / All 由引擎按同一规则算好（引导机 = 首个 Role==master 的主机，否则首台），
// 套件只关心「往哪些主机上加什么角色」。落点顺序与既有实现一致：按主机 Seq 升序，
// 同一主机内按调用先后追加。
type Roles struct {
	// Boot 引导机（主/首节点）。
	Boot model.StackRunHost
	// Rest 除引导机外的全部成员。
	Rest []model.StackRunHost
	// All 全部成员（按 Seq 升序）。
	All []model.StackRunHost
	// HasMaster 是否显式指定了主节点（Role==master）。
	HasMaster bool

	byIP  map[string][]model.RolePlanRole
	warns []string
}

// NewRoles 按既有规则构造落点助手：norm 必须已按 Seq 升序规范化。
func NewRoles(norm []model.StackRunHost) *Roles {
	r := &Roles{All: norm, byIP: map[string][]model.RolePlanRole{}}
	bootIdx := -1
	for i, h := range norm {
		if h.Role == "master" {
			bootIdx, r.HasMaster = i, true
			break
		}
	}
	if bootIdx < 0 {
		bootIdx = 0
	}
	if len(norm) > 0 {
		r.Boot = norm[bootIdx]
	}
	rest := make([]model.StackRunHost, 0, len(norm))
	for i, h := range norm {
		if i != bootIdx {
			rest = append(rest, h)
		}
	}
	r.Rest = rest
	return r
}

// AddAt 在指定主机上追加一个角色落点。scope 语义：""=落点角色（缩容保护）、
// "all"=全员角色、"rest"=非引导成员角色、"aux"=辅助组件（不保护）。
func (r *Roles) AddAt(comp, role, label, scope, source string, h model.StackRunHost) {
	r.byIP[h.HostIP] = append(r.byIP[h.HostIP], model.RolePlanRole{
		Comp: comp, Role: role, Label: label, Source: source, Scope: scope,
	})
}

// AddAll 给全部成员加一个全员角色（scope=all，source=auto）。
func (r *Roles) AddAll(comp, role, label string) {
	for _, h := range r.All {
		r.AddAt(comp, role, label, "all", "auto", h)
	}
}

// AddRest 给除引导机外的成员加一个角色（scope=rest，source=auto）。
func (r *Roles) AddRest(comp, role, label string) {
	for _, h := range r.Rest {
		r.AddAt(comp, role, label, "rest", "auto", h)
	}
}

// Warn 追加一条自动落点提示（格式串语义与既有 append 一致）。
func (r *Roles) Warn(format string, a ...any) {
	r.warns = append(r.warns, fmt.Sprintf(format, a...))
}

// Warnings 返回已累积的提示。
func (r *Roles) Warnings() []string { return r.warns }

// PlanHosts 把累加结果展开为 RolePlan.Hosts（按传入顺序，无角色的主机给空数组而非 null）。
func (r *Roles) PlanHosts(norm []model.StackRunHost) []model.RolePlanHost {
	out := make([]model.RolePlanHost, 0, len(norm))
	for _, h := range norm {
		roles := r.byIP[h.HostIP]
		if roles == nil {
			roles = []model.RolePlanRole{}
		}
		out = append(out, model.RolePlanHost{
			HostID: h.HostID, HostName: h.HostName, HostIP: h.HostIP, Seq: h.Seq, Roles: roles,
		})
	}
	return out
}
