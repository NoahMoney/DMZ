//go:build !windows

package zecswap

import (
	"errors"
	"os"
)

// CreateOwnerOnly is Windows' (acl_windows.go). Elsewhere writeAtomic creates every temporary O_EXCL and 0600, which
// the file system honours, and the command line does not set HardenFile.
func CreateOwnerOnly(path string) (*os.File, error) {
	return nil, errors.New("CreateOwnerOnly is for Windows; here the file's 0600 is the guard")
}

// OwnerOnlyACL is Windows' (acl_windows.go), as CreateOwnerOnly is.
func OwnerOnlyACL(path string) error {
	return errors.New("OwnerOnlyACL is for Windows; here the file's 0600 is the guard")
}
