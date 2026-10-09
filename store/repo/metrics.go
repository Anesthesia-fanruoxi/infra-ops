package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// MetricsRepo 监控查询（Prometheus / VictoriaMetrics）连接配置的数据存取。
type MetricsRepo struct{}

func NewMetricsRepo() *MetricsRepo { return &MetricsRepo{} }

// List 分页查询连接配置。
func (r *MetricsRepo) List(keyword string, page, pageSize int) ([]model.MetricsConnView, int64, error) {
	where := ""
	args := []interface{}{}
	if keyword != "" {
		where = "WHERE name LIKE ? OR url LIKE ? OR remark LIKE ?"
		args = append(args, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	if err := store.DB.QueryRow("SELECT COUNT(*) FROM metrics_conns "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("metrics count: %w", err)
	}

	offset := (page - 1) * pageSize
	rows, err := store.DB.Query("SELECT id,name,kind,url,insecure,auth_type,username,encrypted_secret,remark,created_at,updated_at FROM metrics_conns "+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("metrics list: %w", err)
	}
	defer rows.Close()

	var items []model.MetricsConnView
	for rows.Next() {
		var c model.MetricsConn
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.URL, &c.Insecure, &c.AuthType, &c.Username, &c.EncryptedSecret, &c.Remark, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("metrics scan: %w", err)
		}
		items = append(items, metricsToView(c))
	}
	return items, total, rows.Err()
}

// GetByID 按 ID 查询，含加密密文（仅供后端内部解密使用）。
func (r *MetricsRepo) GetByID(id int64) (*model.MetricsConn, error) {
	c := &model.MetricsConn{}
	err := store.DB.QueryRow(
		"SELECT id,name,kind,url,insecure,auth_type,username,encrypted_secret,remark,created_at,updated_at FROM metrics_conns WHERE id=?",
		id,
	).Scan(&c.ID, &c.Name, &c.Kind, &c.URL, &c.Insecure, &c.AuthType, &c.Username, &c.EncryptedSecret, &c.Remark, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("metrics get: %w", err)
	}
	return c, nil
}

// Create 新增连接配置。
func (r *MetricsRepo) Create(c *model.MetricsConn) (int64, error) {
	res, err := store.DB.Exec(
		"INSERT INTO metrics_conns(name,kind,url,insecure,auth_type,username,encrypted_secret,remark) VALUES(?,?,?,?,?,?,?,?)",
		c.Name, c.Kind, c.URL, boolToInt(c.Insecure), c.AuthType, c.Username, c.EncryptedSecret, c.Remark,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("duplicate name")
		}
		return 0, fmt.Errorf("metrics create: %w", err)
	}
	return res.LastInsertId()
}

// Update 更新连接配置；encryptedSecret 为 nil 时保留原密码。
func (r *MetricsRepo) Update(id int64, c *model.MetricsConn) error {
	if c.EncryptedSecret != nil {
		_, err := store.DB.Exec(
			"UPDATE metrics_conns SET name=?,kind=?,url=?,insecure=?,auth_type=?,username=?,encrypted_secret=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
			c.Name, c.Kind, c.URL, boolToInt(c.Insecure), c.AuthType, c.Username, c.EncryptedSecret, c.Remark, id,
		)
		if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return fmt.Errorf("duplicate name")
		}
		return err
	}
	_, err := store.DB.Exec(
		"UPDATE metrics_conns SET name=?,kind=?,url=?,insecure=?,auth_type=?,username=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
		c.Name, c.Kind, c.URL, boolToInt(c.Insecure), c.AuthType, c.Username, c.Remark, id,
	)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("duplicate name")
	}
	return err
}

// Delete 删除连接配置。
func (r *MetricsRepo) Delete(id int64) error {
	_, err := store.DB.Exec("DELETE FROM metrics_conns WHERE id=?", id)
	return err
}

func metricsToView(c model.MetricsConn) model.MetricsConnView {
	return model.MetricsConnView{
		ID: c.ID, Name: c.Name, Kind: c.Kind, URL: c.URL, Insecure: c.Insecure,
		AuthType: c.AuthType, Username: c.Username,
		HasSecret: len(c.EncryptedSecret) > 0,
		Remark:    c.Remark, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}
