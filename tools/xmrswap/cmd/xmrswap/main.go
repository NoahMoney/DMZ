// xmrswap runs one side of one DERO <-> Monero atomic swap (docs/XMR_SWAP_BUILD.md §3.D) for as long as that
// swap takes, then exits. It talks to the user's own DERO wallet and daemon and their own monero-wallet-rpc,
// and exchanges the protocol's messages with the counterparty through a directory both can reach. Its state
// file holds this party's key share (and, for bob, the gate secret); it is created 0600, restricted to its owner
// on Windows, and is the only place they are written. Restart the same command to resume.
//
//	VEIL_RPC_LOGIN=<user>:<password> XMRSWAP_MONERO_LOGIN=<user>:<password> \
//	xmrswap bob   -state bob.json   -net stagenet -daemon 127.0.0.1:20000 -dero 127.0.0.1:30006 -monero http://127.0.0.1:38082 -htlc <scid> -prover <absolute path to proof-xmr> -channel ./swap -amount-dero 100000 -amount-xmr 1000000000000
//	xmrswap alice -state alice.json -net stagenet -daemon 127.0.0.1:20000 -dero 127.0.0.1:30007 -monero http://127.0.0.1:38082 -htlc <scid> -prover <absolute path to proof-xmr> -channel ./swap -amount-dero 100000 -amount-xmr 1000000000000
//	xmrswap status -dir . -daemon 127.0.0.1:20000 [-monero http://127.0.0.1:38082]
//
// bob has DERO (or the asset -asset names) and locks first; alice has XMR and locks second. Both must give the
// same amounts, the same asset and decimals. Before alice locks any XMR, bob sends her a stipend of DERO for her
// Claim and a Punish (-stipend, Path 0); her companion refuses to lock until it is in her wallet at stable depth.
//
// Nothing that names a network or an endpoint has a default (GAP-007): -net, -daemon, -dero and -monero are given
// every time, and the companion refuses to run unless the DERO daemon and the Monero wallet are on the same side of
// mainnet. Mainnet is never a default: -mainnet must be given whenever either chain is mainnet, and is refused
// unless both are. -net regtest (a local monerod --regtest, whose wallets use mainnet-form addresses) runs only
// against the DERO simulator.
//
// Both wallets are started with a login, and the companion is given it through the environment: VEIL_RPC_LOGIN for
// the DERO wallet (its --rpc-login; -dero-login off mainnet), XMRSWAP_MONERO_LOGIN for monero-wallet-rpc
// (-monero-user/-monero-pass off mainnet), and XMRSWAP_MONERO_WALLET_PASSWORD for the personal wallet's own
// password. On mainnet a login on the command line is refused — any local process can read it there — and so is a
// wallet that answers a call made without credentials (GAP-008, GAP-009).
//
// The monero-wallet-rpc must be started with --wallet-dir, with --rpc-login and without --restricted-rpc: the
// companion makes its two joint wallets with generate_from_keys and opens them by name, and on_open_wallet and
// on_generate_from_keys both refuse outright ("Command unavailable in restricted mode", "No wallet dir
// configured") otherwise. It must also be on the network -net names.
//
//	monero-wallet-rpc --stagenet --wallet-dir <dir> --rpc-bind-port 38082 --rpc-login <user>:<password> --daemon-address 127.0.0.1:38081
//
// On mainnet the companion also needs -monerod, the monerod the wallet-rpc must use, which it names to the
// wallet-rpc itself (set_daemon), because it cannot ask which one the wallet-rpc was started with and a lying
// monerod shows bob a lock that is not there (GAP-010); -monero, -monerod and -dero on loopback, since the joint
// spend key and the wallet logins travel to them; and -monero-wallet-dir, so the joint wallets' files are deleted
// once the sweep has gone out (GAP-020).
//
// -prover is the absolute path of the proof-xmr binary, run directly, and it must hash to the build pinned in the
// companion (xmrswap.ProverSHA256) before every run (GAP-003). A build whose sha256 differs is refused; off mainnet
// -prover-sha256 may name a development build instead.
//
// The rendezvous is sealed (GAP-019): the maker makes a key for the swap with `xmrswap channel-key` and gives it to
// the taker with the offer; both companions read it from XMRSWAP_CHANNEL_KEY and refuse to start without it. Every
// message in the -channel directory is AES-256-GCM under it, and one that does not open is dropped and said.
//
// status prints every swap whose state file is in -dir beside what the DERO chain and, with -monero, the joint
// Monero wallet say now, and lists every divergence; it exits 1 when there is one. It reads and never writes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"veil/tools/xmrswap"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "channel-key" {
		k, err := xmrswap.NewChannelKey()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(k)
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "status" {
		os.Exit(runStatus(os.Args[2:]))
	}
	if len(os.Args) < 2 || (os.Args[1] != "bob" && os.Args[1] != "alice") {
		fmt.Fprintln(os.Stderr, "usage: xmrswap bob|alice|status [flags], or xmrswap channel-key")
		os.Exit(2)
	}
	role := os.Args[1]
	o, err := parseOptions(role, os.Args[2:], os.Getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		log.Fatal(err)
	}
	// GAP-019: the channel is sealed under this swap's key, read from the environment before anything is contacted
	chanKey, err := channelKeyFrom(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if o.confs < xmrswap.DefaultConfirmations {
		fmt.Fprintf(os.Stderr, "\nWARNING: -confs %d is below the default %d. Bob's safety against a Monero reorganisation after the gate rests on\n"+
			"this number and nothing else (docs/XMR_SWAP.md §9 R3, §10); an 18-block reorganisation has been reported.\n\n", o.confs, xmrswap.DefaultConfirmations)
	}
	// Policy (review R18): a lying node is the one loss the protocol cannot see, and a node on another machine
	// is one this companion cannot vouch for. On mainnet the wallets and the monerod must be loopback (parseOptions);
	// the DERO daemon and, off mainnet, everything else is said out loud.
	for _, ep := range []struct{ flag, value string }{{"-daemon", o.daemon}, {"-dero", o.deroWallet}, {"-monero", o.monero}, {"-monerod", o.monerod}} {
		if ep.value != "" && !loopback(ep.value) {
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

	network, regtest, err := parseNet(o.netName)
	if err != nil {
		log.Fatal(err)
	}
	logf := func(format string, a ...interface{}) { fmt.Printf(time.Now().Format("15:04:05")+" "+format+"\n", a...) }
	dero := xmrswap.NewDeroRPC(o.daemon, o.deroWallet, o.htlc)
	dero.FixedFee, dero.Log, dero.Login = o.fee, logf, o.deroLogin
	testnet, name, err := dero.Info(ctx)
	if err != nil {
		log.Fatalf("daemon %s: %v", o.daemon, err)
	}
	simulator := testnet && strings.EqualFold(name, "simulator")
	deroNet := "testnet"
	switch {
	case !testnet:
		deroNet = "mainnet"
	case simulator:
		deroNet = "simulator"
	}
	// MT-A18: an omitted -htlc is the contract the record names for this daemon's DERO network; given or recorded, it
	// must be the pinned htlc.bas v3 (U4) before anything else is read from it
	if o.htlc == "" {
		scid, err := xmrswap.RecordedHTLC(deroNet)
		if err != nil {
			log.Fatal(err)
		}
		o.htlc = scid
		fmt.Fprintf(os.Stderr, "-htlc not given: using %s, the htlc.bas deploy/networks.json records for %s\n", scid, deroNet)
	}
	dero.SCID = o.htlc
	deploy, err := dero.Deployment(ctx)
	if err != nil || deploy.CodeSHA256 != xmrswap.HTLCCodeSHA256 {
		log.Fatalf("the contract %s is not the pinned htlc.bas v3 (code sha256 %q, %v): refused before anything is read from it (U4); a swap begun on htlc.bas v2 cannot be resumed with this build (docs/XMR_SWAP_BUILD.md §3.D)", o.htlc, deploy.CodeSHA256, err)
	}
	if deploy.W1Min == 0 || deploy.W2Min == 0 || deploy.G2 == 0 {
		log.Fatalf("the contract %s stores no w1min, w2min or g2 (%d, %d, %d): not an htlc.bas v3 install", o.htlc, deploy.W1Min, deploy.W2Min, deploy.G2)
	}
	fmt.Fprintf(os.Stderr, "htlc.bas v3 at %s, installed with w1min %d, w2min %d, g2 %d (Punish in [t2, t2 + %d), Reclaim from t2 + %d)\n", o.htlc, deploy.W1Min, deploy.W2Min, deploy.G2, deploy.G2, deploy.G2)
	// MT-B09: every further -daemon is a node the taker's lock check must agree with before she locks XMR. Each is
	// only read, and each must be on this party's DERO network.
	var witnesses []xmrswap.LockReader
	for _, addr := range o.daemons[1:] {
		w := xmrswap.NewDeroRPC(addr, "", o.htlc)
		w.Log = logf
		wt, wn, err := w.Info(ctx)
		if err != nil {
			log.Fatalf("DERO node %s: %v", addr, err)
		}
		if err := sameDeroNetwork(o.daemon, testnet, name, addr, wt, wn); err != nil {
			log.Fatal(err)
		}
		// MT-B09 for the install: every node must show the same code and the same three install arguments, since
		// v3's code alone does not say which install this is
		wd, err := w.Deployment(ctx)
		if err != nil {
			log.Fatalf("DERO node %s: reading %s: %v", addr, o.htlc, err)
		}
		if wd != deploy {
			log.Fatalf("DERO node %s shows %s as code %s with w1min %d, w2min %d, g2 %d; %s shows code %s with %d, %d, %d: refused before anything is locked (MT-B09)",
				addr, o.htlc, wd.CodeSHA256, wd.W1Min, wd.W2Min, wd.G2, o.daemon, deploy.CodeSHA256, deploy.W1Min, deploy.W2Min, deploy.G2)
		}
		witnesses = append(witnesses, w)
	}
	// The minimums are what keep the free option and the reorg risk inside §1's numbers, the gate deadline what
	// prices a suppressed Claim, the stipend what lets the taker pay for her own exits. They may be lowered to run
	// a swap in minutes on the simulator, and nowhere else.
	if (o.t1Min < xmrswap.DefaultT1Min || o.t2Min < xmrswap.DefaultT2Min || o.gateDeadline < xmrswap.DefaultGateDeadline ||
		o.stipend < xmrswap.DefaultStipend || o.stipendMin < xmrswap.DefaultStipend || o.g2Min < xmrswap.DefaultG2Min) && !simulator {
		log.Fatalf("-t1-min/-t2-min below %d/%d, -gate-deadline below %d, -stipend/-stipend-min below %d and -g2-min below %d are only allowed on the simulator; this daemon is %q",
			xmrswap.DefaultT1Min, xmrswap.DefaultT2Min, xmrswap.DefaultGateDeadline, xmrswap.DefaultStipend, xmrswap.DefaultG2Min, name)
	}
	myDero, err := dero.Address(ctx)
	if err != nil {
		log.Fatalf("DERO wallet %s: %v", o.deroWallet, err)
	}
	if err := checkNetworks(testnet, name, myDero, network, regtest, o.mainnet); err != nil {
		log.Fatal(err)
	}
	anonymous, err := dero.WalletAcceptsAnonymous(ctx)
	if err != nil {
		log.Fatalf("DERO wallet %s: %v", o.deroWallet, err)
	}
	wallet := xmrswap.NewWalletRPC(o.monero, o.moneroUser, o.moneroPass)
	// monero-wallet-rpc starts with no wallet open and answers -13 to get_height and get_address until one is
	// (0.18.5.1, research/regtest/monero-smoke.log): the personal wallet is opened first, by name
	if o.moneroName != "" {
		if err := wallet.OpenWallet(ctx, o.moneroName, o.moneroWalletPass); err != nil {
			log.Fatalf("monero-wallet-rpc %s: opening %s: %v", o.monero, o.moneroName, err)
		}
	}
	// Three things about the Monero side are settled here, before anything of either party's is locked. Until
	// this check existed they were all discovered at the first joint wallet — which for bob is after his DERO
	// is locked and irreversible until t1, and for alice is a transfer refused with the joint address on the
	// wrong network.
	addr, err := wallet.Address(ctx)
	if err != nil {
		log.Fatalf("monero-wallet-rpc %s: %v (a wallet-rpc starts with no wallet open: name yours with -monero-name)", o.monero, err)
	}
	warnings, err := authVerdict(deroNet == "mainnet", anonymous, xmrswap.WalletAskedForCredentials(wallet))
	if err != nil {
		log.Fatal(err)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "\nWARNING: %s\n\n", w)
	}
	if err := checkWalletNetwork(addr, network); err != nil {
		log.Fatalf("monero-wallet-rpc %s: %v", o.monero, err)
	}
	if regtest {
		fmt.Fprintf(os.Stderr, "\nWARNING: -net regtest. A regtest Monero wallet's addresses are mainnet-form, and nothing this companion can\n"+
			"read tells a regtest wallet-rpc from a mainnet one. The DERO side is the simulator, so nothing of value is locked on\n"+
			"DERO; if %s is not on a regtest chain, the XMR it sends is real and is being traded for simulator coins.\n\n", o.monero)
	}
	if err := walletManagement(wallet.OpenWallet(ctx, "xmrswap-probe-that-does-not-exist", "")); err != nil {
		log.Fatalf("monero-wallet-rpc %s: %v", o.monero, err)
	}
	pin := xmrswap.ProverSHA256
	if o.proverSHA != "" {
		pin = o.proverSHA
		fmt.Fprintf(os.Stderr, "\nWARNING: -prover-sha256 runs a proof-xmr build that is not the pinned one (%s). Refused on mainnet.\n\n", xmrswap.ProverSHA256)
	}
	prov, err := xmrswap.NewPinnedProver(o.prover, pin)
	if err != nil {
		log.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		xmrswap.HardenFile = xmrswap.OwnerOnlyACL // the state and the register, restricted to this account (GAP-020)
	}
	dir, err := xmrswap.NewDirChannel(o.channel, role)
	if err != nil {
		log.Fatal(err)
	}
	peer := "alice"
	if role == "alice" {
		peer = "bob"
	}
	ch, err := xmrswap.NewSealedChannel(dir, chanKey, role, peer)
	if err != nil {
		log.Fatal(err)
	}
	ch.OnDrop = func(reason string) { logf("channel: %s", reason) }
	st, err := xmrswap.LoadState(o.state, role, o.htlc)
	if err != nil {
		log.Fatal(err)
	}
	// A swap runs for hours, so the lock file must say "alive", not "was started": it is stamped again on every
	// pass and is only taken over after several intervals of silence.
	lock := &stateLock{path: o.state + ".lock", stale: staleAfter(o.interval)}
	if err := lock.take(); err != nil {
		log.Fatal(err)
	}
	defer lock.release()

	if w := xmrswap.StateFileWarning(o.state); w != "" {
		fmt.Fprintf(os.Stderr, "\nWARNING: %s\n\n", w)
	}
	// U9 (MT-B15): the card prints, and the wake runs, the very command that started this companion
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	cfg := xmrswap.Config{HTLC: o.htlc, AmountDero: o.amountDero, AmountXmr: o.amountXmr, Asset: o.asset, AssetDecimals: o.assetDecimals,
		Net: network, DeroNet: deroNet, MyDero: myDero, MoneroWalletName: o.moneroName, MoneroWalletPassword: o.moneroWalletPass, MoneroWalletDir: o.moneroWalletDir, Monerod: o.monerod,
		T1Min: o.t1Min, T2Min: o.t2Min, T1Max: o.t1Max, T2Max: o.t2Max, T1Delta: o.t1, T2Delta: o.t2, Confirmations: o.confs, StableBlocks: o.stable, ClaimMargin: o.margin,
		GateDeadline: o.gateDeadline, Stipend: o.stipend, StipendMin: o.stipendMin, G2Min: o.g2Min, Witnesses: witnesses,
		ResumeCommand: resumeCommand(exe, os.Args[1:]), Wake: xmrswap.RegisterWake, Log: logf}
	var sw *xmrswap.Swap
	if role == "bob" {
		sw, err = xmrswap.NewBob(cfg, st, dero, wallet, prov, ch)
	} else {
		sw, err = xmrswap.NewAlice(cfg, st, dero, wallet, prov, ch)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: DERO %s on %s, contract %s, state %s (phase %q), channel %s\n", role, myDero, deroNet, o.htlc, o.state, st.Phase, o.channel)
	for ctx.Err() == nil {
		if err := lock.touch(); err != nil {
			log.Fatalf("the lock file %s: %v", lock.path, err)
		}
		before := sw.Phase()
		done, err := sw.Step(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("%v", err)
		}
		if done {
			endSwap(role, st, logf)
			return
		}
		if sw.Phase() == before {
			select {
			case <-ctx.Done():
			case <-time.After(o.interval):
			}
		}
	}
	fmt.Printf("%s: stopped in phase %s; run the same command to resume\n", role, sw.Phase())
}

// removeWake removes a swap's wake task; tests replace it.
var removeWake = xmrswap.UnregisterWake

// endSwap prints how the swap ended and removes both wakes the recovery card registered (MT-B15, MT-J13): a finished
// swap leaves no task behind to open a console at a window that no longer matters.
func endSwap(role string, st *xmrswap.State, logf func(string, ...interface{})) {
	fmt.Printf("%s: %s\n", role, st.Outcome)
	for _, name := range xmrswap.WakeTaskNames(st.SwapID) {
		if err := removeWake(name); err != nil {
			logf("the wake task %s was not removed (%v): delete it from the task scheduler", name, err)
		}
	}
}

// options is one companion's command line, checked before anything is contacted.
type options struct {
	role                                                              string
	state, deroWallet, daemon, htlc, monero, monerod, netName, prover string
	proverSHA, channel, asset, moneroName, moneroWalletDir            string
	deroLogin, moneroUser, moneroPass, moneroWalletPass               string
	daemons                                                           []string // every -daemon, in order; the first is this party's own node
	mainnet                                                           bool
	amountDero, amountXmr, assetDecimals, stipend, stipendMin         uint64
	t1, t2, t1Min, t2Min, t1Max, t2Max, gateDeadline, g2Min           uint64
	confs, stable, margin, fee                                        uint64
	interval                                                          time.Duration
}

// resumeCommand is the command line that resumes this companion: the executable and the same arguments, each quoted
// where a shell would split it. The logins and the channel key are not in it; they come from the environment.
func resumeCommand(exe string, args []string) string {
	q := func(a string) string {
		if a == "" || strings.ContainsAny(a, " \t\"&|<>^") {
			return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		return a
	}
	parts := []string{q(exe)}
	for _, a := range args {
		parts = append(parts, q(a))
	}
	return strings.Join(parts, " ")
}

// channelKeyFrom reads the swap's channel key from XMRSWAP_CHANNEL_KEY (GAP-019). There is no flag for it: a command
// line is readable by every local process, and the key is what keeps the rendezvous private and unforgeable. A
// companion without one does not start; no plain channel is offered on any network.
func channelKeyFrom(getenv func(string) string) ([]byte, error) {
	v := getenv("XMRSWAP_CHANNEL_KEY")
	if v == "" {
		return nil, errors.New("XMRSWAP_CHANNEL_KEY is not set: the maker makes one per swap with `xmrswap channel-key` and gives it to the taker with the offer, and both companions read it from the environment (GAP-019)")
	}
	k, err := xmrswap.ParseChannelKey(v)
	if err != nil {
		return nil, fmt.Errorf("XMRSWAP_CHANNEL_KEY: %w", err)
	}
	return k, nil
}

// listFlag is a flag that may be given more than once; every value is kept, in order.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// sameDeroNetwork refuses a witness node on another DERO network than this party's own: what it says about a lock
// says nothing about the chain the lock is on (MT-B09).
func sameDeroNetwork(own string, ownTestnet bool, ownName, addr string, testnet bool, name string) error {
	if testnet != ownTestnet || !strings.EqualFold(name, ownName) {
		return fmt.Errorf("DERO node %s reports %q (testnet=%v), not %q (testnet=%v) as %s does: a node on another network cannot confirm a lock", addr, name, testnet, ownName, ownTestnet, own)
	}
	return nil
}

// parseOptions reads the flags and the environment and refuses, before any endpoint is contacted, every
// combination that could put coins at risk on its face: a network or endpoint left to a default (GAP-007), a prover
// that is not an absolute path (GAP-003), and on mainnet a login on the command line, a missing login, a missing or
// remote monerod, a remote wallet, or no wallet-dir to clean (GAP-008, GAP-009, GAP-010, GAP-020).
func parseOptions(role string, args []string, getenv func(string) string) (*options, error) {
	o := &options{role: role}
	fs := flag.NewFlagSet("xmrswap "+role, flag.ContinueOnError)
	fs.StringVar(&o.state, "state", role+".json", "durable state (holds this party's key share; created 0600, owner-only on Windows)")
	fs.StringVar(&o.netName, "net", "", "Monero network: mainnet (needs -mainnet), stagenet, testnet, or regtest (a local monerod --regtest; only against the DERO simulator). No default")
	fs.BoolVar(&o.mainnet, "mainnet", false, "name mainnet explicitly: required whenever the DERO daemon or -net is mainnet, refused unless both are")
	fs.Var((*listFlag)(&o.daemons), "daemon", "this party's DERO daemon rpc, host:port. No default. Give it again to name further DERO nodes: alice locks XMR only once every one of them shows her the same lock (MT-B09)")
	fs.StringVar(&o.deroWallet, "dero", "", "this party's DERO wallet rpc, host:port, started with --rpc-login. No default")
	fs.StringVar(&o.deroLogin, "dero-login", "", "user:password of the DERO wallet's --rpc-login; better VEIL_RPC_LOGIN, which mainnet requires (the command line is refused there)")
	fs.StringVar(&o.monero, "monero", "", "this party's monero-wallet-rpc URL, started with --rpc-login. No default")
	fs.StringVar(&o.monerod, "monerod", "", "the monerod the wallet-rpc must use, host:port: the companion names it to the wallet-rpc (set_daemon). Required, and loopback, on mainnet")
	fs.StringVar(&o.moneroUser, "monero-user", "", "monero-wallet-rpc --rpc-login user; better XMRSWAP_MONERO_LOGIN=user:password, which mainnet requires")
	fs.StringVar(&o.moneroPass, "monero-pass", "", "monero-wallet-rpc --rpc-login password; refused on mainnet, where the command line is readable by every local process")
	fs.StringVar(&o.moneroName, "monero-name", "", "the open wallet's file name, so it can be reopened after the joint wallets are made on the same rpc (its password: XMRSWAP_MONERO_WALLET_PASSWORD)")
	fs.StringVar(&o.moneroWalletDir, "monero-wallet-dir", "", "the wallet-rpc's --wallet-dir on this machine: the joint wallets' files are deleted there after the sweep. Required on mainnet")
	fs.StringVar(&o.htlc, "htlc", "", "htlc.bas scid (default: the one deploy/networks.json records for the daemon's DERO network, MT-A18); either way its code must hash to the pinned htlc.bas v3, installed with w1min, w2min and g2 inside this party's policy")
	fs.StringVar(&o.prover, "prover", "", "the absolute path of the proof-xmr binary; it must hash to the pinned build")
	fs.StringVar(&o.proverSHA, "prover-sha256", "", "off mainnet only: run a proof-xmr build with this sha256 instead of the pinned one")
	fs.StringVar(&o.channel, "channel", "./swap-channel", "directory both parties can reach (the rendezvous)")
	fs.Uint64Var(&o.amountDero, "amount-dero", 0, "atomic units of the DERO-side asset bob locks (DERO unless -asset)")
	fs.StringVar(&o.asset, "asset", "", "the DERO-side asset's 64-hex SCID (a token or an NFA unit); \"\" is DERO. Both sides must give the same")
	fs.Uint64Var(&o.assetDecimals, "asset-decimals", 0, "the asset's decimals, as both sides agree them")
	fs.Uint64Var(&o.amountXmr, "amount-xmr", 0, "piconero alice locks")
	fs.Uint64Var(&o.stipend, "stipend", xmrswap.DefaultStipend, "bob: the DERO atomic units he sends alice for her gas before she locks XMR (Path 0)")
	fs.Uint64Var(&o.stipendMin, "stipend-min", xmrswap.DefaultStipend, "alice: the least stipend she accepts, and the DERO she must be able to spend before locking XMR")
	fs.Uint64Var(&o.t1, "t1", 0, "bob: blocks from the lock to t1 (default: t1-min + 20)")
	fs.Uint64Var(&o.t2, "t2", 0, "bob: blocks from t1 to t2 (default: t2-min)")
	fs.Uint64Var(&o.t1Min, "t1-min", xmrswap.DefaultT1Min, "policy: the least t1 - now either side accepts (lowering it is refused off the simulator)")
	fs.Uint64Var(&o.t2Min, "t2-min", xmrswap.DefaultT2Min, "policy: the least t2 - t1 either side accepts")
	fs.Uint64Var(&o.t1Max, "t1-max", xmrswap.DefaultT1Max, "policy: the most t1 - now the taker accepts; until t1 her XMR has no exit of its own")
	fs.Uint64Var(&o.t2Max, "t2-max", xmrswap.DefaultT2Max, "policy: the most t2 - t1 the taker accepts")
	fs.Uint64Var(&o.gateDeadline, "gate-deadline", xmrswap.DefaultGateDeadline, "G: alice ignores a gate reaching her at or after t1 − G, and bob releases none that late (lowering it is refused off the simulator)")
	fs.Uint64Var(&o.g2Min, "g2-min", xmrswap.DefaultG2Min, "alice: the least g2 (the Punish window [t2, t2 + g2) of htlc.bas v3) she accepts in the install (lowering it is refused off the simulator; MT-J13)")
	fs.Uint64Var(&o.confs, "confs", xmrswap.DefaultConfirmations, "bob: Monero confirmations before the gate secret goes out (policy; lowering it is bob's own risk and must be typed)")
	fs.Uint64Var(&o.stable, "stable", xmrswap.DefaultStableBlocks, "alice: DERO blocks the lock must age before XMR is locked")
	fs.Uint64Var(&o.margin, "claim-margin", xmrswap.DefaultClaimMargin, "alice: blocks of slack under t1 − 8 after which she no longer sends a Claim")
	fs.Uint64Var(&o.fee, "fee", xmrswap.DefaultFixedFee, "fee for Refund and Punish, which the daemon cannot estimate")
	fs.DurationVar(&o.interval, "interval", 5*time.Second, "pause between passes while waiting on a chain")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if len(o.daemons) > 0 {
		o.daemon = o.daemons[0]
	}
	typed := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { typed[f.Name] = true })

	var missing []string
	for _, f := range []struct{ name, v string }{{"-net", o.netName}, {"-daemon", o.daemon}, {"-dero", o.deroWallet}, {"-monero", o.monero}, {"-prover", o.prover}} {
		if f.v == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s must be given: which network, whose daemon and wallets, and which prover are never left to a default (GAP-007)", strings.Join(missing, ", "))
	}
	if (o.htlc != "" && len(o.htlc) != 64) || o.amountDero == 0 || o.amountXmr == 0 {
		return nil, errors.New("-amount-dero and -amount-xmr are required, and -htlc, when given, is 64 hex (omitted, it is the htlc.bas deploy/networks.json records for the daemon's DERO network)")
	}
	// Invariant 10: the confirmation depth is the one number in the protocol that is policy rather than proof.
	// Below the default it is taken only from the operator's own hand; below Monero's spendable age it is refused
	// outright, since the gate would open on an output nobody can spend yet.
	if o.confs < xmrswap.DefaultConfirmations && !typed["confs"] {
		return nil, fmt.Errorf("-confs %d is below the default %d and was not set on the command line", o.confs, xmrswap.DefaultConfirmations)
	}
	if o.confs < 10 {
		return nil, fmt.Errorf("-confs %d is below CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE (10): nobody can spend the output before then, so the gate must not open on it", o.confs)
	}
	if !filepath.IsAbs(o.prover) {
		return nil, fmt.Errorf("-prover %q is not an absolute path: a bare name runs whatever program PATH finds first, and that program's exit status would decide whether the peer's proof holds (GAP-003)", o.prover)
	}
	if o.deroLogin == "" {
		o.deroLogin = getenv("VEIL_RPC_LOGIN")
	}
	if o.deroLogin != "" && !strings.Contains(o.deroLogin, ":") {
		return nil, errors.New("the DERO wallet login must be user:password")
	}
	if l := getenv("XMRSWAP_MONERO_LOGIN"); l != "" && o.moneroUser == "" && o.moneroPass == "" {
		user, pass, ok := strings.Cut(l, ":")
		if !ok {
			return nil, errors.New("XMRSWAP_MONERO_LOGIN must be user:password")
		}
		o.moneroUser, o.moneroPass = user, pass
	}
	o.moneroWalletPass = getenv("XMRSWAP_MONERO_WALLET_PASSWORD")
	if o.mainnet {
		switch {
		case typed["dero-login"]:
			return nil, errors.New("-dero-login is refused on mainnet: every local process can read a command line; give the login as VEIL_RPC_LOGIN (GAP-008)")
		case typed["monero-pass"]:
			return nil, errors.New("-monero-pass is refused on mainnet: every local process can read a command line; give the login as XMRSWAP_MONERO_LOGIN (GAP-009)")
		case o.deroLogin == "":
			return nil, errors.New("mainnet needs the DERO wallet's --rpc-login, as VEIL_RPC_LOGIN: a wallet RPC with no login lets any local process, and any web page through /ws, call transfer (GAP-008)")
		case o.moneroUser == "" || o.moneroPass == "":
			return nil, errors.New("mainnet needs the monero-wallet-rpc's --rpc-login, as XMRSWAP_MONERO_LOGIN: that wallet holds the joint spend key at sweep time (GAP-009)")
		case o.monerod == "":
			return nil, errors.New("mainnet needs -monerod: the companion cannot ask which monerod the wallet-rpc uses, and one that lies shows bob a lock that is not there (GAP-010)")
		case o.moneroWalletDir == "":
			return nil, errors.New("mainnet needs -monero-wallet-dir, the wallet-rpc's --wallet-dir, so the joint wallets holding s_a + s_b are deleted after the sweep (GAP-020)")
		case o.proverSHA != "":
			return nil, errors.New("-prover-sha256 is refused on mainnet: only the pinned proof-xmr build runs there (GAP-003)")
		}
		for _, ep := range []struct{ flag, v string }{{"-monero", o.monero}, {"-monerod", o.monerod}, {"-dero", o.deroWallet}} {
			if !loopback(ep.v) {
				return nil, fmt.Errorf("%s %s is not loopback: on mainnet the joint spend key and the wallet logins go to it, over plain HTTP, and a monerod elsewhere is one this companion cannot vouch for (GAP-010)", ep.flag, ep.v)
			}
		}
	}
	return o, nil
}

// authVerdict is GAP-008 and GAP-009 once both wallets have answered: on mainnet a DERO wallet that answers a call
// made without credentials, or a monero-wallet-rpc that never asked for them, is refused; elsewhere each is said.
func authVerdict(mainnet, deroAnonymous, moneroAsked bool) ([]string, error) {
	var warnings []string
	if deroAnonymous {
		msg := "the DERO wallet RPC answers calls made with no credentials: while the swap runs any local process, and any web page through its /ws endpoint, can call transfer on it; start it with --rpc-login (GAP-008)"
		if mainnet {
			return nil, errors.New("refusing: " + msg)
		}
		warnings = append(warnings, msg)
	}
	if !moneroAsked {
		msg := "the monero-wallet-rpc answered without asking for credentials: at sweep time it holds the joint spend key, and it can transfer and sweep; start it with --rpc-login (GAP-009)"
		if mainnet {
			return nil, errors.New("refusing: " + msg)
		}
		warnings = append(warnings, msg)
	}
	return warnings, nil
}

// parseNet is -net: the three Monero networks, and regtest, which uses mainnet-form addresses (a monerod
// --regtest chain is a fakechain on the mainnet address prefixes) and is told apart only by being named.
func parseNet(s string) (xmrswap.Network, bool, error) {
	if strings.EqualFold(s, "regtest") {
		return xmrswap.Mainnet, true, nil
	}
	n, err := xmrswap.ParseNetwork(s)
	return n, false, err
}

// checkNetworks is the fail-closed network rule, settled before any secret is made or anything locked.
//
// Mainnet is never a default: it must be named with -mainnet whenever the DERO daemon or the Monero network is
// mainnet, and -mainnet is refused unless the DERO daemon and the Monero network are both mainnet. Nothing may
// mix: a DERO wallet whose address is written for another network than its daemon, or a Monero network on one
// side of mainnet and DERO on the other, is a swap in which a lock on one chain is paid for on another. The Monero
// network here is the wallet's own (checkWalletNetwork requires its address to be on -net). regtest is accepted
// only against the DERO simulator, because its Monero addresses are indistinguishable from mainnet's and the one
// thing that can be required is that nothing of value is locked on the DERO side.
func checkNetworks(deroTestnet bool, deroNetwork, deroAddr string, net xmrswap.Network, regtest, mainnet bool) error {
	deroMain := !deroTestnet
	simulator := deroTestnet && strings.EqualFold(deroNetwork, "simulator")
	deroName := "a DERO test network"
	switch {
	case deroMain:
		deroName = "DERO mainnet"
	case simulator:
		deroName = "the DERO simulator"
	}
	switch p := xmrswap.DeroNetwork(deroAddr); {
	case deroMain && p != "dero":
		return fmt.Errorf("the daemon is %s and the DERO wallet's address %s is not a mainnet address: refusing to mix networks", deroName, deroAddr)
	case !deroMain && p != "deto":
		return fmt.Errorf("the daemon is %s and the DERO wallet's address %s is not a test-network address: refusing to mix networks", deroName, deroAddr)
	}
	if regtest {
		if mainnet {
			return errors.New("-net regtest and -mainnet name two different networks")
		}
		if !simulator {
			return fmt.Errorf("-net regtest runs only against the DERO simulator, and the daemon is %s: a regtest Monero wallet has mainnet-form addresses that nothing here can tell from mainnet, so the DERO side must hold nothing of value", deroName)
		}
		return nil
	}
	moneroMain := net == xmrswap.Mainnet
	if deroMain || moneroMain {
		if !mainnet {
			return fmt.Errorf("refusing mainnet without -mainnet: the daemon is %s and -net is %s; name mainnet explicitly", deroName, net)
		}
		if !deroMain || !moneroMain {
			return fmt.Errorf("refusing to mix networks: the daemon is %s and -net is %s", deroName, net)
		}
		return nil
	}
	if mainnet {
		return fmt.Errorf("-mainnet was given, and the daemon is %s and -net is %s", deroName, net)
	}
	return nil
}

// runStatus is `xmrswap status`: every swap whose state file is in -dir, beside what the chain and the Monero
// wallet say now. Its exit status is 0 when nothing diverges, 1 when something does, 2 when it could not look.
func runStatus(args []string) int {
	fs := flag.NewFlagSet("xmrswap status", flag.ExitOnError)
	dir := fs.String("dir", ".", "the directory holding the swaps' state files")
	daemon := fs.String("daemon", "", "this party's DERO daemon rpc, host:port. No default")
	monero := fs.String("monero", "", "this party's monero-wallet-rpc; \"\" leaves the Monero side unread")
	interval := fs.Duration("interval", 5*time.Second, "the companions' -interval, which says how fresh a running companion's lock file is")
	fs.Parse(args)
	if *daemon == "" {
		fmt.Fprintln(os.Stderr, "-daemon must be given: whose daemon is never left to a default")
		return 2
	}
	files, err := stateFiles(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(files) == 0 {
		fmt.Printf("no swap state files in %s\n", *dir)
		return 0
	}
	ctx := context.Background()
	var wallet xmrswap.Wallet
	if *monero != "" {
		user, pass, _ := strings.Cut(os.Getenv("XMRSWAP_MONERO_LOGIN"), ":")
		wallet = xmrswap.NewWalletRPC(*monero, user, pass)
	}
	code := 0
	for _, f := range files {
		st, err := loadAnyState(f)
		if err != nil {
			fmt.Printf("%s: %v\n", f, err)
			code = 2
			continue
		}
		d := xmrswap.NewDeroRPC(*daemon, "", st.HTLC)
		w := wallet
		if w != nil && lockHeld(f+".lock", staleAfter(*interval)) {
			w = nil // a companion is running on this swap: do not change which wallet its wallet-rpc has open
		}
		rep := xmrswap.Reconcile(ctx, st, d, w)
		fmt.Printf("%s\n", f)
		for _, l := range rep.Lines {
			fmt.Printf("  %s\n", l)
		}
		for _, l := range rep.Diverged {
			fmt.Printf("  DIVERGED: %s\n", l)
		}
		if len(rep.Diverged) > 0 && code == 0 {
			code = 1
		}
	}
	return code
}

// stateFiles is every swap state file in dir: the JSON files that are neither the used-secrets register nor a
// temporary write.
func stateFiles(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		if filepath.Base(m) == filepath.Base(xmrswap.UsedPath(filepath.Join(dir, "x"))) {
			continue
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}

// loadAnyState reads a state file's role and contract, then loads it the strict way the companion does.
func loadAnyState(path string) (*xmrswap.State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var head struct {
		Role string `json:"role"`
		HTLC string `json:"htlc"`
	}
	if err := json.Unmarshal(b, &head); err != nil || (head.Role != "bob" && head.Role != "alice") {
		return nil, errors.New("not a swap state file")
	}
	return xmrswap.LoadState(path, head.Role, head.HTLC)
}

// lockHeld says whether a companion's lock file was stamped within stale: that companion is alive.
func lockHeld(path string, stale time.Duration) bool {
	fi, err := os.Stat(path)
	return err == nil && time.Since(fi.ModTime()) < stale
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

// stateLock keeps two companions off one state file: a second process would repeat the first one's payments,
// and the Monero lock is the payment that cannot be taken back. The file's modification time is the heartbeat —
// touch stamps it on every pass of the Step loop — and only a file that has stood still for stale, which is
// several passes, belonged to a process that died without cleaning up.
type stateLock struct {
	path  string
	stale time.Duration
}

// staleAfter is how long a lock file may stand still before it is treated as a dead process's litter: ten
// passes of the Step loop, and never less than a minute, so a companion held up by a slow chain read is not
// mistaken for a dead one.
func staleAfter(interval time.Duration) time.Duration {
	d := 10 * interval
	if d < time.Minute {
		d = time.Minute
	}
	return d
}

func (l *stateLock) take() error {
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fi, serr := os.Stat(l.path)
		if serr != nil {
			return err
		}
		if age := time.Since(fi.ModTime()); age < l.stale {
			return fmt.Errorf("%s was touched %s ago: another companion is running on this state", l.path, age.Round(time.Second))
		}
		f, err = os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return f.Close()
}

// touch stamps the lock file now. A companion that cannot stamp it stops: the file is what keeps a second
// process from paying the joint address again, and one that has gone is not a thing to carry on without.
func (l *stateLock) touch() error {
	now := time.Now()
	if err := os.Chtimes(l.path, now, now); err != nil {
		return err
	}
	return nil
}

func (l *stateLock) release() { os.Remove(l.path) }

// checkWalletNetwork decodes the wallet's own primary address and requires it to be on the network -net names.
// cfg.Net is otherwise only used to BUILD the joint address and to compare the two parties' declared strings,
// so a wallet on another network is discovered at generate_from_keys — for bob, after his DERO is locked.
func checkWalletNetwork(addr string, want xmrswap.Network) error {
	pa, err := xmrswap.ParseAddress(addr)
	if err != nil {
		return fmt.Errorf("its own address %s does not decode: %v", addr, err)
	}
	if pa.Net != want {
		return fmt.Errorf("its own address %s is on %s, and -net says %s", addr, pa.Net, want)
	}
	return nil
}

// walletManagement reads what open_wallet answered for a name nobody has made, and says whether this
// wallet-rpc will let the companion make and open wallets at all. Started with --restricted-rpc, or without
// --wallet-dir, on_open_wallet and on_generate_from_keys refuse before they look at anything
// (wallet_rpc_server.cpp), and the companion would otherwise find that out at its first joint wallet — for the
// maker, after his DERO is locked and irreversible until t1. A configured wallet-dir answers "file not found",
// which is the pass.
func walletManagement(probe error) error {
	if probe == nil {
		return errors.New("open_wallet opened a wallet named xmrswap-probe-that-does-not-exist, which nothing should have made")
	}
	msg := probe.Error()
	switch {
	case strings.Contains(msg, "restricted mode"):
		return errors.New("it was started with --restricted-rpc, and cannot make or open the swap's joint wallets")
	case strings.Contains(msg, "No wallet dir"):
		return errors.New("it was started without --wallet-dir, and cannot make or open the swap's joint wallets")
	}
	return nil
}
