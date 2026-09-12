//go:build windows

package xmrswap

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestWindowsOwnProgramsAreFoundWithoutTheEnvironment (the review of 2026-09-12, finding 5): System32 is the directory
// GetSystemDirectoryW names, whatever %SystemRoot% and %windir% say, and wsl.exe and powershell.exe are found there.
func TestWindowsOwnProgramsAreFoundWithoutTheEnvironment(t *testing.T) {
	want, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	decoy := t.TempDir()
	t.Setenv("SystemRoot", decoy)
	t.Setenv("windir", decoy)
	got, err := systemDir()
	if err != nil || !strings.EqualFold(got, want) {
		t.Fatalf("with %%SystemRoot%% at a decoy the system directory is %q (%v), want %q", got, err, want)
	}
	if ps, err := systemPowerShell(); err != nil || !strings.EqualFold(filepath.Dir(filepath.Dir(filepath.Dir(ps))), want) {
		t.Fatalf("powershell.exe: %q %v", ps, err)
	}
	if w, err := systemWSL(); err == nil && !strings.EqualFold(filepath.Dir(w), want) {
		t.Fatalf("wsl.exe found at %s, outside %s", w, want)
	}
}

// TestTheDefaultWSLDistributionIsReadWithoutAProgram: the default distribution comes from this user's registry through
// the registry API, as wsl.exe reads it, with no reg.exe started; on a machine without WSL there is none to read.
func TestTheDefaultWSLDistributionIsReadWithoutAProgram(t *testing.T) {
	t.Setenv("PATH", "")
	d, err := defaultWSLDistro()
	if err != nil {
		t.Skipf("no default WSL distribution here: %v", err)
	}
	if !wslDistroName.MatchString(d) {
		t.Fatalf("the default distribution %q is not a name the companion accepts", d)
	}
	t.Logf("the default WSL distribution: %s", d)
}
