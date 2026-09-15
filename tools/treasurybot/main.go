// treasurybot is the wallet-side step the Founders Pass v2 needs: a DERO contract cannot pay another contract, so the
// protocol treasury claims its fees from every listed market (pool.bas and launch.bas ClaimFees: fd in the market's
// quote, fa in its asset) and deposits the promised share of each asset into the Founders Pass with v2's
// Deposit(asset), in the asset the fee was taken in, never converted to DERO. Each registered position then claims
// its part with Claim(p, i, ka, kb), which anyone may send and which pays the position's owner; this bot pushes no
// payouts. Everything it does is public on chain (fc per market, ad_<i> per asset on the pass), so anyone can check
// the share is deposited. The bot makes every deposit; no contract forces it.
//
//	go run ./treasurybot -wallet 127.0.0.1:30002 [-config ../app/config.js] [-min-units 5000] [-interval 60s] [-once]
//
// The share is read from the pass contract (`share` bps, immutable), never from a flag. Amounts are integer atomic
// units of their own asset everywhere (C5): -min-units 5000 is 0.05 DERO on a DERO leg.
//
// While no pass is registered, founders.bas v2 refuses every Deposit. The bot then holds what it owes the passes in
// its journal and deposits it once a pass registers, in the first deposit of each asset (the operator's ruling of
// 2026-09-13; journal.go).
//
// The wallet's RPC login, when it asks for one, comes from VEIL_RPC_LOGIN (user:password), the variable netwallet
// starts it with. On a mainnet config the bot refuses, dry run included, before its first call when the variable is
// unset or when -wallet carries a login in its address (X-27).
//
// It makes no XMR or ZEC payment. The Monero and Zcash addresses a holder may store on the pass with SetXmr and
// SetZec are data only: nothing pays them, and the app only shows a holder their own. The wrapped wXMR and wZEC are
// DERO tokens, and their fees are claimed and deposited like any other asset's. (The XMR/ZEC payouts of bb1ee34
// shared a swap fee that the operator's ruling of 2026-09-15 removed with the atomic swaps.)
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

	rpc "veil/tools/simrpc"
)

var (
	daemon   = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	wallet   = flag.String("wallet", "127.0.0.1:30002", "treasury wallet rpc, host:port (must be the treasury the markets pay); its login comes from VEIL_RPC_LOGIN (required on mainnet)")
	cfgPath  = flag.String("config", "../app/config.js", "app config (factory + founders scids + network)")
	minUnits = flag.Uint64("min-units", 5000, "claim a market's fees once either leg reaches this many atomic units of its own asset (5000 = 0.05 DERO on a DERO leg); the quote leg must hold at least one, since only it moves the fc counter that proves a claim")
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
// only through PayOwed (payowed.go). Neither reaches the Founders Pass, which is given the trading fees of pools and
// launches, per asset.
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

// checkWalletLogin refuses a mainnet config, before the first call, that could not authenticate to the wallet, that
// would carry its password on the command line, or whose wallet is not on this machine: the login rides in every wallet
// request as HTTP basic auth over plain HTTP. A mainnet netwallet always asks for a login, and a dry run reads the
// wallet too.
func checkWalletLogin(network string) error {
	if network != "mainnet" {
		return nil
	}
	if err := rpc.RequireWalletLogin(*wallet); err != nil {
		return err
	}
	return rpc.RequireLoopbackWallets(*wallet)
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
	// fail closed on the pass: this bot speaks v2's Deposit(asset) and reads v2's per-asset counters, and a v1 pass
	// would take a DERO-only Deposit() and credit nothing a v2 reader can check
	if v := pass.U0("v"); v != 2 {
		log.Fatalf("the founders contract %s… is not a Founders Pass v2 (v = %d): this bot deposits per asset with v2's Deposit(asset)", founders[:12], v)
	}
	share := pass.U("share")
	fmt.Printf("treasury %s\nfactory  %s\nfounders %s (v2, share %d bps, %d registered passes)\n", me, factory, founders, share, pass.U0("hst"))
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
	promoScid, promoTpl, pro = cfgValue(string(src), "promo"), promoTemplates(string(src)), &deroChain{c: c, me: me, founders: founders} // promo.go
	if dry {
		ch = dryChain{ch}
		oc = dryOwed{oc}
	}
	if len(nfaTpl) == 0 {
		fmt.Println("config.js names no nfa template: no NFA can be verified, so no PayOwed will be sent")
	}
	if len(jn.Claims) > 0 || len(jn.Deposits) > 0 {
		fmt.Printf("journal %s: %d open claims, %d deposits in flight: reconciling\n", *jnPath, len(jn.Claims), len(jn.Deposits))
	}
	if err := jn.reconcile(ch); err != nil && !errors.Is(err, errDry) { // finish what a previous run started before claiming anything new
		log.Fatal(err)
	}
	for {
		sweep(c, factory, me, share, *minUnits, jn, ch)
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

// market is one listed market and its two legs' asset ids.
type market struct{ scid, quote, asset string }

// sweep is one pass of the bot: claim every listed market's fees, then deposit the passes' share of each asset.
func sweep(c *rpc.Client, factory, me string, share, minUnits uint64, jn *journal, ch chain) {
	reg := c.GetSC(factory, false)
	var markets []market
	for _, p := range registryPrefixes {
		for i := uint64(0); i < reg.U(p.count); i++ {
			s := reg.Str(p.prefix + strconv.FormatUint(i, 10))
			if len(s) != 64 {
				continue
			}
			// every quote is swept: v2 has a pool per asset, so a wrapped-asset fee is deposited as that asset
			q, a := ch.Legs(s, p.prefix == "launch_")
			if !isID(q) || !isID(a) {
				fmt.Printf("skipping %s…: its quote or asset id cannot be read, so its fees could not be deposited as what they are\n", s[:8])
				continue
			}
			markets = append(markets, market{s, q, a})
		}
	}
	// NFA protocol fees: owed by each nfa.bas to the treasury until someone calls PayOwed (payowed.go). A dry run
	// prints what it would pay and sends nothing.
	nfaPass(reg, me)
	claimable := func(fd, fa uint64) bool { return fd > 0 && (fd >= minUnits || fa >= minUnits) }
	if dry {
		for _, m := range markets {
			if fd, fa := ch.Fees(m.scid); claimable(fd, fa) {
				fmt.Printf("dry run: would claim %d of %s… and %d of %s… from %s…\n", fd, m.quote[:8], fa, m.asset[:8], m.scid[:8])
			}
		}
		promoPass(jn, ch, me, minUnits)
		printOwed(jn, share, ch.Registered())
		return
	}
	for _, m := range markets {
		if fd, fa := ch.Fees(m.scid); !claimable(fd, fa) {
			continue
		}
		qd, ad, err := jn.claimMarket(ch, m.scid, m.quote, m.asset) // journaled before the send; reconciled on the next start
		if err != nil {
			log.Fatalf("claim %s…: %v", m.scid[:8], err)
		}
		if qd > 0 || ad > 0 {
			fmt.Printf("%s claimed %d of %s… and %d of %s… from %s…\n", time.Now().Format("15:04:05"), qd, m.quote[:8], ad, m.asset[:8], m.scid[:8])
		}
	}
	promoPass(jn, ch, me, minUnits) // promotion proceeds: owed to the passes in the auction's asset, like a market fee (promo.go)
	hst := ch.Registered()
	if err := jn.depositShare(ch, share); err != nil {
		log.Fatalf("deposit: %v", err)
	}
	printOwed(jn, share, hst)
}

// printOwed says what the journal still owes the passes, per asset, and why it waits.
func printOwed(jn *journal, share, hst uint64) {
	for _, a := range jn.assets() {
		o := jn.Owed[a]
		switch {
		case jn.Deposits[a] != nil:
			fmt.Printf("  %s…: a deposit of %d is in flight\n", a[:8], jn.Deposits[a].Units)
		case hst == 0:
			fmt.Printf("  %s…: %d claimed, %d owed to the passes, held: no pass is registered, and founders.bas v2 refuses a deposit until one is\n", a[:8], o, shareOf(o, share))
		default:
			fmt.Printf("  %s…: %d claimed and not yet deposited (its share, %d, rounds below one unit or waits for the next sweep)\n", a[:8], o, shareOf(o, share))
		}
	}
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
