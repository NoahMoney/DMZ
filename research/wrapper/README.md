# The wrapper (wXMR / wZEC): trust statement, security review, what may be said

Everything a reader must know before trusting, running or describing the bonded k-of-n wrapper. The design documents are in `DESIGN.md`, the operator runbooks in `RUNBOOKS.md`, the formal model in `formal/`.


---

# TRUST STATEMENT

## Trust statement — wrapped XMR and ZEC on Veil (wXMR, wZEC)

Written 2026-09-06 against `contracts/vault.bas` v2, `tools/vaultsigner` and the private-testnet/simulator runs
recorded in `research/crosschain/README.md (Resolved)` §8–§10. Every claim below is either enforced by the contract (with
the test that proves it) or named as a trusted party. Nothing here is "trustless".

### 1. What a wrapped unit is

One wXMR is a claim on one piclonero held by a federation of k-of-n signers in a Monero wallet they control
together; one wZEC is the same claim on one zatoshi in an Orchard address whose spend authority is shared
(2-of-3 RedPallas in the prototype, `poc/frost-zec`). The claim is redeemed by burning the wrapped unit on DERO,
which records a withdrawal the federation pays on the foreign chain.

### 2. Trusted parties, in the order they can hurt you

| party | what they can do | what stops them | evidence |
|---|---|---|---|
| **k of the n signers, colluding** | pay themselves the foreign reserve; mint wrapped units against deposits that never happened | nothing on chain: this is the trust. Bonds are forfeited only by a `Slash` vote, which needs k votes: the honest signers can slash a colluding quorum only when there are at least k of them left, i.e. n ≥ 2k. With 2-of-3 a colluding pair keeps its bonds; the bonds deter a single bad signer, not a quorum. `cap ≤ 3 × Σ bonds` bounds the exposure relative to what the signers have at stake, it does not make theft cost a third of the cap. A slashed bond goes to the treasury, not to the victims. A shortfall between the foreign reserve and the wrapped supply plus unpaid burns triggers a k-of-n `Halt` vote from every honest signer's daemon, which a colluding quorum can outvote | `tests/vault_test.go` (cap bound, slash floor), `tools/vaultsigner` reserve check |
| **the contract owner, before `Seal`** | choose the signers | `Seal` removes every owner power; after it the owner is nobody | `tests/vault_test.go` |
| **one honest signer's daemon, crashing** | pay a withdrawal twice, or never | durable intents (paying → paid → voted) with an idempotent foreign `Release`; a corrupt state file stops the daemon instead of emptying its memory | `tools/vaultsigner/exactly_once_test.go` |
| **fewer than k signers** | freeze nothing: a deposit is minted when k votes agree on `(j, id, amount)`; a lying signer's vote is a dead tuple | tuple-keyed votes, slash epochs voiding a removed signer's votes | `tests/vault_test.go` |
| **the foreign chain reorganising** | un-happen a deposit after it was minted | confirmation depth per chain before a vote (network table); a deposit that vanishes below policy depth is never voted | `chainTable`; foreign finality per backend is still open (S16b) |
| **DERO reorganising** | un-happen a burn after it was paid abroad | a burn is paid only at or below DERO's stable height | `withdrawalFinal`, run in `poc/vault-e2e-v2-finality.log` |
| **the viewing-key holders** | see every deposit and withdrawal amount and timing on the foreign side | that is the design: the federation must see deposits to credit them; publishing the viewing keys makes the reserve auditable by anyone (S23, not yet done) | — |
| **whoever runs the DERO node you read from** | show you a wrong `sup` or a wrong wallet balance | run your own node; the app refuses non-loopback plaintext endpoints | `V.endpointURL` |

### 3. What is verifiable on chain today

- `sup` (wrapped supply), every mint vote, every `md_<id>` (a deposit id is minted once), every burn and its
  withdrawal record `(dest, amount, height, status, ftx)`, every release vote, bonds, the cap and its epochs,
  the halt state and its votes, slashes. Conservation `sup == Σ minted − Σ burned` is fuzzed
  (`tests/vault_fuzz_test.go`).
- Not verifiable on chain: that the federation actually holds the foreign reserve. Until viewing keys are
  published, the reserve is the signers' word plus their bonds.

### 4. What the chain reveals (linkage disclosure)

- A deposit index `j` is registered by a DERO address (`Register`, ring size 2: that address is public) and the
  attested foreign address for `j` is public. Anyone can link the DERO recipient of a mint to the foreign
  deposit address; what they cannot see is who paid into that foreign address, when the foreign chain is
  shielded.
- A withdrawal's `dest` is stored in plaintext on DERO: the foreign destination of every burn is public until
  encrypted destinations (S19) exist. Burn on DERO with that in mind: use a fresh foreign address.
- A DERO contract call at ring size 2 names its signer to every observer (`Extract_signer`); the vault's
  `Register`, `Burn` and every vote are ring-2 calls. Trading the wrapped asset on Veil can use the private
  (ring-16, named recipient) entrypoints; the wrapper's own calls cannot.

### 5. Failure modes you should expect

- **Halt.** Any k signers can halt minting and releases (reserve shortfall, key compromise, foreign-chain
  incident). Halted means: your wrapped units keep their claim, and nothing moves until k signers vote to resume.
- **Slash.** k signers can remove a signer and take its bond; the removed signer's pending votes are void.
- **Cap.** Deposits above the cap are not minted; the foreign funds sit at the deposit address until the cap is
  raised (k votes) or the signers return them, which is a manual process today.
- **Loss.** If k signers vanish, the reserve is unreachable; the bonds are what you get back, and only if the
  remaining signers vote to release them (not implemented: S21).

### 6. Wording rule

Say "federated", "bonded", "k-of-n signers hold the reserve"; never "trustless", "non-custodial" or
"decentralised" for wXMR/wZEC. The atomic-swap path (`contracts/swap.bas`) is the trustless one, and it is slow.


---

# SECURITY WRAPPER

## Security review — the wrapper (contracts/vault.bas v2)

Reviewed 2026-09-06 against the code as tested by `tests/vault_test.go`, after the adversarial reconnaissance of the
same day found five ways v1 could be frozen or captured. v2 fixes those; the remaining gaps are listed as open items
with backlog ids (`docs/history/HISTORY.md (earlier backlog)`). The wrapper is a k-of-n federation: the DERO contract is honest by
construction, the signers are trusted for custody on the foreign chain.

### Trust boundary, stated once

| party | can | cannot |
|---|---|---|
| k colluding signers | mint up to `cap` without a deposit; refuse to release withdrawals; move the foreign-chain reserve | mint above `cap`; raise `cap` above 3 × total bonds; mint the same deposit id twice; act while halted (they can un-halt with k votes) |
| fewer than k signers | vote, bond, attest | change any state that moves value; pin or redirect a deposit (v2) |
| the owner | add signers and seal, before sealing only | anything after `Seal()` |
| a user | register, burn (≥ minburn, minus wfee), read everything | mint, change caps, vote |
| the DERO chain | enforce every rule below | see the foreign chain |

Bounded loss: outstanding supply never exceeds `cap`, and `cap ≤ 3 × Σ bonds` is enforced on chain at `Seal`,
`SetCap` and every mint. The most k colluding signers can extract from the DERO side is `cap`, and they forfeit at
least a third of it in bonds if the others slash them.

### Guarantees enforced by the contract (each has a case in `tests/vault_test.go`)

- Mint needs k distinct signer votes on the same tuple `(j, amount)` for one deposit id; votes are counted per tuple,
  so a wrong first vote neither blocks nor redirects the deposit (v1 let one signer pin it forever).
- The recipient is the address registered under index `j` (`ua_<j>`); no address string round-trip (v1 stored a
  mainnet-prefixed `ADDRESS_STRING`).
- A deposit id mints at most once, ever (`md_<id>`); id length 64–80 covers `txid:vout`.
- Every vote family (mint, release, attest, cap, slash) is keyed by the slash epoch `se`; a slash bumps `se`, so a
  removed signer's counted votes are void and cannot combine with later votes.
- Slashing never takes the active count `na` below k; a signer cannot vote to slash itself.
- `SetLimits(maxdep, winmax, wdelay)` is owner-only and only before `Seal`; afterwards nobody, not even k signers, can
  raise them: a deposit above `maxdep` is refused at the k-th vote, mints over the current and previous 2000-block
  buckets are capped by `winmax`, and a `Released` vote does not count before `wdelay` blocks after the burn
  (`tests/vault_limits_test.go`). The daemons add a second layer: a withdrawal above `-max-withdrawal-bps` of the
  foreign reserve is not paid and triggers a `Halt(1)` vote.
- `Halt(want)` names the target state; a vote is counted under `hc_<se>_<he>_<want>_<bucket>` (slash epoch, halt
  epoch, target, 2000-block bucket) and k votes flip the state and open a new halt epoch. A stale vote from another
  bucket, another epoch or for the current state never counts (`tests/vault_test.go`, halt cases). The signer daemon
  remembers its halt vote per halt epoch and bucket, so a fresh shortfall after a resume is voted again.
- `SetCap` votes carry a cap epoch `ce`; a value can be voted again after other changes; the cap can never exceed
  3 × `bondtot`.
- Every signer holds at least `minbond` before sealing; bonds are summed in `bondtot`, reduced on slash.
- `Released(j, ftx)` needs k votes on the same foreign txid.
- Burn requires `minburn`, refuses more than `sup` (no underflow), deducts `wfee` into `feepool`, records the height.
- Mint, Burn, Released and Attest refuse while halted; Slash and Halt work while halted.
- Re-attestation: k votes on a new address bump `dver_<j>`; the history stays on chain.
- `InitializePrivate` refuses a second call (`initd`); owner power ends at `Seal()`.

### Attacks considered

| attack | outcome |
|---|---|
| one signer votes a wrong tuple first | v2: ignored unless it reaches k; the honest tuple still mints (test: rogue first vote) |
| k signers mint against no deposit | bounded by cap and the bond ratio; visible; slashable by the others |
| same deposit credited twice | refused (`md_`) |
| removed signer's earlier votes complete a decision | v2: void (epoch); test: stale votes after slash do not mint |
| slashing down to fewer than k signers | v2: refused (test: quorum floor) |
| k−1 stale halt votes plus one late vote | v2: bucketed and target-bound; a vote for the current state is refused |
| cap raised beyond bonds | refused at `SetCap`, `Seal` and `Mint` |
| cap value exhausted after one use | v2: cap epochs; test: 1000 → 2000 → 1000 |
| withdrawal closed on a disputed foreign txid | v2: k votes must name the same `ftx` |
| dust-burn griefing (fees paid by the federation) | `minburn` and `wfee`; each burn now costs the user more than the foreign fee |
| burn more than outstanding supply | refused |
| anonymous (ring > 2) call | `SIGNER()` invalid; `isg_` lookup fails closed |
| key collision through user-controlled strings | attest addresses are hashed (`sha256` hex, fixed length); ids are length-bounded and vote keys end with the fixed-length signer raw; family prefixes differ in their first two characters. v1's "suffix appended after it" argument is replaced by these invariants |
| storage/gas griefing | O(1) storage per entry; one index per address; attest strings ≤ 200 bytes; registration is free (rate limit open item) |
| DERO sent with a vote or burn | refused |

### What the contract cannot protect against

- The signers stealing or freezing the foreign-chain reserve. Mitigations live off chain: threshold custody,
  published viewing keys, bonds, the cap ratio, the low starting cap, and the atomic-swap exit hatch.
- A wrong attested address that k signers agree on: the user's deposit goes astray. Re-attestation by k votes fixes
  the address; the app must show the version and let users verify the derivation against the published viewing
  key (open item).
- Off-chain liveness: withdrawals are only as fast as the signers.
- Privacy: `Burn` still stores the destination in plaintext and `ua_<j>` links a DERO address to a deposit index.
  Encrypted destinations are open item S19; the linkage must be disclosed to users until then (S18).

### Open items (backlog ids)

S16 done for the sim backend (durable release intents, atomic state, reserve-shortfall Halt vote: `tools/vaultsigner/exactly_once_test.go`); S16b reorg detection and real-backend finality (DERO side done: a burn is paid only at or below the stable height, C6); S19 encrypted destinations;
S20 output_index and watcher quorum; S21 rotation and unbonding with epochs; S22 in part (per-deposit maximum, rolling mint window, withdrawal delay: `SetLimits` before Seal, `tests/vault_limits_test.go`; veto and outflow breaker still open); S23 published viewing keys, reserve invariant, deposit fee;
C4 done (`tests/vault_fuzz_test.go`, 600 random steps, conservation held after every one); a refundable deposit for `Register` (spam) and incremental
state reads in the daemon.


---

# MARKETING COPY

## Copy for wrapped XMR and ZEC — the sentences the app and the site may use

Every line here is consistent with `README.md (Trust statement)`. Anything not on this page needs a new line here first.

### Allowed

- "wXMR and wZEC are held by a bonded federation of k-of-n signers. Their bonds are on chain; the wrapped supply
  and every mint, burn and release are on chain."
- "Deposits are credited after N confirmations on Monero or Zcash (N is per chain and published); withdrawals are
  paid after DERO calls your burn final and the vault's withdrawal delay has passed."
- "Deposits above the vault's cap or per-deposit limit are not credited until the federation raises the cap; the
  funds wait at your deposit address."
- "The federation can halt minting and releases; your wrapped units keep their claim while it is halted."
- "Trading wXMR/wZEC on Veil is instant and pooled. Moving XMR or ZEC in and out is as slow as those chains."
- "If you want no federation at all, use the atomic-swap path. It is slower and needs a counterparty."
- "The address you burn to is public on DERO. Use a fresh one."

### Not allowed

- "trustless", "non-custodial", "decentralised bridge", "fully backed" (until viewing keys are published and
  the reserve is auditable), "insured", "guaranteed".
- Any number for the reserve that the reader cannot check on chain.
- Any promise about how fast Monero or Zcash confirm.

### The one-line description

"Wrapped XMR and ZEC on DERO: instant pooled markets, a bonded k-of-n federation holds the coins, everything the
federation does is on chain."
