package xmrswap

// MT-B16 (BL-27): the mainnet profile is one file, the companion's defaults are it, and the t1 rule holds at every tier.

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

type profileTierFile struct {
	Confirmations uint64  `json:"confirmations"`
	FromPiconero  *uint64 `json:"from_piconero"`
	T1Max         uint64  `json:"t1_max"`
	// T1MaxRaised is the reason a tier's t1_max is above its quote + t1_slack (MT-B32); "" for a tier at quote + slack
	T1MaxRaised string `json:"t1_max_raised"`
}

type profileFile struct {
	DeroBlockSeconds   uint64            `json:"dero_block_seconds"`
	MoneroBlockSeconds uint64            `json:"monero_block_seconds"`
	StableBlocks       uint64            `json:"stable_blocks"`
	ClaimMargin        uint64            `json:"claim_margin"`
	T1Min              uint64            `json:"t1_min"`
	T1Default          uint64            `json:"t1_default"`
	T1Slack            uint64            `json:"t1_slack"`
	T2Min              uint64            `json:"t2_min"`
	T2MinusT1          uint64            `json:"t2_minus_t1"`
	T2Max              uint64            `json:"t2_max"`
	GateDeadline       uint64            `json:"gate_deadline"`
	K                  uint64            `json:"k"`
	StipendFloor       uint64            `json:"stipend_floor"`
	ConfirmationsFloor uint64            `json:"confirmations_floor"`
	W1Min              uint64            `json:"w1min"`
	W2Min              uint64            `json:"w2min"`
	G2                 uint64            `json:"g2"`
	Tiers              []profileTierFile `json:"confirmation_tiers"`
	Notes              map[string]string `json:"notes"`
	Pending            map[string]string `json:"pending"`
}

// profileKeys are the only top-level keys the file may hold: a key the companion does not read (a stale ceiling, a
// misspelt value) would otherwise sit there looking like policy.
var profileKeys = map[string]bool{"_doc": true, "dero_block_seconds": true, "monero_block_seconds": true, "stable_blocks": true,
	"claim_margin": true, "t1_min": true, "t1_default": true, "t1_slack": true, "t2_min": true, "t2_minus_t1": true, "t2_max": true,
	"gate_deadline": true, "k": true, "stipend_floor": true, "confirmations_floor": true, "w1min": true, "w2min": true, "g2": true,
	"confirmation_tiers": true, "notes": true, "pending": true}

func readProfile(t *testing.T) profileFile {
	t.Helper()
	raw, err := os.ReadFile("mainnet_profile.json")
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for k := range keys {
		if !profileKeys[k] {
			t.Fatalf("mainnet_profile.json has %q, which nothing reads", k)
		}
	}
	var p profileFile
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Tiers) == 0 {
		t.Fatal("mainnet_profile.json has no confirmation tiers")
	}
	return p
}

// TestTheProfileIsTheCompanionsDefaults: every policy value the companion runs with is the file's — through the
// generated constants, which must be the file's (go generate ./xmrswap otherwise) — and the values the companion
// derives rather than decides (DERO's stable depth, the fee-sized stipend) agree with what the file states.
func TestTheProfileIsTheCompanionsDefaults(t *testing.T) {
	p := readProfile(t)
	gen := []struct {
		name      string
		file, got uint64
	}{
		{"dero_block_seconds", p.DeroBlockSeconds, profileDeroBlockSeconds}, {"monero_block_seconds", p.MoneroBlockSeconds, profileMoneroBlockSeconds},
		{"stable_blocks", p.StableBlocks, profileStableBlocks}, {"claim_margin", p.ClaimMargin, profileClaimMargin},
		{"t1_min", p.T1Min, profileT1Min}, {"t1_default", p.T1Default, profileT1Default}, {"t1_slack", p.T1Slack, profileT1Slack},
		{"the lowest tier's t1_max", p.Tiers[0].T1Max, profileT1Max},
		{"t2_min", p.T2Min, profileT2Min}, {"t2_minus_t1", p.T2MinusT1, profileT2MinusT1}, {"t2_max", p.T2Max, profileT2Max},
		{"gate_deadline", p.GateDeadline, profileGateDeadline}, {"k", p.K, profileK}, {"stipend_floor", p.StipendFloor, profileStipendFloor},
		{"confirmations_floor", p.ConfirmationsFloor, profileConfirmationsFloor},
		{"w1min", p.W1Min, profileW1Min}, {"w2min", p.W2Min, profileW2Min}, {"g2", p.G2, profileG2},
	}
	for _, g := range gen {
		if g.file != g.got {
			t.Fatalf("%s: the file says %d, profile_gen.go %d: run `go generate ./xmrswap` from tools/", g.name, g.file, g.got)
		}
	}
	if len(p.Tiers) != len(profileTiers) {
		t.Fatalf("the file has %d tiers, profile_gen.go %d: run go generate", len(p.Tiers), len(profileTiers))
	}
	for i, ft := range p.Tiers {
		gt := profileTiers[i]
		sameFrom := (ft.FromPiconero == nil) == (gt.FromPiconero == nil) && (ft.FromPiconero == nil || *ft.FromPiconero == *gt.FromPiconero)
		if ft.Confirmations != gt.Confirmations || ft.T1Max != gt.T1Max || !sameFrom {
			t.Fatalf("tier %d: the file says %+v, profile_gen.go %+v: run go generate", i, ft, gt)
		}
	}
	cfg := Config{}.withDefaults()
	for _, c := range []struct {
		name      string
		want, got uint64
	}{
		{"T1Min", p.T1Min, DefaultT1Min}, {"T2Min", p.T2Min, DefaultT2Min}, {"T1Max (the lowest tier's)", p.Tiers[0].T1Max, DefaultT1Max},
		{"T1Max, defaulted for a trade at the lowest tier", p.Tiers[0].T1Max, cfg.T1Max}, {"T2Max", p.T2Max, DefaultT2Max},
		{"the default t1 − lock", p.T1Default, cfg.T1Delta}, {"the default t2 − t1", p.T2MinusT1, cfg.T2Delta},
		{"Confirmations", p.ConfirmationsFloor, DefaultConfirmations}, {"GateDeadline (G)", p.GateDeadline, DefaultGateDeadline},
		{"ClaimMargin", p.ClaimMargin, DefaultClaimMargin}, {"StableBlocks (DERO's STABLE_LIMIT)", p.StableBlocks, DefaultStableBlocks},
		{"the stipend floor", p.StipendFloor, DefaultStipend},
		{"G2Min (the taker's floor on the install's g2)", p.G2, DefaultG2Min}, {"G2Min, defaulted", p.G2, cfg.G2Min},
	} {
		if c.want != c.got {
			t.Fatalf("%s: the profile says %d, the companion runs with %d", c.name, c.want, c.got)
		}
	}
	switch {
	case p.T1Default < p.T1Min || p.T1Default > p.Tiers[0].T1Max:
		t.Fatalf("the default t1 %d is outside [%d, %d]", p.T1Default, p.T1Min, p.Tiers[0].T1Max)
	case p.T2MinusT1 < p.T2Min || p.T2MinusT1 > p.T2Max:
		t.Fatalf("the default t2 − t1 %d is outside [%d, %d]", p.T2MinusT1, p.T2Min, p.T2Max)
	case p.ConfirmationsFloor < 10:
		t.Fatalf("a confirmation floor of %d is below Monero's 10-block spendable age", p.ConfirmationsFloor)
	case p.GateDeadline < p.StableBlocks+p.ClaimMargin:
		t.Fatalf("G %d leaves a gate on time no room to claim before the cutoff (%d)", p.GateDeadline, p.StableBlocks+p.ClaimMargin)
	}
	// X-19: the notes are the operator's answers as they stand (2026-09-11; MT-J04's re-confirmation and G2 on
	// 2026-09-12). A decision still open is in pending with its MT-J number, and has no note.
	for key, j := range map[string]string{"confirmations_floor": "MT-J03", "confirmation_tiers": "MT-J03", "t2_max": "MT-J05"} {
		if !strings.Contains(p.Pending[key], j) || p.Notes[key] != "" {
			t.Fatalf("%s is an operator decision still open (%s), and the file does not say so: pending %q, notes %q", key, j, p.Pending[key], p.Notes[key])
		}
	}
	// A decision answered is in notes with its number and the answer's date, and not in pending.
	for key, j := range map[string]string{"t1_max": "MT-J05", "t2_minus_t1": "MT-J05", "gate_deadline": "MT-J04", "g2": "MT-J13"} {
		if p.Pending[key] != "" || !strings.Contains(p.Notes[key], j) || !strings.Contains(p.Notes[key], "answered 2026-09-1") {
			t.Fatalf("%s was answered (%s): pending %q, notes %q", key, j, p.Pending[key], p.Notes[key])
		}
	}
	// MT-J04 was re-confirmed at 100 and MT-J13 answered 4,800, both on 2026-09-12: the file and the answers agree
	if p.GateDeadline != 100 || !strings.Contains(p.Notes["gate_deadline"], "answered 2026-09-12: 100") ||
		p.G2 != 4800 || !strings.Contains(p.Notes["g2"], "answered 2026-09-12: 4,800") {
		t.Fatalf("G %d and g2 %d, with notes %q and %q, are not the answers of 2026-09-12 (100 and 4,800)", p.GateDeadline, p.G2, p.Notes["gate_deadline"], p.Notes["g2"])
	}
}

// TestEveryTierBuildsALockTheInstallAccepts (MT-A03(a), MT-J13): with the profile's own install arguments, the maker's
// Lock is one htlc.bas v3 accepts at every tier. The contract measures t1 − w1min at the height the Lock executes,
// which is up to DeroTxValidity blocks after it was built, so the t1 a maker quotes must be at least
// max(t1_min, w1min + 11); the default t2 − t1 (and the floor under it) must be at least w2min; the install never
// demands a wider t1 than the policy's own floor; and g2 leaves the taker a height at which a Punish can go out and be
// stable before t2 + g2.
func TestEveryTierBuildsALockTheInstallAccepts(t *testing.T) {
	p := readProfile(t)
	floor := p.T1Min
	if f := p.W1Min + DeroTxValidity; f > floor {
		floor = f
	}
	if p.T1Default < floor {
		t.Fatalf("the default t1 %d is below max(t1_min %d, w1min %d + %d) = %d", p.T1Default, p.T1Min, p.W1Min, DeroTxValidity, floor)
	}
	for _, tier := range Tiers() {
		if q := TierT1(tier.Confirmations); q < floor || q > tier.T1Max {
			t.Fatalf("%d confirmations: the maker quotes t1 %d, outside [%d, the tier's T1Max %d]", tier.Confirmations, q, floor, tier.T1Max)
		}
	}
	if p.T2MinusT1 < p.W2Min || p.T2Min < p.W2Min {
		t.Fatalf("t2 − t1 %d (floor %d) is below w2min %d: the contract would refuse the Lock", p.T2MinusT1, p.T2Min, p.W2Min)
	}
	if p.W1Min > p.T1Min {
		t.Fatalf("w1min %d is above t1_min %d: the install would refuse locks the policy allows", p.W1Min, p.T1Min)
	}
	if punishCutoff(0, p.G2, p.ClaimMargin) == 0 {
		t.Fatalf("g2 %d leaves no height to Punish at (it needs more than %d + %d + %d)", p.G2, DeroTxValidity, p.StableBlocks, p.ClaimMargin)
	}
	t.Logf("the maker's t1 floor at this install: max(t1_min %d, w1min %d + %d) = %d, under the default %d; the taker sends Punish from t2 to t2 + %d, g2 %d blocks (%.1f h)",
		p.T1Min, p.W1Min, DeroTxValidity, floor, p.T1Default, punishCutoff(0, p.G2, p.ClaimMargin)-1, p.G2, float64(p.G2*p.DeroBlockSeconds)/3600)
	// MT-J13's candidate values (docs/HTLC_V3_COMPANION_NOTES.md §7): each leaves room to Punish
	for _, g2 := range []uint64{400, 2400, 4800, 9600} {
		if punishCutoff(0, g2, p.ClaimMargin) == 0 {
			t.Fatalf("g2 = %d leaves no height to Punish at", g2)
		}
	}
}

// TestEveryTierMeetsTheT1Rule: at every confirmation tier, the t1 a maker quotes covers the study's rule — the waits
// before the taker's XMR lock, the expected Monero path at the tier, G and k·σ — and stays at or under the tier's
// T1Max. The rule with the study's own count of waits (the lock only, 9 blocks) reproduces its ≈310, ≈390 and ≈467.
func TestEveryTierMeetsTheT1Rule(t *testing.T) {
	study := map[uint64]float64{20: 310, 30: 390, 40: 467}
	prev := uint64(0)
	for _, tier := range Tiers() {
		c := tier.Confirmations
		if c < profileConfirmationsFloor || c <= prev {
			t.Fatalf("tier %d: below the floor %d, or not above the tier before it", c, profileConfirmationsFloor)
		}
		prev = c
		need := RequiredT1(c, profileGateDeadline, profileK)
		t1 := TierT1(c)
		if float64(t1) < need || t1 > tier.T1Max {
			t.Fatalf("%d confirmations: the rule needs %.1f blocks, the maker would quote %d, the tier's T1Max is %d", c, need, t1, tier.T1Max)
		}
		if s, ok := study[c]; ok {
			if got := requiredT1(1+profileStableBlocks, c, profileGateDeadline, profileK); math.Abs(got-s) > 1.5 {
				t.Fatalf("%d confirmations: the study's own count gives %.1f, not its ≈%.0f", c, got, s)
			}
		}
		t.Logf("%d confirmations: the rule needs %.1f blocks (the study, without the stipend's wait: %.1f); t1 quoted %d; T1Max %d", c, need,
			requiredT1(1+profileStableBlocks, c, profileGateDeadline, profileK), t1, tier.T1Max)
	}
}

// TestEveryTierT1MaxIsItsQuotePlusTheSlack (MT-B32, the operator's answer to MT-J05 ceilings (b)): T1Max comes down from
// 1,200 for every trade to each tier's quote plus the profile's slack. At every tier it covers the t1 rule's need and
// the maker's quote. It is exactly quote + slack unless the tier gives a reason for raising it. The reason the notes
// give for the slack holds: a maker quoting at k + 1 standard deviations still fits.
func TestEveryTierT1MaxIsItsQuotePlusTheSlack(t *testing.T) {
	p := readProfile(t)
	for _, tier := range p.Tiers {
		c := tier.Confirmations
		need, quote, wider := RequiredT1(c, p.GateDeadline, p.K), TierT1(c), RequiredT1(c, p.GateDeadline, p.K+1)
		switch {
		case float64(tier.T1Max) < need || tier.T1Max < quote:
			t.Fatalf("%d confirmations: t1_max %d is below the rule's need %.1f or the maker's quote %d", c, tier.T1Max, need, quote)
		case tier.T1MaxRaised == "" && tier.T1Max != quote+p.T1Slack:
			t.Fatalf("%d confirmations: t1_max %d is not the quote %d + t1_slack %d, and the tier gives no t1_max_raised reason", c, tier.T1Max, quote, p.T1Slack)
		case tier.T1MaxRaised != "" && tier.T1Max <= quote+p.T1Slack:
			t.Fatalf("%d confirmations: t1_max_raised is given, but t1_max %d is not above quote + slack %d", c, tier.T1Max, quote+p.T1Slack)
		case float64(tier.T1Max) < wider:
			t.Fatalf("%d confirmations: a maker quoting at k = %d needs %.1f, above t1_max %d: t1_slack %d no longer covers what the notes say", c, p.K+1, wider, tier.T1Max, p.T1Slack)
		case tier.T1Max >= 1200:
			t.Fatalf("%d confirmations: t1_max %d has not come down from 1,200", c, tier.T1Max)
		}
		t.Logf("%d confirmations: the rule needs %.1f, at k + 1 %.1f; the maker quotes %d; t1_max %d (%d min of slack)", c, need, wider, quote, tier.T1Max, (tier.T1Max-quote)*p.DeroBlockSeconds/60)
	}
	if !strings.Contains(p.Notes["t1_max"], "t1_slack") {
		t.Fatalf("the notes do not say why t1_slack is %d: %q", p.T1Slack, p.Notes["t1_max"])
	}
}

// TestATradeIsAtItsTier (MT-B32): a trade's T1Max is its tier's, the highest tier whose threshold is set and at or below
// the amount. While the operator has set no threshold above the lowest tier (MT-J03), every trade is at the lowest.
func TestATradeIsAtItsTier(t *testing.T) {
	unset := true
	for _, tier := range profileTiers[1:] {
		if tier.FromPiconero != nil {
			unset = false
		}
	}
	for _, amount := range []uint64{0, 1, testXmr, 1 << 62} {
		if got := (Config{AmountXmr: amount}).withDefaults().T1Max; got != TierFor(amount).T1Max {
			t.Fatalf("%d piconero: defaulted T1Max %d, its tier's %d", amount, got, TierFor(amount).T1Max)
		}
		if unset && TierFor(amount).Confirmations != profileTiers[0].Confirmations {
			t.Fatalf("%d piconero is at the %d tier while no threshold is set", amount, TierFor(amount).Confirmations)
		}
	}
	if got := (Config{T1Max: 700}).withDefaults().T1Max; got != 700 {
		t.Fatalf("a T1Max the caller set became %d", got)
	}
	tiers := []ProfileTier{{Confirmations: 20, FromPiconero: profileAmount(0), T1Max: 480}, {Confirmations: 30, FromPiconero: profileAmount(100), T1Max: 490},
		{Confirmations: 40, T1Max: 537}, {Confirmations: 50, FromPiconero: profileAmount(1000), T1Max: 600}}
	for amount, want := range map[uint64]uint64{0: 20, 99: 20, 100: 30, 999: 30, 1000: 50, 1 << 62: 50} {
		if got := tierFor(tiers, amount); got.Confirmations != want {
			t.Fatalf("%d piconero: the %d tier, want %d (a tier with no threshold is never chosen)", amount, got.Confirmations, want)
		}
	}
}

// TestWhatEachPendingAnswerChanges: each operator decision still open, answered, in numbers — so the answer is a
// one-line edit to mainnet_profile.json whose consequences are already known.
func TestWhatEachPendingAnswerChanges(t *testing.T) {
	tiers := []uint64{10, 20, 30, 40}
	at := func(g uint64) map[uint64]float64 {
		m := map[uint64]float64{}
		for _, c := range tiers {
			m[c] = RequiredT1(c, g, profileK)
		}
		return m
	}
	g100, g30 := at(100), at(30)
	// MT-J04: G = 30, as the operator answered (re-confirmation asked). Every tier then fits the default t1 of 420; a
	// suppressed Claim costs the maker 30 blocks (9 min) instead of 100 (30 min).
	for _, c := range tiers {
		if g30[c] >= float64(profileT1Default) {
			t.Fatalf("G = 30: %d confirmations would need %.1f, above the default t1", c, g30[c])
		}
	}
	// MT-J03 (b): a 10-confirmation tier, after BL-30: it fits the default t1 at G = 100 as well.
	if g100[10] >= float64(profileT1Default) {
		t.Fatalf("10 confirmations at G = 100 needs %.1f", g100[10])
	}
	// MT-J05 (b), built as MT-B32: the lowest T1Max that keeps the 40 tier is its need, 477 at G = 100; the profile's is
	// that plus the slack. At G = 30 every quote, and with it every tier's T1Max, would be the default 420 plus the slack.
	minT1Max := uint64(math.Ceil(g100[40]))
	if TierT1(40) != minT1Max {
		t.Fatalf("the 40 tier quotes %d, the lowest T1Max that keeps it is %d", TierT1(40), minT1Max)
	}
	for _, tier := range Tiers() {
		if tier.Confirmations == 40 && tier.T1Max < minT1Max {
			t.Fatalf("the 40 tier's T1Max %d is below its need %d", tier.T1Max, minT1Max)
		}
	}
	// MT-J05 (a/b): T2 − T1 at 1,200 is inside today's T2Max.
	if profileT2Max < 1200 {
		t.Fatalf("T2Max %d would refuse T2 − T1 = 1,200", profileT2Max)
	}
	for _, c := range tiers {
		t.Logf("%d confirmations: needs %.1f at G = 100, %.1f at G = 30", c, g100[c], g30[c])
	}
	t.Logf("the 40 tier needs a T1Max of at least %d at G = 100 (%d at G = 30); the profile's is that plus t1_slack %d", minT1Max, profileT1Default, profileT1Slack)
}
