//go:build windows

package coord

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createPrivate creates name, which must not exist yet, for this account alone from the moment it exists: CreateFileW
// with CREATE_NEW and a protected DACL granting full control to this account's SID and nobody else, opened without
// sharing. The same construction as tools/xmrswap (the review of 2026-09-12, finding 4): there is no moment in which
// the file carries the directory's inherited ACL.
func createPrivate(name string) (*os.File, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("the account this process runs as: %w", err)
	}
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

// Rights that let an account read the file, or give itself the right to: FILE_READ_DATA, GENERIC_READ, GENERIC_ALL,
// WRITE_DAC and WRITE_OWNER.
const readableMask = 0x1 | 0x80000000 | 0x10000000 | 0x40000 | 0x80000

// checkPrivate refuses a key file that is not a regular file, or whose DACL grants read (or the power to grant
// read) to any account other than this one, SYSTEM and the Administrators group, which can take any file anyway.
// A missing DACL grants everyone everything and is refused.
func checkPrivate(name string) error {
	st, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("coord: %s is not a regular file (a link is refused too)", name)
	}
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("coord: reading the ACL of %s: %w", name, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("coord: %s has no DACL, so every account can read it. It holds a private key: treat that key as exposed, generate a new one and re-pin it", name)
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("the account this process runs as: %w", err)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("coord: reading the ACL of %s: %w", name, err)
		}
		// ACCESS_ALLOWED (0) and ACCESS_ALLOWED_CALLBACK (9) grant; deny and audit entries only narrow or record
		if t := ace.Header.AceType; t != windows.ACCESS_ALLOWED_ACE_TYPE && t != 9 {
			continue
		}
		if ace.Mask&readableMask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(u.User.Sid) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			continue
		}
		return fmt.Errorf("coord: %s can be read by %s. It holds a private key: treat that key as exposed, generate a new one with `vaultsigner keygen` and re-pin it", name, sid.String())
	}
	return nil
}
