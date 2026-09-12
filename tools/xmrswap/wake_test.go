package xmrswap

// The review of 2026-09-12, finding 3: the Windows wake ran `cmd.exe /k <resume command>`, and cmd /k strips a first and
// a last quote from such a line and splits it at the space, so with a space in the companion's path and another argument
// quoted it ran a program planted beside the companion's directory; the Linux wake ran the same line through `sh -c`,
// which expands $ and backticks. The wake now registers the companion as the task's program and its arguments apart,
// and runs no shell on either.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// wakeRun is what a companion a wake started reports: the arguments and the directory it was started with.
type wakeRun struct {
	Args []string
	Dir  string
}

// wakeChildMain is this test binary as the companion a wake starts (TestMain runs it for a copy named wake-child): it
// writes the arguments and the directory it was started with to <exe>.out, and exits.
func wakeChildMain(exe string) int {
	dir, _ := os.Getwd()
	b, err := json.Marshal(wakeRun{Args: os.Args[1:], Dir: dir})
	if err != nil {
		return 1
	}
	if err := os.WriteFile(exe+".out", b, 0o600); err != nil {
		return 1
	}
	return 0
}

// wakePlantedMain is this test binary as a program planted where a command line split at a space would look for one
// (TestMain runs it for a copy named wake): it leaves <exe>.ran behind.
func wakePlantedMain(exe string) int {
	_ = os.WriteFile(exe+".ran", []byte("ran"), 0o600)
	return 0
}

// awkwardArgs are arguments a shell or cmd.exe would split, strip, expand or run: quotes, spaces and tabs, backslashes
// before a quote and at the end, $ and backticks, and cmd's own metacharacters. A % is among them only off Windows,
// where the Task Scheduler itself expands %VARIABLE% and the wake is refused instead
// (TestAWakeIsRefusedWhatTheSchedulerWouldRewrite).
func awkwardArgs(goos string) []string {
	a := []string{"bob", "-state", `swap dir\bob.json`, "-channel", `C:\swap "dir"\`, `say "hi" & exit`, "$HOME", "${HOME}", "`whoami`",
		"$(id)", `a\\b`, `trailing\`, `\"`, "&calc", "|more", "^", "(x86)", "!x!", "", "'single'", "a\tb"}
	if goos != "windows" {
		a = append(a, "%h", "%%")
	}
	return a
}

// splitWindowsArgs reads a command line's arguments back as Go's own os.Args does on Windows (os.commandLineToArgv): the
// parser the companion's arguments meet when a task starts it.
func splitWindowsArgs(cmd string) []string {
	var args []string
	for len(cmd) > 0 {
		if cmd[0] == ' ' || cmd[0] == '\t' {
			cmd = cmd[1:]
			continue
		}
		var arg []byte
		arg, cmd = nextWindowsArg(cmd)
		args = append(args, string(arg))
	}
	return args
}

func nextWindowsArg(cmd string) ([]byte, string) {
	var b []byte
	inquote, nslash := false, 0
	slashes := func(n int) {
		for ; n > 0; n-- {
			b = append(b, '\\')
		}
	}
	for ; len(cmd) > 0; cmd = cmd[1:] {
		c := cmd[0]
		switch c {
		case ' ', '\t':
			if !inquote {
				slashes(nslash)
				return b, cmd[1:]
			}
		case '"':
			slashes(nslash / 2)
			if nslash%2 == 0 {
				if inquote && len(cmd) > 1 && cmd[1] == '"' {
					b = append(b, c)
					cmd = cmd[1:]
				}
				inquote = !inquote
			} else {
				b = append(b, c)
			}
			nslash = 0
			continue
		case '\\':
			nslash++
			continue
		}
		slashes(nslash)
		nslash = 0
		b = append(b, c)
	}
	slashes(nslash)
	return b, ""
}

// envValues is a NAME=value list as a map.
func envValues(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// TestTheWakeRegistersAProgramAndItsArguments: the Windows task's program is the companion's .exe, quoted as one path;
// its arguments are one command line that the companion's own parser reads back into exactly what it was given; its
// directory is the one the companion ran in; and every value reaches PowerShell through the environment, none of it
// through the script, which names no cmd.exe. The Linux timer's service is the companion's executable with its
// arguments as an argv, in its directory, with systemd's own $VARIABLE expansion off and no shell.
func TestTheWakeRegistersAProgramAndItsArguments(t *testing.T) {
	at := time.Date(2026, 9, 14, 3, 4, 5, 0, time.Local)
	win := Resume{Exe: `C:\Program Files (x86)\my dir\xmrswap.exe`, Args: awkwardArgs("windows"), Dir: `C:\swap dir (2)`}
	env, err := windowsWakeEnv(at, "xmrswap-abc", win)
	if err != nil {
		t.Fatal(err)
	}
	got := envValues(env)
	if len(env) != 5 || got["XMRSWAP_WAKE_EXE"] != `"`+win.Exe+`"` || got["XMRSWAP_WAKE_DIR"] != win.Dir ||
		got["XMRSWAP_WAKE_AT"] != "2026-09-14 03:04:05" || got["XMRSWAP_WAKE_NAME"] != "xmrswap-abc" {
		t.Fatalf("the task's values: %q", env)
	}
	if back := splitWindowsArgs(got["XMRSWAP_WAKE_ARGS"]); !reflect.DeepEqual(back, win.Args) {
		t.Fatalf("the arguments do not read back as given:\n got  %q\n want %q\n from %s", back, win.Args, got["XMRSWAP_WAKE_ARGS"])
	}
	if strings.Contains(strings.ToLower(registerTaskScript), "cmd") || !strings.Contains(registerTaskScript, "$env:XMRSWAP_WAKE_ARGS") ||
		!strings.Contains(registerTaskScript, "WorkingDirectory = $env:XMRSWAP_WAKE_DIR") {
		t.Fatalf("the registration script:\n%s", registerTaskScript)
	}
	lin := Resume{Exe: "/opt/dmz/xmrswap", Args: awkwardArgs("linux"), Dir: "/srv/swap dir"}
	args, err := linuxWakeArgs(at, "xmrswap-abc", lin)
	want := append([]string{"--user", "--unit=xmrswap-abc", "--on-calendar=2026-09-14 03:04:05", "--timer-property=WakeSystem=yes",
		"--expand-environment=no", "--working-directory=/srv/swap dir", "--", lin.Exe}, lin.Args...)
	if err != nil || !reflect.DeepEqual(args, want) {
		t.Fatalf("systemd-run %q (%v)\n want %q", args, err, want)
	}
}

// TestAWakeIsRefusedWhatTheSchedulerWouldRewrite: a resume the Task Scheduler or systemd would not start as written gets
// no wake, and the card says to come back by hand: a program that is not an absolute path to an .exe (Windows starts a
// .bat through cmd.exe, which parses its arguments again), a % anywhere (the Task Scheduler expands %VARIABLE%), no
// directory (a relative -state would name another file), a control character. No refusal repeats the argument, which may
// be a secret.
func TestAWakeIsRefusedWhatTheSchedulerWouldRewrite(t *testing.T) {
	at := time.Now().Add(time.Hour)
	ok := Resume{Exe: `C:\dmz\xmrswap.exe`, Args: []string{"bob"}, Dir: `C:\swaps`}
	if _, err := windowsWakeEnv(at, "x", ok); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		r    Resume
		want string
	}{
		{"a .bat", Resume{Exe: `C:\my dir\app.bat`, Args: ok.Args, Dir: ok.Dir}, ".exe"},
		{"a relative program", Resume{Exe: `xmrswap.exe`, Args: ok.Args, Dir: ok.Dir}, "absolute"},
		{"a % in an argument", Resume{Exe: ok.Exe, Args: []string{"bob", "-channel", `C:\%SECRETVAR%\swap`}, Dir: ok.Dir}, "%VARIABLE%"},
		{"a % in the directory", Resume{Exe: ok.Exe, Args: ok.Args, Dir: `C:\%SECRETVAR%`}, "%VARIABLE%"},
		{"no directory", Resume{Exe: ok.Exe, Args: ok.Args}, "directory"},
		{"a control character", Resume{Exe: ok.Exe, Args: []string{"bob\x01SECRETVAR"}, Dir: ok.Dir}, "control character"},
	} {
		_, err := windowsWakeEnv(at, "x", c.r)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "SECRETVAR") {
			t.Fatalf("windows, %s: %v", c.name, err)
		}
	}
	lin := Resume{Exe: "/opt/dmz/xmrswap", Args: []string{"bob"}, Dir: "/srv"}
	if _, err := linuxWakeArgs(at, "x", lin); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		r    Resume
	}{
		{"a relative program", Resume{Exe: "xmrswap", Args: lin.Args, Dir: lin.Dir}},
		{"no directory", Resume{Exe: lin.Exe, Args: lin.Args}},
		{"a control character", Resume{Exe: lin.Exe, Args: []string{"bob\nSECRETVAR"}, Dir: lin.Dir}},
	} {
		if _, err := linuxWakeArgs(at, "x", c.r); err == nil || strings.Contains(err.Error(), "SECRETVAR") {
			t.Fatalf("linux, %s: %v", c.name, err)
		}
	}
}

// TestAWakeRunsTheCompanionWithItsArgumentsVerbatim: a real wake, registered on this machine and started at once: the
// Task Scheduler on Windows, the user's systemd on Linux. The companion is a copy of this binary in a directory whose
// name has a space and parentheses, a program is planted where a command line split at the space would look, and the
// arguments are every awkward kind. The companion starts in its own directory with every argument as it was given, and
// the planted program never runs. The task is removed at the end. It adds a task to the machine's scheduler and runs it,
// so it runs only with XMRSWAP_TEST_WAKE=1.
func TestAWakeRunsTheCompanionWithItsArgumentsVerbatim(t *testing.T) {
	if os.Getenv("XMRSWAP_TEST_WAKE") != "1" || (runtime.GOOS != "windows" && runtime.GOOS != "linux") {
		t.Skip("set XMRSWAP_TEST_WAKE=1 to register a real wake on this machine, start it at once, and remove it")
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	dir := t.TempDir()
	child, _ := copyTestBinary(t, filepath.Join(dir, "wake dir (x86)"), "wake-child"+ext, nil)
	planted, _ := copyTestBinary(t, dir, "wake"+ext, nil)
	var id [6]byte
	_, _ = rand.Read(id[:])
	name := WakeTaskName("test-verbatim-" + hex.EncodeToString(id[:]))
	defer UnregisterWake(name)
	r := Resume{Exe: child, Args: awkwardArgs(runtime.GOOS), Dir: dir}
	what, err := RegisterWake(time.Now().Add(24*time.Hour), name, r)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("registered: %s", what)
	switch runtime.GOOS {
	case "windows":
		ps, err := systemPowerShell()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", `Start-ScheduledTask -TaskName $env:XMRSWAP_WAKE_NAME`)
		cmd.Env = append(withoutWakeEnv(os.Environ()), "XMRSWAP_WAKE_NAME="+name)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Start-ScheduledTask: %v %s", err, out)
		}
	case "linux":
		if out, err := exec.Command("systemctl", "--user", "start", name+".service").CombinedOutput(); err != nil {
			t.Fatalf("systemctl --user start: %v %s", err, out)
		}
	}
	var got wakeRun
	for deadline := time.Now().Add(90 * time.Second); ; {
		b, err := os.ReadFile(child + ".out")
		if err == nil && json.Unmarshal(b, &got) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the wake did not start the companion within 90 s")
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !reflect.DeepEqual(got.Args, r.Args) || !strings.EqualFold(filepath.Clean(got.Dir), filepath.Clean(dir)) {
		t.Fatalf("the wake started the companion\n in  %s\n with %q\nwant %s\n with %q", got.Dir, got.Args, dir, r.Args)
	}
	if _, err := os.Stat(planted + ".ran"); err == nil {
		t.Fatal("the program planted beside the companion's directory ran")
	}
	if err := UnregisterWake(name); err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "windows":
		if out, _ := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"if (Get-ScheduledTask -TaskName "+psQuote(name)+" -ErrorAction SilentlyContinue) { 'still there' } else { 'gone' }").CombinedOutput(); !strings.Contains(string(out), "gone") {
			t.Fatalf("after UnregisterWake the task is %s", out)
		}
	case "linux":
		if loaded, props := systemdTimer(t, name); loaded {
			t.Fatalf("after UnregisterWake systemd still has the timer:\n%s", props)
		}
	}
	t.Logf("the companion ran in %s with %d arguments as given; the planted %s did not run", got.Dir, len(got.Args), planted)
}
