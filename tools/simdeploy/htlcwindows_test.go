package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veil/tools/xmrswap"
)

// TestTheG2HelpSaysTheOperatorsAnswer: -htlc-g2 still has no default, and its help says what the operator answered on
// 2026-09-12 and what follows from it: the xmrswap taker's floor, DefaultG2Min, refuses any smaller install. It used to
// say G2 had not been chosen.
func TestTheG2HelpSaysTheOperatorsAnswer(t *testing.T) {
	f := flag.Lookup("htlc-g2")
	floor := fmt.Sprintf("%d,%03d", xmrswap.DefaultG2Min/1000, xmrswap.DefaultG2Min%1000)
	if f == nil || f.DefValue != "0" || !strings.Contains(f.Usage, floor) || !strings.Contains(f.Usage, "2026-09-12") ||
		!strings.Contains(f.Usage, "DefaultG2Min") || strings.Contains(f.Usage, "not chosen") {
		t.Fatalf("-htlc-g2 (default %q): %q; want no default and the answer, %s, with the taker's floor", f.DefValue, f.Usage, floor)
	}
}

// htlc.bas v3 takes w1min, w2min and g2 at install and refuses a missing or out-of-bounds one (MT-A03(a), MT-J13(b)),
// so an install that forgot them fails closed. simdeploy must send all three, must refuse before paying for anything a
// value the contract would refuse, and has no default for any of them: each install names its windows.
func TestHtlcWindowBoundsAreTheContracts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "contracts", "htlc.bas"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"Function Initialize(w1min Uint64, w2min Uint64, g2 Uint64) Uint64",
		"6 IF w1min == 0 || w2min == 0 || g2 == 0 THEN GOTO 900",
		"7 IF w1min > 1000000 || w2min > 1000000 || g2 > 1000000 || w1min + w2min > 999999 THEN GOTO 900",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("htlc.bas no longer says %q; update checkHtlcWindows and the install arguments to match", want)
		}
	}
	saved := [3]uint64{*htlcW1min, *htlcW2min, *htlcG2}
	defer func() { *htlcW1min, *htlcW2min, *htlcG2 = saved[0], saved[1], saved[2] }()
	set := func(w1, w2, g2 uint64) { *htlcW1min, *htlcW2min, *htlcG2 = w1, w2, g2 }
	set(0, 0, 0)
	if err := checkHtlcWindows(); err == nil || !strings.Contains(err.Error(), "-htlc-g2") {
		t.Errorf("an install naming none of the three was not refused: %v", err)
	}
	for _, bad := range [][3]uint64{{400, 400, 0}, {0, 400, 1200}, {400, 0, 1200}, {1000001, 1, 1}, {1, 1000001, 1}, {400, 400, 1000001},
		{500000, 500000, 1}, {1, 999999, 1}, {^uint64(0), 2, 1}, {2, ^uint64(0) - 1, 1}} {
		set(bad[0], bad[1], bad[2])
		if checkHtlcWindows() == nil {
			t.Errorf("-htlc-w1min %d -htlc-w2min %d -htlc-g2 %d was accepted; htlc.bas refuses it", bad[0], bad[1], bad[2])
		}
	}
	for _, good := range [][3]uint64{{1, 1, 1}, {400, 400, 1200}, {1, 999998, 1000000}, {499999, 500000, 1}} {
		set(good[0], good[1], good[2])
		if err := checkHtlcWindows(); err != nil {
			t.Errorf("-htlc-w1min %d -htlc-w2min %d -htlc-g2 %d was refused: %v", good[0], good[1], good[2], err)
		}
	}
}

// factory.bas v2 has no owner (MT-J15(b)), so -renounce explains that and exits: it sends nothing. The explanation
// names the way to leave an older factory, -migrate-from, and the contract it describes has no Renounce to call.
func TestRenounceExplainsThereIsNoOwner(t *testing.T) {
	saved := *renounce
	defer func() { *renounce = saved }()
	*renounce = false
	if err := checkRenounce(); err != nil {
		t.Fatalf("checkRenounce refused a run without -renounce: %v", err)
	}
	*renounce = true
	err := checkRenounce()
	if err == nil || !strings.Contains(err.Error(), "no owner") || !strings.Contains(err.Error(), "-migrate-from") {
		t.Fatalf("-renounce: %v", err)
	}
	b, rerr := os.ReadFile(filepath.Join("..", "..", "contracts", "factory.bas"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, gone := range []string{"Function Renounce", "Function SetTreasury", "Function SetRegFee", `STORE("owner"`} {
		if strings.Contains(string(b), gone) {
			t.Errorf("factory.bas has %q again: -renounce's explanation is no longer true", gone)
		}
	}
}
