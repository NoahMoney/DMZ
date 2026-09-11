# DMZ — eight-agent adversarial audit

**Target** `fomo` @ `b5af173` (2026-09-08) · **Method** 13 agents — eight independent specialists, four
adversarial verifiers instructed to refute by default, one reconciliation pass · 3.0M tokens, 716 tool calls,
42 minutes · **117 raw findings → 114 survived verification → 14 distinct defects → 6 systemic causes.**

Full evidence for every finding — evidence, scenario, root cause, fix, verifier reasoning — is in
[`FINDINGS.md`](FINDINGS.md). This document is the analysis.

A finding marked CONFIRMED was checked against the code by a second agent trying to disprove it. Nothing here
claims a runtime behaviour that was not executed; §16 lists what still needs a chain.

| Severity | Count | | Verifier verdict | Count |
|---|---|---|---|---|
| P0 | 1 | | CONFIRMED | 95 |
| P1 | 28 | | PLAUSIBLE | 8 |
| P2 | 47 | | REFUTED | 1 |
| P3 | 35 | | unverified (harness defect, §6) | 13 |
| P4 | 6 | | | |

---

## 1. Executive summary

**DMZ is not ready to deploy, and a vault must not be sealed.** Six independent blockers, each sufficient alone.

The worst finding was reproduced on the real DVM, not argued. **One anonymous call drained a launch's entire
raise.** `launch.bas buyto` refunded a caller's overshoot at line 59 and checked `min_out` at line 60. The
`...To` wrapper turns that ordering into a double payment: the entrypoint returns 1, `refundto` pays
`ASSETVALUE(q)` — still the *whole* deposit, because the DVM never decrements `Assets` on a send — and the
wrapper returns 0, so both transfers commit. Measured: **2,052,806 taken from a 2,150,000 pot**, repeatable
until the contract is empty. It also fires accidentally, on an honest private buy whose cached quote raced
another buyer. Afterwards the contract still reports the pre-drain `real`, so every honest seller's transaction
reverts with an underflow while the app keeps advertising the market as liquid.

This is not an arithmetic mistake, which is why the fuzz differential could never have found it. It is a
property of statement order, and nothing in the repository was checking statement order.

The most instructive finding is about process. **Three of the four fixes made immediately before this audit each
left an adjacent hole, and two of them created the P1s that this audit then found.** The decimals fix reached
two of six price call sites. The impersonation fix guarded the ticker but not the display name rendered beside
it. The `Paying` interlock secured the veto axis but not the halt axis. The common shape is a fix applied to the
instances the author could see, behind an interface that still permits the mistake — `A.price(pt, dec, qdec)`
defaulted a missing `qdec` to DERO's 5, which made *I forgot* and *I meant DERO* the same call. The remedy is
not more care. It is that a fix should remove the *ability* to be wrong.

**The brief's contradiction is a missing decision, not a marketing error.** The brief describes settlement
"without pooled liquidity, wrapped coins, or a centralized custodian holding user funds". `swap.bas` and
`board.bas` — the atomic-swap half — are written, tested and undeployed. `docs/TODO.md` §2.1 records that the
federation-versus-atomic-swaps verdict "has never been answered properly" and that its output
`research/wrapper/MECHANISM_REVIEW.md` was never written. So the brief describes the deferred half of a decision
nobody has made. Meanwhile the shipped app is **more honest than the brief**: `wrap.js`, `token.js:225` and
`screens.js:67` all name the federation and its failure modes plainly. The exception is `app/notice.js` — the
page that declares itself authoritative — which omits the federation entirely and asserts three things that are
not true. The app's least accurate page is the one that outranks the others.

**What is genuinely good, and must survive the fix pass.** The trade sheet discloses each cost in the asset
actually charged, with gas as its own row. The Portfolio refuses to invent a cross-quote rate and writes down
why. `min_out` is enforced on chain in both contracts, with a 10% client-side cap, so a lying node can make a
trade revert or make a user decide badly but cannot make it execute at a bad price. `endpointURL` is a real
allow-list with per-session confirmation for remote nodes. And PROTO checked every product and difference in
`pool.bas` and `launch.bas` for wrap and truncation and found nothing — that is unusual, and worth saying.

### Fixed during this audit

| Finding | Fix | Proof |
|---|---|---|
| `PROTO·BC-1` **P0** | `buyto` records the overshoot and pays it past every failure exit | `TestNoExternalSendBeforeAFailureExit` fails on the pre-fix contract, naming line 59 |
| same shape in `pool.bas AddLiquidity` | same treatment, before a `...To` wrapper made it exploitable | the same control-flow test, now permanent |
| `DATA·D3-01` / `SEC·RT-03` P1 | `A.priceOf(market, pt)` reads both decimals off the market | browser: `c24h` −12.19% not −100%; last candle close ÷ header price = 1.0 |
| `DATA·D3-04` P1 | wizard parses graduation in the quote's decimals; contract ceiling 10¹² → 10¹⁵ | full DVM suite green; the ceiling was 10M DERO but only 1 XMR |
| `UX·F2` P1 | transaction allow-list uses `D.vaults()`, not the single-vault `cfg.vault` | every Zcash deposit and withdrawal was refused as "not a verified listing" |
| `DATA·D3-15` P2 | app installs `pool.bas` with the `quote` argument it has required since v2 | Create-a-pool could never succeed; `simsmoke` had the same omission |

---

## 2. Repository understanding

| Layer | What it is | Constraints that shape everything |
|---|---|---|
| `contracts/*.bas` | 10 DVM-BASIC contracts | uint64 only, silent wrap; `LOAD` of a missing key panics; only `IF cond THEN GOTO n`; strictly ascending line numbers; `SIGNER()` real only at ring 2; **install gas charged on source bytes, 20,000 cap** |
| `app/` | vanilla JS — no framework, no bundler, no runtime dependencies | served as an on-chain TELA site under `default-src 'none'`; **18,432 bytes per file** after gzip+base64 |
| `tools/` | Go operator tooling plus `vaultsigner` | `vaultsigner` is the one real production daemon |
| `tests/` | Go tests against the real DERO DVM | plus a 3,000-step fuzz differential against `reference/pool.go` |
| — | **no server, no database** | the browser talks to a DERO node over JSON-RPC and to the user's wallet over XSWD |

That last row governs how to read this report: findings that would elsewhere be "backend" are here about node
trust, the `localStorage` cache, and the in-browser data layer in `app/feed.js`.

---

## 3. Architecture map

```
browser  (TELA site, CSP: default-src 'none'; script-src 'self'; font-src 'self')
  rpc.js    ──JSON-RPC──>  DERO node        allow-listed; a remote node needs per-session confirmation
  xswd.js   ──WebSocket──> user's wallet    the only signer; no key material ever enters the page
  feed.js   data layer: incremental per-block sync, localStorage cache, template-hash verification
  stats.js  derived statistics        amm.js  pricing and quoting — a third implementation of the AMM maths
  screens:  home · markets · token · wrap · nfts · chat · portfolio · profile · notice

chain
  factory.bas  registry — permissionless, no dedup, unprunable, owned by nobody after deployment
  launch.bas   bonding curve, graduates into an AMM       pool.bas  constant-product AMM
  vault.bas    k-of-n bonded federation: custodies XMR/ZEC, issues wrapped claims
  founders.bas fee-sharing pass    nft.bas  chat.bas  social.bas
  board.bas · swap.bas             written, tested, undeployed — the atomic-swap half

off-chain
  tools/vaultsigner   one federation signer; holds a foreign-chain hot wallet; no operator exists after Seal
```

---

## 4. Trust-boundary model

| Boundary | Can it lie? | Can it vanish? | Authenticated | Integrity checked | Findings |
|---|---|---|---|---|---|
| user → browser | — | — | — | — | — |
| browser → DERO node | **yes** — forge state, omit keys, replay, stall | yes | no | template hash only, served by the same node | `SEC·RT-10`, `SEC·RT-05` |
| browser → wallet (XSWD) | no — user approves each TX | yes | per-session app id | the user reads the sheet | `SEC·RT-08` |
| `localStorage` cache | yes, if same-origin poisoning is reachable | yes | no | only the newest `VERIFY_N` points are re-read | `SEC·RT-06` |
| code → "Verified" badge | the node supplies both the code and the registry | — | — | hash match | `SEC·RT-05`, `SEC·RT-10` |
| **federation (k of n)** | **yes — it holds every deposited coin** | **yes, permanently** | on-chain signer set | a bond, but see `PROTO·BC-4` | `PROTO·BC-2/3/4` |
| foreign chain → signer backend | yes | yes | the backend's own view | `VerifyRelease` | `ARCH·V1` |
| treasury key | — | yes | — | — | `REQ·A8-04` |

**Trusted parties, inventoried.** The standing rule is to document every one. `app/notice.js` — the page that
claims final authority — documents none.

1. **The k-of-n federation.** Holds every deposited XMR and ZEC. At `n = k+1`, which `contracts/README.md`
   explicitly blesses, a colluding quorum is unslashable.
2. **Whoever holds the factory owner key.** One `SetTreasury` de-verifies every market at once.
3. **Whoever holds the TELA INDEX key.** Controls which code users load.
4. **The treasury key.** The Founders Pass promise needs it online forever.
5. **The DERO node.** Supplies both the contract code and the registry the app uses to judge that code.

---

## 5. Threat model

| Actor | Capability | Best attack found | Status |
|---|---|---|---|
| anonymous caller | send any transaction | `PROTO·BC-1` — drain a launch's entire raise | **fixed this session** |
| token creator | set tax and allocation, list freely | `PROD·P7-05` — drain "locked forever" liquidity through the tmode-1 tax | open |
| cheap sybil | ~0.1 DERO per registry entry | `PROD·P7-04` — permanent per-block RPC amplification on every client | open |
| one wallet, ~200 gas | trade | `PROD·P7-06` + `REQ·A8-05` — promote the thin half of a split pair to Home rank 1 | open |
| k colluding signers | quorum | steal the whole reserve; unslashable at `n = k+1` | open — `PROTO·BC-4` |
| malicious node | serve any state | mislead price and balance; **cannot** force a bad fill (`min_out` is on chain) | partly mitigated |
| impersonator | list a token | copy a wrapped asset's **display name** (the ticker is now guarded) | open — `SEC·RT-07` |

---

## 6. Findings by agent

> **A defect in this audit, stated plainly.** UX and PERF both numbered their findings `F1…F14`, and the
> verifier merge collated by bare id. Every PERF verdict except `F8` therefore carries UX's reasoning for the
> same number. **PERF's "CONFIRMED" labels are meaningless and its 13 other findings are single-source.** Its
> measurements are self-reported and reproducible from its own scripts, but nothing challenged them. Ids below
> are prefixed with the agent to remove the ambiguity. This is a harness defect of mine, not a finding defect.

### Agent 1 — Red team / security

Read in full: app/ui.js, feed.js, rpc.js, xswd.js, app.js, amm.js, sheets.js, forms.js, token.js, wrap.js, chat.js, stats.js, chart.js, live.js, notice.js, founders.js, nfts.js, index.html, config.js; contracts/pool.bas, vault.bas, chat.bas and most of launch.bas; tools/vaultsigner/main.go (header, Backend interface, processWithdrawal, main, send/vote, pass outline); plus the CSP/TELA/ringsize sections of DECISIONS.md, RISKS.md and docs/, and the git tracking of tools/.

XSS: I traced every chain-controlled string to the DOM and found no injection. h() sets attributes with setAttribute and appends children as text nodes; the single innerHTML in the app (chart.js:132) interpolates only S.fmtPriceSub / S.fmtDeroShort / fmtT / a number, none of which can emit markup. safeUrl restricts image and avatar URLs to https with no quotes or angle brackets, and images are off by default. No style value is built from chain data (avatar gradients and progress widths are numeric). No prototype pollution: getSC only ever assigns string or BigInt values into plain objects, so a "__proto__" key from a hostile node is a no-op, and every other object is keyed from app-generated lists. No key material can reach the browser: the only signing path is XSWD, the app id lives in sessionStorage, and V.jsonBig avoids a Number round-trip on amounts.

The real exposure is elsewhere, and it clusters in three places. First, the vault: the brief's contradiction is not just marketing — vault.bas has no expiry on a Burn and no timeout on a Halt, so a federation that stops or freezes strands user funds permanently, while both the contract header and the Wrap screen assert that doing nothing pays the user. The same contract publishes a DERO-address-to-foreign-address map that the app explicitly tells users does not exist. Second, the quote-decimals fix is incomplete in a way that is user-visible: every price-change window on a wrapped-quote market reads about -100% and the chart is 10^7 out of scale with the header price (reproduced numerically). Third, the verification story overstates itself: the registry treasury check fails open on an unparsable value while still showing "Verified", the impersonation guard covers the ticker but not the display name rendered next to it, and both the intro and Settings state "every listing is verified" without saying that the code and the registry come from the same node.

What held up under attack: min_out is enforced on chain in both pool.bas and launch.bas and app.trade caps slippage at 10%, so a lying node can make a trade revert or make a user decide badly but cannot make it execute at a bad price; endpointURL is a genuine allow-list with a per-session confirmation for remote nodes; MAX_FEE refuses an inflated gas estimate; the ...To entrypoints correctly avoid SIGNER() so ring 16 works and refund on failure is addressed; signer state files and binaries are gitignored and I found no keys or machine-specific paths in the tracked tree.

**Not checked.** Not read line by line: contracts/founders.bas, factory.bas, social.bas, board.bas, swap.bas, nft.bas; the second half of launch.bas (Stake/Unstake/Claim accumulator and the tick/post helpers, which appear to mirror pool.bas); app/home.js, markets.js, screens.js and nft.js were read only in the regions relevant to money formatting, warning surfaces and DOM sinks; tests/*.go and tools/uitest were not run.

Nothing was executed against a chain or a node — no simulator, no DVM, no Playwright pass — so every contract finding is a source reading. The specific things a runtime would settle:
- RT-01/RT-02: seal a simulator vault, burn, and confirm on chain that no entrypoint returns the units without k signers, and that Released is refused while halted.
- RT-03: open a TOKEN/wXMR market created by tools/simwrapmkt -quote <vault> and read the four change windows and the chart axis; I verified the arithmetic in node but not the rendered screen.
- RT-06: the same-origin precondition. TELA serves each site on localhost:808x (DECISIONS.md:254), so whether a second TELA site can land on a port DMZ previously used, and therefore share localStorage, needs a test against a real TELA host. The cache-verification weakness itself is confirmed from source regardless of how the origin is reached.
- RT-09: whether the 2000-block Veto window is reachable in practice depends on foreign-chain confirmation timing and halt duration; a soak against the sim backend with a stalled VerifyRelease would settle it.
- RT-10: the malicious-node chain is reasoned from code, not demonstrated; a stub node returning template source for an attacker scid would confirm the badge renders.
Also unexamined: whether DERO's GetRandomAddress-supplied burn destination weakens the ring at ringsize 16 (app.js app.tx picks it from the node). At ring 2 the signer is already public so it costs nothing; at ring 16 it may reduce the payer set by one. I could not settle DERO's ring construction from this repo and did not report it.

| Id | Severity | Title | Verdict |
|---|---|---|---|
| `SEC·RT-02` | P1 | Halt freezes burns AND releases with no timeout, and the Wrap screen tells the user halted withdrawals stay payable | CONFIRMED |
| `SEC·RT-03` | P1 | Every price-change window reads about -100% on any market quoted in a wrapped asset; the chart is out of scale with the header price by 10^(quoteDec-5) | CONFIRMED |
| `SEC·RT-04` | P1 | The vault publicly links a DERO address to its foreign deposit address and to every one of its deposit indexes, while the app claims fresh indexes are unlinked | CONFIRMED |
| `SEC·RT-01` | P2 *(auditor said P1)* | A vault Burn with no quorum is unrecoverable — there is no expiry path, and both the contract header and the Wrap screen claim the opposite | CONFIRMED |
| `SEC·RT-05` | P2 *(auditor said P1)* | The registry treasury check silently disables itself when the factory's treasury is unreadable, while the Verified badge still claims the treasury was checked | CONFIRMED |
| `SEC·RT-06` | P2 | The localStorage market cache re-reads only the newest 4 records, so a same-origin writer can forge an entire price history and trade feed that survives verification | CONFIRMED |
| `SEC·RT-07` | P2 | The vault-symbol impersonation guard inspects the ticker only; the attacker-controlled display name is rendered everywhere with no check | CONFIRMED |
| `SEC·RT-08` | P2 | The app sends vault Burn at ring size 2 although the contract never reads SIGNER(), and the screen states the ring-2 exposure as a contract requirement | CONFIRMED |
| `SEC·RT-09` | P2 | The Paying/Veto guard closes the common double-pay case but leaves a 2000-block window, and the mark cannot be renewed even while the signer is still retrying the payment | CONFIRMED |
| `SEC·RT-10` | P3 *(auditor said P2)* | 'Verified' is a claim about what one untrusted node reported, and the intro and Settings copy state it as absolute | CONFIRMED |
| `SEC·RT-11` | P3 | Three leftovers from the quote-decimals fix: a cross-quote volume sum on the profile screen, a DERO-scaled ClaimTax button, and a DERO-worded tax risk line | CONFIRMED |
| `SEC·RT-12` | P3 | The trade sheet's refund figure for a graduating buy does not match what launch.bas refunds | CONFIRMED |

### Agent 2 — Blockchain & protocol correctness

Read line by line: all ten contracts in contracts/ (vault.bas 538 lines, pool.bas 402, launch.bas 547, founders.bas 397, nft.bas, swap.bas, board.bas, social.bas, factory.bas, chat.bas); reference/canon.go and reference/launch.go; tests/harness/chain.go and the test files that fix the semantics (ringswap_test.go, vault_paying_test.go, launch_test.go); tools/vaultsigner/main.go (the real signer daemon) around the halt, veto and withdrawal paths; contracts/README.md's vault rule set and deploy/README.md's sealing checklist. To settle what a non-zero RETURN actually does I read the vendored node itself: vendor/derohe/dvm/sc.go Execute_sc_function, dvm/simulator.go SanityCheckExternalTransfers/ErrorRevert/ProcessExternal, dvm/dvm_functions.go dvm_assetvalue and dvm_send_asset_to_address, and blockchain/transaction_execute.go process_transaction_sc.

That reading changed the shape of the audit. A non-zero RETURN is NOT a plain return: sc.go:279-286 commits state only on a zero return and otherwise raises "Discarded knowingly", which reverts and (at ring size 2) refunds. So every GOTO 900 in the signed entrypoints is safe, and a whole class of "stranded value on a failed call" findings I was chasing does not exist. But the four ...To wrappers deliberately convert a failed inner call into RETURN 0 so they can refund a caller the node cannot identify above ring 2 - and that inverts the meaning of every GOTO 900 inside the helper they wrap. launch.bas is the one place where an external send sits before a failure exit, and the result is a full drain of a launch's raise by an unsigned call. I proved it on the real DVM: 2,150,000 in, 4,202,806 out, contract balance 2,150,000 -> 97,194.

The vault's k-of-n state machine is carefully built and the recently added Paying/Veto interlock is correct in the case it was tested for, but it has a hole on the halt axis: Released refuses while halted and Veto does not, and the Paying mark is one-shot and lapses after 2000 blocks. So any halt lasting longer than that window disables the interlock while leaving the reversal path open, and a withdrawal already paid abroad can be refunded on chain. Proved. It compounds: after such a veto the daemon's own reserve check goes permanently short, halted never clears, and Burn is blocked while halted, so the vault ends in a terminal freeze that no human can leave - which is exactly the failure mode the project's rules forbid and which vault.bas's header claims is impossible by construction.

Arithmetic is in much better shape than the state machines. I checked every product and difference in pool.bas and launch.bas for wrap and truncation: muldiv/muldivup/mulhi/divlu/nlz are a correct Hacker's Delight port with real overflow panics; the bps/bpsup decomposition is exact; rounding consistently favours the pool (outputs floor, fees ceil, LP mint floor); the phase-0 invariant ra - rem is genuinely constant so the graduation divisor can never be zero; the dist()/settle() 2^32 accumulators in both launch.bas and founders.bas have clamps that hold - I worked the bound and stot * 4294967295 cannot overflow because reaching that line requires stot <= 2^32-1. Slash's cap walk-down cannot overflow either. I found no arithmetic defect worth reporting, which is unusual and worth saying.

Three notes on things that are right, so they are not re-litigated: the repo's own test suite is fully green (59 s), so both P0s are live gaps in a suite its author believes is complete; the app and docs are scrupulously honest about the trust model and contradict only the owner's new brief, never each other; and the four recently-fixed items in my brief check out as correct as far as they go - the Paying mark, Canon comment-stripping, vault symbol reservation and the quote-decimals fix are all sound. The Paying fix is simply incomplete on the halt axis.

**Not checked.** Not reached, and why:

- app/ beyond the trade-submission path. I read app/app.js trade(), app/token.js quote/gas, and grepped for trust claims, only to establish whether BC-1 is reachable through the shipped UI (it is, and app/app.js:200's overshoot guard is client-side JS in an on-chain TELA site, so it is advisory only). app/feed.js, the localStorage cache, node-RPC trust and the CSP/18,432-byte caps are another agent's area and I did not audit them.

- tools/uitest/ui.mjs (Playwright) was not run. No browser or node runtime was exercised.

- The foreign-chain backends. tools/vaultsigner's Backend interface (Release, VerifyRelease, ValidDest, Reserve, Finalized) is where the actual XMR/ZEC custody lives, and I read only the call sites, not the implementations or research/crosschain/poc. BC-2's severity depends partly on how often two honest signers disagree about ValidDest for a valid destination; that is a runtime question. A multi-signer soak on tools/simvault with divergent backend versions, plus a forced halt held open past 2000 blocks, would settle it.

- I did not re-run the vault fuzzer at high VEIL_FUZZ_N (VEIL_FUZZ_N=100000 tests/run.sh -run Fuzz). It would be worth doing with a Halt step added to the step generator, since the current fuzz corpus never halts - which is why BC-2 survived it.

- Gas ceilings. Both my proposed vault.bas changes add source bytes to a contract that contracts/README.md records as having hit the 20,000-unit install budget twice. reference.Canon strips comments so there is headroom, but the post-fix install gas needs measuring before deployment; tests/vault_smoke_test.go already does this check.

- Storage-gas exhaustion as a denial vector (unbounded key growth in social.bas fl_ lists, chat.bas rooms, pool.bas rf_<txid> and p_<n> series) was noted but not measured. Whether an attacker can price a contract out of usability by inflating its tree is a chain-level question I could not settle from source.

- founders.bas PayBatch's compute-gas claim (the comment asserts ~0.41M per settle against a 10M limit, hence n <= 15) was read but not measured against dvm's ComputeCost table.

I left the working tree exactly as I found it: my three exploit tests were written to the scratchpad and into the gitignored vendor/derohe/veil copy, then removed. The one modified file in git status, tools/simvault/main.go, was already modified before I started (it adds a -funder flag) and is not mine.

| Id | Severity | Title | Verdict |
|---|---|---|---|
| `PROTO·BC-1` | P0 | launch.bas pays the overshoot refund twice: SwapDeroForAssetTo drains a launch's entire raise in one anonymous call | CONFIRMED |
| `PROTO·BC-2` | P1 *(auditor said P0)* | Vault: Veto works while halted but Released does not, so the Paying interlock lapses and a withdrawal already paid abroad can be refunded on chain | CONFIRMED |
| `PROTO·BC-3` | P1 | A vault halt has no expiry and blocks Burn, so k signers who never vote Halt(0) freeze every holder out of the only exit permanently | CONFIRMED |
| `PROTO·BC-4` | P1 | The federation bond deters nothing for users: Seal permits n = k+1 (a colluding quorum that can never be slashed), and a slashed bond goes to the treasury, not to the victims | CONFIRMED |
| `PROTO·BC-5` | P2 | The executable model has no equivalent of the ...To wrapper, so the differential and fuzz suites structurally cannot see BC-1 | CONFIRMED |
| `PROTO·BC-6` | P2 | launch.bas tmode 2: creator tax accrued before graduation is unreachable for ever if the token never graduates | CONFIRMED |
| `PROTO·BC-8` | P2 | The product brief describes swap.bas/board.bas, which are not deployed; the shipped settlement path is pool.bas + vault.bas, and the repo's own docs already say so | CONFIRMED |
| `PROTO·BC-7` | P3 | nft.bas Unlock does not delete mt_<addr>, so an edition is one-per-address-for-ever, not one-per-lock as documented | CONFIRMED |

### Agent 3 — Data layer & displayed numbers

Read in full: app/feed.js, app/stats.js, app/amm.js, app/live.js, app/rpc.js, app/config.js, app/chart.js, app/markets.js, app/token.js, app/screens.js (Portfolio + Profile + Settings), app/app.js, app/forms.js, plus the parts of app/home.js, app/sheets.js and app/wrap.js that render numbers. Read contracts/pool.bas and contracts/launch.bas in full to establish what p_<n> and t_<n> actually mean, and vendor/derohe/dvm/dvm.go + blockchain/transaction_execute.go to settle what a missing install argument does. Cross-checked git history (b5af173, d68094d, 399f1eb) and docs/TODO.md so I report what the recent fixes missed rather than what they already record. Where a claim was numeric I replicated the exact expressions in node rather than reasoning about them: the −99.99999% change figure, the two side-encoding validators, the fmtDeroShort collapse to "0.0000", and the launchBuy overshoot divergence from launch.bas.

Judgement of the area. The data plane's architecture is sound and better than it looks: PROBE covers every key that can move a displayed number (I checked ClaimFees, ClaimTax, Buyback and graduation against it), the cache's shape-check plus newest-N re-read is a real design rather than a gesture, published-trade volume is computed but never displayed so nothing double-counts with reserve flow, and the honest per-quote framing on the Portfolio and in the Markets/Home flow summaries is genuinely good work.

The failure is uniform and has one shape: the quote-asset programme converted the labels and left the arithmetic. Everything that names an asset was updated; roughly a third of what scales one was not. The decimals commit b5af173 fixed five of the seven sites in stats.js and left priceAt and candles — which is worse than not fixing it, because before that commit change() was internally consistent and now it reports a constant −100% on every window of every wrapped market, and the chart disagrees with the price directly above it by ten million. The same shape produces the launch wizard installing a target 10^7 too small under a label that names XMR, the Claim-tax button using fmtDero one line below a Buyback button that uses qdec correctly, and Profile's "Published vol" adding Monero to DERO on a screen whose sibling section explains at length why that must never happen. Separately, an old 0/1 trade-side encoding that the contracts never used survives in two validators, and it is quietly deleting every unpublished sell from the cost-basis ledger.

On the brief's framing question: the product claim of no pooled liquidity, no wrapped coins and no custodian is contradicted by the data layer at every level, and the code is more honest than the brief — feed.js, token.js and screens.js all state the federation trust assumption in plain words. What the data layer does overstate is certainty: block counts labelled as trade counts, quoted amounts persisted as fills, unknown decimals defaulted to DERO's, a truncated window offered as "all", a failed contract read presented as current beside a live block clock, and unlabelled Vol/Liquidity/FDV columns that sort three currencies against each other on raw magnitude.

Trade chain traced end to end (wallet balance → quote → fee → slippage → transaction → settlement → balance → portfolio): correct for a DERO-quoted pool trade of ordinary size — units, fee side, min_out, the burn-asset branch at app.js:207 and the outcome probe all check out. It breaks in four places: the fee/tax/refund display on a curve-completing buy (D3-14), the ledger recording the quote rather than the fill (D3-10), the sell record being erased (D3-03), and every quote-denominated figure in the chain being off by 10^(quoteDec−5) on a wrapped market (D3-01, D3-02, D3-05).

**Not checked.** No runtime verification. There is no node, wallet or simulator running in this session, so nothing was confirmed against a live chain or a rendered page — I did not run tests/run.sh (needs a DERO DVM harness build) or tools/uitest/ui.mjs (needs Playwright and a served TELA). Every finding is from source plus numeric replication of the exact expressions in node; the confidence levels reflect that.

Specific items a runtime or chain test would settle:
- D3-07 (graduation and Buyback counted as volume) is marked High rather than Confirmed because I inferred the resulting p_ sequence from launch.bas:71-73 and tick() rather than observing it. Settled by: drive a launch to graduation on the simulator, dump p_<n> across the boundary, and compare stats.flow() over that window against the sum of the real trades.
- D3-17 (reorg at equal pc/tc) is High for the same reason — the code path is unambiguous but I could not produce a reorg.
- Whether DERO block timestamps can ever move backwards. If they can, stats.js:15's binary search in priceAt and the early `break` in flow() at stats.js:43 both assume ascending ts, and validCache (feed.js:129) enforces monotonic height but not monotonic timestamp. I did not read the consensus timestamp rule, so I have not raised this as a finding.
- Whether an XSWD wallet's GetBalance returns promptly for the per-vault loop at screens.js:47 and the per-launch loop at app.js:83 on a registry with many markets — a serial wallet call per contract, unbounded, which could make the Portfolio slow rather than wrong.

Out of scope, deliberately not audited: vault.bas and the federation's own accounting, tools/vaultsigner, founders.bas fee distribution (docs/TODO.md §1.1 already records the wrapped-fee gap), chat.js/board.bas/social.bas, and the NFT pricing path (nft.bas is DERO-only by design; TODO §1.4 has it).

| Id | Severity | Title | Verdict |
|---|---|---|---|
| `DATA·D3-01` | P1 | Every price-change window on a wrapped-quote market reads ≈ −100.00% — a regression introduced by the decimals fix | CONFIRMED |
| `DATA·D3-02` | P1 | The chart and the price above it disagree by 10^7 on the same screen for a wrapped-quote market | CONFIRMED |
| `DATA·D3-03` | P1 | Every sell made on this device is written to the trade ledger and then permanently erased by the next write | CONFIRMED |
| `DATA·D3-04` | P1 | The launch wizard parses the graduation target in DERO's decimals whatever quote the creator picked, so a '100 XMR' launch installs a target of 0.00001 XMR | CONFIRMED |
| `DATA·D3-05` | P2 | Two quote-denominated figures were missed by the labelling pass: Profile's 'Published vol' adds XMR to DERO, and the Claim-creator-tax button prints a quote amount as DERO | CONFIRMED |
| `DATA·D3-09` | P2 | The localStorage cache is silently discarded for any market that has a published sell | CONFIRMED |
| `DATA·D3-10` | P2 | The trade ledger records the quote, not the fill, and records the amount sent rather than the amount the contract consumed | CONFIRMED |
| `DATA·D3-11` | P2 | The formatter and the risk thresholds are DERO-magnitude constants applied unchanged to XMR and ZEC amounts | CONFIRMED |
| `DATA·D3-15` | P2 | Create a pool cannot succeed: the install omits the `quote` argument pool.bas v2 requires | CONFIRMED |
| `DATA·D3-06` | P3 *(auditor said P2)* | The Markets table ranks and prints Vol / Liquidity / FDV across quotes on raw magnitude, and on desktop those three columns carry no currency at all | CONFIRMED |
| `DATA·D3-07` | P3 *(auditor said P2)* | Graduation and Buyback are counted as trading volume and as directional pressure, because the LP-move filter only runs on pools | PLAUSIBLE |
| `DATA·D3-08` | P3 *(auditor said P2)* | A market that fails to load stays on screen indefinitely with no staleness marker, and switching nodes invalidates nothing | PLAUSIBLE |
| `DATA·D3-12` | P3 *(auditor said P2)* | The Portfolio omits every token whose only market is a pool contract, and prints an unvaluable holding as zero | CONFIRMED |
| `DATA·D3-13` | P3 *(auditor said P2)* | An unknown quote asset and an unknown base asset both silently fall back to DERO's five decimals | CONFIRMED |
| `DATA·D3-14` | P3 | The trade sheet mis-states fee, tax and refund on the buy that completes a bonding curve | CONFIRMED |
| `DATA·D3-16` | P3 | Block-derived counts are labelled as trade counts, and 'all' range charts show only the retained window | CONFIRMED |
| `DATA·D3-17` | P4 *(auditor said P3)* | The in-memory incremental path never re-verifies existing history, so a reorg that keeps pc and tc unchanged is never corrected in-session | CONFIRMED |

### Agent 4 — Frontend, UX & visual identity

Read in full: docs/DESIGN_SYSTEM.md, docs/DESIGN_DIRECTIVE.md, app/styles.css, app/ui.css, app/parts.css, app/desk.css, app/index.html, app/config.js, app/wrap.js, app/home.js, app/markets.js, app/screens.js, app/notice.js, app/ui.js, app/live.js, app/amm.js (formatters), app/app.js (tx lifecycle, holdings), app/forms.js (launch wizard), app/sheets.js, app/token.js (token page + trade sheet), app/chat.js, app/feed.js (vaults/impersonation), app/stats.js (formatters), app/chart.js (interaction surface only). Cross-checked against contracts/vault.bas (Mint/Burn/Released/Paying/Veto/Halt/Refunded), contracts/launch.bas (grad bounds), tools/simvault/main.go (deployed vault defaults), tools/telainstall/main.go (on-chain headers), tools/uitest/ui.mjs (which widths are actually tested). Rendered evidence read from .sim/ui-dec/{01-home,02-wrap,04-token,08-launch-sheet,desk-home}.png and .sim/ui-imp/02-markets.png.

Judgement: the writing on this app is unusually good and the information architecture is mostly honest — the Portfolio genuinely leads with wrapped XMR/ZEC, the trade sheet puts every cost in front of the button in the asset it is charged in, and the wrap screen's walkthrough and trust panel are better than anything I expected. But the owner's two headline demands are not met. DERO still leads: the Home instrument's headline number is DERO flow, the telemetry tape carries fourteen copies of one token ticker and never names DERO, XMR or ZEC, the nav label is the jargon "Wrap", and the app's own on-chain description calls it "a launchpad for DERO assets". And "every cost easy to see" fails at the single most expensive point in the product: the wrap screen prints the vault's minimum deposit, mint cap and headroom as "0 XMR" because the formatter truncates any value below the requested precision to a bare zero — so the one unrecoverable mistake on a ring-signature chain is disclosed as "Minimum deposit: 0 XMR / Send at least 0 XMR to that address". Alongside that, the second vault (ZEC) cannot transact at all because app.tx's allow-list only knows the single legacy `cfg.vault`. The impersonation defence that was recently added exists in the data layer and on the token page but is a hover-only tooltip on the Markets list and entirely absent from Home and the activity feed. Failure states are the weakest area overall: there is no pending-transaction state anywhere, no retry anywhere, and an unreachable node produces skeletons that animate forever.

The visual thesis (true black, cut geometry, one lime diagonal) is real and mostly executed. The largest thing breaking it is not in the CSS: it is the hsl() gradient identity avatar on every market row and every feed item, which is the loudest colour on every screen in a system that permits exactly one accent.

**Not checked.** Could not run the app or the UI test: doing so needs the DERO simulator and tools/serve running plus a Playwright install (PW_DIR), and this session has no running node. So every rendering claim is either read from the committed screenshots in .sim/ui-dec, .sim/ui-imp and .sim/ui-canon (captured 2026-09-08 at 430px and 1280px) or derived from source. Specifically not verified at runtime:

1. F14's 390px tab-bar overflow. The arithmetic (7 cells x 55.7px against a ~56px "PORTFOLIO" label at 9.5px/700/.04em) is marginal and depends on the actual system font metrics. Settle it by running `PW_DIR=... node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-390` with a 390px viewport added and asserting `document.documentElement.scrollWidth <= 390`. 768px behaviour (the stats4 four-up inside a 430px container) follows directly from the two media queries and is high confidence, but I have no 768px render.

2. F8's exact cost of pressing Burn on a halted vault. The button being enabled is confirmed from source; whether the user then pays a network fee depends on whether DERO.GetGasEstimate errors on a contract that returns 1 at line 900, which I could not determine from the DVM behaviour in this repo. A chain test settles it: halt a simulator vault (tools/simvault Halt via k signers), press Burn, and observe whether app.tx reaches W.transfer or bails at the estimate.

3. Whether the deployed simulator vault's mindep is truly 1 base unit. tools/simvault/main.go:39,50 gives the flag defaults (cap 100000, mindep 1) but I did not read the actual on-chain state of d69c93… . This does not affect F1: fmtUnits(1n,12,8) and fmtUnits(100000n,12,4) both return the literal string "0" by inspection of app/amm.js:71-77, so the screen cannot express those values whatever they are. `go run ./simvault -vault <scid> -status` prints the real numbers.

4. Screen-reader behaviour for the nested `<button class="star">` inside `<button class="mrow">` (markets.js:129) — invalid HTML that the DOM permits because the rows are built with createElement rather than parsed. It renders and clicks work (e.stopPropagation at home.js:28). How NVDA/VoiceOver announce it needs a real AT test; I did not report it as a finding.

5. Whether any deployed vault currently has winmax set non-zero — F5's mint-window branch is real in contracts/vault.bas:59 but I could not confirm it is exercised in the shipped configuration.

6. docs/ui through docs/ui8w (thirteen directories of earlier design iterations) and docs/PLATFORM_GAP_MATRIX.md (71KB) were not read; they are history rather than shipped surface.

| Id | Severity | Title | Verdict |
|---|---|---|---|
| `UX·F1` | P1 *(auditor said P0)* | The wrap screen prints the vault's minimum deposit, mint cap and headroom as "0" — the one number whose whole purpose is to prevent an unrecoverable loss | CONFIRMED |
| `UX·F2` | P1 | The Zcash vault cannot transact: app.tx's contract allow-list only knows the single legacy cfg.vault | CONFIRMED |
| `UX·F6` | P1 | The launch wizard parses the graduation target at DERO's 5 decimals whatever quote is chosen, and labels every preview figure DERO | CONFIRMED |
| `UX·F3` | P2 *(auditor said P1)* | On Markets the impersonation warning is a hover-only tooltip behind a glyph shared with two weaker warnings; on Home and the activity feed it does not exist at all | CONFIRMED |
| `UX·F4` | P2 *(auditor said P1)* | The wrap screen states that doing nothing pays a withdrawal; the contract has no such path | CONFIRMED |
| `UX·F5` | P2 *(auditor said P1)* | A vault that cannot mint still reports "Open" and invites a deposit — headroom, bond ratio and the mint window are read and never used | PLAUSIBLE |
| `UX·F7` | P2 *(auditor said P1)* | There is no pending-transaction state anywhere: the sheet closes and a transaction that can take four and a half minutes is reported only by toasts that expire | CONFIRMED |
| `UX·P8` | P2 | Two buttons pass a boolean where the helper expects an attributes object, so they are never disabled — including Burn on a halted vault | CONFIRMED |
| `UX·F10` | P2 | --fg3 is 3.68:1 on the ground — every label, eyebrow, note, stat caption, inactive tab and empty-state body in the app fails WCAG AA | CONFIRMED |
| `UX·F11` | P2 | An unreachable node produces skeletons that animate forever, and no screen in the app has a retry | CONFIRMED |
| `UX·F12` | P2 | DERO still leads: the Home instrument, the telemetry tape and the app's own on-chain description all put DERO first and never name XMR or ZEC | CONFIRMED |
| `UX·F13` | P2 | The identity avatar is an hsl gradient on every market row and every feed item — the loudest colour on every screen in a system that allows one accent | CONFIRMED |
| `UX·F14` | P2 | 768px is untested and the app is a 430px strip there, while two media queries switch to wide layouts inside that narrow column | PLAUSIBLE |
| `UX·F15` | P2 | Modal sheet has no focus trap or focus return, nav tabs declare role=tab without aria-selected, the chart is pointer-only, and the tape can only be paused by hovering | CONFIRMED |
| `UX·F16` | P2 | The chain name is hard-truncated at 40 characters and lands mid-word in eight places on the wrap screen | CONFIRMED |
| `UX·F9` | P3 *(auditor said P2)* | Only the newest deposit index is readable, and the screen actively encourages creating one — hiding the address a deposit may already be in flight to | CONFIRMED |
| `UX·F17` | P3 | Meaningless badges, decorative statistics and a launch-sheet promise the app's own risk text contradicts | CONFIRMED |
| `UX·F18` | P3 | The page the app calls the one that outranks every other piece of copy is unreachable on mobile, and so is the build stamp | CONFIRMED |
| `UX·F19` | P3 | Vetoed withdrawals are described as a benign delivery failure, and the veto/Paying window is invisible | CONFIRMED |
| `UX·F20` | P3 | Fee and unit disclosure gaps: the token page's Market read-outs carry no quote symbol, the withdrawal fee names no recipient, and the contract id the copy tells you to check is truncated | PLAUSIBLE |
| `UX·F21` | P3 | An irreversible local wipe has no confirmation, the shoulder-surfing control is two hops from the screen it protects, and the reduce-motion setting does not do what it says | CONFIRMED |
| `UX·F22` | P3 | Every on-chain document is permanently described as "Veil", and the install is unfixable after deployment | CONFIRMED |
| `UX·F23` | P4 | Dead code and dead markup left behind by the redesign | CONFIRMED |

### Agent 5 — Performance & lightweight engineering

Read in full: app/feed.js, app/stats.js, app/amm.js, app/rpc.js, app/live.js, app/ui.js, app/app.js, app/home.js, app/markets.js, app/chart.js, app/founders.js, app/index.html, app/config.js; token.js, screens.js, chat.js, wrap.js, nfts.js in the parts that do per-block work; vendor/derohe/cmd/derod/rpc/rpc_dero_getsc.go and vendor/derohe/rpc/daemon_rpc.go to check what DERO.GetSC can return in one call; tests/ listing and tests/run.sh.

MEASURED, not guessed. `cd tools && go run ./genconfig -dist -network simulator` gives 31 files, 405,444 raw bytes, 189,276 gzip+base64 (DOC) bytes on chain. The cap question in the brief has a short answer: there is no cap pressure and no work to do there. The largest file is app.js at 13,168 DOC bytes = 71.4% of the 18,432 cap; wrap.js 13,156 (71.4%), token.js 13,076 (70.9%), screens.js 12,132 (65.8%). Every file has at least 5 KB of headroom. Do not spend effort shrinking files.

CPU was measured by loading app/amm.js and app/stats.js verbatim into a Node 22 VM context and driving the real functions with synthetic markets at the app's own in-memory caps (MAXP=4000 points, MAXT=2000 trades, feed.js:10). Bench scripts: C:\Users\<user>\AppData\Local\Temp\claude\c--Users-<user>-Desktop-codex-app-pigeon\318fe576-a54f-4d13-8d9a-a47534ee32a2\scratchpad\bench.mjs, bench2.mjs, bench3.mjs, bug.mjs. A desktop V8 was the host; a mid-range phone is roughly 3-5x slower.

Per block on the Home screen, at 120 markets at the caps: D.feed() 133.7 ms, S.enrich over all markets 35.5 ms, ten sparklines 69.9 ms — about 240 ms of JavaScript before any DOM work, every ~18 s block. At 60 markets x 2,000 points that falls to ~47 ms; at 20 markets x 500 points to ~7 ms, which is fine and needs nothing. So the app is fast enough today on a small deployment and falls over on a large one, and the three costs above are 100% of the difference. Everything else I looked at — markets.js row reuse and its sig-keyed sparkline gate, token.js's lastSig gate on chart and body, the debounced gas estimates in sheets.js/forms.js, chart.js candle counts (~60 regardless of range), the binary-search priceAt, the backward-walking flow/published windows — is already well optimised and I am not proposing work on any of it.

RPC volume is a static count off feed.js refresh(): 1 factory read + ceil(listings/250) id reads + one probe per pool/launch + one per NFT + one founders read + 2 per changed market. 120 markets, 10 NFTs, 5 changing = 143 JSON-RPC calls per block, issued 4 at a time (pmap, feed.js:230). Cold start with no localStorage cache is far worse: a market at the caps needs 6,000 history keys = 24 chunked calls (CHUNK=250, feed.js:10), so 120 markets is roughly 3,240 round trips, ~810 sequential rounds at concurrency 4.

I also found one confirmed correctness bug in my own measurement path and reproduced it (bug.mjs): the "A.price now takes quote decimals" fix listed as recently landed is incomplete, and every percentage change and every candle on a wrapped-quote market is wrong by 10^7.

On the brief-vs-shipped contradiction: in performance terms the entire cost structure I measured exists *because* of pooled liquidity and wrapped coins. The p_<n>/t_<n> history sync, the reserve-flow metrics, the candle engine, the split-pair grouping and the vault screen are all AMM machinery. A system that actually settled "without pooled liquidity, wrapped coins, or a centralized custodian" would not need feed.js at all. More sharply: F1 below — the single worst defect in my area — is a quote-decimals bug that can only exist because wrapped assets exist. The brief describes a product with none of this code in it.

**Not checked.** Could not check from source, and what would settle each:

1. DOM and rasterise cost. Every CPU number above is JavaScript only, measured in Node with no DOM. The element-construction cost of home.js's ~250 fresh nodes per block, the SVG parse/paint of ten 43 KB paths (F4), and the layout thrashing in F9 are all unmeasured. Settle with: a running simulator plus a Playwright performance trace over ten blocks on Home and on Markets at 100+ markets (tools/uitest/ui.mjs already has the launch scaffolding; it needs PW_DIR pointing at a playwright install, which is not present in this checkout). F9's magnitude in particular is marked Medium confidence for that reason.

2. Real-world scale. I benchmarked at feed.js's own in-memory caps (MAXP=4000, MAXT=2000) across 20/60/120 markets. The simulator's actual registry size is unknown to me — I did not start a node. If this deployment will never exceed ~20 markets with ~500 points each, then F3, F4 and F7 cost about 7 ms per block in total and are not worth doing; that is the honest answer at that scale and I would leave them alone. The findings are written against the caps the code itself declares.

3. localStorage quota behaviour (F2). The 5-10 MB per-origin figure is the standard browser range, not something I measured in this app. The measured half — 134,104 bytes per market at the caps, 15.35 MB at 120 markets — is exact. Settle by filling the store in a real browser and observing where setItem begins throwing, and whether it throws per-key or poisons the whole store.

4. Social contract scale (F6). No populated social contract exists to measure. The unbounded shape is proven from vendor/derohe/cmd/derod/rpc/rpc_dero_getsc.go (the p.Variables branch walks the whole graviton tree with a cursor, no limit), but the byte size at a given user count is an estimate. Settle with a DVM test that registers N profiles and measures the DERO.GetSC response size.

5. Background-tab throttling (F5). I verified from source that nothing on the block-driven refresh path checks document.hidden, and that the author's hidden-tab back-off at app.js:72 is cleared by scheduleRefresh before it can fire. What browsers actually do to those timers after a few minutes hidden — Chrome's intensive throttling, and whether an open WebSocket exempts the page — I did not verify and did not rely on. Settle by counting RPC frames in a hidden tab over ten minutes.

6. First-load round-trip totals (F2, F8). The counts (24 history chunks per market at the caps, ~3,240 round trips for 120 markets) are arithmetic off CHUNK=250, MAXP, MAXT and the call sites in loadContract. Wall-clock time depends entirely on node latency, which I state as a range rather than a measurement.

7. Contracts, wallet, XSWD and the federation. Outside this brief; I read none of contracts/*.bas, app/xswd.js or tools/vaultsigner beyond what F1 needed (the vault decimals in config.js).

| Id | Severity | Title | Verdict |
|---|---|---|---|
| `PERF·F1` | P1 | Quote-decimals fix is incomplete: every change window reads -100% and every candle is 10^7 off on a wrapped-quote market | *(unverified — id collision)* |
| `PERF·F2` | P1 *(auditor said P2)* | The localStorage market cache has no size budget: it silently exceeds quota at ~40 markets, and every page load then pays a ~3,000-round-trip cold start | *(unverified — id collision)* |
| `PERF·F6` | P1 *(auditor said P2)* | loadSocial does an unbounded whole-contract-tree read of the social contract on every page load | *(unverified — id collision)* |
| `PERF·F3` | P2 | D.feed() materialises and sorts every trade of every market on every block to render 24-60 rows | *(unverified — id collision)* |
| `PERF·F4` | P2 | Home rebuilds ten sparklines from full point history every block; each is a ~43 KB SVG path drawn into a 120x34 px box | *(unverified — id collision)* |
| `PERF·F5` | P2 | A backgrounded tab keeps doing full per-block refreshes; the hidden-tab back-off the author wrote is bypassed | *(unverified — id collision)* |
| `PERF·F7` | P2 *(auditor said P3)* | enrich() rebuilds a Set over every trade of every market, every block, for one number shown on one screen | *(unverified — id collision)* |
| `PERF·F10` | P2 | Zero JavaScript tests; the app's entire pricing and display layer is untested, and that is how F1 shipped | *(unverified — id collision)* |
| `PERF·F11` | P2 *(auditor said P3)* | A Settings toggle that does nothing, a CSP allowance for a client that does not exist, and five dead fields | *(unverified — id collision)* |
| `PERF·F12` | P2 *(auditor said P3)* | markets.js documents a 'KNOWN GAP' in pair grouping that no longer exists, and duplicates feed.js's impersonation rule in token.js | *(unverified — id collision)* |
| `PERF·F13` | P2 *(auditor said P3)* | The registry's full id list is refetched every block although its counts say it did not change, and markets.js retains a DOM row for every market ever listed | *(unverified — id collision)* |
| `PERF·F14` | P2 *(auditor said P3)* | The Wrap screen redraws every block from a vault snapshot it read once and never refreshes | *(unverified — id collision)* |
| `PERF·F8` | P3 | Two avoidable RPC round trips per contract on cold start, and a probe concurrency of 4 that is the wrong default | CONFIRMED |
| `PERF·F9` | P3 | flash() forces a synchronous layout per changed number, so a Markets redraw can thrash layout ~120 times in one tick | *(unverified — id collision)* |

### Agent 6 — Code quality & architecture

I read the shipped source rather than the docs, and verified every claim against files and computed hashes.\n\nRead in full: all 21 files in app/ (amm, app, chart, chat, config, feed, forms, founders, home, index.html, live, markets, nft, nfts, notice, rpc, screens, sheets, stats, token, ui, wrap, xswd — the JS ones completely, chart/live/nfts/wrap partially by targeted grep); contracts/pool.bas and vault.bas entrypoint signatures and the Paying/Veto/Released block; reference/canon.go and reference/pool.go; tests/canon_test.go, tests/harness/chain.go, tests/vault_paying_test.go, tests/run.sh, plus a coverage sweep of every tests/*.go for entrypoint names and reference use; tools/simrpc, genconfig, netcfg, simdeploy, simsmoke, simwrapmkt, simlaunch, telainstall, vaultsigner (main loop, send, vote, pass, markPaying), and headers of every other tool; deploy/networks.json; dist/simulator and dist/testnet including manifest.json; .gitignore and git ls-files.\n\nI independently recomputed sha256 for contracts/, app/templates/, dist/simulator/templates and dist/testnet/templates, and replicated Canon's comment-stripping to prove the derivation chain.\n\nOverall judgement: this is unusually careful code for its class — the comments explain trade-offs honestly, the fixes carry their reasoning, and the Go contract suite (fuzz-differential against a Go reference model, run through derohe's real DVM) is genuinely good. The maintainability risk is not sloppiness; it is that a small number of rules are expressed several times in several languages, and the discipline holding those copies equal is human memory rather than a check.\n\nThe pattern behind most of the findings is one thing: the quote-decimals rule. It was fixed in one function (A.price) and left unpropagated to stats.priceAt, stats.candles, and the whole launch form — so a wrapped-quote market shows −100% on every change window, a chart 10⁷ out from the price above it, and a graduation target 10⁷ below what was typed, immutably. Two accessors for the same quantity (BigInt D.qdec vs Number s.quoteDec) plus a silent fallback in A.price mean the next person to fix this can reintroduce it with the obvious spelling and no error.\n\nThe second pattern is validation placed after side effects. genconfig writes an entire bundle before checking its template hashes, so the failed testnet build left dist/testnet fully populated, manifest-less, and internally contradictory: config.js hashes none of the four templates sitting beside it. That is the real blast radius of the stale testnet record — not \"some old markets\", but forms.js unable to create anything and feed.js listing nothing current, from a directory telainstall would happily install to chain forever.\n\nThe third is error policy chosen for the wrong population: simrpc's log.Fatal is right for a simulator script and fatal for vaultsigner, the one unsupervised production daemon, on a project whose own rule is that state needing a human to leave it is a defect. The repo already knows (a comment at vaultsigner main.go:534 and a P1 in the backlog); it is still open.\n\nWhere less code would help most, concretely: A.price should take the market, not three loose numbers (deletes four call sites' worth of convention); decorate() should have a per-market half that token.js calls instead of the 1003-character duplicate at token.js:35; app.tx should expose one estimate() that sheets.js formats; simrpc should return errors and let the twelve sim tools wrap them; and app/templates == Canon(contracts) should be a line in canon_test.go rather than a side effect of a tool that needs a live chain and a wallet to run.

**Not checked.** Not reachable from source alone, and what would settle each:\n\n- The exact DVM failure mode of simsmoke's missing `quote` parameter (T1). Source proves pool.bas requires STRLEN(quote)==32 and that the argument is absent; whether the DVM raises a missing-parameter error or passes an empty string decides the error text, not the outcome. One simulator run of `go run ./simsmoke` settles it.\n- Whether the −100% change and the 10⁷ chart offset are visible today (Q1) depends on a market existing whose QUOTE is wrapped. The code path ships (pool.bas takes `quote`; forms.js offers the chips; zzquote_test covers it at contract level), but the simulator's seeded markets from simwrapmkt are WRAPPED/DERO, i.e. wrapped BASE, which is unaffected. Settled by `go run ./simwrapmkt` with a wrapped `-quote`, then opening that token page.\n- The hashchange-vs-setTimeout(0) ordering behind the dropped first global-search query (N1). Task-source ordering is not specified between the DOM-manipulation and timer sources; I marked the finding High on the structural coupling and did not claim the race resolves any particular way. A Playwright run typing into #gsearch from #home settles it.\n- Whether `dist/testnet` has ever been fed to telainstall (C1). `dist/` is gitignored, so the poisoned bundle is a local artifact; I confirmed telainstall walks a directory and never reads manifest.json, but not that anyone did it. deploy/tela-testnet.json and deploy/tela-testnet-reuse.txt would show the installed DOC scids — I read neither closely enough to date them against the template change.\n- Runtime confirmation of the async-render gap (E1). I proved the structural hole (7 async handlers, a synchronous catch, no unhandledrejection listener) but did not force a rejection to watch the skeletons persist. Stubbing Veil.stats.enrich to throw in the Playwright pass settles it.\n- Vault, launch and founders contract logic beyond the Paying/Veto/Released block and the entrypoint signatures — that is other agents' area and I stayed out of it, except where a constant crossed into the Go daemon (M1).\n- chart.js, live.js, nfts.js and wrap.js were read by targeted grep rather than line by line; wrap.js's error handling I did read in full because it is the model the other screens should follow.\n- I ran no tests. `tests/run.sh` needs the vendored derohe tree, which is gitignored and absent, so I could not confirm the suite is currently green — only what it does and does not assert.

| Id | Severity | Title | Verdict |
|---|---|---|---|
| `ARCH·Q1` | P1 | The quote-decimals fix reached one of four price call sites; priceAt and candles still assume DERO, so a wrapped-quote market shows −100% change and a chart 10⁷× off | CONFIRMED |
| `ARCH·C1` | P1 | genconfig writes every file before it validates, so the failed testnet build left a complete-looking bundle whose config.js hashes none of the templates beside it | CONFIRMED |
| `ARCH·V1` | P1 | vaultsigner, the one production daemon, is built on simrpc, whose entire error policy is log.Fatal — one transient node RPC error retires a federation signer permanently, and there is no operator | CONFIRMED |
| `ARCH·F1` | P1 | The launch form parses the graduation target at DERO's 5 decimals and labels every preview figure "DERO" whichever quote is picked, so a wXMR launch graduates at 10⁻⁷ of the intended target — immutably | CONFIRMED |
| `ARCH·T1` | P2 *(auditor said P1)* | tools/simsmoke installs pool.bas without the `quote` argument the contract has required since v2 — the end-to-end reserve differential cannot run | CONFIRMED |
| `ARCH·E1` | P2 | router.dispatch's try/catch is the app's only render safety net and cannot catch the seven screens that render from an async function; there is no unhandledrejection handler either | CONFIRMED |
| `ARCH·Q2` | P3 *(auditor said P1)* | "Quote decimals" exists in two incompatible types (BigInt from D.qdec, Number on s.quoteDec) and A.price silently defaults to 5 when handed the BigInt one | CONFIRMED |
| `ARCH·A1` | P3 *(auditor said P2)* | dupTicker and pool naming are implemented twice; the copy in token.js runs on the deep-link route and has already drifted — a shared link to the genuine wrapped market shows a hex ticker and a false "Ticker shared" warning | PLAUSIBLE |
| `ARCH·C2` | P3 *(auditor said P2)* | Nothing asserts app/templates == Canon(contracts); the tests test one file and the app ships the other, and the only bridge needs a live simulator and a wallet | CONFIRMED |
| `ARCH·G1` | P3 *(auditor said P2)* | The gas-estimate RPC is built twice and the two copies have already drifted on the field that matters: sheets.js estimates with a decoy signer for privacy, app.tx then estimates the same call with the real address | PLAUSIBLE |
| `ARCH·D1` | P3 *(auditor said P2)* | networks.json's vaultAsset block is dead data that contradicts the live record for the same contract — 5 decimals against 12 — and netcfg.Save silently deletes any field the struct does not know | CONFIRMED |
| `ARCH·M1` | P3 | Protocol constants are unnamed literals in four to twelve places each, and one of them (the thin-market threshold) is compared against a figure whose units now vary per market | CONFIRMED |
| `ARCH·X1` | P3 | amm.js is a third implementation of the AMM maths with no test of any kind; the contract and the Go reference are fuzz-differentialed against each other, and the JS that picks min_out is not | CONFIRMED |
| `ARCH·X2` | P4 *(auditor said P3)* | Two test files marked temporary ship and run as if permanent, and the Paying non-refresh property — the one the contract comment calls load-bearing — is stated and untested | REFUTED |
| `ARCH·N1` | P4 *(auditor said P3)* | window.Veil hides three real couplings: feed.js calls a method defined in another file, V.markets exists only while its screen is mounted, and app.js writes into the data layer's fields | PLAUSIBLE |
| `ARCH·Z1` | P4 | Dead exports and one dead field that is also wrong: app.pnl computes a per-asset average cost at hard-coded DERO decimals that nobody reads | CONFIRMED |

### Agent 7 — Product, economics, abuse & trust

I read the contracts that carry the product's economics end to end — contracts/launch.bas (all 547 lines), factory.bas, founders.bas, vault.bas, nft.bas, chat.bas, social.bas, board.bas — and the app layer that presents them: feed.js (the whole data layer), stats.js (every metric), amm.js, home.js, markets.js, token.js, forms.js, nfts.js, notice.js, ui.js, wrap.js (trust panel), plus tools/treasurybot, tools/simdeploy, tools/simvault and tools/simscam for the parameters an actual deployment uses, and RISKS.md, deploy/README.md, docs/TODO.md and docs/HANDOVER.md for what the team already knows.

Direct answers to the brief. What stops a rug: creator allocation is genuinely capped at 5% and paid at install; graduated liquidity is genuinely locked forever — launch.bas has no RemoveLiquidity, no owner and no UpdateCode, so the AMM reserve can only move through swaps, which is stronger than the timed locks the competition offers. That is the strongest thing in the system and it should be said louder. But the lock is not what it looks like: a tmode-1 creator tax of up to 10% is taken out of that reserve on every sell, forever, and the page still says "locked forever" (P7-05). There is no anti-snipe of any kind — no per-address cap, no cooldown, no launch delay — and because DERO balances are encrypted, the "Creator allocation: none" row is the only concentration signal the product can offer and it measures the one form that is already capped (P7-07).

Paid placement: none exists. I checked every ranking path — there is no boost, no featured slot, no promoted row. Ranking is purely organic, which makes P7-06 worse rather than better: `hot` is dominated by an uncapped per-block term that one wallet drives alone for the cost of ~200 gas fees an hour, and it feeds Home Trending, the Markets default sort, the Trade hub and the tape. Volume is inflatable at roughly 0.5% of the number displayed. The metric's own comment claims an anti-wash property the code does not have. Holder counts are not faked — they are labelled "Traders" with an honest caption — which is the right call.

Sybil: the factory's 0.1 DERO fee deters nothing and the registry is permissionless, dedup-free, unprunable and ownerless after deployment (P7-04). Impersonation: the WXMR ticker fix is correct but scoped to the ticker of a market — display names, NFT collection names and the Founders Pass itself are all still copyable unmarked (P7-09). Founders Pass economics: the known wrapped-asset gap is real and I confirmed both halves of it in source, but the sharper problem is that app/notice.js declares itself the authoritative page and states three things the code contradicts (P7-03). Vault economics: capratio is a nominal unit ratio with no price relationship, a colluding quorum can make itself unslashable, and slashed bonds pay the treasury rather than the victims — honest in RISKS.md R-14, absent from the panel a depositor reads (P7-10).

The single worst thing I found is not in that list of themes: the launch wizard parses the graduation target at DERO's five decimals no matter which quote is chosen, and launch.bas's own bound on that field is DERO-shaped too, so every wrapped-quote launch is created 10^7 too small and graduates on a dust buy (P7-01). Right behind it, three call sites in stats.js drop the quote-decimals argument the recent A.price fix introduced, so on the wrapped markets this product exists for, every change percentage reads about -100% and the chart disagrees with the header price by ten million times (P7-02). Both are on the flagship XMR/ZEC path, and both are the same class of defect the quote-asset programme was meant to close.

**Not checked.** I did not run anything. The DERO simulator was not started, tests/run.sh was not executed against a real DVM, and tools/uitest/ui.mjs was not run, so every finding is source-derived rather than observed on chain. Specific things that need a runtime or chain test to settle:

- P7-01 and P7-02 need a live wrapped-quote market to observe. The repo says one exists on the simulator (docs/TODO.md section 0 records a live BRW/SIM market), but SIM's decimals are not XMR's twelve, and stats.js's own comment says "Both simulator vaults happen to be five-decimal too, so nothing looked wrong" — which is precisely why these two bugs are invisible in the existing simulator setup. The settling test is: bring up a vault with dec 12, install a launch quoted against it, and compare the token page's header price with the chart's last-price tag, and check the four change pills.
- P7-04's amplification factor is arithmetic from the code paths, not a measured load. A simulator run registering a few thousand duplicate SCIDs and timing app refresh would give the real number.
- P7-10's collusion path (k signers slashing down to na == k) is read off the Slash guard; I did not execute it. A DVM test on a 2-of-4 vault would confirm the third slash reverts.
- P7-11's front-running window depends on DERO mempool visibility and block timing, which I could not observe. The no-lock, no-time-weighting property is confirmed from source; the practical exploitability is High rather than Confirmed.
- I did not audit pool.bas, swap.bas or chart.js line by line, nor the XSWD/wallet path, nor tests/*.go — other agents' areas, and nothing I found depends on them.
- I did not evaluate the atomic-swap board (board.bas) as an economic venue beyond reading it; it holds no funds and posts no rankings, so it was out of the abuse surface I was asked for.

| Id | Severity | Title | Verdict |
|---|---|---|---|
| `PROD·P7-01` | P1 *(auditor said P0)* | Every wrapped-quote launch is created with a graduation target 10^7 too small: the wizard parses it at DERO's 5 decimals, and launch.bas's own bound is DERO-shaped too | CONFIRMED |
| `PROD·P7-02` | P1 | On any wrapped-quote market every change percentage reads about -100% and the chart's prices are 10^7 times the header price, because priceAt() and candles() drop the quote-decimals argument that the recent A.price fix added | CONFIRMED |
| `PROD·P7-03` | P1 | The in-app plaque promises "All platform fees are distributed amongst the NFT holders" and "all rights revoked"; the shipped defaults distribute 50%, wrapped-asset fees can reach holders through no path at all, and every payout requires a live treasury key | CONFIRMED |
| `PROD·P7-04` | P1 | The factory registry is permissionless, deduplication-free, unprunable and owned by nobody after deployment: ~0.1 DERO per entry buys a permanent per-block RPC amplification on every client | CONFIRMED |
| `PROD·P7-05` | P2 *(auditor said P1)* | "Locked forever" liquidity is drainable by the creator: in tmode 1 every sell takes up to 10% of the AMM's quote reserve as creator tax, and the token page states the lock without qualification | CONFIRMED |
| `PROD·P7-06` | P2 | Trending is bought with gas: the `hot` score is dominated by an unbounded per-block term that one address can drive alone, and its own comment claims the opposite | CONFIRMED |
| `PROD·P7-08` | P2 | The Markets table ranks and displays volume, liquidity and FDV across different quote assets as bare unlabelled numbers, and the Trade hub sums them and labels the total DERO | CONFIRMED |
| `PROD·P7-09` | P2 | The impersonation guard covers tickers only: display names, NFT collection names and the Founders Pass itself can all be copied with no warning anywhere | CONFIRMED |
| `PROD·P7-07` | P3 *(auditor said P2)* | "Creator allocation: none" is only the install-time grant; the curve has no per-address cap, no cooldown and no launch delay, and DERO's encrypted balances make the resulting concentration unobservable to anyone | CONFIRMED |
| `PROD·P7-10` | P3 | The bond can be worth arbitrarily less than what a quorum could steal, and even when slashed it pays the treasury rather than the victims — the wrap screen's trust panel says 'slashable' and stops there | CONFIRMED |
| `PROD·P7-11` | P3 | Founders Pass rewards are pro-rata to whoever is registered at the instant of a deposit, with no lock and no time weighting, so a just-in-time registrant captures a share a long-term holder earned | CONFIRMED |

### Agent 8 — Requirements & what did we forget

Read exhaustively, from source: docs/TODO.md (the live list), docs/HANDOVER.md (all 368 lines), docs/DESIGN_DIRECTIVE.md, docs/DESIGN_SYSTEM.md, docs/ARCHITECTURE.md, docs/CREATOR_GUIDE.md, docs/PLATFORM_BACKLOG.md, README.md, RISKS.md, deploy/README.md (mainnet + TELA sections in full, headings elsewhere), deploy/networks.json, app/config.js, contracts/factory.bas in full, the relevant parts of contracts/pool.bas, vault.bas, founders.bas, contracts/README.md's invariant and vault-governance sections, app/notice.js in full, app/chat.js's route, and the relevant regions of feed.js, markets.js, token.js, screens.js, live.js, styles.css, tools/treasurybot/main.go, tools/uitest/ui.mjs, tools/genconfig, tools/telainstall, reference/canon.go; plus `git log --oneline -60` and several commit bodies. Ran two commands to establish facts rather than infer them: `go run ./genconfig -dist` (fails on testnet; produced the current per-file DOC sizes and the 31-file count) and a hand comparison of Canon(contracts/*.bas) against app/templates/*.bas (they match today).\n\nJudgement: the engineering is unusually honest at the level of the wrapper and the trade sheet — the app never says trustless, never says atomic, and states the federation, the ring sizes and the wrapped-quote caveat where a user will actually read them. The owner's product brief describing atomic settlement with no wrapped coins and no custodian contradicts the shipped system, but that contradiction does not appear in the UI; it appears in the absence of the one document that would have settled it (docs/TODO.md §2.1's federation-versus-atomic-swaps verdict, output research/wrapper/MECHANISM_REVIEW.md, never written). What is genuinely broken sits at the seams: things half-shipped across screens, and things the deployment runbook does not say.\n\nAgainst the owner's five standing requirements: (1) XMR/ZEC first and obvious — met; there is a Wrap tab, the Portfolio leads with wrapped assets before any DERO figure, and markets name their pair. (2) Balances easy to see — met, and deliberately never summed across quotes because there is no oracle. (3) Fees and costs easy to see — met in the trade sheet (each cost in the asset actually charged, gas as its own row); NOT met for the Founders Pass, where the UI states an unconditional promise the code deliberately does not keep for wrapped-quote markets or the asset leg (A8-08). (4) Nobody can extract more than was deposited — expressed as per-contract invariants that are fuzz-tested, but never stated as a system-level requirement, and the one control that lets a non-signer check it (proof of reserves from the published viewing key) has no tool, no procedure and no backlog item, while the app tells users it exists (A8-11). (5) No overlapping markets that let one be drained — half met: the split-pair defence was built entirely inside the Markets screen and is absent from Home, the tape, deep links, the token page and the trade sheet, which is where the drain happens (A8-05).\n\nThe most serious cluster is truthfulness and deployment: the app's own plaque asserts no owner, no possible code change and full fee distribution, none of which the contract set or the runbook delivers, and the UI test enforces one of those claims (A8-01); a single factory-owner transaction de-verifies every market the app has ever listed because the treasury pointer has no legacy list (A8-02); and the mainnet runbook never states the vault's per-chain parameters, four of which are immutable after an irreversible Seal (A8-03). Behind those sits a process gap: fifteen commits with no loop record, and a working list that no routing document links to.

**Not checked.** Not run and therefore not verified from execution: `bash tests/run.sh` (the ~450 s contract suite), `go test ./vaultsigner ./simrpc ./treasurybot`, and the Playwright passes in tools/uitest — all three need the simulator, tools/serve and tools/xswdwallet running, which I did not start. So every claim I make about test *coverage* comes from reading the test sources, and no claim about test *outcomes* is made.\n\nNot read end to end: DECISIONS.md (496 lines — I read only the images-default paragraph relevant to backlog item W10), docs/history/HISTORY.md (2 684 lines — I read the tail loop record and used git log for the rest), docs/PLATFORM_GAP_MATRIX.md (450 lines — headings, §1 and the counts only), research/wrapper/DESIGN.md and research/crosschain/RESEARCH.md, and the vendored research clones under research/farcaster-core, research/frost, research/mycelium, research/xmr-btc-swap.\n\nNot exercised: the app in a browser. Every UI finding is from source, so anything about rendered layout, contrast, mobile behaviour or the three design-critique passes required by DESIGN_DIRECTIVE §7.7 is unverified by me — TODO §4.3 records those passes as still open and I could not confirm or refute that.\n\nSpecific items that need a runtime or chain test to settle. (a) A8-02: a chain test on the simulator — deploy two markets, call factory `SetTreasury` to a new address, reload the app and confirm both markets go untrusted and vanish from every surface. I read the code path (feed.js:151) but did not drive it. (b) A8-03's magnitude: I derived the cap ceiling from the contract's own check (`cap / capratio > bondtot` → refuse) and the units statement in contracts/README.md:243-247; the exact unusable-cap figure depends on the bonds actually posted, so treat the arithmetic as the finding and TODO §0d's specific number as unverified by me. (c) A8-05: whether the thin duplicate genuinely reaches Home in practice depends on the `hot` score, which I read but did not run against synthetic histories. (d) Whether the operator in fact holds or has destroyed the factory owner key and the TELA INDEX key is unknowable from the repository — that is precisely why A8-04 asks for a documented, verifiable disposition step.\n\nOne thing I deliberately did not treat as a finding: the atomic-swap versus bonded-federation contradiction produces no false claim anywhere in the shipped app (grep for atomic / non-custodial / trustless / fully backed / insured across app/*.js returns only correct, caveated uses). The gap is documentary — the verdict was never reached and MECHANISM_REVIEW.md was never written — and is reported under A8-14 rather than as a UI defect.

| Id | Severity | Title | Verdict |
|---|---|---|---|
| `REQ·A8-01` | P1 | The app's designated final word makes three claims the shipped deployment cannot deliver — and the UI test locks one of them in | CONFIRMED |
| `REQ·A8-03` | P1 | The mainnet runbook cannot produce a working vault: it never says how to choose the per-chain parameters, and none of them can be changed after Seal | CONFIRMED |
| `REQ·A8-04` | P1 | Nothing in the deployment runbook disposes of the three keys that outlive the operator, and the founders promise requires a human to run a bot forever | CONFIRMED |
| `REQ·A8-05` | P1 | The owner's 'one market drained while another is left behind' requirement is defended on the Markets screen only — not where the trade is signed | CONFIRMED |
| `REQ·A8-02` | P2 *(auditor said P1)* | One factory owner transaction de-verifies every market the app has ever listed; template hashes have a `legacy` list, the treasury pointer has none | CONFIRMED |
| `REQ·A8-06` | P2 | The current working list is invisible: every routing document sends the next agent to a backlog that predates all recent work | CONFIRMED |
| `REQ·A8-07` | P2 | Fifteen commits — the whole quote-asset and visual-identity programmes — with no loop record, against a rule that requires one per loop | CONFIRMED |
| `REQ·A8-08` | P2 | The Founders Pass promise is rendered as unconditional in the UI while the code deliberately excludes wrapped-quote markets and the asset leg | CONFIRMED |
| `REQ·A8-09` | P2 | `genconfig -dist` fails outright: all four testnet template hashes are stale, so the on-chain testnet app can no longer be refreshed | CONFIRMED |
| `REQ·A8-11` | P2 | The app tells users anyone can verify the reserve; no tool, procedure or backlog item exists to do it | CONFIRMED |
| `REQ·A8-10` | P3 *(auditor said P2)* | The only build identity a user is shown is a hand-edited constant, and it is already wrong | CONFIRMED |
| `REQ·A8-12` | P3 | chat.bas ships as a top-level tab with a deployed contract and has no entry in the contract reference, the README or the architecture | CONFIRMED |
| `REQ·A8-13` | P3 | No guard that app/templates matches contracts/, and the UI test still covers three of the five required widths | CONFIRMED |
| `REQ·A8-14` | P3 | Five documents that the project's own plans and gates require do not exist | CONFIRMED |
| `REQ·A8-15` | P3 | The published file and DOC counts — which the mainnet funding estimate rests on — are two files and three DOCs out of date across four documents | CONFIRMED |
| `REQ·A8-16` | P4 | DESIGN_SYSTEM.md says six tokens are deleted; they are still defined as aliases, and one dead computation remains in live.js | CONFIRMED |

---

## 7. Cross-agent reconciliation

The reconciliation pass merged, deduplicated and ranked all 117 findings, arbitrated auditor-versus-verifier
disagreements, and identified the systemic causes. It is reproduced in full because its analysis — particularly
§2 (systemic root causes), §3 (the dependency map) and §9 (what all eight agents collectively missed) — is the
most valuable output of the exercise.

## DMZ — cross-agent reconciliation

**Spot-checks I ran before synthesising** (all confirmed): `app/stats.js:16` `price(pts[…], s.dec)` — two args; `app/stats.js:83/88/90` — two args; `app/amm.js:99` defaults an omitted `qdec` to 5; `app/forms.js:63` `A.parseUnits(f.grad.value, 5n)`; `app/forms.js:138` installs `pool` with four args while `contracts/pool.bas:15` and `app/templates/pool.bas:1` both require five; `contracts/launch.bas` line 59 `SEND_ASSET_TO_ADDRESS(s, d - used, q)` precedes line 60 `IF out == 0 || out < min_out THEN GOTO 900`; `contracts/vault.bas` has `halted` guards at 257/281/306/368/398 and **none in Veto**.

---

### 0. A process defect in the audit itself — read this first

**The PERF agent's verdict column is not its own.** UX and PERF both numbered findings `F1…F14`, and the verifier merge collated by bare id. Every PERF verdict except **F8** carries UX's reasoning for the same number: PERF F1 (quote-decimals) shows UX F6's forms.js verdict; PERF F2 (localStorage budget) shows UX F2's `app.js:113` vault-allowlist verdict; PERF F3 (`D.feed()` cost) shows UX F3's impersonation verdict; and so on through F14.

Consequence: **PERF F1–F7 and F9–F14 were never adversarially verified.** Their "CONFIRMED" labels are meaningless. Treat them as single-source claims. PERF F8 (three round trips per contract on cold start, concurrency 4) is the one genuinely verified PERF finding. PERF's own measurements are self-reported and reproducible from its scratchpad scripts, so they are not worthless — but nothing challenged them, and PERF F1 is a duplicate of a cluster four other agents already confirmed, which is how the collision went unnoticed.

Also: the final verifier note claims "nothing in the finding set covers" the `F.pool` missing-`quote` defect. **D3-15 covers it exactly**, same file, same line, same failure path. Two agents found it independently; the note is a false novelty claim.

---

### 1. Deduplication — 89 findings collapse to 14 defects

#### DEFECT 1 — quote decimals are not threaded through the historical-price path
**Root layer: `app/amm.js:99`** (silent `qdec → 5` fallback), symptom layer `app/stats.js`.
Merges: **RT-03, D3-01, D3-02, Q1, P7-02, PERF-F1** — six agents, one bug, four call sites (`stats.js:16`, `:83`, `:88`, `:90`).
Report once as RT-03. D3-01 adds the sharpest fact nobody else has: this is a **regression the fix created** — before commit `b5af173` both sides used 1e5 and the error cancelled.

#### DEFECT 2 — quote decimals are not threaded through the creation path
**Root layer: `app/forms.js:63` + `contracts/launch.bas:15`.**
Merges: **D3-04, UX-F6, ARCH-F1, P7-01**. Four agents. Report as D3-04.
Not the same defect as #1 — different file, different mechanism (hard-coded literal vs omitted argument) — but the *same root cause* (see §2.A). ARCH-F1 and UX-F6 each add half of what the others miss: the chips/label/note are built before `openSheet` and never re-rendered by `update()`, so the form says "DERO" after you click XMR; and `forms.js:86` computes the preview price with a two-arg `A.price`, so the preview numbers are wrong *and* mislabelled.

#### DEFECT 3 — display helpers with a hard-coded DERO scale
Merges: **UX-F1** (`fmtUnits` truncates the vault minimum to `"0 XMR"`), **verifier-note-1** (`fmtDeroShort`'s 4-decimal floor renders every shipped wrapped aggregate as `"0.0000"`), **D3-11** (thin-market `< 20`, `hot` volume term, formatter), **RT-11/D3-05** (`fmtDero` leftovers on Profile vol and ClaimTax), **M1** (`FirstDepositMin`/`MinLiq` meaningless at 12 decimals), **D3-13** (unknown asset ⇒ assume 5).
Root layer: `app/amm.js` `fmtUnits`/`fmtDero` and `app/stats.js:121` `fmtDeroShort`. UX-F1 is the sharpest instance because the number it erases is the one that prevents an unrecoverable loss.

#### DEFECT 4 — the `…To` wrapper inverts failure semantics
**Root layer: `contracts/launch.bas` `SwapDeroForAssetTo` + `buyto` line ordering.**
Merges: **BC-1** (the P0 drain, reproduced on the DVM), **BC-5** (no reference model for any wrapper, so the fuzz suite structurally cannot see it), **verifier-note-1** (`pool.bas AddLiquidity` has the same send-before-check shape and is safe only because it has no wrapper yet).
BC-1 is the finding. BC-5 is its cause. The verifier note is its next occurrence.

#### DEFECT 5 — no terminal vault state has a time-bounded exit
**Root layer: `contracts/vault.bas` — `halted` is a boolean with no lease; withdrawal settlement has no timeout branch.**
Merges: **RT-01** (Burn with no quorum is unrecoverable), **RT-02** (Halt blocks Released), **BC-2** (Veto works while halted, Released does not — reproduced), **BC-3** (Halt blocks Burn forever — reproduced at +2,000,000 blocks), **RT-09** (Paying mark is one-shot, lapses at 2000 blocks), **verifier-note-2** (vaultsigner re-pays abroad under an expired mark).
Six findings, one state machine. Fixing the state machine retires all six.

#### DEFECT 6 — market identity/safety attributes are computed per-screen
**Root layer: `app/feed.js decorate()` operates on the whole map, so any caller needing one market reimplements it.**
Merges: **RT-07** (impersonation checks ticker only; name unguarded), **UX-F3** (warning is hover-only on Markets, absent on Home and the feed), **P7-09** (NFT names/collections never checked at all), **A1** (`token.js:35` is a 1000-char duplicate of `decorate()` that has drifted), **A8-05** (split-pair defence lives entirely inside `markets.js`), **PERF-F12** (same duplication + a stale "KNOWN GAP" comment describing a bug that is already fixed).
A8-05 is the most serious member: the split-pair defence is absent from every surface where the trade is actually signed.

#### DEFECT 7 — one side encoding, two wrong validators
Merges: **D3-03** (unpublished sells are erased from the ledger on the next write) and **D3-09** (the whole localStorage cache is discarded for any market with a published sell). Both are `feed.js` validators asserting `side ∈ {0,1}` against contracts that emit `{1,2}`. One constant fixes both.

#### DEFECT 8 — validation placed after side effects in the build pipeline
Merges: **C1** (`genconfig` writes the whole bundle before checking hashes; `dist/testnet/` is on disk right now, complete-looking, manifest-less, and its `config.js` hashes none of the four templates beside it), **A8-09** (`-dist` exits 1; all four testnet hashes stale, not just founders), **C2** (nothing asserts `app/templates == Canon(contracts)`), **A8-13** (same, plus two missing viewports).
C1 is the root; A8-09 is its current instance; C2/A8-13 are the missing guards that would have caught it.

#### DEFECT 9 — categorical UI claims with no mechanism behind them
Merges: **A8-01** (`notice.js` — owner "none", "no further changes", "all fees"), **P7-03** (same page, from the economics side), **A8-08** (founders promise vs wrapped-quote exclusion), **UX-F4/RT-01/RT-02** (wrap screen: "doing nothing pays"), **RT-04** (deposit-index unlinkability), **RT-05/RT-10** ("every listing is verified"), **P7-05** ("locked forever" while tmode-1 tax drains the reserve), **P7-07** ("nothing to rug"), **UX-F19** (vetoed = "destination could not be paid"), **verifier-note-4** (`notice.js` documents no trusted party at all while claiming final authority), **BC-8** (the owner's brief).
Eleven findings, one missing artefact (§2.C).

#### DEFECT 10 — fail-open guards and no error state in the data layer
Merges: **RT-05** (`registry && treasuryCanon && cond` short-circuits), **D3-08(1)** (a failed read silently re-adopts the previous record under a live block clock), **UX-F11** (no `lastError`, so skeletons animate forever), **E1** (`router.dispatch`'s synchronous catch cannot see six async screens; no `unhandledrejection`).
One `lastError` field plus one `if (r && r.then) r.catch(…)` retires three of the four.

#### DEFECT 11 — cross-quote comparison
Merges: **D3-06**, **P7-08**, **D3-11 thresholds**, **RT-11/D3-05 Profile vol**, plus the item the verifier found that no finding names: **`app/home.js:179`** sums `vol24` across every quote and prints it as `" DERO"` on the Trade hub. That one is the genuine cross-quote *sum*; the Markets columns are a cross-quote *ranking*, which `docs/TODO.md` §1.3 already records as open.

#### DEFECT 12 — trusted parties are neither inventoried nor checkable
Merges: **BC-4** (`n = k+1` seals; a colluding quorum is unslashable; slashed bond → treasury), **P7-10** (capratio is a nominal ratio with no oracle; same slash-destination point), **A8-11** (the app tells users a viewing key makes the reserve checkable; no tool, no runbook, no backlog item — `grep viewkey tools/**/*.go` returns one comment), **A8-04** (three keys survive the runbook with no disposition step; the founders promise needs a daemon forever), **A8-02** (one `SetTreasury` de-verifies every market), **RT-10** (verification inherits the node's honesty).

#### DEFECT 13 — no assertion layer between the Go suite and the browser
Merges: **X1**, **PERF-F10**, **BC-5**, **C2**, **A8-13**. `app/amm.js` is a third implementation of the AMM maths with zero tests; `tools/uitest/ui.mjs`'s 44 assertions include not one money-maths comparison. This is why Defects 1, 2, 3, 7 and 14 all shipped green.

#### DEFECT 14 — per-block work scales with total history
Merges: **PERF-F3** (`D.feed()` materialises and sorts every trade of every market to draw 60 rows — 133.7 ms at the caps), **PERF-F4** (ten 43 KB sparkline paths per block), **PERF-F7** (a `Set` over all trades for one number on one screen), **PERF-F2** (cache exceeds quota at ~40 markets and the empty `catch` hides it), **PERF-F13**, **PERF-F5**, **PERF-F6**. All unverified per §0, but internally consistent and cheap to settle.

**Standalone, not merged:** BC-6 (tmode-2 tax stranded), BC-7 (nft.bas `mt_` never deleted — note the finding's line numbers are wrong; the file is 127 lines and the real lines are 2/65/78/93-95), D3-10 (ledger records the quote, not the fill), D3-12, D3-14/RT-12 (launchBuy overshoot mirror), D3-15 (pool install missing `quote`), D3-16, D3-17, T1, V1, UX-F2 (ZEC vault unusable), P8 (boolean passed where attrs expected), UX-F9, UX-F10 (contrast), UX-F13 (gradient avatars), UX-F14, UX-F15, UX-F16, UX-F18, UX-F21, UX-F22, P7-04 (registry sybil), P7-06 (`hot` gameable), P7-11, D1, M1, N1, Z1, A8-06, A8-07, A8-10, A8-12, A8-14, A8-15, A8-16, UX-F17, UX-F20, UX-F23, G1.

---

### 2. Systemic root causes

Six causes produce 62 of the 89 findings.

#### A. A dangerous default that makes forgetting indistinguishable from meaning it
`app/amm.js:99`: `10 ** (Number.isInteger(qdec) ? qdec : 5)`. The comment above it says the fix exists so "every new caller inherits the fault and has to remember" — and then ships a default that lets two of five callers not remember, silently. `D.qdec()` returns a **BigInt**, and `Number.isInteger(12n)` is `false`, so the public accessor is the one value the function cannot accept (Q2).

The same shape recurs everywhere units matter: `parseUnits(…, 5n)` in `forms.js`, `s.dec = 5n` for unknown pool assets, `fmtDero = fmtUnits(v, 5, …)`, `FirstDepositMin` in DERO units, `liq < 20`.

**Retires:** RT-03, D3-01, D3-02, D3-04, D3-05, D3-11, D3-13, Q1, Q2, RT-11, UX-F1, UX-F6, ARCH-F1, P7-01, P7-02, M1, PERF-F1 — **17 findings.**
**The fix is a deletion:** make `A.price(market, point)` read `market.dec` and `market.quoteDec`; delete the third argument, the fallback, and `D.qdec`. A call site then cannot express the wrong thing.

#### B. Fixes applied at the call sites the author could see
The four "recently fixed" items each left an adjacent hole, and three of them **created** current P1s:

| Recent fix | Hole it left |
|---|---|
| `A.price` takes quote decimals | `priceAt`, `candles`, `forms.js` preview, `sparkline` (DEFECT 1, 3) — and `change()` was *internally consistent* before it |
| Vault symbols reserved (`D.impersonation`) | The **name** on the same object; all of NFTs; the deep-link path (DEFECT 6) |
| `Paying(j)` + 2000-block veto delay | The **halt axis** — Released refuses while halted, Veto does not (DEFECT 5); and `markPaying` has no age check |
| Canon comment-stripping | Removed the only byte-comparison that could have guarded `app/templates == contracts` (C2) |

**This is the highest-leverage process finding in the audit.** The remedy is not more care: it is that each fix should have removed the *ability* to be wrong (make the argument required, make identity one function, make the halt matrix a table with a test) rather than correcting the instances found.

#### C. No claims register
Every categorical UI sentence should name the file:line of the mechanism that makes it true. None does, and `app/notice.js` compounds it by declaring itself authoritative over screens that are **more accurate than it is** — `wrap.js`, `token.js:225` and `screens.js:67` all name the federation honestly; `notice.js`'s fact table omits it entirely. `tools/uitest/ui.mjs:106` then asserts `/revoked/i` must be present, so an untrue claim is a regression requirement.

**Retires:** DEFECT 9 — 11 findings.
Note for the owner: **the brief is not a lie about a nonexistent product.** `swap.bas` and `board.bas` are written and tested and undeployed (`docs/ARCHITECTURE.md:23-24`). The brief describes the deferred half. `docs/TODO.md` §2.1 records that the federation-vs-atomic-swaps verdict "has never been answered properly" and its output `research/wrapper/MECHANISM_REVIEW.md` was never written. The contradiction is a missing decision, not a marketing error.

#### D. State that only a live quorum or key can leave
`contracts/vault.bas` `halted`; `w_<j>_st` after a Burn; `board.bas` `st=1`; `launch.bas` tmode-2 `tacc`; `factory.bas` `owner`; the TELA INDEX key; the treasury key that the founders promise needs online forever. The project's own rule (`docs/HANDOVER.md` §0.0) says any such state is a defect. `vault.bas`'s header claims liveness by construction and is contradicted by its own `Burn` guard.

**Retires:** RT-01, RT-02, BC-2, BC-3, BC-6, A8-04, A8-01(owner), P7-03(fees), A8-08 — **9 findings.**

#### E. Screen modules own logic that belongs to the data layer
`decorate()` is map-wide, so `token.js` reimplements it; `markets.js` owns the split-pair grouping, so Home, the tape, deep links and the trade sheet cannot see it; `home.js` and `markets.js` each recompute warnings.

**Retires:** DEFECT 6 — 6 findings, including A8-05, which is the owner's own stated requirement failing on the path where the trade is signed.

#### F. Validation and derivation as side effects of tools that need a chain
`genconfig` writes then validates. `app/templates` is written only by `simdeploy`, which needs a daemon and two wallets. The registry's authority is a mutable pointer with no legacy set. `simrpc`'s `log.Fatal` policy — correct for twelve throwaway sim scripts — is inherited by the one unsupervised production daemon.

**Retires:** C1, A8-09, C2, A8-13, A8-02, V1, T1, D3-15 — **8 findings.**

---

### 3. Dependency map

```
DEFECT-13  JS assertion layer (X1)
   └─> gates the regression test for: DEFECT 1, 2, 3, 7, D3-14, RT-12, D3-10

ROOT-A  A.price(market, point); delete qdec + D.qdec
   ├─> MUST precede stats.js:16/83/88/90  (else the obvious fix reintroduces Q2)
   ├─> MUST precede forms.js:86 preview
   └─> retires Q2 by construction

D3-15  F.pool sends `quote`  ──> pools can be created at all
   └─> + quote picker ──> wrapped-quote pools creatable from the app
        └─> enables RUNTIME confirmation of DEFECT 1 (or use simwrapmkt -quote)

BC-5  reference/ BuyTo+SellTo model
   ├─> gates proof that BC-1 is fixed
   └─> gates the "no external send before the last failure exit" lint
        └─> prevents pool.bas AddLiquidity becoming BC-1

ANY contracts/*.bas edit  ──> changes Canon hash
   └─> MUST update app/config.js templates[k].legacy FIRST
        └─> else every listed market de-verifies (feed.js templateHashes)
   [applies to BC-1, BC-6, RT-01/02, BC-2/3, factory dedup — nobody stated this]

C1  genconfig validates before RemoveAll
   └─> A8-09  testnet rebuild
        └─> "testnet live one week" mainnet gate
             └─> mainnet install

DEFECT-5  vault state machine (one coherent change)
   ├─> MUST land before any Seal (irreversible)
   ├─> unblocks A8-03 (parameter table — 4 of 11 params immutable after Seal)
   └─> unblocks the wrap.js copy fixes (F4/RT-01/RT-02), which are otherwise lies-in-reverse

ROOT-E  data.decorateOne(s) + data.markDuplicates(peers)
   └─> unblocks RT-07(name), UX-F3(badges), P7-09(NFT), A8-05(split pair everywhere), A1

feed.js  lastError / lastAttempt
   └─> unblocks UX-F11, D3-08(1), E1's per-screen recovery

A8-02  accepted-treasury set (current + legacy)
   └─> MUST land before any SetTreasury and before A8-04 key disposal

ROOT-C  claims register
   └─> unblocks A8-01, P7-03, A8-08, F4, RT-04, RT-05, RT-10, P7-05, P7-07, F19
```

Two non-obvious edges worth stating: **you cannot runtime-confirm the flagship money bug (DEFECT 1) from the app until D3-15 lands**, because `F.pool` cannot create a wrapped-quote pool and `F.launch`'s wrapped path is broken by DEFECT 2 — the only route today is `tools/simwrapmkt -quote`. And **`A.price` must be reshaped before the stats fix**, or the natural spelling `price(pt, s.dec, D.qdec(s))` silently reintroduces the bug (Q2).

---

### 4. Genuine auditor-vs-verifier disagreements

**Verifier right, auditor wrong outright — 3 cases:**

1. **X2** (ARCH). The auditor claims `tests/vault_paying_test.go` never calls `Paying` twice and never calls it from a non-signer. It does both, verbatim, with the comment "The mark cannot be refreshed, or one signer could hold a withdrawal open for ever". The proposed regression scenario is false — the test fails immediately. Verifier is right; only the `zz*` probe-file tidiness (P4) survives.
2. **A1** (ARCH). The auditor's scenario requires `s.wrappedBase` to be unset on the deep-link path; `token.js:23` reads `D.assets.get(scid)` and `:34` re-reads after a 15 s wait, so for a *listed* market the decorated object wins. The claimed false "Ticker shared" warning cannot occur (`tickerKey('D69C')` collides with nothing). But the verifier found a **worse** bug the auditor missed: for an unlisted wrapped-base pool, `s.dec` stays at `feed.js:166`'s hard-coded `5n` instead of the vault's 12 — every amount on that page is 10⁷ out.
3. **UX-F5**, bond-ratio arm. `Mint`'s `cap / capratio > bondtot` cannot fire post-Seal: `Slash` (vault.bas 533-534) explicitly walks `cap` down, and `Unbond` refuses active signers. Verifier right. **Important scope note:** this refutes "mints silently stop from under-bonding"; it does **not** refute BC-4 (collusion makes the quorum unslashable) or P7-10 (capratio is a nominal ratio that drifts with price). Those remain.

**Verifier right on severity, auditor right on substance — 9 cases:** RT-01 (P1→P2 — total quorum loss already strands non-burners; the *copy* contradiction is the real content), RT-05 (P1→P2 — three verification legs, and `factory.bas` always writes `tstr`), RT-10 (P1→P3 — `screens.js:248` discloses node trust at the moment of decision), BC-2 (P0→P1 — the shipped `vetoPass` only vetoes invalid destinations, so a human or divergent backends are required), Q2 (P1→P3 — latent, no live call site), A8-02 (P1→P2 — reversible by the same key), P7-01 (P0→P1 — creator's own fees, not custodied funds), UX-F3 (P1→P2 — both signable surfaces do carry the differentiated warning), P7-05 (P1→P2 — the `Tax` row sits one line above `Liquidity`).

**Verifier materially improves the finding — 5 cases:**
- **RT-08** is the most important. The auditor's fix (`ring: 16` on Burn) **would destroy user funds**: `vault.bas Burn` exits via `RETURN 1` with no `BurnTo`/refund wrapper, and above ring 2 there is no signer to credit (`vendor/derohe/dvm/sc.go:279-286`). The accurate finding is that the contract was written for a private burn and shipped without the wrapper that makes ring 16 safe.
- **BC-1** blast radius: after the drain the contract still reports `real = 1,780,000` while holding 247,194, so **every honest seller's transaction reverts** with a DVM underflow and the app keeps advertising the market as liquid (`stats.js:64` reads `real`).
- **D3-06**: the Home hero is correct and documented; the real cross-quote sum is `home.js:179` (Trade hub) — a defect no finding names.
- **D3-07**: the graduation move is `deff − vd0`, not `−vd0`, and Buyback is a genuine constant-product swap, not a "phantom buy".
- **RT-11**: two of four sub-items refuted (`home.js:24` does name `qsym`; `app.js:228 b.avg` is dead code) — and Z1 independently confirms `b.avg` is dead, which is why it is a trap rather than a wrong number.

**Where I side against the verifier — 2 cases:**
- **D3-11 formatter.** The verifier's counter-example (0.5 XMR ⇒ `liq = 1.0` ⇒ `"1.00"`) is arithmetically right, but the shipped simulator's BRW/XMR pool has liquidity `2.0619e-7 XMR` (`docs/TODO.md:119-121`) and the committed render `.sim/ui-dec/02-markets.png` shows `0.0000`. The auditor's claim is true of the deployment that exists; the verifier's is true of a hypothetical funded one. Both should be stated.
- **G1.** The verifier is right that the decoy protects the browse window and that ring-2 commit names the signer anyway — so the *privacy* framing collapses. But it downgrades to P3 on "code duplication", and the more interesting half survives unresolved: the displayed fee and the charged fee come from two independently-built estimates with no test comparing them. That is a correctness question, not a tidiness one, and it is on the NOT-VERIFIED list.

---

### 5. Ranked by severity against effort

**Tier 1 — ship-blocking, small effort.** Do these first.

| # | Finding | Effort |
|---|---|---|
| 1 | **BC-1** — hoist `min_out` above the send in `launch.bas buyto` | one line moved (+ hash/legacy sequencing) |
| 2 | **D3-15** — `F.pool` sends `quote` | one argument |
| 3 | **UX-F2** — `app.js:113` → `D.vaults().some(...)`; the ZEC vault is entirely unusable | one line |
| 4 | **DEFECT 1** — `stats.js` ×4 + `amm.js` signature | ~10 lines |
| 5 | **D3-03/D3-09** — side `{1,2}` in both validators | two lines |
| 6 | **P8** — `wrap.js:369` / `chat.js:176` `{disabled:true}`; harden `ui.js:84` | three lines |
| 7 | **UX-F1** — floor marker in `fmtUnits`; full precision for vault params | ~6 lines |
| 8 | **A8-09/C1** — move the hash check above `os.RemoveAll` | block move |

**Tier 2 — ship-blocking, medium effort.** DEFECT 5 (one coherent `vault.bas` state machine — halt lease, Released unblocked while halted, Burn permitted while halted, Burn expiry, per-signer Paying marks; **gas-measure before deploy**, the install budget has been hit twice). DEFECT 2 (`forms.js` parser + labels + `launch.bas` bound — needs both halves). ROOT-E (`decorateOne` extraction, retiring six findings). A8-03 (per-chain vault parameter table). ROOT-C (claims register + the ~11 copy corrections). DEFECT 13 (`tests/js/`, `node --test`, no new dependency). V1 (`Try*` twins for the six calls `vaultsigner` makes).

**Tier 3 — high value, low effort, not blocking.** PERF-F4 sparkline downsample (one `if`, ~25× on Home). PERF-F7 delete `holders` + four dead fields. PERF-F5 visibility guard on the block handler. `home.js:179` per-quote. Column symbols (D3-06/P7-08). UX-F10 raise `--fg3` to `#7a7e81` — one token fixes every AA failure. UX-F13 replace the gradient avatar with the network grammar `parts.css:102` already implements — a deletion that fixes the largest break in the visual thesis. `markets.js:32-35` stale comment. F23/Z1/A8-16 deletions.

**Tier 4 — high effort or requires a decision.** BC-4 (`n ≥ 2k`, slash→claim pool), P7-10 (capratio guidance), A8-11 (`tools/reservecheck`), A8-08/P7-03 (per-asset founders accounting), P7-06 (`hot` rework), P7-04 (registry dedup + fee curve), A8-04 (decommissioning), RT-06 (cache binding).

**Tier 5 — deliberately deprioritised.** D3-17 (P4, self-heals on reload), N1 (P4, no demonstrated failure), UX-F17/F20/F21 copy, A8-15/A8-10 counts, PERF-F9/F13.

---

### 6. Still requires runtime or on-chain verification

| Finding | Exact test |
|---|---|
| DEFECT 1 rendered | `go run ./simwrapmkt -quote <12-dec vault>`; open the token page; assert the four change pills are not `-100.00%` and the last candle close equals the header price |
| DEFECT 3 rendered | Same market: assert Volume/Liquidity/FDV cells are not `"0.0000"` and the wrap screen's minimum-deposit line is not `0 XMR` |
| **All PERF findings except F8** | They were never verified (§0). Re-run PERF's own `bench*.mjs` under review, then a Playwright trace for F9/F14 |
| P8 impact | Halt a `simvault`, press Burn, observe whether `DERO.GetGasEstimate` errors on `RETURN 1` or the user signs and pays |
| G1 fee divergence | `DERO.GetGasEstimate` on one pool entrypoint with two different signers; compare `gasstorage` |
| D3-07 | Drive a launch to graduation on the simulator; dump `p_<n>` across the boundary; compare `flow()` against the real trades |
| D3-08(1) | Force intermittent `DERO.GetSC` failures; assert the token page marks staleness rather than showing last-known prices under a live block clock |
| A1 (narrowed) | Deep-link `#token/<unlisted wrapped-base pool>` with an empty cache; assert `s.dec` is the vault's, not `5n` |
| A8-02 | Deploy two markets; `SetTreasury` to a new address; reload; confirm both go untrusted and vanish |
| UX-F5 | Fill a vault to `cap`; confirm the screen still reads "Open" and Register is offered |
| UX-F14 | Add 390 and 768 viewports to `ui.mjs`; assert `documentElement.scrollWidth <= width` |
| PERF-F6 | DVM test registering 5,000 social profiles; measure the `variables:true` response size |
| P7-04 | Register 5,000 duplicate SCIDs; time an app refresh |
| P7-10 | 2-of-4 vault; two signers slash the other two; assert the third slash reverts at `na == k` |
| RT-06 origin | Confirm two TELA sites can occupy port 8082 at different times and share `localStorage` |
| BC-2/BC-3 fixes | Re-run PROTO's two reproductions inverted after the state-machine change |
| Whole suite | `tests/run.sh` was **not run by any agent**. PROTO reports it green from a prior run; that is the only evidence. |

---

### 7. Production readiness

**Not ready. Do not deploy to mainnet, and do not Seal a vault.**

Six independent blockers, each sufficient on its own:

1. **BC-1** — one anonymous call drains a launch's entire raise (reproduced: 1,752,806 stolen from a 2,000,000 pot), and leaves the contract in a state where every honest seller's transaction reverts while the app still advertises the market as liquid.
2. **The vault cannot be safely sealed.** Its halt is a permanent freeze of the only exit (BC-3, reproduced at +2,000,000 blocks); Released is blocked while Veto is not, so a paid withdrawal can be refunded on chain (BC-2, reproduced); a burn with no quorum is unrecoverable (RT-01); the runbook never states the per-chain parameters and four of them are immutable after an irreversible Seal (A8-03); the one check a non-signer could run has no tool (A8-11). Seal is the point of no return.
3. **The flagship money path shows wrong numbers.** Every change window on a wrapped-quote market reads ≈ −100%; the chart disagrees with the price above it by 10⁷; every wrapped aggregate renders `"0.0000"`; the vault's minimum deposit renders `"0 XMR"` on the one screen that exists to prevent an unrecoverable loss.
4. **Creation is broken.** Pools cannot be created at all (D3-15). Wrapped-quote launches install a target 10⁷ too small and graduate on a dust buy, immutably (DEFECT 2). The ZEC vault — half the stated audience — refuses every write with "Refusing: contract is not a verified listing" (UX-F2).
5. **The app's designated authoritative page states falsehoods** (A8-01/P7-03) and the UI test enforces one of them; the founders promise excludes wrapped quotes and the asset leg entirely (A8-08); the wrap screen tells users doing nothing pays them (F4).
6. **The deployment pipeline cannot produce a valid bundle.** `genconfig -dist` exits 1; `dist/testnet/` is on disk complete-looking, manifest-less and internally contradictory; the "testnet live one week" mainnet gate is therefore being satisfied — if at all — by a build that cannot create a contract or list a current market.

Additionally: `deploy/federation.md` is named as a go/no-go evidence artefact and **does not exist**, so that gate cannot fail; `LICENSE` and `SECURITY.md` do not exist; three keys survive the runbook with no disposition step.

**What is genuinely good and should not be lost in the fix pass:** the trade sheet's cost disclosure (each cost in the asset actually charged, gas as its own row); the Portfolio's refusal to invent a cross-quote rate, with its reasoning written down; `min_out` enforced on chain in both contracts with a 10% client cap; the `endpointURL` allow-list plus per-session remote confirmation; `MAX_FEE`; the arithmetic in `pool.bas`/`launch.bas` (PROTO checked every product and difference for wrap and truncation and found nothing — that is unusual and worth saying); the fuzz-differential Go suite; and the fact that the shipped UI is **more honest than the brief** — `token.js:225`, `screens.js:67` and `wrap.js` all name the federation and its failure modes plainly.

---

### 8. Implementation buckets

#### SAFE TO IMPLEMENT NOW
App-only, no protocol semantics, blast radius bounded to display or a single guard.

- `app/stats.js:16, 83, 88, 90` — pass `s.quoteDec` **(only after `A.price` is reshaped; see below)**
- `app/amm.js:99` — coerce `Number(qdec)` so both spellings work (do **not** make it throw yet)
- `app/feed.js:290` and `:131` — side `{1,2}`; `:127` weaken the `pc/tc` invariant
- `app/app.js:113` — `D.vaults().some((v) => v.scid === opts.scid)`
- `app/token.js:114` — `fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s)`
- `app/home.js:179` — replace the cross-quote sum with the per-quote list `home.js:104` already builds
- `app/markets.js:166` and `app/token.js:69` — append `D.qsym(s)`
- `app/ui.js:84` attrs coercion; `wrap.js:369`, `chat.js:176` → `{ disabled: true }`
- `app/amm.js fmtUnits` — floor marker instead of `"0"`; `wrap.js:114` full precision for protocol constants
- `app/feed.js refresh()` — dedup `ids` through a `Set`
- `app/ui.js:119` sparkline — downsample above 160 points
- `app/stats.js` — delete `holders`, `flow24`, `pub24`, `pub1`, `blocks24`, `c7d`
- `app/app.js:74` — `if (!document.hidden)` on the block handler
- `app/ui.js:141` — `const r = fn(...); if (r && r.then) r.catch(...)`; add `unhandledrejection`
- `app/feed.js` — `lastError` / `lastAttempt`; three-way branch on every screen; Retry actions
- Deletions: `app.js:228 b.avg`, `forms.js:17 install:true`, `live.js hue` + `mood()`, `styles.css:25-26` legacy tokens, the `indexer` toggle + config + its `http://127.0.0.1:*` CSP grant, `rpc.js:126 cache`, `home.js:142 hold.change24`, `tests/zz*_test.go`
- `app/styles.css:9` — `--fg3: #7a7e81` (5.13:1); replace `.step.todo{opacity:.55}` with an explicit colour
- `app/ui.js:66` — replace the gradient avatar with `.net` from `parts.css:102`
- `app/markets.js:32-35` — delete the stale KNOWN GAP comment
- `tools/simsmoke/main.go:61` — add the `quote` argument
- **Copy that only removes or softens a claim:** `wrap.js:337-338`, `:386`, `:275`, `:252`, `:357`; `notice.js:15-17, 38-43`; `nfts.js:94`; `token.js` "locked forever"; `forms.js:95`; `home.js:23`; `forms.js:111, 113`; `screens.js` "every listing is verified"
- `tools/genconfig` — move the template-hash check above `os.RemoveAll`, `defer os.RemoveAll` on failure
- `tools/uitest/ui.mjs` — add 390 and 768 viewports with the existing overflow assertion

#### IMPLEMENT WITH REVIEW
Correct direction, but the change alters semantics, ordering, or what gets listed.

- **`A.price(market, point)` reshape + delete `D.qdec`.** Do this *first* in the units work; it is what makes the `stats.js` fix safe (Q2).
- **`app/forms.js:138`** — add `["quote","H",quote.scid]` **and** a quote picker. Review which quotes to offer and confirm the argument list against the fetched template's `InitializePrivate` signature.
- **`data.decorateOne(s)` + `data.markDuplicates(peers)`** extraction, then call from `decorate()`, `token.js:35` and the split-pair path. Touches identity and safety logic on every surface; needs the UI test extended first.
- **Split-pair on the data layer** — `s.splitWith`; suppress non-deepest members from `D.trending()` and the tape; warn box in the trade sheet.
- **`amm.js launchBuy` overshoot mirror** (D3-14/RT-12) — must reproduce `launch.bas` lines 50-59 exactly, including the fee/tax recomputation on `used`. Write the differential test first.
- **Ledger from balance deltas** (D3-10) — mark quote-derived records as estimated.
- **localStorage budget + eviction + `QuotaExceededError` handling** (PERF-F2); **cache binding** (RT-06).
- **`feed.js` treasury fail-closed + a third `verified()` branch** (RT-05) — changes what is listed; pair with the accepted-treasury set.
- **A8-02** — accepted-treasury set with `legacy`, mirroring the template-hash shape. **Must land before any `SetTreasury`.**
- **`hot` rework** (P7-06) — distinct addresses per block, or weight by `vol/liq`; exclude below a liquidity floor.
- **`vaultsigner` `Try*` API** (V1) and **`markPaying` age check** (verifier-note-2) — the daemon currently re-pays abroad under a possibly-expired mark.
- **Founders per-asset accounting or per-asset claim** (A8-08/P7-03).
- **`tests/js/`** — plain `node --test`, vectors generated from `TestPoolFuzz` via `t.Logf`. No new dependency.

#### DO NOT IMPLEMENT BLINDLY

1. **`ring: 16` on the vault Burn (RT-08).** As written this **loses the user's units** on any precondition failure — `Burn` exits `RETURN 1` and above ring 2 there is no signer to refund. Requires a `BurnTo` wrapper in `vault.bas` first, and `D.vaultWithdrawals()` re-keyed off `w_<j>_by`.
2. **Any `vault.bas` change.** Seal is irreversible and there is no operator afterwards. The halt/expiry/Paying fixes interact (removing `halted` from Released changes the safety model; per-signer marks change the veto arithmetic) and must be designed and tested as **one** state machine, with a documented halt matrix (entrypoint × halted) asserted by a test — the current rule is implicit in eleven guard lines and two of them disagree. Measure install gas: the 20,000-unit budget has been hit twice.
3. **The BC-1 hoist — and every other contract edit — changes the Canon hash.** `app/config.js templates[k].legacy` must carry the old hash **before** the new contract ships, or every already-listed market de-verifies and vanishes (`feed.js templateHashes`). No finding stated this; it is the single most likely way a correct fix causes an outage.
4. **`forms.js:63` parser alone.** Parsing 10 XMR correctly gives 1e13, above `launch.bas`'s 1e12 ceiling — the install reverts. The contract bound must change with it, or the wizard must refuse wrapped quotes in the interim.
5. **`Seal` enforcing `n ≥ 2k` (BC-4).** Forbids 2-of-3, which `contracts/README.md` explicitly blesses. Economic/deployment decision. The **app-side** disclosure (`na < 2k` ⇒ "this federation cannot slash its own quorum") uses state `wrap.js` already fetches and is safe now.
6. **Routing slashed bonds to a claim pool (BC-4/P7-10).** Changes who is made whole. Decide, then implement.
7. **Disposing of the factory owner / INDEX / treasury keys (A8-04).** Irreversible, and `deploy/README.md`'s Rollback section *depends* on the INDEX key while `HANDOVER` §0.0 says it may be discarded. Resolve the contradiction in writing first.
8. **Making `A.price`'s quote argument mandatory-throw.** In a CSP'd on-chain app with no error surface, a throw is an invisible dead screen until E1 lands. Sequence it after.
9. **`registry_()` throwing on an unparsable treasury (RT-05's stricter form).** Fail-closed here means the whole market list disappears on one malformed key. Prefer an `unverified` flag plus a third badge branch.

---

### 9. What the eight collectively missed

1. **Immutability sequencing.** Six of the P0/P1s live in immutable contracts. Nobody stated the consequence: **every contract fix must land before any mainnet install**, and each one requires a `legacy` hash entry shipped first or it de-verifies the existing market set. This is the constraint that determines the whole fix order and no agent drew it.
2. **Nobody joined C1+A8-09 to the mainnet gate.** The broken testnet bundle is not a build annoyance — it means the "testnet live and exercised for one week" go/no-go row is being satisfied by an app that cannot create a contract (`forms.js:15` throws `template hash mismatch` on all four) and lists nothing current (`feed.js` drops every market installed since 2026-09-06). The gate is passing on a corpse.
3. **Nobody made the aggregate case about the vault.** BC-3 + BC-4 + P7-10 + A8-03 + A8-11 together say: the custodian is unslashable in the config the docs bless, its halt is a permanent freeze of the only exit, four of its parameters are immutable and undocumented, and the one control a user could exercise has no tool. Each agent found one leg. The conclusion — **the vault should not ship in its current shape** — is nobody's finding.
4. **Nobody counted the missing test layer.** X1, PERF-F10, BC-5, C2 and A8-13 are five findings describing one absence: there is no assertion between the Go DVM suite and the browser. That absence is the proximate cause of at least five confirmed money bugs. It should be a single P1, not five scattered P2/P3s.
5. **Nobody noticed that the recent fixes caused the current P1s.** D3-01 says it about one; the pattern holds for three of the four (§2.B). This is the most actionable process finding available and it is nobody's headline.
6. **The `hot`-score/split-pair interaction.** P7-06 shows `hot` is dominated by an uncapped per-block term one wallet can drive; A8-05 shows the split-pair defence is absent from `D.trending()`. Together: **an attacker can promote the thin half of a split pair to Home's rank 1 for ~200 gas fees**, which is precisely the drain the owner asked to be prevented, reachable by the app's own default route. Neither agent saw the other's half.
7. **Two agents disagreed about who is honest, and nobody arbitrated.** SEC and PROD read the app's trust copy as overstated; PROTO, REQ and the verifiers read it as scrupulous. Both are right about different files: `wrap.js`, `token.js:225` and `screens.js:67` are honest; `notice.js` — the page claiming final authority — omits the federation entirely and asserts three falsehoods. The reconciliation is that **the app's least accurate page is the one that outranks the others**, which is worse than a uniformly overstated app.
8. **`app/notice.js` documents no trusted party** while the standing rule is to document every one. The verifier caught this; no agent did.

---

## 8. Arithmetic audit

44 findings touch units, precision or rounding. The picture is unusual: **the contract arithmetic is clean and
the JavaScript arithmetic is not.** PROTO checked every product, difference and division in `pool.bas` and
`launch.bas` for wrap and truncation and found nothing — every `muldiv` is 128-bit, every subtraction is
guarded, and truncation consistently favours the pool rather than the caller. There is no floating point
anywhere in the money path on chain, and `tests/run.sh` fails the build if `float64` appears in
`reference/`, `tests/harness/`, `tools/treasurybot/` or `tools/vaultsigner/`.

Every arithmetic defect found is in the display and quoting layer, and every one has the same shape: **a
DERO-shaped constant standing in for a quantity whose units depend on the market.**

- `A.price(pt, dec, qdec)` defaulted `qdec` to 5 (fixed — now `A.priceOf(market, pt)`).
- `parseUnits(f.grad.value, 5n)` in the launch wizard (fixed).
- `fmtDero = fmtUnits(v, 5, …)`, `fmtDeroShort`'s 4-decimal floor, `s.dec = 5n` for an unknown pool asset,
  `FirstDepositMin` and `MinLiq` in DERO units, the thin-market threshold `liq < 20` (open).
- `D.qdec()` returns a **BigInt** while `s.quoteDec` is a **Number**, and `Number.isInteger(12n)` is `false` —
  so the public accessor was the one value the old `A.price` could not accept (`ARCH·Q2`).

**Trace of one trade, verified end to end.** Wallet balance (atomic, BigInt) → `A.poolBuy`/`launchBuy` quote
(atomic, BigInt, mirrors the contract) → fee and tax (`bps`/`bpsUp`, BigInt) → `minOut` with slippage (BigInt,
capped at 10%) → transaction (atomic) → settlement (contract recomputes everything; `min_out` enforced) →
updated balance (atomic) → portfolio value (**Number**, per quote, never summed across quotes). The only lossy
step is the last, and it is display-only and correctly per-quote. The conversions are sound; what was wrong was
the *scale constant* applied at the display boundary.

<details>
<summary>All 44 findings in this slice</summary>

| Id | Sev | Finding |
|---|---|---|
| `ARCH·F1` | P1 | The launch form parses the graduation target at DERO's 5 decimals and labels every preview figure "DERO" whichever quote is picked, so a wXMR launch graduates at 10⁻⁷ of the intended target — immutably |
| `ARCH·Q1` | P1 | The quote-decimals fix reached one of four price call sites; priceAt and candles still assume DERO, so a wrapped-quote market shows −100% change and a chart 10⁷× off |
| `ARCH·V1` | P1 | vaultsigner, the one production daemon, is built on simrpc, whose entire error policy is log.Fatal — one transient node RPC error retires a federation signer permanently, and there is no operator |
| `DATA·D3-01` | P1 | Every price-change window on a wrapped-quote market reads ≈ −100.00% — a regression introduced by the decimals fix |
| `DATA·D3-02` | P1 | The chart and the price above it disagree by 10^7 on the same screen for a wrapped-quote market |
| `DATA·D3-04` | P1 | The launch wizard parses the graduation target in DERO's decimals whatever quote the creator picked, so a '100 XMR' launch installs a target of 0.00001 XMR |
| `PERF·F1` | P1 | Quote-decimals fix is incomplete: every change window reads -100% and every candle is 10^7 off on a wrapped-quote market |
| `PERF·F2` | P1 | The localStorage market cache has no size budget: it silently exceeds quota at ~40 markets, and every page load then pays a ~3,000-round-trip cold start |
| `PROD·P7-01` | P1 | Every wrapped-quote launch is created with a graduation target 10^7 too small: the wizard parses it at DERO's 5 decimals, and launch.bas's own bound is DERO-shaped too |
| `PROD·P7-02` | P1 | On any wrapped-quote market every change percentage reads about -100% and the chart's prices are 10^7 times the header price, because priceAt() and candles() drop the quote-decimals argument that the recent A.price fix added |
| `PROTO·BC-2` | P1 | Vault: Veto works while halted but Released does not, so the Paying interlock lapses and a withdrawal already paid abroad can be refunded on chain |
| `PROTO·BC-3` | P1 | A vault halt has no expiry and blocks Burn, so k signers who never vote Halt(0) freeze every holder out of the only exit permanently |
| `REQ·A8-03` | P1 | The mainnet runbook cannot produce a working vault: it never says how to choose the per-chain parameters, and none of them can be changed after Seal |
| `SEC·RT-03` | P1 | Every price-change window reads about -100% on any market quoted in a wrapped asset; the chart is out of scale with the header price by 10^(quoteDec-5) |
| `UX·F1` | P1 | The wrap screen prints the vault's minimum deposit, mint cap and headroom as "0" — the one number whose whole purpose is to prevent an unrecoverable loss |
| `UX·F6` | P1 | The launch wizard parses the graduation target at DERO's 5 decimals whatever quote is chosen, and labels every preview figure DERO |
| `DATA·D3-05` | P2 | Two quote-denominated figures were missed by the labelling pass: Profile's 'Published vol' adds XMR to DERO, and the Claim-creator-tax button prints a quote amount as DERO |
| `DATA·D3-10` | P2 | The trade ledger records the quote, not the fill, and records the amount sent rather than the amount the contract consumed |
| `DATA·D3-11` | P2 | The formatter and the risk thresholds are DERO-magnitude constants applied unchanged to XMR and ZEC amounts |
| `PERF·F5` | P2 | A backgrounded tab keeps doing full per-block refreshes; the hidden-tab back-off the author wrote is bypassed |
| `PROD·P7-05` | P2 | "Locked forever" liquidity is drainable by the creator: in tmode 1 every sell takes up to 10% of the AMM's quote reserve as creator tax, and the token page states the lock without qualification |
| `PROD·P7-08` | P2 | The Markets table ranks and displays volume, liquidity and FDV across different quote assets as bare unlabelled numbers, and the Trade hub sums them and labels the total DERO |
| `SEC·RT-01` | P2 | A vault Burn with no quorum is unrecoverable — there is no expiry path, and both the contract header and the Wrap screen claim the opposite |
| `SEC·RT-09` | P2 | The Paying/Veto guard closes the common double-pay case but leaves a 2000-block window, and the mark cannot be renewed even while the signer is still retrying the payment |
| `UX·F10` | P2 | --fg3 is 3.68:1 on the ground — every label, eyebrow, note, stat caption, inactive tab and empty-state body in the app fails WCAG AA |
| `UX·F14` | P2 | 768px is untested and the app is a 430px strip there, while two media queries switch to wide layouts inside that narrow column |
| `UX·F16` | P2 | The chain name is hard-truncated at 40 characters and lands mid-word in eight places on the wrap screen |
| `UX·F4` | P2 | The wrap screen states that doing nothing pays a withdrawal; the contract has no such path |
| `ARCH·D1` | P3 | networks.json's vaultAsset block is dead data that contradicts the live record for the same contract — 5 decimals against 12 — and netcfg.Save silently deletes any field the struct does not know |
| `ARCH·G1` | P3 | The gas-estimate RPC is built twice and the two copies have already drifted on the field that matters: sheets.js estimates with a decoy signer for privacy, app.tx then estimates the same call with the real address |
| `ARCH·M1` | P3 | Protocol constants are unnamed literals in four to twelve places each, and one of them (the thin-market threshold) is compared against a figure whose units now vary per market |
| `ARCH·Q2` | P3 | "Quote decimals" exists in two incompatible types (BigInt from D.qdec, Number on s.quoteDec) and A.price silently defaults to 5 when handed the BigInt one |
| `DATA·D3-06` | P3 | The Markets table ranks and prints Vol / Liquidity / FDV across quotes on raw magnitude, and on desktop those three columns carry no currency at all |
| `DATA·D3-08` | P3 | A market that fails to load stays on screen indefinitely with no staleness marker, and switching nodes invalidates nothing |
| `DATA·D3-13` | P3 | An unknown quote asset and an unknown base asset both silently fall back to DERO's five decimals |
| `PERF·F8` | P3 | Two avoidable RPC round trips per contract on cold start, and a probe concurrency of 4 that is the wrong default |
| `PERF·F9` | P3 | flash() forces a synchronous layout per changed number, so a Markets redraw can thrash layout ~120 times in one tick |
| `PROD·P7-10` | P3 | The bond can be worth arbitrarily less than what a quorum could steal, and even when slashed it pays the treasury rather than the victims — the wrap screen's trust panel says 'slashable' and stops there |
| `PROD·P7-11` | P3 | Founders Pass rewards are pro-rata to whoever is registered at the instant of a deposit, with no lock and no time weighting, so a just-in-time registrant captures a share a long-term holder earned |
| `SEC·RT-11` | P3 | Three leftovers from the quote-decimals fix: a cross-quote volume sum on the profile screen, a DERO-scaled ClaimTax button, and a DERO-worded tax risk line |
| `UX·F20` | P3 | Fee and unit disclosure gaps: the token page's Market read-outs carry no quote symbol, the withdrawal fee names no recipient, and the contract id the copy tells you to check is truncated |
| `UX·F9` | P3 | Only the newest deposit index is readable, and the screen actively encourages creating one — hiding the address a deposit may already be in flight to |
| `ARCH·X2` | P4 | Two test files marked temporary ship and run as if permanent, and the Paying non-refresh property — the one the contract comment calls load-bearing — is stated and untested |
| `ARCH·Z1` | P4 | Dead exports and one dead field that is also wrong: app.pnl computes a per-asset average cost at hard-coded DERO decimals that nobody reads |

</details>

---

## 9. State-machine audit

48 findings touch state, lifecycle or terminal states. They concentrate in one place.

**`contracts/vault.bas` — the withdrawal and halt state machine.** This is the audit's second-worst area after
BC-1, and unlike BC-1 it is not yet fixed.

| State | Reached by | Exit | Time-bounded? |
|---|---|---|---|
| deposit index registered | `Register` | `Attest` ×k | no — but harmless |
| minted | `Mint` ×k | — | — |
| **burned, `w_j_st = 0`** | `Burn` | `Released` ×k **or** `Veto` ×k | **no. There is no expiry and no reclaim.** |
| marked paying, `w_j_p` set | `Paying` | `Released` ×k | mark lapses at 2000 blocks and **cannot be refreshed** |
| released, `st = 1` | `Released` ×k | terminal | — |
| vetoed, `st = 2` | `Veto` ×k | terminal | — |
| **halted** | `Halt(1)` ×k | `Halt(0)` ×k | **no lease. Permanent unless k signers act.** |

Three reproduced consequences:

1. **`PROTO·BC-3`** — `Halt` blocks `Burn`, so a halt that is never lifted freezes every holder out of the only
   exit. Reproduced at **+2,000,000 blocks** (about a year): `Burn` still returns "Discarded knowingly".
2. **`PROTO·BC-2`** — `Released` refuses while halted; `Veto` does not. So a withdrawal already paid abroad can
   have its units returned on chain during a halt, once the 2000-block `Paying` mark lapses. Reproduced.
   This is the halt axis of the interlock I added earlier the same day — I closed the veto race and left this one.
3. **`SEC·RT-01`** — a `Burn` that never reaches quorum is unrecoverable. The units are destroyed at burn time
   and only k signers can resolve the state. Doing nothing pays nobody.

All three contradict the file's own header: *"LIVENESS BY CONSTRUCTION: after Seal no privileged party exists or
is needed. Every limit empties by itself or expires into a payment; doing nothing pays the user."* That sentence
is true of `wdelay` and `bigdelay`, which do expire. It was then generalised to the whole withdrawal, which it
does not cover. `app/wrap.js` repeats the generalisation to users.

The correct fix is **one coherent state-machine change**, not five patches: a halt lease that decays unless
re-voted; `Released` permitted while halted; `Burn` permitted while halted; an expiry on an unresolved burn; and
a documented halt matrix (entrypoint × halted) asserted by a test — the current rule is implicit in eleven guard
lines, and two of them disagree. **Measure install gas before deploying: the 20,000-unit budget has been hit
twice.**

<details>
<summary>All 48 findings in this slice</summary>

| Id | Sev | Finding |
|---|---|---|
| `ARCH·V1` | P1 | vaultsigner, the one production daemon, is built on simrpc, whose entire error policy is log.Fatal — one transient node RPC error retires a federation signer permanently, and there is no operator |
| `DATA·D3-04` | P1 | The launch wizard parses the graduation target in DERO's decimals whatever quote the creator picked, so a '100 XMR' launch installs a target of 0.00001 XMR |
| `PROD·P7-01` | P1 | Every wrapped-quote launch is created with a graduation target 10^7 too small: the wizard parses it at DERO's 5 decimals, and launch.bas's own bound is DERO-shaped too |
| `PROD·P7-03` | P1 | The in-app plaque promises "All platform fees are distributed amongst the NFT holders" and "all rights revoked"; the shipped defaults distribute 50%, wrapped-asset fees can reach holders through no path at all, and every payout requires a live treasury key |
| `PROTO·BC-2` | P1 | Vault: Veto works while halted but Released does not, so the Paying interlock lapses and a withdrawal already paid abroad can be refunded on chain |
| `PROTO·BC-3` | P1 | A vault halt has no expiry and blocks Burn, so k signers who never vote Halt(0) freeze every holder out of the only exit permanently |
| `REQ·A8-01` | P1 | The app's designated final word makes three claims the shipped deployment cannot deliver — and the UI test locks one of them in |
| `REQ·A8-04` | P1 | Nothing in the deployment runbook disposes of the three keys that outlive the operator, and the founders promise requires a human to run a bot forever |
| `SEC·RT-02` | P1 | Halt freezes burns AND releases with no timeout, and the Wrap screen tells the user halted withdrawals stay payable |
| `SEC·RT-04` | P1 | The vault publicly links a DERO address to its foreign deposit address and to every one of its deposit indexes, while the app claims fresh indexes are unlinked |
| `DATA·D3-05` | P2 | Two quote-denominated figures were missed by the labelling pass: Profile's 'Published vol' adds XMR to DERO, and the Claim-creator-tax button prints a quote amount as DERO |
| `DATA·D3-10` | P2 | The trade ledger records the quote, not the fill, and records the amount sent rather than the amount the contract consumed |
| `PERF·F14` | P2 | The Wrap screen redraws every block from a vault snapshot it read once and never refreshes |
| `PROD·P7-05` | P2 | "Locked forever" liquidity is drainable by the creator: in tmode 1 every sell takes up to 10% of the AMM's quote reserve as creator tax, and the token page states the lock without qualification |
| `PROD·P7-08` | P2 | The Markets table ranks and displays volume, liquidity and FDV across different quote assets as bare unlabelled numbers, and the Trade hub sums them and labels the total DERO |
| `PROTO·BC-6` | P2 | launch.bas tmode 2: creator tax accrued before graduation is unreachable for ever if the token never graduates |
| `REQ·A8-08` | P2 | The Founders Pass promise is rendered as unconditional in the UI while the code deliberately excludes wrapped-quote markets and the asset leg |
| `REQ·A8-11` | P2 | The app tells users anyone can verify the reserve; no tool, procedure or backlog item exists to do it |
| `SEC·RT-01` | P2 | A vault Burn with no quorum is unrecoverable — there is no expiry path, and both the contract header and the Wrap screen claim the opposite |
| `SEC·RT-05` | P2 | The registry treasury check silently disables itself when the factory's treasury is unreadable, while the Verified badge still claims the treasury was checked |
| `SEC·RT-08` | P2 | The app sends vault Burn at ring size 2 although the contract never reads SIGNER(), and the screen states the ring-2 exposure as a contract requirement |
| `SEC·RT-09` | P2 | The Paying/Veto guard closes the common double-pay case but leaves a 2000-block window, and the mark cannot be renewed even while the signer is still retrying the payment |
| `UX·F10` | P2 | --fg3 is 3.68:1 on the ground — every label, eyebrow, note, stat caption, inactive tab and empty-state body in the app fails WCAG AA |
| `UX·F11` | P2 | An unreachable node produces skeletons that animate forever, and no screen in the app has a retry |
| `UX·F4` | P2 | The wrap screen states that doing nothing pays a withdrawal; the contract has no such path |
| `UX·F5` | P2 | A vault that cannot mint still reports "Open" and invites a deposit — headroom, bond ratio and the mint window are read and never used |
| `UX·F7` | P2 | There is no pending-transaction state anywhere: the sheet closes and a transaction that can take four and a half minutes is reported only by toasts that expire |
| `UX·P8` | P2 | Two buttons pass a boolean where the helper expects an attributes object, so they are never disabled — including Burn on a halted vault |
| `ARCH·C2` | P3 | Nothing asserts app/templates == Canon(contracts); the tests test one file and the app ships the other, and the only bridge needs a live simulator and a wallet |
| `ARCH·D1` | P3 | networks.json's vaultAsset block is dead data that contradicts the live record for the same contract — 5 decimals against 12 — and netcfg.Save silently deletes any field the struct does not know |
| `ARCH·X1` | P3 | amm.js is a third implementation of the AMM maths with no test of any kind; the contract and the Go reference are fuzz-differentialed against each other, and the JS that picks min_out is not |
| `DATA·D3-12` | P3 | The Portfolio omits every token whose only market is a pool contract, and prints an unvaluable holding as zero |
| `DATA·D3-14` | P3 | The trade sheet mis-states fee, tax and refund on the buy that completes a bonding curve |
| `PERF·F8` | P3 | Two avoidable RPC round trips per contract on cold start, and a probe concurrency of 4 that is the wrong default |
| `PROD·P7-07` | P3 | "Creator allocation: none" is only the install-time grant; the curve has no per-address cap, no cooldown and no launch delay, and DERO's encrypted balances make the resulting concentration unobservable to anyone |
| `PROD·P7-10` | P3 | The bond can be worth arbitrarily less than what a quorum could steal, and even when slashed it pays the treasury rather than the victims — the wrap screen's trust panel says 'slashable' and stops there |
| `REQ·A8-12` | P3 | chat.bas ships as a top-level tab with a deployed contract and has no entry in the contract reference, the README or the architecture |
| `SEC·RT-10` | P3 | 'Verified' is a claim about what one untrusted node reported, and the intro and Settings copy state it as absolute |
| `SEC·RT-12` | P3 | The trade sheet's refund figure for a graduating buy does not match what launch.bas refunds |
| `UX·F17` | P3 | Meaningless badges, decorative statistics and a launch-sheet promise the app's own risk text contradicts |
| `UX·F18` | P3 | The page the app calls the one that outranks every other piece of copy is unreachable on mobile, and so is the build stamp |
| `UX·F19` | P3 | Vetoed withdrawals are described as a benign delivery failure, and the veto/Paying window is invisible |
| `UX·F21` | P3 | An irreversible local wipe has no confirmation, the shoulder-surfing control is two hops from the screen it protects, and the reduce-motion setting does not do what it says |
| `UX·F22` | P3 | Every on-chain document is permanently described as "Veil", and the install is unfixable after deployment |
| `UX·F9` | P3 | Only the newest deposit index is readable, and the screen actively encourages creating one — hiding the address a deposit may already be in flight to |
| `ARCH·N1` | P4 | window.Veil hides three real couplings: feed.js calls a method defined in another file, V.markets exists only while its screen is mounted, and app.js writes into the data layer's fields |
| `ARCH·X2` | P4 | Two test files marked temporary ship and run as if permanent, and the Paying non-refresh property — the one the contract comment calls load-bearing — is stated and untested |
| `REQ·A8-16` | P4 | DESIGN_SYSTEM.md says six tokens are deleted; they are still defined as aliases, and one dead computation remains in live.js |

</details>

---

## 10. Privacy audit

DMZ is explicitly privacy-oriented, so an overstated privacy claim is worse here than a missing feature.

**What holds.** No key material can reach the page — XSWD is the only signing path. There is no analytics, no
telemetry, no third-party request, and the CSP forbids one. `V.jsonBig` avoids a `Number` round-trip on amounts.
The `...To` entrypoints correctly avoid `SIGNER()` so ring 16 works for buys and sells.

**What does not.**

- **`SEC·RT-04` — the vault publishes the link the app says does not exist.** `vault.bas Register` stores
  `ua_<j>` (index → the registrant's raw DERO address) and `ul_<addr>`, and `Attest` stores the foreign deposit
  address for that index. So the chain carries a public map from a DERO address to its Monero deposit address
  and to every index it owns, while the app tells users fresh indexes are unlinked.
- **`SEC·RT-08` — the vault `Burn` cannot safely use ring 16.** It exits `RETURN 1` with no `BurnTo` wrapper, so
  above ring 2 there is no signer to refund. **The auditor's proposed fix — set `ring: 16` on `Burn` — would
  destroy user funds.** The accurate finding is that the contract was written for a private burn and shipped
  without the wrapper that makes ring 16 safe. This is the clearest case in the audit of a fix being more
  dangerous than the defect, and the verifier caught it.
- Withdrawal destinations are stored in plaintext on chain (`w_<j>_to`), which is inherent to the design and
  should be disclosed rather than fixed.

<details>
<summary>All 29 findings in this slice</summary>

| Id | Sev | Finding |
|---|---|---|
| `PROTO·BC-1` | P0 | launch.bas pays the overshoot refund twice: SwapDeroForAssetTo drains a launch's entire raise in one anonymous call |
| `ARCH·V1` | P1 | vaultsigner, the one production daemon, is built on simrpc, whose entire error policy is log.Fatal — one transient node RPC error retires a federation signer permanently, and there is no operator |
| `DATA·D3-04` | P1 | The launch wizard parses the graduation target in DERO's decimals whatever quote the creator picked, so a '100 XMR' launch installs a target of 0.00001 XMR |
| `PROD·P7-03` | P1 | The in-app plaque promises "All platform fees are distributed amongst the NFT holders" and "all rights revoked"; the shipped defaults distribute 50%, wrapped-asset fees can reach holders through no path at all, and every payout requires a live treasury key |
| `PROD·P7-04` | P1 | The factory registry is permissionless, deduplication-free, unprunable and owned by nobody after deployment: ~0.1 DERO per entry buys a permanent per-block RPC amplification on every client |
| `REQ·A8-05` | P1 | The owner's 'one market drained while another is left behind' requirement is defended on the Markets screen only — not where the trade is signed |
| `SEC·RT-02` | P1 | Halt freezes burns AND releases with no timeout, and the Wrap screen tells the user halted withdrawals stay payable |
| `SEC·RT-04` | P1 | The vault publicly links a DERO address to its foreign deposit address and to every one of its deposit indexes, while the app claims fresh indexes are unlinked |
| `DATA·D3-15` | P2 | Create a pool cannot succeed: the install omits the `quote` argument pool.bas v2 requires |
| `PERF·F12` | P2 | markets.js documents a 'KNOWN GAP' in pair grouping that no longer exists, and duplicates feed.js's impersonation rule in token.js |
| `PERF·F13` | P2 | The registry's full id list is refetched every block although its counts say it did not change, and markets.js retains a DOM row for every market ever listed |
| `PROD·P7-08` | P2 | The Markets table ranks and displays volume, liquidity and FDV across different quote assets as bare unlabelled numbers, and the Trade hub sums them and labels the total DERO |
| `PROD·P7-09` | P2 | The impersonation guard covers tickers only: display names, NFT collection names and the Founders Pass itself can all be copied with no warning anywhere |
| `PROTO·BC-5` | P2 | The executable model has no equivalent of the ...To wrapper, so the differential and fuzz suites structurally cannot see BC-1 |
| `REQ·A8-06` | P2 | The current working list is invisible: every routing document sends the next agent to a backlog that predates all recent work |
| `SEC·RT-08` | P2 | The app sends vault Burn at ring size 2 although the contract never reads SIGNER(), and the screen states the ring-2 exposure as a contract requirement |
| `UX·F15` | P2 | Modal sheet has no focus trap or focus return, nav tabs declare role=tab without aria-selected, the chart is pointer-only, and the tape can only be paused by hovering |
| `UX·F5` | P2 | A vault that cannot mint still reports "Open" and invites a deposit — headroom, bond ratio and the mint window are read and never used |
| `ARCH·A1` | P3 | dupTicker and pool naming are implemented twice; the copy in token.js runs on the deep-link route and has already drifted — a shared link to the genuine wrapped market shows a hex ticker and a false "Ticker shared" warning |
| `ARCH·C2` | P3 | Nothing asserts app/templates == Canon(contracts); the tests test one file and the app ships the other, and the only bridge needs a live simulator and a wallet |
| `ARCH·G1` | P3 | The gas-estimate RPC is built twice and the two copies have already drifted on the field that matters: sheets.js estimates with a decoy signer for privacy, app.tx then estimates the same call with the real address |
| `ARCH·M1` | P3 | Protocol constants are unnamed literals in four to twelve places each, and one of them (the thin-market threshold) is compared against a figure whose units now vary per market |
| `PROD·P7-10` | P3 | The bond can be worth arbitrarily less than what a quorum could steal, and even when slashed it pays the treasury rather than the victims — the wrap screen's trust panel says 'slashable' and stops there |
| `PROD·P7-11` | P3 | Founders Pass rewards are pro-rata to whoever is registered at the instant of a deposit, with no lock and no time weighting, so a just-in-time registrant captures a share a long-term holder earned |
| `SEC·RT-12` | P3 | The trade sheet's refund figure for a graduating buy does not match what launch.bas refunds |
| `UX·F18` | P3 | The page the app calls the one that outranks every other piece of copy is unreachable on mobile, and so is the build stamp |
| `UX·F21` | P3 | An irreversible local wipe has no confirmation, the shoulder-surfing control is two hops from the screen it protects, and the reduce-motion setting does not do what it says |
| `UX·F22` | P3 | Every on-chain document is permanently described as "Veil", and the install is unfixable after deployment |
| `ARCH·X2` | P4 | Two test files marked temporary ship and run as if permanent, and the Paying non-refresh property — the one the contract comment calls load-bearing — is stated and untested |

</details>

---

## 11. Testing gap analysis

Five findings (`ARCH·X1`, `PERF·F10`, `PROTO·BC-5`, `ARCH·C2`, `REQ·A8-13`) describe **one absence**: there is
no assertion layer between the Go DVM suite and the browser. The reconciliation is right that this should be a
single P1 rather than five scattered P2s, because that absence is the proximate cause of at least five confirmed
money bugs — every one of the units defects shipped with the suite green.

| Layer | Covered | Gap |
|---|---|---|
| DVM contracts | Go tests + a 3,000-step fuzz differential against `reference/pool.go` | **no model for any `...To` wrapper** — which is precisely why BC-1 survived |
| `app/amm.js` | **nothing** | a third implementation of the AMM maths with zero tests |
| statement ordering | **nothing** before this session | now `TestNoExternalSendBeforeAFailureExit` |
| `app/templates` vs `contracts/` | nothing | `simdeploy` is the only writer and it needs a daemon |
| browser | `tools/uitest/ui.mjs`, 44 assertions | **not one money-maths comparison**; viewports 390 and 768 unasserted |

The cheapest high-value addition is `tests/js/` under plain `node --test`, with vectors generated from
`TestPoolFuzz` via `t.Logf`. No new dependency, and it closes the gap that let every units bug through.

<details>
<summary>All 29 findings in this slice</summary>

| Id | Sev | Finding |
|---|---|---|
| `PROTO·BC-1` | P0 | launch.bas pays the overshoot refund twice: SwapDeroForAssetTo drains a launch's entire raise in one anonymous call |
| `ARCH·C1` | P1 | genconfig writes every file before it validates, so the failed testnet build left a complete-looking bundle whose config.js hashes none of the templates beside it |
| `DATA·D3-01` | P1 | Every price-change window on a wrapped-quote market reads ≈ −100.00% — a regression introduced by the decimals fix |
| `PERF·F1` | P1 | Quote-decimals fix is incomplete: every change window reads -100% and every candle is 10^7 off on a wrapped-quote market |
| `PROD·P7-03` | P1 | The in-app plaque promises "All platform fees are distributed amongst the NFT holders" and "all rights revoked"; the shipped defaults distribute 50%, wrapped-asset fees can reach holders through no path at all, and every payout requires a live treasury key |
| `PROTO·BC-2` | P1 | Vault: Veto works while halted but Released does not, so the Paying interlock lapses and a withdrawal already paid abroad can be refunded on chain |
| `REQ·A8-01` | P1 | The app's designated final word makes three claims the shipped deployment cannot deliver — and the UI test locks one of them in |
| `REQ·A8-05` | P1 | The owner's 'one market drained while another is left behind' requirement is defended on the Markets screen only — not where the trade is signed |
| `ARCH·T1` | P2 | tools/simsmoke installs pool.bas without the `quote` argument the contract has required since v2 — the end-to-end reserve differential cannot run |
| `PERF·F10` | P2 | Zero JavaScript tests; the app's entire pricing and display layer is untested, and that is how F1 shipped |
| `PERF·F11` | P2 | A Settings toggle that does nothing, a CSP allowance for a client that does not exist, and five dead fields |
| `PROTO·BC-5` | P2 | The executable model has no equivalent of the ...To wrapper, so the differential and fuzz suites structurally cannot see BC-1 |
| `PROTO·BC-6` | P2 | launch.bas tmode 2: creator tax accrued before graduation is unreachable for ever if the token never graduates |
| `REQ·A8-02` | P2 | One factory owner transaction de-verifies every market the app has ever listed; template hashes have a `legacy` list, the treasury pointer has none |
| `REQ·A8-08` | P2 | The Founders Pass promise is rendered as unconditional in the UI while the code deliberately excludes wrapped-quote markets and the asset leg |
| `REQ·A8-09` | P2 | `genconfig -dist` fails outright: all four testnet template hashes are stale, so the on-chain testnet app can no longer be refreshed |
| `SEC·RT-01` | P2 | A vault Burn with no quorum is unrecoverable — there is no expiry path, and both the contract header and the Wrap screen claim the opposite |
| `SEC·RT-05` | P2 | The registry treasury check silently disables itself when the factory's treasury is unreadable, while the Verified badge still claims the treasury was checked |
| `UX·F14` | P2 | 768px is untested and the app is a 430px strip there, while two media queries switch to wide layouts inside that narrow column |
| `ARCH·C2` | P3 | Nothing asserts app/templates == Canon(contracts); the tests test one file and the app ships the other, and the only bridge needs a live simulator and a wallet |
| `ARCH·Q2` | P3 | "Quote decimals" exists in two incompatible types (BigInt from D.qdec, Number on s.quoteDec) and A.price silently defaults to 5 when handed the BigInt one |
| `ARCH·X1` | P3 | amm.js is a third implementation of the AMM maths with no test of any kind; the contract and the Go reference are fuzz-differentialed against each other, and the JS that picks min_out is not |
| `REQ·A8-10` | P3 | The only build identity a user is shown is a hand-edited constant, and it is already wrong |
| `REQ·A8-13` | P3 | No guard that app/templates matches contracts/, and the UI test still covers three of the five required widths |
| `SEC·RT-11` | P3 | Three leftovers from the quote-decimals fix: a cross-quote volume sum on the profile screen, a DERO-scaled ClaimTax button, and a DERO-worded tax risk line |
| `UX·F17` | P3 | Meaningless badges, decorative statistics and a launch-sheet promise the app's own risk text contradicts |
| `UX·F22` | P3 | Every on-chain document is permanently described as "Veil", and the install is unfixable after deployment |
| `UX·F9` | P3 | Only the newest deposit index is readable, and the screen actively encourages creating one — hiding the address a deposit may already be in flight to |
| `ARCH·X2` | P4 | Two test files marked temporary ship and run as if permanent, and the Paying non-refresh property — the one the contract comment calls load-bearing — is stated and untested |

</details>

---

## 12. Dependency audit

There is almost nothing to audit, and that is the correct answer.

| Surface | Dependencies | Verdict |
|---|---|---|
| `app/` runtime | **none** | Correct and worth defending. No framework, no bundler, no CDN — the CSP forbids one anyway. |
| `tools/` | `derohe` (vendored) | Unavoidable; it is the chain. |
| `tests/` | `derohe` vendor tree | Unavoidable — derohe declares no requirements and only builds against its own vendor dir. |
| `tools/uitest` | Playwright, external to the repo via `$PW_DIR` | Correct: a dev-only tool, not vendored, not shipped. |

No dependency should be added or removed. The one genuine finding in this area is the opposite of bloat:
`tools/vaultsigner` — the single unsupervised production daemon — is built on `tools/simrpc`, whose entire error
policy is `log.Fatal` (`ARCH·V1`). That is correct for twelve throwaway simulator scripts and wrong for a signer
that must survive a transient node error with no operator to restart it.

---

## 13. Documentation gaps

| Missing | Why it matters |
|---|---|
| `research/wrapper/MECHANISM_REVIEW.md` | The federation-vs-atomic-swaps verdict. `docs/TODO.md` §2.1 says it "has never been answered properly". Everything in the brief's contradiction traces here. |
| `deploy/federation.md` | Named as a go/no-go evidence artefact and **does not exist**, so that gate cannot fail. |
| Per-chain vault parameter table | `capratio`, `mindep`, `minburn`, `wfee`, `cap` are all in the foreign chain's base units. Four are immutable after Seal. Copying the simulator's values to a real Monero vault is wrong by 10⁷ — the documented `capratio` of 3 would cap a real Monero vault at 0.00000018 XMR. |
| A claims register | Every categorical UI sentence should name the file:line of the mechanism that makes it true. None does. |
| Key decommissioning | Three keys survive the runbook with no disposition step, and `deploy/README.md`'s Rollback section *depends* on the INDEX key while `HANDOVER` §0.0 says it may be discarded. |
| `LICENSE`, `SECURITY.md` | Neither exists in a public repository. |

---

## 14. Design and "AI slop" audit

The design thesis in `docs/DESIGN_SYSTEM.md` is strong and mostly executed: black ground, lime as verb-only,
2px radii, no glow, no gradient, no pill soup. A screenshot of DMZ is not interchangeable with Pump.fun.

The failures are specific rather than stylistic:

- **The gradient avatar** (`app/ui.js:66`) is the largest break in the thesis — a generated hue ramp, in a system
  that bans gradients, next to a network grammar (`parts.css:102`) that already does the job. The fix is a
  deletion.
- **Contrast.** `--fg3` fails AA. One token (`#7a7e81`, 5.13:1) fixes every failure in the app.
- **Copy.** "Nothing to run, nothing to rug" on the launch sheet, "all rights revoked" and "all platform fees are
  distributed amongst the NFT holders" in `notice.js` — categorical claims with no mechanism behind them. These
  are not design problems; they are honesty problems wearing design clothes.
- **Viewports 390 and 768 are unasserted**, so mobile behaviour is unknown rather than good.
- Decorative statistics: several read-outs are shown with more precision than the underlying data supports.

The honest summary: the visual identity is deliberate and defensible. The *language* is where DMZ still sounds
generated, and the fix is to delete adjectives rather than add design.

<details>
<summary>All 44 findings in this slice</summary>

| Id | Sev | Finding |
|---|---|---|
| `PERF·F2` | P1 | The localStorage market cache has no size budget: it silently exceeds quota at ~40 markets, and every page load then pays a ~3,000-round-trip cold start |
| `PROD·P7-03` | P1 | The in-app plaque promises "All platform fees are distributed amongst the NFT holders" and "all rights revoked"; the shipped defaults distribute 50%, wrapped-asset fees can reach holders through no path at all, and every payout requires a live treasury key |
| `PROD·P7-04` | P1 | The factory registry is permissionless, deduplication-free, unprunable and owned by nobody after deployment: ~0.1 DERO per entry buys a permanent per-block RPC amplification on every client |
| `PROTO·BC-2` | P1 | Vault: Veto works while halted but Released does not, so the Paying interlock lapses and a withdrawal already paid abroad can be refunded on chain |
| `PROTO·BC-4` | P1 | The federation bond deters nothing for users: Seal permits n = k+1 (a colluding quorum that can never be slashed), and a slashed bond goes to the treasury, not to the victims |
| `REQ·A8-01` | P1 | The app's designated final word makes three claims the shipped deployment cannot deliver — and the UI test locks one of them in |
| `REQ·A8-03` | P1 | The mainnet runbook cannot produce a working vault: it never says how to choose the per-chain parameters, and none of them can be changed after Seal |
| `REQ·A8-04` | P1 | Nothing in the deployment runbook disposes of the three keys that outlive the operator, and the founders promise requires a human to run a bot forever |
| `SEC·RT-02` | P1 | Halt freezes burns AND releases with no timeout, and the Wrap screen tells the user halted withdrawals stay payable |
| `UX·F6` | P1 | The launch wizard parses the graduation target at DERO's 5 decimals whatever quote is chosen, and labels every preview figure DERO |
| `DATA·D3-09` | P2 | The localStorage cache is silently discarded for any market that has a published sell |
| `PROD·P7-06` | P2 | Trending is bought with gas: the `hot` score is dominated by an unbounded per-block term that one address can drive alone, and its own comment claims the opposite |
| `PROTO·BC-8` | P2 | The product brief describes swap.bas/board.bas, which are not deployed; the shipped settlement path is pool.bas + vault.bas, and the repo's own docs already say so |
| `REQ·A8-02` | P2 | One factory owner transaction de-verifies every market the app has ever listed; template hashes have a `legacy` list, the treasury pointer has none |
| `REQ·A8-07` | P2 | Fifteen commits — the whole quote-asset and visual-identity programmes — with no loop record, against a rule that requires one per loop |
| `REQ·A8-08` | P2 | The Founders Pass promise is rendered as unconditional in the UI while the code deliberately excludes wrapped-quote markets and the asset leg |
| `REQ·A8-09` | P2 | `genconfig -dist` fails outright: all four testnet template hashes are stale, so the on-chain testnet app can no longer be refreshed |
| `SEC·RT-01` | P2 | A vault Burn with no quorum is unrecoverable — there is no expiry path, and both the contract header and the Wrap screen claim the opposite |
| `SEC·RT-05` | P2 | The registry treasury check silently disables itself when the factory's treasury is unreadable, while the Verified badge still claims the treasury was checked |
| `SEC·RT-08` | P2 | The app sends vault Burn at ring size 2 although the contract never reads SIGNER(), and the screen states the ring-2 exposure as a contract requirement |
| `UX·F10` | P2 | --fg3 is 3.68:1 on the ground — every label, eyebrow, note, stat caption, inactive tab and empty-state body in the app fails WCAG AA |
| `UX·F12` | P2 | DERO still leads: the Home instrument, the telemetry tape and the app's own on-chain description all put DERO first and never name XMR or ZEC |
| `UX·F13` | P2 | The identity avatar is an hsl gradient on every market row and every feed item — the loudest colour on every screen in a system that allows one accent |
| `UX·F15` | P2 | Modal sheet has no focus trap or focus return, nav tabs declare role=tab without aria-selected, the chart is pointer-only, and the tape can only be paused by hovering |
| `UX·F16` | P2 | The chain name is hard-truncated at 40 characters and lands mid-word in eight places on the wrap screen |
| `UX·F4` | P2 | The wrap screen states that doing nothing pays a withdrawal; the contract has no such path |
| `UX·P8` | P2 | Two buttons pass a boolean where the helper expects an attributes object, so they are never disabled — including Burn on a halted vault |
| `ARCH·A1` | P3 | dupTicker and pool naming are implemented twice; the copy in token.js runs on the deep-link route and has already drifted — a shared link to the genuine wrapped market shows a hex ticker and a false "Ticker shared" warning |
| `ARCH·Q2` | P3 | "Quote decimals" exists in two incompatible types (BigInt from D.qdec, Number on s.quoteDec) and A.price silently defaults to 5 when handed the BigInt one |
| `ARCH·X1` | P3 | amm.js is a third implementation of the AMM maths with no test of any kind; the contract and the Go reference are fuzz-differentialed against each other, and the JS that picks min_out is not |
| `DATA·D3-13` | P3 | An unknown quote asset and an unknown base asset both silently fall back to DERO's five decimals |
| `DATA·D3-16` | P3 | Block-derived counts are labelled as trade counts, and 'all' range charts show only the retained window |
| `PROD·P7-07` | P3 | "Creator allocation: none" is only the install-time grant; the curve has no per-address cap, no cooldown and no launch delay, and DERO's encrypted balances make the resulting concentration unobservable to anyone |
| `REQ·A8-12` | P3 | chat.bas ships as a top-level tab with a deployed contract and has no entry in the contract reference, the README or the architecture |
| `REQ·A8-13` | P3 | No guard that app/templates matches contracts/, and the UI test still covers three of the five required widths |
| `REQ·A8-14` | P3 | Five documents that the project's own plans and gates require do not exist |
| `SEC·RT-10` | P3 | 'Verified' is a claim about what one untrusted node reported, and the intro and Settings copy state it as absolute |
| `UX·F17` | P3 | Meaningless badges, decorative statistics and a launch-sheet promise the app's own risk text contradicts |
| `UX·F18` | P3 | The page the app calls the one that outranks every other piece of copy is unreachable on mobile, and so is the build stamp |
| `UX·F20` | P3 | Fee and unit disclosure gaps: the token page's Market read-outs carry no quote symbol, the withdrawal fee names no recipient, and the contract id the copy tells you to check is truncated |
| `UX·F9` | P3 | Only the newest deposit index is readable, and the screen actively encourages creating one — hiding the address a deposit may already be in flight to |
| `ARCH·X2` | P4 | Two test files marked temporary ship and run as if permanent, and the Paying non-refresh property — the one the contract comment calls load-bearing — is stated and untested |
| `REQ·A8-16` | P4 | DESIGN_SYSTEM.md says six tokens are deleted; they are still defined as aliases, and one dead computation remains in live.js |
| `UX·F23` | P4 | Dead code and dead markup left behind by the redesign |

</details>

---

## 15. Claims versus mechanisms

62 findings touch a claim the product makes. This is the largest single cluster and it collapses to one missing
artefact: **a register mapping every categorical UI sentence to the file:line that enforces it.**

| Claim | Where | Reality |
|---|---|---|
| "doing nothing releases a withdrawal, it never cancels one" | `wrap.js` | False during a halt, and false if quorum is lost. `SEC·RT-01`, `PROTO·BC-3` |
| "All platform fees are distributed amongst the NFT holders" | `notice.js:16` | Shipped default is 50%; wrapped-asset fees can reach holders through no path at all |
| "all rights revoked" / owner "none" | `notice.js:39` | Three keys survive the runbook; `SetTreasury` de-verifies every market |
| "every listing is verified" | `screens.js`, `app.js:263` | The same node supplies the code and the registry; the treasury check fails open |
| "locked forever" liquidity | token page | tmode-1 tax drains up to 10% of the quote reserve on every sell |
| "Nothing to run, nothing to rug" | launch sheet | Creator allocation is sellable at any time |
| fresh deposit indexes are unlinked | wrap screen | `vault.bas` publishes `ua_<j>` and `ul_<addr>` |
| trustless / non-custodial (brief) | brief | A bonded federation custodies the coins |

`tools/uitest/ui.mjs:106` asserts `/revoked/i` must be present — so one of these untrue claims is currently a
**regression requirement**. That line should be deleted with the claim.

<details>
<summary>All 62 findings in this slice</summary>

| Id | Sev | Finding |
|---|---|---|
| `ARCH·C1` | P1 | genconfig writes every file before it validates, so the failed testnet build left a complete-looking bundle whose config.js hashes none of the templates beside it |
| `ARCH·Q1` | P1 | The quote-decimals fix reached one of four price call sites; priceAt and candles still assume DERO, so a wrapped-quote market shows −100% change and a chart 10⁷× off |
| `DATA·D3-02` | P1 | The chart and the price above it disagree by 10^7 on the same screen for a wrapped-quote market |
| `PERF·F2` | P1 | The localStorage market cache has no size budget: it silently exceeds quota at ~40 markets, and every page load then pays a ~3,000-round-trip cold start |
| `PROD·P7-03` | P1 | The in-app plaque promises "All platform fees are distributed amongst the NFT holders" and "all rights revoked"; the shipped defaults distribute 50%, wrapped-asset fees can reach holders through no path at all, and every payout requires a live treasury key |
| `PROD·P7-04` | P1 | The factory registry is permissionless, deduplication-free, unprunable and owned by nobody after deployment: ~0.1 DERO per entry buys a permanent per-block RPC amplification on every client |
| `PROTO·BC-2` | P1 | Vault: Veto works while halted but Released does not, so the Paying interlock lapses and a withdrawal already paid abroad can be refunded on chain |
| `PROTO·BC-3` | P1 | A vault halt has no expiry and blocks Burn, so k signers who never vote Halt(0) freeze every holder out of the only exit permanently |
| `REQ·A8-01` | P1 | The app's designated final word makes three claims the shipped deployment cannot deliver — and the UI test locks one of them in |
| `REQ·A8-03` | P1 | The mainnet runbook cannot produce a working vault: it never says how to choose the per-chain parameters, and none of them can be changed after Seal |
| `REQ·A8-04` | P1 | Nothing in the deployment runbook disposes of the three keys that outlive the operator, and the founders promise requires a human to run a bot forever |
| `SEC·RT-02` | P1 | Halt freezes burns AND releases with no timeout, and the Wrap screen tells the user halted withdrawals stay payable |
| `SEC·RT-04` | P1 | The vault publicly links a DERO address to its foreign deposit address and to every one of its deposit indexes, while the app claims fresh indexes are unlinked |
| `UX·F1` | P1 | The wrap screen prints the vault's minimum deposit, mint cap and headroom as "0" — the one number whose whole purpose is to prevent an unrecoverable loss |
| `UX·F2` | P1 | The Zcash vault cannot transact: app.tx's contract allow-list only knows the single legacy cfg.vault |
| `UX·F6` | P1 | The launch wizard parses the graduation target at DERO's 5 decimals whatever quote is chosen, and labels every preview figure DERO |
| `ARCH·E1` | P2 | router.dispatch's try/catch is the app's only render safety net and cannot catch the seven screens that render from an async function; there is no unhandledrejection handler either |
| `DATA·D3-05` | P2 | Two quote-denominated figures were missed by the labelling pass: Profile's 'Published vol' adds XMR to DERO, and the Claim-creator-tax button prints a quote amount as DERO |
| `DATA·D3-09` | P2 | The localStorage cache is silently discarded for any market that has a published sell |
| `DATA·D3-10` | P2 | The trade ledger records the quote, not the fill, and records the amount sent rather than the amount the contract consumed |
| `PERF·F11` | P2 | A Settings toggle that does nothing, a CSP allowance for a client that does not exist, and five dead fields |
| `PERF·F14` | P2 | The Wrap screen redraws every block from a vault snapshot it read once and never refreshes |
| `PROD·P7-05` | P2 | "Locked forever" liquidity is drainable by the creator: in tmode 1 every sell takes up to 10% of the AMM's quote reserve as creator tax, and the token page states the lock without qualification |
| `PROD·P7-06` | P2 | Trending is bought with gas: the `hot` score is dominated by an unbounded per-block term that one address can drive alone, and its own comment claims the opposite |
| `PROD·P7-09` | P2 | The impersonation guard covers tickers only: display names, NFT collection names and the Founders Pass itself can all be copied with no warning anywhere |
| `PROTO·BC-6` | P2 | launch.bas tmode 2: creator tax accrued before graduation is unreachable for ever if the token never graduates |
| `PROTO·BC-8` | P2 | The product brief describes swap.bas/board.bas, which are not deployed; the shipped settlement path is pool.bas + vault.bas, and the repo's own docs already say so |
| `REQ·A8-06` | P2 | The current working list is invisible: every routing document sends the next agent to a backlog that predates all recent work |
| `REQ·A8-07` | P2 | Fifteen commits — the whole quote-asset and visual-identity programmes — with no loop record, against a rule that requires one per loop |
| `REQ·A8-08` | P2 | The Founders Pass promise is rendered as unconditional in the UI while the code deliberately excludes wrapped-quote markets and the asset leg |
| `REQ·A8-11` | P2 | The app tells users anyone can verify the reserve; no tool, procedure or backlog item exists to do it |
| `SEC·RT-01` | P2 | A vault Burn with no quorum is unrecoverable — there is no expiry path, and both the contract header and the Wrap screen claim the opposite |
| `SEC·RT-05` | P2 | The registry treasury check silently disables itself when the factory's treasury is unreadable, while the Verified badge still claims the treasury was checked |
| `SEC·RT-06` | P2 | The localStorage market cache re-reads only the newest 4 records, so a same-origin writer can forge an entire price history and trade feed that survives verification |
| `SEC·RT-07` | P2 | The vault-symbol impersonation guard inspects the ticker only; the attacker-controlled display name is rendered everywhere with no check |
| `SEC·RT-08` | P2 | The app sends vault Burn at ring size 2 although the contract never reads SIGNER(), and the screen states the ring-2 exposure as a contract requirement |
| `UX·F10` | P2 | --fg3 is 3.68:1 on the ground — every label, eyebrow, note, stat caption, inactive tab and empty-state body in the app fails WCAG AA |
| `UX·F11` | P2 | An unreachable node produces skeletons that animate forever, and no screen in the app has a retry |
| `UX·F16` | P2 | The chain name is hard-truncated at 40 characters and lands mid-word in eight places on the wrap screen |
| `UX·F4` | P2 | The wrap screen states that doing nothing pays a withdrawal; the contract has no such path |
| `UX·P8` | P2 | Two buttons pass a boolean where the helper expects an attributes object, so they are never disabled — including Burn on a halted vault |
| `ARCH·A1` | P3 | dupTicker and pool naming are implemented twice; the copy in token.js runs on the deep-link route and has already drifted — a shared link to the genuine wrapped market shows a hex ticker and a false "Ticker shared" warning |
| `ARCH·D1` | P3 | networks.json's vaultAsset block is dead data that contradicts the live record for the same contract — 5 decimals against 12 — and netcfg.Save silently deletes any field the struct does not know |
| `DATA·D3-08` | P3 | A market that fails to load stays on screen indefinitely with no staleness marker, and switching nodes invalidates nothing |
| `DATA·D3-14` | P3 | The trade sheet mis-states fee, tax and refund on the buy that completes a bonding curve |
| `DATA·D3-16` | P3 | Block-derived counts are labelled as trade counts, and 'all' range charts show only the retained window |
| `PROD·P7-07` | P3 | "Creator allocation: none" is only the install-time grant; the curve has no per-address cap, no cooldown and no launch delay, and DERO's encrypted balances make the resulting concentration unobservable to anyone |
| `PROD·P7-10` | P3 | The bond can be worth arbitrarily less than what a quorum could steal, and even when slashed it pays the treasury rather than the victims — the wrap screen's trust panel says 'slashable' and stops there |
| `PROD·P7-11` | P3 | Founders Pass rewards are pro-rata to whoever is registered at the instant of a deposit, with no lock and no time weighting, so a just-in-time registrant captures a share a long-term holder earned |
| `REQ·A8-10` | P3 | The only build identity a user is shown is a hand-edited constant, and it is already wrong |
| `SEC·RT-10` | P3 | 'Verified' is a claim about what one untrusted node reported, and the intro and Settings copy state it as absolute |
| `SEC·RT-11` | P3 | Three leftovers from the quote-decimals fix: a cross-quote volume sum on the profile screen, a DERO-scaled ClaimTax button, and a DERO-worded tax risk line |
| `SEC·RT-12` | P3 | The trade sheet's refund figure for a graduating buy does not match what launch.bas refunds |
| `UX·F17` | P3 | Meaningless badges, decorative statistics and a launch-sheet promise the app's own risk text contradicts |
| `UX·F18` | P3 | The page the app calls the one that outranks every other piece of copy is unreachable on mobile, and so is the build stamp |
| `UX·F19` | P3 | Vetoed withdrawals are described as a benign delivery failure, and the veto/Paying window is invisible |
| `UX·F20` | P3 | Fee and unit disclosure gaps: the token page's Market read-outs carry no quote symbol, the withdrawal fee names no recipient, and the contract id the copy tells you to check is truncated |
| `UX·F21` | P3 | An irreversible local wipe has no confirmation, the shoulder-surfing control is two hops from the screen it protects, and the reduce-motion setting does not do what it says |
| `UX·F9` | P3 | Only the newest deposit index is readable, and the screen actively encourages creating one — hiding the address a deposit may already be in flight to |
| `ARCH·Z1` | P4 | Dead exports and one dead field that is also wrong: app.pnl computes a per-asset average cost at hard-coded DERO decimals that nobody reads |
| `REQ·A8-16` | P4 | DESIGN_SYSTEM.md says six tokens are deleted; they are still defined as aliases, and one dead computation remains in live.js |
| `UX·F23` | P4 | Dead code and dead markup left behind by the redesign |

</details>

---

## 16. Remaining unknowns — what still needs a chain

Static analysis does not prove runtime behaviour. These are unresolved, with the exact test for each.

| Finding | Test that settles it |
|---|---|
| **All PERF findings except F8** | They were never adversarially verified (§6). Re-run PERF's own `bench*.mjs` under review, then a Playwright trace |
| `SEC·RT-06` cache origin | Confirm two TELA sites can occupy port 8082 at different times and so share `localStorage` |
| `ARCH·G1` fee divergence | `DERO.GetGasEstimate` on one pool entrypoint with two different signers; compare `gasstorage` |
| `DATA·D3-07` graduation | Drive a launch to graduation on the simulator; dump `p_<n>` across the boundary; compare `flow()` against the real trades |
| `DATA·D3-08` stale reads | Force intermittent `GetSC` failures; assert the token page marks staleness rather than showing last-known prices under a live block clock |
| `ARCH·A1` (narrowed) | Deep-link an **unlisted** wrapped-base pool with an empty cache; assert `s.dec` is the vault's 12, not the hard-coded `5n` |
| `REQ·A8-02` | Deploy two markets, `SetTreasury` to a new address, reload; confirm both go untrusted and vanish |
| `UX·F5` | Fill a vault to `cap`; confirm the screen still reads "Open" and Register is still offered |
| `UX·F14` | Add 390 and 768 viewports to `ui.mjs`; assert `documentElement.scrollWidth <= width` |
| `PROD·P7-04` | Register 5,000 duplicate SCIDs; time an app refresh |
| `PROD·P7-10` | 2-of-4 vault; two signers slash the other two; assert the third slash reverts at `na == k` |
| `PROTO·BC-2/BC-3` after fixing | Re-run PROTO's two reproductions inverted |

One process note: **no agent ran `tests/run.sh`.** PROTO reported it green from a prior run. I have run it since,
repeatedly, including after every change in this session — it is green at 58s.

---

## 17. Prioritised remediation matrix

| # | Finding | Sev | Area | Effort | Risk reduction | Action |
|---|---|---|---|---|---|---|
| 1 | `PROTO·BC-1` | P0 | contract | one line moved | **drains a whole raise** | ✅ **done** |
| 2 | `DATA·D3-15` pool `quote` | P2 | app | one argument | creation is impossible without it | ✅ **done** |
| 3 | `UX·F2` vault allow-list | P1 | app | one line | half the audience unusable | ✅ **done** |
| 4 | Defect 1 — units in the historical path | P1 | app | ~10 lines | flagship numbers wrong | ✅ **done** |
| 5 | `DATA·D3-04` graduation parse + bound | P1 | app + contract | ~8 lines | immutably wrong launches | ✅ **done** |
| 6 | Defect 5 — vault state machine | P1 | contract | one coherent change | **funds frozen or double-paid** | **next, gates Seal** |
| 7 | `DATA·D3-03/09` side `{1,2}` | P1 | app | two lines | sells erased from the ledger | ready |
| 8 | `ARCH·C1` genconfig order | P1 | tools | block move | ships an invalid bundle | ready |
| 9 | Defect 3 — display scale constants | P2 | app | ~15 lines | wrapped aggregates render `0.0000` | ready |
| 10 | Defect 6 — `decorateOne` extraction | P2 | app | medium | safety warnings missing where the trade is signed | needs the UI test first |
| 11 | Defect 10 — `lastError` + async catch | P2 | app | small | skeletons animate forever on failure | ready |
| 12 | `ARCH·V1` `vaultsigner` `Try*` API | P1 | tools | medium | one RPC blip retires a signer permanently | ready |
| 13 | Defect 9 — claims register | P2 | docs + copy | medium | the authoritative page states falsehoods | ready |
| 14 | Defect 13 — `tests/js/` | P1 | tests | medium | **gates the regression tests for 1, 2, 3, 7** | do early |
| 15 | `PROTO·BC-4` `n ≥ 2k` | P2 | contract | decision | unslashable quorum | **decision required** |

---

## 18. Phase plan

**Phase 0 — blockers.** ✅ BC-1, the `AddLiquidity` landmine, the units regression, the graduation parse, the
vault allow-list, pool creation. All landed and proven this session.

**Phase 1 — security foundation.** The vault state machine as one change (halt lease; `Released` permitted while
halted; `Burn` permitted while halted; burn expiry; per-signer `Paying` marks; a halt matrix asserted by a test).
Measure install gas. Then `vaultsigner`'s `Try*` API and the `markPaying` age check. **Nothing may Seal until
this lands.**

**Phase 2 — protocol correctness.** `PROTO·BC-6` (tmode-2 tax stranded), `PROTO·BC-7` (`nft.bas mt_` never
deleted), the `launchBuy` overshoot mirror in `amm.js`, and a `reference/` model for every `...To` wrapper —
without which the fuzz suite structurally cannot see the next BC-1.

**Phase 3 — data integrity.** Side `{1,2}` in both validators, `lastError`/`lastAttempt`, the treasury
fail-closed with a third badge branch, the accepted-treasury set with `legacy` (**before any `SetTreasury`**),
and the localStorage budget with eviction.

**Phase 4 — UX foundation.** `decorateOne` extraction so identity and safety warnings exist on every surface
including the trade sheet; split-pair moved to the data layer; error/empty/retry states; the claims register and
the ~11 copy corrections that follow from it.

**Phase 5 — visual identity.** Delete the gradient avatar for the existing network grammar; raise `--fg3`;
delete the stale comments and dead tokens. All deletions.

**Phase 6 — performance.** Only after re-verifying PERF's findings (§6). Sparkline downsampling and the
visibility guard on the block handler are the two with a measured case.

**Phase 7 — hardening.** `tests/js/`, viewports 390 and 768, the registry sybil test, the `hot`-score rework,
and fuzzing the wrapper models.

**Phase 8 — production readiness.** `MECHANISM_REVIEW.md`, `deploy/federation.md`, the per-chain vault parameter
table, key decommissioning, `LICENSE`, `SECURITY.md`, and a testnet redeploy — the current one cannot produce a
valid bundle.

---

## 19. Sequencing constraints that determine everything

The reconciliation's most important structural point, which no individual agent drew:

1. **Six of the P0/P1s live in immutable contracts.** Every contract fix must land *before* any mainnet install.
2. **Every contract edit changes the Canon hash.** `app/config.js templates[k].legacy` must carry the old hash
   **before** the new contract ships, or every already-listed market de-verifies and vanishes. This is the single
   most likely way a correct fix causes an outage. (`simdeploy` now demotes automatically; it was used for the
   fixes in this session.)
3. **`A.price` had to be reshaped before the `stats.js` call sites were fixed**, or the natural spelling
   `price(pt, s.dec, D.qdec(s))` would silently reintroduce the bug, because `D.qdec` returns a BigInt and
   `Number.isInteger(12n)` is `false`. This is why the fix in this session changed the signature rather than
   adding arguments at call sites.
4. **The graduation parser could not be fixed alone.** Parsing 10 XMR correctly gives 10¹³, which exceeded
   `launch.bas`'s 10¹² ceiling — the install would simply have reverted. The contract bound had to move with it.
5. **The mainnet gate is passing on a corpse.** "Testnet live and exercised for one week" is being satisfied by a
   bundle that cannot create a contract (`forms.js` throws `template hash mismatch` on all four templates) and
   lists nothing installed since 2026-09-06.

---

## 20. Production readiness

**Not ready. Do not deploy to mainnet. Do not Seal a vault.**

Of the six blockers the reconciliation identified, **one is now fixed** (the drain, and creation is restored).
Five remain:

1. ~~One anonymous call drains a launch's entire raise.~~ **Fixed and guarded by a test.**
2. **The vault cannot be safely sealed.** Its halt is a permanent freeze of the only exit; `Released` is blocked
   while `Veto` is not, so a paid withdrawal can be refunded on chain; a burn with no quorum is unrecoverable;
   four parameters are immutable after Seal and undocumented; the one check a non-signer could run has no tool.
   **Seal is the point of no return.**
3. **Wrapped display is still partly wrong.** The chart and change windows are fixed, but wrapped aggregates
   still render `"0.0000"` and the vault's minimum deposit renders `"0 XMR"` — on the one screen that exists to
   prevent an unrecoverable loss.
4. **The authoritative page states falsehoods**, and the UI test enforces one of them.
5. **The deployment pipeline cannot produce a valid bundle**, and the mainnet gate depends on it.
6. **Missing artefacts**: `MECHANISM_REVIEW.md`, `deploy/federation.md`, `LICENSE`, `SECURITY.md`, and the
   per-chain vault parameter table.

The honest one-line assessment: **the contracts' arithmetic is better than most audited DeFi code, and
everything around it — the state machine's terminal states, the display layer's units, and the product's
claims about itself — is not yet at that standard.**
