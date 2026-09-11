package xmrswap

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OwnerOnlyACL restricts a file on Windows to the account running the companion (GAP-020): it removes every inherited
// entry and grants that account alone full control, with icacls, the system's own tool — so no new dependency is
// needed for what one command does. The state file holds this party's key share, and after an exit the peer's too;
// Windows ignores the 0600 it is created with and would give it the directory's ACL, which on a shared or backed-up
// machine may name other accounts. Administrators can still take ownership of any file; this stops everyone else.
func OwnerOnlyACL(path string) error {
	user, domain := os.Getenv("USERNAME"), os.Getenv("USERDOMAIN")
	if user == "" {
		return errors.New("USERNAME is not set: cannot name the owner the file is to be restricted to")
	}
	if domain != "" {
		user = domain + `\` + user
	}
	out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", user+":F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
