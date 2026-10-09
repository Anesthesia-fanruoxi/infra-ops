// system.go 系统信息（只读）：版本、运行时长、数据目录与各表行数。
//
// 桌面形态下工作目录是用户数据目录（%APPDATA%\infra-ops），数据库也在那儿——
// 这些既不写在界面上、也不在配置文件里，出问题时最常被问到，所以集中放出来。
package settings

import (
	"database/sql"
	"os"
	"regexp"
	"runtime"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"infra-ops/common/resp"
	"infra-ops/common/version"
	"infra-ops/model"
	"infra-ops/store"
)

// tableNameRe 表名白名单：统计行数要拼表名进 SQL，只放行常规标识符，杜绝注入。
var tableNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// GetSystem GET /api/settings/system
func (h *Handler) GetSystem(c *gin.Context) {
	wd, _ := os.Getwd()
	info := model.SystemInfo{
		Version:   version.Version,
		GoVersion: runtime.Version(),
		StartedAt: startedAt.Format("2006-01-02 15:04:05"),
		UptimeSec: int64(time.Since(startedAt) / time.Second),
		WorkDir:   wd,
		Tables:    []model.TableStat{},
	}
	if p := dbFilePath(); p != "" {
		info.DBPath = p
		if fi, err := os.Stat(p); err == nil {
			info.DBSizeBytes = fi.Size()
		}
	}
	info.Tables, info.TableTotalRows = tableStats()
	resp.OK(c, info)
}

// dbFilePath 取主库文件的绝对路径（PRAGMA 比手拼 workdir 可靠）。
func dbFilePath() string {
	rows, err := store.DB.Query(`PRAGMA database_list`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name string
		var file sql.NullString
		if err := rows.Scan(&seq, &name, &file); err != nil {
			continue
		}
		if name == "main" && file.Valid {
			return file.String
		}
	}
	return ""
}

// tableStats 统计各表行数（按行数降序）。
// 排查问题时最先想知道的就是「数据堆在哪张表」，故按行数排序而非表名字典序。
func tableStats() ([]model.TableStat, int64) {
	out := []model.TableStat{}
	var total int64

	rows, err := store.DB.Query(
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return out, 0
	}
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil && tableNameRe.MatchString(n) {
			names = append(names, n)
		}
	}
	rows.Close()

	for _, n := range names {
		var c int64
		if err := store.DB.QueryRow(`SELECT COUNT(*) FROM "` + n + `"`).Scan(&c); err != nil {
			continue // 单表统计失败不影响整体（例如虚拟表）
		}
		total += c
		out = append(out, model.TableStat{Name: n, Rows: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rows > out[j].Rows })
	return out, total
}
