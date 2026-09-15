package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const repoRoot = "../.."

func fileLines(t *testing.T, rel string) ([]string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n"), true
}

// Every line the role table cites must still say what it is cited for. contracts/ is edited while this table
// exists; a moved line fails here instead of leaving the report pointing at the wrong statement.
func TestRoleCitations(t *testing.T) {
	var all []cite
	for _, k := range roleTable {
		for _, r := range k.Roles {
			all = append(all, r.Cites...)
		}
	}
	all = append(all, telaOwnerCites...)
	for _, c := range all {
		lines, ok := fileLines(t, c.File)
		if !ok {
			if strings.HasPrefix(c.File, "vendor/") {
				t.Logf("%s not present (vendor/ is not tracked): citation %d not checked", c.File, c.Line)
				continue
			}
			t.Fatalf("%s: cannot read", c.File)
		}
		if c.Line < 1 || c.Line > len(lines) || !strings.Contains(lines[c.Line-1], c.Has) {
			got := "(past the end)"
			if c.Line >= 1 && c.Line <= len(lines) {
				got = strings.TrimSpace(lines[c.Line-1])
			}
			t.Errorf("%s:%d is cited for %q but reads %q", c.File, c.Line, c.Has, got)
		}
	}
}

func parseContract(t *testing.T, kind string) *bprog {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "contracts", kind+".bas"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseBasic(string(b))
	if err != nil {
		t.Fatalf("%s.bas: %v", kind, err)
	}
	return p
}

// The functions that can write a role's keys are exactly the ones the table lists. A new setter — a function that
// could move a treasury, raise a fee or hand a role back — fails this test until the table accounts for it.
func TestRoleWriters(t *testing.T) {
	for _, k := range roleTable {
		p := parseContract(t, k.Kind)
		stores, deletes := p.keySites("STORE"), p.keySites("DELETE")
		for _, r := range k.Roles {
			set := map[string]bool{}
			for _, key := range r.Keys {
				for _, f := range stores[key] {
					set[f] = true
				}
				for _, f := range deletes[key] {
					set[f] = true
				}
			}
			var got []string
			for f := range set {
				got = append(got, f)
			}
			sort.Strings(got)
			want := append([]string{}, r.Writers...)
			sort.Strings(want)
			if r.Legacy && len(want) != 0 {
				t.Errorf("%s.%s is Legacy, a role of an older version, but lists writers %v", k.Kind, r.Role, want)
			}
			if (len(got) != 0 || len(want) != 0) && !reflect.DeepEqual(got, want) {
				t.Errorf("%s.%s: functions writing %v are %v; the table lists %v", k.Kind, r.Role, r.Keys, got, want)
			}
		}
	}
}

// An inert role is one no function reads: the founders "creator" is stored and never used for authority.
func TestRoleReaders(t *testing.T) {
	for _, k := range roleTable {
		p := parseContract(t, k.Kind)
		loads := p.keySites("LOAD")
		for _, r := range k.Roles {
			if r.Policy != pInert {
				continue
			}
			for _, key := range r.Keys {
				if fs := loads[key]; len(fs) > 0 {
					t.Errorf("%s.%s is inert in the table, but %v LOAD %q", k.Kind, r.Role, fs, key)
				}
			}
		}
	}
}

// Every contract in the repository has a table entry, and every entry a contract.
func TestRoleKinds(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join(repoRoot, "contracts", "*.bas"))
	have := map[string]bool{}
	for _, p := range paths {
		have[strings.TrimSuffix(filepath.Base(p), ".bas")] = true
	}
	for _, k := range roleTable {
		if !have[k.Kind] {
			t.Errorf("roleTable has %s, contracts/ has no %s.bas", k.Kind, k.Kind)
		}
		delete(have, k.Kind)
	}
	for k := range have {
		t.Errorf("contracts/%s.bas has no entry in roleTable", k)
	}
}

// fake variables, spelled the way DERO.GetSC returns them: strings as hex, numbers as numbers
func sv(b []byte) value { return value{text: hexOf(b)} }
func ss(s string) value { return sv([]byte(s)) }
func nv(n uint64) value { return value{num: true, u: n, text: strconv.FormatUint(n, 10)} }
func key(b byte) []byte { k := make([]byte, 33); k[0] = 2; k[32] = b; return k }
func find(rows []roleRow, role string) roleRow {
	for _, r := range rows {
		if r.Role == role {
			return r
		}
	}
	return roleRow{}
}

// factory.bas v2 (MT-J15(b)) has no owner, so a v2 factory's report has no owner row and nothing in it fails. An older
// v1 factory is still recognised by its state: a live owner fails and names what it can do, and a renounced one passes.
func TestFactoryOwnerOnlyInAnOlderFactory(t *testing.T) {
	base := vars{"treasury": sv(key(2)), "tstr": ss("dero1x"), "regfee": nv(10000), "initd": nv(1), "pc": nv(0), "lc": nv(0), "nc": nv(0)}
	v2 := vars{"kind": ss("factory"), "v": nv(2)}
	for k, v := range base {
		v2[k] = v
	}
	rows := evalRoles("factory", "factory", "f", v2, nil)
	if o := find(rows, "owner"); o.Role != "" {
		t.Fatalf("a v2 factory shows an owner row: %+v", o)
	}
	if tr := find(rows, "treasury"); !strings.Contains(tr.State, "fixed for ever") {
		t.Fatalf("v2 treasury: %+v", tr)
	}
	if rf := find(rows, "regfee"); !strings.Contains(rf.State, "fixed for ever") {
		t.Fatalf("v2 regfee: %+v", rf)
	}
	for _, r := range rows {
		if r.Status == sFail || r.Status == sAllowed {
			t.Fatalf("a v2 factory needs no -allow, yet: %+v", r)
		}
	}
	live := vars{"owner": sv(key(1))}
	for k, v := range base {
		live[k] = v
	}
	o := find(evalRoles("factory", "factory", "f", live, nil), "owner")
	if o.Status != sFail || !o.Live || !strings.Contains(o.State, "SetTreasury") || !strings.Contains(o.State, "older factory.bas") {
		t.Fatalf("live owner: %+v", o)
	}
	allowed := find(evalRoles("factory", "factory", "f", live, map[string]string{"factory.owner": "testnet: Renounce after the soak"}), "owner")
	if allowed.Status != sAllowed || allowed.Reason == "" {
		t.Fatalf("allowed owner: %+v", allowed)
	}
	ren := vars{"renounced": nv(812)}
	for k, v := range base {
		ren[k] = v
	}
	rows = evalRoles("factory", "factory", "f", ren, nil)
	if o := find(rows, "owner"); o.Status != sPass || o.Live || !strings.Contains(o.Value, "812") {
		t.Fatalf("renounced owner: %+v", o)
	}
	if tr := find(rows, "treasury"); !strings.Contains(tr.State, "fixed for ever") {
		t.Fatalf("treasury after renounce: %+v", tr)
	}
	for _, r := range rows {
		if r.Status == sFail {
			t.Fatalf("a renounced factory still fails: %+v", r)
		}
	}
}

func TestVaultOwnerEndsAtSeal(t *testing.T) {
	v := vars{"owner": sv(key(1)), "treasury": sv(key(2)), "sealed": nv(0), "initd": nv(1), "k": nv(2), "n": nv(3), "na": nv(3),
		"sg_0": sv(key(3)), "sg_1": sv(key(4)), "sg_2": sv(key(5))}
	rows := evalRoles("vault", "vault", "v", v, nil)
	if o := find(rows, "owner"); o.Status != sFail || !o.Live {
		t.Fatalf("unsealed vault owner: %+v", o)
	}
	if s := find(rows, "signers"); s.Live || s.Status != sInfo {
		t.Fatalf("signers before seal: %+v", s)
	}
	v["sealed"] = nv(1)
	rows = evalRoles("vault", "vault", "v", v, nil)
	if o := find(rows, "owner"); o.Status != sPass || o.Live {
		t.Fatalf("sealed vault owner: %+v", o)
	}
	if s := find(rows, "signers"); !s.Live || s.Status != sWarn || !strings.Contains(s.Value, "k=2 of na=3") {
		t.Fatalf("signers after seal: %+v", s)
	}
}

func TestMissingInitGuardFails(t *testing.T) {
	for _, kind := range []string{"social", "htlc", "swap", "board", "chat", "founders", "pool", "launch", "nft", "vault", "factory"} {
		g := find(evalRoles(kind, kind, "x", vars{}, nil), "initd")
		if g.Status != sFail || !g.Live {
			t.Fatalf("%s without initd: %+v", kind, g)
		}
	}
	if g := find(evalRoles("nfa", "nfa", "x", vars{}, nil), "initd"); g.Status != sFail {
		t.Fatalf("nfa without owner: %+v", g)
	}
}

func TestAllowNeedsAKnownRoleAndAReason(t *testing.T) {
	a := allowFlag{}
	if err := a.Set("factory.owner="); err == nil {
		t.Fatal("an -allow without a reason was accepted")
	}
	if err := a.Set("factory.treasury=because"); err == nil {
		t.Fatal("-allow accepted a role that cannot fail")
	}
	if err := a.Set("tela.owner=a ring-2 INDEX kept for the testnet soak"); err != nil || a["tela.owner"] == "" {
		t.Fatalf("a valid -allow was refused: %v", err)
	}
}

func TestCompactCites(t *testing.T) {
	got := compactCites([]cite{c("factory.bas", 25, ""), c("factory.bas", 40, ""), c("factory.bas", 39, ""), c("pool.bas", 3, ""), c("factory.bas", 41, "")})
	want := []string{"factory.bas:25,39-41", "pool.bas:3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// founders.bas v2: no owner, so nothing fails on a well-formed pass; every row reads its value from storage; the
// payout addresses are counted, never judged; and there is no minhst row (v1's floor is gone).
func TestFoundersV2Roles(t *testing.T) {
	v := vars{"creator": sv(key(1)), "treasury": sv(key(2)), "initd": nv(1), "v": nv(2), "share": nv(5000), "mfee": nv(250), "price": nv(100000),
		"maxsupply": nv(20), "start": nv(0), "reserve": nv(4), "hst": nv(3),
		"xm_dero1a": ss(strings.Repeat("8", 95)), "xm_dero1b": ss(strings.Repeat("4", 106)), "zc_dero1a": ss("t1" + strings.Repeat("A", 33))}
	rows := evalRoles("founders", "founders", "f", v, nil)
	for _, r := range rows {
		if r.Status == sFail || r.Status == sAllowed {
			t.Fatalf("a v2 pass needs no -allow, yet: %+v", r)
		}
		if strings.Contains(r.Role, "minhst") || strings.Contains(strings.Join(r.Keys, ","), "minhst") {
			t.Fatalf("a row looks for v1's minhst: %+v", r)
		}
	}
	if r := find(rows, "terms"); !strings.Contains(r.Value, "v=2") || !strings.Contains(r.Value, "share=5000") || !strings.Contains(r.Value, "reserve=4") {
		t.Fatalf("terms: %+v", r)
	}
	if r := find(rows, "payout addresses"); r.Value != "2 Monero, 1 Zcash" || r.Live || !strings.Contains(r.State, "nothing pays to them") {
		t.Fatalf("payout addresses: %+v", r)
	}
	if r := find(rows, "treasury"); r.Policy != pPayee || !strings.Contains(r.State, "mint proceeds") {
		t.Fatalf("treasury: %+v", r)
	}
	if r := find(rows, "creator"); r.Policy != pInert || r.Live {
		t.Fatalf("creator: %+v", r)
	}
}
