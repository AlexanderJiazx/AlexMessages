//go:build !unix

package main

import "errors"

func checkRemovable(dir, home string) error {
	return errors.New("otapublish keys init-root runs only on macOS")
}
