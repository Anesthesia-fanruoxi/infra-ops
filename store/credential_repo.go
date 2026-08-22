package store

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
)

// CredentialRepo 凭据数据存取。
type CredentialRepo struct{}

func NewCredentialRepo() *CredentialRepo {
	return &CredentialRepo{}
}

// GetByID 按 ID 查询凭据。
func (r *CredentialRepo) GetByID(id int64) (*model.Credential, error) {
	c := &model.Credential{}
	err := DB.QueryRow(
		"SELECT id,name,type,username,encrypted_secret,fingerprint,remark,created_at,updated_at FROM credentials WHERE id=?",
		id,
	).Scan(&c.ID, &c.Name, &c.Type, &c.Username, &c.EncryptedSecret, &c.Fingerprint, &c.Remark, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("credential get: %w", err)
	}
	return c, nil
}

// List 分页查询凭据列表。
func (r *CredentialRepo) List(keyword string, page, pageSize int) ([]model.Credential, int64, error) {
	where := ""
	args := []interface{}{}
	if keyword != "" {
		where = "WHERE name LIKE ? OR remark LIKE ?"
		args = append(args, "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	countSQL := "SELECT COUNT(*) FROM credentials " + where
	if err := DB.QueryRow(countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("credential count: %w", err)
	}

	offset := (page - 1) * pageSize
	query := "SELECT id,name,type,username,encrypted_secret,fingerprint,remark,created_at,updated_at FROM credentials " + where + " ORDER BY id DESC LIMIT ? OFFSET ?"
	queryArgs := append(args, pageSize, offset)

	rows, err := DB.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("credential list: %w", err)
	}
	defer rows.Close()

	var items []model.Credential
	for rows.Next() {
		var c model.Credential
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Username, &c.EncryptedSecret, &c.Fingerprint, &c.Remark, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("credential scan: %w", err)
		}
		items = append(items, c)
	}
	return items, total, rows.Err()
}

// Create 新增凭据，返回新 ID。
func (r *CredentialRepo) Create(c *model.Credential) (int64, error) {
	res, err := DB.Exec(
		"INSERT INTO credentials(name,type,username,encrypted_secret,fingerprint,remark) VALUES(?,?,?,?,?,?)",
		c.Name, c.Type, c.Username, c.EncryptedSecret, c.Fingerprint, c.Remark,
	)
	if err != nil {
		if isUniqueErr(err) {
			return 0, fmt.Errorf("duplicate name: %w", err)
		}
		return 0, fmt.Errorf("credential create: %w", err)
	}
	return res.LastInsertId()
}

// Update 更新凭据（encryptedSecret 为空则不改密钥材料）。
func (r *CredentialRepo) Update(id int64, name, username, remark, fingerprint string, encryptedSecret []byte) error {
	if encryptedSecret != nil {
		_, err := DB.Exec(
			"UPDATE credentials SET name=?, username=?, encrypted_secret=?, fingerprint=?, remark=?, updated_at=datetime('now','localtime') WHERE id=?",
			name, username, encryptedSecret, fingerprint, remark, id,
		)
		return err
	}
	_, err := DB.Exec(
		"UPDATE credentials SET name=?, username=?, remark=?, updated_at=datetime('now','localtime') WHERE id=?",
		name, username, remark, id,
	)
	return err
}

// Delete 删除凭据。
func (r *CredentialRepo) Delete(id int64) error {
	_, err := DB.Exec("DELETE FROM credentials WHERE id=?", id)
	return err
}

// Count 统计凭据总数。
func (r *CredentialRepo) Count() (int64, error) {
	var total int64
	err := DB.QueryRow("SELECT COUNT(*) FROM credentials").Scan(&total)
	return total, err
}

// CountByCredentialID 统计引用该凭据的主机数。
func (r *CredentialRepo) CountByCredentialID(id int64) (int, error) {
	var count int
	err := DB.QueryRow("SELECT COUNT(*) FROM hosts WHERE credential_id=?", id).Scan(&count)
	return count, err
}

func isUniqueErr(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
