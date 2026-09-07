package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// A contract's install entrypoint must not be callable again after installation: `Initialize` is an ordinary
// exported function to the DVM, and a second call would reset counters (swap.bas `n`, board.bas `n`) or ownership
// (factory.bas `owner`). Every contract must reject the call, from a stranger and from the deployer alike.
func TestInitializeNotCallable(t *testing.T) {
	c := harness.New()
	owner, treasury, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount()
	cases := []struct {
		file  string
		entry string
		args  rpc.Arguments
		key   string // a key the init writes; must be unchanged afterwards
	}{
		{"swap.bas", "Initialize", nil, "n"},
		{"board.bas", "Initialize", nil, "n"},
		{"social.bas", "Initialize", nil, "kind"},
		{"factory.bas", "Initialize", rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("regfee", 10000)}, "owner"},
		// the zero-allocation / zero-reserve paths used to jump over the initd store (review 2026-09-06): every
		// InitializePrivate contract is covered with the parameters that took that branch
		{"launch.bas", "InitializePrivate", launchArgs(launchParams{supply: 1000000 * 100000, grad: 20 * 100000, cbps: 0, tax: 0, tmode: 0, fee: 50, pfee: 5000}, treasury.String()), "creator"},
		{"founders.bas", "InitializePrivate", rpc.Arguments{harness.S("name", "Veil Founders Pass"), harness.S("descr", "a share of every fee"), harness.S("icon", ""), harness.S("file", ""),
			harness.U("price", 0), harness.U("maxsupply", 100000), harness.U("start", 0), harness.U("reserve", 0), harness.U("share", 5000), harness.U("mfee", 250), harness.S("treasury", treasury.String())}, "creator"},
		{"pool.bas", "InitializePrivate", rpc.Arguments{harness.H("asset", func() crypto.Hash {
			var h crypto.Hash
			for i := range h {
				h[i] = 0xab
			}
			return h
		}()), harness.H("quote", zeroHash), harness.S("treasury", treasury.String()), harness.U("fee", 50), harness.U("pfee", 5000)}, "treasury"},
		{"vault.bas", "InitializePrivate", rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()), harness.U("cap", 1000),
			harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10), harness.U("mindep", 1), harness.U("regfee", 0), harness.U("capratio", 3), harness.U("bigbps", 2000), harness.U("bigdelay", 0)}, "owner"},
		{"nft.bas", "InitializePrivate", rpc.Arguments{harness.S("name", "Badge"), harness.S("descr", ""), harness.S("icon", ""), harness.S("file", ""), harness.S("tags", ""), harness.S("collection", "c"),
			harness.S("token", "tokentokentokentokentokentokento"), harness.U("minhold", 1), harness.U("holdblocks", 1), harness.U("maxsupply", 10), harness.U("price", 0), harness.S("treasury", treasury.String()), harness.U("fee", 50)}, "creator"},
	}
	for _, tc := range cases {
		code := readContract(t, tc.file)
		scid, res := c.Install(code, nil, tc.args, owner, harness.MaxStorageGas)
		if !res.OK() {
			t.Fatalf("%s install: %v", tc.file, res.Err)
		}
		// make the state differ from a fresh install where possible, so a reset would be visible
		switch tc.file {
		case "swap.bas":
			if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", "4dea5fcbba8169ebf2df6bb21fa84d77d13f0d8acbcb7a81041e7f5052a3e136"), harness.S("taker", ""), harness.U("expiry", c.Height+10), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, owner); !r.OK() {
				t.Fatalf("swap lock: %v", r.Err)
			}
		case "board.bas":
			if r := c.Call(scid, "Post", rpc.Arguments{harness.S("chain", "sol"), harness.S("giveAsset", ""), harness.U("giveAmount", 1000), harness.U("wantAmount", 1), harness.S("wantAddr", "So1ana"), harness.U("expiry", c.Height+10)}, nil, owner); !r.OK() {
				t.Logf("board post (informational): %v", r.Err)
			}
		}
		before := c.Var(scid, tc.key)
		beforeN, hasN := uint64(0), c.Exists(scid, "n")
		if hasN {
			beforeN = c.U(scid, "n")
		}
		for _, who := range []*rpc.Address{stranger, owner} {
			if r := c.Call(scid, tc.entry, tc.args, nil, who); r.OK() {
				t.Errorf("%s: %s callable after install by %s", tc.file, tc.entry, who.String()[:12])
			}
		}
		if after := c.Var(scid, tc.key); after != before {
			t.Errorf("%s: key %q changed by a post-install init call: %v → %v", tc.file, tc.key, before, after)
		}
		if hasN && c.U(scid, "n") != beforeN {
			t.Errorf("%s: counter n reset", tc.file)
		}
	}
}
