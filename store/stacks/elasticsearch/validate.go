package elasticsearch

import (
	"fmt"
	"strings"

	"infra-ops/store/stackkit"
)

// validate.go：冷热温模式的角色组合校验（原 api/stack/stack_topo.go 的 validateCWHRoles 原样迁入）。
//
// 逐条硬性非法组合返回 400：master 与数据层互斥、coordinator 不与数据层共存、缺 master 候选、
// 未知角色、单台角色重复；master 数量为偶数时返回引导提示（非错误）。

// ValidateCreate 实现 stackkit.CreateValidator：仅冷热温模式的创建请求需要角色组合校验。
func (d *Driver) ValidateCreate(ctx stackkit.ValidateCtx) error {
	if ctx.Op != "create" || ctx.Mode != "cold_warm_hot" {
		return nil
	}
	_, err := validateCWHRoles(ctx.HostParams)
	return err
}

// validateCWHRoles 校验 ES 冷热温模式角色组合。
// hostRoles 为逐主机角色表（hostID 字符串 -> {roles: "a,b"}）；合法角色：
// master / coordinator（纯协调）/ data_hot / data_warm / data_cold。
func validateCWHRoles(hostRoles map[string]map[string]string) (string, error) {
	validRoles := map[string]bool{
		"master": true, "coordinator": true,
		"data_hot": true, "data_warm": true, "data_cold": true,
	}
	masterCnt := 0
	for hostID, hp := range hostRoles {
		raw := strings.TrimSpace(hp["roles"])
		if raw == "" {
			raw = "coordinator"
		}
		var roles []string
		for _, r := range strings.Split(raw, ",") {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if !validRoles[r] {
				return "", fmt.Errorf("主机 %s 含未知角色: %s", hostID, r)
			}
			if stackkit.Contains(roles, r) {
				return "", fmt.Errorf("主机 %s 角色 %s 重复选择", hostID, r)
			}
			roles = append(roles, r)
		}
		if len(roles) == 0 {
			roles = []string{"coordinator"}
		}
		isMaster := stackkit.Contains(roles, "master")
		hasCoord := stackkit.Contains(roles, "coordinator")
		hasData := stackkit.Contains(roles, "data_hot") || stackkit.Contains(roles, "data_warm") || stackkit.Contains(roles, "data_cold")
		switch {
		case isMaster && (hasCoord || hasData || len(roles) > 1):
			return "", fmt.Errorf("主机 %s: master 不能与数据层/协调角色共存（master 与数据层互斥）", hostID)
		case hasCoord && hasData:
			return "", fmt.Errorf("主机 %s: 纯协调（coordinator）不能同时承担数据层角色", hostID)
		case isMaster:
			masterCnt++
		}
	}
	if masterCnt == 0 {
		return "", fmt.Errorf("冷热温模式至少需要 1 台 master 候选主机（生产建议 ≥3 台奇数）")
	}
	if masterCnt%2 == 0 {
		return fmt.Sprintf("当前 %d 台 master 候选为偶数，生产建议奇数（≥3）以保障仲裁", masterCnt), nil
	}
	return "", nil
}
