package srchash

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ref "veil/reference"
)

const contractsDir = "../../../contracts"

// CanonLines must be Canon with line numbers and nothing else, or a reported "first differing line" could point
// at a line the chain never held.
func TestCanonLinesIsCanon(t *testing.T) {
	inputs := []string{
		"",
		"\n\n",
		"// only a comment\n",
		"Function F() Uint64\n10 RETURN 0\nEnd Function\n",
		"Function F() Uint64\r\n\r\n  // indented comment\r\n10 RETURN 0 // trailing comments are kept\r\nEnd Function",
		"   \t\n10 STORE(\"a\", \"//not a comment\")\n",
	}
	paths, _ := filepath.Glob(filepath.Join(contractsDir, "*.bas"))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, string(b))
	}
	if len(paths) == 0 {
		t.Fatalf("no contracts under %s", contractsDir)
	}
	for i, in := range inputs {
		var texts []string
		for _, l := range CanonLines(in) {
			texts = append(texts, l.Text)
		}
		if got, want := strings.Join(texts, "\n")+"\n", ref.Canon(in); got != want {
			t.Fatalf("input %d: CanonLines joined differs from reference.Canon\n got %q\nwant %q", i, got, want)
		}
	}
}

// The line numbers are the source file's: a comment-heavy contract must still point at the right line.
func TestCanonLinesNumbers(t *testing.T) {
	src := "// header\n\nFunction F() Uint64\n// why\n10 RETURN 0\nEnd Function\n"
	got := CanonLines(src)
	want := []Line{{3, "Function F() Uint64"}, {5, "10 RETURN 0"}, {6, "End Function"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: got %+v want %+v", i, got[i], want[i])
		}
	}
}

// ContractSum is the hash simdeploy records: sha256 of the installed (canonical) code. Canon is idempotent, so a
// file already written by simdeploy (app/templates/*.bas) hashes the same raw and canonical — which is the
// equality genconfig -dist relies on when it compares app/templates against the record.
func TestContractSumIsSha256OfCanon(t *testing.T) {
	src := "// c\nFunction F() Uint64\n\n10 RETURN 0\nEnd Function\n"
	h := sha256.Sum256([]byte(ref.Canon(src)))
	if ContractSum(src) != hex.EncodeToString(h[:]) {
		t.Fatal("ContractSum is not sha256(reference.Canon(src))")
	}
	if c := ref.Canon(src); ContractSum(c) != Sum(c) {
		t.Fatal("Canon is not idempotent: a canonical file would hash two ways")
	}
}

func TestFirstDiff(t *testing.T) {
	src := "// header\nFunction F() Uint64\n// why\n10 STORE(\"kind\", \"social\")\n20 RETURN 0\nEnd Function\n"
	chain := ref.Canon(src)
	if d := FirstDiff(src, chain); d != nil {
		t.Fatalf("identical code reported as different: %+v", d)
	}
	// one character
	one := strings.Replace(chain, `"social"`, `"sociaL"`, 1)
	d := FirstDiff(src, one)
	if d == nil || d.Index != 2 || d.SourceLine != 4 || d.Want != `10 STORE("kind", "social")` || d.Got != `10 STORE("kind", "sociaL")` {
		t.Fatalf("one-character change: %+v", d)
	}
	// the chain is missing the last line
	short := strings.TrimSuffix(chain, "End Function\n")
	d = FirstDiff(src, short)
	if d == nil || !d.GotEOF || d.Index != 4 || d.SourceLine != 6 {
		t.Fatalf("truncated code: %+v", d)
	}
	// the chain has an extra line
	d = FirstDiff(src, chain+"Function G() Uint64\n")
	if d == nil || !d.WantEOF || d.Index != 5 || d.Got != "Function G() Uint64" {
		t.Fatalf("extra line: %+v", d)
	}
	// only the final newline differs: the hashes differ, so a difference must be reported
	d = FirstDiff(src, strings.TrimSuffix(chain, "\n"))
	if d == nil {
		t.Fatal("a missing final newline changes the hash and must be reported")
	}
}

func TestContractsReadsEveryFile(t *testing.T) {
	cs, err := Contracts(contractsDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cs {
		seen[c.File] = true
		b, _ := os.ReadFile(filepath.Join(contractsDir, c.File))
		if c.CanonSha256 != ContractSum(string(b)) || c.SourceSha256 != Sum(string(b)) {
			t.Fatalf("%s: hashes do not match the file", c.File)
		}
	}
	for _, k := range []string{"factory.bas", "social.bas", "founders.bas", "chat.bas", "vault.bas", "pool.bas", "launch.bas", "nft.bas", "nfa.bas", "htlc.bas", "swap.bas", "board.bas"} {
		if !seen[k] {
			t.Fatalf("%s missing from Contracts()", k)
		}
	}
}
