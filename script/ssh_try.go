//go:build ignore

package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("用法: go run script\\ssh_try.go <host:port> <user> <password>")
		os.Exit(1)
	}
	addr, user, pass := os.Args[1], os.Args[2], os.Args[3]

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         8 * time.Second,
	}

	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		fmt.Printf("FAIL: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Println("OK: connection established")

	// 执行 ls 命令
	session, err := conn.NewSession()
	if err != nil {
		fmt.Println("new session FAIL:", err)
		os.Exit(1)
	}
	defer session.Close()

	out, err := session.Output("ls")
	if err != nil {
		fmt.Println("exec ls FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("ls 输出:")
	fmt.Println(string(out))
}
