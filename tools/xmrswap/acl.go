//go:build !windows

package xmrswap

import (
	"errors"
	"os"
)

// OwnerOnlyACL is Windows' (acl_windows.go). Elsewhere a file is created 0600, which the file system honours.
func OwnerOnlyACL(path string) error {
	return errors.New("OwnerOnlyACL is for Windows; here the file's 0600 is the guard")
}

// createPrivate creates name, which must not exist yet, for this account alone: O_EXCL and 0600, which a umask can
// only narrow. A file already at name is an error, never one to open.
func createPrivate(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}
