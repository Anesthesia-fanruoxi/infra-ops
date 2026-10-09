package sshx

import (
	"io"
	"net"

	"golang.org/x/crypto/ssh"
)

// LocalForward 在本机 127.0.0.1 的随机端口监听，把每条进来的连接桥接到
// 「经 ssh 可达」的 target（ip:port），返回的 listener 即隧道入口地址。
//
// 供「数据库在内网、只经跳板机可达」这类场景使用：调用方拿到 listener 后，
// 把它的地址当作数据库地址连接即可。关闭 listener 仅停止接受新连接，
// 已建立的桥接在各自结束时收尾。
func LocalForward(client *ssh.Client, target string) (net.Listener, error) {
	return forwardTo(client, target)
}

// RemoteDialer 经由 SSH 向目标地址拨号的能力（*ssh.Client 天然满足）。
type RemoteDialer interface {
	Dial(network, addr string) (net.Conn, error)
}

// forwardTo 与 LocalForward 相同，但接受任意拨号抽象，便于替换测试。
func forwardTo(dialer RemoteDialer, target string) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go forwardLoop(ln, dialer, target)
	return ln, nil
}

func forwardLoop(ln net.Listener, dialer RemoteDialer, target string) {
	for {
		local, err := ln.Accept()
		if err != nil {
			return
		}
		go bridge(local, dialer, target)
	}
}

func bridge(local net.Conn, dialer RemoteDialer, target string) {
	defer local.Close()
	remote, err := dialer.Dial("tcp", target)
	if err != nil {
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	<-done
}
