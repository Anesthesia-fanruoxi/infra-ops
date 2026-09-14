// 套件服务登记入口：按套件能力（ServiceRegistrar）分发，未实现即走引擎通用角色投影。
package stack

import (
	"infra-ops/model"
	"infra-ops/store/stackkit"
)

// registerStackService 服务登记唯一入口：套件实现 stackkit.ServiceRegistrar 即由其声明服务入口
// 与安装台账，否则走引擎通用角色投影（主 / 工作 / 节点三档）。
//
// 参数取「运行级 + 主机级」合并视图（运行期主机 ParamsJSON 本就是合并后的整体视图，
// 冷热温的 roles 就放在主机参数里）。
func (h *stackHandler) registerStackService(d stackkit.Driver, host *model.StackRunHost, mode string, params map[string]string, instanceID int64, hosts []model.StackRunHost) {
	ctx := stackkit.RegisterCtx{
		Key: d.Key(), Name: d.Blueprint().Name, Mode: mode,
		Host: *host, Hosts: hosts, Params: params, InstanceID: instanceID,
		Emit:        func(svc model.HostService) { _ = h.tplRepo.UpsertHostService(&svc) },
		MarkInstall: func(name string) { _ = h.tplRepo.MarkHostInstalled(host.HostID, 0, name, 0) },
	}
	if reg, ok := d.(stackkit.ServiceRegistrar); ok {
		reg.RegisterServices(ctx)
		return
	}
	stackkit.RegisterByRole(ctx)
}
