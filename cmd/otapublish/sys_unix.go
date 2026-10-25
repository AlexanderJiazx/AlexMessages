//go:build unix

package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
)

// checkRemovable refuses a directory that is not on an external volume: the
// root key never goes on the Mac's disk (D4). On macOS an external volume
// is mounted under /Volumes/ and has another device number than the home
// folder.
func checkRemovable(dir, home string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if abs, err = filepath.EvalSymlinks(abs); err != nil {
		return err
	}
	if !strings.HasPrefix(abs, "/Volumes/") {
		return fmt.Errorf("%s is not on a USB stick (a folder under /Volumes/): the root key never goes on the Mac's disk", dir)
	}
	var d, h syscall.Stat_t
	if err := syscall.Stat(abs, &d); err != nil {
		return err
	}
	if err := syscall.Stat(home, &h); err != nil {
		return err
	}
	if d.Dev == h.Dev {
		return fmt.Errorf("%s is on the same disk as %s: the root key never goes on the Mac's disk", dir, home)
	}
	return nil
}
