//go:build windows

package coord

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A key file that another account can read is refused on Windows too: the DACL is read through the API and any
// allow entry that grants read to someone other than this account, SYSTEM or Administrators fails the load.
func TestLoadRefusesAKeyOthersCanReadOnWindows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k")
	if _, err := GenerateIdentityFile(p, testAddr(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentityFile(p); err != nil {
		t.Fatalf("a fresh owner-only key did not load: %v", err)
	}
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	// *S-1-1-0 is Everyone, named by SID so the test does not depend on the system language
	if out, err := exec.Command(filepath.Join(sys, "icacls.exe"), p, "/grant", "*S-1-1-0:(R)").CombinedOutput(); err != nil {
		t.Fatalf("icacls: %v %s", err, out)
	}
	if _, err := LoadIdentityFile(p); err == nil || !strings.Contains(err.Error(), "S-1-1-0") {
		t.Fatalf("a key readable by Everyone loaded: %v", err)
	}
}
