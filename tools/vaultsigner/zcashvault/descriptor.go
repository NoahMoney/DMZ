package zcashvault

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"veil/tools/zcashtx"
)

// The vault's custody on Zcash, as built for launch (docs/HANDOVER.md §5.1, 2026-09-15): a transparent
// pay-to-script-hash k-of-n multisig per address,
//
//	OP_k <pubkey_1> … <pubkey_n> OP_n OP_CHECKMULTISIG
//
// with the n compressed public keys sorted by their bytes (BIP 67), so the script, and the address, depend only on the
// set of keys and never on who lists them in which order. The keys of one address are the n signers' children at the
// same non-hardened path under each signer's published account key:
//
//	account xpub / branch / chain / index
//
// branch is the vault's own (the top 31 bits of its DERO scid, big-endian, as a non-hardened index), so two vaults
// that share signers never share an address; chain 0 is the deposit chain (index = the vault index j) and chain 1
// the change chain (index 0, the vault's one change address). Anyone holding the descriptor — the network, k and the
// n account keys — derives every address of the vault, and so can sum its reserve on the public chain.

// Net is the Zcash network a vault lives on.
type Net string

const (
	NetMainnet Net = "mainnet"
	NetTestnet Net = "testnet"
	NetRegtest Net = "regtest"
)

// addrNet is the address prefix family: regtest uses testnet's.
func (n Net) addrNet() zcashtx.Network {
	if n == NetMainnet {
		return zcashtx.Mainnet
	}
	return zcashtx.Testnet
}

// chainName is what the node's getblockchaininfo reports (ChainInfo.Chain; "regtest" only for regtest's genesis).
func (n Net) chainName() string {
	switch n {
	case NetMainnet:
		return "main"
	case NetTestnet:
		return "test"
	}
	return "regtest"
}

// coinType is BIP 44's for the account path: 133 is Zcash (SLIP 44); every test network uses 1.
func (n Net) coinType() uint32 {
	if n == NetMainnet {
		return 133
	}
	return 1
}

func parseNet(s string) (Net, error) {
	switch Net(s) {
	case NetMainnet, NetTestnet, NetRegtest:
		return Net(s), nil
	}
	return "", fmt.Errorf("zcashvault: Zcash network %q is not mainnet, testnet or regtest", s)
}

const (
	// ChainDeposit and ChainChange are the two chains under a vault's branch.
	ChainDeposit uint32 = 0
	ChainChange  uint32 = 1
	// MaxSigners is the most keys a standard P2SH CHECKMULTISIG script holds: 15 compressed keys make a 513-byte
	// redeem script, under the interpreter's 520-byte element limit.
	MaxSigners = 15
	// viewKeyMax is vault.bas SetViewKey's bound on the published string.
	viewKeyMax = 400
	// descriptorTag names this descriptor format and its version.
	descriptorTag = "dmz-zec-p2sh/1"
)

// Descriptor is the vault's public custody definition.
type Descriptor struct {
	Net   Net
	K     int
	XPubs []string // sorted, canonical
	Vault string   // the DERO scid, 64 lower-case hex characters

	keys   []*ExtKey // in XPubs order
	branch uint32

	mu    sync.Mutex
	cache map[[2]uint32]*VaultScript
}

// VaultScript is one address of the vault.
type VaultScript struct {
	Chain, Index uint32
	Pubs         [][]byte // sorted
	Redeem       []byte
	ScriptPubKey []byte // P2SH
	Address      string
	// signer[i] is the position in Pubs of the key derived from d.XPubs[i]
	signer []int
}

// NewDescriptor checks and canonicalises a vault's custody definition. It refuses: an unknown network; k outside
// 2..n−1 (a k-of-k set freezes the reserve on one lost key, and 1-of-n is one key); more than MaxSigners keys; an
// extended PRIVATE key (a descriptor is published); an extended key of the other network; and two signers with the
// same account key, which would let one signer fill two of the k signatures CHECKMULTISIG counts.
func NewDescriptor(net string, k int, xpubs []string, vault string) (*Descriptor, error) {
	nt, err := parseNet(net)
	if err != nil {
		return nil, err
	}
	if len(vault) != 64 || strings.ToLower(vault) != vault {
		return nil, errors.New("zcashvault: the vault scid must be 64 lower-case hex characters")
	}
	raw, err := hex.DecodeString(vault)
	if err != nil {
		return nil, errors.New("zcashvault: the vault scid is not hex")
	}
	n := len(xpubs)
	if n > MaxSigners {
		return nil, fmt.Errorf("zcashvault: %d signers; a standard P2SH multisig holds at most %d", n, MaxSigners)
	}
	if k < 2 || k >= n {
		return nil, fmt.Errorf("zcashvault: %d-of-%d: k must be at least 2 and below n (a k-of-k vault freezes on one lost key)", k, n)
	}
	d := &Descriptor{Net: nt, K: k, Vault: vault, cache: map[[2]uint32]*VaultScript{}}
	d.branch = binary.BigEndian.Uint32(raw[:4]) &^ HardenedOffset
	sorted := append([]string{}, xpubs...)
	sort.Strings(sorted)
	seen := map[string]bool{}
	for _, s := range sorted {
		key, err := ParseExtKey(strings.TrimSpace(s))
		if err != nil {
			return nil, err
		}
		if key.IsPrivate() {
			key.Zero()
			return nil, errors.New("zcashvault: the descriptor names an extended PRIVATE key; publish the xpub (and treat that private key as exposed)")
		}
		if key.Testnet() != (nt != NetMainnet) {
			return nil, fmt.Errorf("zcashvault: %s… is not an extended key for %s", s[:8], nt)
		}
		pk := string(key.PubKey())
		if seen[pk] {
			return nil, errors.New("zcashvault: two signers publish the same account key: one signer would fill two signature slots")
		}
		seen[pk] = true
		d.XPubs = append(d.XPubs, key.XPub())
		d.keys = append(d.keys, key)
	}
	return d, nil
}

// N is the number of signers.
func (d *Descriptor) N() int { return len(d.XPubs) }

// Branch is the vault's non-hardened branch index.
func (d *Descriptor) Branch() uint32 { return d.branch }

// Script derives (and caches) the vault's address at chain/index.
func (d *Descriptor) Script(chain, index uint32) (*VaultScript, error) {
	if chain != ChainDeposit && chain != ChainChange {
		return nil, fmt.Errorf("zcashvault: chain %d is neither deposit (0) nor change (1)", chain)
	}
	if index >= HardenedOffset {
		return nil, fmt.Errorf("zcashvault: index %d is not a non-hardened index", index)
	}
	key := [2]uint32{chain, index}
	d.mu.Lock()
	if vs, ok := d.cache[key]; ok {
		d.mu.Unlock()
		return vs, nil
	}
	d.mu.Unlock()
	vs := &VaultScript{Chain: chain, Index: index}
	raw := make([][]byte, len(d.keys))
	for i, k := range d.keys {
		c, err := k.Path(d.branch, chain, index)
		if err != nil {
			return nil, fmt.Errorf("zcashvault: signer %d at %d/%d/%d: %w", i, d.branch, chain, index, err)
		}
		raw[i] = c.PubKey()
	}
	vs.Pubs = append([][]byte{}, raw...)
	sort.Slice(vs.Pubs, func(a, b int) bool { return bytes.Compare(vs.Pubs[a], vs.Pubs[b]) < 0 })
	for i := 1; i < len(vs.Pubs); i++ {
		if bytes.Equal(vs.Pubs[i-1], vs.Pubs[i]) {
			return nil, fmt.Errorf("zcashvault: two signers derive the same key at %d/%d: refusing the address", chain, index)
		}
	}
	vs.signer = make([]int, len(raw))
	for i, pk := range raw {
		for p, q := range vs.Pubs {
			if bytes.Equal(pk, q) {
				vs.signer[i] = p
			}
		}
	}
	redeem, err := RedeemScript(d.K, vs.Pubs)
	if err != nil {
		return nil, err
	}
	vs.Redeem = redeem
	h := zcashtx.Hash160(redeem)
	vs.ScriptPubKey = zcashtx.P2SHScript(h)
	vs.Address = zcashtx.EncodeAddress(d.Net.addrNet(), zcashtx.P2SH, h)
	d.mu.Lock()
	d.cache[key] = vs
	d.mu.Unlock()
	return vs, nil
}

// DepositAddress is vault index j's deposit address.
func (d *Descriptor) DepositAddress(j uint64) (string, error) {
	if j >= HardenedOffset {
		return "", fmt.Errorf("zcashvault: vault index %d is beyond the non-hardened range", j)
	}
	vs, err := d.Script(ChainDeposit, uint32(j))
	if err != nil {
		return "", err
	}
	return vs.Address, nil
}

// Change is the vault's change script.
func (d *Descriptor) Change() (*VaultScript, error) { return d.Script(ChainChange, 0) }

// Text is the canonical descriptor: the tag, the network, k and the sorted account keys. Anyone holding it and the
// vault's scid derives every address of the vault.
func (d *Descriptor) Text() string {
	return descriptorTag + " net=" + string(d.Net) + " k=" + strconv.Itoa(d.K) + " " + strings.Join(d.XPubs, ",")
}

// ViewKey is what the signers publish with vault.bas SetViewKey: the descriptor itself when it fits the contract's
// 400 characters (2-of-3 does), else its SHA-256, which binds whoever holds the full text to the published value.
func (d *Descriptor) ViewKey() string {
	t := d.Text()
	if len(t) <= viewKeyMax {
		return t
	}
	sum := sha256.Sum256([]byte(t))
	return descriptorTag + " sha256=" + hex.EncodeToString(sum[:])
}

// ParseDescriptorText reads Text's output back, for the vault whose scid is given.
func ParseDescriptorText(text, vault string) (*Descriptor, error) {
	f := strings.Fields(text)
	if len(f) != 4 || f[0] != descriptorTag || !strings.HasPrefix(f[1], "net=") || !strings.HasPrefix(f[2], "k=") {
		return nil, fmt.Errorf("zcashvault: not a %s descriptor", descriptorTag)
	}
	k, err := strconv.Atoi(f[2][2:])
	if err != nil {
		return nil, errors.New("zcashvault: the descriptor's k is not a number")
	}
	d, err := NewDescriptor(f[1][4:], k, strings.Split(f[3], ","), vault)
	if err != nil {
		return nil, err
	}
	if d.Text() != text {
		return nil, errors.New("zcashvault: not the canonical form of that descriptor")
	}
	return d, nil
}

// RedeemScript is OP_k <pub_1> … <pub_n> OP_n OP_CHECKMULTISIG for compressed keys already sorted.
func RedeemScript(k int, pubs [][]byte) ([]byte, error) {
	n := len(pubs)
	if k < 1 || k > n || n > MaxSigners {
		return nil, fmt.Errorf("zcashvault: a %d-of-%d CHECKMULTISIG script", k, n)
	}
	b := []byte{byte(zcashtx.OpTrue - 1 + k)}
	for i, p := range pubs {
		if len(p) != 33 || (p[0] != 2 && p[0] != 3) {
			return nil, errors.New("zcashvault: a multisig key must be a compressed public key")
		}
		if i > 0 && bytes.Compare(pubs[i-1], p) >= 0 {
			return nil, errors.New("zcashvault: the multisig keys must be sorted and distinct (BIP 67)")
		}
		b = append(b, 33)
		b = append(b, p...)
	}
	b = append(b, byte(zcashtx.OpTrue-1+n), zcashtx.OpCheckMultiSig)
	if len(b) > zcashtx.MaxScriptElementSize {
		return nil, errors.New("zcashvault: the redeem script is above the 520-byte element limit")
	}
	return b, nil
}
