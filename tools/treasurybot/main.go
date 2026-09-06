// treasurybot is the one wallet-side step the Founders Pass needs: a DERO contract cannot pay another contract, so
// the protocol treasury claims its fees from every listed market, deposits the promised share into the Founders
// Pass contract and pushes the resulting rewards to the registered holders' wallets. Everything it does is public
// on chain (fc claimed per market, dep/paid on the pass), so anyone can check the promise is kept.
//
//	go run ./treasurybot -wallet 127.0.0.1:30002 [-config ../app/config.js] [-min-units 5000] [-interval 60s] [-once]
//
// The share is read from the pass contract (`share` bps, immutable), never from a flag. Amounts are integer
// atomic units everywhere (C5): -min-units 5000 is 0.05 DERO.
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
	wallet   = flag.String("wallet", "127.0.0.1:30002", "treasury wallet rpc (must be the treasury the markets pay)")
	cfgPath  = flag.String("config", "../app/config.js", "app config (factory + founders scids + network)")
	minUnits = flag.Uint64("min-units", 5000, "claim a market's fees once they reach this many atomic units (5000 = 0.05 DERO)")
	interval = flag.Duration("interval", 60*time.Second, "how often to sweep")
	once     = flag.Bool("once", false, "one sweep, then exit")
	timeout  = flag.Duration("timeout", 180*time.Second, "per-tx timeout")
	jnPath   = flag.String("journal", "", "claim/deposit journal (default: treasurybot-<treasury address suffix>.json); see journal.go")
	yes      = flag.Bool("yes", false, "on mainnet, actually send (without it a mainnet config is a dry run)")
	dryRun   = flag.Bool("dry-run", false, "print what would be claimed and deposited, send nothing (reconcile included)")
)

var dry bool // set in main: mainnet without -yes, or -dry-run

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
	if dry {
		ch = dryChain{ch}
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
	for _, p := range []struct{ prefix, count string }{{"pool_", "pc"}, {"launch_", "lc"}} {
		for i := uint64(0); i < reg.U(p.count); i++ {
			if s := reg.Str(p.prefix + strconv.FormatUint(i, 10)); len(s) == 64 {
				markets = append(markets, s)
			}
		}
	}
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
	// push pending rewards to every registered wallet, 15 holders per call
	pass := c.GetSC(founders, false)
	if pass.U("rpool") == 0 && pass.U("und") == 0 {
		return
	}
	hn := pass.U("hn")
	for from := uint64(0); from < hn; from += 15 {
		txid, _ := c.SendWithFee(*wallet, me, founders, nil, []rpc.Arg{rpc.S("entrypoint", "PayBatch"), rpc.U("from", from), rpc.U("n", 15)}, 3000)
		if len(txid) < 8 {
			log.Printf("PayBatch(%d, 15): the wallet returned no txid", from)
			continue
		}
		fmt.Printf("%s PayBatch(%d, 15) tx %s…\n", time.Now().Format("15:04:05"), from, txid[:8])
	}
	after := c.GetSC(founders, false)
	fmt.Printf("%s pass: deposited %d, paid %d, owed %d, waiting %d, holders %d\n", time.Now().Format("15:04:05"), after.U("dep"), after.U("paid"), after.U("rpool"), after.U("und"), after.U("hn"))
}
