//go:build windows

package zecswap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOwnerOnlyACLStartsNoProgram is tools/xmrswap's test of the same guard, for this companion's files. The ACL is set
// with no program to start: with PATH empty nothing can be started by name — the state in which every icacls
// tools/xmrswap once started for this died, and every save failed (b3a707b) — and the file still ends with one entry,
// full control for this account, nothing inherited. A save of a maker's state holding his secret, which applies the ACL
// before a byte is written, goes through and ends the same.
func TestOwnerOnlyACLStartsNoProgram(t *testing.T) {
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	p := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	if err := OwnerOnlyACL(p); err != nil {
		t.Fatalf("with no program reachable, the ACL was not set: %v", err)
	}
	aces := ownerOnly(t, icacls, p)
	old := HardenFile
	HardenFile = OwnerOnlyACL
	defer func() { HardenFile = old }()
	st := NewState(filepath.Join(t.TempDir(), "maker.json"), RoleMaker)
	st.Secret = strings.Repeat("11", 32)
	if err := st.Save(); err != nil {
		t.Fatalf("a state save with no program reachable: %v", err)
	}
	ownerOnly(t, icacls, st.Path())
	t.Logf("icacls: %s", strings.Join(aces, " | "))
}

// TestSwapFilesAreOwnerOnlyOnWindows (tools/xmrswap's GAP-020, for this companion): with HardenFile set, as the command
// line sets it on Windows, every file a whole swap writes — both state files, the taker's pre-signed refund, the two
// registers — carries no inherited entry and grants its owner alone, and the operator is not warned.
func TestSwapFilesAreOwnerOnlyOnWindows(t *testing.T) {
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	old := HardenFile
	HardenFile = OwnerOnlyACL
	defer func() { HardenFile = old }()
	w := runScenario(t, "happy", 81)
	for _, p := range swapFiles(t, w) {
		ownerOnly(t, icacls, p)
	}
	if msg := StateFileWarning(w.path(RoleMaker)); msg != "" {
		t.Fatalf("still warned: %s", msg)
	}
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
