// backup.go 数据备份与恢复：设置页「备份恢复」页签的后端。
//
// 备份 = 一致性快照（VACUUM INTO，运行中出库不带 WAL 残页）+ assets 资产 + 清单，
// 经 common/backupfmt 容器（scrypt + AES-256-GCM + zip）落成单个 .iopsbak 文件。
// 备份里含全部连接密码、凭据与 AI 密钥，故密码强制：忘密码 = 备份作废。
//
// 恢复不热替换——进程内 SQLite 持有库文件句柄，运行中换文件是未定义行为；
// 只把内容落进 data/restore_pending/，由下次启动时的 main.applyRestorePending 换入。
package settings

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/backupfmt"
	"infra-ops/common/resp"
	"infra-ops/common/version"
	"infra-ops/store"
)

const backupExt = ".iopsbak"

// backupReq 三个接口共用：目标文件路径 + 备份密码。
// 密码不挂 binding:required——空串要给出「密码不能为空」的专属提示而非通用「参数不完整」。
type backupReq struct {
	Path     string `json:"path" binding:"required"`
	Password string `json:"password"`
}

// backupFileResp 导出结果。
type backupFileResp struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// backupFileInfo 预览时的条目清单行。
type backupFileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// backupPreviewResp 恢复前预览：不落任何盘，只解清单与条目尺寸。
type backupPreviewResp struct {
	Manifest *backupfmt.Manifest `json:"manifest"`
	Files    []backupFileInfo    `json:"files"`
	DBBytes  int64               `json:"db_bytes"`
}

// backupRestoreResp 恢复结果（暂存成功，重启后生效）。
type backupRestoreResp struct {
	RestoreDir   string `json:"restore_dir"`
	Tables       int    `json:"tables"`
	AssetCount   int    `json:"asset_count"`
	RestartNeeds bool   `json:"restart_required"`
}

// BackupExport POST /api/settings/backup/export {path, password}
func (h *Handler) BackupExport(c *gin.Context) {
	var req backupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数不完整")
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		resp.Fail(c, resp.CodeBadRequest, "备份密码不能为空——它保护的是全部连接密码与凭据")
		return
	}
	target, err := normalizeBackupTarget(req.Path)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	dbFile := dbFilePath()
	if dbFile == "" {
		resp.Fail(c, resp.CodeInternal, "无法定位数据库文件")
		return
	}
	dataDir := filepath.Dir(dbFile)

	// 1. 一致性快照：VACUUM INTO 目标必须不存在，先清残留（上次失败可能留下半截）
	snap := filepath.Join(dataDir, fmt.Sprintf("backup-snapshot-%d.db", time.Now().UnixNano()))
	os.Remove(snap)
	defer os.Remove(snap)
	if _, err := store.DB.Exec("VACUUM INTO " + quoteSQLText(snap)); err != nil {
		resp.Fail(c, resp.CodeInternal, "生成数据库快照失败: "+err.Error())
		return
	}

	// 2. 清单：表行数与系统信息页同一套口径，恢复预览时能对得上
	stats0, total := tableStats()
	stats := make([]backupfmt.TableStat, len(stats0))
	for i, s := range stats0 {
		stats[i] = backupfmt.TableStat{Name: s.Name, Rows: s.Rows}
	}
	assets, assetBytes := walkAssets(filepath.Join(dataDir, "assets"))
	m := backupfmt.Manifest{
		Format:     1,
		AppVersion: version.Version,
		CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
		DBFile:     "db/infra-ops.db",
		DBTables:   stats,
		TotalRows:  total,
		AssetCount: len(assets),
		AssetBytes: assetBytes,
	}
	mb, _ := json.Marshal(m)

	inputs := make([]backupfmt.ZipInput, 0, len(assets)+2)
	inputs = append(inputs, backupfmt.ZipInput{Name: "manifest.json", Data: mb})
	inputs = append(inputs, backupfmt.ZipInput{Name: m.DBFile, Path: snap})
	for _, a := range assets {
		inputs = append(inputs, backupfmt.ZipInput{Name: "assets/" + a.rel, Path: a.abs})
	}

	// 3. 加密落盘：先写 .part 再改名，避免半截文件被当成完整备份
	payload, err := backupfmt.BuildZip(inputs)
	if err == nil {
		var blob []byte
		blob, err = backupfmt.Encrypt(req.Password, payload)
		if err == nil {
			err = atomicWrite(target, blob)
		}
	}
	if err != nil {
		resp.Fail(c, resp.CodeInternal, "生成备份文件失败: "+err.Error())
		return
	}
	var size int64
	if fi, err := os.Stat(target); err == nil {
		size = fi.Size()
	}
	resp.OK(c, backupFileResp{Path: target, SizeBytes: size})
}

// BackupPreview POST /api/settings/backup/preview {path, password}
// 只解密解包到内存读清单，不写任何盘——密码错误在这里就拦住，不给恢复半路失败的机会。
func (h *Handler) BackupPreview(c *gin.Context) {
	var req backupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数不完整")
		return
	}
	m, entries, dbBytes, err := openBackup(req.Path, req.Password)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	files := make([]backupFileInfo, 0, len(entries))
	for name, data := range entries {
		files = append(files, backupFileInfo{Name: name, Size: int64(len(data))})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	resp.OK(c, backupPreviewResp{Manifest: m, Files: files, DBBytes: dbBytes})
}

// BackupRestore POST /api/settings/backup/restore {path, password}
// 校验通过的备份内容落进 data/restore_pending/，重启时换入生效。
func (h *Handler) BackupRestore(c *gin.Context) {
	var req backupReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Fail(c, resp.CodeBadRequest, "参数不完整")
		return
	}
	m, entries, _, err := openBackup(req.Path, req.Password)
	if err != nil {
		resp.Fail(c, resp.CodeBadRequest, err.Error())
		return
	}
	dbFile := dbFilePath()
	if dbFile == "" {
		resp.Fail(c, resp.CodeInternal, "无法定位数据库文件")
		return
	}
	pendDir := filepath.Join(filepath.Dir(dbFile), "restore_pending")

	// 暂存目录全新开始：上次恢复后没重启的残留不能混进这次的备份
	if err := os.RemoveAll(pendDir); err != nil {
		resp.Fail(c, resp.CodeInternal, "清理旧暂存失败: "+err.Error())
		return
	}
	dbEntry, ok := entries[m.DBFile]
	if !ok || len(dbEntry) == 0 {
		resp.Fail(c, resp.CodeBadRequest, "备份里没有数据库文件，无法恢复")
		return
	}

	// 先写库文件并做完整性校验，校验不过整个暂存撤销——绝不把坏库留给下次启动
	stagedDB := filepath.Join(pendDir, "infra-ops.db")
	if err := os.MkdirAll(pendDir, 0750); err != nil {
		resp.Fail(c, resp.CodeInternal, err.Error())
		return
	}
	if err := os.WriteFile(stagedDB, dbEntry, 0600); err != nil {
		os.RemoveAll(pendDir)
		resp.Fail(c, resp.CodeInternal, "写入暂存失败: "+err.Error())
		return
	}
	if err := checkSQLite(stagedDB); err != nil {
		os.RemoveAll(pendDir)
		resp.Fail(c, resp.CodeBadRequest, "备份库完整性校验未通过: "+err.Error())
		return
	}

	// assets 逐个落暂存；单个失败同样整体撤销
	assetCount := 0
	for name, data := range entries {
		rel, ok := strings.CutPrefix(name, "assets/")
		if !ok || rel == "" || strings.Contains(rel, "..") {
			continue
		}
		dst := filepath.Join(pendDir, "assets", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
			os.RemoveAll(pendDir)
			resp.Fail(c, resp.CodeInternal, err.Error())
			return
		}
		if err := os.WriteFile(dst, data, 0640); err != nil {
			os.RemoveAll(pendDir)
			resp.Fail(c, resp.CodeInternal, fmt.Sprintf("写入资产 %s 失败: %s", rel, err.Error()))
			return
		}
		assetCount++
	}

	meta, _ := json.Marshal(map[string]string{
		"restored_at": time.Now().Format("2006-01-02 15:04:05"),
		"created_at":  m.CreatedAt,
		"app_version": m.AppVersion,
	})
	os.WriteFile(filepath.Join(pendDir, "manifest.json"), meta, 0640)

	resp.OK(c, backupRestoreResp{RestoreDir: pendDir, Tables: len(m.DBTables),
		AssetCount: assetCount, RestartNeeds: true})
}

// openBackup 读文件 → 解密 → 解包，返回清单、条目与库文件大小。错误信息面向用户。
func openBackup(path, password string) (*backupfmt.Manifest, map[string][]byte, int64, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("读不到备份文件: %s", err.Error())
	}
	payload, err := backupfmt.Decrypt(password, blob)
	if err != nil {
		return nil, nil, 0, err // ErrWrongPassword/ErrBadFormat 的文案已面向用户
	}
	m, entries, err := backupfmt.ReadZip(payload)
	if err != nil {
		return nil, nil, 0, err
	}
	return m, entries, int64(len(entries[m.DBFile])), nil
}

// checkSQLite 用只读连接对暂存库跑 integrity_check——恢复错版本/坏块的最后一道闸。
func checkSQLite(path string) error {
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer conn.Close()
	var res string
	if err := conn.QueryRow(`PRAGMA integrity_check`).Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("%s", res)
	}
	return nil
}

// normalizeBackupTarget 与 MySQL 导出同口径：绝对路径、目录存在、扩展名补全。
func normalizeBackupTarget(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("请先选择备份文件的保存位置")
	}
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("备份路径必须是绝对路径")
	}
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		return "", fmt.Errorf("备份路径指向的是目录，请指定文件名")
	}
	if !strings.EqualFold(filepath.Ext(target), backupExt) {
		target += backupExt
	}
	if st, err := os.Stat(filepath.Dir(target)); err != nil || !st.IsDir() {
		return "", fmt.Errorf("目录不存在：%s", filepath.Dir(target))
	}
	return target, nil
}

// quoteSQLText 内联进 SQL 的文本字面量：单引号翻倍（VACUUM INTO 的目标路径）。
func quoteSQLText(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// assetFile 随包资产清单项。
type assetFile struct {
	rel string // zip 内相对路径（正斜杠）
	abs string // 本地绝对路径
}

// walkAssets 收集资产目录全部文件（跳过子目录骨架本身）；目录不存在不算错——新装环境没有资产也能备份。
func walkAssets(dir string) ([]assetFile, int64) {
	var out []assetFile
	var total int64
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // 读不到的文件跳过并在资产字节里体现，不拦整个备份
		}
		if rel, err := filepath.Rel(dir, p); err == nil {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
			out = append(out, assetFile{rel: filepath.ToSlash(rel), abs: p})
		}
		return nil
	})
	return out, total
}

// atomicWrite 落盘：写 .part 临时文件再改名，进程半路被杀也不会留下完整假备份。
func atomicWrite(target string, blob []byte) error {
	tmp := target + ".part"
	if err := os.WriteFile(tmp, blob, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
