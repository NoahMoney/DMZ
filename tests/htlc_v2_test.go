package tests

import (
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// GAP-027, closed by htlc.bas v2: an exit refuses the lock's own asset attached to the call, as it already refused
// DERO. v1 accepted it, and since no exit pays more than v_<id>, what was attached stayed in the contract for ever.
// Refused, the call's value goes back to its ring-2 signer; the exits with nothing attached still work, and nothing
// is left behind.
func TestHtlcExitsRefuseAnAttachedAsset(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	var asset crypto.Hash
	asset[0] = 9
	assetHex := hex.EncodeToString(asset[:])
	units := map[crypto.Hash]uint64{asset: 5000}
	extra := map[crypto.Hash]uint64{asset: 7}
	t1, t2 := c.Height+50, c.Height+100
	for i := 0; i < 3; i++ {
		mustOK(t, e.lock(t1, t2, assetHex, units, e.bob), "asset lock "+strconv.Itoa(i))
	}
	c.Height = t1 - 1
	if r := e.claim(0, e.sa, e.kb, extra, e.alice); r.OK() {
		t.Fatal("a claim with the lock's asset attached was accepted")
	}
	e.open(t, 0, asset, 5000)
	c.Height = t1
	if r := e.refund(1, e.sb, extra, e.bob); r.OK() {
		t.Fatal("a refund with the lock's asset attached was accepted")
	}
	e.open(t, 1, asset, 5000)
	c.Height = t2
	if r := e.punish(2, extra, e.carol); r.OK() {
		t.Fatal("a punish with the lock's asset attached was accepted")
	}
	e.open(t, 2, asset, 5000)
	if b := c.SCBalance(e.scid, asset); b != 15000 {
		t.Fatalf("the contract holds %d of the asset after three refused exits, want the three locks' 15000", b)
	}
	c.Height = t1 - 1
	mustOK(t, e.claim(0, e.sa, e.kb, nil, e.alice), "claim")
	c.Height = t1
	mustOK(t, e.refund(1, e.sb, nil, e.bob), "refund")
	c.Height = t2
	mustOK(t, e.punish(2, nil, e.carol), "punish")
	e.empty(t, asset)
}

// LIV-07, closed by htlc.bas v2. In v1 only the taker could send Punish, and Claim and Refund have both closed by
// t2, so a lock the taker never punished — key lost, walked away, or worth less than her fee — was frozen for ever:
// the audit reproduced 100,000 DERO still held ten million blocks past t2. Punish takes no secret, so letting anyone
// send it changes who may press the button and nothing else: the payment still goes to t_<id>, still only from t2,
// still with no DERO attached. This test fails on v1 at its first stranger's Punish.
func TestHtlcPunishByAnyonePaysTheTaker(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	t1, t2 := c.Height+50, c.Height+100
	dero := map[crypto.Hash]uint64{zero: 100000}
	for i := 0; i < 3; i++ {
		mustOK(t, e.lock(t1, t2, "", dero, e.bob), "lock "+strconv.Itoa(i))
	}
	// Before t2 nobody may send it: not a stranger, not the maker, not the taker. The maker's refund window and the
	// taker's claim window are where the lock belongs until then.
	for _, h := range []uint64{c.Height, t1 - 1, t1, t2 - 1} {
		c.Height = h
		for _, who := range []*rpc.Address{e.carol, e.bob, e.alice} {
			if r := e.punish(0, nil, who); r.OK() {
				t.Fatalf("punish at height %d (t2 %d) by %s accepted", h, t2, who.String()[:12])
			}
			e.open(t, 0, zero, 100000)
		}
	}
	c.Height = t2
	// The refusals that stay: DERO attached (the call must strand nothing), a call whose signer the chain cannot
	// name, and a lock that does not exist.
	for _, tc := range []refusal{
		{"a stranger's punish with DERO attached", func() harness.Result { return e.punish(0, dero, e.carol) }},
		{"a punish at ring size > 2", func() harness.Result { return e.punish(0, nil, nil) }},
		{"a stranger's punish of a lock that does not exist", func() harness.Result { return e.punish(9, nil, e.carol) }},
	} {
		if r := tc.call(); r.OK() {
			t.Fatalf("%s accepted", tc.what)
		}
		e.open(t, 0, zero, 100000)
	}
	// A stranger closes lock 0. The taker is paid; the stranger, who sent it, is paid nothing; so is the maker.
	r := e.punish(0, nil, e.carol)
	if !r.OK() || r.Sent(zero, e.alice) != 100000 || r.Sent(zero, e.carol) != 0 || r.Sent(zero, e.bob) != 0 {
		t.Fatalf("stranger's punish: %v; paid alice %d, carol %d, bob %d", r.Err, r.Sent(zero, e.alice), r.Sent(zero, e.carol), r.Sent(zero, e.bob))
	}
	if c.U(e.scid, "st_0") != 3 || c.Exists(e.scid, "xa_0") || c.Exists(e.scid, "xb_0") {
		t.Fatalf("after the stranger's punish: st %d", c.U(e.scid, "st_0"))
	}
	t.Logf("punish storage gas %d, compute gas %d", r.GasStorage, r.GasCompute)
	// The maker closes lock 1 against himself: it is still the taker's, and he gains nothing by it but a closed lock.
	if r := e.punish(1, nil, e.bob); !r.OK() || r.Sent(zero, e.alice) != 100000 || r.Sent(zero, e.bob) != 0 || c.U(e.scid, "st_1") != 3 {
		t.Fatalf("maker's punish: %v; paid alice %d, bob %d", r.Err, r.Sent(zero, e.alice), r.Sent(zero, e.bob))
	}
	// The taker's own punish is unchanged.
	if r := e.punish(2, nil, e.alice); !r.OK() || r.Sent(zero, e.alice) != 100000 || c.U(e.scid, "st_2") != 3 {
		t.Fatalf("taker's punish: %v", r.Err)
	}
	e.empty(t, zero)
	// Once closed, closed, whoever tries: no second payment, and Claim and Refund stay shut.
	for _, who := range []*rpc.Address{e.carol, e.bob, e.alice} {
		if r := e.punish(0, nil, who); r.OK() {
			t.Fatal("a second punish accepted")
		}
	}
	if r := e.refund(0, e.sb, nil, e.bob); r.OK() {
		t.Fatal("refund of a punished lock accepted")
	}
	if r := e.claim(0, e.sa, e.kb, nil, e.alice); r.OK() {
		t.Fatal("claim of a punished lock accepted")
	}
	e.empty(t, zero)

	// The audit's reproduction, inverted: a lock abandoned by both sides and ten million blocks past t2 is closed by
	// a stranger, in the taker's favour, and the contract is empty afterwards.
	t1b, t2b := c.Height+10, c.Height+20
	mustOK(t, e.lock(t1b, t2b, "", dero, e.bob), "lock 3")
	c.Height += 10000000
	if r := e.refund(3, e.sb, nil, e.bob); r.OK() {
		t.Fatal("the maker refunded ten million blocks past t2")
	}
	if r := e.punish(3, nil, e.carol); !r.OK() || r.Sent(zero, e.alice) != 100000 || r.Sent(zero, e.carol) != 0 {
		t.Fatalf("stranger's punish ten million blocks past t2: %v", r.Err)
	}
	e.empty(t, zero)

	// The asset leg: a stranger closes an asset lock and the taker receives the asset, not DERO.
	var asset crypto.Hash
	asset[0] = 9
	h := c.Height
	mustOK(t, e.lock(h+10, h+20, hex.EncodeToString(asset[:]), map[crypto.Hash]uint64{asset: 5000}, e.bob), "asset lock")
	c.Height = h + 20
	if r := e.punish(4, nil, e.carol); !r.OK() || r.Sent(asset, e.alice) != 5000 || r.Sent(asset, e.carol) != 0 || r.Sent(zero, e.alice) != 0 {
		t.Fatalf("stranger's punish of an asset lock: %v; alice %d, carol %d", r.Err, r.Sent(asset, e.alice), r.Sent(asset, e.carol))
	}
	e.empty(t, asset)
}
