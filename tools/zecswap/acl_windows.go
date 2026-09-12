//go:build windows

package zecswap

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS (STANDARD_RIGHTS_REQUIRED | SYNCHRONIZE | 0x1FF), what icacls lists as (F).
const fileAllAccess = windows.ACCESS_MASK(0x1F01FF)

// OwnerOnlyACL restricts a file on Windows to the account running the companion. The state file holds a maker's
// secret until his claim, the refund file the taker's pre-signed refund, and the registers which swaps this account
// has run; Windows ignores the 0600 each is created with and would give it the directory's ACL, which on a shared or
// backed-up machine may name other accounts. It sets the file's DACL through the Windows API (SetNamedSecurityInfo):
// protected, so nothing is inherited, with one entry, full control for the account this process runs as, named by its
// SID — what `icacls <file> /inheritance:r /grant:r <user>:F` does. Administrators can still take ownership of any
// file; this stops everyone else.
//
// It is tools/xmrswap's OwnerOnlyACL, and like it starts no program. tools/xmrswap ran icacls for this on every save
// until its interrupted command-line run tore down the console the companions had inherited: every icacls then died
// at start (0xc0000142), every save failed, and both sides stood still after the Claim (b3a707b). golang.org/x/sys is
// already a requirement of this module, so nothing is added to set the ACL directly.
func OwnerOnlyACL(path string) error {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("the account this process runs as: %w", err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: fileAllAccess,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(u.User.Sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("the owner-only ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("restricting %s to its owner: %w", path, err)
	}
	return nil
}
