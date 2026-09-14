package model

// Registry 工具-镜像仓库：一个可连接的 Docker Registry 配置。
type Registry struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	URL             string `json:"url"`
	Insecure        bool   `json:"insecure"`
	AuthType        string `json:"auth_type"` // anonymous | basic
	Username        string `json:"username"`
	HasPassword     bool   `json:"has_password"`
	EncryptedSecret []byte `json:"-"`
	Remark          string `json:"remark"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// RegistryRepoView Registry 列表展示视图（不返回任何密码材料）。
type RegistryRepoView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Insecure    bool   `json:"insecure"`
	AuthType    string `json:"auth_type"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	Remark      string `json:"remark"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// RegistryImage Registry 中的仓库（镜像名）列表条目。
type RegistryImage struct {
	Name string `json:"name"`
}
