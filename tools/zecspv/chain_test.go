package zecspv

import (
	"errors"
	"math/big"
	"testing"
	"time"
)

// Every recorded run verifies whole: parsing, Equihash, targets, links, times and every recomputable nBits. The
// pinned hash from Zebra's checkpoint list is in the run, so the run is the network's chain.
func TestRealHeaderChains(t *testing.T) {
	for _, f := range loadHeaderFiles(t) {
		p := f.params(t)
		hs := f.headers(t)
		now := time.Unix(int64(hs[len(hs)-1].Time), 0).Add(time.Hour)
		c, err := VerifyHeaders(p, f.FirstHeight, f.raw(t), Options{Now: now})
		if err != nil {
			t.Fatalf("%s: %v", f.path, err)
		}
		if c.Contextual != p.Context() || len(c.Headers) != len(f.Headers) {
			t.Fatalf("%s: contextual %d of %d", f.path, c.Contextual, len(c.Headers))
		}
		for _, pin := range f.Pins {
			if got := c.Headers[f.index(pin.Height)].Hash().String(); got != pin.Hash {
				t.Fatalf("%s: height %d is %s, the checkpoint is %s", f.path, pin.Height, got, pin.Hash)
			}
		}
		for _, b := range f.Blocks {
			if got := c.Headers[f.index(b.Height)].Hash().String(); got != b.Hash {
				t.Fatalf("%s: height %d is %s, recorded %s", f.path, b.Height, got, b.Hash)
			}
		}
		t.Logf("%s: %d headers %d..%d, %d with nBits recomputed, work %s", f.path, len(c.Headers), c.Height,
			c.Height+uint32(len(c.Headers)-1), len(c.Headers)-c.Contextual, c.Work(0, len(c.Headers)))
	}
}

// A pinned checkpoint: every recorded pin anchors its own run; a wrong hash, a pin too near the bottom of the
// run, or one outside it is refused; and VerifyFunding carries the anchor through.
func TestAnchor(t *testing.T) {
	for _, f := range loadHeaderFiles(t) {
		pin := f.Pins[0]
		h, err := ParseHash(pin.Hash)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyHeaders(f.params(t), f.FirstHeight, f.raw(t), Options{Anchor: &Checkpoint{pin.Height, h}}); err != nil {
			t.Fatalf("%s: %v", f.path, err)
		}
	}
	f := loadHeaderFile(t, "mainnet-3436131.json")
	raw := f.raw(t)
	hs := f.headers(t)
	wrong := &Checkpoint{3436171, hs[39].Hash()}
	_, err := VerifyHeaders(Mainnet, f.FirstHeight, raw, Options{Anchor: wrong})
	wantHeaderErr(t, err, 3436171, ErrAnchor)
	for _, at := range []uint32{f.FirstHeight + 27, f.FirstHeight - 1, f.FirstHeight + 41} {
		if _, err := VerifyHeaders(Mainnet, f.FirstHeight, raw, Options{Anchor: &Checkpoint{at, Hash{}}}); !errors.Is(err, ErrAnchor) {
			t.Errorf("anchor at %d: %v", at, err)
		}
	}
	c := newFundingCase(t, "mainnet-3436131.json", 3436162, 3)
	c.policy.Anchor = &Checkpoint{3436159, hs[28].Hash()}
	c.policy.MaxTarget = Mainnet.PowLimit
	if _, err := VerifyFunding(Mainnet, &c.proof, c.policy); err != nil {
		t.Fatalf("anchored funding: %v", err)
	}
	c.policy.Anchor.Hash[0] ^= 1
	if _, err := VerifyFunding(Mainnet, &c.proof, c.policy); !errors.Is(err, ErrAnchor) {
		t.Fatalf("anchored funding, wrong pin: %v", err)
	}
}

// Headers do not name their network. On both recorded Mainnet runs every header whose nBits is recomputed (index
// 28 on) is past Blossom on both networks, so Testnet's rules differ from Mainnet's there only through the
// minimum-difficulty rule: they refuse a run exactly at the first recomputed block more than six spacings (450 s)
// after its parent, and accept it when there is none. Testnet's later max-time height only relaxes a rule.
func TestTestnetRulesOnMainnetHeaders(t *testing.T) {
	for _, name := range []string{"mainnet-653572.json", "mainnet-3436131.json"} {
		f := loadHeaderFile(t, name)
		hs := f.headers(t)
		gap := uint32(0)
		for i := Testnet.Context(); i < len(hs) && gap == 0; i++ {
			if int64(hs[i].Time)-int64(hs[i-1].Time) > 6*Testnet.TargetSpacing(f.FirstHeight+uint32(i)) {
				gap = f.FirstHeight + uint32(i)
			}
		}
		_, err := VerifyHeaders(Testnet, f.FirstHeight, f.raw(t), Options{})
		if gap == 0 && err != nil {
			t.Fatalf("%s: no six-spacing gap, and still refused: %v", name, err)
		}
		if gap != 0 {
			wantHeaderErr(t, err, gap, ErrDifficulty)
		}
		t.Logf("Testnet rules on %s: first recomputed gap over 450 s at %d (0 = none); result %v", name, gap, err)
	}
}

func TestGenesisHashes(t *testing.T) {
	// the height-0 lines of Zebra 6.3.0's main-checkpoints.txt and test-checkpoints.txt
	if Mainnet.Genesis.String() != "00040fe8ec8471911baa1db1266ea15dd06b4a8a5c453883c000b031973dce08" ||
		Testnet.Genesis.String() != "05a60a92d99d85997cce3b87616c089f6124d7342af37106edc76126334a2c38" {
		t.Fatal("genesis hashes")
	}
}

func TestParseHeaderStrict(t *testing.T) {
	good := loadHeaderFile(t, "mainnet-3436131.json").raw(t)[0]
	if _, err := ParseHeader(good); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{
		"truncated":      good[:HeaderLen-1],
		"trailing byte":  append(append([]byte(nil), good...), 0),
		"empty":          nil,
		"size 1343":      func() []byte { b := append([]byte(nil), good...); b[141] = 0x3f; return b }(),
		"compactSize fe": func() []byte { b := append([]byte(nil), good...); b[140] = 0xfe; return b }(),
	} {
		if _, err := ParseHeader(b); !errors.Is(err, ErrParse) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a non-minimal compactSize: fe 40 05 00 00 then the solution, two bytes longer
	nm := append(append(append([]byte(nil), good[:140]...), 0xfe, 0x40, 0x05, 0x00, 0x00), good[143:]...)
	if _, err := ParseHeader(nm); !errors.Is(err, ErrParse) {
		t.Errorf("non-minimal compactSize: %v", err)
	}
	block := append(append([]byte(nil), good...), 1, 2, 3)
	if h, err := HeaderFromBlock(block); err != nil || h.Hash() != sha256d(good) {
		t.Errorf("HeaderFromBlock: %v", err)
	}
}

// The negative cases, one change each to a real run. The run is NU6.3 Mainnet, 3436131..3436171.
func TestChainNegativeCases(t *testing.T) {
	f := loadHeaderFile(t, "mainnet-3436131.json")
	p := Mainnet
	raw := f.raw(t)
	first := f.FirstHeight
	hs := f.headers(t)

	t.Run("broken link: two headers swapped", func(t *testing.T) {
		r := cloneRaw(raw)
		r[30], r[31] = r[31], r[30]
		_, err := VerifyHeaders(p, first, r, Options{})
		wantHeaderErr(t, err, first+30, ErrLink)
	})
	t.Run("broken link: one header missing", func(t *testing.T) {
		r := append(cloneRaw(raw[:20]), cloneRaw(raw[21:])...)
		_, err := VerifyHeaders(p, first, r, Options{})
		wantHeaderErr(t, err, first+20, ErrLink)
	})
	t.Run("tampered solution", func(t *testing.T) {
		r := cloneRaw(raw)
		r[35][offSol+500] ^= 0x10
		_, err := VerifyHeaders(p, first, r, Options{})
		// the hash moves with the solution, so the cheap filter usually catches it before Equihash does
		wantHeaderErr(t, err, first+35, ErrAboveTarget, ErrEquihashCollision, ErrEquihashOrder, ErrEquihashDuplicate, ErrEquihashNonZero)
	})
	t.Run("wrong nonce", func(t *testing.T) {
		r := cloneRaw(raw)
		r[35][offNonce+7] ^= 0x01
		_, err := VerifyHeaders(p, first, r, Options{})
		wantHeaderErr(t, err, first+35, ErrAboveTarget, ErrEquihashCollision, ErrEquihashOrder, ErrEquihashDuplicate, ErrEquihashNonZero)
	})
	t.Run("header above its target", func(t *testing.T) {
		// a context header (no nBits recomputation) claiming a target 2^16 times harder than its hash meets
		r := cloneRaw(raw)
		hard := new(big.Int).Rsh(mustTarget(t, hs[5].Bits), 16)
		setU32(r[5], offBits, TargetToCompact(hard))
		_, err := VerifyHeaders(p, first, r[:6], Options{})
		wantHeaderErr(t, err, first+5, ErrAboveTarget)
	})
	t.Run("nBits not the rule's", func(t *testing.T) {
		r := cloneRaw(raw)
		setU32(r[35], offBits, hs[35].Bits+1)
		_, err := VerifyHeaders(p, first, r, Options{})
		wantHeaderErr(t, err, first+35, ErrDifficulty)
	})
	t.Run("nBits out of range", func(t *testing.T) {
		r := cloneRaw(raw)
		setU32(r[3], offBits, 0x2007ffff) // easier than Mainnet's PoWLimit
		_, err := VerifyHeaders(p, first, r[:4], Options{})
		wantHeaderErr(t, err, first+3, ErrBits)
	})
	t.Run("time not after the median", func(t *testing.T) {
		r := cloneRaw(raw)
		mtp, _ := p.MedianTimePast(hs[:20])
		setU32(r[20], offTime, uint32(mtp))
		_, err := VerifyHeaders(p, first, r, Options{})
		wantHeaderErr(t, err, first+20, ErrTimeTooOld)
	})
	t.Run("time too far past the median", func(t *testing.T) {
		r := cloneRaw(raw)
		mtp, _ := p.MedianTimePast(hs[:20])
		setU32(r[20], offTime, uint32(mtp+p.MaxTimeAfterMedian+1))
		_, err := VerifyHeaders(p, first, r, Options{})
		wantHeaderErr(t, err, first+20, ErrTimeTooFar)
	})
	t.Run("ahead of the local clock", func(t *testing.T) {
		now := time.Unix(int64(hs[10].Time), 0).Add(-maxFutureLocal - time.Second)
		_, err := VerifyHeaders(p, first, raw, Options{Now: now})
		var he *HeaderError
		if !errors.As(err, &he) || !errors.Is(err, ErrFuture) || he.Height > first+10 {
			t.Fatalf("%v", err)
		}
	})
	t.Run("easier than the floor", func(t *testing.T) {
		// the run's difficulty rises; a floor at its last target refuses the first header
		_, err := VerifyHeaders(p, first, raw, Options{MaxTarget: mustTarget(t, hs[len(hs)-1].Bits)})
		wantHeaderErr(t, err, first, ErrBelowFloor)
	})
	t.Run("version below 4", func(t *testing.T) {
		r := cloneRaw(raw)
		setU32(r[2], offVersion, 3)
		_, err := VerifyHeaders(p, first, r[:3], Options{})
		wantHeaderErr(t, err, first+2, ErrVersion)
	})
}
