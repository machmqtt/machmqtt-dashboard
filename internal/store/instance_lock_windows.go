//go:build windows

package store

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func acquireInstanceLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped,
	)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func releaseInstanceLock(file *os.File) error {
	var overlapped windows.Overlapped
	unlockErr := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
	return errors.Join(unlockErr, file.Close())
}
