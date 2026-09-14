// Package shared 提供跨各 API 业务子包复用的辅助函数，避免分包后重复定义或循环依赖。
package shared

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"infra-ops/model"
	"infra-ops/store/repo"
)

// ApplyHostVars 替换内置主机变量：{{__seq}} 任务内序号（1 起）、
// {{__ip}} 主机 IP、{{__ip_last}} IP 末段、{{__name}} 当前主机名。
func ApplyHostVars(script string, seq int, rec repo.HostRecord) string {
	return strings.NewReplacer(
		"{{__seq}}", strconv.Itoa(seq),
		"{{__ip}}", rec.HostIP,
		"{{__ip_last}}", lastOctet(rec.HostIP),
		"{{__name}}", rec.HostName,
	).Replace(script)
}

// lastOctet 取点分 IPv4 的末段；非标准格式原样返回。
func lastOctet(ip string) string {
	if i := strings.LastIndexByte(ip, '.'); i >= 0 {
		return ip[i+1:]
	}
	return ip
}

// RegisterTemplateServices 模板在某主机成功执行后，按其 services 声明登记/刷新服务清单。
// url 支持占位符：{{ip}} 替换为主机 IP，{{变量名}} 替换为该主机执行时的变量值。
// 模板无服务声明时静默跳过。
func RegisterTemplateServices(tplRepo *repo.DeployRepo, hostID int64, hostIP string, templateID int64, vars map[string]string) error {
	tpl, err := tplRepo.GetTemplate(templateID)
	if err != nil || tpl == nil {
		return err
	}
	if len(bytes.TrimSpace(tpl.Services)) == 0 {
		return nil
	}
	var svcs []model.TemplateService
	if err := json.Unmarshal(tpl.Services, &svcs); err != nil || len(svcs) == 0 {
		return nil
	}
	for _, s := range svcs {
		url := strings.ReplaceAll(s.URL, "{{ip}}", hostIP)
		for k, v := range vars {
			url = strings.ReplaceAll(url, "{{"+k+"}}", v)
		}
		if err := tplRepo.UpsertHostService(&model.HostService{
			HostID: hostID, HostIP: hostIP, ServiceName: s.Name,
			URL: url, Web: s.Web, TemplateID: templateID,
		}); err != nil {
			return err
		}
	}
	return nil
}
