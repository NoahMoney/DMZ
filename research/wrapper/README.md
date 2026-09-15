# The wrapper (wXMR / wZEC): trust statement, security review, what may be said

Everything a reader must know before trusting, running or describing the bonded k-of-n wrapper. The design documents are in `DESIGN.md`, the operator runbooks in `RUNBOOKS.md`, the formal model in `formal/`.


---

# TRUST STATEMENT

## Trust statement — wrapped XMR and ZEC on DMZ (wXMR, wZEC)

Written 2026-09-06 and revised 2026-09-07 against `contracts/vault.bas` v3, `tools/vaultsigner` (which speaks v3 and
refuses any other version) and the private-testnet/simulator runs recorded in
`research/crosschain/README.md (Resolved)` §8–§10. Every claim below is either enforced by the contract (with
the test that proves it) or named as a trusted party. Nothing here is "trustless".

### 1. What a wrapped unit is

One wXMR is a claim on one piclonero held by a federation of k-of-n signers in a Monero wallet they control
together; one wZEC is the same claim on one zatoshi held in transparent pay-to-script-hash k-of-n multisig
addresses (`OP_CHECKMULTISIG`, one key per signer, derived from the signers' published account keys: anyone holding
the descriptor published under `viewkey` derives every vault address and sums the reserve on the public Zcash chain;
`tools/vaultsigner/zcashvault`). Zcash deposits and the reserve are therefore public, like any transparent address,
and a wZEC withdrawal can pay only a transparent Zcash address. Orchard with re-randomized FROST (ZIP 312) is the
documented upgrade, not what runs (docs/HANDOVER.md §5.1). The claim is redeemed by burning the wrapped unit on DERO,
which records a withdrawal the federation pays on the foreign chain. On Zcash the destination receives the
withdrawal's `w_<j>_v` less the network fee of the paying transaction (ZIP 317's conventional fee, typically
0.0001–0.00015 ZEC), because what leaves the reserve must be exactly what `Released` removes from `sup`.

### 2. Trusted parties, in the order they can hurt you

| party | what they can do | what stops them | evidence |
|---|---|---|---|
| **k of the n signers, colluding** | pay themselves the foreign reserve; mint wrapped units against deposits that never happened | nothing on chain: this is the trust. Bonds are forfeited only by a `Slash` vote, which needs k votes: the honest signers can slash a colluding quorum only when there are at least k of them left, i.e. n ≥ 2k. With 2-of-3 a colluding pair keeps its bonds; the bonds deter a single bad signer, not a quorum. The bond bound (`cap / capratio > bondtot` is refused, with `capratio` fixed at install) bounds the exposure relative to what the signers have at stake, it does not make theft cost a fraction of the cap. A slashed bond goes to the treasury, not to the victims. A reserve below `sup` less the withdrawals in flight triggers a k-of-n `Halt` vote from every honest signer's daemon, which a colluding quorum can outvote | `tests/vault_test.go` (cap bound, slash floor), `tools/vaultsigner` reserve check |
| **the contract owner, before `Seal`** | choose the signers | `Seal` removes every owner power; after it the owner is nobody | `tests/vault_test.go` |
| **one honest signer's daemon, crashing** | pay a withdrawal twice, or never | durable intents (paying → paid → voted) with an idempotent foreign `Release`; a corrupt state file stops the daemon instead of emptying its memory | `tools/vaultsigner/exactly_once_test.go` |
| **fewer than k signers** | freeze nothing: a deposit is minted when k votes agree on `(j, id, amount)`; a lying signer's vote is a dead tuple | tuple-keyed votes, slash epochs voiding a removed signer's votes | `tests/vault_test.go` |
| **the foreign chain reorganising** | un-happen a deposit after it was minted | confirmation depth per chain before a vote (network table); a deposit that vanishes below policy depth is never voted | `chainTable`; foreign finality per backend is still open (S16b) |
| **DERO reorganising** | un-happen a burn after it was paid abroad | a burn is paid only at or below DERO's stable height | `withdrawalFinal`, run in `poc/vault-e2e-v2-finality.log` |
| **the viewing-key holders** | see every deposit and withdrawal amount and timing on the foreign side | that is the design: the federation must see deposits to credit them; publishing the viewing key is what makes the reserve auditable by anyone. `SetViewKey(key)` (k votes) shipped, so whether a given vault has published one is itself checkable on chain under `viewkey` | `contracts/vault.bas:129-143` |
| **the vault's treasury address** | take the accrued withdrawal fees (`ClaimFees`, treasury-only), and receive every registration fee and every slashed bond | nothing: it is named at install and there is no setter, so it can never be changed. If that key is lost, the fee units in `feepool` stay counted in `sup` for the life of the vault — a permanent, unredeemable claim on the reserve that no vote can clear | `contracts/vault.bas:68, 266, 434, 503` |
| **whoever runs the DERO node you read from** | show you a wrong `sup` or a wrong wallet balance | run your own node; the app refuses non-loopback plaintext endpoints | `V.endpointURL` |

### 3. What is verifiable on chain today

- `sup` (wrapped supply), every mint vote, every `mint_<id>` (a deposit id is closed once: 1 minted, 2 refunded),
  every burn and its withdrawal record `w_<j>_{to,v,f,by,h,rel,st,ftx}` (destination, amount, fee, burner, burn
  height, the height from which it may be released, status, foreign txid), every release vote, bonds, the cap and
  its epochs, the halt state and its votes, slashes. Conservation `sup == Σ minted − Σ burned` is fuzzed
  (`tests/vault_fuzz_test.go`).
- Not verifiable on chain: that the federation actually holds the foreign reserve. The contract can publish the
  foreign viewing key (`viewkey`, set by k votes with `SetViewKey`), which lets anyone count the reserve and check
  the invariant the signers check: `reserve >= sup − inflight`, where `inflight` is every burned withdrawal the
  contract has not closed. The subtraction is not a weakening — a payment leaves the reserve before the `Released`
  quorum records it, so between those two moments the reserve is legitimately short of `sup`. Comparing the reserve
  to `sup` alone halts on every ordinary withdrawal, and because `Released` refuses while halted, `sup` can then
  never fall and the halt can never clear; that froze the vault on its first withdrawal until it was found and fixed
  on 2026-09-07 (`tools/vaultsigner/reserve_test.go`). Until a given deployment has published a viewing key, that
  vault's reserve is the signers' word plus their bonds.

### 4. What the chain reveals (linkage disclosure)

- A deposit index `j` is registered by a DERO address (`Register`, ring size 2: that address is public) and the
  attested foreign address for `j` is public. Anyone can link the DERO recipient of a mint to the foreign
  deposit address; what they cannot see is who paid into that foreign address, when the foreign chain is
  shielded.
- A withdrawal's `dest` is stored in plaintext on DERO: the foreign destination of every burn is public until
  encrypted destinations (S19) exist. Burn on DERO with that in mind: use a fresh foreign address.
- A DERO contract call at ring size 2 names its signer to every observer (`Extract_signer`); the vault's
  `Register`, `Burn` and every vote are ring-2 calls. Trading the wrapped asset on DMZ can use the private
  (ring-16, named recipient) entrypoints; the wrapper's own calls cannot.

### 5. Failure modes you should expect

- **Halt.** A halt is a condition, not a decision. Every honest signer's daemon votes `Halt(1)` while the foreign
  reserve is below what is owed and votes `Halt(0)` on its own once it is not, so the halt forms and clears with
  nobody to ask and no one who can withhold the resume. Halted means: no registrations, attestations, mints, burns
  or releases. Your wrapped units keep their claim throughout, and a k-of-n `Veto` still returns the units of a burn
  that will not be paid — `Veto` carries no halted guard, because returning funds is the one thing a halted vault
  must still do. A halt voted for any cause the daemons cannot compute from public state is voted back up by them.
- **Slash.** k signers can remove a signer and take its bond; the bond goes to the treasury, never to holders, and
  the removed signer's pending votes are void.
- **Cap.** Deposits above the cap are not minted; the foreign funds sit at the deposit address until the cap is
  raised (k votes) or the signers send them back abroad and record it with `Refunded(txid, vout, ftx)` (k votes),
  which closes the deposit id so it can never later be minted.
- **Loss.** If k signers vanish, the reserve is unreachable and the wrapped units have no other backing: the bonds
  are *not* a payout to holders. A signer's bond leaves the contract only through `Slash`, and it goes to the
  treasury; `Unbond()` returns a bond only to a candidate who bonded and was never voted in. A lost or slashed
  signer is replaced by `AddSigner2` (k votes), which is the recovery path — for the federation, not for your claim.

### 6. Wording rule

Say "federated", "bonded", "k-of-n signers hold the reserve"; never "trustless", "non-custodial" or
"decentralised" for wXMR/wZEC. The atomic-swap path (`contracts/swap.bas`) is the trustless one, and it is slow.


---

# SECURITY WRAPPER

## Security review — the wrapper (contracts/vault.bas v3)

Reviewed 2026-09-06 against the code as tested by `tests/vault_test.go`, after the adversarial reconnaissance of the
same day found five ways v1 could be frozen or captured. v2 fixes those; the remaining gaps are listed as open items
with backlog ids (`docs/history/HISTORY.md (earlier backlog)`). Re-reviewed 2026-09-07 after the loop-19 changes
(`Tighten`, `Veto`, `Refunded`, `SetViewKey`, the size-tiered withdrawal delay, `regfee`, and the deletion of the
per-deposit maximum). The wrapper is a k-of-n federation: the DERO contract is honest by construction, the signers
are trusted for custody on the foreign chain.

### Trust boundary, stated once

| party | can | cannot |
|---|---|---|
| k colluding signers | mint up to `cap` without a deposit; refuse to release withdrawals; move the foreign-chain reserve | mint above `cap`; raise `cap` past the bond bound (`cap / capratio > bondtot` is refused); mint the same deposit id twice; mint, burn or release while halted (they can un-halt with k votes, and the halt clears itself anyway once the reserve covers what is owed) |
| fewer than k signers | vote, bond, attest | change any state that moves value; pin or redirect a deposit (v2) |
| the owner | add signers and seal, before sealing only | anything after `Seal()` |
| a user | register, burn (≥ minburn, minus wfee), read everything | mint, change caps, vote |
| the treasury | take the released withdrawals' fees (`ClaimFees`), and receive every `regfee` and every slashed bond | be changed: it is named at install and has no setter |
| the DERO chain | enforce every rule below | see the foreign chain |

Bounded loss: outstanding supply never exceeds `cap`, and `cap / LOAD("capratio") > LOAD("bondtot")` is refused on
chain at `Seal`, `SetCap` and every mint. The division is integer, so the enforced bound is
`floor(cap / capratio) <= bondtot`, permissive by at most `capratio − 1` base units; `capratio` is fixed at install
and is the operator's written-down price assumption relating foreign base units to DERO atomic units — there is no
oracle, and no one can change it afterwards. The most k colluding signers can extract from the DERO side is `cap`,
and they forfeit their bonds if the others slash them.

### Guarantees enforced by the contract (each has a case in `tests/vault_test.go`)

- Mint needs k distinct signer votes on the same tuple `(j, amount)` for one deposit id; votes are counted per tuple,
  so a wrong first vote neither blocks nor redirects the deposit (v1 let one signer pin it forever).
- The recipient is the address registered under index `j` (`ua_<j>`); no address string round-trip (v1 stored a
  mainnet-prefixed `ADDRESS_STRING`).
- A deposit id mints at most once, ever (`mint_<id>`); the contract builds the id itself as `HEX(txid) + ":" + vout`
  from a 32-byte txid (`STRLEN(txid) != 32` is refused), so one deposit output has exactly one id however a caller
  spells it. `Refunded` closes the same key with value 2: minted or refunded, never both.
- Every vote family (mint, release, veto, attest, cap, slash, tighten, view key, add signer) is keyed by the slash
  epoch `se`; a slash bumps `se`, and so does a successful `AddSigner2`, so a removed signer's counted votes are void
  and cannot combine with later votes.
- Slashing never takes the active count `na` below k; a signer cannot vote to slash itself.
- `SetLimits(winmax, wdelay)` is owner-only and only before `Seal`; afterwards only `Tighten(winmax, wdelay)` (k
  votes, safe direction only, with a `mindep` floor and a monotonic `wdelay`) can move them. There is deliberately no
  per-deposit maximum: a deposit above one could never be minted at all and nobody is guaranteed to exist who could
  send it back, so the limit would be a permanent freeze. Mints over the current and previous 2000-block buckets are
  capped by `winmax`, and a `Released` vote does not count before `w_<j>_rel`, which `Burn` sets to
  `BLOCK_HEIGHT() + wdelay`, plus `bigdelay` when the burn exceeds `bigbps` of the supply
  (`tests/vault_limits_test.go`). An outsized withdrawal is delayed, never refused, and the daemon has no
  withdrawal-size flag and no override: stopping a withdrawal takes a k-of-n `Veto` or `Halt`.
- `Halt(want)` names the target state; a vote is counted under `hc_<se>_<he>_<bucket>_<want>` (slash epoch, halt
  epoch, 2000-block bucket, target) and k votes flip the state and open a new halt epoch. A stale vote from another
  bucket, another epoch or for the current state never counts (`tests/vault_test.go`, halt cases). The signer daemon
  remembers its halt vote per halt epoch and bucket, so a fresh shortfall after a resume is voted again.
- `SetCap` votes carry a cap epoch `ce`; a value can be voted again after other changes; `cap / capratio > bondtot`
  is refused. `Slash` re-establishes that bound itself, lowering `cap` and bumping `ce` rather than leaving mints
  stuck.
- Every signer holds at least `minbond` before sealing; bonds are summed in `bondtot`, reduced on slash.
- `Released(j, ftx)` needs k votes on the same foreign txid.
- Burn requires `minburn`, refuses more than `sup` (no underflow), deducts `wfee` into `feepool`, records the burn
  height and the release height `w_<j>_rel`.
- Only Register, Attest, Mint, Burn and Released refuse while halted; everything else — Slash, Halt, Veto, Refunded,
  SetCap, Tighten, Bond, Unbond, AddSigner2, SetViewKey and ClaimFees — still works, because returning funds is the
  one thing a halted vault must still do.
- `Veto(j)` returns a pending withdrawal's units, fee included, to the address that burned them, so a user is never
  left with neither wrapped units nor coins.
- Re-attestation: k votes on a new address bump `dver_<j>`; the history stays on chain.
- `InitializePrivate` refuses a second call (`initd`); owner power ends at `Seal()`.

### Attacks considered

| attack | outcome |
|---|---|
| one signer votes a wrong tuple first | v2: ignored unless it reaches k; the honest tuple still mints (test: rogue first vote) |
| k signers mint against no deposit | bounded by cap and the bond ratio; visible; slashable by the others |
| same deposit credited twice | refused (`mint_`) |
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
| storage/gas griefing | O(1) storage per entry; one index per address; attest strings ≤ 200 bytes; registration costs exactly `regfee` (0 at install disables the fee), forwarded to the treasury, and one address may hold only one unattested index |
| DERO sent with a vote or burn | refused |

### What the contract cannot protect against

- The signers stealing or freezing the foreign-chain reserve. Mitigations live off chain: threshold custody,
  the viewing key published on chain by `SetViewKey`, bonds, the cap ratio, the low starting cap, and the
  atomic-swap exit hatch.
- A wrong attested address that k signers agree on: the user's deposit goes astray. Re-attestation by k votes fixes
  the address; the app must show the version and let users verify the derivation against the published viewing
  key (open item).
- Off-chain liveness: withdrawals are only as fast as the signers.
- Privacy: `Burn` still stores the destination in plaintext and `ua_<j>` links a DERO address to a deposit index.
  Encrypted destinations are open item S19; the linkage must be disclosed to users until then (S18).

### Open items (backlog ids)

S16 done for the sim backend (durable release intents, atomic state, reserve-shortfall Halt vote: `tools/vaultsigner/exactly_once_test.go`); S16b reorg detection and real-backend finality (DERO side done: a burn is paid only at or below the stable height, C6); S19 encrypted destinations;
S20 output_index and watcher quorum; S21 done (`AddSigner2` rotation on k votes, which bumps the slash epoch, and `Unbond` for a candidate never voted in; note the lifetime ceiling of 20 signer slots, which `Slash` never gives back); S22 done (rolling two-bucket mint window, withdrawal delay, size-tiered `bigdelay`, `Veto`: `tests/vault_limits_test.go`; no per-deposit maximum by design); S23 in part (`SetViewKey` publishes the foreign viewing key on k votes and `Register` charges `regfee`; the reserve invariant `reserve >= sup − inflight` is checked by the daemon, `tools/vaultsigner/reserve_test.go`);
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
  paid after DERO calls your burn final and the vault's withdrawal delay has passed. A large withdrawal waits longer;
  the extra delay is fixed at install and the height it becomes payable is recorded on chain when you burn."
- "There is no maximum deposit. A deposit larger than the vault has headroom for is not refused — it waits at your
  deposit address and is credited as soon as there is room, which a later withdrawal or a cap raise opens with
  nobody having to act."
- "A deposit below the vault's published minimum is not credited and must be returned to you off chain; check the
  minimum before you send."
- "The federation can halt minting, burns and releases while the reserve is short of the supply; the halt clears
  itself once it is not. Your wrapped units keep their claim throughout, and a k-of-n veto still returns the units
  of a burn that will not be paid — a veto is not blocked by a halt."
- "Trading wXMR/wZEC on DMZ is instant and pooled. Moving XMR or ZEC in and out is as slow as those chains."
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
