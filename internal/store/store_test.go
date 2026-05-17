package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateSchemaAndMigrate(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createTestSchema(t, dbPath)

	st, err := Open(ctx, dbPath, time.Second)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.ValidateRequiredSchema(ctx); err != nil {
		t.Fatalf("validate schema: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, table := range []string{"xui_factor_rules", "xui_factor_scopes", "xui_factor_excludes", "xui_factor_overrides"} {
		var name string
		if err := st.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("metadata table %s missing: %v", table, err)
		}
	}
}

func TestMigrateUpgradesOldSingleUserMetadataIdempotently(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createTestSchema(t, dbPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	execTestSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
		VALUES(1, 7, 1, 'old@example.com', 10, 20, 30, 0, 0, 0, 0)
	`)
	execTestSQL(t, db, `
		CREATE TABLE xui_factor_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			inbound_id INTEGER,
			email TEXT NOT NULL,
			factor_ppm INTEGER NOT NULL,
			state TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	execTestSQL(t, db, `
		CREATE TABLE xui_factor_rule_clients (
			rule_id INTEGER NOT NULL,
			traffic_id INTEGER NOT NULL,
			last_up INTEGER NOT NULL,
			last_down INTEGER NOT NULL,
			last_all_time INTEGER NOT NULL,
			PRIMARY KEY(rule_id, traffic_id)
		)
	`)
	execTestSQL(t, db, `
		INSERT INTO xui_factor_rules(id, inbound_id, email, factor_ppm, state, created_at, updated_at)
		VALUES(1, 7, 'old@example.com', 2000000, 'active', 100, 200)
	`)
	execTestSQL(t, db, `
		INSERT INTO xui_factor_rule_clients(rule_id, traffic_id, last_up, last_down, last_all_time)
		VALUES(1, 1, 10, 20, 30)
	`)
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	st, err := Open(ctx, dbPath, time.Second)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	for _, column := range []string{"inbound_id", "email", "rem_up", "rem_down", "rem_all_time", "missing_since", "updated_at"} {
		if !testColumnExists(t, st.db, "xui_factor_rule_clients", column) {
			t.Fatalf("xui_factor_rule_clients.%s missing after migration", column)
		}
	}
	var inboundID, up, down, allTime, ruleClients, metaRows int64
	var email string
	err = st.db.QueryRowContext(ctx, `
		SELECT inbound_id, email
		FROM xui_factor_rule_clients
		WHERE rule_id = 1 AND traffic_id = 1
	`).Scan(&inboundID, &email)
	if err != nil {
		t.Fatalf("read rule client identity: %v", err)
	}
	if inboundID != 7 || email != "old@example.com" {
		t.Fatalf("identity = inbound=%d email=%q, want inbound=7 email=old@example.com", inboundID, email)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT up, down, all_time FROM client_traffics WHERE id=1`).Scan(&up, &down, &allTime); err != nil {
		t.Fatalf("read counters: %v", err)
	}
	if up != 10 || down != 20 || allTime != 30 {
		t.Fatalf("migration changed counters: up=%d down=%d all_time=%d", up, down, allTime)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM xui_factor_rule_clients`).Scan(&ruleClients); err != nil {
		t.Fatalf("count rule clients: %v", err)
	}
	if ruleClients != 1 {
		t.Fatalf("rule clients = %d, want 1", ruleClients)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM xui_factor_meta WHERE key='schema_version'`).Scan(&metaRows); err != nil {
		t.Fatalf("count schema version rows: %v", err)
	}
	if metaRows != 1 {
		t.Fatalf("schema version rows = %d, want 1", metaRows)
	}
}

func TestMigrateUpgradesPolicyScopeAndAuditMetadata(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createTestSchema(t, dbPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	execTestSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
		VALUES(1, 1, 1, 'policy@example.com', 100, 200, 300, 0, 0, 0, 0)
	`)
	execTestSQL(t, db, `
		CREATE TABLE xui_factor_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			inbound_id INTEGER,
			email TEXT NOT NULL,
			factor_ppm INTEGER NOT NULL,
			state TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			activated_at INTEGER,
			paused_at INTEGER,
			disabled_at INTEGER
		)
	`)
	execTestSQL(t, db, `
		CREATE TABLE xui_factor_rule_clients (
			rule_id INTEGER NOT NULL,
			traffic_id INTEGER NOT NULL,
			inbound_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			last_up INTEGER NOT NULL,
			last_down INTEGER NOT NULL,
			last_all_time INTEGER NOT NULL,
			rem_up INTEGER NOT NULL DEFAULT 0,
			rem_down INTEGER NOT NULL DEFAULT 0,
			rem_all_time INTEGER NOT NULL DEFAULT 0,
			missing_since INTEGER,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(rule_id, traffic_id)
		)
	`)
	execTestSQL(t, db, `
		CREATE TABLE xui_factor_scopes (
			rule_id INTEGER PRIMARY KEY,
			inbound_id INTEGER,
			limited_only INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	execTestSQL(t, db, `
		CREATE TABLE xui_factor_excludes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			traffic_id INTEGER NOT NULL,
			inbound_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			state TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	execTestSQL(t, db, `
		CREATE TABLE xui_factor_overrides (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			traffic_id INTEGER NOT NULL,
			inbound_id INTEGER NOT NULL,
			email TEXT NOT NULL,
			factor_ppm INTEGER NOT NULL,
			state TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`)
	execTestSQL(t, db, `
		CREATE TABLE xui_factor_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			rule_id INTEGER,
			event_type TEXT NOT NULL,
			created_at INTEGER NOT NULL
		)
	`)
	execTestSQL(t, db, `
		INSERT INTO xui_factor_rules(id, name, inbound_id, email, factor_ppm, state, created_at, updated_at)
		VALUES(1, '', 1, '', 2000000, 'active', 100, 100)
	`)
	execTestSQL(t, db, `
		INSERT INTO xui_factor_rule_clients(rule_id, traffic_id, inbound_id, email, last_up, last_down, last_all_time, updated_at)
		VALUES(1, 1, 1, 'policy@example.com', 100, 200, 300, 100)
	`)
	execTestSQL(t, db, `INSERT INTO xui_factor_scopes(rule_id, inbound_id, limited_only, created_at, updated_at) VALUES(1, 1, 0, 100, 100)`)
	execTestSQL(t, db, `INSERT INTO xui_factor_excludes(id, traffic_id, inbound_id, email, state, created_at, updated_at) VALUES(1, 1, 1, 'policy@example.com', 'active', 100, 100)`)
	execTestSQL(t, db, `INSERT INTO xui_factor_overrides(id, traffic_id, inbound_id, email, factor_ppm, state, created_at, updated_at) VALUES(1, 1, 1, 'policy@example.com', 1200000, 'active', 100, 100)`)
	execTestSQL(t, db, `INSERT INTO xui_factor_events(id, rule_id, event_type, created_at) VALUES(1, 1, 'traffic_applied', 100)`)
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	st, err := Open(ctx, dbPath, time.Second)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	for table, columns := range map[string][]string{
		"xui_factor_scopes":    {"include_disabled_clients", "once"},
		"xui_factor_excludes":  {"note", "activated_at", "disabled_at"},
		"xui_factor_overrides": {"note", "activated_at", "disabled_at"},
		"xui_factor_events":    {"message"},
	} {
		for _, column := range columns {
			if !testColumnExists(t, st.db, table, column) {
				t.Fatalf("%s.%s missing after migration", table, column)
			}
		}
	}
	if _, err := st.ListExcludes(ctx, false, nil); err != nil {
		t.Fatalf("list excludes after migration: %v", err)
	}
	if _, err := st.ListOverrides(ctx, false, nil); err != nil {
		t.Fatalf("list overrides after migration: %v", err)
	}
	events, err := st.ListEvents(ctx, EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list events after migration: %v", err)
	}
	if len(events) != 1 || events[0].Message != "" {
		t.Fatalf("events after migration = %#v, want one empty-message event", events)
	}
}

func TestValidateSchemaRejectsMissingColumn(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	execTestSQL(t, db, `CREATE TABLE inbounds (id INTEGER PRIMARY KEY)`)
	execTestSQL(t, db, `
		CREATE TABLE client_traffics (
			id INTEGER,
			inbound_id INTEGER,
			enable numeric,
			email TEXT,
			up INTEGER,
			down INTEGER,
			expiry_time INTEGER,
			total INTEGER,
			reset INTEGER,
			last_online INTEGER
		)
	`)
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	st, err := Open(ctx, dbPath, time.Second)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := st.ValidateRequiredSchema(ctx); !errors.Is(err, ErrSchema) {
		t.Fatalf("expected ErrSchema, got %v", err)
	}
}

func TestOpenOrValidateRejectsCorruptDatabase(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatalf("write corrupt db: %v", err)
	}

	st, err := Open(ctx, dbPath, time.Second)
	if err == nil {
		err = st.ValidateRequiredSchema(ctx)
		_ = st.Close()
	}
	if err == nil {
		t.Fatal("expected corrupt database error")
	}
	if !strings.Contains(err.Error(), "valid SQLite database") {
		t.Fatalf("expected valid SQLite error, got %v", err)
	}
}

func TestLockedDatabaseReportsBusyTimeout(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createTestSchema(t, dbPath)

	lockDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open locking sqlite: %v", err)
	}
	defer lockDB.Close()
	if _, err := lockDB.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin locking transaction: %v", err)
	}
	defer lockDB.Exec(`ROLLBACK`)

	st, err := Open(ctx, dbPath, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	err = st.Migrate(ctx)
	if err == nil {
		t.Fatal("expected busy timeout error")
	}
	if !strings.Contains(err.Error(), "locked or busy after busy timeout") {
		t.Fatalf("expected busy timeout message, got %v", err)
	}
}

func TestMigrateFailureIsOperatorReadable(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	createTestSchema(t, dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	execTestSQL(t, db, `CREATE TABLE idx_xui_factor_rules_state (id INTEGER PRIMARY KEY)`)
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	st, err := Open(ctx, dbPath, time.Second)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	err = st.Migrate(ctx)
	if err == nil {
		t.Fatal("expected migration failure")
	}
	if !strings.Contains(err.Error(), "metadata migration") {
		t.Fatalf("expected metadata migration error, got %v", err)
	}
}

func TestBackupDoesNotOverwriteExistingPathAndIsUsable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x-ui.db")
	backupDir := filepath.Join(dir, "backups")
	createTestSchema(t, dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	execTestSQL(t, db, `
		INSERT INTO client_traffics(id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online)
		VALUES(1, 1, 1, 'backup@example.com', 10, 20, 30, 0, 0, 0, 0)
	`)
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	st, err := Open(ctx, dbPath, time.Second)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		t.Fatalf("create backup dir: %v", err)
	}
	existing := filepath.Join(backupDir, "x-ui-20260516T100000Z.db")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write existing backup: %v", err)
	}

	backupPath, err := st.Backup(ctx, backupDir, now)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if backupPath == existing {
		t.Fatalf("backup overwrote existing path %s", existing)
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read existing backup: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("existing backup changed: %q", string(data))
	}

	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup sqlite: %v", err)
	}
	defer backupDB.Close()
	var email string
	if err := backupDB.QueryRow(`SELECT email FROM client_traffics WHERE id=1`).Scan(&email); err != nil {
		t.Fatalf("query backup: %v", err)
	}
	if email != "backup@example.com" {
		t.Fatalf("backup email = %q", email)
	}
}

func createTestSchema(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	execTestSQL(t, db, `CREATE TABLE inbounds (id INTEGER PRIMARY KEY)`)
	execTestSQL(t, db, `
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

func execTestSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec SQL: %v", err)
	}
}

func testColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("read table info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table info: %v", err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read table info rows: %v", err)
	}
	return false
}
