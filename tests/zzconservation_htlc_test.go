package tests

// MT-A13 for htlc.bas, beside tests/zzconservation_test.go and in the same style: per asset, the contract holds
// exactly the value of its open locks after every call, accepted or refused, over a seeded random sequence of Lock,
// Claim, Refund and Punish from several wallets. The sequence mixes:
// - right and wrong secrets;
// - the right caller and a stranger;
// - calls inside and outside each window (Claim before t1, Refund in [t1, t2), Punish from t2);
// - DERO, or the lock's own asset, attached to an exit that takes none (GAP-027).
// A lock may be refused or may exit at most once; a call that kept stray value would break the equality this checks.

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestConservationHtlc(t *testing.T) {
	c := harness.New()
	users := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()}
	asset, res := c.Install(assetCode, nil, nil, users[0], harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	for _, u := range users {
		if r := c.Call(asset, "Mint", rpc.Arguments{harness.U("amount", 1<<40)}, nil, u); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	scid, res := c.Install(readContract(t, "htlc.bas"), nil, nil, users[0], harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	type keys struct{ sa, kb, sb string }
	secrets := map[uint64]keys{}
	var y consTally
	rng := rand.New(rand.NewSource(19))
	step := 0
	check := func(what string) {
		t.Helper()
		var dero, held uint64
		for i := uint64(0); i < c.U(scid, "n"); i++ {
			if c.U(scid, "st_"+itoa(i)) != 0 {
				continue
			}
			if v := c.U(scid, "v_"+itoa(i)); c.S(scid, "a_"+itoa(i)) == "" {
				dero += v
			} else {
				held += v
			}
		}
		if c.SCBalance(scid, zero) != dero || c.SCBalance(scid, asset) != held {
			t.Fatalf("step %d %s: htlc.bas holds %d DERO and %d of the asset; its open locks are %d and %d",
				step, what, c.SCBalance(scid, zero), c.SCBalance(scid, asset), dero, held)
		}
	}
	call := func(what, entry string, args rpc.Arguments, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
		t.Helper()
		r := c.Call(scid, entry, args, in, who)
		y.note(r)
		check(what)
		return r
	}
	secret := func() (string, string) {
		var s [32]byte
		rng.Read(s[:])
		h := sha256.Sum256(s[:])
		return hex.EncodeToString(s[:]), hex.EncodeToString(h[:])
	}
	// the user whose raw address is stored under key k of lock id, or a stranger when the coin says so
	party := func(k string, id uint64, fallback *rpc.Address) *rpc.Address {
		if m, ok := c.Var(scid, k+itoa(id)).(string); ok && rng.Intn(4) != 0 {
			for _, u := range users {
				if raw(u) == m {
					return u
				}
			}
		}
		return fallback
	}
	// value attached to an exit, which takes none: usually nothing, sometimes DERO, sometimes the lock's own asset
	stray := func(id uint64) map[crypto.Hash]uint64 {
		switch rng.Intn(8) {
		case 0:
			return consDero(1000)
		case 1:
			// Var, not S: the driver also names ids no lock has, and S panics on a missing key
			if a, ok := c.Var(scid, "a_"+itoa(id)).(string); ok && a != "" {
				return map[crypto.Hash]uint64{asset: 1}
			}
		}
		return nil
	}
	idOf := func() uint64 { return uint64(rng.Int63n(int64(c.U(scid, "n")) + 1)) }

	check("install")
	for step = 0; step < consSteps(); step++ {
		who := consPick(rng, users)
		switch rng.Intn(10) {
		case 0, 1, 2:
			sa, ha := secret()
			kb, hk := secret()
			sb, hb := secret()
			t1 := c.Height + 2 + uint64(rng.Intn(20))
			t2 := t1 + 1 + uint64(rng.Intn(20))
			if rng.Intn(10) == 0 {
				t2 = t1 // refused: t2 must be after t1
			}
			v := consUpTo(rng, 1_000_000)
			assetArg, in := "", map[crypto.Hash]uint64{zero: v}
			if rng.Intn(2) == 0 {
				assetArg, in = hex.EncodeToString(asset[:]), map[crypto.Hash]uint64{asset: v}
				if rng.Intn(6) == 0 {
					in[zero] = 1 // refused: an asset lock with DERO attached
				}
			}
			taker := consPick(rng, users).String()
			id := c.U(scid, "n")
			r := call("lock", "Lock", rpc.Arguments{harness.S("ha", ha), harness.S("hk", hk), harness.S("hb", hb),
				harness.S("taker", taker), harness.U("t1", t1), harness.U("t2", t2), harness.S("asset", assetArg)}, in, who)
			if r.OK() {
				secrets[id] = keys{sa, kb, sb}
			}
		case 3, 4:
			id := idOf()
			k := secrets[id]
			sa, kb := k.sa, k.kb
			if rng.Intn(5) == 0 {
				kb = strings.Repeat("11", 32) // refused: a wrong gate secret
			}
			call("claim", "Claim", rpc.Arguments{harness.U("id", id), harness.S("sa", sa), harness.S("kb", kb)}, stray(id), party("t_", id, who))
		case 5:
			id := idOf()
			sb := secrets[id].sb
			if rng.Intn(5) == 0 {
				sb = strings.Repeat("33", 32) // refused: a wrong key share
			}
			call("refund", "Refund", rpc.Arguments{harness.U("id", id), harness.S("sb", sb)}, stray(id), party("m_", id, who))
		case 6:
			id := idOf()
			call("punish", "Punish", rpc.Arguments{harness.U("id", id)}, stray(id), who) // anyone may send it (LIV-07)
		case 7:
			call("punish of no lock", "Punish", rpc.Arguments{harness.U("id", c.U(scid, "n")+5)}, nil, who)
		default:
			for i := rng.Intn(10); i >= 0; i-- {
				c.NextBlock()
			}
		}
	}
	y.require(t, "htlc.bas")
	var ended uint64
	for i := uint64(0); i < c.U(scid, "n"); i++ {
		if c.U(scid, "st_"+itoa(i)) != 0 {
			ended++
		}
	}
	if ended == 0 {
		t.Fatal("no lock ever exited, so the exits' conservation was not exercised")
	}
	t.Logf("htlc.bas: %d locks made, %d exited", c.U(scid, "n"), ended)
}
