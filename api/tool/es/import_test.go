package es

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/repo"
)

// fakeES 一个够用的假 ES：_bulk 逐条回状态、索引探测/创建/删除/计数各按需应答。
type fakeES struct {
	mu         sync.Mutex
	bulkCalls  int
	lastBody   string
	seq        int            // 全局条目序号，用于「第 N 条拒绝」
	reject     map[int]string // 序号 → 拒绝原因（1-based）
	failBulkAt int            // 第 N 次 bulk 整批拒绝（HTTP 500）
	exists     map[string]bool
	docs       map[string]int64 // 索引路径 → 文档数（_count 用）
	deleted    []string         // DELETE 过的索引路径（重建导入断言用）
	putCalls   int
	lastPut    string
	failPut    bool
}

func newFakeES() *fakeES {
	return &fakeES{reject: map[int]string{}, exists: map[string]bool{}}
}

func (f *fakeES) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/_bulk", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.bulkCalls++
		f.lastBody = string(body)
		if f.failBulkAt > 0 && f.bulkCalls == f.failBulkAt {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":{"type":"cluster_block_exception","reason":"index read-only"}}`)
			return
		}

		lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
		count := len(lines) / 2 // 每两条 NDJSON 行（动作行 + 文档行）对应一条文档
		items := make([]string, 0, count)
		for i := 0; i < count; i++ {
			f.seq++
			if reason, ok := f.reject[f.seq]; ok {
				items = append(items, fmt.Sprintf(
					`{"index":{"status":400,"error":{"type":"mapper_parsing_exception","reason":%q}}}`, reason))
				continue
			}
			items = append(items, `{"index":{"status":201}}`)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"took":3,"errors":false,"items":[%s]}`, strings.Join(items, ","))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodHead:
			if f.exists[r.URL.Path] {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			f.putCalls++
			f.lastPut = string(body)
			if f.failPut {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"error":{"type":"resource_already_exists_exception","reason":"index already exists"}}`)
				return
			}
			f.exists[r.URL.Path] = true
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"acknowledged":true}`)
		case http.MethodDelete:
			f.deleted = append(f.deleted, r.URL.Path)
			delete(f.exists, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"acknowledged":true}`)
		case http.MethodGet:
			if strings.HasSuffix(r.URL.Path, "/_count") {
				fmt.Fprintf(w, `{"count":%d}`, f.docs[strings.TrimSuffix(r.URL.Path, "/_count")])
				return
			}
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{}`)
		}
	})
	return mux
}

func importSetup(t *testing.T) (*gin.Engine, int64, *fakeES) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := store.Open(filepath.Join("data", "infra-ops.db")); err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(store.Close)

	fes := newFakeES()
	srv := httptest.NewServer(fes.handler())
	t.Cleanup(srv.Close)

	esRepo := repo.NewESRepo()
	connID, err := esRepo.Create(&model.ESConn{Name: "import-test", URL: srv.URL, AuthType: "anonymous"})
	if err != nil {
		t.Fatalf("建连接失败: %v", err)
	}

	h := NewHandler(esRepo, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/es/:id/import/preview", h.ImportPreview)
	r.GET("/api/es/:id/import/exists", h.ImportExists)
	r.GET("/api/es/:id/import/count", h.ImportCount)
	r.POST("/api/es/:id/import", h.Import)
	r.GET("/api/es/:id/import/progress", h.ImportProgress)
	r.POST("/api/es/:id/import/resume", h.ImportResume)
	r.POST("/api/es/:id/import/cancel", h.ImportCancel)
	return r, connID, fes
}

func importDo(t *testing.T, r *gin.Engine, method, path string, body any) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON: %s", w.Body.String())
	}
	return out
}

func dataOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	if out["code"] != float64(0) {
		t.Fatalf("接口返回失败: %v", out)
	}
	d, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 data: %v", out)
	}
	return d
}

// TestImportExists 输入目标索引时的即时校验：存在与否决定「追加还是新建」。
func TestImportExists(t *testing.T) {
	r, id, fes := importSetup(t)

	// 不存在的索引：exists=false 且名字有效
	d := dataOf(t, importDo(t, r, http.MethodGet,
		fmt.Sprintf("/api/es/%d/import/exists?index=logs-fresh", id), nil))
	if d["exists"] != false || d["valid"] != true {
		t.Fatalf("不存在的索引应回 exists=false/valid=true: %v", d)
	}

	// 把索引标记为已存在（fakeES 的 HEAD 探测按 path 查表）
	fes.mu.Lock()
	fes.exists["/logs-here"] = true
	fes.mu.Unlock()
	d = dataOf(t, importDo(t, r, http.MethodGet,
		fmt.Sprintf("/api/es/%d/import/exists?index=logs-here", id), nil))
	if d["exists"] != true {
		t.Fatalf("已存在的索引应回 exists=true: %v", d)
	}

	// 名字不合法：不报错，回 exists=false + valid=false（用户还在打字，中途状态别弹错误）
	d = dataOf(t, importDo(t, r, http.MethodGet,
		fmt.Sprintf("/api/es/%d/import/exists?index=Bad-Name", id), nil))
	if d["exists"] != false || d["valid"] != false {
		t.Fatalf("不合法索引名应回 exists=false/valid=false: %v", d)
	}
}

func TestImportPreviewSamplesAndTypes(t *testing.T) {
	r, id, _ := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n{\"a\":2}\n{\"b\":\"x\"}\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import/preview", id),
		map[string]any{"sources": []string{p}, "index": "logs"}))

	if d["kind"] != "json" {
		t.Fatalf("应识别为 JSON 来源，实际 %v", d["kind"])
	}
	if d["scanned"] != float64(3) || len(d["rows"].([]any)) != 3 {
		t.Fatalf("应扫描并回传 3 条预览行: scanned=%v rows=%v", d["scanned"], d["rows"])
	}
	types := d["types"].(map[string]any)
	if types["a"] != "long" || types["b"] != "keyword" {
		t.Fatalf("类型推断不对: %v", types)
	}
	if d["index_exists"] != false {
		t.Fatalf("索引不存在时 index_exists 应为 false，实际 %v", d["index_exists"])
	}
	if len(d["type_options"].([]any)) == 0 {
		t.Fatal("应回传可选类型清单供界面下拉")
	}
	// 逐行预览：JSON 来源没有留存原始文本，原文回退为紧凑 JSON，字段取文档键值
	rows := d["rows"].([]any)
	first := rows[0].(map[string]any)
	if first["status"] != "matched" || !strings.Contains(first["raw"].(string), `{"a":1}`) {
		t.Fatalf("JSON 行应以原文(紧凑 JSON)+matched 回传: %v", first)
	}
	f0 := first["fields"].([]any)[0].(map[string]any)
	if f0["name"] != "a" || f0["value"] != "1" {
		t.Fatalf("JSON 行字段应为文档键值: %v", first["fields"])
	}
}

// TestImportPreviewReportsSkipReasons 未匹配 pattern 的样例要被跳过并说明原因，
// 否则用户看到「扫了 3 条、0 条样本」会完全不知道问题出在规则上。
func TestImportPreviewReportsSkipReasons(t *testing.T) {
	r, id, _ := importSetup(t)
	p := writeTemp(t, "a.log", "GET /a 200\n这条不匹配\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import/preview", id),
		map[string]any{
			"sources": []string{p}, "index": "logs",
			"patterns": []string{`%{WORD:method} %{URIPATHPARAM:path} %{NUMBER:status:int}`},
		}))

	if d["matched"] != float64(1) || d["skipped"] != float64(1) {
		t.Fatalf("应 1 命中 1 跳过，实际 matched=%v skipped=%v", d["matched"], d["skipped"])
	}
	reasons := d["skip_reasons"].([]any)
	if len(reasons) != 1 || !strings.Contains(reasons[0].(string), "第 2 行") {
		t.Fatalf("跳过原因要带行号: %v", reasons)
	}
	// 逐行预览必须**含没解析成功的那行**（status=skipped + 原因）；原文只回纯文本正文，不带前缀
	rows := d["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("应逐行回传 2 条预览: %v", rows)
	}
	ok := rows[0].(map[string]any)
	bad := rows[1].(map[string]any)
	if ok["raw"] != "GET /a 200" || ok["status"] != "matched" {
		t.Fatalf("首行应是整行命中的原文: %v", ok)
	}
	if bad["raw"] != "这条不匹配" || bad["status"] != "skipped" || !strings.Contains(bad["reason"].(string), "第 2 行") {
		t.Fatalf("未命中行应带原文与原因: %v", bad)
	}
	// 命中行的字段按 pattern 顺序（method、path、status）—— 逐行展示要求保序，不能是 map 的随机序
	okFields := ok["fields"].([]any)
	if len(okFields) != 3 || okFields[0].(map[string]any)["name"] != "method" ||
		okFields[2].(map[string]any)["name"] != "status" {
		t.Fatalf("字段应按 pattern 顺序: %v", okFields)
	}
}

func TestImportNeedConfirmFirst(t *testing.T) {
	r, id, fes := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "logs"}))

	if d["need_confirm"] != true {
		t.Fatalf("未确认时只应回待确认信息，实际 %v", d)
	}
	if d["index"] != "logs" || d["size"] == nil {
		t.Fatalf("待确认信息要含目标索引与文件大小: %v", d)
	}
	if fes.bulkCalls != 0 {
		t.Fatal("未确认前绝不能真的写数据")
	}
}

func TestImportWritesBulk(t *testing.T) {
	r, id, fes := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "logs", "confirmed": true, "progress_key": "k1"}))

	if d["read"] != float64(3) || d["indexed"] != float64(3) || d["failed"] != float64(0) {
		t.Fatalf("应读 3 写 3，实际 %v", d)
	}
	if d["created"] != false {
		t.Fatal("未要求新建索引时不该建")
	}
	if fes.bulkCalls != 1 {
		t.Fatalf("3 条应合并成 1 次 bulk 提交，实际 %d 次", fes.bulkCalls)
	}
	if !strings.Contains(fes.lastBody, `"a":1`) {
		t.Fatalf("提交内容应含文档本身:\n%s", fes.lastBody)
	}
}

// TestImportCreatesIndexWithMapping 新建索引要带上用户确认过的字段类型 ——
// 全靠动态映射的话，第一批文档定下的类型会与用户预期不一致。
func TestImportCreatesIndexWithMapping(t *testing.T) {
	r, id, fes := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"status\":\"200\"}\n")
	dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{
			"sources": []string{p}, "index": "logs", "create_index": true, "confirmed": true,
			"progress_key": "k1",
			"types":        map[string]string{"status": "long"},
		}))

	if fes.putCalls != 1 {
		t.Fatalf("应创建一次索引，实际 %d 次", fes.putCalls)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(fes.lastPut), &body); err != nil {
		t.Fatalf("建索引请求体不是 JSON: %s", fes.lastPut)
	}
	mappings := body["mappings"].(map[string]any)
	props := mappings["properties"].(map[string]any)
	status := props["status"].(map[string]any)
	if status["type"] != "long" {
		t.Fatalf("mapping 应按确认过的类型建: %s", fes.lastPut)
	}
	// 值也要按声明类型转换，否则 "200" 进去还是字符串
	if !strings.Contains(fes.lastBody, `"status":200`) {
		t.Fatalf("写入值应按声明类型转换:\n%s", fes.lastBody)
	}
}

// TestImportCreateIndexSkippedWhenExists 「不存在时创建」：索引已存在时跳过创建直接追加。
// 重复往同一索引导入是常态（第一批建好索引，第二批追加），勾选框还勾着也不能 PUT 一发
// already exists 把整个导入打断 —— 确认框上写的可是「已存在，数据追加」。
func TestImportCreateIndexSkippedWhenExists(t *testing.T) {
	r, id, fes := importSetup(t)
	fes.mu.Lock()
	fes.exists["/logs"] = true
	fes.mu.Unlock()
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{
			"sources": []string{p}, "index": "logs", "create_index": true, "confirmed": true,
			"progress_key": "k1",
		}))

	if fes.putCalls != 0 {
		t.Fatalf("索引已存在时不该再 PUT 创建，实际 %d 次", fes.putCalls)
	}
	if d["created"] != false || d["indexed"] != float64(1) {
		t.Fatalf("应跳过创建并追加写入（created=false / indexed=1），实际 %v", d)
	}
}

// TestImportFailureKeepsReportedCounts 中途失败时收尾快照要保留已确认的写入数。
// 曾把 res 计数留到成功收尾才赋值：中途失败/取消时说「已写入 0 条」，
// 哪怕前几批早已 bulk 成功（用户据此以为一条都没进去，多半会重复导入）。
func TestImportFailureKeepsReportedCounts(t *testing.T) {
	r, id, fes := importSetup(t)
	fes.failBulkAt = 2 // 第 2 批整批拒绝（第 1 批已成功）
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n")
	out := importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "logs", "confirmed": true,
			"progress_key": "k1", "batch_size": 1})
	if out["code"] == float64(0) {
		t.Fatalf("第 2 批被拒时应整体报错，实际 %v", out)
	}
	d := dataOf(t, importDo(t, r, http.MethodGet,
		fmt.Sprintf("/api/es/%d/import/progress?key=k1", id), nil))
	st := d["status"].(map[string]any)
	if st["phase"] != "error" || st["indexed"] != float64(1) {
		t.Fatalf("失败收尾要保留已确认写入数（indexed=1），实际 %v", st)
	}
}

// TestImportReportsPerItemFailures 部分条目被 ES 拒绝：结果里要如实报数并给出明细，
// 不能因为 HTTP 是 200 就当成全部成功。
func TestImportReportsPerItemFailures(t *testing.T) {
	r, id, fes := importSetup(t)
	fes.reject[2] = "failed to parse field [status]"
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "logs", "confirmed": true, "progress_key": "k1"}))

	if d["indexed"] != float64(2) || d["failed"] != float64(1) {
		t.Fatalf("应按逐条状态计数：写 2 拒 1，实际 %v", d)
	}
	fails := d["failures"].([]any)
	if len(fails) != 1 || !strings.Contains(fails[0].(string), "failed to parse field") {
		t.Fatalf("要回传失败明细: %v", fails)
	}
	if !strings.Contains(fails[0].(string), ":2") {
		t.Fatalf("明细要能指回原始行: %v", fails)
	}
}

func TestImportProgressAfterRun(t *testing.T) {
	r, id, _ := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n")
	importDo(t, r, http.MethodPost, fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "logs", "confirmed": true, "progress_key": "k1"})

	d := dataOf(t, importDo(t, r, http.MethodGet,
		fmt.Sprintf("/api/es/%d/import/progress?key=k1", id), nil))
	if d["found"] != true || d["active"] != false {
		t.Fatalf("已结束的任务应 found=true / active=false，实际 %v", d)
	}
	st := d["status"].(map[string]any)
	if st["phase"] != "done" || st["indexed"] != float64(1) {
		t.Fatalf("收尾状态不对: %v", st)
	}

	d2 := dataOf(t, importDo(t, r, http.MethodGet,
		fmt.Sprintf("/api/es/%d/import/progress?key=nope", id), nil))
	if d2["found"] != false {
		t.Fatal("查不到的 key 应如实回 found=false（前端据此停止轮询）")
	}
}

func TestImportRejectsBadIndexName(t *testing.T) {
	r, id, _ := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n")
	out := importDo(t, r, http.MethodPost, fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "Bad Index", "confirmed": true})
	if out["code"] != float64(esCodeImportInvalid) {
		t.Fatalf("非法索引名应被拒（%d），实际 %v", esCodeImportInvalid, out)
	}
}

func TestImportRejectsMissingFile(t *testing.T) {
	r, id, _ := importSetup(t)
	out := importDo(t, r, http.MethodPost, fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{filepath.Join(t.TempDir(), "nope.jsonl")}, "index": "logs"})
	if out["code"] != float64(esCodeImportInvalid) {
		t.Fatalf("文件不存在应被拒（%d），实际 %v", esCodeImportInvalid, out)
	}
}

// TestImportCancelUnknownKey 取消一个不存在的任务不该报错，只是没取消到东西。
func TestImportCancelUnknownKey(t *testing.T) {
	r, id, _ := importSetup(t)
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import/cancel?key=nope", id), nil))
	if d["cancelled"] != false {
		t.Fatalf("未知任务应回 cancelled=false，实际 %v", d)
	}
}

// TestImportRecreateDeletesAndRebuilds 重建导入：先 DELETE 旧索引、再 PUT 带 mapping 的新索引，
// 结果标记 recreated 且 count_before 按 0 计（写入量核实的期望值 = indexed）。
func TestImportRecreateDeletesAndRebuilds(t *testing.T) {
	r, id, fes := importSetup(t)
	fes.exists["/logs"] = true
	fes.docs = map[string]int64{"/logs": 123}
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{
			"sources": []string{p}, "index": "logs", "recreate_index": true, "confirmed": true,
			"progress_key": "k1", "types": map[string]string{"a": "long"},
		}))

	if d["recreated"] != true || d["count_before"] != float64(0) {
		t.Fatalf("重建应标记 recreated 且 before=0: %v", d)
	}
	if len(fes.deleted) != 1 || fes.deleted[0] != "/logs" {
		t.Fatalf("应先删除旧索引，实际 %v", fes.deleted)
	}
	if fes.putCalls != 1 || !strings.Contains(fes.lastPut, `"a"`) {
		t.Fatalf("重建应重新 PUT 带 mapping 的索引，实际 put=%d body=%s", fes.putCalls, fes.lastPut)
	}
}

// TestImportCountBeforeForAppend 追加场景要记录导入前的文档数（有基数才能核对增量）。
func TestImportCountBeforeForAppend(t *testing.T) {
	r, id, fes := importSetup(t)
	fes.exists["/logs"] = true
	fes.docs = map[string]int64{"/logs": 123}
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "logs", "confirmed": true, "progress_key": "k1"}))

	if d["count_before"] != float64(123) {
		t.Fatalf("追加导入要记录导入前文档数，实际 %v", d["count_before"])
	}
	if d["recreated"] != false {
		t.Fatalf("非重建不该标记 recreated: %v", d)
	}
}

// TestImportCountEndpoint 计数接口：导入后前端靠它按 5s×12 核实是否全部写入。
func TestImportCountEndpoint(t *testing.T) {
	r, id, fes := importSetup(t)
	fes.docs = map[string]int64{"/logs-here": 42}
	d := dataOf(t, importDo(t, r, http.MethodGet,
		fmt.Sprintf("/api/es/%d/import/count?index=logs-here", id), nil))
	if d["count"] != float64(42) {
		t.Fatalf("计数不对: %v", d)
	}
}

// TestImportResultCarriesSkipSamples 被跳过的行要带出示例文本（前 20 条、不去重），
// 供「用跳过样本重新生成 pattern」把漏网行的真实结构喂回模型。
func TestImportResultCarriesSkipSamples(t *testing.T) {
	r, id, _ := importSetup(t)
	p := writeTemp(t, "a.log", "GET /a 200\nBAD LINE ONE\nBAD LINE TWO\nBAD LINE ONE\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{
			"sources": []string{p}, "index": "logs", "confirmed": true, "progress_key": "k1",
			"patterns": []string{`%{WORD:method} %{URIPATHPARAM:path} %{NUMBER:status:int}`},
		}))

	if d["skipped"] != float64(3) {
		t.Fatalf("应跳过 3 条: %v", d["skipped"])
	}
	ss := d["skip_samples"].([]any)
	if len(ss) != 3 || ss[0] != "BAD LINE ONE" || ss[1] != "BAD LINE TWO" || ss[2] != "BAD LINE ONE" {
		t.Fatalf("跳过样例按前 20 条不去重带出: %v", ss)
	}
}

// TestImportPausesWhenSkipsExceed 跳过超过阈值（20 条）：导入挂起置 waiting + 带到现场样例，
// resume(continue) 后继续跑完 —— 这是「暂停 → 决策」闭环的主路径。
func TestImportPausesWhenSkipsExceed(t *testing.T) {
	r, id, _ := importSetup(t)
	var b strings.Builder
	b.WriteString("GET /a 200\n")
	for i := 0; i < 29; i++ {
		b.WriteString("BAD LINE\n")
	}
	p := writeTemp(t, "a.log", b.String())

	// 导入请求同步阻塞：放 goroutine 里跑，主线程轮询进度直到 waiting
	done := make(chan map[string]any, 1)
	go func() {
		done <- importDo(t, r, http.MethodPost,
			fmt.Sprintf("/api/es/%d/import", id),
			map[string]any{
				"sources": []string{p}, "index": "logs", "confirmed": true, "progress_key": "k1",
				"patterns": []string{`%{WORD:method} %{URIPATHPARAM:path} %{NUMBER:status:int}`},
			})
	}()

	var st map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out := importDo(t, r, http.MethodGet, fmt.Sprintf("/api/es/%d/import/progress?key=k1", id), nil)
		if out["code"] == float64(0) {
			data := out["data"].(map[string]any)
			if s, ok := data["status"].(map[string]any); ok && s["waiting"] == true {
				st = s
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st == nil {
		t.Fatal("跳过超过 20 条应暂停并置 waiting")
	}
	if st["skipped"].(float64) != 21 {
		t.Fatalf("应在第 21 条跳过时暂停，实际 %v", st["skipped"])
	}
	if ss := st["skip_samples"].([]any); len(ss) != 20 {
		t.Fatalf("暂停现场应带 20 条跳过样例: %v", ss)
	}

	if out := importDo(t, r, http.MethodPost, fmt.Sprintf("/api/es/%d/import/resume", id),
		map[string]any{"key": "k1", "action": "continue"}); out["code"] != float64(0) {
		t.Fatalf("resume continue 失败: %v", out)
	}

	select {
	case res := <-done:
		if res["code"] != float64(0) {
			t.Fatalf("继续后导入应成功: %v", res)
		}
		d := res["data"].(map[string]any)
		if d["read"] != float64(30) || d["skipped"] != float64(29) || d["indexed"] != float64(1) {
			t.Fatalf("计数不对: %v", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("暂停解除后导入未在限时内结束")
	}
}

// TestImportResumeWithoutPause 没有暂停任务时 resume 要明确报错，不能静默成功。
func TestImportResumeWithoutPause(t *testing.T) {
	r, id, _ := importSetup(t)
	out := importDo(t, r, http.MethodPost, fmt.Sprintf("/api/es/%d/import/resume", id),
		map[string]any{"key": "nope", "action": "continue"})
	if out["code"] == float64(0) {
		t.Fatalf("不存在暂停任务时不该成功: %v", out)
	}
}

// TestImportFieldRenames 字段映射（预览第二列）：写入文档与建 mapping 都用目标名。
func TestImportFieldRenames(t *testing.T) {
	r, id, fes := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"old_name\":\"v1\"}\n")
	d := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{
			"sources": []string{p}, "index": "logs", "create_index": true, "confirmed": true,
			"progress_key": "k1",
			"types":        map[string]string{"old_name": "keyword"},
			"renames":      map[string]string{"old_name": "new_name"},
		}))
	if d["indexed"] != float64(1) {
		t.Fatalf("导入应成功: %v", d)
	}
	if !strings.Contains(fes.lastBody, `"new_name"`) || strings.Contains(fes.lastBody, `"old_name"`) {
		t.Fatalf("写入应按映射替换字段名: %s", fes.lastBody)
	}
	if fes.putCalls != 1 || !strings.Contains(fes.lastPut, `"new_name"`) || strings.Contains(fes.lastPut, `"old_name"`) {
		t.Fatalf("mapping 也应按目标名建: %s", fes.lastPut)
	}
}

// TestImportRenameConflict 映射冲突（目标名与其它字段重名）直接拒绝 —— 写进去会互相覆盖。
func TestImportRenameConflict(t *testing.T) {
	r, id, _ := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"a\":1,\"b\":2}\n")
	out := importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{
			"sources": []string{p}, "index": "logs", "confirmed": true,
			"types":   map[string]string{"a": "long", "b": "long"},
			"renames": map[string]string{"a": "b"},
		})
	if out["code"] == float64(0) || !strings.Contains(out["message"].(string), "冲突") {
		t.Fatalf("重名映射应被拒绝: %v", out)
	}
}

// TestImportMultiAddsResourceField 多文件导入：每条文档带上 resource=来源文件，
// 否则导完只知道「共 N 条」，不知道哪条来自哪个文件。
func TestImportMultiAddsResourceField(t *testing.T) {
	r, id, fes := importSetup(t)
	pa := writeTemp(t, "a.jsonl", "{\"a\":1}\n")
	pb := writeTemp(t, "b.jsonl", "{\"a\":2}\n")
	dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{pa, pb}, "index": "logs", "confirmed": true, "progress_key": "k1"}))

	if !strings.Contains(fes.lastBody, `"resource":"a.jsonl"`) || !strings.Contains(fes.lastBody, `"resource":"b.jsonl"`) {
		t.Fatalf("多文件导入应带 resource 来源字段: %s", fes.lastBody)
	}
}

// TestImportSingleNoResourceField 单文件导入不加 resource（用户口径：「多个文件导入的时候」才需要）。
func TestImportSingleNoResourceField(t *testing.T) {
	r, id, fes := importSetup(t)
	p := writeTemp(t, "a.jsonl", "{\"a\":1}\n")
	dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "logs", "confirmed": true, "progress_key": "k1"}))
	if strings.Contains(fes.lastBody, `"resource"`) {
		t.Fatalf("单文件导入不该加 resource: %s", fes.lastBody)
	}
}

// TestImportZipEntriesResource zip 多条目：resource 用条目路径（含目录，来源更精确），
// 同时进度状态的 files/file_step 要能对上（抽屉逐文件打勾的数据来源）。
func TestImportZipEntriesResource(t *testing.T) {
	r, id, fes := importSetup(t)
	p := filepath.Join(t.TempDir(), "logs.zip")
	makeZipFile(t, p, []zipIn{
		{"2026-10-13/one.jsonl", "{\"a\":1}\n"},
		{"2026-10-13/two.jsonl", "{\"a\":2}\n"},
	})
	res := dataOf(t, importDo(t, r, http.MethodPost,
		fmt.Sprintf("/api/es/%d/import", id),
		map[string]any{"sources": []string{p}, "index": "logs", "confirmed": true, "progress_key": "k1"}))
	if res["indexed"] != float64(2) {
		t.Fatalf("应写 2 条: %v", res)
	}
	if !strings.Contains(fes.lastBody, `"resource":"2026-10-13/one.jsonl"`) {
		t.Fatalf("zip 条目 resource 应为含目录的条目路径: %s", fes.lastBody)
	}
	// 进度状态里的文件清单（结束后 phase=done，files 保留）
	out := importDo(t, r, http.MethodGet, fmt.Sprintf("/api/es/%d/import/progress?key=k1", id), nil)
	st := out["data"].(map[string]any)["status"].(map[string]any)
	files := st["files"].([]any)
	if len(files) != 2 || files[0] != "2026-10-13/one.jsonl" {
		t.Fatalf("进度 files 应列出包内条目: %v", files)
	}
}
