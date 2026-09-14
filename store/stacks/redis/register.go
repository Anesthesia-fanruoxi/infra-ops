package redis

import (
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// RegisterServices Redis 服务登记（原 api/stack.registerRedisService 原样迁入）：
// 主从/哨兵按主从登记 Redis 入口，哨兵额外登记 Sentinel，集群模式登记统一入口。
func (d *Driver) RegisterServices(ctx stackkit.RegisterCtx) {
	host, params := ctx.Host, ctx.Params
	port := strings.TrimSpace(params["port"])
	if port == "" {
		port = "6379"
	}
	name := "Redis Cluster"
	if ctx.Mode == "replication" || ctx.Mode == "sentinel" {
		if host.Role == "master" {
			name = "Redis 主"
		} else {
			name = "Redis 从"
		}
	}
	ctx.Emit(model.HostService{
		HostID: host.HostID, HostIP: host.HostIP, ServiceName: name,
		URL: fmt.Sprintf("redis://%s:%s", host.HostIP, port), Web: false,
		TemplateID: 0, InstanceID: ctx.InstanceID,
	})
	if ctx.Mode == "sentinel" {
		sPort := strings.TrimSpace(params["sentinel_port"])
		if sPort == "" {
			sPort = "26379"
		}
		ctx.Emit(model.HostService{
			HostID: host.HostID, HostIP: host.HostIP, ServiceName: "Redis Sentinel",
			URL: fmt.Sprintf("redis-sentinel://%s:%s", host.HostIP, sPort), Web: false,
			TemplateID: 0, InstanceID: ctx.InstanceID,
		})
	}
	ctx.MarkInstall("套件:Redis/" + ctx.Mode)
}
