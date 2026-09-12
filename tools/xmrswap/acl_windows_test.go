//go:build windows

package xmrswap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOwnerOnlyACLStartsNoProgram: the owner-only ACL is set with no program to start. With PATH empty nothing can be
// started by name — the state the interrupted command-line run was in, when every icacls the companion started died
// and every save failed — and the file still ends with one entry, full control for this account, nothing inherited;
// a state save, which applies it before a byte is written, goes through.
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
	old := HardenFile
	HardenFile = OwnerOnlyACL
	defer func() { HardenFile = old }()
	st := NewState(filepath.Join(t.TempDir(), "state.json"), "bob", "ab")
	st.Own.Secret = strings.Repeat("11", 32)
	if err := st.Save(); err != nil {
		t.Fatalf("a state save with no program reachable: %v", err)
	}
	t.Logf("icacls: %s", strings.Join(aces, " | "))
}
