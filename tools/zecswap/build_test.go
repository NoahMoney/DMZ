package zecswap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// The builders, the fee and the watcher's readers.

func htlcFor(cltv uint32) (HTLC, [32]byte) {
	var secret [32]byte
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	return HTLC{Hash: sha256.Sum256(secret[:]), ClaimPkh: testKey(makerKeyN).PubKeyHash(), RefundPkh: testKey(takerKeyN).PubKeyHash(), CLTV: cltv}, secret
}

// ZIP 317: the fee is computed from what the transaction contains, never passed in.
func TestZIP317FeeIsComputedFromActions(t *testing.T) {
	key := testKey(takerKeyN)
	p2pkhIn := TxIn{ScriptSig: append(push(make([]byte, 72)), push(make([]byte, 33))...)}
	out := TxOut{ScriptPubKey: P2PKHScript(key.PubKeyHash())}
	for _, c := range []struct {
		name    string
		tx      Tx
		actions uint64
		fee     uint64
	}{
		{"one P2PKH input, two outputs: the grace floor", Tx{In: []TxIn{p2pkhIn}, Out: []TxOut{out, out}}, 2, 10_000},
		{"five P2PKH inputs: one action each", Tx{In: []TxIn{p2pkhIn, p2pkhIn, p2pkhIn, p2pkhIn, p2pkhIn}, Out: []TxOut{out}}, 5, 25_000},
		{"four outputs: 136 bytes over 34", Tx{In: []TxIn{p2pkhIn}, Out: []TxOut{out, out, out, out}}, 4, 20_000},
		{"one Orchard action on top", Tx{In: []TxIn{p2pkhIn}, Out: []TxOut{out}, Orchard: &OrchardBundle{Actions: [][]byte{make([]byte, orchardActionSize)}}}, 2, 10_000},
	} {
		if a, f := c.tx.LogicalActions(), c.tx.ConventionalFee(); a != c.actions || f != c.fee {
			t.Errorf("%s: %d actions, fee %d; want %d, %d", c.name, a, f, c.actions, c.fee)
		}
	}
	// the swap's own spends: one input carrying a 98-byte redeem script is two actions, so 10,000 zatoshi
	h, secret := htlcFor(startZec + 50)
	claim, err := BuildClaim(SpendRequest{Key: testKey(makerKeyN), HTLC: h, Value: 200_000, Payout: h.ClaimPkh, BranchID: BranchNU63}, secret)
	if err != nil {
		t.Fatal(err)
	}
	if fee, _ := claim.Fee(); fee != 10_000 || claim.LogicalActions() != 2 {
		t.Fatalf("the claim pays %d for %d actions", fee, claim.LogicalActions())
	}
}

func TestFundingPaysExactlyAndChangesBack(t *testing.T) {
	key := testKey(takerKeyN)
	own := P2PKHScript(key.PubKeyHash())
	h, _ := htlcFor(startZec + 50)
	coin := func(b byte, v uint64, script []byte) Coin {
		return Coin{PrevOut: OutPoint{Hash: [32]byte{b}, Index: uint32(b)}, Value: v, Script: script}
	}
	coins := []Coin{coin(1, 150_000, own), coin(2, 90_000, own), coin(3, 10_000_000, P2PKHScript(testKey(3).PubKeyHash()))}
	tx, err := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 200_000, Coins: coins, BranchID: BranchNU63, ExpiryHeight: startZec + 40})
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.In) != 2 || tx.Out[FundingOutput].Value != 200_000 || !bytes.Equal(tx.Out[FundingOutput].ScriptPubKey, h.ScriptPubKey()) {
		t.Fatalf("funding %+v", tx)
	}
	fee, _ := tx.Fee()
	if fee < tx.ConventionalFee() || fee >= tx.ConventionalFee()+OutputFloor {
		t.Fatalf("fee %d for conventional %d", fee, tx.ConventionalFee())
	}
	if len(tx.Out) == 2 && !bytes.Equal(tx.Out[1].ScriptPubKey, own) {
		t.Fatal("the change does not go back to the key")
	}
	for i := range tx.In {
		if err := verifyInput(tx, i); err != nil {
			t.Fatalf("input %d: %v", i, err)
		}
	}
	// the foreign coin is never taken; too little is an error that says so
	if _, err := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 1_000_000, Coins: coins, BranchID: BranchNU63, ExpiryHeight: startZec + 40}); err == nil {
		t.Fatal("funded from a coin the key does not own")
	}
	// a funding that never expires is refused: it could land after the swap is over
	if _, err := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 200_000, Coins: coins, BranchID: BranchNU63}); err == nil {
		t.Fatal("a funding with no expiry height was built")
	}
	// bounded work: past MaxFundingInputs dust coins the builder stops
	var dust []Coin
	for i := 0; i < MaxFundingInputs+10; i++ {
		dust = append(dust, Coin{PrevOut: OutPoint{Hash: [32]byte{byte(i), byte(i >> 8), 9}}, Value: 5_000, Script: own})
	}
	if _, err := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 5_000 * (MaxFundingInputs + 5), Coins: dust, BranchID: BranchNU63, ExpiryHeight: startZec + 40}); err == nil {
		t.Fatal("a funding past MaxFundingInputs was built")
	}
}

func TestClaimAndRefundVerifyUnderTheFakeInterpreter(t *testing.T) {
	h, secret := htlcFor(startZec + 50)
	req := SpendRequest{HTLC: h, Prev: OutPoint{Hash: [32]byte{7}}, Value: 200_000, BranchID: BranchNU63}
	req.Key, req.Payout = testKey(makerKeyN), h.ClaimPkh
	claim, err := BuildClaim(req, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyInput(claim, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if kind, got := ClassifySpend(claim.In[0].ScriptSig, h); kind != SpendClaim || got != secret {
		t.Fatalf("the claim reads as %s", kind)
	}
	req.Key, req.Payout = testKey(takerKeyN), h.RefundPkh
	refund, err := BuildRefund(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyInput(refund, 0); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if kind, _ := ClassifySpend(refund.In[0].ScriptSig, h); kind != SpendRefund {
		t.Fatalf("the refund reads as %s", kind)
	}
	// the wrong key for either branch is refused before anything is signed
	req.Key = testKey(takerKeyN)
	if _, err := BuildClaim(req, secret); err == nil {
		t.Fatal("a claim by the refund key was built")
	}
	req.Key = testKey(makerKeyN)
	if _, err := BuildRefund(req); err == nil {
		t.Fatal("a refund by the claim key was built")
	}
	var wrong [32]byte
	if _, err := BuildClaim(req, wrong); err == nil {
		t.Fatal("a claim with a secret that does not hash to H was built")
	}
}

// ZEC-1 at the interpreter: a claim carrying a secret of any length but 32 fails, whatever it hashes to.
func TestShortSecretFailsTheClaimBranch(t *testing.T) {
	short := bytes.Repeat([]byte{0x42}, 31)
	h := HTLC{Hash: sha256.Sum256(short), ClaimPkh: testKey(makerKeyN).PubKeyHash(), RefundPkh: testKey(takerKeyN).PubKeyHash(), CLTV: startZec + 50}
	key := testKey(makerKeyN)
	tx := &Tx{BranchID: BranchNU63, In: []TxIn{{PrevOut: OutPoint{Hash: [32]byte{9}}, Sequence: SequenceFinal, Value: 200_000, ScriptPubKey: h.ScriptPubKey()}},
		Out: []TxOut{{Value: 190_000, ScriptPubKey: P2PKHScript(h.ClaimPkh)}}}
	d, _ := tx.SigDigest(0, SigHashAll)
	sig := key.Sign(d)
	var b bytes.Buffer
	b.Write(push(sig))
	b.Write(push(key.PubKey()))
	b.Write(push(short))
	b.WriteByte(opTrue)
	b.Write(push(h.RedeemScript()))
	tx.In[0].ScriptSig = b.Bytes()
	if err := verifyInput(tx, 0); err == nil {
		t.Fatal("a 31-byte secret opened the claim branch")
	}
}

// ZEC-7's table: every way a refund can be made unminable is refused before it is signed or stored.
func TestRefundTableRefusesUnminableRefunds(t *testing.T) {
	h, _ := htlcFor(startZec + 50)
	good, err := BuildRefund(SpendRequest{Key: testKey(takerKeyN), HTLC: h, Prev: OutPoint{Hash: [32]byte{7}}, Value: 200_000, Payout: h.RefundPkh, BranchID: BranchNU63})
	if err != nil {
		t.Fatal(err)
	}
	if good.LockTime != h.CLTV || good.In[0].Sequence != SequenceRefund || good.ExpiryHeight != 0 {
		t.Fatalf("the refund is %d/%x/%d", good.LockTime, good.In[0].Sequence, good.ExpiryHeight)
	}
	for _, c := range []struct {
		name   string
		mutate func(tx *Tx)
	}{
		{"a final input (BIP 65: CHECKLOCKTIMEVERIFY fails)", func(tx *Tx) { tx.In[0].Sequence = SequenceFinal }},
		{"nLockTime below cltv", func(tx *Tx) { tx.LockTime = h.CLTV - 1 }},
		{"nLockTime a Unix time", func(tx *Tx) { tx.LockTime = LockTimeThreshold }},
		{"nExpiryHeight at cltv (ZIP 203: no block can hold it)", func(tx *Tx) { tx.ExpiryHeight = h.CLTV }},
		{"nExpiryHeight below cltv", func(tx *Tx) { tx.ExpiryHeight = h.CLTV - 10 }},
		{"an expiry at all: the pre-signed refund must not expire", func(tx *Tx) { tx.ExpiryHeight = h.CLTV + 100 }},
		{"two inputs", func(tx *Tx) { tx.In = append(tx.In, tx.In[0]) }},
	} {
		tx := *good
		tx.In = append([]TxIn(nil), good.In...)
		c.mutate(&tx)
		if err := CheckRefundTx(&tx, h); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	if err := CheckRefundTx(good, h); err != nil {
		t.Fatalf("the good refund: %v", err)
	}
}

// CHECKLOCKTIMEVERIFY and nLockTime finality at the node: the refund is refused while the tip is below cltv and
// mined in the block after cltv.
func TestRefundIsMinableOnlyAfterCLTV(t *testing.T) {
	f := newFakeZcash(t, "test", startZec)
	h, _ := htlcFor(startZec + 5)
	fund := f.Pay(h.ScriptPubKey(), 200_000)
	prev, _ := ParseDisplayHex(fund)
	refund, err := BuildRefund(SpendRequest{Key: testKey(takerKeyN), HTLC: h, Prev: OutPoint{Hash: prev}, Value: 200_000, Payout: h.RefundPkh, BranchID: BranchNU63})
	if err != nil {
		t.Fatal(err)
	}
	for f.Tip() < h.CLTV {
		if _, code, msg := f.Submit(refund.Serialize()); code == 0 {
			t.Fatalf("the refund was taken at tip %d, below cltv %d", f.Tip(), h.CLTV)
		} else if !strings.Contains(msg, "non-final") {
			t.Fatalf("refused for the wrong reason: %s", msg)
		}
		f.Mine(1)
	}
	if _, code, msg := f.Submit(refund.Serialize()); code != 0 {
		t.Fatalf("the refund was refused at tip %d = cltv: %s", f.Tip(), msg)
	}
	f.Mine(1)
	if f.Unspent(fund, 0) || f.SpenderOf(fund, 0) == nil {
		t.Fatal("the refund was not mined at cltv + 1")
	}
}

// The taker reads the secret out of the maker's claim whatever version his wallet made it: here a v4 (Sapling-era)
// transaction and a v1 one, built by hand, and a truncated one that must be an error and not a panic.
func TestSecretIsReadFromAnyTransactionVersion(t *testing.T) {
	h, secret := htlcFor(startZec + 50)
	scriptSig := h.ClaimScriptSig(bytes.Repeat([]byte{0x30}, 71), testKey(makerKeyN).PubKey(), secret)
	prev := OutPoint{Hash: [32]byte{0xaa}, Index: 3}
	body := func() []byte {
		var b bytes.Buffer
		b.Write(CompactSize(1))
		b.Write(prev.Hash[:])
		b.Write(le32(prev.Index))
		b.Write(withLen(scriptSig))
		b.Write(le32(SequenceFinal))
		b.Write(CompactSize(1))
		b.Write(le64(190_000))
		b.Write(withLen(P2PKHScript(h.ClaimPkh)))
		return b.Bytes()
	}
	var v4 bytes.Buffer
	v4.Write(le32(0x80000004))
	v4.Write(le32(0x892F2085))
	v4.Write(body())
	v4.Write(le32(0))         // nLockTime
	v4.Write(le32(0))         // nExpiryHeight
	v4.Write(le64(0))         // valueBalanceSapling
	v4.Write([]byte{0, 0, 0}) // no spends, outputs or joinsplits
	var v1 bytes.Buffer
	v1.Write(le32(1))
	v1.Write(body())
	v1.Write(le32(0))
	for name, raw := range map[string][]byte{"v4": v4.Bytes(), "v1": v1.Bytes()} {
		ins, outs, err := transparentParts(raw)
		if err != nil || len(ins) != 1 || len(outs) != 1 || ins[0].PrevOut != prev {
			t.Fatalf("%s: %v", name, err)
		}
		if kind, got := ClassifySpend(ins[0].ScriptSig, h); kind != SpendClaim || got != secret {
			t.Fatalf("%s: read as %s", name, kind)
		}
		if _, ok := ComputeTxID(raw); !ok {
			t.Fatalf("%s: no id", name)
		}
		for cut := 0; cut < len(raw); cut += 7 {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s truncated at %d: panic %v", name, cut, r)
					}
				}()
				transparentParts(raw[:cut])
			}()
		}
	}
	// a claim carrying a 32-byte push that is not the secret is not read as a claim
	var fake [32]byte
	other := h.ClaimScriptSig(bytes.Repeat([]byte{0x30}, 71), testKey(makerKeyN).PubKey(), fake)
	if kind, _ := ClassifySpend(other, h); kind == SpendClaim {
		t.Fatal("a push that does not hash to H was read as the secret")
	}
	// another script's spend is nothing of this swap's
	other2 := HTLC{Hash: h.Hash, ClaimPkh: h.ClaimPkh, RefundPkh: h.RefundPkh, CLTV: h.CLTV + 1}
	if kind, _ := ClassifySpend(scriptSig, other2); kind != SpendOther {
		t.Fatalf("a spend of another redeem script reads as %s", kind)
	}
}

// Anyone can pay the script's address, and a counterparty could bury his claim among thousands of payments. The
// search reads at most its budget per call, keeps its place, and finds the claim in the end.
func TestSpendSearchIsBoundedUnderPadding(t *testing.T) {
	ctx := context.Background()
	f := newFakeZcash(t, "test", startZec)
	c := f.client()
	h, secret := htlcFor(startZec + 500)
	fundID := f.Pay(h.ScriptPubKey(), 200_000)
	fund := Funding{TxID: fundID, Value: 200_000, Height: f.Tip()}
	f.mu.Lock()
	var pad []string
	for i := 0; i < 450; i++ {
		f.nonce++
		pad = append(pad, f.addTxLocked(&Tx{BranchID: f.branch, LockTime: uint32(f.nonce), Out: []TxOut{{Value: 1, ScriptPubKey: h.ScriptPubKey()}}}))
	}
	prev, _ := ParseDisplayHex(fundID)
	claim, err := BuildClaim(SpendRequest{Key: testKey(makerKeyN), HTLC: h, Prev: OutPoint{Hash: prev}, Value: 200_000, Payout: h.ClaimPkh, BranchID: BranchNU63}, secret)
	if err != nil {
		f.mu.Unlock()
		t.Fatal(err)
	}
	claimID := f.addTxLocked(claim)
	f.appendBlockLocked(append(pad, claimID))
	f.mu.Unlock()

	if unspent, err := OutputUnspent(ctx, c, fund, h.ScriptPubKey()); err != nil || unspent {
		t.Fatalf("gettxout says the claimed funding is unspent: %v %v", unspent, err)
	}
	cur := ScanCursor{Base: fund.Height}
	const budget = 100
	for step := 1; ; step++ {
		f.mu.Lock()
		before := f.calls["getaddresstxids"] + f.calls["getrawtransaction"]
		f.mu.Unlock()
		sp, err := FindSpendStep(ctx, c, h, Testnet, fund, &cur, f.Tip(), budget)
		f.mu.Lock()
		used := f.calls["getaddresstxids"] + f.calls["getrawtransaction"] - before
		f.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if used > budget {
			t.Fatalf("step %d made %d reads, over its budget of %d", step, used, budget)
		}
		if sp != nil {
			if sp.Kind != SpendClaim || sp.Secret != secret || sp.TxID != claimID {
				t.Fatalf("found %+v", sp)
			}
			if step < 4 {
				t.Fatalf("found in %d steps: the padding was not read", step)
			}
			t.Logf("the claim among 450 payments was found in %d steps of at most %d reads", step, budget)
			return
		}
		if step > 20 {
			t.Fatal("the search did not finish")
		}
	}
}

// A reorganisation that replaces the funding's block turns its depth into an error, not a number.
func TestDepthDetectsAReorganisation(t *testing.T) {
	ctx := context.Background()
	f := newFakeZcash(t, "test", startZec)
	c := f.client()
	h, _ := htlcFor(startZec + 500)
	f.Pay(h.ScriptPubKey(), 200_000)
	us, err := ScriptOutputs(ctx, c, h, Testnet)
	if err != nil || len(us) != 1 {
		t.Fatalf("%v %v", us, err)
	}
	fund, err := SeeFunding(ctx, c, h, us[0])
	if err != nil {
		t.Fatal(err)
	}
	f.Mine(2)
	if d, err := Depth(ctx, c, fund, f.Tip()); err != nil || d != 3 {
		t.Fatalf("depth %d %v", d, err)
	}
	f.Reorg(3, true)
	f.Mine(4)
	if _, err := Depth(ctx, c, fund, f.Tip()); !errors.Is(err, ErrReorged) {
		t.Fatalf("a replaced block: %v", err)
	}
}

func TestKeysFromFiles(t *testing.T) {
	k := testKey(1)
	for _, net := range []Network{Testnet, Mainnet} {
		prefix := byte(wifTestnet)
		if net == Mainnet {
			prefix = wifMainnet
		}
		var raw [32]byte
		raw[31] = 1
		wif := Base58Check(append(append([]byte{prefix}, raw[:]...), 0x01))
		got, err := ParseKeyText(wif+"\n", net)
		if err != nil || got.PubKeyHash() != k.PubKeyHash() {
			t.Fatalf("%s WIF: %v", net, err)
		}
		other := Testnet
		if net == Testnet {
			other = Mainnet
		}
		if _, err := ParseKeyText(wif, other); err == nil {
			t.Fatalf("a %s WIF key was read on %s", net, other)
		}
		if _, err := ParseKeyText(Base58Check(append([]byte{prefix}, raw[:]...)), net); err == nil {
			t.Fatal("an uncompressed WIF key was read")
		}
	}
	if got, err := ParseKeyText(strings.Repeat("0", 63)+"1", Testnet); err != nil || got.PubKeyHash() != k.PubKeyHash() {
		t.Fatalf("64 hex: %v", err)
	}
	if hex.EncodeToString(k.PubKey())[:2] != "02" {
		t.Fatal("the compressed key of 1 starts 02")
	}
}
