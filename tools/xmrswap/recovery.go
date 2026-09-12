package xmrswap

// U9 / MT-B15: the taker's recovery card, printed before her XMR moves, and wakes registered with the operating system
// for the first window that closes and for t2. Once s_b is on the chain — after a Refund, or the maker's Reclaim after
// t2 + g2 — her XMR waits for her alone, with no deadline. The swap has deadlines: a gate that reaches her after t1 − G
// is not used, no Claim of hers is sent after the claim cutoff, and since htlc.bas v3 her Punish has one, t2 + g2
// (MT-J13). Past it, if nobody punished, her XMR comes back only if the maker Reclaims; one who never does — unwilling,
// or having lost his state or s_b — leaves it, and his DERO, locked for ever, and only a timely Punish (hers, or anyone's
// for her) rules that out. The card says so, says what to keep secret and what to run, and gives every window's opening
// and close in clock time; the wakes bring the machine back, and the resume command up, before the first of them closes
// and as her Punish window opens.
//
// MT-B28: the maker's card, printed before his DERO is locked, and his two wakes, by the same mechanism: before t1, when
// his Refund window opens (he must refund before his refund cutoff, t2 − DeroTxValidity, or leave the lock to anyone's
// Punish), and before t2 + g2, when, nobody having punished, only his Reclaim returns his DERO and the taker's XMR.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// WakeFunc asks the operating system to wake this machine at `at` and start the companion as r says, as a task called
// name. It returns what it registered, in words, or why nothing could be.
type WakeFunc func(at time.Time, name string, r Resume) (string, error)

// Resume is how this companion is started again (U9, MT-B15): its executable, the arguments it was started with, and the
// directory it ran in, which a relative -state or -channel is relative to. The recovery card prints it for a person
// (CommandLine); the wake registers it with the operating system as a program, its arguments and its directory, never as
// a line for a shell to split again (the review of 2026-09-12, finding 3).
type Resume struct {
	Exe  string
	Args []string
	Dir  string
}

// CommandLine is the command as the recovery card prints it for a person to type, each word quoted where this OS's
// command line would split it.
func (r Resume) CommandLine() string { return r.commandLine(runtime.GOOS) }

func (r Resume) commandLine(goos string) string {
	word := shellWord
	if goos == "windows" {
		word = func(s string) string { return windowsArg(s, s == "" || strings.ContainsAny(s, " \t\"&|<>^%!()")) }
	}
	parts := []string{word(r.Exe)}
	for _, a := range r.Args {
		parts = append(parts, word(a))
	}
	return strings.Join(parts, " ")
}

// deroBlock is DERO's target block time: every height on the card is turned into a clock time at this rate.
const deroBlock = 18 * time.Second

// wakeLead is how long before the first window's close the wake is set.
const wakeLead = 10 * time.Minute

// clockAt turns a height into the clock time it is expected at, counting deroBlock a block from height h at now.
func clockAt(h uint64, now time.Time) (at func(uint64) time.Time, clock func(uint64) string) {
	at = func(height uint64) time.Time {
		if height <= h {
			return now
		}
		return now.Add(time.Duration(height-h) * deroBlock)
	}
	return at, func(height uint64) string { return at(height).Format("2006-01-02 15:04 MST") }
}

// resumeLines are the card's lines on how to come back: the command, and where its secrets come from.
func (s *Swap) resumeLines() []string {
	r := s.cfg.Resume
	lines := []string{"resume: the same companion command again, on the same state file"}
	if r.Exe != "" {
		lines = []string{"resume: " + r.CommandLine()}
		if r.Dir != "" {
			lines = append(lines, "        in the directory "+r.Dir+", which its relative paths are relative to")
		}
	}
	return append(lines, "        it reads VEIL_RPC_LOGIN, XMRSWAP_MONERO_LOGIN and XMRSWAP_CHANNEL_KEY from the environment; their values are written nowhere, the wake task included")
}

// wakeAtLead is a wake wakeLead before t, and never earlier than a minute from now.
func wakeAtLead(t, now time.Time) time.Time {
	w := t.Add(-wakeLead)
	if early := now.Add(time.Minute); w.Before(early) {
		return early
	}
	return w
}

// recoveryCard is the card's lines, and the times the two wakes should be set for: before the first window closes,
// and before t2, when her Punish window opens.
func (s *Swap) recoveryCard(l Lock, h uint64, now time.Time) (lines []string, wakeAt, punishWakeAt time.Time) {
	at, clock := clockAt(h, now)
	cutoff := claimCutoff(l.T1, s.cfg.ClaimMargin)
	gate := deadlineOf(l.T1, s.cfg.GateDeadline)
	finish := cutoff
	if gate < finish {
		finish = gate
	}
	lines = []string{
		"RECOVERY CARD — read it before any XMR of yours moves",
		fmt.Sprintf("SECRET: %s holds your key share and the joint wallets' password. Back it up now, where only you can read it: without it, XMR locked now cannot be recovered after a refund, and whoever copies it can take that XMR once s_b is public.", s.st.Path()),
	}
	lines = append(lines, s.resumeLines()...)
	lines = append(lines,
		fmt.Sprintf("now            height %d  %s", h, now.Format("2006-01-02 15:04 MST")),
		fmt.Sprintf("claim cutoff   height %d  about %s  no Claim of yours is sent from here", cutoff, clock(cutoff)),
		fmt.Sprintf("gate deadline  height %d  about %s  a gate reaching you from here is not used (t1 − G)", gate, clock(gate)),
		fmt.Sprintf("finish by      height %d  about %s  be running before this to complete the swap", finish, clock(finish)),
		fmt.Sprintf("t1             height %d  about %s  bob may refund from here; his refund puts s_b on the chain, and your XMR comes back with it", l.T1, clock(l.T1)),
	)
	g2 := s.st.Contract.G2
	pcut := punishCutoff(l.T2, g2, s.cfg.ClaimMargin)
	lastSend := l.T2
	if pcut > l.T2 {
		lastSend = pcut - 1
	}
	lines = append(lines,
		fmt.Sprintf("t2             height %d  about %s  bob can no longer refund; your companion sends Punish at once, and anyone may send it for you: it carries no secret and pays only you", l.T2, clock(l.T2)),
		fmt.Sprintf("punish sent by height %d  about %s  the last height your companion sends Punish at (t2 + g2 − %d), so that it is stable before the window closes", lastSend, clock(lastSend), DeroTxValidity+DefaultStableBlocks+s.cfg.ClaimMargin+1),
		fmt.Sprintf("punish ends    height %d  about %s  the Punish window's last block (g2 = %d): from t2 + g2 the contract refuses Punish and the lock is bob's to Reclaim", l.T2+g2-1, clock(l.T2+g2-1), g2),
		"your XMR has no deadline once s_b is on the chain: bob's refund puts it there, and so does his Reclaim after t2 + g2, and the XMR then comes back to you alone; a Punish in [t2, t2 + g2) pays you the DERO instead, and any XMR you locked then stays under the joint key",
		"if bob neither releases the gate nor refunds, and nobody sends Punish in [t2, t2 + g2), your XMR comes back only if bob Reclaims: a bob who never does — unwilling, or having lost his state or s_b — leaves your XMR and his DERO locked for ever. Only a Punish before t2 + g2 rules that out: be running then, or have someone you trust send it for you (anyone may; it carries no secret and pays only you)",
	)
	return lines, wakeAtLead(at(finish), now), wakeAtLead(at(l.T2), now)
}

// printRecoveryCard logs the taker's card and registers her two wakes.
func (s *Swap) printRecoveryCard(l Lock, h uint64, now time.Time) {
	lines, wakeAt, punishWakeAt := s.recoveryCard(l, h, now)
	s.announce(lines, []cardWake{{wakeAt, "the first window closes"}, {punishWakeAt, "t2, when the Punish window opens"}})
}

// makerCard is the maker's recovery card (MT-B28) for a lock with t1 and t2 decided at height h, and the times his two
// wakes should be set for: before t1, when his Refund window opens, and before t2 + g2, when his Reclaim does.
func (s *Swap) makerCard(t1, t2, h uint64, now time.Time) (lines []string, refundWakeAt, reclaimWakeAt time.Time) {
	at, clock := clockAt(h, now)
	g2 := s.st.Contract.G2
	gate := deadlineOf(t1, s.cfg.GateMargin)
	rcut := refundCutoff(t2)
	lastRefund := t1
	if rcut > t1 {
		lastRefund = rcut - 1
	}
	lines = []string{
		"RECOVERY CARD — read it before any DERO of yours is locked",
		fmt.Sprintf("SECRET: %s holds your key share s_b and the gate secret. Back it up now, where only you can read it: without it the DERO you lock now can be neither refunded nor reclaimed, and whoever copies it can take the XMR once alice's Claim puts s_a on the chain.", s.st.Path()),
	}
	lines = append(lines, s.resumeLines()...)
	lines = append(lines,
		fmt.Sprintf("now            height %d  %s", h, now.Format("2006-01-02 15:04 MST")),
		fmt.Sprintf("gate by        height %d  about %s  your companion releases the gate only before this; from here it waits for t1 to refund", gate, clock(gate)),
		fmt.Sprintf("t1             height %d  about %s  your Refund window opens: if alice has not claimed, your companion refunds, which returns your DERO and puts s_b on the chain for her", t1, clock(t1)),
		fmt.Sprintf("refund sent by height %d  about %s  the last height your companion sends a Refund at (t2 − %d), so that it executes before t2", lastRefund, clock(lastRefund), DeroTxValidity+1),
		fmt.Sprintf("t2             height %d  about %s  from here until t2 + g2 anyone may Punish the lock, which pays alice your DERO; your companion sends none", t2, clock(t2)),
		fmt.Sprintf("reclaim from   height %d  about %s  t2 + g2 (g2 = %d): if nobody punished, your companion Reclaims from here, which returns your DERO and puts s_b on the chain; until it does, alice's XMR waits on it, and nothing else returns either", t2+g2, clock(t2+g2), g2),
		"if alice claims, the XMR at the joint address is yours with no deadline: s_a stays on the chain, and your companion sweeps it once the Claim is stable",
		"be running from t1 until your Refund is stable: a Refund not sent by the height above leaves your DERO to anyone's Punish in [t2, t2 + g2), and if nobody punishes, only your Reclaim from t2 + g2 returns it — be running then too, which the second wake is for",
	)
	return lines, wakeAtLead(at(t1), now), wakeAtLead(at(t2+g2), now)
}

// printMakerCard logs the maker's card and registers his two wakes (MT-B28).
func (s *Swap) printMakerCard(t1, t2, h uint64, now time.Time) {
	lines, refundWakeAt, reclaimWakeAt := s.makerCard(t1, t2, h, now)
	s.announce(lines, []cardWake{{refundWakeAt, "t1, when your Refund window opens"}, {reclaimWakeAt, "t2 + g2, when your Reclaim opens"}})
}

// cardWake is one wake a card asks for: when, and before what.
type cardWake struct {
	at   time.Time
	what string
}

// announce logs a card and registers its wakes under this role's task names (WakeTaskNames). A wake that cannot be
// registered is said, loudly, and does not hold the swap: the card is what the operator needs, and a wake only saves
// them remembering it.
func (s *Swap) announce(lines []string, wakes []cardWake) {
	for _, line := range lines {
		s.cfg.Log("%s: %s", s.st.Role, line)
	}
	names := WakeTaskNames(s.st.Role, s.st.SwapID)
	for i, w := range wakes {
		if s.cfg.Wake == nil {
			s.cfg.Log("%s: wake: none registered (this companion runs without an OS wake hook): be back by %s yourself, before %s", s.st.Role, w.at.Format("2006-01-02 15:04 MST"), w.what)
			continue
		}
		what, err := s.cfg.Wake(w.at, names[i], s.cfg.Resume)
		if err != nil {
			s.cfg.Log("%s: wake: NOT registered (%v): be back by %s yourself, before %s", s.st.Role, err, w.at.Format("2006-01-02 15:04 MST"), w.what)
			continue
		}
		s.cfg.Log("%s: wake: %s (before %s)", s.st.Role, what, w.what)
	}
}

// WakeTaskName is the OS task a swap's first wake is registered under.
func WakeTaskName(swapID string) string {
	var b strings.Builder
	for _, r := range swapID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	return "xmrswap-" + b.String()
}

// WakeTaskNames are both of one party's wakes for a swap, in the order its card registers them: the taker's before her
// first window closes and before t2 (MT-J13); the maker's before t1 and before t2 + g2 (MT-B28). Each party has its own
// swap id, so the two never share a name. A finished swap removes both.
func WakeTaskNames(role, swapID string) []string {
	if role == "bob" {
		return []string{WakeTaskName(swapID), WakeTaskName(swapID) + "-reclaim"}
	}
	return []string{WakeTaskName(swapID), WakeTaskName(swapID) + "-t2"}
}

// RegisterWake is the WakeFunc the command line uses, for the operating system it runs on.
func RegisterWake(at time.Time, name string, r Resume) (string, error) {
	return registerWake(runtime.GOOS, at, name, r)
}

// UnregisterWake removes a wake RegisterWake made; one that is not there is not an error.
func UnregisterWake(name string) error {
	switch runtime.GOOS {
	case "windows":
		ps, err := systemPowerShell()
		if err != nil {
			return err
		}
		cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", unregisterTaskScript)
		cmd.Env = append(withoutWakeEnv(os.Environ()), "XMRSWAP_WAKE_NAME="+name)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("Unregister-ScheduledTask: %v: %s", err, strings.TrimSpace(string(out)))
		}
	case "linux":
		_ = exec.Command("systemctl", "--user", "stop", name+".timer").Run()
	}
	return nil
}

// unregisterTaskScript removes the task named in XMRSWAP_WAKE_NAME. Only a task that is there is removed:
// Unregister-ScheduledTask fails on a name it does not find, whatever -ErrorAction says, and a swap that ended before
// its card was printed (refused at the offer, say) would be told to delete tasks that never existed. The trailing
// `exit 0` is needed because Get-ScheduledTask's own suppressed "not found" error leaves powershell.exe with exit 1 even
// when the removal is skipped; a real removal failure still throws first (-ErrorAction Stop), so it never reaches the
// exit and is reported.
const unregisterTaskScript = `$t = Get-ScheduledTask -TaskName $env:XMRSWAP_WAKE_NAME -ErrorAction SilentlyContinue; if ($t) { $t | Unregister-ScheduledTask -Confirm:$false -ErrorAction Stop }; exit 0`

// registerTaskScript registers the wake from values it reads from its environment (windowsWakeEnv): nothing the task
// runs is written into the script, so no value is ever read as PowerShell. The task's action is the companion itself,
// with its arguments and directory as separate fields, not cmd.exe.
const registerTaskScript = `$ErrorActionPreference = 'Stop'
$p = @{ Execute = $env:XMRSWAP_WAKE_EXE; WorkingDirectory = $env:XMRSWAP_WAKE_DIR }
if ($env:XMRSWAP_WAKE_ARGS) { $p.Argument = $env:XMRSWAP_WAKE_ARGS }
$a = New-ScheduledTaskAction @p
$t = New-ScheduledTaskTrigger -Once -At ([datetime]::ParseExact($env:XMRSWAP_WAKE_AT, 'yyyy-MM-dd HH:mm:ss', [Globalization.CultureInfo]::InvariantCulture))
$s = New-ScheduledTaskSettingsSet -WakeToRun -StartWhenAvailable
Register-ScheduledTask -TaskName $env:XMRSWAP_WAKE_NAME -Action $a -Trigger $t -Settings $s -Description 'xmrswap: resume the swap before its next window closes' -Force | Out-Null`

// registerWake is RegisterWake for a named OS. Neither registration composes a line for a shell: the review of
// 2026-09-12 (finding 3) found the Windows wake running `cmd.exe /k <resume command>`, and cmd /k strips the first and
// the last quote of such a line and splits it at the space, so with a space in the companion's path and another
// argument quoted it ran a program planted beside the companion's directory (`…\my dir\app.bat` became `…\my.bat`).
//
//   - windows: a Task Scheduler task for this user (Register-ScheduledTask, no elevation), triggered once at `at` and
//     set to WakeToRun. Its action's program is the companion's .exe, quoted as one path; its arguments are one
//     command line written for the companion's own parser, which reads them back as they were given (windowsArgs);
//     its working directory is the one the companion ran in. The Task Scheduler expands %VARIABLE% in all three, so
//     a wake whose values hold a % is refused rather than run with other ones (windowsWakeEnv). The companion opens in
//     a console of its own. The power plan must allow wake timers.
//   - linux: a systemd user timer (systemd-run --user --on-calendar) with WakeSystem=yes, whose service runs the
//     companion's executable with its arguments as an argv, with no shell, and --expand-environment=no, so systemd
//     expands no $VARIABLE in them either (systemd 254 or later; an older systemd-run refuses the flag, and then no wake
//     is registered and the reason is said). Registered, found and removed inside WSL's systemd 255 on 2026-09-12
//     (X-23); stopping the transient timer unloads it and its service. Where there is no systemd-run, nothing is
//     registered and the reason is returned.
//   - darwin: nothing. macOS has no unprivileged wake from sleep: `pmset schedule wake` needs root, and a launchd
//     agent's calendar trigger does not wake a sleeping Mac. The companion does not ask for root.
//   - anything else: nothing, and the reason.
func registerWake(goos string, at time.Time, name string, r Resume) (string, error) {
	if r.Exe == "" {
		return "", errors.New("there is no resume command to run")
	}
	switch goos {
	case "windows":
		env, err := windowsWakeEnv(at, name, r)
		if err != nil {
			return "", err
		}
		ps, err := systemPowerShell()
		if err != nil {
			return "", err
		}
		cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", registerTaskScript)
		cmd.Env = append(withoutWakeEnv(os.Environ()), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("Register-ScheduledTask: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("the Windows task %q starts %s in a console of its own at %s and is set to wake this computer (WakeToRun: the power plan must allow wake timers)", name, r.Exe, at.Format("2006-01-02 15:04 MST")), nil
	case "linux":
		args, err := linuxWakeArgs(at, name, r)
		if err != nil {
			return "", err
		}
		if _, err := exec.LookPath("systemd-run"); err != nil {
			return "", errors.New("this system has no systemd-run, so no user-level wake can be registered: use at(1), cron or a reminder")
		}
		out, err := exec.Command("systemd-run", args...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("systemd-run: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("the systemd user timer %s.timer runs %s at %s, set to wake the system (WakeSystem=yes)", name, r.Exe, at.Format("2006-01-02 15:04 MST")), nil
	case "darwin":
		return "", errors.New("macOS offers no unprivileged wake from sleep (pmset schedule wake needs root, and a launchd agent's calendar trigger does not wake a sleeping Mac), and the companion does not ask for root: schedule a wake in System Settings, or set a reminder")
	}
	return "", fmt.Errorf("the companion knows no wake registration on %s: set a reminder", goos)
}

// wakeValuesRefusal is why a resume cannot be registered as it is, or "": its directory is needed, since a relative
// -state would otherwise name another file, and no value may hold a control character (a tab is taken).
func wakeValuesRefusal(r Resume) string {
	if r.Dir == "" {
		return "the directory the companion ran in is not known, and its relative paths would name other files from the task's"
	}
	for _, v := range append([]string{r.Exe, r.Dir}, r.Args...) {
		if strings.IndexFunc(v, func(c rune) bool { return (c < 0x20 && c != '\t') || c == 0x7f }) >= 0 {
			return "an argument holds a control character"
		}
	}
	return ""
}

// windowsWakeEnv is what the task's registration is given, in the environment of the powershell.exe that makes it and
// never in its script: the program, quoted as one path; the arguments, as one command line the companion's own parser
// reads back into exactly what it was given; the directory; the time; the task's name. It refuses a resume the Task
// Scheduler would not start as written:
//   - a program that is not an absolute path to an .exe: Windows starts a .bat or .cmd through cmd.exe, which splits and
//     expands its arguments again, the very thing this is for;
//   - a %, anywhere: the Task Scheduler expands %VARIABLE% in a task's program, arguments and directory. The value is not
//     repeated in the refusal, since an argument may be a secret.
func windowsWakeEnv(at time.Time, name string, r Resume) ([]string, error) {
	switch {
	case !windowsAbs(r.Exe):
		return nil, fmt.Errorf("the companion's path %q is not an absolute Windows path, so the task could start another program", r.Exe)
	case !strings.HasSuffix(strings.ToLower(r.Exe), ".exe"):
		return nil, fmt.Errorf("the companion %s is not an .exe: Windows starts a .bat or .cmd through cmd.exe, which would split and expand its arguments again", r.Exe)
	case r.Dir != "" && !windowsAbs(r.Dir):
		return nil, fmt.Errorf("the companion's directory %q is not an absolute Windows path", r.Dir)
	}
	if why := wakeValuesRefusal(r); why != "" {
		return nil, errors.New(why + ": no wake is registered")
	}
	for _, v := range append([]string{r.Exe, r.Dir}, r.Args...) {
		if strings.Contains(v, "%") {
			return nil, errors.New("the companion's path, directory or an argument holds a %, and the Task Scheduler expands %VARIABLE% in each of them, so the companion would not be started as it was: no wake is registered")
		}
	}
	return []string{
		"XMRSWAP_WAKE_EXE=" + `"` + r.Exe + `"`,
		"XMRSWAP_WAKE_ARGS=" + windowsArgs(r.Args),
		"XMRSWAP_WAKE_DIR=" + r.Dir,
		"XMRSWAP_WAKE_AT=" + at.Format("2006-01-02 15:04:05"),
		"XMRSWAP_WAKE_NAME=" + name,
	}, nil
}

// linuxWakeArgs is systemd-run's command line: a user timer at `at` whose service runs the companion's executable with
// its arguments as an argv, in its directory, with no shell and with systemd's own $VARIABLE expansion off.
func linuxWakeArgs(at time.Time, name string, r Resume) ([]string, error) {
	switch {
	case !strings.HasPrefix(r.Exe, "/"):
		return nil, fmt.Errorf("the companion's path %q is not absolute", r.Exe)
	case r.Dir != "" && !strings.HasPrefix(r.Dir, "/"):
		return nil, fmt.Errorf("the companion's directory %q is not absolute", r.Dir)
	}
	if why := wakeValuesRefusal(r); why != "" {
		return nil, errors.New(why + ": no wake is registered")
	}
	args := []string{"--user", "--unit=" + name, "--on-calendar=" + at.Format("2006-01-02 15:04:05"), "--timer-property=WakeSystem=yes",
		"--expand-environment=no", "--working-directory=" + r.Dir, "--", r.Exe}
	return append(args, r.Args...), nil
}

// withoutWakeEnv is env without any XMRSWAP_WAKE_ variable, so that only the values set for a registration reach it.
func withoutWakeEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(strings.ToUpper(kv), "XMRSWAP_WAKE_") {
			out = append(out, kv)
		}
	}
	return out
}

// systemPowerShell is Windows' own powershell.exe, under the directory GetSystemDirectoryW names: never one that PATH
// finds, since it is handed the companion's command.
func systemPowerShell() (string, error) {
	dir, err := systemDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe")
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("there is no powershell.exe at %s", p)
	}
	return p, nil
}

// windowsAbs says whether p is an absolute Windows path: a drive letter and a separator, or a UNC path.
func windowsAbs(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z') && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// windowsArgs is args as the arguments of one Windows command line, each written by windowsArg.
func windowsArgs(args []string) string {
	words := make([]string, len(args))
	for i, a := range args {
		words[i] = windowsArg(a, false)
	}
	return strings.Join(words, " ")
}

// windowsArg is s as one argument of a Windows command line, as CommandLineToArgvW and Go's own os.Args read one back
// (syscall.EscapeArg's rules): quoted when it is empty or holds a space or a tab, or when force is set, with each quote
// escaped and the backslashes before a quote, and before the closing quote, doubled. No other character means anything
// to that parser, so nothing else is escaped.
func windowsArg(s string, force bool) string {
	quote := force || s == "" || strings.ContainsAny(s, " \t")
	if !quote && !strings.Contains(s, `"`) {
		return s
	}
	var b strings.Builder
	if quote {
		b.WriteByte('"')
	}
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			for ; slashes > 0; slashes-- {
				b.WriteByte('\\')
			}
			b.WriteByte('\\')
		default:
			slashes = 0
		}
		b.WriteByte(c)
	}
	if quote {
		for ; slashes > 0; slashes-- {
			b.WriteByte('\\')
		}
		b.WriteByte('"')
	}
	return b.String()
}

// shellWord is s as one word a POSIX shell reads back unchanged: as it is when it holds nothing a shell treats
// specially, and single-quoted otherwise.
func shellWord(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
