package zecspv

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
)

// zcashd src/test/arith_uint256_tests.cpp, bignum_SetCompact (zcash/zcash 558f6865): the value, GetCompact() of it
// where the test checks one (0 means it does not), and the flags.
func TestCompactZcashdVectors(t *testing.T) {
	cases := []struct {
		bits     uint32
		hex      string
		compact  uint32
		negative bool
		overflow bool
	}{
		{0, "0", 0, false, false},
		{0x00123456, "0", 0, false, false},
		{0x01003456, "0", 0, false, false},
		{0x02000056, "0", 0, false, false},
		{0x03000000, "0", 0, false, false},
		{0x04000000, "0", 0, false, false},
		{0x00923456, "0", 0, false, false},
		{0x01803456, "0", 0, false, false},
		{0x02800056, "0", 0, false, false},
		{0x03800000, "0", 0, false, false},
		{0x04800000, "0", 0, false, false},
		{0x01123456, "12", 0x01120000, false, false},
		{0x01fedcba, "7e", 0, true, false},
		{0x02123456, "1234", 0x02123400, false, false},
		{0x03123456, "123456", 0x03123456, false, false},
		{0x04123456, "12345600", 0x04123456, false, false},
		{0x04923456, "12345600", 0, true, false},
		{0x05009234, "92340000", 0x05009234, false, false},
		{0x20123456, "1234560000000000000000000000000000000000000000000000000000000000", 0x20123456, false, false},
	}
	for _, c := range cases {
		v, neg, over := CompactToTarget(c.bits)
		if v.Text(16) != c.hex || neg != c.negative || over != c.overflow {
			t.Errorf("%08x: %s neg=%v over=%v, want %s %v %v", c.bits, v.Text(16), neg, over, c.hex, c.negative, c.overflow)
		}
		if got := TargetToCompact(v); got != c.compact && (c.compact != 0 || v.Sign() == 0) {
			t.Errorf("%08x: GetCompact %08x, want %08x", c.bits, got, c.compact)
		}
	}
	if _, neg, over := CompactToTarget(0xff123456); neg || !over {
		t.Errorf("ff123456: neg=%v over=%v, want false true", neg, over)
	}
	// "Make sure that we don't generate compacts with the 0x00800000 bit set"
	if got := TargetToCompact(big.NewInt(0x80)); got != 0x02008000 {
		t.Errorf("0x80: %08x", got)
	}
}

func TestPowLimitCompact(t *testing.T) {
	// zcashd pow_tests use 0x1f07ffff as Mainnet's limit; Testnet's is 2^251 − 1, 0x2007ffff
	if got := TargetToCompact(Mainnet.PowLimit); got != 0x1f07ffff {
		t.Errorf("mainnet %08x", got)
	}
	if got := TargetToCompact(Testnet.PowLimit); got != 0x2007ffff {
		t.Errorf("testnet %08x", got)
	}
	if Mainnet.PowLimit.Text(16) != "7ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" {
		t.Error("mainnet PoWLimit is not 0007ff…ff")
	}
}

func TestTargetRange(t *testing.T) {
	if _, err := Mainnet.Target(0x1f07ffff); err != nil {
		t.Errorf("mainnet limit refused: %v", err)
	}
	for _, bits := range []uint32{0, 0x01003456, 0x04923456, 0xff123456, 0x2007ffff, 0x1f080000} {
		if _, err := Mainnet.Target(bits); !errors.Is(err, ErrBits) {
			t.Errorf("mainnet %08x: %v", bits, err)
		}
	}
	if _, err := Testnet.Target(0x2007ffff); err != nil {
		t.Errorf("testnet limit refused: %v", err)
	}
}

func TestWork(t *testing.T) {
	// 2^256 / (target + 1): 0x1d00ffff gives 0x100010001; Mainnet's PoWLimit gives 2^13
	if w := Work(mustTarget(t, 0x1d00ffff)); w.Text(16) != "100010001" {
		t.Errorf("work(1d00ffff) = %s", w.Text(16))
	}
	if w := Work(Mainnet.PowLimit); w.Int64() != 8192 {
		t.Errorf("work(mainnet limit) = %s", w)
	}
}

func TestFloorFromBits(t *testing.T) {
	f, err := Mainnet.FloorFromBits(0x1c00802e, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := new(big.Int).Mul(mustTarget(t, 0x1c00802e), big.NewInt(4)); f.Cmp(want) != 0 {
		t.Fatalf("floor %s", f.Text(16))
	}
	if f, _ := Mainnet.FloorFromBits(0x1f07ffff, 8); f.Cmp(Mainnet.PowLimit) != 0 {
		t.Fatal("the floor is not capped at PoWLimit")
	}
	if _, err := Mainnet.FloorFromBits(0x1c00802e, 0); err == nil {
		t.Fatal("factor 0 accepted")
	}
}

// zcashd src/test/pow_tests.cpp (zcash/zcash 558f6865): CalculateNextWorkRequired(bnAvg, nThisTime,
// nLastRetargetTime, params, 0). The _blossom cases run under RegtestActivateBlossom(true) (src/util/test.cpp):
// Blossom from height 0, PoWMaxAdjustDown 32, PoWMaxAdjustUp 16 and PoWLimit 0007ff…ff, which is Mainnet's
// parameters with Blossom at 0.
func TestCalculateNextWorkRequiredZcashd(t *testing.T) {
	blossom := *Mainnet
	blossom.BlossomHeight = 0
	if Mainnet.TargetSpacing(0) != 150 || blossom.TargetSpacing(0) != 75 {
		t.Fatal("PoWTargetSpacing(0)")
	}
	cases := []struct {
		name        string
		p           *Params
		avg         uint32
		this, last  int64
		want        uint32
		belowAnswer bool // BOOST_CHECK_GT(want, result) rather than equality
	}{
		{"get_next_work", Mainnet, 0x1d00ffff, 1000003570, 1000000000, 0x1d011998, false},
		{"get_next_work_blossom", &blossom, 0x1d00ffff, 1000001445, 1000000000, 0x1d011998, true},
		{"get_next_work_pow_limit", Mainnet, 0x1f07ffff, 1233061996, 1231006505, 0x1f07ffff, false},
		{"get_next_work_pow_limit_blossom", &blossom, 0x1f07ffff, 1233061996, 1231006505, 0x1f07ffff, false},
		{"get_next_work_lower_limit_actual", Mainnet, 0x1c05a3f4, 100000917, 1000000000, 0x1c04bceb, false},
		{"get_next_work_lower_limit_actual_blossom", &blossom, 0x1c05a3f4, 1000000458, 1000000000, 0x1c04bceb, false},
		{"get_next_work_upper_limit_actual", Mainnet, 0x1c387f6f, 1000005815, 1000000000, 0x1c4a93bb, false},
		{"get_next_work_upper_limit_actual_blossom", &blossom, 0x1c387f6f, 1000002908, 1000000000, 0x1c4a93bb, false},
	}
	for _, c := range cases {
		got := c.p.calculateNextBits(mustTarget(t, c.avg), c.this, c.last, 0)
		if c.belowAnswer {
			if !(got < c.want) {
				t.Errorf("%s: %08x, want below %08x", c.name, got, c.want)
			}
		} else if got != c.want {
			t.Errorf("%s: %08x, want %08x", c.name, got, c.want)
		}
	}
}

// Every header in testdata with 28 below it: nBits is what NextBits computes from those 28.
func TestNextBitsRealChains(t *testing.T) {
	for _, f := range loadHeaderFiles(t) {
		p := f.params(t)
		hs := f.headers(t)
		n, minDiff := 0, 0
		for i := p.Context(); i < len(hs); i++ {
			h := f.FirstHeight + uint32(i)
			got, err := p.NextBits(h, hs[i-p.Context():i], hs[i].Time)
			if err != nil {
				t.Fatal(err)
			}
			if got != hs[i].Bits {
				t.Fatalf("%s height %d: rule %08x, block %08x", f.path, h, got, hs[i].Bits)
			}
			n++
			if hs[i].Bits == TargetToCompact(p.PowLimit) {
				minDiff++
			}
		}
		t.Logf("%s: %d headers' nBits recomputed, %d of them minimum-difficulty", f.path, n, minDiff)
	}
}

// Blossom's switch from 150 s to 75 s is at 653600 on Mainnet; moved one block, the rule no longer gives the
// chain's nBits there.
func TestBlossomSpacingMatters(t *testing.T) {
	f := loadHeaderFile(t, "mainnet-653572.json")
	moved := *Mainnet
	moved.BlossomHeight = 653601
	_, err := VerifyHeaders(&moved, f.FirstHeight, f.raw(t), Options{})
	wantHeaderErr(t, err, 653600, ErrDifficulty)
	// heights claimed one too low put Blossom's first block under the 150 s rule, at index 28, the first header
	// whose nBits is recomputed. Claimed one too high, the header moved onto 653600 is index 27, which is not
	// recomputed, so that shift passes: the claimed height is checked only where it changes a rule
	_, err = VerifyHeaders(Mainnet, f.FirstHeight-1, f.raw(t), Options{})
	wantHeaderErr(t, err, 653599, ErrDifficulty)
	if _, err := VerifyHeaders(Mainnet, f.FirstHeight+1, f.raw(t), Options{}); err != nil {
		t.Fatalf("heights one too high: %v", err)
	}
}

// Testnet's minimum-difficulty rule (ZIP 205, ZIP 208): in the recorded range every PoWLimit block is more than
// six spacings after its parent, the same header one second inside that gap would owe the normal difficulty,
// and without the rule the chain fails at its first such block.
func TestTestnetMinimumDifficulty(t *testing.T) {
	f := loadHeaderFile(t, "testnet-299160.json")
	p := Testnet
	hs := f.headers(t)
	limit := TargetToCompact(p.PowLimit)
	first := uint32(0)
	for i := p.Context(); i < len(hs); i++ {
		h := f.FirstHeight + uint32(i)
		if hs[i].Bits != limit {
			continue
		}
		if first == 0 {
			first = h
		}
		gap := int64(hs[i].Time) - int64(hs[i-1].Time)
		if h < p.MinDifficultyHeight || gap <= 6*p.TargetSpacing(h) {
			t.Fatalf("height %d is at PoWLimit with a gap of %d s", h, gap)
		}
		regular, err := p.NextBits(h, hs[i-p.Context():i], hs[i-1].Time+uint32(6*p.TargetSpacing(h)))
		if err != nil || regular == limit {
			t.Fatalf("height %d: inside the gap the rule gives %08x (%v)", h, regular, err)
		}
	}
	if first == 0 {
		t.Fatal("no minimum-difficulty block in the recorded range")
	}
	without := *Testnet
	without.MinDifficultyHeight = 0
	_, err := VerifyHeaders(&without, f.FirstHeight, f.raw(t), Options{})
	wantHeaderErr(t, err, first, ErrDifficulty)
	t.Logf("first minimum-difficulty block in the range: %d", first)
}

func ExampleTargetToCompact() {
	fmt.Printf("%08x\n", TargetToCompact(Mainnet.PowLimit))
	// Output: 1f07ffff
}
