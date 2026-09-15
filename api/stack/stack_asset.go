// 套件部署资产：上传 / 服务端代下 / 就绪检查 / 删除。
// 文件本体落盘 data/assets/<key>/<version>/<file_name>；表 stack_assets 只存元数据。
// 套件经 stackkit.AssetProvisioner 声明需求，引擎在部署前经 SFTP 分发（见 stack_asset_push.go）。
package stack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/repo"
	"infra-ops/store/stackkit"
)

// assetHandler 部署资产处理器（与 stackHandler 解耦：资产是通用能力，不依赖部署引擎）。
type assetHandler struct {
	assetRepo *repo.AssetRepo
}

func NewAssetHandler() *assetHandler { return &assetHandler{assetRepo: repo.NewAssetRepo()} }

// assetMaxBytes 单文件上限（256MB）：驱动 jar / 中型离线包足够，防止误传大文件撑爆磁盘。
const assetMaxBytes = 256 << 20

// assetNameRe 资产标识与文件名的合法字符：防路径穿越与意外字符。
var assetNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func assetNameOK(s string) error {
	if s == "" || strings.Contains(s, "..") || !assetNameRe.MatchString(s) {
		return fmt.Errorf("非法资产标识: %q", s)
	}
	return nil
}

func assetDir(key, version string) string { return filepath.Join("data", "assets", key, version) }

func assetFilePath(a *model.StackAsset) string {
	return filepath.Join(assetDir(a.AssetKey, a.Version), a.FileName)
}

// resolveNeeds 取套件声明的资产需求（未实现 AssetProvisioner 返回空）。
func resolveNeeds(stackKey, mode string, params map[string]string) []model.StackAssetNeed {
	d := store.FindBuiltinStack(stackKey)
	if d == nil {
		return nil
	}
	ap, ok := d.(stackkit.AssetProvisioner)
	if !ok {
		return nil
	}
	return ap.RequiredAssets(mode, params)
}

// List GET /api/stacks/assets：已登记资产清单。
func (h *assetHandler) List(c *gin.Context) {
	items, err := h.assetRepo.List()
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if items == nil {
		items = []model.StackAsset{}
	}
	resp.OK(c, items)
}

// Check POST /api/stacks/assets/check：给定套件+模式+参数，返回资产需求与就绪状态（向导第二步渲染）。
func (h *assetHandler) Check(c *gin.Context) {
	var req struct {
		StackKey string            `json:"stack_key"`
		Mode     string            `json:"mode"`
		Params   map[string]string `json:"params"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	stackKey := strings.TrimSpace(req.StackKey)
	if stackKey == "" {
		stackKey = "bigdata"
	}
	needs := resolveNeeds(stackKey, req.Mode, req.Params)
	items := make([]model.StackAssetStatus, 0, len(needs))
	for _, need := range needs {
		st := model.StackAssetStatus{StackAssetNeed: need}
		if a, err := h.assetRepo.GetByKeyVersion(need.Key, need.Version); err == nil && a != nil {
			if _, ferr := os.Stat(assetFilePath(a)); ferr == nil {
				st.Satisfied = true
				st.Asset = a
			}
		}
		items = append(items, st)
	}
	resp.OK(c, gin.H{"items": items})
}

// Upload POST /api/stacks/assets/upload：本地上传入库（multipart：asset_key/version/file_name/file）。
// file_name 为套件声明的期望文件名，与实际上传文件名不一致时拒绝，避免推错版本驱动。
func (h *assetHandler) Upload(c *gin.Context) {
	key := strings.TrimSpace(c.PostForm("asset_key"))
	version := strings.TrimSpace(c.PostForm("version"))
	expectName := strings.TrimSpace(c.PostForm("file_name"))
	for _, s := range []string{key, version} {
		if err := assetNameOK(s); err != nil {
			resp.Fail(c, resp.CodeBadRequest, err.Error())
			return
		}
	}
	fh, err := c.FormFile("file")
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, "缺少文件")
		return
	}
	if fh.Size <= 0 || fh.Size > assetMaxBytes {
		resp.Fail(c, resp.CodeBadRequest, "文件大小超出范围（1B ~ 256MB）")
		return
	}
	if expectName == "" {
		expectName = fh.Filename
	}
	if fh.Filename != expectName {
		resp.Fail(c, resp.CodeBadRequest, fmt.Sprintf("文件名不匹配：期望 %s，实际 %s", expectName, fh.Filename))
		return
	}
	if err := assetNameOK(expectName); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	a, err := storeAssetFile(key, version, expectName, func() (io.ReadCloser, error) { return fh.Open() }, "upload")
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "保存资产失败: "+err.Error())
		return
	}
	resp.OK(c, a)
}

// Fetch POST /api/stacks/assets/fetch：服务端代下。URL 只接受套件声明（FetchURLs）中的地址，
// 请求携带的额外地址一律拒绝——服务端不成为任意 URL 的下载代理。
func (h *assetHandler) Fetch(c *gin.Context) {
	var req struct {
		StackKey string            `json:"stack_key"`
		Mode     string            `json:"mode"`
		Params   map[string]string `json:"params"`
		AssetKey string            `json:"asset_key"`
		Version  string            `json:"version"`
		FileName string            `json:"file_name"`
		URLs     []string          `json:"urls"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	stackKey := strings.TrimSpace(req.StackKey)
	if stackKey == "" {
		stackKey = "bigdata"
	}
	if err := assetNameOK(req.AssetKey); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	if err := assetNameOK(req.Version); err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	var need *model.StackAssetNeed
	for _, n := range resolveNeeds(stackKey, req.Mode, req.Params) {
		if n.Key == req.AssetKey && n.Version == req.Version {
			need = &n
			break
		}
	}
	if need == nil {
		resp.Fail(c, resp.CodeBadRequest, "套件未声明该资产需求，无法代下")
		return
	}
	if req.FileName != "" && req.FileName != need.FileName {
		resp.Fail(c, resp.CodeBadRequest, "文件名与套件声明不一致: "+need.FileName)
		return
	}
	allowed := map[string]bool{}
	for _, u := range need.FetchURLs {
		allowed[u] = true
	}
	urls := req.URLs
	if len(urls) == 0 {
		urls = need.FetchURLs
	}
	for _, u := range urls {
		if !allowed[u] {
			resp.Fail(c, resp.CodeBadRequest, "下载地址不在套件声明的镜像列表内，已拒绝")
			return
		}
	}
	a, err := fetchAssetURLs(req.AssetKey, req.Version, need.FileName, urls, "fetch")
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "服务端下载失败: "+err.Error())
		return
	}
	resp.OK(c, a)
}

// Delete DELETE /api/stacks/assets/:id：删除登记与文件。
func (h *assetHandler) Delete(c *gin.Context) {
	id := int64(0)
	_, _ = fmt.Sscanf(c.Param("id"), "%d", &id)
	items, err := h.assetRepo.List()
	if err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	var target *model.StackAsset
	for i := range items {
		if items[i].ID == id {
			target = &items[i]
			break
		}
	}
	if target == nil {
		resp.Fail(c, resp.CodeNotFound, "资产不存在")
		return
	}
	_ = os.Remove(assetFilePath(target)) // 文件缺失不阻塞登记删除
	if err := h.assetRepo.Delete(id); err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	resp.OK(c, gin.H{"deleted": id})
}

// storeAssetFile 流式写入资产文件（同目录临时文件 + sha256 校验后改名），并登记元数据。
func storeAssetFile(key, version, fileName string, open func() (io.ReadCloser, error), source string) (*model.StackAsset, error) {
	dir := assetDir(key, version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	src, err := open()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hasher), src)
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		return nil, fmt.Errorf("文件为空")
	}

	a := &model.StackAsset{
		AssetKey: key, Version: version, FileName: fileName,
		SizeBytes: size, SHA256: hex.EncodeToString(hasher.Sum(nil)), Source: source,
	}
	final := assetFilePath(a)
	_ = os.Remove(final)
	if err := os.Rename(tmpName, final); err != nil {
		return nil, err
	}
	tmpName = "" // 已改名，清理钩子不再删除
	if err := repo.NewAssetRepo().Upsert(a); err != nil {
		return nil, err
	}
	return a, nil
}

// fetchAssetURLs 依次尝试镜像地址下载资产（服务端代下）。
func fetchAssetURLs(key, version, fileName string, urls []string, source string) (*model.StackAsset, error) {
	var lastErr error
	for _, u := range urls {
		a, err := fetchOneURL(key, version, fileName, u, source)
		if err == nil {
			return a, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无可用下载地址")
	}
	return nil, lastErr
}

func fetchOneURL(key, version, fileName, rawURL, source string) (*model.StackAsset, error) {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	respBody, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer respBody.Body.Close()
	if respBody.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s 返回 %d", hostOf(rawURL), respBody.StatusCode)
	}
	return storeAssetFile(key, version, fileName, func() (io.ReadCloser, error) { return respBody.Body, nil }, source)
}

func hostOf(rawURL string) string {
	u := rawURL
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.Index(u, "/"); i >= 0 {
		u = u[:i]
	}
	return u
}
