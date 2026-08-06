//go:build !windows

package store

import (
	"errors"
	"os"
	"syscall"
)

func acquireInstanceLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func releaseInstanceLock(file *os.File) error {
	return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
}
