package store

import (
	"database/sql"
	"fmt"
)

// HostKeyRepo host key TOFU 指纹存取。
type HostKeyRepo struct{}

func NewHostKeyRepo() *HostKeyRepo {
	return &HostKeyRepo{}
}

// Get 查询指定地址的指纹，不存在返回空字符串。
func (r *HostKeyRepo) Get(addr string) (string, error) {
	var fp string
	err := DB.QueryRow("SELECT fingerprint FROM host_keys WHERE addr=?", addr).Scan(&fp)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("host_key get: %w", err)
	}
	return fp, nil
}

// Save 保存首次见到的指纹。
func (r *HostKeyRepo) Save(addr, fingerprint string) error {
	_, err := DB.Exec(
		"INSERT INTO host_keys(addr,fingerprint) VALUES(?,?)",
		addr, fingerprint,
	)
	return err
}

// Delete 删除指定地址的指纹（用于重置）。
func (r *HostKeyRepo) Delete(addr string) error {
	_, err := DB.Exec("DELETE FROM host_keys WHERE addr=?", addr)
	return err
}
