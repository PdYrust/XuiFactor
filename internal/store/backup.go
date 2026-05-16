package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (s *Store) Backup(ctx context.Context, backupDir string, now time.Time) (string, error) {
	if backupDir == "" {
		return "", fmt.Errorf("backup directory is required")
	}
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	path, err := availableBackupPath(backupDir, now)
	if err != nil {
		return "", err
	}

	if _, err := s.db.ExecContext(ctx, "VACUUM main INTO ?", path); err != nil {
		_ = os.Remove(path)
		return "", wrapSQLiteError("create sqlite backup", err)
	}
	return path, nil
}

func availableBackupPath(backupDir string, now time.Time) (string, error) {
	timestamp := now.UTC().Format("20060102T150405Z")
	for i := 0; i < 1000; i++ {
		name := "x-ui-" + timestamp + ".db"
		if i > 0 {
			name = fmt.Sprintf("x-ui-%s-%03d.db", timestamp, i)
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
