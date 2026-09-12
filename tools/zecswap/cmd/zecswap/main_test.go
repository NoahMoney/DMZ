package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
