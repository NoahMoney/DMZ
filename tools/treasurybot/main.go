// treasurybot is the one wallet-side step the Founders Pass needs: a DERO contract cannot pay another contract, so
// the protocol treasury claims its fees from every listed market, deposits the promised share into the Founders
// Pass contract and pushes the resulting rewards to the registered holders' wallets. Everything it does is public
// on chain (fc claimed per market, dep/paid on the pass), so anyone can check the promise is kept.
//
//	go run ./treasurybot -wallet 127.0.0.1:30002 [-config ../app/config.js] [-min-units 5000] [-interval 60s] [-once]
//
// The share is read from the pass contract (`share` bps, immutable), never from a flag. Amounts are integer
// atomic units everywhere (C5): -min-units 5000 is 0.05 DERO.
//
// The wallet's RPC login, when it asks for one, comes from VEIL_RPC_LOGIN (user:password), the variable netwallet
// starts it with. On a mainnet config the bot refuses, dry run included, before its first call when the variable is
// unset or when -wallet carries a login in its address (X-27).
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"time"

	ref "veil/reference"
	rpc "veil/tools/simrpc"
)

var (
	daemon   = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	wallet   = flag.String("wallet", "127.0.0.1:30002", "treasury wallet rpc, host:port (must be the treasury the markets pay); its login comes from VEIL_RPC_LOGIN (required on mainnet)")
	cfgPath  = flag.String("config", "../app/config.js", "app config (factory + founders scids + network)")
	minUnits = flag.Uint64("min-units", 5000, "claim a market's fees once they reach this many atomic units (5000 = 0.05 DERO)")
	interval = flag.Duration("interval", 60*time.Second, "how often to sweep")
	once     = flag.Bool("once", false, "one sweep, then exit")
	timeout  = flag.Duration("timeout", 180*time.Second, "per-tx timeout")
	jnPath   = flag.String("journal", "", "claim/deposit journal (default: treasurybot-<treasury address suffix>.json); see journal.go")
	yes      = flag.Bool("yes", false, "on mainnet, actually send (without it a mainnet config is a dry run)")
	dryRun   = flag.Bool("dry-run", false, "print what would be claimed and deposited, send nothing (reconcile included)")
	minOwed  = flag.Uint64("min-owed", 5000, "call PayOwed on an NFA once it owes the treasury this many units of that NFA's quote")
)

var dry bool // set in main: mainnet without -yes, or -dry-run

// The NFA pass (payowed.go): the template hashes an NFA must match, and the chain it pays through. Set in main.
var (
	nfaTpl map[string]bool
	oc     owedChain
)

// The registry lists this bot claims market fees from: a market's fees are claimable through ClaimFees and counted
// by fc. nft_ is not walked (nft.bas keeps its mint fee in fd behind its own ClaimFees, which this bot does not
// call). nfa_ is walked separately: nfa.bas owes its protocol fee to the treasury (ow_<treasury>) and pays it out
// only through PayOwed (payowed.go). Neither reaches the Founders Pass, which counts pool and launch DERO fees only.
var registryPrefixes = []struct{ prefix, count string }{{"pool_", "pc"}, {"launch_", "lc"}}

// cfgString reads a quoted word from config.js (network: "simulator").
func cfgString(src, key string) string {
	m := regexp.MustCompile(key + `:\s*"([a-z]+)"`).FindStringSubmatch(src)
	if m == nil {
		return ""
	}
	return m[1]
}

func cfgValue(src, key string) string {
	m := regexp.MustCompile(key + `:\s*"([0-9a-f]{64})"`).FindStringSubmatch(src)
	if m == nil {
		return ""
	}
	return m[1]
}

// checkWalletLogin refuses a mainnet config, before the first call, that could not authenticate to the wallet or
// that would carry its password on the command line. A mainnet netwallet always asks for a login, and a dry run
// reads the wallet too.
func checkWalletLogin(network string) error {
	if network != "mainnet" {
		return nil
	}
	return rpc.RequireWalletLogin(*wallet)
}

func main() {
	flag.Parse()
	src, err := os.ReadFile(*cfgPath)
	rpc.Must(err)
	factory, founders := cfgValue(string(src), "factory"), cfgValue(string(src), "founders")
	if factory == "" || founders == "" {
		log.Fatal("config.js has no factory/founders scid")
	}
	// fail closed on the network: an unreadable value is treated as mainnet, and the daemon must agree with it
	network := cfgString(string(src), "network")
	switch network {
	case "simulator", "testnet", "mainnet":
	default:
		log.Fatalf("config.js has no readable network (%q): refusing to guess which chain this is", network)
	}
	if err := checkWalletLogin(network); err != nil {
		log.Fatalf("mainnet: %v", err)
	}
	c := &rpc.Client{Daemon: *daemon, Timeout: *timeout}
	if testnet, name := c.Info(); (network == "mainnet") == testnet {
		log.Fatalf("config.js says network %q but the daemon at %s reports %q (testnet=%v)", network, *daemon, name, testnet)
	}
	dry = *dryRun || (network == "mainnet" && !*yes)
	if dry {
		fmt.Printf("DRY RUN (network %q): nothing will be sent; pass -yes on mainnet to move funds\n", network)
	}
	me := rpc.Address(*wallet)
	pass := c.GetSC(founders, false)
	share := pass.U("share")
	fmt.Printf("treasury %s\nfactory  %s\nfounders %s (share %d bps, %d registered units)\n", me, factory, founders, share, pass.U("hst"))
	if *jnPath == "" {
		*jnPath = "treasurybot-" + me[len(me)-12:] + ".json" // one journal per treasury identity, not per port
	}
	// one bot per journal: a second one would claim and deposit on records it never wrote
	lock := *jnPath + ".lock"
	lf, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		log.Fatalf("%s exists: another treasurybot runs on this journal (or crashed without removing it): %v", lock, err)
	}
	fmt.Fprintf(lf, "%d\n", os.Getpid())
	lf.Close()
	defer os.Remove(lock)
	jn, err := loadJournal(*jnPath)
	if err != nil {
		log.Fatal(err)
	}
	var ch chain = &deroChain{c: c, me: me, founders: founders}
	nfaTpl, oc = nfaTemplates(string(src)), &deroChain{c: c, me: me, founders: founders}
	if dry {
		ch = dryChain{ch}
		oc = dryOwed{oc}
	}
	if len(nfaTpl) == 0 {
		fmt.Println("config.js names no nfa template: no NFA can be verified, so no PayOwed will be sent")
	}
	if len(jn.Claims) > 0 || jn.Deposit != nil {
		fmt.Printf("journal %s: %d open claims, deposit in flight %v: reconciling\n", *jnPath, len(jn.Claims), jn.Deposit != nil)
	}
	if err := jn.reconcile(ch); err != nil && !errors.Is(err, errDry) { // finish what a previous run started before claiming anything new
		log.Fatal(err)
	}
	for {
		sweep(c, factory, founders, me, share, *minUnits, jn, ch)
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

func sweep(c *rpc.Client, factory, founders, me string, share, minUnits uint64, jn *journal, ch chain) {
	reg := c.GetSC(factory, false)
	var markets []string
	const deroQuote = "0000000000000000000000000000000000000000000000000000000000000000"
	var foreign []string
	for _, p := range registryPrefixes {
		for i := uint64(0); i < reg.U(p.count); i++ {
			s := reg.Str(p.prefix + strconv.FormatUint(i, 10))
			if len(s) != 64 {
				continue
			}
			// A market quoted in a wrapped asset accrues its fees in that asset. founders.bas counts one asset
			// and Deposit() reads DEROVALUE(), so depositing here would credit holders with DERO this market
			// never earned. Leave those fees where they are and say so.
			if q := ch.Quote(s); q != "" && q != deroQuote {
				foreign = append(foreign, s)
				continue
			}
			markets = append(markets, s)
		}
	}
	if len(foreign) > 0 {
		fmt.Printf("skipping %d market(s) whose fees are not DERO: founders.bas accounts for one asset, so these are owed but not sweepable\n", len(foreign))
		for _, s := range foreign {
			fmt.Printf("  %s fd %d in its own quote\n", s[:8], ch.FD(s))
		}
	}
	// NFA protocol fees: owed by each nfa.bas to the treasury until someone calls PayOwed (payowed.go). A dry run
	// prints what it would pay and sends nothing.
	nfaPass(reg, me)
	if dry {
		for _, scid := range markets {
			if fd := ch.FD(scid); fd >= minUnits {
				fmt.Printf("dry run: would claim %d units from %s…\n", fd, scid[:8])
			}
		}
		fmt.Printf("dry run: %d units already claimed and owed to the pass\n", jn.owed())
		return
	}
	for _, scid := range markets {
		fd := ch.FD(scid)
		if fd < minUnits {
			continue
		}
		if err := jn.claimMarket(ch, scid, fd); err != nil { // journaled before the send; reconciled on the next start
			log.Fatalf("claim %s…: %v", scid[:8], err)
		}
		fmt.Printf("%s claimed %d units from %s…\n", time.Now().Format("15:04:05"), jn.Claims[len(jn.Claims)-1].FD, scid[:8])
	}
	if owed := jn.owed(); owed > 0 {
		if err := jn.depositShare(ch, share); err != nil {
			log.Fatalf("deposit: %v", err)
		}
		fmt.Printf("%s deposited %d bps of %d claimed units to the founders pass\n", time.Now().Format("15:04:05"), share, owed)
	}
	// Push pending rewards to the registered wallets, 15 holders per call, for the slices that have something to
	// pay. `rpool != 0` is not that test: rpool never returns to zero, so it bought ceil(hn/15) transactions that
	// moved nothing every -interval, for ever. See payoutBatches.
	pass := c.GetSC(founders, false)
	batches := payoutBatches(pass)
	sent := 0
	for _, from := range batches {
		txid, _ := c.SendWithFee(*wallet, me, founders, nil, []rpc.Arg{rpc.S("entrypoint", "PayBatch"), rpc.U("from", from), rpc.U("n", 15)}, 3000)
		if len(txid) < 8 {
			log.Printf("PayBatch(%d, 15): the wallet returned no txid", from)
			continue
		}
		sent++
		fmt.Printf("%s PayBatch(%d, 15) tx %s…\n", time.Now().Format("15:04:05"), from, txid[:8])
	}
	if sent == 0 {
		return
	}
	after := c.GetSC(founders, false)
	fmt.Printf("%s pass: deposited %d, paid %d, owed %d, waiting %d, holders %d\n", time.Now().Format("15:04:05"), after.U("dep"), after.U("paid"), after.U("rpool"), after.U("und"), after.U("hn"))
}

// payoutBatches lists the slice starts PayBatch must be called with for this pass state: the ones that pay a
// holder something. An empty result means the sweep sends no transaction at all.
func payoutBatches(pass rpc.SCState) []uint64 {
	var out []uint64
	for from, hn := uint64(0), pass.U0("hn"); from < hn; from += 15 {
		if payoutSlice(pass, from, 15) > 0 {
			out = append(out, from)
		}
	}
	return out
}

// payoutSlice reports what PayBatch(from, n) would pay, in atomic DERO units, so the bot can tell a call that
// moves money from one that only costs a fee. It mirrors founders.bas: distribute(0) first (PayBatch line 30),
// then settle() for each holder of the slice (lines 41-42), capped by rpool the way settle() caps it.
//
// Why the bot needs it: distribute() credits rpool a ceiling (line 40) while settle() pays each holder a floor
// (line 40), so every distribution leaves a residual behind, and no entrypoint can release it — Claim, PayBatch,
// Withdraw and Register all pay through settle(), which can only take the same floor. rpool is therefore
// non-zero from the first distribution onward and stays that way, and gating the payout loop on it bought
// ceil(hn/15) empty transactions every sweep for the life of the pass.
func payoutSlice(pass rpc.SCState, from, n uint64) uint64 {
	hn := pass.U0("hn")
	if n == 0 || from >= hn {
		return 0
	}
	end := from + n
	if end > hn {
		end = hn
	}
	acc, rpool, rolled := afterDistribute(pass)
	if rolled {
		// The accumulator wraps 2^64 in the distribute() that PayBatch runs first, which takes a deposit of more
		// than 2^32 units of acc per registered pass: every holder is owed. Say so rather than model the era
		// file distribute() has not written yet.
		return rpool
	}
	era, sum := pass.U0("era"), uint64(0)
	for i := from; i < end; i++ {
		k := "h_" + strconv.FormatUint(i, 10)
		if !pass.Has(k) {
			continue
		}
		d, ok := holderPending(pass, pass.Str(k), acc, era)
		if !ok {
			return rpool // an arithmetic state the contract cannot reach: make the call and let it fail in the open
		}
		sum += d
		if sum >= rpool {
			return rpool
		}
	}
	return sum
}

// holderPending is settle()'s arithmetic for one holder (founders.bas:263-288) without the payment: one term for
// each era the holder has not been settled through, then the term for the current era. ok is false only where the
// contract's muldiv would panic.
func holderPending(pass rpc.SCState, a string, acc, era uint64) (uint64, bool) {
	if a == "" || !pass.Has("hu_"+a) {
		return 0, true
	}
	hu, hd, e := pass.U0("hu_"+a), pass.U0("hd_"+a), pass.U0("he_"+a)
	var pend uint64
	for ; e < era; e++ {
		t, ok := ref.MulDiv(hu, pass.U0("ea_"+strconv.FormatUint(e, 10))-hd, 1<<32)
		if !ok {
			return 0, false
		}
		pend += t
		hd = 0
	}
	t, ok := ref.MulDiv(hu, acc-hd, 1<<32)
	if !ok {
		return 0, false
	}
	return pend + t, true
}

// afterDistribute is distribute(0) (founders.bas:241-261) without the writes: what waits in und moves into acc
// and rpool once minhst units are registered. PayBatch runs it before it settles anybody, so the gate must too.
func afterDistribute(pass rpc.SCState) (acc, rpool uint64, eraRolled bool) {
	acc, rpool = pass.U0("acc"), pass.U0("rpool")
	pool, hst := pass.U0("und"), pass.U0("hst")
	if pool == 0 || hst == 0 || hst < pass.U0("minhst") {
		return acc, rpool, false
	}
	if ref.Mulhi(pool, 1<<32) >= hst { // lines 24-25: the clamp that keeps inc inside 64 bits
		pool = hst * (1<<32 - 1)
	}
	inc, ok := ref.MulDiv(pool, 1<<32, hst)
	if !ok {
		return acc, rpool, false
	}
	covered, ok := ref.MulDivUp(inc, hst, 1<<32)
	if !ok {
		return acc, rpool, false
	}
	if acc+inc < acc { // lines 32-35: the accumulator wraps, the era is filed and acc restarts at 0
		return inc, rpool + covered, true
	}
	return acc + inc, rpool + covered, false
}

// nfaPass is the sweep's NFA step: call PayOwed for the treasury on every listed NFA that runs the shipped template
// and owes it at least -min-owed, and say what moved. A failure is logged and left for the next sweep; it never
// stops the bot, because nothing about one NFA's balance bears on the market claims and the pass deposit.
func nfaPass(reg rpc.SCState, me string) {
	payee := rpc.DVMAddress(me) // ow_ keys use the spelling ADDRESS_STRING writes: the "dero" prefix, no arguments
	if payee == "" || oc == nil {
		return
	}
	paid, errs := payOwed(oc, registeredNFAs(reg), payee, nfaTpl, *minOwed)
	for scid, units := range paid {
		fmt.Printf("%s PayOwed on %s…: %d units of its quote paid to the treasury\n", time.Now().Format("15:04:05"), scid[:8], units)
	}
	for _, err := range errs {
		log.Print(err)
	}
}
