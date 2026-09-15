//go:build !windows

package zcashvault

import (
	"fmt"
	"os"
)

// createOwnerOnly creates a new file readable and writable by its owner only, refusing a path that exists.
func createOwnerOnly(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// checkOwnerOnly refuses a file any other account can read or write.
func checkOwnerOnly(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("it is not a regular file")
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("its mode is %o; a seed must be 0600 (chmod 600 it)", st.Mode().Perm())
	}
	return nil
}
