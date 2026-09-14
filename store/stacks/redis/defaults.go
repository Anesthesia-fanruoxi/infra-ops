package redis

import (
	"strings"
)

// defaults.go：Redis 参数默认值补全（原 api/stack/stack_phase.go 的 redis 特判原样迁入）。
//
// 集群模式的 bootstrap（CLUSTER CREATE）脚本需要 replicas 有确定取值：未传时按 0（三主）处理。
// 该口径与扩容路径（stack_op.go runPhaseScaleOut）保持一致。

// Defaults 实现 stackkit.DefaultsProvider：就地写入参数默认值。
//
// 仅在**运行期**（bootstrap / scale_out）兜底：创建期 replicas 已由蓝图共享变量默认值经
// mergeStackParams 落到各主机参数与实例参数，此时不再改写实例参数（与拆分前逐字一致）。
func (d *Driver) Defaults(op, mode string, params map[string]string) error {
	if op == "create" {
		return nil
	}
	if mode == "cluster" && strings.TrimSpace(params["replicas"]) == "" {
		params["replicas"] = "0"
	}
	return nil
}
