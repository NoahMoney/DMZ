package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// swapTestWmin is the minimum escrow width the scenario tests install swap.bas with: small, so their few-block
// expiries stay legal. TestSwapInstallNamesAMinimumEscrowWidth tests the rule itself at the market's 1,600.
const swapTestWmin = 3

// swapInit is swap.bas's install argument list for the scenario tests.
func swapInit() rpc.Arguments { return rpc.Arguments{harness.U("wmin", swapTestWmin)} }

// swapSecret is a fixed 32-byte secret as 64 hex characters, and its canonical (lower-case) SHA-256.
func swapSecret() (secret, hash string) {
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(0x30 + i)
	}
	sum := sha256.Sum256(raw[:])
	return hex.EncodeToString(raw[:]), hex.EncodeToString(sum[:])
}

func swapLockArgs(hash, taker string, expiry uint64, asset string) rpc.Arguments {
	return rpc.Arguments{harness.S("hash", hash), harness.S("taker", taker), harness.U("expiry", expiry), harness.S("asset", asset)}
}

// GAP-025 / Z-D (docs/ATOMIC_SWAP_STUDY.md §2.2): swap.bas takes its minimum escrow width at install, stores it as
// wmin, and Lock refuses an expiry closer than that. Before, a short-expiry lock — the one that lets a maker claim the
// ZEC late and refund the DERO before the taker can claim — was refused only by the taker's companion. The install
// fails closed: without the argument the DVM refuses the call (vendor/derohe/dvm/sc.go:232-234), with 0 Initialize
// refuses it, and a refused Initialize installs nothing (vendor/derohe/blockchain/transaction_execute.go:400-406
// returns before ProcessExternal). A width past the million-block expiry ceiling would leave no legal lock at all.
func TestSwapInstallNamesAMinimumEscrowWidth(t *testing.T) {
	c := harness.New()
	maker, taker := c.NewAccount(), c.NewAccount()
	code := readContract(t, "swap.bas")
	for _, tc := range []struct {
		name string
		args rpc.Arguments
	}{
		{"no wmin argument", nil},
		{"wmin 0", rpc.Arguments{harness.U("wmin", 0)}},
		{"wmin 1,000,001, past the expiry ceiling", rpc.Arguments{harness.U("wmin", 1_000_001)}},
	} {
		if _, res := c.Install(code, nil, tc.args, maker, harness.MaxStorageGas); res.OK() {
			t.Fatalf("install with %s succeeded: a deployment that left out the width would have none", tc.name)
		} else {
			t.Logf("install with %s refused: %v", tc.name, res.Err)
		}
	}
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("wmin", 1600)}, maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install with wmin 1600: %v", res.Err)
	}
	t.Logf("swap.bas: %d bytes installed, install storage gas %d of %d", len(code), res.GasStorage, harness.MaxStorageGas)
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d does not fit the %d cap", res.GasStorage, harness.MaxStorageGas)
	}
	if w := c.U(scid, "wmin"); w != 1600 {
		t.Fatalf("wmin %d, want 1600", w)
	}
	_, hash := swapSecret()
	dero := map[crypto.Hash]uint64{zero: 100000}
	if r := c.Call(scid, "Lock", swapLockArgs(hash, taker.String(), c.Height+1599, ""), dero, maker); r.OK() {
		t.Fatal("a lock one block narrower than wmin was accepted")
	}
	if c.U(scid, "n") != 0 || c.SCBalance(scid, zero) != 0 {
		t.Fatal("the refused lock left state or value behind")
	}
	if r := c.Call(scid, "Lock", swapLockArgs(hash, taker.String(), c.Height+1600, ""), dero, maker); !r.OK() || c.U(scid, "n") != 1 {
		t.Fatalf("a lock exactly wmin wide was refused: %v", r.Err)
	}
	if r := c.Call(scid, "Lock", swapLockArgs(hash, taker.String(), c.Height+1_000_001, ""), dero, maker); r.OK() {
		t.Fatal("the million-block expiry ceiling no longer holds")
	}
	// Initialize cannot be called again to lower it.
	for _, who := range []*rpc.Address{maker, taker} {
		if r := c.Call(scid, "Initialize", rpc.Arguments{harness.U("wmin", 1)}, nil, who); r.OK() {
			t.Fatal("Initialize ran again after install")
		}
	}
	if c.U(scid, "wmin") != 1600 {
		t.Fatalf("wmin changed after install: %d", c.U(scid, "wmin"))
	}
}

// GAP-024: a lock must name its taker, as htlc.bas does. A lock that named nobody paid whoever sent the first Claim,
// and once the maker's Zcash claim has put s in public that is any observer.
func TestSwapLockRequiresATaker(t *testing.T) {
	c := harness.New()
	maker, taker := c.NewAccount(), c.NewAccount()
	scid, res := c.Install(readContract(t, "swap.bas"), nil, swapInit(), maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	_, hash := swapSecret()
	var asset crypto.Hash
	asset[0] = 0x61
	exp := c.Height + 10
	if r := c.Call(scid, "Lock", swapLockArgs(hash, "", exp, ""), map[crypto.Hash]uint64{zero: 100000}, maker); r.OK() {
		t.Fatal("a DERO lock naming no taker was accepted: the first Claim after s went public would take it")
	}
	if r := c.Call(scid, "Lock", swapLockArgs(hash, "", exp, hex.EncodeToString(asset[:])), map[crypto.Hash]uint64{asset: 5000}, maker); r.OK() {
		t.Fatal("an asset lock naming no taker was accepted")
	}
	if c.U(scid, "n") != 0 || c.SCBalance(scid, zero) != 0 || c.SCBalance(scid, asset) != 0 {
		t.Fatal("a refused lock left state or value behind")
	}
	if r := c.Call(scid, "Lock", swapLockArgs(hash, taker.String(), exp, ""), map[crypto.Hash]uint64{zero: 100000}, maker); !r.OK() || c.S(scid, "t_0") != string(taker.Compressed()) {
		t.Fatalf("a lock naming its taker: %v", r.Err)
	}
}

// GAP-027: Claim and Refund refuse the lock's own asset attached to the call, as they already refused DERO. Before,
// it was accepted and, since no exit pays more than v_<id>, kept for ever. Refused, the call's value goes back to its
// ring-2 signer. The DVM reads ASSETVALUE only for an asset it is named, so an unrelated asset cannot be seen.
func TestSwapExitsRefuseTheLocksAssetAttached(t *testing.T) {
	c := harness.New()
	maker, taker := c.NewAccount(), c.NewAccount()
	scid, res := c.Install(readContract(t, "swap.bas"), nil, swapInit(), maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	secret, hash := swapSecret()
	var asset crypto.Hash
	asset[0] = 0x77
	units, extra := map[crypto.Hash]uint64{asset: 5000}, map[crypto.Hash]uint64{asset: 7}
	exp := c.Height + 10
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Lock", swapLockArgs(hash, taker.String(), exp, hex.EncodeToString(asset[:])), units, maker); !r.OK() {
			t.Fatalf("asset lock %d: %v", i, r.Err)
		}
	}
	claim := func(in map[crypto.Hash]uint64) harness.Result {
		return c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 0), harness.S("secret", secret)}, in, taker)
	}
	refund := func(in map[crypto.Hash]uint64) harness.Result {
		return c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 1)}, in, maker)
	}
	if r := claim(extra); r.OK() {
		t.Fatal("a Claim with the lock's asset attached was accepted: the attached units would stay in the contract for ever")
	}
	if c.U(scid, "st_0") != 0 || c.SCBalance(scid, asset) != 10000 {
		t.Fatalf("the refused Claim moved state: st_0 %d balance %d", c.U(scid, "st_0"), c.SCBalance(scid, asset))
	}
	if r := claim(nil); !r.OK() || r.Sent(asset, taker) != 5000 {
		t.Fatalf("claim with nothing attached: %v", r.Err)
	}
	c.Height = exp
	if r := refund(extra); r.OK() {
		t.Fatal("a Refund with the lock's asset attached was accepted")
	}
	if c.U(scid, "st_1") != 0 || c.SCBalance(scid, asset) != 5000 {
		t.Fatalf("the refused Refund moved state: st_1 %d balance %d", c.U(scid, "st_1"), c.SCBalance(scid, asset))
	}
	if r := refund(nil); !r.OK() || r.Sent(asset, maker) != 5000 {
		t.Fatalf("refund with nothing attached: %v", r.Err)
	}
	if b := c.SCBalance(scid, asset); b != 0 {
		t.Fatalf("the contract kept %d of the asset", b)
	}
}
