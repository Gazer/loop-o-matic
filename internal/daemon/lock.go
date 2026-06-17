package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

type Lock struct {
	path string
}

func AcquireLock(root string) (*Lock, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "loopd.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("loopd already running or stale lock exists: %s", path)
		}
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Close()
	return &Lock{path: path}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	return os.Remove(l.path)
}
