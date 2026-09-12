package xmrswap

import "math"

//go:generate go run gen_profile.go

// ProfileTier is one confirmation tier of the mainnet profile (MT-B16, U7): trades from FromPiconero up wait for
// Confirmations before the gate. A nil FromPiconero is a threshold the operator has not set (MT-J03); until it is,
// every trade is at the lowest tier. T1Max is the most t1 − now a taker accepts at the tier (MT-B32): the tier's quote
// (TierT1) plus the profile's slack, unless the profile raises it for the tier explicitly.
type ProfileTier struct {
	Confirmations uint64
	FromPiconero  *uint64
	T1Max         uint64
}

func profileAmount(v uint64) *uint64 { return &v }

// Tiers are the profile's confirmation tiers, lowest first.
func Tiers() []ProfileTier { return append([]ProfileTier(nil), profileTiers...) }

// TierFor is the tier a trade of amountXmr piconero is at: the highest whose threshold is set and at or below the
// amount, or the lowest when none is.
func TierFor(amountXmr uint64) ProfileTier { return tierFor(profileTiers, amountXmr) }

func tierFor(tiers []ProfileTier, amountXmr uint64) ProfileTier {
	t := tiers[0]
	for _, x := range tiers[1:] {
		if x.FromPiconero != nil && *x.FromPiconero <= amountXmr {
			t = x
		}
	}
	return t
}

// companionWaitBlocks is how many DERO blocks the companion lets pass between the maker's lock and the taker's XMR
// lock: the lock mined (1) and stable (the stable depth), then the maker's stipend mined (1) and stable (the stable
// depth again) — Path 0 has her lock no XMR before it. The study's §2.1 U7 counted only the lock's 1 + 8, before the
// stipend's wait was added.
func companionWaitBlocks() uint64 { return 1 + profileStableBlocks + 1 + profileStableBlocks }

// RequiredT1 is the study's t1 rule (docs/ATOMIC_SWAP_STUDY.md §2.1 U7) in DERO blocks: t1 − lock must cover the
// waits before the taker's XMR lock, the Monero path at c confirmations — the lock mined and c more blocks, (c + 1)
// blocks of 120 s — the gate deadline G, and k standard deviations of that path under an exponential model of Monero
// blocks (σ = √(c + 1) × 120 s). DERO's own block variance is left out, as the study leaves it.
func RequiredT1(confirmations, gate, k uint64) float64 {
	return requiredT1(companionWaitBlocks(), confirmations, gate, k)
}

func requiredT1(waits, confirmations, gate, k uint64) float64 {
	r := float64(profileMoneroBlockSeconds) / float64(profileDeroBlockSeconds)
	n := float64(confirmations + 1)
	return float64(waits) + n*r + float64(gate) + float64(k)*math.Sqrt(n)*r
}

// TierT1 is the t1 − lock a maker quotes at a tier: the default t1, raised to the rule's requirement where the tier
// needs more. The profile test holds every tier's TierT1 at or under T1Max.
func TierT1(confirmations uint64) uint64 {
	need := T1Quote(confirmations, profileGateDeadline)
	if need < profileT1Default {
		return profileT1Default
	}
	return need
}

// T1Quote is the least whole t1 − lock the t1 rule allows at c confirmations and gate deadline gate, at the profile's
// k: RequiredT1, rounded up. Config.withDefaults raises the maker's default width to it, off the simulator.
func T1Quote(confirmations, gate uint64) uint64 {
	return uint64(math.Ceil(RequiredT1(confirmations, gate, profileK)))
}
