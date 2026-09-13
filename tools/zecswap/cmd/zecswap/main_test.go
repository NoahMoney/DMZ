package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veil/tools/zecswap"
)

// MT-A18: -board and -escrow default to the deployment record (deploy/networks.json) for the network, and an
// explicit flag always wins. The scid is only a pointer; the companion still checks the code hash (not tested here).
func TestDefaultContractsFromRecord(t *testing.T) {
	board := strings.Repeat("b", 64)
	swap := strings.Repeat("e", 64)
	rec := `{
	  "_doc": "a string value under a non-network key must not break the read",
	  "simulator": {"daemon": "127.0.0.1:20000", "board": "` + board + `", "swap": "` + swap + `"},
	  "testnet":   {"daemon": "x", "board": "", "swap": ""}
	}`
	path := filepath.Join(t.TempDir(), "networks.json")
	if err := os.WriteFile(path, []byte(rec), 0o600); err != nil {
		t.Fatal(err)
	}
	// regtest maps to the simulator block; both scids come from the record
	var b, e string
	if err := defaultContractsFromRecord("regtest", path, &b, &e); err != nil {
		t.Fatalf("regtest default: %v", err)
	}
	if b != board || e != swap {
		t.Fatalf("defaulted to %s / %s", b, e)
	}
	// an explicit -board wins and the missing -escrow is filled from the record
	given := strings.Repeat("a", 64)
	b, e = given, ""
	if err := defaultContractsFromRecord("regtest", path, &b, &e); err != nil {
		t.Fatalf("half-given: %v", err)
	}
	if b != given || e != swap {
		t.Fatalf("explicit -board was overwritten: %s / %s", b, e)
	}
	b, e = "x", "y"
	if err := defaultContractsFromRecord("regtest", "/does/not/exist.json", &b, &e); err != nil {
		t.Fatalf("both given: the record must not be read: %v", err)
	}
	// a network with no scid yet fails closed rather than defaulting to an empty id
	b, e = "", ""
	if err := defaultContractsFromRecord("testnet", path, &b, &e); err == nil {
		t.Fatal("an empty record entry should fail closed")
	}
	// a missing record file, with the flags omitted, fails closed
	b, e = "", ""
	if err := defaultContractsFromRecord("regtest", "/does/not/exist.json", &b, &e); err == nil {
		t.Fatal("a missing record file with no flags should fail closed")
	}
}

// The swap fee's treasury comes from the record's zecTreasury, for the network's block, and fails closed: on mainnet
// an empty, missing or invalid one refuses the swap and names the field; elsewhere empty means no fee.
func TestTreasuryFromRecord(t *testing.T) {
	main := zecswap.EncodeAddress(zecswap.Mainnet, zecswap.P2PKH, [20]byte{1})
	test := zecswap.EncodeAddress(zecswap.Testnet, zecswap.P2SH, [20]byte{2})
	write := func(rec string) string {
		path := filepath.Join(t.TempDir(), "networks.json")
		if err := os.WriteFile(path, []byte(rec), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, m := range []string{`{"zecTreasury": ""}`, `{"daemon": "x"}`, `{"zecTreasury": "` + test + `"}`, `{"zecTreasury": "zs1abc"}`, `{"zecTreasury": "t1bogus"}`} {
		if _, _, err := treasuryFromRecord("mainnet", write(`{"mainnet": `+m+`}`)); err == nil || !strings.Contains(err.Error(), "zecTreasury") {
			t.Errorf("mainnet %s: %v", m, err)
		}
	}
	if addr, warn, err := treasuryFromRecord("mainnet", write(`{"mainnet": {"zecTreasury": "`+main+`"}}`)); err != nil || addr != main || warn != "" {
		t.Fatalf("a valid mainnet treasury: %q %q %v", addr, warn, err)
	}
	if addr, _, err := treasuryFromRecord("regtest", write(`{"_doc": "x", "simulator": {"zecTreasury": "`+test+`"}}`)); err != nil || addr != test {
		t.Fatalf("regtest reads the simulator's block: %q %v", addr, err)
	}
	if addr, warn, err := treasuryFromRecord("testnet", write(`{"testnet": {"zecTreasury": ""}}`)); err != nil || addr != "" || !strings.Contains(warn, "no swap fee") {
		t.Fatalf("testnet without a treasury: %q %q %v", addr, warn, err)
	}
	if _, _, err := treasuryFromRecord("testnet", write(`{"testnet": {"zecTreasury": "`+main+`"}}`)); err == nil {
		t.Fatal("a mainnet treasury was accepted for a testnet swap")
	}
	if _, _, err := treasuryFromRecord("regtest", "/does/not/exist.json"); err == nil {
		t.Fatal("a missing record was not refused")
	}
	if _, _, err := treasuryFromRecord("signet", write(`{}`)); err == nil {
		t.Fatal("an unknown network was not refused")
	}
}

// The committed record: regtest's treasury (the simulator block's) is valid, and mainnet refuses while its
// zecTreasury is empty.
func TestTheCommittedRecordsTreasury(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "deploy", "networks.json")
	if addr, _, err := treasuryFromRecord("regtest", path); err != nil || addr == "" {
		t.Fatalf("the simulator block's zecTreasury: %q %v", addr, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]struct {
		ZecTreasury *string `json:"zecTreasury"`
	}
	json.Unmarshal(b, &rec)
	for _, n := range []string{"mainnet", "testnet", "simulator"} {
		if rec[n].ZecTreasury == nil {
			t.Errorf("the %s block has no zecTreasury field", n)
		}
	}
	_, _, err = treasuryFromRecord("mainnet", path)
	if m := rec["mainnet"].ZecTreasury; m != nil && *m == "" && err == nil {
		t.Fatal("the record's mainnet zecTreasury is empty and a mainnet swap was not refused")
	}
}

// GAP-029: the maker's secret is read from a file or from stdin, never from the command line.
func TestSecretIsReadFromAFileOrStdin(t *testing.T) {
	secret := strings.Repeat("ab", 32)
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := readSecret(path, nil); err != nil || s != secret {
		t.Fatalf("from a file: %q %v", s, err)
	}
	if s, err := readSecret("-", strings.NewReader(secret+"\n")); err != nil || s != secret {
		t.Fatalf("from stdin: %q %v", s, err)
	}
	for _, bad := range []string{strings.ToUpper(secret), secret[:62], "not hex at all"} {
		if _, err := readSecret("-", strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
