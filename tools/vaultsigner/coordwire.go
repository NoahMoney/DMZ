package main

// Signer coordination wiring: the -coord and -coord-key flags, the handle the foreign backends hold, and the
// `vaultsigner keygen | identity | relay` subcommands. The channel itself is tools/vaultsigner/coord; the design is
// research/wrapper/DESIGN.md §C7 and the operator procedures are docs/SIGNER_RUNBOOK.md.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	rpc "veil/tools/simrpc"
	"veil/tools/vaultsigner/coord"
)

var (
	coordPath = flag.String("coord", "", "signer coordination configuration (JSON: the vault, every signer's pinned identity, the relays or shared directories; docs/SIGNER_RUNBOOK.md). Backends that sign with their peers (zcash, monero) exchange signatures through it")
	coordKey  = flag.String("coord-key", "", "this signer's coordination identity file (made by `vaultsigner keygen`); overrides identity_file in -coord")
)

// Coordinator is what a backend holds to exchange signing material with its peers:
//
//	Put(ctx, round, payload) error                    publish this signer's payload for a round
//	Get(ctx, round) (map[signer id][]byte, error)     every signer's payload that has arrived; never waits
type Coordinator = coord.Coordinator

// coordHandle is the Coordinator a backend is given when pickBackend builds it, which is before main knows this
// signer's address. It fails closed until startCoord has opened the node: a backend that asks for its peers'
// payloads before coordination runs gets an error, never an empty answer it could take for silence.
var coordHandle = &lazyCoord{}

var errCoordOff = errors.New("signer coordination is not running: start vaultsigner with -coord <config> (docs/SIGNER_RUNBOOK.md)")

type lazyCoord struct{ n atomic.Pointer[coord.Node] }

func (l *lazyCoord) Put(ctx context.Context, round string, payload []byte) error {
	n := l.n.Load()
	if n == nil {
		return errCoordOff
	}
	return n.Put(ctx, round, payload)
}

func (l *lazyCoord) Get(ctx context.Context, round string) (map[string][]byte, error) {
	n := l.n.Load()
	if n == nil {
		return nil, errCoordOff
	}
	return n.Get(ctx, round)
}

// startCoord opens this signer's node from -coord and hands it to coordHandle. It refuses a configuration for
// another vault, an identity that is not this signer's wallet, and a pinned signer the vault never seated; it warns
// about a pinned signer the vault has since slashed.
func startCoord(c *rpc.Client, vaultID, self string) (*coord.Node, error) {
	fc, err := coord.ReadConfigFile(*coordPath)
	if err != nil {
		return nil, err
	}
	if fc.Vault != strings.ToLower(vaultID) {
		return nil, fmt.Errorf("%s is for vault %s, not %s", *coordPath, fc.Vault, vaultID)
	}
	cfg, err := fc.NodeConfig(*coordKey)
	if err != nil {
		return nil, err
	}
	if cfg.Identity.ID() != self {
		return nil, fmt.Errorf("the coordination identity belongs to %s, but this signer's wallet is %s", cfg.Identity.ID(), self)
	}
	pinned := make([]string, 0, len(cfg.Signers))
	for _, p := range cfg.Signers {
		pinned = append(pinned, p.ID)
	}
	warnings, err := checkPinned(pinned, seatedSigners(c.GetSC(vaultID, false)))
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		log.Printf("coordination: %s", w)
	}
	cfg.Logf = log.Printf
	node, err := coord.New(cfg)
	if err != nil {
		return nil, err
	}
	coordHandle.n.Store(node)
	var names []string
	for _, s := range cfg.Stores {
		names = append(names, s.Name())
	}
	fmt.Printf("coordination: %d signers pinned, fingerprint %s (compare it with every other signer's); stores %s\n", len(cfg.Signers), node.Fingerprint(), strings.Join(names, ", "))
	go watchCoord(node, max(*interval, 30*time.Second))
	return node, nil
}

// watchCoord logs the channel's health when it changes. It never stops the daemon: a signer that cannot reach its
// peers still attests, mints and verifies; only the signing that needs its peers waits.
func watchCoord(n *coord.Node, every time.Duration) {
	last := ""
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := n.Health(ctx)
		cancel()
		msg := "every store reachable"
		if err != nil {
			msg = err.Error()
		}
		if msg != last {
			log.Printf("coordination: %s", msg)
			last = msg
		}
		time.Sleep(every)
	}
}

type seat int

const (
	seatActive seat = iota
	seatSlashed
	seatUnknown
)

// seatedSigners reads every signer the vault ever seated (sg_<i> for i < n, each a raw 33-byte key) and whether it is
// still active: isg_<raw key> is 1 for an active signer and 0 once slashed. That key's name holds raw bytes, which
// DERO.GetSC returns through JSON, so it is looked up by the name JSON would have given it (as verifydeploy does).
func seatedSigners(st rpc.SCState) map[string]seat {
	out := map[string]seat{}
	n := st.U0("n")
	for i := uint64(0); i < n && i < 64; i++ {
		v, ok := st.StringKeys["sg_"+strconv.FormatUint(i, 10)].(string)
		if !ok {
			continue
		}
		raw, err := hex.DecodeString(v)
		if err != nil {
			continue
		}
		id := rpc.AddressFromRaw(raw)
		if id == "" {
			continue
		}
		name := lossyName("isg_" + string(raw))
		switch {
		case !st.Has(name):
			out[id] = seatUnknown
		case st.U(name) == 0:
			out[id] = seatSlashed
		default:
			out[id] = seatActive
		}
	}
	return out
}

func lossyName(key string) string {
	b, _ := json.Marshal(key)
	var s string
	_ = json.Unmarshal(b, &s)
	return s
}

// checkPinned refuses a pinned signer the vault never seated (a typo, or a configuration for another vault) and
// returns a warning for each one the vault has slashed: its messages are still accepted until every signer removes
// it from the configuration, and its key stays in the foreign multisig until the funds migrate.
func checkPinned(pinned []string, seated map[string]seat) ([]string, error) {
	var warnings []string
	for _, id := range pinned {
		s, ok := seated[id]
		if !ok {
			return nil, fmt.Errorf("the coordination configuration pins %s, which this vault never seated as a signer (a typo, or a configuration for another vault)", id)
		}
		if s == seatSlashed {
			warnings = append(warnings, id+" was slashed from this vault: remove it from the coordination configuration on every signer, and migrate the foreign funds to a key set without it (docs/SIGNER_RUNBOOK.md)")
		}
	}
	return warnings, nil
}

// runSubcommand runs `vaultsigner keygen | identity | relay …` and reports whether args named one, with the exit code.
func runSubcommand(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "keygen":
		return true, cmdKeygen(args[1:], stdout, stderr)
	case "identity":
		return true, cmdIdentity(args[1:], stdout, stderr)
	case "relay":
		return true, cmdRelay(args[1:], stdout, stderr)
	}
	return false, 0
}

func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vaultsigner keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "this signer's DERO address: its signer wallet's, as the vault seats it")
	out := fs.String("out", "", "identity file to create; it must not exist and is written readable by this account only")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *out == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: vaultsigner keygen -id <DERO address> -out <identity file>")
		return 2
	}
	pub, err := coord.GenerateIdentityFile(*out, *id)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (readable by this account only; back it up offline; it is never printed)\n\n", *out)
	fmt.Fprintf(stdout, "id           %s\nidentity     %s\nfingerprint  %s\n\n", pub.ID, pub.String(), pub.Fingerprint())
	fmt.Fprintln(stdout, "Send the id and identity lines to every other signer. Read the fingerprint to each of them on a second channel.")
	return 0
}

func cmdIdentity(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vaultsigner identity", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "identity file")
	cfgPath := fs.String("coord", "", "optional: a coordination configuration to check this identity against")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *key == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: vaultsigner identity -key <identity file> [-coord <configuration>]")
		return 2
	}
	idn, err := coord.LoadIdentityFile(*key)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	pub := idn.Public()
	fmt.Fprintf(stdout, "id           %s\nidentity     %s\nfingerprint  %s\n", pub.ID, pub.String(), pub.Fingerprint())
	if *cfgPath == "" {
		return 0
	}
	fc, err := coord.ReadConfigFile(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	peers, err := fc.Peers()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fp, _ := fc.Fingerprint()
	fmt.Fprintf(stdout, "vault        %s\nsigner set   %d signers, fingerprint %s\n", fc.Vault, len(peers), fp)
	for _, p := range peers {
		if p.ID == pub.ID {
			if p.String() != pub.String() {
				fmt.Fprintln(stderr, "this configuration pins a DIFFERENT identity for this signer: the other signers would refuse its messages")
				return 1
			}
			fmt.Fprintln(stdout, "this identity is pinned in the configuration")
			return 0
		}
	}
	fmt.Fprintln(stderr, "this signer is not pinned in the configuration")
	return 1
}

func cmdRelay(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vaultsigner relay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:8445", "address to serve on")
	var configs []string
	fs.Func("config", "a coordination configuration whose vault and pinned signers this relay serves (repeat for several vaults; no identity file is read)", func(s string) error {
		configs = append(configs, s)
		return nil
	})
	cert := fs.String("tls-cert", "", "TLS certificate file (with -tls-key)")
	key := fs.String("tls-key", "", "TLS key file")
	maxBytes := fs.Int64("max-bytes", 1<<30, "total bytes held, shared equally among the pinned signers")
	maxPayload := fs.Int("max-payload", coord.DefaultMaxPayload, "largest payload accepted")
	maxRounds := fs.Int("max-rounds", 4096, "rounds held per signer")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if len(configs) == 0 || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: vaultsigner relay -config <coordination configuration> [-config …] [-listen host:port] [-tls-cert f -tls-key f]")
		return 2
	}
	var groups []coord.RelayGroup
	for _, p := range configs {
		fc, err := coord.ReadConfigFile(p)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		g, err := fc.RelayGroup()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		groups = append(groups, g)
	}
	r, err := coord.NewRelay(coord.RelayConfig{Groups: groups, MaxPayload: *maxPayload, MaxTotalBytes: *maxBytes, MaxRoundsPerSender: *maxRounds, Logf: log.Printf})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer r.Close()
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	scheme := "http"
	if *cert != "" {
		scheme = "https"
	}
	fmt.Fprintf(stdout, "relay: %s://%s for %d vault(s); it holds no key and is trusted with nothing\n", scheme, ln.Addr(), len(groups))
	if *cert == "" && !rpc.IsLoopback(*listen) {
		fmt.Fprintln(stdout, "relay: plain HTTP on a non-loopback address: payloads stay sealed end to end, but a network observer sees when signers talk; prefer -tls-cert/-tls-key or a tunnel")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := coord.ServeRelay(ctx, ln, r, *cert, *key); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
