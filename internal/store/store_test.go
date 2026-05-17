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
	execTestSQL(t, db, `CREATE TABLE xui_factor_meta (key TEXT PRIMARY KEY)`)
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
