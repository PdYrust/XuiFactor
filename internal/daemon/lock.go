package daemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

const DefaultLockPath = "/run/xui-factor.lock"

type Lock struct {
	file *os.File
	path string
}

func AcquireLock(path string) (*Lock, error) {
	if path == "" {
		path = DefaultLockPath
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open process lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("process lock already held: %s: %w", path, err)
		}
		return nil, fmt.Errorf("acquire process lock: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		file.Close()
		return nil, fmt.Errorf("write process lock: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		file.Close()
		return nil, fmt.Errorf("write process lock: %w", err)
	}
	return &Lock{file: file, path: path}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	_ = os.Remove(l.path)
	l.file = nil
	return err
}
