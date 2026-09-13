package zecswap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ref "veil/reference"
)

// TestRegtestEndToEnd runs whole swaps against a REAL zebrad 6.3.0 on regtest (tools/regtest) and the DERO simulator,
// through the same Swap machines, ZcashRPC and DeroRPC the command line runs, with RegtestPolicy. It records every
// transaction id in research/regtest/zecswap-e2e.log. Skipped unless ZECSWAP_REGTEST=1.
//
//	A  v5 claim; the taker is away from the moment she funds, and the maker's companion sends her DERO Claim for her
//	   once his own claim is deep (swap.bas v2 pays the named taker whoever sends it)
//	B  v6 (ZIP 229) claim; the maker is away once it is sent, so the taker's own watcher must read the secret out of
//	   the real v6 transaction and claim the DERO herself
//	C  refund: the maker is away past cltv, the taker away from funding on; a third party broadcasts the pre-signed
//	   refund from the refund file — refused before cltv, accepted at cltv, mined after it — and both come back to
//	   finish: the taker refunded on Zcash, the maker refunded on DERO after T_dero
//
// It installs its own board.bas v3 and swap.bas v2 (wmin 20) from DERO wallet 30013, and uses 30011 (maker) and
// 30012 (taker). Zcash keys are made here and never written anywhere but the test's temporary directory.
func TestRegtestEndToEnd(t *testing.T) {
	if os.Getenv("ZECSWAP_REGTEST") == "" {
		t.Skip("set ZECSWAP_REGTEST=1 to run against the real regtest zebrad and the DERO simulator (tools/regtest/README.md)")
	}
	e := newE2E(t)
	defer e.close()
	e.logf("== zecswap end to end on real nodes: zebrad 6.3.0 regtest %s, DERO simulator %s", e2eZcash, e2eDaemon)
	if note := os.Getenv("ZECSWAP_REGTEST_NOTE"); note != "" {
		e.logf("== note: %s", note)
	}
	run := os.Getenv("ZECSWAP_REGTEST_SWAPS")
	if run == "" {
		run = "ABC"
	}
	e.logf("== swaps to run: %s", run)
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.logf("zcash: chain %q (zebrad says \"test\"; block 0 is regtest's genesis), height %d, nextblock %08x, regtest %v", ci.Chain, ci.Height, ci.NextBranch, ci.Regtest)
	if !ci.Regtest || CheckNetworks("regtest", ci.Chain, true) != nil {
		t.Fatalf("not a regtest node: %+v", ci)
	}
	e.install()

	var takerKeys [3][32]byte
	for i := range takerKeys {
		takerKeys[i] = randomKey(t)
		k, _ := ParseKey(takerKeys[i])
		e.mineTo(k.Address(Testnet), 1)
		e.logf("zcash: taker key %d is %s; a coinbase is mined to it", i+1, k.Address(Testnet))
	}
	e.mine(101) // coinbase maturity: spendable 100 blocks after its own
	for i := range takerKeys {
		k, _ := ParseKey(takerKeys[i])
		us, err := e.zc.AddressUTXOs(e.ctx, k.Address(Testnet))
		if err != nil || len(us) == 0 {
			t.Fatalf("taker key %d has no coin: %v", i+1, err)
		}
		e.logf("zcash: taker key %d holds coinbase %s:%d, %d zatoshi, height %d", i+1, us[0].TxID, us[0].Index, us[0].Value, us[0].Height)
	}

	// ---- A ----
	if strings.Contains(run, "A") {
		a := e.runSwap("A", 5, takerKeys[0], func(r *e2eRun, role string) bool {
			if role != RoleTaker {
				return false
			}
			tk := r.state(RoleTaker)
			return tk != nil && tk.Phase == "funded" && tk.HaveEscrow && r.escrowState(tk.EscrowID) == EscrowOpen
		}, nil)
		if _, ok := a.mk.DeroTxs["relay"]; !ok || a.mk.Phase != "swapped" || a.tk.Phase != "swapped" {
			t.Fatalf("A: maker %s %v, taker %s", a.mk.Phase, a.mk.DeroTxs, a.tk.Phase)
		}
		e.logf("A: the taker's DERO Claim was sent by the maker's companion (DERO tx %s, from wallet %s), while the taker was away", a.mk.DeroTxs["relay"], e2eMaker)

	}

	// ---- B ----
	if strings.Contains(run, "B") {
		b := e.runSwap("B", 6, takerKeys[1], func(r *e2eRun, role string) bool {
			if role != RoleMaker {
				return false
			}
			mk, tk := r.state(RoleMaker), r.state(RoleTaker)
			return mk != nil && mk.Phase == "claimed" && (tk == nil || !tk.Done)
		}, nil)
		rt, err := e.zc.RawTx(e.ctx, b.mk.ClaimTxID)
		if err != nil || binaryHeader(rt.Raw) != V6Version || !rt.Verified {
			t.Fatalf("B: the maker's claim %s is not a verified v6 transaction: %08x %v", b.mk.ClaimTxID, binaryHeader(rt.Raw), err)
		}
		if b.tk.SecretSeen != b.mk.Secret || b.tk.SpendTxID != b.mk.ClaimTxID || b.tk.DeroTxs["claim"] == "" || b.tk.Phase != "swapped" {
			t.Fatalf("B: the taker did not read the secret from the v6 claim herself: %+v", b.tk)
		}
		e.logf("B: zebrad mined the v6 claim %s at height %d; the taker's watcher read the secret from it and sent her own DERO Claim %s", b.mk.ClaimTxID, rt.Height, b.tk.DeroTxs["claim"])
		hc, _ := (&Swap{st: b.mk}).htlc()
		capture, _ := json.MarshalIndent(map[string]string{
			"source": "the v6 (ZIP 229) claim of swap B in TestRegtestEndToEnd, built by this package and mined by zebrad 6.3.0 on regtest; research/regtest/zecswap-e2e.log",
			"txid":   b.mk.ClaimTxID, "hex": hex.EncodeToString(rt.Raw), "hash": b.mk.Hash, "redeem": hex.EncodeToString(hc.RedeemScript()),
		}, "", "  ")
		if err := os.WriteFile("testdata/regtest_v6_claim.json", append(capture, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}

	}

	// ---- C ----
	if strings.Contains(run, "C") {
		third := &thirdParty{}
		c := e.runSwap("C", 5, takerKeys[2], func(r *e2eRun, role string) bool {
			mk, tk := r.state(RoleMaker), r.state(RoleTaker)
			if role == RoleMaker {
				return mk != nil && mk.Phase == "locked" && mk.CLTV != 0 && r.zecTip() < mk.CLTV+3
			}
			// the taker is away from funding on, until the third party's refund is deep
			return tk != nil && (tk.Phase == "funded" || tk.Phase == "refunding") && !third.deep
		}, third.act)
		if !third.refusedEarly || third.txid == "" || c.tk.Phase != "refunded" || c.mk.Phase != "refunded" || c.tk.SpendTxID != third.txid {
			t.Fatalf("C: early refusal %v, third-party refund %q, taker %s (%s), maker %s", third.refusedEarly, third.txid, c.tk.Phase, c.tk.SpendTxID, c.mk.Phase)
		}
	}
	e.logf("== done: swaps %s; every id above is on the two chains", run)
}

const (
	e2eZcash  = "http://127.0.0.1:28232/"
	e2eCookie = "../../.sim/regtest/zcash/.cookie"
	e2eDaemon = "127.0.0.1:20000"
	e2eMaker  = "127.0.0.1:30011"
	e2eTaker  = "127.0.0.1:30012"
	e2eThird  = "127.0.0.1:30013"
	e2eLog    = "../../research/regtest/zecswap-e2e.log"
)

type e2e struct {
	t        *testing.T
	ctx      context.Context
	zc       *ZcashRPC
	log      *os.File
	mu       sync.Mutex
	board    string
	escrow   string
	makerKey [32]byte
	dir      string
	repo     string
	cookie   string  // absolute path to the zebrad cookie, for the command-line processes
	bin      string  // the built zecswap binary
	cn       *cliNet // where the command line runs; nil is regtest (regtest_cli_test.go)
}

func newE2E(t *testing.T) *e2e { return newE2EAt(t, e2eLog) }

// newE2EAt is the harness writing its evidence to logPath (appended to, never truncated).
// regtestCookie is the regtest zebrad's cookie: ZECSWAP_REGTEST_COOKIE, for a work tree whose own .sim holds no node,
// or the harness's default.
func regtestCookie() string {
	if c := os.Getenv("ZECSWAP_REGTEST_COOKIE"); c != "" {
		return c
	}
	return e2eCookie
}

func newE2EAt(t *testing.T, logPath string) *e2e {
	user, pass, err := ReadCookie(regtestCookie())
	if err != nil {
		t.Fatalf("the regtest zebrad's cookie: %v", err)
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	e := &e2e{t: t, ctx: context.Background(), zc: NewZcashRPC(e2eZcash, user, pass), log: f, makerKey: randomKey(t), dir: t.TempDir(), repo: repo}
	return e
}

func (e *e2e) close() { e.log.Close() }

func (e *e2e) logf(format string, a ...interface{}) {
	line := time.Now().UTC().Format("2006-01-02T15:04:05Z") + " " + fmt.Sprintf(format, a...)
	// the log is committed evidence: the test's temporary directory is this machine's path, so it is written as <tmp>
	// the most specific first: a work tree can itself sit under the temporary directory
	if e.dir != "" {
		line = strings.ReplaceAll(line, e.dir, "<tmp>")
	}
	if e.repo != "" {
		line = strings.ReplaceAll(line, e.repo, "<repo>")
	}
	if td := os.TempDir(); td != "" {
		line = strings.ReplaceAll(line, td, "<tmp>")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Fprintln(e.log, line)
	e.t.Log(line)
}

func randomKey(t *testing.T) [32]byte {
	for {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := ParseKey(b); err == nil {
			return b
		}
	}
}

func (e *e2e) zrpc(method string, params ...interface{}) json.RawMessage {
	var out json.RawMessage
	if err := e.zc.call(e.ctx, method, params, &out); err != nil {
		e.t.Fatalf("zebrad %s: %v", method, err)
	}
	return out
}

// mine mines n Zcash blocks to the node's own miner address; mineTo to an address of ours. It mines in small
// batches: one generate of many blocks can take longer than the RPC client's 30 s timeout once the regtest chain
// is tall (a few thousand blocks), so each call asks for at most a handful.
func (e *e2e) mine(n int) {
	for n > 0 {
		k := n
		if k > 8 {
			k = 8
		}
		e.zrpc("generate", k)
		n -= k
	}
}

func (e *e2e) mineTo(addr string, n int) { e.zrpc("generatetoaddress", n, addr) }

// install puts a contract on the DERO simulator from wallet 30013, canonicalised as the pins expect, and checks its
// code is the pinned code.
func (e *e2e) installOne(name, file string, args []deroArg, pin string) string {
	b, err := os.ReadFile(file)
	if err != nil {
		e.t.Fatal(err)
	}
	code := ref.Canon(string(b))
	w := e.where()
	c := NewDeroRPC(w.dero, w.installWallet, "", "")
	me, err := c.Address(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	est := append(append([]deroArg{}, args...), argU("SC_ACTION", 1), argS("SC_CODE", code))
	var g struct {
		GasStorage uint64 `json:"gasstorage"`
	}
	fee := uint64(len(code)) + 712
	if err := c.call(e.ctx, c.Daemon, "DERO.GetGasEstimate", map[string]interface{}{"sc": code, "sc_rpc": est, "signer": me, "ringsize": 2}, &g); err == nil {
		fee = g.GasStorage + 200
	} else if !strings.Contains(err.Error(), "Bucket cannot start") {
		e.t.Fatalf("%s refused at estimate: %v", name, err)
	}
	var r struct {
		TXID string `json:"txid"`
	}
	if err := c.call(e.ctx, c.Wallet, "transfer", map[string]interface{}{"sc": code, "sc_rpc": args, "ringsize": 2, "fees": fee}, &r); err != nil {
		e.t.Fatalf("installing %s: %v", name, err)
	}
	if err := c.waitTx(e.ctx, r.TXID); err != nil {
		e.t.Fatal(err)
	}
	got, err := c.CodeSHA256(e.ctx, r.TXID)
	if err != nil || got != pin {
		e.t.Fatalf("%s at %s hashes to %s (%v), the pin is %s", name, r.TXID, got, err, pin)
	}
	e.logf("dero: installed %s from %s: scid %s, fee %d, code sha256 %s (the pin)", name, w.installWallet, r.TXID, fee, got)
	return r.TXID
}

func (e *e2e) install() {
	e.board = e.installOne("board.bas v3", "../../contracts/board.bas", nil, BoardCodeSHA256)
	wmin := e.where().wmin // 20 on regtest; the taker's policy minimum (1,200) on testnet, or she refuses the escrow
	e.escrow = e.installOne(fmt.Sprintf("swap.bas v2 (wmin %d)", wmin), "../../contracts/swap.bas", []deroArg{argU("wmin", wmin)}, EscrowCodeSHA256)
	w, err := NewDeroRPC(e.where().dero, "", e.board, e.escrow).EscrowWMin(e.ctx)
	if err != nil || w != wmin {
		e.t.Fatalf("the escrow's wmin: %d %v", w, err)
	}
}

type e2eRun struct {
	e      *e2e
	dir    string
	mk, tk *State
	sw     map[string]*Swap
	deroMk *DeroRPC
	// noMine hands the Zcash miner to the scenario's act; stop ends the run where it stands (the run is then
	// recorded as it is, finished or not); lines is every line either machine logged, in order.
	noMine bool
	stop   bool
	lines  []string
}

// said reports whether a machine of this role has logged a line containing every one of the words.
func (r *e2eRun) said(role string, words ...string) bool {
	for _, l := range r.lines {
		if !strings.HasPrefix(l, role+": ") {
			continue
		}
		all := true
		for _, w := range words {
			if !strings.Contains(strings.ToLower(l), strings.ToLower(w)) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func (r *e2eRun) state(role string) *State {
	st, err := LoadState(filepath.Join(r.dir, role+".json"), role)
	if err != nil || st.Phase == "" && !st.Done && role == RoleTaker && r.sw[RoleTaker] == nil {
		return nil
	}
	return st
}

func (r *e2eRun) escrowState(id uint64) uint64 {
	es, err := r.deroMk.ReadEscrow(r.e.ctx, id)
	if err != nil {
		return 99
	}
	return es.State
}

func (r *e2eRun) zecTip() uint32 {
	ci, err := r.e.zc.ChainInfo(r.e.ctx)
	if err != nil {
		return 0
	}
	return ci.Height
}

// runSwap drives one swap to its end: both machines step each second, unless away says a party is absent; the
// Zcash chain is mined one block for each DERO block the simulator makes (RegtestPolicy's pace), and more once the
// taker's funding is in (bringing cltv sooner costs the taker nothing).
func (e *e2e) runSwap(name string, claimVersion uint32, takerKey [32]byte, away func(r *e2eRun, role string) bool, act func(r *e2eRun)) *e2eRun {
	r := &e2eRun{e: e, dir: filepath.Join(e.dir, name), sw: map[string]*Swap{}}
	os.MkdirAll(r.dir, 0o700)
	r.deroMk = NewDeroRPC(e2eDaemon, e2eMaker, e.board, e.escrow)
	deroTk := NewDeroRPC(e2eDaemon, e2eTaker, e.board, e.escrow)
	makerAddr, err := r.deroMk.Address(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	takerAddr, err := deroTk.Address(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	logf := func(format string, a ...interface{}) {
		line := fmt.Sprintf(format, a...)
		r.lines = append(r.lines, line)
		e.logf("%s: %s", name, line)
	}
	base := Config{Policy: RegtestPolicy(), Board: e.board, Escrow: e.escrow, ZcashChain: "regtest", Log: logf}
	mcfg := base
	mcfg.MyDero, mcfg.GiveAmount, mcfg.WantAmount, mcfg.TxVersion = makerAddr, 50_000, 200_000, claimVersion
	mk := e.makerKey
	mcfg.Key = func(Network) (*Key, error) { return ParseKey(mk) }
	tcfg := base
	tcfg.MyDero, tcfg.HaveOffer = takerAddr, true
	tcfg.Key = func(Network) (*Key, error) { return ParseKey(takerKey) }
	e.logf("%s: maker %s (DERO %s), taker %s (DERO %s); claim version %d", name, e2eMaker, makerAddr, e2eTaker, takerAddr, claimVersion)

	start := func(role string) *Swap {
		st, err := LoadState(filepath.Join(r.dir, role+".json"), role)
		if err != nil {
			e.t.Fatal(err)
		}
		var sw *Swap
		if role == RoleMaker {
			sw, err = NewMaker(mcfg, st, r.deroMk, NewZcashRPC(e2eZcash, e.zc.User, e.zc.Pass))
		} else {
			sw, err = NewTaker(tcfg, st, deroTk, NewZcashRPC(e2eZcash, e.zc.User, e.zc.Pass))
		}
		if err != nil {
			e.t.Fatal(err)
		}
		return sw
	}
	lastDero, err := r.deroMk.Height(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	deadline := time.Now().Add(75 * time.Minute)
	lastErr := map[string]string{}
	repeats := map[string]int{}
	for time.Now().Before(deadline) {
		mst := r.state(RoleMaker)
		if r.sw[RoleMaker] == nil {
			r.sw[RoleMaker] = start(RoleMaker)
		}
		if r.sw[RoleTaker] == nil && mst != nil && mst.HaveOffer {
			tcfg.OfferID = mst.OfferID
			r.sw[RoleTaker] = start(RoleTaker)
		}
		done := true
		for _, role := range []string{RoleMaker, RoleTaker} {
			sw := r.sw[role]
			if sw == nil {
				done = false
				continue
			}
			if sw.State().Done {
				continue
			}
			done = false
			if away != nil && away(r, role) {
				continue
			}
			// the same error pass after pass is logged once, with how often it came when it changes
			_, err := sw.Step(e.ctx)
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			if msg != lastErr[role] {
				if repeats[role] > 1 {
					logf("%s: (the step error above came %d times in all)", role, repeats[role])
				}
				if msg != "" {
					logf("%s: step error: %s", role, msg)
				}
				lastErr[role], repeats[role] = msg, 1
			} else if msg != "" {
				repeats[role]++
			}
		}
		if done {
			break
		}
		if act != nil {
			act(r)
		}
		if r.stop {
			break
		}
		if h, err := r.deroMk.Height(e.ctx); err == nil && h > lastDero {
			n := int(h - lastDero)
			if tk := r.state(RoleTaker); tk != nil && tk.FundingTxID != "" && !tk.Done {
				n *= 3
			}
			if !r.noMine {
				e.mine(n)
			}
			lastDero = h
		}
		time.Sleep(time.Second)
	}
	for role, n := range repeats {
		if n > 1 && lastErr[role] != "" {
			logf("%s: (the step error above came %d times in all)", role, n)
		}
	}
	r.mk, r.tk = r.sw[RoleMaker].State(), r.sw[RoleTaker].State()
	if !r.stop && (!r.mk.Done || !r.tk.Done) {
		e.t.Fatalf("%s did not finish: maker %s, taker %s", name, r.mk.Phase, r.tk.Phase)
	}
	e.record(name, r)
	return r
}

// record writes every id of a finished swap, from both state files, and what status says beside the nodes.
func (e *e2e) record(name string, r *e2eRun) {
	mk, tk := r.mk, r.tk
	e.logf("%s: RESULT maker %s — %s", name, mk.Phase, mk.Outcome)
	e.logf("%s: RESULT taker %s — %s", name, tk.Phase, tk.Outcome)
	e.logf("%s: board %s offer %d; escrow %s id %d, T_dero %d; hash %s; cltv %d", name, mk.Board, mk.OfferID, mk.Escrow, mk.EscrowID, mk.TDero, mk.Hash, tk.CLTV)
	for _, side := range []struct {
		who string
		st  *State
	}{{"maker", mk}, {"taker", tk}} {
		for call, txid := range side.st.DeroTxs {
			e.logf("%s: dero %s %s tx %s", name, side.who, call, txid)
		}
	}
	if tk.FundingTxID != "" {
		h := uint32(0)
		if tk.Funding != nil {
			h = tk.Funding.Height
		}
		e.logf("%s: zcash funding %s (v%d), %d zatoshi to %s, mined at %d", name, tk.FundingTxID, versionOf(e, tk.FundingTxID), tk.WantAmount, (&Swap{st: tk}).mustAddress(), h)
	}
	if mk.ClaimTxID != "" {
		e.logf("%s: zcash claim %s (v%d)", name, mk.ClaimTxID, versionOf(e, mk.ClaimTxID))
	}
	if tk.SpendTxID != "" && tk.SpendTxID != mk.ClaimTxID {
		e.logf("%s: zcash refund %s (v%d)", name, tk.SpendTxID, versionOf(e, tk.SpendTxID))
	}
	zsrc := []ZcashSource{{Name: e2eZcash, Node: e.zc}}
	dsrc := []DeroSource{{Name: e2eDaemon, Node: NewDeroRPC(e2eDaemon, "", mk.Board, mk.Escrow)}}
	for _, st := range []*State{mk, tk} {
		for _, line := range Status(e.ctx, st, zsrc, dsrc) {
			e.logf("%s: status %s", name, line)
		}
	}
}

func versionOf(e *e2e, txid string) uint32 {
	rt, err := e.zc.RawTx(e.ctx, txid)
	if err != nil {
		return 0
	}
	return binaryHeader(rt.Raw) & 0x7fffffff
}

// thirdParty is swap C's someone-else: it holds only the refund file.
type thirdParty struct {
	refusedEarly bool
	txid         string
	deep         bool
}

func (p *thirdParty) act(r *e2eRun) {
	tk := r.state(RoleTaker)
	if tk == nil || tk.RefundFile == "" || tk.FundingTxID == "" || p.deep {
		return
	}
	// the taker is away and records nothing more: the third party asks the node itself whether the funding is mined
	if frt, err := r.e.zc.RawTx(r.e.ctx, tk.FundingTxID); err != nil || !frt.Mined {
		return
	}
	b, err := os.ReadFile(tk.RefundFile)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	var level1 string
	for i, l := range lines {
		if strings.HasPrefix(l, "level 1 ") && i+1 < len(lines) {
			level1 = lines[i+1]
		}
	}
	raw, err := hex.DecodeString(level1)
	if err != nil {
		return
	}
	tip := r.zecTip()
	switch {
	case !p.refusedEarly && tip < tk.CLTV:
		_, err := r.e.zc.SendRawTx(r.e.ctx, raw)
		if err == nil {
			r.e.t.Fatalf("C: zebrad took the refund at tip %d, below cltv %d", tip, tk.CLTV)
		}
		p.refusedEarly = true
		r.e.logf("C: third party: the pre-signed refund (level 1) sent at Zcash tip %d, below cltv %d, is refused by zebrad: %v", tip, tk.CLTV, err)
	case p.refusedEarly && p.txid == "" && tip >= tk.CLTV:
		id, err := r.e.zc.SendRawTx(r.e.ctx, raw)
		if err != nil {
			r.e.logf("C: third party: the refund at tip %d was not taken yet: %v", tip, err)
			return
		}
		p.txid = id
		r.e.logf("C: third party: the pre-signed refund %s taken by zebrad at Zcash tip %d (cltv %d); nobody holding the file could change where it pays", id, tip, tk.CLTV)
	case p.txid != "":
		rt, err := r.e.zc.RawTx(r.e.ctx, p.txid)
		if err == nil && rt.Mined && rt.Confirmations >= r.e.policy().ConfZec {
			p.deep = true
			r.e.logf("C: third party: the refund %s is mined at height %d (cltv %d), %d confirmations", p.txid, rt.Height, tk.CLTV, rt.Confirmations)
		}
	}
}

func (e *e2e) policy() Policy { return RegtestPolicy() }
