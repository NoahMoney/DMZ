# DMZ: security threat model

*2026-09-11, the final mainnet pass; updated on 2026-09-12 for `htlc.bas` v3 (X-20). This page covers what is being protected, who
attacks it, where they can reach, what stops them, and what happens in every failure the directive lists. The finding
lists are elsewhere:*
- *`docs/audit/FINDINGS_2026-09-10.md`: 125 findings, most reproduced;*
- *`MAINNET_GAP_ANALYSIS.md`: this pass's sweep;*
- *`CROSS_CHAIN_TRUST_MODEL.md`: the trust assumptions T1–T10 and the data-source attacks.*

---

## 1. What is protected

| Asset | Where it lives | Who may move it |
|---|---|---|
| A DERO-side asset in a Monero-swap escrow | `htlc.bas` storage and balance | the four exits only: Claim, Refund, Punish in [`t2`, `t2 + g2`), and Reclaim (`htlc.bas:99-214`) |
| A DERO-side asset in a Zcash-swap escrow | `swap.bas` | Claim (paid to the stored taker) and Refund (`swap.bas:78-122`) |
| XMR at a swap's joint address | Monero | whoever holds `s_a + s_b` |
| ZEC at a swap's P2SH output | Zcash | the maker with `s`, or the taker after `cltv` |
| Pool reserves and LP shares | `pool.bas` | swaps under `min_out`; liquidity providers, for their own share |
| Bonding-curve reserves, taxes, stakes | `launch.bas` | its entrypoints |
| NFA units, standing bids, owed shares | `nfa.bas` | the owner; the settlement; `PayOwed`, which pays only the named payee |
| Founders Pass deposits and rewards | `founders.bas` | registered holders, by its entrypoints |
| Keys | DERO wallet; Monero wallet; the Zcash spend key given to `zecswap`; each swap's key share (`xmrswap` state file) and secret (`zecswap` state) | their owner |
| Privacy | the linkage between chains and between addresses | — |
| The integrity of what the app shows | TELA documents; the node the app reads | — |

---

## 2. Adversaries

| # | Adversary | Can | Cannot |
|---|---|---|---|
| A1 | **The counterparty** in a swap | walk away before or after locking; choose timings within the protocol's bounds; withhold a secret; front-run in the mempool | take the other side's coins through any exit (`CROSS_CHAIN_TRUST_MODEL.md` §2) |
| A2 | **Any stranger** | call every public entrypoint of every contract with any arguments, at any ring size, with any value attached; post junk offers, locks and messages at the gas price | pass a check that needs a preimage, a height or a signer they do not have |
| A3 | **DERO miners** | order by fee per byte (`mempool.go:377`); delay a transaction; with a majority, censor for as long as they hold it | reorganise past 8 blocks (consensus); forge a signer |
| A4 | **Monero or Zcash miners** | reorganise; delay | spend an output without its key |
| A5 | **The operator of a data source** a party reads | lie about the chain; learn the party's IP address and queries; collude with the counterparty | anything, against a party that reads only its own node |
| A6 | **Whoever can update the TELA INDEX** | change what the app shows, including contract ids | move a coin; the companions check contract code themselves |
| A7 | **The companion's publisher** (supply chain) | ship a build that leaks keys | against a party who builds from source, or checks the reproducible build's hash (`tools/release`) |
| A8 | **Malware on a user's machine** | read state files and wallets | — (outside any protocol's reach; the state files are marked as secrets to back up) |
| A9 | **A network observer** | see IP addresses, timing and volumes of traffic to nodes | read XSWD (localhost) |
| A10 | **The operator, before revocation** | set the factory's treasury and regfee at its install, for its life (v2 has no owner); install the Founders Pass with its parameters; publish the app | take a coin from any escrow, pool or NFA. After the installs and the ring-16 INDEX step in `MAINNET_DEPLOYMENT.md`, nothing |

---

## 3. Attack surface

- **Contracts.** Every exported function (capitalised name) of `pool`, `launch`, `nft`, `nfa`, `founders`, `factory`,
  `social`, `chat`, `htlc`, `swap` and `board`, callable by anyone. The DVM has no cross-contract calls, so no call
  can re-enter another contract. Storage gas is capped at 20,000 per call, and every call pays for its gas.
  `tests/parse_test.go` pins each contract's entrypoints.
- **Companions:**
  - every response from `monero-wallet-rpc`, `monerod`, Zebra and `derod`;
  - their command-line flags;
  - their state files;
  - for the Monero swap, the rendezvous directory the two parties share.
- **The app:**
  - every response from the node the user names;
  - XSWD responses from the local wallet;
  - the URL fragment (contract ids and addresses only, `app/ui.js:154`);
  - contract storage written by strangers: names, descriptions, messages. The app sanitises and flags it
    (`tests/js/sanitize.test.mjs`).
- **Deployment tooling:** `simdeploy`, `genconfig`, `telainstall`, `verifyvendor`. Each refuses mainnet without `-yes`.

---

## 4. Threats and what stops them

| Threat | Where | Stopped by | What is left |
|---|---|---|---|
| uint64 overflow in a price, a fee or a share | pool, launch, nfa, founders | bounds checked before every product (`RISKS.md` R-1; `nfa.bas` note 4); `tests/zzdvmarith_test.go`, `zzextreme_test.go`, `uint128_test.go` | none known |
| A transfer sent before a failure exit, which DERO does not roll back | every contract | every send comes after every check (`tests/sendorder_test.go`, `TestNoExternalSendBeforeAFailureExit`) | none known |
| An unregistered payee destroys a settlement (the DVM keeps only the last registry error, then panics mid-list; LIV-01/DVM-1) | nfa (repaired: `PayOwed`); any contract that pays several addresses in one call | `nfa.bas` pays only signers in a settlement and owes everyone else; the treasury and other contracts pay one address per call | none known: a static check reads every contract and fails one that pays an address not known to have signed before its last transfer (`tests/sendorder_test.go` `TestNoUnverifiedPayeeBeforeTheLastTransfer`, MT-A10) |
| A refused call loses its value when above ring 2 (`transaction_execute.go:395-404`) | every contract | the app and the companions call at ring 2; the `...To` wrappers are the ring-16 exceptions and carry no refund risk | a user who calls by hand above ring 2 can lose attached value |
| The Monero Claim is withheld until `t1` (attack A) | `htlc.bas` + DERO miners | the claim cutoff; the gate deadline G (U2) | priced, not closed (T4) |
| A secret front-run in the mempool | `swap.bas` Claim; `htlc.bas` Claim | swap v2 pays the stored taker whoever sends it. The `htlc` Claim is taker-only, and `s_a` in it helps only Bob, who already knows the swap | none |
| An abandoned lock frozen for ever (LIV-07) | `htlc.bas` | v3: anyone may Punish in [`t2`, `t2 + g2`), and it pays the stored taker; from `t2 + g2` the maker Reclaims, revealing `s_b`, from which the taker recovers her XMR | the taker's Punish deadline, `t2 + g2`. If nobody punishes in time and the maker never reclaims (hostile, or he lost his state or `s_b`), his DERO and any XMR she locked stay locked for ever. New in v3: v2's Punish had no end. Anyone may send the Punish for her |
| Offer freezing (ADV-PRICE-07) | `board.bas` | v3: an accepted offer is dead at `ad_<id>` (accept height + 200) | the griefer pays one Accept fee per 200 blocks |
| A stale bid settles a later listing (NFA-1) | `nfa.bas` | every end of an auction settles or refunds its bid | none known |
| A look-alike contract or token | app; companions | the app checks template hashes and flags impersonating names (APP-1). The companions compare installed code with canonical source (U4) | an asset leg's token identity must be shown by the companion (U1) |
| A lying data source (A5) | companions | own nodes by default; reads from several sources that must all agree (MT-B09, MT-C10) | T6, `CROSS_CHAIN_TRUST_MODEL.md` §5 |
| The app republished to lie (A6) | TELA INDEX | the INDEX's final state (`MAINNET_DEPLOYMENT.md`); `tools/verifydeploy` | until the INDEX step is done, the publisher key is trusted |
| A malicious companion build (A7) | release | reproducible build; hashes printed in the repository | who signs releases (operator decision 14) |
| An unsound SHA-256-to-ed25519 proof | `proof-xmr` | a Bulletproofs R1CS circuit over dalek's `bulletproofs` 5 with the `yoloproofs` feature, the flag under which dalek ships its **unstable, unaudited** R1CS API, from a patched local copy (`research/crosschain/poc/proof-xmr/Cargo.toml`) | **the largest trust item on the Monero path.** An external audit is a mainnet gate (MT-G03) |
| Spam | chat, board, htlc, swap | every call pays gas; chat charges a fee; strings have length caps (`board.bas:40`) | storage grows (`RISKS.md` R-5) |
| **A v6 claim hides the secret from the taker's watcher.** NU6.3 made v6 transactions valid, and current Zcash wallets build them by default. A watcher that reads only v5 misses the maker's claim, the taker never learns `s`, and the maker refunds the DERO | `zecswap` | zecswap reads versions 1–6, and stops on anything at the address it cannot read (MT-C16). Found by running against real Zebra, not by the fakes | **closed**: a real v6 claim was mined on Zebra and the taker's watcher read the secret |
| **The wallet server trusts whatever keys it is given.** Real `monero-wallet-rpc` 0.18.5.1 accepts wrong view or spend keys in `generate_from_keys` and echoes the address back. A view-only wallet's transfer returns a hash but broadcasts nothing | `xmrswap` | the companion derives the address from the keys itself before trusting a wallet made from them, and confirms every send from the chain side (MT-B20, MT-B21). Found on the real server; the fake had refused both | **closed**: the fixes ran on the real server, and all three exits completed twice |

---

## 5. The failure model

Every case the directive lists. "Funds safe" means no party loses coins it did not agree to lose.

| Failure | User outcome | Protocol outcome | Funds safe? | Recovery path |
|---|---|---|---|---|
| **Zcash node offline** | that party's companion waits | nothing moves on Zcash for that party | yes, while the node returns before the party's window; the Zcash taker's refund must be broadcast between `cltv` and the maker's spend (T5) | restart the node; the pre-signed refund can be broadcast from any other node |
| **Monero node offline** | the companion waits | — | yes, for Alice (no deadline to recover her XMR after Refund, or for Bob to sweep after Claim). Bob must Refund in `[t1, t2)`, which needs only DERO | restart |
| **DERO node offline** | the app shows "no node" with the last records marked; companions wait | — | yes, unless it stays down across the party's own window (T8) | restart, or name another node |
| **One relayer malicious** | — | no relayer has authority; the optional senders can only complete an exit for its stored payee | yes | anyone else sends it |
| **Several relayers offline** | — | the parties send their own exits | yes | — |
| **One relayer compromised** | — | as above | yes | — |
| **Oracle disagreement** | — | there is no oracle | — | — |
| **DERO reorganisation** | companions act only on state 8 deep; the app says "included" until then | nothing deeper than 8 can be replaced (consensus) | yes | — |
| **Zcash reorganisation** | up to 10: the maker waits. Deeper, after his claim: the funding vanishes | the maker's `s` is public; the taker refunds her ZEC and claims the DERO | **the maker loses both legs** (T3; Z5) | none. It is the named assumption |
| **Monero reorganisation** | up to the policy: Bob waits. Deeper, after he releases the gate: Alice's lock vanishes | Alice can still Claim; Bob sweeps nothing | **the maker loses** (T3; R3) | none. It is the named assumption |
| **Network partition** | a party cut off from a chain is offline for it | — | as for "offline" | reconnect |
| **A chain split** | a party follows whichever branch its node follows | — | at risk, as for a reorganisation | none. T3 |
| **Database corruption** | there is no database. A corrupt companion state file or used-hash register makes the companion refuse to continue (`tools/zecswap/state.go:321`) | — | yes, if backed up. **Losing Alice's state file after she sent XMR loses that XMR**, because she cannot form the joint key | the recovery card (U9, MT-B15) |
| **Process crash** | the companion resumes from durable state written before every irreversible step | — | yes (`TestCrashFuzz` kills both sides at random points). A kill during Bob's sweep is resumed (XMR-R1, MT-B12: `TestASweepInterruptedByAKillIsResumed`, and the crash fuzz covers the sweep) | `resume` |
| **Transaction timeout** | a DERO transaction is mined at most 10 blocks after the height it was built at, or never (`TX_VALIDITY_HEIGHT` = 11, `miner_block.go:43, 275`); the companion re-sends at a doubled fee, twice at most, inside its margins | — | yes, inside the margins | re-send |
| **RPC timeout** | reads are bounded and retried (MT-B07, MT-C11) | — | yes | — |
| **RPC lies** | see `CROSS_CHAIN_TRUST_MODEL.md` §5 | — | **no, when the liar colludes with the counterparty** | own nodes; several sources that must agree |
| **Stale data** | companions re-read at every irreversible step; the app marks cached figures on failure, with their age, on every screen (a `#stale` banner, `app/app.js:32`; MT-D07, BL-129) | — | yes | — |
| **Invalid proof** | Alice will not lock XMR unless Bob's proof verifies; Bob will not release the gate unless Alice's does | the swap ends in a Refund | yes | — |
| **Malformed transaction** | companions refuse to parse it (bounded parsers; `zecswap` refuses a Sapling bundle by name) | — | yes | — |
| **Duplicate transaction** | DERO: `st_<id>` single-shot. Zcash and Monero: consensus | the duplicate is refused | yes | — |
| **Wallet unavailable** | the party cannot sign | — | as for "offline" (T8) | — |
| **External chain halted** | Monero halted: Alice's lock cannot confirm, so Bob Refunds at `t1` and she recovers after the restart. Zcash halted: the taker cannot refund after `cltv`; if Zcash resumes after `T_dero`, the maker, who has refunded the DERO, can race her refund with a claim | — | Monero: yes. **Zcash: at risk for the taker** (Z17 squeezed by the halt) | broadcast the pre-signed refund the moment Zcash resumes |
| **User closes the browser** | nothing; the app holds nothing and the companions are separate programs | — | yes | — |
| **User loses connection** | as for "offline" | — | as for "offline" | — |
| **Partial settlement** | Monero: once Alice's Claim publishes `s_a`, Bob can sweep at any time, with no deadline. Zcash: once the maker's claim publishes `s`, the taker's DERO Claim must land before `T_dero`; swap v2 lets anyone send it, and the maker's companion sends it straight after its own claim | — | Monero: yes. Zcash: yes if anyone sends the Claim before `T_dero` | the Claim, by anyone |
| **Contract call fails** | a refused call returns its value when the signer is known (ring 2) | the state is unchanged | yes, at ring 2 | retry |
| **DERO fee spike** | companions re-send at doubled fees, twice | a fee above that can miss a margin | at risk for the party whose window it is (T4) | the profile's fee ceiling (MT-B16) |
| **External fee spike** | Zcash: the refund is pre-signed at several fee levels (ZIP 317 picks each transaction on its own fee). Monero: Bob's sweep and Alice's recovery have no deadline | — | yes | re-broadcast a higher-fee refund |

**Every state has an exit that needs no particular person**, with the exceptions T8 names:
- `htlc.bas`: an open lock ends in Claim, Refund, Punish or Reclaim. Punish, in [`t2`, `t2 + g2`), needs nobody in
  particular. From `t2 + g2` only the maker's Reclaim can end it, and it has no deadline: if nobody punished in time and
  the maker never reclaims, the lock stays open for ever, with any XMR the taker locked (the maker's per-party window,
  MT-J11(a)).
- `swap.bas`: every open lock ends in Claim, which needs nobody in particular, or Refund.
- `board.bas`: every row ends by time.
- `nfa.bas`: every owed share leaves by `PayOwed`, which anyone may send.

---

## 6. Denial of service and resource exhaustion

- **Contracts.** Every call is paid for, and bounded by the compute and the 20,000 storage gas of one call. No contract
  loops over a structure a stranger can grow: `founders.bas` PayBatch takes at most 15 holders per call
  (`founders.bas:162`). String arguments have length caps.
- **Companions.** Every node response is read through a size cap with a timeout, and anything malformed is refused
  deterministically (MT-B07, MT-C11). Proof verification is a fixed ≈0.74 s, plus ≈1.57 s of generator
  setup (`research/crosschain/poc/proof-xmr/run.log`), once per swap.
- **The app.** It reads only the node the user names. A hostile node can send large responses to that user's own tab
  and nobody else's. Every write goes through the user's wallet, which asks before signing.

---

## 7. Cryptography

| Use | Primitive | Implementation | Notes |
|---|---|---|---|
| Hashlocks | SHA-256 | the DVM's `SHA256`; Go `crypto/sha256`; Zcash script `OP_SHA256` | the preimage is exactly 32 bytes everywhere: `htlc.bas:108-109, 111-112, 140-141, 200-201`, `swap.bas:86-87`, the Zcash script's `OP_SIZE 32` |
| Monero key shares | ed25519 scalars and points | `filippo.io/edwards25519` v1.1.1 (constant time) | per-swap shares from `crypto/rand` (`tools/xmrswap/swap.go`) |
| Tying a hash to a key share | Bulletproofs R1CS with a SHA-256 gadget | dalek `bulletproofs` 5, feature `yoloproofs`, patched locally; `curve25519-dalek` 4; `merlin` 3 | **not audited outside this project; the R1CS API is marked unstable upstream.** Mainnet gate MT-G03 |
| Zcash spend signatures | ECDSA over secp256k1, RFC 6979 nonces | `github.com/decred/dcrd/dcrec/secp256k1/v4` v4.3.0 (constant time, no cgo) | the spend key is loaded for one use and overwritten straight after (`tools/zecswap/swap.go:370-377` `withKey` → `defer k.Zero()`; `sign.go:49-50`). In Go this is best effort: the collector may have copied the scalar before it is overwritten |
| Zcash digests | BLAKE2b-256 with a personalisation string | in tree, `tools/zecswap/blake2.go` | no secret passes through it; checked against RFC 7693 and every ZIP 244 vector in `tools/zecswap/testdata/zip_0244.json` |
| Randomness | — | Go `crypto/rand` in both companions (`tools/xmrswap/swap.go`, `tools/zecswap/swap.go`); Rust `rand` 0.8 for the proof's blinding | no `math/rand` outside tests |
| Domain separation | Merlin transcripts in the proof; ZIP 244 personalisations; each hash is used for exactly one purpose per swap | — | a secret is never reused across swaps (`UsedSecrets`, `UsedHashes`) |

No DMZ code implements a curve, a signature scheme or a proof system of its own. The one in-tree primitive is a hash.

---

## 8. Privacy map

| Metadata | Who learns it | Avoidable? |
|---|---|---|
| IP address | the node a user reads (the app contacts only it, the local wallet and its own files: the UI pass records "requests that left this device: none"); the Monero and Zcash peers a transaction is broadcast to | yes, by running your own nodes, or reaching them over Tor |
| DERO address | everyone, for every ring-2 call (almost every call; `README.md` §3) | only the `...To` wrappers run at ring 16, and they hide the payer among 8 accounts, not 16 (a public bit in the proof shows which half of the ring the payer is in, `vendor/derohe` `proof_generate.go`) |
| Zcash addresses and amounts | everyone (transparent) | no. Stated first in `docs/ZEC_SWAP.md` §1 |
| XMR amount and timing | the counterparty maker (he holds the joint view key) | no, not in this protocol |
| The link between a DERO address and a foreign transaction | Zcash: everyone, through `H` and `s`. Monero: the counterparty, and anyone who can correlate timing | no for Zcash; partly for Monero |
| Transaction ids, amounts on DERO | everyone: pool reserves are recorded per block; escrow amounts are in storage | no |
| Browser fingerprint | nobody: no third-party request, no analytics, no fonts, no CDN | — |
| RPC endpoint | stored in the browser's own settings | — |
| Logs | local files of each companion | MT-L02: a test that no secret reaches a log |
| Server-side request metadata | there is no DMZ server | — |

---

## 9. What remains, ranked

1. **The Monero proof is unaudited** (T1; MT-G03). A mainnet blocker for the Monero path.
2. **A party's data source can cost it a whole leg** if it colludes with the counterparty (T6). Disclosed; reduced by
   own nodes and by several sources that must agree.
3. **Foreign reorganisations deeper than the policy** cost the party who receives on that chain (T3). Neither chain has
   a consensus bound.
4. **DERO censorship over a margin** (T4, attack A). Priced by fees and bounded by G; not closed.
5. **Liveness windows** (T8): the Monero maker in `[t1, t2)`; the Monero taker's Punish in `[t2, t2 + g2)`, after which
   her XMR depends on the maker's Reclaim; the Zcash taker from `cltv`. Operator decision 15, answered (a) (MT-J11).
6. **The TELA INDEX key**, until its final state is reached and verified (T9).
7. **The companion's publisher** (T7; operator decision 14).
8. **Nothing has run on a public Monero or Zcash test network yet.** Every exit of both swaps has run on real node
   software in regtest (MT-B10, MT-C13); Monero stagenet and Zcash testnet are MT-F03 and MT-F04.
