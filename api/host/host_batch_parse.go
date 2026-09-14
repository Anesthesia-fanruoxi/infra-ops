// IP 范围解析器：单 IP / 简写范围 / 完整范围 / CIDR 网段。
package host

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// maxBatchIPs 单批最大 IP 数量。
const maxBatchIPs = 100

// errTooManyIPs 解析后数量超过上限。
var errTooManyIPs = errors.New("数量超过上限（100 台/批）")

// parseIPList 解析 IP 列表文本：支持换行/逗号/分号/空格分隔，单项支持
// 单 IP、简写范围 a.b.c.d-e、完整范围 a.b.c.d-w.x.y.z；按出现顺序去重。
// 任一非法项返回 error（含首个非法项），展开总数 > 100 返回 errTooManyIPs。
func parseIPList(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\r'
	})
	var out []string
	seen := make(map[string]bool)
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		ips, err := expandRange(f)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if seen[ip] {
				continue
			}
			seen[ip] = true
			out = append(out, ip)
			if len(out) > maxBatchIPs {
				return nil, errTooManyIPs
			}
		}
	}
	return out, nil
}

// expandRange 展开单个 IP、范围项或 CIDR 网段。
func expandRange(item string) ([]string, error) {
	if strings.Contains(item, "/") {
		return expandCIDR(item)
	}
	parts := strings.Split(item, "-")
	switch len(parts) {
	case 1:
		if !validIPv4(item) {
			return nil, fmt.Errorf("非法 IP: %s", item)
		}
		return []string{item}, nil
	case 2:
		start, end := parts[0], parts[1]
		if !validIPv4(start) {
			return nil, fmt.Errorf("非法 IP: %s", start)
		}
		if strings.Contains(end, ".") {
			if !validIPv4(end) {
				return nil, fmt.Errorf("非法 IP: %s", end)
			}
			return expandRange2(start, end)
		}
		n, err := strconv.Atoi(end)
		if err != nil || n < 1 || n > 254 {
			return nil, fmt.Errorf("非法范围终点: %s", end)
		}
		return expandRange2(start, joinIP(start, n))
	default:
		return nil, fmt.Errorf("非法范围: %s", item)
	}
}

// expandCIDR 展开 IPv4 网段（a.b.c.d/prefix，prefix 2-30）：排出网络地址与广播地址，
// 返回可用主机 IP。展开总数 > maxBatchIPs 时返回 errTooManyIPs。
func expandCIDR(item string) ([]string, error) {
	ip, ipnet, err := net.ParseCIDR(item)
	if err != nil {
		return nil, fmt.Errorf("非法网段: %s", item)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("仅支持 IPv4 网段: %s", item)
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones < 2 || ones > 30 {
		return nil, fmt.Errorf("非法掩码: %s", item)
	}
	// 网络地址 -> 网络号；hostMask 覆盖主机位
	var network [4]byte
	for i := 0; i < 4; i++ {
		network[i] = ip4[i] & ipnet.Mask[i]
	}
	hostMask := uint32(0xFFFFFFFF) >> uint(ones)
	base := uint32(network[0])<<24 | uint32(network[1])<<16 | uint32(network[2])<<8 | uint32(network[3])
	first := base + 1       // 排网络地址
	last := base | hostMask // 广播地址
	if last-first > uint32(maxBatchIPs) {
		return nil, errTooManyIPs
	}
	out := make([]string, 0, int(last-first))
	for a := first; a < last; a++ {
		out = append(out, fmt.Sprintf("%d.%d.%d.%d", a>>24, a>>16&0xFF, a>>8&0xFF, a&0xFF))
	}
	return out, nil
}

// validIPv4 校验 IPv4 格式（4 段，每段 0-255 纯数字）。
func validIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
		n, _ := strconv.Atoi(p)
		if n < 0 || n > 255 {
			return false
		}
	}
	return true
}

// expandRange2 展开完整起止范围：前三段必须一致，终值 ≥ 起值。
func expandRange2(start, end string) ([]string, error) {
	sp, ep := strings.Split(start, "."), strings.Split(end, ".")
	if sp[0] != ep[0] || sp[1] != ep[1] || sp[2] != ep[2] {
		return nil, fmt.Errorf("范围跨网段: %s-%s", start, end)
	}
	s, _ := strconv.Atoi(sp[3])
	e, _ := strconv.Atoi(ep[3])
	if e < s {
		return nil, fmt.Errorf("范围倒挂: %s-%s", start, end)
	}
	var out []string
	for i := s; i <= e; i++ {
		out = append(out, joinIP(start, i))
	}
	return out, nil
}

// joinIP 用末段值拼回完整 IP（start 须为合法 4 段 IP）。
func joinIP(start string, last int) string {
	parts := strings.Split(start, ".")
	return fmt.Sprintf("%s.%s.%s.%d", parts[0], parts[1], parts[2], last)
}
