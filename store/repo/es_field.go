// es_field.go ES 系统自带字段缓存（es_field_cache）的读写。
//
// 两类数据同表不同命，是本文件最重要的约定：
//   - fields / es_version / status / synced_at 是**探测产物**，每轮后台刷新整块覆盖；
//   - extra 是**人登记的**补充清单，任何一轮探测都不许碰它。
//
// 所以写探测结果走 ON CONFLICT ... DO UPDATE 且**只更新探测那几列**，绝不 DELETE+INSERT
// （先删后插会把 extra 一并抹掉 —— 用户手填的 offset / logYear 就这么没了）。
package repo

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"infra-ops/model"
	"infra-ops/store"
)

// ESFieldRepo ES 系统自带字段缓存的数据存取。
type ESFieldRepo struct{}

func NewESFieldRepo() *ESFieldRepo { return &ESFieldRepo{} }

const esFieldCols = "id,conn_id,es_version,fields,extra,status,error,synced_at,updated_at"

// rowScanner QueryRow / Rows 都能满足的最小接口（Get 与 ListAll 共用一套扫描）。
type rowScanner interface {
	Scan(dest ...interface{}) error
}

// Get 读某个连接的缓存。**没有记录时返回 (nil, nil)** —— 那是「还没探测过」，
// 是正常状态（新连接、或后台还没轮到），不该让调用方去辨认 sql.ErrNoRows。
func (r *ESFieldRepo) Get(connID int64) (*model.ESFieldCache, error) {
	row := store.DB.QueryRow("SELECT "+esFieldCols+" FROM es_field_cache WHERE conn_id=?", connID)
	c, err := scanESField(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("es field cache get: %w", err)
	}
	return c, nil
}

// ListAll 全部缓存（后台一轮遍历用）。
func (r *ESFieldRepo) ListAll() ([]model.ESFieldCache, error) {
	rows, err := store.DB.Query("SELECT " + esFieldCols + " FROM es_field_cache ORDER BY conn_id ASC")
	if err != nil {
		return nil, fmt.Errorf("es field cache list: %w", err)
	}
	defer rows.Close()
	var out []model.ESFieldCache
	for rows.Next() {
		c, err := scanESField(rows)
		if err != nil {
			return nil, fmt.Errorf("es field cache scan: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// SaveProbe 落一次探测结果：版本 / 字段 / 状态 / 时间整块覆盖，**extra 与 updated_at 不动**。
func (r *ESFieldRepo) SaveProbe(connID int64, esVersion string, fields []string, status, errMsg string) error {
	_, err := store.DB.Exec(`
		INSERT INTO es_field_cache(conn_id, es_version, fields, status, error, synced_at, updated_at)
		VALUES(?,?,?,?,?,datetime('now','localtime'),datetime('now','localtime'))
		ON CONFLICT(conn_id) DO UPDATE SET
			es_version = excluded.es_version,
			fields     = excluded.fields,
			status     = excluded.status,
			error      = excluded.error,
			synced_at  = excluded.synced_at`,
		connID, esVersion, encodeStrList(fields), status, errMsg)
	if err != nil {
		return fmt.Errorf("es field cache save probe: %w", err)
	}
	return nil
}

// SaveExtra 只更新人工补充清单（记录不存在时建行：用户可以先填补充再等探测）。
func (r *ESFieldRepo) SaveExtra(connID int64, extra []string) error {
	_, err := store.DB.Exec(`
		INSERT INTO es_field_cache(conn_id, extra, updated_at)
		VALUES(?,?,datetime('now','localtime'))
		ON CONFLICT(conn_id) DO UPDATE SET extra = excluded.extra, updated_at = excluded.updated_at`,
		connID, encodeStrList(extra))
	if err != nil {
		return fmt.Errorf("es field cache save extra: %w", err)
	}
	return nil
}

// Delete 连接删除时清理（不留孤儿行：conn_id 是外键语义，但库里没建外键约束）。
func (r *ESFieldRepo) Delete(connID int64) error {
	_, err := store.DB.Exec("DELETE FROM es_field_cache WHERE conn_id=?", connID)
	return err
}

// scanESField 扫描一行；JSON 列解析失败按空列表处理（缓存内容坏掉不该让整个面板打不开）。
func scanESField(s rowScanner) (*model.ESFieldCache, error) {
	var c model.ESFieldCache
	var fieldsJSON, extraJSON string
	if err := s.Scan(&c.ID, &c.ConnID, &c.ESVersion, &fieldsJSON, &extraJSON,
		&c.Status, &c.Error, &c.SyncedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Fields = decodeStrList(fieldsJSON)
	c.Extra = decodeStrList(extraJSON)
	return &c, nil
}

func decodeStrList(s string) []string {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []string{}
	}
	return out
}

// encodeStrList nil 存成 [] 而不是 null —— 前端拿到的永远是数组，少一处判空。
func encodeStrList(list []string) string {
	if list == nil {
		return "[]"
	}
	b, err := json.Marshal(list)
	if err != nil {
		return "[]"
	}
	return string(b)
}
