// settings KV 配置存取与首次启动引导初始化。
package setting

import (
	"database/sql"

	"infra-ops/store"
)

// settings 表键常量。
const (
	SettingSecretKey        = "security.secret_key"
	SettingSSHTimeout       = "ssh.timeout"
	SettingSSHHostKeyPolicy = "ssh.host_key_policy"
	SettingProbeInterval    = "probe.interval"
	SettingProbeConcurrency = "probe.concurrency"
	SettingDeployConc       = "deploy.concurrency"
	SettingLogRetentionDays = "deploy.log_retention_days"

	// AI（MySQL 工具的「自然语言生成 SQL」）：内置厂商 + OpenAI 兼容接口三件套 + 超时。
	// 选定内置厂商后接口地址由平台派生，用户只需填密钥；密钥以密文（Base64 包裹）存库，读取时由 crypto.Service 解密。
	SettingAIProvider = "ai.provider"
	SettingAIEnabled  = "ai.enabled"
	SettingAIBaseURL  = "ai.base_url"
	SettingAIModel    = "ai.model"
	SettingAIAPIKey   = "ai.api_key"
	SettingAITimeout  = "ai.timeout"
)

// SettingsRepo settings 表 KV 存取。
type SettingsRepo struct{}

// NewSettingsRepo 创建 settings 存取对象。
func NewSettingsRepo() *SettingsRepo { return &SettingsRepo{} }

// Get 读取单个配置项，不存在返回空串。
func (r *SettingsRepo) Get(key string) (string, error) {
	var v string
	err := store.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// GetAll 读取全部配置项。
func (r *SettingsRepo) GetAll() (map[string]string, error) {
	rows, err := store.DB.Query(`SELECT key, value FROM settings`)
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
	_, err := store.DB.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,
		updated_at=datetime('now','localtime')`, key, value)
	return err
}

// EnsureBootstrap 首次启动写入默认配置；已初始化则跳过。
// 返回是否执行了本次初始化。
func (r *SettingsRepo) EnsureBootstrap(secretKey string) (bool, error) {
	v, err := r.Get(SettingSecretKey)
	if err != nil {
		return false, err
	}
	if v != "" {
		return false, nil
	}
	defaults := [][2]string{
		{SettingSecretKey, secretKey},
		{SettingSSHTimeout, "8"},
		{SettingSSHHostKeyPolicy, "tofu"},
		{SettingProbeInterval, "60"},
		{SettingProbeConcurrency, "auto"},
		{SettingDeployConc, "auto"},
		{SettingLogRetentionDays, "30"},
	}
	for _, kv := range defaults {
		if err := r.Set(kv[0], kv[1]); err != nil {
			return false, err
		}
	}
	return true, nil
}

// EnsureRuntimeDefaults 补齐后续版本新增的运行配置键。
// 仅 INSERT 缺失项，不覆盖已有值；每次启动幂等调用。
func (r *SettingsRepo) EnsureRuntimeDefaults() error {
	defaults := [][2]string{
		{SettingProbeInterval, "60"},
		{SettingProbeConcurrency, "auto"},
		{SettingDeployConc, "auto"},
		{SettingLogRetentionDays, "30"},
		{SettingAIProvider, ""},
		{SettingAIEnabled, "0"},
		{SettingAIBaseURL, ""},
		{SettingAIModel, ""},
		{SettingAITimeout, "90"},
	}
	for _, kv := range defaults {
		if _, err := store.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}
