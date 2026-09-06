package tests

import (
	"testing"

	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestLaunchInitializeNotCallableAnyCbps(t *testing.T) {
	for _, cbps := range []uint64{0, 500} {
		e := newLaunchEnv(t, launchParams{supply: 100000000, grad: 100000000, cbps: cbps, tax: 0, tmode: 0, fee: 100, pfee: 5000})
		t.Logf("cbps=%d initd exists after install: %v", cbps, e.c.Exists(e.scid, "initd"))
		for i := 0; i < 20 && e.m.Phase == 0; i++ {
			e.buy(t, e.alice, 20000000, 0, 0, 0)
		}
		t.Logf("cbps=%d phase=%d rd=%d ra=%d sold=%d balance=%d creator=%s", cbps, e.c.U(e.scid, "phase"), e.c.U(e.scid, "rd"), e.c.U(e.scid, "ra"), e.c.U(e.scid, "sold"), e.c.SCBalance(e.scid, zero), e.c.S(e.scid, "creator")[:12])
		attacker := e.c.NewAccount()
		r := e.c.Call(e.scid, "InitializePrivate", launchArgs(launchParams{supply: 1 << 62, grad: 1000000, cbps: 500, tax: 1000, tmode: 1, fee: 100, pfee: 10000}, attacker.String()), nil, attacker)
		t.Logf("cbps=%d re-init by attacker OK=%v err=%v minted-to-attacker=%d", cbps, r.OK(), r.Err, r.Sent(e.scid, attacker))
		t.Logf("cbps=%d after: phase=%d rd=%d ra=%d sold=%d real=%d balance=%d creator=%s treasury=%s tax=%d", cbps, e.c.U(e.scid, "phase"), e.c.U(e.scid, "rd"), e.c.U(e.scid, "ra"), e.c.U(e.scid, "sold"), e.c.U(e.scid, "real"), e.c.SCBalance(e.scid, zero), e.c.S(e.scid, "creator")[:12], e.c.S(e.scid, "treasury")[:12], e.c.U(e.scid, "tax"))
		if r.OK() {
			rs := e.c.Call(e.scid, "SwapAssetForDero", rpc.Arguments{harness.U("min_out", 0), harness.U("publish", 0)}, e.in(0, 1000), e.alice)
			t.Logf("cbps=%d alice sell after reinit OK=%v err=%v", cbps, rs.OK(), rs.Err)
		}
	}
}
