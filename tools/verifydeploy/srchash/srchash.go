// Package srchash is the one definition of "the hash of a contract's source" that DMZ's release tooling uses.
//
// A contract is installed as reference.Canon(<file>) — tools/simdeploy's read() — so the hash a third party
// compares against the chain is sha256(reference.Canon(file)), never the hash of the file with its comments.
// tools/verifydeploy compares installed code against it and tools/release writes it into manifest.json; both
// import this package so the two can never disagree about what was hashed.
package srchash

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ref "veil/reference"
)

// Sum is the lower-case hex sha256 of s.
func Sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Canon is the code a contract source is installed as. It is reference.Canon and nothing else.
func Canon(src string) string { return ref.Canon(src) }

// ContractSum is sha256(reference.Canon(src)): the hash of the code simdeploy installs from src.
func ContractSum(src string) string { return Sum(ref.Canon(src)) }

// Line is one line of installed code and the line of the source file it came from.
type Line struct {
	N    int    // 1-based line number in the source file
	Text string // the line as installed
}

// CanonLines is Canon with provenance: the lines Canon keeps, each with its source line number. Joined with "\n"
// and terminated with "\n" it is exactly Canon(src) (TestCanonLinesIsCanon holds the two together).
func CanonLines(src string) []Line {
	var out []Line
	for i, l := range strings.Split(src, "\n") {
		l = strings.TrimSuffix(l, "\r")
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		out = append(out, Line{N: i + 1, Text: l})
	}
	return out
}

// Diff says where installed code first departs from the source.
type Diff struct {
	Index      int    // 1-based line of the installed code
	SourceLine int    // the source file's line for Index, 0 when the source has no line there
	Want       string // the source's line ("" with WantEOF)
	Got        string // the chain's line ("" with GotEOF)
	WantEOF    bool   // the source ended before the chain did
	GotEOF     bool   // the chain ended before the source did
}

// FirstDiff compares chain (the code DERO.GetSC returned) with Canon(src) line by line and returns the first
// difference, or nil when chain is exactly Canon(src). A lone trailing newline difference is reported as such,
// since the hashes differ and a user must be told why.
func FirstDiff(src, chain string) *Diff {
	want := CanonLines(src)
	got := strings.Split(chain, "\n")
	if n := len(got); n > 0 && got[n-1] == "" {
		got = got[:n-1] // the terminating newline, which Canon always writes
	}
	for i := 0; i < len(want) || i < len(got); i++ {
		d := &Diff{Index: i + 1}
		switch {
		case i >= len(want):
			d.WantEOF, d.Got = true, got[i]
			return d
		case i >= len(got):
			d.GotEOF, d.Want, d.SourceLine = true, want[i].Text, want[i].N
			return d
		case want[i].Text != got[i]:
			d.Want, d.Got, d.SourceLine = want[i].Text, got[i], want[i].N
			return d
		}
	}
	if ref.Canon(src) != chain {
		// Same lines, different bytes: only the final newline (or a CR the split hid) can differ now.
		return &Diff{Index: len(want) + 1, WantEOF: true, GotEOF: true}
	}
	return nil
}

// Contract is one contracts/*.bas file and its two hashes.
type Contract struct {
	File         string `json:"file"`         // base name, e.g. "factory.bas"
	SourceSha256 string `json:"sourceSha256"` // sha256 of the file as it is in the repository, comments included
	CanonSha256  string `json:"canonSha256"`  // sha256 of reference.Canon(file): what is installed and verified
}

// Contracts reads every *.bas directly under dir, sorted by name.
func Contracts(dir string) ([]Contract, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.bas"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	out := make([]Contract, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, Contract{File: filepath.Base(p), SourceSha256: Sum(string(b)), CanonSha256: ContractSum(string(b))})
	}
	return out, nil
}
