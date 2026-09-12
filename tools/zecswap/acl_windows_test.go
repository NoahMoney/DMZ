//go:build windows

package zecswap

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestOwnerOnlyACLStartsNoProgram is tools/xmrswap's test of the same guard, for this companion's files. The files are
// made owner-only with no program to start: with PATH empty nothing can be started by name — the state in which every
// icacls tools/xmrswap once started for this died, and every save failed (b3a707b) — and a file CreateOwnerOnly makes,
// and one OwnerOnlyACL restricts, each end with one entry, full control for this account, nothing inherited. A save of a
// maker's state holding his secret, through HardenFile as the command line sets it, goes through and ends the same.
func TestOwnerOnlyACLStartsNoProgram(t *testing.T) {
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	if err := OwnerOnlyACL(p); err != nil {
		t.Fatalf("with no program reachable, the ACL was not set: %v", err)
	}
	f, err := CreateOwnerOnly(filepath.Join(dir, "c.json"))
	if err != nil {
		t.Fatalf("with no program reachable, the owner-only file was not created: %v", err)
	}
	f.Close()
	old := HardenFile
	HardenFile = CreateOwnerOnly
	defer func() { HardenFile = old }()
	st := NewState(filepath.Join(t.TempDir(), "maker.json"), RoleMaker)
	st.Secret = strings.Repeat("11", 32)
	if err := st.Save(); err != nil {
		t.Fatalf("a state save with no program reachable: %v", err)
	}
	aces := ownerOnly(t, icacls, p)
	ownerOnly(t, icacls, filepath.Join(dir, "c.json"))
	ownerOnly(t, icacls, st.Path())
	t.Logf("icacls: %s", strings.Join(aces, " | "))
}

// TestOwnerOnlyFromTheMomentItExists (the 2026-09-12 review of writeAtomic): the old write created its temporary with
// the directory's ACL and restricted it afterwards, and Windows checks access only at open, so an account the
// inherited ACL let read the file could open it in that moment and keep reading what was written after. CreateOwnerOnly
// makes the file with its security descriptor in the call that creates it. Here the directory passes read access for
// BUILTIN\Users down to every file made in it, which a file created the ordinary way there does inherit; the file
// CreateOwnerOnly makes, read back through the handle that created it before a byte is written, is owned by this
// account and has a protected DACL of one entry, full control for this account, and no inherited entry. A name that
// already exists is refused as existing and not opened, and the file under it is left as it was.
func TestOwnerOnlyFromTheMomentItExists(t *testing.T) {
	me := currentUser(t)
	dir := t.TempDir()
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	// this account full control and BUILTIN\Users read, both passed down to files and folders (OI CI), nothing else
	dsd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + me.String() + ")(A;OICI;FR;;;" + users.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := dsd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatalf("setting the test directory's ACL: %v", err)
	}

	// the control: a file created the ordinary way in this directory inherits the Users entry
	plain := filepath.Join(dir, "plain.json")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	psd, err := windows.GetNamedSecurityInfo(plain, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if n := inheritedFor(t, psd, users); n == 0 {
		t.Fatalf("a file created the ordinary way did not inherit the directory's Users entry, so this test proves nothing:\n%s", psd)
	}

	p := filepath.Join(dir, "maker.json.x.tmp")
	f, err := CreateOwnerOnly(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("reading the new file's security through its handle: %v", err)
	}
	if fi, err := f.Stat(); err != nil || fi.Size() != 0 {
		t.Fatalf("the file was not read back before its first byte: %v %v", fi, err)
	}
	owner, _, err := sd.Owner()
	if err != nil || !owner.Equals(me) {
		t.Fatalf("owner %v (%v), not this account %v", owner, err, me)
	}
	ctl, _, err := sd.Control()
	if err != nil || ctl&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("the DACL is not protected, so the directory's entries flow in (control %#x, %v):\n%s", ctl, err, sd)
	}
	fdacl, _, err := sd.DACL()
	if err != nil || fdacl == nil {
		t.Fatalf("no DACL (%v): a missing DACL grants everyone everything", err)
	}
	if fdacl.AceCount != 1 {
		t.Fatalf("%d entries, not one:\n%s", fdacl.AceCount, sd)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(fdacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 ||
		ace.Mask != fileAllAccess || !sid.Equals(me) {
		t.Fatalf("the one entry is type %d flags %#x mask %#x for %v, not full control for %v, explicit:\n%s",
			ace.Header.AceType, ace.Header.AceFlags, ace.Mask, sid, me, sd)
	}
	if n := inheritedFor(t, sd, users); n != 0 {
		t.Fatalf("the owner-only file carries %d entries for Users:\n%s", n, sd)
	}

	// a name someone made first is refused, never opened
	taken := filepath.Join(dir, "maker.json.y.tmp")
	if err := os.WriteFile(taken, []byte("planted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if g, err := CreateOwnerOnly(taken); err == nil {
		g.Close()
		t.Fatal("CreateOwnerOnly opened a file that existed")
	} else if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("refused, but not as existing, so writeAtomic would not draw another name: %v", err)
	}
	if b, err := os.ReadFile(taken); err != nil || string(b) != "planted" {
		t.Fatalf("the existing file changed: %q %v", b, err)
	}
}

// TestSwapFilesAreOwnerOnlyOnWindows (tools/xmrswap's GAP-020, for this companion): with HardenFile set, as the command
// line sets it on Windows, every file a whole swap writes — both state files, the taker's pre-signed refund, the two
// registers — carries no inherited entry and grants its owner alone, and the operator is not warned.
func TestSwapFilesAreOwnerOnlyOnWindows(t *testing.T) {
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	old := HardenFile
	HardenFile = CreateOwnerOnly
	defer func() { HardenFile = old }()
	w := runScenario(t, "happy", 81)
	for _, p := range swapFiles(t, w) {
		ownerOnly(t, icacls, p)
	}
	if msg := StateFileWarning(w.path(RoleMaker)); msg != "" {
		t.Fatalf("still warned: %s", msg)
	}
}

// currentUser is the SID of the account the test runs as.
func currentUser(t *testing.T) *windows.SID {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid
}

// inheritedFor counts the DACL entries of sd that name sid, and fails if any of them is explicit.
func inheritedFor(t *testing.T, sd *windows.SECURITY_DESCRIPTOR, sid *windows.SID) int {
	t.Helper()
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("no DACL: %v", err)
	}
	n := 0
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			n++
		}
	}
	return n
}

// ownerOnly reads a file's ACL with icacls, by its full path, and fails unless it is one entry, full control for this
// account, nothing inherited.
func ownerOnly(t *testing.T, icacls, path string) []string {
	t.Helper()
	out, err := exec.Command(icacls, path).CombinedOutput()
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
		t.Fatalf("%s is not owner-only:\n%s", filepath.Base(path), text)
	}
	return aces
}
