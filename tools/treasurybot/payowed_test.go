package main

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	rpc "veil/tools/simrpc"
)

// fakeOwed holds each NFA's code hash and its ow_/op_ balances, keyed "<scid>|<payee>". A PayOwed moves the whole
// ow_ balance into op_ exactly as nfa.bas PayOwed does (80-150), but only for an NFA listed in lands: the others
// model a call that is included and does not execute.
type fakeOwed struct {
	code   map[string]string
	ow, op map[string]uint64
	lands  map[string]bool
	sent   []string
}

func (f *fakeOwed) CodeHash(s string) string { return f.code[s] }
func (f *fakeOwed) Owed(s, p string) uint64  { return f.ow[s+"|"+p] }
func (f *fakeOwed) Paid(s, p string) uint64  { return f.op[s+"|"+p] }
func (f *fakeOwed) SendPayOwed(s, p string) (string, error) {
	f.sent = append(f.sent, s)
	if f.lands[s] {
		k := s + "|" + p
		f.op[k] += f.ow[k]
		delete(f.ow, k)
	}
	return strings.Repeat("7", 64), nil
}

const (
	tplHash  = "528f7fdc241a590a442e0f7812c8c968baf774a0fb42e884befb944548ea11dd"
	treasury = "dero1qytreasury"
)

func scid(c byte) string { return strings.Repeat(string(c), 64) }

func newFake() *fakeOwed {
	return &fakeOwed{code: map[string]string{}, ow: map[string]uint64{}, op: map[string]uint64{}, lands: map[string]bool{}}
}

// The treasury is paid on a shipped NFA that owes it enough; a contract that is not the shipped template is never
// called, whatever keys it keeps; a balance below the floor waits; and a call that does not execute is reported
// and not counted as paid, its balance still owed.
func TestPayOwedPaysOnlyTheShippedTemplate(t *testing.T) {
	f := newFake()
	a, b, c, d := scid('a'), scid('b'), scid('c'), scid('d')
	for _, s := range []string{a, c, d} {
		f.code[s] = tplHash
	}
	f.code[b] = strings.Repeat("0", 64) // registered with the factory, but not nfa.bas
	f.ow[a+"|"+treasury], f.ow[b+"|"+treasury], f.ow[c+"|"+treasury], f.ow[d+"|"+treasury] = 10000, 10000, 100, 9000
	f.lands[a], f.lands[b] = true, true
	paid, errs := payOwed(f, []string{a, b, c, d}, treasury, map[string]bool{tplHash: true}, 5000)
	if strings.Join(f.sent, ",") != a+","+d {
		t.Fatalf("sent to %v, want exactly the two shipped NFAs owing at least 5000", f.sent)
	}
	if len(paid) != 1 || paid[a] != 10000 {
		t.Fatalf("paid %v, want only %s… with 10000", paid, a[:4])
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "did not grow") {
		t.Fatalf("errs %v, want one report for the call that did not execute", errs)
	}
	if f.ow[d+"|"+treasury] != 9000 {
		t.Fatalf("the unexecuted call's balance moved: %d", f.ow[d+"|"+treasury])
	}
}

// Only the treasury's own ow_ key is read: shares owed to the artificer or to a charity are theirs to collect
// (anyone may press PayOwed for them in the app), and the bot never spends a fee on them.
func TestPayOwedLeavesOtherPayeesAlone(t *testing.T) {
	f := newFake()
	a := scid('a')
	f.code[a] = tplHash
	f.ow[a+"|dero1qycharity"], f.ow[a+"|dero1qyartificer"] = 50000, 50000
	f.lands[a] = true
	paid, errs := payOwed(f, []string{a}, treasury, map[string]bool{tplHash: true}, 1)
	if len(f.sent) != 0 || len(paid) != 0 || len(errs) != 0 {
		t.Fatalf("sent %v paid %v errs %v: nothing is owed to the treasury here", f.sent, paid, errs)
	}
}

// A dry run reads and prints; it sends nothing and reports nothing as paid, which is how the bot starts on mainnet
// without -yes.
func TestPayOwedDryRunSendsNothing(t *testing.T) {
	f := newFake()
	a := scid('a')
	f.code[a] = tplHash
	f.ow[a+"|"+treasury] = 20000
	f.lands[a] = true
	paid, errs := payOwed(dryOwed{f}, []string{a}, treasury, map[string]bool{tplHash: true}, 1)
	if len(f.sent) != 0 || len(paid) != 0 || len(errs) != 0 || f.ow[a+"|"+treasury] != 20000 {
		t.Fatalf("dry run moved something: sent %v paid %v errs %v owed %d", f.sent, paid, errs, f.ow[a+"|"+treasury])
	}
}

// The template hashes come from config.js's nfa entry, current and legacy, and from nowhere else in the file.
func TestNFATemplatesFromConfig(t *testing.T) {
	legacyA, legacyB, pool := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)
	src := `templates: {
    nfa:      { file: "templates/nfa.bas", sha256: "` + tplHash + `", legacy: ["` + legacyA + `", "` + legacyB + `"] },
    pool:     { file: "templates/pool.bas", sha256: "` + pool + `", legacy: [] }
  }`
	got := nfaTemplates(src)
	if len(got) != 3 || !got[tplHash] || !got[legacyA] || !got[legacyB] || got[pool] {
		t.Fatalf("nfaTemplates = %v", got)
	}
	if got := nfaTemplates(`nfa: { file: "templates/nfa.bas", sha256: "` + tplHash + `", legacy: [] }`); len(got) != 1 || !got[tplHash] {
		t.Fatalf("an empty legacy list: %v", got)
	}
	if got := nfaTemplates(`pool: { sha256: "` + pool + `" }`); len(got) != 0 {
		t.Fatalf("a config with no nfa template must verify nothing: %v", got)
	}
}

// nft_ and nfa_ share the factory's nc counter; only nfa_ slots are NFAs.
func TestRegisteredNFAsTakesOnlyNFASlots(t *testing.T) {
	enc := func(s string) string { return hex.EncodeToString([]byte(s)) }
	reg := rpc.SCState{StringKeys: map[string]interface{}{
		"nc":    json.Number("3"),
		"nfa_0": enc(scid('a')),
		"nft_1": enc(scid('b')),
		"nfa_2": enc(scid('c')),
	}}
	got := registeredNFAs(reg)
	if len(got) != 2 || got[0] != scid('a') || got[1] != scid('c') {
		t.Fatalf("registeredNFAs = %v", got)
	}
}
