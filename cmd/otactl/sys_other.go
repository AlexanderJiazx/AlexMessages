//go:build !linux && !darwin

package main

import (
	"errors"
	"io"
)

// lockStore does not lock on this platform; otactl runs on the Pi (Linux)
// and in tests on the Mac.
func lockStore(path string, stderr io.Writer) (func(), error) {
	return func() {}, nil
}

func freeBytes(path string) (uint64, error) {
	return 0, errors.New("free space is not measured on this platform")
}
