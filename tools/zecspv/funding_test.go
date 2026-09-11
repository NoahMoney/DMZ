package zecspv

import (
	"errors"
	"math/big"
	"testing"
	"time"
)

// fundingCase is a real transaction in a real block with real headers above it.
type fundingCase struct {
	f      *headerFile
	proof  FundingProof
	policy Policy
	hs     []*Header
}

// newFundingCase takes transaction txIndex of the recorded block at height, with every recorded header.
func newFundingCase(t testing.TB, file string, height uint32, txIndex int) *fundingCase {
	t.Helper()
	f := loadHeaderFile(t, file)
	p := f.params(t)
	hs := f.headers(t)
	var leaves []Hash
	for _, b := range f.Blocks {
		if b.Height == height {
			leaves = leavesOf(t, b.TxIDs)
		}
	}
	if leaves == nil {
		t.Fatalf("no txids recorded for %d", height)
	}
	br, err := MerkleBranch(leaves, txIndex)
	if err != nil {
		t.Fatal(err)
	}
	floor, err := p.FloorFromBits(hs[0].Bits, 4)
	if err != nil {
		t.Fatal(err)
	}
	tip := time.Unix(int64(hs[len(hs)-1].Time), 0)
	return &fundingCase{
		f:  f,
		hs: hs,
		proof: FundingProof{Height: f.FirstHeight, Headers: f.raw(t), Block: f.index(height), TxID: leaves[txIndex],
			Index: uint32(txIndex), Branch: br},
		policy: Policy{Depth: len(hs) - 1 - f.index(height), Below: 28, MaxTarget: floor, Now: tip.Add(10 * time.Minute),
			MaxTipAge: 3 * time.Hour},
	}
}

func TestVerifyFundingRealNU63(t *testing.T) {
	// block 3436162 (7 transactions, a v6 coinbase), transaction 3, 9 headers above it: 10 confirmations
	c := newFundingCase(t, "mainnet-3436131.json", 3436162, 3)
	got, err := VerifyFunding(Mainnet, &c.proof, c.policy)
	if err != nil {
		t.Fatal(err)
	}
	if got.BlockHeight != 3436162 || got.TipHeight != 3436171 || got.Confirmations != 10 || c.policy.Depth != 9 {
		t.Fatalf("%+v", got)
	}
	want, least := new(big.Int), (*big.Int)(nil)
	for _, h := range c.hs[c.proof.Block:] {
		w := Work(mustTarget(t, h.Bits))
		want.Add(want, w)
		if least == nil || w.Cmp(least) < 0 {
			least = w
		}
	}
	if got.Work.Cmp(want) != 0 || got.MinWork.Cmp(least) != 0 {
		t.Fatalf("work %s / %s, want %s / %s", got.Work, got.MinWork, want, least)
	}
	if got.BlockHash.String() != c.f.Blocks[3].Hash || got.TipHash.String() != c.f.Pins[0].Hash {
		t.Fatalf("hashes %s %s", got.BlockHash, got.TipHash)
	}
	t.Logf("funding at %d, tip %d (the Zebra checkpoint), %d confirmations, work %s, least per block %s",
		got.BlockHeight, got.TipHeight, got.Confirmations, got.Work, got.MinWork)
}

func TestVerifyFundingRealBlossom(t *testing.T) {
	// block 653601, the first after Blossom's activation block, transaction 5 of 9, 48 headers above
	c := newFundingCase(t, "mainnet-653572.json", 653601, 5)
	got, err := VerifyFunding(Mainnet, &c.proof, c.policy)
	if err != nil {
		t.Fatal(err)
	}
	if got.BlockHeight != 653601 || got.Confirmations != 49 {
		t.Fatalf("%+v", got)
	}
}

func TestVerifyFundingNegativeCases(t *testing.T) {
	fresh := func(t *testing.T) *fundingCase { return newFundingCase(t, "mainnet-3436131.json", 3436162, 3) }
	run := func(c *fundingCase) error { _, err := VerifyFunding(Mainnet, &c.proof, c.policy); return err }

	t.Run("wrong Merkle branch", func(t *testing.T) {
		c := fresh(t)
		c.proof.Branch[1][5] ^= 1
		if err := run(c); !errors.Is(err, ErrMerkle) {
			t.Fatal(err)
		}
	})
	t.Run("wrong index", func(t *testing.T) {
		c := fresh(t)
		c.proof.Index = 2
		if err := run(c); !errors.Is(err, ErrMerkle) {
			t.Fatal(err)
		}
	})
	t.Run("index past the branch", func(t *testing.T) {
		c := fresh(t)
		c.proof.Index |= 1 << len(c.proof.Branch)
		if err := run(c); !errors.Is(err, ErrBranch) {
			t.Fatal(err)
		}
	})
	t.Run("transaction not in the block", func(t *testing.T) {
		c := fresh(t)
		c.proof.TxID[0] ^= 1
		if err := run(c); !errors.Is(err, ErrMerkle) {
			t.Fatal(err)
		}
	})
	t.Run("transaction in another block", func(t *testing.T) {
		c := fresh(t)
		c.proof.Block++ // the same branch against the next header's root
		c.policy.Depth--
		if err := run(c); !errors.Is(err, ErrMerkle) {
			t.Fatal(err)
		}
	})
	t.Run("too shallow", func(t *testing.T) {
		c := fresh(t)
		c.policy.Depth = 10
		if err := run(c); !errors.Is(err, ErrDepth) {
			t.Fatal(err)
		}
		c = fresh(t)
		c.proof.Headers = c.proof.Headers[:len(c.proof.Headers)-1]
		if err := run(c); !errors.Is(err, ErrDepth) {
			t.Fatal(err)
		}
	})
	t.Run("too few headers below", func(t *testing.T) {
		c := fresh(t)
		c.policy.Below = 32
		if err := run(c); !errors.Is(err, ErrContext) {
			t.Fatal(err)
		}
		c = fresh(t)
		c.proof.Headers = c.proof.Headers[4:]
		c.proof.Height += 4
		c.proof.Block -= 4 // 27 below: its own nBits could not be recomputed
		if err := run(c); !errors.Is(err, ErrContext) {
			t.Fatal(err)
		}
	})
	t.Run("no floor", func(t *testing.T) {
		c := fresh(t)
		c.policy.MaxTarget = nil
		if err := run(c); !errors.Is(err, ErrNoFloor) {
			t.Fatal(err)
		}
	})
	t.Run("floor above the chain", func(t *testing.T) {
		c := fresh(t)
		c.policy.MaxTarget = new(big.Int).Rsh(mustTarget(t, c.hs[len(c.hs)-1].Bits), 1)
		if err := run(c); !errors.Is(err, ErrBelowFloor) {
			t.Fatal(err)
		}
	})
	t.Run("stale tip", func(t *testing.T) {
		c := fresh(t)
		c.policy.Now = c.policy.Now.Add(4 * time.Hour)
		if err := run(c); !errors.Is(err, ErrStale) {
			t.Fatal(err)
		}
	})
	t.Run("headers from the future", func(t *testing.T) {
		c := fresh(t)
		c.policy.Now = c.policy.Now.Add(-3 * time.Hour)
		if err := run(c); !errors.Is(err, ErrFuture) {
			t.Fatal(err)
		}
	})
	t.Run("a confirmation with a tampered solution", func(t *testing.T) {
		c := fresh(t)
		c.proof.Headers = cloneRaw(c.proof.Headers)
		c.proof.Headers[36][offSol+9] ^= 4
		wantHeaderErr(t, run(c), 3436167, ErrAboveTarget, ErrEquihashCollision, ErrEquihashOrder, ErrEquihashDuplicate, ErrEquihashNonZero)
	})
	t.Run("a confirmation with a wrong nonce", func(t *testing.T) {
		c := fresh(t)
		c.proof.Headers = cloneRaw(c.proof.Headers)
		c.proof.Headers[40][offNonce] ^= 1
		wantHeaderErr(t, run(c), 3436171, ErrAboveTarget, ErrEquihashCollision, ErrEquihashOrder, ErrEquihashDuplicate, ErrEquihashNonZero)
	})
	t.Run("a confirmation that does not link", func(t *testing.T) {
		c := fresh(t)
		c.proof.Headers = cloneRaw(c.proof.Headers)
		c.proof.Headers[38], c.proof.Headers[39] = c.proof.Headers[39], c.proof.Headers[38]
		wantHeaderErr(t, run(c), 3436169, ErrLink)
	})
	t.Run("the funding block with other nBits", func(t *testing.T) {
		c := fresh(t)
		c.proof.Headers = cloneRaw(c.proof.Headers)
		setU32(c.proof.Headers[c.proof.Block], offBits, c.hs[c.proof.Block].Bits-1)
		wantHeaderErr(t, run(c), 3436162, ErrDifficulty)
	})
}

// The taker's check before funding: the tip her cltv arithmetic reads is backed by work and recent.
func TestVerifyTip(t *testing.T) {
	c := newFundingCase(t, "mainnet-3436131.json", 3436162, 3)
	pol := c.policy
	pol.Depth = 99 // ignored
	tip, err := VerifyTip(Mainnet, c.proof.Height, c.proof.Headers, pol)
	if err != nil {
		t.Fatal(err)
	}
	if tip.Height != 3436171 || tip.Hash.String() != c.f.Pins[0].Hash || tip.Time.Unix() != int64(c.hs[40].Time) {
		t.Fatalf("%+v", tip)
	}
	if want := (&Chain{Headers: c.hs}).Work(28, 41); tip.Work.Cmp(want) != 0 {
		t.Fatalf("work %s, want %s", tip.Work, want)
	}
	stale := pol
	stale.Now = stale.Now.Add(4 * time.Hour)
	if _, err := VerifyTip(Mainnet, c.proof.Height, c.proof.Headers, stale); !errors.Is(err, ErrStale) {
		t.Fatalf("stale: %v", err)
	}
	if _, err := VerifyTip(Mainnet, c.proof.Height+13, c.proof.Headers[13:], pol); !errors.Is(err, ErrContext) {
		t.Fatalf("27 below the tip: %v", err)
	}
	more := pol
	more.Below = 41
	if _, err := VerifyTip(Mainnet, c.proof.Height, c.proof.Headers, more); !errors.Is(err, ErrContext) {
		t.Fatalf("Below 41: %v", err)
	}
	none := pol
	none.MaxTarget = nil
	if _, err := VerifyTip(Mainnet, c.proof.Height, c.proof.Headers, none); !errors.Is(err, ErrNoFloor) {
		t.Fatalf("no floor: %v", err)
	}
	fake := cloneRaw(c.proof.Headers)
	fake[40][offSol] ^= 1
	if _, err := VerifyTip(Mainnet, c.proof.Height, fake, pol); err == nil {
		t.Fatal("a tip with a tampered solution")
	}
}

// On Testnet the minimum-difficulty rule lets any block after a six-spacing gap sit at PoWLimit, so a floor
// derived from real difficulty refuses real Testnet chains, and with no floor the check proves little. The
// testnet pilot exercises the code, not the security.
func TestVerifyFundingTestnetFloor(t *testing.T) {
	f := loadHeaderFile(t, "testnet-299160.json")
	var height uint32
	var leaves []Hash
	for _, b := range f.Blocks {
		if f.index(b.Height) >= 28 {
			height, leaves = b.Height, leavesOf(t, b.TxIDs)
			break
		}
	}
	if leaves == nil {
		t.Fatal("no recorded block with 28 headers below it")
	}
	br, _ := MerkleBranch(leaves, 0)
	proof := FundingProof{Height: f.FirstHeight, Headers: f.raw(t), Block: f.index(height), TxID: leaves[0], Branch: br}
	pol := Policy{Depth: len(f.Headers) - 1 - f.index(height), MaxTarget: Testnet.PowLimit}
	if _, err := VerifyFunding(Testnet, &proof, pol); err != nil {
		t.Fatalf("no floor: %v", err)
	}
	hs := f.headers(t)
	pol.MaxTarget, _ = Testnet.FloorFromBits(hs[0].Bits, 4)
	if _, err := VerifyFunding(Testnet, &proof, pol); !errors.Is(err, ErrBelowFloor) {
		t.Fatalf("a real-difficulty floor on Testnet: %v", err)
	}
}
