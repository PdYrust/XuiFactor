package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	DefaultPath         = "/etc/xui-factor/config.json"
	DefaultDatabasePath = "/etc/x-ui/x-ui.db"
	DefaultBackupDir    = "/var/backups/xui-factor"
	DefaultLogLevel     = "info"
)

type Config struct {
	DatabasePath  string
	PollInterval  time.Duration
	BusyTimeout   time.Duration
	BackupDir     string
	EnableBackups bool
	LogLevel      string
}

type fileConfig struct {
	DatabasePath  string `json:"database_path"`
	PollInterval  string `json:"poll_interval"`
	BusyTimeout   string `json:"busy_timeout"`
	BackupDir     string `json:"backup_dir"`
	EnableBackups *bool  `json:"enable_backups"`
	LogLevel      string `json:"log_level"`
}

func Defaults() Config {
	return Config{
		DatabasePath:  DefaultDatabasePath,
		PollInterval:  5 * time.Second,
		BusyTimeout:   5 * time.Second,
		BackupDir:     DefaultBackupDir,
		EnableBackups: true,
		LogLevel:      DefaultLogLevel,
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		path = DefaultPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var raw fileConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if strings.TrimSpace(raw.DatabasePath) != "" {
		cfg.DatabasePath = strings.TrimSpace(raw.DatabasePath)
	}
	if strings.TrimSpace(raw.PollInterval) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(raw.PollInterval))
		if err != nil {
			return Config{}, fmt.Errorf("invalid poll_interval: %w", err)
		}
		cfg.PollInterval = d
	}
	if strings.TrimSpace(raw.BusyTimeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(raw.BusyTimeout))
		if err != nil {
			return Config{}, fmt.Errorf("invalid busy_timeout: %w", err)
		}
		cfg.BusyTimeout = d
	}
	if strings.TrimSpace(raw.BackupDir) != "" {
		cfg.BackupDir = strings.TrimSpace(raw.BackupDir)
	}
	if raw.EnableBackups != nil {
		cfg.EnableBackups = *raw.EnableBackups
	}
	if strings.TrimSpace(raw.LogLevel) != "" {
		cfg.LogLevel = strings.TrimSpace(raw.LogLevel)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.DatabasePath) == "" {
		return errors.New("database_path is required")
	}
	if c.PollInterval <= 0 {
		return errors.New("poll_interval must be positive")
	}
	if c.BusyTimeout <= 0 {
		return errors.New("busy_timeout must be positive")
	}
	if strings.TrimSpace(c.BackupDir) == "" {
		return errors.New("backup_dir is required")
	}
	if strings.TrimSpace(c.LogLevel) == "" {
		return errors.New("log_level is required")
	}
	return nil
}
