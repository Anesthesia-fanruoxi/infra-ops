// 部署远程执行运行时：前置依赖检查、单主机 SSH 执行、输出节流与缓冲。
package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	icrypto "infra-ops/common/crypto"
	"infra-ops/common/sshx"
	"infra-ops/model"
	"infra-ops/store/repo"
)

// TemplateRequires 解析模板前置依赖声明。
func TemplateRequires(t *model.DeployTemplate) []model.TemplateDependency {
	if t == nil || len(t.Requires) == 0 {
		return nil
	}
	var deps []model.TemplateDependency
	if err := json.Unmarshal(t.Requires, &deps); err != nil {
		return nil
	}
	return deps
}

// CheckRequires 对单主机逐条执行前置依赖检查；全部通过返回空串，否则返回阻断提示。
func CheckRequires(hostRepo *repo.HostRepo, credRepo *repo.CredentialRepo, cryptoS *icrypto.Service,
	sshC *sshx.Client, hostID int64, reqs []model.TemplateDependency) string {
	for _, d := range reqs {
		if strings.TrimSpace(d.Check) == "" {
			continue
		}
		if _, err := ExecHostWith(hostRepo, credRepo, cryptoS, sshC, hostID, d.Check, nil); err != nil {
			if d.Hint != "" {
				return d.Hint
			}
			return "依赖检查未通过：" + d.Check
		}
	}
	return ""
}

// ExecHostWith 部署与编排共用的单主机执行：解密凭据→SSH 拨号→运行脚本。
func ExecHostWith(hostRepo *repo.HostRepo, credRepo *repo.CredentialRepo, cryptoS *icrypto.Service,
	sshC *sshx.Client, hostID int64, script string, onLog func(string)) (string, error) {
	host, err := hostRepo.GetByID(hostID)
	if err != nil || host == nil {
		return "", fmt.Errorf("主机不存在")
	}
	cred, err := credRepo.GetByID(host.CredentialID)
	if err != nil || cred == nil {
		return "", fmt.Errorf("凭据不存在")
	}
	secret, err := cryptoS.Decrypt(cred.EncryptedSecret)
	if err != nil {
		return "", fmt.Errorf("凭据解密失败: %w", err)
	}

	dialCfg := sshx.DialConfig{
		Addr:     fmt.Sprintf("%s:%d", host.IP, host.Port),
		Username: cred.Username,
	}
	if cred.Type == "private_key" {
		dialCfg.PrivateKey = secret
	} else {
		dialCfg.Password = string(secret)
	}
	client, err := sshC.Dial(dialCfg)
	if err != nil {
		return "", err
	}
	defer client.Close()

	return runRemoteScript(client, script, onLog)
}

// runRemoteScript 在连接上执行脚本：合并输出落缓冲（上限 64KB），
// 同时按 ~400ms 节流把增量输出回调给 onLog（SSE 实时日志），超时 600s。
func runRemoteScript(client *ssh.Client, script string, onLog func(string)) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	tw := &streamTee{onLog: onLog}
	session.Stdout = tw
	session.Stderr = tw

	done := make(chan error, 1)
	go func() { done <- session.Run(script) }()

	flushTicker := time.NewTicker(400 * time.Millisecond)
	stop := make(chan struct{})
	defer func() { close(stop); flushTicker.Stop() }()
	go func() {
		for {
			select {
			case <-flushTicker.C:
				tw.Flush()
			case <-stop:
				return
			}
		}
	}()

	var execErr error
	select {
	case execErr = <-done:
		tw.Flush() // 收尾冲刷残余输出
	case <-time.After(execTimeout):
		_ = session.Close()
		tw.Flush()
		return tw.Snapshot(), fmt.Errorf("执行超时(%s)", execTimeout)
	}
	if execErr != nil {
		return tw.Snapshot(), fmt.Errorf("exit: %w", execErr)
	}
	return tw.Snapshot(), nil
}

// streamTee 把 SSH 输出同时写入全量快照与待发送增量区；Flush 由节流器周期调用。
type streamTee struct {
	mu      sync.Mutex
	all     limitedBuffer // 全量快照，最终落库
	pending []byte        // 待推送增量
	onLog   func(string)
}

func (w *streamTee) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.all.Write(p)
	// 待发送区将溢出时先同步冲刷一次，保证输出顺序
	if len(w.pending)+len(p) > 16<<10 && len(w.pending) > 0 && w.onLog != nil {
		w.onLog(string(w.pending))
		w.pending = nil
	}
	if remain := (16 << 10) - len(w.pending); len(p) > remain {
		p = p[:remain]
	}
	w.pending = append(w.pending, p...)
	return len(p), nil
}

// Flush 把当前累积的增量输出推送给回调。
func (w *streamTee) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 || w.onLog == nil {
		return
	}
	w.onLog(string(w.pending))
	w.pending = nil
}

// Snapshot 返回全量输出快照。
func (w *streamTee) Snapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.all.String()
}

// limitedBuffer 带写入上限的缓冲，防止超长输出撑爆内存。
type limitedBuffer struct{ b []byte }

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if remain := outputLimit - len(w.b); remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		w.b = append(w.b, p...)
	}
	return len(p), nil
}

func (w *limitedBuffer) String() string { return string(w.b) }
