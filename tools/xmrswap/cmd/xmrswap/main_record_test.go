package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestHtlcMayBeOmittedButNotMalformed (MT-A18): -htlc is optional — the companion then takes the scid the record names
// for the daemon's DERO network (xmrswap.RecordedHTLC, checked against deploy/networks.json by its own test) and
// refuses it unless its code is the pinned htlc.bas — while a malformed -htlc is still refused at parse time.
func TestHtlcMayBeOmittedButNotMalformed(t *testing.T) {
	none := func(string) string { return "" }
	base := []string{"-net", "stagenet", "-daemon", "127.0.0.1:40402", "-dero", "127.0.0.1:40403", "-monero", "http://127.0.0.1:38083",
		"-prover", filepath.Join(t.TempDir(), "proof-xmr"), "-amount-dero", "1", "-amount-xmr", "1"}
	o, err := parseOptions("alice", base, none)
	if err != nil || o.htlc != "" {
		t.Fatalf("an omitted -htlc: %v (htlc %q)", err, o.htlc)
	}
	for _, bad := range []string{"abcd", strings.Repeat("ab", 33)} {
		if _, err := parseOptions("alice", append(append([]string(nil), base...), "-htlc", bad), none); err == nil {
			t.Fatalf("-htlc %q was accepted", bad)
		}
	}
	if o, err := parseOptions("alice", append(append([]string(nil), base...), "-htlc", strings.Repeat("ab", 32)), none); err != nil || o.htlc != strings.Repeat("ab", 32) {
		t.Fatalf("a well-formed -htlc: %v", err)
	}
}
