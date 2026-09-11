// verifydeploy proves, from a DERO daemon alone, that a DMZ deployment is this repository's source and that no
// privileged role survives the revocation ceremony. It needs no wallet and sends nothing: it reads with
// DERO.GetInfo and DERO.GetSC only.
//
//	go run ./verifydeploy -network <simulator|testnet|mainnet> [-daemon host:port] [-networks ../deploy/networks.json]
//	                      [-extra kind=scid,...] [-dist ../dist/<network> | -app ../app] [-tela-record <file>]
//	                      [-allow <kind>.<role>=<reason>]... [-json]
//
// Checks, each reported PASS / FAIL / WARN / INFO with exactly what was compared:
//   - record:    the record's template hashes are sha256(reference.Canon(contracts/<kind>.bas)); legacy hashes are
//     traced to the commit whose file produces them.
//   - identity:  every contract the record names (factory, social, founders, chat, each vault) and every -extra:
//     sha256 of the installed code against sha256(reference.Canon(<source>)), or the first differing line.
//   - templates: every contract in the factory registry against the record's accepted template hashes, as the app
//     does (app/feed.js verify()); anything unknown is named.
//   - roles:     every privileged role of every contract (roles.go), its value now, and whether it can still act.
//     A live "must end" role fails the run unless -allow names it with a reason.
//   - tela:      the INDEX is the standard TELA-INDEX-1 and whether anyone can still update it; every DOC is a
//     standard TELA-DOC-1 and serves the bundle's file byte for byte; config.js and templates served are
//     the record's.
//
// Exit status: 0 no failure, 1 at least one FAIL, 2 bad usage.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"veil/tools/netcfg"
)

type allowFlag map[string]string

func (a allowFlag) String() string {
	var s []string
	for k, v := range a {
		s = append(s, k+"="+v)
	}
	sort.Strings(s)
	return strings.Join(s, ",")
}

func (a allowFlag) Set(s string) error {
	p := strings.SplitN(s, "=", 2)
	if len(p) != 2 || strings.TrimSpace(p[1]) == "" {
		return fmt.Errorf("-allow wants <kind>.<role>=<the written reason>, got %q", s)
	}
	id := strings.TrimSpace(p[0])
	if !roleIDs()[id] {
		var ids []string
		for k := range roleIDs() {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		return fmt.Errorf("-allow %q: not a role that can fail; one of %s", id, strings.Join(ids, ", "))
	}
	a[id] = strings.TrimSpace(p[1])
	return nil
}

type options struct {
	registry, marketRoles bool
	dist, app, telaRecord string
}

type verifier struct {
	n         node
	rec       *netcfg.Network
	network   string
	contracts string
	allow     map[string]string
	hist      *history
	opts      options
	r         *report
}

// parseExtra reads -extra kind=scid,kind=scid.
func parseExtra(spec, contracts string) ([]target, error) {
	var out []target
	for _, kv := range strings.Split(spec, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		p := strings.SplitN(kv, "=", 2)
		if len(p) != 2 || !is64hex(strings.TrimSpace(p[1])) {
			return nil, fmt.Errorf("-extra %q: want kind=<64-hex scid>", kv)
		}
		kind, scid := strings.TrimSpace(p[0]), strings.TrimSpace(p[1])
		if kind != "tela" {
			if _, err := os.Stat(contracts + "/" + kind + ".bas"); err != nil {
				return nil, fmt.Errorf("-extra %q: no contracts/%s.bas to verify it against", kv, kind)
			}
		}
		out = append(out, target{Label: "extra " + kind, Kind: kind, SCID: scid})
	}
	return out, nil
}

// networkMismatch says why a daemon is not on the network the record block is for.
func networkMismatch(network string, info daemonInfo) string {
	sim := strings.Contains(strings.ToLower(info.Network), "simulator")
	switch network {
	case "mainnet":
		if info.Testnet {
			return fmt.Sprintf("-network mainnet, but the daemon reports %q (testnet=true)", info.Network)
		}
	case "simulator":
		if !info.Testnet || !sim {
			return fmt.Sprintf("-network simulator, but the daemon reports %q (testnet=%v)", info.Network, info.Testnet)
		}
	case "testnet":
		if !info.Testnet || sim {
			return fmt.Sprintf("-network testnet, but the daemon reports %q (testnet=%v)", info.Network, info.Testnet)
		}
	}
	return ""
}

func (v *verifier) run(extras []target) *report {
	v.r = &report{Tool: "tools/verifydeploy", Network: v.network, Daemon: v.n.addr, Contracts: v.contracts}
	r := v.r
	info, err := v.n.info()
	if err != nil {
		r.add("record", "daemon", "", sFail, "DERO.GetInfo at %s: %v", v.n.addr, err)
		r.finish()
		return r
	}
	r.Chain, r.Height, r.Stable = info.Network, info.Height, info.StableHeight
	if m := networkMismatch(v.network, info); m != "" {
		r.add("record", "daemon", "", sFail, "%s: nothing read from it could verify this record", m)
		r.finish()
		return r
	}
	r.SourceNote = v.hist.describe()
	v.checkRecord()
	for _, t := range v.targets(extras) {
		v.checkTarget(t)
	}
	if v.opts.registry {
		v.checkRegistry()
	}
	v.checkTela(extras)
	r.finish()
	return r
}

func main() {
	network := flag.String("network", "", "the block of -networks to verify: simulator, testnet or mainnet")
	daemon := flag.String("daemon", "", "DERO daemon RPC host:port (default: the record's daemon)")
	networks := flag.String("networks", "../deploy/networks.json", "the deployment record")
	contracts := flag.String("contracts", "../contracts", "the contract sources")
	extra := flag.String("extra", "", "comma-separated kind=scid the record does not carry: htlc=, swap=, board=, vault=, …, tela=<INDEX>")
	dist := flag.String("dist", "", "the bundle the DOCs must equal byte for byte, e.g. ../dist/<network>")
	app := flag.String("app", "", "without -dist: compare the DOCs with this app source directory (config.js rendered from the record)")
	telaRecord := flag.String("tela-record", "", "a telainstall record (path -> DOC scid) the INDEX's DOC list must equal")
	asJSON := flag.Bool("json", false, "print the report as JSON")
	registry := flag.Bool("registry", true, "check every contract in the factory registry against the record's templates")
	marketRoles := flag.Bool("market-roles", false, "also read the roles of every registered market")
	hist := flag.Bool("history", true, "trace hashes that are not the current source through git history")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request timeout")
	roles := flag.Bool("roles", false, "print the privileged-role table this tool applies, with its citations, and exit (no daemon needed)")
	allow := allowFlag{}
	flag.Var(allow, "allow", "<kind>.<role>=<reason>: accept one live must-end role, with a written reason (repeatable)")
	flag.Parse()
	if *roles {
		printRoleTable(os.Stdout)
		return
	}
	if *network == "" || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: verifydeploy -network <name> [flags]; -h lists them")
		os.Exit(2)
	}
	if *dist != "" && *app != "" {
		fmt.Fprintln(os.Stderr, "-dist and -app are two answers to one question: pass one")
		os.Exit(2)
	}
	http.DefaultClient.Timeout = *timeout // simrpc.Call posts with the default client
	f, err := netcfg.Load(*networks)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	rec, ok := f.Networks[*network]
	if !ok {
		fmt.Fprintf(os.Stderr, "%s has no %q block\n", *networks, *network)
		os.Exit(2)
	}
	extras, err := parseExtra(*extra, *contracts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	addr := *daemon
	if addr == "" {
		addr = rec.Daemon
	}
	v := &verifier{n: node{addr}, rec: rec, network: *network, contracts: *contracts, allow: allow, hist: newHistory(*contracts, *hist),
		opts: options{registry: *registry, marketRoles: *marketRoles, dist: *dist, app: *app, telaRecord: *telaRecord}}
	r := v.run(extras)
	if *asJSON {
		if err := r.writeJSON(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	} else {
		r.writeText(os.Stdout)
	}
	if r.failed() {
		os.Exit(1)
	}
}
