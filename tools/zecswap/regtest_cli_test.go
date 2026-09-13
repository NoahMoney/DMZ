package zecswap

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const cliLog = "../../research/regtest/zecswap-cli.log"

// cliNet is where the command line runs: nil is regtest with the test mining the chain (TestRegtestCLI); the Zcash
// testnet is set by TestTestnetSwap.
type cliNet struct {
	network   string        // the -network flag
	zcash     string        // the node (regtest) or the gate (testnet)
	mine      bool          // the test mines the Zcash chain (regtest only)
	wmin      uint64        // swap.bas's wmin at install: the taker refuses one below her policy's minimum
	wantZat   uint64        // the offer's price in zatoshi
	interval  string        // the companions' -interval
	wait      time.Duration // how long one process may run; with followTDero, the maker's from T_dero on
	verifyAll bool          // -verify-headers on both swaps, not only the whole one
	// followTDero stretches the maker's wait to the DERO chain's pace (waitMaker), and stall is how long that chain may
	// stand still before the wait stops following it.
	followTDero bool
	stall       time.Duration
	// dero is the DERO daemon, and the three wallets are the maker's, the taker's and the one that installs the
	// contracts: the simulator's by default, the private DERO testnet's when TestTestnetSwap is pointed at it.
	dero, makerWallet, takerWallet, installWallet string
	// makerKey and takerKey are key files; empty makes a fresh key in the swap's directory (regtest, where the test
	// mines the taker's coin). On testnet they are the operator's funded keys, kept outside the repository.
	makerKey, takerKey string
	// networks is the deployment record the companions read (-networks); its zecTreasury is the swap fee's payee.
	// Empty is this work tree's deploy/networks.json.
	networks string
}

func (e *e2e) where() *cliNet {
	w := &cliNet{network: "regtest", zcash: e2eZcash, mine: true, wmin: 20, wantZat: 200_000, interval: "2s", wait: 75 * time.Minute}
	if e.cn != nil {
		w = e.cn
	}
	if w.dero == "" {
		w.dero, w.makerWallet, w.takerWallet, w.installWallet = e2eDaemon, e2eMaker, e2eTaker, e2eThird
	}
	if w.networks == "" {
		w.networks = filepath.Join(e.repo, "deploy", "networks.json")
	}
	return w
}

// TestRegtestCLI runs the zecswap COMMAND LINE — the built binary, as separate maker, taker and status processes —
// against the real regtest zebrad and the DERO simulator, for one whole swap and one refund. It records every id in
// research/regtest/zecswap-cli.log. Skipped unless ZECSWAP_REGTEST=1.
//
// The maker and taker are each `zecswap maker`/`zecswap taker` looping on their own; this test only feeds the Zcash
// chain blocks (one per DERO block, three once the funding is in, RegtestPolicy's pace) and, for the refund, sends
// the maker process a stop and starts it again — what a person would do. `zecswap status` is run against the live
// state at the end of each.
func TestRegtestCLI(t *testing.T) {
	if os.Getenv("ZECSWAP_REGTEST") == "" {
		t.Skip("set ZECSWAP_REGTEST=1 to run the zecswap command line against the real regtest zebrad and the DERO simulator")
	}
	e := newE2EAt(t, cliLog)
	defer e.close()
	bin := buildCLI(t)
	cookie, err := filepath.Abs(regtestCookie())
	if err != nil {
		t.Fatal(err)
	}
	e.cookie = cookie
	e.bin = bin
	e.logf("== zecswap COMMAND LINE on real nodes: binary %s, zebrad 6.3.0 regtest %s, DERO simulator %s", "<tmp>/zecswap", e2eZcash, e2eDaemon)
	if note := os.Getenv("ZECSWAP_REGTEST_NOTE"); note != "" {
		e.logf("== note: %s", note)
	}
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ci.Regtest {
		t.Fatalf("not a regtest node: %+v", ci)
	}
	e.logf("zcash: chain %q (block 0 is regtest's genesis), height %d; the command line is run with -network regtest", ci.Chain, ci.Height)
	e.install()

	e.cliSwap("S", false) // a whole swap: both sides present, both end swapped
	e.cliSwap("R", true)  // a refund: the maker is stopped after he locks and the taker refunds her ZEC at cltv
	e.logf("== done: the command line drove one swap and one refund")
}

// buildCLI builds cmd/zecswap to a temporary binary.
func buildCLI(t *testing.T) string {
	bin := filepath.Join(t.TempDir(), "zecswap.exe")
	out, err := exec.Command("go", "build", "-o", bin, "./cmd/zecswap").CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/zecswap: %v\n%s", err, out)
	}
	return bin
}

// proc is one running companion process.
type proc struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
}

// spawn starts `zecswap <role> ...` with its key file and DERO wallet, its stdout and stderr streamed to the log with
// the machine paths scrubbed. It runs until it ends the swap (and exits) or stop() cancels it.
func (e *e2e) spawn(tag, role, dir, wallet, keyPath string, extra ...string) *proc {
	w := e.where()
	args := []string{role,
		"-network", w.network,
		"-zcash", w.zcash, "-zcash-cookie", e.cookie,
		"-daemon", w.dero, "-wallet", wallet,
		"-board", e.board, "-escrow", e.escrow,
		"-key", keyPath, "-dir", dir,
		"-interval", w.interval,
		"-networks", w.networks,
	}
	args = append(args, extra...)
	ctx, cancel := context.WithCancel(e.ctx)
	cmd := exec.CommandContext(ctx, e.bin, args...)
	cmd.Dir, _ = filepath.Abs(".")
	pr := &proc{cmd: cmd, cancel: cancel, done: make(chan struct{})}
	out, err := cmd.StdoutPipe()
	if err != nil {
		e.t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout // one stream, in order
	if err := cmd.Start(); err != nil {
		e.t.Fatalf("%s: starting %s: %v", tag, role, err)
	}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			e.logf("%s %s| %s", tag, role, e.scrub(sc.Text(), dir))
		}
		close(pr.done)
	}()
	return pr
}

func (p *proc) wait() { <-p.done; p.cmd.Wait() }

// waitFor waits for the process to end, at most d; one still running then is stopped, and false is returned.
func (p *proc) waitFor(d time.Duration) bool {
	select {
	case <-p.done:
		p.cmd.Wait()
		return true
	case <-time.After(d):
		p.stop()
		return false
	}
}

// waitMaker waits for the maker's process: waitFor(w.wait), unless the network follows T_dero. A maker whose lock is
// open may have to wait for T_dero, 1,600 DERO blocks after the lock: about 6 h on the simulator, 22–27 h on the
// private DERO testnet. With followTDero the wait lasts while the DERO chain, moving, is below the maker's T_dero,
// however long that takes, and w.wait runs from the last moment it was; a maker that never locked gets w.wait from its
// start. A chain that has stood still for w.stall is no longer followed.
func (e *e2e) waitMaker(p *proc, dir string, w *cliNet) bool {
	if !w.followTDero {
		return p.waitFor(w.wait)
	}
	d := NewDeroRPC(w.dero, "", "", "")
	var top uint64
	moved, from := time.Now(), time.Now()
	for {
		select {
		case <-p.done:
			p.cmd.Wait()
			return true
		case <-time.After(time.Minute):
		}
		if h, err := d.Height(e.ctx); err == nil && h > top {
			top, moved = h, time.Now()
		}
		if st := e.cliState(dir, RoleMaker); st != nil && !st.Done && top < st.TDero && time.Since(moved) < w.stall {
			from = time.Now()
		}
		if time.Since(from) >= w.wait {
			p.stop()
			return false
		}
	}
}

// stop sends the process SIGINT-equivalent (context cancel) — what Ctrl+C does — and waits for it to go.
func (p *proc) stop() {
	p.cancel()
	<-p.done
	p.cmd.Wait()
}

func (e *e2e) scrub(s, dir string) string {
	for _, r := range []string{dir, e.dir, e.repo, os.TempDir()} {
		if r != "" {
			s = strings.ReplaceAll(s, r, "<tmp>")
		}
	}
	return s
}

// miner mines the Zcash chain while the processes run: one block per DERO block, three once the taker's funding is
// on the chain (bringing cltv sooner costs the taker nothing), until stopped. Regtest only.
func (e *e2e) miner(dir string, stop <-chan struct{}) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		d := NewDeroRPC(e2eDaemon, "", e.board, e.escrow)
		last, _ := d.Height(e.ctx)
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Second):
			}
			h, err := d.Height(e.ctx)
			if err != nil || h <= last {
				continue
			}
			n := int(h - last)
			if tk, err := LoadState(filepath.Join(dir, RoleTaker+".json"), RoleTaker); err == nil && tk.FundingTxID != "" && !tk.Done {
				n *= 3
			}
			// never t.Fatal from this goroutine: a failed generate is logged, and the next DERO block mines again
			for n > 0 {
				k := n
				if k > 8 {
					k = 8
				}
				if _, err := e.try("generate", k); err != nil {
					e.logf("miner: generate %d: %v", k, err)
					break
				}
				n -= k
			}
			last = h
		}
	}()
	return &wg
}

func (e *e2e) cliState(dir, role string) *State {
	st, err := LoadState(filepath.Join(dir, role+".json"), role)
	if err != nil {
		return nil
	}
	return st
}

// keyFile is the key a companion signs with: the configured file, or a fresh key written into the swap's directory.
func (e *e2e) keyFile(dir, role, given string) (string, *Key) {
	if given != "" {
		k, err := LoadKey(given, Testnet)
		if err != nil {
			e.t.Fatalf("the %s's key file: %v", role, err)
		}
		return given, k
	}
	raw := randomKey(e.t)
	path := filepath.Join(dir, role+".key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(raw[:])), 0o600); err != nil {
		e.t.Fatal(err)
	}
	k, _ := ParseKey(raw)
	return path, k
}

// balance is the key's confirmed transparent coins, in zatoshi.
func (e *e2e) balance(k *Key) uint64 {
	us, err := e.zc.AddressUTXOs(e.ctx, k.Address(Testnet))
	if err != nil {
		e.t.Fatalf("the taker's coins: %v", err)
	}
	var sum uint64
	for _, u := range us {
		sum += u.Value
	}
	return sum
}

func (e *e2e) cliSwap(tag string, refund bool) {
	w := e.where()
	dir := filepath.Join(e.dir, tag)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatal(err)
	}
	stop := make(chan struct{})
	var mining *sync.WaitGroup
	if w.mine {
		mining = e.miner(dir, stop)
	}
	endMining := func() {
		close(stop)
		if mining != nil {
			mining.Wait()
		}
	}
	makerKeyPath, mkKey := e.keyFile(dir, RoleMaker, w.makerKey)
	takerKeyPath, tkKey := e.keyFile(dir, RoleTaker, w.takerKey)
	if w.mine {
		// the taker funds from matured coinbases of her own: as many as the funding needs, because regtest's subsidy
		// halves every few hundred blocks, and on a tall chain one coinbase no longer covers it (at height 3,745 it
		// was 76,293 zatoshi against a 200,000-zatoshi funding)
		n := 0
		for ; n < 40 && e.balance(tkKey) < w.wantZat+4*MinimumFee; n++ {
			e.mineTo(tkKey.Address(Testnet), 1)
		}
		e.mine(101)
		e.logf("%s: the taker's Zcash coins are %d coinbases to %s, %d zatoshi; maker posts 50000 DERO for %d zatoshi", tag, n, tkKey.Address(Testnet), e.balance(tkKey), w.wantZat)
	} else {
		e.logf("%s: the taker funds from %s, holding %d zatoshi confirmed; the maker posts 50000 DERO for %d zatoshi, claimed to %s", tag, tkKey.Address(Testnet), e.balance(tkKey), w.wantZat, mkKey.Address(Testnet))
	}

	// -verify-headers on the whole swap always, and on both where the network asks for it; on regtest the gate is
	// skipped and says so, on testnet it checks the plumbing only
	var hdr []string
	if w.verifyAll || !refund {
		hdr = []string{"-verify-headers"}
	}
	makerArgs := append(append([]string{}, hdr...), "-give-amount", "50000", "-want-zat", fmt.Sprint(w.wantZat))
	maker := e.spawn(tag, RoleMaker, dir, w.makerWallet, makerKeyPath, makerArgs...)
	var taker *proc
	abort := func(format string, a ...interface{}) {
		maker.stop()
		if taker != nil {
			taker.stop()
		}
		endMining()
		e.t.Fatalf("%s: %s", tag, fmt.Sprintf(format, a...))
	}

	// wait for the maker to post, then start the taker on that offer
	offer := int64(-1)
	for i := 0; i < 180 && offer < 0; i++ {
		if st := e.cliState(dir, RoleMaker); st != nil && st.HaveOffer {
			offer = int64(st.OfferID)
		}
		time.Sleep(time.Second)
	}
	if offer < 0 {
		abort("the maker never posted an offer")
	}
	e.logf("%s: the maker posted offer %d; the taker is started on it", tag, offer)
	taker = e.spawn(tag, RoleTaker, dir, w.takerWallet, takerKeyPath, append(append([]string{}, hdr...), "-offer", fmt.Sprint(offer))...)

	if refund {
		// the maker is stopped once he has locked: he never claims, and the taker must refund
		for i := 0; i < 900; i++ {
			if st := e.cliState(dir, RoleMaker); st != nil && (st.Phase == "locked" || st.Done) {
				break
			}
			time.Sleep(time.Second)
		}
		maker.stop()
		e.logf("%s: the maker process is stopped after it locked (phase %s): it will not claim; the taker refunds at cltv", tag, e.cliState(dir, RoleMaker).Phase)
	}

	// the taker runs to its end
	if !taker.waitFor(w.wait) {
		abort("the taker process did not end within %s (phase %s)", w.wait, e.cliState(dir, RoleTaker).Phase)
	}
	tk := e.cliState(dir, RoleTaker)
	e.logf("%s: the taker process exited in phase %s", tag, tk.Phase)

	if refund {
		// the maker comes back to refund his DERO after T_dero. The killed process left its lock file; removing it
		// is the operator's recovery after a process died (it would otherwise clear on its own after the stale time).
		os.Remove(filepath.Join(dir, RoleMaker+".json.lock"))
		e.logf("%s: the maker process is started again to refund the DERO after T_dero %d", tag, e.cliState(dir, RoleMaker).TDero)
		maker = e.spawn(tag, RoleMaker, dir, w.makerWallet, makerKeyPath, makerArgs...)
	}
	if !e.waitMaker(maker, dir, w) {
		abort("the maker process did not end within %s (from T_dero, when the wait follows the chain; phase %s)", w.wait, e.cliState(dir, RoleMaker).Phase)
	}
	endMining()

	mk, tk := e.cliState(dir, RoleMaker), e.cliState(dir, RoleTaker)
	e.logf("%s: RESULT maker %s — %s", tag, mk.Phase, mk.Outcome)
	e.logf("%s: RESULT taker %s — %s", tag, tk.Phase, tk.Outcome)
	for _, side := range []struct {
		who string
		st  *State
	}{{"maker", mk}, {"taker", tk}} {
		for call, txid := range side.st.DeroTxs {
			e.logf("%s: dero %s %s tx %s", tag, side.who, call, txid)
		}
	}
	if tk.FundingTxID != "" {
		e.logf("%s: zcash funding %s (v%d)", tag, tk.FundingTxID, versionOf(e, tk.FundingTxID))
	}
	if mk.ClaimTxID != "" {
		e.logf("%s: zcash claim %s (v%d)", tag, mk.ClaimTxID, versionOf(e, mk.ClaimTxID))
	}
	if tk.SpendTxID != "" && tk.SpendTxID != mk.ClaimTxID {
		e.logf("%s: zcash refund %s (v%d)", tag, tk.SpendTxID, versionOf(e, tk.SpendTxID))
	}
	// `zecswap status`, the third process, against the live state
	e.cliStatus(tag, dir)

	want := "swapped"
	if refund {
		want = "refunded"
	}
	if mk.Phase != want || tk.Phase != want {
		e.t.Fatalf("%s: maker %s, taker %s; want %s", tag, mk.Phase, tk.Phase, want)
	}
}

func (e *e2e) cliStatus(tag, dir string) {
	cmd := exec.CommandContext(e.ctx, e.bin, "status",
		"-zcash", e.where().zcash, "-zcash-cookie", e.cookie,
		"-daemon", e.where().dero, "-dir", dir)
	cmd.Dir, _ = filepath.Abs(".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.logf("%s: status exited: %v", tag, err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		e.logf("%s status| %s", tag, e.scrub(line, dir))
	}
}
