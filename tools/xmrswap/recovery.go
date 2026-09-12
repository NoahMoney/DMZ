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
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// WakeFunc asks the operating system to wake this machine at `at` and run command, as a task called name. It returns
// what it registered, in words, or why nothing could be.
type WakeFunc func(at time.Time, name, command string) (string, error)

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
	resume := s.cfg.ResumeCommand
	if resume == "" {
		resume = "the same companion command again, on the same state file"
	}
	return []string{
		"resume: " + resume,
		"        it reads VEIL_RPC_LOGIN, XMRSWAP_MONERO_LOGIN and XMRSWAP_CHANNEL_KEY from the environment; their values are written nowhere, the wake task included",
	}
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
		what, err := s.cfg.Wake(w.at, names[i], s.cfg.ResumeCommand)
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
func RegisterWake(at time.Time, name, command string) (string, error) {
	return registerWake(runtime.GOOS, at, name, command)
}

// UnregisterWake removes a wake RegisterWake made; one that is not there is not an error.
func UnregisterWake(name string) error {
	switch runtime.GOOS {
	case "windows":
		// Only a task that is there is removed: Unregister-ScheduledTask fails on a name it does not find, whatever
		// -ErrorAction says, and a swap that ended before its card was printed (refused at the offer, say) would be
		// told to delete tasks that never existed.
		// The trailing `exit 0` is needed because Get-ScheduledTask's own suppressed "not found" error leaves
		// powershell.exe with exit 1 even when the removal is skipped; a real removal failure still throws first
		// (-ErrorAction Stop), so it never reaches the exit and is reported.
		script := fmt.Sprintf("$t = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue; if ($t) { $t | Unregister-ScheduledTask -Confirm:$false -ErrorAction Stop }; exit 0", psQuote(name))
		if out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput(); err != nil {
			return fmt.Errorf("Unregister-ScheduledTask: %v: %s", err, strings.TrimSpace(string(out)))
		}
	case "linux":
		_ = exec.Command("systemctl", "--user", "stop", name+".timer").Run()
	}
	return nil
}

// registerWake is RegisterWake for a named OS.
//
//   - windows: a Task Scheduler task for this user (Register-ScheduledTask, no elevation), triggered once at `at` and
//     set to WakeToRun, which starts the resume command in a console. The power plan must allow wake timers.
//   - linux: a systemd user timer (systemd-run --user --on-calendar) with WakeSystem=yes. Registered, found and removed
//     inside WSL's systemd 255 on 2026-09-12 (X-23); stopping the transient timer unloads it and its service. Where
//     there is no systemd-run, nothing is registered and the reason is returned.
//   - darwin: nothing. macOS has no unprivileged wake from sleep: `pmset schedule wake` needs root, and a launchd
//     agent's calendar trigger does not wake a sleeping Mac. The companion does not ask for root.
//   - anything else: nothing, and the reason.
func registerWake(goos string, at time.Time, name, command string) (string, error) {
	if command == "" {
		return "", errors.New("there is no resume command to run")
	}
	switch goos {
	case "windows":
		script := fmt.Sprintf("$ErrorActionPreference = 'Stop'\n"+
			"$a = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument %s\n"+
			"$t = New-ScheduledTaskTrigger -Once -At ([datetime]::ParseExact(%s, 'yyyy-MM-dd HH:mm:ss', [Globalization.CultureInfo]::InvariantCulture))\n"+
			"$s = New-ScheduledTaskSettingsSet -WakeToRun -StartWhenAvailable\n"+
			"Register-ScheduledTask -TaskName %s -Action $a -Trigger $t -Settings $s -Description %s -Force | Out-Null",
			psQuote("/k "+command), psQuote(at.Format("2006-01-02 15:04:05")), psQuote(name),
			psQuote("xmrswap: resume the swap before its first window closes"))
		if out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput(); err != nil {
			return "", fmt.Errorf("Register-ScheduledTask: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("the Windows task %q starts the resume command in a console at %s and is set to wake this computer (WakeToRun: the power plan must allow wake timers)", name, at.Format("2006-01-02 15:04 MST")), nil
	case "linux":
		if _, err := exec.LookPath("systemd-run"); err != nil {
			return "", errors.New("this system has no systemd-run, so no user-level wake can be registered: use at(1), cron or a reminder")
		}
		out, err := exec.Command("systemd-run", "--user", "--unit="+name, "--on-calendar="+at.Format("2006-01-02 15:04:05"),
			"--timer-property=WakeSystem=yes", "/bin/sh", "-c", command).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("systemd-run: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return fmt.Sprintf("the systemd user timer %s.timer runs the resume command at %s, set to wake the system (WakeSystem=yes)", name, at.Format("2006-01-02 15:04 MST")), nil
	case "darwin":
		return "", errors.New("macOS offers no unprivileged wake from sleep (pmset schedule wake needs root, and a launchd agent's calendar trigger does not wake a sleeping Mac), and the companion does not ask for root: schedule a wake in System Settings, or set a reminder")
	}
	return "", fmt.Errorf("the companion knows no wake registration on %s: set a reminder", goos)
}

// psQuote is a PowerShell single-quoted string.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
