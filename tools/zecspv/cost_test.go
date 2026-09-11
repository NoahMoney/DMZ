package zecspv

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"
)

// forgery is the cheapest header run found for a funding the network never saw.
type forgery struct {
	fork int     // index in the proof of the first header the forger mines
	work float64 // their total work, in blocks at the honest difficulty
}

// cheapestForgery prices, in blocks of the honest difficulty, the least work a forger spends on a proof that
// VerifyFunding accepts with Policy{Below: below, Depth: conf-1} and a floor factor times easier than the honest
// target. The honest chain runs at honestBits with blocks exactly one target spacing apart. The forger picks the
// fork point: every header from it on is theirs, the fake funding block and all confirmations included.
//
// Their headers take the latest time the checks allow: the median-time-past plus 90 minutes, and at most two
// hours past the clock, where the clock is when honest miners would have finished the same run. Below index 28
// no header's nBits is recomputed, so there they sit at the floor. From index 28 on nBits is the rule's; when the
// rule, at that time, would be easier than the floor, the model gives the forger the floor and still the late
// time, which is generous to them. A later time never makes a later header harder while the forged run is under
// PoWAveragingWindow + 1 headers long, so for forks near the funding block the greedy times are the forger's best.
//
// forks chooses the fork points tried: "pruned" as argued below, "all" for every one, "anchored" for the fork at
// the funding block only, which is all an Options.Anchor below the funding block leaves the forger.
func cheapestForgery(t testing.TB, p *Params, honestBits, height uint32, below, conf int, factor int64, forks string) forgery {
	honest := mustTarget(t, honestBits)
	honestWork := Work(honest)
	floor := new(big.Int).Mul(honest, big.NewInt(factor))
	if floor.Cmp(p.PowLimit) > 0 {
		floor.Set(p.PowLimit)
	}
	floorBits := TargetToCompact(floor) // rounds down: the easiest nBits at or under the floor
	n := below + conf
	spacing := p.TargetSpacing(height)
	const t0 = 1_700_000_000
	clock := int64(t0) + int64(n)*spacing + int64(maxFutureLocal/time.Second)
	best := forgery{work: math.Inf(1)}
	// Forks at index 28 or above start from an all-honest window; from there the forged run is the same sequence
	// whatever the fork, and a later fork mines fewer of it, so among them the fork at the funding block is the
	// cheapest. Only the forks below 28, whose first headers escape the rule, need trying besides it.
	tried := []int{below}
	switch forks {
	case "pruned":
		for f := 0; f < min(below, p.Context()); f++ {
			tried = append(tried, f)
		}
	case "all":
		for f := 0; f < below; f++ {
			tried = append(tried, f)
		}
	case "anchored":
	default:
		t.Fatalf("forks %q", forks)
	}
	for _, f := range tried {
		hs := make([]*Header, n)
		for i := 0; i < f; i++ {
			hs[i] = &Header{Time: uint32(t0 + int64(i)*spacing), Bits: honestBits}
		}
		total := new(big.Rat)
		for i := f; i < n; i++ {
			latest := clock
			if i >= p.MedianBlockSpan {
				mtp, _ := p.MedianTimePast(hs[:i])
				latest = min(latest, mtp+p.MaxTimeAfterMedian)
			}
			bits := floorBits
			if i >= p.Context() {
				rule, err := p.NextBits(height-uint32(below)+uint32(i), hs[i-p.Context():i], uint32(latest))
				if err != nil {
					t.Fatal(err)
				}
				if mustTarget(t, rule).Cmp(floor) <= 0 {
					bits = rule
				}
			}
			hs[i] = &Header{Time: uint32(latest), Bits: bits}
			total.Add(total, new(big.Rat).SetFrac(Work(mustTarget(t, bits)), honestWork))
		}
		if w, _ := total.Float64(); w < best.work {
			best = forgery{fork: f, work: w}
		}
	}
	return best
}

// TestForgeryCost is the table in docs/ZCASH_HEADER_VERIFICATION.md §4: what the cheapest accepted forgery costs
// on Mainnet, by confirmations, headers below the funding block, and floor factor. Honest difficulty is block
// 3436171's (the Zebra 6.3.0 checkpoint in testdata/mainnet-3436131.json).
func TestForgeryCost(t *testing.T) {
	const honestBits, height = 0x1c00802e, 3436200
	var b strings.Builder
	fmt.Fprintf(&b, "\nconfirmations  below  factor  forger's work (honest blocks)  fork index  (of which below the funding)\n")
	for _, conf := range []int{1, 3, 6, 10, 20} {
		for _, below := range []int{28, 50, 100, 200} {
			for _, factor := range []int64{1, 2, 4, 8, 16} {
				got := cheapestForgery(t, Mainnet, honestBits, height, below, conf, factor, "pruned")
				if below <= 50 {
					// the pruning argument, checked where the full search is cheap
					if all := cheapestForgery(t, Mainnet, honestBits, height, below, conf, factor, "all"); math.Abs(all.work-got.work) > 1e-9 {
						t.Errorf("conf %d below %d factor %d: pruned %.4f, every fork %.4f at %d", conf, below, factor, got.work, all.work, all.fork)
					}
				}
				fmt.Fprintf(&b, "%13d  %5d  %6d  %29.2f  %10d  %d\n", conf, below, factor, got.work, got.fork, below-got.fork)
				// every forged header is at least floor work, and the forger mines at least conf of them; and
				// the forgery is never dearer than conf honest blocks, since forking at the funding block from
				// an honest window is always open (the rule's floor division and ToCompact round its target
				// down a little, hence the slack)
				if got.work < float64(conf)/float64(factor)-1e-9 || got.work > float64(conf)*1.001 {
					t.Errorf("conf %d below %d factor %d: %.3f outside [conf/factor, conf]", conf, below, factor, got.work)
				}
				if factor == 1 && got.work < float64(conf)-1e-9 {
					t.Errorf("with the floor at the honest target a forgery cannot be cheaper than conf blocks")
				}
			}
		}
	}
	// Anchored: a checkpoint below the funding block fixes every header under it, so the forger forks from real
	// history and no floor is needed (factor 2^40, capped at PoWLimit)
	fmt.Fprintf(&b, "\nanchored, no floor: confirmations → forger's work (honest blocks)\n")
	for _, conf := range []int{1, 3, 6, 10, 20, 40} {
		got := cheapestForgery(t, Mainnet, honestBits, height, 28, conf, 1<<40, "anchored")
		fmt.Fprintf(&b, "%13d  %.2f\n", conf, got.work)
		if got.work < 0.5*float64(conf) {
			t.Errorf("anchored, conf %d: %.3f", conf, got.work)
		}
	}
	t.Log(b.String())
}
