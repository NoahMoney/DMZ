package zecswap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
