//go:build !windows

package xmrswap

import "errors"

// OwnerOnlyACL is Windows' (acl_windows.go). Elsewhere the state file is created 0600, which the file system honours,
// and the command line does not set HardenFile.
func OwnerOnlyACL(path string) error {
	return errors.New("OwnerOnlyACL is for Windows; here the file's 0600 is the guard")
}
