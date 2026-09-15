// zecswap is the Zcash-side companion of the transparent-Zcash swap market (docs/ZEC_SWAP.md §7). Each person runs
// it on their own machine, against their own nodes and their own DERO wallet, for one swap; nobody hosts it and
// nothing in it holds anyone else's coins.
//
//	zecswap maker  -network N -key K -zcash URL -daemon H:P -wallet H:P -board SCID -escrow SCID
//	               {-give-amount N [-give-asset SCID] -want-zat N | -offer ID -secret-file F}  [-dir D] [-state S] [-payout T]
//	zecswap taker  -network N -key K -zcash URL -daemon H:P -wallet H:P -board SCID -escrow SCID -offer ID
//	               [-dir D] [-state S] [-refund-file F] [-payout T]
//	zecswap status -zcash URL -daemon H:P [-dir D]
//	zecswap script  {-hash H | -secret-file F} -claim <t-addr|pkh> -refund <t-addr|pkh> -cltv N -tip <Zcash height> [-net ...]
//	zecswap address <t-address>
//
// -network is mainnet, testnet or regtest, and has no default. The Zcash node must be on it and the DERO daemon on
// the matching kind, or nothing runs (zecswap.CheckNetworks).
//
// -zcash and -daemon may be repeated. The first of each is the primary; every further one is a witness that must
// agree before the step a swap's safety rests on (the maker's claim, which reveals the secret; the taker's funding).
// Every node named is a TRUSTED PARTY: the companion believes it about what its chain contains, and a single node —
// above all one on another machine — that colludes with the counterparty can cost the whole swap. More nodes make
// it take more liars; they never remove the trust. Run your own.
//
// -verify-headers turns on the header gate (tools/zecswap/spv.go, tools/zecspv): before the maker reveals the secret,
// the funding's block and every header above it, and before the taker funds, the headers up to the tip her cltv
// checks read, must carry real proof of work under a pinned floor. It is off by default until it has run against the
// Zcash testnet node. On regtest it is SKIPPED, and says so, because zebrad's regtest mines with no proof of work; on
// testnet it checks the plumbing only, because the minimum-difficulty rule makes forged headers nearly free. It reads
// getblock and getblockheader from the node, besides the seven methods every run needs.
//
// THE DERO WALLET must be started with --rpc-login user:password, and the companion given the same in the
// VEIL_RPC_LOGIN environment variable (or -dero-login, refused on mainnet because the process list shows it). A
// wallet RPC without a login authenticates nobody, and its /ws endpoint accepts every web page's connection, so
// while a swap runs any page could spend the wallet; on mainnet the companion refuses a wallet that answers a call
// made without credentials.
//
// -key is a file holding the one transparent Zcash key this swap spends with, as 64 hex characters or WIF: for the
// maker the claim key (the offer pays its P2PKH address), for the taker the key whose address holds the ZEC to fund
// with and which the refund pays back to. Use a key made for this purpose only. It is read for each signature and
// zeroed after; it is never written anywhere by this program.
//
// THE SWAP FEE. Every swap pays DMZ 0.5 % of the ZEC, in ZEC. The maker pays it: his claim of the taker's funding
// carries a second output of floor(funding × 50 / 10,000) zatoshi to the network's zecTreasury in the deployment record
// (-networks, always read), unless that would be below zebrad's dust threshold, when it is left out and the log says
// so. A refund pays none, and the taker's amounts do not change: her companion only states the fee. On mainnet a
// record with an empty or invalid zecTreasury refuses every swap. The fee is added by this program and enforced by no
// contract, and every fee output pays one public address, which lists every DMZ Zcash swap (docs/ZEC_SWAP.md §1, §7).
//
// FILES. The state lives in -dir (default: the user's configuration directory, zecswap/ — outside any repository),
// as -state (default <role>.json). zecswap.WrittenFiles lists every file written beside it; the program prints the
// list at start, and warns when any of them would land inside a git work tree.
//
// The Zcash node must be zebrad with its RPC enabled, or zcashd with txindex=1, experimentalfeatures=1 and
// insightexplorer=1 (see tools/zecswap/node.go for each method and what it needs). A lightwalletd is not supported.
//
// WHAT HAS RUN. Swaps have run against one real Zcash node only: zebrad 6.3.0 on regtest, beside the DERO simulator —
// through the Swap machines from tests (research/regtest/zecswap-e2e.log, zecswap-reorg.log) and through this command
// line as separate maker, taker and status processes (TestRegtestCLI, research/regtest/zecswap-cli.log). No swap has
// run on zcashd, Zcash testnet or Zcash mainnet; against the testnet node only the header gate's reads have been
// tested (research/regtest/zecswap-headers.log). The testnet swap (BL-42) is still to be done.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"veil/tools/swapstatus"
	"veil/tools/zecswap"
)

// tipDero is this party's own DERO client, noting the heights the swap read through it, for the -status answer only:
// its DERO deadlines are counted from them, and nothing else is read for them.
type tipDero struct {
	zecswap.Dero
	height, stable uint64
}

func (d *tipDero) Height(ctx context.Context) (uint64, error) {
	h, err := d.Dero.Height(ctx)
	if err == nil && h > d.height {
		d.height = h
	}
	return h, err
}

// StableHeight is noted too: a phase that waits for a block to become stable reads only this, and the tip is at least
// DERO's stable depth above it.
func (d *tipDero) StableHeight(ctx context.Context) (uint64, error) {
	s, err := d.Dero.StableHeight(ctx)
	if err == nil && s > d.stable {
		d.stable = s
	}
	return s, err
}

// tip is the highest DERO height this party has read or can infer from what it read: never above the chain's.
func (d *tipDero) tip() uint64 {
	if d.stable == 0 {
		return d.height
	}
	return max(d.height, d.stable+zecswap.DeroStableDepth)
}

// appNetwork is the DERO network as the app names it: mainnet, the simulator, or a test network.
func appNetwork(testnet bool, name string) string {
	switch {
	case !testnet:
		return "mainnet"
	case strings.EqualFold(name, "simulator"):
		return "simulator"
	}
	return "testnet"
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "maker", "taker":
		run(os.Args[1], os.Args[2:])
	case "status":
		status(os.Args[2:])
	case "script":
		script(os.Args[2:], os.Stdin)
	case "address":
		if len(os.Args) != 3 {
			usage()
		}
		net, kind, hash, err := zecswap.ParseAddress(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Printf("network %s\nkind    %s\nhash160 %x\n", net, kind, hash[:])
	default:
		usage()
	}
}

// endpoints is a repeatable flag.
type endpoints []string

func (e *endpoints) String() string     { return strings.Join(*e, ",") }
func (e *endpoints) Set(v string) error { *e = append(*e, v); return nil }

type nodeFlags struct {
	zcash     endpoints
	zcashUser *string
	zcashPass *string
	zcashCook *string
	daemons   endpoints
	board     *string
	escrow    *string
	dir       *string
}

func addNodeFlags(fs *flag.FlagSet, n *nodeFlags) {
	fs.Var(&n.zcash, "zcash", "a Zcash node's JSON-RPC (repeatable; the first is the primary). A witness's credentials go in its URL: http://user:pass@host:port")
	n.zcashUser = fs.String("zcash-user", "", "the primary Zcash node's rpcuser")
	n.zcashPass = fs.String("zcash-pass", "", "the primary Zcash node's rpcpassword")
	n.zcashCook = fs.String("zcash-cookie", "", "the primary Zcash node's cookie file (instead of -zcash-user/-zcash-pass)")
	fs.Var(&n.daemons, "daemon", "a DERO daemon's RPC (repeatable; the first is the primary)")
	n.board = fs.String("board", "", "board.bas scid")
	n.escrow = fs.String("escrow", "", "swap.bas scid")
	def, _ := zecswap.DefaultDir()
	n.dir = fs.String("dir", def, "the directory the swaps' files live in (default: outside any repository)")
}

func zcashSources(n *nodeFlags) ([]zecswap.ZcashSource, *zecswap.ZcashRPC) {
	if len(n.zcash) == 0 {
		log.Fatal("-zcash is required")
	}
	var out []zecswap.ZcashSource
	var primary *zecswap.ZcashRPC
	for i, e := range n.zcash {
		user, pass, endpoint := "", "", e
		if u, err := url.Parse(e); err == nil && u.User != nil {
			user = u.User.Username()
			pass, _ = u.User.Password()
			u.User = nil
			endpoint = u.String()
		}
		if i == 0 {
			if *n.zcashCook != "" {
				cu, cp, err := zecswap.ReadCookie(*n.zcashCook)
				if err != nil {
					log.Fatal(err)
				}
				user, pass = cu, cp
			} else if *n.zcashUser != "" || *n.zcashPass != "" {
				user, pass = *n.zcashUser, *n.zcashPass
			}
		}
		c := zecswap.NewZcashRPC(endpoint, user, pass)
		if i == 0 {
			primary = c
		}
		out = append(out, zecswap.ZcashSource{Name: endpoint, Node: c})
	}
	return out, primary
}

func run(role string, args []string) {
	fs := flag.NewFlagSet("zecswap "+role, flag.ExitOnError)
	var n nodeFlags
	addNodeFlags(fs, &n)
	network := fs.String("network", "", "mainnet, testnet or regtest (required, no default)")
	wallet := fs.String("wallet", "", "this party's DERO wallet RPC (it signs every DERO call, at ring size 2)")
	deroLogin := fs.String("dero-login", "", "the DERO wallet RPC's --rpc-login user:password; refused on mainnet — use VEIL_RPC_LOGIN")
	stateName := fs.String("state", role+".json", "the swap's state file, in -dir unless absolute (created 0600, and owner-only on Windows; for a maker it holds the secret)")
	keyFile := fs.String("key", "", "the file holding this swap's Zcash key (64 hex or WIF)")
	payout := fs.String("payout", "", "the transparent P2PKH address the claim (maker) or refund (taker) pays; default: the key's own")
	fixedFee := fs.Uint64("fee", 5000, "the DERO fee for the calls the estimator cannot judge (Refund, Cancel, and Post/Lock above height 1,000,000)")
	interval := fs.Duration("interval", 5*time.Second, "pause between passes while waiting on a chain")
	giveAsset := fs.String("give-asset", "", "maker: the asset to give, a 64 lower-case hex SCID (empty: DERO)")
	giveAmount := fs.Uint64("give-amount", 0, "maker: how much of it, in its atomic units")
	wantZat := fs.Uint64("want-zat", 0, "maker: how many zatoshi")
	offer := fs.Int64("offer", -1, "the board offer id: the taker's offer to take, or the maker's offer to adopt")
	secretFile := fs.String("secret-file", "", "maker adopting an offer: the file holding its 32-byte secret as 64 lower-case hex (- for stdin)")
	refundFile := fs.String("refund-file", "", "taker: where the pre-signed refund is written (default: beside the state file)")
	networks := fs.String("networks", "deploy/networks.json", "the deployment record, always read: the swap fee's treasury is its zecTreasury for this network, and -board and -escrow default to its entry when omitted (the code at the scid is still checked against the pinned hash)")
	verifyHeaders := fs.Bool("verify-headers", false, "verify Zcash proof of work (tools/zecspv) before the maker reveals and before the taker funds; skipped on regtest, plumbing only on testnet")
	statusAddr := fs.String("status", "", "127.0.0.1:PORT: serve this swap's stages to the DMZ app on this machine, behind a one-time pairing code printed at start (docs/SWAP_STATUS.md). Off by default; any host but 127.0.0.1 is refused")
	fs.Parse(args)
	if *statusAddr != "" {
		if _, err := swapstatus.ParseAddr(*statusAddr); err != nil {
			log.Fatal(err)
		}
	}
	if *keyFile == "" || *wallet == "" || len(n.daemons) == 0 {
		log.Fatal("-key, -wallet and -daemon are required")
	}
	if err := defaultContractsFromRecord(*network, *networks, n.board, n.escrow); err != nil {
		log.Fatal(err)
	}
	// the swap fee's treasury, from the record only; before any node is asked anything, a mainnet swap without a valid
	// one stops here
	treasury, noFee, err := treasuryFromRecord(*network, *networks)
	if err != nil {
		log.Fatal(err)
	}
	if len(*n.board) != 64 || len(*n.escrow) != 64 {
		log.Fatal("-board and -escrow must be 64-hex scids")
	}
	mainnet := *network == "mainnet"
	user, pass, err := zecswap.ResolveDeroLogin(*deroLogin, os.Getenv("VEIL_RPC_LOGIN"), mainnet)
	if err != nil {
		log.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// the state, the pre-signed refund and the two registers, restricted to this account from the moment each
		// exists: Windows ignores the 0600 they would be created with (zecswap.HardenFile)
		zecswap.HardenFile = zecswap.CreateOwnerOnly
	}
	statePath := *stateName
	if !filepath.IsAbs(statePath) {
		if err := os.MkdirAll(*n.dir, 0o700); err != nil {
			log.Fatal(err)
		}
		statePath = filepath.Join(*n.dir, statePath)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	zsrc, zprimary := zcashSources(&n)
	ci, err := zprimary.ChainInfo(ctx)
	if err != nil {
		log.Fatalf("Zcash node %s: %v", n.zcash[0], err)
	}
	dero := zecswap.NewDeroRPC(n.daemons[0], *wallet, *n.board, *n.escrow)
	dero.FixedFee, dero.WalletUser, dero.WalletPass = *fixedFee, user, pass
	testnet, deroNet, err := dero.Info(ctx)
	if err != nil {
		log.Fatalf("DERO daemon %s: %v", n.daemons[0], err)
	}
	if err := zecswap.CheckNetworks(*network, ci.Chain, testnet); err != nil {
		log.Fatal(err)
	}
	if mainnet {
		anon, err := dero.WalletAnswersWithoutLogin(ctx)
		if err != nil {
			log.Fatalf("DERO wallet %s: %v", *wallet, err)
		}
		if anon {
			log.Fatalf("the DERO wallet %s answers a call made without credentials: any program on this machine, and any web page through its /ws endpoint, could spend it while the swap runs. Restart it with --rpc-login and give the companion VEIL_RPC_LOGIN.", *wallet)
		}
	}
	var dwit []zecswap.DeroSource
	for _, d := range n.daemons[1:] {
		w := zecswap.NewDeroRPC(d, "", *n.board, *n.escrow)
		wt, _, err := w.Info(ctx)
		if err != nil {
			log.Fatalf("DERO witness %s: %v", d, err)
		}
		if wt != testnet {
			log.Fatalf("DERO witness %s is on another network than %s", d, n.daemons[0])
		}
		dwit = append(dwit, zecswap.DeroSource{Name: d, Node: w})
	}
	for _, z := range zsrc[1:] {
		wi, err := z.Node.ChainInfo(ctx)
		if err != nil {
			log.Fatalf("Zcash witness %s: %v", z.Name, err)
		}
		if wi.Chain != ci.Chain {
			log.Fatalf("Zcash witness %s is on %s, the primary on %s", z.Name, wi.Chain, ci.Chain)
		}
	}
	myDero, err := dero.Address(ctx)
	if err != nil {
		log.Fatalf("DERO wallet %s: %v", *wallet, err)
	}
	if testnet == strings.HasPrefix(strings.ToLower(myDero), "dero1") {
		log.Fatalf("the DERO wallet's address %s is not on the daemon's network (%s)", myDero, deroNet)
	}
	for _, w := range zecswap.TrustWarnings(n.zcash, n.daemons) {
		fmt.Fprintln(os.Stderr, w)
	}
	if w := zecswap.StateFileWarning(statePath); w != "" {
		fmt.Fprintf(os.Stderr, "WARNING: %s\n", w)
	}
	fmt.Fprintf(os.Stderr, "this swap's files: %s\n", strings.Join(zecswap.WrittenFiles(statePath), ", "))
	for _, p := range []string{statePath, *keyFile, *refundFile} {
		if p != "" && zecswap.InsideGitRepo(p) {
			fmt.Fprintf(os.Stderr, "WARNING: %s is inside a git work tree; a commit can carry it off. Keep secrets and keys outside any repository (the default -dir is).\n", p)
		}
	}
	fmt.Fprintln(os.Stderr, "zecswap has run swaps only on zebrad 6.3.0 regtest (research/regtest/zecswap-e2e.log, zecswap-cli.log); never a swap on zcashd, Zcash testnet or Zcash mainnet.")
	if noFee != "" {
		fmt.Fprintln(os.Stderr, noFee)
	} else {
		fmt.Fprintf(os.Stderr, "the swap fee: the maker's claim pays 0.5%% of the ZEC to the DMZ treasury %s, a public address that every DMZ Zcash swap pays; the companion adds it, no contract enforces it, and a refund pays none\n", treasury)
	}

	st, err := zecswap.LoadState(statePath, role)
	if err != nil {
		log.Fatal(err)
	}
	lock := &stateLock{path: statePath + ".lock", stale: staleAfter(*interval)}
	if err := lock.take(); err != nil {
		log.Fatal(err)
	}
	defer lock.release()

	logf := func(format string, a ...interface{}) { fmt.Printf(time.Now().Format("15:04:05")+" "+format+"\n", a...) }
	keyPath := *keyFile
	cfg := zecswap.Config{
		// §5's policy everywhere but regtest, which CheckNetworks above admitted only from a regtest node
		Policy: zecswap.PolicyFor(*network), ZcashChain: zecswap.ChainFor(*network),
		Board: *n.board, Escrow: *n.escrow, MyDero: myDero, DeroMainnet: !testnet,
		Key:    func(net zecswap.Network) (*zecswap.Key, error) { return zecswap.LoadKey(keyPath, net) },
		Payout: *payout, Treasury: treasury, GiveAsset: *giveAsset, GiveAmount: *giveAmount, WantAmount: *wantZat,
		RefundFile: *refundFile, ZcashName: n.zcash[0], DeroName: n.daemons[0],
		ZcashWitnesses: zsrc[1:], DeroWitnesses: dwit, Log: logf,
	}
	if *verifyHeaders {
		cfg.Headers = zecswap.DefaultHeaderCheck()
		fmt.Fprintln(os.Stderr, zecswap.HeaderGateNote(*network))
	}
	if *offer >= 0 {
		cfg.HaveOffer, cfg.OfferID = true, uint64(*offer)
	}
	if *secretFile != "" {
		s, err := readSecret(*secretFile, os.Stdin)
		if err != nil {
			log.Fatal(err)
		}
		cfg.Secret = s
	}
	var sw *zecswap.Swap
	tip := &tipDero{Dero: dero}
	if role == zecswap.RoleMaker {
		if cfg.HaveOffer != (cfg.Secret != "") {
			log.Fatal("a maker adopts an offer with both -offer and -secret-file, or posts a new one with neither")
		}
		sw, err = zecswap.NewMaker(cfg, st, tip, zprimary)
	} else {
		if !cfg.HaveOffer && st.Phase == "" {
			log.Fatal("-offer is required")
		}
		sw, err = zecswap.NewTaker(cfg, st, tip, zprimary)
	}
	if err != nil {
		log.Fatal(err)
	}
	// -status (docs/SWAP_STATUS.md): nothing is served unless asked, and a bind that fails stops the companion here,
	// before the swap takes a step
	appNet := appNetwork(testnet, deroNet)
	feed, err := swapstatus.Start(*statusAddr, func() swapstatus.Payload { return sw.StatusV1(appNet, tip.tip(), time.Now()) }, os.Stderr, logf)
	if err != nil {
		log.Fatal(err)
	}
	defer feed.Close()
	feed.Publish()
	fmt.Printf("%s: DERO %s (%s), Zcash %s, state %s (phase %q)\n", role, myDero, deroNet, ci.Chain, statePath, st.Phase)
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
			// whatever the end does is done before the outcome is served, so that stopping the program then loses nothing
			fmt.Printf("%s: %s\n", role, st.Outcome)
			feed.Linger(ctx, swapstatus.Linger)
			return
		}
		feed.Publish()
		if sw.Phase() == before {
			select {
			case <-ctx.Done():
			case <-time.After(*interval):
			}
		}
	}
	info := zecswap.Phases(role)[sw.Phase()]
	fmt.Printf("%s: stopped in phase %s; run the same command to resume. If it stays stopped: %s\n", role, sw.Phase(), info.IfStopped)
}

func status(args []string) {
	fs := flag.NewFlagSet("zecswap status", flag.ExitOnError)
	var n nodeFlags
	addNodeFlags(fs, &n)
	fs.Parse(args)
	if len(n.daemons) == 0 {
		log.Fatal("-daemon is required")
	}
	ctx := context.Background()
	zsrc, _ := zcashSources(&n)
	for _, w := range zecswap.TrustWarnings(n.zcash, n.daemons) {
		fmt.Println(w)
	}
	files, err := zecswap.StateFiles(*n.dir)
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			fmt.Printf("%s: %v\n", f, err)
			continue
		}
		st, err := zecswap.ParseState(b)
		if err != nil {
			fmt.Printf("%s: not a zecswap state (%v)\n", f, err)
			continue
		}
		st, err = zecswap.LoadState(f, st.Role)
		if err != nil {
			fmt.Printf("%s: %v\n", f, err)
			continue
		}
		var dsrc []zecswap.DeroSource
		for _, d := range n.daemons {
			dsrc = append(dsrc, zecswap.DeroSource{Name: d, Node: zecswap.NewDeroRPC(d, "", st.Board, st.Escrow)})
		}
		for _, line := range zecswap.Status(ctx, st, zsrc, dsrc) {
			fmt.Println(line)
		}
	}
}

// defaultContractsFromRecord fills -board and -escrow from the deployment record (deploy/networks.json) when either
// is omitted (MT-A18). The record's DERO network for each Zcash network: mainnet→mainnet, testnet→testnet,
// regtest→simulator. The scid is only a pointer to a row; the companion still checks that the code at it hashes to
// the pinned board.bas v3 / swap.bas v2 before trusting it (zecswap.CheckPins), so a wrong, stale or swapped id is
// refused, not obeyed. A record that has no scid for the network yet fails closed: give -board and -escrow.
func defaultContractsFromRecord(network, path string, board, escrow *string) error {
	if *board != "" && *escrow != "" {
		return nil
	}
	deroNet := recordNetwork[network]
	if deroNet == "" {
		return fmt.Errorf("-network %q has no deployment-record network; give -board and -escrow", network)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("-board/-escrow omitted and the deployment record %s could not be read (%v); give -board and -escrow, or point -networks at deploy/networks.json", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("the deployment record %s is not JSON: %w", path, err)
	}
	v, ok := raw[deroNet]
	if !ok {
		return fmt.Errorf("the deployment record %s has no %q network", path, deroNet)
	}
	var rec struct {
		Board string `json:"board"`
		Swap  string `json:"swap"`
	}
	if err := json.Unmarshal(v, &rec); err != nil {
		return fmt.Errorf("the deployment record %s: the %s network: %w", path, deroNet, err)
	}
	if *board == "" {
		*board = rec.Board
	}
	if *escrow == "" {
		*escrow = rec.Swap
	}
	if len(*board) != 64 || len(*escrow) != 64 {
		return fmt.Errorf("the deployment record %s has no board and swap scid for %s yet; give -board and -escrow (the scid is still checked against the pinned code hash)", path, deroNet)
	}
	return nil
}

// recordNetwork is the deployment record's DERO network for each Zcash network.
var recordNetwork = map[string]string{"mainnet": "mainnet", "testnet": "testnet", "regtest": "simulator"}

// treasuryFromRecord reads the network's zecTreasury from the deployment record, the way defaultContractsFromRecord
// reads board and swap. It is the address the maker's claim pays the 0.5 % swap fee to (zecswap.SwapFee), and it
// comes from the record only: no flag names another. It fails closed. A record that cannot be read, or an address that
// is not a transparent one for this network, refuses every swap. An empty one refuses a mainnet swap, and elsewhere
// comes back as a warning that no fee is taken.
func treasuryFromRecord(network, path string) (addr, noFee string, err error) {
	deroNet := recordNetwork[network]
	if deroNet == "" {
		return "", "", fmt.Errorf("-network %q has no deployment-record network, so its %s cannot be read", network, zecswap.TreasuryField)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("the deployment record %s could not be read (%v); the swap fee's treasury, %s, is read from it: point -networks at deploy/networks.json", path, err, zecswap.TreasuryField)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return "", "", fmt.Errorf("the deployment record %s is not JSON: %w", path, err)
	}
	v, ok := raw[deroNet]
	if !ok {
		return "", "", fmt.Errorf("the deployment record %s has no %q network, so no %s", path, deroNet, zecswap.TreasuryField)
	}
	var rec struct {
		ZecTreasury string `json:"zecTreasury"`
	}
	if err := json.Unmarshal(v, &rec); err != nil {
		return "", "", fmt.Errorf("the deployment record %s: the %s network: %w", path, deroNet, err)
	}
	if err := zecswap.CheckTreasury(rec.ZecTreasury, network == "mainnet"); err != nil {
		return "", "", fmt.Errorf("the deployment record %s, %s network, field %s: %w", path, deroNet, zecswap.TreasuryField, err)
	}
	if rec.ZecTreasury == "" {
		return "", fmt.Sprintf("WARNING: the deployment record's %s network has no %s, so this %s swap takes no swap fee", deroNet, zecswap.TreasuryField, network), nil
	}
	return rec.ZecTreasury, "", nil
}

// readSecret reads a secret from a file, or from stdin for "-" — never from the command line, where the process
// list and the shell history would show it (GAP-029).
func readSecret(path string, stdin io.Reader) (string, error) {
	var b []byte
	var err error
	if path == "-" {
		line, rerr := bufio.NewReader(io.LimitReader(stdin, 1024)).ReadString('\n')
		if rerr != nil && rerr != io.EOF {
			return "", rerr
		}
		b = []byte(line)
	} else if b, err = os.ReadFile(path); err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if _, err := zecswap.ParseSecret(s); err != nil {
		return "", err
	}
	return s, nil
}

// ---- the node-free commands ----

func script(args []string, stdin io.Reader) {
	fs := flag.NewFlagSet("script", flag.ExitOnError)
	hashHex := fs.String("hash", "", "the board's h_<id>: SHA256 of the maker's 32-byte secret, 64 lower-case hex")
	secretFile := fs.String("secret-file", "", "a file holding the maker's own secret as 64 lower-case hex (32 bytes), or - for stdin; the hash is derived from it. Use instead of -hash")
	claim := fs.String("claim", "", "the maker's transparent address (the offer's wantAddr) or its 40-hex key hash")
	refund := fs.String("refund", "", "the taker's transparent address or its 40-hex key hash (the accept's refundPkh)")
	cltv := fs.Uint("cltv", 0, "the Zcash height after which the taker may refund (the accept's cltv)")
	tip := fs.Uint("tip", 0, "the current Zcash height, from the node; -cltv is checked against it and the policy of docs/ZEC_SWAP.md §5. 0 means the swap is already funded and only the form of -cltv is checked")
	netName := fs.String("net", "testnet", "testnet or mainnet: only the address prefix depends on it")
	fs.Parse(args)
	var h zecswap.HTLC
	switch {
	case (*hashHex == "") == (*secretFile == ""):
		must(errors.New("give exactly one of them"), "-hash/-secret-file")
	case *secretFile != "":
		str, err := readSecret(*secretFile, stdin)
		must(err, "-secret-file")
		s, _ := zecswap.ParseSecret(str)
		h.Hash = sha256.Sum256(s[:])
		fmt.Printf("hash          %x (SHA256 of the secret; this is what Post and Lock carry)\n", h.Hash[:])
	default:
		var err error
		h.Hash, err = zecswap.ParseSwapHash(*hashHex)
		must(err, "-hash")
	}
	must(keyHash(&h.ClaimPkh, *claim), "-claim")
	must(keyHash(&h.RefundPkh, *refund), "-refund")
	if *cltv > 0xffffffff || *tip > 0xffffffff {
		must(errors.New("a Zcash height is a uint32"), "-cltv/-tip")
	}
	if *tip == 0 {
		must(zecswap.CheckCLTVHeight(uint32(*cltv)), "-cltv")
	} else {
		must(zecswap.CheckCLTV(uint32(*cltv), uint32(*tip)), "-cltv")
	}
	h.CLTV = uint32(*cltv)
	net := zecswap.Testnet
	switch *netName {
	case "testnet":
	case "mainnet":
		net = zecswap.Mainnet
	default:
		must(errors.New("testnet or mainnet"), "-net")
	}
	fmt.Printf("redeem script %x (%d bytes)\nscriptPubKey  %x\naddress       %s (%s)\nnLockTime     %d (the refund transaction's, at least)\n",
		h.RedeemScript(), len(h.RedeemScript()), h.ScriptPubKey(), h.Address(net), net, h.CLTV)
}

// keyHash takes either a transparent P2PKH address or a bare 40-hex key hash; a P2SH address is refused, since
// the script pays a key, not another script.
func keyHash(dst *[20]byte, s string) error {
	if len(s) == 40 {
		b, err := hex.DecodeString(s)
		if err != nil {
			return err
		}
		copy(dst[:], b)
		return nil
	}
	_, kind, hash, err := zecswap.ParseAddress(s)
	if err != nil {
		return err
	}
	if kind != zecswap.P2PKH {
		return fmt.Errorf("%s is a script address; the swap script pays a key", s)
	}
	*dst = hash
	return nil
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `zecswap maker  -network N -key K -zcash URL -daemon H:P -wallet H:P -board SCID -escrow SCID {-give-amount N [-give-asset SCID] -want-zat N | -offer ID -secret-file F} [-dir D] [-state S] [-payout T]
zecswap taker  -network N -key K -zcash URL -daemon H:P -wallet H:P -board SCID -escrow SCID -offer ID [-dir D] [-state S] [-refund-file F] [-payout T]
zecswap status -zcash URL -daemon H:P [-dir D]
zecswap script {-hash H|-secret-file F} -claim <t-addr|pkh> -refund <t-addr|pkh> -cltv N -tip <Zcash height> [-net testnet|mainnet]
zecswap address <t-addr>
-network is mainnet, testnet or regtest and has no default; -zcash and -daemon repeat; every node named is a trusted party.
The DERO wallet's login comes from VEIL_RPC_LOGIN (user:password).`)
	os.Exit(2)
}

// ---- one companion per state file ----

// stateLock keeps two companions off one state file: a second process would repeat the first one's broadcasts
// and calls. The file's modification time is the heartbeat; only a file that has stood still for stale belonged
// to a process that died without cleaning up.
type stateLock struct {
	path  string
	stale time.Duration
}

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

func (l *stateLock) touch() error {
	now := time.Now()
	return os.Chtimes(l.path, now, now)
}

func (l *stateLock) release() { os.Remove(l.path) }
