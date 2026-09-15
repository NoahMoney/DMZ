# The wrapper: mechanism review, controls, pipeline specification

The design record behind `contracts/vault.bas` v2 and `tools/vaultsigner`. Read `README.md` first.


---

# MECHANISM REVIEW

## MECHANISM_REVIEW — Phase A (2026-09-06)

**First paragraph, as the prompt demands: this is a federation.** wXMR and wZEC on DERO are minted by a k-of-n
signer set that holds the real coins. The DERO contract cannot see Monero or Zcash, so it enforces only the
DERO-side rules (k votes, once-only deposit ids, a supply cap, bonds, halt). Everything about the reserve depends
on the signers. What makes it better than "a company's hot wallet" is threshold custody (the vault key is never
assembled), a supply cap tied to slashable bonds, published viewing keys for proof of reserves, and a trustless
atomic-swap exit that does not depend on the signers. What it is not is trustless, and it should never be
described as such.

### §1 Input, filled in from the workspace

```
MECHANISM_NAME: Veil vault — bonded k-of-n federation wrapper
ONE-PARAGRAPH DESCRIPTION: A user registers on DERO (`Register()` → deposit index j) and pays XMR/ZEC to a
  per-index deposit address derived by the signers from a shared viewing key and attested on chain by k votes
  (`Attest(j, addr)`). k independent signer daemons, each on its own node, observe the deposit reaching the
  confirmation policy and vote `Mint(txid, user, amount)`; the k-th matching vote mints the contract's own asset
  one-for-one to the user. Withdrawal: the user sends wrapped units to the contract with `Burn(dest)`; the units
  vanish (the DVM keeps no own-asset balance for a contract), `sup` is lowered, a withdrawal record is written; the
  signers run a threshold signing round on the foreign chain, pay `dest`, and vote `Released(j, ftx)`. Custody on
  the foreign chain: Zcash Orchard spend authority split with re-randomized FROST (ZIP 312, reddsa `frost`
  feature; 2-of-3 demonstrated in `research/crosschain/poc/frost-zec`); Monero via native wallet multisig
  (experimental) at launch, threshold CLSAG (FROSTLASS, monero-oxide) or the FCMP++ multisig when they ship;
  Solana via FROST ed25519. DERO enforcement: `SIGNER()` at ring size 2 gates every vote.
CODE / DOCS: contracts/vault.bas; tests/vault_test.go; tools/vaultsigner (sim backend only); tools/simvault;
  research/wrapper/README.md (security review); research/crosschain/{DECISION,SPEC_CROSSCHAIN,RESOLVED,REVIEW}.md; poc/frost-zec,
  poc/proof-xmr, poc/proof-zec (removed 2026-09-15; in history at c58e3e3), poc/zec-t-htlc; simulator run
  research/crosschain/poc/vault-e2e.log.
CUSTODY MODEL AS WE UNDERSTAND IT: threshold-signature federation for ZEC (FROST RedPallas, key never assembled);
  native multisig federation for XMR at launch (k-of-n, wallet-rpc `make_multisig`/`sign_multisig`, marked
  experimental by Monero); k-of-n vote contract on DERO. No atomic construction for the wrapper itself; the
  atomic swap is a separate exit path.
WHO HOLDS SIGNING POWER: NOT DECIDED. DECISION.md proposes 3-of-5 at launch, n ≤ 20 in the contract. Names,
  organisations, hardware and jurisdictions are the operator's call and are needed before Phase B's ceremony
  and jurisdiction controls can be filled in. Phase A does not depend on them.
WHAT WE THINK IS NOVEL: nothing in the wrapper. Everything here has prior art (Wormhole/Ronin-class federations,
  tBTC v2 threshold custody, Serai's threshold Monero). The parts that are new in this repo are outside the
  wrapper: the sha256-preimage = key-share proofs that make DERO↔XMR and DERO↔shielded-ZEC atomic swaps possible,
  and the on-chain attested per-user deposit index (no memos). Claiming novelty for the wrapper would be false.
KNOWN OPEN QUESTIONS: Monero custody primitive (multisig experimental, FROSTLASS unreleased, FCMP++ multisig
  unfinished); the re-randomized FROST variant Zcash needs is outside the NCC audit scope; DKG ceremony not
  standardised; watcher quorum and reorg handling not built (sim backend only); destination privacy on DERO;
  fee estimation on release; signer rotation and unbonding missing from vault.bas v1.
```

*Addendum, 2026-09-15.* Three things in the input above no longer hold.
- The operator ruled that the ZEC and XMR atomic swaps are removed (`docs/MAINNET_TODO.md` Phase Y). So the wrap has
  no "separate exit path" and no trustless exit, whatever the first paragraph says. A holder leaves wXMR or wZEC
  through the federation's Burn, or by selling on a DMZ market.
- Zcash custody, as it is being built for launch, is a transparent pay-to-script-hash k-of-n multisig. The keys of
  each address are derived from the signers' published account xpubs (Y3-1, the wrap-zcash branch). It is not an
  Orchard address under re-randomized FROST, as the description's custody sentence and the custody model say. So
  deposits and withdrawals on Zcash are public, and anyone who holds the descriptor can link every deposit.
- `poc/proof-zec` was removed. `poc/frost-zec` is kept as research.

Monero custody is unchanged: wallet-rpc multisig k-of-n (Y3-2).

### A1. Classification

**Hybrid, honestly stated as two different classes for the two coins, with a federation in both cases:**

- **ZEC: threshold-signature federation** (FROST, key never assembled). Evidence: `poc/frost-zec` produces a
  2-of-3 re-randomized RedPallas signature over an Orchard spend-authorizing key using reddsa 0.5.2's `frost`
  feature; the group verifying key is a valid `ak`; one signer alone cannot sign. The DKG exists in frost-core 3.0
  (`keys::dkg::part1/2/3`, plus `refresh_share` and repairable shares) but the prototype used a dealer.
- **XMR: multisig federation (native chain multisig)** at launch. Evidence: no other Monero threshold code is
  shipped for mainnet use. Monero's own docs gate multisig behind `set enable-multisig-experimental 1` with the
  warning that funds "can't be spent at all, can only be spent with the participation of a malicious group
  member, or can be stolen by a malicious group member". This is a weaker class than the ZEC side and must be
  presented as such.
- **DERO: `SIGNER()`-gated k-of-n vote contract**, the expected answer given the DVM has no signature
  verification of external chains (`RESEARCH.md (DVM primitives)`).

Nothing in the workspace supports a more trust-minimised class (optimistic, light-client, ZK) and the earlier
report already established why: the DVM cannot verify any foreign proof. The §1 description does not overclaim.

### A2. Trust model re-derived from primitives

#### Monero vault key

- **Native multisig.** Wallet-rpc exposes `prepare_multisig`, `make_multisig(threshold, multisig_info)`,
  `exchange_multisig_keys`, `export/import_multisig_info`, `sign_multisig`, `submit_multisig`, `is_multisig`
  (docs.getmonero.org wallet-rpc). The CLI docs call it experimental and list the failure modes verbatim (above).
  History: on 2021-12-06 the Monero core team disclosed vulnerabilities affecting "(i) multisignature wallet
  creation and (ii) multisignature transaction signing" that "can lead to funds being stolen by one of the signing
  parties" (getmonero.org). The fixes landed through PR #8149 and the 0.18.x "Fluorine Fermi" releases
  (0.18.1.1 and 0.18.1.2, September 2022, "bugfixes and improvements to multisig"); the current line is 0.18.5.x
  (July 2026). The experimental flag has not been removed. Consequence for us: Monero multisig is usable only with
  signers who are trusted not to exploit *undiscovered* bugs of the kind found in 2021, which is a weaker guarantee
  than the threshold property we want. It is acceptable behind a low cap and only as a bridge to a real threshold
  scheme.
- **Threshold CLSAG (FROSTLASS).** Cypher Stack audited monero-serai's multisig protocol and formalised
  FROSTLASS as "a formalized and proven-secure threshold signing protocol for CLSAGs inspired by FROST"
  (serai.exchange, monero-oxide announcement, 2025-09-09). The code now lives in monero-oxide, whose stated goal
  is "a 1.0 release"; no 1.0 exists and no mainnet product uses it for custody that we could find. Serai's own
  eVRF one-round robust DKG was audited by Least Authority (2026-08-19, two low-severity findings, resolved), but
  that DKG is built on generalized-bulletproofs and is not a Monero key-generation procedure in production.
- **FCMP++.** The CCS proposal (!448, submitted 2024-04-12, still marked unfinished) replaces CLSAG with
  full-chain membership proofs and a Generalized Schnorr Protocol for spend authorisation, and plans to
  "implement a 2-round multisignature protocol, inspired by FROST, for the aforementioned Generalized Schnorr
  Protocol… O(n) signing complexity and identifiable aborts". The stressnet "does not yet support hardware wallet,
  multisig, or watch-only wallet features". The getmonero.org blog shows only 0.18.x releases through July 2026;
  there is no release post announcing an FCMP++ hard fork. Third-party blog claims of a Q1-2026 activation are
  wrong (WRONG_PREMISES #18). What the fork does to our custody: CLSAG multisig (native or FROSTLASS) stops being
  the spend mechanism for new outputs after activation; the vault would have to migrate to the GSP multisig, which
  is the last unfinished milestone of the proposal. Any Monero custody we ship must plan a key migration.
- **Monero finality data.** Source constants: `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE 10`, `DIFFICULTY_TARGET_V2 120`,
  `CRYPTONOTE_MINED_MONEY_UNLOCK_WINDOW 60` (cryptonote_config.h). No consensus reorg limit exists; the 10-block
  spend lock is the network's own statement of "deep enough to spend". Reorg history remains secondary-sourced
  (an 18-block reorg reported by exchanges); the policy below uses 10 for small and 20 for large deposits and
  treats a deeper reorg as a halt event, not a credit event.

#### Zcash vault key

- **Pool.** Orchard (Sapling is legacy; ZIP 316 unified addresses put Orchard first in receiver priority). The
  vault publishes a unified address whose only receiver is Orchard so no sender can pay a transparent receiver
  (C2 decision, recorded here because it changes the threat model: no transparent leg to attribute).
- **Spend authority.** ZIP 312 (status **Draft**, created 2022-08) adapts FROST with re-randomization so
  threshold-signed spends are unlinkable; "a rogue Coordinator will be able to break unlinkability and privacy,
  but should not be able to create signed transactions without the approval of MIN_PARTICIPANTS participants";
  the ZIP "does not specify a key generation procedure". Implementation: reddsa `frost` feature over
  frost-rerandomized 3.0 / frost-core 3.0. **Audit gap:** NCC audited frost-core v0.6.0 and the plain ciphersuites
  and "explicitly excludes frost-secp256k1-tr and rerandomized FROST"; reddsa's README says the feature "is still
  experimental since ZIP-312 is still a draft". ZF's own status page: the reference implementation is done,
  frost-client exists, "the missing piece is for wallets to integrate FROST", and there are "existing concerns
  about the lack of a standardized key generation specification". Least Authority audited `frostd`/`frost-client`,
  not the rerandomized signing. Consequence: the exact code path our custody uses is proven in a draft ZIP and
  implemented by Zcash's foundation, but unaudited as a whole. It still beats Monero's position (no threshold
  option at all), and it is what Zcash's own tooling ships.
- **Proof of reserves.** With the Orchard full viewing key published (`ak`, `nk`, `rivk`, 96 bytes, what
  `FullViewingKey::from_bytes` takes) anyone can scan the chain and sum the vault's unspent notes. Publishing the
  FVK also reveals the vault's outgoing payments (withdrawals), which is acceptable and desirable for a public
  reserve. The alternative, a privacy-preserving reserve proof, does not exist for Orchard today; the incoming
  viewing key alone would hide spends but then reserves cannot be computed. Decision: publish the FVK.
- **Finality data.** Zcash consensus limits reorganisations to 100 blocks: "the new best chain must be a
  side-chain that forked within 100 blocks of the tip" (zcashd rule quoted in Zebra RFC 0005); state beyond that
  depth is final by rule, not probability. Block spacing 75 s (ZIP 208; a 25-second spacing ZIP 218 exists as a
  proposal). Zebra 5.2.0 widened its local rollback window to 1000 blocks while keeping the 100-block consensus
  limit. Policy below: 10 blocks for credit; 100 blocks is the hard bound after which a reorg is impossible by
  consensus, which is a property Monero does not have.

#### DERO mint authority

`SIGNER()` is valid only for ring size 2 (`transaction_execute.go:430`), so every vote is attributable to one
signer address; `vault.bas` keys votes by that address and refuses duplicates; the k-th matching vote mints;
`md_<id>` makes an id unmintable forever; `sup ≤ cap` is checked with an overflow guard. The DVM cannot see
foreign chains, so the contract cannot know whether a deposit is real: k colluding signers can mint up to `cap`.
This is the same bound the earlier review stated; it is a property of the class, not a bug.

### A3. Threat model

| attacker | steal | freeze | mis-credit | mechanism response |
|---|---|---|---|---|
| single compromised signer | nothing (needs k) | **v1: every deposit, permanently.** The first vote pinned (to, amount) and every honest vote reverted forever (found by the adversarial review). **v2 (2026-09-06): nothing.** Votes are counted per tuple and the first tuple to reach k mints | one wrong vote, visible on chain, grounds for slash | **prevents** in v2; v1 must never be deployed |
| k colluding signers | the whole reserve on the foreign chain; up to `cap` of unbacked wrapped units on DERO | yes | yes | **bounds** (cap, bonds); **detects** (reserve ≠ supply via published keys); cannot prevent. This is the trust. |
| n−k+1 signers offline | nothing | everything: no mints, no releases, no halt changes | no | **detects** (liveness monitors); recovery needs signer rotation, which v1 lacks (open item, must be added before any real funds) |
| compromised deposit watcher (one) | nothing alone | nothing | one wrong vote | **prevents**: credit needs k independent observations with identical (txid, index, amount) |
| compromised withdrawal processor (one) | nothing alone (threshold signing) | can stall its share | could try to sign a wrong destination; the others sign only what they decrypted themselves | **prevents** for k > compromised; **detects** by signed ledger disagreement |
| depositor: fake deposit | nothing: watchers read their own nodes | — | — | **prevents** |
| depositor: dust | nothing; could grief with many tiny outputs | — | dust below policy is not credited and is accounted, never silently dropped | **handles** (C2 policy) |
| depositor: reorged deposit | credit before finality would create unbacked units | — | yes if credited early | **prevents** by policy (10/20 XMR blocks; 10 ZEC blocks, hard bound 100); a deeper reorg after credit → **halt** (C2/C4) |
| depositor: wrong address / no intent | funds land in the vault with no owner | — | risk of silent burn | **handles**: credited to an unattributed-funds ledger, never minted, refundable by the signers after manual proof of ownership |
| depositor: replay (same txid twice) | — | — | double credit | **prevents** on chain (`md_<id>`), independently of the watcher |
| withdrawer: double burn claim | two releases for one burn | — | — | **prevents**: the release is keyed by the burn record `j`; k votes on `Released(j)`; the signed ledger refuses a second release for the same `j` (C3 exactly-once model) |
| withdrawer: front-run | nothing to front-run: the burn names the destination inside the same transaction | — | — | n/a |
| withdrawer: address substitution | a malicious signer substitutes a destination | — | — | **prevents** when destinations are encrypted to the signer set and each signer decrypts independently and compares (C3); v1 stores `dest` plaintext, which leaks privacy but cannot be substituted after the fact |
| Monero reorg > 10 blocks | unbacked units if credit was given at 10 | — | yes | **policy**: 20 blocks above a size threshold; any reorg deeper than the credited depth → halt + reconcile; there is no consensus bound on Monero |
| Zcash reorg | impossible beyond 100 blocks by consensus | — | below 100: same as Monero | **policy** 10 blocks; halt on reorg past credited depth |
| DERO halt / chain stall | nothing | mints and burns stall; foreign deposits keep arriving | none: intents/credits are only ever written on DERO | **safe by construction**; queue resumes |
| network partition between watchers | nothing | mints stall (no quorum) | none | **safe**: quorum is required, not eventual |
| DERO reorg of a Burn | a release for a burn that later disappears | — | yes | **prevents** by DERO finality depth before release (C3: `STABLE_LIMIT` 8 plus margin) |
| operator / owner key | none after `Seal()` | none | none | **prevents** |
| DVM quirks | `ADDRESS_STRING` mainnet prefix; own-asset balance not tracked; freshly minted asset unspendable ~2 blocks | — | none | documented (RD_LOG 2026-09-06) |

### A4. Verdict

**Yes-with-changes. Confidence: M for Zcash, L for Monero until a threshold scheme ships.** The mechanism can
hold user funds under the stated trust once the following are in place; none of them is optional:

1. **Watcher quorum on observation** (k independent nodes; identical tuple) instead of the current sim backend.
2. **Exactly-once release ledger** signed by the signers, with abandoned-partial-signature handling (C3).
3. **Encrypted destinations** on DERO and independent decryption by each signer (C3).
4. **Signer rotation and unbonding** in the contract, with epochs (v1 freezes forever on quorum loss).
5. **Per-deposit maximum and rate limit; withdrawal delay with veto for large amounts; circuit breaker on the
   reserve invariant** (C3/C4).
6. **Published viewing keys and a continuously checked reserve invariant** (C4) before the first real deposit.
7. **Monero:** launch behind a low cap on native multisig with all signers under contract and treated as
   trusted-not-malicious; migrate to FROSTLASS / FCMP++ multisig when released; plan the key migration for the
   FCMP++ fork now. Or launch ZEC first and XMR later; that is the honest ordering given the primitives.
8. **Zcash:** run the DKG (no dealer), publish the transcript, and commission an audit of the exact rerandomized
   FROST path (reddsa + frost-rerandomized) since NCC's audit excluded it.
9. **A named signer set** with diversity of organisation, hardware and jurisdiction (§1 blank field).

What would make it a **No**: any plan to describe it as trustless; any launch with a single operator holding
k shares; any launch on Monero multisig without the cap; any credit before the confirmation policy.

Stopped here, per the prompt. Phase B (controls), C (pipeline spec, formal model, code), D (tests), E (platform)
follow on your go.


---

# CONTROLS

## CONTROLS — security standard mapping for the wrapper (Phase B, 2026-09-06)

Status legend: **D** = implemented in the design and code that exists today; **P** = planned (backlog id in
`docs/history/HISTORY.md (earlier backlog)`); **N/A** = not applicable, with the reason. "Signer" = one of the k-of-n federation
members; "vault" = the foreign-chain custody key; "contract" = `contracts/vault.bas` (and its successor
`wrapped_asset.bas`). Every control names its standard; "best practice" is not a citation.

### 1. CCSS (CryptoCurrency Security Standard, C4, Level III)

| CCSS aspect | control | status | how |
|---|---|---|---|
| 1.01 Key/seed generation | keys generated with a verified RNG, on an operator-controlled device, never on a shared host; entropy from the device plus operator input | P (S7) | DKG (frost-core `dkg::part1/2/3`) on air-gapped machines; Monero multisig `prepare_multisig` on dedicated hosts; no cloud key generation |
| 1.01 | key generation is distributed so no single party ever holds the full key (Level III: "keys generated by multiple parties") | P (S7) for ZEC; **N/A for Monero native multisig** (the scheme creates shares but the 2021 disclosure class means shares are only as safe as every participant) | FROST DKG for Orchard; FROSTLASS/FCMP++ multisig for Monero when shipped |
| 1.02 Wallet creation | deterministic, reproducible wallet/address derivation verified by more than one implementation | P (Q3) | ≥ 3 independent derivations must agree on 10 000 deposit addresses before use |
| 1.02 | multi-signature / threshold wallets with geographically and organisationally separate keyholders | P (§1 blank field) | operator to name signers; requirement: no two signers in one organisation, one jurisdiction or one hosting provider |
| 1.03 Key storage | keys stored encrypted at rest, on hardware under the keyholder's physical control, with backups that are themselves split | P (S7, RUNBOOKS) | share encrypted with a device key; backup share encrypted to a recovery key held by a different person; refresh via `refresh_share` after any restore |
| 1.03 | no key material in browsers, logs, chat, tickets or CI | D | app never holds custody keys (only the user's XSWD wallet); `.testnet/*.seed` are testnet-only and git-ignored (tools audit to confirm) |
| 1.04 Key usage | every use requires a documented, logged, four-eyes-authorised operation; the signer software has no admin API that can move funds | D (design) / P (services) | releases are driven only by on-chain burn records; there is no "send" endpoint (`tools/vaultsigner` has none) |
| 1.04 | usage limits per period and per operation | P (S5) | per-deposit max, rolling mint window, withdrawal delay with veto |
| 1.05 Key compromise policy (KCP) | written procedure: detect, halt, rotate, communicate, recover | P (RUNBOOKS/key-compromise.md) | halt by k votes (D); rotation (S4); `refresh_share` (D in library) |
| 1.06 Keyholder grant/revoke | onboarding and offboarding of signers with identity verification and revocation of all access | P (S4 + RUNBOOKS/signer-rotation.md) | contract-level add/remove by k votes with a new epoch; off-chain: revoke node credentials, rotate shared viewing keys if a signer leaves |
| 2.01 Security audits / pentests | third-party audit before mainnet, annually after | P (S8, AUDIT_SCOPE.md) | scope: contract, signer/watcher services, rerandomized FROST path, DKG ceremony |
| 2.02 Data sanitisation | decommissioned signer hardware and backups are wiped | P (RUNBOOKS) | documented wipe procedure per device class |
| 2.03 Proof of reserve | reserves auditable by anyone; audited by a third party at Level III | D (design) / P (S6) | Orchard FVK and Monero view key published on chain (`pub_view`); reconciler compares to `sup` every block; external auditor signs a quarterly statement |
| 2.04 Audit logs | all key operations logged, tamper-evident, retained | P (services) | signer ledger is append-only and signed; DERO votes are themselves an immutable log |

### 2. NIST SP 800-57 Part 1 Rev. 5 (key management lifecycle)

| lifecycle phase | control | status |
|---|---|---|
| generation | DKG with published commitments (transcript); randomness from a NIST SP 800-90A/B-quality source on each participant | P (S7) |
| distribution | shares never distributed; only public commitments and packages travel over authenticated channels | P (S7) |
| storage | encrypted at rest, hardware-bound, backups split | P |
| cryptoperiod | vault key rotated (proactive refresh) at least yearly and on every signer change | P (S4, `refresh_share`) |
| usage | single purpose per key: the vault key signs only vault spends; DERO vote keys are separate wallets | D |
| compromise recovery | halt → new DKG → sweep reserve to the new key → resume | P (RUNBOOKS) |
| destruction | shares of a retired key destroyed after the sweep is final on both chains | P |

### 3. NIST SP 800-53 Rev. 5 control families for signer infrastructure

| family | controls applied | status |
|---|---|---|
| AC (access control) | AC-2 account management per signer host; AC-3 least privilege (signer process cannot read other signers' shares); AC-6 separation of duties (watcher ≠ signer roles inside one org are still one signer; separation is across signers) | P |
| AU (audit) | AU-2/AU-3 every vote, observation and release attempt logged with content; AU-9 protection of audit information (signed ledger); AU-12 generation on every host | P |
| IA (identification/authentication) | IA-2 multi-factor for signer operators; IA-5 authenticator management (hardware keys); node RPC bound to localhost, mutual TLS between watcher and signer if split | P |
| SC (system and communications) | SC-8 transmission confidentiality/integrity (encrypted destinations, authenticated channels for FROST rounds); SC-12/SC-13 cryptographic key establishment/protection; SC-28 protection at rest | P (S3, S7) |
| IR (incident response) | IR-4 handling, IR-5 monitoring, IR-6 reporting, IR-8 plan: the halt is the first response step and needs k votes, so the plan must name who calls for it | P (RUNBOOKS/halt-recovery.md) |
| CP (contingency planning) | CP-9 backups of shares and ledgers; CP-10 recovery: restore a signer from backup mid-operation (Phase D drill 5); CP-2 plan for quorum loss (rotation) | P |

### 4. Threshold key generation ceremony (ZF FROST DKG, ZIP 312, Serai eVRF DKG as reference)

| requirement | status | source |
|---|---|---|
| DKG, not a dealer; 3 parts, 2 broadcast rounds; round-1 secret packages kept private | P (S7) | reddsa `frost/redpallas/dkg.md` (vendored): "The distributed key generation process has 3 parts, with 2 communication rounds" |
| air-gapped participants; packages moved by QR or one-way media; no network on the key host during the ceremony | P | CCSS 1.01; ZF FROST DKG guidance |
| transcript publication: every round-1 commitment and the resulting `PublicKeyPackage` published; anyone can recompute the group key | P | ZIP 312 notes the ZIP "does not specify a key generation procedure"; publication is our added control |
| participant diversity: distinct organisations, hardware vendors, operating systems, jurisdictions; no two shares on the same platform | P (§1 blank) | Ronin/Harmony lessons below |
| identifier assignment fixed before the ceremony; identifiers are public | P | dkg.md: "each participant needs an unique identifier … The process … is up to the application" |
| the Orchard sign rule: if the group `ak` has its top bit set, the ceremony is repeated (dealer) or a negation round is run (DKG) | D (poc/frost-zec redraws) / P (DKG) | orchard `keys.rs`: "If the last bit of repr_P(ak) is 1, negate ask" |
| coordinator cannot sign alone and is not trusted for confidentiality of the randomizer beyond unlinkability | D (protocol property) | ZIP 312 |
| proactive refresh after any suspected compromise or signer change | P | frost-core `refresh_share`, `refresh_dkg_*` |
| rehearsal on testnet with the exact hardware before mainnet | P (D1 testnet) | — |

### 5. Bridge post-mortems and the control that answers each

- **Ronin (March 2022, 173,600 ETH + 25.5M USDC).** Nine validators, five signatures required. Four Sky Mavis
  validator keys fell to a spear-phishing job offer against an employee; the fifth signature came from the Axie DAO
  validator through an allowlist that let Sky Mavis sign on the DAO's behalf, granted in November 2021 for load and
  "discontinued in December 2021, but the allowlist access was not revoked" (Sky Mavis postmortem). Root cause:
  one organisation effectively held the quorum, plus a stale delegation. Our control: **no two signers in one
  organisation or hosting provider; no delegation of signing rights at all** (a signer's vote is its own DERO
  wallet at ring size 2, and the vault share never leaves its host); grant/revoke reviews on every signer change
  (CCSS 1.06).
- **Wormhole (February 2022, 120,000 wETH).** The Solana contract used the deprecated `load_instruction_at` to
  check that the secp256k1 signature-verification precompile had run, without checking that the account passed was
  the real Instructions sysvar; the attacker passed a fake one and minted without guardian signatures. Root cause:
  signature verification trusted an unchecked input. Our control: **the DERO contract never verifies foreign
  signatures at all; the only authority is `SIGNER()` at ring size 2, checked by consensus**, and every vote must
  match an on-chain tuple. Nothing the contract reads is attacker-supplied except the tuple itself, which needs k
  independent votes.
- **Nomad (August 2022, ~$190M).** An upgrade initialised the Replica's trusted root to `0x00`; unproven
  messages also read as `0x00`, so `acceptableRoot` accepted every message and anyone could replay a withdrawal
  with a changed recipient. Root cause: a default value became a valid proof after a config change. Our control:
  **no "proven" state is derivable from an absent key**: `vault.bas` refuses when `mc_<id>`/`md_<id>` are absent
  where presence is required and mints only on a positive count reaching `k`; `EXISTS` guards precede every
  `LOAD`; the fuzz test (C4) must include "every key absent" states; config changes (cap, signers) need k votes,
  never a single upgrade.
- **Harmony Horizon (June 2022, ~$100M).** A 2-of-5 multisig; two keys, most likely hot, were compromised after
  an attacker phished a developer and studied the bridge infrastructure for weeks. Root cause: a threshold too
  low for the value held, with hot keys. Our control: **k is never below a majority and never 2; hot keys hold
  only the DERO vote wallets (which cannot move the reserve); the vault share is never on an internet-facing
  host; cap ≤ 3 × Σ bonds keeps the value in line with the security posture.**
- **Multichain (July 2023, ~$126M of outflows and a shutdown).** The MPC node servers ran under the CEO's
  personal cloud account; after his detention nobody else could access them, then funds moved from the MPC
  addresses to unknown addresses. Root cause: an MPC in name only, with one person holding operational control.
  Our control: **each signer runs its own infrastructure under its own control, with no shared cloud account,
  and the on-chain quorum rule is what enforces k, not a server.** A detained or absent signer is handled by
  rotation (S4), not by a shared root login.
- **Orbit Bridge (January 2024, ~$81.5M).** A compromised multisig on the Ethereum vault; Ozys later stated a
  departing security lead "abruptly made the firewall vulnerable and left the company" without handover. Root
  cause: insider access and infrastructure hygiene around signer hosts. Our control: **offboarding is a
  documented procedure with credential revocation and share refresh (CCSS 1.06, `refresh_share`), signer hosts
  have no inbound network exposure, and the withdrawal delay with veto (S5) gives the others time to stop a
  release that a single compromised host initiates.**

### 6. Audit checklists adapted (Trail of Bits / OpenZeppelin cross-chain items, restated for DVM-BASIC and the services)

| item | status |
|---|---|
| Replay: every credited event keyed by (chain, txid, output_index); rejects repeats on chain, not only in the watcher | D for `id`; P (S1) for output_index |
| Message authentication: votes are transactions signed by the signer's DERO wallet; no off-chain signatures are trusted by the contract | D |
| Finality: credit only after the per-chain confirmation policy; release only after DERO finality | P (S1, C1) |
| Idempotent retries in services: every action is safe to repeat (state file + on-chain check before each vote) | D (vaultsigner checks `md_`, `da_`, `w_st` before voting) / P (ledger) |
| Pause/halt: exists, needs k votes, stops mints and burns; un-halt also needs k votes | D |
| Upgradeability: the contract is immutable; a new version means a new contract and a migration runbook | D (no `update_sc_code` path in vault.bas) |
| Access control: owner power ends at `Seal()`; signer set fixed until rotation exists | D / P (S4) |
| Arithmetic: uint64 everywhere; overflow guard on `sup + amount`; no floats in any pipeline or UI feeding transactions | D on chain / P (zero-float lint, C5) |
| Denial of service: O(1) storage per entry; Register once per address; attest strings bounded at 200 bytes | D |
| Dependencies pinned; vendored crypto with recorded patches | D (Cargo.lock, vendored bulletproofs fix, go.mod) |
| Monitoring: reserve invariant, signer liveness, queue ages, halt state | P (S6, O1) |
| Emergency contacts and disclosure policy | P (BOUNTY_SCOPE.md) |

### 7. Operational controls

| control | status | note |
|---|---|---|
| change management: every contract or service change reviewed by a second person, recorded in DECISIONS.md | P | today the repo has one author |
| four-eyes on configuration (cap, thresholds, confirmation policy) | D on chain (k votes) / P off chain | |
| secrets handling: no secrets in repo, env, logs; testnet seeds isolated | D / P (tools audit) | |
| dependency pinning and reproducible builds: Go modules, Cargo.lock, vendored forks by commit, `genconfig -dist` manifest with sha256 per file | D / P (D2 signed manifest) | |
| signed releases: manifest signed by the owner key; verifiers recompute DOC hashes | P (D2) | |
| monitoring and alerting SLOs: invariant check every block; a stalled queue > 30 min alerts; signer liveness < k−1 alerts | P (O1) | |
| incident response runbook | P (RUNBOOKS/halt-recovery.md) | |
| key-compromise runbook | P (RUNBOOKS/key-compromise.md) | |
| bug bounty scope | P (BOUNTY_SCOPE.md) | |
| external audit plan: candidates with Zcash and Monero competence: Cypher Stack (audited monero-serai/FROSTLASS), NCC Group (audited ZF FROST), Least Authority (audited frostd/frost-client and Serai's DKG), Trail of Bits (bridges) | P (AUDIT_SCOPE.md) | |
| insurance / reserve policy: the treasury keeps a first-loss reserve funded from fees; caps grow only with bonds and reserve | P (E1) | |

### Sources for this file

Incidents: Sky Mavis "Back to Building: Ronin Security Breach Postmortem" (roninchain.com/blog) and The Block's
account of the spear-phishing; Wormhole February 2022 analyses (Halborn, Ackee, Kudelski) of the deprecated
`load_instruction_at` check; Nomad "Bridge Hack: Root Cause Analysis" (Nomad, Medium) and Coinbase's incident
analysis; Harmony "Harmony's Horizon Bridge Hack" (Medium) and Halborn; Multichain July 2023 statements as
reported by CoinDesk, Blockworks and Decrypt; Orbit Bridge January 2024 (rekt.news, Ozys statement of 25 January
2024). Standards: CCSS (C4) aspects 1.01–1.06, 2.01–2.04; NIST SP 800-57 Part 1 Rev. 5; NIST SP 800-53 Rev. 5;
ZIP 312; reddsa `dkg.md`; frost-core `refresh.rs`/`repairable.rs` (vendored).


---

# PIPELINE SPEC

## PIPELINE_SPEC — deposits and withdrawals engineered for no silent wrong outcome (Phase C, normative)

Standard: every path ends in the correct state or in a visible halt. Each transition that moves or credits value
has a written invariant here and a test named in `tests/` or the formal model. Numbers are exact integers.

### C1. Units and representation

| asset | native unit | decimals | on-DERO decimals | rounding | max representable in uint64 |
|---|---|---|---|---|---|
| XMR | piconero | 12 | **12** (no conversion) | none | 18,446,744.07 XMR per single value |
| ZEC | zatoshi | 8 | **8** (no conversion) | none | 184,467,440,737 ZEC |
| DERO | atomic | 5 | 5 | none | 184,467,440,737 DERO |

- No precision reduction: wXMR = piconero, wZEC = zatoshi. There is therefore no rounding on deposit or
  withdrawal and no dust term from conversion; `accounted_dust` in C4 is only the sub-threshold deposits held
  in the unattributed ledger, in native units.
- Bounds: Zcash total supply is 21,000,000 ZEC = 2.1 × 10^15 zatoshi, far below 2^64. Monero's cumulative
  emission exceeds 2^64 piconero (Monero itself only guarantees per-output uint64), so the wrapper bounds what it
  holds: `cap` is a uint64 and the contract checks `sup + amount ≥ sup` (wrap) and `sup + amount ≤ cap` before
  every mint; `cap` must be set below 2^63 so `sup + amount` can never wrap for any `amount ≤ cap`. Proof: for
  `sup ≤ cap < 2^63` and `amount ≤ cap < 2^63`, `sup + amount < 2^64`.
- Fees: fee schedules are integers in native units; splits use floor and account the remainder to the fee pool
  (`launch.bas`/`founders.bas` already use `muldivup`/floor pairs with the remainder kept in the pool; the same
  rule applies here). No division by amounts anywhere in the pipeline.
- AMM interaction: `pool.bas` multiplies reserves by amounts in emulated uint128 (`tests/uint128_test.go`); with
  12-decimal wXMR a pool of 1,000 XMR (10^15 units) against 100,000 DERO (10^10 units) gives products of 10^25 <
  2^128; the pool's existing guard rejects a swap whose intermediate exceeds the bound (must be re-proved in
  Phase E for the new decimals, task G-AMM in MASTER_TODO).
- Zero-float rule: no `float`/`double`/`Number` arithmetic on amounts anywhere: DVM has none; Go services use
  `uint64`/`math/big`; the app uses `BigInt` for every amount that can reach a transaction (existing `A.fmtUnits`
  path); a lint (`tools/lint-nofloat`) greps for `parseFloat`, `Number(` on amount variables and `/ 1e` patterns
  in `app/` and `services/` and fails CI.

### C2. Deposit attribution: exactly one account, provably

#### Intent first
- `RequestDeposit(asset)` on DERO at ring size 2 records `SIGNER()` and returns deposit id `i` (a counter).
  Nothing is ever credited without a prior intent. (v1 `Register()` gives one index per user for life; v2 makes
  the index **per intent** so every deposit has a fresh destination and an expiry.)
- Storage: `di_<i>_u` user raw, `di_<i>_a` asset, `di_<i>_h` height, `di_<i>_x` expiry height (default 30 days
  of blocks), `di_<i>_s` status {0 open, 1 attested, 2 credited, 3 expired}.

#### Unique destination per intent
- **XMR: a fresh subaddress per intent**, index (0, i). Subaddresses are derived from the vault's view/spend
  public keys without any on-chain payment id, so the destination itself carries the attribution; payment ids
  are deprecated in Monero and are not used (documented reason: integrated addresses leak the id on chain and
  were removed from the protocol's recommended path).
- **ZEC: a unified address per intent** (ZIP 316) whose **only receiver is Orchard**, diversifier index `i`.
  A memo carrying `i` is written by our own app when it constructs the payment and is **diagnostic only**; the
  attribution depends on the receiver address, never on the memo (external wallets may not set memos). Because
  the UA has no transparent receiver, a deposit "to a transparent receiver of the unified address" cannot occur;
  a transparent payment to the vault is impossible by construction.
- Derivation is deterministic from `(vault keys, asset, i)`: Monero `subaddress(0, i)` per the Monero subaddress
  scheme; Orchard `fvk.address_at(i, External)`. Every signer derives it independently and votes `Attest(i,
  addr)`; the contract publishes `da_<i>` only when k votes agree on the same string. Acceptance: three
  independent implementations (Rust via the `orchard`/`monero-wallet` crates, Go, and the reference wallet RPC)
  agree on 10,000 addresses (Phase D item 6).

#### Confirmation policy
- XMR: credit at **10 blocks** for deposits ≤ 50 XMR and **20 blocks** above (10 is Monero's own spend lock,
  `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE`; 20 covers the deepest reorg reported by exchanges, which is secondary
  data, hence the margin). Monero has no consensus reorg bound: any reorg deeper than the credited depth is a
  halt event.
- ZEC: credit at **10 blocks**; consensus forbids reorgs deeper than 100 blocks (Zebra RFC 0005), so a deposit
  older than 100 blocks is final by rule. Deposits between 10 and 100 blocks can still be reorged; a reorg of a
  credited deposit is a halt event exactly as for Monero.
- Credit is never issued before the policy is met; the watcher reports the height and the contract stores it
  (`mh_<id>`), so the policy is auditable per mint.

#### Quorum on observation
- Each signer runs its own foreign node and wallet-rpc/scanner; it votes only what it has seen itself. The vote
  tuple is `(chain, txid, output_index, amount, intent i)`; the contract's first vote fixes the tuple and later
  votes must match (`vault.bas` rule extended to `output_index`, task S1). One watcher is never trusted: k
  identical observations from distinct signers are required.

#### Idempotency
- The contract stores `md_<chain>_<txid>_<output_index>` and refuses any repeat forever. Double credit is
  impossible on chain regardless of watcher behaviour (`ByzantineCannotMint`, `NoDoubleCredit` in the model;
  `vault_test`: replay refused).

#### Reorg handling
- Below policy depth: the watcher simply never votes; if it had cached the deposit it drops it (no state to undo).
- After credit (should be impossible under the policy): the reconciler sees `vault_balance < sup` or the
  watcher sees the credited txid disappear → it votes `Halt()`; alert; runbook `RUNBOOKS/reorg-response.md`:
  wait for the foreign chain to settle, recompute the invariant, either the deposit re-appears (resume) or the
  federation covers the gap from the reserve/insurance and records it, then resumes by k votes.
- A deposit seen below depth that then disappears: nothing was credited; the intent stays open until expiry.

#### Wrong-address, partial, dust, late deposits
- To a vault subaddress with no intent (index never issued): impossible by construction (addresses are only
  derived for issued intents); a payment to the vault's base address (index 0) is **unattributed**: credited to
  the unattributed ledger on the signer side, never minted; refundable by k votes after the sender proves
  ownership (Monero: tx key proof; Zcash: a signed message from the sending address is not possible for shielded
  senders, so the refund requires the memo or the intent record; otherwise the funds stay in the reserve and are
  reported).
- Below dust threshold (XMR: 0.0001 XMR; ZEC: 0.0001 ZEC): not minted; recorded as `accounted_dust`; visible in
  the reserve equation; refundable in aggregate.
- Partial (less than the user announced): the intent has no announced amount; whatever arrives at the intent's
  address at policy depth is minted exactly. Multiple payments to one intent: each `(txid, output_index)` is a
  separate mint to the same user.
- After intent expiry: the address still belongs to the vault; the deposit is minted if it reaches policy depth
  within 7 days after expiry, otherwise it is unattributed-refundable. Expiry exists to bound watcher work, not
  to confiscate.

### C3. Withdrawal: exactly one release, provably

- `Withdraw(amount, dest_enc)` burns wrapped units (sent with the call) and stores `dest_enc`, the destination
  encrypted to the signer set: `X25519` to each signer's published encryption key (n small ciphertexts of one
  32-byte symmetric key) plus an AEAD ciphertext of the destination; the plaintext never appears on DERO. Each
  signer decrypts independently; the release round starts only when the k participating signers' decrypted
  destinations are byte-identical (they exchange a hash of the plaintext first). Any disagreement or a
  malformed/undecryptable destination → the withdrawal is marked `w_st = 9 (invalid)` by k votes and the wrapped
  units are re-minted to the burner (`Refund(j)`, k votes), never released to a fallback.
- DERO-side validation as far as the DVM allows: length bounds only (the DVM cannot parse foreign address
  formats); full validation on the signer side (Monero base58 with network byte and checksum; Zcash UA Bech32m
  with F4Jumble and the `u`/`utest` HRP, receiver types checked; transparent destinations allowed for ZEC-t).
- Release requires (a) the burn final on DERO: `STABLE_LIMIT` (8) + 4 margin = 12 blocks after the burn's block,
  and (b) k signers independently observing the final burn.
- **Exactly-once:** every signer keeps a signed, append-only ledger `burn j → {state, release_txid}`. A release
  is constructed only if the ledger has no entry for `j`. The round: coordinator proposes `(j, dest, amount, fee)`;
  participants compare with their own ledger and decrypted destination; shares are produced; the coordinator
  aggregates and broadcasts; **before broadcast** every participant records `j: released(txid)` (pre-commit). A
  crash between pre-commit and broadcast leaves the ledger saying released with a txid that may not exist: the
  recovery rule is "look up the txid on the foreign chain; if absent after the chain's own mempool timeout, mark
  `abandoned` by k votes, then and only then a new round may start". A round abandoned before pre-commit leaves
  no entry. Two rounds for one `j` can never both broadcast because the pre-commit is checked by every
  participant before signing (modelled: `Crash`/`Retry`/`Release`, invariant `NoDoubleRelease`). Monero-specific:
  a multisig transaction is bound to specific outputs; a second signing over the same outputs after a broadcast
  is rejected by the network (double-spend), which is the network-level backstop.
- **Fees:** a fixed per-asset withdrawal fee in wrapped units is deducted at burn time into a fee pool
  (`fee_w`, k-of-n adjustable: XMR 0.0005 XMR, ZEC 0.0001 ZEC at launch). The foreign transaction fee is paid
  from the reserve and accounted in `accumulated_fees`; estimation: Monero dynamic per-byte fee from
  `get_fee_estimate` at priority normal; Zcash ZIP 317 (marginal fee 5,000 zatoshi per logical action, grace
  window 2 actions). A fee spike cannot change the released amount: the user's amount is fixed at burn; if the
  actual fee exceeds the pool's coverage rule (`fee ≤ fee_pool`) the release **waits** and alerts; it never
  releases less than the recorded amount and never dips into user reserves.
- **Limits:** per-withdrawal maximum; rolling 24 h maximum; amounts above a threshold enter a delay window
  (6 h) during which k−1 signers can veto (`Veto(j)`), after which the release proceeds; a circuit breaker halts
  all releases when the C4 invariant fails.

### C4. Global invariant and reconciliation

```
vault_balance(chain) == total_minted(chain) − total_burned(chain) + pending_deposits(chain)
                        − pending_withdrawals(chain) − accumulated_fees(chain) − accounted_dust(chain)
                        + fee_pool_received(chain)
```
Terms and their independent sources:

| term | source |
|---|---|
| `vault_balance` | the foreign chain scanned with the published viewing key (Orchard FVK; Monero view key + known spends) by the reconciler on its own node |
| `total_minted`, `total_burned` | DERO contract state (`sup` plus per-id records), read from the reconciler's own DERO node |
| `pending_deposits` | deposits seen at ≥ 1 confirmation but below policy depth (reconciler's own scan) |
| `pending_withdrawals` | burns with `w_st = 0` |
| `accumulated_fees`, `accounted_dust`, `fee_pool_received` | the signers' signed ledgers, cross-checked against chain data |

Any drift beyond zero (the equation is exact; there is no tolerance) → `Halt()` votes from every honest
signer, alert, runbook. The reconciler runs on every signer and on any third party who wants to: the viewing
keys are public (`pub_view` in the contract) and the DERO state is public, so the left and right sides are
recomputable by anyone. Privacy-preserving alternatives (zero-knowledge proof of reserves) do not exist for
Orchard notes today and would hide exactly the information users need; rejected.

### C5. Formal model

`formal/VeilWrap.tla` with invariants NoDoubleCredit, NoCreditWithoutFinalDeposit, ReorgedCreditImpliesHalt,
ByzantineCannotMint, NoDoubleRelease, NoReleaseWithoutFinalBurn, Conservation; checked with TLC over
2,745,505 states (README there). Design changes forced by the model so far: the release ledger must be written
before broadcast, and a retry may start only from an explicit "abandoned" state (an earlier draft allowed a new
round whenever no release was observed, which admits a race between a slow broadcast and a retry).

### C6. Implementation plan

- `contracts/wrapped_asset.bas` (successor of `vault.bas`): mint keyed by `(chain, txid, output_index)`;
  `RequestDeposit(asset)` intents with expiry; `Withdraw(dest_enc)` with fee pool; `Refund(j)`; `Veto(j)`;
  per-deposit max, rolling window, delay threshold; `pub_view`; rotation with epochs; per-line gas/overflow
  annotations.
- `services/signer` (Go): backends `monero` (wallet-rpc multisig now, FROSTLASS later), `zcash` (Orchard scan +
  FROST via reddsa through a small Rust sidecar or `frost-client`), `sim`; deterministic and replayable from chain
  data; persistent signed ledger; health endpoint (read-only); no admin API that can move funds.
- `services/watcher`: same binary, observer role; one per signer, own node.
- Second-language re-derivation (`services/rederive`, Rust): address derivation, unit conversion, invariant
  computation; must agree with the Go implementation on all test vectors.
- Lint: `tools/lint-nofloat` in `tests/run.sh`.

### C7. Signer coordination: the channel the foreign signatures travel on

Built 2026-09-15 in `tools/vaultsigner/coord`; operator procedures in `docs/SIGNER_RUNBOOK.md`. The k-of-n
foreign custody needs signers to hand each other material: Zcash per-input signatures for a P2SH spend, and
Monero multisig ceremony rounds, `export_multisig_info` output and the `multisig_txset` to sign. §C3 says a round
"starts only when the k participating signers' decrypted destinations are byte-identical", and HANDOVER §5.3 step 6
says the txset travels "off chain, a small authenticated relay between signers, or a shared directory in the
pilot". Neither fixes a transport. This section does.

**Nothing on the channel is authority.** A backend uses a peer's payload only after checking it against its own
view of the chain. A Zcash signature is checked against the sighash the backend computed from the transaction its
own deterministic `Release` built. A Monero txset is checked against the transfer the backend would build itself.
The channel authenticates who said something. It never decides what is signed.

#### Interface

The backends code against two methods, `coord.Coordinator`:

- `Put(ctx, round, payload) error` publishes this signer's payload for a round.
- `Get(ctx, round) (map[id][]byte, error)` returns the newest authentic payload of every signer that has arrived,
  this signer's own included. It never waits: callers poll.

Ids are the signers' DERO addresses in the DVM's `ADDRESS_STRING` spelling (`dero1…`, the daemon's `dvmMe`).
Round names are 1–200 bytes of `[A-Za-z0-9._:/@+=-]`. A later `Put` for the same round replaces the earlier one,
so a backend names a round by everything its payload depends on. Examples:
`zec/release/<j>/<sighash prefix>/sig/<input>`, `xmr/txset/<j>/<attempt>`, `xmr/info/<epoch>`,
`xmr/ceremony/<step>`.

#### Transport: untrusted dumb stores, several at once

A store is either an HTTP relay (`vaultsigner relay`, a store-and-forward box that holds no key) or a shared
directory (a sync folder, a network share, or removable media between air-gapped hosts). Every node writes to all of
its stores and reads from all of them. The reasons:

1. **No inbound exposure on a signer host.** Controls §5 (Orbit, Harmony) requires that "signer hosts have no
   inbound network exposure" and that "the vault share is never on an internet-facing host". Option (a), each signer
   serving an endpoint its peers poll, puts a listening port on the machine that holds the share. A store needs
   outbound connections only.
2. **No trusted central server.** Any number of relays can be configured, and anyone can run one: a signer on a
   separate host, or a third party. A relay needs no key and no state that matters. One relay that every
   participating signer reaches is enough. Option (a) is still available as a deployment choice: a signer willing to
   expose a host runs a relay on it.
3. **It is what HANDOVER §5.3 names**, with the authentication moved from the relay to the ends, so the relay needs
   no trust at all.
4. **Standard library only.** The channel uses `net/http`, `crypto/ed25519`, `crypto/hpke` (RFC 9180, Go 1.26),
   `crypto/aes`/`cipher` and `crypto/hkdf`. Two modules that `tools/go.mod` already required are reused, and no new
   dependency is added:
   - `filippo.io/edwards25519`, for the small-order check on pinned keys (the standard library exposes no point
     decoding);
   - `golang.org/x/sys/windows`, for owner-only key files, the same construction as `tools/xmrswap`.

Rejected alternatives:

- **The DERO chain as the transport.** Every message would be a public, fee-paying transaction, with 18 s
  blocks and 20,000 storage gas per transaction. A Monero txset runs to hundreds of kilobytes.
- **A DHT or gossip network.** There is no standard-library implementation, and it adds more attack surface than it
  removes.
- **Tor onion services.** Not in the standard library. A relay can still be reached through a SOCKS proxy taken from
  the environment.

#### Identity

Each signer has one 32-byte seed, written by `vaultsigner keygen`:

- The file is mode 0600, or on Windows carries a protected DACL for the owner only, set when the file is created. It
  is never overwritten and never printed.
- The Ed25519 key comes from the seed (RFC 8032).
- The X25519 key comes from `HKDF-SHA256(seed, "DMZ coord hpke x25519 v1")` through RFC 9180 `DeriveKeyPair`.

The public identity line is `dmzcoord1:` followed by hex of the Ed25519 key (32 bytes), the X25519 key (32) and a
proof (64). The proof is an Ed25519ctx signature (context `DMZ coord identity v1`) over the address and both keys.
Loading a pinned identity checks three things:

- the proof, so an identity line pasted against the wrong address is refused;
- that the Ed25519 key is a canonical point that is not of small order (under such a key anyone can forge
  signatures);
- that the X25519 key is not of low order.

Every signer pins every identity, its own included, in its configuration. Signers compare a 96-bit fingerprint per
identity, and one fingerprint for the whole set (vault plus every pin), on a second channel.

#### Message, version 1

Integers are big-endian and every length is checked. Nothing may follow the signature.

```
header  "DMZC" | 01 | suite 01 | vault (32) | tag (32) | sender Ed25519 key (32) | idlen (1) | id
        | created (8) | expires (8)                       -- the wrap context ends here
        | n (1) | n × { recipient Ed25519 key (32) | wrap (80) } | nonce (12)
body    ctlen (4) | AES-256-GCM(K, nonce, payload, AD = SHA-256("DMZ coord payload v1" 0 | header))
sig     Ed25519ctx(sender, context "DMZ coord msg v1", every byte above)

tag     = SHA-256("DMZ coord round v1" 0 | vault | round)
wrap    = HPKE base mode, DHKEM(X25519, HKDF-SHA256) / HKDF-SHA256 / AES-256-GCM, sealing the fresh 32-byte
          content key K to the recipient, with info = SHA-256("DMZ coord wrap v1" 0 | SHA-256(wrap context) |
          recipient key)
```

The vault, the round tag, the sender key and the sender id are all inside the signature, and the recipient node
re-checks each of them. A store sees only the tag, never the round name. Every message is encrypted to every pinned
signer, the sender included, so a restarted node can read its own payload back.

#### Threats and where each is handled

| attack | outcome | test (`tools/vaultsigner/coord`) |
|---|---|---|
| forged message: an unpinned key, or a pinned key's name signed by another key | dropped | `TestForgedMessagesAreRefused` |
| altered in flight (any bit, anywhere) | dropped | `TestEveryBitFlipIsRefused` |
| replayed into another round or another vault | dropped: tag and vault are signed | `TestReplayIntoAnotherRoundOrVaultIsRefused` |
| replay of an older copy, or copies reordered | the newest (created, then hash) wins, and a node never rolls back | `TestReorderedCopiesConvergeOnTheNewest`, `TestTheRelayKeepsTheNewestAndBoundsEachSender` |
| truncated or extended | dropped by the strict parser | `TestEveryTruncationAndExtensionIsRefused`, `TestTruncatedAndOversizedMessagesAreRefused` |
| oversized | refused at `Put`, at the relay (413, message or payload length) and at the receiver before any crypto | same, `TestTheRelayRefusesWhatItShouldNotStore` |
| expired, dated more than 5 minutes ahead, or living longer than 7 days | dropped; the relay answers 410 | `TestExpiredAndFutureMessagesAreRefused` |
| a sender naming another signer | dropped: the id is bound to the key | `TestASenderNamingAnotherSignerIsRefused` |
| the relay or the network reading a payload | impossible: HPKE to each pinned signer | `TestSealOpenRoundTrip` |
| an insider moving a key wrap to another recipient | does not open | `TestAnInsiderCannotRouteAnotherSignersWrap` |
| a weak or mispasted pinned key | refused at load | `TestParsePublicIdentityRefusesWeakOrAlteredKeys`, `TestAConfigWithAMistakeIsRefused` |
| a key file other accounts can read | refused at load (Unix mode; Windows DACL) | `TestLoadRefusesAKeyOthersCanRead`, `…OnWindows` |
| key material or payloads in output and logs | never: an identity formats as its address and fingerprint for every verb, refuses JSON, and logs carry reasons only | `TestAnIdentityNeverPrintsOrSerialisesItsKey`, `TestLogsNeverCarryPayloadsOrKeys`, `vaultsigner` `TestKeygenWritesAPrivateFileAndPrintsOnlyThePublicHalf` |
| outsiders flooding a relay | refused: the relay checks the signature against the pinned keys before storing | `TestTheRelayRefusesWhatItShouldNotStore` |
| an insider flooding a relay | a byte quota per sender, a cap on rounds, 507 | `TestTheRelayKeepsTheNewestAndBoundsEachSender` |
| a hostile relay (huge or too many frames, redirects) | refused with bounded memory | `TestAClientRefusesAHostileRelay` |
| equivocation: a signer posting different payloads to different stores | **not prevented.** The channel gives authenticity, not agreement; backends name rounds by content and verify every payload | `TestEquivocationIsVisibleNotPrevented` |

Storage is bounded and expires on its own:

- **Node.** At most max(256 MiB, 8 × the payload limit) and 4096 rounds in memory, evicted least-recently-used.
  Payloads are 4 MiB by default and 16 MiB at the hard limit. Messages live 24 hours by default, configurable from
  1 minute to 7 days.
- **Relay.** At most 1 GiB by default, shared equally among the pinned senders, with at most 4096 rounds per sender
  and one copy per (vault, round, sender). Expired copies are pruned every 30 s.
- **Directory.** One file per sender per round. Expired files are removed when read and swept every 10 minutes.

#### Liveness and the fallback

Safety never depends on the channel. The worst a store can do is withhold. A backend that cannot gather k
payloads does not sign, its `Release` returns an error, and `processWithdrawal` retries on the next pass. Everything
the vault already does about an unpaid withdrawal applies unchanged: the Paying mark, `Veto`, `Reclaim`.

Attestations, deposits, mints, the reserve check and `Halt` do not use the channel at all. Losing it therefore
delays foreign payments and nothing else. On Monero this includes the multisig-info refresh that must follow every
spend, so Monero releases wait for it.

Coordination completes when at least k signers are online and share one store each of them reaches. A node reads
every store it has, and re-offers its own newest message to any store that answers without it. That heals a relay
that restarted empty.

The fallback, in order:

1. Configure at least two relays from day one, run by different signers or organisations, on hosts that hold no key.
2. If both relays fail, start a new relay anywhere. It needs no key and no state: every signer adds its URL and
   restarts.
3. If no relay can run, use a shared directory over any file carrier. The messages and the verification are the
   same.

Monitoring:

- `vaultsigner` logs the channel's `Health` whenever it changes. The HTTP store also compares the local clock with
  the relay's `Date` header, because a clock more than half the 5-minute window away is reported before it starts
  costing messages.
- `Node.Status` counts drops by reason.

#### What it does not do

- **Agreement.** See equivocation in the table above.
- **Hide who talks, and when.** A relay sees sender keys, round tags, timing and sizes.
- **Forward secrecy.** HPKE base mode encrypts to static recipient keys. Whoever records relay traffic and later
  steals a signer's identity file can read that traffic's payloads. Rotate identities yearly and on any suspicion
  (`docs/SIGNER_RUNBOOK.md` §7).
- **Post-quantum confidentiality.** Not attempted. The custody keys themselves (secp256k1 for P2SH, Ed25519 for
  Monero) fall to the same adversary, so it would protect nothing.
