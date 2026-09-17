package elasticsearch

import (
	"fmt"
	"strings"

	"infra-ops/store/stackkit"
)

// validate.go：冷热温模式的角色组合校验（一机多容器口径）。
//
// 逐条硬性非法组合返回 400：数据层（hot/warm/cold）与 master/纯协调互斥（不混部）、
// 缺 master 候选、未知角色、单台角色重复；master 与纯协调可同机叠加（两容器，合法）；
// 空 roles 兜底冷热温全数据层（与 vars.go / 脚本口径一致）；master 偶数返回引导提示（非错误）。

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
		var roles []string
		for _, r := range strings.Split(strings.TrimSpace(hp["roles"]), ",") {
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
			roles = cwhDataRoles // 未分配主机自动归冷热温全数据层
		}
		isMaster := stackkit.Contains(roles, "master")
		hasCoord := stackkit.Contains(roles, "coordinator")
		hasData := stackkit.Contains(roles, "data_hot") || stackkit.Contains(roles, "data_warm") || stackkit.Contains(roles, "data_cold")
		if hasData && (isMaster || hasCoord) {
			return "", fmt.Errorf("主机 %s: 数据层节点不能与 master/纯协调混部（数据层与两者互斥）", hostID)
		}
		if isMaster {
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
