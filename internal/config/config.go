package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
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

	BackendAuto     = "auto"
	BackendSQLite   = "sqlite"
	BackendPostgres = "postgres"
)

type Config struct {
	DatabasePath          string
	DatabaseBackend       string
	DatabaseDSN           string
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
	DatabaseBackend       string `json:"database_backend"`
	DatabaseDSN           string `json:"database_dsn"`
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
		DatabaseBackend:       BackendAuto,
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

// panelEnvFilePaths lists the service environment files 3x-ui writes its
// database settings into, covering the distro layouts the panel supports:
// Debian/Ubuntu use /etc/default, Arch /etc/conf.d, RHEL /etc/sysconfig.
var panelEnvFilePaths = []string{
	"/etc/default/x-ui",
	"/etc/conf.d/x-ui",
	"/etc/sysconfig/x-ui",
}

// applyDatabaseEnv fills database settings from the environment when the
// config file leaves them unset. Process environment variables win over the
// panel service environment files (x-ui reads the same variables).
func applyDatabaseEnv(cfg *Config) {
	panelValues := readPanelEnvValues(panelEnvFilePaths)
	lookup := func(key string) string {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
		return panelValues[key]
	}
	backend := strings.ToLower(strings.TrimSpace(cfg.DatabaseBackend))
	if backend == "" || backend == BackendAuto {
		if value := lookup("XUI_DB_TYPE"); value != "" {
			cfg.DatabaseBackend = value
		}
	}
	if strings.TrimSpace(cfg.DatabaseDSN) == "" {
		if value := lookup("XUI_DB_DSN"); value != "" {
			cfg.DatabaseDSN = value
		}
	}
}

// readPanelEnvValues extracts XUI_DB_TYPE and XUI_DB_DSN from 3x-ui style
// KEY=VALUE environment files. Missing files are skipped and the first value
// found for a key wins.
func readPanelEnvValues(paths []string) map[string]string {
	values := make(map[string]string, 2)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if rest, ok := strings.CutPrefix(line, "export"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
				line = strings.TrimSpace(rest)
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			if key != "XUI_DB_TYPE" && key != "XUI_DB_DSN" {
				continue
			}
			if _, exists := values[key]; exists {
				continue
			}
			value = strings.TrimSpace(value)
			if len(value) >= 2 {
				first, last := value[0], value[len(value)-1]
				if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
					value = value[1 : len(value)-1]
				}
			}
			values[key] = value
		}
	}
	return values
}

// ResolvedBackend picks the database backend: the configured value wins, then
// a configured DSN implies postgres, and sqlite is the fallback.
func (c Config) ResolvedBackend() string {
	switch strings.ToLower(strings.TrimSpace(c.DatabaseBackend)) {
	case BackendSQLite:
		return BackendSQLite
	case BackendPostgres, "postgresql", "pg":
		return BackendPostgres
	}
	if strings.TrimSpace(c.DatabaseDSN) != "" {
		return BackendPostgres
	}
	return BackendSQLite
}

// DatabaseLabel is the operator-facing database identifier with any password
// removed, safe to print in command output.
func (c Config) DatabaseLabel() string {
	if c.ResolvedBackend() == BackendPostgres {
		return RedactDSN(c.DatabaseDSN)
	}
	return c.DatabasePath
}

func RedactDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "REDACTED")
		}
		query := u.Query()
		for key := range query {
			if strings.EqualFold(key, "password") || strings.EqualFold(key, "sslpassword") {
				query.Set(key, "REDACTED")
			}
		}
		u.RawQuery = query.Encode()
		return u.String()
	}
	passwordField := regexp.MustCompile(`(?i)(password\s*=\s*)('[^']*'|"[^"]*"|\S+)`)
	return passwordField.ReplaceAllString(dsn, "${1}REDACTED")
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		path = DefaultPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			applyDatabaseEnv(&cfg)
			if err := cfg.Validate(); err != nil {
				return Config{}, err
			}
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
	if strings.TrimSpace(raw.DatabaseBackend) != "" {
		cfg.DatabaseBackend = strings.TrimSpace(raw.DatabaseBackend)
	}
	if strings.TrimSpace(raw.DatabaseDSN) != "" {
		cfg.DatabaseDSN = strings.TrimSpace(raw.DatabaseDSN)
	}
	applyDatabaseEnv(&cfg)
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
	switch c.ResolvedBackend() {
	case BackendPostgres:
		if strings.TrimSpace(c.DatabaseDSN) == "" {
			return errors.New("database_dsn is required for the postgres backend")
		}
	default:
		if strings.TrimSpace(c.DatabasePath) == "" {
			return errors.New("database_path is required")
		}
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
