package xmrswap

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTheMakersRecoveryCardComesBeforeHisLock (MT-B28): before any DERO of the maker's is locked his companion has
// printed his card — the state file named as a secret, the resume command, and every height of his as a clock time,
// his refund cutoff and t2 + g2 among them — and asked the OS to wake the machine, with that command, before t1, when
// his Refund window opens, and before t2 + g2, when his Reclaim does, under task names that are his own.
func TestTheMakersRecoveryCardComesBeforeHisLock(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 233})
	r.chain.flaky = nil
	start := time.Now()
	const resume = `xmrswap bob -state "swap dir/bob.json" -net stagenet`
	var woken []string
	var wakeAt []time.Time
	var atWake string
	locksAtWake := -1
	r.bob = startWith(r, "bob", func(c *Config) {
		c.ResumeCommand = resume
		c.Wake = func(at time.Time, name, command string) (string, error) {
			if locksAtWake < 0 {
				r.chain.mu.Lock()
				locksAtWake = len(r.chain.locks)
				r.chain.mu.Unlock()
				r.mu.Lock()
				atWake = strings.Join(r.logs, "\n")
				r.mu.Unlock()
			}
			wakeAt = append(wakeAt, at)
			woken = append(woken, name+"|"+command)
			return "a test wake at " + at.Format(time.RFC3339), nil
		}
	})
	r.alice = startWith(r, "alice", nil)
	r.until(400, r.bothDone, nil)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	if locksAtWake != 0 {
		t.Fatalf("the maker's first wake was registered with %d locks on the chain: his card comes before his Lock", locksAtWake)
	}
	u := func(v uint64) string { return strconv.FormatUint(v, 10) }
	for _, want := range []string{"RECOVERY CARD — read it before any DERO of yours is locked", "SECRET: " + r.statePath("bob"), "resume: " + resume,
		"XMRSWAP_CHANNEL_KEY", "t1             height " + u(bob.Lock.T1), "refund sent by height " + u(refundCutoff(bob.Lock.T2)-1),
		"t2             height " + u(bob.Lock.T2), "reclaim from   height " + u(bob.Lock.T2+testG2), "anyone may Punish", "only your Reclaim"} {
		if !strings.Contains(atWake, want) {
			t.Fatalf("the maker's card, printed before his Lock, lacks %q:\n%s", want, atWake)
		}
	}
	names := WakeTaskNames("bob", bob.SwapID)
	if len(woken) != 2 || woken[0] != names[0]+"|"+resume || woken[1] != names[1]+"|"+resume {
		t.Fatalf("wakes registered: %v, want %v", woken, names)
	}
	for _, a := range WakeTaskNames("alice", alice.SwapID) {
		for _, b := range names {
			if a == b {
				t.Fatalf("the maker's wake %s has the taker's name", b)
			}
		}
	}
	// before t1 and before t2 + g2, each counted from the height his windows were decided at, and no more than the
	// wake's lead early
	for i, blocks := range []uint64{bob.Lock.T1 - bob.Lock.IntentHeight, bob.Lock.T2 + testG2 - bob.Lock.IntentHeight} {
		due := start.Add(time.Duration(blocks) * deroBlock)
		if wakeAt[i].Before(due.Add(-wakeLead-time.Minute)) || !wakeAt[i].Before(due.Add(time.Minute)) {
			t.Fatalf("wake %d is set for %v, %d blocks from the decision are due at %v", i, wakeAt[i], blocks, due)
		}
	}
	if !wakeAt[1].After(wakeAt[0]) {
		t.Fatalf("the t2 + g2 wake (%v) is not after the t1 wake (%v)", wakeAt[1], wakeAt[0])
	}
	r.check()
}

// TestRegisterTheMakersWakesOnThisOS (MT-B28): the maker's two wakes, registered on this machine by his card as the
// command line registers them, found with their wake setting and times under his task names, and removed as endSwap
// removes them. It adds two tasks to the machine's scheduler for a moment, so it runs only with XMRSWAP_TEST_WAKE=1.
func TestRegisterTheMakersWakesOnThisOS(t *testing.T) {
	if os.Getenv("XMRSWAP_TEST_WAKE") != "1" {
		t.Skip("set XMRSWAP_TEST_WAKE=1 to register, find and remove the maker's two real wake tasks on this machine")
	}
	var id [6]byte
	_, _ = rand.Read(id[:])
	st := NewState(filepath.Join(t.TempDir(), "bob.json"), "bob", testHTLC)
	st.SwapID = "test-maker-" + hex.EncodeToString(id[:])
	st.Contract.G2 = 4800
	names := WakeTaskNames("bob", st.SwapID)
	for _, n := range names {
		defer UnregisterWake(n)
	}
	var logs []string
	s := &Swap{st: st, cfg: Config{ResumeCommand: `echo xmrswap maker wake test`, Wake: RegisterWake,
		Log: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }}.withDefaults()}
	// a day and more away, so that neither task could run while it exists
	const h = 1000
	t1, t2, now := uint64(h+4800), uint64(h+5200), time.Now()
	_, refundAt, reclaimAt := s.makerCard(t1, t2, h, now)
	s.printMakerCard(t1, t2, h, now)
	if n := strings.Count(strings.Join(logs, "\n"), "wake: the "); n != 2 {
		t.Fatalf("%d wakes registered:\n%s", n, strings.Join(logs, "\n"))
	}
	if runtime.GOOS == "windows" {
		for i, at := range []time.Time{refundAt, reclaimAt} {
			out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
				"$t = Get-ScheduledTask -TaskName "+psQuote(names[i])+"; 'WakeToRun=' + $t.Settings.WakeToRun; 'Start=' + $t.Triggers[0].StartBoundary").CombinedOutput()
			if err != nil || !strings.Contains(string(out), "WakeToRun=True") || !strings.Contains(string(out), at.Format("2006-01-02T15:04")) {
				t.Fatalf("the task %s as the scheduler has it: %v\n%s", names[i], err, out)
			}
			t.Logf("%s: %s", names[i], strings.Join(strings.Fields(string(out)), " "))
		}
	}
	for _, n := range names {
		if err := UnregisterWake(n); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS == "windows" {
		for _, n := range names {
			if out, _ := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
				"if (Get-ScheduledTask -TaskName "+psQuote(n)+" -ErrorAction SilentlyContinue) { 'still there' } else { 'gone' }").CombinedOutput(); !strings.Contains(string(out), "gone") {
				t.Fatalf("after UnregisterWake the task %s is %s", n, out)
			}
		}
	}
}

// TestTheRecoveryCardComesBeforeTheXmrLock (MT-B15, U9): at the moment alice's XMR transfer is made, her companion has
// already printed the card — the state file named as a secret, the resume command, every height as a clock time — and
// asked the OS to wake the machine, with that command, before the first window that closes.
func TestTheRecoveryCardComesBeforeTheXmrLock(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 231})
	start := time.Now()
	const resume = `xmrswap alice -state "swap dir/alice.json" -net stagenet`
	var woken []string
	var wakeAt []time.Time
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", func(c *Config) {
		c.ResumeCommand = resume
		c.Wake = func(at time.Time, name, command string) (string, error) {
			wakeAt = append(wakeAt, at)
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
	st := r.loadState("alice")
	for _, want := range []string{"RECOVERY CARD", "SECRET: " + r.statePath("alice"), "resume: " + resume,
		"claim cutoff", "gate deadline", "finish by", "t1 ", "t2 ", "wake: a test wake at", "XMRSWAP_CHANNEL_KEY",
		// MT-J13: her Punish window's end, as a height and a clock time, and what follows it — including that past it,
		// with nobody having punished, her XMR comes back only if bob Reclaims, and who else may send the Punish
		"punish sent by", "punish ends    height " + strconv.FormatUint(st.Lock.T2+testG2-1, 10), "Reclaim",
		"comes back only if bob Reclaims", "locked for ever", "someone you trust"} {
		if !strings.Contains(atTransfer, want) {
			t.Fatalf("the card printed before the XMR lock lacks %q:\n%s", want, atTransfer)
		}
	}
	if n := len(regexp.MustCompile(`height \d+\s+(about )?\d{4}-\d\d-\d\d \d\d:\d\d`).FindAllString(atTransfer, -1)); n < 8 {
		t.Fatalf("%d heights carry a clock time; the card has eight", n)
	}
	names := WakeTaskNames("alice", st.SwapID)
	if len(woken) != 2 || woken[0] != names[0]+"|"+resume || woken[1] != names[1]+"|"+resume {
		t.Fatalf("wakes registered: %v", woken)
	}
	// the first window to close is at most (t1 − the lock's first sight) blocks away, and the first wake comes before
	// it; the second comes before t2, when her Punish window opens
	for i, blocks := range []uint64{st.Lock.T1 - st.Lock.SeenHeight, st.Lock.T2 - st.Lock.SeenHeight} {
		latest := start.Add(time.Duration(blocks)*deroBlock + time.Minute)
		if !wakeAt[i].After(start) || !wakeAt[i].Before(latest) {
			t.Fatalf("wake %d is set for %v, outside (%v, %v)", i, wakeAt[i], start, latest)
		}
	}
	if !wakeAt[1].After(wakeAt[0]) {
		t.Fatalf("the t2 wake (%v) is not after the first (%v)", wakeAt[1], wakeAt[0])
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

// TestRemovingAWakeThatIsNotThereIsNoError (MT-B15): a finished swap removes its wakes, and one that ended before its
// card was printed removes wakes that were never there; that is no error. It only reads the scheduler. (In the
// command-line run of 2026-09-12 the maker, who then registered none, was told to delete a task that never existed:
// Unregister-ScheduledTask fails on a missing name.)
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
