package repo

import (
	"database/sql"
	"fmt"

	"infra-ops/model"
	"infra-ops/store"
)

// AssetRepo 部署资产的数据存取：只管元数据登记，文件本体由调用方落盘。
type AssetRepo struct{}

func NewAssetRepo() *AssetRepo { return &AssetRepo{} }

// List 全量列出资产（资产数量级很小，不分页）。
func (r *AssetRepo) List() ([]model.StackAsset, error) {
	rows, err := store.DB.Query(
		"SELECT id,asset_key,version,file_name,size_bytes,sha256,source,created_at,updated_at FROM stack_assets ORDER BY asset_key,version")
	if err != nil {
		return nil, fmt.Errorf("asset list: %w", err)
	}
	defer rows.Close()
	var items []model.StackAsset
	for rows.Next() {
		var a model.StackAsset
		if err := rows.Scan(&a.ID, &a.AssetKey, &a.Version, &a.FileName, &a.SizeBytes, &a.SHA256, &a.Source, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("asset scan: %w", err)
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// GetByKeyVersion 按 key+version 查询；不存在返回 nil（不视为错误）。
func (r *AssetRepo) GetByKeyVersion(key, version string) (*model.StackAsset, error) {
	a := &model.StackAsset{}
	err := store.DB.QueryRow(
		"SELECT id,asset_key,version,file_name,size_bytes,sha256,source,created_at,updated_at FROM stack_assets WHERE asset_key=? AND version=?",
		key, version,
	).Scan(&a.ID, &a.AssetKey, &a.Version, &a.FileName, &a.SizeBytes, &a.SHA256, &a.Source, &a.CreatedAt, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("asset get: %w", err)
	}
	return a, nil
}

// Upsert 登记或覆盖资产元数据（同 key+version 重复上传/代下时原地更新）。
func (r *AssetRepo) Upsert(a *model.StackAsset) error {
	_, err := store.DB.Exec(`INSERT INTO stack_assets(asset_key,version,file_name,size_bytes,sha256,source)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(asset_key,version) DO UPDATE SET
		file_name=excluded.file_name, size_bytes=excluded.size_bytes, sha256=excluded.sha256,
		source=excluded.source, updated_at=datetime('now','localtime')`,
		a.AssetKey, a.Version, a.FileName, a.SizeBytes, a.SHA256, a.Source)
	return err
}

// Delete 删除资产登记。
func (r *AssetRepo) Delete(id int64) error {
	_, err := store.DB.Exec("DELETE FROM stack_assets WHERE id=?", id)
	return err
}
