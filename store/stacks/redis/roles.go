package redis

// roles.go：Redis 运行期主机角色命名（原 api/stack.stackHostRole 的 redis 特判原样迁入）。

// HostRole 主从 / 哨兵模式把非主节点命名为 replica；集群模式返回空串，
// 由引擎按「AssignMaster → master/worker，否则 node」通用规则命名。
func (d *Driver) HostRole(mode string, hostID, masterID int64, assignMaster bool) string {
	if mode != "replication" && mode != "sentinel" {
		return ""
	}
	if hostID == masterID {
		return "master"
	}
	return "replica"
}
