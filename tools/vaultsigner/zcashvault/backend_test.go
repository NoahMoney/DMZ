package zcashvault

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"veil/tools/zcashtx"
)

func mustRelease(t *testing.T, b *Backend, j uint64, dest string, amount uint64) string {
	t.Helper()
	id, err := b.Release(j, dest, amount)
	if err != nil {
		t.Fatalf("Release(%d): %v", j, err)
	}
	return id
}

func wantWaiting(t *testing.T, b *Backend, j uint64, dest string, amount uint64) {
	t.Helper()
	id, err := b.Release(j, dest, amount)
	if err == nil || !strings.Contains(err.Error(), "waiting for the co-signers") {
		t.Fatalf("Release(%d) = %q, %v; want a wait for the co-signers", j, id, err)
	}
}

func (tv *testVault) tx(id string) *zcashtx.Tx {
	rt, err := tv.nodes[0][0].RawTx(context.Background(), id)
	if err != nil {
		tv.t.Fatal(err)
	}
	tx, err := zcashtx.ParseTx(rt.Raw)
	if err != nil {
		tv.t.Fatal(err)
	}
	return tx
}

// The whole custody loop on a validating chain: a deposit every signer reports and verifies identically; a 2-of-3
// release that the second signer assembles and the first completes to the same transaction; the payment certified
// only once final, and only for the exact withdrawal; the reserve down by exactly the withdrawal; Release
// idempotent; a second withdrawal paid by two signers with the third offline, spending the change chain's head; and
// the third, back online, recognising the payment without signing anything.
func TestReleaseTwoOfThree(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	dep := tv.chain.pay(tv.depositScript(0), 500_000_000)
	tv.finalize()
	A, B, C := tv.signers[0], tv.signers[1], tv.signers[2]
	for i, s := range tv.signers {
		ds, err := s.Deposits()
		if err != nil || len(ds) != 1 || ds[0].TxID != dep || ds[0].Amount != 500_000_000 || ds[0].Index != 0 {
			t.Fatalf("signer %d deposits %+v %v", i, ds, err)
		}
		if ok, err := s.Verify(ds[0]); !ok || err != nil {
			t.Fatalf("signer %d does not verify the deposit: %v", i, err)
		}
		if r, err := s.Reserve(); r != 500_000_000 || err != nil {
			t.Fatalf("signer %d reserve %d %v", i, r, err)
		}
	}
	dest := userAddress(t)
	const amount = 100_000_000
	wantWaiting(t, A, 0, dest, amount)
	id := mustRelease(t, B, 0, dest, amount)
	if got := mustRelease(t, A, 0, dest, amount); got != id {
		t.Fatalf("the two signers paid %s and %s", id, got)
	}
	tx := tv.tx(id)
	if tx.LockTime != 0 || len(tx.Out) != 2 {
		t.Fatalf("release shape: lock time %d, %d outputs", tx.LockTime, len(tx.Out))
	}
	ch, _ := tv.d.Change()
	if string(tx.Out[1].ScriptPubKey) != string(ch.ScriptPubKey) || tx.Out[1].Value != 400_000_000 {
		t.Fatalf("change %d to %x", tx.Out[1].Value, tx.Out[1].ScriptPubKey)
	}
	paidFee := 500_000_000 - tx.Out[0].Value - tx.Out[1].Value
	if tx.Out[0].Value+paidFee != amount || paidFee < zcashtx.MinimumFee {
		t.Fatalf("paid %d with a fee of %d for a withdrawal of %d", tx.Out[0].Value, paidFee, amount)
	}
	tv.chain.mine(1)
	if ok, _ := A.VerifyRelease(0, id, dest, amount); ok {
		t.Fatal("a payment one block deep was certified")
	}
	tv.chain.mine(100)
	for i, s := range tv.signers {
		if ok, err := s.VerifyRelease(0, id, dest, amount); !ok || err != nil {
			t.Fatalf("signer %d does not certify the final payment: %v", i, err)
		}
	}
	for name, c := range map[string]struct {
		j      uint64
		dest   string
		amount uint64
	}{"another amount": {0, dest, amount + 1}, "another withdrawal": {1, dest, amount}, "another destination": {0, userAddress(t), amount}} {
		if ok, _ := A.VerifyRelease(c.j, id, c.dest, c.amount); ok {
			t.Fatalf("%s: certified", name)
		}
	}
	if r, _ := C.Reserve(); r != 400_000_000 {
		t.Fatalf("reserve %d after paying %d of 500000000: what leaves the reserve must be exactly the withdrawal", r, amount)
	}
	if again := mustRelease(t, B, 0, dest, amount); again != id {
		t.Fatalf("Release is not idempotent: %s then %s", id, again)
	}

	// C is offline for the second withdrawal
	dest2 := userAddress(t)
	wantWaiting(t, A, 1, dest2, 50_000_000)
	id2 := mustRelease(t, B, 1, dest2, 50_000_000)
	tx2 := tv.tx(id2)
	if zcashtx.DisplayHex(tx2.In[0].PrevOut.Hash) != id || tx2.In[0].PrevOut.Index != 1 || tx2.LockTime != 1 {
		t.Fatalf("the second release does not spend the change chain's head first (input 0 %s:%d)", zcashtx.DisplayHex(tx2.In[0].PrevOut.Hash), tx2.In[0].PrevOut.Index)
	}
	tv.chain.mine(1)
	if got := mustRelease(t, C, 1, dest2, 50_000_000); got != id2 {
		t.Fatalf("C, back online, pays %s instead of recognising %s", got, id2)
	}
	if lt := C.led.Entries["1"].Txs; len(lt) != 1 || lt[0].State != "adopted" {
		t.Fatalf("C's ledger: %+v", lt)
	}
}

// A co-signer or the coordinator can put anything in the round. Nothing but a valid signature by the signer it
// names, over the digest of the transaction this signer built, counts: not a signature by a key outside the vault,
// not a vault signer's signature over another digest, not one signer's signatures relabelled as another's.
func TestForgedAndReplayedSignaturesAreIgnored(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.chain.pay(tv.depositScript(0), 300_000_000)
	tv.finalize()
	A := tv.signers[0]
	dest := userAddress(t)
	wantWaiting(t, A, 0, dest, 1_000_000)
	lt := A.led.Entries["0"].Txs[0]
	round := sigRound(testScid, 0, lt.TxID)
	ctx := context.Background()
	got, _ := DirCoordinator{Dir: tv.coord}.Get(ctx, round)
	var mine sigPayload
	for _, b := range got {
		json.Unmarshal(b, &mine)
	}
	put := func(who string, p sigPayload) {
		b, _ := json.Marshal(p)
		if err := (DirCoordinator{Dir: tv.coord, Me: who}).Put(ctx, round, b); err != nil {
			t.Fatal(err)
		}
	}
	// 1. A's signatures relabelled as B's
	relabel := mine
	relabel.Signer = tv.xpubs[1]
	put("mallory-1", relabel)
	// 2. C's own key over another digest
	acct, _ := accountKey(tv.seeds[2], NetRegtest, 0)
	child, _ := acct.Path(tv.d.Branch(), ChainDeposit, 0)
	key, _ := child.signingKey()
	var other [32]byte
	rand.Read(other[:])
	wrong := mine
	wrong.Signer, wrong.Sigs = tv.xpubs[2], []string{hex.EncodeToString(key.Sign(other))}
	put("mallory-2", wrong)
	// 3. a key outside the vault over the right digest
	var kb [32]byte
	rand.Read(kb[:])
	outsider, _ := zcashtx.ParseKey(kb)
	tx, _ := zcashtx.ParseTx(mustHex(t, lt.Hex))
	tx.In[0].Value, tx.In[0].ScriptPubKey = lt.Values[0], mustHex(t, lt.Scripts[0])
	d, _ := tx.SigDigest(0, zcashtx.SigHashAll)
	stranger := mine
	stranger.Signer, stranger.Sigs = tv.xpubs[1], []string{hex.EncodeToString(outsider.Sign(d))}
	put("mallory-3", stranger)
	// 4. garbage and a payload for another vault
	put("mallory-4", sigPayload{V: 1, Vault: strings.Repeat("0", 64), J: 0, TxID: lt.TxID, Signer: tv.xpubs[1], Sigs: mine.Sigs})
	(DirCoordinator{Dir: tv.coord, Me: "mallory-5"}).Put(ctx, round, []byte("{not json"))

	wantWaiting(t, A, 0, dest, 1_000_000)
	// the honest co-signer completes it
	id := mustRelease(t, tv.signers[1], 0, dest, 1_000_000)
	if id != lt.TxID {
		t.Fatalf("assembled %s, A signed %s", id, lt.TxID)
	}
}

func mustHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Every rule a signer checks before it signs: each mutation of a correct release is refused.
func TestSigningRefusals(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	vs, _ := tv.d.Script(ChainDeposit, 0)
	ch, _ := tv.d.Change()
	var h [32]byte
	rand.Read(h[:])
	coins := []coin{{Out: zcashtx.OutPoint{Hash: h}, TxID: zcashtx.DisplayHex(h), Value: 300_000_000, Height: 50, VS: vs}}
	r := buildRules{K: 2, Depth: 100, Epoch: 10, ExpiryWindow: 40, MaxInputs: 200}
	dest := userAddress(t)
	destScript, _, _ := zcashtx.AddressScript(dest, zcashtx.Testnet)
	p, err := buildRelease(r, 7, destScript, 100_000_000, 155, zcashtx.BranchNU63, coins, nil, ch)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(spk []byte) *VaultScript {
		switch string(spk) {
		case string(vs.ScriptPubKey):
			return vs
		case string(ch.ScriptPubKey):
			return ch
		}
		return nil
	}
	if p.Tx.LockTime != 7 || p.Expiry != 150+100+10+40 || p.Tx.Out[0].Value != 100_000_000-p.Fee || p.Tx.Out[1].Value != 200_000_000 {
		t.Fatalf("release: lock %d expiry %d payout %d change %d fee %d", p.Tx.LockTime, p.Expiry, p.Tx.Out[0].Value, p.Tx.Out[1].Value, p.Fee)
	}
	mut := func(f func(tx *zcashtx.Tx)) error {
		tx, _ := zcashtx.ParseTx(p.Tx.Serialize())
		for i := range tx.In {
			tx.In[i].Value, tx.In[i].ScriptPubKey = p.Tx.In[i].Value, p.Tx.In[i].ScriptPubKey
		}
		f(tx)
		return checkRelease(tx, 2, 7, destScript, 100_000_000, ch, lookup)
	}
	if err := mut(func(*zcashtx.Tx) {}); err != nil {
		t.Fatalf("the correct release is refused: %v", err)
	}
	other, _, _ := zcashtx.AddressScript(userAddress(t), zcashtx.Testnet)
	for name, f := range map[string]func(tx *zcashtx.Tx){
		"pays one zatoshi more (a lower fee)":  func(tx *zcashtx.Tx) { tx.Out[0].Value++ },
		"pays one zatoshi less (a higher fee)": func(tx *zcashtx.Tx) { tx.Out[0].Value-- },
		"another destination":                  func(tx *zcashtx.Tx) { tx.Out[0].ScriptPubKey = other },
		"change sent elsewhere":                func(tx *zcashtx.Tx) { tx.Out[1].ScriptPubKey = other },
		"change moved to the destination":      func(tx *zcashtx.Tx) { tx.Out[0].Value += 5000; tx.Out[1].Value -= 5000 },
		"a third output": func(tx *zcashtx.Tx) {
			tx.Out = append(tx.Out, zcashtx.TxOut{Value: 1000, ScriptPubKey: other})
			tx.Out[1].Value -= 1000
		},
		"another withdrawal's marker":     func(tx *zcashtx.Tx) { tx.LockTime = 8 },
		"a non-final sequence":            func(tx *zcashtx.Tx) { tx.In[0].Sequence = 0xfffffffe },
		"no expiry":                       func(tx *zcashtx.Tx) { tx.ExpiryHeight = 0 },
		"an input from outside the vault": func(tx *zcashtx.Tx) { tx.In[0].ScriptPubKey = other },
		"version 6":                       func(tx *zcashtx.Tx) { tx.Version = 6 },
		"a shielded bundle":               func(tx *zcashtx.Tx) { tx.Orchard = &zcashtx.OrchardBundle{} },
		"the destination is dust":         func(tx *zcashtx.Tx) { tx.Out[1].Value += tx.Out[0].Value - 10; tx.Out[0].Value = 10 },
	} {
		if err := mut(f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// the construction is a function of its inputs only
	rev := append([]coin{}, coins...)
	q, _ := buildRelease(r, 7, destScript, 100_000_000, 155, zcashtx.BranchNU63, rev, nil, ch)
	if q.Tx.TxID() != p.Tx.TxID() {
		t.Fatal("the same inputs built another transaction")
	}
}

// Input selection: the anchor, then the largest; dust and outputs younger than the epoch are never selected; the
// fee is the withdrawer's; the vault's last coins keep MinChange; the input cap is a refusal, not a guess.
func TestInputSelection(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	vs, _ := tv.d.Script(ChainDeposit, 0)
	ch, _ := tv.d.Change()
	mk := func(v uint64, h uint32, onChange bool) coin {
		var x [32]byte
		rand.Read(x[:])
		s := vs
		if onChange {
			s = ch
		}
		return coin{Out: zcashtx.OutPoint{Hash: x, Index: 1}, TxID: zcashtx.DisplayHex(x), Vout: 1, Value: v, Height: h, VS: s}
	}
	old, big, dust, young := mk(50_000_000, 10, false), mk(900_000_000, 20, false), mk(5_000, 5, false), mk(2_000_000_000, 149, false)
	r := buildRules{K: 2, Depth: 100, Epoch: 10, ExpiryWindow: 40, MaxInputs: 200}
	destScript, _, _ := zcashtx.AddressScript(userAddress(t), zcashtx.Testnet)
	p, err := buildRelease(r, 1, destScript, 600_000_000, 145, zcashtx.BranchNU63, []coin{young, big, dust, old}, nil, ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Inputs) != 2 || p.Inputs[0].key() != old.key() || p.Inputs[1].key() != big.key() {
		t.Fatalf("selected %v; want the oldest (not the dust, not the young) as anchor, then the largest", p.Inputs)
	}
	if p.Payout != 600_000_000-p.Fee || p.Change != 950_000_000-600_000_000 {
		t.Fatalf("payout %d change %d fee %d", p.Payout, p.Change, p.Fee)
	}
	head := mk(1_000_000, 150, true)
	p, err = buildRelease(r, 1, destScript, 600_000_000, 145, zcashtx.BranchNU63, []coin{young, big, dust, old}, &head, ch)
	if err != nil || p.Inputs[0].key() != head.key() {
		t.Fatalf("the change chain's head is not input 0 (young as it is): %v", err)
	}
	// the last coins
	p, err = buildRelease(r, 1, destScript, 50_000_000-3_000, 145, zcashtx.BranchNU63, []coin{old}, nil, ch)
	if err != nil || p.Change != MinChange || p.Payout != 50_000_000-MinChange-p.Fee {
		t.Fatalf("drained: %+v %v", p, err)
	}
	if _, err := buildRelease(r, 1, destScript, 50_000_001, 145, zcashtx.BranchNU63, []coin{old}, nil, ch); err == nil {
		t.Fatal("paid more than the reserve holds")
	}
	r.MaxInputs = 2
	var many []coin
	for i := 0; i < 5; i++ {
		many = append(many, mk(10_000_000, uint32(10+i), false))
	}
	if _, err := buildRelease(r, 1, destScript, 35_000_000, 145, zcashtx.BranchNU63, many, nil, ch); err == nil || !strings.Contains(err.Error(), "more than 2 inputs") {
		t.Fatalf("the input cap: %v", err)
	}
}

// A signer never signs a second transaction for a withdrawal while the first could still be mined — unless the
// two share an input — and a transaction whose expiry is final may be replaced.
func TestMayReplace(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.finalize()
	b := tv.signers[0]
	vs, _ := tv.d.Script(ChainDeposit, 0)
	scripts, _, _ := b.vaultScripts()
	v, _ := b.ns.view(context.Background())
	rid := func() string { var x [32]byte; rand.Read(x[:]); return zcashtx.DisplayHex(x) }
	a, c := rid(), rid()
	earlier := &ledgerTx{TxID: rid(), Inputs: []string{a + ":0"}, Scripts: []string{hex.EncodeToString(vs.ScriptPubKey)}, Expiry: v.Tip + 50, State: "assembled"}
	b.led.Entries["0"] = &ledgerEntry{J: 0, Txs: []*ledgerTx{earlier}}
	p := &plan{Tx: &zcashtx.Tx{LockTime: 0}, Inputs: []coin{{TxID: c, VS: vs}}}
	if ok, _, err := b.mayReplace(context.Background(), v, earlier, p, scripts); ok || err != nil {
		t.Fatalf("replaced a transaction that could still be mined by one sharing nothing with it: %v", err)
	}
	p.Inputs = append(p.Inputs, coin{TxID: a, Vout: 0, VS: vs})
	if ok, _, _ := b.mayReplace(context.Background(), v, earlier, p, scripts); !ok {
		t.Fatal("two transactions spending one output were not seen as exclusive")
	}
	p.Inputs = p.Inputs[:1]
	earlier.Expiry = v.Final
	if ok, _, _ := b.mayReplace(context.Background(), v, earlier, p, scripts); !ok || earlier.State != "abandoned" {
		t.Fatal("a transaction whose expiry is final was not replaceable")
	}
}

// A release assembled and broadcast, evicted, and never mined: once its expiry is final it is abandoned, never
// adopted again from the coordinator, and a new one is built; only the new one is ever mined.
func TestExpiredReleaseIsReplaced(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.chain.pay(tv.depositScript(0), 300_000_000)
	tv.finalize()
	A, B := tv.signers[0], tv.signers[1]
	dest := userAddress(t)
	wantWaiting(t, A, 0, dest, 10_000_000)
	first := mustRelease(t, B, 0, dest, 10_000_000)
	tv.chain.dropMempool()
	tv.chain.mine(160)
	wantWaiting(t, B, 0, dest, 10_000_000)
	second := mustRelease(t, A, 0, dest, 10_000_000)
	if second == first {
		t.Fatal("the expired release was used again")
	}
	tv.chain.mine(1)
	if s, _ := A.ns.seen(context.Background(), first); s.KnownAny {
		t.Fatal("the expired release reached the chain")
	}
	if s, _ := A.ns.seen(context.Background(), second); !s.MinedAll {
		t.Fatal("the replacement is not mined")
	}
}

// A release built while the previous one was still in the mempool spends the same reserve output and is refused by
// the nodes; once the previous one is mined, the withdrawal is re-built on the new change head, which is allowed only
// because the change chain proves the two exclusive — without waiting 100 blocks.
func TestReplacementThroughTheChangeChain(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.chain.pay(tv.depositScript(0), 300_000_000)
	tv.finalize()
	A, B := tv.signers[0], tv.signers[1]
	d0, d1 := userAddress(t), userAddress(t)
	wantWaiting(t, A, 0, d0, 10_000_000)
	id0 := mustRelease(t, B, 0, d0, 10_000_000)
	wantWaiting(t, A, 1, d1, 20_000_000)
	if _, err := B.Release(1, d1, 20_000_000); err == nil || !strings.Contains(err.Error(), "no node took it") {
		t.Fatalf("a release spending what the mempool already spends was taken: %v", err)
	}
	tv.chain.mine(1)
	wantWaiting(t, B, 1, d1, 20_000_000)
	id1 := mustRelease(t, A, 1, d1, 20_000_000)
	tv.chain.mine(1)
	tx1 := tv.tx(id1)
	if zcashtx.DisplayHex(tx1.In[0].PrevOut.Hash) != id0 {
		t.Fatal("the replacement does not spend the new head")
	}
}

// The ledger precedes the signature: a signer whose coordinator failed after it recorded the transaction restarts
// and publishes the same transaction and, by RFC 6979, the same signature.
func TestRestartAfterSigningPublishesTheSameSignature(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.chain.pay(tv.depositScript(0), 300_000_000)
	tv.finalize()
	ledgerPath := tv.dir + "/ledger0.json"
	broken := tv.signers[0]
	broken.coord = failingCoord{}
	dest := userAddress(t)
	if _, err := broken.Release(0, dest, 5_000_000); err == nil {
		t.Fatal("published through a dead coordinator")
	}
	rec, err := loadLedger(ledgerPath, testScid, tv.xpubs[0])
	if err != nil || rec.Entries["0"] == nil || len(rec.Entries["0"].Txs) != 1 {
		t.Fatalf("the transaction was not recorded before signing: %v", err)
	}
	signed := rec.Entries["0"].Txs[0].TxID
	restarted := tv.backend(0, ledgerPath, tv.coord)
	wantWaiting(t, restarted, 0, dest, 5_000_000)
	got, _ := DirCoordinator{Dir: tv.coord}.Get(context.Background(), sigRound(testScid, 0, signed))
	if len(got) != 1 {
		t.Fatalf("the restarted signer published %d payloads for %s", len(got), signed)
	}
	first := string(firstPayload(got))
	wantWaiting(t, restarted, 0, dest, 5_000_000)
	got, _ = DirCoordinator{Dir: tv.coord}.Get(context.Background(), sigRound(testScid, 0, signed))
	if string(firstPayload(got)) != first {
		t.Fatal("signing the same transaction again gave another signature")
	}
	if id := mustRelease(t, tv.signers[1], 0, dest, 5_000_000); id != signed {
		t.Fatalf("assembled %s, recorded %s", id, signed)
	}
}

func firstPayload(m map[string][]byte) []byte {
	for _, b := range m {
		return b
	}
	return nil
}

type failingCoord struct{}

func (failingCoord) Put(context.Context, string, []byte) error { return errors.New("coordinator down") }
func (failingCoord) Get(context.Context, string) (map[string][]byte, error) {
	return nil, errors.New("coordinator down")
}

// A signer that lost its ledger, with a coordinator that remembers nothing, finds withdrawal j's payment on the
// chain by its marker and signs nothing new.
func TestLostLedgerFindsThePaymentOnChain(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.chain.pay(tv.depositScript(0), 300_000_000)
	tv.finalize()
	dest := userAddress(t)
	wantWaiting(t, tv.signers[0], 0, dest, 7_000_000)
	id := mustRelease(t, tv.signers[1], 0, dest, 7_000_000)
	tv.chain.mine(2)
	empty := t.TempDir()
	fresh := tv.backend(1, tv.dir+"/ledger1-lost.json", empty)
	if got := mustRelease(t, fresh, 0, dest, 7_000_000); got != id {
		t.Fatalf("paid %s again instead of finding %s", got, id)
	}
	if ents, _ := os.ReadDir(empty); len(ents) != 0 {
		t.Fatal("the signer with a lost ledger published signatures")
	}
}

// Deposits that must not be reported: to an index not registered yet (its address is derivable, and anyone can pay
// it); to a registered index not yet attested; and one a reorganisation removed.
func TestDepositsThatAreNotReported(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	b := tv.signers[0]
	tv.chain.pay(tv.depositScript(1), 70_000_000) // index 1: a future index
	good := tv.chain.pay(tv.depositScript(0), 80_000_000)
	tv.finalize()
	ds, err := b.Deposits()
	if err != nil || len(ds) != 1 || ds[0].TxID != good {
		t.Fatalf("deposits %+v %v", ds, err)
	}
	tv.view.mu.Lock()
	tv.view.un = 2 // registered, not attested
	tv.view.mu.Unlock()
	if ds, _ := b.Deposits(); len(ds) != 1 {
		t.Fatalf("an unattested index's deposit was reported: %+v", ds)
	}
	tv.register(1)
	if ds, _ := b.Deposits(); len(ds) != 2 {
		t.Fatalf("once attested, the deposit is reported (the daemon drops it as older than the address): %+v", ds)
	}
	// an attested address that is not the derived one is never read
	tv.view.mu.Lock()
	tv.view.da[1] = userAddress(t)
	tv.view.mu.Unlock()
	if ds, _ := b.Deposits(); len(ds) != 1 {
		t.Fatalf("a wrongly attested index was read: %+v", ds)
	}
	// a reorganisation below the finalized height (impossible on Zcash; a lying chain here) removes the deposit
	d := ds[0]
	tv.chain.reorg(int(tv.chain.tip())-int(d.Height)+1, false)
	if ok, _ := b.Verify(d); ok {
		t.Fatal("a deposit that is no longer in the chain verified")
	}
}

// A second node that lies: inventing a deposit stops the report (fail closed) rather than crediting it; claiming a
// larger reserve is outvoted by the lower; a node that is down stops everything.
func TestLyingAndDeadNodes(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.chain.pay(tv.depositScript(0), 80_000_000)
	tv.finalize()
	b := tv.signers[0]
	liar := &lyingNode{fakeNode: fakeNode{c: tv.chain}}
	b.ns.list = append(b.ns.list, node{name: "liar", zc: liar})
	if ds, err := b.Deposits(); err != nil || len(ds) != 1 {
		t.Fatalf("with an honest second node: %+v %v", ds, err)
	}
	liar.extraTx = strings.Repeat("ab", 32)
	if ds, err := b.Deposits(); err == nil {
		t.Fatalf("a deposit only one node shows was reported: %+v", ds)
	}
	liar.extraTx = ""
	liar.extraValue = 1_000_000_000
	if r, err := b.Reserve(); err != nil || r != 80_000_000 {
		t.Fatalf("reserve %d %v: the lower reading must win", r, err)
	}
	dead := &fakeNode{c: tv.chain, down: true}
	b.ns.list = append(b.ns.list, node{name: "dead", zc: dead})
	if err := b.Health(); err == nil {
		t.Fatal("healthy with a node down")
	}
	if _, err := b.Release(0, userAddress(t), 1_000_000); err == nil {
		t.Fatal("released with a node down")
	}
}

type lyingNode struct {
	fakeNode
	extraTx    string
	extraValue uint64
}

func (l *lyingNode) AddressTxIDs(ctx context.Context, addr string, start, end uint32) ([]string, error) {
	ids, err := l.fakeNode.AddressTxIDs(ctx, addr, start, end)
	if l.extraTx != "" {
		ids = append(ids, l.extraTx)
	}
	return ids, err
}

func (l *lyingNode) AddressUTXOs(ctx context.Context, addr string) ([]zcashtx.UTXO, error) {
	us, err := l.fakeNode.AddressUTXOs(ctx, addr)
	if l.extraValue > 0 && len(us) > 0 {
		us[0].Value += l.extraValue
	}
	return us, err
}

// A release broadcast and then lost from every mempool before it was mined is sent again, not rebuilt.
func TestALostBroadcastIsSentAgain(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.chain.pay(tv.depositScript(0), 300_000_000)
	tv.finalize()
	dest := userAddress(t)
	wantWaiting(t, tv.signers[0], 0, dest, 9_000_000)
	id := mustRelease(t, tv.signers[1], 0, dest, 9_000_000)
	tv.chain.dropMempool()
	if again := mustRelease(t, tv.signers[0], 0, dest, 9_000_000); again != id {
		t.Fatalf("rebuilt %s instead of resending %s", again, id)
	}
	tv.chain.mine(1)
	if s, _ := tv.signers[0].ns.seen(context.Background(), id); !s.MinedAll {
		t.Fatal("the resent release was not mined")
	}
}

// The ledger names one withdrawal by its destination and amount; a different pair for the same j is refused.
func TestLedgerRefusesAChangedWithdrawal(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	tv.register(0)
	tv.chain.pay(tv.depositScript(0), 300_000_000)
	tv.finalize()
	dest := userAddress(t)
	wantWaiting(t, tv.signers[0], 0, dest, 9_000_000)
	if _, err := tv.signers[0].Release(0, dest, 9_000_001); err == nil || !strings.Contains(err.Error(), "not signing anything") {
		t.Fatalf("a changed amount for the same withdrawal: %v", err)
	}
	if _, err := tv.signers[0].Release(0, userAddress(t), 9_000_000); err == nil {
		t.Fatal("a changed destination for the same withdrawal was accepted")
	}
}
