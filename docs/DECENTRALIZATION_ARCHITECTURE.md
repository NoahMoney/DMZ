# DMZ: decentralisation architecture

*2026-09-11, the final mainnet pass; the swap exits and line citations brought to `htlc.bas` v3 on 2026-09-12 (X-20). This page covers what DMZ is built from, what
each part depends on, which of five cross-chain architectures it uses and why, and what is left once the operator has
gone. Two companion pages go with it:*
- *`CROSS_CHAIN_TRUST_MODEL.md` answers, direction by direction, who can release, mint, censor or deanonymise, and
  sets out what proving a Monero or Zcash payment would take.*
- *`MAINNET_SECURITY_GATE.md` has the verdict.*

---

## 0. The answer

**DMZ is not a bridge, and on mainnet it mints nothing.**

XMR and ZEC move between people by atomic swap, in three parts:
- **The DERO side** is an escrow contract. It releases on a SHA-256 preimage, or on a block height.
- **The Monero or Zcash side** is a lock that only the swap's own reveals can complete.
- **Each person's own software** checks the foreign chain, using that person's own node.

No DERO contract ever learns anything about Monero or Zcash, so nothing has to tell it. No relayer, oracle, attestation
network or signer set is needed, and none is deployed on mainnet.

This is the strongest architecture DERO allows:
- **On-chain verification of a foreign chain is impossible on this VM** (§3 A).
- **Relayers, oracles and federations each add a party who can mint or release against nothing** (§3 C–E).

What it does not remove, in `CROSS_CHAIN_TRUST_MODEL.md` §1:
- each person's trust in their own view of each chain (their nodes);
- the chains' own consensus;
- their own companion software;
- their own presence at stated block heights.

---

## 1. The system as built

```
  a person's browser                 the DERO chain (immutable contracts)          that person's own machine
 ┌──────────────────────┐  reads    ┌──────────────────────────────────────┐    ┌──────────────────────────────┐
 │ DMZ app              │─────────► │ htlc.bas   DERO leg, Monero swap     │◄───│ xmrswap ─ monero-wallet-rpc   │
 │ TELA documents,      │           │ swap.bas   DERO leg, Zcash swap      │    │            └─ monerod         │
 │ served by a TELA     │  signs    │ board.bas  Zcash offers and accepts  │◄───│ zecswap ─ zebrad (or named    │
 │ host on the same     │─────────► │ pool · launch · nfa · nft            │    │            remote nodes)      │
 │ machine; holds no    │  through  │ factory · founders · social · chat   │    │ DERO wallet and derod         │
 │ key, has no server   │  the      └──────────────────────────────────────┘    └──────────────┬───────────────┘
 └──────────┬───────────┘  wallet (XSWD, localhost)                                              │
            │ reads                                                                             │ talks directly to
            ▼                                                                                   ▼
   the DERO node the person names                                         the Monero and Zcash networks
```

**The Monero swap** (`contracts/htlc.bas`, `tools/xmrswap`, `docs/XMR_SWAP.md`). Bob holds a DERO-side asset and Alice
holds XMR.
1. Bob locks the asset in `htlc.bas` for Alice, under three hashes.
2. Alice sends XMR to a joint address. Its private spend key is `s_a + s_b`, and a client-side proof ties each share to
   one of the hashes.
3. Once her XMR is deep enough, Bob releases a gate secret.
4. Alice claims the asset. Her claim publishes `s_a`, and Bob sweeps the XMR.

If the swap stalls:
- Bob refunds in `[t1, t2)`, which publishes `s_b` so Alice can recover her XMR;
- or, in `[t2, t2 + g2)`, anyone sends Punish, which pays Alice (`htlc.bas:165-185`);
- or, from `t2 + g2`, only Bob can move the lock, by Reclaim, which publishes `s_b` as a Refund does
  (`htlc.bas:191-214`). If nobody punished in time, Alice gets her XMR back only if he reclaims.

**The Zcash swap** (`contracts/board.bas`, `contracts/swap.bas`, `tools/zecswap`, `docs/ZEC_SWAP.md`). The maker holds a
DERO-side asset and the taker holds transparent ZEC.
1. The maker posts an offer under `H = SHA256(s)`.
2. The taker accepts it, naming her refund key and refund height.
3. The maker locks the asset in `swap.bas`.
4. The taker funds a P2SH script on Zcash.
5. The maker claims the ZEC with `s`, which publishes it.
6. Anyone who reads `s` can then send the escrow's Claim, and it pays only the taker (`swap.bas:78-101`).

If the swap stalls, each side has a height after which it recovers its own coins.

**What exists off chain, and whose it is:**

| Component | Runs on | Run by | Holds | In the settlement path |
|---|---|---|---|---|
| The DMZ app | the person's browser, from a local TELA host (Engram, `tela-cli`) | the person | nothing secret | no. Every contract can be called from any DERO wallet without it |
| DERO wallet with XSWD | the person's machine | the person | their DERO keys | signs every DERO call |
| `derod` | the person's machine, or a node they name | the person | nothing | yes. Safety-critical for a companion's reads (`CROSS_CHAIN_TRUST_MODEL.md` §5) |
| `xmrswap` | the person's machine | the person | their key share for one swap, in a state file | yes, for the Monero swap |
| `monero-wallet-rpc` and `monerod` | the person's machine | the person | their Monero keys | yes |
| `zecswap` | the person's machine | the person | the one spend key the person hands it | yes, for the Zcash swap |
| `zebrad` (or a named remote) | the person's machine, or a named third party | the person, or a party they name | nothing | yes, reads and broadcasts |
| `treasurybot` | the treasury holder's machine | the treasury holder | the treasury key | no. Fees only |
| `vaultsigner` | **nobody on mainnet** | — | — | — (no vault is deployed) |

---

## 2. Every off-chain dependency

Each row answers the directive's seven questions. **Nothing in this table is run by the operator.**

| Dependency | Why it exists | Mandatory? | Can it be eliminated? | Adds trust? | Availability dependence | Privacy leakage | Decentralised replacement |
|---|---|---|---|---|---|---|---|
| **zcashd** | — | **not used** | — | — | — | — | It reached its automatic end of support on 2026-07-18 at height 3,417,100 and refuses to restart ([zcashd book](https://zcash.github.io/zcash/user/end-of-life.html)). `tools/zecswap/node.go:55` does not depend on which node it talks to |
| **zebrad** (Zcash full node) | the ZEC-side party's companion reads the P2SH funding and the counterparty's claim, and broadcasts | yes, for each participant in a Zcash swap: their own, or one they name. **Not for the operator** | no. Someone must read Zcash for that party | a node the party does not run is trusted for safety-critical reads, and a lie can cost them the swap (`CROSS_CHAIN_TRUST_MODEL.md` §5) | while it is down that party's companion waits. Timelocks keep the coins safe, and the taker's pre-signed refund can be broadcast through any other node | a remote node sees the party's IP address, the P2SH address it asks about, and where its broadcasts came from | the party's own node; or several independent nodes, where every safety-critical read must agree (reduces the trust, does not remove it); client-side header verification (§7: built behind `-verify-headers`, off by default until a testnet swap, MT-C14) |
| **lightwalletd / Zaino** (ZIP 307 light-wallet server) | an alternative to a full node for reads | no. `zecswap` speaks node JSON-RPC and does not use it | yes | if ever used, a named trusted party, the same as a remote node | the same | it sees the addresses and blocks asked for | the same as above |
| **monerod** | the Monero party's wallet reads the chain through it | yes, for each participant in a Monero swap: their own, or one they name | no | a remote `monerod` is trusted. Monero wallets do not check proof of work, so a remote daemon can show a payment that is not on the chain (`docs/XMR_SWAP.md` R18) | while it is down that party's companion waits; timelocks keep the coins safe | IP address, timing, and where a transaction was first broadcast | the party's own node. Monero has no trust-minimised light client in production (`CROSS_CHAIN_TRUST_MODEL.md` §3.2) |
| **monero-wallet-rpc** | `xmrswap` restores the joint wallet from keys, opens a view-only wallet and sweeps | yes, for the Monero swap | no | none beyond the software itself. It runs locally, on keys the party holds | — | — | — |
| **derod** | the app and the companions read contract state | yes, for anyone using DMZ: their own node, or one they name | no | for the app: false prices or balances on screen, but no loss by itself. For a companion: a node that shows a lock that is not there can cost the party the swap (`CROSS_CHAIN_TRUST_MODEL.md` §5) | the app shows "no node" and keeps its last records, marked with the error (`app/app.js:33`, `app/feed.js:389`); companions wait | the node sees the IP address and every contract read | the party's own node |
| **DERO wallet with XSWD** | signs every DERO call | yes | no | the party's own wallet | — | — | — |
| **Indexers** | — | **none**. The app reads contracts directly (`RISKS.md` R-12) | — | — | — | — | — |
| **Databases** | — | **none**. Contract storage is the ledger. Each companion's state file is local to its owner | — | — | — | — | — |
| **Message queues** | — | **none** | — | — | — | — | — |
| **Relayers** | — | **none required.** Three optional senders exist, and anyone may be one: Punish on `htlc.bas` in [`t2`, `t2 + g2`) (pays the stored taker); Claim on `swap.bas` with a published `s` (pays the stored taker); broadcasting the Zcash taker's pre-signed refund (pays only her refund key). **None can direct value anywhere else** | — | none | — | a sender learns nothing that is not already public, except the holder of a pre-signed refund, who learns of the swap early | — |
| **Bridge signers** | — | **none on mainnet**. `vault.bas` is not deployed; `deploy/networks.json` `mainnet.vault` is empty and `vaults` is null | — | — | — | — | — |
| **Oracle operators** | — | **none**. No contract reads a price. An AMM's price is its own reserves | — | — | — | — | — |
| **API providers** | — | **none** | — | — | — | — | — |
| **Centralised web servers** | — | **none for the app**: its files are TELA documents on DERO, served by a host program on the same machine. The Monero swap's rendezvous is a directory the two parties share between their own machines (BL-10) | — | — | — | — | — |
| **Centralised DNS** | — | **none in the settlement path**. `dmz.tela` is a TELA name held in the INDEX, not a DNS name. URLs in NFA metadata and the Founders Pass image are web links their creators chose (`RISKS.md` R-11) | — | a link's host is trusted for what it serves | an image fails to load | fetching one reveals the viewer's IP address to its host | none needed for settlement |
| **Centralised storage** | — | **none for the protocol**. An NFA's file may live on any host its creator chose | — | the host is trusted for availability. Integrity is checked against the NFA's recorded hash and signature | — | as above | — |
| **Centralised key custody** | — | **none on mainnet** | — | — | — | — | — |

**One product gap that is not a trust assumption.** There is no on-chain place to find a Monero counterparty. Zcash
offers live on `board.bas`. Until XMR offers have a board, or a channel between strangers exists (BL-10, BL-12), the
Monero swap works only between people who already know each other.

---

## 3. The five architectures

### A. Full on-chain verification

A DERO contract would check Monero and Zcash headers, proof of work, Merkle inclusion and the payment itself.

- **Feasibility: impossible on this VM, not merely expensive.** DVM-BASIC has 37 built-ins
  (`vendor/derohe/dvm/dvm_functions.go:58-94`), and the verification needs things none of them provides:
  - no BLAKE2b, which both Zcash's v5 transaction ids (ZIP 244) and its Equihash proof of work require;
  - no RandomX, Monero's proof of work;
  - no curve arithmetic, which a Monero payment proof or any Zcash shielded proof requires;
  - no signature verification;
  - arithmetic is uint64 only;
  - every call is capped at 20,000 storage gas (`vendor/derohe/config/config.go:44`);
  - no contract can call or read another.
- **What it would take:** a DERO hard fork adding precompiles, which is not DMZ's to make.
- **If it existed:**
  - trust would reduce to the chains' consensus;
  - latency would be the foreign confirmations;
  - privacy would be worse for Monero, because proving a payment publishes the transaction key;
  - gas would be large.
- **Verdict: rejected. It cannot be built.**

### B. Client-side verification, with atomic settlement (selected)

Each party's own software checks the foreign chain. The contract checks only a hash, a height and who signed.

- **Security.** No party can take another's coins. Every state has an exit, and all but one need no particular
  person:
  - Punish on `htlc.bas` may be sent by anyone in [`t2`, `t2 + g2`) (`htlc.bas:165-185`);
  - `swap.bas` Claim may be sent by anyone holding `s` (`swap.bas:78-101`);
  - `swap.bas` Refund opens at expiry (`swap.bas:104-122`);
  - the exception: from `t2 + g2` an `htlc.bas` lock nobody punished moves only by its maker's Reclaim
    (`htlc.bas:191-214`), the per-party window the operator accepted (MT-J11(a)), and the taker's XMR waits on him.
- **Trust.** Each party's own nodes, the chains' consensus to a stated depth, the companion software, the
  cryptography (including an unaudited proof on the Monero path), and the party's presence at stated windows.
- **Complexity.** Three small contracts: `htlc.bas` 214 lines, `swap.bas` 122, `board.bas` 111. Two companions.
- **Latency.** The foreign confirmations plus about three minutes: ≈48 min for XMR at 20 confirmations, ≈21 min for ZEC
  at 10 (`docs/ATOMIC_SWAP_STUDY.md` §1). A second trade is one DERO transaction.
- **Privacy.**
  - The Zcash leg is transparent: `H` and `s` link the two parties' Zcash addresses to their DERO addresses.
  - On the Monero leg the maker holds the joint address's view key.
  - DERO calls are made at ring size 2, so the signer is visible.
- **Operations.** Each person runs a companion and nodes. The operator runs nothing.
- **Failure modes.**
  - a party offline for the whole of its window;
  - a node that lies to its own user;
  - a foreign reorganisation deeper than the confirmation policy;
  - DERO censoring an exit for longer than its margin.
- **Censorship resistance.** Only miners can delay an exit, and each window bounds how long they can. No operator can
  censor anything.
- **Attack surface.** The contracts' public entrypoints, the companions, and each person's nodes.
- **Cost.** DERO gas and the foreign chain's fees. The swaps charge no protocol fee.
- **Verdict: selected.** It is the only architecture in which nobody but the two parties can move the coins.

### C. Multiple independent relayers

Anyone relays foreign-chain evidence, and the contract accepts it.

- On the DVM the contract cannot check the evidence, for all of A's reasons. A relayed claim is therefore accepted on
  the relayer's word, which makes this D or E under another name.
- **Adopted only where a relayer carries no authority**: the three optional senders in §2. They cannot direct value
  anywhere, and the contract checks everything that matters (the preimage, the height, the stored recipient).

### D. A decentralised oracle or attestation network

N of M verifiers vote, and the contract mints or releases at the threshold.

- **What the DVM can do.** It cannot verify a signature. The only threshold it can enforce is k votes, each sent by a
  distinct ring-2 signer, and `vault.bas` implements exactly that.
  - Domain separation, chain id, asset id, amount, destination, height, expiry and replay protection can all be bound
    through vote arguments and storage keys.
  - Oracle networks such as Chainlink CCIP have no DERO integration, and a contract could not check their signatures
    anyway.
- **Why it is custody.** Minting against a deposit means somebody holds the foreign coins, and paying a withdrawal
  means the signers sign on Monero or Zcash. The DVM cannot see or enforce what they do there. So:
  - k colluding signers can mint against nothing and take the reserve;
  - one signer can strand a withdrawal it has marked (LIV-03, `docs/audit/FINDINGS_2026-09-10.md`);
  - no signer other than the operator has been named;
  - no real Monero or Zcash backend exists (`tools/vaultsigner/main.go`, the `sim` backend only).
- **Verdict: rejected for mainnet.** The design stays in the repository, parked, as the only candidate for an
  XMR-denominated balance or for shielded ZEC (`docs/ATOMIC_SWAP_STUDY.md` §3, operator decision 12).

### E. A centralised bridge operator

- One key mints and pays out.
- It is custody by one party, and it contradicts the operator's own ruling that they hold nothing after deployment.
- **Verdict: rejected.**

---

## 4. Single points of failure

| Component | Single point? | Required? | Can it be decentralised? | Final design |
|---|---|---|---|---|
| Zcash node | for its user only | for Zcash swap participants | yes: own node, or several with unanimity | each participant's own `zebrad` by default; named remotes are disclosed as trusted |
| Monero node | for its user only | for Monero swap participants | only by running one's own | each participant's own `monerod` |
| DERO node | for its user only | yes | yes: anyone can run one | the node the person names in Settings; companions read their own |
| Relayer | no | no | already permissionless | anyone may send Punish (in [`t2`, `t2 + g2`)), the `swap.bas` Claim, or a pre-signed refund; none can redirect value |
| Oracle | — | no | — | none exists |
| Signer | — | no | — | none on mainnet |
| Database | — | no | — | none exists |
| Frontend | the TELA INDEX that names the app's files | no. Every contract can be called from any wallet | yes: an INDEX nobody can update, verified against source | recorded in `MAINNET_DEPLOYMENT.md` and checked by `tools/verifydeploy` |
| DNS | — | no | — | none used; `dmz.tela` is a TELA name |
| Storage | — | no | — | app files are on chain; NFA media is on hosts their creators chose, checked against the recorded hash |
| Indexer | — | no | — | none exists |
| Price feed | — | no | — | none exists; an AMM price is its reserves |
| Configuration | the app's `config.js`, which lists contract ids | no | yes | a TELA document, checked against source by `tools/verifydeploy`; the companions check a contract's code before they trust it (`docs/ATOMIC_SWAP_STUDY.md` U4) |
| Upgrade authority | none for contracts | — | — | no DMZ contract calls `UPDATE_SC_CODE`; each fix is a new contract at a new id |
| Emergency authority | none | — | — | no mainnet contract has a pause; the vault's halt lease is not deployed |
| Registry settings | none: `factory.bas` v2 fixes the treasury and the fee at install and has no owner | — | — | `MAINNET_DEPLOYMENT.md` §1; `verifydeploy` fails a factory that has an owner |
| Companion releases | the publisher | to get fixes | only by naming more publishers | a reproducible build (`tools/release`) whose hashes are printed in the repository; who signs releases is operator decision 14 |
| Discovery | — | for Zcash, `board.bas` on chain | — | Monero discovery does not exist yet (BL-10, BL-12) |

---

## 5. Relayers and verifiers

**Relayers.** None is required. Anyone may take each of the three roles below, and none of them lets anyone choose
where value goes:

| Role | Who may do it | What the protocol checks for itself | What a malicious sender can do |
|---|---|---|---|
| Send Punish on `htlc.bas` | anyone, in [`t2`, `t2 + g2`) (v3) | the height; the lock is still open; the payee is `t_<id>` from storage (`htlc.bas`, `Punish`) | choose the moment the lock closes within that window, never who is paid |
| Send Claim on `swap.bas` | anyone holding `s` | the preimage, the expiry, the open state; the payee is `t_<id>` (`swap.bas:78-101`) | pay the fee to complete the taker's leg |
| Broadcast the taker's pre-signed Zcash refund | anyone holding its hex | Zcash consensus: the locktime, and the taker's signature over the whole transaction | broadcast it, or not; it pays only the taker's refund key |

Each sender still pays DERO gas and needs a valid ring-2 signer, as every exit does.

**Verifiers.** None exists, because nothing on mainnet needs one. A threshold verifier set was considered and rejected
(§3 D). If one is ever built, `DECISIONS.md` §10.13 and `contracts/README.md` describe the parked design. Even then it is
custody by k named parties and must never be called trustless.

---

## 6. Protocol autonomy against infrastructure availability

These are different claims, and they are stated separately.

**Protocol autonomy.** Once deployed, the swap contracts have:
- no owner, admin, fee, pause or upgrade path (`htlc.bas:22`, `swap.bas:3`; `board.bas` stores no owner, admin or pause key and never calls `UPDATE_SC_CODE`);
- storage that only their own entrypoints change, under the rules written in them.

Nobody, the operator included, can change a rule, stop a swap or redirect a payment. That can be verified: fetch the
installed code and compare it with the source (`tools/verifydeploy`).

The other contracts carry the roles listed in `MAINNET_GAP_ANALYSIS.md` (the privileged-path table). Each is absent by design or
justified in `MAINNET_DEPLOYMENT.md`.

**Infrastructure availability.** The protocol depends on things DMZ does not provide and cannot guarantee:
- the DERO, Monero and Zcash networks continuing to produce blocks;
- each person having working software and a node;
- a TELA host program existing to render the app.

If DERO stops, nothing settles until it resumes. If the app's files could no longer be served, every contract could still
be called from a wallet. None of this makes the protocol centralised. It does mean "autonomous" describes the rules, not
the availability of the machines around them.

**What would break autonomy:**
- a TELA INDEX left updatable by a key someone holds;
- a factory with an owner key (an older v1 factory never renounced; v2 has none);
- a companion that only one publisher can fix.

Each is a named item in `MAINNET_SECURITY_GATE.md`.

---

## 7. Upgrades that would reduce trust further (the first two built since 2026-09-11)

1. **Client-side header verification for Zcash.**
   - **What it buys.** The companion would check the Equihash proof of work (n = 200, k = 9, over BLAKE2b; `zecswap`
     already has an in-tree BLAKE2b with the personalisation Zcash needs) and the funding's Merkle path, before it
     reveals `s` or funds a script. A lying node would then need at least `C_zec + 1` blocks of real Zcash work to fake a
     funding, instead of a single false answer.
   - **What it costs.** Parsing v4 and v5 transactions well enough to compute every transaction id in a block (v5 ids
     are ZIP 244 digests), plus checking difficulty against recent headers. Weeks of work.
   - **Status.** Built since this page was written. `tools/zecspv` came with W-02 (`88c8f20`), and is wired into
     `zecswap` behind `-verify-headers` (`8201fa6`). It stays off by default until it has run against the Zcash
     testnet (MT-C14). Under the shipped policy a faked funding costs about 8.9 blocks of network work
     (`docs/ZCASH_HEADER_VERIFICATION.md` §4).
2. **Reads from several sources, which must all agree**, for every read that gates an irreversible step. Built:
   `zecswap` requires every configured source to agree (MT-C10), and `xmrswap` reads its DERO checks from several
   (MT-B09). It reduces "one liar" to "every source colludes". It does not remove the trust.
3. **Monero: no equivalent is practical.** Checking RandomX needs a 256 MiB cache per key epoch. Detecting a payment
   needs a wallet scan with the view key. So the answer for Monero stays the party's own `monerod`. The FCMP++/Carrot
   hard fork will change Monero's transaction and address layer when it activates; no mainnet height was set as of
   2026-09-11 (the [milestone](https://github.com/monero-project/monero/milestone/1) is open). `xmrswap` must be
   re-verified against that release before any use after the fork.
4. **An external audit of the proof in `research/crosschain/poc/proof-xmr`**, the one piece of new cryptography on the
   Monero path (`docs/ATOMIC_SWAP_STUDY.md` U8). It is the gate before any mainnet Monero swap.
