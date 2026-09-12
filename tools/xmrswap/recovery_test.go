package xmrswap

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestTheRecoveryCardComesBeforeTheXmrLock (MT-B15, U9): at the moment alice's XMR transfer is made, her companion has
// already printed the card — the state file named as a secret, the resume command, every height as a clock time — and
// asked the OS to wake the machine, with that command, before the first window that closes.
func TestTheRecoveryCardComesBeforeTheXmrLock(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 231})
	start := time.Now()
	const resume = `xmrswap alice -state "swap dir/alice.json" -net stagenet`
	var woken []string
	var wakeAt time.Time
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", func(c *Config) {
		c.ResumeCommand = resume
		c.Wake = func(at time.Time, name, command string) (string, error) {
			wakeAt = at
			woken = append(woken, name+"|"+command)
			return "a test wake at " + at.Format(time.RFC3339), nil
		}
	})
	var atTransfer string
	check := r.aliceW.onTransfer
	r.aliceW.onTransfer = func(to string, amount uint64) error {
		r.mu.Lock()
		atTransfer = strings.Join(r.logs, "\n")
		r.mu.Unlock()
		return check(to, amount)
	}
	r.until(400, r.bothDone, nil)
	if atTransfer == "" {
		t.Fatal("alice never locked XMR")
	}
	for _, want := range []string{"RECOVERY CARD", "SECRET: " + r.statePath("alice"), "resume: " + resume,
		"claim cutoff", "gate deadline", "finish by", "t1 ", "t2 ", "wake: a test wake at", "XMRSWAP_CHANNEL_KEY"} {
		if !strings.Contains(atTransfer, want) {
			t.Fatalf("the card printed before the XMR lock lacks %q:\n%s", want, atTransfer)
		}
	}
	if n := len(regexp.MustCompile(`height \d+\s+(about )?\d{4}-\d\d-\d\d \d\d:\d\d`).FindAllString(atTransfer, -1)); n < 6 {
		t.Fatalf("%d heights carry a clock time; the card has six", n)
	}
	st := r.loadState("alice")
	if len(woken) != 1 || woken[0] != WakeTaskName(st.SwapID)+"|"+resume {
		t.Fatalf("wakes registered: %v", woken)
	}
	// the first window to close is at most (t1 − the lock's first sight) blocks away; the wake comes before it
	latest := start.Add(time.Duration(st.Lock.T1-st.Lock.SeenHeight)*deroBlock + time.Minute)
	if !wakeAt.After(start) || !wakeAt.Before(latest) {
		t.Fatalf("the wake is set for %v, outside (%v, %v)", wakeAt, start, latest)
	}
}

// TestWakeRegistrationSaysWhyWhereThereIsNone (MT-B15): where an OS offers the companion no unprivileged wake, nothing
// is registered and the reason is given, and a wake with nothing to run is refused everywhere.
func TestWakeRegistrationSaysWhyWhereThereIsNone(t *testing.T) {
	at := time.Now().Add(time.Hour)
	if _, err := registerWake("darwin", at, "xmrswap-x", "xmrswap alice"); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("macOS: %v", err)
	}
	if _, err := registerWake("plan9", at, "xmrswap-x", "xmrswap alice"); err == nil {
		t.Fatal("an OS the companion knows nothing of registered a wake")
	}
	for _, goos := range []string{"windows", "linux", "darwin"} {
		if _, err := registerWake(goos, at, "xmrswap-x", ""); err == nil {
			t.Fatalf("%s: a wake with nothing to run was registered", goos)
		}
	}
	if got := WakeTaskName(`ab"; rm -rf /`); got != "xmrswap-abrm-rf" {
		t.Fatalf("task name %q", got)
	}
}

// TestRemovingAWakeThatIsNotThereIsNoError (MT-B15): a finished swap removes its wake, and the maker, who registers
// none, removes one that was never there; that is no error. It only reads the scheduler. (In the command-line run of
// 2026-09-12 the maker was told to delete a task that never existed: Unregister-ScheduledTask fails on a missing name.)
func TestRemovingAWakeThatIsNotThereIsNoError(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Task Scheduler is Windows'")
	}
	var id [6]byte
	_, _ = rand.Read(id[:])
	if err := UnregisterWake(WakeTaskName("never-registered-" + hex.EncodeToString(id[:]))); err != nil {
		t.Fatalf("removing a wake that is not there: %v", err)
	}
}

// TestRegisterWakeOnThisOS (MT-B15): the real registration on this machine, made, found with its wake setting, and
// removed. It adds a task to the machine's scheduler for a moment, so it runs only with XMRSWAP_TEST_WAKE=1.
func TestRegisterWakeOnThisOS(t *testing.T) {
	if os.Getenv("XMRSWAP_TEST_WAKE") != "1" {
		t.Skip("set XMRSWAP_TEST_WAKE=1 to register, find and remove a real wake task on this machine")
	}
	var id [6]byte
	_, _ = rand.Read(id[:])
	name := WakeTaskName("test-" + hex.EncodeToString(id[:]))
	at := time.Now().Add(24 * time.Hour).Truncate(time.Minute)
	what, err := RegisterWake(at, name, `echo xmrswap wake test`)
	if err != nil {
		t.Fatal(err)
	}
	defer UnregisterWake(name)
	t.Logf("registered: %s", what)
	if runtime.GOOS == "windows" {
		out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"$t = Get-ScheduledTask -TaskName "+psQuote(name)+"; 'WakeToRun=' + $t.Settings.WakeToRun; 'Start=' + $t.Triggers[0].StartBoundary; 'Run=' + $t.Actions[0].Execute + ' ' + $t.Actions[0].Arguments").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "WakeToRun=True") || !strings.Contains(string(out), at.Format("2006-01-02T15:04")) {
			t.Fatalf("the task as the scheduler has it: %v\n%s", err, out)
		}
		t.Logf("the scheduler has:\n%s", strings.TrimSpace(string(out)))
	}
	if err := UnregisterWake(name); err != nil {
		t.Fatal(err)
	}
	if err := UnregisterWake(name); err != nil {
		t.Fatalf("removing the wake a second time, when it is already gone: %v", err)
	}
	if runtime.GOOS == "windows" {
		if out, _ := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
			"if (Get-ScheduledTask -TaskName "+psQuote(name)+" -ErrorAction SilentlyContinue) { 'still there' } else { 'gone' }").CombinedOutput(); !strings.Contains(string(out), "gone") {
			t.Fatalf("after UnregisterWake the task is %s", out)
		}
	}
}
