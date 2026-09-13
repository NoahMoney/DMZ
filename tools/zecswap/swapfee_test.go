package zecswap

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The 0.5 % swap fee (fee.go): the arithmetic, zebrad's dust rule, the treasury address, the two-output claim and its
// digests, the refund that never pays it, and the mainnet refusal.

// floor(value × 50 / 10,000), exact at every size and never overflowing.
func TestSwapFeeArithmetic(t *testing.T) {
	for _, c := range []struct{ v, fee uint64 }{
		{0, 0}, {199, 0}, {200, 1}, {399, 1}, {12_345, 61}, {10_799, 53}, {10_800, 54},
		{100_000, 500}, {200_000, 1_000}, {MaxMoney, MaxMoney / 200}, {^uint64(0), ^uint64(0) / 200},
	} {
		if got := SwapFee(c.v); got != c.fee {
			t.Errorf("SwapFee(%d) = %d, want %d", c.v, got, c.fee)
		}
	}
	// the same as the formula with the product, wherever the product fits
	for v := uint64(0); v < 60_000; v += 7 {
		if SwapFee(v) != v*SwapFeeBps/BpsDenominator {
			t.Fatalf("SwapFee(%d) = %d, the formula %d", v, SwapFee(v), v*SwapFeeBps/BpsDenominator)
		}
	}
}

// zebrad 6.3.0 (zebra-chain/src/transparent.rs, Output::is_dust): 3 × (100 × (size + 148) / 1,000), integer division.
func TestDustThresholdIsZebrads(t *testing.T) {
	p2pkh := TxOut{ScriptPubKey: P2PKHScript([20]byte{1})}
	p2sh := TxOut{ScriptPubKey: P2SHScript([20]byte{1})}
	if txOutSize(p2pkh) != 34 || txOutSize(p2sh) != 32 {
		t.Fatalf("serialized sizes %d and %d, want 34 and 32", txOutSize(p2pkh), txOutSize(p2sh))
	}
	if DustThreshold(p2pkh) != 54 || DustThreshold(p2sh) != 54 {
		t.Fatalf("thresholds %d and %d, want 54 for both", DustThreshold(p2pkh), DustThreshold(p2sh))
	}
	// the size counts: a 100-byte script is a 109-byte output, 3 × (25,700 / 1,000) = 75
	if got := DustThreshold(TxOut{ScriptPubKey: make([]byte, 100)}); got != 75 {
		t.Fatalf("a 109-byte output's threshold %d, want 75", got)
	}
	for _, c := range []struct {
		v    uint64
		dust bool
	}{{0, true}, {53, true}, {54, false}, {1_000, false}} {
		o := p2sh
		o.Value = c.v
		if IsDust(o) != c.dust {
			t.Errorf("IsDust(%d) = %v", c.v, !c.dust)
		}
	}
	// the fee output is left out exactly when it would be dust
	if o, ok := TreasuryOutput(10_799, p2pkh.ScriptPubKey); ok || o.Value != 53 {
		t.Fatalf("a %d-zatoshi fee output was kept", o.Value)
	}
	if o, ok := TreasuryOutput(10_800, p2pkh.ScriptPubKey); !ok || o.Value != 54 {
		t.Fatalf("a %d-zatoshi fee output was left out", o.Value)
	}
	// with today's floors a claim that can be built at all spends at least 15,000 zatoshi, whose fee is not dust
	if SwapFee(MinimumFee+OutputFloor) < DustThreshold(p2sh) {
		t.Fatal("a claim that can be built would drop its fee: fee.go's note is wrong")
	}
}

// The treasury is a transparent address on the swap's network, P2PKH or P2SH; nothing else is accepted.
func TestTreasuryAddressValidation(t *testing.T) {
	hash := [20]byte{0xd3, 0x2a}
	for _, c := range []struct {
		addr string
		net  Network
		want []byte
	}{
		{EncodeAddress(Mainnet, P2PKH, hash), Mainnet, P2PKHScript(hash)},
		{EncodeAddress(Mainnet, P2SH, hash), Mainnet, P2SHScript(hash)},
		{EncodeAddress(Testnet, P2PKH, hash), Testnet, P2PKHScript(hash)},
		{EncodeAddress(Testnet, P2SH, hash), Testnet, P2SHScript(hash)},
		{EncodeAddress(Testnet, P2PKH, hash), Mainnet, nil},
		{EncodeAddress(Mainnet, P2SH, hash), Testnet, nil},
		{"", Mainnet, nil},
		{"zs1z7rejlpsa98s2rrrfkwmaxu53e4ue0ulcrw0h4x5g8jl04tak0d3mm47vdtahatqrlkngh9sly", Mainnet, nil},
		{"u1qqqqqqqqqqqqqqqqqqqqqqqqqqq", Mainnet, nil},
		{"t1notanaddressatall", Mainnet, nil},
		{" " + EncodeAddress(Mainnet, P2PKH, hash), Mainnet, nil},
	} {
		got, err := TreasuryScript(c.addr, c.net)
		if c.want == nil {
			if err == nil || !strings.Contains(err.Error(), TreasuryField) {
				t.Errorf("%q on %s: accepted, or refused without naming %s: %v", c.addr, c.net, TreasuryField, err)
			}
			continue
		}
		if err != nil || !bytes.Equal(got, c.want) {
			t.Errorf("%q on %s: %x %v", c.addr, c.net, got, err)
		}
	}
	main, test := EncodeAddress(Mainnet, P2PKH, hash), EncodeAddress(Testnet, P2PKH, hash)
	for _, c := range []struct {
		addr    string
		mainnet bool
		ok      bool
	}{{"", true, false}, {test, true, false}, {"t1bogus", true, false}, {main, true, true}, {"", false, true}, {test, false, true}, {main, false, false}} {
		if err := CheckTreasury(c.addr, c.mainnet); (err == nil) != c.ok || (err != nil && !strings.Contains(err.Error(), TreasuryField)) {
			t.Errorf("CheckTreasury(%q, mainnet %v): %v", c.addr, c.mainnet, err)
		}
	}
}

// The claim with the fee: two outputs, the fee exactly, the network fee from the maker's output alone, a signature
// that verifies over the ZIP 244 (v5) and ZIP 229 (v6) digests of both outputs, and a digest that commits to the
// treasury output's value and script.
func TestClaimPaysTheTreasury(t *testing.T) {
	h, secret := htlcFor(startZec + 50)
	for _, ver := range []uint32{5, 6} {
		for _, tr := range [][]byte{P2PKHScript([20]byte{0xd3}), P2SHScript([20]byte{0xd3})} {
			claim, err := BuildClaim(SpendRequest{Key: testKey(makerKeyN), HTLC: h, Prev: OutPoint{Hash: [32]byte{7}}, Value: 200_000, Payout: h.ClaimPkh, BranchID: BranchNU63, Version: ver, Treasury: tr}, secret)
			if err != nil {
				t.Fatal(err)
			}
			if len(claim.Out) != 2 || claim.Out[1].Value != 1_000 || !bytes.Equal(claim.Out[1].ScriptPubKey, tr) {
				t.Fatalf("v%d: the claim's outputs %+v", ver, claim.Out)
			}
			// two outputs are still two logical actions: the network fee is ZIP 317's 10,000, taken from output 0
			if fee, _ := claim.Fee(); fee != 10_000 || fee != claim.ConventionalFee() {
				t.Fatalf("v%d: the claim pays a network fee of %d, conventional %d", ver, fee, claim.ConventionalFee())
			}
			if claim.Out[0].Value != 200_000-10_000-1_000 || !bytes.Equal(claim.Out[0].ScriptPubKey, P2PKHScript(h.ClaimPkh)) {
				t.Fatalf("v%d: the maker's output %+v", ver, claim.Out[0])
			}
			if err := verifyInput(claim, 0); err != nil {
				t.Fatalf("v%d: %v", ver, err)
			}
			if kind, got := ClassifySpend(claim.In[0].ScriptSig, h); kind != SpendClaim || got != secret {
				t.Fatalf("v%d: the claim reads as %s", ver, kind)
			}
			raw := claim.Serialize()
			if id, ok := ComputeTxID(raw); !ok || id != DisplayHex(claim.TxID()) {
				t.Fatalf("v%d: the two-output claim's id cannot be computed back from its bytes", ver)
			}
			// the signature is over both outputs: a zatoshi moved between them, or another treasury script, breaks it
			for _, mutate := range []func(o []TxOut){
				func(o []TxOut) { o[1].Value--; o[0].Value++ },
				func(o []TxOut) { o[1].ScriptPubKey = P2PKHScript([20]byte{0xee}) },
			} {
				m := *claim
				m.Out = append([]TxOut{}, claim.Out...)
				mutate(m.Out)
				if verifyInput(&m, 0) == nil {
					t.Fatalf("v%d: a signature survived a change to the treasury output", ver)
				}
			}
		}
	}
	// a treasury script that is neither P2PKH nor P2SH is refused before anything is signed
	if _, err := BuildClaim(SpendRequest{Key: testKey(makerKeyN), HTLC: h, Value: 200_000, Payout: h.ClaimPkh, BranchID: BranchNU63, Treasury: []byte{opTrue}}, secret); err == nil {
		t.Fatal("a claim paying the fee to OP_TRUE was built")
	}
}

// ZIP 244's own vectors with a transparent input and two or more transparent outputs: the digest this package signs
// is right whatever the number of outputs.
func TestZIP244DigestsOverSeveralOutputs(t *testing.T) {
	n := 0
	for i, row := range loadVectors(t) {
		raw, err := hex.DecodeString(str(t, row[0]))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := ParseV5(raw)
		var input *int
		json.Unmarshal(row[5], &input)
		if err != nil || input == nil || len(tx.Out) < 2 {
			continue
		}
		var amounts []uint64
		var scripts []string
		json.Unmarshal(row[3], &amounts)
		json.Unmarshal(row[4], &scripts)
		for j := range tx.In {
			tx.In[j].Value = amounts[j]
			tx.In[j].ScriptPubKey, _ = hex.DecodeString(scripts[j])
		}
		want, _ := hex.DecodeString(str(t, row[7]))
		got, err := tx.SigDigest(*input, SigHashAll)
		if err != nil || !bytes.Equal(got[:], want) {
			t.Fatalf("vector %d (%d outputs): sighash_all %x %v, want %x", i, len(tx.Out), got, err, want)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no vector with a transparent input and two or more transparent outputs")
	}
	t.Logf("%d vectors with a transparent input and at least two transparent outputs: SIGHASH_ALL matches", n)
}

// No fee on a failed swap: the refund never pays it, and a request that names a treasury is refused.
func TestRefundNeverPaysTheFee(t *testing.T) {
	h, _ := htlcFor(startZec + 50)
	req := SpendRequest{Key: testKey(takerKeyN), HTLC: h, Prev: OutPoint{Hash: [32]byte{7}}, Value: 200_000, Payout: h.RefundPkh, BranchID: BranchNU63, Treasury: P2PKHScript([20]byte{0xd3})}
	if _, err := BuildRefund(req); err == nil || !strings.Contains(err.Error(), "never pays the swap fee") {
		t.Fatalf("a refund naming a treasury: %v", err)
	}
	req.Treasury = nil
	refund, err := BuildRefund(req)
	if err != nil {
		t.Fatal(err)
	}
	if fee, _ := refund.Fee(); len(refund.Out) != 1 || refund.Out[0].Value != 200_000-fee || !bytes.Equal(refund.Out[0].ScriptPubKey, P2PKHScript(h.RefundPkh)) {
		t.Fatalf("the refund's outputs %+v", refund.Out)
	}
}

// On mainnet no swap starts without a valid treasury, for either party; elsewhere an empty one means no fee, and an
// invalid one is refused everywhere.
func TestMainnetSwapRefusesWithoutTreasury(t *testing.T) {
	w := newWorld(t, 1)
	mainTreasury := EncodeAddress(Mainnet, P2PKH, [20]byte{1})
	for _, role := range []string{RoleMaker, RoleTaker} {
		for _, c := range []struct {
			name string
			set  func(c *Config)
			ok   bool
		}{
			{"mainnet DERO, no treasury", func(c *Config) { c.DeroMainnet, c.Treasury = true, "" }, false},
			{"mainnet Zcash, no treasury", func(c *Config) { c.ZcashChain, c.Treasury = "main", "" }, false},
			{"mainnet, a testnet treasury", func(c *Config) { c.DeroMainnet = true }, false},
			{"mainnet, a shielded treasury", func(c *Config) {
				c.DeroMainnet, c.Treasury = true, "zs1z7rejlpsa98s2rrrfkwmaxu53e4ue0ulcrw0h4x5g8jl04tak0d3mm47vdtahatqrlkngh9sly"
			}, false},
			{"mainnet, a mainnet treasury", func(c *Config) { c.DeroMainnet, c.Treasury = true, mainTreasury }, true},
			{"testnet, no treasury", func(c *Config) { c.Treasury = "" }, true},
			{"testnet, an invalid treasury", func(c *Config) { c.Treasury = "t1bogus" }, false},
		} {
			cfg := w.config(role)
			c.set(&cfg)
			st := NewState(filepath.Join(t.TempDir(), role+".json"), role)
			var err error
			if role == RoleMaker {
				_, err = NewMaker(cfg, st, newMemDero(w.dero, makerAddr), w.zc.client())
			} else {
				_, err = NewTaker(cfg, st, newMemDero(w.dero, takerAddr), w.zc.client())
			}
			if (err == nil) != c.ok || (err != nil && !strings.Contains(err.Error(), TreasuryField)) {
				t.Errorf("%s, %s: %v", role, c.name, err)
			}
		}
	}
}

// A whole swap in the fake world: the maker's log states the fee with the claim, the taker's states it at her
// accept, and the claim on the chain pays it (world.check asserts the amount for every scenario and the crash fuzz).
func TestSwapFeeIsStatedByBothSides(t *testing.T) {
	w := newWorld(t, 3)
	if !w.drive(400) {
		t.Fatalf("did not finish:\n%s", w.dump())
	}
	w.check("fee", []string{"swapped"}, []string{"swapped"})
	logs := w.dump()
	for _, want := range []string{"0.5% of 200000 zatoshi = 1000 zatoshi, goes to the DMZ treasury " + testTreasury, "Your funding of 200000 zatoshi and your refund do not change"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("no %q in the logs:\n%s", want, logs)
		}
	}
	if st := w.state(RoleMaker); st.Treasury != testTreasury {
		t.Fatalf("the maker's state fixed the treasury %q", st.Treasury)
	}
}
