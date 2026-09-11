package zecspv

import (
	"errors"
	"testing"
)

// Real blocks: the recorded txid lists hash to the headers' hashMerkleRoot, across v4 coinbases (Blossom-era
// Mainnet, pre-Overwinter Testnet) and v5 and v6 coinbases (NU6.3 Mainnet), whose leaves are ZIP 244 txids. Every
// position's branch verifies, and a wrong branch, index or leaf does not.
func TestMerkleRealBlocks(t *testing.T) {
	versions := map[string]int{}
	blocks := 0
	for _, f := range loadHeaderFiles(t) {
		hs := f.headers(t)
		for _, b := range f.Blocks {
			root := hs[f.index(b.Height)].MerkleRoot
			leaves := leavesOf(t, b.TxIDs)
			got, err := MerkleRoot(leaves)
			if err != nil || got != root {
				t.Fatalf("%s block %d: root %s, header %s (%v)", f.path, b.Height, got, root, err)
			}
			versions[b.CoinbaseVersion]++
			blocks++
			for i, leaf := range leaves {
				br, err := MerkleBranch(leaves, i)
				if err != nil {
					t.Fatal(err)
				}
				if err := VerifyMerkleBranch(leaf, uint32(i), br, root); err != nil {
					t.Fatalf("block %d tx %d: %v", b.Height, i, err)
				}
				if len(br) == 0 {
					continue
				}
				// the neighbour's position: a different order of the pair, unless the pair is one node doubled
				if j := i ^ 1; j < len(leaves) {
					if err := VerifyMerkleBranch(leaf, uint32(j), br, root); !errors.Is(err, ErrMerkle) {
						t.Fatalf("block %d tx %d at index %d: %v", b.Height, i, j, err)
					}
				}
				bad := append([]Hash(nil), br...)
				bad[len(bad)-1][0] ^= 1
				if err := VerifyMerkleBranch(leaf, uint32(i), bad, root); !errors.Is(err, ErrMerkle) {
					t.Fatalf("block %d tx %d, branch changed: %v", b.Height, i, err)
				}
				wrong := leaf
				wrong[31] ^= 0x80
				if err := VerifyMerkleBranch(wrong, uint32(i), br, root); !errors.Is(err, ErrMerkle) {
					t.Fatalf("block %d tx %d, leaf changed: %v", b.Height, i, err)
				}
				if err := VerifyMerkleBranch(leaf, uint32(i)|1<<len(br), br, root); !errors.Is(err, ErrBranch) {
					t.Fatalf("block %d tx %d, index past the branch: %v", b.Height, i, err)
				}
			}
		}
	}
	for _, v := range []string{"80000004", "80000005", "80000006"} {
		if versions[v] == 0 {
			t.Errorf("no recorded block has a coinbase of version %s", v)
		}
	}
	t.Logf("%d real blocks' Merkle roots, by coinbase version: %v", blocks, versions)
}

// The documented weakness of duplicate-last (CVE-2012-2459, zcashd consensus/merkle.cpp) and the odd-end
// ambiguity: both hold, and neither lets a leaf that is not in the list verify.
func TestMerkleDuplicationBehaviour(t *testing.T) {
	var l [8]Hash
	for i := range l {
		l[i][0] = byte(i + 1)
	}
	a, _ := MerkleRoot(l[:6])
	b, _ := MerkleRoot([]Hash{l[0], l[1], l[2], l[3], l[4], l[5], l[4], l[5]})
	if a != b {
		t.Fatal("[1..6] and [1..6,5,6] should share a root")
	}
	three := l[:3]
	root, _ := MerkleRoot(three)
	br, _ := MerkleBranch(three, 2)
	if VerifyMerkleBranch(l[2], 2, br, root) != nil || VerifyMerkleBranch(l[2], 3, br, root) != nil {
		t.Fatal("the last leaf of an odd level verifies at its index and the next")
	}
	if VerifyMerkleBranch(l[7], 2, br, root) == nil {
		t.Fatal("a leaf not in the list verified")
	}
}

func TestMerkleEdges(t *testing.T) {
	var x Hash
	x[3] = 9
	if r, err := MerkleRoot([]Hash{x}); err != nil || r != x {
		t.Fatal("one transaction: the root is its id")
	}
	if br, _ := MerkleBranch([]Hash{x}, 0); len(br) != 0 {
		t.Fatal("one transaction: empty branch")
	}
	if err := VerifyMerkleBranch(x, 0, nil, x); err != nil {
		t.Fatal(err)
	}
	if _, err := RootFromBranch(x, 1, nil); !errors.Is(err, ErrBranch) {
		t.Fatal("index 1 with no branch")
	}
	if _, err := MerkleRoot(nil); err == nil {
		t.Fatal("empty list")
	}
	if _, err := MerkleBranch([]Hash{x}, 1); !errors.Is(err, ErrBranch) {
		t.Fatal("index out of range")
	}
	if _, err := RootFromBranch(x, 0, make([]Hash, 33)); !errors.Is(err, ErrBranch) {
		t.Fatal("33 levels")
	}
}

func TestParseHash(t *testing.T) {
	const s = "000000000007ec04a25dc7552851b4cd512b71f9af26bfb77e4ff6b465c257fa"
	h, err := ParseHash(s)
	if err != nil || h.String() != s || h[31] != 0 || h[0] != 0xfa {
		t.Fatalf("%v %s", err, h)
	}
	for _, bad := range []string{"", s[:62], s + "00", "zz" + s[2:]} {
		if _, err := ParseHash(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
