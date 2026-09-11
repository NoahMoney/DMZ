package zecspv

import (
	"errors"
	"fmt"
)

// The block's transaction tree is Bitcoin's (zcashd consensus/merkle.cpp ComputeMerkleRoot): leaves are the
// transaction ids in block order, each level pairs neighbours and hashes them with SHA-256d, and a level of odd
// length pairs its last node with itself. For v1–v4 transactions the leaf is SHA-256d of the transaction; for v5
// and v6 it is the ZIP 244 txid (ZIP 244, "Transaction Identifier"). Leaves are in internal byte order.
//
// Duplicating the last node lets two different lists share a root (CVE-2012-2459): [1..6] and [1..6,5,6]. The
// extra entries of such a list repeat real ones, so a root proves that every leaf of either list is in the
// block; it does not prove the count or, at the odd end, the position (index i and i+1 can both verify for the
// last leaf of an odd level).

var (
	// ErrMerkle is a branch that does not lead from the leaf to the header's hashMerkleRoot.
	ErrMerkle = errors.New("zecspv: Merkle branch does not match the header's root")
	// ErrBranch is a malformed branch: too deep, or an index with bits beyond its depth.
	ErrBranch = errors.New("zecspv: malformed Merkle branch")
)

// maxBranch bounds a branch's depth. A 2,000,000-byte block holds fewer than 2^15 transactions.
const maxBranch = 32

// MerkleRoot is the root of the tree over leaves. An empty list has no root.
func MerkleRoot(leaves []Hash) (Hash, error) {
	if len(leaves) == 0 {
		return Hash{}, errors.New("zecspv: a block has at least one transaction")
	}
	level := append([]Hash(nil), leaves...)
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := level[:0:0]
		for i := 0; i < len(level); i += 2 {
			next = append(next, sha256d(level[i][:], level[i+1][:]))
		}
		level = next
	}
	return level[0], nil
}

// MerkleBranch is the branch for leaves[index]: its sibling at each level, the leaf's level first.
func MerkleBranch(leaves []Hash, index int) ([]Hash, error) {
	if index < 0 || index >= len(leaves) {
		return nil, fmt.Errorf("%w: index %d of %d leaves", ErrBranch, index, len(leaves))
	}
	var branch []Hash
	level := append([]Hash(nil), leaves...)
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		branch = append(branch, level[index^1])
		next := level[:0:0]
		for i := 0; i < len(level); i += 2 {
			next = append(next, sha256d(level[i][:], level[i+1][:]))
		}
		level = next
		index /= 2
	}
	return branch, nil
}

// RootFromBranch climbs from leaf at position index through branch and returns the root it reaches. Bit j of
// index says whether the node at level j is a right child; bits beyond the branch's depth must be zero.
func RootFromBranch(leaf Hash, index uint32, branch []Hash) (Hash, error) {
	if len(branch) > maxBranch || (len(branch) < 32 && index>>len(branch) != 0) {
		return Hash{}, fmt.Errorf("%w: index %d with a branch of depth %d", ErrBranch, index, len(branch))
	}
	node := leaf
	for _, sib := range branch {
		if index&1 == 1 {
			node = sha256d(sib[:], node[:])
		} else {
			node = sha256d(node[:], sib[:])
		}
		index >>= 1
	}
	return node, nil
}

// VerifyMerkleBranch checks that leaf, at position index, is under root.
func VerifyMerkleBranch(leaf Hash, index uint32, branch []Hash, root Hash) error {
	got, err := RootFromBranch(leaf, index, branch)
	if err != nil {
		return err
	}
	if got != root {
		return fmt.Errorf("%w: the branch gives %s, the header has %s", ErrMerkle, got, root)
	}
	return nil
}
