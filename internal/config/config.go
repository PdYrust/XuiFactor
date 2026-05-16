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
	DefaultPath                  = "/etc/xui-factor/config.json"
	DefaultDatabasePath          = "/etc/x-ui/x-ui.db"
	DefaultBackupDir             = "/var/backups/xui-factor"
	DefaultLogLevel              = "info"
	DefaultMissingClientGrace    = 30 * time.Second
	DefaultDisabledRuleRetention = 7 * 24 * time.Hour
	DefaultAuditRetention        = 30 * 24 * time.Hour
)

type Config struct {
	DatabasePath          string
	PollInterval          time.Duration
	BusyTimeout           time.Duration
	BackupDir             string
	EnableBackups         bool
	LogLevel              string
	AutoCleanup           bool
	MissingClientGrace    time.Duration
	DisabledRuleRetention time.Duration
	AuditRetention        time.Duration
}

type fileConfig struct {
	DatabasePath          string `json:"database_path"`
	PollInterval          string `json:"poll_interval"`
	BusyTimeout           string `json:"busy_timeout"`
	BackupDir             string `json:"backup_dir"`
	EnableBackups         *bool  `json:"enable_backups"`
	LogLevel              string `json:"log_level"`
	AutoCleanup           *bool  `json:"auto_cleanup"`
	MissingClientGrace    string `json:"missing_client_grace"`
	DisabledRuleRetention string `json:"disabled_rule_retention"`
	AuditRetention        string `json:"audit_retention"`
}

func Defaults() Config {
	return Config{
		DatabasePath:          DefaultDatabasePath,
		PollInterval:          5 * time.Second,
		BusyTimeout:           5 * time.Second,
		BackupDir:             DefaultBackupDir,
		EnableBackups:         true,
		LogLevel:              DefaultLogLevel,
		AutoCleanup:           true,
		MissingClientGrace:    DefaultMissingClientGrace,
		DisabledRuleRetention: DefaultDisabledRuleRetention,
		AuditRetention:        DefaultAuditRetention,
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
		d, err := parseDuration(strings.TrimSpace(raw.PollInterval))
		if err != nil {
			return Config{}, fmt.Errorf("invalid poll_interval: %w", err)
		}
		cfg.PollInterval = d
	}
	if strings.TrimSpace(raw.BusyTimeout) != "" {
		d, err := parseDuration(strings.TrimSpace(raw.BusyTimeout))
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
	if raw.AutoCleanup != nil {
		cfg.AutoCleanup = *raw.AutoCleanup
	}
	if strings.TrimSpace(raw.MissingClientGrace) != "" {
		d, err := parseDuration(strings.TrimSpace(raw.MissingClientGrace))
		if err != nil {
			return Config{}, fmt.Errorf("invalid missing_client_grace: %w", err)
		}
		cfg.MissingClientGrace = d
	}
	if strings.TrimSpace(raw.DisabledRuleRetention) != "" {
		d, err := parseDuration(strings.TrimSpace(raw.DisabledRuleRetention))
		if err != nil {
			return Config{}, fmt.Errorf("invalid disabled_rule_retention: %w", err)
		}
		cfg.DisabledRuleRetention = d
	}
	if strings.TrimSpace(raw.AuditRetention) != "" {
		d, err := parseDuration(strings.TrimSpace(raw.AuditRetention))
		if err != nil {
			return Config{}, fmt.Errorf("invalid audit_retention: %w", err)
		}
		cfg.AuditRetention = d
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
	if c.MissingClientGrace <= 0 {
		return errors.New("missing_client_grace must be positive")
	}
	if c.DisabledRuleRetention <= 0 {
		return errors.New("disabled_rule_retention must be positive")
	}
	if c.AuditRetention <= 0 {
		return errors.New("audit_retention must be positive")
	}
	return nil
}

func parseDuration(value string) (time.Duration, error) {
	if d, err := time.ParseDuration(value); err == nil {
		return d, nil
	}
	if !strings.HasSuffix(value, "d") {
		return 0, fmt.Errorf("time: invalid duration %q", value)
	}
	daysPart := strings.TrimSuffix(value, "d")
	if strings.TrimSpace(daysPart) == "" {
		return 0, fmt.Errorf("time: invalid duration %q", value)
	}
	days, err := time.ParseDuration(daysPart + "h")
	if err != nil {
		return 0, fmt.Errorf("time: invalid duration %q", value)
	}
	return days * 24, nil
}
