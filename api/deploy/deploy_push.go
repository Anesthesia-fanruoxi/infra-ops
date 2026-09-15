// 部署远程执行运行时：SFTP 文件推送（部署资产分发等）。
package deploy

import (
	"fmt"
	"io"
	"os"
	"path"

	"github.com/pkg/sftp"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/sshx"
	"infra-ops/store/repo"
)

// PushFileToHost 经 SFTP 把本地文件推送到主机指定目录（目录不存在则逐级创建）。
// 与 ExecHostWith 同一套凭据解密与拨号路径；远端已存在的同名文件直接覆盖。
func PushFileToHost(hostRepo *repo.HostRepo, credRepo *repo.CredentialRepo, cryptoS *icrypto.Service,
	sshC *sshx.Client, hostID int64, localPath, remoteDir, remoteName string) error {
	client, _, err := DialHost(hostRepo, credRepo, cryptoS, sshC, hostID)
	if err != nil {
		return err
	}
	defer client.Close()

	sftpC, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("sftp client: %w", err)
	}
	defer sftpC.Close()

	if err := sftpC.MkdirAll(remoteDir); err != nil {
		return fmt.Errorf("创建远端目录 %s: %w", remoteDir, err)
	}
	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("打开本地文件: %w", err)
	}
	defer src.Close()

	dst, err := sftpC.Create(path.Join(remoteDir, remoteName))
	if err != nil {
		return fmt.Errorf("创建远端文件: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("传输文件: %w", err)
	}
	return nil
}
