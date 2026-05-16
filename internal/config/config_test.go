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
