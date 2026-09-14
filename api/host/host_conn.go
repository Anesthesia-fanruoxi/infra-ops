package host

import (
	"fmt"
	"log"
	"strings"

	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/model"
)

// testConnection 执行 SSH 连接测试与信息采集，返回结果和错误码。
func (h *Handler) testConnection(host *model.Host) (*sshx.CollectResult, int) {
	cred, err := h.credRepo.GetByID(host.CredentialID)
	if err != nil || cred == nil {
		return nil, resp.CodeSSHConnFail
	}

	// 解密凭据
	secret, err := h.cryptoS.Decrypt(cred.EncryptedSecret)
	if err != nil {
		log.Printf("decrypt credential %d: %v", cred.ID, err)
		return nil, resp.CodeSSHAuthFail
	}

	addr := fmt.Sprintf("%s:%d", host.IP, host.Port)
	dialCfg := sshx.DialConfig{
		Addr:     addr,
		Username: cred.Username,
	}
	switch cred.Type {
	case "private_key":
		dialCfg.PrivateKey = secret
	case "password":
		dialCfg.Password = string(secret)
	}

	client, err := h.sshC.Dial(dialCfg)
	if err != nil {
		log.Printf("ssh dial %s: %v", addr, err)
		return nil, classifySSHCode(err)
	}
	defer client.Close()

	result, err := sshx.Collect(client)
	if err != nil {
		log.Printf("ssh collect %s: %v", addr, err)
		return nil, resp.CodeSSHCollectFail
	}
	return result, 0
}

// classifySSHCode 根据 SSH 错误返回对应错误码。
func classifySSHCode(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "auth"):
		return resp.CodeSSHAuthFail
	case strings.Contains(msg, "host key"):
		return resp.CodeSSHHostKey
	default:
		return resp.CodeSSHConnFail
	}
}

func connectionFailMsg(code int) string {
	switch code {
	case resp.CodeSSHConnFail:
		return "SSH 连接失败（网络/超时）"
	case resp.CodeSSHAuthFail:
		return "SSH 认证失败（密钥/密码不匹配）"
	case resp.CodeSSHHostKey:
		return "SSH host key 指纹变更，拒绝连接"
	case resp.CodeSSHCollectFail:
		return "信息采集失败"
	default:
		return "连接测试失败"
	}
}
