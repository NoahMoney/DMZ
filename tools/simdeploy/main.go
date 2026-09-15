// simdeploy installs factory.bas, social.bas, founders.bas, chat.bas, rate.bas and the swap contracts on a running simulator
// (wallet 0 = the deployer, wallet 2 = treasury), copies the templates into app/templates and writes app/config.js with
// the SCIDs and template hashes.
//
//	go run ./simdeploy -contracts ../contracts -app ../app -htlc-w1min 400 -htlc-w2min 400 -htlc-g2 <G2>
//	go run ./simdeploy -factory <scid> -social <scid> -founders <scid>   # reuse what is deployed, just rewrite config
//	-migrate-from <old factory>             # install a new factory and re-register the old one's listings on it
//	-legacy launch:<sha256>,pool:<sha256>   # older template hashes the app should still accept (simulator history)
//	-revoke launch:<sha256>                 # a hash the app must STOP accepting: the code it names has a defect
//
// The wallets' RPC login, when they ask for one, comes from VEIL_RPC_LOGIN (user:password), the variable netwallet
// starts them with; simrpc sends it to the wallets only. On -network mainnet simdeploy refuses before its first call
// when the variable is unset, or when -w0 or -w2 carries a login in its address (X-27).
//
// factory.bas v2 has no owner (MT-J15(b)): its treasury and registration fee are the -w2 address and -regfee at
// install, for the life of the factory. htlc.bas v3 takes three install arguments with no default (-htlc-w1min,
// -htlc-w2min, -htlc-g2), checked against the contract's bounds before anything is installed.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ref "veil/reference"
	"veil/tools/netcfg"
	rpc "veil/tools/simrpc"
)

var (
	daemon    = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	w0        = flag.String("w0", "127.0.0.1:30000", "deployer wallet rpc, host:port; its login comes from VEIL_RPC_LOGIN (required on mainnet)")
	w2        = flag.String("w2", "127.0.0.1:30002", "treasury wallet rpc, host:port; its login comes from VEIL_RPC_LOGIN (required on mainnet)")
	contracts = flag.String("contracts", "../contracts", "contracts directory")
	app       = flag.String("app", "../app", "app directory")
	regfee    = flag.Uint64("regfee", 10000, "registration fee in atomic units, fixed for the life of the factory: factory.bas v2 has no setter")
	network   = flag.String("network", "simulator", "network label for config.js and the deploy/networks.json block to update")
	networks  = flag.String("networks", "../deploy/networks.json", "per-network deployment record")
	factory   = flag.String("factory", "", "reuse an existing factory scid instead of installing")
	social    = flag.String("social", "", "reuse an existing social scid instead of installing")
	founders  = flag.String("founders", "", "reuse an existing founders scid instead of installing")
	chat      = flag.String("chat", "", "reuse an existing chat scid instead of installing")
	htlcF     = flag.String("htlc", "", "reuse an existing htlc.bas scid (the Monero swap's DERO leg) instead of installing")
	swapF     = flag.String("swap", "", "reuse an existing swap.bas scid (the Zcash swap's DERO leg) instead of installing")
	boardF    = flag.String("board", "", "reuse an existing board.bas scid (the Zcash market's offers) instead of installing")
	renounce  = flag.Bool("renounce", false, "kept only to explain, and exit: factory.bas v2 has no owner, so there is nothing to renounce (MT-J15(b))")
	swapWmin  = flag.Uint64("swap-wmin", 1600, "swap.bas install argument: the minimum escrow width in blocks (Z-D; 1,600 for the ZEC market, docs/ZEC_SWAP.md §5). Fixed for the life of the contract; the taker's companion refuses a swap.bas whose wmin is below its policy")
	htlcW1min = flag.Uint64("htlc-w1min", 0, "htlc.bas install argument, required to install it: the least t1 - lock height, in blocks (MT-A03(a); the Monero profile's minimum t1 is 400). Fixed for the life of the contract")
	htlcW2min = flag.Uint64("htlc-w2min", 0, "htlc.bas install argument, required to install it: the least t2 - t1, in blocks (the Monero profile's T2 - T1 is 400). Fixed for the life of the contract")
	htlcG2    = flag.Uint64("htlc-g2", 0, "htlc.bas install argument, required to install it: the Punish window in blocks from t2; from t2 + g2 only the maker may Reclaim (MT-J13(b)). The operator answered 4,800 blocks (24 h) on 2026-09-12, and the xmrswap taker refuses an install whose g2 is below her floor, 4,800 (DefaultG2Min), so every taker refuses a smaller one. No default all the same: each install names it")
	chatFee   = flag.Uint64("chat-fee", 10000, "fee to post one message, in atomic units — the only spam control there is")
	chatMax   = flag.Uint64("chat-maxlen", 280, "longest message the contract will store")
	rateF     = flag.String("rate", "", "reuse an existing rate.bas scid (community sentiment) instead of installing")
	rateFee   = flag.Uint64("rate-fee", 10000, "fee per vote in atomic units, paid to the treasury and fixed for the life of rate.bas (no setter). 10,000 = 0.1 DERO, the price of a chat message: the only spam control, paid again by each address a voter adds")
	legacy    = flag.String("legacy", "", "comma-separated kind:sha256 of older template versions the app keeps accepting")
	revoke    = flag.String("revoke", "", "comma-separated kind:sha256 of template versions the app must STOP accepting: code with a defect. A revoked hash is dropped from the record instead of being carried forward, so markets deployed from it stop verifying")
	timeout   = flag.Duration("timeout", 180*time.Second, "per-tx timeout")
	yes       = flag.Bool("yes", false, "required on mainnet: installs and the founders reserve mint spend real DERO")
	migrate   = flag.String("migrate-from", "", "an older factory scid whose pools, launches, NFTs and NFAs are re-registered on the newly installed factory (regfee per listing from the owner wallet; a factory has no in-place upgrade)")

	passName    = flag.String("pass-name", "DMZ Founders Pass", "founders pass name")
	passDescr   = flag.String("pass-descr", passDescrDefault, "founders pass description — IMMUTABLE once installed; pass it explicitly on any real deployment")
	passImage   = flag.String("pass-image", "", "founders pass image URL (https)")
	passPrice   = flag.Uint64("pass-price", 100000, "mint price in atomic units (100000 = 1 DERO, the operator's number, MT-J16)")
	passMax     = flag.Uint64("pass-max", 20, "editions (20, MT-J16)")
	passStart   = flag.Uint64("pass-start", 0, "mint opens at this block height")
	passReserve = flag.Uint64("pass-reserve", 4, "editions minted to the installer at install: at most -pass-max / 5 (4 of 20, MT-J16)")
	passShare   = flag.Uint64("pass-share", 10000, "the share of the fees the treasury claims that goes to the passes, in bps (10000 = all of it: the operator's ruling of 2026-09-15, at least 5% per registered pass across 20)")
	passMfee    = flag.Uint64("pass-mfee", 250, "marketplace fee in bps, deposited for the registered passes")
)

// passDescrDefault is the Founders Pass description the operator approved for founders.bas v2 (2026-09-13, MT-J16,
// X-29), 236 of the contract's 240 bytes. founders.bas stores its headers once and defines no setter, so whatever
// goes in here is on chain for the life of the contract, and app/nfts.js renders it under the mint button. It says
// what v2 and the treasury's bot do: each fee is paid in the asset it was taken in, DERO and DERO tokens through the
// contract (Deposit, then each position's Claim), XMR and ZEC by the bot to the addresses holders store (SetXmr,
// SetZec), and no contract forces the bot to make any of those payments. app/notice.js, the page the app declares
// authoritative, says the same in more words. The v1 wording, "Fees in any asset other than DERO do not reach
// holders", is false of v2 and must not be installed with it.
const passDescrDefault = "Register a pass for a fixed share of DMZ's trading and swap fees, paid in the asset each fee was taken in: " +
	"DERO and DERO tokens through this contract, XMR and ZEC by the treasury's bot. The bot makes each payment; no " +
	"contract forces it."

// checkSwapWmin refuses, before anything is installed, a minimum width swap.bas Initialize would refuse
// (swap.bas line 6, `wmin == 0 || wmin > 1000000`; TestSwapWminBoundsAreTheContracts holds the two in step).
func checkSwapWmin() error {
	if *swapWmin == 0 || *swapWmin > 1000000 {
		return fmt.Errorf("-swap-wmin is %d; swap.bas takes 1 to 1000000", *swapWmin)
	}
	return nil
}

// rateArgs is rate.bas InitializePrivate's arguments: the treasury every vote fee goes to and the fee, both fixed for the
// contract's life (TestRateArgsAreTheContracts holds the two in step).
func rateArgs(treasury string) []rpc.Arg {
	return []rpc.Arg{rpc.S("treasury", treasury), rpc.U("fee", *rateFee)}
}

// checkHtlcWindows refuses, before anything is installed, htlc.bas install arguments its Initialize would refuse
// (htlc.bas lines 6-7: w1min, w2min and g2 each 1 to 1,000,000, and w1min + w2min at most 999,999, past which no lock
// could meet both; TestHtlcWindowBoundsAreTheContracts holds the two in step). The flags have no default: each install
// names its windows (G2 was answered 4,800 on 2026-09-12, and the taker's floor is that), and a width typed in here would
// be a policy nobody chose for that install. An install that names none of them therefore stops here, rather than after
// the factory and social contracts have been paid for.
func checkHtlcWindows() error {
	w1, w2, g2 := *htlcW1min, *htlcW2min, *htlcG2
	switch {
	case w1 == 0 || w2 == 0 || g2 == 0:
		return fmt.Errorf("installing htlc.bas needs -htlc-w1min, -htlc-w2min and -htlc-g2, each at least 1 (got %d, %d, %d); pass -htlc <scid> to reuse an installed one", w1, w2, g2)
	case w1 > 1000000 || w2 > 1000000 || g2 > 1000000:
		return fmt.Errorf("-htlc-w1min %d, -htlc-w2min %d, -htlc-g2 %d: htlc.bas takes each up to 1000000", w1, w2, g2)
	case w1+w2 > 999999:
		return fmt.Errorf("-htlc-w1min %d + -htlc-w2min %d = %d: htlc.bas refuses a sum above 999999, where no lock could meet both widths", w1, w2, w1+w2)
	}
	return nil
}

// checkWalletLogin refuses a mainnet run, before its first call, that could not authenticate to the wallets, that
// would carry their password on its command line, or whose wallets are not on this machine. netwallet will not start a
// mainnet wallet without a login, so a run without VEIL_RPC_LOGIN would only stop at its first wallet call (the X-09
// rehearsal, X-27); and the login rides in every wallet request as HTTP basic auth over plain HTTP, readable on any
// network between here and a wallet elsewhere.
func checkWalletLogin() error {
	if *network != "mainnet" {
		return nil
	}
	if err := rpc.RequireWalletLogin(*w0, *w2); err != nil {
		return err
	}
	return rpc.RequireLoopbackWallets(*w0, *w2)
}

// checkRenounce answers the -renounce of older runbooks. factory.bas v2 (MT-J15(b)) has no owner: its treasury and
// registration fee are fixed at Initialize and no function can change them, so there is no ceremony to run, and this
// tool sends Renounce to nothing. An older factory that still has an owner is left, not renounced: install a v2
// factory and move its listings to it with -migrate-from, then publish an app that reads the new one.
func checkRenounce() error {
	if !*renounce {
		return nil
	}
	return fmt.Errorf("-renounce: factory.bas v2 has no owner, so there is nothing to renounce: its treasury and registration fee are fixed when it is installed and no function can change them. " +
		"To leave an older factory that still has an owner, install a new one and re-register its listings: go run ./simdeploy -migrate-from <old factory> (docs/MAINNET_DEPLOYMENT.md)")
}

// checkPassHeaders refuses, before anything is installed, a Founders Pass that founders.bas InitializePrivate would
// refuse (founders.bas v2, DVM lines 22-24; TestPassLimitsAreTheContracts holds the two in step). The contract's
// refusal surfaces only at the gas estimate, by which time the factory and social contracts are already installed and
// paid for. v2 also refuses DERO attached to its install (line 21), and simdeploy attaches none (the install's
// transfers are nil); a treasury that is not a valid address is refused at line 26, and -w2 is a wallet's own address.
func checkPassHeaders() error {
	switch {
	case len(*passName) == 0 || len(*passName) > 64:
		return fmt.Errorf("-pass-name is %d bytes; founders.bas takes 1 to 64", len(*passName))
	case len(*passDescr) > 240:
		return fmt.Errorf("-pass-descr is %d bytes; founders.bas takes at most 240", len(*passDescr))
	case len(*passImage) > 200:
		return fmt.Errorf("-pass-image is %d bytes; founders.bas takes at most 200", len(*passImage))
	case *passMax == 0 || *passMax > 100000:
		return fmt.Errorf("-pass-max is %d; founders.bas takes 1 to 100000", *passMax)
	case *passPrice > 10000000000000:
		return fmt.Errorf("-pass-price is %d; founders.bas takes at most 10000000000000", *passPrice)
	case *passReserve > *passMax/5:
		return fmt.Errorf("-pass-reserve is %d; founders.bas takes at most -pass-max / 5 = %d", *passReserve, *passMax/5)
	case *passShare > 10000:
		return fmt.Errorf("-pass-share is %d bps; founders.bas takes at most 10000", *passShare)
	case *passMfee > 1000:
		return fmt.Errorf("-pass-mfee is %d bps; founders.bas takes at most 1000", *passMfee)
	}
	return nil
}

// read returns the source a contract is installed with, which is also the source written to app/templates and
// hashed into config.js: ref.Canon drops whole-line comments and blank lines, so the template hash a user
// checks covers the code that runs and nothing else.
func read(p string) string { b, err := os.ReadFile(p); rpc.Must(err); return ref.Canon(string(b)) }
func sha(s string) string  { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func main() {
	flag.Parse()
	if err := checkRenounce(); err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	if *network == "mainnet" && !*yes {
		fmt.Println("mainnet: this installs contracts and mints the founders reserve with real DERO; pass -yes to run it")
		os.Exit(2)
	}
	if err := checkWalletLogin(); err != nil {
		fmt.Println("mainnet:", err)
		os.Exit(2)
	}
	// Both hash flags are checked against the record before a single contract is installed. A mistyped kind or
	// hash in -revoke leaves defective code listed as verified, and the operator would find that out only after
	// paying for the install.
	old, rev := parseHashes(*legacy), parseHashes(*revoke)
	if err := checkHashFlags(old, rev); err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	if *founders == "" {
		if err := checkPassHeaders(); err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
	}
	if *swapF == "" {
		if err := checkSwapWmin(); err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
	}
	if *htlcF == "" {
		if err := checkHtlcWindows(); err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
	}
	c := &rpc.Client{Daemon: *daemon, Timeout: *timeout}
	if testnet, name := c.Info(); (*network == "mainnet") == testnet {
		fmt.Printf("-network %s but the daemon at %s reports %q (testnet=%v)\n", *network, *daemon, name, testnet)
		os.Exit(2)
	}
	owner, treasury := rpc.Address(*w0), rpc.Address(*w2)
	fmt.Printf("owner %s\ntreasury %s\n", owner, treasury)
	if *factory == "" {
		fmt.Println("installing factory.bas")
		*factory, _, _ = c.Send(*w0, owner, read(filepath.Join(*contracts, "factory.bas")), "", nil, []rpc.Arg{rpc.S("treasury", treasury), rpc.U("regfee", *regfee)})
	}
	if *social == "" {
		fmt.Println("installing social.bas")
		*social, _, _ = c.Send(*w0, owner, read(filepath.Join(*contracts, "social.bas")), "", nil, nil)
	}
	if *founders == "" {
		fmt.Println("installing founders.bas")
		*founders, _, _ = c.Send(*w0, owner, read(filepath.Join(*contracts, "founders.bas")), "", nil, []rpc.Arg{
			rpc.S("name", *passName), rpc.S("descr", *passDescr), rpc.S("icon", *passImage), rpc.S("file", *passImage),
			rpc.U("price", *passPrice), rpc.U("maxsupply", *passMax), rpc.U("start", *passStart), rpc.U("reserve", *passReserve),
			rpc.U("share", *passShare), rpc.U("mfee", *passMfee), rpc.S("treasury", treasury)})
	}
	if *chat == "" {
		fmt.Println("installing chat.bas")
		*chat, _, _ = c.Send(*w0, owner, read(filepath.Join(*contracts, "chat.bas")), "", nil, []rpc.Arg{
			rpc.S("treasury", treasury), rpc.U("fee", *chatFee), rpc.U("maxlen", *chatMax)})
	}
	if *rateF == "" {
		fmt.Println("installing rate.bas")
		*rateF, _, _ = c.Send(*w0, owner, read(filepath.Join(*contracts, "rate.bas")), "", nil, rateArgs(treasury))
	}
	// The swap contracts hold no role: Initialize sets their counters, swap.bas's its fixed minimum width and htlc.bas's
	// its two minimum widths and its Punish window. The install's signer is recorded nowhere in their storage. One
	// instance per network serves every swap (GAP-002).
	htlcArgs := []rpc.Arg{rpc.U("w1min", *htlcW1min), rpc.U("w2min", *htlcW2min), rpc.U("g2", *htlcG2)}
	for _, s := range []struct {
		name string
		scid *string
		args []rpc.Arg
	}{{"htlc.bas", htlcF, htlcArgs}, {"swap.bas", swapF, []rpc.Arg{rpc.U("wmin", *swapWmin)}}, {"board.bas", boardF, nil}} {
		if *s.scid == "" {
			fmt.Println("installing", s.name)
			*s.scid, _, _ = c.Send(*w0, owner, read(filepath.Join(*contracts, s.name)), "", nil, s.args)
		}
	}
	if *migrate != "" && *migrate != *factory {
		migrateRegistry(c, owner, *migrate, *factory)
	}
	fmt.Printf("factory  %s\nsocial   %s\nfounders %s\nchat     %s\nhtlc     %s\nswap     %s\nboard    %s\nrate     %s\n",
		*factory, *social, *founders, *chat, *htlcF, *swapF, *boardF, *rateF)

	rpc.Must(os.MkdirAll(filepath.Join(*app, "templates"), 0o755))
	hashes := map[string]string{}
	// nfa is the ART-NFA-MS1 asset (contracts/nfa.bas), a fifth template beside nft, which stays the holder badge
	// under its own key: a market deployed from either keeps verifying against the hash it was made from.
	for _, k := range []string{"pool", "launch", "nft", "nfa", "founders"} {
		code := read(filepath.Join(*contracts, k+".bas"))
		rpc.Must(os.WriteFile(filepath.Join(*app, "templates", k+".bas"), []byte(code), 0o644))
		hashes[k] = sha(code)
	}
	// app/sentiment.js reads and votes only once the node's rate.bas hashes to this file. It is not a market template, so
	// it has no entry under config.js templates: the id it checks is config.js `rate`.
	rpc.Must(os.WriteFile(filepath.Join(*app, "templates", "rate.bas"), []byte(read(filepath.Join(*contracts, "rate.bas"))), 0o644))
	for k, hs := range rev {
		for h := range hs {
			if hashes[k] == h {
				fmt.Printf("template %s: -revoke names %s…, which is the hash of the code being installed now\n", k, short(h))
				os.Exit(2)
			}
		}
	}
	// One spelling of config.js. This tool used to render its own copy of the template, which silently dropped
	// every field it did not know about — the vault and its asset record among them — each time it ran. Record
	// the deployment first, then render the app config from that record, the same way genconfig does.
	n := record(hashes, old, rev, treasury)
	rpc.Must(os.WriteFile(filepath.Join(*app, "config.js"), []byte(netcfg.ConfigJS(*network, n, "tools/simdeploy")), 0o644))
	fmt.Println("wrote", filepath.Join(*app, "config.js"))
}

// parseHashes reads a `kind:sha256,kind:sha256` flag into kind -> set of hashes. Space around an entry is
// ignored; an entry without a colon on both sides is not a hash and is skipped.
func parseHashes(spec string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, kv := range strings.Split(spec, ",") {
		p := strings.SplitN(strings.TrimSpace(kv), ":", 2)
		if len(p) != 2 || p[0] == "" || p[1] == "" {
			continue
		}
		if out[p[0]] == nil {
			out[p[0]] = map[string]bool{}
		}
		out[p[0]][p[1]] = true
	}
	return out
}

// templateKinds are the templates this tool writes, hashes and records.
var templateKinds = []string{"pool", "launch", "nft", "nfa", "founders"}

// checkHashFlags validates -legacy and -revoke against deploy/networks.json before anything is installed. Every
// failure here is silent otherwise: a kind nobody reads, a hash nobody matches, a revocation that revokes
// nothing. -revoke exists to de-list code with a defect, so a -revoke that quietly did nothing is the worst
// outcome the flag has.
func checkHashFlags(old, rev map[string]map[string]bool) error {
	bad := fmt.Errorf
	kinds := map[string]bool{}
	for _, k := range templateKinds {
		kinds[k] = true
	}
	if len(old["founders"]) > 0 {
		return bad("-legacy founders:...: the founders template keeps no legacy hash; the app reads one pass, with the current contract's keys (docs/FOUNDERS_V2_NOTES.md §1)")
	}
	for _, m := range []map[string]map[string]bool{old, rev} {
		for k, hs := range m {
			if !kinds[k] {
				return bad("unknown template kind %q (%s)", k, strings.Join(templateKinds, ", "))
			}
			for h := range hs {
				if len(h) != 64 || strings.Trim(h, "0123456789abcdef") != "" {
					return bad("template %s: %q is not a 64-character lowercase sha256", k, h)
				}
				if rev[k][h] && old[k][h] {
					return bad("template %s: %s… is named by both -legacy and -revoke", k, short(h))
				}
			}
		}
	}
	if len(rev) == 0 {
		return nil
	}
	f, err := netcfg.Load(*networks)
	if err != nil {
		return bad("-revoke needs %s to say what is listed today: %v", *networks, err)
	}
	n := f.Networks[*network]
	for k, hs := range rev {
		for h := range hs {
			listed := false
			if n != nil {
				t := n.Templates[k]
				listed = t.Sha256 == h
				for _, l := range t.Legacy {
					listed = listed || l == h
				}
			}
			if !listed {
				return bad("template %s: -revoke names %s…, which %s does not list for %s — nothing would be revoked", k, short(h), *networks, *network)
			}
		}
	}
	return nil
}

// short is the first 12 characters of a hash, or all of it when it is shorter: the prefix this tool prints,
// without the panic a fixed slice takes on a mistyped flag.
func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// record updates the network block in deploy/networks.json so genconfig can rebuild config.js and dist bundles,
// and returns the block it wrote. When the record cannot be read it still returns a block built from the flags,
// so config.js is written from exactly one code path whether or not the file was there.
func record(hashes map[string]string, old, rev map[string]map[string]bool, treasury string) *netcfg.Network {
	f, err := netcfg.Load(*networks)
	if err != nil {
		fmt.Println("networks.json not updated:", err)
		f = &netcfg.File{Networks: map[string]*netcfg.Network{}}
	}
	if f.Networks == nil {
		f.Networks = map[string]*netcfg.Network{}
	}
	n := f.Networks[*network]
	if n == nil {
		n = &netcfg.Network{Templates: map[string]netcfg.Template{}}
		f.Networks[*network] = n
	}
	n.Daemon, n.Factory, n.Social, n.Founders, n.Chat = *daemon, *factory, *social, *founders, *chat
	n.Htlc, n.Swap, n.Board = *htlcF, *swapF, *boardF
	n.Rate = *rateF
	n.FeeBps, n.PfeeBps, n.LaunchPfeeBps = 50, 5000, 5000
	// GAP-005: the treasury set config.js ships, current first, in the spelling a contract writes. A market keeps
	// the treasury it was installed with, so an earlier one stays in the set the way a legacy template hash does.
	if t := rpc.DVMAddress(treasury); t != "" {
		ts := []string{t}
		for _, p := range n.Treasuries {
			if p != "" && p != t {
				ts = append(ts, p)
			}
		}
		n.Treasuries = ts
	}
	if n.Templates == nil {
		n.Templates = map[string]netcfg.Template{}
	}
	for k, h := range hashes {
		prev := n.Templates[k]
		// The founders template keeps no legacy hash (docs/FOUNDERS_V2_NOTES.md §1). The app reads exactly one pass,
		// the one config.js names, with the key layout of the current contracts/founders.bas: an earlier hash left in
		// the record would let a v1 pass verify as "founders" and be read with v2's keys. So a changed founders
		// template replaces the old hash instead of demoting it, and -legacy founders:... is refused (checkHashFlags).
		if k == "founders" {
			if prev.Sha256 != "" && prev.Sha256 != h {
				fmt.Printf("template founders changed: %s… is dropped, not kept as legacy: the app reads one pass, with this version's keys\n", short(prev.Sha256))
			}
			n.Templates[k] = netcfg.Template{Sha256: h}
			continue
		}
		// Carry the previous hashes forward, and demote the outgoing current hash to legacy whenever the template
		// has actually changed. Without this, running this tool for an unrelated reason silently invalidated every
		// market already deployed from the old template: the app verifies a listing by hashing its on-chain code
		// against this record, so a dropped hash turns a good market into an unlisted one.
		//
		// The carry-forward is not unconditional any more. A template changes for two different reasons and they
		// need opposite answers: a change that adds something leaves the old code sound, and its markets should
		// keep verifying; a change that fixes a defect leaves the old code exploitable, and a legacy entry is then
		// the app certifying it. -revoke names those hashes. The tool cannot tell the two apart — which release
		// fixed what is the operator's knowledge — so it does not guess: it demotes by default and drops only what
		// is named, and it says out loud which of the two it did.
		leg := append([]string{}, prev.Legacy...)
		if prev.Sha256 != "" && prev.Sha256 != h {
			leg = append(leg, prev.Sha256)
		}
		add := make([]string, 0, len(old[k]))
		for q := range old[k] {
			add = append(add, q)
		}
		sort.Strings(add) // -legacy is a set; fix an order so two runs of the same command write the same file
		leg = append(leg, add...)
		seen, out := map[string]bool{h: true}, []string{}
		for _, l := range leg {
			if l == "" || seen[l] {
				continue
			}
			seen[l] = true
			if rev[k][l] {
				fmt.Printf("template %s: %s… revoked, dropped from the record — markets deployed from it stop verifying\n", k, short(l))
				continue
			}
			out = append(out, l)
		}
		if prev.Sha256 != "" && prev.Sha256 != h && !rev[k][prev.Sha256] {
			fmt.Printf("template %s changed: %s… kept as legacy so markets already deployed from it still verify (use -revoke %s:%s if that code has a defect)\n", k, short(prev.Sha256), k, prev.Sha256)
		}
		n.Templates[k] = netcfg.Template{Sha256: h, Legacy: out}
	}
	if n.Tela.Durl == "" {
		n.Tela.Durl = "dmz.tela"
	}
	if err == nil {
		rpc.Must(f.Save(*networks))
		fmt.Println("recorded", *network, "in", *networks)
	}
	return n
}

// migrateRegistry re-registers every pool, launch, holder-NFT and NFA an older factory lists on the newly
// installed one. A factory cannot be upgraded in place — it has no UPDATE_SC_CODE entrypoint by design, the
// registry's code being the listing's guarantee — so a factory.bas change, or a treasury or fee other than the one
// fixed at install (factory.bas v2 has no setter), is a new install with an empty registry, and the app reads exactly
// one factory. Duplicate registrations on the old factory (this simulator has three) collapse to one; each
// registration burns the new factory's own regfee, read from the chain, from the deployer wallet, which the factory
// forwards to the treasury, so on a simulator the money moves between two of its own wallets.
func migrateRegistry(c *rpc.Client, owner, oldF, newF string) {
	st := c.GetSC(oldF, false)
	kinds := []struct{ count, prefix, entry string }{{"pc", "pool_", "RegisterPool"}, {"lc", "launch_", "RegisterLaunch"}, {"nc", "nft_", "RegisterNFT"}, {"nc", "nfa_", "RegisterNFA"}}
	seen := map[string]bool{}
	// Whatever the new factory already lists is not registered again, so a run interrupted half-way (the owner
	// wallet ran dry on the simulator the first time) is resumed by running it again with -factory <new>.
	ns0 := c.GetSC(newF, false)
	var pay []rpc.Transfer
	if fee := ns0.U0("regfee"); fee > 0 {
		pay = []rpc.Transfer{{Destination: c.RandomAddress(), Burn: fee}}
	}
	fmt.Printf("the new factory's registration fee is %d\n", ns0.U0("regfee"))
	for _, k := range kinds {
		n := ns0.U0(k.count)
		for i := uint64(0); i < n; i++ {
			if s := scidVar(ns0, fmt.Sprintf("%s%d", k.prefix, i)); s != "" {
				seen[s] = true
			}
		}
	}
	fmt.Printf("the new factory already lists %d contracts\n", len(seen))
	done := 0
	for _, k := range kinds {
		n := st.U0(k.count)
		for i := uint64(0); i < n; i++ {
			scid := scidVar(st, fmt.Sprintf("%s%d", k.prefix, i))
			if scid == "" || seen[scid] {
				continue
			}
			seen[scid] = true
			fmt.Printf("migrating %s%d %s… via %s\n", k.prefix, i, scid[:12], k.entry)
			c.Send(*w0, owner, "", newF, pay, []rpc.Arg{rpc.S("entrypoint", k.entry), rpc.H("scid", scid)})
			done++
		}
	}
	ns := c.GetSC(newF, false)
	fmt.Printf("migrated %d listings from %s…: the new factory lists pc %d lc %d nc %d\n", done, oldF[:12], ns.U0("pc"), ns.U0("lc"), ns.U0("nc"))
}

// scidVar reads a string variable holding a 64-hex SCID, whether the daemon returns it raw or hex-encoded.
func scidVar(st rpc.SCState, k string) string {
	v, ok := st.StringKeys[k]
	if !ok {
		return ""
	}
	s := fmt.Sprint(v)
	if is64hex(s) {
		return s
	}
	if b, err := hex.DecodeString(s); err == nil && is64hex(string(b)) {
		return string(b)
	}
	return ""
}

func is64hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, ch := range s {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}
