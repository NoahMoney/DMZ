//go:build !windows

package xmrswap

import (
	"os"
	"testing"
)

// assertPrivate: here a file's mode is its guard, and the companion's files are 0600.
func assertPrivate(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if m := fi.Mode().Perm(); m&0o077 != 0 {
		t.Fatalf("%s has mode %o", path, m)
	}
}
