// Package netinfo 提供本机网卡信息与公网出口 IP 探测。
package netinfo

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// publicIPProviders 公网 IP 探测服务，按序尝试。
var publicIPProviders = []string{
	"https://api.ipify.org",
	"https://myexternalip.com/raw",
	"https://ifconfig.me/ip",
	"https://ip.3322.net",
}

// LocalIPv4s 返回本机所有非回环、已启用网卡的 IPv4 地址（排除链路本地地址）。
func LocalIPv4s() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ip.String())
		}
	}
	return ips
}

// PublicIP 通过公共探测服务获取公网出口 IP；全部失败返回空串。
func PublicIP() string {
	client := &http.Client{Timeout: 3 * time.Second}
	for _, u := range publicIPProviders {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return ""
}
