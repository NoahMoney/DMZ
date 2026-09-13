package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"veil/tools/netcfg"
	rpc "veil/tools/simrpc"
	"veil/tools/xmrswap"
	"veil/tools/zecswap"
)

// The XMR and ZEC payouts' command line (payout.go). Each asset is configured on its own and fails closed on its own:
// anything missing or invalid switches that asset off and says why, and the DERO side runs exactly as before. On a
// mainnet config every check below is required.
var (
	netsPath     = flag.String("networks", "../deploy/networks.json", "deploy/networks.json, for the network's xmrTreasury and zecTreasury (the swap fees' addresses)")
	periodBlocks = flag.Uint64("period-blocks", 4800, "XMR/ZEC payout period in DERO blocks (4800 is about a day): a period ends at each multiple of it, once the daemon marks that height stable")
	xmrWallet    = flag.String("xmr-wallet", "", "the treasury's own monero-wallet-rpc, host:port, with the treasury wallet open; unset: no XMR payouts. Its --rpc-login comes from "+xmrLoginEnv+" (user:password; required on mainnet)")
	xmrNetFlag   = flag.String("xmr-net", "", "the Monero network paid on: mainnet (mainnet config only), stagenet, testnet, or regtest (mainnet's address prefixes)")
	xmrTreasFlag = flag.String("xmr-treasury", "", "the XMR fee address instead of networks.json's xmrTreasury (refused on mainnet)")
	xmrMinPart   = flag.Uint64("xmr-min-part", 100_000_000, "piconero: a holder's XMR part below this is carried, not paid; set it about the network fee (100000000 = 0.0001 XMR)")
	xmrJournal   = flag.String("xmr-journal", "", "XMR payout journal (default: -journal's name with .xmr.jsonl); it holds each payout's tx_metadata, so keep it out of the repository")
	zecNodesFlag = flag.String("zec-node", "", "the treasury's own Zcash node(s), comma-separated, the first primary; unset: no ZEC payouts. Login: -zec-cookie, or "+zecLoginEnv+" (user:password)")
	zecCookie    = flag.String("zec-cookie", "", "the Zcash node's cookie file")
	zecNetFlag   = flag.String("zec-net", "", "the Zcash network paid on: mainnet (mainnet config only), testnet or regtest")
	zecTreasFlag = flag.String("zec-treasury", "", "the ZEC fee address instead of networks.json's zecTreasury (refused on mainnet)")
	zecKeyFlag   = flag.String("zec-key", "", "file holding zecTreasury's private key (64 hex or WIF), outside the repository; read at start, never printed")
	zecMinPart   = flag.Uint64("zec-min-part", 10_000, "zatoshi: a holder's ZEC part below this is carried, not paid (10000 is ZIP 317's minimum fee)")
	zecJournal   = flag.String("zec-journal", "", "ZEC payout journal (default: -journal's name with .zec.jsonl)")
)

const (
	xmrLoginEnv = "VEIL_XMR_RPC_LOGIN"
	zecLoginEnv = "VEIL_ZEC_RPC_LOGIN"
)

// payoutFlags is the command line's XMR/ZEC part, gathered so the checks can be tested without flags.
type payoutFlags struct {
	network                                  string          // config.js's DERO network
	root                                     string          // the repository: no key file may be in it
	nw                                       *netcfg.Network // this network's block of networks.json, or nil
	nwErr                                    error
	xmrWallet, xmrNet, xmrTreasury, xmrLogin string
	zecNodes                                 []string
	zecNet, zecTreasury, zecKey              string
	zecLogin, zecCookie                      string
	xmrMin, zecMin                           uint64
}

type xmrPlan struct {
	wallet, login, treasury string
	want                    xmrswap.Network
	min                     uint64
}

type zecPlan struct {
	nodes             []string
	user, pass, chain string
	net               zecswap.Network
	treasury          string
	key               *zecswap.Key
	min               uint64
}

// planPayouts checks each asset's configuration and returns its plan, or nil and the reason it does not pay. It
// reads the key file and no other secret, and no reason repeats a secret.
func planPayouts(f payoutFlags) (*xmrPlan, *zecPlan, []string) {
	var notes []string
	x, why := planXmr(f)
	if why != "" {
		notes = append(notes, why)
	}
	z, why := planZec(f)
	if why != "" {
		notes = append(notes, why)
	}
	return x, z, notes
}

// treasuryOf is the fee address: networks.json's, or on a network other than mainnet the flag's.
func treasuryOf(f payoutFlags, field, flagVal, name string) (string, string) {
	rec := ""
	if f.nw != nil {
		rec = field
	}
	if flagVal != "" {
		if f.network == "mainnet" {
			return "", "-" + name + " is not accepted on mainnet: the fee address is deploy/networks.json's " + strings.ReplaceAll(name, "-t", "T") + ", the one the swap tools pay"
		}
		return flagVal, ""
	}
	if rec == "" {
		if f.nwErr != nil {
			return "", fmt.Sprintf("deploy/networks.json could not be read (%v)", f.nwErr)
		}
		return "", fmt.Sprintf("network %s has no %s in deploy/networks.json", f.network, strings.ReplaceAll(name, "-t", "T"))
	}
	return rec, ""
}

func planXmr(f payoutFlags) (*xmrPlan, string) {
	mainnet := f.network == "mainnet"
	refuse := func(format string, a ...interface{}) (*xmrPlan, string) {
		return nil, "XMR payouts refused: " + fmt.Sprintf(format, a...)
	}
	if f.xmrWallet == "" {
		if mainnet {
			return refuse("no -xmr-wallet: the passes' XMR share is not paid until the bot runs with the treasury's wallet-rpc")
		}
		return nil, "XMR payouts off: no -xmr-wallet"
	}
	field := ""
	if f.nw != nil {
		field = f.nw.XmrTreasury
	}
	treasury, why := treasuryOf(f, field, f.xmrTreasury, "xmr-treasury")
	if why != "" {
		return refuse("%s", why)
	}
	var want xmrswap.Network
	switch f.xmrNet {
	case "mainnet", "regtest":
		want = xmrswap.Mainnet // regtest uses mainnet's prefixes
	case "stagenet":
		want = xmrswap.Stagenet
	case "testnet":
		want = xmrswap.Testnet
	default:
		return refuse("-xmr-net must be mainnet, stagenet, testnet or regtest, not %q", f.xmrNet)
	}
	if mainnet != (f.xmrNet == "mainnet") {
		return refuse("-xmr-net %s with a %s config: real Monero is paid from a mainnet deployment only, and a mainnet deployment pays nothing else", f.xmrNet, f.network)
	}
	if err := checkXmr(treasury, want); err != nil {
		return refuse("xmrTreasury %s: %v", treasury, err)
	}
	if mainnet {
		if f.xmrLogin == "" {
			return refuse("%s is not set: the treasury's wallet-rpc can spend, so on mainnet it must run with --rpc-login", xmrLoginEnv)
		}
		if strings.Contains(f.xmrWallet, "@") {
			return refuse("-xmr-wallet carries a login in its address; give host:port and set %s", xmrLoginEnv)
		}
		if !rpc.IsLoopback(f.xmrWallet) {
			return refuse("-xmr-wallet %s is not on this machine: its login would cross the network", f.xmrWallet)
		}
	}
	if f.xmrMin == 0 {
		return refuse("-xmr-min-part is 0")
	}
	return &xmrPlan{wallet: f.xmrWallet, login: f.xmrLogin, treasury: treasury, want: want, min: f.xmrMin}, ""
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func planZec(f payoutFlags) (*zecPlan, string) {
	mainnet := f.network == "mainnet"
	refuse := func(format string, a ...interface{}) (*zecPlan, string) {
		return nil, "ZEC payouts refused: " + fmt.Sprintf(format, a...)
	}
	if len(f.zecNodes) == 0 {
		if mainnet {
			return refuse("no -zec-node: the passes' ZEC share is not paid until the bot runs with the treasury's Zcash node")
		}
		return nil, "ZEC payouts off: no -zec-node"
	}
	field := ""
	if f.nw != nil {
		field = f.nw.ZecTreasury
	}
	treasury, why := treasuryOf(f, field, f.zecTreasury, "zec-treasury")
	if why != "" {
		return refuse("%s", why)
	}
	var want zecswap.Network
	switch f.zecNet {
	case "mainnet":
		want = zecswap.Mainnet
	case "testnet", "regtest":
		want = zecswap.Testnet // regtest uses testnet's prefixes
	default:
		return refuse("-zec-net must be mainnet, testnet or regtest, not %q", f.zecNet)
	}
	if mainnet != (f.zecNet == "mainnet") {
		return refuse("-zec-net %s with a %s config: real Zcash is paid from a mainnet deployment only, and a mainnet deployment pays nothing else", f.zecNet, f.network)
	}
	n, kind, _, err := zecswap.ParseAddress(treasury)
	if err != nil || n != want || kind != zecswap.P2PKH {
		return refuse("zecTreasury %s is not a transparent P2PKH address on Zcash %s (t1… on mainnet, tm… elsewhere), which a key this bot holds can spend", treasury, f.zecNet)
	}
	if f.zecKey == "" {
		return refuse("no -zec-key: zecTreasury's key signs the payouts")
	}
	abs, err := filepath.Abs(f.zecKey)
	if err != nil {
		return refuse("-zec-key: %v", err)
	}
	if f.root != "" && within(abs, f.root) {
		return refuse("the -zec-key file is inside the repository; keep it outside, where no commit can take it")
	}
	key, err := zecswap.LoadKey(abs, want)
	if err != nil {
		return refuse("-zec-key could not be read as a key (%v)", err)
	}
	if key.Address(want) != treasury {
		key.Zero()
		return refuse("the -zec-key key does not control zecTreasury %s", treasury)
	}
	user, pass := "", ""
	switch {
	case f.zecCookie != "":
		if user, pass, err = zecswap.ReadCookie(f.zecCookie); err != nil {
			key.Zero()
			return refuse("-zec-cookie could not be read")
		}
	case f.zecLogin != "":
		user, pass, _ = strings.Cut(f.zecLogin, ":")
	case mainnet:
		key.Zero()
		return refuse("no node login: give -zec-cookie or set %s", zecLoginEnv)
	}
	if f.zecMin < zecswap.OutputFloor {
		key.Zero()
		return refuse("-zec-min-part %d is below the smallest output zecswap makes (%d)", f.zecMin, zecswap.OutputFloor)
	}
	return &zecPlan{nodes: f.zecNodes, user: user, pass: pass, chain: f.zecNet, net: want, treasury: treasury, key: key, min: f.zecMin}, ""
}

// payoutFlagsFromCommandLine gathers the flags and the network's networks.json block.
func payoutFlagsFromCommandLine(network string) payoutFlags {
	f := payoutFlags{network: network, xmrWallet: *xmrWallet, xmrNet: *xmrNetFlag, xmrTreasury: *xmrTreasFlag, xmrLogin: os.Getenv(xmrLoginEnv),
		zecNet: *zecNetFlag, zecTreasury: *zecTreasFlag, zecKey: *zecKeyFlag, zecLogin: os.Getenv(zecLoginEnv), zecCookie: *zecCookie,
		xmrMin: *xmrMinPart, zecMin: *zecMinPart}
	for _, n := range strings.Split(*zecNodesFlag, ",") {
		if n = strings.TrimSpace(n); n != "" {
			f.zecNodes = append(f.zecNodes, n)
		}
	}
	if abs, err := filepath.Abs(*netsPath); err == nil {
		f.root = filepath.Dir(filepath.Dir(abs))
	}
	file, err := netcfg.Load(*netsPath)
	switch {
	case err != nil:
		f.nwErr = err
	case file.Networks[network] == nil:
		f.nwErr = fmt.Errorf("it has no %s block", network)
	default:
		f.nw = file.Networks[network]
	}
	return f
}

// startPayouts connects each planned asset and opens its journal. An asset that cannot start is reported and left
// off; the DERO side is not affected.
func startPayouts(ctx context.Context, x *xmrPlan, z *zecPlan, pr passReader, share uint64, dry bool, base string, out io.Writer) []*assetPayer {
	var ps []*assetPayer
	start := func(n payNet, key, path string) {
		p, err := newPayer(n, pr, key, share, dry, path, out)
		if err != nil {
			fmt.Fprintf(out, "%s payouts refused: %v\n", n.Name(), err)
			return
		}
		fmt.Fprintf(out, "%s payouts on: journal %s, period %d DERO blocks, share %d bps, minimum part %d\n", n.Name(), path, p.every, share, n.MinPart())
		ps = append(ps, p)
	}
	if x != nil {
		n := &xmrNet{w: newMoneroRPC(x.wallet, x.login), treasury: x.treasury, want: x.want, minPart: x.min}
		if err := n.locate(ctx); err != nil {
			fmt.Fprintf(out, "XMR payouts refused: %v\n", err)
		} else {
			if n.minor == 0 {
				fmt.Fprintln(out, "XMR payouts: xmrTreasury is its account's primary address; a dedicated subaddress keeps anything but fees out of the count")
			}
			start(n, "xm_", journalPath(*xmrJournal, base, ".xmr.jsonl"))
		}
	}
	if z != nil {
		n := &zecNet{chain: z.chain, net: z.net, treasury: z.treasury, key: z.key, minPart: z.min}
		for _, u := range z.nodes {
			n.nodes = append(n.nodes, zecswap.ZcashSource{Name: u, Node: zecswap.NewZcashRPC(u, z.user, z.pass)})
		}
		if _, err := n.info(ctx); err != nil {
			fmt.Fprintf(out, "ZEC payouts refused: %v\n", err)
		} else {
			start(n, "zc_", journalPath(*zecJournal, base, ".zec.jsonl"))
		}
	}
	return ps
}

func journalPath(flagVal, base, ext string) string {
	if flagVal != "" {
		return flagVal
	}
	return strings.TrimSuffix(base, ".json") + ext
}

// newPayer takes the journal's lock (one bot per journal) and replays it.
func newPayer(n payNet, pr passReader, key string, share uint64, dry bool, path string, out io.Writer) (*assetPayer, error) {
	if *periodBlocks == 0 {
		return nil, fmt.Errorf("-period-blocks is 0")
	}
	lf, err := os.OpenFile(path+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%s.lock exists: another treasurybot pays from this journal (or crashed without removing it)", path)
	}
	fmt.Fprintf(lf, "%d\n", os.Getpid())
	lf.Close()
	l, err := openLedger(path)
	if err != nil {
		os.Remove(path + ".lock")
		return nil, err
	}
	return &assetPayer{net: n, pr: pr, l: l, key: key, share: share, every: *periodBlocks, dry: dry, out: out}, nil
}

func (p *assetPayer) release() {
	p.l.close()
	os.Remove(p.l.path + ".lock")
}
