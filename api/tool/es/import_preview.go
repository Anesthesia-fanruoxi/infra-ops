// import_preview.go 导入预览：先取若干条样本 + 推断字段类型给用户过目。
//
// 这一步不是走形式：ES 的 dynamic mapping 首次出现定终身，而自动推断只能保守
// （status 一条是 200、一条是 "-" 时只能判 keyword）。把样本与推断结果一起摆出来，
// 用户改完再导，比导到一半被 mapping 拒掉、再回头查哪条脏数据要省事得多。
package es

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"infra-ops/api/shared"
	"infra-ops/common/grok"
	"infra-ops/common/resp"
)

// ImportPreview POST /api/es/:id/import/preview
func (h *Handler) ImportPreview(c *gin.Context) {
	var req importReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	client, builder, ok := h.prepareImport(c, &req)
	if !ok {
		return
	}
	sources, total, err := validateSources(req.Sources)
	if err != nil {
		resp.Fail(c, esCodeImportInvalid, err.Error())
		return
	}
	req.Sources = sources
	out, err := previewSource(c.Request.Context(), client, &req, builder)
	if err != nil {
		resp.Fail(c, esCodeImportInvalid, err.Error())
		return
	}
	out["sources"] = sources
	out["size"] = total
	resp.OK(c, out)
}

// ImportExists GET /api/es/:id/import/exists?index=xxx
// 输入目标索引时的即时校验：存在与否直接决定「追加还是新建」，
// 等到真跑才发现 already exists 就太晚了。名字不合法按「不存在 + 无效」回，
// 不报错 —— 用户还在打字，中途状态犯不着弹错误。
func (h *Handler) ImportExists(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	index := strings.TrimSpace(c.Query("index"))
	if !validIndexName(index) {
		resp.OK(c, gin.H{"exists": false, "valid": false})
		return
	}
	exists, err := client.indexExists(c.Request.Context(), index)
	if err != nil {
		resp.Fail(c, esCodeImportInvalid, err.Error())
		return
	}
	resp.OK(c, gin.H{"exists": exists, "valid": true})
}

// ImportCount GET /api/es/:id/import/count?index=xxx
// 目标索引当前文档数：导入完成后前端按「每 5 秒刷新一次、最长 1 分钟」核实是否全部写入
// （ES 写入是近实时的，_bulk 返回成功≠立刻可查；用计数收口比只看写入回执更可信）。
func (h *Handler) ImportCount(c *gin.Context) {
	client, code, msg := h.resolve(shared.ParseID(c))
	if client == nil {
		resp.Fail(c, code, msg)
		return
	}
	index := strings.TrimSpace(c.Query("index"))
	if !validIndexName(index) {
		resp.Fail(c, esCodeImportInvalid, "索引名不合法")
		return
	}
	count, err := client.docCount(c.Request.Context(), index)
	if err != nil {
		resp.Fail(c, esCodeImportInvalid, err.Error())
		return
	}
	resp.OK(c, gin.H{"count": count})
}

// previewRow 预览弹框右栏的一行：原文 + 该行的解析结果（或跳过原因）。
//
// 逐行成对收集，而不是「原文一份、文档一份」分开攒：两边中间一有跳过（未命中 pattern）就会错位，
// 第 3 行的原文可能配上第 2 条文档 —— 逐行渲染必须按记录成对走。
type previewRow struct {
	No     int            `json:"no"`               // 文件内行号
	Origin string         `json:"origin,omitempty"` // 来源文件名 / 压缩包内条目名（多来源时才有）
	Raw    string         `json:"raw"`              // 原文（截断）
	Status string         `json:"status"`           // matched / partial / skipped
	Reason string         `json:"reason,omitempty"` // skipped 的原因
	Fields []previewField `json:"fields"`           // 解析出的字段：保 pattern 顺序；JSON 来源回退为文档键值
}

type previewField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

// previewSource 扫描前若干条记录，返回逐行预览、命中统计与推断出的字段类型。
func previewSource(ctx context.Context, client *escClient, req *importReq, b *recordBuilder) (gin.H, error) {
	src, err := openSources(req.Sources, req.Filter)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	var docs []map[string]any // 参与类型推断；有上限，不会把大文件整个读进来
	var reasons []string
	var rows []previewRow // 逐行预览（弹框右栏），上限 importPreviewRows 条
	scanned, matched, partial, skipped := 0, 0, 0, 0

	for scanned < importPreviewScanMax {
		rec, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			var se *skipError
			if errors.As(err, &se) {
				skipped++
				reasons = appendReason(reasons, se.Error())
				continue
			}
			return nil, err // 真错误（文件读不动等）：直接报，别让用户对着空预览猜
		}
		scanned++
		doc, fields, isPartial, err := b.buildRow(rec)
		if err != nil {
			skipped++
			reasons = appendReason(reasons, err.Error())
			if len(rows) < importPreviewRows {
				rows = append(rows, previewRow{
					No: rec.Line, Origin: rec.Origin, Raw: rawTextOf(rec), Status: "skipped",
					Reason: err.Error(), Fields: []previewField{},
				})
			}
			continue
		}
		if isPartial {
			partial++
		} else {
			matched++
		}
		docs = append(docs, doc)
		if len(rows) < importPreviewRows {
			status := "matched"
			if isPartial {
				status = "partial"
			}
			rows = append(rows, previewRow{
				No: rec.Line, Origin: rec.Origin, Raw: rawTextOf(rec), Status: status, Fields: previewFields(fields, doc),
			})
		}
	}

	exists, _ := client.indexExists(ctx, strings.TrimSpace(req.Index))
	if rows == nil {
		rows = []previewRow{}
	}
	return gin.H{
		"kind":         sourceKind(src),
		"files":        fileNamesOf(src),
		"scanned":      scanned,
		"matched":      matched,
		"partial":      partial,
		"skipped":      skipped,
		"skip_reasons": reasons,
		"rows":         rows,
		"types":        inferTypes(docs),
		"type_options": typeOptions,
		"index":        strings.TrimSpace(req.Index),
		"index_exists": exists,
		"scan_capped":  scanned >= importPreviewScanMax,
	}, nil
}

// previewFields 把一行解析出的字段摊成 name/value 列表。
// grok 字段按 pattern 顺序原样给（预览里就该是这个顺序）；JSON / 整段存文本的模式没有 grok 字段，
// 回退到文档键值（键名排序，保证多次预览的顺序稳定）。
func previewFields(fields []grok.Field, doc map[string]any) []previewField {
	if len(fields) > 0 {
		out := make([]previewField, 0, len(fields))
		for _, f := range fields {
			out = append(out, previewField{Name: f.Name, Value: f.Value, Type: f.Type})
		}
		return out
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]previewField, 0, len(keys))
	for _, k := range keys {
		out = append(out, previewField{Name: k, Value: toStr(doc[k])})
	}
	return out
}

// rawTextOf 取一条记录的原文用于预览展示。**含没解析成功的行** —— 那正是用户要找的问题行。
// 只回纯文本、不加「文件名:行号」前缀（行号已单列展示）；条数由调用方控、单条截断到
// importRawMaxRunes：几 GB 的文件预览也不会拖慢响应。JSON 来源没留存原始文本时回退为紧凑 JSON。
func rawTextOf(rec *record) string {
	text := rec.Text
	if strings.TrimSpace(text) == "" && rec.Doc != nil {
		if b, err := json.Marshal(rec.Doc); err == nil {
			text = string(b)
		}
	}
	text = strings.TrimSpace(text)
	if r := []rune(text); len(r) > importRawMaxRunes {
		text = string(r[:importRawMaxRunes]) + " …"
	}
	return text
}

// appendReason 收集跳过原因，但设上限 —— 几万条都跳过时，回传几万条理由没意义。
func appendReason(list []string, reason string) []string {
	if len(list) >= importMaxSkipReasons {
		return list
	}
	return append(list, reason)
}

// sourceKind 回报来源形态，供界面显示（用户据此判断「它有没有把文件认对」）。
func sourceKind(src recordSource) string {
	switch src.(type) {
	case *zipSource:
		return "archive"
	case *multiSource:
		return "multi"
	case *jsonArraySource, *jsonValueSource:
		return "json"
	default:
		return "text"
	}
}

// fileNamesOf 来源的文件清单（压缩包内条目 / 多选文件），用于在结果里按文件对账。
func fileNamesOf(src recordSource) []string {
	switch s := src.(type) {
	case *zipSource:
		out := make([]string, 0, len(s.files))
		for _, f := range s.files {
			out = append(out, f.name)
		}
		return out
	case *multiSource:
		out := make([]string, 0, len(s.paths))
		for _, p := range s.paths {
			out = append(out, filepath.Base(p))
		}
		return out
	}
	return []string{}
}

// indexExists 用 HEAD 探测索引是否存在。探测失败（权限不足等）按「未知」处理，
// 不拦流程 —— 这个信息只用来决定界面上默认选「新建」还是「指定已有」。
func (c *escClient) indexExists(ctx context.Context, index string) (bool, error) {
	status, body, err := c.do(ctx, http.MethodHead, "/"+pathEscape(index), nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, esErr(status, body, "探测索引失败")
}
