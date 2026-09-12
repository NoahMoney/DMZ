package zecswap

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"veil/tools/zecspv"
)

// The header gate: -verify-headers, tools/zecspv, docs/ZCASH_HEADER_VERIFICATION.md §5.
//
// Without it the companion believes its Zcash nodes about work: a node that shows a funding in a block nobody mined,
// or a tip higher than the network's, is believed. With it:
//   - before the maker reveals s (makerWatchFunding, and again in sendClaim), the funding's block and every header
//     above it to the tip must carry real proof of work, under the floor and the difficulty rule, with HeaderBelow
//     headers beneath so that the funding block's own difficulty is recomputed rather than taken from the node, and
//     the funding must be in that block by its Merkle branch;
//   - before the taker funds (takerFund), the headers up to the tip her cltv checks read must do the same and be
//     recent, and those checks read the verified tip.
//
// It proves work, not that this is the chain the rest of the network follows (§2 of that document), and a colluding
// miner can still pay for a forgery (§4: ten confirmations cost about 8.94 blocks of network work at these
// settings). It is off by default until it has run against the Zcash testnet node.
//
// REGTEST: zebrad's regtest mines with no proof of work (a zero nonce, an all-zero solution and a 2011 timestamp in
// every header, measured) and zecspv has no regtest parameters, so on regtest the gate is SKIPPED, and says so each
// time it would have run. TESTNET: the minimum-difficulty rule lets anyone mine a Testnet block nearly free, so there
// the floor is PoWLimit and the gate checks only the plumbing — that the node's headers parse, link, obey the rules,
// are recent and hold the funding — never that a forgery would be costly. Only on mainnet does a lie cost work.

const (
	// MainnetFloorBits is Mainnet block 3436171's nBits, the last Zebra 6.3.0 checkpoint (§4); the floor is
	// MainnetFloorFactor times easier. Refresh the pin with each release: a stale pin loosens the floor as the
	// network's difficulty grows.
	MainnetFloorBits   = 0x1c00802e
	MainnetFloorFactor = 16
	// HeaderBelow is how many headers below the funding block (or the tip) the proof carries. With 200, faking ten
	// confirmations costs 8.94 blocks of network work; with the bare 28 the package needs, 4.49 (§4's table).
	HeaderBelow = 200
	// HeaderMaxTipAge is how old the newest header may be by the local clock.
	HeaderMaxTipAge = time.Hour
)

// HeaderCheck configures the gate; a nil *HeaderCheck in Config is the gate off.
type HeaderCheck struct {
	Below     int
	MaxTipAge time.Duration
	Now       func() time.Time
	// Params and Floor override the network's (tests with recorded headers). nil takes zecspv.Mainnet with
	// MainnetFloorBits × MainnetFloorFactor on mainnet, and zecspv.Testnet with PoWLimit on testnet.
	Params *zecspv.Params
	Floor  *big.Int
}

// DefaultHeaderCheck is the gate as -verify-headers sets it.
func DefaultHeaderCheck() *HeaderCheck {
	return &HeaderCheck{Below: HeaderBelow, MaxTipAge: HeaderMaxTipAge, Now: time.Now}
}

// HeaderSource is what the gate reads from a Zcash node; ZcashRPC is one.
type HeaderSource interface {
	BlockHash(ctx context.Context, height uint32) (string, error)
	BlockHeader(ctx context.Context, hash string) ([]byte, error)
	BlockTxIDs(ctx context.Context, hash string) ([]string, error)
}

// HeaderGateNote is the line the command prints at start about the gate on a network.
func HeaderGateNote(network string) string {
	switch network {
	case "regtest":
		return "HEADER VERIFICATION IS SKIPPED ON REGTEST: zebrad's regtest mines with no proof of work, so there is nothing to verify; -verify-headers checks nothing here"
	case "testnet":
		return "header verification on testnet checks the plumbing only: Testnet's minimum-difficulty rule makes forged headers nearly free, so a lie costs a forger almost nothing"
	}
	return fmt.Sprintf("header verification on: %d headers below the funding (or the tip), floor nBits %08x × %d, newest header at most %s old; it proves work, not that this is the network's chain (docs/ZCASH_HEADER_VERIFICATION.md §2, §4)", HeaderBelow, MainnetFloorBits, MainnetFloorFactor, HeaderMaxTipAge)
}

// setup picks the network's parameters and floor; plumbing is true on testnet.
func (hc *HeaderCheck) setup(net Network) (params *zecspv.Params, floor *big.Int, plumbing bool, err error) {
	params = hc.Params
	if params == nil {
		params = zecspv.Testnet
		if net == Mainnet {
			params = zecspv.Mainnet
		}
	}
	floor = hc.Floor
	if floor == nil {
		if params == zecspv.Mainnet {
			if floor, err = params.FloorFromBits(MainnetFloorBits, MainnetFloorFactor); err != nil {
				return nil, nil, false, err
			}
		} else {
			floor = params.PowLimit
		}
	}
	return params, floor, params == zecspv.Testnet, nil
}

func (hc *HeaderCheck) policy(depth int, floor *big.Int) zecspv.Policy {
	now := time.Now
	if hc.Now != nil {
		now = hc.Now
	}
	return zecspv.Policy{Depth: depth, Below: hc.Below, MaxTarget: floor, Now: now(), MaxTipAge: hc.MaxTipAge}
}

// maxHeaderRun bounds one read: 200 below, the confirmations above, and slack.
const maxHeaderRun = 5_000

// GatherHeaders reads the serialized headers from height first to last, both included, each checked to hash to the
// block the node names at its height.
func GatherHeaders(ctx context.Context, src HeaderSource, first, last uint32) ([][]byte, []string, error) {
	if last < first || last-first >= maxHeaderRun {
		return nil, nil, fmt.Errorf("zecswap: a header run from %d to %d", first, last)
	}
	var raw [][]byte
	var hashes []string
	for h := first; ; h++ {
		bh, err := src.BlockHash(ctx, h)
		if err != nil {
			return nil, nil, err
		}
		hb, err := src.BlockHeader(ctx, bh)
		if err != nil {
			return nil, nil, err
		}
		hdr, err := zecspv.ParseHeader(hb)
		if err != nil {
			return nil, nil, fmt.Errorf("zecswap: the header at %d: %w", h, err)
		}
		if got := hdr.Hash().String(); got != bh {
			return nil, nil, fmt.Errorf("zecswap: the node names block %s at %d and serves a header that hashes to %s", bh, h, got)
		}
		raw, hashes = append(raw, hb), append(hashes, bh)
		if h == last {
			return raw, hashes, nil
		}
	}
}

// GatherFundingProof reads from the node everything VerifyFunding needs for the transaction leaf in the block at
// height, seen there as blockHash: the headers from height − below to tip, and the block's transaction list for the
// Merkle branch. Nothing it reads is trusted — the headers are what VerifyFunding checks, and a wrong transaction
// list does not hash to the header's root.
func GatherFundingProof(ctx context.Context, src HeaderSource, height, tip uint32, below int, leaf zecspv.Hash, blockHash string) (*zecspv.FundingProof, error) {
	if below < 0 || uint32(below) > height {
		return nil, fmt.Errorf("zecswap: the chain below height %d is shorter than the %d headers the header check needs", height, below)
	}
	if tip < height {
		return nil, fmt.Errorf("%w: the tip %d is below the funding's height %d", ErrReorged, tip, height)
	}
	first := height - uint32(below)
	raw, hashes, err := GatherHeaders(ctx, src, first, tip)
	if err != nil {
		return nil, err
	}
	if hashes[below] != blockHash {
		return nil, fmt.Errorf("%w: the block at %d is now %s, not %s", ErrReorged, height, hashes[below], blockHash)
	}
	ids, err := src.BlockTxIDs(ctx, blockHash)
	if err != nil {
		return nil, err
	}
	leaves := make([]zecspv.Hash, len(ids))
	at := -1
	for i, id := range ids {
		if leaves[i], err = zecspv.ParseHash(id); err != nil {
			return nil, err
		}
		if leaves[i] == leaf {
			at = i
		}
	}
	if at < 0 {
		return nil, fmt.Errorf("zecswap: block %s does not list the transaction %s", blockHash, leaf)
	}
	branch, err := zecspv.MerkleBranch(leaves, at)
	if err != nil {
		return nil, err
	}
	return &zecspv.FundingProof{Height: first, Headers: raw, Block: below, TxID: leaf, Index: uint32(at), Branch: branch}, nil
}

// headerSource is the primary node as a HeaderSource.
func (s *Swap) headerSource() (HeaderSource, error) {
	src, ok := s.zc.(HeaderSource)
	if !ok {
		return nil, errors.New("zecswap: this Zcash node cannot serve headers, so the header check cannot run")
	}
	return src, nil
}

// headerGateFunding is the maker's gate before the secret leaves. It returns nil when the funding's headers verify,
// and when the gate is off or skipped on regtest (which it says).
func (s *Swap) headerGateFunding(ctx context.Context, ci ChainInfo, f Funding) error {
	hc := s.cfg.Headers
	if hc == nil {
		return nil
	}
	if ci.Regtest {
		s.sayOnce("spvskip", "HEADER VERIFICATION SKIPPED: this is regtest, whose zebrad mines with no proof of work; nothing about this funding is verified by work")
		return nil
	}
	key := fmt.Sprintf("%s|%s|%d", f.TxID, f.BlockHash, ci.Height)
	if s.spvOK == key {
		return nil
	}
	src, err := s.headerSource()
	if err != nil {
		return err
	}
	params, floor, plumbing, err := hc.setup(ci.Net)
	if err != nil {
		return err
	}
	// the leaf is computed from the funding's own bytes, never taken from the node's txid string
	rt, err := s.zc.RawTx(ctx, f.TxID)
	if err != nil {
		return err
	}
	id, ok := ComputeTxID(rt.Raw)
	if !ok || id != f.TxID {
		return fmt.Errorf("zecswap: the funding's Merkle leaf cannot be computed from its bytes (a v5 or v6 transaction with shielded parts has no leaf here yet)")
	}
	leaf, err := zecspv.ParseHash(id)
	if err != nil {
		return err
	}
	proof, err := GatherFundingProof(ctx, src, f.Height, ci.Height, hc.Below, leaf, f.BlockHash)
	if err != nil {
		return fmt.Errorf("zecswap: reading the funding's headers: %w", err)
	}
	vf, err := zecspv.VerifyFunding(params, proof, hc.policy(int(s.cfg.Policy.ConfZec)-1, floor))
	if err != nil {
		return fmt.Errorf("zecswap: the funding's headers do not verify on %s: %w", params.Name, err)
	}
	note := ""
	if plumbing {
		note = " — TESTNET: plumbing only, since the minimum-difficulty rule makes such headers nearly free to forge"
	}
	s.logf("headers verify the funding %s in block %s at %d: %d confirmations, work %s (least per block %s), %d headers checked%s", f.TxID, vf.BlockHash, vf.BlockHeight, vf.Confirmations, vf.Work, vf.MinWork, len(proof.Headers), note)
	s.spvOK = key
	return nil
}

// headerGateTip is the taker's gate before she funds: the tip her cltv checks read must be backed by work. It returns
// the tip to use for them — the verified one, or the cautious tip as before when the gate is off or skipped.
func (s *Swap) headerGateTip(ctx context.Context, ci ChainInfo, cautious uint32) (uint32, error) {
	hc := s.cfg.Headers
	if hc == nil {
		return cautious, nil
	}
	if ci.Regtest {
		s.sayOnce("spvskip", "HEADER VERIFICATION SKIPPED: this is regtest, whose zebrad mines with no proof of work; the tip is not verified by work")
		return cautious, nil
	}
	src, err := s.headerSource()
	if err != nil {
		return 0, err
	}
	params, floor, plumbing, err := hc.setup(ci.Net)
	if err != nil {
		return 0, err
	}
	if hc.Below < 0 || uint32(hc.Below) > ci.Height {
		return 0, fmt.Errorf("zecswap: the chain is shorter than the %d headers the header check needs", hc.Below)
	}
	first := ci.Height - uint32(hc.Below)
	raw, _, err := GatherHeaders(ctx, src, first, ci.Height)
	if err != nil {
		return 0, fmt.Errorf("zecswap: reading the headers up to the tip %d: %w", ci.Height, err)
	}
	t, err := zecspv.VerifyTip(params, first, raw, hc.policy(0, floor))
	if err != nil {
		return 0, fmt.Errorf("zecswap: the headers up to the tip %d do not verify on %s: %w; not funding", ci.Height, params.Name, err)
	}
	if cautious > t.Height+2 {
		return 0, fmt.Errorf("zecswap: a Zcash node reports the tip at %d, %d above the %d this node's headers prove; not funding against a tip no one has shown the work for", cautious, cautious-t.Height, t.Height)
	}
	note := ""
	if plumbing {
		note = " — TESTNET: plumbing only"
	}
	s.onceLogf("headers verify the tip %d (%s, %s): work %s over %d headers%s", t.Height, t.Hash, t.Time.Format(time.RFC3339), t.Work, len(raw), note)
	return t.Height, nil
}
