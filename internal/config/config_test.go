package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanupConfigDefaults(t *testing.T) {
	cfg := Defaults()
	if !cfg.AutoCleanup {
		t.Fatal("auto cleanup default = false, want true")
	}
	if cfg.MissingClientGrace != 30*time.Second {
		t.Fatalf("missing grace = %s, want 30s", cfg.MissingClientGrace)
	}
	if cfg.DisabledRuleRetention != 7*24*time.Hour {
		t.Fatalf("disabled retention = %s, want 168h", cfg.DisabledRuleRetention)
	}
	if cfg.AuditRetention != 30*24*time.Hour {
		t.Fatalf("audit retention = %s, want 720h", cfg.AuditRetention)
	}
}

func TestCleanupConfigLoadsOptionalFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{
  "auto_cleanup": false,
  "missing_client_grace": "45s",
  "disabled_rule_retention": "7d",
  "audit_retention": "30d"
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.AutoCleanup {
		t.Fatal("auto cleanup = true, want false")
	}
	if cfg.MissingClientGrace != 45*time.Second || cfg.DisabledRuleRetention != 7*24*time.Hour || cfg.AuditRetention != 30*24*time.Hour {
		t.Fatalf("unexpected cleanup config: %#v", cfg)
	}
}

func TestCleanupConfigValidationRejectsInvalidDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"missing_client_grace":"0s"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "missing_client_grace must be positive") {
		t.Fatalf("expected missing_client_grace validation error, got %v", err)
	}
}

func TestDatabaseBackendResolution(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"default is sqlite", Defaults(), BackendSQLite},
		{"dsn implies postgres", Config{DatabaseBackend: BackendAuto, DatabaseDSN: "postgres://x"}, BackendPostgres},
		{"explicit postgres", Config{DatabaseBackend: BackendPostgres}, BackendPostgres},
		{"postgres alias", Config{DatabaseBackend: "PostgreSQL"}, BackendPostgres},
		{"explicit sqlite wins over dsn", Config{DatabaseBackend: BackendSQLite, DatabaseDSN: "postgres://x"}, BackendSQLite},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.ResolvedBackend(); got != tc.want {
				t.Fatalf("backend = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDatabaseBackendValidation(t *testing.T) {
	cfg := Defaults()
	cfg.DatabaseBackend = BackendPostgres
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "database_dsn") {
		t.Fatalf("expected database_dsn validation error, got %v", err)
	}
	cfg.DatabaseDSN = "postgres://xui@127.0.0.1/xui"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("postgres config should validate: %v", err)
	}
}

func TestRedactDSN(t *testing.T) {
	urlForm := RedactDSN("postgres://xui:secret@127.0.0.1:5432/xui?sslmode=disable")
	if strings.Contains(urlForm, "secret") || !strings.Contains(urlForm, "xui:REDACTED@") {
		t.Fatalf("url dsn not redacted: %s", urlForm)
	}
	queryForm := RedactDSN("postgres://xui@127.0.0.1:5432/xui?password=secret&sslmode=disable")
	if strings.Contains(queryForm, "secret") || !strings.Contains(queryForm, "password=REDACTED") {
		t.Fatalf("query dsn not redacted: %s", queryForm)
	}
	keywordForm := RedactDSN("host=127.0.0.1 user=xui password=secret dbname=xui")
	if strings.Contains(keywordForm, "secret") || !strings.Contains(keywordForm, "password=REDACTED") {
		t.Fatalf("keyword dsn not redacted: %s", keywordForm)
	}
	if got := RedactDSN(""); got != "" {
		t.Fatalf("empty dsn = %q, want empty", got)
	}
}

func TestDatabaseEnvFallback(t *testing.T) {
	t.Setenv("XUI_DB_TYPE", "postgres")
	t.Setenv("XUI_DB_DSN", "postgres://env@127.0.0.1/xui")

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load without config file: %v", err)
	}
	if cfg.ResolvedBackend() != BackendPostgres || cfg.DatabaseDSN != "postgres://env@127.0.0.1/xui" {
		t.Fatalf("env fallback not applied: %#v", cfg)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"database_backend":"sqlite","database_path":"/tmp/x-ui.db"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ResolvedBackend() != BackendSQLite {
		t.Fatalf("explicit file backend overridden by env: %s", cfg.ResolvedBackend())
	}
}

func TestPanelEnvFileFallback(t *testing.T) {
	t.Setenv("XUI_DB_TYPE", "")
	t.Setenv("XUI_DB_DSN", "")

	dir := t.TempDir()
	panelFile := filepath.Join(dir, "x-ui")
	data := "XRAY_VMESS_AEAD_FORCED=false\n" +
		"# database settings\n" +
		"XUI_DB_TYPE=postgres\n" +
		`XUI_DB_DSN="host=127.0.0.1 port=5432 user=xui password=secret dbname=x-ui sslmode=disable"` + "\n"
	if err := os.WriteFile(panelFile, []byte(data), 0o644); err != nil {
		t.Fatalf("write panel env: %v", err)
	}

	original := panelEnvFilePaths
	panelEnvFilePaths = []string{filepath.Join(dir, "does-not-exist"), panelFile}
	t.Cleanup(func() { panelEnvFilePaths = original })

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load without config file: %v", err)
	}
	if cfg.ResolvedBackend() != BackendPostgres {
		t.Fatalf("panel env file not applied: %s", cfg.ResolvedBackend())
	}
	if cfg.DatabaseDSN != "host=127.0.0.1 port=5432 user=xui password=secret dbname=x-ui sslmode=disable" {
		t.Fatalf("panel env dsn = %q", cfg.DatabaseDSN)
	}

	// A process environment value wins over the panel file.
	t.Setenv("XUI_DB_DSN", "postgres://env@127.0.0.1/xui")
	cfg, err = Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load with env override: %v", err)
	}
	if cfg.DatabaseDSN != "postgres://env@127.0.0.1/xui" {
		t.Fatalf("process env should win over panel file: %q", cfg.DatabaseDSN)
	}

	// An explicit config file still wins over both.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"database_backend":"sqlite","database_path":"/tmp/x-ui.db"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("load explicit config: %v", err)
	}
	if cfg.ResolvedBackend() != BackendSQLite {
		t.Fatalf("explicit file backend overridden by panel env: %s", cfg.ResolvedBackend())
	}
}

func TestReadPanelEnvValues(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	if err := os.WriteFile(first, []byte("XUI_DB_TYPE=postgres\r\nexport XUI_DB_DSN='postgres://a@h/x'\n"), 0o644); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := os.WriteFile(second, []byte("XUI_DB_DSN=postgres://b@h/x\nXUI_DB_TYPE=sqlite\n"), 0o644); err != nil {
		t.Fatalf("write second: %v", err)
	}

	values := readPanelEnvValues([]string{filepath.Join(dir, "missing"), first, second})
	if values["XUI_DB_TYPE"] != "postgres" {
		t.Fatalf("type = %q, want postgres from the first file", values["XUI_DB_TYPE"])
	}
	if values["XUI_DB_DSN"] != "postgres://a@h/x" {
		t.Fatalf("dsn = %q, want the first file value", values["XUI_DB_DSN"])
	}

	empty := readPanelEnvValues([]string{filepath.Join(dir, "missing")})
	if len(empty) != 0 {
		t.Fatalf("missing files returned values: %#v", empty)
	}
}

func TestDatabaseLabelRedactsDSN(t *testing.T) {
	cfg := Defaults()
	cfg.DatabaseBackend = BackendPostgres
	cfg.DatabaseDSN = "postgres://xui:secret@127.0.0.1:5432/xui"
	if label := cfg.DatabaseLabel(); strings.Contains(label, "secret") {
		t.Fatalf("label leaked the password: %s", label)
	}
}
