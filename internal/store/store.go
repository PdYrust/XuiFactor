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
	db            *sql.DB
	path          string
	dsn           string
	driver        string
	lockTimeoutMs int64
}

type Tx struct {
	conn   *sql.Conn
	driver string
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
		return nil, wrapDatabaseError("open database", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, wrapDatabaseError("open database", err)
	}

	return &Store{db: db, path: path, driver: DriverSQLite, lockTimeoutMs: busyTimeout.Milliseconds()}, nil
}

// OpenPostgres connects to a 3x-ui PostgreSQL database. Write access is
// required for commands that apply factors; read-only commands use the same
// connection because PostgreSQL does not need file-level locking.
func OpenPostgres(ctx context.Context, dsn string, busyTimeout time.Duration) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("database dsn is required")
	}
	if busyTimeout <= 0 {
		return nil, errors.New("busy timeout must be positive")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, wrapDatabaseError("open database", err)
	}
	lockTimeout := "SET lock_timeout = '" + strconv.FormatInt(busyTimeout.Milliseconds(), 10) + "ms'"
	if _, err := db.ExecContext(ctx, lockTimeout); err != nil {
		db.Close()
		return nil, wrapDatabaseError("open database", err)
	}

	return &Store{db: db, dsn: dsn, driver: DriverPostgres, lockTimeoutMs: busyTimeout.Milliseconds()}, nil
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
	for _, table := range []string{"client_traffics", "inbounds", "clients", "client_inbounds"} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			return fmt.Errorf("%w: check table %s: %v", ErrSchema, table, err)
		}
		if !exists {
			return fmt.Errorf("%w: missing table %s (3x-ui 3.x database required)", ErrSchema, table)
		}
	}

	columns, err := s.tableColumns(ctx, "client_traffics")
	if err != nil {
		return fmt.Errorf("%w: read client_traffics columns: %v", ErrSchema, err)
	}
	if columns["all_time"] {
		return fmt.Errorf("%w: client_traffics.all_time found; upgrade 3x-ui to 3.x before running xui-factor", ErrSchema)
	}
	for _, column := range []string{
		"id",
		"inbound_id",
		"enable",
		"email",
		"up",
		"down",
		"total",
	} {
		if !columns[column] {
			return fmt.Errorf("%w: missing client_traffics.%s", ErrSchema, column)
		}
	}
	return nil
}

func (s *Store) tableExists(ctx context.Context, table string) (bool, error) {
	return tableExistsWith(ctx, s.queryRowContext, s.driver, table)
}

func (tx *Tx) TableExists(ctx context.Context, table string) (bool, error) {
	return tableExistsWith(ctx, tx.QueryRowContext, tx.driver, table)
}

func tableExistsWith(ctx context.Context, queryRow queryRowFunc, driver, table string) (bool, error) {
	query := "SELECT name FROM sqlite_master WHERE type='table' AND name=?"
	if driver == DriverPostgres {
		query = `SELECT table_name FROM information_schema.tables
			WHERE table_name::text = ?
				AND table_schema = ANY (current_schemas(false))
				AND table_type = 'BASE TABLE'
			LIMIT 1`
	}
	var name string
	err := queryRow(ctx, query, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, wrapDatabaseError("read database schema", err)
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	return tableColumnsWith(ctx, s.queryContext, s.driver, table)
}

func (tx *Tx) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	return tableColumnsWith(ctx, tx.QueryContext, tx.driver, table)
}

func tableColumnsWith(ctx context.Context, query queryFunc, driver, table string) (map[string]bool, error) {
	columns := make(map[string]bool)

	if driver == DriverPostgres {
		rows, err := query(ctx, `
			SELECT column_name
			FROM information_schema.columns
			WHERE table_name::text = ? AND table_schema = ANY (current_schemas(false))
		`, table)
		if err != nil {
			return nil, wrapDatabaseError("read table columns", err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, wrapDatabaseError("read table columns", err)
			}
			columns[name] = true
		}
		if err := rows.Err(); err != nil {
			return nil, wrapDatabaseError("read table columns", err)
		}
		return columns, nil
	}

	rows, err := query(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, wrapDatabaseError("read table columns", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, wrapDatabaseError("read table columns", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDatabaseError("read table columns", err)
	}
	return columns, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	return s.WithImmediateTx(ctx, func(tx *Tx) error {
		for _, stmt := range migrationStatements(tx.driver) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("run metadata migration: %w", err)
			}
		}
		if err := tx.ensureMetadataColumns(ctx); err != nil {
			return fmt.Errorf("run metadata migration: %w", err)
		}
		if err := tx.dropLegacyMetadataColumns(ctx); err != nil {
			return fmt.Errorf("run metadata migration: %w", err)
		}
		now := time.Now().Unix()
		for _, stmt := range migrationPolicyDedupeStatements {
			if _, err := tx.ExecContext(ctx, stmt, now, now); err != nil {
				return fmt.Errorf("run metadata migration: %w", err)
			}
		}
		if err := tx.backfillMetadataIdentity(ctx); err != nil {
			return fmt.Errorf("run metadata migration: %w", err)
		}
		for _, stmt := range migrationIndexStatements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("run metadata migration: %w", err)
			}
		}
		versionInsert := `
			INSERT OR IGNORE INTO xui_factor_meta(key, value, updated_at)
			VALUES('schema_version', '3', ?)`
		if tx.driver == DriverPostgres {
			versionInsert = `
			INSERT INTO xui_factor_meta(key, value, updated_at)
			VALUES('schema_version', '3', ?)
			ON CONFLICT (key) DO NOTHING`
		}
		if _, err := tx.ExecContext(ctx, versionInsert, now); err != nil {
			return fmt.Errorf("run metadata migration: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE xui_factor_meta
			SET value = '3', updated_at = ?
			WHERE key = 'schema_version' AND value <> '3'
		`, now); err != nil {
			return fmt.Errorf("run metadata migration: %w", err)
		}
		return nil
	})
}

func (tx *Tx) ensureMetadataColumns(ctx context.Context) error {
	for table, columns := range migrationColumns {
		exists, err := tx.TableExists(ctx, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		current, err := tx.tableColumns(ctx, table)
		if err != nil {
			return err
		}
		for _, column := range columns {
			if current[column.name] {
				continue
			}
			if _, err := tx.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tx *Tx) dropLegacyMetadataColumns(ctx context.Context) error {
	for _, target := range migrationDropColumns {
		exists, err := tx.TableExists(ctx, target.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		current, err := tx.tableColumns(ctx, target.table)
		if err != nil {
			return err
		}
		for _, column := range target.columns {
			if !current[column] {
				continue
			}
			if _, err := tx.ExecContext(ctx, "ALTER TABLE "+target.table+" DROP COLUMN "+column); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tx *Tx) backfillMetadataIdentity(ctx context.Context) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rule_clients
		SET inbound_id = COALESCE(
				(SELECT ct.inbound_id FROM client_traffics ct WHERE ct.id = xui_factor_rule_clients.traffic_id LIMIT 1),
				(SELECT r.inbound_id FROM xui_factor_rules r WHERE r.id = xui_factor_rule_clients.rule_id LIMIT 1),
				inbound_id
			)
		WHERE inbound_id = 0
	`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rule_clients
		SET email = COALESCE(
				(SELECT ct.email FROM client_traffics ct WHERE ct.id = xui_factor_rule_clients.traffic_id LIMIT 1),
				(SELECT r.email FROM xui_factor_rules r WHERE r.id = xui_factor_rule_clients.rule_id LIMIT 1),
				email
			)
		WHERE email = ''
	`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE xui_factor_rule_clients
		SET updated_at = COALESCE(
				(SELECT r.updated_at FROM xui_factor_rules r WHERE r.id = xui_factor_rule_clients.rule_id LIMIT 1),
				updated_at
			)
		WHERE updated_at = 0
	`)
	return err
}

func (s *Store) WithImmediateTx(ctx context.Context, fn func(*Tx) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if s.driver == DriverPostgres {
		if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
			return wrapDatabaseError("begin write transaction", err)
		}
		if s.lockTimeoutMs > 0 {
			lockTimeout := "SET LOCAL lock_timeout = '" + strconv.FormatInt(s.lockTimeoutMs, 10) + "ms'"
			if _, err := conn.ExecContext(ctx, lockTimeout); err != nil {
				_, _ = conn.ExecContext(ctx, "ROLLBACK")
				return wrapDatabaseError("begin write transaction", err)
			}
		}
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", postgresAdvisoryLockKey); err != nil {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
			return wrapDatabaseError("lock write transaction", err)
		}
	} else {
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return wrapDatabaseError("begin write transaction", err)
		}
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if err := fn(&Tx{conn: conn, driver: s.driver}); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return wrapDatabaseError("commit transaction", err)
	}
	committed = true
	return nil
}

func (s *Store) WithReadTx(ctx context.Context, fn func(*Tx) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return wrapDatabaseError("begin read transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if err := fn(&Tx{conn: conn, driver: s.driver}); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return wrapDatabaseError("commit read transaction", err)
	}
	committed = true
	return nil
}

func wrapDatabaseError(action string, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "busy") || strings.Contains(message, "locked") ||
		strings.Contains(message, "lock timeout") || strings.Contains(message, "deadlock detected") {
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
	if tx.driver == DriverPostgres {
		query = rebindPostgres(query)
	}
	result, err := tx.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, wrapDatabaseError("execute database statement", err)
	}
	return result, nil
}

func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx.driver == DriverPostgres {
		query = rebindPostgres(query)
	}
	rows, err := tx.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapDatabaseError("query database", err)
	}
	return rows, nil
}

func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if tx.driver == DriverPostgres {
		query = rebindPostgres(query)
	}
	return tx.conn.QueryRowContext(ctx, query, args...)
}
