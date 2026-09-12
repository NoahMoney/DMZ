package xmrswap

// The review of 2026-09-12, finding 6: only T1Max followed the trade's confirmation tier. The maker's Confirmations and
// T1Delta stayed 20 and 420 at every tier, and nothing checked the study's t1 rule (RequiredT1) at runtime. Each test here
// sets a tier's threshold, as the operator's answer to MT-J03 will.

import (
	"strings"
	"testing"
)

// withTiers runs the rest of the test with these confirmation tiers in place of the profile's.
func withTiers(t *testing.T, tiers []ProfileTier) {
	t.Helper()
	saved := profileTiers
	t.Cleanup(func() { profileTiers = saved })
	profileTiers = tiers
}

// fortyFrom is the profile's tiers with the 40-confirmation tier's threshold set at from piconero.
func fortyFrom(t *testing.T, from uint64) []ProfileTier {
	t.Helper()
	tiers := Tiers()
	set := false
	for i := range tiers {
		if tiers[i].Confirmations == 40 {
			tiers[i].FromPiconero, set = profileAmount(from), true
		}
	}
	if !set {
		t.Fatal("the profile has no 40-confirmation tier")
	}
	return tiers
}

// TestTheTierDrivesTheConfirmationsAndTheT1: with the 40-confirmation tier's threshold set, a trade at or above it
// defaults to 40 confirmations, to the t1 the rule needs there (477 blocks) and to that tier's ceiling (537); a trade
// below it to the lowest tier's 20, 420 and 480. What a caller sets stands. On the simulator, whose blocks are neither
// DERO's nor Monero's, the width is the floor's default, and the depth is still the tier's.
func TestTheTierDrivesTheConfirmationsAndTheT1(t *testing.T) {
	withTiers(t, fortyFrom(t, testXmr))
	small := Config{AmountXmr: testXmr - 1, DeroNet: "testnet"}.withDefaults()
	large := Config{AmountXmr: testXmr, DeroNet: "testnet"}.withDefaults()
	if small.Confirmations != 20 || small.T1Delta != 420 || small.T1Max != 480 {
		t.Fatalf("below the threshold: %d confirmations, t1 %d, T1Max %d; want 20, 420, 480", small.Confirmations, small.T1Delta, small.T1Max)
	}
	if large.Confirmations != 40 || large.T1Delta != TierT1(40) || large.T1Delta != 477 || large.T1Max != 537 {
		t.Fatalf("at the threshold: %d confirmations, t1 %d, T1Max %d; want 40, 477, 537", large.Confirmations, large.T1Delta, large.T1Max)
	}
	if need := RequiredT1(large.Confirmations, large.GateDeadline, profileK); float64(large.T1Delta) < need || large.T1Delta > large.T1Max {
		t.Fatalf("the default t1 %d does not sit in [the rule's %.1f, T1Max %d]", large.T1Delta, need, large.T1Max)
	}
	if c := (Config{AmountXmr: testXmr, DeroNet: "testnet", Confirmations: 30, T1Delta: 500}).withDefaults(); c.Confirmations != 30 || c.T1Delta != 500 {
		t.Fatalf("a caller's own values became %d confirmations and t1 %d", c.Confirmations, c.T1Delta)
	}
	if c := (Config{AmountXmr: testXmr, DeroNet: "simulator", T1Min: 10}).withDefaults(); c.Confirmations != 40 || c.T1Delta != 30 {
		t.Fatalf("on the simulator: %d confirmations and t1 %d, want 40 and the floor's default 30", c.Confirmations, c.T1Delta)
	}
	// a gate deadline raised above the profile's raises the quote with it
	if c := (Config{AmountXmr: testXmr - 1, DeroNet: "testnet", GateDeadline: 200}).withDefaults(); float64(c.T1Delta) < RequiredT1(20, 200, profileK) {
		t.Fatalf("with G 200 the default t1 is %d, below the rule's %.1f", c.T1Delta, RequiredT1(20, 200, profileK))
	}
}

// tierConfig is a run's config for the profile's windows on the test network: the policy floors and G as mainnet has
// them, and the t1 width and depth left to the tier unless set.
func tierConfig(c *Config) {
	c.DeroNet = "testnet"
	c.T1Min, c.T2Min, c.T2Delta, c.GateDeadline, c.GateMargin, c.T1Max, c.T2Max = DefaultT1Min, DefaultT2Min, DefaultT2Min, DefaultGateDeadline, 0, 0, 0
	c.Confirmations, c.T1Delta = 0, 0
}

// TestAWidthThatBreaksTheT1RuleIsRefused: off the simulator, a maker whose t1 width does not carry the rule at his own
// confirmations is refused when his companion starts; one who leaves the width to the default, or who typed the depth
// his width does carry, is not. On the simulator the rule, which counts real block times, is not applied.
func TestAWidthThatBreaksTheT1RuleIsRefused(t *testing.T) {
	withTiers(t, fortyFrom(t, testXmr))
	r := newRun(t, plan{scenario: "happy", seed: 601})
	bob := func(mod func(*Config)) error {
		cfg := r.config("bob")
		tierConfig(&cfg)
		mod(&cfg)
		_, err := NewBob(cfg, NewState(r.statePath("bob"), "bob", testHTLC), newMemDero(r.chain, bobAddr), r.bobW, r.prover, nil)
		return err
	}
	if err := bob(func(c *Config) { c.T1Delta = 420 }); err == nil || !strings.Contains(err.Error(), "t1 rule") || !strings.Contains(err.Error(), "477") {
		t.Fatalf("a width of 420 at 40 confirmations: %v", err)
	}
	if err := bob(func(c *Config) {}); err != nil {
		t.Fatalf("the default width at 40 confirmations: %v", err)
	}
	if err := bob(func(c *Config) { c.T1Delta, c.Confirmations = 420, 20 }); err != nil {
		t.Fatalf("a width of 420 at 20 confirmations, typed: %v", err)
	}
	if err := bob(func(c *Config) { c.T1Delta, c.DeroNet = 420, "simulator" }); err != nil {
		t.Fatalf("on the simulator: %v", err)
	}
}

// TestTheTakerRefusesAnOfferTheT1RuleDoesNotCover: at the 40-confirmation tier the taker holds the maker's offer to the
// rule at 40 confirmations: a maker who typed the lowest tier's depth and quotes its 420 blocks is refused before
// anything of hers is said, and one who quotes the tier's 477 is taken.
func TestTheTakerRefusesAnOfferTheT1RuleDoesNotCover(t *testing.T) {
	withTiers(t, fortyFrom(t, testXmr))
	for _, c := range []struct {
		name          string
		t1, confs     uint64
		phase, reason string
	}{{"420 blocks at 20 confirmations", 420, 20, "refused", "t1 rule"}, {"the tier's quote", 0, 0, "offer_checked", ""}} {
		r := newRun(t, plan{scenario: "happy", seed: 602})
		r.bob = startWith(r, "bob", func(cfg *Config) { tierConfig(cfg); cfg.T1Delta, cfg.Confirmations = c.t1, c.confs })
		r.alice = startWith(r, "alice", tierConfig)
		for i := 0; i < 20; i++ {
			if p, _ := r.phase("alice"); p == c.phase {
				break
			}
			r.step("bob")
			r.step("alice")
		}
		alice := r.loadState("alice")
		if alice.Phase != c.phase || !strings.Contains(alice.Outcome, c.reason) {
			t.Fatalf("%s: alice %s (%s), want %s", c.name, alice.Phase, alice.Outcome, c.phase)
		}
		t.Logf("%s: alice %s %s", c.name, alice.Phase, alice.Outcome)
	}
}
