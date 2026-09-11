package model

// SFTPConn 工具-SFTP：一个可连接的 SFTP 服务器配置。
type SFTPConn struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	EncryptedSecret []byte `json:"-"` // 密码密文（认证用私钥优先）
	EncryptedKey    []byte `json:"-"` // 私钥密文（PEM 原文）
	Remark          string `json:"remark"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// SFTPConnView SFTP 连接列表展示视图（不含任何密码材料）。
type SFTPConnView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	HasKey      bool   `json:"has_key"`
	Remark      string `json:"remark"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// SFTPEntry 远程目录中的一个条目（文件 / 目录）。
type SFTPEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mtime"`
	IsDir   bool   `json:"is_dir"`
	Mode    string `json:"mode"`
}

// SFTPBrowse LFTP 列目录结果。
type SFTPBrowse struct {
	Current string      `json:"current"`
	Parent  string      `json:"parent"`
	Entries []SFTPEntry `json:"entries"`
}