package zecspv

import (
	"errors"
	"fmt"
	"math/big"
	"time"
)

var (
	// ErrVersion is a header with nVersion below 4 (§7.6).
	ErrVersion = errors.New("zecspv: block version below 4")
	// ErrLink is a header whose hashPrevBlock is not the hash of the header before it.
	ErrLink = errors.New("zecspv: header does not link to the one before it")
	// ErrDifficulty is a header whose nBits is not ThresholdBits for its height (§7.6, §7.7.3).
	ErrDifficulty = errors.New("zecspv: nBits is not the difficulty adjustment's")
	// ErrTimeTooOld is a header whose nTime is not after the median-time-past (§7.6).
	ErrTimeTooOld = errors.New("zecspv: nTime not after the median-time-past")
	// ErrTimeTooFar is a header whose nTime is more than 90 minutes past the median-time-past (§7.6).
	ErrTimeTooFar = errors.New("zecspv: nTime too far past the median-time-past")
	// ErrFuture is a header more than two hours ahead of the caller's clock (§7.6's rule for full validators).
	ErrFuture = errors.New("zecspv: nTime more than two hours ahead of the local clock")
	// ErrAnchor is a run that does not hold Options.Anchor where it must, or holds another header there.
	ErrAnchor = errors.New("zecspv: the pinned checkpoint is not in the run where it must be")
)

// Checkpoint pins a header by height and hash, as the checkpoint lists Zebra ships do.
type Checkpoint struct {
	Height uint32
	Hash   Hash
}

// maxFutureLocal is how far ahead of the local clock a header may be (§7.6: "two hours").
const maxFutureLocal = 2 * time.Hour

// HeaderError says which header failed, and wraps the reason.
type HeaderError struct {
	Height uint32
	Err    error
}

func (e *HeaderError) Error() string {
	return fmt.Sprintf("zecspv: header at height %d: %v", e.Height, e.Err)
}
func (e *HeaderError) Unwrap() error { return e.Err }

// Options are the caller's policy for VerifyHeaders.
type Options struct {
	// MaxTarget is the floor: the easiest target any header may carry. nil means PoWLimit, which is no floor at
	// all; see docs/ZCASH_HEADER_VERIFICATION.md §4 before leaving it nil.
	MaxTarget *big.Int
	// Now is the local clock. When set, no header may be more than two hours ahead of it.
	Now time.Time
	// Anchor, when set, must be in the run with at least Params.Context() headers below it, and the header at its
	// height must have its hash. The hash fixes every header below it through the hashPrevBlock links, so from the
	// anchor up every nBits is recomputed from the network's own history rather than from headers a node chose.
	Anchor *Checkpoint
}

// CheckHeader applies the checks that need no other header: version, nBits in range and under the floor, the
// difficulty filter, and the Equihash solution. The cheap checks run first.
func (p *Params) CheckHeader(h *Header, maxTarget *big.Int) error {
	if p == nil {
		return errNoNetwork
	}
	if h.Version < 4 {
		return ErrVersion
	}
	target, err := p.Target(h.Bits)
	if err != nil {
		return err
	}
	if maxTarget != nil && target.Cmp(maxTarget) > 0 {
		return fmt.Errorf("%w: nBits %08x", ErrBelowFloor, h.Bits)
	}
	if h.hash.value().Cmp(target) > 0 {
		return fmt.Errorf("%w: %s over nBits %08x", ErrAboveTarget, h.hash, h.Bits)
	}
	pow := h.powHeader()
	return VerifyEquihash(p.EquihashN, p.EquihashK, pow[:108], pow[108:], h.Solution)
}

// Chain is a run of consecutive headers that VerifyHeaders accepted.
type Chain struct {
	Params  *Params
	Height  uint32 // the height of Headers[0], as the caller stated it
	Headers []*Header
	// Contextual is the index of the first header whose nBits was recomputed from the headers below it
	// (Params.Context(), 28). Headers before it were checked alone and for their link, and their time against
	// the median-time-past once 11 headers were below them.
	Contextual int
}

// Tip is the last header.
func (c *Chain) Tip() *Header { return c.Headers[len(c.Headers)-1] }

// Work is the summed work of Headers[from:to] (§7.7.5).
func (c *Chain) Work(from, to int) *big.Int {
	w := new(big.Int)
	for _, h := range c.Headers[from:to] {
		t, _, _ := CompactToTarget(h.Bits)
		w.Add(w, Work(t))
	}
	return w
}

// VerifyHeaders checks raw, consecutive serialized headers oldest first, the first at the given height.
//
// Every header: parsed exactly, version ≥ 4, nBits in range and no easier than opt.MaxTarget, hash at or below
// its target, Equihash valid, linked to the one before it, and (with opt.Now) no more than two hours ahead.
// Every header with 11 below it: nTime after their median, and no more than 90 minutes past it from
// MaxTimeHeight. Every header with 28 below it: nBits equal to ThresholdBits for its height.
//
// The height is the caller's claim, used to pick the rules (Blossom's spacing, Testnet's minimum-difficulty
// rule); the headers themselves do not prove it.
func VerifyHeaders(p *Params, height uint32, raw [][]byte, opt Options) (*Chain, error) {
	if p == nil {
		return nil, errNoNetwork
	}
	if len(raw) == 0 {
		return nil, errors.New("zecspv: no headers")
	}
	if uint64(height)+uint64(len(raw)) > 1<<32 {
		return nil, errors.New("zecspv: heights overflow")
	}
	c := &Chain{Params: p, Height: height, Headers: make([]*Header, 0, len(raw)), Contextual: min(len(raw), p.Context())}
	anchor := -1
	if a := opt.Anchor; a != nil {
		anchor = int(int64(a.Height) - int64(height))
		if anchor < p.Context() || anchor >= len(raw) {
			return nil, fmt.Errorf("%w: checkpoint %d needs %d headers below it in a run of heights %d..%d", ErrAnchor,
				a.Height, p.Context(), height, uint64(height)+uint64(len(raw))-1)
		}
	}
	for i, b := range raw {
		hh := height + uint32(i)
		fail := func(err error) (*Chain, error) { return nil, &HeaderError{Height: hh, Err: err} }
		h, err := ParseHeader(b)
		if err != nil {
			return fail(err)
		}
		if i > 0 && h.PrevBlock != c.Headers[i-1].hash {
			return fail(fmt.Errorf("%w: its parent is %s, the header below is %s", ErrLink, h.PrevBlock, c.Headers[i-1].hash))
		}
		if !opt.Now.IsZero() && int64(h.Time) > opt.Now.Add(maxFutureLocal).Unix() {
			return fail(fmt.Errorf("%w: %d", ErrFuture, h.Time))
		}
		if i >= p.MedianBlockSpan {
			mtp, _ := p.MedianTimePast(c.Headers[:i])
			if int64(h.Time) <= mtp {
				return fail(fmt.Errorf("%w: %d ≤ %d", ErrTimeTooOld, h.Time, mtp))
			}
			if hh >= p.MaxTimeHeight && int64(h.Time) > mtp+p.MaxTimeAfterMedian {
				return fail(fmt.Errorf("%w: %d > %d + %d", ErrTimeTooFar, h.Time, mtp, p.MaxTimeAfterMedian))
			}
		}
		if i >= p.Context() {
			want, err := p.NextBits(hh, c.Headers[i-p.Context():i], h.Time)
			if err != nil {
				return fail(err)
			}
			if h.Bits != want {
				return fail(fmt.Errorf("%w: %08x, the rule gives %08x", ErrDifficulty, h.Bits, want))
			}
		}
		if err := p.CheckHeader(h, opt.MaxTarget); err != nil {
			return fail(err)
		}
		if i == anchor && h.hash != opt.Anchor.Hash {
			return fail(fmt.Errorf("%w: the run has %s, the checkpoint %s", ErrAnchor, h.hash, opt.Anchor.Hash))
		}
		c.Headers = append(c.Headers, h)
	}
	return c, nil
}
