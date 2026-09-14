package repo

import (
	"database/sql"
	"fmt"
	"strings"

	"infra-ops/model"
	"infra-ops/store"
)

// RegistryRepo 镜像仓库配置的数据存取。
type RegistryRepo struct{}

func NewRegistryRepo() *RegistryRepo { return &RegistryRepo{} }

// List 分页查询 registry 配置。
func (r *RegistryRepo) List(keyword string, page, pageSize int) ([]model.RegistryRepoView, int64, error) {
	where := ""
	args := []interface{}{}
	if keyword != "" {
		where = "WHERE name LIKE ? OR url LIKE ? OR remark LIKE ?"
		args = append(args, "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}

	var total int64
	if err := store.DB.QueryRow("SELECT COUNT(*) FROM registries "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("registry count: %w", err)
	}

	offset := (page - 1) * pageSize
	rows, err := store.DB.Query("SELECT id,name,url,insecure,auth_type,username,encrypted_secret,remark,created_at,updated_at FROM registries "+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("registry list: %w", err)
	}
	defer rows.Close()

	var items []model.RegistryRepoView
	for rows.Next() {
		var reg model.Registry
		if err := rows.Scan(&reg.ID, &reg.Name, &reg.URL, &reg.Insecure, &reg.AuthType, &reg.Username, &reg.EncryptedSecret, &reg.Remark, &reg.CreatedAt, &reg.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("registry scan: %w", err)
		}
		items = append(items, toRepoView(reg))
	}
	return items, total, rows.Err()
}

// GetByID 按 ID 查询，含加密密文（仅供后端内部解密使用）。
func (r *RegistryRepo) GetByID(id int64) (*model.Registry, error) {
	reg := &model.Registry{}
	err := store.DB.QueryRow(
		"SELECT id,name,url,insecure,auth_type,username,encrypted_secret,remark,created_at,updated_at FROM registries WHERE id=?",
		id,
	).Scan(&reg.ID, &reg.Name, &reg.URL, &reg.Insecure, &reg.AuthType, &reg.Username, &reg.EncryptedSecret, &reg.Remark, &reg.CreatedAt, &reg.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("registry get: %w", err)
	}
	return reg, nil
}

// Create 新增 registry 配置，返回新 ID。
func (r *RegistryRepo) Create(reg *model.Registry) (int64, error) {
	res, err := store.DB.Exec(
		"INSERT INTO registries(name,url,insecure,auth_type,username,encrypted_secret,remark) VALUES(?,?,?,?,?,?,?)",
		reg.Name, reg.URL, boolToInt(reg.Insecure), reg.AuthType, reg.Username, reg.EncryptedSecret, reg.Remark,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("duplicate name")
		}
		return 0, fmt.Errorf("registry create: %w", err)
	}
	return res.LastInsertId()
}

// Update 更新 registry 配置；encryptedSecret 为 nil 时保留原密码。
func (r *RegistryRepo) Update(id int64, reg *model.Registry) error {
	if reg.EncryptedSecret != nil {
		_, err := store.DB.Exec(
			"UPDATE registries SET name=?,url=?,insecure=?,auth_type=?,username=?,encrypted_secret=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
			reg.Name, reg.URL, boolToInt(reg.Insecure), reg.AuthType, reg.Username, reg.EncryptedSecret, reg.Remark, id,
		)
		return err
	}
	_, err := store.DB.Exec(
		"UPDATE registries SET name=?,url=?,insecure=?,auth_type=?,username=?,remark=?,updated_at=datetime('now','localtime') WHERE id=?",
		reg.Name, reg.URL, boolToInt(reg.Insecure), reg.AuthType, reg.Username, reg.Remark, id,
	)
	return err
}

// Delete 删除 registry 配置。
func (r *RegistryRepo) Delete(id int64) error {
	_, err := store.DB.Exec("DELETE FROM registries WHERE id=?", id)
	return err
}

func toRepoView(reg model.Registry) model.RegistryRepoView {
	return model.RegistryRepoView{
		ID:          reg.ID,
		Name:        reg.Name,
		URL:         reg.URL,
		Insecure:    reg.Insecure,
		AuthType:    reg.AuthType,
		Username:    reg.Username,
		HasPassword: len(reg.EncryptedSecret) > 0,
		Remark:      reg.Remark,
		CreatedAt:   reg.CreatedAt,
		UpdatedAt:   reg.UpdatedAt,
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
