package zecswap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// DeroRPC against the fake daemon: the request shapes (entrypoint and argument names and types, ring size 2), the
// fixed fee for the calls the estimator cannot judge, reads by named keys, and a mined-but-refused call reported as
// a refusal.

// argsOf reads a transfer's sc_rpc list as name → (datatype, value as text). The daemon decodes with UseNumber,
// so a number arrives as a json.Number and prints as its digits.
func argsOf(p map[string]interface{}) map[string][2]string {
	out := map[string][2]string{}
	list, _ := p["sc_rpc"].([]interface{})
	for _, a := range list {
		m, _ := a.(map[string]interface{})
		out[jsonS(m["name"])] = [2]string{jsonS(m["datatype"]), fmt.Sprint(m["value"])}
	}
	return out
}

// lockWidth is wide enough for the fake escrow's wmin (swap.bas v2 refuses an expiry below height + wmin).
const lockWidth = 300

func TestDeroClientBoardAndEscrow(t *testing.T) {
	ctx := context.Background()
	c := newFakeDero(startDero)
	maker, md := newDeroClient(t, c, makerAddr)
	taker, td := newDeroClient(t, c, takerAddr)
	var secret [32]byte
	secret[0] = 1
	sum := sha256.Sum256(secret[:])
	hash := hex.EncodeToString(sum[:])
	want := testKey(makerKeyN).Address(Testnet)

	_, id, err := maker.Post(ctx, PostArgs{Chain: ZcashChainLabel, GiveAmount: 100, WantAmount: 200_000, WantAddr: want, Hash: hash, Expiry: startDero + 100})
	if err != nil || id != 0 {
		t.Fatalf("Post: %d %v", id, err)
	}
	p := md.last("transfer")
	a := argsOf(p)
	if jsonU(p["ringsize"]) != 2 || jsonS(p["scid"]) != testBoard || a["entrypoint"][1] != "Post" || a["hash"] != [2]string{"S", hash} ||
		a["giveAmount"][0] != "U" || a["wantAmount"] != [2]string{"U", "200000"} || a["expiry"][0] != "U" || a["chain"] != [2]string{"S", ZcashChainLabel} {
		t.Fatalf("Post request %v", p)
	}
	if jsonU(p["fees"]) != 700 {
		t.Fatalf("Post below height 1,000,000 is estimated (500 + 200), paid %d", jsonU(p["fees"]))
	}
	// found by its hash: nothing is sent twice
	before := len(md.requests)
	if txid, id2, err := maker.Post(ctx, PostArgs{Chain: ZcashChainLabel, GiveAmount: 100, WantAmount: 200_000, WantAddr: want, Hash: hash, Expiry: startDero + 100}); err != nil || txid != "" || id2 != 0 {
		t.Fatalf("a second Post: %q %d %v", txid, id2, err)
	}
	for _, r := range md.requests[before:] {
		if r["method"] == "transfer" {
			t.Fatal("a second Post was sent")
		}
	}
	o, err := maker.ReadOffer(ctx, 0)
	if err != nil || o.Hash != hash || o.WantAddr != want || o.Chain != ZcashChainLabel || o.State != OfferOpen || o.Taker != "" || o.CLTV != 0 {
		t.Fatalf("offer %+v %v", o, err)
	}
	makerRaw, _ := DeroRaw(makerAddr)
	takerRaw, _ := DeroRaw(takerAddr)
	if o.Maker != makerRaw {
		t.Fatalf("maker %s, want %s (lower-case hex of the raw key)", o.Maker, makerRaw)
	}

	rp := hex.EncodeToString(make([]byte, 20))
	if _, err := taker.Accept(ctx, 0, rp, 4_200_032); err != nil {
		t.Fatal(err)
	}
	a = argsOf(td.last("transfer"))
	if a["entrypoint"][1] != "Accept" || a["refundPkh"] != [2]string{"S", rp} || a["cltv"] != [2]string{"U", "4200032"} || a["id"][0] != "U" {
		t.Fatalf("Accept request %v", a)
	}
	o, _ = taker.ReadOffer(ctx, 0)
	if o.State != OfferAccepted || o.Taker != takerRaw || o.RefundPkh != rp || o.CLTV != 4_200_032 {
		t.Fatalf("accepted offer %+v", o)
	}

	tAddr, err := DeroAddress(o.Taker, false)
	if err != nil || tAddr != takerAddr {
		t.Fatalf("the board's raw taker back to an address: %s %v", tAddr, err)
	}
	_, eid, err := maker.Lock(ctx, hash, tAddr, c.Height()+lockWidth, "", testGive)
	if err != nil || eid != 0 {
		t.Fatalf("Lock %d %v", eid, err)
	}
	p = md.last("transfer")
	a = argsOf(p)
	xf, _ := p["transfers"].([]interface{})
	burn := jsonU(xf[0].(map[string]interface{})["burn"])
	if jsonS(p["scid"]) != testEscrow || a["entrypoint"][1] != "Lock" || a["taker"] != [2]string{"S", takerAddr} || a["asset"] != [2]string{"S", ""} || burn != testGive {
		t.Fatalf("Lock request %v", p)
	}
	e, err := taker.ReadEscrow(ctx, 0)
	if err != nil || e.Maker != makerRaw || e.Taker != takerRaw || e.Hash != hash || e.Amount != testGive || e.State != EscrowOpen || e.Asset != "" {
		t.Fatalf("escrow %+v %v", e, err)
	}
	if found, ok, err := taker.FindEscrow(ctx, makerRaw, takerRaw, hash); err != nil || !ok || found.ID != 0 {
		t.Fatalf("FindEscrow %+v %v %v", found, ok, err)
	}
	// a lock narrower than the escrow's wmin passes the estimator (it runs at height 0) and is refused on chain;
	// the read-back says so rather than reporting a lock
	if _, _, err := maker.Lock(ctx, hash, otherAddr(7), c.Height()+50, "", 1); err == nil || !strings.Contains(err.Error(), "recorded no lock") {
		t.Fatalf("a lock below wmin: %v", err)
	}

	// a wrong secret is refused by the estimator: nothing is sent
	before = len(td.requests)
	if _, err := taker.Claim(ctx, 0, strings.Repeat("00", 32)); !errors.Is(err, ErrEstimate) {
		t.Fatalf("a wrong secret: %v", err)
	}
	for _, r := range td.requests[before:] {
		if r["method"] == "transfer" {
			t.Fatal("a Claim with a wrong secret was sent")
		}
	}
	if _, err := taker.Claim(ctx, 0, hex.EncodeToString(secret[:])); err != nil {
		t.Fatal(err)
	}
	e, _ = taker.ReadEscrow(ctx, 0)
	if e.State != EscrowClaimed || e.Secret != hex.EncodeToString(secret[:]) {
		t.Fatalf("claimed escrow %+v", e)
	}

	// Refund is height-gated: never estimated, always the fixed fee, and a refusal is reported as one
	_, eid2, err := maker.Lock(ctx, hash, otherAddr(5), c.Height()+lockWidth, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	before = len(md.requests)
	if _, err := maker.Refund(ctx, eid2); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a Refund before expiry: %v", err)
	}
	for _, r := range md.requests[before:] {
		if r["method"] == "DERO.GetGasEstimate" {
			t.Fatal("Refund was estimated")
		}
	}
	if fee := jsonU(md.last("transfer")["fees"]); fee != maker.FixedFee {
		t.Fatalf("Refund paid %d, the fixed fee is %d", fee, maker.FixedFee)
	}
	c.Advance(lockWidth + 5)
	if _, err := maker.Refund(ctx, eid2); err != nil {
		t.Fatal(err)
	}
	// Cancel of an accepted offer, likewise
	c.Advance(100)
	if _, err := maker.Cancel(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if fee := jsonU(md.last("transfer")["fees"]); fee != maker.FixedFee {
		t.Fatalf("Cancel paid %d", fee)
	}
}

// Above height 1,000,000 — every height on DERO mainnet — the estimator refuses every Post and Lock, because it runs
// the contract at height 0 against "expiry > height + 1000000"; they go out with the fixed fee.
func TestDeroPostAndLockAreFeeFixedAboveTheEstimableExpiry(t *testing.T) {
	ctx := context.Background()
	c := newFakeDero(2_000_000)
	maker, md := newDeroClient(t, c, makerAddr)
	var h [32]byte
	h[0] = 3
	hash := hex.EncodeToString(h[:])
	if _, _, err := maker.Post(ctx, PostArgs{Chain: ZcashChainLabel, GiveAmount: 1, WantAmount: 200_000, WantAddr: "tm", Hash: hash, Expiry: 2_000_100}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := maker.Lock(ctx, hash, takerAddr, 2_001_600, "", 7); err != nil {
		t.Fatal(err)
	}
	md.mu.Lock()
	defer md.mu.Unlock()
	for _, r := range md.requests {
		if r["method"] == "DERO.GetGasEstimate" {
			t.Fatal("a Post or Lock above the estimable expiry was estimated")
		}
		if r["method"] == "transfer" && jsonU(r["params"].(map[string]interface{})["fees"]) != maker.FixedFee {
			t.Fatal("a Post or Lock above the estimable expiry did not pay the fixed fee")
		}
	}
}

// An asset lock burns the asset through a transfer naming its SCID, and the escrow reads it back as hex.
func TestDeroLockOfAnAsset(t *testing.T) {
	ctx := context.Background()
	c := newFakeDero(startDero)
	maker, md := newDeroClient(t, c, makerAddr)
	asset := strings.Repeat("ad", 32)
	var h [32]byte
	hash := hex.EncodeToString(h[:])
	if _, _, err := maker.Lock(ctx, hash, takerAddr, startDero+lockWidth, asset, 9); err != nil {
		t.Fatal(err)
	}
	xf, _ := md.last("transfer")["transfers"].([]interface{})
	if jsonS(xf[0].(map[string]interface{})["scid"]) != asset {
		t.Fatal("the asset transfer does not name the SCID")
	}
	if e, _ := maker.ReadEscrow(ctx, 0); e.Asset != asset || e.Amount != 9 {
		t.Fatalf("asset escrow %+v", e)
	}
}

// Reads ask for named keys and nothing else, so a contract that gains keys changes nothing a reader sees unless this
// package names them; and a key not yet set is simply absent.
func TestDeroReadsAreByNamedKeys(t *testing.T) {
	ctx := context.Background()
	c := newFakeDero(startDero)
	maker, md := newDeroClient(t, c, makerAddr)
	md.extra = map[string]string{"zz_0": "4242", "py_0": "ABCD"}
	var h [32]byte
	if _, _, err := maker.Post(ctx, PostArgs{Chain: ZcashChainLabel, GiveAmount: 1, WantAmount: 200_000, WantAddr: "tm", Hash: hex.EncodeToString(h[:]), Expiry: startDero + 10}); err != nil {
		t.Fatal(err)
	}
	o, err := maker.ReadOffer(ctx, 0)
	if err != nil || o.AcceptDeadline != 0 {
		t.Fatalf("an open offer: %+v %v", o, err)
	}
	asked, _ := md.last("DERO.GetSC")["keysstring"].([]interface{})
	want := offerKeys(0)
	if len(asked) != len(want) {
		t.Fatalf("asked for %v", asked)
	}
	for i := range want {
		if jsonS(asked[i]) != want[i] {
			t.Fatalf("asked for %v, want %v", asked, want)
		}
	}
	if _, err := maker.ReadOffer(ctx, 7); !errors.Is(err, ErrNoSuchOffer) {
		t.Fatalf("an offer that does not exist: %v", err)
	}
}

func TestDeroAddressRoundTrip(t *testing.T) {
	for _, a := range []string{makerAddr, takerAddr, otherAddr(9)} {
		raw, err := DeroRaw(a)
		if err != nil {
			t.Fatal(err)
		}
		back, err := DeroAddress(raw, false)
		if err != nil || back != a {
			t.Fatalf("%s → %s → %s %v", a, raw, back, err)
		}
		main, _ := DeroAddress(raw, true)
		if !strings.HasPrefix(main, "dero1") {
			t.Fatalf("mainnet form %s", main)
		}
		if r2, _ := DeroRaw(main); r2 != raw {
			t.Fatal("the mainnet form names another key")
		}
	}
	if _, err := DeroAddress("abcd", false); err == nil {
		t.Fatal("a short key was encoded")
	}
}
