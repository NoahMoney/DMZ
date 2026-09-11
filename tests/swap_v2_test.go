package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// swap.bas v2 (docs/ATOMIC_SWAP_STUDY.md §2.2, Z-C): Claim pays the taker Lock named, whoever sends it. The secret is
// revealed by the Claim either way, so a relayer, the maker's own companion or the taker's second device can complete
// the taker's leg the moment s is public on Zcash — and a taker who is away when the maker claims the ZEC late no
// longer loses her DERO leg to the maker's Refund at expiry. Who presses the button changes; who is paid does not.
// Every lock names its taker (GAP-024, tests/swap_gaps_test.go), so no Claim pays its sender.

// swapV2 installs swap.bas and returns helpers bound to it, with a fixed 32-byte secret and its canonical hash.
func swapV2(t *testing.T) (c *harness.Chain, scid crypto.Hash, secret, hash string,
	lock func(taker string, expiry uint64, asset string, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result,
	claim func(id uint64, secret string, who *rpc.Address) harness.Result,
	refund func(id uint64, who *rpc.Address) harness.Result, maker *rpc.Address) {
	t.Helper()
	c = harness.New()
	maker = c.NewAccount()
	code := readContract(t, "swap.bas")
	var res harness.Result
	scid, res = c.Install(code, nil, swapInit(), maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("swap.bas: %d bytes installed, install storage gas %d of %d", len(code), res.GasStorage, harness.MaxStorageGas)
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d does not fit the %d cap", res.GasStorage, harness.MaxStorageGas)
	}
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(0xa0 + i)
	}
	secret = hex.EncodeToString(raw[:])
	sum := sha256.Sum256(raw[:])
	hash = hex.EncodeToString(sum[:])
	lock = func(taker string, expiry uint64, asset string, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
		return c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hash), harness.S("taker", taker), harness.U("expiry", expiry), harness.S("asset", asset)}, in, who)
	}
	claim = func(id uint64, secret string, who *rpc.Address) harness.Result {
		return c.Call(scid, "Claim", rpc.Arguments{harness.U("id", id), harness.S("secret", secret)}, nil, who)
	}
	refund = func(id uint64, who *rpc.Address) harness.Result {
		return c.Call(scid, "Refund", rpc.Arguments{harness.U("id", id)}, nil, who)
	}
	return
}

func TestSwapV2ClaimPaysTheNamedTakerWhoeverSendsIt(t *testing.T) {
	c, scid, secret, _, lock, claim, refund, maker := swapV2(t)
	taker, relayer := c.NewAccount(), c.NewAccount()
	wrong := strings.Repeat("00", 32)

	// A DERO lock for the taker. A relayer — anyone who has read s off the maker's Zcash claim — sends the Claim.
	exp := c.Height + 10
	if r := lock(taker.String(), exp, "", map[crypto.Hash]uint64{zero: 100000}, maker); !r.OK() {
		t.Fatalf("lock: %v", r.Err)
	}
	if r := claim(0, wrong, relayer); r.OK() {
		t.Fatal("a relayer's Claim with a wrong secret was accepted")
	}
	r := claim(0, secret, relayer)
	if !r.OK() {
		t.Fatalf("a third party's Claim with the right secret was refused: %v", r.Err)
	}
	if got := r.Sent(zero, taker); got != 100000 {
		t.Fatalf("the named taker received %d of 100000", got)
	}
	if got := r.Sent(zero, relayer); got != 0 || len(r.Transfers) != 1 {
		t.Fatalf("the relayer who sent the Claim received %d (%d transfers): only the taker may be paid", got, len(r.Transfers))
	}
	if c.U(scid, "st_0") != 1 || c.S(scid, "x_0") != secret || c.SCBalance(scid, zero) != 0 {
		t.Fatalf("after the claim: st_0 %d x_0 %q balance %d", c.U(scid, "st_0"), c.S(scid, "x_0"), c.SCBalance(scid, zero))
	}
	// Nothing pays twice: not to the taker, not to the relayer, and not back to the maker at expiry.
	if r := claim(0, secret, taker); r.OK() {
		t.Fatal("the taker claimed a lock a relayer had already claimed for her")
	}
	if r := claim(0, secret, relayer); r.OK() {
		t.Fatal("double claim by the relayer")
	}
	for c.Height < exp {
		c.NextBlock()
	}
	if r := refund(0, maker); r.OK() {
		t.Fatal("the maker refunded a lock that had been claimed")
	}

	// An asset lock for the taker, claimed by the maker's own companion (the study's plan: it sends the DERO Claim
	// straight after broadcasting its Zcash claim). The maker is paid nothing by it.
	var asset crypto.Hash
	asset[0] = 0x5a
	exp = c.Height + 10
	if r := lock(taker.String(), exp, hex.EncodeToString(asset[:]), map[crypto.Hash]uint64{asset: 7000}, maker); !r.OK() {
		t.Fatalf("asset lock: %v", r.Err)
	}
	r = claim(1, secret, maker)
	if !r.OK() || r.Sent(asset, taker) != 7000 || r.Sent(asset, maker) != 0 || c.U(scid, "st_1") != 1 || c.SCBalance(scid, asset) != 0 {
		t.Fatalf("the maker's Claim of an asset lock for the taker: %v; taker got %d, maker got %d, st_1 %d, left %d",
			r.Err, r.Sent(asset, taker), r.Sent(asset, maker), c.U(scid, "st_1"), c.SCBalance(scid, asset))
	}

	if v := c.U(scid, "v"); v != 2 {
		t.Fatalf("swap version %d, want 2", v)
	}
}

// Everything the relayer could try to bend stays where v1 had it: the expiry, the secret, the refund, and the value.
func TestSwapV2EveryOtherRuleHolds(t *testing.T) {
	c, scid, secret, _, lock, claim, refund, maker := swapV2(t)
	taker, relayer := c.NewAccount(), c.NewAccount()

	// After expiry the right secret is refused from anyone, and only the maker refunds.
	exp := c.Height + 3
	if r := lock(taker.String(), exp, "", map[crypto.Hash]uint64{zero: 50000}, maker); !r.OK() {
		t.Fatalf("lock: %v", r.Err)
	}
	for c.Height < exp {
		c.NextBlock()
	}
	for _, who := range []*rpc.Address{relayer, taker, maker} {
		if r := claim(0, secret, who); r.OK() {
			t.Fatal("a Claim after expiry was accepted")
		}
	}
	for _, who := range []*rpc.Address{relayer, taker} {
		if r := refund(0, who); r.OK() {
			t.Fatal("someone other than the maker refunded")
		}
	}
	if r := refund(0, maker); !r.OK() || r.Sent(zero, maker) != 50000 || c.U(scid, "st_0") != 2 {
		t.Fatalf("refund: %v sent %d", r.Err, r.Sent(zero, maker))
	}

	// A wrong secret, and a secret that is not 32 bytes, are refused from the relayer and from the taker alike.
	exp = c.Height + 10
	if r := lock(taker.String(), exp, "", map[crypto.Hash]uint64{zero: 20000}, maker); !r.OK() {
		t.Fatalf("lock 1: %v", r.Err)
	}
	for _, who := range []*rpc.Address{relayer, taker} {
		if r := claim(1, strings.Repeat("11", 32), who); r.OK() {
			t.Fatal("a wrong secret was accepted")
		}
		if r := claim(1, secret[:62], who); r.OK() {
			t.Fatal("a 31-byte secret was accepted")
		}
	}
	// DERO attached to a Claim is still refused: the call carries no value, whoever sends it.
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 1), harness.S("secret", secret)}, map[crypto.Hash]uint64{zero: 1}, relayer); r.OK() {
		t.Fatal("a Claim carrying DERO was accepted")
	}
	// And at ring size above 2 there is no signer at all: still refused, as the study requires (the check stays).
	if r := claim(1, secret, nil); r.OK() {
		t.Fatal("an anonymous Claim was accepted")
	}
	if c.U(scid, "st_1") != 0 || c.SCBalance(scid, zero) != 20000 {
		t.Fatalf("a refused Claim moved state: st_1 %d balance %d", c.U(scid, "st_1"), c.SCBalance(scid, zero))
	}

	// A taker whose address was never registered cannot be paid: DERO refuses a contract's transfer to it, so the
	// Claim — from anyone — is refused and reverted, and nothing is lost. The lock stays claimable until expiry and
	// refundable after it. The contract cannot tell a registered address from an unregistered one without paying
	// to find out (tests/vault_repair_test.go), so the maker's companion must refuse to lock for an unregistered taker.
	ghost := rpc.NewAddressFromKeys(crypto.GPoint.ScalarMult(crypto.RandomScalarBNRed()))
	exp2 := c.Height + 5
	if r := lock(ghost.String(), exp2, "", map[crypto.Hash]uint64{zero: 40000}, maker); !r.OK() {
		t.Fatalf("lock for an unregistered taker: %v", r.Err)
	}
	if r := claim(2, secret, relayer); r.OK() {
		t.Fatal("a Claim paying an unregistered taker succeeded")
	} else {
		t.Logf("a Claim paying an unregistered taker is refused and reverted: %v", r.Err)
	}
	if c.U(scid, "st_2") != 0 || c.Exists(scid, "x_2") {
		t.Fatal("the refused Claim for an unregistered taker still wrote state")
	}
	for c.Height < exp2 {
		c.NextBlock()
	}
	if r := refund(2, maker); !r.OK() || r.Sent(zero, maker) != 40000 {
		t.Fatalf("the maker could not refund the lock for an unregistered taker: %v", r.Err)
	}
	for c.Height < c.U(scid, "e_1") {
		c.NextBlock()
	}
	if r := refund(1, maker); !r.OK() || r.Sent(zero, maker) != 20000 {
		t.Fatalf("refund 1: %v", r.Err)
	}
	if b := c.SCBalance(scid, zero); b != 0 {
		t.Fatalf("the escrow kept %d", b)
	}
}
