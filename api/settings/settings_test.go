package settings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"infra-ops/common/aiconfig"
	"infra-ops/common/crypto"
	"infra-ops/model"
	"infra-ops/store"
	"infra-ops/store/setting"
)

// env 测试用的响应信封（resp.R 的 Data 是 interface{}，这里按需解到具体类型）。
type env struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// setup 每个用例一个独立临时库 + 独立加密服务：store.DB 是包级全局，用例之间必须互不干扰。
func setup(t *testing.T) (*Handler, *crypto.Service) {
	t.Helper()
	if err := store.Open(filepath.Join(t.TempDir(), "settings.db")); err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(store.Close)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("生成主密钥失败: %v", err)
	}
	svc, err := crypto.NewService(key)
	if err != nil {
		t.Fatalf("初始化加密服务失败: %v", err)
	}
	repo := setting.NewSettingsRepo()
	if err := repo.EnsureRuntimeDefaults(); err != nil {
		t.Fatalf("补齐默认配置失败: %v", err)
	}
	return NewHandler(Deps{Settings: repo, CryptoS: svc}), svc
}

// do 直接调 handler（不经路由，专注接口自身的入参/出参契约）。
func do(t *testing.T, h gin.HandlerFunc, method, body string) (int, env) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h(c)

	var e env
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（body=%s）", err, w.Body.String())
	}
	return w.Code, e
}

func decodeAI(t *testing.T, e env) model.AIConfig {
	t.Helper()
	var v model.AIConfig
	if err := json.Unmarshal(e.Data, &v); err != nil {
		t.Fatalf("解析 AI 配置失败: %v", err)
	}
	return v
}

// ---------- AI 接入 ----------

func TestAIConfigRoundTrip(t *testing.T) {
	h, svc := setup(t)

	_, e := do(t, h.SaveAI, "PUT",
		`{"enabled":true,"base_url":" https://api.deepseek.com/v1 ","model":" deepseek-chat ","api_key":"sk-test-123","timeout":9999}`)
	if e.Code != 0 {
		t.Fatalf("保存失败: %+v", e)
	}
	got := decodeAI(t, e)
	if !got.Enabled || got.BaseURL != "https://api.deepseek.com/v1" || got.Model != "deepseek-chat" {
		t.Fatalf("保存后回显不对: %+v", got)
	}
	if !got.HasKey {
		t.Fatal("保存密钥后 has_key 应为 true")
	}
	if got.Timeout != aiconfig.MaxTimeoutSeconds {
		t.Fatalf("超时应被钳到 %d，实际 %d", aiconfig.MaxTimeoutSeconds, got.Timeout)
	}

	// 密钥必须密文落库，且能解回原文
	raw, _ := setting.NewSettingsRepo().Get(setting.SettingAIAPIKey)
	if raw == "" || raw == "sk-test-123" {
		t.Fatalf("密钥疑似明文落库: %q", raw)
	}
	if s := aiconfig.Load(setting.NewSettingsRepo(), svc); s.APIKey != "sk-test-123" {
		t.Fatalf("密钥解密失败，得到 %q", s.APIKey)
	}

	// GET 与 PUT 回显一致，且密钥永不下发
	_, e2 := do(t, h.GetAI, "GET", "")
	if got2 := decodeAI(t, e2); !reflect.DeepEqual(got2, got) {
		t.Fatalf("GET 与 PUT 回显不一致: %+v vs %+v", got2, got)
	}
	if strings.Contains(string(e2.Data), "sk-test-123") {
		t.Fatal("接口响应里出现了明文密钥")
	}
}

func TestAIConfigClearKey(t *testing.T) {
	h, _ := setup(t)
	do(t, h.SaveAI, "PUT", `{"enabled":true,"base_url":"https://x/v1","model":"m","api_key":"sk-1"}`)

	_, e := do(t, h.SaveAI, "PUT", `{"enabled":true,"base_url":"https://x/v1","model":"m","clear_key":true}`)
	if decodeAI(t, e).HasKey {
		t.Fatal("clear_key 后 has_key 应为 false")
	}
}

// 留空 api_key 只表示「不修改」，不能把已存密钥抹掉
func TestAIConfigKeepKeyWhenBlank(t *testing.T) {
	h, svc := setup(t)
	do(t, h.SaveAI, "PUT", `{"enabled":true,"base_url":"https://x/v1","model":"m","api_key":"sk-keep"}`)
	do(t, h.SaveAI, "PUT", `{"enabled":true,"base_url":"https://y/v1","model":"m2"}`)

	if s := aiconfig.Load(setting.NewSettingsRepo(), svc); s.APIKey != "sk-keep" {
		t.Fatalf("留空不应改动密钥，实际 %q", s.APIKey)
	}
}

// 关掉开关时允许保存半成品配置（先存下来稍后再填是合理用法）
func TestAIConfigDisabledAllowsEmpty(t *testing.T) {
	h, _ := setup(t)
	if _, e := do(t, h.SaveAI, "PUT", `{"enabled":false,"base_url":"","model":""}`); e.Code != 0 {
		t.Fatalf("关闭状态下应允许保存空配置: %+v", e)
	}
	// 但一旦启用，地址与模型就是必填
	if _, e := do(t, h.SaveAI, "PUT", `{"enabled":true,"base_url":"","model":""}`); e.Code == 0 {
		t.Fatal("启用时地址为空应被拒")
	}
	if _, e := do(t, h.SaveAI, "PUT", `{"enabled":true,"base_url":"https://x/v1","model":""}`); e.Code == 0 {
		t.Fatal("启用时模型为空应被拒")
	}
}

// ---------- 连通性测试 ----------

// fakeOpenAI 假模型服务：withUsage 控制是否回 usage（不回则客户端应降级为估算）。
func fakeOpenAI(t *testing.T, withUsage bool, status int, hitURL *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hitURL != nil {
			*hitURL = append(*hitURL, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		out := map[string]interface{}{
			"model":   "test-model",
			"choices": []map[string]interface{}{{"message": map[string]string{"role": "assistant", "content": "可用"}}},
		}
		if withUsage {
			out["usage"] = map[string]int{"prompt_tokens": 12, "completion_tokens": 3, "total_tokens": 15}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

func decodeTest(t *testing.T, e env) model.AITestResult {
	t.Helper()
	var v model.AITestResult
	if err := json.Unmarshal(e.Data, &v); err != nil {
		t.Fatalf("解析测试结果失败: %v", err)
	}
	return v
}

func TestAITestSuccess(t *testing.T) {
	h, _ := setup(t)
	var paths []string
	srv := fakeOpenAI(t, true, http.StatusOK, &paths)
	defer srv.Close()

	_, e := do(t, h.TestAI, "POST",
		`{"base_url":"`+srv.URL+`/v1","model":"test-model","api_key":"sk-t","timeout":30}`)
	got := decodeTest(t, e)
	if !got.OK {
		t.Fatalf("应测试成功: %+v", got)
	}
	if got.Model != "test-model" || got.Reply != "可用" {
		t.Fatalf("回显不对: %+v", got)
	}
	if got.TotalTokens != 15 || got.TokenSource != model.TokenSourceAPI {
		t.Fatalf("接口给了 usage 就应原样透出为 api 来源: %+v", got)
	}
	// base_url 带 /v1 时应自动补 /chat/completions
	if len(paths) == 0 || !strings.HasSuffix(paths[0], "/chat/completions") {
		t.Fatalf("请求路径不对: %v", paths)
	}
}

func TestAITestNoUsageFallsBackToEstimated(t *testing.T) {
	h, _ := setup(t)
	srv := fakeOpenAI(t, false, http.StatusOK, nil)
	defer srv.Close()

	_, e := do(t, h.TestAI, "POST", `{"base_url":"`+srv.URL+`/v1","model":"m","api_key":"sk"}`)
	got := decodeTest(t, e)
	if !got.OK || got.TokenSource != model.TokenSourceEstimated {
		t.Fatalf("接口不回 usage 时应标为估算: %+v", got)
	}
}

func TestAITestFailure(t *testing.T) {
	h, _ := setup(t)
	srv := fakeOpenAI(t, false, http.StatusInternalServerError, nil)
	defer srv.Close()

	_, e := do(t, h.TestAI, "POST", `{"base_url":"`+srv.URL+`/v1","model":"m","api_key":"sk"}`)
	got := decodeTest(t, e)
	// 失败是业务结果：HTTP 200 + ok=false，前端据此区分「网络不通」与「接口报错」
	if got.OK {
		t.Fatal("服务端 500 时不应报告成功")
	}
	if got.Error == "" {
		t.Fatal("失败必须给出原因")
	}
}

// 关键契约：测试用的是**表单当前值**，不是库里的值——这样「只改了一个字段想先试试」才成立。
func TestAITestUsesFormValuesNotStored(t *testing.T) {
	h, _ := setup(t)
	do(t, h.SaveAI, "PUT", `{"enabled":false,"base_url":"http://127.0.0.1:1/v1","model":"stored-model"}`)

	var paths []string
	srv := fakeOpenAI(t, true, http.StatusOK, &paths)
	defer srv.Close()

	// 表单里给的是假服务地址与另一个模型名，库里那份是打不通的地址
	_, e := do(t, h.TestAI, "POST",
		`{"base_url":"`+srv.URL+`/v1","model":"form-model","api_key":"sk-form"}`)
	if got := decodeTest(t, e); !got.OK {
		t.Fatalf("应按表单值测试成功（说明用的是表单值而非库内值）: %+v", got)
	}
	if len(paths) == 0 {
		t.Fatal("假服务没收到请求，说明用的仍是库内地址")
	}
}

// ---------- 厂商预设 & 模型清单 ----------

// 选了内置厂商就不必填地址：地址由预设派生，只填密钥即可
func TestAISaveDerivesBaseURLFromProvider(t *testing.T) {
	h, _ := setup(t)
	_, e := do(t, h.SaveAI, "PUT", `{"enabled":true,"provider":"deepseek","model":"deepseek-chat","api_key":"sk"}`)
	if e.Code != 0 {
		t.Fatalf("保存失败: %+v", e)
	}
	got := decodeAI(t, e)
	if got.Provider != "deepseek" || got.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("厂商地址未派生: %+v", got)
	}
	if len(got.Providers) < 2 {
		t.Fatalf("应下发厂商清单: %+v", got.Providers)
	}
	// 落库的也是派生地址，别的功能按库内值解析同样拿得到
	if s := aiconfig.Load(setting.NewSettingsRepo(), nil); s.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("库内解析地址不对: %q", s.BaseURL)
	}
}

// 每个内置接入点都能仅凭 ID 派生出正确地址（含小米 MiMo 的按量付费与 Token Plan 各集群）
func TestAISaveDerivesEveryProviderBaseURL(t *testing.T) {
	for _, p := range aiconfig.Providers() {
		if p.ID == "" || p.BaseURL == "" || p.KeyHint == "" {
			t.Fatalf("接入点预设不完整: %+v", p)
		}
		t.Run(p.ID, func(t *testing.T) {
			h, _ := setup(t)
			_, e := do(t, h.SaveAI, "PUT",
				`{"enabled":true,"provider":"`+p.ID+`","model":"m","api_key":"sk"}`)
			if e.Code != 0 {
				t.Fatalf("保存失败: %+v", e)
			}
			if got := decodeAI(t, e); got.BaseURL != p.BaseURL {
				t.Fatalf("地址未派生: got %q want %q", got.BaseURL, p.BaseURL)
			}
		})
	}
}

func decodeModels(t *testing.T, e env) model.AIModelList {
	t.Helper()
	var v model.AIModelList
	if err := json.Unmarshal(e.Data, &v); err != nil {
		t.Fatalf("解析模型清单失败: %v", err)
	}
	return v
}

func fakeModels(t *testing.T, status int, body string, hitAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hitAuth != nil {
			*hitAuth = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestAIListModels(t *testing.T) {
	h, _ := setup(t)
	var auth string
	srv := fakeModels(t, http.StatusOK, `{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`, &auth)
	defer srv.Close()

	_, e := do(t, h.ListAIModels, "POST", `{"base_url":"`+srv.URL+`/v1","api_key":"sk-m"}`)
	got := decodeModels(t, e)
	if !got.OK || len(got.Models) != 2 || got.Models[0] != "deepseek-chat" {
		t.Fatalf("模型清单不对: %+v", got)
	}
	if auth != "Bearer sk-m" {
		t.Fatalf("应带上密钥: %q", auth)
	}
}

func TestAIListModelsFailure(t *testing.T) {
	h, _ := setup(t)
	srv := fakeModels(t, http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, nil)
	defer srv.Close()

	_, e := do(t, h.ListAIModels, "POST", `{"base_url":"`+srv.URL+`/v1","api_key":"sk-bad"}`)
	got := decodeModels(t, e)
	if got.OK || got.Error == "" {
		t.Fatalf("401 时应返回 ok=false 且带原因: %+v", got)
	}
}

// ---------- 系统信息 ----------

func TestSystemInfo(t *testing.T) {
	h, _ := setup(t)
	_, e := do(t, h.GetSystem, "GET", "")
	var got model.SystemInfo
	if err := json.Unmarshal(e.Data, &got); err != nil {
		t.Fatalf("解析系统信息失败: %v", err)
	}
	if got.Version == "" || got.GoVersion == "" {
		t.Fatalf("版本信息缺失: %+v", got)
	}
	if got.WorkDir == "" || got.DBPath == "" {
		t.Fatalf("路径信息缺失: %+v", got)
	}
	if !strings.HasSuffix(got.DBPath, "settings.db") {
		t.Fatalf("数据库路径不像测试库: %q", got.DBPath)
	}
	// 表行数：至少要有 settings 表，且排序按行数降序
	var hasSettings bool
	for i, tb := range got.Tables {
		if tb.Name == "settings" {
			hasSettings = true
		}
		if i > 0 && got.Tables[i-1].Rows < tb.Rows {
			t.Fatalf("表行数未按降序排列: %+v", got.Tables)
		}
	}
	if !hasSettings {
		t.Fatalf("表清单里应有 settings：%+v", got.Tables)
	}
}
