// xmrswap runs one side of one DERO <-> Monero atomic swap (docs/XMR_SWAP_BUILD.md §3.D) for as long as that
// swap takes, then exits. It talks to the user's own DERO wallet and daemon and their own monero-wallet-rpc,
// and exchanges the protocol's messages with the counterparty through a directory both can reach. Its state
// file holds this party's key share (and, for bob, the gate secret); it is created 0600 and is the only place
// they are written. Restart the same command to resume.
//
//	xmrswap bob   -state bob.json   -dero 127.0.0.1:30006 -daemon 127.0.0.1:20000 -htlc <scid> -monero http://127.0.0.1:38082 -net stagenet -prover "proof-xmr" -channel ./swap -amount-dero 100000 -amount-xmr 1000000000000
//	xmrswap alice -state alice.json -dero 127.0.0.1:30007 -daemon 127.0.0.1:20000 -htlc <scid> -monero http://127.0.0.1:38082 -net stagenet -prover "proof-xmr" -channel ./swap -amount-dero 100000 -amount-xmr 1000000000000
//
// bob has DERO and locks first; alice has XMR and locks second. Both must give the same amounts.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"veil/tools/xmrswap"
)

func main() {
	if len(os.Args) < 2 || (os.Args[1] != "bob" && os.Args[1] != "alice") {
		fmt.Fprintln(os.Stderr, "usage: xmrswap bob|alice [flags]")
		os.Exit(2)
	}
	role := os.Args[1]
	fs := flag.NewFlagSet("xmrswap "+role, flag.ExitOnError)
	var (
		state      = fs.String("state", role+".json", "durable state (holds this party's key share; created 0600)")
		deroWallet = fs.String("dero", "127.0.0.1:30006", "this party's DERO wallet rpc")
		daemon     = fs.String("daemon", "127.0.0.1:20000", "DERO daemon rpc")
		htlc       = fs.String("htlc", "", "htlc.bas scid")
		monero     = fs.String("monero", "http://127.0.0.1:38082", "this party's monero-wallet-rpc")
		moneroUser = fs.String("monero-user", "", "monero-wallet-rpc --rpc-login user")
		moneroPass = fs.String("monero-pass", "", "monero-wallet-rpc --rpc-login password")
		moneroName = fs.String("monero-name", "", "the open wallet's file name, so it can be reopened after the joint wallets are made on the same rpc")
		net        = fs.String("net", "stagenet", "Monero network: mainnet, stagenet, testnet")
		prover     = fs.String("prover", "proof-xmr", "the proof-xmr command, e.g. \"wsl.exe -e /home/wilma/target/proof-xmr/release/proof-xmr\"")
		channel    = fs.String("channel", "./swap-channel", "directory both parties can reach (the rendezvous)")
		amountDero = fs.Uint64("amount-dero", 0, "DERO atomic units bob locks")
		amountXmr  = fs.Uint64("amount-xmr", 0, "piconero alice locks")
		t1         = fs.Uint64("t1", 0, "bob: blocks from the lock to t1 (default: t1-min + 20)")
		t2         = fs.Uint64("t2", 0, "bob: blocks from t1 to t2 (default: t2-min)")
		t1Min      = fs.Uint64("t1-min", xmrswap.DefaultT1Min, "policy: the least t1 - now either side accepts (lowering it is refused off the simulator)")
		t2Min      = fs.Uint64("t2-min", xmrswap.DefaultT2Min, "policy: the least t2 - t1 either side accepts")
		confs      = fs.Uint64("confs", xmrswap.DefaultConfirmations, "bob: Monero confirmations before the gate secret goes out (policy; lowering it is bob's own risk and must be typed)")
		stable     = fs.Uint64("stable", xmrswap.DefaultStableBlocks, "alice: DERO blocks the lock must age before XMR is locked")
		margin     = fs.Uint64("claim-margin", xmrswap.DefaultClaimMargin, "alice: blocks of slack under t1 − 8 after which she no longer sends a Claim")
		fee        = fs.Uint64("fee", 3000, "fee for Refund and Punish, which the daemon cannot estimate")
		interval   = fs.Duration("interval", 5*time.Second, "pause between passes while waiting on a chain")
	)
	fs.Parse(os.Args[2:])
	if len(*htlc) != 64 || *amountDero == 0 || *amountXmr == 0 {
		log.Fatal("-htlc (64 hex), -amount-dero and -amount-xmr are required")
	}
	typed := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { typed[f.Name] = true })
	// Invariant 10: the confirmation depth is the one number in the protocol that is policy rather than proof.
	// Below the default it is taken only from the operator's own hand, and said back loudly; below Monero's
	// spendable age it is refused outright, since the gate would open on an output nobody can spend yet.
	if *confs < xmrswap.DefaultConfirmations && !typed["confs"] {
		log.Fatalf("-confs %d is below the default %d and was not set on the command line", *confs, xmrswap.DefaultConfirmations)
	}
	if *confs < 10 {
		log.Fatalf("-confs %d is below CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE (10): nobody can spend the output before then, so the gate must not open on it", *confs)
	}
	if *confs < xmrswap.DefaultConfirmations {
		fmt.Fprintf(os.Stderr, "\nWARNING: -confs %d is below the default %d. Bob's safety against a Monero reorganisation after the gate rests on\n"+
			"this number and nothing else (docs/XMR_SWAP.md §9 R3, §10); an 18-block reorganisation has been reported.\n\n", *confs, xmrswap.DefaultConfirmations)
	}
	// Policy (review R18): a lying node is the one loss the protocol cannot see, and a node on another machine
	// is one this companion cannot vouch for. The companion still runs — the operator may have a reason — but
	// not quietly.
	for _, ep := range []struct{ flag, value string }{{"-daemon", *daemon}, {"-dero", *deroWallet}, {"-monero", *monero}} {
		if !loopback(ep.value) {
			fmt.Fprintf(os.Stderr, "\nWARNING: %s %s is not loopback. Everything this companion decides — the lock it reads, the confirmations\n"+
				"it counts, the transactions it signs — it takes from that endpoint on trust, and nothing in the protocol can tell a\n"+
				"lying node from an honest one (docs/XMR_SWAP.md §9 R18). Run your own node and wallet on this machine.\n\n", ep.flag, ep.value)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	network, err := xmrswap.ParseNetwork(*net)
	if err != nil {
		log.Fatal(err)
	}
	logf := func(format string, a ...interface{}) { fmt.Printf(time.Now().Format("15:04:05")+" "+format+"\n", a...) }
	dero := xmrswap.NewDeroRPC(*daemon, *deroWallet, *htlc)
	dero.FixedFee, dero.Log = *fee, logf
	testnet, name, err := dero.Info(ctx)
	if err != nil {
		log.Fatalf("daemon %s: %v", *daemon, err)
	}
	// The minimums are what keep the free option and the reorg risk inside §1's numbers. They may be lowered
	// to run a swap in minutes on the simulator, and nowhere else.
	if (*t1Min < xmrswap.DefaultT1Min || *t2Min < xmrswap.DefaultT2Min) && !(testnet && strings.EqualFold(name, "simulator")) {
		log.Fatalf("-t1-min/-t2-min below %d/%d is only allowed on the simulator; this daemon is %q", xmrswap.DefaultT1Min, xmrswap.DefaultT2Min, name)
	}
	myDero, err := dero.Address(ctx)
	if err != nil {
		log.Fatalf("DERO wallet %s: %v", *deroWallet, err)
	}
	wallet := xmrswap.NewWalletRPC(*monero, *moneroUser, *moneroPass)
	if _, err := wallet.Height(ctx); err != nil {
		log.Fatalf("monero-wallet-rpc %s: %v", *monero, err)
	}
	ch, err := xmrswap.NewDirChannel(*channel, role)
	if err != nil {
		log.Fatal(err)
	}
	st, err := xmrswap.LoadState(*state, role, *htlc)
	if err != nil {
		log.Fatal(err)
	}
	if err := takeLock(*state + ".lock"); err != nil {
		log.Fatal(err)
	}
	defer os.Remove(*state + ".lock")

	cfg := xmrswap.Config{HTLC: *htlc, AmountDero: *amountDero, AmountXmr: *amountXmr, Net: network, MyDero: myDero, MoneroWalletName: *moneroName,
		T1Min: *t1Min, T2Min: *t2Min, T1Delta: *t1, T2Delta: *t2, Confirmations: *confs, StableBlocks: *stable, ClaimMargin: *margin, Log: logf}
	prov := xmrswap.NewProverCLI(strings.Fields(*prover))
	var sw *xmrswap.Swap
	if role == "bob" {
		sw, err = xmrswap.NewBob(cfg, st, dero, wallet, prov, ch)
	} else {
		sw, err = xmrswap.NewAlice(cfg, st, dero, wallet, prov, ch)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: DERO %s, contract %s, state %s (phase %q), channel %s\n", role, myDero, *htlc, *state, st.Phase, *channel)
	for ctx.Err() == nil {
		before := sw.Phase()
		done, err := sw.Step(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("%v", err)
		}
		if done {
			fmt.Printf("%s: %s\n", role, st.Outcome)
			return
		}
		if sw.Phase() == before {
			select {
			case <-ctx.Done():
			case <-time.After(*interval):
			}
		}
	}
	fmt.Printf("%s: stopped in phase %s; run the same command to resume\n", role, sw.Phase())
}

// loopback says whether an endpoint ("host:port", a URL, or a bare host) names this machine.
func loopback(endpoint string) bool {
	host := endpoint
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return false
		}
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// takeLock keeps two companions off one state file: a second process would repeat the first one's payments.
// A lock older than ten minutes belonged to a process that died without cleaning up and is taken over.
func takeLock(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fi, serr := os.Stat(path)
		if serr != nil {
			return err
		}
		if age := time.Since(fi.ModTime()); age < 10*time.Minute {
			return fmt.Errorf("%s was touched %s ago: another companion is running on this state", path, age.Round(time.Second))
		}
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return f.Close()
}
