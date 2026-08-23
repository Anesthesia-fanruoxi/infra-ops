// Package config 定义全局配置结构，并从 settings KV 构建运行配置。
package config

import (
	"strconv"

	"infra-ops/store"
)

// Config 全局配置结构。
type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Security SecurityConfig
	Auth     AuthConfig
	SSH      SSHConfig
	Probe    ProbeConfig
}

type ServerConfig struct {
	Host string
	Port int
}

type DatabaseConfig struct {
	Path string
}

type SecurityConfig struct {
	SecretKey string
}

type AuthConfig struct {
	Username           string
	PasswordHash       string
	MustChangePassword bool
}

type SSHConfig struct {
	Timeout       int
	HostKeyPolicy string
}

type ProbeConfig struct {
	Interval    int
	Concurrency int
}

// FromSettings 从 settings KV 构建配置，缺失项回退默认值。
func FromSettings(m map[string]string) *Config {
	cfg := defaultConfig()
	if h := m[store.SettingServerHost]; h != "" {
		cfg.Server.Host = h
	}
	cfg.Security.SecretKey = m[store.SettingSecretKey]
	cfg.Auth.Username = m[store.SettingAuthUsername]
	cfg.Auth.PasswordHash = m[store.SettingAuthPasswordHash]
	cfg.Auth.MustChangePassword = m[store.SettingAuthMustChange] == "1"
	if p, err := strconv.Atoi(m[store.SettingServerPort]); err == nil && p > 0 {
		cfg.Server.Port = p
	}
	if t, err := strconv.Atoi(m[store.SettingSSHTimeout]); err == nil && t > 0 {
		cfg.SSH.Timeout = t
	}
	if v := m[store.SettingSSHHostKeyPolicy]; v != "" {
		cfg.SSH.HostKeyPolicy = v
	}
	if i, err := strconv.Atoi(m[store.SettingProbeInterval]); err == nil && i > 0 {
		cfg.Probe.Interval = i
	}
	if c, err := strconv.Atoi(m[store.SettingProbeConcurrency]); err == nil && c > 0 {
		cfg.Probe.Concurrency = c
	}
	return cfg
}

func defaultConfig() *Config {
	return &Config{
		Server: ServerConfig{Port: 8090},
		Database: DatabaseConfig{Path: "data/infra-ops.db"},
		SSH:   SSHConfig{Timeout: 8, HostKeyPolicy: "tofu"},
		Probe: ProbeConfig{Interval: 60, Concurrency: 5},
	}
}
