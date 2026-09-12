# DMZ: the cross-chain trust model

*2026-09-11, the final mainnet pass, branch `wip/2026-09-09-swap-nfa`. This page answers, for each direction of
value between Monero or Zcash and DERO, who can release, mint, censor, alter, halt, deanonymise, fake or double-count
anything. It then sets out what proving a Monero or Zcash payment actually requires, and whether DERO could check any
of it. Contract lines are those of the working tree on this date: `contracts/htlc.bas` v2, `contracts/swap.bas` v2,
`contracts/board.bas` v3. Related pages:*
- *`DECENTRALIZATION_ARCHITECTURE.md` covers the components and the choice of architecture;*
- *`docs/XMR_SWAP.md` and `docs/ZEC_SWAP.md` are the protocol specifications;*
- *`docs/ATOMIC_SWAP_STUDY.md` has the reasoning behind every design choice.*

**The word "trustless" on this page** means one thing: *no party other than the two people in a swap can take or
freeze either side's coins.* It never means that nothing can go wrong. Every assumption that remains is in §1, by
name.

---

## 0. The four directions, and the one that does not exist

DMZ settles XMR and ZEC by atomic swap. It is not a bridge: no contract on the mainnet set creates a DERO-side unit
that stands for XMR or ZEC. The directive's "deposit → credit → trade → redemption" lifecycle therefore takes this
form:

| Direction | What the person does | The protocol |
|---|---|---|
| **ZEC → DMZ** | a transparent-ZEC holder takes an offer on `board.bas`, and receives a DERO-side asset (DERO, a token, an NFA unit) | `board.bas` Accept → the maker's `swap.bas` Lock → the taker funds a P2SH script → the maker claims with `s` → `swap.bas` Claim pays the taker |
| **DMZ → ZEC** | a DERO-side holder posts an offer and receives ZEC | the same protocol, from the maker's side |
| **XMR → DMZ** | an XMR holder (Alice, the taker) receives a DERO-side asset | `htlc.bas` Lock by Bob → Alice's XMR to the joint address → Bob's gate secret → Alice's Claim, which publishes `s_a` |
| **DMZ → XMR** | a DERO-side holder (Bob, the maker) receives XMR | the same protocol, from Bob's side: he sweeps the joint address with `s_a + s_b` |
| *XMR or ZEC → wrapped units* | *not on mainnet* | `vault.bas` (a federation) is not deployed: `deploy/networks.json` `mainnet.vault` is `""` and `vaults` is `null` |

Between the swaps, trading on DERO (pools, launches, NFAs) is ordinary DERO contract execution. It needs nothing from
another chain.

---

## 1. The minimum trust assumptions

These are all of them. Nothing else is trusted: no operator, signer, relayer, oracle, API, indexer or server.

| # | Assumption | Held by | If it fails |
|---|---|---|---|
| T1 | **The cryptography holds.** SHA-256 preimage resistance (every hashlock); the discrete log on ed25519 (the Monero key shares) and on secp256k1 (the Zcash spend keys); **the soundness of the Bulletproofs proof in `research/crosschain/poc/proof-xmr`**, which has not been audited outside this project | everyone | a broken hash lets a stranger claim; an unsound proof means a counterparty can reveal a value that hashes correctly but is not their key share, and the other side's XMR is locked for good |
| T2 | **DERO executes the contracts as written, and never reorganises deeper than 8 blocks** (`STABLE_LIMIT`, `vendor/derohe/config/config.go:77`, a consensus rule) | everyone | out of scope: DERO consensus failing |
| T3 | **Monero and Zcash do not reorganise deeper than the confirmation policy** (Monero 20 by default and never below 10; Zcash 10). **Neither chain has a consensus bound.** Zebra finalises blocks more than 1,000 deep, and that is local node policy, not consensus (`MAX_BLOCK_REORG_HEIGHT = 1000`, `zebra-chain/src/parameters/constants.rs`) | whoever receives on that chain | the receiver loses: §4 |
| T4 | **DERO includes an exit within its margin.** Alice's Claim within the claim cutoff (16 blocks before `t1`, and G with U2); the maker's Refund within `[t1, t2)`. The mempool is ordered by fee per byte (`vendor/derohe/blockchain/mempool/mempool.go:377`) | the party sending | censoring Alice's Claim until `t1` lets Bob take both legs (attack A, `docs/ATOMIC_SWAP_STUDY.md` §2.1); the cost is the fee on that block space for that many blocks, or a mining majority |
| T5 | **Zcash includes the taker's refund between `cltv` and the maker's spend** | the Zcash taker | the claim branch of the script never expires, so a maker who has refunded the DERO can later spend the ZEC with `s` (Z17). The pre-signed refund lets anyone broadcast it |
| T6 | **Each party's own view of each chain is honest.** The DERO node, Zcash node and Monero daemon each party reads | each party, for itself | **total loss of that party's leg**, when the lying source colludes with the counterparty (§5) |
| T7 | **Each party's companion is what its source says** (`tools/xmrswap`, `tools/zecswap`) | each party | a malicious build can hand over the party's key share or secret. Mitigated by reproducible builds (`tools/release`); who publishes them is operator decision 14 |
| T8 | **Each party is present at its window**, or has a machine or delegate that is: the Monero maker in `[t1, t2)`; the Zcash taker from `cltv` | each party | the Monero maker loses the locked asset to Punish; the Zcash taker loses the ZEC (Z17), unless someone broadcasts her pre-signed refund |
| T9 | **The page or command line that names the contract id** is honest | each party | a look-alike contract. The companions compare the installed code with canonical `htlc.bas` before trusting it (U4); the app is covered in §6 |
| T10 | **A little DERO for fees** exists in the receiving wallet, because every DERO transaction pays its fee from a DERO payload (`vendor/derohe/blockchain/transaction_verify.go:265-276`) | each party | the party cannot send the transaction that pays them until they hold DERO (Path 0, `docs/ATOMIC_SWAP_STUDY.md` §2.0.1) |

---

## 2. The directive's questions, direction by direction

### 2.1 The Zcash market (`board.bas` v3 + `swap.bas` v2 + `tools/zecswap`)

| Question | Answer | Where |
|---|---|---|
| Who can cause funds to be released? | **DERO leg:** anyone who presents `s` before expiry, and the payment goes only to the stored taker; after expiry only the maker, by Refund. **ZEC leg:** the maker, with `s` and his key (the claim branch); the taker, with her key, after `cltv` (the refund branch). Nobody else | `swap.bas:70-93`, `:96-112`; `tools/zecswap/script.go` (the redeem script: `OP_SIZE 32 OP_EQUALVERIFY` on the claim branch, `OP_CHECKLOCKTIMEVERIFY` on the refund branch) |
| Who can cause tokens to be minted? | **Nobody.** No contract represents ZEC | the mainnet record names no vault |
| Who can falsely claim a deposit? | No contract accepts a claim that a deposit happened. A party's **own data source** can show that party a funding or a lock that does not exist (§5) | — |
| Who can censor a withdrawal? | There is no withdrawal, only exits. DERO miners can delay a Claim or Refund; Zcash miners can delay a claim or refund. Each exit has a window, so censorship past a window moves value (T4, T5). No operator can censor | — |
| Who can alter verification logic? | **Contracts: nobody.** None calls `UPDATE_SC_CODE`, and a DVM contract cannot change its own code any other way. **Companion: its publisher.** **The app: whoever can update the TELA INDEX** (§6) | `swap.bas`, `board.bas` in full |
| Who can upgrade the contract? | **Nobody.** A fix is a new contract at a new id | — |
| Who can change settlement addresses? | After Accept and Lock, nobody. The taker is fixed in `t_<id>`; the maker's ZEC key hash and the taker's refund key hash are inside the P2SH script, which the funding commits to | `board.bas:76-80`, `swap.bas:51-58` |
| Who can steal bridge reserves? | **There are none.** Each lock holds only its own swap's value | — |
| Who can halt the system? | **Nobody.** No contract has a pause. A DERO network halt stops DERO-side exits until it resumes, while Zcash heights keep advancing (§4) | — |
| Who can deanonymise users? | Anyone reading both chains. `H` appears on `board.bas`, on `swap.bas` and in the P2SH script, and `s` in both claims. That links the maker's and taker's transparent Zcash addresses to the ring-2 DERO addresses that signed Post, Accept, Lock and Claim. Stated before anything else in `docs/ZEC_SWAP.md` §1. The operator of any node a party uses also sees that party's IP address and queries | `docs/ZEC_SWAP.md` §1 |
| Who can manipulate confirmation state? | The party's **own data source** (§5); Zcash miners, by reorganising | — |
| Who can exploit a reorganisation? | A Zcash mining majority that reorganises deeper than 10 blocks after the maker has claimed. The funding vanishes, `s` is already public, and the taker refunds her ZEC and claims the DERO with `s`: **the maker loses both legs** (Z5). DERO cannot reorganise deeper than 8, and the companions act only on DERO state that deep | `docs/ZEC_SWAP.md` §9 Z5, Z6 |
| Who can double-credit a deposit? | **Nobody.** Nothing is credited. Each lock's `st_<id>` moves from 0 exactly once (`swap.bas:23, 40, 101, 104`). The P2SH output can be spent once (Zcash consensus). A companion refuses a hash it has already used for another swap (`tools/zecswap/state.go:287-343`, `ErrHashReused`) | as cited |
| Who can double-spend a withdrawal? | **Nobody**, for the same reasons | as cited |

### 2.2 The Monero swap (`htlc.bas` v2 + `tools/xmrswap`)

| Question | Answer | Where |
|---|---|---|
| Who can cause funds to be released? | **DERO leg:** the taker, before `t1`, with `s_a` and the gate secret `k_b` (Claim); the maker, in `[t1, t2)`, with `s_b` (Refund); **anyone**, from `t2`, and the payment goes only to the stored taker (Punish). **XMR leg:** whoever holds `s_a + s_b`, which is Bob once Claim publishes `s_a`, or Alice once Refund publishes `s_b`. After Punish, Alice's XMR stays under the joint key unless Bob ever reveals `s_b` | `htlc.bas:69-93`, `:97-120`, `:125-141` |
| Who can cause tokens to be minted? | **Nobody** | — |
| Who can falsely claim a deposit? | No contract accepts a claim that a deposit happened. A party's own data source can (§5) | — |
| Who can censor a withdrawal? | DERO miners can hold Alice's Claim out past `t1` (T4). Monero miners can delay Alice's XMR lock, which ends in a Refund, at the cost of time | — |
| Who can alter verification logic? | Contracts: nobody. The companion and the proof verifier: their publisher. The app: the TELA INDEX (§6) | `htlc.bas` in full |
| Who can upgrade the contract? | **Nobody** | — |
| Who can change settlement addresses? | Nobody after Lock. The maker `m_<id>` is the Lock's signer; the taker `t_<id>` is fixed at Lock; the joint Monero address is fixed by `S_a + S_b` | `htlc.bas:51-61` |
| Who can steal bridge reserves? | **There are none** | — |
| Who can halt the system? | **Nobody** | — |
| Who can deanonymise users? | On DERO, the maker, the taker, the amount, the three hashes, `s_a` (after Claim) and `s_b` (after Refund) are public. The maker holds the joint address's private view key, so a dominant maker sees every taker's XMR amount and timing. On Monero the joint address looks like any other; only timing ties it to the DERO lock | `docs/XMR_SWAP.md` §5, §6 |
| Who can manipulate confirmation state? | The party's own data source (§5); Monero miners | — |
| Who can exploit a reorganisation? | A Monero mining majority that reorganises Alice's XMR lock away after Bob has released the gate. She can still Claim the DERO, and Bob finds nothing to sweep: **the maker bears the Monero reorganisation risk** (R3). The largest Monero miner observed in 2025 averaged 23.38 % of blocks, and 28.33 % in its selfish-mining periods ([arXiv 2512.01437](https://arxiv.org/html/2512.01437)) | `docs/XMR_SWAP.md` §9 R3 |
| Who can double-credit a deposit? | **Nobody.** `st_<id>` moves from 0 once (`htlc.bas:74, 84, 103, 111, 130, 132`). A Monero output can be spent once (its key image). The companion refuses a secret it has generated before (`tools/xmrswap/state.go:245-300`, `UsedSecrets`; `TestRefusesReusedSecret`) | as cited |
| Who can double-spend a withdrawal? | **Nobody**, for the same reasons | as cited |

### 2.3 Summary

"The counterparty" means the other person in the same swap, acting only through the protocol's own exits.

| Question | ZEC → DMZ | DMZ → ZEC | XMR → DMZ | DMZ → XMR | *Federation (not deployed)* |
|---|---|---|---|---|---|
| Release funds | the counterparty, through the exits; the timelocks | same | same | same | *k of n signers* |
| Mint | nobody | nobody | nobody | nobody | *k of n signers, against nothing if they wish* |
| Falsely claim a deposit | your own data source, to you | same | same | same | *k of n signers* |
| Censor | miners, bounded by the windows | same | same | same | *any one signer can strand a marked withdrawal (LIV-03)* |
| Alter verification logic | nobody (contracts); the companion's publisher | same | same | same | *the signers' own software* |
| Upgrade a contract | nobody | nobody | nobody | nobody | *nobody (`vault.bas` is sealed)* |
| Change settlement addresses | nobody after lock | same | same | same | *the signers, on the foreign chain* |
| Steal reserves | none exist | none exist | none exist | none exist | *k of n signers* |
| Halt | nobody | nobody | nobody | nobody | *a halt lease that expires* |
| Deanonymise | anyone: `H` and `s` link the chains; your node's operator | same | the maker (view key); anyone on DERO | same | *the signers see every deposit* |
| Manipulate confirmations | your own data source; Zcash miners | same | your own data source; Monero miners | same | *the signers* |
| Exploit a reorganisation | a Zcash majority, deeper than 10 (the maker loses) | same | a Monero majority, deeper than the policy (the maker loses) | same | *a majority deeper than the signers' depth* |
| Double-credit, double-spend | nobody | nobody | nobody | nobody | *k of n signers* |

---

## 3. What proving a payment requires, and whether DERO can check it

### 3.1 Zcash (transparent; the market DMZ runs)

| Property | What proves it | What checking it needs | Can the DVM do it? | What DMZ does instead |
|---|---|---|---|---|
| The transaction exists and is in a block | its id (for v5, the ZIP 244 digest, a tree of BLAKE2b-256 personalised hashes; for v4, double SHA-256 of the serialisation) and a Merkle branch to the header's `hashMerkleRoot` | BLAKE2b with a personalisation string; SHA-256d; parsing v4 and v5 transactions | **no.** The DVM has SHA-256, SHA3-256 and Keccak-256, but not BLAKE2b (`vendor/derohe/dvm/dvm_functions.go:58-94`) | the companion asks its own node (`getrawtransaction`, `getblock`) |
| The block is valid work | Equihash (n = 200, k = 9) over BLAKE2b; difficulty from the DigiShield averaging window (ZIP 208 changed the targets at Blossom) | BLAKE2b and 512 index collisions per header; 256-bit targets | **no**: no BLAKE2b, uint64 arithmetic only | the node |
| Confirmation depth | headers built on top, each valid work | the same, per header | **no** | the node's `confirmations`, checked against 10 |
| Value and destination | the output's value, and a `scriptPubKey` equal to `OP_HASH160 HASH160(redeem script) OP_EQUAL` | parse the output; HASH160 = RIPEMD-160(SHA-256) | the DVM has no RIPEMD-160, and could not tie the bytes to the chain anyway | the companion checks the exact outpoint, value and script |
| Not already spent | membership of the UTXO set, which no block header commits to | a full UTXO set | **no, and no succinct proof exists** | the node's `gettxout`. The swap's safety does not depend on it: a spent funding cannot be claimed twice |
| Reorganisation | none in consensus | — | — | a policy of 10 confirmations (T3) |
| A shielded (Orchard) payment | trial decryption with a viewing key, the note commitment in the tree, a Halo 2 proof | Pallas/Vesta arithmetic, Poseidon, Halo 2 verification | **no** | not supported. The transparent market is the only Zcash path, and its lack of privacy is stated first |

**Current Zcash facts that bear on this:**
- **zcashd is gone.** It reached its automatic end of support on 2026-07-18 at height 3,417,100, refuses to restart,
  and does not support NU6.3 ([zcashd book](https://zcash.github.io/zcash/user/end-of-life.html)). The node is Zebra.
- **NU6.3** (ZIP 258) activated at mainnet height 3,428,143 with consensus branch id `0x37A5165B`. It adds the v6
  transaction format (ZIP 229) and leaves v5 valid ([ZIP 258](https://zips.z.cash/zip-0258)).
- **Branch ids.** `zecswap` signs v5 transactions and knows every branch id through NU6.3
  (`tools/zecswap/repair_test.go:113-126`). It reads the branch id from the node's `consensus.nextblock` and refuses to
  sign under one it does not know.
- **v6 is what current wallets build.** Measured on real Zebra 6.3.0 in regtest (`tools/regtest/README.md`): Zebra
  mines v6 coinbases, and librustzcash's builder produces v6 by default under NU6.3. So the counterparty's claim that
  reveals `s` may be a v6 transaction. A watcher that reads only v5 would never see the secret, and the taker would lose
  her DERO leg to the maker's refund. `zecswap` now reads versions 1–6 and builds v5 or v6. It stops on
  anything at the script address it cannot read. On real Zebra, a v6 claim was mined and the taker's watcher read the
  secret from it (`MAINNET_TODO.md` MT-C16). Reading the preimage needs no trust in the transaction's format, because
  the companion checks it against `H`.

### 3.2 Monero

| Property | What proves it | What checking it needs | Can the DVM do it? | What DMZ does instead |
|---|---|---|---|---|
| The transaction exists and is in a block | its hash (Keccak over the prefix and the RingCT parts) and the block's transaction tree hash in the header | Keccak (original, pre-SHA-3 padding) | the DVM has `KECCAK256`, but that alone proves nothing without the header's validity | the party's own `monerod`, read by `monero-wallet-rpc` |
| The block is valid work | RandomX | a 256 MiB cache per key epoch (or a 2 GiB dataset) and the RandomX virtual machine | **no, and no contract on any chain does this economically** | the party's own `monerod` |
| Confirmation depth | headers on top | the same | **no** | the wallet's unlocked balance (10 blocks), and the companion's depth policy (20 by default) |
| The output belongs to an address, and the amount | the transaction private key `r` or the recipient's private view key `a`; derivation `D = 8·r·A`; output key `P = H_s(D‖i)·G + B`; the amount decrypted from `ecdhInfo` and checked against the commitment `C = x·G + a·H` | ed25519 scalar multiplication and point addition | **no**: no curve arithmetic in the DVM | Bob's view-only wallet on the joint address, built from the joint view key |
| Spend state | the key image `I = x·H_p(P)`, computable only with the spend key; whether it is spent is membership of the global key-image set | the full set | **no, and no succinct proof exists** | not needed: the joint output is spent only by `s_a + s_b` |
| Reorganisation | none in consensus | — | — | a policy of 20 confirmations by default, never below 10 (T3) |

**Does Monero have a practical trust-minimised light client? No.**
- **Monero wallets do not check proof of work.** A remote `monerod` can show a wallet a payment that is not on the chain.
- **Light-wallet servers need the view key.** Servers such as `monero-lws` scan on the user's behalf only if given it,
  which hands them the user's incoming history.
- **For DMZ this means** the one trust-minimised option is each party's own `monerod`.
- **Current Monero facts:**
  - The current release is 0.18.5.1 ([getmonero.org](https://www.getmonero.org/downloads/)).
  - The FCMP++/Carrot hard fork, which will change the transaction and address layer, had no mainnet height as of
    2026-09-11 (the [milestone](https://github.com/monero-project/monero/milestone/1) is open).
  - `xmrswap` must be re-verified against that release before it is used after the fork (`MAINNET_TODO.md` MT-B17).

### 3.3 Conclusion

Neither chain's payment can be verified by a DERO contract, and the reason is not cost. The DVM lacks BLAKE2b,
RandomX, RIPEMD-160, curve arithmetic and signature checks, and every call is capped at 20,000 storage gas. So the
choice is between:
- **(a) trusting someone to tell the contract what happened** on the other chain: an oracle, a relayer or a
  federation. Each is a party who can mint or release against nothing.
- **(b) never asking the contract to know.** Each person's own software checks the other chain, and the contract
  checks only a hash, a height and a signer.

DMZ does (b), on every mainnet path. The trust left in (b) is each party's trust in its own view of the chains (T6),
which §5 prices.

---

## 4. Event identities, replay, and reorganisation

**The unique identity of every event:**

| Chain | Event | Identity | Why it cannot be replayed |
|---|---|---|---|
| DERO | an escrow | the contract's SCID and the lock id `id`, from the contract's own counter `n` (`htlc.bas:50, 61`; `swap.bas:52, 58`) | `st_<id>` moves from 0 once, and every exit checks it first. A SCID exists on one network only, so a testnet lock names nothing on mainnet. Contract calls are DERO transactions, which cannot be replayed (a transaction is valid for 11 blocks, `vendor/derohe/blockchain/miner_block.go:43`, and each spends its own ring inputs) |
| DERO | an offer | `board.bas` SCID and `id` | `st_<id>`: one Accept, one Cancel, no reopening (`board.bas:72-76`, `:92-97`) |
| Zcash | a swap's funding | `(network, txid, vout)` of the P2SH output, whose script is unique to the swap (`H`, both key hashes, `cltv`) | Zcash consensus spends an output once. A testnet outpoint does not exist on mainnet, and the addresses differ by prefix (`t2…` against `t3…` for P2SH). `H` is fresh per offer, and a companion refuses one it has used (`ErrHashReused`) |
| Zcash | a claim or refund | the spending transaction's id (ZIP 244, not malleable) | only one transaction can spend the funding |
| Monero | a swap's lock | `(txid, output index)` at the joint address, which is fresh per swap (fresh `s_a`, `s_b`) | the key image prevents a second spend. The companion refuses a reused secret (`UsedSecrets`) |

**The confirmation policy:**

| Chain | MIN_CONFIRMATIONS | Reorganisation policy | Finality | Rollback |
|---|---|---|---|---|
| DERO | **8** (`STABLE_LIMIT`, consensus) | the companions act only on state at least 8 deep | final at 8 by consensus | none is needed past 8. Before 8, companions re-read, and the app labels a result "included", not "confirmed" |
| Monero | **20** by default; never below 10 (the 10-block spend age; `tools/xmrswap/cmd/xmrswap/main.go` refuses less); tiers upward by value are planned (U7) | no consensus bound. A reorganisation past the gate is borne by the maker (R3) | probabilistic | the maker's companion re-checks the lock before releasing the gate. After release there is no rollback: that is the risk T3 names |
| Zcash | **10** (`docs/ZEC_SWAP.md` §5) | no consensus bound. Zebra finalises past 1,000 blocks (node policy) | probabilistic | the maker claims only at 10 or more. A funding that later vanishes leaves the maker with neither leg (Z5); T3 names it |

**Cross-chain time.** Both swaps count in block heights, never in wall-clock time. If DERO halts:
- the foreign chain's heights keep advancing while DERO's do not;
- on the Zcash market the taker's `cltv` can pass while `T_dero` stands still. She refunds her ZEC, and the maker's DERO
  stays locked until DERO resumes. Nobody loses coins; the maker loses time;
- on the Monero swap nothing on DERO moves until it resumes. Alice's XMR stays in the joint address, and the exits
  resume with DERO.

---

## 5. The data-source attacks: what T6 costs

Every party must read the other chain, and DERO, from somewhere. When that somewhere lies, and colludes with the
counterparty, the result is not a delay. It is the loss of a whole leg:

| Who is lied to | The lie | What the colluding counterparty then does | Loss |
|---|---|---|---|
| **Zcash maker** | his Zcash source shows a P2SH funding that does not exist, at full depth | the maker's companion broadcasts its claim through that source, which reveals `s` to it. `swap.bas` v2 Claim pays the stored taker whoever sends it, so the colluder completes the taker's DERO leg | **the maker's DERO-side asset**, and the taker never funded |
| **Zcash taker** | her DERO node shows a `swap.bas` lock that does not exist | she funds the P2SH; the maker claims the ZEC with `s` before `cltv`; her DERO Claim finds no lock | **the taker's ZEC** |
| **Monero taker (Alice)** | her DERO node shows an `htlc.bas` lock that does not exist | she locks XMR; Bob releases the gate; her Claim, sent through the lying node, reveals `s_a`, and Bob sweeps | **Alice's XMR** |
| **Monero maker (Bob)** | his `monerod` shows XMR at the joint address, deep enough, that is not on the chain | he releases the gate; Alice Claims the DERO; Bob sweeps nothing | **Bob's DERO-side asset** |

`docs/ZEC_SWAP.md` Z14 and Z15 describe the first two as costing time. That is true only when the liar is not the
counterparty's accomplice, and the documents are corrected to say so (`MAINNET_TODO.md` MT-I11).

**The Zcash maker's row, with `-verify-headers`.** Before the companion reveals `s`, the funding's block and every
header above it must carry real Equihash work (`tools/zecswap/spv.go`, `tools/zecspv`). A faked funding then costs real
mining: about 8.94 blocks of network work, roughly 14 ZEC, for ten confirmations under the shipped policy (Depth 9, 200
headers below, floor nBits `1c00802e` × 16, newest header at most 1 h old), against 4.49 blocks with only the 28 headers
the difficulty rule needs (`docs/ZCASH_HEADER_VERIFICATION.md` §4). It proves work, not the network's consensus (§2
there). The gate is off by default until it has run against the Zcash testnet node.

**What reduces T6, strongest first:**
1. **Run your own nodes.** This is the companions' default, and the trust statement's.
2. **Check the proof of work yourself.** For Zcash, check Equihash and the Merkle path before revealing `s` or funding.
   This is built into `tools/zecswap` behind `-verify-headers`, with the cost of a lie as measured in
   `docs/ZCASH_HEADER_VERIFICATION.md` §4 (above). It is not built for Monero (`DECENTRALIZATION_ARCHITECTURE.md` §7).
3. **Read from several independent sources, and act only when they all agree.** Being built into `zecswap` in this
   pass (MT-C10), with the same for the DERO read before an XMR lock (MT-B09). This reduces "one liar" to "every
   source colludes". **It does not remove the trust**, and the companion says so.

A single remote source is disclosed as able to cause a total loss that nothing in the protocol can detect (operator
decision 16).

---

## 6. The page that names the contracts (T9)

The app is TELA documents on DERO, listed by one TELA INDEX. Whoever can update the INDEX can change what the app
shows, including which contract ids it offers.
- **The companions do not rely on the app.** They take contract ids by flag, and compare the installed code with the
  canonical source before trusting it (U4, `docs/ATOMIC_SWAP_STUDY.md` §2.1).
- **For the app,** `MAINNET_DEPLOYMENT.md` records the INDEX's final state, and `tools/verifydeploy` checks both the
  installed code and whether any key can still update it.

A person who does not trust the page can call every contract from their wallet directly. The page is a convenience,
not a party to settlement.
