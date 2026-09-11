package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// SFTPRepo SFTP 连接配置的数据存取。
type SFTPRepo struct{}

func NewSFTPRepo() *SFTPRepo { return &SFTPRepo{} }

// List 分页查询 SFTP 连接配置。
func (r *SFTPRepo) List(keyword string, page, pageSize int) ([]model.SFTPConnView, int64, error) {
	where := ""
	args := []interface{}{}
	if keyword != "" {
		where = "WHERE name LIKE ? OR host LIKE ? OR username LIKE ? OR remark LIKE ?"
		args = append(args, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	if err := store.DB.QueryRow("SELECT COUNT(*) FROM sftp_conns "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("sftp count: %w", err)
	}

	offset := (page - 1) * pageSize
	rows, err := store.DB.Query("SELECT id,name,host,port,username,encrypted_secret,encrypted_key,remark,created_at,updated_at FROM sftp_conns "+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("sftp list: %w", err)
	}
	defer rows.Close()

	var items []model.SFTPConnView
	for rows.Next() {
		var c model.SFTPConn
		if err := rows.Scan(&c.ID, &c.Name, &c.Host, &c.Port, &c.Username, &c.EncryptedSecret, &c.EncryptedKey, &c.Remark, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("sftp scan: %w", err)
		}
		items = append(items, sftpToView(c))
	}
	return items, total, rows.Err()
}

// GetByID 按 ID 查询，含加密密文/私钥（仅供后端内部解密使用）。
func (r *SFTPRepo) GetByID(id int64) (*model.SFTPConn, error) {
	c := &model.SFTPConn{}
	err := store.DB.QueryRow(
		"SELECT id,name,host,port,username,encrypted_secret,encrypted_key,remark,created_at,updated_at FROM sftp_conns WHERE id=?",
		id,
	).Scan(&c.ID, &c.Name, &c.Host, &c.Port, &c.Username, &c.EncryptedSecret, &c.EncryptedKey, &c.Remark, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sftp get: %w", err)
	}
	return c, nil
}

// Create 新增 SFTP 连接配置。
func (r *SFTPRepo) Create(c *model.SFTPConn) (int64, error) {
	res, err := store.DB.Exec(
		"INSERT INTO sftp_conns(name,host,port,username,encrypted_secret,encrypted_key,remark) VALUES(?,?,?,?,?,?,?)",
		c.Name, c.Host, c.Port, c.Username, c.EncryptedSecret, c.EncryptedKey, c.Remark,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("duplicate name")
		}
		return 0, fmt.Errorf("sftp create: %w", err)
	}
	return res.LastInsertId()
}

// Update 更新 SFTP 连接配置；传入非 nil 时才覆盖对应密文，nil 则保留原值。
func (r *SFTPRepo) Update(id int64, c *model.SFTPConn) error {
	if c.EncryptedSecret != nil || c.EncryptedKey != nil {
		_, err := store.DB.Exec(
			"UPDATE sftp_conns SET name=?,host=?,port=?,username=?,encrypted_secret=?,encrypted_key=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
			c.Name, c.Host, c.Port, c.Username, c.EncryptedSecret, c.EncryptedKey, c.Remark, id,
		)
		return err
	}
	_, err := store.DB.Exec(
		"UPDATE sftp_conns SET name=?,host=?,port=?,username=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
		c.Name, c.Host, c.Port, c.Username, c.Remark, id,
	)
	return err
}

// Delete 删除 SFTP 连接配置。
func (r *SFTPRepo) Delete(id int64) error {
	_, err := store.DB.Exec("DELETE FROM sftp_conns WHERE id=?", id)
	return err
}

func sftpToView(c model.SFTPConn) model.SFTPConnView {
	return model.SFTPConnView{
		ID: c.ID, Name: c.Name, Host: c.Host, Port: c.Port,
		Username: c.Username, HasPassword: len(c.EncryptedSecret) > 0,
		HasKey: len(c.EncryptedKey) > 0,
		Remark: c.Remark, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}