package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// ESRepo Elasticsearch 连接配置的数据存取。
type ESRepo struct{}

func NewESRepo() *ESRepo { return &ESRepo{} }

// List 分页查询 ES 连接配置。
func (r *ESRepo) List(keyword string, page, pageSize int) ([]model.ESConnView, int64, error) {
	where := ""
	args := []interface{}{}
	if keyword != "" {
		where = "WHERE name LIKE ? OR url LIKE ? OR remark LIKE ?"
		args = append(args, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	if err := store.DB.QueryRow("SELECT COUNT(*) FROM es_conns "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("es count: %w", err)
	}

	offset := (page - 1) * pageSize
	rows, err := store.DB.Query("SELECT id,name,url,insecure,auth_type,username,encrypted_secret,remark,created_at,updated_at FROM es_conns "+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("es list: %w", err)
	}
	defer rows.Close()

	var items []model.ESConnView
	for rows.Next() {
		var c model.ESConn
		if err := rows.Scan(&c.ID, &c.Name, &c.URL, &c.Insecure, &c.AuthType, &c.Username, &c.EncryptedSecret, &c.Remark, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("es scan: %w", err)
		}
		items = append(items, esToView(c))
	}
	return items, total, rows.Err()
}

// GetByID 按 ID 查询，含加密密文（仅供后端内部解密使用）。
func (r *ESRepo) GetByID(id int64) (*model.ESConn, error) {
	c := &model.ESConn{}
	err := store.DB.QueryRow(
		"SELECT id,name,url,insecure,auth_type,username,encrypted_secret,remark,created_at,updated_at FROM es_conns WHERE id=?",
		id,
	).Scan(&c.ID, &c.Name, &c.URL, &c.Insecure, &c.AuthType, &c.Username, &c.EncryptedSecret, &c.Remark, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("es get: %w", err)
	}
	return c, nil
}

// Create 新增 ES 连接配置。
func (r *ESRepo) Create(c *model.ESConn) (int64, error) {
	res, err := store.DB.Exec(
		"INSERT INTO es_conns(name,url,insecure,auth_type,username,encrypted_secret,remark) VALUES(?,?,?,?,?,?,?)",
		c.Name, c.URL, boolToInt(c.Insecure), c.AuthType, c.Username, c.EncryptedSecret, c.Remark,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("duplicate name")
		}
		return 0, fmt.Errorf("es create: %w", err)
	}
	return res.LastInsertId()
}

// Update 更新 ES 连接配置；encryptedSecret 为 nil 时保留原密码。
func (r *ESRepo) Update(id int64, c *model.ESConn) error {
	if c.EncryptedSecret != nil {
		_, err := store.DB.Exec(
			"UPDATE es_conns SET name=?,url=?,insecure=?,auth_type=?,username=?,encrypted_secret=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
			c.Name, c.URL, boolToInt(c.Insecure), c.AuthType, c.Username, c.EncryptedSecret, c.Remark, id,
		)
		return err
	}
	_, err := store.DB.Exec(
		"UPDATE es_conns SET name=?,url=?,insecure=?,auth_type=?,username=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
		c.Name, c.URL, boolToInt(c.Insecure), c.AuthType, c.Username, c.Remark, id,
	)
	return err
}

// Delete 删除 ES 连接配置。
func (r *ESRepo) Delete(id int64) error {
	_, err := store.DB.Exec("DELETE FROM es_conns WHERE id=?", id)
	return err
}

func esToView(c model.ESConn) model.ESConnView {
	return model.ESConnView{
		ID: c.ID, Name: c.Name, URL: c.URL, Insecure: c.Insecure,
		AuthType: c.AuthType, Username: c.Username,
		HasPassword: len(c.EncryptedSecret) > 0,
		Remark:      c.Remark, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}