package cli

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLITickSmoke(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "smoke@example.com", 100, 200, 300)

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		fullArgs := append([]string{"--database", dbPath}, args...)
		if code := app.Run(fullArgs); code != 0 {
			t.Fatalf("%v exited %d, stderr=%q stdout=%q", args, code, errOut.String(), out.String())
		}
		return out.String()
	}

	run("enable", "--email", "smoke@example.com", "--factor", "5")
	setCLITestCounters(t, dbPath, 1, 110, 220, 330)

	if output := run("tick"); !strings.Contains(output, "applied: 1") {
		t.Fatalf("expected tick apply output, got %q", output)
	}
	if output := run("status"); !strings.Contains(output, "state=active") {
		t.Fatalf("expected active status, got %q", output)
	}
	if output := run("audit"); !strings.Contains(output, "traffic_applied") {
		t.Fatalf("expected audit output, got %q", output)
	}
	if output := run("audit", "--email", "smoke@example.com", "--inbound-id", "1"); !strings.Contains(output, "traffic_applied") {
		t.Fatalf("expected filtered audit output, got %q", output)
	}
	run("disable", "--email", "smoke@example.com")
	if output := run("status", "--all"); !strings.Contains(output, "state=disabled") {
		t.Fatalf("expected disabled status, got %q", output)
	}
}

func TestCLIBulkLifecycleSmoke(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "a@example.com", 100, 200, 300)
	insertCLITestTraffic(t, dbPath, 2, 1, "b@example.com", 400, 500, 900)

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		fullArgs := append([]string{"--database", dbPath}, args...)
		if code := app.Run(fullArgs); code != 0 {
			t.Fatalf("%v exited %d, stderr=%q stdout=%q", args, code, errOut.String(), out.String())
		}
		return out.String()
	}

	if output := run("enable-all", "--factor", "2"); !strings.Contains(output, "mode: persistent") || !strings.Contains(output, "enrolled: 2") {
		t.Fatalf("expected enable-all summary, got %q", output)
	}
	if output := run("status"); !strings.Contains(output, "state=active") || !strings.Contains(output, "scope=global") {
		t.Fatalf("expected active scope status, got %q", output)
	}
	if output := run("pause-all"); !strings.Contains(output, "changed: 1") {
		t.Fatalf("expected pause-all summary, got %q", output)
	}
	if output := run("resume-all"); !strings.Contains(output, "changed: 1") {
		t.Fatalf("expected resume-all summary, got %q", output)
	}
	if output := run("disable-all"); !strings.Contains(output, "changed: 1") {
		t.Fatalf("expected disable-all summary, got %q", output)
	}
}

func TestCLIPersistentScopeSmoke(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "a@example.com", 100, 200, 300)
	insertCLITestTraffic(t, dbPath, 2, 2, "b@example.com", 400, 500, 900)

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		fullArgs := append([]string{"--database", dbPath}, args...)
		if code := app.Run(fullArgs); code != 0 {
			t.Fatalf("%v exited %d, stderr=%q stdout=%q", args, code, errOut.String(), out.String())
		}
		return out.String()
	}

	if output := run("enable-all", "--inbound-id", "1", "--factor", "2"); !strings.Contains(output, "mode: persistent") || !strings.Contains(output, "enrolled: 1") {
		t.Fatalf("expected inbound persistent summary, got %q", output)
	}
	if output := run("status"); !strings.Contains(output, "scope=inbound:1") || !strings.Contains(output, "clients=1") {
		t.Fatalf("expected inbound scope status, got %q", output)
	}
	insertCLITestTraffic(t, dbPath, 3, 1, "c@example.com", 1000, 2000, 3000)
	if output := run("tick"); !strings.Contains(output, "enrolled: 1") {
		t.Fatalf("expected tick enrollment, got %q", output)
	}
	if output := run("audit"); !strings.Contains(output, "scope_auto_enroll") {
		t.Fatalf("expected scope enrollment audit, got %q", output)
	}
	if output := run("disable-all"); !strings.Contains(output, "changed: 1") {
		t.Fatalf("expected disable-all summary, got %q", output)
	}
}

func TestCLIEnableAllOnceSmoke(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "a@example.com", 100, 200, 300)

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		fullArgs := append([]string{"--database", dbPath}, args...)
		if code := app.Run(fullArgs); code != 0 {
			t.Fatalf("%v exited %d, stderr=%q stdout=%q", args, code, errOut.String(), out.String())
		}
		return out.String()
	}

	if output := run("enable-all", "--inbound-id", "1", "--factor", "2", "--once"); !strings.Contains(output, "mode: snapshot") || !strings.Contains(output, "enrolled: 1") {
		t.Fatalf("expected snapshot summary, got %q", output)
	}
	insertCLITestTraffic(t, dbPath, 2, 1, "future@example.com", 1000, 2000, 3000)
	if output := run("tick"); !strings.Contains(output, "enrolled: 0") {
		t.Fatalf("expected no snapshot enrollment, got %q", output)
	}
}

func TestCLIEnableErrorIncludesContextAndHint(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createCLITestSchema(t, dbPath)

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{
		"--database", dbPath,
		"enable",
		"--email", "missing@example.com",
		"--inbound-id", "1",
		"--factor", "2",
	})
	if code == 0 {
		t.Fatalf("enable succeeded for missing client, stdout=%q", out.String())
	}
	output := errOut.String()
	for _, want := range []string{
		"error: client not found",
		"Target",
		"email: missing@example.com",
		"inbound: 1",
		"Hint",
		"run: xui-factor status",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("enable error output missing %q: %q", want, output)
		}
	}
}

func TestCLICleanupSmoke(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "a@example.com", 100, 200, 300)

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		fullArgs := append([]string{"--database", dbPath}, args...)
		if code := app.Run(fullArgs); code != 0 {
			t.Fatalf("%v exited %d, stderr=%q stdout=%q", args, code, errOut.String(), out.String())
		}
		return out.String()
	}

	if output := run("enable-all", "--inbound-id", "1", "--factor", "2"); !strings.Contains(output, "mode: persistent") {
		t.Fatalf("expected enable-all summary, got %q", output)
	}
	if output := run("tick"); !strings.Contains(output, "active clients:") {
		t.Fatalf("expected tick summary, got %q", output)
	}
	if output := run("status"); !strings.Contains(output, "scope=inbound:1") {
		t.Fatalf("expected status scope output, got %q", output)
	}
	if output := run("audit"); !strings.Contains(output, "rule_enabled") {
		t.Fatalf("expected audit output, got %q", output)
	}
	if output := run("cleanup", "--dry-run"); !strings.Contains(output, "vacuum run: no") {
		t.Fatalf("expected dry-run cleanup summary, got %q", output)
	}
	if output := run("cleanup"); !strings.Contains(output, "cleanup:") {
		t.Fatalf("expected cleanup summary, got %q", output)
	}
	if output := run("cleanup", "--older-than", "1h"); !strings.Contains(output, "cleanup:") {
		t.Fatalf("expected older-than cleanup summary, got %q", output)
	}
	if output := run("cleanup", "--vacuum"); !strings.Contains(output, "vacuum run: yes") {
		t.Fatalf("expected vacuum cleanup summary, got %q", output)
	}
	if output := run("disable-all"); !strings.Contains(output, "changed: 1") {
		t.Fatalf("expected disable-all summary, got %q", output)
	}
}

func TestCLIReconcileSmoke(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "legacy@example.com", 100, 200, 300)

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	run := func(args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		fullArgs := append([]string{"--database", dbPath}, args...)
		if code := app.Run(fullArgs); code != 0 {
			t.Fatalf("%v exited %d, stderr=%q stdout=%q", args, code, errOut.String(), out.String())
		}
		return out.String()
	}

	run("enable", "--email", "legacy@example.com", "--factor", "2")
	db := openCLITestDB(t, dbPath)
	execCLITestSQL(t, db, `DELETE FROM xui_factor_rule_clients`)
	db.Close()

	if output := run("reconcile", "--dry-run"); !strings.Contains(output, "orphaned: 1") {
		t.Fatalf("expected dry-run reconcile summary, got %q", output)
	}
	if output := run("reconcile"); !strings.Contains(output, "reconciled: 1") {
		t.Fatalf("expected reconcile summary, got %q", output)
	}
	if output := run("status"); !strings.Contains(output, "active rules: 0") {
		t.Fatalf("expected clean normal status, got %q", output)
	}
	if output := run("status", "--all"); !strings.Contains(output, "state=orphaned") {
		t.Fatalf("expected orphaned rule in status --all, got %q", output)
	}
	if output := run("cleanup", "--dry-run"); !strings.Contains(output, "cleanup:") {
		t.Fatalf("expected cleanup dry-run summary, got %q", output)
	}
	if output := run("tick"); !strings.Contains(output, "active clients: 0") {
		t.Fatalf("expected clean tick summary, got %q", output)
	}
}

func TestCLIConfigFlagDoctorAndBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	backupDir := filepath.Join(dir, "backups")
	configPath := filepath.Join(dir, "config.json")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "doctor@example.com", 100, 200, 300)
	writeCLITestConfig(t, configPath, dbPath, backupDir)
	t.Setenv("XUI_FACTOR_CONFIG", filepath.Join(dir, "ignored-config.json"))

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)

	if code := app.Run([]string{"--config", configPath, "doctor"}); code != 0 {
		t.Fatalf("doctor exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	doctorOutput := out.String()
	for _, want := range []string{
		"XuiFactor ",
		"config: " + configPath,
		"database: " + dbPath,
		"database read: ok",
		"database write: ok",
		"schema: ok",
		"metadata: warning",
		"backup dir: " + backupDir,
		"installed:",
		"enabled:",
		"active:",
		"warning: metadata unavailable: metadata tables are missing",
		"warning: rules unavailable: metadata tables are missing",
		"doctor: OK",
	} {
		if !strings.Contains(doctorOutput, want) {
			t.Fatalf("doctor output missing %q: %q", want, doctorOutput)
		}
	}
	if cliMetadataTableExists(t, dbPath, "xui_factor_rules") {
		t.Fatalf("doctor created metadata tables")
	}

	out.Reset()
	errOut.Reset()
	if code := app.Run([]string{"--config", configPath, "backup"}); code != 0 {
		t.Fatalf("backup exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	backupOutput := out.String()
	if !strings.Contains(backupOutput, "backup: created") || !strings.Contains(backupOutput, "path: "+backupDir) {
		t.Fatalf("unexpected backup output: %q", backupOutput)
	}
	if !strings.Contains(backupOutput, "mode: manual only") {
		t.Fatalf("backup output missing restore guidance: %q", backupOutput)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("backup entries = %d, want 1", len(entries))
	}
}

func TestCLIDoctorReportsServiceState(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	createCLITestSchema(t, dbPath)
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))
	withMockDoctorService(t, "enabled", "active", true)

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code != 0 {
		t.Fatalf("doctor exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	output := out.String()
	for _, want := range []string{
		"installed: yes",
		"enabled: yes",
		"active: yes",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q: %q", want, output)
		}
	}
}

func TestCLIDoctorWarnsWhenActiveRulesExistButServiceInactive(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "warn@example.com", 100, 200, 300)
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	if code := app.Run([]string{"--config", configPath, "enable", "--email", "warn@example.com", "--factor", "2"}); code != 0 {
		t.Fatalf("enable exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	withMockDoctorService(t, "disabled", "inactive", true)

	out.Reset()
	errOut.Reset()
	if code := app.Run([]string{"--config", configPath, "doctor"}); code != 0 {
		t.Fatalf("doctor exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	output := out.String()
	if !strings.Contains(output, "enabled: no") || !strings.Contains(output, "active: no") {
		t.Fatalf("doctor did not report inactive service: %q", output)
	}
	if !strings.Contains(output, "warning: active rules exist but xui-factor.service is not running") {
		t.Fatalf("doctor missing active rule warning: %q", output)
	}
}

func TestCLIDoctorWarnsWhenPersistentScopesNeedService(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "scope@example.com", 100, 200, 300)
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	if code := app.Run([]string{"--config", configPath, "enable-all", "--inbound-id", "1", "--factor", "2"}); code != 0 {
		t.Fatalf("enable-all exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	withMockDoctorService(t, "enabled", "inactive", true)

	out.Reset()
	errOut.Reset()
	if code := app.Run([]string{"--config", configPath, "doctor"}); code != 0 {
		t.Fatalf("doctor exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	output := out.String()
	if !strings.Contains(output, "warning: persistent scopes exist but future client auto-enrollment requires xui-factor.service") {
		t.Fatalf("doctor missing persistent scope warning: %q", output)
	}
}

func TestCLIInvalidConfigJSON(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"database_path":`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code == 0 {
		t.Fatalf("doctor succeeded with invalid config, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "parse config") {
		t.Fatalf("expected parse config error, got %q", errOut.String())
	}
}

func TestCLIConfigPathDirectoryFails(t *testing.T) {
	configPath := t.TempDir()

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code == 0 {
		t.Fatalf("doctor succeeded with directory config path, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "read config") {
		t.Fatalf("expected read config error, got %q", errOut.String())
	}
}

func TestCLIInvalidConfigValue(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"poll_interval":"0s"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code == 0 {
		t.Fatalf("doctor succeeded with invalid config value, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "poll_interval must be positive") {
		t.Fatalf("expected poll_interval validation error, got %q", errOut.String())
	}
}

func TestCLIRunInvalidConfigFailsAtStartup(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"busy_timeout":"0s"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "run"})
	if code == 0 {
		t.Fatalf("run succeeded with invalid config, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "busy_timeout must be positive") {
		t.Fatalf("expected startup config error, got %q", errOut.String())
	}
}

func TestCLIDoctorMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	writeCLITestConfig(t, configPath, filepath.Join(dir, "missing.db"), filepath.Join(dir, "backups"))

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code == 0 {
		t.Fatalf("doctor succeeded with missing database, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "database file") {
		t.Fatalf("expected database file error, got %q", errOut.String())
	}
}

func TestCLIDoctorCorruptDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(dbPath, []byte("not sqlite"), 0o644); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code == 0 {
		t.Fatalf("doctor succeeded with corrupt database, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "valid SQLite database") {
		t.Fatalf("expected valid SQLite error, got %q", errOut.String())
	}
}

func TestCLIDoctorMissingSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	db := openCLITestDB(t, dbPath)
	execCLITestSQL(t, db, `PRAGMA user_version = 1`)
	db.Close()
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code == 0 {
		t.Fatalf("doctor succeeded with missing schema, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "missing table client_traffics") {
		t.Fatalf("expected missing table error, got %q", errOut.String())
	}
}

func TestCLIDoctorPartialMetadataIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	createCLITestSchema(t, dbPath)
	db := openCLITestDB(t, dbPath)
	execCLITestSQL(t, db, `CREATE TABLE xui_factor_meta (key TEXT PRIMARY KEY)`)
	db.Close()
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code != 0 {
		t.Fatalf("doctor exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "metadata: warning") || !strings.Contains(out.String(), "warning: metadata unavailable") {
		t.Fatalf("expected metadata warning, got %q", out.String())
	}
}

func TestCLIDoctorMissingRequiredColumn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	db := openCLITestDB(t, dbPath)
	defer db.Close()
	execCLITestSQL(t, db, `CREATE TABLE inbounds (id INTEGER PRIMARY KEY)`)
	execCLITestSQL(t, db, `
		CREATE TABLE client_traffics (
			id INTEGER,
			inbound_id INTEGER,
			enable numeric,
			email TEXT,
			up INTEGER,
			down INTEGER,
			all_time INTEGER,
			expiry_time INTEGER,
			total INTEGER,
			reset INTEGER
		)
	`)
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "doctor"})
	if code == 0 {
		t.Fatalf("doctor succeeded with missing column, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "missing client_traffics.last_online") {
		t.Fatalf("expected missing column error, got %q", errOut.String())
	}
}

func TestCLIBackupDirectoryFailure(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	backupPath := filepath.Join(dir, "backup-file")
	configPath := filepath.Join(dir, "config.json")
	createCLITestSchema(t, dbPath)
	if err := os.WriteFile(backupPath, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write backup path: %v", err)
	}
	writeCLITestConfig(t, configPath, dbPath, backupPath)

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "backup"})
	if code == 0 {
		t.Fatalf("backup succeeded with file backup path, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "create backup directory") {
		t.Fatalf("expected backup directory error, got %q", errOut.String())
	}
}

func TestCLIStatusAndAuditUseReadOnlyDatabase(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permission check is not meaningful as root")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "readonly@example.com", 100, 200, 300)
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))

	var out, errOut bytes.Buffer
	app := New(&out, &errOut)
	if code := app.Run([]string{"--config", configPath, "enable", "--email", "readonly@example.com", "--factor", "1.2"}); code != 0 {
		t.Fatalf("enable exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	if err := os.Chmod(dbPath, 0o444); err != nil {
		t.Fatalf("chmod db read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dbPath, 0o644)
	})

	out.Reset()
	errOut.Reset()
	if code := app.Run([]string{"--config", configPath, "status"}); code != 0 {
		t.Fatalf("status exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "state=active") {
		t.Fatalf("expected status output, got %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := app.Run([]string{"--config", configPath, "audit"}); code != 0 {
		t.Fatalf("audit exited %d, stderr=%q stdout=%q", code, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "rule_enabled") {
		t.Fatalf("expected audit output, got %q", out.String())
	}
}

func TestCLIWriteCommandRequiresWritableDatabase(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permission check is not meaningful as root")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	configPath := filepath.Join(dir, "config.json")
	createCLITestSchema(t, dbPath)
	insertCLITestTraffic(t, dbPath, 1, 1, "write@example.com", 100, 200, 300)
	writeCLITestConfig(t, configPath, dbPath, filepath.Join(dir, "backups"))
	if err := os.Chmod(dbPath, 0o444); err != nil {
		t.Fatalf("chmod db read-only: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dbPath, 0o644)
	})

	var out, errOut bytes.Buffer
	code := New(&out, &errOut).Run([]string{"--config", configPath, "enable", "--email", "write@example.com", "--factor", "1.2"})
	if code == 0 {
		t.Fatalf("enable succeeded on read-only database, stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "requires write access") {
		t.Fatalf("expected write access error, got %q", errOut.String())
	}
}

func TestSystemdServiceUsesInstalledConfigPath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "systemd", "xui-factor.service"))
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"After=network.target x-ui.service",
		"User=root",
		"ExecStart=/usr/local/bin/xui-factor --config /etc/xui-factor/config.json run",
		"Restart=on-failure",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("service file missing %q: %s", want, text)
		}
	}
}

func TestUninstallPurgeDoesNotTargetXUIDatabase(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "uninstall.sh"))
	if err != nil {
		t.Fatalf("read uninstall script: %v", err)
	}
	text := string(data)
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "rm ") && strings.Contains(line, "/etc/x-ui") {
			t.Fatalf("uninstall purge removes 3x-ui path: %s", line)
		}
	}
	if !strings.Contains(text, "did not modify /etc/x-ui/x-ui.db") {
		t.Fatalf("uninstall script should state 3x-ui database is preserved")
	}

	common, err := os.ReadFile(filepath.Join("..", "..", "scripts", "installer-common.sh"))
	if err != nil {
		t.Fatalf("read installer common script: %v", err)
	}
	commonText := string(common)
	for _, want := range []string{
		"assert_safe_purge_paths",
		"refusing purge path inside 3x-ui data directory",
	} {
		if !strings.Contains(commonText, want) {
			t.Fatalf("installer common missing %q", want)
		}
	}
}

func createCLITestSchema(t *testing.T, dbPath string) {
	t.Helper()
	db := openCLITestDB(t, dbPath)
	defer db.Close()
	execCLITestSQL(t, db, `CREATE TABLE inbounds (id INTEGER PRIMARY KEY)`)
	execCLITestSQL(t, db, `
		CREATE TABLE client_traffics (
			id INTEGER,
			inbound_id INTEGER,
			enable numeric,
			email TEXT,
			up INTEGER,
			down INTEGER,
			all_time INTEGER,
			expiry_time INTEGER,
			total INTEGER,
			reset INTEGER,
			last_online INTEGER
		)
	`)
}

func insertCLITestTraffic(t *testing.T, dbPath string, id, inboundID int64, email string, up, down, allTime int64) {
	t.Helper()
	db := openCLITestDB(t, dbPath)
	defer db.Close()
	execCLITestSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
		VALUES(?, ?, 1, ?, ?, ?, ?, 0, 0, 0, 0)
	`, id, inboundID, email, up, down, allTime)
}

func setCLITestCounters(t *testing.T, dbPath string, id, up, down, allTime int64) {
	t.Helper()
	db := openCLITestDB(t, dbPath)
	defer db.Close()
	execCLITestSQL(t, db, `UPDATE client_traffics SET up=?, down=?, all_time=? WHERE id=?`, up, down, allTime, id)
}

func cliMetadataTableExists(t *testing.T, dbPath, table string) bool {
	t.Helper()
	db := openCLITestDB(t, dbPath)
	defer db.Close()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("check metadata table: %v", err)
	}
	return true
}

func withMockDoctorService(t *testing.T, enabledState, activeState string, installed bool) {
	t.Helper()
	oldSystemctl := systemctlCommand
	oldRuntime := systemdRuntimeDir
	oldUnit := systemdUnitFile
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "run-systemd")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("create runtime dir: %v", err)
	}
	unitFile := filepath.Join(dir, "xui-factor.service")
	if installed {
		if err := os.WriteFile(unitFile, []byte("[Service]\n"), 0o644); err != nil {
			t.Fatalf("write unit file: %v", err)
		}
	}
	mock := filepath.Join(dir, "systemctl")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  is-enabled) printf '%s\\n' \"" + enabledState + "\"; [ \"" + enabledState + "\" = enabled ]; exit $? ;;\n" +
		"  is-active) printf '%s\\n' \"" + activeState + "\"; [ \"" + activeState + "\" = active ]; exit $? ;;\n" +
		"  *) exit 0 ;;\n" +
		"esac\n"
	if err := os.WriteFile(mock, []byte(script), 0o755); err != nil {
		t.Fatalf("write mock systemctl: %v", err)
	}
	systemctlCommand = mock
	systemdRuntimeDir = runtimeDir
	systemdUnitFile = unitFile
	t.Cleanup(func() {
		systemctlCommand = oldSystemctl
		systemdRuntimeDir = oldRuntime
		systemdUnitFile = oldUnit
	})
}

func openCLITestDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func execCLITestSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec SQL: %v", err)
	}
}

func writeCLITestConfig(t *testing.T, configPath, dbPath, backupDir string) {
	t.Helper()
	data := `{
  "database_path": "` + dbPath + `",
  "poll_interval": "5s",
  "busy_timeout": "5s",
  "backup_dir": "` + backupDir + `",
  "enable_backups": true,
  "log_level": "info"
}`
	if err := os.WriteFile(configPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
