// import.go 数据导入：把本地文件（JSON / 文本 / 压缩包，可一次选多份）读进 ES 索引。
//
// 交互照抄 mysql/import.go 的两阶段：先探一次回「哪些文件、多大、进哪个索引」，
// 让人确认后再真跑（前端 timeout: 0），进度走 progress_key 轮询、可取消。
// 区别是这里多了一层预处理：提字段（grok pattern）→ 按声明类型转换。
//
// 只支持桌面端：文件路径由原生对话框取得，Go 侧流式读。压缩包动辄几个 GB，
// 走 webview 的 multipart 会被整段缓冲在内存（见 desktop/file_service.go 的文件头说明）。
package es

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/resp"
)

const (
	importPreviewScanMax  = 500  // 预览最多扫描多少条（推断类型的样本量）
	importPreviewRows     = 20   // 回传的逐行预览条数（预览弹框右栏「解析预览」）
	importRawMaxRunes     = 4000 // 单条原始文本截断长度（rune，防止日志单行超长拖爆响应）
	importMaxSkipReasons  = 10
	importSourceMaxBytes  = 20 << 30 // 单文件 20GB 上限：再大就不是「导入日志」的场景了
	importMaxSourceFiles  = 200      // 一次最多选这么多文件：更多的请先打包成压缩包
	importIndexNameMaxLen = 255
)

// importReq 预览与导入共用的请求体。
type importReq struct {
	Sources       []string          `json:"sources"`        // 本地文件绝对路径列表（原生对话框/拖拽取得）
	Index         string            `json:"index"`          // 目标索引
	CreateIndex   bool              `json:"create_index"`   // true = 新建（按 types 建 mapping）
	RecreateIndex bool              `json:"recreate_index"` // true = 先删除既有索引再重建导入（不可撤销）
	Types         map[string]string `json:"types"`          // 字段类型：建 mapping 与写入转换都用它
	Renames       map[string]string `json:"renames"`        // 字段映射（预览第二列）：源字段名 → 目标字段名（空 = 不改）
	Patterns      []string          `json:"patterns"`       // grok pattern；为空则整段文本存 message 字段
	Definitions   map[string]string `json:"definitions"`    // 自定义 pattern 定义
	Filter        string            `json:"filter"`         // 压缩包内条目过滤（空 = 全部）：见 zipEntryMatch
	IDField       string            `json:"id_field"`       // 取该字段值作为 _id；空 = ES 自动生成
	BatchSize     int               `json:"batch_size"`
	Confirmed     bool              `json:"confirmed"`    // 预检（false）→ 真跑（true）
	ProgressKey   string            `json:"progress_key"` // 进度与取消的标识
}

// prepareImport 校验连接、索引名与提取规则，返回客户端与文档加工器。
// 已应答错误时 ok=false，调用方直接返回。
func (h *Handler) prepareImport(c *gin.Context, req *importReq) (*escClient, *recordBuilder, bool) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return nil, nil, false
	}
	if idx := strings.TrimSpace(req.Index); !validIndexName(idx) {
		resp.Fail(c, esCodeImportInvalid,
			"索引名不合法（只能用小写字母、数字与 - _ .，且不能以 - _ + 开头）")
		return nil, nil, false
	}
	builder, err := newRecordBuilder(req.Patterns, req.Definitions)
	if err != nil {
		resp.Fail(c, esCodeImportInvalid, err.Error())
		return nil, nil, false
	}
	// 字段映射（预览第二列）：校验冲突并折算成「最终字段名 → 类型」，
	// 之后建 mapping 与写入转换都以最终名为准
	finalTypes, renames, err := applyFieldPlan(req.Types, req.Renames)
	if err != nil {
		resp.Fail(c, esCodeImportInvalid, err.Error())
		return nil, nil, false
	}
	req.Types = finalTypes
	req.Renames = renames
	return client, builder, true
}

// validIndexName 按 ES 的索引命名约束粗略校验。
// 交给 ES 校验也行，但那样报错要等一个来回，而且提示是英文的。
func validIndexName(s string) bool {
	if s == "" || len(s) > importIndexNameMaxLen || s == "." || s == ".." {
		return false
	}
	if s != strings.ToLower(s) {
		return false
	}
	switch s[0] {
	case '-', '_', '+':
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '+', r == '.':
		default:
			return false
		}
	}
	return true
}

// importSourceInfo 校验待导入文件并返回基本信息。
func importSourceInfo(path string) (os.FileInfo, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return nil, fmt.Errorf("请先选择要导入的文件")
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("文件不存在或无法访问: %s", filepath.Base(p))
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("选中的是目录，请选择文件")
	}
	if fi.Size() > importSourceMaxBytes {
		return nil, fmt.Errorf("文件 %.1f GB 超过 %d GB 上限，请先自行切分",
			float64(fi.Size())/(1<<30), importSourceMaxBytes>>30)
	}
	return fi, nil
}

// validateSources 校验一次选择的多份文件（数量上限 + 逐个存在性/大小），
// 返回规范化后的路径列表与总字节数。哪份文件有问题直接点名，别让整个导入跑一半才炸。
func validateSources(sources []string) ([]string, int64, error) {
	paths := make([]string, 0, len(sources))
	var total int64
	for _, s := range sources {
		p := strings.TrimSpace(s)
		if p == "" {
			continue
		}
		fi, err := importSourceInfo(p)
		if err != nil {
			return nil, 0, err
		}
		paths = append(paths, p)
		total += fi.Size()
	}
	if len(paths) == 0 {
		return nil, 0, fmt.Errorf("请先选择要导入的文件")
	}
	if len(paths) > importMaxSourceFiles {
		return nil, 0, fmt.Errorf("一次最多 %d 个文件，更多的请先打包成压缩包", importMaxSourceFiles)
	}
	return paths, total, nil
}
