//go:build linux || darwin

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// lockStore takes the store's lock, waiting for another otactl that holds
// it, and returns the function that releases it.
func lockStore(path string, stderr io.Writer) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		fmt.Fprintln(stderr, "otactl: waiting for another otactl to finish")
		if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, err
		}
	}
	return func() {
		syscall.Flock(fd, syscall.LOCK_UN)
		f.Close()
	}, nil
}

// freeBytes is the space available to this user on the filesystem of path.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
