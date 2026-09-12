package zecswap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"veil/tools/zecspv"
)

const (
	testnetGate   = "http://127.0.0.1:18232/"
	testnetCookie = "../../.sim/zcash-testnet/.cookie"
)

// TestTestnetHeaderPlumbing runs the header gate's reads and zecspv's checks against the REAL Zcash testnet node,
// through tools/regtest/rpcgate, on HISTORICAL blocks, and records what they show in
// research/regtest/zecswap-headers.log. Skipped unless ZECSWAP_TESTNET=1.
//
// It is a test-only harness and changes nothing the companion ships. The node is still syncing, so its tip is years
// old and the shipped gate refuses it (the tip-age rule; recorded below). This harness therefore reads a window of
// blocks well below that tip and checks them with no tip age, which is the one rule it leaves out; every other rule
// — Equihash, the difficulty rule with 200 headers below, the Merkle branch, the links — is the shipped one. TESTNET
// CAVEAT: the minimum-difficulty rule lets anyone mine a Testnet block nearly free, so this proves the plumbing — the
// node's answers through the gate have the shapes the gate reads, and real headers pass zecspv — never that a
// forgery would cost anything.
func TestTestnetHeaderPlumbing(t *testing.T) {
	if os.Getenv("ZECSWAP_TESTNET") == "" {
		t.Skip("set ZECSWAP_TESTNET=1 to read headers from the Zcash testnet node through its rpcgate (tools/regtest/zcash-testnet-up.sh)")
	}
	user, pass, err := ReadCookie(testnetCookie)
	if err != nil {
		t.Fatalf("the testnet gate's cookie: %v", err)
	}
	f, err := os.OpenFile(headersLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	e := &e2e{t: t, ctx: context.Background(), zc: NewZcashRPC(testnetGate, user, pass), log: f, dir: t.TempDir(), repo: repo}
	defer e.close()
	ctx := e.ctx

	var info struct {
		Chain     string `json:"chain"`
		Blocks    uint32 `json:"blocks"`
		Estimated uint32 `json:"estimatedheight"`
	}
	if err := e.zc.call(ctx, "getblockchaininfo", nil, &info); err != nil {
		t.Fatal(err)
	}
	g, err := e.zc.BlockHash(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Chain != "test" || g != zecspv.Testnet.Genesis.String() {
		t.Fatalf("not the Zcash testnet: chain %q, genesis %s", info.Chain, g)
	}
	e.logf("== the header gate's reads on the Zcash TESTNET node through rpcgate %s: chain %q, genesis %s (testnet's), height %d of an estimated %d (still syncing)", testnetGate, info.Chain, g, info.Blocks, info.Estimated)
	e.logf("== TESTNET CAVEAT: the minimum-difficulty rule makes Testnet headers nearly free to forge; this checks the plumbing only, never the cost of a lie")

	// The shipped gate, on this node's tip: refused, because the newest header is far older than an hour.
	hc := DefaultHeaderCheck()
	params, floor, plumbing, err := hc.setup(Testnet)
	if err != nil || params != zecspv.Testnet || !plumbing {
		t.Fatalf("setup: %v %v", err, plumbing)
	}
	tipFirst := info.Blocks - uint32(hc.Below)
	tipRaw, _, err := GatherHeaders(ctx, e.zc, tipFirst, info.Blocks)
	if err != nil {
		t.Fatal(err)
	}
	_, err = zecspv.VerifyTip(params, tipFirst, tipRaw, hc.policy(0, floor))
	e.logf("the shipped gate's tip check (VerifyTip, %d headers below, tip age at most %s) on the node's tip %d: %v", hc.Below, hc.MaxTipAge, info.Blocks, err)
	if !errors.Is(err, zecspv.ErrStale) {
		t.Fatalf("the shipped tip check on a tip years old: %v, want ErrStale", err)
	}

	// A historical window: a block B with 200 headers below it and 9 above (ten confirmations), well below the tip.
	// B is the first block below tip − 1000 that holds a transaction besides its coinbase, if one is found nearby.
	below, depth := hc.Below, int(DefaultPolicy().ConfZec)-1
	var bh string
	var ids []string
	var b uint32
	for h := info.Blocks - 1000; h > info.Blocks-1400; h-- {
		hash, err := e.zc.BlockHash(ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		list, err := e.zc.BlockTxIDs(ctx, hash)
		if err != nil {
			t.Fatal(err)
		}
		if b == 0 || len(list) > 1 {
			b, bh, ids = h, hash, list
		}
		if len(list) > 1 {
			break
		}
	}
	idx := len(ids) - 1 // a non-coinbase transaction when the block has one
	leaf, err := zecspv.ParseHash(ids[idx])
	if err != nil {
		t.Fatal(err)
	}
	tip := b + uint32(depth)
	proof, err := GatherFundingProof(ctx, e.zc, b, tip, below, leaf, bh)
	if err != nil {
		t.Fatal(err)
	}
	pol := hc.policy(depth, floor)
	pol.MaxTipAge = 0 // historical blocks: the one rule this harness leaves out
	vf, err := zecspv.VerifyFunding(params, proof, pol)
	if err != nil {
		t.Fatalf("real testnet headers through the gate did not verify: %v", err)
	}
	e.logf("VerifyFunding on testnet transaction %d of %d in block %s (height %d): %d headers read through the gate (%d below, %d above), Equihash, links, the difficulty rule and the Merkle branch all hold; %d confirmations, work %s. Tip age not checked: these blocks are historical.",
		idx, len(ids), bh, b, len(proof.Headers), below, depth, vf.Confirmations, vf.Work)

	// every transaction of that block reaches the header's root; an altered branch does not
	hdr, err := zecspv.ParseHeader(proof.Headers[below])
	if err != nil {
		t.Fatal(err)
	}
	leaves := make([]zecspv.Hash, len(ids))
	for i, id := range ids {
		leaves[i], _ = zecspv.ParseHash(id)
	}
	for i := range leaves {
		br, err := zecspv.MerkleBranch(leaves, i)
		if err != nil {
			t.Fatal(err)
		}
		if err := zecspv.VerifyMerkleBranch(leaves[i], uint32(i), br, hdr.MerkleRoot); err != nil {
			t.Fatalf("transaction %d: %v", i, err)
		}
	}
	var wrong zecspv.Hash
	wrong[0] = 1
	if _, err := GatherFundingProof(ctx, e.zc, b, tip, below, wrong, bh); err == nil {
		t.Fatal("a proof was built for a transaction the block does not hold")
	}
	e.logf("getblock %s 1: %d transaction(s); every one's Merkle branch reaches the header's root %s; a transaction the block does not hold gets no proof", bh, len(ids), hdr.MerkleRoot)
	e.logf("== done at %s: getblock and getblockheader through the testnet rpcgate have the shapes the gate reads, and real testnet headers pass zecspv — plumbing only", time.Now().UTC().Format(time.RFC3339))
}
