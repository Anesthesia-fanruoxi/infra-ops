// import_bulk.go 写入层：把文档按批拼成 NDJSON 提交给 _bulk。
//
// 两件必须自己做、不能推给 ES 的事：
//  1. _bulk 的 Content-Type 是 application/x-ndjson，与其余接口的 application/json 不同；
//  2. **HTTP 200 不代表都成功** —— 部分失败写在响应体的 items[].status 里。
//     只看状态码，会把「整批被 mapping 拒绝」当成导入成功，用户以为数据进去了。
package es

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	importBatchDefault = 500
	importBatchMax     = 5000
	importMaxErrors    = 20 // 最多回传多少条失败明细（再多也没人看，还会撑爆响应）
)

// bulkItemResult _bulk 响应里单条操作的结果。
type bulkItemResult struct {
	Status int `json:"status"`
	Error  *struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	} `json:"error"`
}

type bulkResponse struct {
	Errors bool                        `json:"errors"`
	Items  []map[string]bulkItemResult `json:"items"`
}

// bulkWriter 累积文档并按批写入。
type bulkWriter struct {
	client  *escClient
	index   string
	idField string

	buf     bytes.Buffer
	labels  []string // 与 buf 中条目的出现顺序一一对应，失败时要能指回原始行
	pending int
	batch   int

	Succeeded int64
	Failed    int64
	Batches   int64
	errs      []string
}

// newBulkWriter 构造写入器；idField 非空时用它取值作为文档 _id（重复导入即覆盖）。
func newBulkWriter(c *escClient, index, idField string, batchSize int) *bulkWriter {
	if batchSize <= 0 {
		batchSize = importBatchDefault
	}
	if batchSize > importBatchMax {
		batchSize = importBatchMax
	}
	return &bulkWriter{client: c, index: index, idField: idField, batch: batchSize}
}

// Add 追加一条文档。label 是来源标记（如 "app.log:12"），只用于失败时定位。
func (w *bulkWriter) Add(ctx context.Context, doc map[string]any, label string) error {
	if len(doc) == 0 {
		return nil
	}
	meta := map[string]any{"_index": w.index}
	if w.idField != "" {
		if v, ok := doc[w.idField]; ok && v != nil {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				meta["_id"] = s
			}
		}
	}
	action, err := json.Marshal(map[string]any{"index": meta})
	if err != nil {
		return err
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	w.buf.Write(action)
	w.buf.WriteByte('\n')
	w.buf.Write(body)
	w.buf.WriteByte('\n')
	w.pending++
	w.labels = append(w.labels, label)

	if w.pending >= w.batch {
		return w.Flush(ctx)
	}
	return nil
}

// Flush 提交已累积的文档。批量写入是幂等感知的：单条失败不影响同批其余文档，
// 所以这里只在「整批请求发不出去」时返回错误，单条失败记在 errs 里继续。
func (w *bulkWriter) Flush(ctx context.Context) error {
	if w.pending == 0 {
		return nil
	}
	payload := w.buf.Bytes()
	status, respBody, err := w.client.doRaw(ctx, http.MethodPost, "/_bulk", "application/x-ndjson", payload)
	if err != nil {
		return fmt.Errorf("提交批量写入失败: %w", err)
	}
	if status >= 300 {
		return esErr(status, respBody, "批量写入被拒绝")
	}
	var resp bulkResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("批量写入的响应无法解析: %w", err)
	}

	n := 0
	for _, item := range resp.Items {
		for _, r := range item {
			label := ""
			if n < len(w.labels) {
				label = w.labels[n]
			}
			if r.Status >= 200 && r.Status < 300 {
				w.Succeeded++
			} else {
				w.Failed++
				if len(w.errs) < importMaxErrors {
					w.errs = append(w.errs, label+": "+bulkErrText(r))
				}
			}
			n++
		}
	}
	w.buf.Reset()
	w.labels = w.labels[:0]
	w.pending = 0
	w.Batches++
	return nil
}

// Failures 返回失败明细（最多 importMaxErrors 条）。
func (w *bulkWriter) Failures() []string { return w.errs }

func bulkErrText(r bulkItemResult) string {
	if r.Error == nil {
		return fmt.Sprintf("HTTP %d", r.Status)
	}
	reason := strings.TrimSpace(r.Error.Reason)
	if r := []rune(reason); len(r) > 200 {
		reason = string(r[:200]) + "…"
	}
	if r.Error.Type != "" {
		return r.Error.Type + ": " + reason
	}
	return reason
}
