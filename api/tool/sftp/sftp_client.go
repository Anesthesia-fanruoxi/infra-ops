// SFTP 连接层与文件系统原语：SSH 拨号与递归删除。
package sftp

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"infra-ops/model"
)

// dial 建立一次 SFTP 连接（调用方负责 Close）。
func (h *Handler) dial(conn *model.SFTPConn) (*sftp.Client, error) {
	cfg := &ssh.ClientConfig{
		User:            conn.Username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}
	var auths []ssh.AuthMethod
	if len(conn.EncryptedKey) > 0 {
		key, err := h.cryptoS.Decrypt(conn.EncryptedKey)
		if err == nil {
			if signer, err := ssh.ParsePrivateKey(key); err == nil {
				auths = append(auths, ssh.PublicKeys(signer))
			}
		}
	}
	if len(conn.EncryptedSecret) > 0 {
		if pwd, err := h.cryptoS.Decrypt(conn.EncryptedSecret); err == nil {
			auths = append(auths, ssh.Password(string(pwd)))
		}
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("未配置认证凭据（密码或私钥）")
	}
	cfg.Auth = auths

	addr := net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port))
	sshConn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
	}
	client, err := sftp.NewClient(sshConn)
	if err != nil {
		sshConn.Close()
		return nil, fmt.Errorf("初始化 SFTP 会话失败: %w", err)
	}
	return client, nil
}

func removeRecursive(client *sftp.Client, p string) error {
	entries, err := client.ReadDir(p)
	if err != nil {
		return fmt.Errorf("读取目录 %s: %v", p, err)
	}
	for _, e := range entries {
		ep := p + "/" + e.Name()
		if p == "/" {
			ep = "/" + e.Name()
		}
		if e.IsDir() {
			if err := removeRecursive(client, ep); err != nil {
				return err
			}
		} else {
			if err := client.Remove(ep); err != nil {
				return err
			}
		}
	}
	return client.RemoveDirectory(p)
}
