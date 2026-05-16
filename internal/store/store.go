package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

type Tx struct {
	conn *sql.Conn
}

func Open(ctx context.Context, path string, busyTimeout time.Duration) (*Store, error) {
	return open(ctx, path, busyTimeout, false)
}

func OpenReadOnly(ctx context.Context, path string, busyTimeout time.Duration) (*Store, error) {
	return open(ctx, path, busyTimeout, true)
}

func open(ctx context.Context, path string, busyTimeout time.Duration, readOnly bool) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if busyTimeout <= 0 {
		return nil, errors.New("busy timeout must be positive")
	}
	if shouldStatPath(path) {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
	}

	openPath := path
	if readOnly {
		openPath = readOnlyDSN(path)
	}

	db, err := sql.Open("sqlite", openPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = "+strconv.FormatInt(busyTimeout.Milliseconds(), 10)); err != nil {
		db.Close()
		return nil, wrapSQLiteError("open database", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, wrapSQLiteError("open database", err)
	}

	return &Store{db: db, path: path}, nil
}

func readOnlyDSN(path string) string {
	if path == ":memory:" {
		return path
	}
	if u, err := url.Parse(path); err == nil && u.Scheme == "file" {
		q := u.Query()
		q.Set("mode", "ro")
		u.RawQuery = q.Encode()
		return u.String()
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	return u.String()
}

func shouldStatPath(path string) bool {
	if path == ":memory:" {
		return false
	}
	u, err := url.Parse(path)
	if err == nil && u.Scheme == "file" {
		return false
	}
	return true
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) EnsureReady(ctx context.Context) error {
	if err := s.ValidateRequiredSchema(ctx); err != nil {
		return err
	}
	return s.Migrate(ctx)
}

func (s *Store) MetadataReady(ctx context.Context) (bool, error) {
	for _, table := range []string{
		"xui_factor_meta",
		"xui_factor_rules",
		"xui_factor_rule_clients",
		"xui_factor_scopes",
		"xui_factor_events",
	} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			return false, fmt.Errorf("check metadata table %s: %w", table, err)
		}
		if !exists {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) ValidateRequiredSchema(ctx context.Context) error {
	for _, table := range []string{"client_traffics", "inbounds"} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			return fmt.Errorf("%w: check table %s: %v", ErrSchema, table, err)
		}
		if !exists {
			return fmt.Errorf("%w: missing table %s", ErrSchema, table)
		}
	}

	columns, err := s.tableColumns(ctx, "client_traffics")
	if err != nil {
		return fmt.Errorf("%w: read client_traffics columns: %v", ErrSchema, err)
	}
	for _, column := range []string{
		"id",
		"inbound_id",
		"enable",
		"email",
		"up",
		"down",
		"all_time",
		"expiry_time",
		"total",
		"reset",
		"last_online",
	} {
		if !columns[column] {
			return fmt.Errorf("%w: missing client_traffics.%s", ErrSchema, column)
		}
	}
	return nil
}

func (s *Store) tableExists(ctx context.Context, table string) (bool, error) {
	var name string
	err := s.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, wrapSQLiteError("read sqlite schema", err)
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, wrapSQLiteError("read table columns", err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, wrapSQLiteError("read table columns", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, wrapSQLiteError("read table columns", err)
	}
	return columns, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	return s.WithImmediateTx(ctx, func(tx *Tx) error {
		for _, stmt := range migrationStatements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("run metadata migration: %w", err)
			}
		}
		now := time.Now().Unix()
		_, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO xui_factor_meta(key, value, updated_at)
			VALUES('schema_version', '1', ?)
		`, now)
		if err != nil {
			return fmt.Errorf("run metadata migration: %w", err)
		}
		return nil
	})
}

func (s *Store) WithImmediateTx(ctx context.Context, fn func(*Tx) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return wrapSQLiteError("begin write transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if err := fn(&Tx{conn: conn}); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return wrapSQLiteError("commit transaction", err)
	}
	committed = true
	return nil
}

func wrapSQLiteError(action string, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "busy") || strings.Contains(message, "locked") {
		return fmt.Errorf("%s: database is locked or busy after busy timeout: %w", action, err)
	}
	if strings.Contains(message, "file is not a database") ||
		strings.Contains(message, "database disk image is malformed") ||
		strings.Contains(message, "malformed database") {
		return fmt.Errorf("%s: database is not a valid SQLite database: %w", action, err)
	}
	return err
}

func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := tx.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, wrapSQLiteError("execute sqlite statement", err)
	}
	return result, nil
}

func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := tx.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapSQLiteError("query sqlite database", err)
	}
	return rows, nil
}

func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.conn.QueryRowContext(ctx, query, args...)
}
