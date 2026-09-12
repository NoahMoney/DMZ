//go:build windows

package xmrswap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestTheTaskCommandLineRunsTheCompanionAsGiven (the review of 2026-09-12, finding 3): the task's program and arguments,
// joined into one command line and started by CreateProcessW with no program named apart from the line — the start that
// splits an unquoted path at its first space, the most a task's line could be misread — start the companion, in a
// directory whose name has a space and parentheses, in its own working directory, with every awkward argument as it was
// given; the program planted where a split at the space would look never runs; and CommandLineToArgvW reads the line the
// same way.
func TestTheTaskCommandLineRunsTheCompanionAsGiven(t *testing.T) {
	dir := t.TempDir()
	child, _ := copyTestBinary(t, filepath.Join(dir, "wake dir (x86)"), "wake-child.exe", nil)
	planted, _ := copyTestBinary(t, dir, "wake.exe", nil)
	r := Resume{Exe: child, Args: awkwardArgs("windows"), Dir: dir}
	env, err := windowsWakeEnv(time.Now(), "x", r)
	if err != nil {
		t.Fatal(err)
	}
	v := envValues(env)
	line := v["XMRSWAP_WAKE_EXE"] + " " + v["XMRSWAP_WAKE_ARGS"]
	if parts, err := windows.DecomposeCommandLine(line); err != nil || len(parts) == 0 || parts[0] != child || !reflect.DeepEqual(parts[1:], r.Args) {
		t.Fatalf("CommandLineToArgvW reads %s as %q (%v)", line, parts, err)
	}
	cl, err := windows.UTF16PtrFromString(line)
	if err != nil {
		t.Fatal(err)
	}
	wd, err := windows.UTF16PtrFromString(v["XMRSWAP_WAKE_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cl, nil, nil, false, windows.CREATE_NO_WINDOW, nil, wd, &si, &pi); err != nil {
		t.Fatalf("CreateProcessW: %v", err)
	}
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)
	if ev, err := windows.WaitForSingleObject(pi.Process, 60000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("the companion did not finish: %d %v", ev, err)
	}
	b, err := os.ReadFile(child + ".out")
	if err != nil {
		t.Fatalf("the companion did not run: %v", err)
	}
	var got wakeRun
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Args, r.Args) || !strings.EqualFold(filepath.Clean(got.Dir), filepath.Clean(dir)) {
		t.Fatalf("the companion ran\n in  %s\n with %q\nwant %s\n with %q", got.Dir, got.Args, dir, r.Args)
	}
	if _, err := os.Stat(planted + ".ran"); err == nil {
		t.Fatal("the program planted beside the companion's directory ran")
	}
}
