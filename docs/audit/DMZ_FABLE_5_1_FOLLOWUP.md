# DMZ — remediation and verification specification

**For the engineer who picks this up next.** A thirteen-agent adversarial audit ran against this repository on
2026-09-08 (`b5af173`): eight independent specialists, four verifiers instructed to refute by default, one
reconciliation pass. The full report is [`DMZ_OPUS5_MASSIVE_AUDIT.md`](DMZ_OPUS5_MASSIVE_AUDIT.md); every
finding's evidence is [`FINDINGS.md`](FINDINGS.md).

**Do not assume the audit is correct.** Verify every finding against the actual code before acting on it. One
proposed fix in the audit — setting `ring: 16` on the vault `Burn` — **would have destroyed user funds**, and
was caught only because a verifier read the surrounding code. A dangerous-looking pattern is not a defect if
something nearby prevents it, and a harmless-looking line can be a defect because of its caller.

**Do not assume the existing implementation is correct either.** Six confirmed money defects shipped with the
full test suite green.

Determine the root cause, verify exploitability or correctness impact, implement the smallest robust fix, add
regression coverage, and re-audit the affected subsystem. Preserve the lightweight architecture: no framework,
no bundler, no runtime dependency, every app file under 18,432 bytes after gzip+base64. Prefer deletion and
simplification over new abstraction — in this codebase the best fixes have all been deletions or signature
changes that removed the ability to be wrong.

Treat as high-risk code requiring explicit verification: all financial calculation, transaction construction,
wallet operations, the vault state machine, permissions, and privacy boundaries. When a requirement is
ambiguous, document the ambiguity instead of inventing behaviour. When the audit names a missing requirement,
decide whether it is genuinely necessary before implementing it. Never claim a security property that is not
technically enforced. Never hide uncertainty.

---

## 0. The five hard sequencing constraints

Read these before planning anything. They determine the order of every remaining fix, and two of them can cause
an outage if ignored.

1. **Every contract edit changes the Canon hash.** `app/config.js templates[k].legacy` must carry the outgoing
   hash **before** the new contract ships, or every already-listed market de-verifies and disappears from the
   app (`feed.js templateHashes`). `tools/simdeploy` demotes automatically — use it, and check the output says
   "kept as legacy".
2. **Six of the remaining P1s live in immutable contracts.** They must all land before any mainnet install, and
   before any vault `Seal`.
3. **`Seal` is the point of no return.** Four vault parameters are immutable after it and none is documented.
   Do not seal anything until §2 is complete.
4. **The vault's halt, expiry and `Paying` fixes interact.** Removing `halted` from `Released` changes the safety
   model; per-signer marks change the veto arithmetic. Design and test them as **one** state machine, not five
   patches. **Measure install gas before deploying — the 20,000-unit budget has been hit twice.**
5. **A units fix must change the interface, not the call sites.** This is why the last one failed: a third
   optional argument defaulting to DERO's 5 decimals made "I forgot" and "I meant DERO" the same call, and four
   of six call sites forgot. `A.priceOf(market, pt)` now reads both decimals off the market. Apply the same
   principle to the remaining scale constants rather than fixing instances.

---

## 1. Verify what was already fixed — do not redo it

Landed in `d1c5a89` during the audit, each proven against a failing case. Confirm each still holds before
building on it; if any is wrong, that is a higher priority than anything below.

| Id | What was changed | Verify by |
|---|---|---|
| `PROTO·BC-1` | `launch.bas buyto` records the overshoot at line 59 and pays it at line 76, past every exit | `tests/run.sh -run TestNoExternalSend`; revert the contract and watch it name line 59 |
| `DATA·D3-01` | `A.priceOf(market, pt)` replaces `A.price(pt, dec, qdec)` at every call site with a market | open a wrapped-quote market: change windows must not read −100%, last candle close must equal the header price |
| `DATA·D3-04` | wizard parses graduation in the quote's decimals; `launch.bas` ceiling 10¹²→10¹⁵ | pick wXMR in the wizard; the preview must be labelled XMR and a 100 XMR target must install |
| `UX·F2` | transaction allow-list uses `D.vaults()` instead of the single-vault `cfg.vault` | attempt a ZEC deposit; it must not be refused as "not a verified listing" |
| `DATA·D3-15` | app and `simsmoke` install `pool.bas` with the `quote` argument | Markets → Create a pool must succeed |

---

## 2. Phase 1 — the vault state machine. **Nothing may `Seal` until this is done.**

This is the largest remaining risk and the audit reproduced all three failures on the real DVM.

**`PROTO·BC-3`** — `Halt` blocks `Burn`, and a halt has no expiry. Reproduced at **+2,000,000 blocks**: `Burn`
still returns "Discarded knowingly". k signers who never vote `Halt(0)` — or who can no longer reach k because a
key was lost — freeze every holder out of the only exit, permanently.

**`PROTO·BC-2`** — `Released` refuses while halted (`vault.bas` line 398); `Veto` does not (line 443). The
`Paying` mark lapses after 2000 blocks and **cannot be refreshed**. So during a halt longer than ten hours, a
withdrawal whose coins have already been sent abroad can have its units returned on chain. Reproduced. The
aftermath is terminal: `inflightUnits` stops counting the vetoed withdrawal, `reserve < owed` becomes
permanently true, the daemon halts forever, and `Burn` is blocked by that halt.

**`SEC·RT-01`** — a `Burn` that never reaches quorum is unrecoverable. There is no expiry and no reclaim.

All three contradict `vault.bas`'s own header — *"doing nothing pays the user"* — which is true of `wdelay` and
`bigdelay` but was generalised to the whole withdrawal. `app/wrap.js` repeats the generalisation to users.

**Required outcome.** One coherent change to `contracts/vault.bas`:

- a **halt lease** — store the height the halt was set and let it decay unless re-voted, so `halted` cannot
  outlive the federation's attention;
- **`Released` permitted while halted** — a halt must stop the operations that *add* exposure (`Register`,
  `Attest`, `Mint`), and must never stop the truthful recording of a payment already made;
- **`Burn` permitted while halted** — burning during a halt is safe; the resulting withdrawals queue behind
  `wdelay`;
- an **expiry on an unresolved burn**, so the units return without anyone acting;
- **per-signer `Paying` marks** with an age check, so one signer's lapsed mark does not speak for the quorum;
- a **halt matrix** (entrypoint × halted) written down and asserted by a test. The current rule is implicit in
  eleven guard lines and two of them disagree.

**Verification.** Re-run PROTO's two reproductions inverted: a halt must expire; `Released` must succeed while
halted; `Burn` must succeed while halted; an unresolved burn must return its units after the expiry. Then
measure install gas and confirm it is under 20,000 — `reference.Canon` strips comments, so measure the
canonicalised source, which is what `tests/vault_smoke_test.go` already reports.

**Then** `tools/vaultsigner`: give `tools/simrpc` a `Try*` API for the six calls the daemon makes
(`ARCH·V1`). Its entire error policy is `log.Fatal`, which is correct for throwaway simulator scripts and wrong
for the one unsupervised production daemon — a single transient node error permanently retires a federation
signer, and by design there is no operator to restart it. Add the `markPaying` age check at the same time: the
daemon currently re-pays abroad under a possibly-expired mark.

---

## 3. Phase 2 — protocol correctness

- **`PROTO·BC-5` — write a `reference/` model for every `...To` wrapper.** This is the cause of BC-1 surviving:
  the fuzz differential models the plain entrypoints only, so it structurally could not see a defect in the
  wrapper. Until this exists, the next BC-1 is equally invisible.
- **`TestNoExternalSendBeforeAFailureExit`** (added this session, `tests/sendorder_test.go`) now guards statement
  order. Keep it. It walks control flow from every external send and asserts it cannot reach a non-zero
  `RETURN`. It carries one documented allow-list entry — `factory.bas reg`, which returns 1 on *success*.
- **`PROTO·BC-6`** — tmode-2 tax is stranded in `tacc` with no path out.
- **`PROTO·BC-7`** — `nft.bas` never deletes `mt_` keys. Note the finding's line numbers are wrong; the file is
  127 lines and the real lines are 2, 65, 78 and 93–95. Verify before acting.
- **`DATA·D3-14` / `SEC·RT-12`** — `amm.js launchBuy` does not mirror the contract's overshoot branch. It must
  reproduce `launch.bas` lines 50–59 exactly, including the fee and tax recomputation on `used`. **Write the
  differential test first.**
- **`PROD·BC-4` / `PROD·P7-10`** — a colluding quorum is unslashable at `n = k+1`, which `contracts/README.md`
  explicitly blesses, and slashed bond goes to the treasury rather than to the users who lost the reserve.
  **This is an economic decision, not a code fix.** The app-side disclosure is safe to ship now: `wrap.js`
  already fetches `na` and `k`, so it can say "this federation cannot slash its own quorum" when `na < 2k`.

---

## 4. Phase 3 — data integrity

- **`DATA·D3-03` / `DATA·D3-09`** — one side encoding, two wrong validators. `feed.js:290 validLocalTrade` and
  the cache validator both require `side ∈ {0,1}` while the contracts emit `{1,2}`. Consequence: every
  unpublished sell is written to the ledger and erased by the next write, and the entire cache is discarded for
  any market with a published sell. **One constant fixes both.**
- **Defect 3 — the remaining display scale constants.** `A.fmtDero` is `fmtUnits(v, 5, …)`; `S.fmtDeroShort` has
  a 4-decimal floor that renders every shipped wrapped aggregate as `"0.0000"`; an unknown pool asset gets
  `s.dec = 5n` (`feed.js:166`); the thin-market threshold is `liq < 20`; `FirstDepositMin` and `MinLiq` are in
  DERO units. **`UX·F1` is the one to do first**: `fmtUnits` truncates the vault minimum deposit to `"0 XMR"` on
  the one screen that exists to prevent an unrecoverable loss, because a deposit below the minimum can never be
  returned on a ring-signature chain.
- **`SEC·RT-05`** — the registry treasury check silently disables itself when the treasury is unparsable, while
  the badge still says "Verified". **Do not make it throw**: fail-closed here means the whole market list
  disappears on one malformed key. Add an `unverified` flag and a third badge branch.
- **`REQ·A8-02`** — one `SetTreasury` de-verifies every market at once. Add an accepted-treasury set with
  `legacy`, mirroring the template-hash shape. **This must land before any `SetTreasury` and before key
  disposal.**
- **Defect 10 — fail-open guards and no error state.** Add `lastError`/`lastAttempt` to `feed.js`, a three-way
  branch on every screen, and Retry actions. `ui.js:141` must catch async screen handlers
  (`const r = fn(...); if (r && r.then) r.catch(...)`) and the app should register an `unhandledrejection`
  handler. Without this, a failed read silently re-adopts the previous record under a live block clock, and
  skeletons animate forever.
- **`PERF·F2`** — the localStorage cache exceeds quota at roughly 40 markets and an empty `catch` hides it.
  Needs a budget, eviction, and `QuotaExceededError` handling.

---

## 5. Phase 4 — identity, safety and claims

**Defect 6 — market identity is computed per-screen.** `feed.js decorate()` operates on the whole map, so any
caller needing one market reimplements it: `token.js:35` is a ~1000-character duplicate that has already
drifted. Extract `data.decorateOne(s)` and `data.markDuplicates(peers)`, then call them from `decorate()`,
`token.js` and the split-pair path. **Extend the UI test before doing this** — it touches identity and safety
logic on every surface.

That retires:

- **`SEC·RT-07`** — the impersonation guard added this session checks the *ticker* but not the display **name**
  rendered next to it. A token named "Wrapped Monero" with a different ticker is still unguarded.
- **`PROD·P7-09`** — NFT names and collections are never checked at all.
- **`UX·F3`** — the warning is hover-only on Markets and absent from Home and the feed.
- **`REQ·A8-05`** — the most serious member. The split-pair defence lives entirely inside `markets.js`, so it is
  **absent from every surface where the trade is actually signed.** This is the owner's own stated requirement —
  "no overlapping markets so people can drain what's available and leave one behind" — failing on the path that
  matters. Move split-pair detection to the data layer as `s.splitWith`, suppress non-deepest members from
  `D.trending()` and the tape, and put a warning box in the trade sheet.

**Defect 9 — the claims register.** Eleven findings collapse to one missing artefact: a register mapping every
categorical UI sentence to the `file:line` of the mechanism that makes it true. Build it, then correct or delete
every claim that has no mechanism. Start with `app/notice.js`, which declares itself authoritative while omitting
the federation entirely and asserting three untrue things: owner "none", "all rights revoked", and "All platform
fees are distributed amongst the NFT holders" (the shipped default is 50%, and wrapped-asset fees can reach
holders through no path at all). **`tools/uitest/ui.mjs:106` asserts `/revoked/i` must be present — delete that
assertion with the claim, or an untrue statement remains a regression requirement.**

Also correct: `wrap.js`'s "doing nothing releases a withdrawal, it never cancels one" (false during a halt and
false if quorum is lost — fix the copy *after* §2 changes the behaviour, so the copy becomes true rather than
being softened into vagueness); the token page's "locked forever" liquidity (tmode-1 tax drains up to 10% of the
quote reserve per sell); the launch sheet's "Nothing to run, nothing to rug"; and "every listing is verified".

---

## 6. Phase 5–7 — identity, performance, hardening

**Visual** (all deletions, all safe now): replace the gradient avatar at `ui.js:66` with the network grammar
`parts.css:102` already implements — it is the largest single break in the design thesis, in a system that bans
gradients. Raise `--fg3` to `#7a7e81` (5.13:1); one token fixes every AA contrast failure. Delete the stale
`markets.js:32-35` KNOWN GAP comment, which describes a bug that is already fixed.

**Performance — re-verify before acting.** PERF's findings were never adversarially verified: UX and PERF both
numbered their findings `F1…F14` and the verifier merge collated by bare id, so every PERF verdict except `F8`
carries UX's reasoning for a different finding. Re-run PERF's own `bench*.mjs` under review first. The two with
a plausible measured case are the sparkline downsample above 160 points (`ui.js:119`) and a `!document.hidden`
guard on the block handler (`app.js:74`). `PERF·F8` — three round trips per contract on cold start at
concurrency 4 — is the one genuinely verified performance finding.

**Testing — `ARCH·X1`, and it gates the regression tests for four other defects.** Add `tests/js/` under plain
`node --test` with vectors generated from `TestPoolFuzz` via `t.Logf`. **No new dependency.** `app/amm.js` is a
third implementation of the AMM maths with zero tests, and `tools/uitest/ui.mjs`'s 44 assertions contain not one
money-maths comparison. This absence is the proximate cause of at least five confirmed money bugs.

Also add viewports **390 and 768** to `ui.mjs` with the existing `scrollWidth <= width` assertion; and
`ARCH·C2` — a test that fails when `app/templates` differs from `reference.Canon(contracts/*)`.

**Build pipeline — `ARCH·C1` / `REQ·A8-09`.** `tools/genconfig` writes the whole bundle before it validates, so
`dist/testnet/` is on disk right now looking complete, without a manifest, its `config.js` hashing none of the
four templates beside it. Move the hash check above `os.RemoveAll` and `defer os.RemoveAll` on failure. This
matters beyond tidiness: the "testnet live and exercised for one week" mainnet gate is being satisfied by a
bundle that cannot create a contract and lists nothing installed since 2026-09-06. **The gate is passing on a
corpse.**

---

## 7. Phase 8 — the artefacts that do not exist

| Artefact | Why |
|---|---|
| `research/wrapper/MECHANISM_REVIEW.md` | The federation-vs-atomic-swaps verdict. `docs/TODO.md` §2.1 says it "has never been answered properly". **The brief's contradiction — "no wrapped coins, no custodian" versus a shipped bonded federation — is this missing decision, not a marketing error.** `swap.bas` and `board.bas` are written, tested and undeployed. Answer it. |
| `deploy/federation.md` | Named as a go/no-go evidence artefact and does not exist, so that gate cannot fail. |
| Per-chain vault parameter table | `capratio`, `mindep`, `minburn`, `wfee`, `cap` are all in the foreign chain's base units and four are immutable after `Seal`. The documented `capratio` of 3 would cap a **real Monero vault at 0.00000018 XMR** — an operator following the current runbook deploys a vault that cannot hold anything. |
| `tools/reservecheck` | `REQ·A8-11`: the app tells users a viewing key makes the reserve checkable. There is no tool, no runbook and no backlog item — `grep viewkey tools/**/*.go` returns one comment. Either build it or stop claiming it. |
| Key decommissioning procedure | Three keys survive the runbook with no disposition step, and `deploy/README.md`'s Rollback section *depends* on the INDEX key while `HANDOVER` §0.0 says it may be discarded. **Resolve that contradiction in writing before disposing of anything.** |
| `LICENSE`, `SECURITY.md` | Neither exists in a public repository. |

---

## 8. Open questions requiring a human decision — do not implement blindly

1. **Federation or atomic swaps?** The product brief and the shipped system disagree. Everything about the
   vault's risk profile follows from this answer.
2. **`Seal` enforcing `n ≥ 2k`?** It would forbid 2-of-3, which `contracts/README.md` explicitly blesses. An
   economic and deployment decision.
3. **Where does slashed bond go?** Today, the treasury. Arguably it should go to the users whose reserve was
   lost. Decide, then implement.
4. **Founders Pass and wrapped assets.** Holders are promised a share of *every* fee. `founders.bas Deposit()`
   reads only `DEROVALUE()`, so fees earned in wXMR can reach them through no path at all. Options: per-asset
   accounting in the contract; a treasury that converts before depositing (needs a price, so probably no); or
   per-asset claims. Whichever is chosen, **the app must never show a fee total that mixes assets.**
5. **Ring 16 on the vault `Burn`.** Wanted for privacy. Requires a `BurnTo` wrapper in `vault.bas` first and
   `D.vaultWithdrawals()` re-keyed off `w_<j>_by`. **Setting `ring: 16` without the wrapper destroys user
   funds** — `Burn` exits `RETURN 1` and above ring 2 there is no signer to refund.
6. **`SEC·RT-04` — the vault publishes `ua_<j>` and `ul_<addr>`**, a public map from a DERO address to its
   foreign deposit address, while the app says fresh indexes are unlinked. Either remove the mapping (does the
   contract need it?) or correct the claim. This is a privacy claim on a privacy product.

---

## 9. Every open finding

101 findings remain open after the audit fixes. Severity is the verifier's where it adjusted the auditor's.
`unverified` in the last column means the harness defect described in §6 of the report — treat those as
single-source claims and check them yourself.

| Id | Sev | Finding | Where to look | Verifier |
|---|---|---|---|---|
| `ARCH·C1` | P1 | genconfig writes every file before it validates, so the failed testnet build left a complete-looking bundle w… | `tools/genconfig/main.go` | CONFIRMED |
| `ARCH·V1` | P1 | vaultsigner, the one production daemon, is built on simrpc, whose entire error policy is log.Fatal — one tran… | `tools/simrpc/simrpc.go` | CONFIRMED |
| `DATA·D3-03` | P1 | Every sell made on this device is written to the trade ledger and then permanently erased by the next write | `app/feed.js:290` | CONFIRMED |
| `PERF·F2` | P1 | The localStorage market cache has no size budget: it silently exceeds quota at ~40 markets, and every page lo… | `app/feed.js:134` | unverified |
| `PERF·F6` | P1 | loadSocial does an unbounded whole-contract-tree read of the social contract on every page load | `app/feed.js:249` | unverified |
| `PROD·P7-03` | P1 | The in-app plaque promises "All platform fees are distributed amongst the NFT holders" and "all rights revoke… | `app/notice.js:16` | CONFIRMED |
| `PROD·P7-04` | P1 | The factory registry is permissionless, deduplication-free, unprunable and owned by nobody after deployment: … | `contracts/factory.bas` | CONFIRMED |
| `PROTO·BC-2` | P1 | Vault: Veto works while halted but Released does not, so the Paying interlock lapses and a withdrawal already… | `contracts/vault.bas` | CONFIRMED |
| `PROTO·BC-3` | P1 | A vault halt has no expiry and blocks Burn, so k signers who never vote Halt(0) freeze every holder out of th… | `contracts/vault.bas` | CONFIRMED |
| `PROTO·BC-4` | P1 | The federation bond deters nothing for users: Seal permits n = k+1 (a colluding quorum that can never be slas… | `contracts/vault.bas` | CONFIRMED |
| `REQ·A8-01` | P1 | The app's designated final word makes three claims the shipped deployment cannot deliver — and the UI test lo… | `app/notice.js:15` | CONFIRMED |
| `REQ·A8-03` | P1 | The mainnet runbook cannot produce a working vault: it never says how to choose the per-chain parameters, and… | `deploy/README.md` | CONFIRMED |
| `REQ·A8-04` | P1 | Nothing in the deployment runbook disposes of the three keys that outlive the operator, and the founders prom… | `docs/HANDOVER.md:9` | CONFIRMED |
| `REQ·A8-05` | P1 | The owner's 'one market drained while another is left behind' requirement is defended on the Markets screen o… | `app/markets.js:36` | CONFIRMED |
| `SEC·RT-02` | P1 | Halt freezes burns AND releases with no timeout, and the Wrap screen tells the user halted withdrawals stay p… | `contracts/vault.bas` | CONFIRMED |
| `SEC·RT-04` | P1 | The vault publicly links a DERO address to its foreign deposit address and to every one of its deposit indexe… | `contracts/vault.bas` | CONFIRMED |
| `UX·F1` | P1 | The wrap screen prints the vault's minimum deposit, mint cap and headroom as "0" — the one number whose whole… | `app/wrap.js:114` | CONFIRMED |
| `ARCH·E1` | P2 | router.dispatch's try/catch is the app's only render safety net and cannot catch the seven screens that rende… | `app/ui.js:141` | CONFIRMED |
| `ARCH·T1` | P2 | tools/simsmoke installs pool.bas without the `quote` argument the contract has required since v2 — the end-to… | `contracts/pool.bas:15` | CONFIRMED |
| `DATA·D3-05` | P2 | Two quote-denominated figures were missed by the labelling pass: Profile's 'Published vol' adds XMR to DERO, … | `app/screens.js:198` | CONFIRMED |
| `DATA·D3-09` | P2 | The localStorage cache is silently discarded for any market that has a published sell | `app/feed.js:131` | CONFIRMED |
| `DATA·D3-10` | P2 | The trade ledger records the quote, not the fill, and records the amount sent rather than the amount the cont… | `app/app.js:213` | CONFIRMED |
| `DATA·D3-11` | P2 | The formatter and the risk thresholds are DERO-magnitude constants applied unchanged to XMR and ZEC amounts | `app/stats.js:121` | CONFIRMED |
| `PERF·F10` | P2 | Zero JavaScript tests; the app's entire pricing and display layer is untested, and that is how F1 shipped | `_test.mjs` | unverified |
| `PERF·F11` | P2 | A Settings toggle that does nothing, a CSP allowance for a client that does not exist, and five dead fields | `app/screens.js:249` | unverified |
| `PERF·F12` | P2 | markets.js documents a 'KNOWN GAP' in pair grouping that no longer exists, and duplicates feed.js's impersona… | `app/markets.js:32` | unverified |
| `PERF·F13` | P2 | The registry's full id list is refetched every block although its counts say it did not change, and markets.j… | `app/feed.js:79` | unverified |
| `PERF·F14` | P2 | The Wrap screen redraws every block from a vault snapshot it read once and never refreshes | `app/wrap.js:465` | unverified |
| `PERF·F3` | P2 | D.feed() materialises and sorts every trade of every market on every block to render 24-60 rows | `app/feed.js:271` | unverified |
| `PERF·F4` | P2 | Home rebuilds ten sparklines from full point history every block; each is a ~43 KB SVG path drawn into a 120x… | `app/home.js:50` | unverified |
| `PERF·F5` | P2 | A backgrounded tab keeps doing full per-block refreshes; the hidden-tab back-off the author wrote is bypassed | `app/app.js:72` | unverified |
| `PERF·F7` | P2 | enrich() rebuilds a Set over every trade of every market, every block, for one number shown on one screen | `app/stats.js:68` | unverified |
| `PROD·P7-05` | P2 | "Locked forever" liquidity is drainable by the creator: in tmode 1 every sell takes up to 10% of the AMM's qu… | `contracts/launch.bas` | CONFIRMED |
| `PROD·P7-06` | P2 | Trending is bought with gas: the `hot` score is dominated by an unbounded per-block term that one address can… | `app/stats.js:70` | CONFIRMED |
| `PROD·P7-08` | P2 | The Markets table ranks and displays volume, liquidity and FDV across different quote assets as bare unlabell… | `app/markets.js:81` | CONFIRMED |
| `PROD·P7-09` | P2 | The impersonation guard covers tickers only: display names, NFT collection names and the Founders Pass itself… | `app/feed.js` | CONFIRMED |
| `PROTO·BC-5` | P2 | The executable model has no equivalent of the ...To wrapper, so the differential and fuzz suites structurally… | `reference/launch.go` | CONFIRMED |
| `PROTO·BC-6` | P2 | launch.bas tmode 2: creator tax accrued before graduation is unreachable for ever if the token never graduates | `contracts/launch.bas` | CONFIRMED |
| `PROTO·BC-8` | P2 | The product brief describes swap.bas/board.bas, which are not deployed; the shipped settlement path is pool.b… | `pool.bas` | CONFIRMED |
| `REQ·A8-02` | P2 | One factory owner transaction de-verifies every market the app has ever listed; template hashes have a `legac… | `app/feed.js:85` | CONFIRMED |
| `REQ·A8-06` | P2 | The current working list is invisible: every routing document sends the next agent to a backlog that predates… | `docs/TODO.md` | CONFIRMED |
| `REQ·A8-07` | P2 | Fifteen commits — the whole quote-asset and visual-identity programmes — with no loop record, against a rule … | `docs/history/HISTORY.md` | CONFIRMED |
| `REQ·A8-08` | P2 | The Founders Pass promise is rendered as unconditional in the UI while the code deliberately excludes wrapped… | `app/nfts.js:94` | CONFIRMED |
| `REQ·A8-09` | P2 | `genconfig -dist` fails outright: all four testnet template hashes are stale, so the on-chain testnet app can… | `networks.js` | CONFIRMED |
| `REQ·A8-11` | P2 | The app tells users anyone can verify the reserve; no tool, procedure or backlog item exists to do it | `app/wrap.js:327` | CONFIRMED |
| `SEC·RT-01` | P2 | A vault Burn with no quorum is unrecoverable — there is no expiry path, and both the contract header and the … | `contracts/vault.bas` | CONFIRMED |
| `SEC·RT-05` | P2 | The registry treasury check silently disables itself when the factory's treasury is unreadable, while the Ver… | `app/feed.js` | CONFIRMED |
| `SEC·RT-06` | P2 | The localStorage market cache re-reads only the newest 4 records, so a same-origin writer can forge an entire… | `app/feed.js` | CONFIRMED |
| `SEC·RT-07` | P2 | The vault-symbol impersonation guard inspects the ticker only; the attacker-controlled display name is render… | `app/feed.js` | CONFIRMED |
| `SEC·RT-08` | P2 | The app sends vault Burn at ring size 2 although the contract never reads SIGNER(), and the screen states the… | `contracts/vault.bas` | CONFIRMED |
| `SEC·RT-09` | P2 | The Paying/Veto guard closes the common double-pay case but leaves a 2000-block window, and the mark cannot b… | `contracts/vault.bas` | CONFIRMED |
| `UX·F10` | P2 | --fg3 is 3.68:1 on the ground — every label, eyebrow, note, stat caption, inactive tab and empty-state body i… | `app/styles.css:9` | CONFIRMED |
| `UX·F11` | P2 | An unreachable node produces skeletons that animate forever, and no screen in the app has a retry | `app/feed.js:239` | CONFIRMED |
| `UX·F12` | P2 | DERO still leads: the Home instrument, the telemetry tape and the app's own on-chain description all put DERO… | `app/home.js:120` | CONFIRMED |
| `UX·F13` | P2 | The identity avatar is an hsl gradient on every market row and every feed item — the loudest colour on every … | `app/ui.js:66` | CONFIRMED |
| `UX·F14` | P2 | 768px is untested and the app is a 430px strip there, while two media queries switch to wide layouts inside t… | `app/styles.css:32` | PLAUSIBLE |
| `UX·F15` | P2 | Modal sheet has no focus trap or focus return, nav tabs declare role=tab without aria-selected, the chart is … | `app/ui.js:45` | CONFIRMED |
| `UX·F16` | P2 | The chain name is hard-truncated at 40 characters and lands mid-word in eight places on the wrap screen | `app/wrap.js:194` | CONFIRMED |
| `UX·F3` | P2 | On Markets the impersonation warning is a hover-only tooltip behind a glyph shared with two weaker warnings; … | `app/markets.js:142` | CONFIRMED |
| `UX·F4` | P2 | The wrap screen states that doing nothing pays a withdrawal; the contract has no such path | `app/wrap.js:337` | CONFIRMED |
| `UX·F5` | P2 | A vault that cannot mint still reports "Open" and invites a deposit — headroom, bond ratio and the mint windo… | `app/wrap.js:233` | PLAUSIBLE |
| `UX·F7` | P2 | There is no pending-transaction state anywhere: the sheet closes and a transaction that can take four and a h… | `app/token.js:216` | CONFIRMED |
| `UX·P8` | P2 | Two buttons pass a boolean where the helper expects an attributes object, so they are never disabled — includ… | `app/ui.js:84` | CONFIRMED |
| `ARCH·C2` | P3 | Nothing asserts app/templates == Canon(contracts); the tests test one file and the app ships the other, and t… | `tests/pool_test.go:38` | CONFIRMED |
| `ARCH·D1` | P3 | networks.json's vaultAsset block is dead data that contradicts the live record for the same contract — 5 deci… | `deploy/networks.js` | CONFIRMED |
| `ARCH·M1` | P3 | Protocol constants are unnamed literals in four to twelve places each, and one of them (the thin-market thres… | `deploy/networks.js` | CONFIRMED |
| `ARCH·Q2` | P3 | "Quote decimals" exists in two incompatible types (BigInt from D.qdec, Number on s.quoteDec) and A.price sile… | `app/feed.js:65` | CONFIRMED |
| `ARCH·X1` | P3 | amm.js is a third implementation of the AMM maths with no test of any kind; the contract and the Go reference… | `app/amm.js:1` | CONFIRMED |
| `DATA·D3-06` | P3 | The Markets table ranks and prints Vol / Liquidity / FDV across quotes on raw magnitude, and on desktop those… | `app/markets.js:81` | CONFIRMED |
| `DATA·D3-07` | P3 | Graduation and Buyback are counted as trading volume and as directional pressure, because the LP-move filter … | `app/stats.js:25` | PLAUSIBLE |
| `DATA·D3-08` | P3 | A market that fails to load stays on screen indefinitely with no staleness marker, and switching nodes invali… | `app/feed.js:235` | PLAUSIBLE |
| `DATA·D3-12` | P3 | The Portfolio omits every token whose only market is a pool contract, and prints an unvaluable holding as zero | `app/app.js:83` | CONFIRMED |
| `DATA·D3-13` | P3 | An unknown quote asset and an unknown base asset both silently fall back to DERO's five decimals | `app/feed.js:64` | CONFIRMED |
| `DATA·D3-14` | P3 | The trade sheet mis-states fee, tax and refund on the buy that completes a bonding curve | `app/amm.js:47` | CONFIRMED |
| `DATA·D3-16` | P3 | Block-derived counts are labelled as trade counts, and 'all' range charts show only the retained window | `app/home.js:127` | CONFIRMED |
| `PERF·F8` | P3 | Two avoidable RPC round trips per contract on cold start, and a probe concurrency of 4 that is the wrong defa… | `app/feed.js:106` | CONFIRMED |
| `PERF·F9` | P3 | flash() forces a synchronous layout per changed number, so a Markets redraw can thrash layout ~120 times in o… | `app/live.js:32` | unverified |
| `PROD·P7-07` | P3 | "Creator allocation: none" is only the install-time grant; the curve has no per-address cap, no cooldown and … | `contracts/launch.bas` | CONFIRMED |
| `PROD·P7-10` | P3 | The bond can be worth arbitrarily less than what a quorum could steal, and even when slashed it pays the trea… | `contracts/vault.bas` | CONFIRMED |
| `PROD·P7-11` | P3 | Founders Pass rewards are pro-rata to whoever is registered at the instant of a deposit, with no lock and no … | `contracts/founders.bas` | CONFIRMED |
| `PROTO·BC-7` | P3 | nft.bas Unlock does not delete mt_<addr>, so an edition is one-per-address-for-ever, not one-per-lock as docu… | `contracts/nft.bas` | CONFIRMED |
| `REQ·A8-10` | P3 | The only build identity a user is shown is a hand-edited constant, and it is already wrong | `app/index.html:9` | CONFIRMED |
| `REQ·A8-12` | P3 | chat.bas ships as a top-level tab with a deployed contract and has no entry in the contract reference, the RE… | `app/index.html:37` | CONFIRMED |
| `REQ·A8-13` | P3 | No guard that app/templates matches contracts/, and the UI test still covers three of the five required widths | `docs/PLATFORM_BACKLOG.md` | CONFIRMED |
| `REQ·A8-14` | P3 | Five documents that the project's own plans and gates require do not exist | `README.md` | CONFIRMED |
| `REQ·A8-15` | P3 | The published file and DOC counts — which the mainnet funding estimate rests on — are two files and three DOC… | `README.md` | CONFIRMED |
| `SEC·RT-10` | P3 | 'Verified' is a claim about what one untrusted node reported, and the intro and Settings copy state it as abs… | `app/feed.js` | CONFIRMED |
| `SEC·RT-11` | P3 | Three leftovers from the quote-decimals fix: a cross-quote volume sum on the profile screen, a DERO-scaled Cl… | `app/screens.js` | CONFIRMED |
| `SEC·RT-12` | P3 | The trade sheet's refund figure for a graduating buy does not match what launch.bas refunds | `app/amm.js` | CONFIRMED |
| `UX·F17` | P3 | Meaningless badges, decorative statistics and a launch-sheet promise the app's own risk text contradicts | `app/home.js:118` | CONFIRMED |
| `UX·F18` | P3 | The page the app calls the one that outranks every other piece of copy is unreachable on mobile, and so is th… | `app/app.js:52` | CONFIRMED |
| `UX·F19` | P3 | Vetoed withdrawals are described as a benign delivery failure, and the veto/Paying window is invisible | `app/wrap.js:379` | CONFIRMED |
| `UX·F20` | P3 | Fee and unit disclosure gaps: the token page's Market read-outs carry no quote symbol, the withdrawal fee nam… | `app/token.js:69` | PLAUSIBLE |
| `UX·F21` | P3 | An irreversible local wipe has no confirmation, the shoulder-surfing control is two hops from the screen it p… | `app/screens.js:276` | CONFIRMED |
| `UX·F22` | P3 | Every on-chain document is permanently described as "Veil", and the install is unfixable after deployment | `tools/telainstall/main.go:203` | CONFIRMED |
| `UX·F9` | P3 | Only the newest deposit index is readable, and the screen actively encourages creating one — hiding the addre… | `app/wrap.js:51` | CONFIRMED |
| `ARCH·N1` | P4 | window.Veil hides three real couplings: feed.js calls a method defined in another file, V.markets exists only… | `app/founders.js:7` | PLAUSIBLE |
| `ARCH·Z1` | P4 | Dead exports and one dead field that is also wrong: app.pnl computes a per-asset average cost at hard-coded D… | `app/app.js:219` | CONFIRMED |
| `DATA·D3-17` | P4 | The in-memory incremental path never re-verifies existing history, so a reorg that keeps pc and tc unchanged … | `app/feed.js:180` | CONFIRMED |
| `REQ·A8-16` | P4 | DESIGN_SYSTEM.md says six tokens are deleted; they are still defined as aliases, and one dead computation rem… | `docs/DESIGN_SYSTEM.md` | CONFIRMED |
| `UX·F23` | P4 | Dead code and dead markup left behind by the redesign | `app/live.js:56` | CONFIRMED |

---

## 10. Required outputs when you are done

1. **A change log** — what was fixed, what was rejected as a false positive and why.
2. **A remaining-risk report** — what is still open, and what the residual risk is for each.
3. **A list of everything needing human review**, separate from the above.
4. **A regression search** — for each fix, what nearby behaviour could it have broken? Three of the four fixes
   made before this audit each left an adjacent hole and two created new P1s. Assume yours will too, and go
   looking.
5. **Re-run the audit** on the subsystems you touched.

**Verification commands.** `tests/run.sh` (full DVM suite, ~58s, must be green);
`PW_DIR=<dir with playwright> node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-N`;
`cd tools && go run ./genconfig -dist -network simulator` (every file must stay under the TELA DOC cap);
`cd tools && go vet ./...`. After any contract change, re-run `tools/simdeploy` with the existing SCIDs so the
outgoing template hash is demoted to `legacy`, and confirm the output says so.
