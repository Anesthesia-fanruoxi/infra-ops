package settings

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"infra-ops/common/backupfmt"
	"infra-ops/store"
)

// backupSetup 备份接口共用环境：临时库（带一张有数据的表）+ 临时导出目录 + 干净路由。
// 不与 settings_test.go 的 setup 复用：那边被并行会话改到中途，接口签名不稳。
func backupSetup(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := store.Open(filepath.Join("data", "infra-ops.db")); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(store.Close)
	if _, err := store.DB.Exec(`CREATE TABLE hosts(id INTEGER PRIMARY KEY, name TEXT);
		INSERT INTO hosts(name) VALUES ('a'),('b'),('c')`); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(Deps{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/settings/backup/export", h.BackupExport)
	r.POST("/api/settings/backup/preview", h.BackupPreview)
	r.POST("/api/settings/backup/restore", h.BackupRestore)
	return r, t.TempDir()
}

// backupPost JSON POST 快捷方式。
func backupPost(t *testing.T, r *gin.Engine, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)))
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// backupDoExport 走完整 HTTP 链路导出一份备份，返回文件路径。
func backupDoExport(t *testing.T, r *gin.Engine, dir, password string) string {
	t.Helper()
	target := filepath.Join(dir, "backup.iopsbak")
	w, out := backupPost(t, r, "/api/settings/backup/export", map[string]string{"path": target, "password": password})
	if w.Code != 200 || out["code"].(float64) != 0 {
		t.Fatalf("导出失败: %s", w.Body.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("备份文件未落盘: %v", err)
	}
	return target
}

func TestBackupExportEncryptsWholeFile(t *testing.T) {
	r, dir := backupSetup(t)
	target := backupDoExport(t, r, dir, "secret-key")

	// 备份文件整体是密文：内容里不允许出现 SQL 关键词或表名（防「假加密」回归）
	blob, _ := os.ReadFile(target)
	for _, kw := range []string{"CREATE TABLE", "hosts", "manifest.json"} {
		if bytes.Contains(blob, []byte(kw)) {
			t.Errorf("备份明文里出现了 %q —— 加密失效", kw)
		}
	}
	if !bytes.HasPrefix(blob, backupfmt.Magic) {
		t.Error("容器缺少魔数")
	}
}

func TestBackupPreviewAndRestore(t *testing.T) {
	r, dir := backupSetup(t)
	target := backupDoExport(t, r, dir, "secret-key")

	// 预览：清单 + 条目，不落盘
	w, out := backupPost(t, r, "/api/settings/backup/preview", map[string]string{"path": target, "password": "secret-key"})
	if w.Code != 200 || out["code"].(float64) != 0 {
		t.Fatalf("预览失败: %s", w.Body.String())
	}
	mraw, _ := json.Marshal(out["data"].(map[string]any)["manifest"])
	var m backupfmt.Manifest
	json.Unmarshal(mraw, &m)
	if m.DBFile != "db/infra-ops.db" || m.TotalRows < 3 || len(m.DBTables) == 0 {
		t.Fatalf("清单不符: %+v", m)
	}
	if _, err := os.Stat(filepath.Join("data", "restore_pending")); err == nil {
		t.Fatal("预览不该产生暂存")
	}

	// 错密码：预览必须拦住
	w, _ = backupPost(t, r, "/api/settings/backup/preview", map[string]string{"path": target, "password": "wrong"})
	if !strings.Contains(w.Body.String(), "密码错误") {
		t.Fatalf("错密码应报「密码错误」: %s", w.Body.String())
	}

	// 恢复：暂存目录里有库 + 标志，重启换入由 restore_test.go（package main）覆盖
	w, out = backupPost(t, r, "/api/settings/backup/restore", map[string]string{"path": target, "password": "secret-key"})
	if w.Code != 200 || out["code"].(float64) != 0 {
		t.Fatalf("恢复失败: %s", w.Body.String())
	}
	if _, err := os.Stat(filepath.Join("data", "restore_pending", "infra-ops.db")); err != nil {
		t.Fatalf("暂存库缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join("data", "restore_pending", "manifest.json")); err != nil {
		t.Fatalf("暂存标志缺失: %v", err)
	}
}

func TestBackupRestoreRejectsGarbage(t *testing.T) {
	r, dir := backupSetup(t)
	notBackup := filepath.Join(dir, "junk.iopsbak")
	os.WriteFile(notBackup, []byte("用户误选的普通文件内容，长度超过容器头部以触发魔数判断........"), 0600)
	w, _ := backupPost(t, r, "/api/settings/backup/preview", map[string]string{"path": notBackup, "password": "x"})
	if !strings.Contains(w.Body.String(), "不是有效的备份文件") {
		t.Fatalf("魔数不符应给可读提示: %s", w.Body.String())
	}
	w, out := backupPost(t, r, "/api/settings/backup/restore", map[string]string{"path": notBackup, "password": "x"})
	if out["code"].(float64) == 0 {
		t.Fatalf("垃圾文件必须恢复失败: %s", w.Body.String())
	}
	if _, err := os.Stat(filepath.Join("data", "restore_pending")); err == nil {
		t.Fatal("失败恢复不得留下暂存")
	}
}

func TestBackupExportValidation(t *testing.T) {
	r, dir := backupSetup(t)
	// 空密码
	w, _ := backupPost(t, r, "/api/settings/backup/export", map[string]string{"path": filepath.Join(dir, "a.iopsbak"), "password": ""})
	if !strings.Contains(w.Body.String(), "不能为空") {
		t.Fatalf("空密码应被拒: %s", w.Body.String())
	}
	// 相对路径
	w, _ = backupPost(t, r, "/api/settings/backup/export", map[string]string{"path": "backup.iopsbak", "password": "secret"})
	if !strings.Contains(w.Body.String(), "绝对路径") {
		t.Fatalf("相对路径应被拒: %s", w.Body.String())
	}
	// 不存在的目录
	w, _ = backupPost(t, r, "/api/settings/backup/export", map[string]string{"path": filepath.Join(dir, "nope", "a.iopsbak"), "password": "secret"})
	if !strings.Contains(w.Body.String(), "目录不存在") {
		t.Fatalf("坏目录应被拒: %s", w.Body.String())
	}
}
