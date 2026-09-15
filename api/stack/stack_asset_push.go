// 部署资产分发：引擎在执行部署阶段前，把套件声明（stackkit.AssetProvisioner）且已就绪的
// 资产经 SFTP 推送到目标机。资产未上传时跳过——脚本内建下载兜底（如 ha.sh 的 aliyun/repo1
// curl），分发失败只记日志不阻塞部署；{{home_dir}} 等占位按各主机参数渲染。
package stack

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"infra-ops/api/deploy"
	"infra-ops/model"
	"infra-ops/store/repo"
	"infra-ops/store/stackkit"
)

// provisionAssets 分发本次运行所需的离线资产到全部运行主机。
// 只推「已就绪」的资产；目标机上文件已存在时 SFTP 覆盖写（幂等，重装/扩容安全）。
func (h *stackHandler) provisionAssets(runID int64, run *model.StackRun, d stackkit.Driver, mode *model.StackMode, hosts []model.StackRunHost) {
	ap, ok := d.(stackkit.AssetProvisioner)
	if !ok {
		return
	}
	params := map[string]string{}
	_ = json.Unmarshal([]byte(run.ParamsJSON), &params)
	needs := ap.RequiredAssets(run.Mode, params)
	if len(needs) == 0 {
		return
	}
	assetRepo := repo.NewAssetRepo()
	for _, need := range needs {
		a, err := assetRepo.GetByKeyVersion(need.Key, need.Version)
		if err != nil || a == nil {
			h.appendLog(runID, "node", 0, "", "资产未就绪，跳过分发（目标机将尝试现场下载）: "+need.Desc)
			continue
		}
		local := assetFilePath(a)
		if _, err := os.Stat(local); err != nil {
			h.appendLog(runID, "node", 0, "", "资产文件缺失，跳过分发（目标机将尝试现场下载）: "+need.FileName)
			continue
		}
		h.appendLog(runID, "node", 0, "", fmt.Sprintf("开始分发资产 %s → %d 台主机", need.FileName, len(hosts)))
		var failed []string
		h.forEachHost(hosts, func(host *model.StackRunHost) {
			remoteDir := renderAssetDir(need.RemoteDir, host, mode)
			if err := deploy.PushFileToHost(h.hostRepo, h.credRepo, h.cryptoS, h.sshC,
				host.HostID, local, remoteDir, a.FileName); err != nil {
				failed = append(failed, host.HostIP+": "+err.Error())
				return
			}
			h.appendLog(runID, "node", host.HostID, host.HostIP, "资产已就位 "+remoteDir+"/"+a.FileName)
		})
		if len(failed) > 0 {
			// 单机失败不阻塞部署：ha.sh 现场下载兜底；失败明细入日志便于排查
			for _, f := range failed {
				h.appendLog(runID, "node", 0, "", "资产分发失败（不阻塞，脚本将现场下载兜底）: "+f)
			}
		} else {
			h.appendLog(runID, "node", 0, "", "资产分发完成 "+a.FileName+"（"+humanSize(a.SizeBytes)+"）")
		}
	}
}

// renderAssetDir 渲染资产目标目录中的 {{home_dir}} 占位：优先主机参数，回落模式默认值。
func renderAssetDir(dir string, host *model.StackRunHost, mode *model.StackMode) string {
	if !strings.Contains(dir, "{{home_dir}}") {
		return dir
	}
	home := ""
	if host != nil {
		p := map[string]string{}
		_ = json.Unmarshal([]byte(host.ParamsJSON), &p)
		home = strings.TrimSpace(p["home_dir"])
	}
	if home == "" && mode != nil {
		home = mode.DefaultHomeDir
	}
	if home == "" {
		home = "/data"
	}
	return strings.ReplaceAll(dir, "{{home_dir}}", home)
}

// humanSize 字节数的可读展示（日志用）。
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", float64(n)/(1<<20)), "0"), ".") + "MB"
	case n >= 1<<10:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", float64(n)/(1<<10)), "0"), ".") + "KB"
	default:
		return fmt.Sprintf("%dB", n)
	}
}
