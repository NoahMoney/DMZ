//go:build windows

package zecswap

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAllAccess is FILE_ALL_ACCESS (STANDARD_RIGHTS_REQUIRED | SYNCHRONIZE | 0x1FF), what icacls lists as (F).
const fileAllAccess = windows.ACCESS_MASK(0x1F01FF)

// CreateOwnerOnly creates path, which must not exist yet, restricted on Windows to the account running the companion
// from the moment it exists, and returns it open for writing. It is what the command line sets HardenFile to. The state
// file holds a maker's secret until his claim, the refund file the taker's pre-signed refund, and the registers which
// swaps this account has run; Windows ignores the 0600 each would be created with and would give it the directory's
// ACL, which on a shared or backed-up machine may name other accounts.
//
// The file is made with its security descriptor in the one CreateFile call that makes it: owned by this account, a
// protected DACL, so nothing the directory would pass down is inherited, and one entry, full control for this account,
// named by its SID. Windows checks access only when a file is opened, so a file created with the directory's ACL and
// restricted afterwards could be opened by another account in between, and a handle opened then keeps reading what is
// written after. CREATE_NEW refuses a name that exists, so a file someone else made first, and owns, is never opened
// or written into (writeAtomic then draws another name). Nothing shares the handle while it is open. Administrators can
// still take ownership of any file; this stops everyone else. It starts no program (TestOwnerOnlyACLStartsNoProgram).
func CreateOwnerOnly(path string) (*os.File, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("the account this process runs as: %w", err)
	}
	sid := u.User.Sid.String()
	// O: the owner, this account; D:P a protected DACL; (A;;FA;;;sid) its one entry, full control for this account
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;" + sid + ")")
	if err != nil {
		return nil, fmt.Errorf("the owner-only security descriptor: %w", err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.READ_CONTROL, 0, sa,
		windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	runtime.KeepAlive(sa)
	if err != nil {
		// ERROR_FILE_EXISTS is os.ErrExist to errors.Is, which is how writeAtomic knows to draw another name
		return nil, &os.PathError{Op: "CreateFile", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// OwnerOnlyACL restricts a file that already exists to the account running the companion: it sets the file's DACL
// through the Windows API (SetNamedSecurityInfo), protected, with one entry, full control for this account — what
// `icacls <file> /inheritance:r /grant:r <user>:F` does. writeAtomic no longer uses it: restricting a file after it is
// created leaves the moment in between open (CreateOwnerOnly does it in the creation).
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
