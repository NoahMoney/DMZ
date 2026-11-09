package monerovault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	rpc "veil/tools/simrpc"
	"veil/tools/xmrswap"
)

// Config is one signer's Monero backend configuration. Every signer must use the same Network and finality policy,
// and a wallet that holds the same multisig address (or, for a pilot, the same single-key wallet).
type Config struct {
	Network        string `json:"network"`   // mainnet, stagenet or testnet
	WalletRPC      string `json:"walletRpc"` // host:port or URL of monero-wallet-rpc
	Address        string `json:"address"`   // the vault's primary Monero address every signer must hold
	Account        uint64 `json:"account,omitempty"`
	FinalityDepth  int64  `json:"finalityDepth,omitempty"` // default and minimum 10: Monero's spendable age
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	Retries        int    `json:"retries,omitempty"`      // default 4
	RetryDelayMS   int    `json:"retryDelayMs,omitempty"` // default 500
}

func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("monerovault: %s: %w", path, err)
	}
	return c, nil
}

// Deposit is one confirmed output paying a vault subaddress. Vout is the subaddress minor index, not a raw output
// index: the contract only uses (txid, vout) as an identity, and the wallet exposes the subaddress but not the
// output position. Two outputs to one subaddress in one transaction are summed into that one deposit; two depositors
// cannot be told apart by this wallet API, and inventing a second identity would let one payment be minted twice.
type Deposit struct {
	TxID   string
	Vout   uint64
	Index  uint64
	Amount uint64
	Height int64
}

type Backend struct {
	c       *Client
	net     xmrswap.Network
	acct    uint64
	depth   int64
	vault   string
	pub     xmrswap.ParsedAddress
	logf    func(string, ...interface{})
	timeout time.Duration

	mu    sync.Mutex
	addrs map[uint64]string
}

func New(cfg Config, vault string, logf func(string, ...interface{})) (*Backend, error) {
	if vault == "" {
		return nil, errors.New("monerovault: the vault id is required (the payment id is derived from it)")
	}
	vault = strings.ToLower(vault)
	net, err := xmrswap.ParseNetwork(cfg.Network)
	if err != nil {
		return nil, fmt.Errorf("monerovault: %w", err)
	}
	if cfg.WalletRPC == "" {
		return nil, errors.New("monerovault: walletRpc is required")
	}
	if cfg.Address == "" {
		return nil, errors.New("monerovault: address is required: every signer must name the wallet it is a share of")
	}
	pa, err := xmrswap.ParseAddress(cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("monerovault: configured address: %w", err)
	}
	if pa.Net != net {
		return nil, fmt.Errorf("monerovault: configured address is on %s, not %s", pa.Net, net)
	}
	if pa.Subaddress {
		return nil, errors.New("monerovault: address must be the wallet's primary address, not a subaddress")
	}
	if cfg.FinalityDepth < 0 || cfg.TimeoutSeconds < 0 {
		return nil, errors.New("monerovault: finalityDepth and timeoutSeconds cannot be negative")
	}
	if cfg.FinalityDepth == 0 {
		cfg.FinalityDepth = 10
	}
	if cfg.FinalityDepth < 10 {
		return nil, fmt.Errorf("monerovault: a finality depth of %d on %s; Monero's spendable age is 10 blocks and a shallower policy is not final", cfg.FinalityDepth, net)
	}
	if cfg.Retries == 0 {
		cfg.Retries = 4
	}
	if cfg.TimeoutSeconds == 0 {
		cfg.TimeoutSeconds = 300
	}
	user, pass, err := LoginFromEnv()
	if err != nil {
		return nil, err
	}
	c, err := NewClient(cfg.WalletRPC, user, pass, cfg.Retries, time.Duration(cfg.RetryDelayMS)*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	b := &Backend{c: c, net: net, acct: cfg.Account, depth: cfg.FinalityDepth, vault: vault, pub: pa, logf: logf,
		timeout: time.Duration(cfg.TimeoutSeconds) * time.Second, addrs: map[uint64]string{}}
	if !rpc.IsLoopback(cfg.WalletRPC) {
		b.logf("monero wallet-rpc %s is on another machine: its RPC login and every query travel in the clear unless a TLS proxy fronts it", cfg.WalletRPC)
	}
	return b, nil
}

func (b *Backend) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), b.timeout)
}

func (b *Backend) Summary() string {
	return fmt.Sprintf("Monero %s; wallet-rpc %s; account %d; finality %d blocks", b.net, redactURL(b.c.url), b.acct, b.depth)
}

func redactURL(s string) string {
	if i := strings.Index(s, "@"); i >= 0 {
		if j := strings.Index(s, "://"); j >= 0 {
			return s[:j+3] + s[i+1:]
		}
	}
	return s
}

func (b *Backend) Height() (int64, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	h, err := b.c.Height(ctx)
	if err != nil {
		return 0, err
	}
	if h > 1<<62 {
		return 0, errors.New("monerovault: the wallet reported an impossible height")
	}
	return int64(h), nil
}

func (b *Backend) Finalized() (int64, error) {
	h, err := b.Height()
	if err != nil {
		return 0, err
	}
	if h <= b.depth {
		return 0, fmt.Errorf("monerovault: the wallet is at height %d, not deeper than the finality policy of %d", h, b.depth)
	}
	return h - b.depth, nil
}

func (b *Backend) Health() error {
	ctx, cancel := b.ctx()
	defer cancel()
	if _, err := b.c.Version(ctx); err != nil {
		return err
	}
	if _, err := b.c.Height(ctx); err != nil {
		return err
	}
	if _, err := b.c.Balance(ctx, b.acct); err != nil {
		return err
	}
	addr, err := b.c.Address(ctx, b.acct, 0)
	if err != nil {
		return err
	}
	pa, err := xmrswap.ParseAddress(addr)
	if err != nil {
		return fmt.Errorf("monerovault: the wallet's primary address: %w", err)
	}
	if pa.Net != b.net {
		return fmt.Errorf("monerovault: the wallet is on %s, but this vault is on %s", pa.Net, b.net)
	}
	if pa.Spend != b.pub.Spend || pa.View != b.pub.View {
		return errors.New("monerovault: the wallet's primary address is not the configured vault address")
	}
	return nil
}

func (b *Backend) Address(index uint64) (string, error) {
	if index > 1<<32-1 {
		return "", fmt.Errorf("monerovault: index %d is outside the subaddress range", index)
	}
	b.mu.Lock()
	if a := b.addrs[index]; a != "" {
		b.mu.Unlock()
		return a, nil
	}
	b.mu.Unlock()
	ctx, cancel := b.ctx()
	defer cancel()
	addr, err := b.c.Address(ctx, b.acct, index)
	if err != nil {
		if index == 0 {
			return "", fmt.Errorf("monerovault: primary address: %w", err)
		}
		// Not existing yet is the normal first call. Create the next subaddress and then read the index asked for:
		// a wallet that was already ahead may have created a different one.
		created, got, cerr := b.c.CreateAddress(ctx, b.acct, "dmz-vault-"+strconv.FormatUint(index, 10))
		if cerr != nil {
			return "", fmt.Errorf("monerovault: subaddress %d: %v; create: %w", index, err, cerr)
		}
		addr = created
		if got == 0 && index > 0 { // an older wallet-rpc may omit address_index: ask for the one we need
			if a, e := b.c.Address(ctx, b.acct, index); e == nil {
				addr, got = a, index
			}
		}
		if got == 0 {
			return "", errors.New("monerovault: create_address returned neither an address index nor a readable address")
		}
		for got < index {
			if got > 1<<32-2 {
				return "", fmt.Errorf("monerovault: the wallet is at subaddress %d and cannot reach %d", got, index)
			}
			created, got, cerr = b.c.CreateAddress(ctx, b.acct, "dmz-vault-"+strconv.FormatUint(got+1, 10))
			if cerr != nil {
				return "", fmt.Errorf("monerovault: creating subaddress %d: %w", got+1, cerr)
			}
			addr = created
		}
		if got != index {
			addr, cerr = b.c.Address(ctx, b.acct, index)
			if cerr != nil {
				return "", fmt.Errorf("monerovault: create_address returned index %d, and reading index %d failed: %w", got, index, cerr)
			}
		}
	}
	pa, err := xmrswap.ParseAddress(addr)
	if err != nil {
		return "", fmt.Errorf("monerovault: subaddress %d: %w", index, err)
	}
	if pa.Net != b.net {
		return "", fmt.Errorf("monerovault: subaddress %d is on %s, but this vault is on %s", index, pa.Net, b.net)
	}
	if index == 0 && pa.Subaddress {
		return "", errors.New("monerovault: index 0 returned a subaddress, not the wallet's primary address")
	}
	if index > 0 && !pa.Subaddress {
		return "", fmt.Errorf("monerovault: index %d returned a standard address, not a subaddress", index)
	}
	b.mu.Lock()
	b.addrs[index] = addr
	b.mu.Unlock()
	return addr, nil
}

func (b *Backend) ValidDest(dest string) bool {
	pa, err := xmrswap.ParseAddress(dest)
	return err == nil && pa.Net == b.net
}

func (b *Backend) Deposits() ([]Deposit, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	ts, err := b.c.Incoming(ctx, b.acct)
	if err != nil {
		return nil, err
	}
	if len(ts) > 10000 {
		return nil, fmt.Errorf("monerovault: the wallet reports %d incoming transfers; this bounded backend will not scan them", len(ts))
	}
	type key struct {
		tx    string
		index uint64
	}
	sum := map[key]uint64{}
	height := map[key]int64{}
	for _, t := range ts {
		tx := strings.ToLower(t.ID())
		if len(tx) != 64 || t.Amount == 0 || t.Height == 0 || t.Locked || t.UnlockTime != 0 {
			continue
		}
		sub := t.subaddr()
		if sub.Major != b.acct {
			continue
		}
		if t.Height > 1<<62 {
			return nil, fmt.Errorf("monerovault: the wallet reported an impossible transfer height %d", t.Height)
		}
		k := key{tx, sub.Minor}
		if sum[k]+t.Amount < sum[k] {
			return nil, errors.New("monerovault: a deposit sum overflowed")
		}
		sum[k] += t.Amount
		if height[k] == 0 || int64(t.Height) < height[k] {
			height[k] = int64(t.Height)
		}
	}
	out := make([]Deposit, 0, len(sum))
	for k, amount := range sum {
		out = append(out, Deposit{TxID: k.tx, Vout: k.index, Index: k.index, Amount: amount, Height: height[k]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Height != out[j].Height {
			return out[i].Height < out[j].Height
		}
		if out[i].TxID != out[j].TxID {
			return out[i].TxID < out[j].TxID
		}
		return out[i].Vout < out[j].Vout
	})
	return out, nil
}

func (b *Backend) Verify(d Deposit) (bool, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	ts, err := b.c.Incoming(ctx, b.acct)
	if err != nil {
		return false, err
	}
	var sum uint64
	var at int64
	for _, t := range ts {
		sub := t.subaddr()
		if !strings.EqualFold(t.ID(), d.TxID) || sub.Major != b.acct || sub.Minor != d.Vout {
			continue
		}
		if t.Height > 1<<62 {
			return false, fmt.Errorf("monerovault: the wallet reported an impossible transfer height %d", t.Height)
		}
		if t.Locked || t.UnlockTime != 0 || t.Height == 0 {
			return false, nil
		}
		if sum+t.Amount < sum {
			return false, errors.New("monerovault: a deposit sum overflowed")
		}
		sum += t.Amount
		if at == 0 || int64(t.Height) < at {
			at = int64(t.Height)
		}
	}
	return sum == d.Amount && at == d.Height, nil
}

func (b *Backend) Reserve() (uint64, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	return b.c.Balance(ctx, b.acct)
}

// paymentID is the on-chain idempotency key for withdrawal j: 8 bytes derived from the vault id and j. It is visible
// on Monero, as a payment id always is; it reveals no destination and no amount.
func (b *Backend) paymentID(j uint64) string {
	sum := sha256.Sum256([]byte("dmz:vault:" + b.vault + "|" + strconv.FormatUint(j, 10)))
	return hex.EncodeToString(sum[:8])
}

func (b *Backend) Release(j uint64, dest string, amount uint64) (string, error) {
	if !b.ValidDest(dest) {
		return "", fmt.Errorf("monerovault: %q is not a Monero address on %s", dest, b.net)
	}
	if amount == 0 {
		return "", errors.New("monerovault: a withdrawal of 0")
	}
	pmt := b.paymentID(j)
	ctx, cancel := b.ctx()
	defer cancel()
	if txid, err := b.lookupPayment(ctx, pmt); err != nil {
		return "", err
	} else if txid != "" {
		return txid, nil
	}
	txid, txset, err := b.c.Transfer(ctx, b.acct, dest, amount, pmt)
	if err != nil {
		return "", err
	}
	if txset != "" {
		return "", ErrNeedsSignatures
	}
	// A view-only wallet can answer with a hash it never sent. Ask the wallet's own outgoing list before calling
	// this a payment; if it is not visible yet, the next pass finds it by payment id and returns the same hash.
	if seen, err := b.lookupPayment(ctx, pmt); err != nil {
		return txid, err
	} else if seen == "" {
		return txid, fmt.Errorf("monerovault: the wallet answered %s and lists no outgoing payment with that id: it may not have been sent", txid)
	} else if !strings.EqualFold(seen, txid) {
		return txid, fmt.Errorf("monerovault: the wallet lists %s for withdrawal %d, not the %s it just returned", seen, j, txid)
	}
	return txid, nil
}

func (b *Backend) lookupPayment(ctx context.Context, pmt string) (string, error) {
	ts, err := b.c.Outgoing(ctx, b.acct)
	if err != nil {
		return "", err
	}
	var found string
	for _, t := range ts {
		if !strings.EqualFold(t.PaymentID, pmt) {
			continue
		}
		id := strings.ToLower(t.ID())
		if len(id) != 64 {
			continue
		}
		if found != "" && found != id {
			return "", fmt.Errorf("monerovault: two outgoing payments carry withdrawal id %s (%s, %s); operator must reconcile", pmt, found, id)
		}
		found = id
	}
	return found, nil
}

func (b *Backend) VerifyRelease(j uint64, ftx string, dest string, amount uint64) (bool, error) {
	if !b.ValidDest(dest) || amount == 0 {
		return false, nil
	}
	ctx, cancel := b.ctx()
	defer cancel()
	t, known, err := b.c.TransferByTxID(ctx, ftx, b.acct)
	if err != nil || !known {
		return false, err
	}
	if t.Type != "" && t.Type != "out" {
		return false, nil
	}
	if !strings.EqualFold(t.PaymentID, b.paymentID(j)) {
		return false, nil
	}
	if t.Height == 0 {
		return false, nil
	}
	if t.Height > 1<<62 {
		return false, fmt.Errorf("monerovault: the wallet reported an impossible transaction height %d", t.Height)
	}
	final, err := b.Finalized()
	if err != nil {
		return false, err
	}
	if int64(t.Height) > final {
		return false, fmt.Errorf("monerovault: %s is mined at %d and final at %d (finalized height now %d)", ftx, t.Height, int64(t.Height)+b.depth, final)
	}
	if len(t.Destinations) != 1 {
		return false, fmt.Errorf("monerovault: the wallet lists %d destinations for %s, not the one payment this backend builds", len(t.Destinations), ftx)
	}
	d := t.Destinations[0]
	return d.Address == dest && d.Amount == amount, nil
}
