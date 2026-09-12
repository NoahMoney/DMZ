package zecswap

import (
	"os"
	"testing"
	"time"

	"veil/tools/zecspv"
)

const headersLog = "../../research/regtest/zecswap-headers.log"

// TestRegtestHeaderPlumbing runs the header gate's reads — getblockhash, getblockheader and getblock, through the same
// ZcashRPC and GatherHeaders the gate uses — against the REAL regtest zebrad, and records in
// research/regtest/zecswap-headers.log what they show: the answers have the shapes the gate reads, every header
// hashes to the block the node names and links to the one below, each transaction's Merkle branch reaches its
// header's root, and zecspv refuses these headers because zebrad's regtest mines with no proof of work — which is why
// the gate is skipped on regtest. It needs zebrad only, not the DERO simulator. Skipped unless ZECSWAP_REGTEST=1.
func TestRegtestHeaderPlumbing(t *testing.T) {
	if os.Getenv("ZECSWAP_REGTEST") == "" {
		t.Skip("set ZECSWAP_REGTEST=1 to read headers from the real regtest zebrad (tools/regtest/README.md)")
	}
	e := newE2EAt(t, headersLog)
	defer e.close()
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ci.Regtest {
		t.Fatalf("not a regtest node: %+v", ci)
	}
	e.logf("== the header gate's reads on zebrad 6.3.0 regtest %s, height %d", e2eZcash, ci.Height)
	const run = 40
	first := ci.Height - run + 1
	raw, hashes, err := GatherHeaders(e.ctx, e.zc, first, ci.Height)
	if err != nil {
		t.Fatal(err)
	}
	var prev *zecspv.Header
	for i, b := range raw {
		h, err := zecspv.ParseHeader(b)
		if err != nil {
			t.Fatal(err)
		}
		if prev != nil && h.PrevBlock != prev.Hash() {
			t.Fatalf("the header at %d does not link to the one below", first+uint32(i))
		}
		prev = h
	}
	zeroSol := true
	for _, x := range prev.Solution {
		zeroSol = zeroSol && x == 0
	}
	e.logf("getblockhash + getblockheader %d..%d: %d headers of %d bytes; each hashes to the block the node names at its height and links to the one below. The tip %s: version %d, nonce %x…, solution all zero: %v, time %s",
		first, ci.Height, len(raw), len(raw[0]), hashes[len(hashes)-1], prev.Version, prev.Nonce[:4], zeroSol, time.Unix(int64(prev.Time), 0).UTC().Format(time.RFC3339))

	// Merkle branches: the tip block and up to three blocks holding more than one transaction
	checked, multi := 0, 0
	for h := ci.Height; h+400 > ci.Height && h > 0 && multi < 3; h-- {
		bh, err := e.zc.BlockHash(e.ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := e.zc.BlockTxIDs(e.ctx, bh)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) < 2 && h != ci.Height {
			continue
		}
		hb, err := e.zc.BlockHeader(e.ctx, bh)
		if err != nil {
			t.Fatal(err)
		}
		hdr, err := zecspv.ParseHeader(hb)
		if err != nil {
			t.Fatal(err)
		}
		leaves := make([]zecspv.Hash, len(ids))
		for i, id := range ids {
			if leaves[i], err = zecspv.ParseHash(id); err != nil {
				t.Fatal(err)
			}
		}
		for i := range leaves {
			br, err := zecspv.MerkleBranch(leaves, i)
			if err != nil {
				t.Fatal(err)
			}
			if err := zecspv.VerifyMerkleBranch(leaves[i], uint32(i), br, hdr.MerkleRoot); err != nil {
				t.Fatalf("block %s transaction %d: %v", bh, i, err)
			}
		}
		e.logf("getblock %s 1 (height %d): %d transaction(s) %v; every one's Merkle branch reaches the header's root %s", bh, h, len(ids), ids, hdr.MerkleRoot)
		checked++
		if len(ids) > 1 {
			multi++
		}
	}
	if checked == 0 {
		t.Fatal("no block was checked")
	}
	// zecspv refuses these headers: regtest has no proof of work
	_, err = zecspv.VerifyHeaders(zecspv.Testnet, first, raw, zecspv.Options{})
	e.logf("zecspv.VerifyHeaders with Testnet's rules on these regtest headers: %v — so on regtest the header gate is skipped, and says so", err)
	if err == nil {
		t.Fatal("zecspv accepted headers mined with no proof of work")
	}
	e.logf("== done: getblock and getblockheader have the shapes the gate reads; the gate itself is skipped on regtest")
}
