package zecswap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAtomicWritesNeverWriteIntoATemporaryNameAlreadyTaken (the 2026-09-12 review of writeAtomic): the temporary every
// write passes through was the predictable <file>.tmp, opened with O_TRUNC, so an account able to create files in the
// directory could make it first, own it, and read what the companion then wrote into it. Now the name is random and the
// file is created new. With the names drawn made predictable here, a name someone made first is left as it was and the
// save goes through under the next; with every name taken the save fails and the file on disk is the one before; and a
// write that fails once its temporary exists fails the save and removes the temporary. Run as the command line runs on
// this platform: through HardenFile on Windows, O_EXCL and 0600 elsewhere.
func TestAtomicWritesNeverWriteIntoATemporaryNameAlreadyTaken(t *testing.T) {
	oldHarden, oldSuffix := HardenFile, tempSuffix
	defer func() { HardenFile, tempSuffix = oldHarden, oldSuffix }()
	HardenFile = nil
	if runtime.GOOS == "windows" {
		HardenFile = CreateOwnerOnly
	}
	dir := t.TempDir()
	st := NewState(filepath.Join(dir, "maker.json"), RoleMaker)
	first, second := strings.Repeat("22", 32), strings.Repeat("33", 32)
	planted := st.Path() + ".taken.tmp"
	if err := os.WriteFile(planted, []byte("planted"), 0o644); err != nil {
		t.Fatal(err)
	}
	unchanged := func(when string) {
		t.Helper()
		if b, err := os.ReadFile(planted); err != nil || string(b) != "planted" {
			t.Fatalf("%s: the temporary name made first was written into or removed: %q %v", when, b, err)
		}
	}
	onDisk := func(when, want string) {
		t.Helper()
		got, err := LoadState(st.Path(), RoleMaker)
		if err != nil || got.Secret != want {
			t.Fatalf("%s: the state on disk holds %q (%v), want %q", when, got.Secret, err, want)
		}
		m, _ := filepath.Glob(filepath.Join(dir, "*"))
		if len(m) != 2 {
			t.Fatalf("%s: the directory holds %v, not the state and the planted file", when, m)
		}
	}

	names := []string{"taken", "fresh"}
	tempSuffix = func() (string, error) {
		s := names[0]
		names = names[1:]
		return s, nil
	}
	st.Secret = first
	if err := st.Save(); err != nil {
		t.Fatalf("a save beside a temporary name already taken: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("the save did not draw a second name after the first was taken (%v left)", names)
	}
	unchanged("the save")
	onDisk("the save", first)

	tempSuffix = func() (string, error) { return "taken", nil }
	st.Secret = second
	if err := st.Save(); err == nil {
		t.Fatal("a save whose every temporary name was taken went through")
	}
	unchanged("every name taken")
	onDisk("every name taken", first)

	// the temporary is created, and then the write into it fails: the save fails and leaves nothing behind
	tempSuffix = oldSuffix
	HardenFile = func(p string) (*os.File, error) {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		f.Close()
		return os.Open(p) // open for reading only, so the write fails
	}
	if err := st.Save(); err == nil {
		t.Fatal("a save whose write failed went through")
	}
	unchanged("a failed write")
	onDisk("a failed write", first)
}

// swapFiles is every file a finished swap left in its directory: both state files (the maker's holds his secret), the
// taker's pre-signed refund, and the used-hash and consumed-funding registers. It fails if one is missing, or if the
// directory holds anything else, so a test of their protection cannot pass over a file never written or one it missed.
func swapFiles(t *testing.T, w *world) []string {
	t.Helper()
	want := []string{w.path(RoleMaker), w.path(RoleTaker), w.state(RoleTaker).RefundFile, UsedPath(w.path(RoleMaker)), ConsumedPath(w.path(RoleMaker))}
	for _, p := range want {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("the swap did not write %s: %v", filepath.Base(p), err)
		}
	}
	if m, _ := filepath.Glob(filepath.Join(w.dir, "*")); len(m) != len(want) {
		t.Fatalf("the swap's directory holds %v, not the %d files expected", m, len(want))
	}
	return want
}

// TestSwapFilesArePrivate is tools/xmrswap's TestStateFileIsPrivate for every file a swap writes. Elsewhere than
// Windows each is created 0600, which the file system enforces. On Windows, where the mode bits are not enforced, a
// companion without HardenFile says so, naming the file, instead of the assertion being skipped in silence
// (TestSwapFilesAreOwnerOnlyOnWindows covers the ACL itself).
func TestSwapFilesArePrivate(t *testing.T) {
	old := HardenFile
	HardenFile = nil
	defer func() { HardenFile = old }()
	w := runScenario(t, "happy", 82)
	files := swapFiles(t, w)
	if runtime.GOOS == "windows" {
		if msg := StateFileWarning(files[0]); msg == "" || !strings.Contains(msg, files[0]) {
			t.Fatalf("no warning naming %s on a platform that does not enforce the mode: %q", files[0], msg)
		}
		return
	}
	if msg := StateFileWarning(files[0]); msg != "" {
		t.Fatalf("a warning on a platform that does enforce the mode: %q", msg)
	}
	for _, p := range files {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if m := fi.Mode().Perm(); m&0o077 != 0 {
			t.Fatalf("%s: mode %o", filepath.Base(p), m)
		}
	}
}
