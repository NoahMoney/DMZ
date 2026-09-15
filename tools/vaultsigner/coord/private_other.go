//go:build !windows

package coord

import (
	"fmt"
	"os"
)

// createPrivate creates name, which must not exist yet, for this account alone: O_EXCL and 0600, which a umask can
// only narrow. A file already at name is an error, never one to open.
func createPrivate(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// checkPrivate refuses a key file that is not a regular file (a symbolic link included) or that any other account
// can read or write.
func checkPrivate(name string) error {
	st, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("coord: %s is not a regular file (a symbolic link is refused too)", name)
	}
	if p := st.Mode().Perm(); p&0o077 != 0 {
		return fmt.Errorf("coord: %s can be read or written by other accounts (mode %04o). It holds a private key: treat that key as exposed, generate a new one and re-pin it", name, p)
	}
	return nil
}
