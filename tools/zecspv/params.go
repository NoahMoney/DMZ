package zecspv

import (
	"errors"
	"fmt"
	"math/big"
	"slices"
)

// Params are one network's proof-of-work rules (§5.3, §7.6, §7.7).
type Params struct {
	Name    string
	Genesis Hash // the block at height 0, so a caller can tell which network a node serves

	PowLimit             *big.Int // the easiest target any block may have
	EquihashN, EquihashK uint32

	AveragingWindow int   // PoWAveragingWindow: 17
	MedianBlockSpan int   // PoWMedianBlockSpan: 11
	MaxAdjustDown   int64 // PoWMaxAdjustDown, in percent: 32
	MaxAdjustUp     int64 // PoWMaxAdjustUp, in percent: 16
	DampingFactor   int64 // PoWDampingFactor: 4

	PreBlossomSpacing  int64 // seconds: 150
	PostBlossomSpacing int64 // seconds: 75
	BlossomHeight      uint32

	// MinDifficultyHeight is the first height where a block whose nTime is more than MinDifficultyGap target
	// spacings after its parent's may have nBits = ToCompact(PoWLimit) (Testnet from 299188: ZIP 205, ZIP 208).
	// Zero means the rule does not apply (Mainnet).
	MinDifficultyHeight uint32
	MinDifficultyGap    int64

	// MaxTimeHeight is the first height where nTime must be at most the median-time-past plus MaxTimeAfterMedian
	// seconds (§7.6: height 2 on Mainnet, 653606 on Testnet).
	MaxTimeHeight      uint32
	MaxTimeAfterMedian int64
}

func mustHash(s string) Hash {
	h, err := ParseHash(s)
	if err != nil {
		panic(err)
	}
	return h
}

func powLimit(bits uint) *big.Int {
	one := big.NewInt(1)
	return new(big.Int).Sub(new(big.Int).Lsh(one, bits), one)
}

// Mainnet and Testnet carry the constants of §5.3 and the heights of §7.6 and §7.7.3, which agree with zcashd's
// chainparams.cpp and Zebra 6.3.0's network_upgrade.rs. The genesis hashes are the height-0 lines of Zebra
// 6.3.0's main-checkpoints.txt and test-checkpoints.txt.
var (
	Mainnet = &Params{
		Name:                "mainnet",
		Genesis:             mustHash("00040fe8ec8471911baa1db1266ea15dd06b4a8a5c453883c000b031973dce08"),
		PowLimit:            powLimit(243),
		EquihashN:           200,
		EquihashK:           9,
		AveragingWindow:     17,
		MedianBlockSpan:     11,
		MaxAdjustDown:       32,
		MaxAdjustUp:         16,
		DampingFactor:       4,
		PreBlossomSpacing:   150,
		PostBlossomSpacing:  75,
		BlossomHeight:       653600,
		MaxTimeHeight:       2,
		MaxTimeAfterMedian:  90 * 60,
		MinDifficultyHeight: 0,
	}
	Testnet = &Params{
		Name:                "testnet",
		Genesis:             mustHash("05a60a92d99d85997cce3b87616c089f6124d7342af37106edc76126334a2c38"),
		PowLimit:            powLimit(251),
		EquihashN:           200,
		EquihashK:           9,
		AveragingWindow:     17,
		MedianBlockSpan:     11,
		MaxAdjustDown:       32,
		MaxAdjustUp:         16,
		DampingFactor:       4,
		PreBlossomSpacing:   150,
		PostBlossomSpacing:  75,
		BlossomHeight:       584000,
		MinDifficultyHeight: 299188,
		MinDifficultyGap:    6,
		MaxTimeHeight:       653606,
		MaxTimeAfterMedian:  90 * 60,
	}
)

// Context is the number of headers below a block that its nBits and nTime rules read:
// PoWAveragingWindow + PoWMedianBlockSpan, 28 on both networks.
func (p *Params) Context() int { return p.AveragingWindow + p.MedianBlockSpan }

// TargetSpacing is PoWTargetSpacing(height) (§7.7.3): 150 s before Blossom, 75 s from it.
func (p *Params) TargetSpacing(height uint32) int64 {
	if height >= p.BlossomHeight {
		return p.PostBlossomSpacing
	}
	return p.PreBlossomSpacing
}

// medianTime is median(S) = sorted(S)[ceiling((length(S)+1)/2)], one-based (§7.7.3).
func medianTime(headers []*Header) int64 {
	t := make([]int64, len(headers))
	for i, h := range headers {
		t[i] = int64(h.Time)
	}
	slices.Sort(t)
	return t[len(t)/2]
}

// MedianTimePast is the median of the nTime fields of the last PoWMedianBlockSpan headers of prev, the headers
// below a block, oldest first (§7.6).
func (p *Params) MedianTimePast(prev []*Header) (int64, error) {
	if len(prev) < p.MedianBlockSpan {
		return 0, fmt.Errorf("zecspv: the median-time-past needs %d headers below the block, got %d", p.MedianBlockSpan, len(prev))
	}
	return medianTime(prev[len(prev)-p.MedianBlockSpan:]), nil
}

// NextBits is ThresholdBits(height) (§7.7.3) for a block at height whose nTime is candidateTime, with Testnet's
// minimum-difficulty rule (ZIP 205, ZIP 208). prev are the headers at heights height−Context() .. height−1, oldest
// first. It is zcashd's GetNextWorkRequired and CalculateNextWorkRequired (pow.cpp), which Zebra 6.3.0's
// AdjustedDifficulty matches.
func (p *Params) NextBits(height uint32, prev []*Header, candidateTime uint32) (uint32, error) {
	n := p.Context()
	if len(prev) != n || int64(height) < int64(n) {
		return 0, fmt.Errorf("zecspv: ThresholdBits(%d) needs the %d headers below it", height, n)
	}
	limit := TargetToCompact(p.PowLimit)
	if p.MinDifficultyHeight != 0 && height >= p.MinDifficultyHeight &&
		int64(candidateTime) > int64(prev[n-1].Time)+p.MinDifficultyGap*p.TargetSpacing(height) {
		return limit, nil
	}
	// MeanTarget: the mean of the last PoWAveragingWindow targets. zcashd floors it here, before dividing by
	// AveragingWindowTimespan; the spec divides first and floors once, and the two are equal (pow.cpp's note)
	sum := new(big.Int)
	for _, h := range prev[n-p.AveragingWindow:] {
		t, negative, overflow := CompactToTarget(h.Bits)
		if negative || overflow {
			return 0, fmt.Errorf("%w: %08x below height %d", ErrBits, h.Bits, height)
		}
		sum.Add(sum, t)
	}
	mean := sum.Quo(sum, big.NewInt(int64(p.AveragingWindow)))
	// ActualTimespan = MedianTime(height) − MedianTime(height − PoWAveragingWindow): the medians of the 11 times
	// below the block, and of the 11 below the block PoWAveragingWindow lower
	return p.calculateNextBits(mean, medianTime(prev[n-p.MedianBlockSpan:]), medianTime(prev[:p.MedianBlockSpan]), height), nil
}

// calculateNextBits is zcashd's CalculateNextWorkRequired: Threshold from MeanTarget (already floored) and the two
// median times, then ToCompact. zcashd's pow_tests call it directly, and so do this package's.
func (p *Params) calculateNextBits(mean *big.Int, lastMedian, firstMedian int64, height uint32) uint32 {
	window := int64(p.AveragingWindow) * p.TargetSpacing(height) // AveragingWindowTimespan
	minSpan := window * (100 - p.MaxAdjustUp) / 100              // floor, both operands positive
	maxSpan := window * (100 + p.MaxAdjustDown) / 100
	// damped by PoWDampingFactor with truncation toward zero, as the spec's trunc and C++ and Go division do
	span := window + (lastMedian-firstMedian-window)/p.DampingFactor
	span = max(minSpan, min(maxSpan, span))

	next := new(big.Int).Quo(mean, big.NewInt(window))
	next.Mul(next, big.NewInt(span))
	if next.Cmp(p.PowLimit) > 0 {
		next.Set(p.PowLimit)
	}
	return TargetToCompact(next)
}

// errNoNetwork guards against a nil Params.
var errNoNetwork = errors.New("zecspv: no network parameters")
