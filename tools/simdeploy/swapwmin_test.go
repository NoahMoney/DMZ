package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// swap.bas v2 takes its minimum escrow width at install and refuses 0 (Z-D, GAP-025), so an install that forgot it
// fails closed. simdeploy must send it, and must refuse a value the contract would refuse before paying for anything.
func TestSwapWminBoundsAreTheContracts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "contracts", "swap.bas"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{"Function Initialize(wmin Uint64) Uint64", "wmin == 0 || wmin > 1000000"} {
		if !strings.Contains(src, want) {
			t.Errorf("swap.bas no longer says %q; update checkSwapWmin and the install arguments to match", want)
		}
	}
	if err := checkSwapWmin(); err != nil {
		t.Errorf("the default -swap-wmin is refused: %v", err)
	}
	saved := *swapWmin
	defer func() { *swapWmin = saved }()
	for _, bad := range []uint64{0, 1000001} {
		*swapWmin = bad
		if checkSwapWmin() == nil {
			t.Errorf("-swap-wmin %d was accepted; swap.bas refuses it", bad)
		}
	}
	for _, good := range []uint64{1, 1600, 1000000} {
		*swapWmin = good
		if err := checkSwapWmin(); err != nil {
			t.Errorf("-swap-wmin %d was refused: %v", good, err)
		}
	}
}
