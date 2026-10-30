package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) Backup(ctx context.Context, backupDir string, now time.Time) (string, error) {
	if backupDir == "" {
		return "", fmt.Errorf("backup directory is required")
	}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	if s.driver == DriverPostgres {
		return s.backupPostgres(ctx, backupDir, now)
	}

	path, err := availableBackupPath(backupDir, now, ".db")
	if err != nil {
		return "", err
	}

	if _, err := s.db.ExecContext(ctx, "VACUUM main INTO ?", path); err != nil {
		_ = os.Remove(path)
		return "", wrapDatabaseError("create sqlite backup", err)
	}
	return path, nil
}

// Connection parameters reach pg_dump through PG* variables so the password
// never appears in the process list.
func (s *Store) backupPostgres(ctx context.Context, backupDir string, now time.Time) (string, error) {
	path, err := availableBackupPath(backupDir, now, ".dump")
	if err != nil {
		return "", err
	}

	parsed, err := pgconn.ParseConfig(s.dsn)
	if err != nil {
		return "", fmt.Errorf("create postgres backup: parse dsn: %w", err)
	}

	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--file", path)
	cmd.Env = postgresCommandEnv(os.Environ(), parsed, s.dsn)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(path)
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("create postgres backup: pg_dump not found; install the postgresql client tools")
		}
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("create postgres backup: pg_dump failed: %s", message)
	}
	return path, nil
}

// postgresCommandEnv replaces every inherited PG* variable with values from the
// DSN, so the dump cannot be redirected to another server and keeps the DSN's
// TLS verification level.
func postgresCommandEnv(base []string, parsed *pgconn.Config, dsn string) []string {
	env := make([]string, 0, len(base)+7)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(name), "PG") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"PGHOST="+parsed.Host,
		"PGPORT="+strconv.Itoa(int(parsed.Port)),
		"PGUSER="+parsed.User,
		"PGDATABASE="+parsed.Database,
	)
	if parsed.Password != "" {
		env = append(env, "PGPASSWORD="+parsed.Password)
	}
	if sslmode := dsnParam(dsn, "sslmode"); sslmode != "" {
		env = append(env, "PGSSLMODE="+sslmode)
	} else if parsed.TLSConfig == nil {
		env = append(env, "PGSSLMODE=disable")
	}
	if sslrootcert := dsnParam(dsn, "sslrootcert"); sslrootcert != "" {
		env = append(env, "PGSSLROOTCERT="+sslrootcert)
	}
	return env
}

func dsnParam(dsn, key string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		return u.Query().Get(key)
	}
	pattern := regexp.MustCompile(`(?i)(?:^|\s)` + regexp.QuoteMeta(key) + `\s*=\s*('[^']*'|\S+)`)
	match := pattern.FindStringSubmatch(dsn)
	if match == nil {
		return ""
	}
	return strings.Trim(match[1], "'")
}

func availableBackupPath(backupDir string, now time.Time, extension string) (string, error) {
	timestamp := now.UTC().Format("20060102T150405Z")
	for i := 0; i < 1000; i++ {
		name := "x-ui-" + timestamp + extension
		if i > 0 {
			name = fmt.Sprintf("x-ui-%s-%03d%s", timestamp, i, extension)
		}
		path := filepath.Join(backupDir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("check backup path: %w", err)
		}
		return path, nil
	}
	return "", fmt.Errorf("backup path: no unused filename available for timestamp %s", timestamp)
}
