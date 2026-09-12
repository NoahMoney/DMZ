package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veil/tools/netcfg"
)

const (
	hOld = "3fc1d05b00000000000000000000000000000000000000000000000000000000" // the outgoing template
	hSup = "aaaaaaaa11111111111111111111111111111111111111111111111111111111" // a hash already in legacy
	hNew = "bbbbbbbb22222222222222222222222222222222222222222222222222222222" // the template being installed
)

// writeRecord puts one network block on disk and points the tool's flags at it.
func writeRecord(t *testing.T, legacy []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "networks.json")
	f := &netcfg.File{Networks: map[string]*netcfg.Network{
		"simulator": {Templates: map[string]netcfg.Template{"launch": {Sha256: hOld, Legacy: legacy}}},
	}}
	if err := f.Save(p); err != nil {
		t.Fatal(err)
	}
	*networks, *network = p, "simulator"
	return p
}

func legacyOf(t *testing.T, n *netcfg.Network) []string {
	t.Helper()
	return n.Templates["launch"].Legacy
}

// FE-1, simdeploy half. The outgoing hash was demoted to legacy unconditionally, with the comment "kept as legacy
// so markets already deployed from it still verify". That is right for a template that gained a feature and wrong
// for one that fixed a drain: the app verifies a listing against this list, so a legacy entry is the app
// certifying that code. -revoke names a hash the app must stop accepting.
func TestRevokeDropsTheOutgoingHashInsteadOfDemotingIt(t *testing.T) {
	writeRecord(t, []string{hSup})
	n := record(map[string]string{"launch": hNew}, nil, map[string]map[string]bool{"launch": {hOld: true}}, "")
	got := legacyOf(t, n)
	for _, l := range got {
		if l == hOld {
			t.Fatalf("the revoked hash is still accepted: legacy = %v", got)
		}
	}
	if len(got) != 1 || got[0] != hSup {
		t.Fatalf("legacy = %v, want only the untouched %s…", got, hSup[:8])
	}
}

// Without -revoke nothing changes: an unnamed outgoing hash is still carried forward, because de-listing every
// market on every template change is the defect this behaviour was added to prevent.
func TestWithoutRevokeTheOutgoingHashIsStillCarriedForward(t *testing.T) {
	writeRecord(t, []string{hSup})
	n := record(map[string]string{"launch": hNew}, nil, nil, "")
	got := legacyOf(t, n)
	if len(got) != 2 {
		t.Fatalf("legacy = %v, want the previous hash and the older one", got)
	}
	found := false
	for _, l := range got {
		found = found || l == hOld
	}
	if !found {
		t.Fatalf("the outgoing hash was dropped without -revoke: legacy = %v", got)
	}
}

// A hash already sitting in legacy is revocable too: the defective code is usually one release behind by the time
// anyone reads the audit.
func TestRevokeAlsoRemovesAHashAlreadyInLegacy(t *testing.T) {
	writeRecord(t, []string{hSup})
	n := record(map[string]string{"launch": hNew}, nil, map[string]map[string]bool{"launch": {hSup: true}}, "")
	got := legacyOf(t, n)
	if len(got) != 1 || got[0] != hOld {
		t.Fatalf("legacy = %v, want only the outgoing hash %s…", got, hOld[:8])
	}
}

// The written file is what genconfig reads, so the revocation has to survive the round trip, not only the return
// value this test could otherwise be fooled by.
func TestTheRevocationIsWrittenToTheRecord(t *testing.T) {
	p := writeRecord(t, []string{hSup})
	record(map[string]string{"launch": hNew}, nil, map[string]map[string]bool{"launch": {hOld: true}}, "")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), hOld) {
		t.Fatalf("%s still names the revoked hash", p)
	}
}

// A -revoke that matches nothing revokes nothing, and would leave the defective template listed while reading
// like it had been handled. It is refused before any contract is installed.
func TestCheckHashFlagsRefusesARevocationThatMatchesNothing(t *testing.T) {
	writeRecord(t, []string{hSup})
	cases := []struct {
		name   string
		old    map[string]map[string]bool
		rev    map[string]map[string]bool
		errHas string
	}{
		{"unlisted hash", nil, map[string]map[string]bool{"launch": {hNew: true}}, "does not list"},
		{"unknown kind", nil, map[string]map[string]bool{"lunch": {hOld: true}}, "unknown template kind"},
		{"short hash", nil, map[string]map[string]bool{"launch": {"3fc1d05b": true}}, "not a 64-character"},
		{"upper-case hash", nil, map[string]map[string]bool{"launch": {strings.ToUpper(hOld): true}}, "not a 64-character"},
		{"both lists", map[string]map[string]bool{"launch": {hOld: true}}, map[string]map[string]bool{"launch": {hOld: true}}, "both -legacy and -revoke"},
	}
	for _, c := range cases {
		err := checkHashFlags(c.old, c.rev)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.errHas) {
			t.Errorf("%s: refusal does not say %q: %v", c.name, c.errHas, err)
		}
	}
	if err := checkHashFlags(nil, map[string]map[string]bool{"launch": {hOld: true}}); err != nil {
		t.Errorf("a revocation of the listed hash was refused: %v", err)
	}
}

// UX-02: the compiled-in Founders Pass description goes on chain permanently. tests/templates_test.go checks the
// same two substrings from outside the package; this one holds the sentence itself against what v2 and the bot do.
func TestFoundersPassDescriptionPromisesOnlyWhatTheContractsDo(t *testing.T) {
	for _, s := range []string{"every fee", "paid in DERO", "do not reach holders"} {
		if strings.Contains(passDescrDefault, s) {
			t.Errorf("the default description still says %q: %s", s, passDescrDefault)
		}
	}
	// v2 pays each asset in itself (per-asset pools), and the XMR/ZEC payments are the bot's, forced by nothing
	for _, s := range []string{"paid in the asset each fee was taken in", "XMR and ZEC by the treasury's bot", "no contract forces it"} {
		if !strings.Contains(passDescrDefault, s) {
			t.Errorf("the default description does not say %q: %s", s, passDescrDefault)
		}
	}
}

// parseHashes is the flag reader both lists go through.
func TestParseHashes(t *testing.T) {
	got := parseHashes(" launch:" + hOld + " , pool:" + hNew + ",nonsense,")
	if !got["launch"][hOld] || !got["pool"][hNew] {
		t.Fatalf("parseHashes dropped an entry: %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("parseHashes kept an entry with no colon: %v", got)
	}
	if len(parseHashes("")) != 0 {
		t.Fatal("an empty flag is not an empty set")
	}
}
