// Package config 负责配置加载：env 优先、config.yaml 兜底。
package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config 全局配置结构。
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Security SecurityConfig `yaml:"security"`
	Auth     AuthConfig     `yaml:"auth"`
	SSH      SSHConfig      `yaml:"ssh"`
	Probe    ProbeConfig    `yaml:"probe"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type SecurityConfig struct {
	SecretKey string `yaml:"secret_key"`
}

type AuthConfig struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"`
}

type SSHConfig struct {
	Timeout       int    `yaml:"timeout"`
	HostKeyPolicy string `yaml:"host_key_policy"`
}

type ProbeConfig struct {
	Interval    int `yaml:"interval"`
	Concurrency int `yaml:"concurrency"`
}

// Load 加载配置：先读 yaml，再用环境变量覆盖。
func Load(path string) (*Config, error) {
	cfg := defaultConfig()

	if path != "" {
		if err := loadYAML(path, cfg); err != nil {
			return nil, fmt.Errorf("load yaml: %w", err)
		}
	}

	applyEnv(cfg)
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Server:   ServerConfig{Port: 8090},
		Database: DatabaseConfig{Path: "data/infra-ops.db"},
		SSH:      SSHConfig{Timeout: 8, HostKeyPolicy: "tofu"},
		Probe:    ProbeConfig{Interval: 60, Concurrency: 5},
	}
}

func loadYAML(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在则跳过，不报错
		}
		return err
	}
	return yaml.Unmarshal(data, cfg)
}

// applyEnv 用环境变量覆盖配置。
func applyEnv(cfg *Config) {
	if v := os.Getenv("INFRA_OPS_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = p
		}
	}
	if v := os.Getenv("INFRA_OPS_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("INFRA_OPS_SECRET"); v != "" {
		cfg.Security.SecretKey = v
	}
	if v := os.Getenv("INFRA_OPS_USERNAME"); v != "" {
		cfg.Auth.Username = v
	}
	if v := os.Getenv("INFRA_OPS_PASSWORD_HASH"); v != "" {
		cfg.Auth.PasswordHash = v
	}
	if v := os.Getenv("INFRA_OPS_SSH_TIMEOUT"); v != "" {
		if t, err := strconv.Atoi(v); err == nil {
			cfg.SSH.Timeout = t
		}
	}
	if v := os.Getenv("INFRA_OPS_HOST_KEY_POLICY"); v != "" {
		cfg.SSH.HostKeyPolicy = v
	}
	if v := os.Getenv("INFRA_OPS_PROBE_INTERVAL"); v != "" {
		if t, err := strconv.Atoi(v); err == nil {
			cfg.Probe.Interval = t
		}
	}
	if v := os.Getenv("INFRA_OPS_PROBE_CONCURRENCY"); v != "" {
		if c, err := strconv.Atoi(v); err == nil {
			cfg.Probe.Concurrency = c
		}
	}
}
