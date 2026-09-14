package rabbitmq

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// defaults.go：RabbitMQ 参数默认值补全（原 api/stack/stack_topo.go 的 randomErlangCookie
// 与 stack_create.go 的 rabbitmq 特判原样迁入）。
//
// 集群内各节点必须使用同一 Erlang cookie 才能互相通信；未填则自动生成一枚
// （16 字节十六进制），由平台注入到各主机的渲染参数中。

// Defaults 实现 stackkit.DefaultsProvider：就地写入参数默认值。
func (d *Driver) Defaults(op, _ string, params map[string]string) error {
	if op != "create" || strings.TrimSpace(params["erlang_cookie"]) != "" {
		return nil
	}
	cookie, err := randomErlangCookie()
	if err != nil {
		return fmt.Errorf("生成 Erlang Cookie 失败: %w", err)
	}
	params["erlang_cookie"] = cookie
	return nil
}

// randomErlangCookie 生成 16 字节十六进制 Erlang cookie。
func randomErlangCookie() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
