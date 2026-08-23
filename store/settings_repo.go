// settings KV 配置存取与首次启动引导初始化。
package store

import "database/sql"

// settings 表键常量。
const (
	SettingServerHost       = "server.host"
	SettingServerPort       = "server.port"
	SettingSecretKey        = "security.secret_key"
	SettingAuthUsername     = "auth.username"
	SettingAuthPasswordHash = "auth.password_hash"
	SettingAuthMustChange   = "auth.must_change_password"
	SettingSSHTimeout       = "ssh.timeout"
	SettingSSHHostKeyPolicy = "ssh.host_key_policy"
	SettingProbeInterval    = "probe.interval"
	SettingProbeConcurrency = "probe.concurrency"
)

// SettingsRepo settings 表 KV 存取。
type SettingsRepo struct{}

// NewSettingsRepo 创建 settings 存取对象。
func NewSettingsRepo() *SettingsRepo { return &SettingsRepo{} }

// Get 读取单个配置项，不存在返回空串。
func (r *SettingsRepo) Get(key string) (string, error) {
	var v string
	err := DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// GetAll 读取全部配置项。
func (r *SettingsRepo) GetAll() (map[string]string, error) {
	rows, err := DB.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}

// Set 写入单个配置项（upsert）。
func (r *SettingsRepo) Set(key, value string) error {
	_, err := DB.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,
		updated_at=datetime('now','localtime')`, key, value)
	return err
}

// EnsureBootstrap 首次启动写入默认配置；已初始化则跳过。
// 返回是否执行了本次初始化。
func (r *SettingsRepo) EnsureBootstrap(secretKey, passwordHash string) (bool, error) {
	v, err := r.Get(SettingSecretKey)
	if err != nil {
		return false, err
	}
	if v != "" {
		return false, nil
	}
	defaults := [][2]string{
		{SettingServerHost, "127.0.0.1"},
		{SettingServerPort, "8090"},
		{SettingSecretKey, secretKey},
		{SettingAuthUsername, "admin"},
		{SettingAuthPasswordHash, passwordHash},
		{SettingAuthMustChange, "1"},
		{SettingSSHTimeout, "8"},
		{SettingSSHHostKeyPolicy, "tofu"},
		{SettingProbeInterval, "60"},
		{SettingProbeConcurrency, "5"},
	}
	for _, kv := range defaults {
		if err := r.Set(kv[0], kv[1]); err != nil {
			return false, err
		}
	}
	return true, nil
}
