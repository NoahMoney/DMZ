//go:build !windows

package zecswap

import "errors"

// OwnerOnlyACL is Windows' (acl_windows.go). Elsewhere every file is created 0600, which the file system honours, and
// the command line does not set HardenFile.
func OwnerOnlyACL(path string) error {
	return errors.New("OwnerOnlyACL is for Windows; here the file's 0600 is the guard")
}
