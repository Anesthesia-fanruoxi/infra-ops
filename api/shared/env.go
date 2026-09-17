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
// url 支持占位符：{{ip}} 替换为主机 IP，{{变量名}} 替换为该主机执行时的变量值，
// 参数缺省回落模板变量默认值（与 RenderScript 同口径——曾因编排步未显式填端口，
// 脚本按默认值执行成功而 URL 留下 {{port}} 未渲染，hub 候选下拉显示出坏地址）。
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
	// 模板变量默认值表：执行参数没给（或给空）的字段回落默认值
	defaults := map[string]string{}
	var decls []struct {
		Name    string `json:"name"`
		Default string `json:"default"`
	}
	if len(bytes.TrimSpace(tpl.Variables)) > 0 {
		if err := json.Unmarshal(tpl.Variables, &decls); err == nil {
			for _, d := range decls {
				defaults[d.Name] = d.Default
			}
		}
	}
	for _, s := range svcs {
		url := strings.ReplaceAll(s.URL, "{{ip}}", hostIP)
		for k, v := range vars {
			if strings.TrimSpace(v) != "" {
				url = strings.ReplaceAll(url, "{{"+k+"}}", v)
			}
		}
		for k, v := range defaults {
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
