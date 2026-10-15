// import_run.go 导入主流程：读 → 加工 → 批量写 → 汇报。
//
// 全程同步执行（前端 timeout: 0），进度走 progress_key 轮询、可取消。
// 取消靠 ctx：流式读取与 bulk 提交都监听它，取消后当场中断，已写入的部分保留
// —— 回滚几万条 bulk 既慢又不可靠，如实告诉用户写到哪儿了更有用。
package es

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
)

// 进度刷新间隔：每条都更新一次只是为展示精度反复抢锁，没必要。
const importProgressEvery = 500

// importMaxSkipSamples 导入结果里保留多少条「被跳过的原始文本」示例（去重后）：
// 供「用跳过样本重新生成 pattern」这类补救动作使用。
const importMaxSkipSamples = 20

// importSkipSampleRunes 单条跳过样例的截断长度（rune）：日志行可能很长，
// 只取能看清格式的前缀即可，重新生成时也够用。
const importSkipSampleRunes = 500

// importSkipPauseAfter 跳过条数阈值：超过即暂停导入一次，等用户决定「重新生成 / 继续」。
const importSkipPauseAfter = 20

// errImportPausedAbort 暂停时用户选择「中止转重新生成」：与主动取消区分开
// （前端要静默接住它，转入补救链而不是弹错误）。
var errImportPausedAbort = errors.New("已中止本次导入，转入重新生成解析规则")

// errImportCanceled 用于让上层区分「用户取消」与「真出错」。
var errImportCanceled = errors.New("导入已取消")

// importResult 导入结果；预检阶段只填 source/size/index 几项。
type importResult struct {
	Source        string   `json:"source"`
	Index         string   `json:"index"`
	Read          int64    `json:"read"`    // 从文件读出的记录数
	Indexed       int64    `json:"indexed"` // 成功写入 ES 的条数
	Failed        int64    `json:"failed"`  // 被 ES 拒绝的条数
	Skipped       int64    `json:"skipped"` // 未匹配 pattern / 坏数据
	Partial       int64    `json:"partial"` // 只局部命中 pattern 的条数
	Batches       int64    `json:"batches"`
	Bytes         int64    `json:"bytes"` // 源文件大小
	ElapsedMs     int64    `json:"elapsed_ms"`
	Created       bool     `json:"created"`      // 是否新建了索引
	Recreated     bool     `json:"recreated"`    // 是否删除并重建了索引
	CountBefore   int64    `json:"count_before"` // 导入前索引文档数（写入量核实用；新建/重建为 0）
	Files         []string `json:"files"`        // 压缩包内的文件清单
	Failures      []string `json:"failures"`
	SkipReasons   []string `json:"skip_reasons"`
	SkipSamples   []string `json:"skip_samples"`   // 被跳过的原始文本示例（重新生成 pattern 用）
	DroppedFields []string `json:"dropped_fields"` // 按声明类型转不掉、被丢掉的字段
	Truncated     bool     `json:"failures_truncated"`
}

// Import POST /api/es/:id/import
//
// 两阶段：confirmed=false 只回「哪个文件、多大、进哪个索引」让人确认；
// confirmed=true 才真跑。导入是批量写操作，一次点击挡不住误选文件。
func (h *Handler) Import(c *gin.Context) {
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
	idx := strings.TrimSpace(req.Index)

	if !req.Confirmed {
		exists, _ := client.indexExists(c.Request.Context(), idx)
		resp.OK(c, gin.H{
			"need_confirm": true,
			"sources":      sources,
			"size":         total,
			"index":        idx,
			"index_exists": exists,
			"create_index": req.CreateIndex,
		})
		return
	}

	// 取消登记用 progress_key：同一连接并发导入时不会互相顶掉凭据
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	entry := h.cancels.register(req.ProgressKey, cancel)
	defer h.cancels.finish(req.ProgressKey, entry)
	handle := h.imports.begin(req.ProgressKey)

	res, err := h.runImport(ctx, client, &req, builder, handle, total)
	if err != nil {
		handle.Finish(err, res.Read, res.Indexed, res.Failed, res.Skipped, res.Batches, res.Bytes)
		if errors.Is(err, errImportCanceled) || errors.Is(err, errImportPausedAbort) {
			resp.Fail(c, esCodeImportCanceled, err.Error())
			return
		}
		resp.Fail(c, esCodeImportInvalid, err.Error())
		return
	}
	handle.Finish(nil, res.Read, res.Indexed, res.Failed, res.Skipped, res.Batches, res.Bytes)
	resp.OK(c, res)
}

func (h *Handler) runImport(ctx context.Context, client *escClient, req *importReq,
	b *recordBuilder, handle *importHandle, fileSize int64) (*importResult, error) {

	idx := strings.TrimSpace(req.Index)
	label := ""
	if len(req.Sources) == 1 {
		label = req.Sources[0]
	} else if len(req.Sources) > 1 {
		label = fmt.Sprintf("共 %d 个文件", len(req.Sources))
	}
	res := &importResult{Source: label, Index: idx, Bytes: fileSize}
	start := time.Now()
	defer func() { res.ElapsedMs = time.Since(start).Milliseconds() }()

	// 导入前文档数（追加场景用于核实写入量；重建 / 新建场景按 0 计）
	if !req.RecreateIndex {
		if n, err := client.docCount(ctx, idx); err == nil {
			res.CountBefore = n
		}
	}
	if req.RecreateIndex {
		// 复用索引管理的 deleteIndex（不存在时报错），先探测再删，语义干净
		if exists, _ := client.indexExists(ctx, idx); exists {
			if err := client.deleteIndex(ctx, idx); err != nil {
				return res, err
			}
			// 防御：删除是同步确认的，但用户担心 ES 近实时语义下旧数据有残留可能；
			// 建新索引前再探一次，确认旧索引彻底消失（避免新旧数据混写）
			if still, _ := client.indexExists(ctx, idx); still {
				return res, fmt.Errorf("索引 %s 删除后仍存在，已中止重建（避免新旧数据混写）", idx)
			}
		}
		res.Recreated = true
	}
	if req.CreateIndex || req.RecreateIndex {
		created, err := ensureIndexWithMapping(ctx, client, idx, req.Types)
		if err != nil {
			return res, err
		}
		res.Created = created
	}

	src, err := openSources(req.Sources, req.Filter)
	if err != nil {
		return res, err
	}
	defer src.Close()
	res.Files = nonNil(fileNamesOf(src))

	// 抽屉里的文件清单：压缩包看条目、多选看各文件；单文件也列出来（一行，进度直观）
	displayFiles := fileNamesOf(src)
	if len(displayFiles) == 0 && len(req.Sources) == 1 {
		displayFiles = []string{filepath.Base(req.Sources[0])}
	}
	handle.Files(displayFiles)
	// 多文件（或压缩包多条目）时给每条文档打来源：resource = 来源文件（压缩包内为条目路径），
	// 否则导入完只知道「共 N 条」，不知道哪条来自哪个文件
	addResource := len(displayFiles) > 1

	writer := newBulkWriter(client, idx, strings.TrimSpace(req.IDField), req.BatchSize)
	var skipped, partial int64
	var skipReasons, skipSamples, dropped []string
	pauseDone := false
	lastStep := 0
	curOrigin := ""
	// 中途出错 / 取消时也要如实汇报「写到哪儿了」：取消消息与收尾快照都读 res 的计数，
	// 而这几个字段原本要到成功收尾才赋值（取消时明明 bulk 进去几万条，消息里却是 0 条）。
	snapshot := func() {
		res.Indexed = writer.Succeeded
		res.Failed = writer.Failed
		res.Skipped = skipped
		res.Batches = writer.Batches
	}

	for {
		if err := ctx.Err(); err != nil {
			snapshot()
			return res, fmt.Errorf("%w（已确认写入 %d 条）", errImportCanceled, res.Indexed)
		}
		rec, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			var se *skipError
			if errors.As(err, &se) {
				skipped++
				skipReasons = appendReason(skipReasons, se.Error())
				continue
			}
			snapshot()
			return res, err
		}
		res.Read++
		if rec.Origin != curOrigin {
			curOrigin = rec.Origin
			handle.Origin(curOrigin)
		}
		// 文件级游标（压缩包条目 / 多选文件）：抽屉里逐项从「转圈」变「打勾」
		if fs, ok := src.(fileStepper); ok {
			if cur, _ := fs.FileCursor(); cur != lastStep {
				lastStep = cur
				handle.FileStep(cur)
			}
		}
		doc, isPartial, err := b.build(rec)
		if err != nil {
			skipped++
			skipReasons = appendReason(skipReasons, err.Error())
			skipSamples = appendSkipSample(skipSamples, rec)
			// 跳过超阈值：暂停一次等用户决策（继续 = 不再二次暂停；中止 = 转重新生成）
			if !pauseDone && skipped > importSkipPauseAfter {
				pauseDone = true
				decision, answered := h.pauseForDecision(ctx, req.ProgressKey, handle, skipped, skipSamples)
				if !answered {
					snapshot()
					return res, fmt.Errorf("%w（已确认写入 %d 条）", errImportCanceled, res.Indexed)
				}
				if decision == "abort" {
					snapshot()
					return res, fmt.Errorf("%w（已确认写入 %d 条，将删除索引重写）", errImportPausedAbort, res.Indexed)
				}
			}
			continue
		}
		if isPartial {
			partial++
		}
		// 字段映射（预览第二列）：替换第一列字段名后再按类型转换
		applyFieldRenames(doc, req.Renames)
		// 多来源时给每条文档打上 resource=来源文件；文档里已有同名字段则不覆盖（解析结果优先）
		if addResource {
			if _, exists := doc["resource"]; !exists {
				doc["resource"] = rec.Origin
			}
		}
		// 按用户确认过的类型转换：转不掉的字段被丢掉（否则 ES 会拒掉整条文档）
		dropped = appendUnique(dropped, coerceDoc(doc, req.Types)...)
		if err := writer.Add(ctx, doc, importLabel(rec)); err != nil {
			snapshot()
			return res, err
		}
		if res.Read%importProgressEvery == 0 {
			handle.Running(res.Read, writer.Succeeded, writer.Failed, skipped, writer.Batches, fileSize)
		}
	}
	if err := writer.Flush(ctx); err != nil {
		snapshot()
		return res, err
	}

	res.Partial = partial
	snapshot()
	res.Failures = nonNil(writer.Failures())
	res.SkipReasons = nonNil(skipReasons)
	res.SkipSamples = nonNil(skipSamples)
	res.DroppedFields = nonNil(dropped)
	res.Truncated = writer.Failed > int64(len(writer.Failures()))
	return res, nil
}

// pauseForDecision 跳过超阈值时挂起导入等用户决策（进度状态置 Waiting + 样例）：
// continue = 继续导入；abort = 中止（前端转「重新生成 + 删除重建」）。
// 第二个返回值为 false 表示等待期间被取消（ctx 结束），调用方按用户取消处理。
func (h *Handler) pauseForDecision(ctx context.Context, key string, handle *importHandle,
	skipped int64, samples []string) (string, bool) {
	handle.Pause(skipped, samples)
	defer handle.Resume()
	ch := h.pauses.register(key)
	defer h.pauses.unregister(key)
	select {
	case decision := <-ch:
		return decision, true
	case <-ctx.Done():
		return "", false
	}
}

// appendSkipSample 记下被跳过记录的原始文本（取前 importMaxSkipSamples 条，单条截断）：
// 供「暂停决策 / 结果补救」把漏网行喂回 AI 重新生成 pattern。按用户口径不去重，先到先得。
func appendSkipSample(list []string, r *record) []string {
	if len(list) >= importMaxSkipSamples {
		return list
	}
	t := strings.TrimSpace(r.Text)
	if t == "" {
		return list
	}
	if rr := []rune(t); len(rr) > importSkipSampleRunes {
		t = string(rr[:importSkipSampleRunes]) + " …"
	}
	return append(list, t)
}

// importLabel 失败的条数要能指回「哪个文件第几行」，否则几千条里挑不出问题那行。
func importLabel(r *record) string {
	if r.Origin != "" {
		return fmt.Sprintf("%s:%d", r.Origin, r.Line)
	}
	return fmt.Sprintf("第 %d 行", r.Line)
}

func appendUnique(list []string, items ...string) []string {
	for _, it := range items {
		seen := false
		for _, ex := range list {
			if ex == it {
				seen = true
				break
			}
		}
		if !seen {
			list = append(list, it)
		}
	}
	return list
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
