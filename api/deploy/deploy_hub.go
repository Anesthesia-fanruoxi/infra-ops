// hub 镜像主机：部署任务可选一台已部署 Docker Registry 的主机作为镜像源。
// 链路：建任务重写 image 变量 → 执行期 hub 健康校验（复用 Registry 模板口径）→
// 镜像预热（pull→tag→push，严格中止）→ 目标机 insecure-registries 预检（可选自动配置）。
// 仅覆盖模板 image 变量；脚本内硬编码的辅助镜像不在范围内。
package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	icrypto "infra-ops/common/crypto"
	"infra-ops/common/resp"
	"infra-ops/common/sshx"
	"infra-ops/model"
	"infra-ops/store/repo"
)

// hubServiceName 「安装 Docker Registry」模板在 host_services 登记的服务名。
const hubServiceName = "Docker Registry"

// hubRegistry 一次解析出的 hub 镜像源地址。
type hubRegistry struct {
	HostID int64
	IP     string
	// remote 供目标机拉取的地址 ip:port
	remote string
	// local 供 hub 本机操作（预热 tag/push、健康检查）的地址 127.0.0.1:port
	local string
}

// Remote 供目标机拉取的仓库地址（ip:port）。
func (r *hubRegistry) Remote() string { return r.remote }

// resolveHubRegistry 从台账解析 hub 主机的 Registry 服务地址；未登记/格式非法均报错。
func (h *deployHandler) resolveHubRegistry(hubHostID int64) (*hubRegistry, error) {
	return ResolveHubRegistry(h.hubDeps(), hubHostID)
}

// hubDeps 组装当前 handler 的 hub 阶段依赖。
func (h *deployHandler) hubDeps() HubStageDeps {
	return HubStageDeps{HostRepo: h.hostRepo, TplRepo: h.tplRepo,
		CredRepo: h.credRepo, CryptoS: h.cryptoS, SSHC: h.sshC}
}

// HubStageDeps hub 镜像源阶段所需的最小依赖——部署模板引擎与套件引擎共用
// （两个引擎的 SSH 执行通道同为 ExecHostWith，只是各自的日志与状态落点不同）。
type HubStageDeps struct {
	HostRepo *repo.HostRepo
	TplRepo  *repo.DeployRepo
	CredRepo *repo.CredentialRepo
	CryptoS  *icrypto.Service
	SSHC     *sshx.Client
}

func (d HubStageDeps) execWith(hostID int64, script string, onChunk func(string)) (string, error) {
	return ExecHostWith(d.HostRepo, d.CredRepo, d.CryptoS, d.SSHC, hostID, script, onChunk)
}

// ResolveHubRegistry 从台账解析 hub 主机的 Registry 服务地址；未登记/格式非法均报错。
func ResolveHubRegistry(d HubStageDeps, hubHostID int64) (*hubRegistry, error) {
	hh, err := d.HostRepo.GetByID(hubHostID)
	if err != nil || hh == nil {
		return nil, errors.New("hub 镜像主机不存在或已删除")
	}
	svcs, err := d.TplRepo.HostServices(hubHostID)
	if err != nil {
		return nil, fmt.Errorf("查询 hub 服务登记失败: %w", err)
	}
	var svc *model.HostService
	for i := range svcs {
		if svcs[i].ServiceName == hubServiceName {
			svc = &svcs[i]
			break
		}
	}
	if svc == nil {
		return nil, errors.New("hub 主机未登记 Docker Registry 服务（请先执行「安装 Docker Registry」模板）")
	}
	u, err := url.Parse(svc.URL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("hub Registry 地址非法: %s", svc.URL)
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	return &hubRegistry{HostID: hubHostID, IP: hh.IP, remote: u.Host, local: "127.0.0.1:" + port}, nil
}

// RunHubStage hub 镜像源执行前阶段（串行）：健康校验（复用 Registry 模板口径）→ 逐镜像预热。
// logf 收到 (hub 主机 IP, 日志文本)。返回 (hub 远程地址, "") 表示通过；非空第二值为失败原因。
func RunHubStage(d HubStageDeps, hubHostID int64, images []string, logf func(ip, text string)) (string, string) {
	hr, err := ResolveHubRegistry(d, hubHostID)
	if err != nil {
		return "", err.Error()
	}
	logf(hr.IP, "hub 校验: Docker Registry @ "+hr.remote)
	out, execErr := d.execWith(hubHostID, hubHealthScript(hr.local), nil)
	if execErr != nil || !strings.Contains(out, "REGISTRY_OK") {
		return "", "hub Registry 健康检查未通过（" + hr.remote + "）：请确认该机 Docker Registry 正在运行，且未开启 HTTP 基础认证（暂不支持带认证仓库）"
	}
	logf(hr.IP, "hub Registry 健康检查通过")

	for _, img := range images {
		target := hr.local + "/" + trimRegistryDomain(img)
		logf(hr.IP, "镜像预热: "+img+" → "+target)
		if _, perr := d.execWith(hubHostID, hubPrewarmScript(img, target), func(chunk string) {
			for _, ln := range shared.SplitLogLines(chunk) {
				logf(hr.IP, ln)
			}
		}); perr != nil {
			return "", "镜像预热失败（" + img + "）: " + perr.Error() + " —— 已严格中止，不回退直连拉取"
		}
	}
	logf(hr.IP, fmt.Sprintf("全部镜像预热完成（%d 个）", len(images)))
	return hr.remote, ""
}

// EnsureInsecureRegistryWith 目标机预检：Docker 须信任 hub 地址（HTTP 明文仓库的硬前提）。
// 未信任时按 auto 决定自动写 daemon.json + 重启 Docker，或返回失败提示。
func EnsureInsecureRegistryWith(d HubStageDeps, hostID int64, addr string, auto bool, logf func(ip, text string)) string {
	if addr == "" {
		return ""
	}
	ip := hostIP(d.HostRepo, hostID)
	out, err := d.execWith(hostID, insecureDetectScript(addr), nil)
	if err == nil && strings.Contains(out, "INSECURE_OK") {
		return ""
	}
	if !auto {
		return "前置依赖不满足：目标机 Docker 未信任 hub 镜像仓库地址 " + addr +
			"（HTTP 明文仓库需加入 insecure-registries）；可在部署向导勾选「自动配置」重试，或手动配置 daemon.json"
	}
	logf(ip, "自动配置 insecure-registries: "+addr)
	if _, cerr := d.execWith(hostID, insecureConfigScript(addr), func(chunk string) {
		for _, ln := range shared.SplitLogLines(chunk) {
			logf(ip, ln)
		}
	}); cerr != nil {
		return "自动配置 insecure-registries 失败：" + cerr.Error() + "（可手动配置后重试）"
	}
	return ""
}

// hostIP 查主机 IP；查不到返回空串（仅用于日志展示，不参与判定）。
func hostIP(hostRepo *repo.HostRepo, hostID int64) string {
	hh, err := hostRepo.GetByID(hostID)
	if err != nil || hh == nil {
		return ""
	}
	return hh.IP
}

// trimRegistryDomain 去掉镜像引用里的源仓库域名前缀（首段含 . 或 : 或为 localhost 时视为域名）：
// docker.io/library/mysql:8.0 → library/mysql:8.0，mysql:8.0 原样，quay.io/coreos/etcd:v3 → coreos/etcd:v3。
func trimRegistryDomain(image string) string {
	seg, rest, ok := strings.Cut(image, "/")
	if !ok {
		return image
	}
	if strings.ContainsAny(seg, ".:") || seg == "localhost" {
		return rest
	}
	return image
}

// Registries GET /api/deploy/registries：已登记 Docker Registry 的主机候选（含探活状态）。
func (h *deployHandler) Registries(c *gin.Context) {
	items, err := h.tplRepo.RegistryHosts()
	if err != nil {
		resp.ErrHTTP(c, 500, resp.CodeInternal, "查询 hub 候选失败")
		return
	}
	if items == nil {
		items = []model.HostService{}
	}
	resp.OK(c, items)
}

// templateHasVar 模板是否声明了同名变量。
func templateHasVar(t *model.DeployTemplate, name string) bool {
	vars, err := parseVariables(t.Variables)
	if err != nil {
		return false
	}
	for _, v := range vars {
		if v.Name == name {
			return true
		}
	}
	return false
}

// mapKeysSorted 返回去重集合的键并排序（镜像清单需稳定顺序，便于日志与复现）。
func mapKeysSorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hubImagesFromParams 从任务参数 JSON 取预热镜像清单（__hub_images，逗号分隔的原始镜像引用）。
func hubImagesFromParams(paramsJSON string) []string {
	var m map[string]string
	if json.Unmarshal([]byte(paramsJSON), &m) != nil {
		return nil
	}
	var out []string
	for _, s := range strings.Split(m["__hub_images"], ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hubFlagFromParams 从任务参数 JSON 取自动配置开关（__hub_auto_insecure）。
func hubFlagFromParams(paramsJSON string) bool {
	var m map[string]string
	if json.Unmarshal([]byte(paramsJSON), &m) != nil {
		return false
	}
	return m["__hub_auto_insecure"] == "1"
}

// hubHealthScript hub 健康检查：与「安装 Docker Registry」模板同一口径（本机 curl /v2/，
// -f 使 401/5xx 均失败——v1 不支持开启 htpasswd 认证的仓库）。
func hubHealthScript(local string) string {
	return "if curl -sfo /dev/null 'http://" + local + "/v2/'; then echo REGISTRY_OK; else echo REGISTRY_FAIL; exit 1; fi"
}

// hubPrewarmScript 单镜像预热：hub 上已有目标 tag 则跳过，否则 pull→tag→push。
func hubPrewarmScript(original, target string) string {
	return `set -e
if docker image inspect '` + target + `' >/dev/null 2>&1; then
  echo "hub 已存在镜像: ` + target + `，跳过"
  exit 0
fi
echo "hub 拉取镜像: ` + original + ` ..."
docker pull '` + original + `'
docker tag '` + original + `' '` + target + `'
echo "推送镜像: ` + target + ` ..."
docker push '` + target + `'
echo "预热完成: ` + target + `"`
}

// insecureDetectScript 检测目标机 docker info 是否已信任 hub 地址（匹配输出 INSECURE_OK）。
// 标签大小写不敏感匹配：docker info 实际输出「Insecure Registries:」（大写 R），
// 曾因按小写 registries 匹配导致预检恒判未信任、复检恒判未生效。
func insecureDetectScript(addr string) string {
	return `docker info 2>/dev/null | awk 'tolower($0) ~ /insecure registries:/{f=1;next} /^[^ \t]/{f=0} f' | sed 's/^[[:space:]]*//' | grep -Fxq '` + addr + `' && echo INSECURE_OK`
}

// insecureConfigScript 自动把 hub 地址写入 /etc/docker/daemon.json 并重启 Docker。
// 合并策略：优先 jq，其次 python3，都没有则失败并提示手动配置（不盲改用户文件）。
// 注意：重启 Docker 会短暂中断该机在跑的容器——由用户在向导勾选开关后才会执行。
func insecureConfigScript(addr string) string {
	return `set -e
ADDR='` + addr + `'
F=/etc/docker/daemon.json
mkdir -p /etc/docker
if command -v jq >/dev/null 2>&1; then
  if [ -f "$F" ]; then
    cp "$F" "$F.bak.infra-ops"
    tmp=$(mktemp)
    jq --arg a "$ADDR" '(.["insecure-registries"] = ((.["insecure-registries"] // []) + [$a] | unique))' "$F" > "$tmp" || { echo "daemon.json 不是合法 JSON，无法自动合并"; exit 3; }
    mv "$tmp" "$F"
  else
    printf '{"insecure-registries":["%s"]}\n' "$ADDR" > "$F"
  fi
elif command -v python3 >/dev/null 2>&1; then
  python3 - "$ADDR" <<'PYEOF'
import json, sys
addr = sys.argv[1]
path = '/etc/docker/daemon.json'
try:
    with open(path) as f:
        cfg = json.load(f)
except Exception:
    cfg = {}
cfg['insecure-registries'] = sorted(set((cfg.get('insecure-registries') or []) + [addr]))
with open(path, 'w') as f:
    json.dump(cfg, f, indent=2)
PYEOF
else
  echo "目标机缺少 jq / python3，无法自动配置；请手动在 /etc/docker/daemon.json 的 insecure-registries 加入 $ADDR 并重启 docker" >&2
  exit 3
fi
echo "已写入 $F，重启 Docker（会短暂中断该机在跑的容器）..."
systemctl restart docker 2>/dev/null || service docker restart || { echo "重启 Docker 失败，请手动检查"; exit 4; }
sleep 3
docker info 2>/dev/null | awk 'tolower($0) ~ /insecure registries:/{f=1;next} /^[^ \t]/{f=0} f' | sed 's/^[[:space:]]*//' | grep -Fxq "$ADDR" || { echo "配置后仍未生效"; exit 5; }
echo "insecure-registries 配置完成: $ADDR"`
}
