package zecswap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const reorgLog = "../../research/regtest/zecswap-reorg.log"

// TestRegtestReorg makes REAL reorganisations on the regtest zebrad — invalidateblock on the block that holds the
// funding, a competing branch mined on the block below it, then reconsiderblock on the old block — under the same
// Swap machines, ZcashRPC and DeroRPC as TestRegtestEndToEnd, and records what each companion did in
// research/regtest/zecswap-reorg.log. Skipped unless ZECSWAP_REGTEST=1; ZECSWAP_REGTEST_REORGS picks the scenarios
// (default a,b).
//
//	a   the funding is mined and read by the maker at depth 1, below the policy's C_zec (2); its block is invalidated
//	    and the taker, away from her companion, spends the same coin again on the new branch, so the funding is off
//	    the chain for good. The maker must not reveal s, and his watcher must say the funding went.
//	b   the funding reaches C_zec, the maker claims — s is public — and the taker reads s and claims the DERO while
//	    the maker's companion is away. Then the funding's block is invalidated, a reorganisation deeper than the
//	    policy, and the taker spends her coin again on the new branch: docs/ZEC_SWAP.md §9 Z5, the maker has lost
//	    both legs. What his companion does when it comes back is recorded.
//	b2  the claim is invalidated with the funding, three blocks deep, before the taker has read it, and nobody spends
//	    the coin again: the funding's bytes are public, so the maker's companion can put it back, then its claim.
//
// With ZECSWAP_REGTEST_BEFORE_FIX=1 the run only records (for a run against code that predates the companion's
// reorganisation handling); otherwise the maker must report each reorganisation, and end a refunded, b lost and b2
// swapped.
func TestRegtestReorg(t *testing.T) {
	if os.Getenv("ZECSWAP_REGTEST") == "" {
		t.Skip("set ZECSWAP_REGTEST=1 to run against the real regtest zebrad and the DERO simulator (tools/regtest/README.md)")
	}
	e := newE2EAt(t, reorgLog)
	defer e.close()
	strict := os.Getenv("ZECSWAP_REGTEST_BEFORE_FIX") == ""
	run := os.Getenv("ZECSWAP_REGTEST_REORGS")
	if run == "" {
		run = "a,b"
	}
	e.logf("== zecswap reorganisations on real nodes: zebrad 6.3.0 regtest %s (invalidateblock, reconsiderblock), DERO simulator %s; scenarios %s", e2eZcash, e2eDaemon, run)
	if note := os.Getenv("ZECSWAP_REGTEST_NOTE"); note != "" {
		e.logf("== note: %s", note)
	}
	if !strict {
		e.logf("== this run only records: its companion code predates the reorganisation handling")
	}
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ci.Regtest || CheckNetworks("regtest", ci.Chain, true) != nil {
		t.Fatalf("not a regtest node: %+v", ci)
	}
	e.logf("zcash: chain %q (block 0 is regtest's genesis), height %d, nextblock %08x", ci.Chain, ci.Height, ci.NextBranch)
	e.install()
	names := strings.Split(run, ",")
	keys := map[string][32]byte{}
	for _, n := range names {
		keys[n] = randomKey(t)
		k, _ := ParseKey(keys[n])
		e.mineTo(k.Address(Testnet), 1)
		e.logf("zcash: scenario %s's taker key is %s; a coinbase is mined to it", n, k.Address(Testnet))
	}
	e.mine(101) // coinbase maturity
	for _, n := range names {
		switch n {
		case "a":
			e.reorgBeforeDepth(keys[n], strict)
		case "b":
			e.reorgAfterClaim(n, keys[n], true, strict)
		case "b2":
			e.reorgAfterClaim(n, keys[n], false, strict)
		default:
			t.Fatalf("no reorganisation scenario %q", n)
		}
	}
	e.logf("== done: %s", run)
}

// ---- the node, as the scenarios drive it ----

// try is a raw JSON-RPC call. invalidateblock and reconsiderblock answer null on success, which call reports as
// errNullResult; these control methods take that as success.
func (e *e2e) try(method string, params ...interface{}) (json.RawMessage, error) {
	if params == nil {
		params = []interface{}{}
	}
	var out json.RawMessage
	err := e.zc.call(e.ctx, method, params, &out)
	if errors.Is(err, errNullResult) {
		err = nil
	}
	return out, err
}

func (e *e2e) tipNow() (uint32, string) {
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		e.t.Fatalf("zebrad getblockchaininfo: %v", err)
	}
	h, err := e.zc.BlockHash(e.ctx, ci.Height)
	if err != nil {
		e.t.Fatalf("zebrad getblockhash %d: %v", ci.Height, err)
	}
	return ci.Height, h
}

func (e *e2e) mempool() []string {
	var ids []string
	if out, err := e.try("getrawmempool"); err == nil {
		_ = json.Unmarshal(out, &ids)
	}
	return ids
}

func (e *e2e) txStatus(txid string) string {
	rt, known, err := TxKnown(e.ctx, e.zc, txid)
	switch {
	case err != nil:
		return "unreadable (" + err.Error() + ")"
	case !known:
		return "unknown to the node"
	case !rt.Mined:
		return "in the mempool"
	}
	return fmt.Sprintf("mined at %d in %s (%d confirmations)", rt.Height, rt.BlockHash, rt.Confirmations)
}

// invalidate is invalidateblock on the best chain's block at height: it and every block above it leave the best
// chain, and the tip falls to the block below.
func (e *e2e) invalidate(scn string, height uint32) (string, uint32) {
	top, topHash := e.tipNow()
	hash, err := e.zc.BlockHash(e.ctx, height)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.try("invalidateblock", hash); err != nil {
		e.t.Fatalf("%s: invalidateblock %s: %v", scn, hash, err)
	}
	after, afterHash := e.tipNow()
	for i := 0; after >= height && i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		after, afterHash = e.tipNow()
	}
	if after >= height {
		e.t.Fatalf("%s: invalidateblock %s was taken, and the tip is still %d", scn, hash, after)
	}
	e.logf("%s: invalidateblock %s (height %d): the tip fell from %d (%s) to %d (%s); %d block(s) left the best chain", scn, hash, height, top, topHash, after, afterHash, top-after)
	return hash, top - after
}

// reconsider is reconsiderblock on the old branch's first block, once the new branch is longer: the node may
// validate the old branch again, and must keep the heavier one.
func (e *e2e) reconsider(scn, hash string) {
	before, bh := e.tipNow()
	_, err := e.try("reconsiderblock", hash)
	time.Sleep(2 * time.Second)
	after, ah := e.tipNow()
	res := "taken"
	if err != nil {
		res = "refused (" + err.Error() + ")"
	}
	kept := "the node keeps the new, longer branch"
	if ah != bh {
		kept = fmt.Sprintf("THE TIP MOVED from %d (%s)", before, bh)
	}
	e.logf("%s: reconsiderblock %s %s; %s: tip %d (%s)", scn, hash, res, kept, after, ah)
}

// doubleSpend is the taker spending, a second time, the coin her funding spent: built by this package with her key,
// paying 100000 zatoshi to a script of a random hash and the rest back to her. Once it is mined the funding can
// never be mined again.
func (e *e2e) doubleSpend(scn string, takerKey [32]byte, fundingHex string) string {
	k, err := ParseKey(takerKey)
	if err != nil {
		e.t.Fatal(err)
	}
	defer k.Zero()
	raw, err := hex.DecodeString(fundingHex)
	if err != nil {
		e.t.Fatal(err)
	}
	ftx, err := ParseTx(raw)
	if err != nil {
		e.t.Fatal(err)
	}
	us, err := e.zc.AddressUTXOs(e.ctx, k.Address(Testnet))
	if err != nil {
		e.t.Fatal(err)
	}
	var coins []Coin
	for _, u := range us {
		c, err := CoinOf(u)
		if err != nil {
			e.t.Fatal(err)
		}
		coins = append(coins, c)
	}
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	h := HTLC{ClaimPkh: k.PubKeyHash(), RefundPkh: k.PubKeyHash(), CLTV: ci.Height + 100}
	if _, err := rand.Read(h.Hash[:]); err != nil {
		e.t.Fatal(err)
	}
	tx, err := BuildFunding(FundingRequest{Key: k, HTLC: h, Amount: 100_000, Coins: coins, BranchID: ci.NextBranch, ExpiryHeight: ci.Height + 20, Version: 5})
	if err != nil {
		e.t.Fatalf("%s: building the second spend: %v", scn, err)
	}
	var shared []string
	for _, a := range tx.In {
		for _, b := range ftx.In {
			if a.PrevOut == b.PrevOut {
				shared = append(shared, fmt.Sprintf("%s:%d", DisplayHex(a.PrevOut.Hash), a.PrevOut.Index))
			}
		}
	}
	if len(shared) == 0 {
		e.t.Fatalf("%s: the node lists %v for the taker, and none of it is an input of the funding", scn, us)
	}
	var id string
	for i := 0; ; i++ {
		if id, err = e.zc.SendRawTx(e.ctx, tx.Serialize()); err == nil {
			break
		}
		if i == 10 {
			e.t.Fatalf("%s: zebrad refused the second spend: %v", scn, err)
		}
		time.Sleep(time.Second)
	}
	e.logf("%s: the taker spends her coin a second time: tx %s spends %s, the funding's own input, and pays another script; zebrad took it", scn, id, strings.Join(shared, ", "))
	return id
}

func escrowWord(s uint64) string {
	switch s {
	case EscrowOpen:
		return "open"
	case EscrowClaimed:
		return "claimed"
	case EscrowRefunded:
		return "refunded"
	}
	return fmt.Sprintf("in state %d", s)
}

func reported(ok bool) string {
	if ok {
		return "REPORTED"
	}
	return "did NOT report"
}

// ---- a: the funding goes before the claim depth ----

type reorgA struct {
	key               [32]byte
	stage, waits      int
	fundTx, fundBlock string
	fundH             uint32
	conflict          string
	revealed          bool
}

func (e *e2e) reorgBeforeDepth(key [32]byte, strict bool) {
	s := &reorgA{key: key}
	e.logf("a: the taker funds, the funding is mined one deep (below the claim depth C_zec %d), then invalidateblock takes its block away and, in the same moment, the taker spends her coin a second time on the new branch, so the funding can never come back. (Without that second spend zebrad puts the funding back in its mempool and mines it again, and the maker rightly counts its depth afresh and claims: seen on this node, see the run above.) The maker must never reveal the secret and his watcher must report the reorganisation. The taker, whose coin is now in her own second spend, stays away.", RegtestPolicy().ConfZec)
	r := e.runSwap("a", 5, key, func(r *e2eRun, role string) bool {
		return role == RoleTaker && s.stage >= 1
	}, s.act)
	// Scenario 1(a) is about the maker not revealing and his watcher noticing; the run stops once the maker has decided
	// not to reveal (refund_wait). His DERO refund at T_dero is the ordinary refund path, shown end to end on the real
	// nodes by the e2e swap C and the command-line refund; this escrow is refundable by wallet 30011 after T_dero.
	noticed := r.said(RoleMaker, "reorganis")
	// both parties have settled; asking the node to reconsider the funding's old block shows it keeps the heavier
	// branch — the funding does not come back
	if s.fundBlock != "" {
		e.reconsider("a", s.fundBlock)
	}
	e.logf("a: at the end the funding %s is %s", s.fundTx, e.txStatus(s.fundTx))
	e.logf("a: RESULT: the maker %s; he ended %s, the taker ended %s. The maker's companion %s the reorganisation.",
		map[bool]string{true: "BUILT A CLAIM " + r.mk.ClaimTxID, false: "never built a claim, so s was never revealed"}[s.revealed || r.mk.ClaimTxID != ""], r.mk.Phase, r.tk.Phase, reported(noticed))
	// the core safety property holds with or without the fix: a funding seen only below C_zec, and then gone, must
	// never have drawn out the secret
	if s.revealed || r.mk.ClaimTxID != "" {
		e.t.Fatalf("a: the maker built a claim (%s) for a funding that never reached C_zec and left the chain", r.mk.ClaimTxID)
	}
	if strict {
		switch r.mk.Phase {
		case "refund_wait", "dero_refunding", "refunded":
		default:
			e.t.Fatalf("a: the maker ended %s, not heading to a refund", r.mk.Phase)
		}
		if !noticed {
			e.t.Fatalf("a: the maker's watcher did not report the reorganisation")
		}
	}
}

func (s *reorgA) act(r *e2eRun) {
	e, p := r.e, RegtestPolicy()
	mk, tk := r.state(RoleMaker), r.state(RoleTaker)
	if mk != nil && mk.ClaimTxID != "" {
		s.revealed = true
	}
	switch s.stage {
	case 0:
		if tk == nil || tk.FundingTxID == "" {
			return
		}
		r.noMine = true
		s.fundTx = tk.FundingTxID
		e.logf("a: the taker's funding %s is sent (%s); from here the scenario mines by hand, and the taker's companion is away", s.fundTx, e.txStatus(s.fundTx))
		e.mine(1)
		rt, err := e.zc.RawTx(e.ctx, s.fundTx)
		if err != nil || !rt.Mined {
			e.t.Fatalf("a: the funding is not in the next block: %v", err)
		}
		s.fundH, s.fundBlock = rt.Height, rt.BlockHash
		e.logf("a: the funding is mined at height %d in %s: depth 1, and the maker's policy claims at depth %d", rt.Height, rt.BlockHash, p.ConfZec)
		s.stage = 1
	case 1:
		// let the maker read the funding at depth 1 (he records it, below C_zec), then remove its block and build a
		// longer branch without it — zebrad drops the transaction from the mempool, so nothing re-mines it
		s.waits++
		if !r.said(RoleMaker, "seen at depth 1") && s.waits < 20 {
			return
		}
		e.invalidate("a", s.fundH)
		e.logf("a: after invalidateblock the funding is %s; the mempool holds %v", e.txStatus(s.fundTx), e.mempool())
		// at once, before zebrad can put the funding back: the taker's second spend of the same coin
		s.conflict = e.doubleSpend("a", s.key, tk.FundingHex)
		e.mine(3)
		tip, tipHash := e.tipNow()
		at, _ := e.zc.BlockHash(e.ctx, s.fundH)
		e.logf("a: a longer branch was mined: tip %d (%s); height %d is now %s, was %s. The second spend is %s; the funding is %s", tip, tipHash, s.fundH, at, s.fundBlock, e.txStatus(s.conflict), e.txStatus(s.fundTx))
		r.noMine = false
		e.logf("a: harness mining resumes; the maker keeps watching the script address")
		s.stage = 2
	case 2:
		// stop once the maker has decided the secret stays (refund_wait): that is the end of scenario 1(a)
		if mk != nil {
			switch mk.Phase {
			case "refund_wait", "dero_refunding", "refunded":
				e.logf("a: the maker is in %s: the funding never reached C_zec on the chain as it is, the secret stays with him, and the DERO is refunded after T_dero %d; the run stops here", mk.Phase, mk.TDero)
				r.stop = true
			}
		}
	}
}

// ---- b and b2: the funding goes after the claim ----

type reorgB struct {
	name            string
	key             [32]byte
	double          bool
	stage           int
	fundTx, claimTx string
	fundBlock       string
	fundH, removed  uint32
	conflict        string
	backAt          uint64
}

func (e *e2e) reorgAfterClaim(name string, key [32]byte, double, strict bool) {
	s := &reorgB{name: name, key: key, double: double}
	p := RegtestPolicy()
	if double {
		e.logf("%s: after both sides have settled (the taker's DERO Claim has landed), a reorganisation deeper than C_zec %d takes the claim and its funding off the chain, and the taker's coin is spent again on the new branch: the funding can never be mined, and the maker has lost both legs (docs/ZEC_SWAP.md §9 Z5)", name, p.ConfZec)
	} else {
		e.logf("%s: after both sides have settled, a reorganisation deeper than C_zec %d takes the claim and its funding off the chain, but nobody spends the coin again: the maker puts the funding and the claim back from their own bytes and regains his ZEC", name, p.ConfZec)
	}
	r := e.runSwap(name, 5, key, func(r *e2eRun, role string) bool {
		// both sides run to the end of the ordinary swap; only then does the reorganisation hit (s.act, stage 0)
		return false
	}, s.act)
	mk := r.mk
	noticed := r.said(RoleMaker, "reorganis")
	e.logf("%s: at the end the funding %s is %s; the claim %s is %s; the escrow is %s", name, s.fundTx, e.txStatus(s.fundTx), s.claimTx, e.txStatus(s.claimTx), escrowWord(r.escrowState(mk.EscrowID)))
	if s.double {
		e.logf("%s: the second spend %s is %s", name, s.conflict, e.txStatus(s.conflict))
	}
	end := "ended " + mk.Phase + ": " + mk.Outcome
	if !mk.Done {
		end = "DID NOT END: it is still in phase " + mk.Phase
	}
	e.logf("%s: RESULT: the maker's companion %s. It %s the reorganisation. The taker ended %s.", name, end, reported(noticed), r.tk.Phase)
	if !strict {
		return
	}
	want := "lost"
	if !double {
		want = "swapped"
	}
	if mk.Phase != want || !noticed {
		e.t.Fatalf("%s: the maker ended %q (want %s) and %s the reorganisation", name, mk.Phase, want, reported(noticed))
	}
}

func (s *reorgB) act(r *e2eRun) {
	e, n, p := r.e, s.name, RegtestPolicy()
	mk, tk := r.state(RoleMaker), r.state(RoleTaker)
	switch s.stage {
	case 0:
		// the moment the taker has funded, the scenario takes the Zcash miner by hand, so the maker can be held in
		// "claimed" (the claim mined but below C_zec) while the taker finishes her DERO Claim from the same secret
		if tk == nil || tk.FundingTxID == "" {
			return
		}
		r.noMine = true
		s.fundTx = tk.FundingTxID
		e.mine(1)
		frt, err := e.zc.RawTx(e.ctx, s.fundTx)
		if err != nil || !frt.Mined {
			e.t.Fatalf("%s: the funding %s is not in the next block: %v", n, s.fundTx, err)
		}
		s.fundH, s.fundBlock = frt.Height, frt.BlockHash
		e.logf("%s: the taker funded %s, mined at %d in %s (depth 1); the scenario now mines by hand", n, s.fundTx, frt.Height, frt.BlockHash)
		s.stage = 1
	case 1:
		// bring the funding to C_zec so the maker claims, then mine one more so his claim is mined (depth 1)
		if mk == nil || mk.Phase != "claimed" || mk.ClaimTxID == "" {
			tip, _ := e.tipNow()
			if tip-s.fundH+1 < p.ConfZec+1 {
				e.mine(1)
			}
			return
		}
		s.claimTx = mk.ClaimTxID
		crt, _, err := TxKnown(e.ctx, e.zc, s.claimTx)
		if err != nil {
			e.t.Fatal(err)
		}
		if !crt.Mined {
			e.mine(1) // mine the claim
			return
		}
		tip, tipHash := e.tipNow()
		e.logf("%s: the maker's claim %s is mined at %d in %s (depth %d, below C_zec %d, so the maker stays in \"claimed\"); the funding %s is at %d, %d deep at tip %d (%s)", n, s.claimTx, crt.Height, crt.BlockHash, tip-crt.Height+1, p.ConfZec, s.fundTx, s.fundH, tip-s.fundH+1, tip, tipHash)
		s.stage = 2
	case 2:
		// hold (no mining) until the taker's DERO Claim has landed: she read the secret from the mined claim
		if tk == nil || !tk.Done {
			return
		}
		e.logf("%s: the taker read the secret from the claim and her DERO Claim %q landed (she is %s); now the reorganisation removes the claim and its funding", n, tk.DeroTxs["claim"], tk.Phase)
		depth := func() uint32 { tip, _ := e.tipNow(); return tip - s.fundH + 1 }()
		if depth <= p.ConfZec {
			e.t.Fatalf("%s: the funding is only %d deep; this scenario is a reorganisation deeper than C_zec", n, depth)
		}
		_, s.removed = e.invalidate(n, s.fundH)
		// For b, spend the coin again at once — before the maker's next pass can re-broadcast the funding from its
		// bytes — so the funding can never be mined again (docs/ZEC_SWAP.md §9 Z5). For b2, nobody spends it: the
		// funding and claim are simply gone, and the maker must put them back himself.
		if s.double {
			s.conflict = e.doubleSpend(n, s.key, tk.FundingHex)
		}
		e.mine(int(s.removed) + 1)
		tip, tipHash := e.tipNow()
		at, _ := e.zc.BlockHash(e.ctx, s.fundH)
		e.logf("%s: a reorganisation %d blocks deep (deeper than C_zec %d); %d blocks mined on the new branch, one more than left: tip %d (%s); height %d is now %s, was %s. The funding is %s; the claim is %s", n, s.removed, p.ConfZec, s.removed+1, tip, tipHash, s.fundH, at, s.fundBlock, e.txStatus(s.fundTx), e.txStatus(s.claimTx))
		if s.double {
			e.logf("%s: the second spend %s is %s", n, s.conflict, e.txStatus(s.conflict))
		}
		r.noMine = false
		s.backAt, _ = r.deroMk.Height(e.ctx)
		e.logf("%s: harness mining resumes; the maker's companion (which never stopped) is in phase %s, escrow %s", n, mk.Phase, escrowWord(r.escrowState(mk.EscrowID)))
		s.stage = 3
	case 3:
		if mk.Done {
			return
		}
		if h, err := r.deroMk.Height(e.ctx); err == nil && h >= s.backAt+40 && (tk == nil || tk.Done) {
			e.logf("%s: the maker's companion is still in phase %s, 40 DERO blocks on: the run stops here", n, mk.Phase)
			r.stop = true
		}
	}
}
