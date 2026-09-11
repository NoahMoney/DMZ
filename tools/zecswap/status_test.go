package zecswap

import (
	"context"
	"strings"
	"testing"
)

// Network selection fails closed (the GAP-007 class): -network has no default, the Zcash node must be on it, and
// the DERO daemon on the matching kind; a mainnet leg is never paired with a test one in either direction.
func TestCheckNetworksFailsClosed(t *testing.T) {
	for _, c := range []struct {
		network, chain string
		deroTestnet    bool
		ok             bool
	}{
		{"mainnet", "main", false, true},
		{"testnet", "test", true, true},
		{"regtest", "regtest", true, true},
		{"", "main", false, false},        // no default
		{"", "test", true, false},         // no default either
		{"mainnet", "main", true, false},  // Zcash mainnet with a DERO testnet or simulator daemon
		{"testnet", "test", false, false}, // Zcash testnet with a DERO mainnet daemon
		{"mainnet", "test", false, false}, // -network mainnet on a testnet Zcash node
		{"testnet", "main", true, false},  // -network testnet on a mainnet Zcash node
		{"testnet", "regtest", true, false},
		{"stagenet", "test", true, false},
	} {
		err := CheckNetworks(c.network, c.chain, c.deroTestnet)
		if (err == nil) != c.ok {
			t.Errorf("CheckNetworks(-network %q, Zcash %q, DERO testnet %v) = %v", c.network, c.chain, c.deroTestnet, err)
		}
	}
}

// GAP-008: on mainnet the wallet login comes from the environment only, and a mainnet swap has one.
func TestResolveDeroLogin(t *testing.T) {
	for _, c := range []struct {
		flag, env  string
		mainnet    bool
		user, pass string
		ok         bool
	}{
		{"", "u:p", true, "u", "p", true},
		{"u:p", "", true, "", "", false},   // never argv on mainnet
		{"", "", true, "", "", false},      // mainnet without a login
		{"u:p", "", false, "u", "p", true}, // the flag is fine elsewhere
		{"", "", false, "", "", true},      // the simulator's wallet has no login
		{"", "nocolon", false, "", "", false},
		{"", ":p", false, "", "", false},
	} {
		u, p, err := ResolveDeroLogin(c.flag, c.env, c.mainnet)
		if (err == nil) != c.ok || u != c.user || p != c.pass {
			t.Errorf("ResolveDeroLogin(%q, %q, mainnet %v) = %q %q %v", c.flag, c.env, c.mainnet, u, p, err)
		}
	}
}

// Every node is named as a trusted party; a chain read through one node prints the warning, and one on another
// machine the total-loss warning.
func TestSingleEndpointPrintsTheWarning(t *testing.T) {
	joined := func(ws []string) string { return strings.Join(ws, "\n") }
	local := joined(TrustWarnings([]string{"http://127.0.0.1:18232"}, []string{"127.0.0.1:20000"}))
	if strings.Count(local, "WARNING") != 2 || strings.Count(local, "trusted party") != 2 || !strings.Contains(local, "single node can cost you the whole swap") {
		t.Fatalf("one local node per chain:\n%s", local)
	}
	remote := joined(TrustWarnings([]string{"http://203.0.113.5:8232"}, []string{"127.0.0.1:20000"}))
	if !strings.Contains(remote, "ON ANOTHER MACHINE") || !strings.Contains(remote, "can cost you the whole swap") {
		t.Fatalf("one remote Zcash node:\n%s", remote)
	}
	two := joined(TrustWarnings([]string{"http://127.0.0.1:18232", "http://127.0.0.1:18233"}, []string{"127.0.0.1:20000", "127.0.0.1:20001"}))
	if strings.Contains(two, "WARNING") || strings.Count(two, "trusted party") != 4 {
		t.Fatalf("two nodes per chain:\n%s", two)
	}
}

func TestPolicyValidate(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatalf("§5's policy: %v", err)
	}
	if err := testPolicy().Validate(); err != nil {
		t.Fatalf("the test policy: %v", err)
	}
	for name, mut := range map[string]func(*Policy){
		"tolerance at the window":       func(p *Policy) { p.CLTVTolerance = p.CLTVWindow },
		"no block to claim in":          func(p *Policy) { p.ClaimCutoff = p.CLTVWindow },
		"a funding alive at cltv":       func(p *Policy) { p.FundingExpiry = p.CLTVWindow },
		"taker minimum above T_dero":    func(p *Policy) { p.TakerMinDeroLeft = p.TDeroWindow },
		"T_dero before cltv in seconds": func(p *Policy) { p.TakerMinDeroLeft, p.TDeroWindow = 100, 200 },
		"a zero":                        func(p *Policy) { p.SettleBlocks = 0 },
	} {
		p := DefaultPolicy()
		mut(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// status prints each swap beside what the nodes say now, and flags a divergence between the two.
func TestStatusFlagsDivergence(t *testing.T) {
	w := newWorld(t, 3)
	if !w.drive(300) {
		t.Fatalf("the swap did not finish:\n%s", w.dump())
	}
	ctx := context.Background()
	zsrc := []ZcashSource{{Name: "zcash", Node: w.zc.client()}}
	dsrc := []DeroSource{{Name: "dero", Node: newMemDero(w.dero, makerAddr)}}
	for _, role := range []string{RoleMaker, RoleTaker} {
		out := strings.Join(Status(ctx, w.state(role), zsrc, dsrc), "\n")
		if strings.Contains(out, "DIVERGENCE") || !strings.Contains(out, "trusted party") || !strings.Contains(out, "ended") {
			t.Fatalf("%s after a clean swap:\n%s", role, out)
		}
	}
	// a state that says the lock is still waiting for its funding, beside an escrow already claimed
	st := w.state(RoleMaker)
	st.Phase, st.Done = "locked", false
	out := strings.Join(Status(ctx, st, zsrc, dsrc), "\n")
	if !strings.Contains(out, "DIVERGENCE") || !strings.Contains(out, "if it stops now") {
		t.Fatalf("a stale state beside the chain:\n%s", out)
	}
	// a state bound to another Zcash network
	st = w.state(RoleTaker)
	st.ZcashChain = "main"
	if out := strings.Join(Status(ctx, st, zsrc, dsrc), "\n"); !strings.Contains(out, "DIVERGENCE") {
		t.Fatalf("a mainnet swap read through a testnet node:\n%s", out)
	}
}
