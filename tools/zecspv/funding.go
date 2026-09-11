package zecspv

import (
	"errors"
	"fmt"
	"math/big"
	"time"
)

var (
	// ErrNoFloor is VerifyFunding or VerifyTip called without a floor.
	ErrNoFloor = errors.New("zecspv: a floor (Policy.MaxTarget) is required")
	// ErrContext is a funding block, or a tip, with fewer headers below it in the proof than the policy needs.
	ErrContext = errors.New("zecspv: too few headers below the block to recompute its difficulty")
	// ErrDepth is a proof with fewer headers above the funding block than the policy's depth.
	ErrDepth = errors.New("zecspv: too few headers above the funding block")
	// ErrStale is a tip older than Policy.MaxTipAge by the caller's clock.
	ErrStale = errors.New("zecspv: the newest header is too old")
)

// FundingProof is what the companion gathers from its node for one transaction.
type FundingProof struct {
	Height  uint32   // height of Headers[0]
	Headers [][]byte // consecutive serialized headers, oldest first
	Block   int      // index in Headers of the block holding the transaction
	TxID    Hash     // the Merkle leaf, internal byte order: SHA-256d of a v1–v4 transaction, the ZIP 244 txid of v5/v6
	Index   uint32   // the transaction's position in the block
	Branch  []Hash   // its Merkle branch, the leaf's sibling first
}

// Policy is the companion's side of the check.
type Policy struct {
	// Depth is how many headers of valid work must be above the funding block. zecswap's confirmations are
	// Depth + 1. VerifyTip does not use it.
	Depth int
	// Below is how many headers the proof must carry below the funding block (or the tip). It is at least
	// Params.Context() (28), so that block's own nBits is recomputed. More is better: every header from the 29th
	// on is held to the difficulty rule, so a forger who fakes the headers under the funding block, to let
	// difficulty fall to the floor, has to mine all of them (docs/ZCASH_HEADER_VERIFICATION.md §4).
	Below int
	// MaxTarget is the floor: the easiest target any header may carry (FloorFromBits). Required. Passing
	// PoWLimit is allowed and means no floor; docs/ZCASH_HEADER_VERIFICATION.md §4 says what that gives up.
	MaxTarget *big.Int
	// Now is the local clock. When set, no header may be more than two hours ahead of it.
	Now time.Time
	// MaxTipAge, with Now, is how old the newest header may be. Zero skips the check.
	MaxTipAge time.Duration
	// Anchor is passed to VerifyHeaders (Options.Anchor): a checkpoint the run must hold with Params.Context()
	// headers below it. With one below the funding block, a forger can fork only above it, from real history.
	Anchor *Checkpoint
}

// Funding is what VerifyFunding proved.
type Funding struct {
	BlockHash     Hash
	BlockHeight   uint32
	TipHash       Hash
	TipHeight     uint32
	Confirmations int      // headers from the funding block to the tip, both included
	Work          *big.Int // summed work of the funding block and every header above it
	MinWork       *big.Int // the least work of any one of those headers
}

// Tip is what VerifyTip proved about the newest header.
type Tip struct {
	Hash   Hash
	Height uint32 // as the caller stated it (VerifyHeaders)
	Time   time.Time
	Work   *big.Int // summed work of the headers whose nBits was recomputed, the tip included
}

// verifyRun is the part VerifyFunding and VerifyTip share: the policy's shape, every header through VerifyHeaders
// with the floor and the clock, enough headers below index block, and a tip no older than MaxTipAge.
func verifyRun(p *Params, height uint32, headers [][]byte, block int, pol Policy) (*Chain, error) {
	if p == nil {
		return nil, errNoNetwork
	}
	if pol.MaxTarget == nil || pol.MaxTarget.Sign() <= 0 {
		return nil, ErrNoFloor
	}
	if pol.Depth < 0 || pol.Below < 0 {
		return nil, errors.New("zecspv: negative depth")
	}
	if below := max(p.Context(), pol.Below); block < below || block >= len(headers) {
		return nil, fmt.Errorf("%w: the block is at index %d; it needs %d headers below it", ErrContext, block, below)
	}
	if above := len(headers) - 1 - block; above < pol.Depth {
		return nil, fmt.Errorf("%w: %d above it, the policy needs %d", ErrDepth, above, pol.Depth)
	}
	c, err := VerifyHeaders(p, height, headers, Options{MaxTarget: pol.MaxTarget, Now: pol.Now, Anchor: pol.Anchor})
	if err != nil {
		return nil, err
	}
	tip := c.Tip()
	if !pol.Now.IsZero() && pol.MaxTipAge > 0 && int64(tip.Time) < pol.Now.Add(-pol.MaxTipAge).Unix() {
		return nil, fmt.Errorf("%w: its time is %s", ErrStale, time.Unix(int64(tip.Time), 0).UTC().Format(time.RFC3339))
	}
	return c, nil
}

// VerifyFunding checks that proof.TxID is in the block proof.Headers[proof.Block], and that at least pol.Depth
// headers follow that block. Every header in the proof passes VerifyHeaders with the policy's floor and clock.
// The funding block has at least max(Params.Context(), pol.Below) headers below it in the proof, so its nBits
// and every nBits above it is recomputed from the headers below, not taken from the node.
//
// It proves work, not that this is the chain the rest of the network follows: see
// docs/ZCASH_HEADER_VERIFICATION.md for what it cannot rule out and what a lie costs.
func VerifyFunding(p *Params, proof *FundingProof, pol Policy) (*Funding, error) {
	c, err := verifyRun(p, proof.Height, proof.Headers, proof.Block, pol)
	if err != nil {
		return nil, err
	}
	block := c.Headers[proof.Block]
	if err := VerifyMerkleBranch(proof.TxID, proof.Index, proof.Branch, block.MerkleRoot); err != nil {
		return nil, fmt.Errorf("zecspv: transaction %s in block %s: %w", proof.TxID, block.hash, err)
	}
	tip := c.Tip()
	f := &Funding{
		BlockHash:     block.hash,
		BlockHeight:   proof.Height + uint32(proof.Block),
		TipHash:       tip.hash,
		TipHeight:     proof.Height + uint32(len(c.Headers)-1),
		Confirmations: len(c.Headers) - proof.Block,
		Work:          c.Work(proof.Block, len(c.Headers)),
	}
	for _, h := range c.Headers[proof.Block:] {
		t, _, _ := CompactToTarget(h.Bits)
		if w := Work(t); f.MinWork == nil || w.Cmp(f.MinWork) < 0 {
			f.MinWork = w
		}
	}
	return f, nil
}

// VerifyTip checks a run of consecutive headers ending at the tip the node reports, the first at height: every
// header passes VerifyHeaders with the policy's floor and clock, the tip has at least max(Params.Context(),
// pol.Below) headers below it so its nBits is recomputed, and it is no older than pol.MaxTipAge. A taker calls it
// before funding, so that the tip her cltv checks read is backed by work and recent. pol.Depth is not used.
func VerifyTip(p *Params, height uint32, headers [][]byte, pol Policy) (*Tip, error) {
	pol.Depth = 0
	c, err := verifyRun(p, height, headers, len(headers)-1, pol)
	if err != nil {
		return nil, err
	}
	tip := c.Tip()
	return &Tip{
		Hash:   tip.hash,
		Height: height + uint32(len(c.Headers)-1),
		Time:   time.Unix(int64(tip.Time), 0).UTC(),
		Work:   c.Work(c.Contextual, len(c.Headers)),
	}, nil
}
