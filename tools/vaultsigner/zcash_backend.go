package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	rpc "veil/tools/simrpc"
	"veil/tools/vaultsigner/zcashvault"
)

// -backend zcash: the vault's reserve in transparent P2SH k-of-n multisig addresses on Zcash (docs/HANDOVER.md §5.1,
// §5.4). The backend is tools/vaultsigner/zcashvault; this file adapts it to the daemon's Backend interface and gives
// it the one thing it reads from DERO — the vault's registered indexes and attested addresses.

var zcashConfig = flag.String("zcash-config", "", "-backend zcash: the backend's JSON configuration (tools/vaultsigner/zcashvault Config): Zcash network, k, the signers' account xpubs, this signer's seed file (outside every repository), the nodes, the release ledger")

// zcashCoordinator relays signatures between the signers. The coordinator's own wiring sets it before pickBackend;
// left nil, the configuration's coordinatorDir (a shared directory, the pilot's relay) is used, and with neither the
// backend refuses to start.
var zcashCoordinator zcashvault.Coordinator

// zcashNetFor is the only Zcash network a vault on each DERO network may hold: test coins behind test vaults, real
// coins only behind mainnet ones.
var zcashNetFor = map[string]string{"simulator": "regtest", "testnet": "testnet", "mainnet": "mainnet"}

func newZcashBackend(network string) (Backend, error) {
	if *zcashConfig == "" {
		return nil, errors.New("-backend zcash needs -zcash-config <file>: the Zcash network, k, the signers' account xpubs, this signer's seed file, the nodes and the release ledger")
	}
	cfg, err := zcashvault.LoadConfig(*zcashConfig)
	if err != nil {
		return nil, err
	}
	if want := zcashNetFor[network]; cfg.Network != want {
		return nil, fmt.Errorf("a %s vault holds Zcash %s coins, and -zcash-config says %q", network, want, cfg.Network)
	}
	if len(*vault) != 64 {
		return nil, errors.New("-backend zcash needs -vault: the vault's scid names its addresses")
	}
	co := zcashCoordinator
	if co == nil && *coordPath != "" {
		// the signer coordination channel (coordwire.go): it fails closed until main has started it, so nothing can
		// be signed before the peers are reachable through it
		if cfg.CoordinatorDir != "" {
			return nil, errors.New("-coord and the configuration's coordinatorDir both name a coordinator; use -coord (coordinatorDir is the pilot's unauthenticated shared directory)")
		}
		co = coordHandle
	}
	if co == nil && cfg.CoordinatorDir != "" && network == "mainnet" {
		return nil, errors.New("on mainnet the signatures travel only through -coord (authenticated, encrypted), never the pilot's shared directory")
	}
	be, err := zcashvault.New(cfg, *vault, &contractView{daemon: *daemon, scid: *vault}, co, log.Printf)
	if err != nil {
		return nil, err
	}
	fmt.Println(be.Summary())
	fmt.Printf("view key (publish with SetViewKey): %s\n", be.Descriptor().ViewKey())
	return zcashBackend{be}, nil
}

type zcashBackend struct{ *zcashvault.Backend }

func (z zcashBackend) Deposits() ([]Deposit, error) {
	ds, err := z.Backend.Deposits()
	if err != nil {
		return nil, err
	}
	out := make([]Deposit, len(ds))
	for i, d := range ds {
		out[i] = Deposit{TxID: d.TxID, Vout: d.Vout, Index: d.Index, Amount: d.Amount, Height: d.Height}
	}
	return out, nil
}

func (z zcashBackend) Verify(d Deposit) (bool, error) {
	return z.Backend.Verify(zcashvault.Deposit{TxID: d.TxID, Vout: d.Vout, Index: d.Index, Amount: d.Amount, Height: d.Height})
}

// contractView reads un and da_<j> from the vault, with errors instead of simrpc's exits, and holds one reading for
// two seconds so a pass that asks about every index reads the contract once.
type contractView struct {
	daemon, scid string
	mu           sync.Mutex
	at           time.Time
	keys         map[string]interface{}
}

func (c *contractView) read() (map[string]interface{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keys != nil && time.Since(c.at) < 2*time.Second {
		return c.keys, nil
	}
	var raw json.RawMessage
	if err := rpc.Call(c.daemon, "DERO.GetSC", map[string]interface{}{"scid": c.scid, "variables": true, "code": false}, &raw); err != nil {
		return nil, err
	}
	var st struct {
		StringKeys map[string]interface{} `json:"stringkeys"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&st); err != nil {
		return nil, fmt.Errorf("DERO.GetSC: %w", err)
	}
	if st.StringKeys == nil {
		return nil, fmt.Errorf("DERO.GetSC: vault %s has no state", c.scid)
	}
	c.keys, c.at = st.StringKeys, time.Now()
	return c.keys, nil
}

func (c *contractView) Indexes() (uint64, error) {
	keys, err := c.read()
	if err != nil {
		return 0, err
	}
	n, ok := keys["un"].(json.Number)
	if !ok {
		return 0, errors.New("the vault has no un")
	}
	return strconv.ParseUint(n.String(), 10, 64)
}

func (c *contractView) Attested(j uint64) (string, bool, error) {
	keys, err := c.read()
	if err != nil {
		return "", false, err
	}
	v, ok := keys["da_"+strconv.FormatUint(j, 10)]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("da_%d is not a string", j)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", false, fmt.Errorf("da_%d is not hex", j)
	}
	return string(b), true, nil
}
