//go:build windows

package xmrswap

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownerOnlyDACL fails unless sd's DACL is protected and holds exactly one entry: full control for this account.
func ownerOnlyDACL(t *testing.T, what string, sd *windows.SECURITY_DESCRIPTOR) {
	t.Helper()
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("%s: no DACL (%v)", what, err)
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 || dacl.AceCount != 1 {
		t.Fatalf("%s: the DACL is not protected with one entry (control %#x, %d entries)", what, control, dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != fileAllAccess || !sid.Equals(u.User.Sid) {
		t.Fatalf("%s: the entry is type %d, mask %#x, for %s; want full control for %s alone", what, ace.Header.AceType, ace.Mask, sid, u.User.Sid)
	}
}

// assertPrivate: on Windows a file's mode means nothing, and the companion's files carry a protected DACL granting this
// account alone.
func assertPrivate(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	ownerOnlyDACL(t, path, sd)
}

// TestATempFileIsOwnerOnlyFromItsCreation (the review of 2026-09-12, finding 4): the temporary state file is owner-only
// before a byte is written to it, as CreateFileW made it, with no moment in which it had the directory's inherited ACL;
// while it is being written nothing else can open it, this account's other processes included; and a name already taken
// is refused.
func TestATempFileIsOwnerOnlyFromItsCreation(t *testing.T) {
	name := filepath.Join(t.TempDir(), "s.json.tmp-0123456789abcdef")
	f, err := createPrivate(name)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	ownerOnlyDACL(t, "the new temporary file", sd)
	if g, err := os.Open(name); err == nil {
		g.Close()
		t.Fatal("the temporary file could be opened a second time while it was being written")
	} else if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Logf("a second open was refused with %v", err)
	}
	if _, err := f.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if g, err := createPrivate(name); err == nil {
		g.Close()
		t.Fatal("a name already taken was opened")
	}
	assertPrivate(t, name)
}

// TestOwnerOnlyACLStartsNoProgram: the owner-only ACL is set with no program to start. With PATH empty nothing can be
// started by name — the state the interrupted command-line run was in, when every icacls the companion started died
// and every save failed — and the file still ends with one entry, full control for this account, nothing inherited;
// a state save, whose file is created owner-only, goes through.
func TestOwnerOnlyACLStartsNoProgram(t *testing.T) {
	sys, err := systemDir()
	if err != nil {
		t.Fatal(err)
	}
	icacls := filepath.Join(sys, "icacls.exe")
	p := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	if err := OwnerOnlyACL(p); err != nil {
		t.Fatalf("with no program reachable, the ACL was not set: %v", err)
	}
	out, err := exec.Command(icacls, p).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls (by its full path, to look): %v %s", err, out)
	}
	text := string(out)
	var aces []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, ":(") {
			aces = append(aces, strings.TrimSpace(line))
		}
	}
	if len(aces) != 1 || !strings.Contains(text, os.Getenv("USERNAME")+":(F)") || strings.Contains(text, "(I)") {
		t.Fatalf("the file's ACL is not one owner-only entry:\n%s", text)
	}
	assertPrivate(t, p)
	st := NewState(filepath.Join(t.TempDir(), "state.json"), "bob", "ab")
	st.Own.Secret = strings.Repeat("11", 32)
	if err := st.Save(); err != nil {
		t.Fatalf("a state save with no program reachable: %v", err)
	}
	assertPrivate(t, st.Path())
	t.Logf("icacls: %s", strings.Join(aces, " | "))
}
