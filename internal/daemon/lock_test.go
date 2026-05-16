package daemon

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireLockReportsAlreadyHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xui-factor.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer first.Close()

	second, err := AcquireLock(path)
	if err == nil {
		second.Close()
		t.Fatal("expected already-held lock error")
	}
	if !strings.Contains(err.Error(), "process lock already held") {
		t.Fatalf("expected already-held message, got %v", err)
	}
}
