// Package sshx 是纯 SSH 通道层，不感知业务概念。
package sshx

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostKeyStore host key 指纹存取（TOFU）。
type HostKeyStore interface {
	Get(addr string) (string, error)
	Save(addr, fingerprint string) error
}

// Client SSH 连接客户端。
type Client struct {
	timeout   time.Duration
	hkStore   HostKeyStore
	insecure  bool
	mu        sync.Mutex
}

// NewClient 创建 SSH 客户端。
func NewClient(timeoutSec int, hkStore HostKeyStore, insecure bool) *Client {
	return &Client{
		timeout:  time.Duration(timeoutSec) * time.Second,
		hkStore:  hkStore,
		insecure: insecure,
	}
}

// DialConfig 建连所需参数（显式传入，不依赖业务模型）。
type DialConfig struct {
	Addr       string // ip:port
	Username   string
	Password   string   // 密码认证
	PrivateKey []byte   // 密钥认证（PEM 原文）
}

// Dial 建立 SSH 连接，返回 *ssh.Client（调用方负责 Close）。
func (c *Client) Dial(cfg DialConfig) (*ssh.Client, error) {
	sshCfg, err := c.buildSSHConfig(cfg.Addr)
	if err != nil {
		return nil, err
	}
	sshCfg.User = cfg.Username

	// 认证方式
	var authMethods []ssh.AuthMethod
	if len(cfg.PrivateKey) > 0 {
		signer, err := ssh.ParsePrivateKey(cfg.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no auth method provided")
	}
	sshCfg.Auth = authMethods

	conn, err := ssh.Dial("tcp", cfg.Addr, sshCfg)
	if err != nil {
		return nil, classifySSHErr(err)
	}
	return conn, nil
}

func (c *Client) buildSSHConfig(addr string) (*ssh.ClientConfig, error) {
	cfg := &ssh.ClientConfig{
		Timeout: c.timeout,
	}

	if c.insecure {
		cfg.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		return cfg, nil
	}

	cfg.HostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		return c.verifyHostKey(addr, fp)
	}

	return cfg, nil
}

func (c *Client) verifyHostKey(addr, fingerprint string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	existing, err := c.hkStore.Get(addr)
	if err != nil {
		return fmt.Errorf("read host key: %w", err)
	}

	if existing == "" {
		// 首次连接，记录指纹
		return c.hkStore.Save(addr, fingerprint)
	}

	if existing != fingerprint {
		return fmt.Errorf("host key mismatch for %s: expected %s, got %s", addr, existing, fingerprint)
	}
	return nil
}

// classifySSHErr 将 SSH 错误归类为可读错误。
func classifySSHErr(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no suitable authentication method"):
		return fmt.Errorf("ssh auth failed: %w", err)
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "no route to host"):
		return fmt.Errorf("ssh connection failed: %w", err)
	case strings.Contains(msg, "host key mismatch"):
		return fmt.Errorf("ssh host key changed: %w", err)
	default:
		return fmt.Errorf("ssh error: %w", err)
	}
}
