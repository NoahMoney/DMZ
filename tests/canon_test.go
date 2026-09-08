package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ref "github.com/deroproject/derohe/veil/reference"
)

// Canon decides what bytes reach the chain and what the shipped template hash covers, so it has to be provably
// incapable of changing a contract. It may only drop whole-line comments and blank lines: every other line must
// survive byte for byte, in order.
func TestCanonOnlyDropsCommentsAndBlanks(t *testing.T) {
	files, err := filepath.Glob("../contracts/*.bas")
	if err != nil || len(files) == 0 {
		t.Fatalf("no contracts: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		got := strings.Split(strings.TrimSuffix(ref.Canon(src), "\n"), "\n")
		var want []string
		for _, l := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
			if tl := strings.TrimSpace(l); tl != "" && !strings.HasPrefix(tl, "//") {
				want = append(want, l)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: kept %d lines, expected %d", f, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s line %d altered:\n  in:  %q\n  out: %q", f, i, want[i], got[i])
			}
		}
		if ref.Canon(ref.Canon(src)) != ref.Canon(src) {
			t.Fatalf("%s: Canon is not idempotent", f)
		}
		// Nothing executable may go missing: every numbered statement in the source is in the output.
		for _, l := range strings.Split(src, "\n") {
			tl := strings.TrimSpace(l)
			if tl == "" || tl[0] < '0' || tl[0] > '9' {
				continue
			}
			if !strings.Contains(ref.Canon(src), tl) {
				t.Fatalf("%s: dropped a statement: %q", f, tl)
			}
		}
	}
}

// Comments are not free: they are charged as install storage gas against a 20,000-unit cap, which is why they
// are stripped. This records what that is worth, and fails if a contract ever grows past the cap in the form
// that is actually installed.
func TestEveryContractFitsTheInstallGasCapAsInstalled(t *testing.T) {
	files, _ := filepath.Glob("../contracts/*.bas")
	for _, f := range files {
		b, _ := os.ReadFile(f)
		raw, canon := len(b), len(ref.Canon(string(b)))
		t.Logf("%-14s %6d bytes -> %6d installed (%d bytes of comment and blank line not charged)",
			filepath.Base(f), raw, canon, raw-canon)
		if canon > 20000 {
			t.Fatalf("%s: %d bytes cannot fit the 20,000-unit install gas cap in any form", f, canon)
		}
	}
}
