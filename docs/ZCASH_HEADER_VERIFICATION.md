# Zcash header verification (`tools/zecspv`)

Phase W item W-02, `MAINNET_TODO.md` MT-C14. Written 2026-09-11 with the package; the wiring into `tools/zecswap` is
W-09's and is not done here.

## 0. In one paragraph

`zecswap` asks its node whether the swap's P2SH funding exists and how deep it is. A node that colludes with the
counterparty can answer "yes, ten deep" for a funding that does not exist, the maker's claim then reveals `s` to it,
and the maker loses his DERO leg (`CROSS_CHAIN_TRUST_MODEL.md` §5). `tools/zecspv` lets the companion check the
answer itself: the funding's transaction id is under the `hashMerkleRoot` of a block header, and that header has
further headers above it, each with a valid Equihash (200, 9) solution, a hash under its target, the target the
difficulty rule gives for that height, and a link to the one below. A node can then lie only by mining the lie. With
the policy in §4 a forged ten-confirmation funding costs at least **8.9 blocks of work at network difficulty** in
the model this package's tests compute, and the package returns the work it verified.

## 1. What it proves

For a run of consecutive serialized headers (`VerifyHeaders`), per the protocol specification (v2026.7.0, §7.6, §7.7):

| Check | Where | Rule |
|---|---|---|
| Encoding | every header | exactly 1,487 bytes: 140 bytes of fields, the compactSize 1344 in its minimal 3-byte form (§7.6 refuses any other encoding), the solution |
| Version | every header | `nVersion` ≥ 4 |
| nBits in range | every header | not negative, zero or overflowing, and not easier than PoWLimit (`2^243 − 1` Mainnet, `2^251 − 1` Testnet), as zcashd's `CheckProofOfWork` |
| Floor | every header | not easier than the caller's `MaxTarget` (§4) |
| Difficulty filter | every header | SHA-256d of the whole header, solution included, read little-endian, ≤ `ToTarget(nBits)` (§7.7.2) |
| Equihash | every header | a valid (200, 9) solution over the first 140 bytes (§7.7.1): 512 distinct 21-bit indices, BLAKE2b-400 personalised `"ZcashPoW" ‖ LE32(200) ‖ LE32(9)`, collisions on each 20-bit chunk at each of the 9 levels, left subtree's first index below the right's, XOR of all 512 strings zero |
| Link | every header after the first | `hashPrevBlock` = the previous header's hash |
| Time after the median | every header with 11 below it | `nTime` > median-time-past (§7.6) |
| Time not too far ahead | every header with 11 below it, from height 2 (Mainnet) or 653,606 (Testnet) | `nTime` ≤ median-time-past + 90 min (§7.6) |
| Local clock | every header, when the caller passes `Now` | `nTime` ≤ now + 2 h (§7.6's rule for full validators) |
| Difficulty adjustment | every header with 28 below it | `nBits` = `ThresholdBits(height)` exactly (§7.7.3): mean of the last 17 targets, median times 17 blocks apart, damping by 4 with truncation toward zero, bounds −16 % / +32 %, 150 s spacing before Blossom and 75 s from it, and Testnet's minimum-difficulty rule (ZIPs 205 and 208) |
| Anchor | when the caller passes one | the header at the pinned height has the pinned hash, with 28 headers below it in the run |

On top of that, `VerifyFunding` proves that a transaction id is in one of those blocks by a Merkle branch to its
`hashMerkleRoot` (Bitcoin's tree, zcashd `consensus/merkle.cpp`: SHA-256d of pairs, the last node paired with itself on
an odd level), that at least `Depth` headers follow the block, that at least `Below` (and never fewer than 28) headers
are under it, and that the newest header is not older than `MaxTipAge`. It returns the summed work of the block and
everything above it (§7.7.5: `floor(2^256 / (target + 1))` per header) and the least work of any one of them.
`VerifyTip` is the same check for a run that ends at the node's tip, with no transaction.

The leaf is the transaction id in internal byte order: SHA-256d of the transaction for v1–v4, the ZIP 244 txid for
v5 and v6 (ZIP 244, "Transaction Identifier"; ZIP 229 for v6). The package does not compute it and does not import
`zecswap`; `zecswap` computes it from the bytes it has already checked and passes it in (§5).

## 2. What it cannot prove

- **That this is the chain everyone else follows.** Headers of valid work prove work, not consensus. A miner, or
  someone renting hash power, can mine a private branch that holds a fake funding. The price of that is §4.
- **Anything a node withholds.** A node can decline to show the funding, the maker's claim, or new blocks. Header
  verification cannot see what is not shown. For the taker this is serious: a node that hides the maker's claim hides
  `s`, and she misses her DERO claim before `T_dero` (`CROSS_CHAIN_TRUST_MODEL.md` §5). Only reading several
  independent sources addresses withholding (MT-C10, `ConfirmFunding` / `BroadcastAll` in `zecswap`).
- **Freshness beyond timestamps.** A node can show a real but stale chain. `MaxTipAge` bounds how old the tip's
  timestamp may be. It cannot tell a slow network from a node that stopped relaying. A stale chain delays a swap. It
  cannot fake a funding, because the funding's script is new for each swap and cannot be in an old block.
- **The height.** Zcash headers carry no height. The caller states the height of the first header, and the package
  uses it only to pick rules (Blossom's spacing, Testnet's minimum-difficulty and max-time heights). A wrong height
  is caught where it changes a rule inside the run (`TestBlossomSpacingMatters`: a run shifted one block down fails
  at Blossom's first block) and not otherwise (shifted one block up, it passes). Proving the height needs the
  coinbase and its branch (BIP 34 height in the coinbase script); that is §10.
- **The block's contents beyond the one leaf.** This is simplified payment verification: whoever mines the forged
  block can put an invalid transaction in it (one that spends coins that do not exist). The cost of mining the block
  is the whole defence, and §4 prices it.
- **Reorganisations.** A real funding can still be reorganised away by a longer branch. That is the confirmation
  policy's job (T3), unchanged.
- **Testnet security.** On Testnet any block whose time is more than six target spacings after its parent's may
  carry PoWLimit (§7.7.3, ZIP 205/208). Such a block costs almost nothing. The recorded Testnet run has three at
  PoWLimit (299188, 299189, 299202), and a floor derived from real Testnet difficulty refuses the real chain
  (`TestVerifyFundingTestnetFloor`). On Testnet run it with the floor at PoWLimit: that tests the plumbing and proves
  nothing about cost.
- **Which network.** Headers do not name their network. Testnet's rules refuse a run of Mainnet headers only at a
  recomputed block more than six spacings after its parent, or where the two networks' Blossom or max-time heights
  differ inside the recomputed part. Both recorded Mainnet runs pass Testnet's rules: neither has such a gap, and
  their recomputed headers are past Blossom on both networks (`TestTestnetRulesOnMainnetHeaders`). The network is
  pinned by the floor (Mainnet difficulty is far above Testnet's), by an anchor, and by `zecswap`'s genesis-hash check
  (`Params.Genesis` carries both genesis hashes).
- **A 64-byte transaction.** A Merkle branch cannot tell a leaf from an inner node whose two children happen to form
  the leaf's preimage. That matters only for a 64-byte v1–v4 transaction (SHA-256d of 64 bytes is how inner nodes
  are hashed); ZIP 244 leaves are BLAKE2b and cannot collide with them. A swap funding pays a P2SH output and is far
  longer than 64 bytes, and `zecswap` parses it whole before computing its id.
- **Position at the odd end.** With duplicate-last, the last leaf of an odd level also verifies at the next index
  (`TestMerkleDuplicationBehaviour`), and `[1..6]` and `[1..6,5,6]` share a root (CVE-2012-2459). Both lists hold only
  real transactions, so inclusion is unaffected. Position is not proven at the odd end, and the swap does not use it.

## 3. The difficulty check: exact, and what exactness buys

The rule is implemented exactly (`Params.NextBits`), following zcashd's `GetNextWorkRequired` /
`CalculateNextWorkRequired` (`src/pow.cpp`), cross-read against Zebra 6.3.0's `AdjustedDifficulty`. It is pinned by:

- **zcashd's own vectors:** the eight `pow_tests.cpp` cases, including the four Blossom ones under
  `RegtestActivateBlossom(true)` (Mainnet's PoW parameters with Blossom at height 0), and the `arith_uint256_tests`
  `SetCompact` / `GetCompact` cases.
- **Real chains:** 106 real headers had their nBits recomputed and matched exactly:
  - Mainnet across Blossom's activation (653600, where the spacing halves and nBits steps from `1c01abbb` to
    `1c02208d`);
  - Mainnet under NU6.3;
  - Testnet from 299160 to 299230, where the minimum-difficulty rule starts and three PoWLimit blocks enter the
    averaging window, so the difficulty collapses from `1e02…` to `2001…–2007…`.
- **Negative cases:** the Blossom height moved by one block; the minimum-difficulty rule switched off, which fails at
  299188; a real Testnet PoWLimit block that would owe the normal difficulty one second inside the gap; nBits off by one.

**What exactness does not give on its own.** The rule is recomputed from the 28 headers below each block, and those
28 are whatever the node sent. For the first 28 headers of any run nothing is recomputed. A forger who controls those can
fill the averaging window with easy targets, and the rule then accepts easy targets above them. Exact difficulty only
becomes a cost when it is anchored to something the forger cannot choose. There are two anchors:

1. **A floor** (`Policy.MaxTarget`): no header may be easier than a target the companion knows the network to be at,
   with slack for a fall in hash rate (`Params.FloorFromBits(bits, factor)`). The floor also bounds the forger's
   descent after the window: the rule lets a target grow at most 32 % above the window's mean per block.
2. **A checkpoint** (`Policy.Anchor`): a pinned (height, hash), for example a line from Zebra's
   `main-checkpoints.txt`, held in the run with 28 headers below it. Its hash fixes every header below it through the
   `hashPrevBlock` links, so from the checkpoint up every nBits is the network's own. A forger can then only fork
   above it, from real history, and the floor no longer matters.

If no floor is set this would all be meaningless, so `VerifyFunding` refuses a nil floor. The caller may pass
PoWLimit deliberately, which is right on Testnet or with an anchor.

## 4. What a lie costs

`TestForgeryCost` (`cost_test.go`) searches for the cheapest header run `VerifyFunding` accepts for a funding the
network never saw, measured in blocks at the honest network difficulty.

**The model:**
- The honest chain runs at Mainnet block 3436171's difficulty (`1c00802e`, the last Zebra 6.3.0 checkpoint), one
  block per 75 s.
- The forger picks the fork point and mines everything from it: the fake funding block, the confirmations above it,
  and any headers below it they replace.
- Their timestamps are as late as the rules allow: median-time-past + 90 min, and never more than 2 h past the
  moment honest miners would have finished.
- Below index 28 their headers sit at the floor. From 28 on, nBits is the rule's, capped at the floor. When the rule
  would be easier than the floor, the model gives them the floor and still the late time, which errs in the forger's
  favour.
- The search over fork points is pruned by an argument written in the test, and checked against an exhaustive search
  where that is cheap (below ≤ 50).

**Cheapest forgery, in blocks of network work** (extract; the full table is in `research/zecspv/tests.log`):

| confirmations | headers below the funding | floor factor | forger's work | where they fork |
|---|---|---|---|---|
| 10 | 28 | 4 | 4.49 | 6 headers below the funding |
| 10 | 28 | 16 | 1.23 | 8 below |
| 10 | 50 | 4 | 8.94 | at the funding block |
| 10 | 50 | 8 | 5.46 | 29 below |
| 10 | 100 | 8 | 8.94 | at the funding block |
| 10 | 100 | 16 | 8.84 | 78 below |
| 10 | 200 | 16 | 8.94 | at the funding block |
| 10 | anchored, no floor | — | 8.94 | at the funding block |
| 20 | 200 | 16 | 15.17 | at the funding block |
| 40 | anchored, no floor | — | 26.00 | at the funding block |

How to read the table:
- **With only 28 headers below**, a forger fakes a few headers just under the first recomputed one, and a ten-deep
  funding costs under half of ten blocks.
- **With 100–200 headers below**, or an anchor, the cheapest forgery forks at the funding block itself and descends
  as fast as the rule allows. Ten confirmations then cost **8.94 blocks** of network work, because the rule's easing
  begins only once the forged timestamps dominate the median. Below six confirmations the forger saves nothing at all.
- The factor is relative to the network's difficulty now, not at the time the floor was set. A floor set when the
  network was easier is looser than its nominal factor.

**What 8.94 blocks is:**
- **In work:** at 3436171's nBits, one block is about 2.2 × 10^12 Equihash solutions (§7.7.5). That is about
  29 GSol/s across the network at 75 s a block, derived from the target, not measured.
- **In time:** forging ten confirmations takes the whole network 11 minutes, a miner with 10 % of the hash rate
  1.9 hours, and one with 1 % 19 hours.
- **In money:** the block subsidy at these heights is 1.5625 ZEC (Blockchair's `chainSupply.valueDelta` for 3436171),
  and funding streams take part of it. A block's hash power therefore earns at most about 1.6 ZEC plus fees, and
  renting it costs about that. A forged ten-confirmation funding costs about **14 ZEC** of forgone or rented mining,
  roughly US$15,500 at the price Blockchair's API reported with the fetch (US$1,110; not a primary source).
- **The consequence:** a swap worth more than that to a colluding miner is not protected by ten confirmations. The
  confirmation count should grow with the value at stake, as the Monero side already plans (U7).

**Recommended policy for Mainnet** (the Zcash companion engineer decides):
- `Depth` = `C_zec` − 1 = 9.
- `Below` = 200, which is 210 headers, 312 KB, and 55–80 ms to verify.
- `MaxTarget` = `FloorFromBits` of a recent pinned nBits × 16. Refresh the pin each release; a stale pin weakens the
  floor as the network grows.
- `Now` = the local clock, and `MaxTipAge` = 1 h.
- Once a header store exists (§9), use an `Anchor` at the release's last Zebra checkpoint, with the floor at PoWLimit.

## 5. How `zecswap` should wire it in

Both gates come before an irreversible step. Neither needs the rest of `zecswap` to change.

**Maker, before `s` leaves the machine:**
- **Where.** In `makerWatchFunding` (`tools/zecswap/swap.go`), after `ConfirmFunding` and before `consumed.Record`,
  `buildClaim` and `save("claiming")`. Again in `sendClaim` before `BroadcastAll`, because a restart in "claiming" is
  a fresh decision to reveal.
- **The leaf.** `ParseTx(frt.Raw).TxID()` for v5/v6, or SHA-256d of `frt.Raw` for v1–v4. These are the bytes
  `SeeFunding` checked for the script and the value; `ComputeTxID` gives the same id in display order. Never take the
  leaf from the node's txid string alone. `zecswap` today computes the id of a v5 transaction only without a Sapling
  bundle, and of a v6 only without shielded parts (`ComputeTxID`'s comment). A funding sent from a shielded wallet
  carries such parts, so it cannot be given a leaf until those digests are added. Until then the maker refuses it,
  and the taker's companion builds a transparent funding.
- **The branch.** `getblock <f.BlockHash> 1` lists the block's txids. Turn them from display order to internal order
  (`zecspv.ParseHash`), find the funding's index, and call `zecspv.MerkleBranch`. The list needs no trust: a wrong
  list does not hash to the header's root.
- **The headers.** From `f.Height − Below` to the tip: `getblockhash h`, then either `getblock <hash> 0` and
  `zecspv.HeaderFromBlock`, or `getblockheader <hash> false`. The first was measured against Zebra 6.3.0 for this
  work. The second returns only the 1,487 bytes, where `getblock` returns whole blocks of up to 2 MB, but it was not
  measured here.
- **The client-facing gate.** `tools/regtest/rpcgate` serves only `zecswap`'s seven methods, so `getblock` or
  `getblockheader` must be added to its allowlist. Both are read-only.
- **The call and the answer.** Call `zecspv.VerifyFunding(zecspv.Mainnet, proof, policy)`. On any error, do not claim.
  Log the error, which names the height and the reason, and try again next step. `ClaimOpen` still ends the attempt at
  the cutoff, and then the DERO is refunded after `T_dero` and `s` never leaves. Log `Funding.Work` and
  `Confirmations` with the claim.

**Taker, before she funds against a lock:**
- **Where.** In `takerFund`, after `ConfirmEscrow` and before `CheckCLTV` / `CheckTakerTimes`.
- **Why.** Her Zcash-side decision is arithmetic on the tip height: `cltv − tip` must be within 96 ± 32, and the times
  must leave her ≥ 3.5 h on DERO after a last-moment claim. A node that shows a tip higher than the real one makes her
  accept a `cltv` that is really later. The maker can then claim the ZEC after her DERO window has closed.
- **The call.** Fetch `Below + 1` headers ending at the tip `chain()` reports. Call `zecspv.VerifyTip`, and use its
  `Height` for the two checks. Showing a higher tip then costs the forger real work, and `MaxTipAge` and the 2-hour
  clock rule bound how far ahead its timestamps can run.
- **What this does not cover.** Her DERO-side read of the lock is DERO's to verify (`ConfirmEscrow`). Her later watch
  for the claim is exposed to withholding, which only several sources address (§2).

**Regtest and Testnet:**
- Zebra's regtest mines with no proof of work (`tools/regtest/README.md`), and the package has no regtest parameters.
  Skip the gate on regtest and say so in the output.
- On Testnet use `zecspv.Testnet` with the floor at PoWLimit (§2).

## 6. API

```go
type Hash [32]byte                          // internal byte order; String / ParseHash use display order
func ParseHeader(b []byte) (*Header, error) // exactly one 1,487-byte header
func HeaderFromBlock(block []byte) (*Header, error)
func (h *Header) Hash() Hash

var Mainnet, Testnet *Params                // §5.3 constants, §7.6 / §7.7.3 heights, genesis hashes
func (p *Params) CheckHeader(h *Header, maxTarget *big.Int) error // version, range, floor, filter, Equihash
func (p *Params) NextBits(height uint32, prev []*Header, candidateTime uint32) (uint32, error) // ThresholdBits
func (p *Params) Target(bits uint32) (*big.Int, error)
func (p *Params) FloorFromBits(bits uint32, factor int64) (*big.Int, error)
func CompactToTarget(bits uint32) (*big.Int, bool, bool); func TargetToCompact(*big.Int) uint32; func Work(*big.Int) *big.Int
func VerifyEquihash(n, k uint32, input, nonce, soln []byte) error

type Options struct{ MaxTarget *big.Int; Now time.Time; Anchor *Checkpoint }
func VerifyHeaders(p *Params, height uint32, raw [][]byte, opt Options) (*Chain, error) // errors are *HeaderError{Height, Err}

func MerkleRoot(leaves []Hash) (Hash, error)
func MerkleBranch(leaves []Hash, index int) ([]Hash, error)
func VerifyMerkleBranch(leaf Hash, index uint32, branch []Hash, root Hash) error

type FundingProof struct{ Height uint32; Headers [][]byte; Block int; TxID Hash; Index uint32; Branch []Hash }
type Policy struct{ Depth, Below int; MaxTarget *big.Int; Now time.Time; MaxTipAge time.Duration; Anchor *Checkpoint }
func VerifyFunding(p *Params, proof *FundingProof, pol Policy) (*Funding, error) // Work, MinWork, Confirmations, hashes
func VerifyTip(p *Params, height uint32, headers [][]byte, pol Policy) (*Tip, error)
```

Every failure wraps a sentinel (`ErrLink`, `ErrAboveTarget`, `ErrDifficulty`, `ErrBelowFloor`, `ErrTimeTooOld`,
`ErrTimeTooFar`, `ErrFuture`, `ErrMerkle`, `ErrDepth`, `ErrContext`, `ErrStale`, `ErrAnchor`, the five Equihash
kinds, …), so callers can use `errors.Is`.

## 7. Test vectors and their sources

Hashes and commits for all of these are in `research/zecspv/sources.log`.

| What | Source | Test |
|---|---|---|
| BLAKE2b | RFC 7693 Appendix A (`"abc"`); `golang.org/x/crypto/blake2b` at every length 0–400 and four digest sizes, unpersonalised | `blake2b_test.go` |
| Equihash, zcashd | `src/test/equihash_tests.cpp` `validator_testvectors`, 10 cases ((96, 5): the valid one and 9 mutations), and `validator_allbitsmatter` (every one of 544 bits flipped) | `TestEquihashZcashdValidatorVectors`, `TestEquihashAllBitsMatter` |
| Equihash, librustzcash (the crate zcashd calls) | `components/equihash/src/test_vectors/{valid,invalid}.rs`: 46 valid solutions ((96, 5) ×26, (200, 9) ×8, (144, 5) ×12), 9 invalid, each failing for the crate's reason (Collision / OutOfOrder / DuplicateIdxs); the minimal encoding from `all_bits_matter`; two 21-bit vectors from `minimal.rs` | `TestEquihashLibrustzcashVectors`, `TestDecodeBitsLibrustzcash` |
| Solution encoding | the spec's §7.7.1 example (`[69, 42, 2^21]` → `[0, 2, 32, 0, 10, 127, 255]`) | `TestEquihashSpecEncodingExample` |
| Real headers | 190 real (200, 9) solutions; tampered solution bits, every header field, swapped and duplicated indices | `TestEquihashRealHeaders`, `TestEquihashTamperedRealHeader` |
| Compact and work | zcashd `arith_uint256_tests.cpp` `bignum_SetCompact`; PoWLimit ↔ `1f07ffff` / `2007ffff` | `target_test.go` |
| Difficulty | zcashd `pow_tests.cpp`, all eight cases; 106 real recomputed nBits | `TestCalculateNextWorkRequiredZcashd`, `TestNextBitsRealChains`, `TestTestnetMinimumDifficulty`, `TestBlossomSpacingMatters` |
| Real chains | Mainnet 653572–653649 and 3436131–3436171 (Blockchair's raw blocks, each run holding a Zebra 6.3.0 checkpoint hash); Testnet 299160–299230 (the local zebrad 6.3.0 Testnet node, holding a checkpoint too) | `TestRealHeaderChains`, `TestAnchor` |
| Merkle | 20 real blocks, whose txid lists hash to their headers' roots: v4 coinbases (Mainnet at Blossom, Testnet), and v5 and v6 coinbases (NU6.3), whose leaves are ZIP 244 ids; every position's branch; a wrong branch, index or leaf | `TestMerkleRealBlocks` |
| Negative cases | tampered solution, wrong nonce, broken link (swapped and missing header), header above its target, nBits off the rule, out of range, below the floor, too old / too far ahead of the median / ahead of the clock, version 3, non-minimal compactSize, wrong Merkle branch / index / leaf / block, too shallow, too few below, no floor, stale tip, wrong anchor | `TestChainNegativeCases`, `TestVerifyFundingNegativeCases`, `TestVerifyTip`, `TestAnchor`, `TestParseHeaderStrict` |
| Funding, end to end | a real transaction in 3436162 with 9 headers above, up to the checkpoint (10 confirmations), and one in 653601 across Blossom | `TestVerifyFundingRealNU63`, `TestVerifyFundingRealBlossom` |

Blockchair is not a primary source. Each Mainnet run is pinned by a hash from Zebra's checkpoint list at its top.
The links fix every header below the pin, and each header's own Equihash and target stand on their own.

## 8. Performance

Measured with `go test -bench . -count 3` on an AMD Ryzen 7 5700X, Go 1.26.2, windows/amd64, single-threaded
(`research/zecspv/bench.log`):

| | per operation |
|---|---|
| one header: difficulty filter + Equihash (`CheckHeader`) | 0.30–0.37 ms |
| a 41-header run, everything (`VerifyHeaders`) | 0.25–0.37 ms per header |
| a ten-confirmation funding proof, 41 headers + branch (`VerifyFunding`) | 10.4–10.9 ms |
| one `ThresholdBits` (`NextBits`) | 2.0–2.2 µs |

Equihash is almost all of it: 512 BLAKE2b compressions per header, after the 140-byte header is absorbed once. The
recommended 210-header proof takes 55–80 ms. An anchored check from the last Zebra checkpoint (3,436,171; about
43,000 blocks behind the tip on 2026-09-11) is about 64 MB of headers and 11–16 s the first time, then only new
headers.

## 9. Dependencies

The package itself uses the standard library only: `crypto/sha256`, `math/big`, `encoding/binary`, `slices`,
`time`, and a few more. `golang.org/x/crypto/blake2b`, already in `tools/go.mod`, cannot set the personalisation
Equihash needs, so BLAKE2b is written in the package, as `tools/zecswap/blake2.go` did, and made incremental. The
tests use `x/crypto/blake2b` as an independent second implementation of unpersonalised BLAKE2b. Nothing new is added
to `go.mod`.

## 10. What remains

1. **The wiring** (W-09), as §5 describes, and `getblock` or `getblockheader` added to `rpcgate`'s allowlist.
   `zecswap`'s ZIP 244 / ZIP 229 ids for transactions with Sapling or Orchard parts, if fundings from shielded
   wallets are to be accepted.
2. **A header store anchored at a checkpoint:** keep verified headers from the release's last Zebra checkpoint (minus
   28), extend them as blocks arrive, and use `Policy.Anchor` with no floor. This removes the floor's dependence on a
   pinned difficulty.
3. **The height:** prove the funding block's height from its coinbase (index 0, branch, BIP 34 height), so a node
   cannot shift heights where no rule notices.
4. **Confirmations by value:** §4's price of a forgery says ten confirmations protect about 14 ZEC against a colluding
   miner. Larger swaps need more.
5. **Testnet around Blossom (583,972–584,030, pinned by checkpoint 584,000):** the local node had not synced that far
   when this was written (it was between 450,000 and 500,000 at 10:09Z). Mainnet's Blossom run already pins the
   spacing switch. Add the Testnet range once the node has it.
6. **Fuzzing** `ParseHeader`, `VerifyEquihash` and `VerifyHeaders` with the rest of MT-G06.
7. **Parallel Equihash** across headers, if the anchored first run's 11–16 s matters.
