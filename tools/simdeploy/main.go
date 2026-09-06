// simdeploy installs factory.bas, social.bas and founders.bas on a running simulator (wallet 0 = owner, wallet 2 =
// treasury), copies the templates into app/templates and writes app/config.js with the SCIDs and template hashes.
//
//	go run ./simdeploy -contracts ../contracts -app ../app
//	go run ./simdeploy -factory <scid> -social <scid> -founders <scid>   # reuse what is deployed, just rewrite config
//	-legacy launch:<sha256>,pool:<sha256>   # older template hashes the app should still accept (simulator history)
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"veil/tools/netcfg"
	rpc "veil/tools/simrpc"
)

var (
	daemon    = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	w0        = flag.String("w0", "127.0.0.1:30000", "owner wallet rpc")
	w2        = flag.String("w2", "127.0.0.1:30002", "treasury wallet rpc")
	contracts = flag.String("contracts", "../contracts", "contracts directory")
	app       = flag.String("app", "../app", "app directory")
	regfee    = flag.Uint64("regfee", 10000, "registration fee in atomic units")
	network   = flag.String("network", "simulator", "network label for config.js and the deploy/networks.json block to update")
	networks  = flag.String("networks", "../deploy/networks.json", "per-network deployment record")
	factory   = flag.String("factory", "", "reuse an existing factory scid instead of installing")
	social    = flag.String("social", "", "reuse an existing social scid instead of installing")
	founders  = flag.String("founders", "", "reuse an existing founders scid instead of installing")
	legacy    = flag.String("legacy", "", "comma-separated kind:sha256 of older template versions the app keeps accepting")
	timeout   = flag.Duration("timeout", 180*time.Second, "per-tx timeout")
	yes       = flag.Bool("yes", false, "required on mainnet: installs and the founders reserve mint spend real DERO")

	passName    = flag.String("pass-name", "DMZ Founders Pass", "founders pass name")
	passDescr   = flag.String("pass-descr", "Registered holders receive the promised share of every fee the exchange earns, paid in DERO to their wallets. Tradeable on the built-in marketplace.", "founders pass description")
	passImage   = flag.String("pass-image", "", "founders pass image URL (https)")
	passPrice   = flag.Uint64("pass-price", 500000, "mint price in atomic units (5 DERO)")
	passMax     = flag.Uint64("pass-max", 500, "editions")
	passStart   = flag.Uint64("pass-start", 0, "mint opens at this block height")
	passReserve = flag.Uint64("pass-reserve", 50, "editions minted to the owner at install (≤ 20%)")
	passShare   = flag.Uint64("pass-share", 5000, "promised share of protocol fees in bps (5000 = 50%)")
	passMfee    = flag.Uint64("pass-mfee", 250, "marketplace fee in bps, paid to holders")
)

func read(p string) string { b, err := os.ReadFile(p); rpc.Must(err); return string(b) }
func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func main() {
	flag.Parse()
	if *network == "mainnet" && !*yes {
		fmt.Println("mainnet: this installs contracts and mints the founders reserve with real DERO; pass -yes to run it")
		os.Exit(2)
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
	fmt.Printf("factory  %s\nsocial   %s\nfounders %s\n", *factory, *social, *founders)

	rpc.Must(os.MkdirAll(filepath.Join(*app, "templates"), 0o755))
	hashes := map[string]string{}
	for _, k := range []string{"pool", "launch", "nft", "founders"} {
		code := read(filepath.Join(*contracts, k+".bas"))
		rpc.Must(os.WriteFile(filepath.Join(*app, "templates", k+".bas"), []byte(code), 0o644))
		hashes[k] = sha(code)
	}
	old := map[string][]string{}
	if *legacy != "" {
		for _, kv := range strings.Split(*legacy, ",") {
			p := strings.SplitN(strings.TrimSpace(kv), ":", 2)
			if len(p) == 2 {
				old[p[0]] = append(old[p[0]], fmt.Sprintf("%q", p[1]))
			}
		}
	}
	leg := func(k string) string { return "[" + strings.Join(old[k], ", ") + "]" }
	cfg := fmt.Sprintf(`// Veil deployment configuration — generated by tools/simdeploy on %s.
// Template hashes are SHA-256 of the exact contract source; a listed contract is shown only if its on-chain
// code hashes to one of these and its treasury equals the factory's. `+"`legacy`"+` lists earlier versions of a
// template that stay accepted (their contracts are immutable and were verified when they were current).
window.VEIL_CONFIG = {
  network: %q,
  daemon: %q,
  factory: %q,
  social: %q,
  founders: %q,
  feeBps: 50, pfeeBps: 5000, launchPfeeBps: 5000,
  templates: {
    pool:     { file: "templates/pool.bas",     sha256: %q, legacy: %s },
    launch:   { file: "templates/launch.bas",   sha256: %q, legacy: %s },
    nft:      { file: "templates/nft.bas",      sha256: %q, legacy: %s },
    founders: { file: "templates/founders.bas", sha256: %q, legacy: %s }
  },
  indexer: { enabled: false, url: "http://127.0.0.1:8082" }
};
`, time.Now().UTC().Format(time.RFC3339), *network, *daemon, *factory, *social, *founders,
		hashes["pool"], leg("pool"), hashes["launch"], leg("launch"), hashes["nft"], leg("nft"), hashes["founders"], leg("founders"))
	rpc.Must(os.WriteFile(filepath.Join(*app, "config.js"), []byte(cfg), 0o644))
	fmt.Println("wrote", filepath.Join(*app, "config.js"))
	record(hashes, old)
}

// record updates the network block in deploy/networks.json so genconfig can rebuild config.js and dist bundles.
func record(hashes map[string]string, old map[string][]string) {
	f, err := netcfg.Load(*networks)
	if err != nil {
		fmt.Println("networks.json not updated:", err)
		return
	}
	n := f.Networks[*network]
	if n == nil {
		n = &netcfg.Network{Templates: map[string]netcfg.Template{}}
		f.Networks[*network] = n
	}
	n.Daemon, n.Factory, n.Social, n.Founders = *daemon, *factory, *social, *founders
	n.FeeBps, n.PfeeBps, n.LaunchPfeeBps = 50, 5000, 5000
	if n.Templates == nil {
		n.Templates = map[string]netcfg.Template{}
	}
	for k, h := range hashes {
		var leg []string
		for _, q := range old[k] {
			leg = append(leg, strings.Trim(q, "\""))
		}
		if leg == nil {
			leg = []string{}
		}
		n.Templates[k] = netcfg.Template{Sha256: h, Legacy: leg}
	}
	if n.Tela.Durl == "" {
		n.Tela.Durl = "dmz.tela"
	}
	rpc.Must(f.Save(*networks))
	fmt.Println("recorded", *network, "in", *networks)
}
