// Package shared 提供跨各 API 业务子包复用的辅助函数，避免分包后重复定义或循环依赖。
package shared

import (
	"strconv"
	"strings"

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
