// Package netcfg reads and writes deploy/networks.json (the per-network source of truth) and renders a
// network's block as app/config.js — the same shape the app has always read, one network per build.
package netcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type Template struct {
	Sha256 string   `json:"sha256"`
	Legacy []string `json:"legacy"`
}

// VaultAsset describes what a vault wraps, for display only. The contract stores no decimals and no chain name:
// it counts foreign base units and nothing else. These are the deployer's written-down answer to "base units of
// what", so the app can render a number a person recognises without ever guessing one.
type VaultAsset struct {
	Name  string `json:"name"`  // "Monero"
	Sym   string `json:"sym"`   // "XMR"
	Chain string `json:"chain"` // "Monero mainnet"
	Dec   int    `json:"dec"`   // base units per whole coin, as a power of ten: 12 for XMR, 8 for ZEC
	Conf  int    `json:"conf"`  // foreign confirmations the federation waits for, published so a user can time it
}

// VaultRec is one wrapped asset: its vault contract and what that vault wraps. There is one per foreign chain,
// because a vault holds exactly one asset — a network that can wrap Monero and Zcash runs two of them, and a
// single `vault` field could never say so. `vaults` is the only place a vault's metadata is recorded: the file
// used to carry a second copy under `vaultAsset`, and the two copies drifted (5 decimals against 12 for the same
// contract), so that key is gone and anything still in the file under it is ignored.
type VaultRec struct {
	Scid string `json:"scid"`
	VaultAsset
	// A superseded vault. Its markets keep their name and their decimals — a market quoted in it must never
	// render a contract id where a symbol belongs, or scale by a placeholder — and a holder of its units can
	// still burn them. It is not offered for a new deposit and not offered as a launch quote. The same shape
	// as a legacy template hash: what was verified stays verifiable, what is current is one thing.
	Legacy bool `json:"legacy,omitempty"`
}

type Network struct {
	Daemon   string     `json:"daemon"`
	Factory  string     `json:"factory"`
	Social   string     `json:"social"`
	Founders string     `json:"founders"`
	Vault    string     `json:"vault"` // the first wrapped asset, for the app's older readers; its record is in Vaults
	Vaults   []VaultRec `json:"vaults"`
	Chat     string     `json:"chat"`
	// The atomic-swap contracts (GAP-002). They have no owner or fee, and their parameters (swap.bas wmin; htlc.bas
	// w1min, w2min, g2) are fixed at install, so one instance per network serves every swap. The companions compare
	// the code at these ids with the canonical source, and read those parameters, before trusting them.
	Htlc  string `json:"htlc"`  // contracts/htlc.bas: the DERO leg of the Monero swap
	Swap  string `json:"swap"`  // contracts/swap.bas: the DERO leg of the transparent-Zcash swap
	Board string `json:"board"` // contracts/board.bas: the Zcash market's offers
	// Rate is contracts/rate.bas, community sentiment (app/sentiment.js): no owner, a fee per vote fixed at install.
	Rate string `json:"rate"`
	// ZecTreasury is the transparent Zcash address (P2PKH or P2SH: t1…/t3… on mainnet, tm…/t2… on testnet, and on
	// regtest through the simulator's block) that the maker's claim in a transparent-Zcash swap pays the 0.5 % swap
	// fee to (tools/zecswap, docs/ZEC_SWAP.md §7). Empty or invalid on mainnet, the companion refuses to start a swap.
	ZecTreasury string `json:"zecTreasury"`
	// XmrTreasury is the Monero address or subaddress the 0.5 % swap fee pays (tools/xmrswap). tools/treasurybot counts what
	// reaches it and pays the passes their share. Empty: not set for this network.
	XmrTreasury string `json:"xmrTreasury"`
	// The treasury addresses a listed market may pay (GAP-005), current first. A market's treasury is fixed at its
	// install, so the app checks it against this published set instead of the factory's tstr pointer, which an older
	// factory's owner could move until Renounce (factory.bas v2 fixes it at install). A record without it renders
	// without it, and the app falls back to tstr.
	Treasuries    []string            `json:"treasuries,omitempty"`
	FeeBps        uint64              `json:"feeBps"`
	PfeeBps       uint64              `json:"pfeeBps"`
	LaunchPfeeBps uint64              `json:"launchPfeeBps"`
	Templates     map[string]Template `json:"templates"`
	Tela          struct {
		Index string `json:"index"`
		Durl  string `json:"durl"`
	} `json:"tela"`
}

// hasVault says whether scid has a record in Vaults.
func (n *Network) hasVault(scid string) bool {
	for _, r := range n.Vaults {
		if r.Scid == scid {
			return true
		}
	}
	return false
}

type File struct {
	Doc      string              `json:"_doc"`
	Networks map[string]*Network `json:"-"`
}

var Names = []string{"simulator", "testnet", "mainnet"}

// Load reads networks.json; unknown top-level keys other than _doc are networks.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	f := &File{Networks: map[string]*Network{}}
	for k, v := range raw {
		if k == "_doc" {
			_ = json.Unmarshal(v, &f.Doc)
			continue
		}
		n := &Network{Templates: map[string]Template{}}
		if err := json.Unmarshal(v, n); err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		// A vault named without its record has no decimals and no chain name to render, and there is no second
		// place to look for them any more: the file refuses to load rather than let config.js drop the vault.
		if len(n.Vault) == 64 && !n.hasVault(n.Vault) {
			return nil, fmt.Errorf("%s: vault %s has no record in vaults: add it there, with its name, sym, chain, dec and conf", k, n.Vault)
		}
		f.Networks[k] = n
	}
	return f, nil
}

func (f *File) Save(path string) error {
	out := map[string]interface{}{"_doc": f.Doc}
	for k, v := range f.Networks {
		out[k] = v
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Deployed says whether the network has the three core contracts.
func (n *Network) Deployed() bool {
	return len(n.Factory) == 64 && len(n.Social) == 64 && len(n.Founders) == 64
}

// ConfigJS renders the block as the app's config.js.
func ConfigJS(name string, n *Network, generator string) string {
	tpl := func(k string) string {
		t := n.Templates[k]
		leg := make([]string, 0, len(t.Legacy))
		for _, l := range t.Legacy {
			leg = append(leg, fmt.Sprintf("%q", l))
		}
		return fmt.Sprintf(`{ file: "templates/%s.bas", sha256: %q, legacy: [%s] }`, k, t.Sha256, strings.Join(leg, ", "))
	}
	keys := make([]string, 0, len(n.Templates))
	for k := range n.Templates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("    %-9s %s", k+":", tpl(k)))
	}
	// One entry per wrapped asset, from `vaults` alone: a network wrapping Monero and Zcash has two. The single
	// `vault` / `vaultAsset` pair the app's older readers (names.js allVaults, vault.js loadVault) fall back to is the first record,
	// derived here rather than stored a second time.
	recs := n.Vaults
	vault := ""
	if len(recs) > 0 {
		var lines []string
		first := -1
		for i, r := range recs {
			leg := ""
			if r.Legacy {
				leg = ", legacy: true"
			} else if first < 0 {
				first = i // the single-vault spelling names the first CURRENT record, never a superseded one
			}
			lines = append(lines, fmt.Sprintf("    { scid: %q, name: %q, sym: %q, chain: %q, dec: %d, conf: %d%s }",
				r.Scid, r.Name, r.Sym, r.Chain, r.Dec, r.Conf, leg))
		}
		vault = fmt.Sprintf("  vaults: [\n%s\n  ],\n", strings.Join(lines, ",\n"))
		if first >= 0 {
			vault += fmt.Sprintf("  vault: %q,\n  vaultAsset: { name: %q, sym: %q, chain: %q, dec: %d, conf: %d },\n",
				recs[first].Scid, recs[first].Name, recs[first].Sym, recs[first].Chain, recs[first].Dec, recs[first].Conf)
		}
	}
	if len(n.Chat) == 64 {
		vault += fmt.Sprintf("  chat: %q,\n", n.Chat)
	}
	for _, c := range []struct{ k, v string }{{"htlc", n.Htlc}, {"swap", n.Swap}, {"board", n.Board}, {"rate", n.Rate}} {
		if len(c.v) == 64 {
			vault += fmt.Sprintf("  %s: %q,\n", c.k, c.v)
		}
	}
	if len(n.Treasuries) > 0 {
		q := make([]string, 0, len(n.Treasuries))
		for _, t := range n.Treasuries {
			q = append(q, fmt.Sprintf("%q", t))
		}
		vault += fmt.Sprintf("  treasuries: [%s],\n", strings.Join(q, ", "))
	}
	return fmt.Sprintf(`// DMZ deployment configuration — generated by %s on %s for %s.
// Template hashes are SHA-256 of the exact contract source; a listed contract is shown only if its on-chain
// code hashes to one of these and its treasury is one this build lists (the factory's, when it lists none). `+"`legacy`"+` lists earlier versions of a
// template that stay accepted (their contracts are immutable and were verified when they were current).
window.VEIL_CONFIG = {
  network: %q,
  daemon: %q,
  factory: %q,
  social: %q,
  founders: %q,
%s  feeBps: %d, pfeeBps: %d, launchPfeeBps: %d,
  templates: {
%s
  }
};
`, generator, time.Now().UTC().Format(time.RFC3339), name, name, n.Daemon, n.Factory, n.Social, n.Founders, vault,
		n.FeeBps, n.PfeeBps, n.LaunchPfeeBps, strings.Join(lines, ",\n"))
}
