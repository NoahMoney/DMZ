package tests

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// Shared scaffolding for the vault tests. v3 identifies a deposit by (raw txid, vout) and builds the id itself,
// so a test never spells an id by hand.

type vaultCfg struct {
	k, cap, minbond, minburn, wfee, mindep, regfee uint64
	signers                                        int
}

func installVault(t *testing.T, c *harness.Chain, owner, treasury *rpc.Address, cfg vaultCfg) (crypto.Hash, []*rpc.Address) {
	t.Helper()
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", cfg.k), harness.S("treasury", treasury.String()),
		harness.U("cap", cfg.cap), harness.U("minbond", cfg.minbond), harness.U("minburn", cfg.minburn),
		harness.U("wfee", cfg.wfee), harness.U("mindep", cfg.mindep), harness.U("regfee", cfg.regfee), harness.U("capratio", 3), harness.U("bigbps", 2000), harness.U("bigdelay", 0)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d is at the cap %d", res.GasStorage, harness.MaxStorageGas)
	}
	sg := make([]*rpc.Address, cfg.signers)
	for i := range sg {
		sg[i] = c.NewAccount()
		if r := c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", sg[i].String())}, nil, owner); !r.OK() {
			t.Fatalf("add signer %d: %v", i, r.Err)
		}
		if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: cfg.minbond}, sg[i]); !r.OK() {
			t.Fatalf("bond %d: %v", i, r.Err)
		}
	}
	return scid, sg
}

// txhash builds a distinct raw txid for a test deposit.
func txhash(n int) crypto.Hash {
	var h crypto.Hash
	h[0], h[1], h[30], h[31] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	return h
}

// depID is the id the contract builds for (txid, vout): the hex of the raw txid, a colon, the output index.
func depID(n int, vout uint64) string {
	h := txhash(n)
	return hex.EncodeToString(h[:]) + ":" + fmt.Sprint(vout)
}

func mintArgs(j uint64, n int, vout, amount uint64) rpc.Arguments {
	return rpc.Arguments{harness.U("j", j), harness.H("txid", txhash(n)), harness.U("vout", vout), harness.U("amount", amount)}
}
