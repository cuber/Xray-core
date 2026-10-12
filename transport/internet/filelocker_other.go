//go:build !windows
// +build !windows

package internet

import (
	"context"
	"os"

	"github.com/xtls/xray-core/common/errors"
	"golang.org/x/sys/unix"
)

// Acquire lock
func (fl *FileLocker) Acquire() error {
	return fl.acquire(unix.LOCK_EX)
}

// TryAcquire refuses an occupied lock instead of waiting for its owner.
func (fl *FileLocker) TryAcquire() error {
	return fl.acquire(unix.LOCK_EX | unix.LOCK_NB)
}

func (fl *FileLocker) acquire(operation int) error {
	f, err := os.Create(fl.path)
	if err != nil {
		return err
	}
	if err := unix.Flock(int(f.Fd()), operation); err != nil {
		f.Close()
		return errors.New("failed to lock file: ", fl.path).Base(err)
	}
	fl.file = f
	return nil
}

// Release lock
func (fl *FileLocker) Release() {
	if err := unix.Flock(int(fl.file.Fd()), unix.LOCK_UN); err != nil {
		errors.LogInfoInner(context.Background(), err, "failed to unlock file: ", fl.path)
	}
	if err := fl.file.Close(); err != nil {
		errors.LogInfoInner(context.Background(), err, "failed to close file: ", fl.path)
	}
	if err := os.Remove(fl.path); err != nil {
		errors.LogInfoInner(context.Background(), err, "failed to remove file: ", fl.path)
	}
}
