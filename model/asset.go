package model

// StackAsset 部署资产：套件部署所需的离线物料（如 Hive HA 的 MySQL Connector/J 驱动），
// 由用户上传或服务端代下后登记于此；文件本体落盘 data/assets/<asset_key>/<version>/<file_name>，
// 部署时由引擎经 SFTP 分发到目标机（套件经 stackkit.AssetProvisioner 声明需求）。
type StackAsset struct {
	ID        int64  `json:"id"`
	AssetKey  string `json:"asset_key"` // 资产键（如 mysql-connector-j）
	Version   string `json:"version"`   // 版本（如 8.4.0）
	FileName  string `json:"file_name"` // 落盘文件名
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Source    string `json:"source"` // upload=本地上传 / fetch=服务端代下
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// StackAssetNeed 套件声明的资产需求（stackkit.AssetProvisioner 返回值）。
type StackAssetNeed struct {
	Key       string   `json:"key"`        // 资产键
	Version   string   `json:"version"`    // 期望版本
	FileName  string   `json:"file_name"`  // 期望文件名（上传校验）
	RemoteDir string   `json:"remote_dir"` // 目标机目录，可含 {{home_dir}} 占位
	Desc      string   `json:"desc"`       // 展示说明
	FetchURLs []string `json:"fetch_urls"` // 服务端代下的镜像地址（仅允许此处声明的 URL，防 SSRF）
}

// StackAssetStatus 资产需求的就绪状态（检查接口返回：需求 + 本地是否已就绪）。
type StackAssetStatus struct {
	StackAssetNeed
	Satisfied bool        `json:"satisfied"` // 服务端已有该 key+version 的资产文件
	Asset     *StackAsset `json:"asset,omitempty"`
}
