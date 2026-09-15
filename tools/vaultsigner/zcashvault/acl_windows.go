//go:build windows

package zcashvault

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows ignores a file's 0600: a new file takes its directory's ACL, which on a shared or backed-up machine may
// name other accounts. So the seed file is created with an owner-only security descriptor in the one CreateFile call
// that makes it (the technique of tools/zecswap's CreateOwnerOnly, reproduced here so the vault depends on nothing
// the swap tools own), and every load checks the file's DACL grants nobody but this account, SYSTEM and the
// Administrators group — the two that can take any file anyway.

func createOwnerOnly(path string) (*os.File, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("the account this process runs as: %w", err)
	}
	sid := u.User.Sid.String()
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
		return nil, &os.PathError{Op: "CreateFile", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

func checkOwnerOnly(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("it is not a regular file")
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("the account this process runs as: %w", err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("reading its ACL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errors.New("it has no access list, which on Windows means everyone may read it")
	}
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	admins, _ := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("reading its ACL: %w", err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue // a deny entry takes access away; it cannot widen it
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(u.User.Sid) || (system != nil && sid.Equals(system)) || (admins != nil && sid.Equals(admins)) {
			continue
		}
		return fmt.Errorf("its ACL also grants %s; restrict it to this account (icacls <file> /inheritance:r /grant:r %%USERNAME%%:F)", sid.String())
	}
	return nil
}
