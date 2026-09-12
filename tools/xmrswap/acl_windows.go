//go:build windows

package xmrswap

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS (STANDARD_RIGHTS_REQUIRED | SYNCHRONIZE | 0x1FF), what icacls lists as (F).
const fileAllAccess = windows.ACCESS_MASK(0x1F01FF)

// OwnerOnlyACL restricts an existing file on Windows to the account running the companion (GAP-020): its DACL is set
// through the Windows API (SetNamedSecurityInfo), protected, so nothing is inherited, with one entry, full control for
// the account this process runs as, named by its SID — what `icacls <file> /inheritance:r /grant:r <user>:F` does.
// Administrators can still take ownership of any file; this stops everyone else. It starts no program. The companion's
// own files no longer need it: writeAtomic creates each one owner-only (createPrivate), with no moment in between.
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

// createPrivate creates name, which must not exist yet, for this account alone from the moment it exists: CreateFileW
// with CREATE_NEW, so a file already at name is an error and never one opened, and with SECURITY_ATTRIBUTES whose DACL
// is protected (nothing inherited) and grants full control to this account's SID and nobody else. The file is opened
// without sharing, so not even this account's other processes can open it while it is written.
//
// Before, the temporary state file was created with the directory's inherited ACL and restricted a moment later: a
// process that opened it in between kept its handle, and could read the key share written through it after the ACL
// changed, and one that planted the file first kept its ACL (the review of 2026-09-12, finding 4).
func createPrivate(name string) (*os.File, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("the account this process runs as: %w", err)
	}
	// D:P — a protected DACL; (A;;FA;;;<SID>) — one entry, allowing FILE_ALL_ACCESS to this account
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + u.User.Sid.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("the owner-only security descriptor: %w", err)
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: name, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}
