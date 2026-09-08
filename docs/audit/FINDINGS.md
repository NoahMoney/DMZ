# DMZ audit — full findings

Every finding from the eight-agent audit of 2026-09-08, severity first, with the adversarial verifier's
reasoning where one was recorded. Ids are prefixed with the agent because UX and PERF both numbered theirs
`F1..F14`; see `DMZ_OPUS5_MASSIVE_AUDIT.md` §6 for why PERF's verdicts are not its own.

---

### `PROTO·BC-1` — launch.bas pays the overshoot refund twice: SwapDeroForAssetTo drains a launch's entire raise in one anonymous call

**P0 · fund-loss / accounting · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\launch.bas, function buyto, source lines 129-130 (DVM lines 59-60):
  59 SEND_ASSET_TO_ADDRESS(s, d - used, q)
  60 IF out == 0 || out < min_out THEN GOTO 900
The external send happens BEFORE the last failure exit. The caller, SwapDeroForAssetTo (launch.bas lines 85-93, DVM lines 30-40), treats a non-zero return as "refund everything" and calls refundto(s), which at launch.bas lines 187-188 sends ASSETVALUE(q) -- still the FULL incoming amount, because vendor/derohe/dvm/dvm_functions.go dvm_send_asset_to_address never decrements dvm.State.Assets, and dvm_assetvalue returns dvm.State.Assets[asset] verbatim. The wrapper then RETURNs 0, so everything commits (vendor/derohe/dvm/sc.go:279-286: state is committed only on a zero return; a non-zero return sets err="Discarded knowingly" and reverts -- which is why the plain signed entrypoint is safe and only the ...To wrapper is not).
PROVEN on the real DVM via tests/harness (dvm.Execute_sc_function + SanityCheckExternalTransfers + ProcessExternal): deposited 2,150,000 atomic DERO into a launch holding 2,150,000, dest was paid 4,202,806, contract balance fell 2,150,000 -> 97,194.
pool.bas does not have this defect: its buyto/sellto emit no external send before their last GOTO 900, so only launch.bas is affected.

**Problem.** Any caller can withdraw more quote than they deposited from a bonding-curve launch. The only precondition is that the buy overshoots the remaining curve (out >= rem) with a non-zero remainder, and that min_out exceeds rem -- both fully caller-controlled. The profit is d - used, bounded only by what the contract holds, i.e. the whole raise. The call needs no signer (ring 16), so the thief is not identifiable on chain.

**Failure scenario.** Precondition: a launch.bas market in phase 0 whose curve is nearly sold out; the contract holds ~grad of quote (its entire raise plus fees and tax).
1. Attacker reads rd/ra/sold/cs from the node and computes rem = cs - sold.
2. Attacker sends SwapDeroForAssetTo(dest=<their address>, min_out=2^62, publish=0) at ring size 16, attaching d = the contract's whole quote balance.
3. buyto takes the overshoot branch (line 115 out >= rem), computes used << d, and at line 129 sends the overshoot d-used to dest.
4. Line 130 fails min_out and returns 1 with no state written.
5. The wrapper calls refundto, which sends ASSETVALUE(q) = d to dest again, then RETURNs 0 so both transfers commit.
Impact: dest receives 2d-used for a deposit of d; net theft d-used. Measured: 2,052,806 stolen from a 2,150,000 pot. Repeatable until the contract is empty. It also fires ACCIDENTALLY: app/token.js:184 sets min_out from a slippage-derived quote, and app/app.js:200 only guards the overshoot case client-side ("Refusing: this buy overshoots the curve..."), so an honest private buy whose cached quote raced another buyer takes the windfall too -- and that guard is unenforceable JS in an on-chain TELA site, bypassable with a direct RPC call.

**Root cause.** An external transfer was placed before the final validation in a helper whose non-zero return is interpreted by its caller as "nothing happened, refund the lot". The DVM's revert-on-non-zero-return only protects the signed entrypoints; the ...To wrappers exist precisely to return 0 (because above ring 2 derohe cannot credit a reverted call back to an unknown signer -- see the comment in tests/ringswap_test.go:54-55), and that inverts the meaning of every GOTO 900 inside buyto. The invariant "no external send before the last failure exit" was held in pool.bas and broken in launch.bas when the partial-refund path was added.

**Fix.** In contracts/launch.bas buyto, evaluate the min_out guard before any external send: hoist `IF out == 0 || out < min_out THEN GOTO 900` so it runs immediately after `46 LET out = rem` (and keep the existing check for the `out < rem` path that jumps straight to line 60). pool.bas is the correct reference -- both its buyto and sellto send only after every check. Do not attempt to fix this inside refundto by subtracting what was already sent: ASSETVALUE cannot see the earlier transfer, so the wrapper has no way to compute the residual.

**Regression test.** Add to tests/ringswap_test.go, alongside the existing refused-private-buy case at line 59 which only covers a NON-graduating buy: drive the curve to within one buy of graduation, then call SwapDeroForAssetTo with min_out = 1<<62 and d equal to the contract's quote balance, and assert `r.Sent(quote, dest) == d` exactly and `c.SCBalance(scid, quote)` is unchanged. I ran exactly this and it currently reports Sent=4,202,806 against d=2,150,000.

**Verifier.** Reproduced on the real DVM through tests/run.sh. Setup: a launch.bas market, grad 2,000,000, fee 100 bps, tax 1000 bps, curve driven to real = 1,780,000 with signed buys, phase still 0, contract holding 2,000,000 atomic DERO. One call to SwapDeroForAssetTo(dest=bob, min_out=2^62, publish=0) with no signer (ring > 2) attaching d = 2,000,000: result ok=true, bob paid 3,752,806, contract balance 2,000,000 -> 247,194. Net theft 1,752,806 in one anonymous call. The code path is exactly as the finding describes: launch.bas line 129 `SEND_ASSET_TO_ADDRESS(s, d - used, q)` precedes line 130 `IF out == 0 || out < min_out THEN GOTO 900`; SwapDeroForAssetTo line 89 treats the non-zero return as 'nothing happened' and calls refundto at line 90, which at line 188 sends ASSETVALUE(q) — still the full d, because vendor/derohe/dvm/dvm_functions.go dvm_send_asset_to_address (line 398) calls SendExternal without decrementing dvm.State.Assets and dvm_assetvalue (line 440) returns dvm.State.Assets[asset] verbatim. The wrapper then RETURNs 0, and vendor/derohe/dvm/sc.go lines 279-286 commit the transfer list only on a zero return, which is why the signed entrypoint is safe and only the wrapper is not. I also confirmed pool.bas is clean: its buyto checks min_out at line 165 before the send at line 170, and sellto likewise.

---

### `ARCH·C1` — genconfig writes every file before it validates, so the failed testnet build left a complete-looking bundle whose config.js hashes none of the templates beside it

**P1 · build-pipeline/fail-open · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `tools/genconfig/main.go` `bundle()`: `os.RemoveAll(out)` destroys any previous bundle first; `add()` writes each file to disk inside the `filepath.Walk` loop; the template-hash check (`"template %s: app/templates hash %x differs from networks.json %s"`) runs AFTER every file is already on disk; `manifest.json` is written after that; `main()` turns the returned error into `log.Fatalf`.
On disk right now: `dist/testnet/` has all 29 app files and `templates/{founders,launch,nft,pool}.bas`, and NO `manifest.json` (`dist/simulator/` has one). Measured:
  dist/testnet/templates/pool.bas → 33a126a8da585b38…
  dist/testnet/config.js pool sha256 → "d4e42ece020cff3c…", legacy: []
Same mismatch for founders, launch and nft. `tools/telainstall/main.go:131` walks `-app` and never reads manifest.json.

**Problem.** A build tool that fails leaves output that looks exactly like success. The bundle is self-contradicting: `app/forms.js:15` fetches `templates/launch.bas`, hashes it, compares to `t.sha256` and throws "template hash mismatch", so nothing can be launched, pooled or minted; `app/feed.js:194` builds its accepted-hash map from the same stale values with `legacy: []`, so any market installed from the current template is dropped with `console.warn("skipping", id, ...)`. That is the blast radius of the known-stale testnet record: not "some old markets", but the entire creation half of the product dead and the registry showing only pre-change contracts.

**Failure scenario.** Precondition: `deploy/networks.json` testnet block carries the pre-Canon hashes (8afd70fb / bc05b8e3 / 389deff9 / d4e42ece) while `app/templates/` carries the current ones. Steps: (1) `go run ./genconfig -dist`; (2) simulator succeeds, testnet fails on the hash check and log.Fatals — but `dist/testnet/` is already fully written; (3) an operator, or a script, points `telainstall -app ../dist/testnet` at it. Impact: an immutable TELA install of a build that cannot create a contract and lists nothing current. On chain, permanently, with no operator left to fix it.

**Root cause.** Validation is placed after the side effects instead of before them, and the only marker distinguishing a good bundle from a bad one (manifest.json) is written last and read by nothing downstream.

**Fix.** Move the template-hash check to the top of `bundle()`, before `os.RemoveAll(out)` — it only needs `app/templates/` and `n.Templates`, both available immediately. On any later failure, `os.RemoveAll(out)` in a defer so a failed build leaves no directory at all.

**Regression test.** A Go test that runs `bundle()` against a network block with a deliberately wrong template hash and asserts the output directory does not exist afterwards.

**Verifier.** Read tools/genconfig/main.go bundle(): `os.RemoveAll(out)` first, then the filepath.Walk loop calls add() which does `os.WriteFile` per file, then config.js is written, and only AFTER all of that does the loop `for k, t := range n.Templates` do the sha256 comparison and return the `template %s: app/templates hash %x differs from networks.json %s` error. manifest.json is written after that check, and main() wraps the return in log.Fatalf. On disk: dist/testnet/ holds 27 app files plus templates/{founders,launch,nft,pool}.bas and NO manifest.json; dist/simulator/ has one. Measured sha256 dist/testnet/templates/pool.bas = 33a126a8da585b38..., while dist/testnet/config.js line 16 records pool sha256 d4e42ece020cff3c... with `legacy: []` — same mismatch for founders (49009d2c vs 8afd70fb), launch (09cb53d2 vs bc05b8e3) and nft (f8b85449 vs 389deff9). Blast radius confirmed: app/forms.js:15 fetches t.file, hashes it and throws `template hash mismatch` — so launch/pool/NFT creation is dead; app/feed.js templateHashes() builds the accepted map from the same stale values with empty legacy, and refresh() at feed.js:234 drops non-matching markets with `console.warn("skipping", id, ...)`. tools/telainstall/main.go walks -app (line ~130) and never reads manifest.json, so nothing downstream distinguishes a good bundle from a bad one.

---

### `ARCH·F1` — The launch form parses the graduation target at DERO's 5 decimals and labels every preview figure "DERO" whichever quote is picked, so a wXMR launch graduates at 10⁻⁷ of the intended target — immutably

**P1 · duplicated-logic/units · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `app/forms.js:69` `const ... grad = A.parseUnits(f.grad.value, 5n);` — the literal 5, regardless of `quote`.
`app/forms.js:75` `error = "Graduation target must be between 10 and 10 000 000 " + (quote.sym || "DERO")` — the range is checked in atomic units (`grad < 1000000n || grad > 1000000000000n`) that only mean 10…10 000 000 at five decimals.
`app/forms.js:88-89` `pv.start.textContent = S.fmtPriceSub(p0) + " DERO"; pv.end … + " DERO"; pv.mcap … + " DERO"` — hard-coded.
`app/forms.js:86` `A.price({ rd: vd0, ra: vt0 }, dec)` — no qdec (see Q1).
`app/forms.js:96` `[10, 50, 100, 500].map((g) => ... g + " " + (quote.sym || "DERO"))` and `:107` `field("Graduation target (" + (quote.sym || "DERO") + ")", f.grad)` are built once, before `openSheet`, and are never re-rendered by `update()` — so after clicking the wXMR chip both still say DERO.
The quote is genuinely passed on: `app/forms.js:126` `["quote", "H", quote.scid]`.

**Problem.** The chip UI offers wXMR and wZEC as first-class quotes and the contract accepts them, but every number in the form is still computed and labelled in DERO. With the shipped 12-decimal XMR vault, typing 100 produces grad = 10⁷ atomic units = 0.00001 XMR. The value passes validation (10⁷ is inside 10⁶…10¹²), so the user gets no warning, and `launch.bas` is immutable once installed.

**Failure scenario.** Precondition: a build with a 12-decimal wrapped vault, i.e. the shipped config. Steps: (1) open Launch a token; (2) click the XMR quote chip; (3) the label still reads "Graduation target (DERO)" and the preset chips still read "100 DERO"; (4) type 100, intending 100 XMR; (5) confirm two transactions. Impact: the curve's graduation target is 0.00001 XMR. vd0 = grad/3, so the first non-trivial buy completes the curve and graduates the token instantly at a price seven orders of magnitude below what the preview showed. The contract is immutable; the creator's supply is now in a locked AMM seeded with dust.

**Root cause.** The same units rule as Q1, in a fourth place. `forms.js` predates the quote parameter and was extended with a quote selector without the amounts, labels or preview being made quote-aware.

**Fix.** In `readForm()` take the decimals from the selected quote: `const qdec = BigInt(Number.isInteger(quote.dec) ? quote.dec : 5)` and use it for `A.parseUnits(f.grad.value, qdec)` and for the bounds (express the bounds in whole coins and scale, rather than as atomic-unit literals). Replace the three `+ " DERO"` suffixes with `+ " " + (quote.sym || "DERO")`, pass `Number(qdec)` to `A.price` at :86, and rebuild the `chips` row and the grad field label inside `update()` so they follow the chip.

**Regression test.** In `tools/uitest/ui.mjs` (which already opens the launch sheet at :191 and asserts the preview text), click the wXMR chip and assert the graduation field label and preset chips contain "XMR", and that the args handed to `V.app.tx` carry grad = value × 10¹².

**Verifier.** app/forms.js:62 `grad = A.parseUnits(f.grad.value, 5n)` — literal 5n regardless of the selected quote. Line 71 checks `grad < 1000000n || grad > 1000000000000n`, bounds that only mean 10..10,000,000 at five decimals. Line 89 hard-codes ` DERO` on pv.start, pv.end and pv.mcap. Line 86 `A.price({rd: vd0, ra: vt0}, dec)` omits qdec (same fault as Q1). Line 58 builds the preset chips with `(quote.sym || "DERO")` and line 106/107 build the field label and note the same way — all three are constructed as arguments to openSheet at lines 95-113, before any chip click can change `quote`, and the chip handler at lines 45-53 only rewrites quoteNote.textContent and calls update(); update() (lines 77-93) never re-renders the chips, the field label or the note. So after picking XMR the label still reads 'Graduation target (DERO)' and the presets still read '100 DERO'. The quote IS passed on at line 122 `["quote", "H", quote.scid]`. With config.js vaults[0].dec = 12, typing 100 gives grad = 1e7 piconero = 0.00001 XMR, which passes the 1e6..1e12 bound. launch.bas is immutable. Every step verified.

---

### `ARCH·Q1` — The quote-decimals fix reached one of four price call sites; priceAt and candles still assume DERO, so a wrapped-quote market shows −100% change and a chart 10⁷× off

**P1 · duplicated-logic/drift · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `app/amm.js:99` is the single place that knows the units: `const price = (s, dec, qdec) => ... 10 ** (Number.isInteger(qdec) ? qdec : 5) ...`. `app/stats.js:6` re-exports it as `price(p, dec, qdec)`. Two of the four callers pass the quote decimals and two do not:
- CORRECT: `app/stats.js:19` `const p1 = price(s, s.dec, s.quoteDec)` and `app/stats.js:57` (enrich).
- WRONG: `app/stats.js:16` `return price(pts[r < 0 ? 0 : r], s.dec);` (priceAt — no qdec).
- WRONG: `app/stats.js:83` `let prevClose = price(pts[0], dec)`, `:88` and `:90` `const pr = price(p, dec)` (candles — no qdec).
- Harmless but same shape: `app/ui.js:120` (sparkline autoscales), `app/forms.js:86` (launch preview, see F1).
`app/chart.js:82,:104,:132` render exactly those candle values as axis ticks, the last-price tag and the OHLC tooltip via `S.fmtPriceSub`.

**Problem.** `change()` divides a qdec-correct current price by a 5-decimal historical price. For the shipped 12-decimal XMR vault (`app/config.js` vaults[0] `dec: 12`) p1 = p0 × 10⁻⁷, so every window returns ((10⁻⁷−1))×100 ≈ −99.99999%. The chart is internally consistent but its absolute labels are 10⁷ too large, and it sits on the same screen as the header price, which is right. The comment at `app/amm.js:96` says the point of the fix was that "every new caller inherits the fault and has to remember" — two existing callers were never updated, so the fault was not removed, only narrowed.

**Failure scenario.** Precondition: a market whose quote is a wrapped asset — `forms.js` offers exactly this (F.launch quote chips, `app/forms.js:52-54`), and `pool.bas:15` takes `quote` as an install parameter. Steps: (1) launch or pool a token quoted in wXMR; (2) open its token page. Impact: the 5m/1h/6h/24h boxes all read −100.00%, `changePill` paints them red on Home, Markets and the trade sheet, `D.movers()` (feed.js:245, sorts on |c24h|) puts every wXMR market at the top of the movers rail, and the chart's y-axis and OHLC readout disagree with the price printed 40px above them by seven orders of magnitude.

**Root cause.** The price scaling rule is expressed four times as an argument list rather than once as a property of the market. `A.price` treats an omitted qdec as "DERO", so a caller that forgets is indistinguishable from a caller that means DERO — the API cannot fail loudly.

**Fix.** Pass the quote decimals at the two sites: `app/stats.js:16` → `price(pts[r < 0 ? 0 : r], s.dec, s.quoteDec)`; `app/stats.js:83,88,90` → hoist `const qd = s.quoteDec;` next to `const dec = s.dec;` and pass it. Then delete the third argument from `A.price` entirely and read the decimals off the point's market instead, so there is no argument to forget.

**Regression test.** In the Playwright pass (`tools/uitest/ui.mjs`), after `simwrapmkt` seeds a market with a wrapped QUOTE, assert `page.evaluate(() => Veil.stats.change(m, 3600))` is within a factor of two of the header price change, and that the chart's last candle close equals `s.price` to 6 significant figures. Today nothing in the repo compares them.

**Verifier.** Read app/amm.js:99 — `price = (s, dec, qdec) => ... 10 ** (Number.isInteger(qdec) ? qdec : 5) ...`. Read app/stats.js: line 16 `return price(pts[r < 0 ? 0 : r], s.dec);` (priceAt, no qdec) against line 19 `const p1 = price(s, s.dec, s.quoteDec), p0 = priceAt(...)`. change() therefore divides a qdec-correct p1 by a 5-decimal p0. Candles at stats.js:83/88/90 all call `price(p, dec)` with no qdec, while the volume in the same loop DOES use qunit(s) — so the file is internally inconsistent at exactly the boundary claimed. app/config.js vaults[0] is `dec: 12`, confirmed, and deploy/networks.json simulator.vaults[0].dec = 12. For a 12-decimal quote p1 = p0 x 1e-7, both > 0, so change() does not return null and yields ((1e-7)-1)*100 = -99.99999%. chart.js:70/82/104/127/132 render candle values through S.fmtPriceSub, so the axis, last-price tag and OHLC tooltip carry the 1e7 error. feed.js:268 movers() sorts on Math.abs(m.c24h), so every wrapped-quote market pins to the top of the rail. app/ui.js:120 (sparkline) and app/forms.js:86 also omit qdec, as claimed. Line numbers all check out.

---

### `ARCH·V1` — vaultsigner, the one production daemon, is built on simrpc, whose entire error policy is log.Fatal — one transient node RPC error retires a federation signer permanently, and there is no operator

**P1 · error-handling/library-misuse · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `tools/simrpc/simrpc.go`: `func Must(err error) { if err != nil { log.Fatal(err) } }`, and `Address`, `Balance`, `RandomAddress`, `Height`, `StableHeight`, `WalletHeight`, `WaitBlocks`, `WaitTx`, `Send`, `SendWithFee`, `GetSC`, `SCState.U`, `SCState.Str` all route errors through it or call `log.Fatalf` directly.
`tools/vaultsigner/main.go` calls these from inside the `for { pass(...); time.Sleep(*interval) }` loop at :554-560 — `c.GetSC` at :596, :615, :794, :800, :846, :859; `c.Height()` / `c.StableHeight()` at :631, :789; `c.SendWithFee` at :577.
The repo already knows: `tools/vaultsigner/main.go:534-540` — "simrpc exits through log.Fatal on any RPC trouble, which skips every deferred call". `docs/history/HISTORY.md:1206` lists "supervisor units for the daemons; `Try*` RPC API instead of `log.Fatal`" as P1.

**Problem.** The workaround built for this (a heartbeat lock that can be taken over when stale) fixes the consequence for a SECOND signer process; it does nothing for the first, which is simply gone. The failure needs no attacker: a node restart, a 180-second timeout under load, a dropped TCP connection. `Str(key)` at :598 also log.Fatals when a vote key is absent or not a string — a normal outcome the caller two lines below is written to handle ("included but the contract did not record our vote: will vote again").

**Failure scenario.** Precondition: k-of-n = 2-of-3, signers running unsupervised as designed ("any state that needs a human to leave it is a defect"). Steps: (1) the DERO node restarts for 30 seconds; (2) two signers happen to be inside `c.GetSC` in `pass()`; (3) both `log.Fatal`. Impact: the vault drops below quorum. Deposits stop being minted, withdrawals stop being released, `Paying` marks expire after 2000 blocks and coins that were already sent abroad become Veto-able. Nothing restarts the signers because nothing was ever going to.

**Root cause.** One RPC client serves two populations with opposite requirements — throwaway simulator scripts where exiting on error is correct, and a daemon where it is fatal to the system — and the library was written for the first.

**Fix.** Add error-returning twins for the calls vaultsigner uses (`TryGetSC`, `TryHeight`, `TryStableHeight`, `TrySendWithFee`) and switch vaultsigner to them, treating an error as "skip this pass, retry after -interval". `pass()` already returns cleanly on every other failure path, so the call sites need no new structure.

**Regression test.** Point vaultsigner at a daemon port with nothing listening and assert the process is still alive after two intervals. `tools/vaultsigner/exactly_once_test.go` already simulates restarts from disk; this is the complementary case of not restarting at all.

**Verifier.** tools/simrpc/simrpc.go:28-31 `func Must(err error) { if err != nil { log.Fatal(err) } }`. GetSC (line 267) routes the http.Post error, the decode and the rpc error object through Must/log.Fatalf; U (289), Str (336), WaitTx, WaitBlocks, Send, SendWithFee all do the same. tools/vaultsigner/main.go:554-560 is the unsupervised `for { pass(...); time.Sleep(*interval) }` loop, and pass() calls c.GetSC at 596, 615, 794, 800, 846, 859 and c.Height()/c.StableHeight() at 631 and 789. The repo's own comment at main.go:534-540 states the problem verbatim, and docs/history/HISTORY.md:1206 lists `Try*` RPC API instead of log.Fatal plus supervisor units as P1. I searched docs/, deploy/, tools/vaultsigner/ and research/ for supervisor/systemd/Restart= and found no supervisor artefact. The lock-takeover heartbeat helps only a second process, as claimed.

---

### `DATA·D3-01` — Every price-change window on a wrapped-quote market reads ≈ −100.00% — a regression introduced by the decimals fix

**P1 · correctness / units · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/stats.js:16 `return price(pts[r < 0 ? 0 : r], s.dec);` — priceAt() calls A.price with no qdec, so it falls back to DERO's 5 decimals (app/amm.js:99). app/stats.js:19 `const p1 = price(s, s.dec, s.quoteDec), p0 = priceAt(s, now() - secs);` — p1 uses the market's real quote decimals, p0 does not. Commit b5af173 threaded qdec into p1 and into qunit() but left priceAt untouched; `git show b5af173 -- app/stats.js` confirms line 16 was not changed. Before that commit both sides used 1e5 and the error cancelled, so this is a regression the fix created.

**Problem.** change() divides a price computed with the correct quote decimals by a price computed with DERO's. For the configured 12-decimal XMR vault the ratio is a fixed 1e-7; for the 8-decimal ZEC vault, 1e-3. The result is not noisy — it is a constant. Replicating the exact expressions on the pool the b5af173 commit message cites (rd = 103095, XMR quote): p1 = 1.2081e-12, p0 = 1.2081e-5, change = −99.99999%.

**Failure scenario.** Precondition: any market whose `qt` is a vault with dec ≠ 5 (config.js ships XMR at 12 and ZEC at 8). Steps: open Home or Markets. Impact: c5m, c1h, c6h, c24h and c7d all render as −100.00% on every wXMR market and −99.90% on every wZEC one, permanently, whatever the price does. This feeds changePill on every row, tile and tape item; D.movers() (feed.js:268, sorted on |c24h|) so wrapped markets monopolise Home's Movers; the Gainers/Losers filters (markets.js:73-74) so no wrapped market can ever appear under Gainers; the `chg` sort column; and live.mood() (live.js:58) which paints the whole app's ambient state 'down'.

**Root cause.** The unit conversion was pushed into A.price as an optional third argument with a silent DERO default, so a caller that forgets it gets a plausible wrong number instead of an error. priceAt is the one caller inside stats.js that was missed.

**Fix.** app/stats.js:16 → `return price(pts[r < 0 ? 0 : r], s.dec, s.quoteDec);`

**Regression test.** Unit test over stats.js: build a synthetic market with quoteDec = 12 and two identical points; assert change(s, 3600) === 0, not −99.99999. Run it for quoteDec ∈ {5, 8, 12}.

**Verifier.** app/stats.js:16 is literally `return price(pts[r < 0 ? 0 : r], s.dec);` — two arguments. app/stats.js:19 is `const p1 = price(s, s.dec, s.quoteDec), p0 = priceAt(s, now() - secs);`. app/amm.js:99 defaults an omitted qdec to 5. app/config.js:12-13 ship dec 12 (XMR) and dec 8 (ZEC); app/feed.js:65 sets s.quoteDec from that record. I loaded the unmodified amm.js and stats.js into a Node VM against a synthetic pool with quoteDec 12 (rd=2e12, ra=1e6, dec=3n): price with qdec 0.002, priceAt(1h ago) 12500, change(3600) = -99.999984, change(86400) = -99.99997. A.pct (amm.js:101) prints that as "-100.00%". The same reserves with quoteDec 5 give +60.00%. Consumers confirmed: feed.js:268 movers sorts on |c24h|; markets.js:68 `case "gainers": return s.m.c24h !== null && s.m.c24h > 0` can never match; markets.js:81 has `chg` as a sort key; live.js:56-61 mood() weights c24h by log1p(vol24) and would sit at the -1 clamp.

---

### `DATA·D3-02` — The chart and the price above it disagree by 10^7 on the same screen for a wrapped-quote market

**P1 · correctness / units · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/stats.js:83 `let prevClose = price(pts[0], dec)`, :88 `prevClose = price(pts[i], dec)`, :90 `const p = pts[i], pr = price(p, dec)` — candles() never passes qdec. Its output drives chart.js:74 (y-axis extent), :82 (tick labels), :104 (the last-price tag), :127 (the crosshair price) and :132 (the OHLC tooltip). token.js:78 renders the header price from `s.price`, which enrich() computes at stats.js:57 *with* s.quoteDec.

**Problem.** On one token page the header reads 'Price · XMR  1.2074e-11' while the chart's last-price tag, axis and tooltip read 1.2074e-4 for the same block. Neither the chart nor the header says which is meant, and the chart carries the caption 'Exact at block resolution' (token.js:53).

**Failure scenario.** Precondition: a market quoted in a vault asset with dec ≠ 5. Steps: open the token page for a wXMR market. Impact: the chart's price axis, last-price tag, crosshair readout and OHLC tooltip are all larger than the true price by 10^(quoteDec − 5) — 10 million times for XMR, a thousand for ZEC — while the number directly above them is correct. A user reading a limit off the chart is reading a price that does not exist. (Candle *volume* at stats.js:92 was fixed and uses qunit, so the volume pane is right while the price pane is not.)

**Root cause.** Same as D3-01: A.price's optional qdec defaults to DERO, and candles() was not updated when enrich() was.

**Fix.** app/stats.js:83, 88, 90 — pass `s.quoteDec` as the third argument, e.g. `const qd = s.quoteDec;` at the top of candles() and `price(p, dec, qd)` at all three sites.

**Regression test.** Assert candles(s, 60, from).at(-1).c === enrich(s).price for a synthetic market with quoteDec = 12 — the chart's last close must equal the header price by construction.

**Verifier.** app/stats.js:83 `let prevClose = price(pts[0], dec)`, :88 `prevClose = price(pts[i], dec)`, :90 `const p = pts[i], pr = price(p, dec)` — `dec` is set at :83 from `s.dec` and no third argument is ever passed. app/stats.js:92 volume does use qunit(s), so the volume pane really is correct while the price pane is not. In the same VM run the last candle close came back 20000 against a header price of 0.002 — exactly 10^(12-5). Chart consumers verified in app/chart.js: y-extent from `cs.flatMap(c => [c.h, c.l])`, axis tick labels via `S.fmtPriceSub(v)`, the last-price tag `S.fmtPriceSub(last.c)`, the crosshair `pt.textContent = S.fmtPriceSub(pv)` and the OHLC tooltip all read candle prices. app/token.js:60 renders the header from `s.price` (stats.js:57, computed with s.quoteDec) and token.js:46 carries the caption "Exact at block resolution".

---

### `DATA·D3-03` — Every sell made on this device is written to the trade ledger and then permanently erased by the next write

**P1 · data loss / P&L · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/feed.js:290 `validLocalTrade` requires `(t.side === 0 || t.side === 1)`. app/token.js:293 opens the sell sheet with `T.tradeSheet(s, 2)`, and app/app.js:213 records `side` verbatim: `D.addLocalTrade({ scid: s.scid, asset: s.asset, side, in: ..., out: ..., published: ..., dec: ... })`. pool.bas:12 and launch.bas define side 2 = sold; nothing maps 2 → 0. Replicated the validator verbatim: side 1 accepted, side 2 rejected. app/feed.js:286 `addLocalTrade(t) { const l = this.localTrades(); l.push(t); localStorage.setItem(...) }` — localTrades() filters through validLocalTrade, so each write rewrites storage without the sells already in it.

**Problem.** Local sells are not merely hidden on read; the next addLocalTrade deletes them from localStorage. Cost basis, realised P&L, unrealised P&L and the equity curve are computed from a ledger that contains only buys.

**Failure scenario.** Precondition: a wallet that trades without switching 'Post this trade' on (posting is off by default — token.js publish toggle). Steps: buy 1000 TOK, then sell all 1000. Impact: app.pnl (app.js:224) never applies the sell branch, so byAsset.units stays at 1000 and byAsset.cost stays at the full purchase price; the Portfolio still lists a position the wallet no longer holds, prints Realised 0 and prints an Unrealised figure against a cost basis that never fell. equityPoints (app.js:238) draws the same phantom holding forward for the whole curve. The Profile screen's 'Trade ledger: N trades kept for cost basis' (screens.js:222) under-counts by every sell. Published sells still arrive from the chain via D.tradesOf, so the damage is confined to — and total for — unpublished sells, which are the default.

**Root cause.** The validator was written against a 0/1 side encoding that the contracts never used; the contracts use 1/2. Every consumer (stats.published, home.feedItem, app.pnl) treats 'not 1' as a sell and so works, which is why the validator's disagreement is invisible.

**Fix.** app/feed.js:290 → `(t.side === 1 || t.side === 2)`. Existing ledgers cannot be recovered; the sells are gone.

**Regression test.** `validLocalTrade({...side: 2})` must be true and `{...side: 0}` false; plus a round-trip test that addLocalTrade(buy) then addLocalTrade(sell) then localTrades() returns two records.

**Verifier.** app/feed.js:290 `validLocalTrade` requires `(t.side === 0 || t.side === 1)`. app/token.js:125 and app/home.js:187 both open the sell sheet as `tradeSheet(s, 2)`; app/app.js:213 writes `side` verbatim. contracts/pool.bas:12 documents side 1 = bought, 2 = sold, and `post(2, ...)` is at pool.bas:234 and launch.bas:222/238. Nothing maps 2 to 0 anywhere. app/feed.js:286 `addLocalTrade(t) { const l = this.localTrades(); l.push(t); ... }` and feed.js:285 `localTrades()` filters through validLocalTrade, so every write rewrites storage from the filtered list — the sell survives only until the next addLocalTrade, and is invisible to every read in between. The 0/1 encoding is dead code: no caller ever passes side 0.

---

### `DATA·D3-04` — The launch wizard parses the graduation target in DERO's decimals whatever quote the creator picked, so a '100 XMR' launch installs a target of 0.00001 XMR

**P1 · correctness / units · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/forms.js:39-41 builds `quotes = [DERO].concat(D.vaults())` and lets the creator pick wXMR or wZEC. app/forms.js:56 labels the presets `g + " " + (quote.sym || "DERO")` and :106 labels the field `"Graduation target (" + (quote.sym || "DERO") + ")"`. app/forms.js:63 parses it: `grad = A.parseUnits(f.grad.value, 5n)` — the literal 5n, never `quote.dec`. app/forms.js:140 passes that value straight to InitializePrivate as `["grad", "U", grad]`.

**Problem.** With XMR selected (dec 12, config.js:12) typing 100 yields 10,000,000 atomic units = 0.00001 XMR, not 100 XMR — off by 10^7. The range check on the next line (forms.js:70, `grad < 1000000n || grad > 1000000000000n`) is DERO-scaled too, so the stated bound 'between 10 and 10 000 000 XMR' is really 0.000001 to 1 XMR, and no XMR launch above 1 XMR can be configured at all. contracts/launch.bas:25 carries the same two literals, so the contract enforces the DERO-scaled bound as well.

**Failure scenario.** Precondition: at least one vault in config (both ship). Steps: Launch a token → Raised in: XMR → Graduation target: 100 → Launch. Impact: the token graduates the moment the curve has raised 0.00001 XMR. graduation seeds the AMM with `real` (launch.bas:72) — a dust reserve — against 20% of supply, so the post-graduation price is 10^7 below what the creator was shown, and the market opens effectively empty. The creator signed two transactions and paid two fees for a market that is not the one the form described. docs/TODO.md §0 records this as 'DONE — launch wizard asks Raised in; label, presets, help and validation all name the chosen asset'; the label was converted and the parser was not.

**Root cause.** The quote-asset work changed every string that names the asset and none of the arithmetic that scales it. parseUnits' decimals argument is the one place the quote had to reach and it was hard-coded.

**Fix.** app/forms.js:63 → `A.parseUnits(f.grad.value, BigInt(quote.dec ?? 5))`; forms.js:70 → derive the bounds from that same decimal count and state them in the chosen asset; forms.js:88 → pass `quote.dec` to A.price and label with `quote.sym`, not the literal ' DERO'. The contract bound (launch.bas:25) needs the same treatment or a documented per-quote range.

**Regression test.** Drive the wizard with quote = XMR and grad = '100'; assert the argument sent to InitializePrivate is 100 * 10^12, and that the preview labels read XMR.

**Verifier.** app/forms.js:63 `grad = A.parseUnits(f.grad.value, 5n)` — the literal 5n, in the same readForm() that reads `quote` from the chip picker built at forms.js:40. forms.js:106 labels the field `"Graduation target (" + (quote.sym || "DERO") + ")"`, forms.js:58 labels the presets with quote.sym, forms.js:70 words the range error as "between 10 and 10 000 000 " + quote.sym, and forms.js:122 passes the value straight through as `["grad", "U", grad]`. The bound at forms.js:70 is `grad < 1000000n || grad > 1000000000000n`, and contracts/launch.bas:25 carries the identical two literals, so the contract enforces the DERO scale too: with XMR's 12 decimals the real range is 1e-6 to 1 XMR and typing 100 installs 1e-5 XMR. Graduation seeds the AMM from `real` (launch.bas:72 `STORE("rd", real + deff)`), so the post-graduation market opens with that dust as its whole quote reserve.

---

### `PERF·F1` — Quote-decimals fix is incomplete: every change window reads -100% and every candle is 10^7 off on a wrapped-quote market

**P1 · correctness · auditor confidence Confirmed · verifier *(unverified — id collision)***

**Evidence.** app/stats.js:16 — `priceAt()` ends `return price(pts[r < 0 ? 0 : r], s.dec);`, passing no third argument. app/stats.js:83, 88, 90 — `candles()` uses `const dec = s.dec` and calls `price(pts[0], dec)` / `price(pts[i], dec)` / `price(p, dec)`, also with no third argument. app/amm.js:99 — `const price = (s, dec, qdec) => (s.ra === Z ? 0 : Number(s.rd) / 10 ** (Number.isInteger(qdec) ? qdec : 5) / (Number(s.ra) / 10 ** Number(dec)))`: an omitted qdec silently means DERO's 5. app/stats.js:19 `change()` computes p1 WITH quoteDec and p0 WITHOUT it, then divides them. app/config.js:12-13 ships vaults with dec 12 (XMR) and dec 8 (ZEC), and app/feed.js:65 sets `s.quoteDec` from that record.

**Problem.** The recently-landed fix taught A.price about quote decimals and updated the two call sites inside enrich() (stats.js:19 p1, stats.js:57), but left priceAt() and candles() on the old DERO-only default. change() then divides a 12-decimal-scaled current price by a 5-decimal-scaled historical price, and the chart is drawn entirely in the wrong scale.

**Failure scenario.** Precondition: a pool whose `qt` is the Monero vault — exactly what `tools/simwrapmkt -quote <vault>` exists to create (its flag help reads "Set it to a vault to make a TOKEN/WRAPPED pair"). Steps: open that market's token page. Impact: reproduced in scratchpad/bug.mjs against the real stats.js — header price 0.0005344, priceAt() one hour ago 4906.36, so m.c5m/c1h/c6h/c24h all come out -99.99998% and render as "-100.00%" in every window, forever, on every screen (token header, Markets rows, Movers, gainers/losers filters). The same market's last candle close reads 5344.44 directly under a header price of 0.0005344. With the identical reserves and a DERO quote the same code returns +8.93% / +23.95%. Every market this product is aimed at — the ones a Monero or Zcash holder would trade — shows a permanent -100% and a chart that disagrees with the price above it by seven orders of magnitude.

**Root cause.** A.price's signature makes the dangerous case the default: omitting qdec silently asserts DERO. The fix had to find every call site by hand and missed two. app/ui.js:120 (sparkline) has the same omission but is cosmetically invisible because the sparkline normalises min/max.

**Fix.** stats.js:16 → `return price(pts[r < 0 ? 0 : r], s.dec, s.quoteDec);`. stats.js:83 → capture `const qd = s.quoteDec;` alongside `const dec = s.dec;` and pass `qd` at lines 83, 88 and 90. ui.js:120 → `A.price(p, dec, qdec)` with qdec threaded through sparkline's signature from its callers (home.js:50, markets.js:108).

**Regression test.** There is no JavaScript test harness in this repo at all (see F10), so add one: a Node assertion that builds a market with quoteDec 12 and a rising reserve series and asserts `S.enrich(s).c1h > 0` and `S.candles(s,...).at(-1).c === S.enrich(s).price` to within float epsilon. scratchpad/bug.mjs is already exactly this test and currently fails.

**Verifier.** app/forms.js:62 `grad = A.parseUnits(f.grad.value, 5n)` — literal 5n regardless of the selected quote. Line 71 checks `grad < 1000000n || grad > 1000000000000n`, bounds that only mean 10..10,000,000 at five decimals. Line 89 hard-codes ` DERO` on pv.start, pv.end and pv.mcap. Line 86 `A.price({rd: vd0, ra: vt0}, dec)` omits qdec (same fault as Q1). Line 58 builds the preset chips with `(quote.sym || "DERO")` and line 106/107 build the field label and note the same way — all three are constructed as arguments to openSheet at lines 95-113, before any chip click can change `quote`, and the chip handler at lines 45-53 only rewrites quoteNote.textContent and calls update(); update() (lines 77-93) never re-renders the chips, the field label or the note. So after picking XMR the label still reads 'Graduation target (DERO)' and the presets still read '100 DERO'. The quote IS passed on at line 122 `["quote", "H", quote.scid]`. With config.js vaults[0].dec = 12, typing 100 gives grad = 1e7 piconero = 0.00001 XMR, which passes the 1e6..1e12 bound. launch.bas is immutable. Every step verified.

---

### `PERF·F2` — The localStorage market cache has no size budget: it silently exceeds quota at ~40 markets, and every page load then pays a ~3,000-round-trip cold start

**P1 · performance · auditor confidence Confirmed · verifier *(unverified — id collision)***

**Evidence.** app/feed.js:134-142 `saveCache` — `try { localStorage.setItem(...) } catch (e) {}`; the JSON carries up to CACHE_P=1500 points and CACHE_T=600 trades per market (feed.js:11). app/feed.js:195 `flushCache()` writes every dirty market; app/feed.js:294 `setInterval(() => data.flushCache(), SAVE_EVERY)` at 30 s, plus a `pagehide` handler at feed.js:293. Nothing anywhere enumerates or evicts `veil.m.*` keys — the only removals are the per-record ones at feed.js:119 and feed.js:183, and the manual Settings button at screens.js:276.

**Problem.** Measured record size at the caps is 134,104 bytes per market (43,086 at 500 points / 200 trades). 60 markets = 7.53 MB, 120 markets = 15.35 MB, against a typical 5-10 MB per-origin localStorage quota. Past that point setItem throws for every market, the empty catch swallows it, and the cache silently stops working — with no eviction, no budget and no signal to the user or the developer. Records for delisted markets are never reclaimed, so the ceiling only ever rises.

**Failure scenario.** Precondition: a deployment with more than ~40 active markets that have real history. Steps: use the app for a session, then reload. Impact: loadContract() falls through to the uncached path, where a market at MAXP/MAXT needs 6,000 history keys = 24 chunked fetchKeys calls (CHUNK=250, feed.js:10) on top of the code fetch, the 45-key read and the getTx — roughly 3,240 JSON-RPC round trips for 120 markets, run 4 at a time (feed.js:230) = ~810 sequential rounds. On a remote wss node at 60 ms that is ~50 s of blank screen on every single load, and it never gets better because the cache can never be written again. The cache exists precisely to prevent this, and it disables itself exactly when it is most needed.

**Root cause.** Per-record caps (CACHE_P, CACHE_T) with no aggregate cap. The design budgeted one market and never the store.

**Fix.** Give the store a budget. In `flushCache`, sort dirty markets by usefulness (D.trending order, or last activity) and write until a running byte total reaches ~3 MB; skip the rest. Before writing, sweep `Object.keys(localStorage)` for `veil.m.` keys whose scid is not in `this.assets`/`this.nfts` and remove them. In `saveCache`, catch QuotaExceededError specifically, drop the oldest cached market, and retry once — the empty catch is the bug that makes this invisible.

**Regression test.** Node test over the serialisation shape: build N markets at the caps, sum JSON.stringify lengths, assert the total stays under a declared budget constant. Browser test: a Playwright pass (tools/uitest/ui.mjs already exists) that fills localStorage to quota, reloads, and asserts the app still renders and that `Object.keys(localStorage).filter(k=>k.startsWith('veil.m.')).length` is bounded.

**Verifier.** app/app.js:113 is quoted verbatim and correct: the vault arm of the allow-list is `(D.cfg.vault && opts.scid === D.cfg.vault)` only. app/config.js:11-15 ships vaults[] with XMR d69c93… and ZEC bfd444…, and `vault:` is the XMR one. app/feed.js:27-32 vaults() returns both; app/wrap.js:217-221 draws the picker at all.length>=2 (visible as the XMR|ZEC chip strip in .sim/ui-dec/02-wrap.png); app/wrap.js:426-447 doRegister/doBurn pass `scid: w.scid`. I checked the escape hatch: D.assets is populated only from `[...reg.launches, ...reg.pools]` keyed by the registry SCID (app/feed.js:227-234), and the ZEC *pool* row visible in the markets render is a separate pool contract whose `asset` is the vault SCID, not the vault SCID itself — so D.assets.has(bfd444…) is false. Every write path on the second vault is refused with "Refusing: contract is not a verified listing".

---

### `PERF·F6` — loadSocial does an unbounded whole-contract-tree read of the social contract on every page load

**P1 · performance · auditor confidence High · verifier *(unverified — id collision)***

**Evidence.** app/feed.js:249 — `const r = await V.node.getSC(this.cfg.social, { variables: true });`, then feed.js:251-260 iterate `Object.entries(r.strs)` and `Object.entries(r.vars)` to build three Maps. Called at app/app.js:283 on boot and app/app.js:59 on every wallet connect. In vendor/derohe/cmd/derod/rpc/rpc_dero_getsc.go the `p.Variables` branch opens a graviton cursor and walks EVERY key in the contract's tree with no limit and no pagination. app/rpc.js:143 then hex-decodes every string value into a second map.

**Problem.** This is the only unbounded read in the app — everything else (markets, chat, vault, founders) asks for exact keys. The social contract accumulates 3 keys per profile (pn_/pb_/pa_), one fr_ per followed account and one f_a_b per follow edge, and none are ever removed. The response size grows without limit with adoption, and the whole thing is fetched, JSON-parsed, hex-decoded and re-mapped on every page load.

**Failure scenario.** Precondition: the social contract has real usage. Steps: load the app. Impact: at 10,000 profiles with 20 follows each that is roughly 230,000 keys in one JSON-RPC response, walked twice on the daemon side and three times in the browser. This is the scaling wall the rest of feed.js was carefully written to avoid, and it sits on the boot path. I did not measure it — there is no populated social contract to measure against — but the shape is unambiguous from the daemon source.

**Root cause.** Follower counts and the follow set are only reachable by enumeration in the current key layout, so the app enumerates everything.

**Fix.** Read only what the current view needs, by exact key, the way myKeys() already does: `fc_<me>` for the connected wallet's follow count, then `f_<me>_<target>` for the specific addresses on screen, and `pn_/pb_/pa_<addr>` lazily per profile actually rendered. This deletes the `variables: true` call entirely.

**Regression test.** A Go DVM test (tests/ style) that registers 5,000 profiles and asserts the app's key-reading path issues a bounded number of keysstring reads; and a browser assertion that no DERO.GetSC call in a normal session carries `variables: true`.

**Verifier.** app/forms.js:63 is `grad = A.parseUnits(f.grad.value, 5n)` with a literal 5n; `quote.dec` is never read in readForm(). forms.js:38-44 builds quotes from `[{DERO,dec:5}].concat(D.vaults())` and config.js gives XMR dec:12, ZEC dec:8. forms.js:70 renders the range against `quote.sym`; forms.js:88 hard-codes " DERO" on start/end/mcap; forms.js:97 hard-codes the "DR" net mark; forms.js:113 says "the DERO raised". contracts/launch.bas DVM line 25 (file:15) bounds grad to 1e6..1e12 atomic units with no reference to the quote. This is the same defect as P7-01.

---

### `PROD·P7-01` — Every wrapped-quote launch is created with a graduation target 10^7 too small: the wizard parses it at DERO's 5 decimals, and launch.bas's own bound is DERO-shaped too

**P1 · launch-economics / units · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/forms.js:63 `const ... grad = A.parseUnits(f.grad.value, 5n);` — the literal 5n is used no matter which quote the creator picked. The quote objects carry the right decimals (app/forms.js:44 `const quotes = [{scid: ZEROQ, sym:"DERO", dec:5}].concat(D.vaults())`, and app/config.js gives the XMR vault `dec: 12`, ZEC `dec: 8`), and `quote.dec` is never read. The preset chips at app/forms.js:58 set `f.grad.value` to 10/50/100/500 and label them `g + " " + (quote.sym || "DERO")`, so the button literally reads "100 XMR". The value is passed straight through at app/forms.js:118 `["grad", "U", grad]`. contracts/launch.bas line 25 (`25 IF grad < 1000000 || grad > 1000000000000 THEN GOTO 900`, file line 15) bounds grad to 1e6..1e12 atomic units regardless of quote.

**Problem.** A creator who picks wXMR and clicks the "100 XMR" preset installs a curve whose graduation target is 100 x 10^5 = 10,000,000 atomic XMR units = 0.00001 XMR. The wizard's own validation message (app/forms.js:70) tells them the legal range is "between 10 and 10 000 000 XMR", which in XMR's twelve decimals is really 0.000001 to 1 XMR. Both the value and the stated bounds are wrong by 10^7. Correcting only the parser does not fix it: 10 XMR parsed correctly is 1e13, above the contract's 1e12 ceiling, so the install would revert — the contract constant is DERO-denominated too.

**Failure scenario.** Precondition: a wXMR vault is configured (app/config.js already ships two) and a creator uses the Launch sheet. Steps: pick wXMR as "Raised in", click the "100 XMR" chip, launch. The contract installs with G = 10,000,000 atomic = 0.00001 XMR. vd0 = G/3, and the whole curve (80% of supply after the creator cut) is bought out by the first buy of a few hundred-thousand atomic units — dust. Impact: the launch graduates instantly into its permanent AMM, whose entire quote reserve is 0.00001 XMR. The first bot to see the install owns ~80% of the supply for a fraction of a cent, and every subsequent buyer trades against a reserve so thin that any order moves the price arbitrarily. The creator believed they had set a 100 XMR raise. This is on the exact path the product exists for — a Monero holder trading against XMR.

**Root cause.** The quote-asset programme (docs/TODO.md section 0) made the contracts quote-agnostic and taught the display layer to name the quote, but the creation path still hard-codes DERO's five decimals in the one place that converts a human number into contract units. launch.bas's grad range was written when DERO was the only quote and was never re-derived per quote.

**Fix.** app/forms.js: parse with the chosen quote's decimals — `A.parseUnits(f.grad.value, BigInt(quote.dec))` — and derive the validation message's bounds from the same value rather than hard-coding "10 and 10 000 000". In contracts/launch.bas line 25, replace the absolute 1e6/1e12 pair with bounds expressed relative to the quote (pass a `qdec` or a `gmin`/`gmax` install parameter and check `grad` against those), because no single pair of integers is right for a 5-, 8- and 12-decimal quote at once. Until the contract changes, the wizard must refuse a wrapped quote rather than silently produce a 10^7-wrong curve.

**Regression test.** A DVM test in tests/ that installs launch.bas with quote = the wZEC vault and grad expressed as 1 ZEC (1e8), asserting the install succeeds and `G == 100000000`; plus a Playwright pass in tools/uitest/ui.mjs that selects the XMR quote, clicks the 100 preset, and asserts the args submitted to the wallet carry grad = 100 * 10^12.

**Verifier.** Same defect as F6, verified independently. app/forms.js:63 hard-codes 5n; forms.js:44 builds the quote list including D.vaults(); config.js gives XMR dec:12 and ZEC dec:8; quote.dec is read nowhere in readForm(). The preset chips (forms.js:58) really do label a raw number with quote.sym, so the button reads "100 XMR" and sets a value parsed at 10^5. contracts/launch.bas DVM line 25 (file:15) is `IF grad < 1000000 || grad > 1000000000000 THEN GOTO 900` with no quote awareness, so the second half of the finding is right too: correcting only the parser makes 10 XMR = 1e13 exceed the contract ceiling and the install reverts. The contract's real window for a 12-decimal quote is 0.000001 to 1 XMR.

---

### `PROD·P7-02` — On any wrapped-quote market every change percentage reads about -100% and the chart's prices are 10^7 times the header price, because priceAt() and candles() drop the quote-decimals argument that the recent A.price fix added

**P1 · correctness / display · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/amm.js:99 `const price = (s, dec, qdec) => (s.ra === Z ? 0 : Number(s.rd) / 10 ** (Number.isInteger(qdec) ? qdec : 5) / (Number(s.ra) / 10 ** Number(dec)));` — an omitted qdec silently means DERO's 5. app/stats.js:16 `return price(pts[r < 0 ? 0 : r], s.dec);` (priceAt, two arguments). app/stats.js:57 `const p = price(s, s.dec, s.quoteDec);` (enrich, three arguments). app/stats.js:20 `change()` compares those two. app/stats.js:83, 88, 90 (candles) all call `price(pts[i], dec)` with two arguments. app/ui.js:120 (sparkline) likewise.

**Problem.** Within one function, `change()` divides the current price by 10^12 for an XMR-quoted market and the historical price by 10^5. p0 comes out 10^7 times p1, so ((p1-p0)/p0)*100 is about -99.99999 whatever the market actually did. The same omission in candles() means the token page renders its big price from the corrected call and its chart axis, last-price tag and OHLC tooltip from the uncorrected one, showing two prices for the same market that differ by a factor of ten million.

**Failure scenario.** Precondition: a TOKEN/wXMR market exists (docs/TODO.md section 0 records a live BRW/SIM market and the quote parameter shipped on 2026-09-08). Steps: open Markets or the token page. Impact: c5m/c1h/c6h/c24h all show about -100.00% (app/token.js drawHead renders all four); `D.movers()` in app/feed.js:268 sorts by |c24h| so every wrapped market is pinned to the top of Home's "Movers" with a fabricated collapse; markets.js's "Losers" filter catches all of them; app/live.js:58 weights the ambient mood by c24h so the whole UI goes red. A user reads a total wipe-out on a market that has not moved, and the chart beside it disagrees with the headline price by 10^7.

**Root cause.** The A.price fix put the units knowledge in one place and documented that an omitted qdec still means DERO (app/amm.js:93-98), but three of its five call sites were left at the old two-argument form. docs/TODO.md 1.3 names stats.js as still open, but describes the symptom as cross-quote sorting, not as a wrong number on the screen.

**Fix.** Give priceAt a market-aware signature and pass s.quoteDec: `return price(pts[...], s.dec, s.quoteDec);` (app/stats.js:16). Thread the same value through candles() — it already receives `s` — at app/stats.js:83, 88, 90. app/ui.js:120's sparkline is shape-only so it is cosmetically safe, but pass it anyway for consistency. Better still, make the third argument mandatory in A.price and delete the `: 5` default, so a missing quote is a crash in test rather than a wrong price in production.

**Regression test.** Unit test over a synthetic market with quoteDec 12 and two price points at identical reserves, asserting `S.change(s, 3600) === 0` and `S.candles(s, 60, t0).at(-1).c === s.m.price`. Both fail today.

**Verifier.** The arithmetic is exactly as stated and I have rendered proof. app/amm.js:99 defaults an omitted qdec to 5. stats.js:19 change() takes p1 from `price(s, s.dec, s.quoteDec)` and p0 from priceAt (stats.js:16) which calls `price(pts[r], s.dec)` — two arguments. For a 12-decimal quote p0 = 10^7 × p1, so ((p1−p0)/p0)×100 = −99.99999 whatever the market did. candles() at stats.js:83, 88 and 90 has the same two-argument omission, so the chart axis and the header price differ by 10^7. docs/TODO.md:119-121 confirms a live BRW/XMR pool exists on the simulator at 12 decimals, and .sim/ui-dec/02-markets.png row 22 renders "BRW / XMR ⚠ pool … −100.00%" against every neighbouring row's ordinary percentage. feed.js:268 movers() sorts on |c24h| and markets.js:10 has a "Losers" filter, so the downstream effects are real.

---

### `PROD·P7-03` — The in-app plaque promises "All platform fees are distributed amongst the NFT holders" and "all rights revoked"; the shipped defaults distribute 50%, wrapped-asset fees can reach holders through no path at all, and every payout requires a live treasury key

**P1 · trust / false promise · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/notice.js:16 `"All platform fees are distributed amongst the NFT holders."`, and the facts table below it, app/notice.js:39-40, `U.kv("Owner", "none after deployment")` / `U.kv("Fees", "distributed to Founders Pass holders")`, closing with "If any other screen in this app appears to contradict it, this page is the one that is true." Against that: tools/simdeploy/main.go:50 `passShare = flag.Uint64("pass-share", 5000, ...)` — the shipped default is 50%, and app/nfts.js:31 correctly renders `N(f.share)/100 + "% of every protocol fee"`. contracts/founders.bas `Deposit()` reads `LET d = DEROVALUE()` and nothing else, so no non-DERO fee can ever enter it. contracts/launch.bas `ClaimFees` sends `fd` in the market's quote asset `q` and `fa` in SCID to the treasury address. tools/treasurybot/main.go:130-146 skips every market whose `qt` is not the zero hash, printing "owed but not sweepable". The deposit half needs the treasury wallet's key online forever (tools/treasurybot header comment; RISKS.md R-13), while docs/HANDOVER.md:9 states "The operator intends to revoke their own access after deployment."

**Problem.** Three separate falsehoods on the one page the app designates as authoritative. (1) "All" is 50% at the shipped default. (2) For any market quoted in wXMR or wZEC — the markets the product exists to create — the fee accrues in the wrapped asset and there is no code path from there to a holder; founders.bas counts one DERO number. (3) "Owner: none after deployment" and "all rights revoked" cannot both be true and have anyone running treasurybot. If the key is really destroyed, every protocol fee ever collected lands at a dead address and holders receive nothing forever, while the NFTs screen keeps rendering the promise next to a live counter.

**Failure scenario.** Precondition: the pass is minted and holders register. Steps: markets trade; ClaimFees (permissionless) moves fees to the treasury address. Impact: DERO-market fees deposit at half the advertised rate only while an operator runs the bot; wrapped-market fees accumulate at the treasury as wXMR/wZEC with no route to holders at all; if the operator honours the revocation promise, the whole revenue stream stops permanently. A buyer of a Founders Pass at 5 DERO (tools/simdeploy/main.go:45) paid for a cash flow whose delivery depends on a party the same app says does not exist.

**Root cause.** The plaque was written as a manifesto and the accounting was built afterwards. RISKS.md R-13 and R-14 and docs/TODO.md 1.1 all state the truth honestly; the app's own copy was never reconciled with them, and notice.js asserts primacy over the screens that are correct.

**Fix.** Rewrite app/notice.js:16 to the shape app/nfts.js:31 already uses — the share read from the contract, not "all" — and change the `Owner`/`Fees` rows to name the treasury as a trusted party that must run tools/treasurybot, with a line saying fees earned in a wrapped asset currently reach no holder. Then either give founders.bas per-asset accounting (a `dep_<scid>`/`acc_<scid>` family and an asset-aware Deposit/settle) or add a claim-per-asset entrypoint, per docs/TODO.md 1.1. Do not ship a mainnet pass while the promise on the plaque is wider than the code.

**Regression test.** A copy test that asserts no user-facing string contains an unconditional 'all fees' claim, plus a Go test asserting `pass.share == 10000` whenever notice.js says 'All'. And a DVM test sending a wrapped asset to founders.bas Deposit, asserting it reverts — so the gap is visible in CI rather than in prose.

**Verifier.** All three falsehoods verified. (1) app/notice.js:16 "All platform fees are distributed amongst the NFT holders." against tools/simdeploy/main.go:50 `passShare = flag.Uint64("pass-share", 5000, …)`, and deploy/README.md's own mainnet command line passes `-pass-share 5000`; nfts.js:31 correctly renders the real figure. (2) contracts/founders.bas Deposit (file:137-146) reads `LET d = DEROVALUE()` and nothing else — I grepped for ASSETVALUE in the file and every hit is ASSETVALUE(SCID()), the pass NFT itself, never a wrapped asset — so no non-DERO fee can enter; contracts/launch.bas ClaimFees (file:245-259) sends fd in the market's quote `q`; tools/treasurybot/main.go:130-146 skips those markets by name, printing "owed but not sweepable". (3) notice.js:39 "Owner: none after deployment" and BODY line 15 "All rights and permissions have been revoked from its creators" against docs/HANDOVER.md §0.0 ("The operator intends to revoke their own access after deployment") and a deposit path that needs the treasury wallet's key online forever. docs/TODO.md 1.1 states the second point honestly in the team's own words: "holders cannot yet receive those fees at all… this breaks that promise silently, which is worse than breaking it loudly."

---

### `PROD·P7-04` — The factory registry is permissionless, deduplication-free, unprunable and owned by nobody after deployment: ~0.1 DERO per entry buys a permanent per-block RPC amplification on every client

**P1 · sybil / availability · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** contracts/factory.bas `reg(scid)` checks only `STRLEN(scid) != 32` and `DEROVALUE() < LOAD("regfee")` — it never checks the SCID exists, is a contract, or has not already been registered. `RegisterLaunch`/`RegisterPool`/`RegisterNFT` append to a monotonic list; there is no delete, no cap, and no cleanup entrypoint anywhere in the file. `SetRegFee` is `SIGNER() != LOAD("owner")`-gated and docs/HANDOVER.md:9 says the owner revokes access. tools/simdeploy/main.go:30 `regfee = flag.Uint64("regfee", 10000, ...)` = 0.1 DERO. On the client, app/feed.js `registry_()` builds one key per listing and `refresh()` does `const ids = [...reg.launches, ...reg.pools]; await pmap(ids, 4, ...)` with no dedup — a repeated SCID is probed once per occurrence. app/app.js:74 `L.onBlock(() => app.scheduleRefresh(200))` and app/app.js:72's 45 s safety net drive that loop every block.

**Problem.** Registration is a paid hint with the price fixed forever at install and no way to remove an entry. Registering one legitimate SCID N times costs N x regfee and makes every client do N `probe()` calls (each a getSC over eighteen keys) every ~18 seconds, for the life of the deployment. On the first load of a session each entry also costs a `verify()` that downloads the contract's full code. Nothing in the app bounds the registry length, paginates it, or dedups it.

**Failure scenario.** Precondition: mainnet deployment, owner key destroyed as planned. Steps: an attacker calls RegisterLaunch 10,000 times with the same valid SCID (or with the zero hash — `reg` does not care), paying 10,000 x 0.1 = 1,000 DERO plus gas. Impact: every user's client issues ~10,000 node round trips per block forever; the app becomes unusable and the node operators serving it get hammered. There is no owner to raise regfee, no entrypoint to prune, and the contract is immutable — the only remedy is shipping a new factory SCID in config.js, which orphans every genuine listing registered against the old one.

**Root cause.** The registry was designed for the honest case (RISKS.md R-12 models growth as 'one key per listing' from organic listings) and the fee was sized as a nuisance cost, not as an adversarial price. The immutability and owner-revocation rules that make the rest of the system trustworthy also remove every lever that would normally fix this.

**Fix.** Client-side, immediately and cheaply: dedup `ids` in app/feed.js `refresh()` (`[...new Set([...reg.launches, ...reg.pools])]`), cap how many entries a session will load, and load newest-first so a spam flood cannot bury real markets. Contract-side, before mainnet: make `reg()` reject an already-registered SCID (`IF EXISTS("r_" + HEX(scid)) != 0 THEN GOTO 900` plus a `STORE`), and make regfee a function of the current list length rather than a constant an absent owner can no longer change.

**Regression test.** A DVM test registering the same SCID twice and asserting the second call returns 1. A JS test that hands `refresh()` a registry with 500 duplicate ids and asserts `V.node.getSC` is called once per distinct id.

**Verifier.** contracts/factory.bas reg() (file:90-97) checks only STRLEN(scid) != 32 and DEROVALUE() < regfee — it never checks the SCID exists, is a contract, or is already listed. RegisterPool/RegisterLaunch/RegisterNFT append to monotonic counters; there is no delete, no cap and no prune entrypoint in the file. SetRegFee (file:38) is owner-gated and the owner is to be discarded. tools/simdeploy/main.go:30 sets regfee to 10000 atomic = 0.1 DERO. On the client, feed.js:227-228 builds `ids = [...reg.launches, ...reg.pools]` with no dedup and pmaps a probe() over every occurrence; registry_() (feed.js:75-84) fetches one key per listing with no paging bound; app.js:74 drives it every block and app.js:72 every 45 s as a safety net.

---

### `PROTO·BC-2` — Vault: Veto works while halted but Released does not, so the Paying interlock lapses and a withdrawal already paid abroad can be refunded on chain

**P1 · protocol correctness / reserve invariant · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\vault.bas:
- Released, line 398 (DVM line 21): `IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("halted") != 0 THEN GOTO 900` -- refuses while halted.
- Veto, line 443 (DVM line 21): `IF issigner(s) == 0 || DEROVALUE() != 0 THEN GOTO 900` -- no halted check.
- Paying, line 432 (DVM line 24): `IF EXISTS("w_" + j + "_p") != 0 THEN GOTO 100` -- the mark is one-shot and can never be refreshed.
- Veto, line 447 (DVM line 25): `IF BLOCK_HEIGHT() < LOAD("w_" + j + "_p") + 2000 THEN GOTO 900` -- the mark protects for 2000 blocks only.
Released is the ONLY thing that closes the mark permanently, and it is exactly what the halt blocks.
The daemon runs the veto path during a halt on purpose: C:\Users\Wilma\Desktop\fomo\tools\vaultsigner\main.go:671 `vetoPass(...) // returning funds is the one thing a halted vault must still do`.
PROVEN on the real DVM: 2-of-3 sealed vault, sup=2000, Burn 500, sg[0] Paying(0), signers Halt(1), Released refused twice while halted, height advanced to w_0_p+2000, two Veto votes -> w_0_st=2 and 500 units returned to the burner with sup unchanged at 2000.
vault.bas line 424 claims a slashed-after-marking signer "still costs the withdrawal that one window, which is bounded delay rather than lost money". Under a halt longer than 2000 blocks that is false: the delay converts into lost money.

**Problem.** The reserve >= sup - inflight invariant is broken by design once a halt outlasts the 2000-block window. The federation pays the foreign coins out, cannot record it, the mark expires, and a quorum of signers who have no local record of that payment (only the one paying signer keeps an Intent file -- tools/vaultsigner/main.go:324) can return the wrapped units to the burner. The vault has then paid twice for one claim, which is precisely the hole the Paying mark was added to close.

**Failure scenario.** Precondition: a sealed vault; withdrawal j burned and open (w_j_st == 0).
1. Signer A marks Paying(j) and sends the XMR/ZEC abroad; it confirms. The coins are gone.
2. Before A's Released quorum forms, the reserve check trips for an unrelated reason (a genuine shortfall, a backend outage misreporting, a slashed signer) and k signers vote Halt(1). tools/vaultsigner/main.go:660-672 does this automatically.
3. Released is now refused for every signer, so w_j_st stays 0 and A's Intent stays stuck in state "paid".
4. 2000 blocks (~10 h at DERO's 18 s blocks) pass. The mark cannot be refreshed.
5. The other signers -- whose backends disagree about the destination, or who are simply following vetoPass -- reach k on Veto(j). The burner gets their units back on top of the coins they already received.
6. Aftermath: w_j_st becomes 2, so inflightUnits (tools/vaultsigner/main.go:310-319, which counts only st==0) stops counting j; owed = sup - inflight jumps by w_j_v; reserve < owed is now permanently true; the daemon halts forever; Burn is blocked while halted (vault.bas line 368) so no holder can ever exit. The double-pay and BC-3 lock together into a terminal state.

**Root cause.** The halt was applied to "anything that moves value" rather than to "anything that creates new exposure". Released does not create exposure -- it only records that money which has already left the reserve should stop being counted in sup -- so blocking it removes the vault's only way to close a payment while leaving open the path that reverses one. The asymmetry was invisible because the two guards live in different functions and the Paying interlock was tested only in the unhalted case (tests/vault_paying_test.go covers both halves of the mark but never sets halted).

**Fix.** Remove `|| LOAD("halted") != 0` from Released's guard at contracts/vault.bas line 398. A halt should stop Register, Attest, Mint and Burn -- the operations that add exposure -- and must not stop the truthful recording of a payment already made. This also dissolves the secondary trap the daemon works around at tools/vaultsigner/main.go:648-653 ("because vault.bas Released refuses while halted, sup can then never fall and the halt can never clear"). If Released must stay blocked for some reason not visible in the source, then the minimal alternative is to make Veto refuse whenever `EXISTS("w_"+j+"_p") != 0 && LOAD("halted") != 0`, so the mark cannot lapse during exactly the window in which it cannot be discharged.

**Regression test.** The test I wrote: sealed 2-of-3 vault, mint 2000, burn 500, Paying(0) by sg[0], Halt(1) by k signers, assert Released is refused, advance to w_0_p+2000, cast k Veto votes and assert the veto is REFUSED and w_0_st stays 0. It currently fails with w_0_st=2 and 500 units returned. Pair it with a second case asserting Released succeeds while halted once the fix lands.

**Verifier.** Every citation checks out against contracts/vault.bas at the file lines given: Released 398 refuses while halted, Veto 443 has no halted check, Paying 432 is one-shot, Veto 447 expires the mark at 2000 blocks. Reproduced on the DVM: sealed 2-of-3, sup = 2000, Burn 500, sg[0] Paying(0), both signers Halt(1); Released was then refused for both signers with w_0_st still 0; a second Paying at +100 blocks left w_0_p unchanged; at w_0_p + 2001 two Veto votes succeeded while halted = 1, giving w_0_st = 2 with sup unchanged at 2000. So the reserve invariant is broken by exactly the withdrawal amount, permanently, and the aftermath the finding describes follows from inflightUnits (tools/vaultsigner/main.go lines 310-319, counting only st == 0) and the auto-halt at line 668.

---

### `PROTO·BC-3` — A vault halt has no expiry and blocks Burn, so k signers who never vote Halt(0) freeze every holder out of the only exit permanently

**P1 · liveness / stuck state · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\vault.bas Burn line 368 (DVM line 22): `IF LOAD("halted") != 0 || LOAD("sealed") == 0 THEN GOTO 900`. The only writer of "halted" is Halt (line 484), which needs k votes inside one 2000-block bucket. There is no height-based expiry anywhere in the file: grepping "halted" finds it only in Register:257, Attest:281, Mint:306, Burn:368, Released:398 and Halt itself.
PROVEN: sealed 2-of-3 vault, sup=2000, k signers vote Halt(1), height advanced by 2,000,000 blocks (~1 year of DERO blocks); Burn still returns "Discarded knowingly".
This contradicts the file's own header, vault.bas lines 4-5: "LIVENESS BY CONSTRUCTION: after Seal no privileged party exists or is needed. Every limit empties by itself or expires into a payment; doing nothing pays the user." While halted, doing nothing pays nobody and nothing expires.

**Problem.** The project rule is that any state needing a human to leave it is a defect, because the operator revokes their own access after deployment. `halted` is exactly such a state, and it is the one that gates the exit. Losing k signer keys, a permanent disagreement, or the aftermath of BC-2 all leave every wrapped-unit holder unable to redeem, with no timeout and nobody to appeal to.

**Failure scenario.** Precondition: a sealed vault with wrapped units in circulation.
1. The reserve check trips and k signers vote Halt(1) (automatic -- tools/vaultsigner/main.go:660).
2. The condition that caused it does not clear: a backend is permanently gone, a signer key is lost so k can no longer be reached for the resume vote, or BC-2 has just fired and made reserve < owed structurally true.
3. Burn is refused from that block onward, for ever. Mint and Register are too, so no new money enters, but the money already in cannot leave either.
4. Every holder of wrapped XMR/ZEC keeps a token that can be traded on the AMM pools but never redeemed. Price discovery on those pools continues against a claim that has no exit.

**Root cause.** Halt was modelled as a switch rather than as a lease. Burn was included in the halted set although it is the one operation that cannot lose the vault money -- it only moves units INTO the contract and creates a claim record; nothing is handed out until a separate k-of-n Released. Blocking it converts a safety pause into a confiscation.

**Fix.** Two changes in contracts/vault.bas. (a) Drop the `LOAD("halted") != 0` term from Burn's guard at line 368: burning during a halt is safe by construction, and the resulting withdrawals simply queue behind the wdelay. (b) Give the halt a lease: store the height at which it was set and require Halt(1) to be re-voted to extend it, so `halted` decays to 0 on its own if the federation stops re-affirming it. That satisfies the header's own liveness claim rather than contradicting it.

**Regression test.** TestAUDITHaltFreezesExitsForever, as written: sealed vault, mint, k Halt(1) votes, advance 2,000,000 blocks, assert Burn SUCCEEDS. It currently passes only because it asserts the failure; invert it after the fix.

**Verifier.** contracts/vault.bas Burn line 368 refuses while halted. Grepping "halted" over the file finds it only at InitializePrivate 62, Register 257, Attest 281, Mint 306, Burn 368, Released 398, Halt 477/478/484 — no height-based expiry anywhere, and Halt (473-488) needs k votes inside one 2000-block bucket to flip either way. Reproduced: sealed 2-of-3, k signers vote Halt(1), height advanced by 2,000,000 blocks (about a year of 18 s blocks), and Burn still returned 'Discarded knowingly' with halted = 1. This directly contradicts the file's own header at lines 4-5 ('Every limit empties by itself or expires into a payment; doing nothing pays the user'), and it is the clearest instance in the repo of the standing project rule that any state needing a human to leave it is a defect — here the human is a quorum that may no longer exist. The reasoning that Burn is the one operation that cannot lose the vault money is sound: it only moves units into the contract and creates a claim record; nothing is paid out until a separate k-of-n Released.

---

### `PROTO·BC-4` — The federation bond deters nothing for users: Seal permits n = k+1 (a colluding quorum that can never be slashed), and a slashed bond goes to the treasury, not to the victims

**P1 · trust model / economic design · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\vault.bas:
- Seal line 236 (DVM line 23): `IF n < LOAD("k") + 1 THEN GOTO 900` -- n = k+1 seals fine. The n >= 2k rule is prose only: contracts/README.md line ~237 "n >= 2k is a deployment rule, not a check".
- Slash line 518 (DVM line 25): `IF na < LOAD("k") + 1 THEN GOTO 900`, and Slash itself needs k votes. With n = k+1 a colluding k cannot be outvoted by the single remaining signer, so the quorum is unslashable by construction.
- Slash line 532 (DVM line 49): `SEND_DERO_TO_ADDRESS(LOAD("treasury"), b)` -- the forfeited bond goes to the protocol treasury. No burner or wrapped-unit holder can claim any part of it; there is no victim-compensation path anywhere in the file.
- The bond ceiling is the only quantitative limit: `cap / capratio > bondtot` (Seal line 243, SetCap line 497, Mint line 318). capratio is a fixed install parameter with no oracle -- contracts/README.md line ~243: "cap is in foreign base units and bondtot in DERO atomic units, so capratio is the operator's written-down price assumption ... there is no oracle".
What k colluding signers can take, read off the code: the entire foreign reserve (it lives off chain in their own k-of-n multisig; nothing in vault.bas touches it), PLUS up to cap - sup in freshly minted wrapped units -- Register is permissionless (line 252) so they register their own indices, Attest and Mint are pure k-of-n votes with no external proof, and Mint's window limit (line 328) admits one unbounded deposit per 2000-block bucket by its own documented design.

**Problem.** The wrapper is described everywhere as "bonded", and the bond is the whole of the economic story. But the bond is (a) unreachable in the exact scenario it exists for whenever n < 2k, (b) payable to the treasury rather than to the people who lost money, and (c) sized against a hardcoded price assumption that drifts with DERO/XMR. So there is no configuration in which a user's expected recovery from signer collusion is non-zero.

**Failure scenario.** Precondition: a vault deployed 2-of-3 or any n = k+1 (both are permitted by Seal, and contracts/README.md explicitly blesses 2-of-3 as "a reasonable small-vault choice").
1. Two of the three signers collude. They cannot be slashed: Slash needs k=2 votes and the honest signer is alone.
2. They Register indices they control, Attest addresses they control, and Mint up to cap against fabricated deposits -- one unbounded mint per 2000-block bucket once wm+pm < winmax.
3. They sell the minted wrapped units into the pool.bas wXMR/wZEC markets for DERO and real assets.
4. They also take the off-chain foreign reserve, which the contract cannot see or constrain.
5. Users hold wrapped units backed by nothing. Even in the n >= 2k case where a slash IS possible, the bond lands in the treasury and no user is made whole.

**Root cause.** Two separable design gaps. The quorum-capture one is a deliberate trade recorded in contracts/README.md ("enforcing it would forbid 2-of-3") but the code offers no way to signal to a user which kind of vault they are in -- there is no on-chain flag an app could read to say "this federation cannot slash its own quorum". The compensation gap looks unexamined: Slash's destination was chosen when the bond was framed as a penalty rather than as collateral.

**Fix.** In contracts/vault.bas: (a) enforce `n >= 2 * k` in Seal, or -- if 2-of-3 must stay legal -- store the choice on chain at Seal (e.g. STORE("slashable", 1) only when n >= 2k) so app/wrap.js can label the vault honestly rather than the rule living only in a runbook. (b) Route the slashed bond to a claim pool inside the vault that burners can draw from pro rata against their unpaid claims, rather than SEND_DERO_TO_ADDRESS(treasury). The treasury is not the injured party.

**Regression test.** A test asserting Seal REFUSES n = k+1 (currently it accepts: tests/vault_v3_test.go covers k-of-k refusal but not k+1). Plus a test that after Slash the forfeited bond is claimable by an open withdrawal's burner rather than sent to the treasury address.

**Verifier.** All three sub-claims verified in contracts/vault.bas. Seal line 236 is `23 IF n < LOAD("k") + 1 THEN GOTO 900`, so n = k+1 seals. Slash line 518 is `25 IF na < LOAD("k") + 1 THEN GOTO 900` and Slash itself is gated on votek("s",...) at line 520, so with n = k+1 a colluding k cannot be outvoted by the single honest signer — the quorum is unslashable by construction. Slash line 532 is `49 SEND_DERO_TO_ADDRESS(LOAD("treasury"), b)`, and there is no victim-compensation path in the file. capratio is written once in InitializePrivate (line 66) and never again; contracts/README.md lines 243-247 confirm it is 'the operator's written-down price assumption' with 'no oracle'. contracts/README.md lines 237-239 confirm 'n >= 2k is a deployment rule, not a check' and that 2-of-3 is explicitly blessed. Mint's window limit at line 328 (`IF LOAD("winmax") != 0 && wm + pm >= LOAD("winmax")`) blocks only a full window, so one unbounded mint per 2000-block bucket is admitted by design, as the comment at lines 299-300 says.

---

### `REQ·A8-01` — The app's designated final word makes three claims the shipped deployment cannot deliver — and the UI test locks one of them in

**P1 · contradictory-requirement / truthfulness · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/notice.js:15 ("All rights and permissions have been revoked from its creators"), :16 ("All platform fees are distributed amongst the NFT holders"), :17 ("Code is law. This code is final. There can be no further changes"), :38 (kv "Owner" → "none after deployment"), :41 (kv "Fees" → "distributed to Founders Pass holders"), and :44-47 ("Nothing on this page can be switched off, because there is nobody left holding a switch… If any other screen in this app appears to contradict it, this page is the one that is true"). Against that: contracts/factory.bas:27 SetTreasury and :39 SetRegFee are gated only on LOAD("owner"), and the only ownership entrypoints are :45 TransferOwnership / :55 ClaimOwnership — a two-step handover in which the recipient must sign, so there is no renounce path and no way to set the owner to nobody. docs/ARCHITECTURE.md:24 records the TELA INDEX as "updatable, commit chain"; deploy/README.md "Rollback" instructs a broken app release to be fixed by another `telainstall -index`. Fees: contracts/pool.bas:161 accrues only `pf = fee × pfee/10000` (pfeeBps 5000 in app/config.js, i.e. half the 50 bps trade fee) to `fd`, contracts/pool.bas:241-253 ClaimFees pays both `fd` and the asset-leg `fa` to the treasury address, and tools/treasurybot/main.go:166 deposits only `share` bps (5000) of the claimed DERO into the pass — with foreign-quoted markets skipped entirely at main.go:133-136. tools/uitest/ui.mjs:106 asserts /revoked/i must be present on the page.

**Problem.** The one screen the app nominates as authoritative over all other copy states as settled fact three things that are false or unachievable as the system is built and as the runbook deploys it: (a) there is a permanent factory owner with no renounce entrypoint; (b) the on-chain app is updatable by the INDEX key and the runbook depends on that; (c) holders receive roughly a quarter of a DERO-quoted trade's fee, none of the asset leg, and nothing at all from wXMR/wZEC-quoted markets. The regression gate enforces the presence of the word "revoked", so the untrue claim is now a test requirement.

**Failure scenario.** Precondition: a mainnet deployment following deploy/README.md. Step 1: a user reads #notice and concludes nobody holds any key and the code cannot change. Step 2: the operator (who still holds the factory owner key — no step ever disposes of it) calls SetTreasury, or re-installs the app with `telainstall -index`. Step 3: the user's belief was wrong in a way the app told them was the one thing that was true. Impact: the product's single differentiating claim — systematic honesty about what can and cannot happen — is falsified on its own plaque; a user makes custody decisions on it.

**Root cause.** The notice copy was written as a statement of intent about a deployment that has not happened, rather than derived from what the contracts and the runbook can actually enforce. No requirement said 'every categorical claim on the notice page must name a mechanism that makes it true', so nothing checked it.

**Fix.** Rewrite the three claims to what the mechanisms deliver, and give each a mechanism: (1) 'Owner' → name what the factory owner can still do (treasury pointer, registration fee) and what disposing of that key would mean, or add a renounce path; (2) 'This code is final' applies to the contracts — say the on-chain app is an updatable INDEX and name who can update it, or state that the INDEX key was destroyed and how to check; (3) state the fee path precisely: the protocol's share of the DERO leg, `share` bps of it, DERO-quoted markets only, deposited by the treasury. Then change tools/uitest/ui.mjs:106 to assert the corrected statements.

**Regression test.** Extend tools/uitest/ui.mjs's notice block to assert the fee sentence names both the share and the DERO-only limitation, and add a contract-level test asserting factory.bas exposes no entrypoint that can clear `owner` (so the copy and the contract cannot drift apart silently).

**Verifier.** Read app/notice.js in full: the BODY array carries 'All rights and permissions have been revoked from its creators', 'All platform fees are distributed amongst the NFT holders' and 'Code is law. This code is final. There can be no further changes.'; the facts block renders kv('Owner','none after deployment') and kv('Fees','distributed to Founders Pass holders'); the closing note is 'Nothing on this page can be switched off, because there is nobody left holding a switch... this page is the one that is true.' Against that: contracts/factory.bas SetTreasury and SetRegFee are gated only on `SIGNER() != LOAD("owner")`, and the only ownership entrypoints are TransferOwnership (stores tmpowner) and ClaimOwnership (requires the recipient to sign, then STORE("owner", SIGNER())) — there is no renounce and no way to leave owner unset. deploy/README.md 'Rollback' says a broken app release is fixed by another `telainstall -index`. Fees: contracts/pool.bas:120 `pf = bps(fee, pfee)` accrues half the 50 bps to fd; ClaimFees (238-253) pays fd AND the asset leg fa to the treasury with no path into the pass; tools/treasurybot/main.go:131-136 skips any market whose quote is not DERO ('owed but not sweepable') and deposits only `share` bps into the pass. tools/uitest/ui.mjs:106 does require /revoked/i, so the untrue claim is a test requirement.

---

### `REQ·A8-03` — The mainnet runbook cannot produce a working vault: it never says how to choose the per-chain parameters, and none of them can be changed after Seal

**P1 · operational-requirement-missing · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** deploy/README.md (Mainnet → Contracts) gives only the call shape: `InitializePrivate(k, treasury, cap, minbond, minburn, wfee, mindep, regfee, capratio, bigbps, bigdelay)` … `Seal()` — 'Sealing is the point of no return'. contracts/vault.bas:41 takes all eleven; :65-74 stores `cap`, `capratio`, `minburn`, `mindep`; grep of vault.bas shows setters for `cap` only (`SetCap`, :491, k votes) and for `winmax`/`wdelay` (`SetLimits`, :89, pre-Seal). There is no setter for `capratio`, `mindep`, `minburn` or `wfee`. The bound checked at :243, :318, :497 is `cap / capratio > bondtot` → refuse; contracts/README.md:243-247 states plainly that `cap` is in foreign base units while `bondtot` is DERO atomic units, so `capratio` is a written-down price assumption. The mainnet go/no-go gate table checks only that `cap / capratio ≤ Σ bonds` — which any wrong capratio satisfies. docs/TODO.md §0d and §2.4 both record that the runbook must state these per foreign chain and does not.

**Problem.** Every economically meaningful vault parameter is in the foreign chain's base units (piconero 1e12, zatoshi 1e8) while the simulator's worked example and the runbook's flags are five-decimal DERO values, and four of them are immutable after install. An operator following the runbook literally deploys a vault whose maximum wrappable supply is a rounding error, whose minimum deposit is dust, and which can never be corrected — Seal is irreversible and there is no owner afterwards by design.

**Failure scenario.** Precondition: the federation go/no-go gates pass; the operator installs the mainnet vault with the parameter values carried over from the simulator (the only worked example in the repo). Step 1: capratio is 3 and bonds total, say, 6 DERO = 600 000 atomic units, so `cap` can never exceed 1 800 000 piconero ≈ 1.8e-6 XMR. Step 2: `mindep` and `minburn`, copied from a five-decimal example, are below the Monero network fee. Step 3: Seal is called; the owner's power ends. Impact: a vault that cannot hold a usable amount of Monero, cannot be re-parameterised by anyone, and whose only remedy is to deploy a new vault and migrate — with the old one's bonds and any deposits stranded inside it.

**Root cause.** The parameter semantics are documented in contracts/README.md (a contract reference) but the deployment runbook — the document an operator actually executes, after which they revoke their own access — was never updated with per-chain values. The decimals bug fixed in the app on 2026-09-08 was never propagated to the deployment path.

**Fix.** Add a 'Vault parameters per foreign chain' table to deploy/README.md (Mainnet) giving cap, capratio, mindep, minburn, wfee, bigbps and bigdelay for Monero (1e12) and Zcash (1e8) with the arithmetic shown and the price assumption behind capratio written out; add a go/no-go gate that the chosen `mindep` exceeds that chain's typical network fee and that `cap` at the posted bonds is a meaningful quantity of the asset.

**Regression test.** A unit test over a proposed parameter set that fails when `cap` expressed in the asset's own decimals is below a stated floor, or when `mindep` is below a per-chain minimum; run it from the deploy script before InitializePrivate is sent.

**Verifier.** I read deploy/README.md lines 235-252: the mainnet vault section gives only the call shape `InitializePrivate(k, treasury, cap, minbond, minburn, wfee, mindep, regfee, capratio, bigbps, bigdelay)` then `Seal()`, notes 'Sealing is the point of no return: it is what revokes the deployer', and gives no per-chain values for any of them. contracts/vault.bas:41 takes all eleven; lines 66/72/73/74 store capratio, minburn, wfee, mindep. `grep '^Function ' contracts/vault.bas` lists every entrypoint: the only post-install parameter setters are SetCap (491, k votes), SetLimits (89, pre-Seal) and Tighten (102). There is no setter for capratio, mindep, minburn or wfee — confirmed by enumeration, not by inference. The bound at 243/318/497 is `LOAD("cap") / LOAD("capratio") > LOAD("bondtot")` -> refuse, and deploy/README.md:202's go/no-go row checks only `cap / capratio <= Sum bonds`, which any capratio satisfies by construction. The arithmetic in the scenario is right: cap <= capratio x bondtot, so capratio 3 on 6 DERO of bonds caps the vault at 1.8e6 piconero.

---

### `REQ·A8-04` — Nothing in the deployment runbook disposes of the three keys that outlive the operator, and the founders promise requires a human to run a bot forever

**P1 · operational-requirement-missing · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** docs/HANDOVER.md:9-16 rule 0: 'The operator intends to revoke their own access after deployment: the contract owner keeps no power after Seal, and the TELA INDEX key may be discarded… any state that requires a privileged party… is a permanent freeze… and is a defect however well it is documented.' The only revocation in deploy/README.md is the vault's Seal ('it is what revokes the deployer'). Searching deploy/README.md and docs/HANDOVER.md for discard/destroy/renounce returns nothing else. Meanwhile: contracts/factory.bas keeps a live `owner` (SetTreasury:27, SetRegFee:39) with no renounce entrypoint; deploy/README.md (Mainnet → Rollback) requires the INDEX key to fix a broken app release, contradicting 'the INDEX key may be discarded'; deploy/README.md (Mainnet → After install) step 2 is 'Start tools/treasurybot from the treasury wallet' — and RISKS.md R-13 concedes 'an operator can stop the bot; holders would see deposits stall… That remains the trusted step, and it is the one to design out next.' No item in docs/PLATFORM_BACKLOG.md (W1–W11, B-series) covers designing it out; W8 covers only finality and sweep cost.

**Problem.** The project's first standing rule is that nothing may need a human after deployment. Three keys survive the documented deployment (factory owner, TELA INDEX, treasury) and one daemon must run forever for a promise the UI states flatly. The runbook neither disposes of the keys, nor states that they are retained and who holds them, nor gives a user any way to tell which is the case. Rule 0 and the Rollback section directly contradict each other on the INDEX key.

**Failure scenario.** Precondition: the operator deploys per the runbook and then, per their stated intent, stops participating. Step 1: nobody starts treasurybot, or it stops. Step 2: `ClaimFees` remains permissionless so fees still reach the treasury address, but the deposit into the pass — which no contract can do — never happens. Step 3: Founders Pass holders watch `dep` stall against a promise the pass contract stores immutably and the app renders as a commitment. Impact: the pass's entire value proposition depends on a wallet key remaining online and willing, indefinitely, with no fallback and no expiry; and separately, the factory owner key remains a live authority (see A8-02) with no documented disposition.

**Root cause.** Rule 0 was applied rigorously to the vault (it drove the loop-19 redesign) and never applied to the rest of the system. There is no deployment step whose done-criterion is 'no key remains that can change anything', and no inventory of surviving privileged parties.

**Fix.** Add a 'Decommissioning' section to deploy/README.md that, for each of the factory owner key, the TELA INDEX key and the treasury key, states the decision (retain or dispose), the exact steps, and how an outsider verifies it; resolve the Rollback contradiction explicitly (either the INDEX key is retained and the app is not final, or it is destroyed and there is no rollback). Add a backlog item to design out the founders deposit — e.g. holders claiming each asset directly from a per-market claim path — and until it exists, change the UI copy to say the deposit is a treasury action that can stop.

**Regression test.** Not code-testable; make it a go/no-go gate row with a named evidence artefact, checked the same way as `verifyvendor`.

**Verifier.** Verified each surviving authority. contracts/factory.bas keeps a live `owner` with SetTreasury and SetRegFee and no renounce path (see A8-01). deploy/README.md 'Rollback' requires the INDEX key to fix a broken app release, and 'After install' step 2 is 'Start tools/treasurybot from the treasury wallet' — a wallet key that must stay online for the pass promise, since founders.bas Deposit() reads DEROVALUE() and no contract can move fees into the pass on its own. The only revocation named anywhere in deploy/README.md is the vault's Seal. Searching deploy/README.md and docs/HANDOVER.md for discard/destroy/renounce turns up nothing else. ClaimFees on pool.bas is indeed permissionless (no owner check at lines 241-253), so fees still reach the treasury address while the pass deposit stalls — exactly the stated failure.

---

### `REQ·A8-05` — The owner's 'one market drained while another is left behind' requirement is defended on the Markets screen only — not where the trade is signed

**P1 · partially-implemented · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/markets.js:36 `pairKey`, :41-48 `pairs`/`dupOf`, :88 ('Inside a split pair the deepest contract is always first, under every filter and every sort'), :141-154 the per-row 'N markets for X/Y · liquidity is split' band, :199-217 the Split pairs chip and count. All of that is local to markets.js. app/feed.js:267 `trending(n)` sorts by `m.hot` alone with no pair awareness, and it is what drives Home's Trending (app/home.js:96,155) and the ticker tape (app/home.js:170). app/token.js has no split-pair notice (the only 'split' at :208 is the price-impact copy 'Split the order'). The trade sheet's market switcher, app/token.js:218, lists `D.active().sort(hot).slice(0,30)` — top markets by heat, not the other contracts trading this pair. Commit d019572's own body names this work as 'the operator's drain worry'.

**Problem.** The requirement is that no user can be routed into the thin duplicate of a pair while the deep one sits untouched. The defence was implemented in the one screen where a user is comparing markets and omitted from every path where they arrive at one and buy: Home, the tape, search, a shared deep link, and the trade sheet itself. The sheet's 'switch market' control — the natural place to say 'there is a deeper contract for this pair' — instead offers the hottest 30 markets overall.

**Failure scenario.** Precondition: two pool contracts trade BRW/XMR; the thin one holds 3% of the liquidity and, per the commit's own measurement, prices 34× away from the deep one. Step 1: the thin pool's dust activity gives it a high `hot`, so `D.trending()` places it on Home and in the tape. Step 2: a user taps it from Home (never passing through Markets), opens the trade sheet, and sees ticker, name, price, verified badge and contract id — nothing about the other contract. Step 3: the order empties the thin pool at a ruinous price. Impact: exactly the failure the requirement exists to prevent, reachable by the app's own default route.

**Root cause.** The split-pair grouping was built as a Markets-screen feature (a filter, a chip, a row band) rather than as a property of a market computed once in the data layer, so no other screen can see it.

**Fix.** Move `pairKey`/`pairs`/`dupOf` out of markets.js into feed.js's enrichment pass so every market carries `s.splitWith` (the sibling contracts, deepest first). Then: have `D.trending()` and the tape drop all but the deepest contract of a split group; show a warn box in the trade sheet (app/token.js, beside the wrapped-quote box that already exists) naming the deeper contract with a one-tap switch; and make the switcher's first section 'other contracts trading this pair'.

**Regression test.** Extend tools/uitest/ui.mjs's synthetic-market assertion block (the same harness used for impersonation) with two pools of one pair at 30:1 depth and assert: the thin one does not appear in `D.trending()`, and opening its trade sheet renders text naming the deeper contract.

**Verifier.** `grep -rn 'pairKey|dupOf|dupTitle|pairName' app/*.js` returns hits in app/markets.js ONLY (lines 36, 41, 44, 48, 49, 54, 73, 95, 141, 144, 152, 154). app/feed.js:267 `trending(n)` sorts by `b.m.hot - a.m.hot` with no pair awareness and is what app/home.js:96 and :170 (the tape) and app/markets.js:227 consume. app/token.js has no split-pair notice — the only 'Split' at :208 is the price-impact copy. The trade sheet's market switcher at app/token.js:219 is `D.active().sort((a, b) => b.m.hot - a.m.hot).slice(0, 30)` — the hottest 30 markets overall, not the other contracts on this pair. The heat metric itself (stats.js:71 `m.hot = f1.blocks * 4 + pub1.who * 3 + Math.min(50, Math.log1p(f1.vol) * 8)`) is dominated by block count and distinct publishers, so a thin pool with dust activity can genuinely out-rank a deep one — the routing hazard is real, not hypothetical.

---

### `SEC·RT-02` — Halt freezes burns AND releases with no timeout, and the Wrap screen tells the user halted withdrawals stay payable

**P1 · contract / availability / false-claim · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\vault.bas: Burn line 22 `IF LOAD("halted") != 0 || LOAD("sealed") == 0 THEN GOTO 900`; Released line 21 `IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("halted") != 0 THEN GOTO 900`; Mint line 22 and Register line 22 likewise. Halt (lines 20-41) flips `halted` on k signer votes and has no expiry — only another k-of-n Halt(0) clears it. C:\Users\Wilma\Desktop\fomo\app\wrap.js, the halted warnbox: 'A halt is a condition, not a decision: each signer votes it on and off from public state, so it clears by itself when the condition does. Withdrawals already burned are unaffected and stay payable.' The same screen's trust panel says 'Stopping one takes k signers voting to veto or halt', contradicting its own earlier sentence. C:\Users\Wilma\Desktop\fomo\tools\vaultsigner\main.go, pass() comment around line 651, already knows: 'because vault.bas Released refuses while halted, `sup` can then never fall and the halt ...'.

**Problem.** A halted vault is a total freeze of the wrapped asset: no new mints, no new burns, and no settlement of burns that are already pending. Only the federation can lift it, and nothing expires. The screen states the opposite of what the contract does, on the one screen where a user decides whether to trust the vault with foreign coins.

**Failure scenario.** Precondition: a sealed vault with pending withdrawals. 1) k signers vote Halt(1) — legitimately (a suspected reserve shortfall) or maliciously. 2) Every user holding wXMR now cannot burn, and every user who already burned cannot be paid: Released is refused. 3) The signers' auto-resume in vaultsigner only fires when `reserve >= sup - inflight`; if the shortfall is real, or if the signers stop running, the halt is permanent. 4) Meanwhile the AMM pools on DMZ keep trading wXMR that can no longer be redeemed, so the price is free to detach from the peg while holders believe (from the Wrap screen) that their pending withdrawals are safe.

**Root cause.** `halted` is a boolean with no height bound, and the guard was added to Released alongside Mint without distinguishing 'stop taking on new obligations' from 'stop honouring existing ones'. The UI copy was written from the intended design rather than from the contract.

**Fix.** Remove `halted` from the Released guard (paying out an already-burned claim reduces exposure and can never make a shortfall worse), and give Halt an automatic expiry — e.g. `halted` stores the height it was set and is treated as clear after N blocks unless re-voted. At minimum, correct the wrap.js warnbox now: while halted, burns are refused and pending withdrawals cannot be settled; only Veto still works.

**Regression test.** tests/: burn, then Halt(1), then attempt Released; assert it is refused today, and after the fix assert it succeeds. Add a UI assertion that the halted copy matches the contract's actual behaviour.

**Verifier.** All four contract citations check out against the file: Burn line 368 `IF LOAD("halted") != 0 || LOAD("sealed") == 0 THEN GOTO 900`; Released line 398 `IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("halted") != 0 THEN GOTO 900`; Mint line 306; Register line 257. Halt (lines 473-488) has no height bound — grepping "halted" finds writers only in Halt itself (line 484) and InitializePrivate (line 62). Veto line 443 has no halted check. Reproduced: with a sealed 2-of-3 vault halted, Burn returned 'Discarded knowingly' and both signers' Released calls were refused, w_0_st stayed 0. The UI contradiction is verified verbatim at app/wrap.js line 252: 'Withdrawals already burned are unaffected and stay payable.' The same screen's trust panel (line 338) then says 'Stopping one takes k signers voting to veto or halt.' The vaultsigner comment the finding quotes is real, at tools/vaultsigner/main.go lines 650-652: the author already knew Released refuses while halted and used it to justify the inflight-narrowed invariant rather than fixing the asymmetry.

---

### `SEC·RT-03` — Every price-change window reads about -100% on any market quoted in a wrapped asset; the chart is out of scale with the header price by 10^(quoteDec-5)

**P1 · correctness / money display · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\app\stats.js line 16, priceAt(): `return price(pts[r < 0 ? 0 : r], s.dec);` — two arguments, so A.price falls back to qdec = 5. Line 19, change(): `const p1 = price(s, s.dec, s.quoteDec), p0 = priceAt(s, now() - secs);` — p1 is quote-aware, p0 is not. Lines 83, 88 and 90 in candles() have the same two-argument call. C:\Users\Wilma\Desktop\fomo\app\amm.js: `const price = (s, dec, qdec) => (s.ra === Z ? 0 : Number(s.rd) / 10 ** (Number.isInteger(qdec) ? qdec : 5) / (Number(s.ra) / 10 ** Number(dec)));`. Reproduced with node: rd=1.1e12 (12-dec wXMR), ra=9e5 (3-dec token) gives p1 = 0.0012222 and p0 = 10000, change = -99.99998777777776%. Wrapped-quote markets are supported and shipped tooling creates them: C:\Users\Wilma\Desktop\fomo\tools\simwrapmkt\main.go flag `-quote` — 'Set it to a vault to make a TOKEN/WRAPPED pair', and app/forms.js F.launch offers every vault as a 'Raised in' quote (config.js vaults have dec 12 and dec 8).

**Problem.** c5m, c1h, c6h, c24h and c7d are all computed by dividing a quote-scaled price by a DERO-scaled price. On a 12-decimal wXMR quote the ratio is 1e-7, so every window renders as approximately -100.00% forever, coloured red, on every surface: the token page's four change readings, the markets table change columns, the changePill on the trade sheet and market picker, and the price tape. It also poisons D.movers() (which sorts on |c24h|, so wrapped markets permanently occupy the movers list) and L.mood(). Separately, candles() is DERO-scaled while the token page header uses s.price (quote-scaled), so the chart's axis labels, last-price tag, crosshair tag and OHLC tooltip disagree with the price printed directly above them by a factor of 10^7 on a wXMR market.

**Failure scenario.** Precondition: a TOKEN/wXMR pool or a launch raised in wXMR is listed. 1) A user opens the market. 2) The header shows the correct price, e.g. 0.00122 XMR, but every change window beside it shows -100.00% and the chart's y-axis reads 10000. 3) The user concludes the token has collapsed and either sells at a loss or never buys. Nothing on screen indicates a unit error. Impact: financially material misinformation on exactly the markets the product is built around.

**Root cause.** The 'A.price now takes quote decimals' fix was applied at the two call sites that compute the CURRENT price (stats.js:19 first half, stats.js:57) but not to priceAt() or candles(), which are the historical-price paths. The default `qdec = 5` fallback in A.price makes the omission silent rather than a thrown error.

**Fix.** Pass the quote decimals at all four remaining sites: stats.js line 16 `return price(pts[r < 0 ? 0 : r], s.dec, s.quoteDec);` and lines 83, 88, 90 `price(pts[i], dec, s.quoteDec)` (hoist `const qd = s.quoteDec` next to `const dec = s.dec`). ui.js:120 sparkline() is internally consistent so it can stay, but passing qdec there too removes the trap for the next caller.

**Regression test.** A unit test that builds a market with quoteDec 12 and two price points an hour apart at the same real price, and asserts change(s, 3600) === 0 (today it returns about -100). A second asserting candles()[n].c equals A.price(point, dec, quoteDec).

**Verifier.** app/amm.js line 99: `const price = (s, dec, qdec) => (s.ra === Z ? 0 : Number(s.rd) / 10 ** (Number.isInteger(qdec) ? qdec : 5) / (Number(s.ra) / 10 ** Number(dec)));` — omitting qdec silently means DERO. app/stats.js line 16 `return price(pts[r < 0 ? 0 : r], s.dec);` (two args) versus line 19 `const p1 = price(s, s.dec, s.quoteDec)` (three). candles() repeats the two-arg form at lines 83, 88 and 90 while its own volume line 92 uses the quote-aware qunit(s), so one candle mixes a quote-scaled volume with a DERO-scaled price. On a 12-decimal quote p1/p0 = 10^(5-12) = 1e-7, so change() returns (1e-7 - 1) * 100 ≈ -99.99999% for every window. Wrapped-quote markets are reachable from the shipped UI, not only from tooling: app/forms.js line 40 builds `quotes = [DERO].concat(D.vaults())` and line 122 installs with `["quote","H",quote.scid]`; app/config.js ships vaults with dec 12 and dec 8. The downstream claims hold: feed.js line 268 movers() sorts on Math.abs(m.c24h), and app/live.js line 56 mood() is volume-weighted 24h change.

---

### `SEC·RT-04` — The vault publicly links a DERO address to its foreign deposit address and to every one of its deposit indexes, while the app claims fresh indexes are unlinked

**P1 · privacy / false-claim · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\vault.bas, Function Register lines 30-32: `STORE("ua_" + j, s)` (index -> the signer's raw address), `STORE("ul_" + s + "_" + c, j)` (address -> index), `STORE("uc_" + s, c + 1)`. Function Attest line 40: `STORE("da_" + j, addr)` — the Monero/Zcash address, in the clear. Function Burn lines 41-44: `STORE("w_" + j + "_to", dest)` (foreign destination) alongside `STORE("w_" + j + "_by", s)` (DERO address). All of these are ordinary contract keys, readable by anyone with `DERO.GetSC {variables:true}` — the app itself does exactly that in feed.js loadSocial(). The contract comment above Register asserts 'A FRESH index per deposit (nothing on chain links two deposits of one user)'. C:\Users\Wilma\Desktop\fomo\app\wrap.js, deposit step 1 detail: 'Registering again gives you a fresh index, so two deposits of yours are not linked on chain.'

**Problem.** The stated privacy property is the opposite of what the contract stores. `ua_<j>` maps every index to its owner and `ul_<addr>_<c>` maps every owner to all of their indexes, so all of one wallet's deposits are trivially linked, and `da_<j>` ties each of them to a specific Monero or Zcash address. On the withdrawal side `w_<j>_by` + `w_<j>_to` publish a DERO-address-to-foreign-address pair permanently. This is a product aimed at Monero and Zcash holders, sold on privacy, and the linkage is the exact thing those users are avoiding.

**Failure scenario.** Precondition: a sealed vault with any deposits. 1) An observer (chain analyst, exchange, the DERO node the victim uses, anyone) calls DERO.GetSC on the vault with variables:true. 2) They enumerate ua_0..ua_n and uc_<addr>/ul_<addr>_<c>, producing a complete table of DERO address -> {every deposit index that address ever registered}. 3) They read da_<j> for each, producing DERO address -> {every Monero deposit address the federation attested for it}. 4) They read w_<j>_by and w_<j>_to for every withdrawal, producing DERO address -> foreign payout address. Impact: full deanonymisation of the bridge in both directions, from public state, with no privileged access.

**Root cause.** Register uses SIGNER() (ring 2, so the address is real and is then stored verbatim) and keeps a per-address index list for the app's own convenience (myVault() reads uc_/ul_). The privacy claim was written about the FOREIGN side — one address per deposit, so the foreign chain does not link them — and was then stated as if it held on the DERO side too.

**Fix.** Correct the copy immediately in both places: state that the vault records, publicly and permanently, which DERO address owns which deposit index, which foreign address was attested to it, and which foreign address a burn paid. For the design: index ownership does not need to be an address — Register could store a commitment (hash of a user-chosen secret) and Mint could pay a named address supplied at mint time, removing ua_/ul_/uc_ entirely; the app already has the '...To' pattern for exactly this.

**Regression test.** A chain test that registers two indexes from one wallet, then asserts from GetSC variables alone that the two indexes cannot be attributed to the same address.

**Verifier.** Register (contracts/vault.bas lines 264-266) stores `ua_<j> = s`, `ul_<s>_<c> = j` and `uc_<s> = c+1`, where s = SIGNER() at ring 2 (line 255), i.e. the real address. Attest line 290 stores `da_<j> = addr` — the foreign address in the clear. Burn lines 380 and 383 store `w_<j>_to` and `w_<j>_by` side by side. All are ordinary contract keys; app/feed.js loadSocial() line 249 shows the app itself reads a contract with `{variables: true}`. Reproduced on the DVM: after one Register + Attest + Burn, ua_0, ul_<addr>_0, uc_<addr> and w_0_by all exist, and da_0 read back as the plain attested string '4Axmr-user', w_0_to as '4AxmrPayMeAbroadNow'. The contract's own comment at line 250 ('nothing on chain links two deposits of one user') and app/wrap.js line 275 ('Registering again gives you a fresh index, so two deposits of yours are not linked on chain') both assert the opposite of what the code stores.

---

### `UX·F1` — The wrap screen prints the vault's minimum deposit, mint cap and headroom as "0" — the one number whose whole purpose is to prevent an unrecoverable loss

**P1 · data-display · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/wrap.js:114 `const amt = (u, places) => (dec() === 0 ? String(u) + " base units" : A.fmtUnits(u, dec(), places === undefined ? 6 : places) + " " + sym());` calling app/amm.js:71 fmtUnits, which slices the fraction to maxFrac and then `frac.replace(/0+$/, "")` — so any non-zero value below 10^-maxFrac becomes the string "0". The XMR vault is 12 decimals (app/config.js:12 `dec: 12`) and tools/simvault/main.go:39,50 deploy cap=100000 and mindep=1 base units. fmtUnits(1n,12,8) → "0"; fmtUnits(100000n,12,4) → "0". Confirmed in the render .sim/ui-dec/02-wrap.png: "CLAIMS OUTSTANDING 0 XMR / MINT CAP 0 XMR / HEADROOM 0 XMR", the warning box "Minimum deposit: 0 XMR — Anything smaller cannot be minted, and there is nobody who can return it" (app/wrap.js:259-261), and step 3 "Send at least 0 XMR to that address" (app/wrap.js:293).

**Problem.** The screen's own header comment calls the minimum deposit one of "the two numbers a user is most often surprised by" and says a rule you cannot see is a trap. The formatter turns that rule into "0", which reads as "there is no minimum". The same call renders the mint cap and headroom as zero, so the Level 1 instrument reports a vault with capacity as having none, and the trust panel says "0 XMR of claims exist and the contract will not mint past 0 XMR". There is no distinction anywhere between a true zero and a value too small to render.

**Failure scenario.** Precondition: a vault on a 12-decimal asset (XMR) with mindep set to any value below 10^-8 of a whole coin, which is the deployed default. Steps: a Monero holder opens #wrap, reads "Minimum deposit: 0 XMR" and step 3's "Send at least 0 XMR to that address", registers an index, and sends a small amount — below the real minimum — to the published deposit address. Impact: no honest signer will vote to mint it (contracts/vault.bas:324 `IF amount < LOAD("mindep") THEN GOTO 900`), the coins are on a ring-signature chain with no return address, and there is no operator to ask. The money is gone, and the screen built to prevent exactly this told the user there was no floor.

**Root cause.** fmtUnits truncates rather than rounds and has no floor representation. Every caller that passes a maxFrac smaller than the asset's decimals inherits the fault, and wrap.js passes fixed places (4 and 8) against a 12-decimal asset. The recent "A.price now takes quote decimals" fix corrected the price path and left the amount path.

**Fix.** In app/amm.js fmtUnits, when v !== 0 and the trimmed fraction is empty, return a floor form rather than "0" — e.g. `"<0." + "0".repeat(maxFrac-1) + "1"`. Independently, in app/wrap.js:114 make amt() fall back to full precision for vault parameters: `amt(u, places)` should use `dec()` places (not 4 or 8) for mindep, minburn, wfee, cap and headroom, since those are protocol constants a user must read exactly. Add a hard guard in the deposit warning box: if `w.mindep === 0n`, say "This vault sets no minimum deposit" explicitly instead of formatting a number.

**Regression test.** Assert in tools/uitest/ui.mjs that the wrap screen's minimum-deposit line does not match /Minimum deposit: 0 [A-Z]/ when the contract's `mindep` key is non-zero — the test already reads that the wrap screen publishes the minimum, so extend that assertion rather than adding one.

**Verifier.** app/forms.js:62 `grad = A.parseUnits(f.grad.value, 5n)` — literal 5n regardless of the selected quote. Line 71 checks `grad < 1000000n || grad > 1000000000000n`, bounds that only mean 10..10,000,000 at five decimals. Line 89 hard-codes ` DERO` on pv.start, pv.end and pv.mcap. Line 86 `A.price({rd: vd0, ra: vt0}, dec)` omits qdec (same fault as Q1). Line 58 builds the preset chips with `(quote.sym || "DERO")` and line 106/107 build the field label and note the same way — all three are constructed as arguments to openSheet at lines 95-113, before any chip click can change `quote`, and the chip handler at lines 45-53 only rewrites quoteNote.textContent and calls update(); update() (lines 77-93) never re-renders the chips, the field label or the note. So after picking XMR the label still reads 'Graduation target (DERO)' and the presets still read '100 DERO'. The quote IS passed on at line 122 `["quote", "H", quote.scid]`. With config.js vaults[0].dec = 12, typing 100 gives grad = 1e7 piconero = 0.00001 XMR, which passes the 1e6..1e12 bound. launch.bas is immutable. Every step verified.

---

### `UX·F2` — The Zcash vault cannot transact: app.tx's contract allow-list only knows the single legacy cfg.vault

**P1 · correctness · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/app.js:113 — `if (!opts.code && !(D.assets.has(opts.scid) || D.nfts.has(opts.scid) || opts.scid === D.cfg.factory || opts.scid === D.cfg.social || (D.founders && opts.scid === D.founders.scid) || (D.cfg.vault && opts.scid === D.cfg.vault) || (D.cfg.chat && opts.scid === D.cfg.chat))) { toast("Refusing: contract is not a verified listing", "err"); return null; }`. app/config.js:11-15 ships two vaults (XMR d69c93… and ZEC bfd444…) and `vault:` is only the XMR one. app/feed.js:27-32 `vaults()` returns both. app/wrap.js:217-221 draws a picker over all of them and app/screens.js:69 links each one's "Deposit / withdraw" button. app/wrap.js:426-447 doRegister/doBurn pass `scid: w.scid`, which for the ZEC vault is not `D.cfg.vault`. Vault SCIDs are not factory listings, so `D.assets.has()` cannot cover them.

**Problem.** Every write path on the second vault is refused by the app's own guard. The picker, the portfolio entry point and the whole deposit/withdraw walkthrough are drawn as available, and the failure surfaces only as a red toast reading "Refusing: contract is not a verified listing" — which tells a user the contract is untrustworthy rather than that the app has a bug.

**Failure scenario.** Precondition: a build with more than one vault, which is the shipped config. Steps: open #wrap, tap the ZEC chip, connect a wallet, press "Register a deposit index". Impact: toast "Refusing: contract is not a verified listing"; no transaction is built. Same for "Burn and withdraw". Half of the product's stated audience — Zcash holders — has no working path in or out, and the error implicates the contract.

**Root cause.** The allow-list was written against the single-vault `cfg.vault` field and was not updated when `vaults[]` and `D.vaults()` were introduced; feed.js:31 folds `cfg.vault` in for compatibility but app.js reads the raw config field instead of the accessor.

**Fix.** app/app.js:113 — replace `(D.cfg.vault && opts.scid === D.cfg.vault)` with `D.vaults().some((v) => v.scid === opts.scid)`. That accessor already validates each scid against /^[0-9a-f]{64}$/ and already folds in the legacy `cfg.vault`, so nothing else changes.

**Regression test.** In tests or tools/uitest, drive #wrap/<second vault scid>, click Register with a stubbed wallet and assert no toast matching /not a verified listing/ appears. A cheaper unit guard: assert `D.vaults().every(v => allowedScid(v.scid))`.

**Verifier.** app/app.js:113 is quoted verbatim and correct: the vault arm of the allow-list is `(D.cfg.vault && opts.scid === D.cfg.vault)` only. app/config.js:11-15 ships vaults[] with XMR d69c93… and ZEC bfd444…, and `vault:` is the XMR one. app/feed.js:27-32 vaults() returns both; app/wrap.js:217-221 draws the picker at all.length>=2 (visible as the XMR|ZEC chip strip in .sim/ui-dec/02-wrap.png); app/wrap.js:426-447 doRegister/doBurn pass `scid: w.scid`. I checked the escape hatch: D.assets is populated only from `[...reg.launches, ...reg.pools]` keyed by the registry SCID (app/feed.js:227-234), and the ZEC *pool* row visible in the markets render is a separate pool contract whose `asset` is the vault SCID, not the vault SCID itself — so D.assets.has(bfd444…) is false. Every write path on the second vault is refused with "Refusing: contract is not a verified listing".

---

### `UX·F6` — The launch wizard parses the graduation target at DERO's 5 decimals whatever quote is chosen, and labels every preview figure DERO

**P1 · correctness · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/forms.js:63 — `grad = A.parseUnits(f.grad.value, 5n)`, a hard-coded 5, while the quote is selected from `quotes = [{DERO, dec:5}].concat(D.vaults())` (forms.js:38) where the XMR vault is dec 12 and ZEC is dec 8 (app/config.js:12-13). app/forms.js:70 — the validation message "Graduation target must be between 10 and 10 000 000 " + (quote.sym || "DERO"). contracts/launch.bas:15 enforces `IF grad < 1000000 || grad > 1000000000000 THEN GOTO 900` in the quote's own base units. app/forms.js:88 — `pv.start.textContent = … + " DERO"; pv.end.textContent = … + " DERO"; pv.mcap.textContent = … + " DERO"` unconditionally. app/forms.js:113 — "20% seeds the AMM at graduation with the DERO raised". app/forms.js:55 — the presets are labelled with quote.sym but set a raw number parsed at 5 decimals. app/forms.js:97 — the preview hero's network mark is the literal `h("span", { class: "net" }, "DR")` regardless of quote. Visible in .sim/ui-dec/08-launch-sheet.png.

**Problem.** With XMR selected, typing 100 produces 10,000,000 base units = 0.00001 XMR, not 100 XMR — wrong by a factor of 10^7. The contract's real range for a 12-decimal quote is 0.000001 to 1 XMR, while the form tells the creator the range is 10 to 10,000,000 XMR. The preview's first price, graduation price and FDV are all labelled DERO on a wXMR market, so the creator cannot even read what they are about to install.

**Failure scenario.** Precondition: a creator picks the XMR chip in the launch wizard, which the UI presents as a first-class option. Steps: they enter a graduation target of 100 intending 100 XMR, read a preview quoting prices in DERO, and confirm two wallet transactions. Impact: the token installs with a curve that graduates after 0.00001 XMR — effectively instantly, on the first buy — and the creator has paid install and registration fees for a market that is not the one they specified. The contract accepts it, so nothing reverts and nothing can be changed afterwards; launch.bas is immutable.

**Root cause.** The quote selector was added to the wizard without threading `quote.dec` through readForm(), the preview labels or the explanatory copy. The rest of the app was converted to quote-aware units (D.qsym/D.qdec throughout token.js, markets.js, screens.js); forms.js was not.

**Fix.** app/forms.js:63 — `A.parseUnits(f.grad.value, BigInt(quote.dec ?? 5))`. app/forms.js:70 — compute the bounds for display from the quote: `A.fmtUnits(1000000n, quote.dec, quote.dec)` and `A.fmtUnits(1000000000000n, quote.dec, quote.dec)`, so the message states the real range in the real asset. app/forms.js:88 — replace the three literal " DERO" strings with `" " + (quote.sym || "DERO")` and price with `A.price(..., quote.dec)`. app/forms.js:113 — "with the " + (quote.sym || "DERO") + " raised". app/forms.js:97 — derive the network mark from the quote the way wrap.js:129 netOf() already does. app/forms.js:55 — derive the preset values from the contract bounds for the chosen quote rather than the fixed [10,50,100,500].

**Regression test.** Extend tools/uitest/ui.mjs's launch-sheet pass: select the XMR quote chip, type 100 into the graduation field, and assert the preview text does not contain " DERO" and that the parsed argument equals 100 * 10^12.

**Verifier.** app/forms.js:63 is `grad = A.parseUnits(f.grad.value, 5n)` with a literal 5n; `quote.dec` is never read in readForm(). forms.js:38-44 builds quotes from `[{DERO,dec:5}].concat(D.vaults())` and config.js gives XMR dec:12, ZEC dec:8. forms.js:70 renders the range against `quote.sym`; forms.js:88 hard-codes " DERO" on start/end/mcap; forms.js:97 hard-codes the "DR" net mark; forms.js:113 says "the DERO raised". contracts/launch.bas DVM line 25 (file:15) bounds grad to 1e6..1e12 atomic units with no reference to the quote. This is the same defect as P7-01.

---

### `ARCH·E1` — router.dispatch's try/catch is the app's only render safety net and cannot catch the seven screens that render from an async function; there is no unhandledrejection handler either

**P2 · error-handling · auditor confidence High · verifier CONFIRMED**

**Evidence.** `app/ui.js:141`: `try { fn(screen, param); } catch (e) { console.error(e); render(screen, h("div", { class: "errbox" }, String(e.message || e))); }` — a synchronous catch around a call whose return value is discarded.
Async handlers: `app/screens.js:39` (`portfolio`), `:187` (`profile`), `app/wrap.js:186`, `app/chat.js:77`, plus `app/screens.js:280,281` which delegate to `V.nft.page` (`app/nft.js:7` `page: async`) and `V.token.page` (`app/token.js:20` `T.page = async`).
`grep -n "unhandledrejection\|window.onerror" app/*.js` returns nothing.
`app/wrap.js:452-461` is the one handler that does it properly: `try { ... } catch (e) { console.error(e); draw(D.vault, null, "The vault could not be read from your node: " + ...); }`.

**Problem.** The errbox path exists and is written as if it covers every screen; it covers `home`, `trade`, `markets`, `nfts` and `settings`. On the other seven, a rejection anywhere after the first `await` leaves whatever was last rendered — usually `U.skeleton(3)` from `app/token.js:26` or `app/screens.js:95` — permanently on screen, with the failure visible only in a console the user does not have open. `app/token.js:26` deliberately renders skeletons and then awaits up to 15 seconds, which is exactly the window where a rejection is least distinguishable from slowness.

**Failure scenario.** Precondition: a node that answers some calls and fails others (a resyncing node, a rate-limited public node — `README`/`deploy` note gamma rate limits). Steps: (1) open `#token/<scid>` for a market not yet in `D.assets`; (2) `D.loadContract` succeeds; (3) `S.enrich(s, D.height)` at `app/token.js:35` throws on an unexpected shape, or `V.sha256Hex` rejects because `crypto.subtle` is unavailable on a non-secure origin. Impact: three grey skeleton bars, forever, on the route every shared link uses. No message, no retry, no errbox.

**Root cause.** The safety net was written for synchronous handlers and never revisited when handlers became async; nothing at the boundary enforces that a handler is synchronous or that its promise is handled.

**Fix.** Two lines. In `router.dispatch`, capture the return value: `const r = fn(screen, param); if (r && r.then) r.catch((e) => { console.error(e); render(screen, h("div", { class: "errbox" }, String(e.message || e))); });` — the same recovery, applied to the async case. Separately add `window.addEventListener("unhandledrejection", (e) => U.toast("Something failed: " + String(e.reason && e.reason.message || e.reason), "err", 8000));` so a rejection outside a handler is at least visible.

**Regression test.** In `tools/uitest/ui.mjs`, stub `Veil.stats.enrich` to throw, navigate to a token page, and assert `.errbox` is present rather than `.skel`.

**Verifier.** app/ui.js:143 (the finding says 141) is `try { fn(screen, param); } catch (e) { ... errbox ... }` — a synchronous catch around a discarded return value. Async handlers confirmed: chat.js:77, screens.js:39 (portfolio), screens.js:187 (profile), wrap.js:186, plus screens.js:280/281 delegating to nft.js:7 `page: async` and token.js:21 `T.page = async`. `grep -rn 'unhandledrejection|window.onerror|addEventListener("error"' app/*.js app/index.html` returns nothing. token.js:26 does render U.skeleton(3) and then awaits up to 15 s at line 30. Critically, token.js:35's try only wraps `s = listed || (await D.loadContract(scid))`; the S.enrich(s, D.height) call at the end of the same line is outside it, so a throw there is an unhandled rejection with skeletons left on screen.

---

### `ARCH·T1` — tools/simsmoke installs pool.bas without the `quote` argument the contract has required since v2 — the end-to-end reserve differential cannot run

**P2 · dead-tool/drift · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `contracts/pool.bas:15` `Function InitializePrivate(asset String, quote String, treasury String, fee Uint64, pfee Uint64)`, line 18 `IF STRLEN(asset) != 32 || STRLEN(quote) != 32 THEN GOTO 900`.
`tools/simsmoke/main.go:61`: `c.Send(*w0, a0, pool, "", nil, []rpc.Arg{rpc.H("asset", assetSCID), rpc.S("treasury", a2), rpc.U("fee", 50), rpc.U("pfee", 5000)})` — no `quote`.
The two sibling tools that install the same contract WERE updated: `tools/simlaunch/main.go:65` `rpc.H("quote", "000…0")` and `tools/simwrapmkt/main.go:85` `rpc.H("quote", quoteA)`.
`tests/pool_test.go:243` uses a missing/short quote as an explicit REJECT case.

**Problem.** simsmoke's own header calls it the tool that will "compare the on-chain reserves with the reference model to the atomic unit" — it is the only live-chain differential between `reference/pool.go` and `pool.bas`. It cannot get past step 3. `simrpc.Send` verifies an install landed (`if st := c.GetSC(r.TXID, true); st.Code != code { log.Fatalf("install %s confirmed but the contract did not execute") }`), so the failure is a hard exit, not a silent skip — but nothing runs simsmoke in CI, so nobody sees it.

**Failure scenario.** Precondition: a running simulator. Steps: (1) `cd tools && go run ./simsmoke -contracts ../contracts`; (2) step 3 installs pool.bas with four of five parameters; (3) InitializePrivate takes the STRLEN branch and returns 1; the install reverts and no code lands; (4) `Send` log.Fatals. Impact: the differential check between the Go reference model and the deployed AMM has been silently unavailable since the quote parameter landed, on a repo whose stated testing strategy is "integration tests are the source of truth".

**Root cause.** Four call sites install pool.bas (simsmoke, simwrapmkt, tests, and app/forms.js) with four independently maintained argument lists. Changing the contract signature required finding all four by hand.

**Fix.** Add `rpc.H("quote", "0000000000000000000000000000000000000000000000000000000000000000")` at `tools/simsmoke/main.go:61`, matching simlaunch.

**Regression test.** Add simsmoke to a `tools/uitest/run.sh`-style smoke script so a contract signature change breaks a run rather than a tool nobody executes. Verifying the exact DVM failure (missing-parameter error vs empty string) needs one simulator run — the install failing either way is certain from the source.

**Verifier.** contracts/pool.bas:15 is `Function InitializePrivate(asset String, quote String, treasury String, fee Uint64, pfee Uint64)` and line 20 is `IF STRLEN(asset) != 32 || STRLEN(quote) != 32 THEN GOTO 900`. tools/simsmoke/main.go:61 sends only asset/treasury/fee/pfee — no quote. tools/simlaunch/main.go:65 and tools/simwrapmkt/main.go:85 both pass `rpc.H("quote", ...)`, confirming the sibling tools were updated and simsmoke was not. tools/simrpc/simrpc.go:239 does `log.Fatalf("install %s confirmed but the contract did not execute")` when st.Code != code, so it is a hard exit rather than a silent skip. tests/pool_test.go:73 always passes quote, so the Go suite is unaffected.

---

### `DATA·D3-05` — Two quote-denominated figures were missed by the labelling pass: Profile's 'Published vol' adds XMR to DERO, and the Claim-creator-tax button prints a quote amount as DERO

**P2 · correctness / units · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/screens.js:198 `const vol = trades.reduce((a, t) => a + (t.side === 1 ? t.in : t.out), 0n)` over `D.tradesOf(addr)`, which spans every market; :209 renders it as `stat("Published vol", A.fmtDero(vol, 1))`. A.fmtDero is `fmtUnits(v, 5, f)` (amm.js:88) — 5 decimals, fixed. app/token.js:114 `"Claim creator tax (" + A.fmtDero(s.tacc, 2) + ")"`, where `tacc` is accrued in the market's quote (launch.bas:67, 208). The Buyback button one line above (token.js:113) does it correctly: `A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s)`.

**Problem.** 'Published vol' is a cross-currency sum with no unit shown at all: one 0.5 XMR trade contributes 500,000,000,000 atomic units, printed as 5,000,000.0 next to a DERO trade's true figure. This is the exact fault commit d68094d ('the app was adding Monero to DERO and calling the answer DERO') set out to remove, and the same file's Portfolio section (screens.js:1-8, 172) refuses to do it and explains at length why. The tax button understates or overstates the claimable amount by 10^(quoteDec − 5) and gives it no symbol.

**Failure scenario.** Precondition: an address that has published trades on both a DERO market and a wXMR market. Steps: open its Profile. Impact: 'Published vol' shows a number that is not a quantity of anything. Separately, a creator of a wXMR launch with tmode 1 sees 'Claim creator tax (X)' where X is 10^7 times the XMR actually claimable.

**Root cause.** D.qsym/D.qdec were applied file by file; these two call sites were not in the sweep. The Profile one additionally needs the per-quote grouping the Portfolio already has.

**Fix.** token.js:114 → mirror line 113 exactly: `A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s)`. screens.js:198/209 → group `trades` by `D.qsym(t.asset)` and render one figure per quote (or drop the stat), never a single total.

**Regression test.** Grep guard in the UI test: no call to fmtDero may take a value read from a market record (`tacc`, `fd`, `rd`, `real`, `t.in`, `t.out`).

**Verifier.** app/screens.js:195 `const vol = trades.reduce((a, t) => a + (t.side === 1 ? t.in : t.out), 0n)` over `D.tradesOf(addr)`, which app/feed.js:282 builds across every market in the registry; screens.js:209 renders it as `stat("Published vol", A.fmtDero(vol, 1))`, and A.fmtDero is `fmtUnits(v, 5, f)` at amm.js:88 — 5 decimals fixed, no symbol. app/token.js:114 `"Claim creator tax (" + A.fmtDero(s.tacc, 2) + ")"`, while tacc accrues in the market's quote (launch.bas lines 67/137/220/236 all `STORE("tacc", ... )` on the quote leg). token.js:113, one line above, does it correctly: `A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s)`. The Portfolio's refusal to sum across quotes is real and is spelled out at screens.js:1-8 and screens.js:173-174.

---

### `DATA·D3-09` — The localStorage cache is silently discarded for any market that has a published sell

**P2 · correctness / cache · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/feed.js:131, inside validCache: `if (!Array.isArray(t) || t.length !== 7 || t[0] !== n++ || (t[1] !== 0 && t[1] !== 1) || ...) return false;`. Chain side values are 1 and 2 (pool.bas:12, `post(2, ...)` at pool.bas:234 and launch.bas:47/214). Replicated: a cached row with side 2 fails. feed.js:119 then does `localStorage.removeItem(this.cacheKey(scid))`.

**Problem.** The whole incremental-cache feature — the header comment's 'a bounded copy is kept in localStorage so reopening the app is instant', and with it the VERIFY_N re-read path at feed.js:180-186 — is dead for any market whose last CACHE_T trades include one sell. Every reopen re-reads up to MAXP + MAXT keys from the node for those markets. Nothing reports it; the cache write at feed.js:139 still succeeds, so the code looks like it is working.

**Failure scenario.** Precondition: a market with at least one published sell in its retained window. Steps: reload the app. Impact: full re-read of that market's history on every load, and the tamper-verification path that the cache was designed around never executes for it. A second, narrower instance of the same class: feed.js:127 asserts `if (c.pc === 0 && c.tc > 0) return false` ('a published trade always leaves a price point'), but tick() writes no p_ on a contract's first active block (pool.bas:265 `IF LOAD("lh") == 0 THEN GOTO 80`), so pc = 0 with tc = 1 is a legitimate on-chain state that this rejects.

**Root cause.** Same 0/1-versus-1/2 side confusion as D3-03, in the second of the two validators.

**Fix.** feed.js:131 → `(t[1] !== 1 && t[1] !== 2)`; feed.js:127 → drop the pc/tc invariant or weaken it to `c.pc === 0 && c.tc > 1`.

**Regression test.** Write a cache record containing a side-2 trade, call loadCache, assert it survives and that the key is still in localStorage afterwards.

**Verifier.** app/feed.js:131 requires `(t[1] !== 0 && t[1] !== 1)` to fail; chain sides are 1 and 2 (pool.bas:12, pool.bas:234, launch.bas:222/238). feed.js:119 then does `localStorage.removeItem(this.cacheKey(scid))`, and feed.js:138 keeps the last CACHE_T=600 trades, so once a published sell enters that window the record is rejected on every load for a long time. The pc=0/tc>0 case is real: on a launch's first buy, launch.bas:61 calls tick(rd,ra) with lh still 0, and pool.bas:265 `IF LOAD("lh") == 0 THEN GOTO 80` skips the p_ write; post() at launch.bas:69 then sets tc=1. feed.js:127 rejects exactly that.

---

### `DATA·D3-10` — The trade ledger records the quote, not the fill, and records the amount sent rather than the amount the contract consumed

**P2 · correctness / P&L · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/app.js:213 `D.addLocalTrade({ scid: s.scid, asset: s.asset, side, in: amount.toString(), out: q.out.toString(), ... })` — `q` is the pre-signature quote object from A.launchBuy/A.poolBuy, and `amount` is the full amount sent. The contract only guarantees `min_out` (pool.bas:165, launch.bas:60), which is `q.out` minus up to 10% slippage. On a curve buy that completes graduation, launch.bas:50-59 recomputes `used` and refunds `d - used`, so the contract consumes less than `amount`. app.js already reads the balance before and after the call (app.js:143 b0, app.js:163 balanceAfter) but only compares them for direction, never for magnitude.

**Problem.** Cost basis, average price, realised and unrealised P&L, the Portfolio value column and the equity curve are all computed from what the app predicted rather than from what happened. Two separate errors: (1) `out` is the quote, so any fill between min_out and q.out over-states units bought and proceeds received; (2) `in` includes the refunded portion of a graduating buy.

**Failure scenario.** Precondition: a trade whose fill differs from the quote — any market that moves between the quote and inclusion, which is the normal case at 18-second blocks. Steps: buy with 1% slippage while another buy lands first. Impact: the ledger records the better, unfilled price; the Portfolio's average cost is wrong in the user's favour and never corrects, because nothing re-reads the trade. Worked example of (2), replicating amm.js:47 against launch.bas:44-59 on a curve with fee 50 bps, tax 200 bps and d = 900,000 overshooting a 101,000-token remainder: the contract consumes 346,456 and refunds 553,544; the app records `in` = 900,000 — a cost basis 2.6× too high.

**Root cause.** The quote object is the only structured record of the trade in the app, so it was the convenient thing to persist; the settlement read that would give the real figures is used for a yes/no answer and then thrown away.

**Fix.** Use the balances already read: `units = b1 - b0` for a buy (and the quote-asset delta for a sell) and write those into the ledger; fall back to the quote only when balanceAfter returns null, and mark that record as estimated so the Portfolio can say so.

**Regression test.** Simulator test: place a buy, have a second buy land in the same block so the fill is below the quote, then assert the stored local trade's `out` equals the wallet's balance delta.

**Verifier.** app/app.js:213 `D.addLocalTrade({ ... in: amount.toString(), out: q.out.toString(), ... })` where q is the pre-signature quote from A.launchBuy/A.poolBuy and `amount` is the full amount sent. The contract only guarantees min_out (pool.bas:165 `IF out == 0 || out < min_out THEN GOTO 900`, launch.bas:130), and app.js:197 accepts a min_out as low as q.out minus 10%. On a graduating buy launch.bas:50-59 recomputes `used` and refunds d − used at line 59. I reproduced the worked example against the real amm.js: with fee 50 bps, tax 200 bps, rd 1e6, ra 4e5, rem 101000 and d 900000, the contract consumes used = 346456 and refunds 553544, while the app records in = 900000 — a cost basis 2.598× too high. app.js:144 reads b0 and app.js:163 reads the balance after, but only compares them for direction at app.js:163 (`executed = ... b1 > b0`).

---

### `DATA·D3-11` — The formatter and the risk thresholds are DERO-magnitude constants applied unchanged to XMR and ZEC amounts

**P2 · correctness / display · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/stats.js:121 `fmtDeroShort = x => (x >= 1e6 ? ...M : x >= 1e4 ? ...K : x >= 100 ? toFixed(0) : x >= 1 ? toFixed(2) : x > 0 ? x.toFixed(4) : "0")` — four decimals is the floor. app/home.js:24 `if (s.m.liq < 20)` fires the 'Thin market' warning. app/stats.js:72 `m.hot = f1.blocks * 4 + pub1.who * 3 + Math.min(50, Math.log1p(f1.vol) * 8)`, and home.js:12 gives a HOT pill at `m.hot > 12`.

**Problem.** Evaluated on the figures the b5af173 commit message itself cites for the shipped simulator's BRW/XMR pool (liquidity 2.0619e-7 XMR): fmtDeroShort renders '0.0000'. Any wrapped-market amount below 1e-4 collapses to '0.0000' and is indistinguishable from an empty market. The thin-market rule fires for any pool holding under 10 XMR a side — i.e. every plausible wXMR market, including deep ones. The hot volume term gives 0.016 for 0.002 XMR of flow and saturates at 50 for 2,000 DERO, so wrapped markets can essentially never earn a HOT pill or lead Trending, whatever they trade.

**Failure scenario.** Precondition: a market quoted in a 12- or 8-decimal vault asset. Steps: open Markets. Impact: Volume and Liquidity columns read '0.0000' on a market that is trading; every wrapped market carries a 'Thin market' warning whose stated figure is '0.0000 XMR'; ranking systematically buries the wrapped markets the product exists to serve.

**Root cause.** These three constants were tuned when every amount on screen was DERO at 5 decimals. They are magnitude comparisons, so unlike a label they cannot be fixed by naming the asset.

**Fix.** Make fmtDeroShort take the decimals (or a significant-figure mode — fmtPriceSub at stats.js:113 already handles small numbers correctly and could be reused). Express the thin-market rule and the hot volume term as a fraction of that quote's own scale (e.g. liquidity relative to 10^quoteDec) rather than an absolute float.

**Regression test.** Assert the rendered Liquidity cell for a pool holding 0.5 XMR is not '0.0000', and that a pool holding 9 XMR a side does not raise the thin-market warning.

**Verifier.** app/stats.js:121 ends `... : x > 0 ? x.toFixed(4) : "0"` — four decimals is the floor. app/home.js:24 `if (s.m.liq < 20) out.push({ k: "thin", ... })`, and since liq counts both sides, that fires on any pool holding under 10 XMR a side — a deep market by any measure. app/stats.js:72 `m.hot = f1.blocks * 4 + pub1.who * 3 + Math.min(50, Math.log1p(f1.vol) * 8)` with vol in whole quote units: reaching the 50 cap needs e^6.25 − 1 ≈ 517 XMR of one-hour flow against 2,000 DERO. home.js:12 gives the HOT pill at m.hot > 12 and feed.js:267 ranks Trending on it, so the ranking systematically buries the wrapped markets. These are magnitude comparisons and, as the finding says, cannot be fixed by naming the asset.

---

### `DATA·D3-15` — Create a pool cannot succeed: the install omits the `quote` argument pool.bas v2 requires

**P2 · correctness / broken feature · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/forms.js:138 `installAndRegister("pool", [["asset", "H", a], ["treasury", "S", D.registry.treasury], ["fee", "U", ...], ["pfee", "U", ...]], "RegisterPool")` — four arguments. contracts/pool.bas:15 `Function InitializePrivate(asset String, quote String, treasury String, fee Uint64, pfee Uint64)` — five. tests/pool_test.go:73 passes `harness.H("quote", zeroHash)`; the app does not. vendor/derohe/dvm/dvm.go:305-307: a missing parameter returns `Argument "quote" is missing while invoking "InitializePrivate"`.

**Problem.** The gas estimate in app.tx (app.js:126) runs the install and receives that error, so app.js:133 toasts 'Install pool would fail: Argument "quote" is missing…' and returns null. No transaction is sent and no money is lost — but the Create-a-pool form has been non-functional since pool.bas took its quote parameter, and the failure surfaces as a raw DVM string rather than as a known defect.

**Failure scenario.** Precondition: any user. Steps: Markets → Create a pool → enter a valid asset SCID → Create. Impact: the flow always fails. Had the estimate been skipped (app.js:120, the `opts.fee` path, or the 'Bucket cannot start' fallback at app.js:135), the install would have been sent, reverted, and the fee spent for nothing — vendor/derohe/blockchain/transaction_execute.go:258 discards all trees on error, so no code would land.

**Root cause.** pool.bas gained a fifth parameter (docs/TODO.md §0 records it as DONE) and the launch form was updated to offer a quote; the pool form was not, and nothing checks the app's argument list against the template it is about to install.

**Fix.** forms.js:138 → add `["quote", "H", quote.scid]` and give F.pool the same quote chips F.launch has (forms.js:41-53), so a pool can be created over a wrapped quote — which is the whole point of the quote-asset programme.

**Regression test.** A test that reads each templates/*.bas InitializePrivate parameter list and asserts the corresponding installAndRegister call site supplies exactly those names.

**Verifier.** app/forms.js:138 `installAndRegister("pool", [["asset", "H", a], ["treasury", "S", D.registry.treasury], ["fee", "U", ...], ["pfee", "U", ...]], "RegisterPool")` — four arguments. The template the app actually fetches, app/templates/pool.bas:1, is `Function InitializePrivate(asset String, quote String, treasury String, fee Uint64, pfee Uint64)` — five, matching contracts/pool.bas:15. vendor/derohe/dvm/dvm.go:305-307 returns `Argument "%s" is missing while invoking "%s"`. I traced the estimate path: app.js:130 calls DERO.GetGasEstimate; vendor/derohe/cmd/derod/rpc/rpc_dero_estimategas.go:75 returns early on any SCInstall error, and SCInstall (vendor/derohe/dvm/simulator.go:117) propagates Execute_sc_function's error. So app.js:135 toasts the raw DVM string and returns null. The fail-closed claim also checks out: the `Bucket cannot start` branch at app.js:134 does not match this message, and vendor/derohe/blockchain/transaction_execute.go:257 confirms "if any error occurs we dicard all trees". tests/pool_test.go passes the quote argument; the app does not.

---

### `PERF·F10` — Zero JavaScript tests; the app's entire pricing and display layer is untested, and that is how F1 shipped

**P2 · test-coverage · auditor confidence Confirmed · verifier *(unverified — id collision)***

**Evidence.** `ls tests/` returns 25 Go files and no JavaScript. `tests/run.sh:24` is `go test -mod=vendor -count=1 ./veil/...`. A repo-wide find for `*.test.*` and `*_test.mjs` outside vendor/ returns nothing. tools/uitest/ui.mjs is a Playwright smoke pass (every tab renders without console errors), not an assertion suite over values. app/amm.js, app/stats.js and the pure parts of app/feed.js are dependency-free, DOM-free, deterministic functions — I loaded them unmodified into a Node VM context in ten lines (scratchpad/bench.mjs).

**Problem.** The Go tests prove the contracts are right. Nothing proves the browser reports them correctly. F1 is the demonstration: a fix landed in A.price, two of four call sites were updated, and a market showed -100.00% change and a chart seven orders of magnitude off the price above it, with the full test suite green.

**Failure scenario.** Precondition: any change to amm.js, stats.js or feed.js's derivation code. Steps: run tests/run.sh, see green, ship. Impact: F1 reached the shipped app. So did the dead fields in F7 and the dead read in F11. Every future change to how a price, change, candle or volume figure is computed carries the same risk.

**Root cause.** The test strategy stops at the chain boundary. The app has no runtime dependencies and no build step, which makes it unusually easy to test in Node — and that ease is precisely why no harness was ever felt to be needed.

**Fix.** Add `tests/js/` with plain `node --test` files that load app/amm.js and app/stats.js into a vm context (the loader is ten lines, already written in scratchpad/bench.mjs). First tests: the F1 assertions; A.price against known reserves at 5, 8 and 12 quote decimals; launchBuy/launchSell/poolBuy/poolSell against the same fixtures the Go pool_test.go and launch_test.go already use, so the BigInt mirror in amm.js is proven to match the DVM it claims to mirror. No new dependency — node --test is built in, which satisfies the project's dependency rule.

**Regression test.** This finding is the regression test.

**Verifier.** I recomputed every ratio from the hex values rather than trusting them. app/styles.css:9 --fg3:#63676a has relative luminance 0.1339; against --bg:#000 (styles.css:6) that is 3.68:1 and against --s1:#0a0b0a (L=0.00325) it is 3.45:1 — both below the 4.5:1 AA threshold for text under 24px. --fg2:#9a9d97 computes to 7.64:1, --acc:#b4f03f to 15.48:1 and --graphite:#4b525a to 2.65:1, exactly as claimed. Every cited selector exists at the cited size: .eyebrow/.label 10.5px (styles.css:50), .note 12.5px (:56), .tab 9.5px (:85), .stat .l 10px (parts.css:48), .mini .l 10px (parts.css:83), .msg .when 10.5px (parts.css:127), .empty 13px (ui.css:68), .chip 12.5px (ui.css:29), .chg 12.5px (ui.css:39), and .step.todo{opacity:.55} (parts.css:204).

---

### `PERF·F11` — A Settings toggle that does nothing, a CSP allowance for a client that does not exist, and five dead fields

**P2 · dead-code · auditor confidence Confirmed · verifier *(unverified — id collision)***

**Evidence.** app/screens.js:249 — `U.toggle("Use indexer read cache", !!st.indexer, (on) => V.app.saveSettings({ indexer: on }), "Optional Gnomon cache. The app always falls back to reading the node.")`. app/config.js:25 — `indexer: { enabled: false, url: "http://127.0.0.1:8082" }`. app/app.js:11 defaults `indexer: false`. Grepping all of app/ for `indexer` returns exactly those three lines — nothing reads the setting or the config. app/index.html CSP `connect-src` includes `http://127.0.0.1:*` and `http://localhost:*`, plain HTTP to any local port, which no shipped code uses. app/rpc.js:126 — `cache: new Map(),` on the node facade, never read anywhere. app/home.js:142 — `changePill(hold.change24)`, but app/app.js:95 sets `app.holdings = { dero, rows, totals, at }` with no change24, and app.js:92-94 explicitly documents removing it. tests/zzquote_test.go:3 — "TEMPORARY measurement + smoke test for the quote-asset-parameter study. Delete after reading."

**Problem.** A user-facing switch that changes nothing while its help text describes a fallback behaviour the app does not have is a control that lies. The CSP allowance is a real widening of the network surface kept alive only for that dead feature — the project rule is fail closed, and a policy broader than the code needs is the opposite. `hold.change24` renders a permanent em-dash pill next to the wallet balance on Home. `V.node.cache` is a Map nobody touches. And a test file that says to delete it is still in the suite tests/run.sh executes.

**Failure scenario.** Precondition: none. Steps: open Settings and toggle "Use indexer read cache". Impact: nothing happens, ever, in either position; the app writes a flag no code reads. Meanwhile the shipped CSP permits plain-HTTP connections to every port on localhost for a client that does not exist. On Home, a connected wallet's balance row shows a grey "—" change pill that can never show anything else.

**Root cause.** A planned Gnomon indexer path was removed (correctly — a server would violate the no-backend rule) and its config, its setting, its UI control and its CSP grant were all left behind.

**Fix.** Delete screens.js:249, the `indexer` key from config.js:25 and its generator in tools/genconfig, the `indexer: false` default in app.js:11, and `http://127.0.0.1:*` / `http://localhost:*` from the index.html connect-src (ws:// and wss:// are what the node facade actually uses — rpc.js:99-111 refuses anything else). Delete rpc.js:126. Delete `hold.change24` from home.js:142, or restore the field if a per-quote 24 h change is wanted, but do not leave a read with no writer. Delete tests/zzquote_test.go and tests/zz_quoteprobe_test.go, both self-described investigations.

**Regression test.** A grep-based check in tests/run.sh (or the JS suite from F10) asserting every key in VEIL_CONFIG has at least one reader in app/, and every `connect-src` scheme in index.html corresponds to a scheme endpointURL() accepts.

**Verifier.** app/feed.js:239 sets lastRefresh at the end of the success path only; app/app.js:71 catches with `console.warn("refresh", e)` and nothing else. home.js:156, home.js:166, markets.js:221, nfts.js:117 and nfts.js:131 all branch on `D.lastRefresh` with U.skeleton on the falsy side, so a permanent failure pins the skeleton branch. grep for /retry|try again|reconnect/ across app/*.js returns nothing — I ran it. Every error surface (wrap.js:189, chat.js:123, token.js:35, ui.js:143, nft.js:8, screens.js:189) renders an .errbox and stops; the one exception with an action is the endpoint-confirmation warnbox at screens.js:246. app.js:31 sets the header pill to "no node"/"pill down".

---

### `PERF·F12` — markets.js documents a 'KNOWN GAP' in pair grouping that no longer exists, and duplicates feed.js's impersonation rule in token.js

**P2 · correctness · auditor confidence High · verifier *(unverified — id collision)***

**Evidence.** app/markets.js:32-35 — "KNOWN GAP, and it belongs to feed.js, not here: loadContract() reads `qt` only in its pool branch, so every launch arrives with s.quote unset". app/feed.js:159-160 reads `qt` and sets `s.quote` BEFORE the `if (kind === "launch")` branch at feed.js:161, so launches do get s.quote. Separately, app/token.js:35 recomputes `lookalike`, `dupTicker` and `impersonates` inline for a deep-linked unlisted contract, duplicating the rule that app/feed.js:212-220 implements for listed ones — and the two are not the same rule: feed.js marks BOTH members of a colliding pair (feed.js:215), token.js only marks the one being viewed.

**Problem.** The stale comment will send a reader to 'fix' something already fixed, or make them distrust grouping that is in fact correct. The duplicated impersonation rule is worse: it is security-relevant display logic living in two places with two behaviours, and the deep-link route — the route a shared link always takes — has the weaker one.

**Failure scenario.** Precondition: someone opens a contract id directly (`#token/<scid>`) that is not in the registry and whose ticker collides with a listed market. Steps: view it. Impact: token.js:35's `dupTicker` check marks the viewed contract but never marks the listed original, and the group-wide `wrappedBase` exemption at feed.js:219 is not reproduced — so the two code paths can disagree about whether the same pair of markets is a collision. Any future change to the impersonation rule must be made twice, and nothing enforces that.

**Root cause.** token.js needs the decorated fields for a contract that never went through decorate(), so it reimplemented the decoration inline instead of calling it.

**Fix.** Delete the stale comment at markets.js:32-35. Extract feed.js's per-asset decoration into `data.decorateOne(s, peers)` and call it from both decorate() (feed.js:212-220) and token.js:35, so there is one rule.

**Regression test.** Node test: build a listed wrapped-asset market plus an unlisted impersonator, run decorate() and token.js's inline path, assert both mark both markets identically.

**Verifier.** app/home.js:104-106 picks leadSym as the largest-volume quote and :120-121 renders it as the hero; on the shipped sim data that is DERO (the markets summary reads "24h flow 0.0000 XMR · 15.28 DERO"). home.js:167 feeds the tape from D.trending(14) and .sim/ui-dec/desk-markets.png shows the tape as seven consecutive "BRW" tickers with no DERO/XMR/ZEC price line — docs/DESIGN_DIRECTIVE.md:304 specifies exactly the opposite. index.html:35 labels the nav "Wrap" while wrap.js:225 titles the screen "Deposit & withdraw XMR". tools/telainstall/main.go:82 sets the INDEX description to "DMZ: social DEX and launchpad for DERO assets".

---

### `PERF·F13` — The registry's full id list is refetched every block although its counts say it did not change, and markets.js retains a DOM row for every market ever listed

**P2 · performance · auditor confidence Confirmed · verifier *(unverified — id collision)***

**Evidence.** app/feed.js:79-87 `registry_()` reads `pc`/`lc`/`nc` and then unconditionally `await this.fetchKeys(cfg.factory, want)` for pool_0..pc, launch_0..lc, nft_0..nc — every block, via refresh() at feed.js:228. The ids are append-only and immutable; the counts are already in hand. app/markets.js:117 — `const rows = new Map();` at module scope, populated at markets.js:131, never cleared or pruned. app/home.js:66 — `const seen = new Set();` accumulates one `scid:n` string per feed item ever rendered, unbounded (unlike feedEls at home.js:87, which is capped at 400).

**Problem.** One redundant RPC per block carrying up to 250 keys, plus unbounded retention of detached DOM. Each retained markets row holds a full SVG sparkline path, which per F4 can be 43 KB of string.

**Failure scenario.** Precondition: long-lived session on a deployment where markets come and go. Steps: browse for hours. Impact: `rows` accumulates a ~15-element subtree plus a large path string for every scid ever listed; 500 markets over a session is on the order of 20 MB of detached DOM that nothing can collect. `seen` grows one string per trade ever displayed. Neither is fatal — both are bounded by real registry size — but neither has a reason to be unbounded.

**Root cause.** registry_() has no incremental path. rows/seen were written as caches with no eviction policy.

**Fix.** In registry_(), cache the previous `{pc, lc, nc}` and the resulting id arrays; skip the fetchKeys entirely and reuse the arrays when all three counts are unchanged, fetching only the newly appended range otherwise. In markets.js draw(), after computing `items`, drop entries from `rows` whose scid is not in `D.assets`. Bound `seen` the same way feedEls is bounded, or drop it and derive freshness from `feedEls.has(key)`.

**Regression test.** Playwright frame count: assert the number of DERO.GetSC calls per steady-state block equals 2 + markets + nfts (no id-list call) once counts have stabilised. Memory: assert `rows.size <= D.assets.size` after a draw.

**Verifier.** app/ui.js:66 builds `background:linear-gradient(135deg,hsl(h1 62% 52%),hsl(h2 70% 34%))` from a FNV hash of the id, and avatar() is called from markets.js:129, home.js:47, home.js:72, home.js (trade hub), token.js:52, screens.js and nfts.js. Both renders confirm it: .sim/ui-dec/02-markets.png and desk-markets.png show a column of saturated multi-hue squares down the left of an otherwise black/off-white/lime screen. docs/DESIGN_SYSTEM.md line 93-94 defines the network grammar as "a two-letter mono label plus a hairline leading edge in its tint. The tint is never a fill", implemented at parts.css:102 and used on wrap and portfolio but on no list row. docs/DESIGN_DIRECTIVE.md:141 bans "generic gradient backgrounds" by name.

---

### `PERF·F14` — The Wrap screen redraws every block from a vault snapshot it read once and never refreshes

**P2 · correctness · auditor confidence High · verifier *(unverified — id collision)***

**Evidence.** app/wrap.js:465-468 — `reload(false)` runs once when the screen mounts, then `D.onChange(() => { if (... name === "wrap" && D.vault) { tip = (V.live && V.live.height) || tip; draw(D.vault, lastMine); } })`. The handler advances the block height and redraws, but never calls `D.loadVault()` or `D.myVault()` again. The only other reload paths are the explicit `reload(true)` calls after a Register (wrap.js:433) or Burn (wrap.js:448).

**Problem.** This is an RPC saving that buys staleness. `halted`, `sealed`, `sup`, `headroom`, `wn` and the user's own withdrawal records are read once and then displayed as live for as long as the tab stays on that screen, under a block counter that keeps ticking — which makes stale numbers look freshly read.

**Failure scenario.** Precondition: user sits on the Wrap screen. Steps: the federation halts the vault, or another user's deposit consumes the remaining `headroom`, or the user's withdrawal reaches its release height and is paid. Impact: none of it appears. The screen keeps showing an un-halted vault with headroom available and a pending withdrawal, with a block number updating beside it. A user could compose a deposit against a halted or full vault and pay gas for a call that reverts. The one screen whose whole purpose is to state the federation's current state is the one screen that does not re-read it.

**Root cause.** The block handler was written to make the release-height countdown tick (its comment says so) and the vault re-read was not brought along with it.

**Fix.** Have the block handler call `reload(false)` rather than `draw(D.vault, lastMine)`. That is 2 RPC calls per block while the Wrap screen is open (loadVault's single keyed read plus myVault's two), which is negligible against the 143 the app already makes. If even that is unwanted, re-read on every Nth block and never let the displayed block number advance past the height the data was read at.

**Regression test.** Playwright against the simulator: open Wrap, halt the vault from tools/simvault, advance a block, assert the screen shows the halt without navigating away.

**Verifier.** The breakpoint conflict is confirmed from source: app/styles.css:32 `.app{max-width:430px}` with the desktop shell only at desk.css:16 `@media (min-width:960px)`, while parts.css:96-99 switches .stats4 to four columns at min-width:720px and parts.css:151 switches .msg back to its wide 58px+104px+1fr grid above 720px. Between 720 and 959 the container is still 430px, so four .stats4 cells share ~398px; .stat .v carries white-space:nowrap;overflow:hidden;text-overflow:ellipsis (parts.css:49), so values will ellipsise. stats4 is used at nfts.js:39, nft.js:27 and screens.js:209 as claimed. tools/uitest/ui.mjs opens 430 (lines 26/38), 1280 (197) and 1024 (210) only, and docs/DESIGN_DIRECTIVE.md:422 and :481 both require 390 and 768.

---

### `PERF·F3` — D.feed() materialises and sorts every trade of every market on every block to render 24-60 rows

**P2 · performance · auditor confidence Confirmed · verifier *(unverified — id collision)***

**Evidence.** app/feed.js:271-279 — `feed(filter, me)` loops `for (const s of this.assets.values()) for (const t of s.trades)`, does `out.push(Object.assign({ asset: s }, t))` per trade, then `out.sort(...)` over the whole array. Called at app/home.js:99 on every draw, and home's draw is wired to every block at app/home.js:172 (`D.onChange(() => ... draw())`). The result is used at home.js:109 (a 24h count), home.js:164 (`feed.length`) and home.js:165 (`feed.slice(0, wide ? 60 : 24)`).

**Problem.** One allocated object per trade in the whole registry, plus a comparison sort over all of them, ~every 18 seconds, to display at most 60 rows.

**Failure scenario.** Precondition: Home open, a deployment at the in-memory caps. Steps: leave the tab on Home. Impact: measured 133.7 ms per block at 120 markets x 2,000 trades (240,000 trades), 20.4 ms at 60 x 800, 1.6 ms at 20 x 200. On a mid-range phone the first figure is 0.4-0.7 s of main-thread block every 18 s — taps drop, the rolling-number animations from live.js stutter, and the page is unresponsive for a visible fraction of every block.

**Root cause.** The function returns a fully materialised, fully sorted list because two of its three consumers want aggregate counts. It has no notion of how many rows the caller will actually draw.

**Fix.** Give feed() a limit and select rather than sort. Keep a bounded max-heap (or just an insertion into a 60-element array) keyed on `height, n` while scanning, and only build the `{asset, ...t}` wrapper for entries that survive into it. Return `{items, total, day}` so home.js:109 and :164 get their counts from the same single pass instead of from a full array. Complexity added: about 15 lines in one function, no new state.

**Regression test.** Node bench asserting D.feed cost stays sub-linear in total trades: build 120 markets x 2,000 trades, assert the call returns in under, say, 15 ms and that the first 60 entries are byte-identical to the current full-sort implementation's first 60.

**Verifier.** app/markets.js:142 appends one bare "⚠" for `dup || s.dupTicker || s.lookalike || s.impersonates`, and :144 puts the only differentiated explanation in `p.name.title`. app/home.js:12 H.badges has curve/AMM/pool, NEW and HOT and no impersonation case, so H.marketRow (Trending, Movers, Trade hub) shows nothing; H.feedItem (home.js:68-84) renders the ticker as a plain button with no mark. The strong copy exists only in ui.js:90 verified(), ui.js:103 warnings() and home.js:21 H.risks. The "~90% of rows" claim is backed by .sim/ui-dec/02-markets.png: 22 of 24 rows wear the identical glyph (only ZEC row 21 and one BRW row do not).

---

### `PERF·F4` — Home rebuilds ten sparklines from full point history every block; each is a ~43 KB SVG path drawn into a 120x34 px box

**P2 · performance · auditor confidence Confirmed · verifier *(unverified — id collision)***

**Evidence.** app/home.js:50 — `H.marketRow` calls `sparkline(s.points.filter((p) => p.ts >= S.now() - 86400), s.dec, ...)` unconditionally on every draw; home.js:96 draws `D.trending(6)` and `D.movers(4)` = 10 rows, and home.js:172 wires draw to every block. app/ui.js:119-130 `sparkline()` maps every point, spreads them into `Math.min(...xs)` / `Math.max(...ys)`, and joins one `L x y` segment per point into a single path `d`. Contrast app/markets.js:167-179, which gates exactly this work behind `r.key !== key` and explains in a comment why.

**Problem.** markets.js learned this lesson and home.js did not. A 24 h window at 18 s blocks is up to 4,800 points, capped by MAXP at 4,000 — 4,000 vertices rendered into 120 horizontal pixels, i.e. 33 vertices per pixel, none of which can be distinguished.

**Failure scenario.** Precondition: Home open on markets with dense history. Steps: leave the tab on Home. Impact: measured 69.9 ms per block for the path maths alone at 4,000 points/market (17.5 ms at 1,000; 3.5 ms at 200), producing ten path strings of 43,285 characters each — ~430 KB of SVG geometry handed to the renderer per block, for ten thumbnails. The DOM parse and rasterise cost sits on top of the 69.9 ms and I did not measure it.

**Root cause.** sparkline() draws every point it is given and no caller downsamples. The 24 h filter bounds the time window but not the vertex count.

**Fix.** Two independent one-liners, either of which helps and both of which should land. (a) In ui.js sparkline, after the filter: if `s.length > 160`, keep every `Math.ceil(s.length/160)`-th point plus the last. A 120 px box cannot show more, so this is lossless on screen and cuts the work ~25x. (b) In home.js:48, cache rows by scid and gate the sparkline on `s.sig` exactly as markets.js:171-179 does. Prefer (a): it is smaller, it fixes every caller including markets.js's changed rows and the token page, and it adds no state.

**Regression test.** Node bench: assert sparkline path length stays under ~2 KB regardless of input point count, and that the rendered polyline's min/max y still match the unsampled series to within one pixel.

**Verifier.** I enumerated every Function in contracts/vault.bas. The only writers of `w_<j>_st` are Released (file:394, requires issigner(s) and votek reaching k on a foreign txid) and Veto (file:439, also k). There is no height-triggered release, no timeout refund, no permissionless claim. wrap.js:337-338 and :386 assert the opposite as fact, inside the "What you are trusting" panel, and .sim/ui-dec/02-wrap.png renders it: "doing nothing releases a withdrawal, it never cancels one."

---

### `PERF·F5` — A backgrounded tab keeps doing full per-block refreshes; the hidden-tab back-off the author wrote is bypassed

**P2 · performance · auditor confidence High · verifier *(unverified — id collision)***

**Evidence.** app/app.js:72 — `timer = setTimeout(() => app.refresh(), document.hidden ? 120000 : 45000);` shows the intent to back off when hidden. app/app.js:74 — `L.onBlock(() => app.scheduleRefresh(200));` and app/app.js:68 `scheduleRefresh` does `clearTimeout(timer)` first, so the block clock overrides that safety net on every block. app/live.js:10-14 — `poll()` reschedules itself with a bare `setTimeout(..., 2500)` and has no visibility check. app/live.js:10 also starts `setInterval(() => this.tickAges(), 1000)` unconditionally.

**Problem.** The only line that respects `document.hidden` is a fallback that the block-driven path clears before it can fire. So a hidden tab keeps polling DERO.GetHeight every 2.5 s and keeps running the full ~143-call, ~240 ms refresh on every block, indefinitely.

**Failure scenario.** Precondition: DMZ open in a background tab, which is the normal state for a trading page. Steps: switch to another tab and leave the browser open. Impact: sustained ~8 RPC/s against the user's node and ~240 ms of JS per block, per open tab, for nothing anybody can see. On a laptop this is measurable battery drain; on a remote wss node it is continuous bandwidth the user did not ask for.

**Root cause.** Two independent refresh triggers (the block clock and the safety-net timer) with the visibility policy expressed on only one of them.

**Fix.** In app.js:74 make the block handler visibility-aware: `L.onBlock(() => { if (!document.hidden) app.scheduleRefresh(200); });`. The existing `visibilitychange` handler at app.js:282 already does `scheduleRefresh(300)`, so the screen is correct within 300 ms of coming back. Optionally also skip `tickAges` while hidden (live.js:16) — nothing it writes is visible.

**Regression test.** Playwright: open the app, count DERO.GetSC calls over 60 s visible, then hide the page (CDP Emulation.setPageVisibility or a background context) and assert the call count over the next 60 s is near zero.

**Verifier.** Two of the three claims hold. app/wrap.js:233 really is `flag(halted ? "down" : "acc", …)`, and grep confirms capratio, minbond and winmax are fetched at wrap.js:21-22 and used nowhere; headroom (wrap.js:42) is only printed at :239. contracts/vault.bas Mint line 41 (file:317) refuses over cap and line 59 (file:328) refuses a full mint window. tools/vaultsigner/main.go:771-774 does print "waits for headroom" to a console nobody reads.

---

### `PERF·F7` — enrich() rebuilds a Set over every trade of every market, every block, for one number shown on one screen

**P2 · performance · auditor confidence Confirmed · verifier *(unverified — id collision)***

**Evidence.** app/stats.js:68 — `holders: new Set(s.trades.map((t) => t.addr)).size,` inside `enrich()`, which app/feed.js:221 runs for every asset on every refresh. The only read of that field in the entire app is app/token.js:73 — `mini("Traders", String(m.holders))`.

**Problem.** A full-history array map plus a Set build, per market, per block, whose result is displayed only when the user is on one particular market's page.

**Failure scenario.** Precondition: any deployment with trade history. Steps: normal use, any screen. Impact: measured 9.4 ms per block at 120 markets x 2,000 trades — 27% of enrich's total 35.5 ms — and 2.0 ms at 60 x 800. Pure waste on 119 of 120 markets.

**Root cause.** enrich() computes one metric set for all consumers, and the most expensive member of it has exactly one consumer.

**Fix.** Delete the field from enrich() and make it a lazy getter or a direct computation in token.js's drawHead: `new Set(s.trades.map(t => t.addr)).size`. It is already inside a `s.sig !== lastSig` gate there (token.js:130), so it will be computed only when that one market actually changes.

**Regression test.** Node bench asserting `S.enrich` cost per market is independent of `s.trades.length` for the fields that remain.

**Verifier.** app/token.js:216 closes the sheet before app.trade is awaited. app/app.js:151-165 is toast → toast → `await app.waitTx(txid)` → toast, and app.js:177 loops 180 × 1500 ms = 270 s. app/ui.js:41 removes every toast on a timer and .toast (app/ui.css:117) has no dismiss control. app.js:139 blocks concurrency with a message that names nothing. I grepped: there is no transaction list, no persistent in-flight surface, and no state machine anywhere in app/.

---

### `PROD·P7-05` — "Locked forever" liquidity is drainable by the creator: in tmode 1 every sell takes up to 10% of the AMM's quote reserve as creator tax, and the token page states the lock without qualification

**P2 · rug economics · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** contracts/launch.bas phase-1 sell path: `204 LET dout = muldiv(aeff, rd, ra + aeff)` / `205 LET tx = bps(dout, LOAD("tax"))` / `206 LET out = dout - tx` / `210 STORE("rd", rd - dout)` / `212 STORE("tacc", LOAD("tacc") + tx)`. The pool's quote reserve falls by the whole `dout`; the seller receives `dout - tx`; the difference accrues to `tacc`, which `ClaimTax` pays to `LOAD("creator")` when `tmode == 1`. `tax` is capped at 1000 bps (contracts/launch.bas line 26) and is set by the creator at install with no expiry and no off switch. Against that, app/token.js's Info tab renders `kv("Liquidity", s.phase === Z ? "curve (virtual)" : "contract-owned, locked forever")`, and app/token.js:63 tells the buyer "At 100% the curve becomes a permanently locked AMM at the same price." app/forms.js:95 opens the launch sheet with "Nothing to run, nothing to rug."

**Problem.** The lock is real in the sense that no entrypoint returns reserves to a depositor — there is no RemoveLiquidity in launch.bas, which is genuinely stronger than a timed lock. But a 10% tmode-1 tax is a permanent, uncapped siphon out of that same reserve, one sell at a time, payable to one address. Over enough round-trip churn the creator extracts an unbounded fraction of the locked liquidity while the UI calls it locked forever. The risk box only speaks above 5% (app/home.js `H.risks`: `if (s.kind === "launch" && s.tax > 500n)`), so a 5.00% tax — a 10% round-trip drag plus a continuous reserve drain — produces no warning at all on the token page.

**Failure scenario.** Precondition: a launch installed with tax = 1000, tmode = 1, which the wizard offers directly (app/forms.js: "Tax % of each trade's DERO leg (≤10)" with a "Creator" mode chip). Steps: the curve fills, the AMM opens, ordinary trading begins. Every sell removes `dout` from `rd` and hands 10% of it to the creator's `tacc`; the creator calls ClaimTax whenever they like. Impact: the reserve backing every remaining holder shrinks monotonically with volume, into one wallet, from a market page that says the liquidity is locked forever and a launch sheet that says there is nothing to rug.

**Root cause.** Two separate mechanisms — an irrevocable liquidity lock and an irrevocable creator tax — were reasoned about separately. The lock is described as a safety property without stating what it does not protect against; the tax's disclosure threshold (5%) was set for the trade-sheet cost line, not for the reserve-drain effect.

**Fix.** Say the true thing on the token page: when `tmode == 1 && tax > 0`, render Liquidity as "contract-owned and not withdrawable, but the creator takes <tax>% of every sell out of this reserve". Lower the `H.risks` tax threshold to `> 0n` for tmode 1 specifically — any non-zero creator tax is a standing claim on other people's money and belongs in the amber box, not only in an Info row. Remove or qualify "Nothing to run, nothing to rug" at app/forms.js:95; a launchpad that permits a 10% creator tax and a 5% instant allocation cannot make that claim.

**Regression test.** A DVM test: install with tax=1000/tmode=1, graduate, then perform N sells and assert `rd` has fallen by more than the sum paid to sellers, and that `ClaimTax` transfers the difference to the creator. A UI test asserting a tmode-1 market with tax=100 renders a risk line.

**Verifier.** The mechanism is exactly as described. contracts/launch.bas phase-1 sell (file:228-236): `dout = muldiv(aeff, rd, ra + aeff)` / `tx = bps(dout, LOAD("tax"))` / `out = dout - tx` / `STORE("rd", rd - dout)` / `STORE("tacc", tacc + tx)` — the reserve pays out the full dout and the seller receives dout − tx. ClaimTax (file:263-267) pays LOAD("creator") when tmode == 1. tax is capped at 1000 bps at install (file:16) with no expiry and no off switch, and I grepped the whole file: there is no RemoveLiquidity, so the "locked" half of the claim is genuinely true. token.js:106 renders `kv("Liquidity", … "contract-owned, locked forever")` and token.js:63 promises "a permanently locked AMM". home.js:22 gates the tax risk on `s.tax > 500n`, so exactly 5.00% produces no warning at all.

---

### `PROD·P7-06` — Trending is bought with gas: the `hot` score is dominated by an unbounded per-block term that one address can drive alone, and its own comment claims the opposite

**P2 · ranking / abuse · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/stats.js:70 `m.hot = f1.blocks * 4 + pub1.who * 3 + Math.min(50, Math.log1p(f1.vol) * 8);` with the comment above it: "anti-wash: distinct publishers count, not published trades (one account spamming posts gains nothing), and volume enters through a log so a single large reserve move does not own the ranking". `f1.blocks` comes from app/stats.js `flow(s, 3600)`, which counts every block in the last hour where `b.rd !== a.rd` — one address's dust trade qualifies. `hot` is the default sort and the discovery surface everywhere: app/feed.js:267 `trending()`, app/markets.js:13 `let filter = "trending", sort = "hot"`, app/home.js Trending section, app/home.js Trade hub (`D.active().sort((a, b) => b.m.hot - a.m.hot)`), and the ticker tape (`L.tape(U.$("#tape"), D.trending(14))`). The HOT pill fires at `s.m.hot > 12` (app/home.js `H.badges`). `active()` is `rd > 0n` — a market holding one atomic unit qualifies.

**Problem.** At ~18 s blocks there are 200 blocks in an hour, so a single wallet trading dust once per block scores 800 from the `blocks` term alone, against a hard ceiling of 50 for volume and 3 per distinct publisher. The anti-wash design lives entirely in the two small terms; the large term has no cap and no distinct-address requirement. The `publish` flag on every swap (contracts/launch.bas `post(..., publish, ...)`) means the visible trade log and the ranking are driven by different data, so a manipulator can top Trending while publishing nothing.

**Failure scenario.** Precondition: any launch, however thin. Steps: the creator runs a script that buys and sells the minimum non-reverting size once per block for an hour, publish=0. Impact: `hot` exceeds 800, the market takes rank 1 on Home Trending, the Markets table's default sort, the Trade hub list and the tape, and wears a HOT badge — for the cost of ~200 DERO transaction fees and no meaningful capital. Volume is inflatable at similar cost: `flow()` sums |Δrd| per block, so a buy in block N and a sell in block N+1 books roughly 2x notional of 'Volume 24h' for a round-trip fee of about 1%, i.e. about 200 units of displayed volume per unit of fee spent. There is no paid placement on DMZ — I checked; ranking is purely organic — which makes this worse, not better: there is no honest paid lane and no way for a reader to tell a bought position from an earned one.

**Root cause.** The metric was tuned for liveliness (activity per block reads as a real market) rather than against an adversary who controls how many blocks contain a transaction. The comment records the intent of two terms and does not notice the third dominates them.

**Fix.** Make the dominant term adversary-bounded: count distinct trading addresses per block rather than blocks, or weight `f1.blocks` by `min(1, f1.vol / liq)` so churn on a dust reserve scores nothing. Exclude markets below a liquidity floor from Trending and from the HOT badge entirely (the data is already there — `m.liq`, and `H.risks` already calls under-20 'thin'). And correct the comment, which currently asserts a defence the code does not provide.

**Regression test.** A test that synthesises 200 single-address dust blocks on a 1-unit-reserve market and asserts it does not enter `D.trending(6)` and does not receive a HOT pill.

**Verifier.** app/stats.js:72 is `m.hot = f1.blocks * 4 + pub1.who * 3 + Math.min(50, Math.log1p(f1.vol) * 8)` under the comment at :70-71 claiming anti-wash properties. flow() (stats.js:37-49) counts any block where `b.rd !== a.rd` and lpMove() is false — a one-atomic-unit trade qualifies, and there is no distinct-address requirement on that term. At ~18 s blocks the term's ceiling is ~800 against 50 for volume and 3 per distinct publisher, so it dominates by more than an order of magnitude. hot is the default everywhere: markets.js:15 `let filter = "trending", sort = "hot"`, feed.js:267 trending(), home.js Trending and the Trade hub sort, live.js tape via D.trending(14), and home.js:12 fires the HOT pill at hot > 12. contracts/launch.bas post() takes a `publish` flag, so the ranking input and the visible trade log are genuinely different data.

---

### `PROD·P7-08` — The Markets table ranks and displays volume, liquidity and FDV across different quote assets as bare unlabelled numbers, and the Trade hub sums them and labels the total DERO

**P2 · cross-quote comparison · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/markets.js:81 `const key = { hot: ..., vol: (s) => s.m.vol24, liq: (s) => s.m.liq, mcap: (s) => s.m.mcap || 0, ... }[sort];` then app/markets.js:87 `out.sort((a, b) => key(b) - key(a) || b.m.vol24 - a.m.vol24);` — no quote is consulted. app/markets.js:166 `p.vol.textContent = S.fmtDeroShort(s.m.vol24); p.liq.textContent = ...; p.mcap.textContent = ...` — three desktop columns with no unit, under heads reading "Vol 24h", "Liquidity", "FDV" (app/markets.js:235). app/token.js's Market band does the same: `band(mini("FDV", ...), mini("Liquidity", ...), mini("Volume 24h", ...))`, with no symbol, on a page whose price eyebrow does say "Price · XMR". app/home.js:179 in the Trade hub: `const vol = list.reduce((a, s) => a + s.m.vol24, 0);` then `mini("Flow 24h", S.fmtDeroShort(vol) + " DERO")` — a cross-quote sum, labelled DERO.

**Problem.** Home and Markets both go to real trouble to avoid this (app/home.js:104 and app/markets.js:208 build a per-quote map with the comment "There is no rate between them and there must not be one"), and the Trade hub then does exactly the forbidden thing three screens away. Meanwhile the table sorts a 12-decimal XMR magnitude against a 5-decimal DERO one on raw size, so quote choice, not market quality, decides the ranking. A reader comparing two rows in the Liquidity column is comparing quantities in different currencies with no marking of either.

**Failure scenario.** Precondition: at least one wrapped-quote market alongside DERO markets. Steps: open Markets, sort by Liquidity. Impact: ordering is arbitrary with respect to real depth; the columns invite a comparison they cannot support. On the Trade hub the headline 'Flow 24h' number is a sum of XMR and DERO presented as DERO — a fabricated figure at the top of a trading screen.

**Root cause.** docs/TODO.md 1.3 records this as knowingly open for sorting and totals. The unlabelled columns and the Trade hub's summed DERO label are the parts that are not just a ranking imperfection but a stated-wrong number.

**Fix.** Immediately: append `D.qsym(s)` to the Vol/Liquidity/FDV cells (the phone sub line at app/markets.js:165 already does), and replace the Trade hub's single sum with the per-quote list app/home.js:104 already computes. Then, per docs/TODO.md 1.3, rank within a quote: group the table by quote or make the sort key a tuple of (quote, value) so magnitudes are never compared across assets.

**Regression test.** A test asserting no rendered numeric cell for vol/liq/mcap lacks a quote symbol, and that the Trade hub renders one entry per quote rather than one total.

**Verifier.** markets.js:81-87 sorts on s.m.vol24 / s.m.liq / s.m.mcap with no quote consulted, and markets.js:166 writes the three desktop columns through S.fmtDeroShort with no unit. .sim/ui-dec/desk-markets.png confirms it: the VOL 24H, LIQUIDITY and FDV columns are bare numbers under bare heads. token.js:69's Market band does the same on a page whose price eyebrow names the quote. home.js:179-183 is the sharpest case — `list.reduce((a, s) => a + s.m.vol24, 0)` summed across every quote and rendered as `mini("Flow 24h", … + " DERO")`, three screens from home.js:101 and markets.js:208, whose own comments say "There is no rate between them and there must not be one". docs/TODO.md 1.3 records the sorting half as knowingly open.

---

### `PROD·P7-09` — The impersonation guard covers tickers only: display names, NFT collection names and the Founders Pass itself can all be copied with no warning anywhere

**P2 · impersonation · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/feed.js `impersonation(s)` calls `this.reservedBy(s.ticker)` — the ticker, never `s.name`. `reservedBy` folds against `[v.sym, v.name, "w" + v.sym]` but only ever receives a ticker. app/feed.js `decorate()` builds the look-alike groups with `if (s.kind === "nft" || ...) continue;`, so NFT records are excluded from `dupTicker`, `lookalike` and `impersonates` entirely, and their `name`/`collection` fields (contracts/nft.bas: name ≤64, collection ≤64, both free text with no uniqueness check) are never examined. app/nfts.js `collections()` renders `h("b", ..., n.name)` on a tile with the creator's own `n.file` image, in a grid on the same screen as the real Founders Pass instrument. The team's own tool documents the gap: tools/simscam/main.go takes both `-ticker` and `-name`, and its header says "the only thing separating it from the real wrapped asset is what the app chooses to say about the name."

**Problem.** The recent D.impersonation fix closed the WXMR ticker case and left the name case open on the same object, plus the whole NFT surface. A launch with ticker "XMR2" and name "Monero" renders as `XMR2` in bold with `Monero` beneath it and a green "Verified" shield (app/ui.js:97), because the code really does match the shipped template. A holder-NFT collection named "DMZ Founders Pass" with the real pass's icon URL — which is public on chain — renders in the grid directly beneath the genuine pass with nothing distinguishing it.

**Failure scenario.** Precondition: none beyond the registration fee. Steps: run the equivalent of `simscam -ticker XMR2 -name Monero`, or install nft.bas with `name = "DMZ Founders Pass"` and `icon` set to the real pass's URL, and register. Impact: on the NFTs screen a buyer clicks the counterfeit tile expecting a fee-sharing pass and mints an unrelated holder NFT gated on a token they do not want; on Markets a buyer reading the name column buys a token that no vault backs, with a shield next to it.

**Root cause.** The impersonation work was scoped to the specific incident that prompted it (a token calling itself WXMR) rather than to the general shape 'any user-supplied string that names something the user already trusts'.

**Fix.** One line for the name case: in app/feed.js `impersonation(s)`, test `reservedBy(s.ticker) || reservedBy(s.name)` and word the warning from whichever matched. For NFTs, stop excluding `kind === "nft"` from `decorate()`'s grouping, key the group on `tickerKey(n.name + "|" + n.collection)`, and add an explicit check of every NFT name and collection against the Founders Pass name from config, marking any match. Give the real Founders Pass tile a config-derived identity mark that a copy cannot mint.

**Regression test.** Extend tools/simscam to install a launch with a clean ticker and a reserved NAME, and an nft.bas collection named after the Founders Pass; assert the UI marks both. This is exactly the pattern simscam already exists to serve.

**Verifier.** app/feed.js:52 impersonation(s) calls `this.reservedBy(s.ticker)` and nothing else; reservedBy (feed.js:40-49) folds against [v.sym, v.name, "w"+v.sym] but is only ever handed a ticker, so a token with ticker "XMR2" and name "Monero" produces tickerKey "XMR2", matches nothing, and renders the plain green "Verified" shield (ui.js:97). NFTs are never examined at all: decorate() (feed.js:200-221) builds its look-alike groups from this.assets, which after refresh() holds only launches and pools, and the only thing it does with this.nfts is set tokenTicker/tokenDec. contracts/nft.bas takes name and collection as free text with no uniqueness check. nfts.js:105-113 renders n.name in bold over the creator's own n.file image. tools/simscam/main.go:30-31 takes both -ticker and -name and its header says the gap out loud.

---

### `PROTO·BC-5` — The executable model has no equivalent of the ...To wrapper, so the differential and fuzz suites structurally cannot see BC-1

**P2 · test-coverage · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\reference\launch.go, func (l *Launch) Buy, lines 128-134: it appends `Send{"dero", d - used}` and then, four lines later, `if out == 0 || out < minOut { return nil, false }` -- the same ordering as the contract, but the model has no refundto and no wrapper, so the leaked Send is simply discarded by every caller that checks ok first. There is no Go function anywhere in reference/ that models SwapDeroForAssetTo.
The one test that exercises a refused private buy, tests/ringswap_test.go:56-63, asserts `r.Sent(zero, e.bob) != d` -- which WOULD catch the drain -- but feeds it a d small enough that out < rem, so it never reaches the overshoot branch. The whole suite is green: `go test -mod=vendor -count=1 ./veil/...` passes in 59 s.

**Problem.** The project's safety argument rests on contract-versus-model differential testing plus fuzzing (tests/*_test.go, VEIL_FUZZ_N). A P0 fund-loss bug sits in the gap between what the model covers (the signed entrypoint) and what ships (the anonymous wrapper), and the suite reports success while it is present.

**Failure scenario.** Precondition: any future change to launch.bas buyto or to either ...To wrapper.
1. A developer adds or reorders an external send in buyto.
2. tests/launch_test.go compares the signed entrypoint against ref.Launch.Buy and passes, because the signed path reverts on any non-zero return.
3. No test drives SwapDeroForAssetTo through the overshoot branch, and no model exists to compare it against.
4. The change ships. This is how BC-1 got in and stayed in.

**Root cause.** The wrapper was introduced as a transport detail ("pay a named address so the call can go out at ring 16") rather than as a distinct execution semantic. It is in fact the only place in the codebase where a non-zero inner return does NOT revert, which makes it the highest-risk path and the only one with no model.

**Fix.** Add `func (l *Launch) BuyTo(...)` and `SellTo(...)` to reference/launch.go (and the pool equivalents to reference/pool.go) that reproduce the wrapper exactly: run the inner op, and on failure emit a refund of the full incoming value. Then extend the fuzz driver to pick the To variants as well as the signed ones, so conservation is checked on the path that actually commits on failure.

**Regression test.** Extend the existing tests/ringswap_test.go refused-private-buy case to a second scenario at the graduation boundary, asserting exact conservation: sum of transfers to dest == d, and SCBalance unchanged.

**Verifier.** Grepping reference/ for SwapDeroForAssetTo, refundto, Refundto and RefundTo returns nothing — there is no model of either wrapper. reference/launch.go Buy does mirror the contract's ordering, appending `sends = append(sends, Send{"dero", d - used})` and then four lines later `if out == 0 || out < minOut { return nil, false }`, so the leaked Send is discarded by every caller that checks ok first and the differential test can never see it. tests/ringswap_test.go is the only test that drives a refused private buy, and at lines 20 and 56-63 it uses d = 300,000 against grad = 2,000,000, so out < rem and the overshoot branch is never entered — I confirmed this by having to raise the curve to real > 1,750,000 in my own reproduction before the drain would fire. The suite's own launch env helper (tests/launch_test.go line 84 e.check) compares stored keys, not the transfer list, on a failing call. The characterisation of the wrapper as the only place where a non-zero inner return does not revert is correct and follows from vendor/derohe/dvm/sc.go lines 279-286.

---

### `PROTO·BC-6` — launch.bas tmode 2: creator tax accrued before graduation is unreachable for ever if the token never graduates

**P2 · stuck value · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\launch.bas: buyto line 137 (`67 STORE("tacc", LOAD("tacc") + tx)`) and sellto line 220 accrue tax in phase 0 for every tmode. The only three readers of tacc are ClaimTax (line 267, `IF LOAD("tmode") != 1 ... THEN GOTO 900`), Buyback (line 281, `IF LOAD("tmode") != 2 || LOAD("phase") != 1 THEN GOTO 900`) and dist() (line 370, reached only from Distribute/Stake/Unstake/Claim, all of which require tmode == 3). For tmode == 2 there is no path that touches tacc while phase == 0, and no path at all if the curve never sells out. The contract is immutable -- no UpdateCode, no operator.

**Problem.** Every buy and sell on a tmode-2 launch skims `tax` bps into an account that cannot be spent until graduation. Most launches do not graduate. The quote sits in the contract for ever with no claimant, and the traders who paid it have no recourse.

**Failure scenario.** Precondition: a launch installed with tmode = 2 (buyback-and-lock) and tax up to 1000 bps.
1. Traders buy and sell on the curve; each leg accrues tx into tacc (up to 10% of the DERO leg).
2. The token fails to reach its graduation target -- the ordinary outcome for a memecoin launchpad.
3. phase stays 0 for ever. Buyback refuses. ClaimTax refuses (wrong tmode). dist refuses (wrong tmode).
4. The accumulated tax is permanently stranded. Sellers still get their curve refunds (sellto phase 0 pays from `real`, which excludes tacc), so nothing else breaks -- the money is simply gone.

**Root cause.** Buyback needs an AMM to buy from, which only exists after graduation, and no phase-0 fallback was defined for the tax it is supposed to spend. tmode 1 and tmode 3 both have phase-independent paths (ClaimTax and dist); tmode 2 is the only one that does not, and the gap is invisible in the tests because launch_test.go exercises Buyback only after graduation.

**Fix.** In contracts/launch.bas, give tmode 2 a phase-0 behaviour. The cheapest correct one: refuse tmode == 2 at install unless the operator also accepts that pre-graduation tax is burned, and set tax to 0 while phase == 0 for tmode 2 (skip the accrual at line 137/220 when tmode==2 && phase==0) so the money is never taken in the first place. Taking a fee that provably cannot be spent is worse than not taking it.

**Regression test.** tests/launch_test.go: install with tmode=2, run several buys and sells without graduating, assert tacc > 0, then assert every entrypoint that could move it (Buyback, ClaimTax, Distribute, Claim) is refused -- that test should fail after the fix because tacc should be 0.

**Verifier.** contracts/launch.bas accrues tax in phase 0 for every tmode: buyto line 137 `67 STORE("tacc", LOAD("tacc") + tx)` and sellto line 220 `45 STORE("tacc", LOAD("tacc") + tx)`. The three readers are ClaimTax line 267 (`IF LOAD("tmode") != 1 || s != LOAD("creator")`), Buyback line 281 (`IF LOAD("tmode") != 2 || LOAD("phase") != 1`) and dist() line 370, reachable only from Distribute/Stake/Unstake/Claim, all of which require tmode == 3 (lines 305, 315, 337, 360). So for tmode 2 in phase 0 there is no reader, and no path at all if the curve never sells out. The contract is immutable — I confirmed there is no UpdateCode and no owner entrypoint in the file. The asymmetry is real: tmode 1 and tmode 3 both have phase-independent paths and tmode 2 does not, and tests/launch_test.go exercises Buyback only after graduation. Sellers are unaffected because sellto phase 0 pays from `real` (line 208), which excludes tacc — so the money is stranded rather than causing a further failure.

---

### `PROTO·BC-8` — The product brief describes swap.bas/board.bas, which are not deployed; the shipped settlement path is pool.bas + vault.bas, and the repo's own docs already say so

**P2 · claims-vs-code · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** The brief says DMZ settles "without pooled liquidity, wrapped coins, or a centralized custodian holding user funds". The shipped path is the opposite on all three counts: pool.bas is a constant-product AMM with LP shares (contracts/pool.bas AddLiquidity/RemoveLiquidity), vault.bas issues wrapped claims (Mint line 335 `SEND_ASSET_TO_ADDRESS(LOAD("ua_" + j), amount, SCID())`), and a k-of-n federation custodies the foreign coins off chain with the contract holding no foreign asset at all.
The only contracts matching the brief are swap.bas (HTLC escrow) and board.bas (order board), and docs/ARCHITECTURE.md lines 23-24 record both as "written and tested, not deployed" / "written, not deployed". Neither has a screen in app/.
Crucially, the shipped UI and docs are already honest and contradict only the brief, not each other: app/screens.js:67 "held one for one by a k-of-n federation. Backed, not trustless"; app/token.js:225 "Wrapped units are backed one for one by a k-of-n federation that can fail, so they are not trustless"; README.md:276 "Never claim trustless. Name every trusted party"; docs/HANDOVER.md:23 and :336 forbid exactly the words the brief uses. contracts/README.md documents the trust model in full, including that n >= 2k is unenforced.

**Problem.** The defect is in the brief, not in the code. Publishing it would put a claim in front of users that every screen of the product then contradicts, and that the code cannot support: a bonded federation of named humans holds the XMR and ZEC, and BC-4 shows their bond is not a meaningful backstop. It would also make the honest in-app disclosures read as small print contradicting the headline.

**Failure scenario.** Precondition: the brief ships as marketing copy while the current contracts are deployed.
1. A Monero or Zcash holder reads "no wrapped coins, no centralized custodian" and wraps XMR through app/wrap.js.
2. The wrap screen tells them the opposite -- that k signers can steal the reserve -- which is the truth.
3. Either they stop (the honest copy did its job and the brief wasted the acquisition) or they proceed believing the headline, and BC-4's exposure applies to them without informed consent.
Impact is reputational and, for a no-KYC venue holding third-party foreign assets, a regulatory exposure the repo itself flags: docs/history/HISTORY.md:223 asks for a plain-English section on operator exposure and recommends legal review before public deployment.

**Root cause.** The brief describes the atomic-swap design (research/crosschain, swap.bas, board.bas) that the project explicitly deferred. docs/TODO.md:189 still carries the open item "Federation versus atomic swaps: reach a verdict", and docs/history/HISTORY.md:805 records the resolution -- the federation wrapper is the shipped layer and atomic swaps remain the future exit hatch. The brief was written against the roadmap rather than against the build.

**Fix.** Rewrite the brief against the deployed system, reusing sentences the repo has already vetted: README.md:107 ("The wrapper's trust is stated once and never called trustless: k of n signers can steal") and app/screens.js:67 ("Backed, not trustless"). If the trustless claim is wanted, it must wait until swap.bas and board.bas are deployed and have a UI -- and even then it describes a different product surface than the AMM pools.

**Regression test.** Not a code test: a publish-time lint over the brief text for the forbidden-claim list already recorded in docs/HANDOVER.md:336, failing the publish on a hit.

**Verifier.** Every citation verified. docs/ARCHITECTURE.md lines 23-24 record swap.bas as 'HTLC escrow — written and tested, not deployed' and board.bas as 'atomic-swap order board — written, not deployed'; neither has a screen in app/. The shipped path is the opposite of the brief on all three counts: contracts/pool.bas is a constant-product AMM with LP shares (AddLiquidity line 48, RemoveLiquidity line 101), contracts/vault.bas Mint line 335 issues wrapped claims with SEND_ASSET_TO_ADDRESS(LOAD("ua_" + j), amount, SCID()), and the foreign coins live entirely off chain in the signers' own k-of-n multisig — nothing in vault.bas touches them. The in-app copy is already honest and contradicts only the brief: app/screens.js line 67 'held one for one by a k-of-n federation. Backed, not trustless', app/token.js line 225 'Wrapped units are backed one for one by a k-of-n federation that can fail, so they are not trustless'. The repo's own rules forbid the brief's exact words: README.md line 276 'Never claim trustless. Name every trusted party', docs/HANDOVER.md line 23 and section 5.7 at line 336, which lists 'Trustless', 'non-custodial', 'decentralised bridge' and 'fully backed' as things that may never be said. The defect is in the brief, not the code.

---

### `REQ·A8-02` — One factory owner transaction de-verifies every market the app has ever listed; template hashes have a `legacy` list, the treasury pointer has none

**P2 · security-requirement-missing · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/feed.js:85-86 builds the registry from the factory's live `tstr` key each refresh; app/feed.js:151 `if (this.registry && this.registry.treasuryCanon && tCanon !== this.registry.treasuryCanon) return { scid, untrusted: "treasury differs from the factory" }`. A market's treasury is fixed at its own install (contracts/pool.bas:15 InitializePrivate stores `treasury`; contracts/launch.bas likewise), so it can never follow a change. contracts/factory.bas:27-36 SetTreasury rewrites `treasury`/`tstr` on the owner's signature alone. By contrast app/config.js carries a `legacy` array per template hash precisely so an earlier-but-still-valid version stays verified.

**Problem.** The app's verification treats the factory's *current* treasury pointer as the authority for markets whose treasury was baked in at install. A single SetTreasury call therefore marks every previously deployed market untrusted at once. The app then refuses to list them: the entire market surface disappears, permanently, with no migration path and no legacy list — and the contracts and the users' funds are entirely unaffected, so nothing on chain signals what happened.

**Failure scenario.** Precondition: mainnet live, N markets deployed, factory owner key still held (nothing disposes of it — see A8-04). Step 1: the owner rotates the treasury wallet, or the key is compromised, or the owner is compelled. Step 2: SetTreasury(newaddr) is mined. Step 3: on the next refresh every market fails feed.js:151 and is dropped from Markets, Home, the tape and search. Impact: a global, one-transaction, irreversible-for-the-app delisting of every market — held by exactly the party app/notice.js:38 tells users does not exist. Users can still trade by contract id only if they kept one.

**Root cause.** The treasury check was designed as an anti-impersonation test ('this market pays the protocol we know'), but it was implemented against a mutable pointer rather than against a set of accepted treasuries, and the mutability of that pointer was never considered as an availability input.

**Fix.** Give the treasury the same shape as the template hashes: ship an accepted-treasury list in app/config.js (current + legacy) and compare a market's treasury against that set, not against the factory's live value; keep the factory read only as a hint. Alternatively, remove SetTreasury from factory.bas in the next template so the pointer is immutable.

**Regression test.** A feed.js unit/UI-test case: build two synthetic markets, one paying the config treasury and one paying a legacy treasury, change the factory's `tstr` in the mock, and assert both stay listed and verified while a market paying an unknown address stays untrusted.

**Verifier.** app/feed.js:85-86 builds the registry from the factory's live `tstr` key on every refresh (reg.treasuryCanon = V.canonAddress(reg.treasury)), and app/feed.js:151 returns `{ scid, untrusted: "treasury differs from the factory" }` when a market's stored treasury does not equal it. contracts/pool.bas:15/83 fixes `treasury` at the market's own install, so it can never follow a change. contracts/factory.bas SetTreasury rewrites treasury/tstr on the owner's signature alone. app/config.js carries `legacy` arrays for template hashes and nothing equivalent for the treasury. feed.js:234 then drops every failing market from D.assets, which is what Markets, Home, the tape and search read. All verified.

---

### `REQ·A8-06` — The current working list is invisible: every routing document sends the next agent to a backlog that predates all recent work

**P2 · contradictory-documentation · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** docs/TODO.md is the live list (dated 2026-09-08, carries §0/§0b/§0c/§0d — the veto, Canon, impersonation and decimals fixes — and the open items). `grep -rn "TODO.md" --include=*.md` over the repo returns no hits outside research/: it is referenced by no other document. README.md §11 'Documentation map' lists DECISIONS, contracts/README, ARCHITECTURE, CREATOR_GUIDE, RISKS, research/wrapper, RING_PRIVACY, deploy/README, PLATFORM_GAP_MATRIX, PLATFORM_BACKLOG, HANDOVER, history/HISTORY — and neither TODO.md nor DESIGN_DIRECTIVE.md nor DESIGN_SYSTEM.md. docs/HANDOVER.md §4 instructs: 'Follow docs/PLATFORM_BACKLOG.md for everything else.' docs/PLATFORM_BACKLOG.md is still headed 'Veil platform backlog (2026-09-06…)' and contains no mention of the quote-asset programme, the split-pair work or the design programme; its 'Measured baseline' table records 19 JS files and 28 DOCs. docs/PLATFORM_GAP_MATRIX.md §1 still asserts 'app/templates/launch.bas lags contracts/launch.bas' and 'verification never checks fee/pfee' — both fixed since (B0.1, B0.2).

**Problem.** There are now three overlapping plan documents that disagree, and the routing documents point at the two stale ones. A next agent following HANDOVER §4 will work from a backlog that does not know quote assets exist, and will read a gap matrix whose headline diagnosis was fixed two days earlier.

**Failure scenario.** Precondition: the operator hands the project on, as HANDOVER is written to support. Step 1: the new agent reads README §11 and HANDOVER §4. Step 2: they never open docs/TODO.md, so they do not learn that founders fee accounting for wrapped quotes is broken (§1.1), that cross-quote ranking is still wrong (§1.3), or that the testnet record is stale (§0b). Step 3: they re-derive or re-break work that was deliberately left half-done with a reason. Impact: the four most load-bearing open items in the project are invisible to the person the handover was written for.

**Root cause.** docs/TODO.md was created as a working file during a run of focused fix commits and never wired into the document map; PLATFORM_BACKLOG.md stopped being updated at commit 5622e78 but is still cited as the plan of record.

**Fix.** Either fold docs/TODO.md into docs/PLATFORM_BACKLOG.md and delete it, or make TODO.md the plan of record: add it to README §11, change HANDOVER §4's pointer, mark PLATFORM_BACKLOG.md and PLATFORM_GAP_MATRIX.md as historical with a dated header, and correct the gap matrix §1 sentences that are now false.

**Regression test.** A trivial docs lint in the test script: every .md under docs/ must be reachable by a link from README.md or docs/HANDOVER.md; fail otherwise.

**Verifier.** `grep -rn 'TODO\.md' --include=*.md .` excluding research/ returns nothing — docs/TODO.md is referenced by no other document. I read README.md section 11: the table lists DECISIONS, contracts/README, ARCHITECTURE, CREATOR_GUIDE, RISKS, research/wrapper (README/DESIGN/RUNBOOKS), RING_PRIVACY, deploy/README, PLATFORM_GAP_MATRIX, PLATFORM_BACKLOG, HANDOVER and history/HISTORY — no TODO.md, no DESIGN_DIRECTIVE.md, no DESIGN_SYSTEM.md. docs/HANDOVER.md:136 does say 'Follow docs/PLATFORM_BACKLOG.md for everything else.' The routing is exactly as described.

---

### `REQ·A8-07` — Fifteen commits — the whole quote-asset and visual-identity programmes — with no loop record, against a rule that requires one per loop

**P2 · documentation-drift / process · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `git rev-list --count 4db3bbc..HEAD` = 15. docs/history/HISTORY.md was last written at 4db3bbc ('five more ways the vault could stop'); everything since (c872260 the identity, 3360620 the design directive, d890667/f127b87 the quote asset, 399f1eb, 7047a01, bd58ee4, f472fb3, 5cb8d1b, d68094d, d019572, e3fc36c, 234beab, b5af173) has none. The rule is docs/HANDOVER.md §0.10 ('append one record per loop… with the headings COMPLETED / DISCOVERED / SECURITY / UX / PERFORMANCE / ECONOMICS / STORAGE / PRIVACY / COMPETITIVE GAP / UNCONSIDERED (≥3) / NEXT'), repeated in README §12 ('Every change ships with its test and its doc line… and a loop record') and again as an unticked box in docs/DESIGN_DIRECTIVE.md §8 Definition of done. There is also one uncommitted change in the tree (`tools/simvault/main.go`), against HANDOVER §0.7 'Commit after every loop that changes code'.

**Problem.** The loop record is the project's only structured account of what was verified, what was discovered and what was not considered. For the two largest recent programmes that account does not exist; what remains is commit bodies, which record what was done but not the UNCONSIDERED section that has repeatedly been where this project's real findings came from.

**Failure scenario.** Precondition: a future audit or a handover asks 'what evidence was there that the quote-asset change is correct across all value paths?'. Step 1: HISTORY.md stops before the change. Step 2: docs/TODO.md §0 gives the outcome but not the verification commands, timings or the things deliberately not checked. Step 3: §3.1 of TODO ('Adversarial pass over the quote-asset change itself. NEXT') is the only trace that the change has never had one — and it is in the document nothing links to. Impact: the change that touched every value path in two contracts has no recorded verification pass.

**Root cause.** The loop discipline was maintained while the work was contract-side and lapsed when the work moved to the app and the design system; nothing enforces it.

**Fix.** Append records covering the quote-asset programme, the identity/design programme, and the four fixes in TODO §0–§0d, each citing the commands actually run and their results; commit or revert tools/simvault/main.go.

**Regression test.** A pre-commit or CI check that fails when a commit touching app/, contracts/ or tools/ does not also touch docs/history/HISTORY.md.

**Verifier.** `git rev-list --count 4db3bbc..HEAD` = 15, and `git log -1 -- docs/history/HISTORY.md` returns 4db3bbc — so no loop record covers c872260 through b5af173, which is the whole identity, design-directive, quote-asset, split-pair and decimals programme. docs/HANDOVER.md section 0.10 and README section 12 both require a loop record per loop.

---

### `REQ·A8-08` — The Founders Pass promise is rendered as unconditional in the UI while the code deliberately excludes wrapped-quote markets and the asset leg

**P2 · implemented-incorrectly / half-done · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/nfts.js:94: 'The promise is stored in the contract and cannot be changed: N% of the protocol fees claimed from **every market** is deposited here… the treasury cannot pay out less than what it deposited', with :95 rendering every figure through `dero(x)` (`A.fmtDero(x) + " DERO"`). The code: tools/treasurybot/main.go:131-136 skips any market whose `qt` is not DERO ('depositing here would credit holders with DERO this market never earned') and main.go:141 prints them as 'owed but not sweepable'; contracts/pool.bas:241-253 ClaimFees pays the asset-leg `fa` to the treasury with no path into the pass; contracts/founders.bas Deposit() reads DEROVALUE() only and `dep`/`und`/`paid` are single DERO counters. docs/TODO.md §1.1 records this as 'HALF DONE… holders cannot yet receive those fees at all… this breaks that promise silently, which is worse than breaking it loudly' and states the requirement 'the app must never show a fee total that mixes assets'.

**Problem.** The bot half of the fix landed (fail closed, name the unsweepable markets) and the UI half did not. The screen still asserts 'every market' and presents the pass's accounting in DERO, so a holder cannot see that fees from wXMR/wZEC markets and every asset-leg fee are structurally outside the promise. The fix's own stated requirement — never show a fee total that mixes assets — is met only because the excluded assets are invisible, not because they are labelled.

**Failure scenario.** Precondition: at least one wXMR-quoted market trades (the simulator already runs BRW/XMR). Step 1: fees accrue in `fd` denominated in wXMR. Step 2: anyone calls the permissionless ClaimFees; the treasury receives wXMR. Step 3: the pass's 'Deposited by the treasury and others' figure does not move and no screen explains why. Impact: holders of a pass whose promise is immutable in the contract are silently short-changed, and the only evidence is a line in a bot's stdout.

**Root cause.** The contract's fee accounting is single-asset by construction; the app's copy was written when every market was DERO-quoted and was not revisited when quote assets shipped.

**Fix.** Change the nfts.js copy to state the scope exactly ('of the DERO fees claimed from DERO-quoted markets'), and add a row that names the outstanding non-DERO fees per asset (the data is already on each market as `fd` with `qt`), labelled as owed and not distributable under the current contract. Then decide TODO §1.1's open question (per-asset accounting, or per-asset claiming) as a backlog item.

**Regression test.** Extend the UI test's synthetic-market block with one wXMR-quoted market carrying non-zero `fd` and assert the Founders screen renders a non-DERO 'owed' line and does not include it in any DERO total.

**Verifier.** app/nfts.js:94 does render the 'every market' promise and :95 renders figures through dero(x). tools/treasurybot/main.go:131-136 skips any market whose quote is not the zero hash with the comment 'depositing here would credit holders with DERO this market never earned', and :139-142 prints them as 'owed but not sweepable'. contracts/pool.bas:241-253 ClaimFees sends fd in the quote asset and fa in the traded asset, both to the treasury address, with no path into the pass; contracts/founders.bas Deposit reads DEROVALUE() only. So the bot half fails closed and the UI half still asserts 'every market'. Verified as stated.

---

### `REQ·A8-09` — `genconfig -dist` fails outright: all four testnet template hashes are stale, so the on-chain testnet app can no longer be refreshed

**P2 · testing / deployment-blocked · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** Ran `cd tools && go run ./genconfig -dist`: simulator bundle renders (31 files), then `2026/09/08 14:41:26 testnet: template launch: app/templates hash 09cb53d2cfc33c64 differs from networks.json bc05b8e38949c77d (redeploy or update the record)`, exit status 1. deploy/networks.json testnet block carries founders 8afd70fb…, launch bc05b8e3…, nft 389deff9…, pool d4e42ece… — every one of which appears in app/config.js's *legacy* arrays for the simulator, i.e. all four are superseded, not just founders. docs/TODO.md §0b names only the founders hash as the cause. docs/HANDOVER.md §6.6 makes refreshing the testnet bundle a wrap-up requirement.

**Problem.** The default `-dist` invocation — the one the DESIGN_DIRECTIVE definition of done requires to be green before any commit — fails, so the size check that guards the 18 432-byte DOC cap is only ever run per-network by hand. And the testnet, the mandatory stage between simulator and mainnet, cannot be given a bundle matching any commit since 2026-09-06 without a full contract redeploy.

**Failure scenario.** Precondition: an agent follows DESIGN_DIRECTIVE §8 ('cd tools && go run ./genconfig -dist must pass… before any commit'). Step 1: it exits 1. Step 2: they either skip the check or silently switch to `-network simulator`, and the cap check for the network that matters is never run. Step 3: the testnet on-chain app stays at the 2026-09-06 build, so the 'testnet deployment live and exercised for at least one week' mainnet gate is being satisfied by an app several programmes out of date.

**Root cause.** The testnet was deployed once and the contract templates have changed four times since; simdeploy maintains legacy hashes for the simulator only, and nothing marks a network's record as stale.

**Fix.** Redeploy the testnet contracts (TODO §0b is right that this needs a redeploy, not a record edit) and re-run `genconfig -dist -network testnet` plus `deploy/testnet-tela.sh`; correct TODO §0b to say all four templates are stale, not founders alone.

**Regression test.** Make `genconfig -dist` (all networks) part of tests/run.sh so a template change that strands a network fails the suite the day it happens, with a `-skip-network` escape that must be named explicitly.

**Verifier.** I did not re-run genconfig (it would RemoveAll and rewrite dist/), but the failure is settled statically. sha256 of app/templates/{founders,launch,nft,pool}.bas = 49009d2c, 09cb53d2, f8b85449, 33a126a8. deploy/networks.json testnet.templates records 8afd70fb, bc05b8e3, 389deff9, d4e42ece with empty legacy arrays — all four differ, and all four appear in the SIMULATOR block's legacy lists, confirming they are superseded rather than merely different. tools/genconfig/main.go bundle() returns an error on the first mismatch and main() log.Fatalf's, and netcfg.Names is [simulator testnet mainnet] with mainnet skipped by !n.Deployed(), so the run renders simulator and then dies on testnet — which is also exactly the state left on disk (dist/simulator has manifest.json, dist/testnet does not). docs/TODO.md naming only the founders hash is an undercount of four.

---

### `REQ·A8-11` — The app tells users anyone can verify the reserve; no tool, procedure or backlog item exists to do it

**P2 · requirement-implied-never-written · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/wrap.js:327-329 renders, when a viewing key is published: 'A viewing key is published, so anyone can check the reserve on <chain> really covers the claims.' docs/HANDOVER.md §5.6 item 5 makes it part of the definition of done for a backend: 'The reserve invariant reserve ≥ sup − inflight… checked continuously by every signer **and by an independent script using only the published viewing key**.' No such tool exists in tools/ (grep for viewkey/ViewKey across tools/*/*.go returns nothing). research/wrapper/RUNBOOKS.md covers signer daemon operation, cap and bonds, halt/resume and the shortfall incident — its sections contain no public verification procedure; §incident step 2 says the reserve count 'until this vault has published a viewkey… is the signers' statement'. docs/PLATFORM_BACKLOG.md W1–W11 contains no item for it.

**Problem.** Proof of reserves is the single mechanism that makes 'nobody can extract more than was deposited' checkable by someone who is not a signer, and it is the justification the UI gives for asking users to trust a federation. The requirement is stated once, inside a handover document, with no owner, no backlog item and no artefact — so it will not be built, and the UI claim has nothing behind it.

**Failure scenario.** Precondition: a vault is live with a published viewing key. Step 1: a holder reads the wrap screen and wants to check the backing. Step 2: there is no tool, no documented RPC sequence and no runbook step; the only reserve figure available anywhere is the signers' own `Reserve()` reading via the daemon log. Step 3: the holder is back to trusting the federation's word, which is exactly what the viewing key was published to avoid. Impact: the strongest control in the trust statement is unusable by the people it exists for.

**Root cause.** The requirement was written as a bullet in the 'standard for flawless' rather than as a backlog item with a file and a done criterion, so it never entered the work queue.

**Fix.** Add a backlog item (W12) for `tools/reservecheck`: reads the vault's `sup`, open withdrawals and `viewkey` from the DERO node, scans the foreign chain with the viewing key only, prints `reserve`, `sup − inflight` and the verdict, holds no spending material, and takes no machine-specific paths. Add a 'How to check the reserve yourself' section to research/wrapper/RUNBOOKS.md and link it from the wrap screen's trust panel.

**Regression test.** Run the tool against the simulator vault in tools/simvault/e2e.sh and assert it agrees with the contract's own state at every step, including mid-withdrawal when the reserve is legitimately short.

**Verifier.** app/wrap.js does render the 'a viewing key is published, so anyone can check the reserve' copy. `grep -rn 'viewkey|ViewKey|viewing key' tools/ --include=*.go` returns exactly one hit, tools/vaultsigner/main.go:151, and it is a comment about subaddress derivation — there is no independent verification tool anywhere in tools/. contracts/vault.bas:124 SetViewKey exists, so the key can be published; nothing consumes it. docs/PLATFORM_BACKLOG.md W1-W11 has no item for it. The requirement lives only as a bullet in docs/HANDOVER.md 5.6.

---

### `SEC·RT-01` — A vault Burn with no quorum is unrecoverable — there is no expiry path, and both the contract header and the Wrap screen claim the opposite

**P2 · contract / liveness / false-claim · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\vault.bas, Function Burn (lines 20-48) and the file header (lines 4-6). Burn takes ASSETVALUE(SCID()) into the contract and writes w_<j>_st = 0. The only two functions that ever change w_<j>_st are Released (line 40, requires k signer votes via votek("r",...)) and Veto (line 40, requires k signer votes via votek("g",...)). There is no height-based expiry, no single-party reclaim, and no path that returns units without k signers. The full entrypoint list of the file is InitializePrivate, SetLimits, Tighten, SetViewKey, AddSigner, AddSigner2, Bond, Unbond, Seal, Register, Attest, Mint, Refunded, Burn, Released, Paying, Veto, ClaimFees, Halt, SetCap, Slash — none of them is a reclaim. The header states 'LIVENESS BY CONSTRUCTION: after Seal no privileged party exists or is needed. Every limit empties by itself or expires into a payment; doing nothing pays the user.' C:\Users\Wilma\Desktop\fomo\app\wrap.js repeats it in the trust panel: 'There is no operator and no override. Every limit either empties by itself or expires into a payment: doing nothing releases a withdrawal, it never cancels one.'

**Problem.** Burn is the only way out of the wrapped asset, and it destroys the user's units before any payment is owed. The state it creates can only be resolved by an act of the federation. Doing nothing after a burn does not pay the user; it leaves the user with neither wrapped units nor foreign coins, permanently. The design document, the contract comment and the screen copy all assert the reverse, so a user reading the app has no way to learn the actual risk.

**Failure scenario.** Precondition: the vault is sealed and holds live claims. 1) Alice burns 10 wXMR with a Monero destination; the contract takes her units and writes w_7_st=0. 2) Before k signers agree a Released vote, the federation loses quorum — two of three signers lose their keys, are slashed, or simply stop running vaultsigner (there is no operator to restart them; the deployer revoked access at Seal). 3) No Released and no Veto can ever be voted. Alice's 10 wXMR are inside the contract, sup still counts them, no foreign payment is owed on chain, and there is no entrypoint that can return them. Impact: total, permanent loss of the burned amount for every user who burned before the quorum failed.

**Root cause.** Withdrawal settlement is modelled as a pure federation action with no timeout branch. The 'liveness by construction' claim in the header was written about the release DELAY (wdelay/bigdelay, which do expire) and was then generalised to the whole withdrawal, which it does not cover.

**Fix.** Add an expiry branch to the withdrawal record: if BLOCK_HEIGHT() > w_<j>_rel + <expiry> and w_<j>_st == 0 and no fresh Paying(j) mark exists, let anyone (or the burner) call an Expire(j) that sets st=2 and sends v+f back to w_<j>_by, exactly as Veto does. Until that exists, delete the 'doing nothing pays the user' sentence from vault.bas's header and from wrap.js's trust panel and replace it with the true statement: a burn can only be resolved by k signers, and if they stop, the units are gone.

**Regression test.** tests/: seal a 2-of-3 vault, Burn from a user wallet, advance past w_rel + expiry with zero signer activity, assert the burner's wrapped balance is restored and sup is unchanged.

**Verifier.** Read contracts/vault.bas in full. Burn (lines 363-390) writes w_<j>_st = 0 at line 385 and takes ASSETVALUE(SCID()) at line 369. The only writers of w_<j>_st are Released line 408 (behind votek("r",...), k signer votes) and Veto line 452 (behind votek("g",...)). Grepping the whole file confirms no other entrypoint touches it and there is no height-based reclaim. Reproduced on the real DVM (harness Chain, 2-of-3 sealed vault, sup=2000, Burn 500): after advancing 2,000,000 blocks with no quorum, w_0_st=0 and sup=2000 — the 500 units sit in the contract with no path out. The copy contradiction is real: app/wrap.js line 337-338 says 'There is no operator and no override. Every limit either empties by itself or expires into a payment: doing nothing releases a withdrawal, it never cancels one.' Doing nothing after a Burn releases nothing; the release itself needs k votes, only the wdelay expires. vault.bas header lines 4-5 make the same overstatement.

---

### `SEC·RT-05` — The registry treasury check silently disables itself when the factory's treasury is unreadable, while the Verified badge still claims the treasury was checked

**P2 · verification / fail-open · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\app\feed.js line 86: `reg.treasuryCanon = V.canonAddress(reg.treasury);` where `reg.treasury = V.hexToStr(r0.vals.tstr || "")`. V.canonAddress (rpc.js) returns null for an empty or unparsable string. Line 151: `if (this.registry && this.registry.treasuryCanon && tCanon !== this.registry.treasuryCanon) return { scid, untrusted: "treasury differs from the factory" };` — a null treasuryCanon short-circuits the whole comparison, so no contract is ever rejected on treasury. C:\Users\Wilma\Desktop\fomo\app\ui.js, verified(): the default branch returns a badge titled 'Contract code matches the shipped template and pays the protocol treasury'.

**Problem.** One of the app's two verification legs (code hash, treasury match) fails open. If the factory returns no `tstr`, or a value that does not parse as a DERO address, every listed contract passes the treasury test and is still shown with a badge that asserts the test passed. This violates the project's fail-closed rule, and it is the leg an attacker would target because the code-hash leg can be satisfied by installing the genuine template.

**Failure scenario.** Precondition: the app is pointed at a node the user confirmed for the session, or the real factory's tstr key is ever missing/renamed. 1) The node returns a factory record with `tstr` absent (feed.js then reads `""`). 2) treasuryCanon is null. 3) The node returns, for each registered scid, genuine pool.bas source (so the hash check passes) and a `treasury` value pointing at an address the attacker controls, with fee/pfee equal to the config values so the fee check also passes. 4) Every such market renders with the plain green 'Verified' shield. 5) The user trades; ClaimFees on those pools pays the attacker. Impact: the protocol fee stream is redirected and the app vouches for it. The same fail-open also hides an honest misconfiguration.

**Root cause.** The guard was written as 'compare only if we have something to compare against' rather than 'refuse if we cannot verify'. There is no state that says 'the registry could not be verified' for verified() to render.

**Fix.** Make registry_() throw (or set a `reg.unverified = true` flag) when canonAddress(reg.treasury) is null, and in loadContract return `{ scid, untrusted: "the factory's treasury could not be read" }` whenever `this.registry` is present but treasuryCanon is not. Add a third branch to ui.js verified() for 'code checked, treasury unknown' so the badge can never claim more than was tested.

**Regression test.** Unit test on loadContract with a registry whose treasuryCanon is null: assert the returned record carries `untrusted` rather than passing.

**Verifier.** app/feed.js line 85 sets `reg.treasury = V.hexToStr(r0.vals.tstr || "")` and line 86 `reg.treasuryCanon = V.canonAddress(reg.treasury)`. app/rpc.js canonAddress (line 46) returns null when bech32Decode fails, which it does on the empty string. Line 151 is `if (this.registry && this.registry.treasuryCanon && tCanon !== this.registry.treasuryCanon) return { scid, untrusted: ... }` — a null treasuryCanon short-circuits, so no contract is ever rejected on treasury. app/ui.js verified() has no branch for 'the registry could not be verified'; its default (line 97) still asserts 'Contract code matches the shipped template and pays the protocol treasury'. This is a genuine fail-open against the project's fail-closed rule. The reverse direction does fail closed: a market with no treasury key gives tCanon = null, which is !== the registry value, so it is rejected.

---

### `SEC·RT-06` — The localStorage market cache re-reads only the newest 4 records, so a same-origin writer can forge an entire price history and trade feed that survives verification

**P2 · cache poisoning / data integrity · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\app\feed.js line 12 `const VERIFY_N = 4;`, line 176 `pFrom = Number(base.pc); tFrom = Number(base.tc);`, line 180 builds `chk` from `points.slice(-VERIFY_N)` and `trades.slice(-VERIFY_N)` only, and line 182-186 drops the cache only if one of those 8 keys mismatches. Everything older is taken from localStorage untouched. Caps are CACHE_P = 1500 points and CACHE_T = 600 trades (line 11). validCache() checks shape only: it enforces height monotonicity (`p[0] < h` -> reject) but never checks that `ts` (p[1]) is monotonic, even though stats.js priceAt() binary-searches on ts and its own comment says 'Points are appended in ascending ts'. Origin note: DECISIONS.md line 254 — TELA 'host clones DOCs to disk and serves with http.FileServer on localhost:808x', i.e. one port per served site, so localStorage is shared by whatever site occupies that port.

**Problem.** The verification is a spot check on the newest few records, and an attacker who can write the cache can also read the chain, so supplying 4 correct newest records costs nothing. Everything the app derives from history is then attacker-chosen: candles and the chart, all five change windows, flow24/vol24, m.hot (so trending order), the published-trade feed, the Traders list, buys/sells counts, and the chart markers for followed accounts. The trades array also carries addresses, so fabricated trades can be attributed to any address, including one the victim follows.

**Failure scenario.** Precondition: a page on the same origin can write localStorage. On a TELA host that means a different TELA site that is served on the same 808x port at some point (ports are reused as sites are opened and closed); on a local dev serve it means any page on 127.0.0.1:8082. 1) The attacker's page reads the real market from the node and copies the true newest 4 p_ and 4 t_ records. 2) It writes veil.m.<scid> with v:4, the true tc/pc, those 4 real records at the end, and up to 1496 forged points before them describing a steady rise, plus up to 596 forged trades attributed to well-known addresses. 3) The victim opens DMZ. loadContract sees the cache, checks 8 keys, they match, and pFrom == s.pc so no new keys are fetched. 4) The victim sees a fabricated chart, a fabricated 24h change, a fabricated feed of buys by accounts they follow, and the market at the top of Trending. Impact: the user is induced to buy on invented data. The trade itself executes at the true price (min_out is enforced on chain), so the loss is the loss from a decision made on forged evidence.

**Root cause.** VERIFY_N was chosen to bound RPC cost; the threat model treated the cache as 'a hint' but then let unverified points reach every derived metric without marking them. Nothing distinguishes a verified point from an unverified one downstream.

**Fix.** Two changes. (a) Bind the cache to the chain rather than sampling it: store a rolling hash over the whole points/trades arrays and one chain-read anchor, or simply verify a random sample plus the first, middle and last record rather than the tail. (b) Add `ts` monotonicity to validCache() (`if (!int(p[1]) || p[1] < lastTs) return false`), since priceAt()'s binary search is already documented to depend on it.

**Regression test.** A test that writes a cache whose newest 4 records match the chain and whose older records are fabricated, loads the market, and asserts the app either re-reads the window or refuses the cache. A second asserting validCache() rejects non-monotonic ts.

**Verifier.** app/feed.js line 12 `const VERIFY_N = 4`. Line 176 sets `pFrom = Number(base.pc); tFrom = Number(base.tc)`, so when the cache's watermark equals the chain's, keys(pFrom, tFrom) fetches nothing new. Line 180 builds chk from `points.slice(-VERIFY_N)` and `trades.slice(-VERIFY_N)` only; I checked the index arithmetic (`pFrom - a.length + i`) against validCache's `c.p0 + c.points.length === c.pc` invariant at line 126 and it addresses the correct newest keys, so the check works — it is just narrow. Line 182 drops the cache only on a mismatch in those 8. Caps are CACHE_P = 1500 and CACHE_T = 600 (line 11). validCache line 129 enforces height monotonicity (`p[0] < h` rejects) but only `int(p[1])` on ts, while stats.js priceAt (lines 12-17) binary-searches on ts and its comment claims 'Points are appended in ascending ts'. Line 131 requires `V.canonAddress(t[2])` — any valid address, so forged trades can be attributed to a followed account and reach D.feed()'s 'following' filter at line 274. The origin premise is verified from source, not just DECISIONS.md: vendor/tela/tela.go DEFAULT_PORT_START = 8082 with DEFAULT_MAX_SERVER = 20, FindOpenPort() takes the first free port from 8082 upward, and serveTELA() serves at http://localhost:<port>/<path>. localStorage is keyed by origin, not path, so two TELA sites that occupy 8082 at different times share it. The app's CSP is a meta tag in app/index.html line 6 and does not isolate localStorage.

---

### `SEC·RT-07` — The vault-symbol impersonation guard inspects the ticker only; the attacker-controlled display name is rendered everywhere with no check

**P2 · impersonation / incomplete fix · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\app\feed.js, impersonation(s): `const v = this.reservedBy(s.ticker);` — the ticker is the only input. reservedBy() folds v.sym, v.name and 'w'+v.sym through tickerKey. hasHomoglyph is likewise called only on s.ticker (feed.js decorate(), and token.js's deep-link path). Meanwhile the name is taken straight from the contract — feed.js loadContract: `name: str("nameHdr", 32) || "?"` — and rendered unguarded at app/markets.js (`p.sub.textContent = s.name + ...` and `p.nm.textContent = s.name`), app/token.js (`h("div", { class: "sub ellipsis" }, s.name)` in drawHead), app/token.js tradeSheet's market picker, and app/home.js. launch.bas InitializePrivate line 22 accepts any name up to 32 characters.

**Problem.** The fix that reserved vault symbols closed the ticker channel and left the name channel wide open, on the same row. V.ui.clean strips control and bidi characters but does not restrict the character set, so a creator can name a token 'Wrapped Monero', 'Monero (wXMR)' or 'Official XMR bridge' while using a ticker that trips no guard, and it will render with a clean 'Verified' shield and no warning of any kind.

**Failure scenario.** Precondition: anyone can launch a token (permissionless by design). 1) The attacker launches a token with ticker XMR2 (no key match against XMR / MONERO / WXMR, so impersonates is null and dupTicker is false) and name 'Wrapped Monero'. 2) The market lists with the standard Verified badge, no warning triangle, no risk line, and the name 'Wrapped Monero' printed under the ticker in the markets table, on the token page and in the trade sheet's market picker. 3) A Monero holder arriving from the Wrap screen buys it believing it is the vault asset. Impact: exactly the loss the recent D.impersonation fix was written to prevent, through the field that carries most of the visual weight.

**Root cause.** The guard was scoped to the ticker because tickers are the collision space the dupTicker logic already grouped on; the name was treated as free text rather than as a second identity claim.

**Fix.** Run reservedBy() over the folded name as well: in impersonation(s), `const v = this.reservedBy(s.ticker) || this.reservedBy(s.name);` and add the same for hasHomoglyph. Because names are longer than tickers, also match on whether the folded name CONTAINS a reserved key rather than only equalling it (a name of 'WRAPPEDMONERO' should trip the Monero rule).

**Regression test.** A test that builds a launch with ticker 'XMR2' and name 'Wrapped Monero' and asserts s.impersonates is set and U.warnings(s) is non-empty.

**Verifier.** app/feed.js impersonation() line 53 is `const v = this.reservedBy(s.ticker);` — the ticker is the only input, and reservedBy (line 39) folds v.sym, v.name and 'w'+v.sym through tickerKey. decorate() line 214 calls hasHomoglyph on s.ticker only. The name comes straight from the contract at line 162 (`name: str("nameHdr", 32) || "?"`) and is rendered unguarded at markets.js lines 165-166, token.js line 52 (`h("div", { class: "sub ellipsis" }, s.name)`) and home.js lines 46 and 55. app/ui.js clean() (line 23) strips control and bidi codepoints but imposes no character-set restriction, and warnings() (lines 99-106) derives every pill from ticker flags. I checked the escape the scenario needs: tickerKey('XMR2') = 'XMR2' matches neither 'XMR' nor 'MONERO' nor 'WXMR', so impersonates is null, dupTicker is false, and ui.js verified() falls through to the plain green 'Verified' at line 97. launch.bas InitializePrivate line 12 accepts any name up to 32 characters.

---

### `SEC·RT-08` — The app sends vault Burn at ring size 2 although the contract never reads SIGNER(), and the screen states the ring-2 exposure as a contract requirement

**P2 · privacy / incomplete use of contract capability · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\vault.bas Function Burn line 20: `LET s = ADDRESS_RAW(refund)` — SIGNER() is never called in Burn; the code comment above it says 'refund is where a Veto returns the units, named rather than taken from SIGNER() ... Naming it also lets a burn stay private.' C:\Users\Wilma\Desktop\fomo\app\wrap.js doBurn(): `V.app.tx({ scid: w.scid, entry: "Burn", args: [["dest","S",dst],["refund","S",me()]], asset: {...} })` — no `ring` option. C:\Users\Wilma\Desktop\fomo\app\app.js app.tx: `const ring = opts.ring === 16 ? 16 : 2;` and, for ring 2, the gas estimate is sent with `{ signer: W.address }`. wrap.js's burn form asserts: 'Burn is a ring-size-2 call: the contract must record who burned, so this address is public in that transaction.'

**Problem.** The contract was deliberately written so a burn can go out at ring 16, and the app never uses that. Every withdrawal therefore names the burner on chain (DECISIONS.md 2.4: Extract_signer succeeds only at RingSize == 2, so a ring-2 call is attributable by anyone), and the node is additionally told the payer in the gas estimate. The copy compounds it by presenting the exposure as unavoidable, so a user cannot learn there was a choice.

**Failure scenario.** Precondition: a user withdraws from the vault. 1) They fill in the burn form and confirm. 2) The transaction goes out at ring size 2 with signer = their address, and w_<j>_by records the same address next to w_<j>_to, the foreign destination. 3) Anyone reading the chain now has the DERO-address-to-foreign-address pair AND an attributable ring-2 transaction tying it to the wallet. A ring-16 burn with `refund` set to a fresh address would have leaked neither. Impact: an avoidable, permanent deanonymisation on the product's headline privacy path.

**Root cause.** app.tx's ring selection is opt-in per call site (`opts.ring === 16`), and wrap.js was written before the '...To'/named-refund pattern was generalised. Nothing checks that a call site's ring matches whether the entrypoint actually reads SIGNER().

**Fix.** Pass `ring: 16` from wrap.js doBurn(), and offer a 'refund to' field the way the trade sheet offers 'receive at' — defaulting to the connected address but allowing a fresh one. Correct the burn-form copy: the contract does not need to know who burned; the address is public only because the app chooses ring 2, and the destination is public because it is stored. Note that D.vaultWithdrawals() matches on w_<j>_by === the connected address, so a non-default refund address needs the list keyed differently.

**Regression test.** A chain test asserting the ring size of the Burn transaction the app builds is 16 and that ADDRESS_STRING of the sender is not recoverable from it.

**Verifier.** contracts/vault.bas Burn line 366 is `LET s = ADDRESS_RAW(refund)`; SIGNER() appears nowhere in the function, and the comment at lines 361-362 says so explicitly ('Naming it also lets a burn stay private'). app/wrap.js doBurn (lines 442-445) passes no `ring`, and app/app.js line 113 is `const ring = opts.ring === 16 ? 16 : 2;`, so every burn goes out at ring 2; line 130 additionally sends the gas estimate with `{ signer: W.address }` for ring 2. wrap.js line 367 states the exposure as a contract requirement: 'Burn is a ring-size-2 call: the contract must record who burned'. That sentence is false about this contract.

---

### `SEC·RT-09` — The Paying/Veto guard closes the common double-pay case but leaves a 2000-block window, and the mark cannot be renewed even while the signer is still retrying the payment

**P2 · contract / incomplete fix · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\vault.bas Function Paying line 24: `IF EXISTS("w_" + j + "_p") != 0 THEN GOTO 100` — the mark is written once and never refreshed. Function Veto lines 24-25: `IF EXISTS("w_" + j + "_p") == 0 THEN GOTO 30` / `IF BLOCK_HEIGHT() < LOAD("w_" + j + "_p") + 2000 THEN GOTO 900` — after 2000 blocks the veto is legal again regardless of whether the foreign payment went out. Veto line 41 then returns v+f units to w_<j>_by while sup is unchanged. C:\Users\Wilma\Desktop\fomo\tools\vaultsigner\main.go processWithdrawal(): the 'paid' branch retries VerifyRelease up to -verify-tries (default 20) passes, then resets `it.State = "paying"` and calls markPaying(j) again — which the contract accepts (RETURN 0) without updating w_<j>_p. Released is also blocked entirely while halted (line 21), which can consume the whole window.

**Problem.** The guard's own comment accepts this ('A signer slashed after marking still costs the withdrawal that one window'), but the residual case is not bounded delay, it is a broken reserve invariant: if the coins leave the foreign chain and Released is not recorded within 2000 blocks of the FIRST Paying mark, a quorum can veto and hand the burner their units back. Reserve then sits below sup by v with nothing on chain recording why.

**Failure scenario.** Precondition: a withdrawal is being paid. 1) A signer calls Paying(7) at height H and pays 10 XMR abroad. 2) The payment is slow to become visible, or a quorum votes Halt(1) for an unrelated reason — Released is refused while halted (RT-02). 3) 2000 blocks (about 10 hours at 18s) pass. Veto is not blocked by halted, so the signers, seeing an unsettled withdrawal, vote Veto(7). 4) Alice receives her 10 wXMR back; the 10 XMR are already in her Monero wallet. sup is unchanged, so the contract now claims backing it does not have. Impact: reserve < sup by the withdrawal amount, which the signers' own shortfall detector will then read as a shortfall and halt on, compounding RT-02.

**Root cause.** The mark is a one-shot timer chosen so that no single signer can freeze a withdrawal forever, which is the right instinct, but the timer is not tied to the thing it protects (the payment being outstanding). The retry path in vaultsigner assumes markPaying refreshes it; the contract does not.

**Fix.** Let Paying be re-marked by a signer that has not marked before (key it per signer: `w_<j>_p_<addr>`, and treat the veto block as 'the newest mark is fresher than 2000 blocks'), so an honest retry keeps the protection alive without letting one signer renew indefinitely. Alternatively, refuse Veto outright once any Paying mark exists and require the quorum to prove non-payment through a Refunded-style vote naming the foreign txid.

**Regression test.** tests/: Paying(j), advance 2001 blocks without Released, then Veto(j) — assert it is refused after the fix. Second test: markPaying twice and assert w_<j>_p advances.

**Verifier.** contracts/vault.bas Paying line 432 `IF EXISTS("w_" + j + "_p") != 0 THEN GOTO 100` — the mark is one-shot; Veto lines 446-447 gate on `BLOCK_HEIGHT() < LOAD("w_" + j + "_p") + 2000`; Veto line 452 sets st = 2 and line 453 returns v+f units, with no change to sup. Released line 398 refuses while halted, and Released is the only thing that closes the mark permanently. Reproduced on the DVM: mark at H, halt, advance to H+2001, two Veto votes -> w_0_st = 2, 500 units returned, sup still 2000. The daemon side is worse than described: markPaying at tools/vaultsigner/main.go lines 792-801 returns true whenever the key merely exists, with no age check at all, and the retry path at lines 393-397 resets an intent to 'paying' after -verify-tries (default 20) passes, so the daemon re-pays abroad under a mark that may already be past its window.

---

### `UX·F10` — --fg3 is 3.68:1 on the ground — every label, eyebrow, note, stat caption, inactive tab and empty-state body in the app fails WCAG AA

**P2 · accessibility · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/styles.css:9 `--fg3:#63676a` on `--bg:#000` (app/styles.css:6). Measured: 3.68:1 against #000 and 3.45:1 against --s1 (#0a0b0a); AA requires 4.5:1 for text below 18.66px bold / 24px regular. --fg3 is the colour of: .eyebrow and .label at 10.5px (styles.css:50), .note at 12.5px (styles.css:56), .stat .l at 10px (parts.css:48), .mini .l at 10px (parts.css:83), inactive .tab at 9.5px (styles.css:85), .empty body at 13px (ui.css:68), .chip at rest 12.5px (ui.css:29), .msg .when at 10.5px (parts.css:127), .chg with no direction (ui.css:40). Nothing here is large text. `.step.todo{opacity:.55}` (parts.css:204) drops --fg3 content to roughly 2.5:1. --graphite #4b525a measures 2.65:1. For comparison --fg2 is 7.64:1 and --acc is 15.48:1, so the palette is otherwise sound.

**Problem.** The tertiary ink carries the entire Label type role, and Label type is how this design system communicates — every Level 3 read-out is "label above, number below". The labels are the part that says what the numbers mean, and they are the part that fails. The directive §6 asks for excellent contrast; this is systematic, not incidental.

**Failure scenario.** Precondition: a user with low vision, or any user on a phone in daylight. Steps: open Portfolio or the token page. Impact: the numbers are legible at 17.39:1 and the labels naming them are not, so the readings lose their meaning — "FDV", "Liquidity", "Volume 24h", "Headroom", "Mint cap", "Minimum deposit" are exactly the words a user cannot afford to miss.

**Root cause.** A three-step ink ramp tuned by eye on a bright display, with the third step used at the smallest sizes.

**Fix.** app/styles.css:9 — raise `--fg3` to `#7a7e81` (measured 5.13:1 on --bg, 4.82:1 on --s1, comfortably AA at every size the token is used at) and keep --fg and --fg2 as they are. That single token change fixes every listed selector by inheritance, which is what the token system was built for. Separately, replace `.step.todo{opacity:.55}` with an explicit colour so contrast is not multiplied away, and stop using --graphite for anything but the mark.

**Verifier.** I recomputed every ratio from the hex values rather than trusting them. app/styles.css:9 --fg3:#63676a has relative luminance 0.1339; against --bg:#000 (styles.css:6) that is 3.68:1 and against --s1:#0a0b0a (L=0.00325) it is 3.45:1 — both below the 4.5:1 AA threshold for text under 24px. --fg2:#9a9d97 computes to 7.64:1, --acc:#b4f03f to 15.48:1 and --graphite:#4b525a to 2.65:1, exactly as claimed. Every cited selector exists at the cited size: .eyebrow/.label 10.5px (styles.css:50), .note 12.5px (:56), .tab 9.5px (:85), .stat .l 10px (parts.css:48), .mini .l 10px (parts.css:83), .msg .when 10.5px (parts.css:127), .empty 13px (ui.css:68), .chip 12.5px (ui.css:29), .chg 12.5px (ui.css:39), and .step.todo{opacity:.55} (parts.css:204).

---

### `UX·F11` — An unreachable node produces skeletons that animate forever, and no screen in the app has a retry

**P2 · missing-state · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/feed.js:239 `this.lastRefresh = Date.now()` is set only inside the success path of refresh(). app/app.js:71 swallows the failure: `catch (e) { console.warn("refresh", e); }`. app/home.js:156 and :166 and app/markets.js:221 all branch `D.lastRefresh ? <empty state> : U.skeleton(n)`, so a permanent failure holds the skeleton branch permanently. app/nfts.js:117 and :131 the same. Grep for /retry|try again|reconnect/ across app/*.js returns nothing. Every error surface is a terminal render: app/wrap.js:189, app/chat.js:123, app/token.js:35, app/ui.js:143 all render an .errbox and stop. The one exception is the settings endpoint warning (app/screens.js:247) which does offer an action. app/app.js:31 sets the header pill to "no node" with class `pill down`, and that ~90px pill is the only signal on screen.

**Problem.** The most common real-world failure — a node that is down, wrong, or rate-limited (the fleet guide notes gamma polling 429s) — has no representation. Home, Markets and NFTs pulse skeleton blocks indefinitely, which reads as "loading", not "broken". And when an error is shown, the user's only recovery is to reload the page, which the interface never says.

**Failure scenario.** Precondition: the configured node is unreachable, or the stored remote endpoint has not been confirmed this session (app/app.js:26 sets it to null). Steps: open DMZ. Impact: four grey blocks breathing under a "TRENDING" heading forever, with no error text, no elapsed time, and no action. The user has no way to learn that the problem is their node rather than an empty deployment.

**Root cause.** Loading and failure share one representation because the data layer records success but not failure. There is no `lastError` and no attempt counter.

**Fix.** In app/feed.js add `lastError` and `lastAttempt` alongside `lastRefresh`, set in refresh()'s catch. Change the three-way branch on every screen to items ? list : lastError ? errorState : lastRefresh ? emptyState : skeleton. Build the error state on U.emptyState — it already takes an action node — with the message naming the endpoint (V.node.endpoint) and two actions: "Retry" calling V.app.refresh(true) and "Node settings" going to #settings. app/wrap.js:189 and app/chat.js:123 gain the same Retry calling their own reload(true).

**Verifier.** app/feed.js:239 sets lastRefresh at the end of the success path only; app/app.js:71 catches with `console.warn("refresh", e)` and nothing else. home.js:156, home.js:166, markets.js:221, nfts.js:117 and nfts.js:131 all branch on `D.lastRefresh` with U.skeleton on the falsy side, so a permanent failure pins the skeleton branch. grep for /retry|try again|reconnect/ across app/*.js returns nothing — I ran it. Every error surface (wrap.js:189, chat.js:123, token.js:35, ui.js:143, nft.js:8, screens.js:189) renders an .errbox and stops; the one exception with an action is the endpoint-confirmation warnbox at screens.js:246. app.js:31 sets the header pill to "no node"/"pill down".

---

### `UX·F12` — DERO still leads: the Home instrument, the telemetry tape and the app's own on-chain description all put DERO first and never name XMR or ZEC

**P2 · information-architecture · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/home.js:120-121 — the Level 1 hero is "Flow · last 24 hours" with `leadSym` = whichever quote has the largest 24h volume; .sim/ui-dec/01-home.png and desk-home.png render "15.28 DERO" as the headline with "0.0000 XMR" as a small secondary line. app/home.js:167 `L.tape(U.$("#tape"), D.trending(14))` feeds the tape trending market tickers; both renders show fourteen copies of "BRW" and no DERO, XMR or ZEC price at all — the directive §5.2 asked for exactly "DERO 0.09292 ▲0.4%  XMR 312.42 —0.2%  ZEC 41.20 ▲1.1%". app/index.html:35 the nav label is "Wrap" while the screen it opens is titled "Deposit & withdraw XMR" (app/wrap.js:225). tools/telainstall/main.go:82 — the on-chain INDEX description is "DMZ: social DEX and launchpad for DERO assets", the first sentence anyone reads in a TELA browser, and it names neither Monero nor Zcash.

**Problem.** Portfolio and Markets do lead with the wrapped assets, and they do it well — the portfolio hero and the markets filter strip both put XMR and ZEC first. But the two surfaces a first-time visitor meets before either of those (the Home instrument and the always-visible tape) are DERO-first and token-first, and the product's permanent on-chain self-description positions it as a DERO launchpad. The owner's repeated demand is not met where it is most visible.

**Failure scenario.** Precondition: a Monero holder arrives from the on-chain site listing. Steps: they read "DMZ: social DEX and launchpad for DERO assets", open it, and see a hero reading 15.28 DERO, a tape of memecoin tickers, and a nav item called "Wrap". Impact: nothing on the first screen tells them this product is for them. The XMR path exists and is good, and it is two taps behind vocabulary that does not name it.

**Root cause.** Home was designed as a market console for the deployment as a whole rather than as an answer to "what is this for". The tape is fed from trending markets because that is what the data layer offers, not because it is what the strip is for.

**Fix.** 1) Home hero: lead with the wrapped assets. The data is already loaded — D.vaults() plus each vault's `sup`/`cap`/`k`/`na` — so make the Level 1 surface the two networks and their state (claims outstanding, signers, open) with market flow demoted to the read-out strip beneath it. This is a removal, not an addition: the "Flow · last 24 hours" number and its LIVE badge come out. 2) app/live.js:35 tape(): prepend one item per vault asset before the trending markets, so the strip always opens with DR / XM / ZC. 3) app/index.html:35 — relabel the tab "XMR · ZEC" or "Deposit", matching the screen title. 4) tools/telainstall/main.go:82 — change the default descr to name Monero and Zcash. That string is written on chain and immutable after install, so it must be fixed before deployment.

**Verifier.** app/home.js:104-106 picks leadSym as the largest-volume quote and :120-121 renders it as the hero; on the shipped sim data that is DERO (the markets summary reads "24h flow 0.0000 XMR · 15.28 DERO"). home.js:167 feeds the tape from D.trending(14) and .sim/ui-dec/desk-markets.png shows the tape as seven consecutive "BRW" tickers with no DERO/XMR/ZEC price line — docs/DESIGN_DIRECTIVE.md:304 specifies exactly the opposite. index.html:35 labels the nav "Wrap" while wrap.js:225 titles the screen "Deposit & withdraw XMR". tools/telainstall/main.go:82 sets the INDEX description to "DMZ: social DEX and launchpad for DERO assets".

---

### `UX·F13` — The identity avatar is an hsl gradient on every market row and every feed item — the loudest colour on every screen in a system that allows one accent

**P2 · ai-slop · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/ui.js:66 — `style: \`background:linear-gradient(135deg,hsl(${h1} 62% 52%),hsl(${h2} 70% 34%))\`` where the hues come from a hash of the id. It is rendered on every markets row (markets.js:129), every home market row (home.js:47), every feed item (home.js:72), every trade-hub row, the token page header at 60px (token.js:52), portfolio rows and profile heroes. docs/DESIGN_SYSTEM.md principle 3 reserves colour meaning to lime, §Tokens defines a three-ecosystem network grammar as "a two-letter mono label plus a hairline leading edge… The tint is never a fill", and the directive §2.2 bans "generic gradient backgrounds" outright. Visible in .sim/ui-imp/02-markets.png: twenty-four saturated rainbow squares down the left edge of a screen whose entire palette is otherwise black, off-white and one lime.

**Problem.** This is the single largest break in the visual thesis and the thing that makes a cropped screenshot look like any other token screener. The gradients carry no information — they are a hash, so two hues that look related mean nothing — while the network grammar that would carry information (DR / XM / ZC with a tinted leading edge) is defined in the system, implemented in parts.css:102, used on the wrap and portfolio screens, and absent from every list.

**Failure scenario.** Precondition: any list view. Steps: crop the logo out of .sim/ui-imp/02-markets.png and show it to someone. Impact: they see a crypto screener. The one-sentence target of the whole directive — "someone should see a screenshot with the logo cropped out and say that's DMZ" — fails on the app's densest screen, and it fails because of one CSS declaration.

**Root cause.** The avatar primitive predates the redesign and was never brought across when the tokens were replaced; it is the only place in the app that generates colour at runtime.

**Fix.** Replace the gradient with the grammar the system already owns: a flat `--s2` plate, `--r1`, the two-or-three-letter ticker in mono at --fg2, and a 2px leading edge in the network tint from netOf() — reusing .net from parts.css:102. Keep the `hash()` only to pick among the three network tints where the network is unknown, or drop it entirely. This is strictly a removal (one linear-gradient and two hsl computations out, one existing class in), it shrinks ui.js, and it makes the left edge of every list say which chain a market settles on — information the lists currently do not carry at all.

**Verifier.** app/ui.js:66 builds `background:linear-gradient(135deg,hsl(h1 62% 52%),hsl(h2 70% 34%))` from a FNV hash of the id, and avatar() is called from markets.js:129, home.js:47, home.js:72, home.js (trade hub), token.js:52, screens.js and nfts.js. Both renders confirm it: .sim/ui-dec/02-markets.png and desk-markets.png show a column of saturated multi-hue squares down the left of an otherwise black/off-white/lime screen. docs/DESIGN_SYSTEM.md line 93-94 defines the network grammar as "a two-letter mono label plus a hairline leading edge in its tint. The tint is never a fill", implemented at parts.css:102 and used on wrap and portfolio but on no list row. docs/DESIGN_DIRECTIVE.md:141 bans "generic gradient backgrounds" by name.

---

### `UX·F14` — 768px is untested and the app is a 430px strip there, while two media queries switch to wide layouts inside that narrow column

**P2 · responsive · auditor confidence High · verifier PLAUSIBLE**

**Evidence.** app/styles.css:32 `.app{max-width:430px}` with the desktop shell only at app/desk.css:16 `@media (min-width:960px)`. Between 720 and 959 the container is still 430px wide (398px of content) but app/parts.css:96 `@media (min-width:720px){.stats4{grid-template-columns:repeat(4,minmax(0,1fr))}}` makes the four-stat strip four-up in that 398px, giving ~99px cells whose values carry `white-space:nowrap;overflow:hidden;text-overflow:ellipsis` (parts.css:49) — so they ellipsise. app/parts.css:151 `@media (max-width:719px)` means chat messages fold to a single line below 720 and switch back to the 58px+104px+1fr grid above it, consuming 182px of a 398px column. stats4 is used on NFTs (nfts.js:39), the founders/nft page (nft.js:27) and Profile (screens.js:209). tools/uitest/ui.mjs opens viewports at 430 (line 38), 1280 (line 197) and 1024 (line 210) only — 390 and 768 are absent, and docs/DESIGN_DIRECTIVE.md §6 and §8 both require them.

**Problem.** On an iPad in portrait the app is a phone column with two thirds of the screen empty, and inside that column two components have already switched to their wide layouts and no longer fit. At 390px the seven-cell tab bar gives each cell 55.7px while the label PORTFOLIO at 9.5px/700/.04em measures around 56px with no overflow guard on `.tab` (styles.css:85) — a grid track with min-width:auto will expand and push the fixed bar past the viewport.

**Failure scenario.** Precondition: any 720–959px viewport (iPad portrait is 768). Steps: open Profile or NFTs. Impact: the four-stat strip shows truncated values — "Published vol" reading as a clipped number — which is the one thing the codebase repeatedly says a balance may never do (screens.js:146 makes exactly this argument for abandoning the three-up tile strip on phones). Chat loses 46% of its message width at the same breakpoint.

**Root cause.** parts.css uses viewport media queries to decide layouts for a container whose width is decided by a different breakpoint in styles.css/desk.css. The two never agree between 720 and 959.

**Fix.** Change app/parts.css:96 and :151 from 720 to 960 so component layout switches when the container actually widens, matching what home.js:34 `wideShell()` already does with matchMedia(960). Then either raise `.app{max-width}` in the 720–959 band or accept the strip deliberately — but decide it once. Add `min-width:0` and an ellipsis/second-line rule to `.tab` so the bar cannot overflow at 390. Finally add 390 and 768 to tools/uitest/ui.mjs alongside the existing horizontal-overflow assertion at 1024, which is the assertion that would have caught this.

**Regression test.** tools/uitest/ui.mjs already asserts `over > 1` fails at 1024; run the same assertion at 390 and 768, and add a check that no `.stat .v` element has scrollWidth > clientWidth.

**Verifier.** The breakpoint conflict is confirmed from source: app/styles.css:32 `.app{max-width:430px}` with the desktop shell only at desk.css:16 `@media (min-width:960px)`, while parts.css:96-99 switches .stats4 to four columns at min-width:720px and parts.css:151 switches .msg back to its wide 58px+104px+1fr grid above 720px. Between 720 and 959 the container is still 430px, so four .stats4 cells share ~398px; .stat .v carries white-space:nowrap;overflow:hidden;text-overflow:ellipsis (parts.css:49), so values will ellipsise. stats4 is used at nfts.js:39, nft.js:27 and screens.js:209 as claimed. tools/uitest/ui.mjs opens 430 (lines 26/38), 1280 (197) and 1024 (210) only, and docs/DESIGN_DIRECTIVE.md:422 and :481 both require 390 and 768.

---

### `UX·F15` — Modal sheet has no focus trap or focus return, nav tabs declare role=tab without aria-selected, the chart is pointer-only, and the tape can only be paused by hovering

**P2 · accessibility · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/ui.js:45-52 openSheet(): sets role=dialog aria-modal=true (index.html:44) and focuses the first input or button, but nothing constrains Tab, and closeSheet() (ui.js:51) does not restore focus to the trigger. app/index.html:30-39: `role="tablist"` with eight `role="tab"` buttons; app/ui.js:139 `$$(".tab").forEach(t => t.classList.toggle("on", …))` toggles a class only — aria-selected is never set, and there is no tabpanel, so the required state of the declared role is simply missing. app/ui.js:81 segmented() has the same gap. app/chart.js:59 `role: "img", "aria-label": "price chart"` is the entire accessible content of the hero element of the token page, and app/chart.js:136 binds crosshair and tooltip to pointermove/pointerdown/pointerleave only — no focusable element, no keyboard handler. app/parts.css:67 `.tape:hover .tape-track{animation-play-state:paused}` is the only pause mechanism for an infinite marquee that runs from page load (WCAG 2.2.2 requires a mechanism to pause moving content lasting over five seconds).

**Problem.** Keyboard users can Tab out of an open trade sheet into the page behind it and keep operating it while the backdrop suggests otherwise; when the sheet closes their focus is at the top of the document. Screen readers announce eight tabs with no indication which is current. The chart — described in the design system as "the hero" — conveys nothing at all to a non-pointer user, not even the range or the last price it is already rendering as text elsewhere. The marquee cannot be stopped by keyboard or touch.

**Failure scenario.** Precondition: a keyboard-only or screen-reader user opens the trade sheet. Steps: Tab past the confirm button. Impact: focus moves into the Markets list behind the backdrop; Enter navigates away with the sheet still marked aria-modal, leaving an inert dialog over a changed page. Separately, a screen-reader user on the token page reaches an element announced as "price chart, image" and can learn nothing further.

**Root cause.** ARIA roles were applied as labels rather than as contracts, and the sheet was built as a visual overlay rather than as a dialog.

**Fix.** openSheet: record document.activeElement, cycle Tab within the sheet on keydown, and restore focus in closeSheet — about fifteen lines in ui.js. Drop role=tablist/role=tab from the nav in index.html:30-39 (it is site navigation, not a tab widget) and set `aria-current="page"` in ui.js:139's dispatch loop instead; for segmented() set aria-selected on the buttons since that one really is a tab set. chart.js:59: build the aria-label from data already computed — asset, range, first and last price, high and low — and add tabindex=0 with ArrowLeft/ArrowRight moving the crosshair, reusing onMove's index maths. tape: add a pause control, or honour a click on the strip as pause/resume; the hover rule already proves the mechanism works.

**Verifier.** app/ui.js:45-50 openSheet focuses the first input/button and installs a backdrop click handler; there is no keydown trap and closeSheet (ui.js:51) restores nothing. index.html:29-39 declares role="tablist" over eight role="tab" buttons, and app/ui.js:139 toggles only a class — I grepped the whole app for aria-selected and there is none, nor any tabpanel. ui.js:81 segmented() has the same gap. app/chart.js:59 is the only accessibility affordance on the chart (`role:"img", aria-label:"price chart"`), and chart.js:136 binds pointermove/pointerdown/pointerleave with no keyboard handler and no focusable element — I grepped chart.js for keydown/tabindex and found neither. app/parts.css:67 `.tape:hover .tape-track{animation-play-state:paused}` is the only pause mechanism.

---

### `UX·F16` — The chain name is hard-truncated at 40 characters and lands mid-word in eight places on the wrap screen

**P2 · copywriting · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/wrap.js:194 `chain = U.clean(a.chain || "its own chain", 40)` against app/config.js:12 `chain: "Monero — simulated on this network (12 decimals, as Monero)"`. U.clean (app/ui.js:23) ends in `.slice(0, max)` with no ellipsis and no word boundary. Rendered in .sim/ui-dec/02-wrap.png as "MONERO — SIMULATED ON THIS NETWORK (12 D" in the network label, the settlement panel, the custody line, the prose paragraph ("Send Monero on Monero — simulated on this network (12 d to your own deposit address"), step 2's title, step 4's title, the destination field label and its placeholder — eight occurrences on one screen. The same truncation appears at app/wrap.js:348 and :364 with a separate `U.clean(..., 40)` call, and at app/screens.js:66 with `44`.

**Problem.** Every sentence on the screen that names the chain breaks off mid-word. It is the most visible thing on the page and it reads as machine-generated text, on the one screen where the copy is otherwise the app's best work. The prose is also doubly redundant — "Send {name} on {chain}" renders as "Send Monero on Monero — …".

**Failure scenario.** Precondition: any vault whose `chain` field exceeds 40 characters, which both shipped vaults do. Steps: open #wrap. Impact: eight broken sentences; a user reading "Send at least … to that address" and "Your address on Monero — simulated on this network (12 d" cannot tell whether the label is truncated or the configuration is corrupt.

**Root cause.** A sanitiser's length bound is being used as a layout device. `clean()` exists to strip control and bidi characters; the 40 is a safety cap, not a typographic decision, and the config field carries a parenthetical that was written for a different context.

**Fix.** Two changes, both small. In app/config.js and tools/genconfig, keep `chain` to a short proper noun ("Monero", "Zcash") and move the decimals note to a separate field rendered once, in the Level 3 read-outs, rather than concatenated into eight sentences. In app/ui.js:23 clean(), append "…" when the slice actually shortened the string, so a truncation is always visibly a truncation. Then drop the redundant "{name} on {chain}" construction at app/wrap.js:244.

**Verifier.** app/wrap.js:194 `chain = U.clean(a.chain || "its own chain", 40)` against config.js:12's 58-character chain string; U.clean (ui.js:23) ends in `.slice(0, max)` with no ellipsis and no word boundary. Separate 40-caps at wrap.js:348 and :364, and a 44-cap at screens.js:66. .sim/ui-dec/02-wrap.png renders "MONERO — SIMULATED ON THIS NETWORK (12 D" in the network mark, the Settlement row, the Custody row, the prose paragraph ("Send Monero on Monero — simulated on this network (12 d to your own deposit address"), step 2's title, step 4's title, the destination field label and its placeholder — nine occurrences on one screen, and the doubled "Monero on Monero —" redundancy is visible too.

---

### `UX·F3` — On Markets the impersonation warning is a hover-only tooltip behind a glyph shared with two weaker warnings; on Home and the activity feed it does not exist at all

**P2 · risk-disclosure · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/markets.js:142 appends a bare "⚠" for any of four conditions — `dup || s.dupTicker || s.lookalike || s.impersonates` — and app/markets.js:144 puts the only explanation in `p.name.title`, a hover tooltip. app/home.js:12 `H.badges` has no impersonation case, so H.marketRow (Trending, Movers, Trade hub) shows nothing; app/home.js:68-84 `H.feedItem` renders the ticker as a plain link with no mark. The strong copy exists but only reaches the token page and the trade sheet (app/ui.js:90 verified(), app/ui.js:103 `pill bad` "Not real …", app/home.js:21 H.risks). Rendered proof, .sim/ui-imp/02-markets.png: rows 13, 14 read "XMR / DERO ⚠  pool — Monero" and row 24 "WXMR ⚠ curve 0% — Wrapped Monero", while rows 1–12, 15–20 and 23 carry the identical ⚠ merely for sharing the ticker BRW — the glyph fires on roughly 90% of rows. .sim/ui-dec/desk-home.png shows feed entries "Bought 0.000000000024 XMR" and "Sold 0.00000487 ZEC" with no mark of any kind.

**Problem.** The attack the vault symbol reservation was added to stop is a token that calls itself XMR or WXMR. Discovery happens on Markets, Home Trending and the activity feed — the three places where the warning is either hover-only, undifferentiated, or missing. A title attribute is unreachable on touch, unreachable by keyboard, and read by screen readers only in some configurations. A glyph that appears on nearly every row carries no information.

**Failure scenario.** Precondition: anyone lists a token named "Monero" with ticker XMR — the deployed simulator data already contains three. Steps: a Monero holder opens DMZ on a phone, sees "XMR / DERO — Monero — 11.5788 +15.79%" on Markets among twenty other rows all wearing the same ⚠, taps it, and is now on the token page. Impact: on the token page the "Not real XMR" pill does appear, so the informed user is saved one screen late — but the same holder scrolling Home's Trending list or the activity feed sees a token called XMR trading actively with nothing to distinguish it from the wrapped asset the product exists to offer.

**Root cause.** The impersonation flag is computed once in feed.js:214 and consumed by two of five presentation surfaces. The markets row conflates four warnings of very different severity into one glyph because the row has no room budgeted for a differentiated mark.

**Fix.** 1) app/markets.js:142 — stop overloading the glyph. Keep ⚠ for dupTicker/lookalike, and for `s.impersonates` render the existing `.pill.bad` "Not real XMR" node into `parts.badges` instead (it is already styled in ui.css:122 and is the only filled pill in the system, so it reads louder without a new colour). 2) app/markets.js — when `s.impersonates`, also write the reason into the existing `parts.dup` band (it already spans all columns and already renders visible text on both grids), so the warning is readable without hover. 3) app/home.js:12 — add `s.impersonates ? pill("NOT REAL " + s.impersonates, "bad") : null` as the first entry of H.badges, and since `named()` slices badges to one, make the impersonation badge win that slot. 4) app/home.js:81 — in H.feedItem, mark the ticker link when `s.impersonates`.

**Regression test.** Extend tools/uitest/ui.mjs: with an impersonating market listed, assert the Markets row contains visible text matching /Not real/ (textContent, not the title attribute), and assert the same on Home's trending list and in the feed.

**Verifier.** app/markets.js:142 appends one bare "⚠" for `dup || s.dupTicker || s.lookalike || s.impersonates`, and :144 puts the only differentiated explanation in `p.name.title`. app/home.js:12 H.badges has curve/AMM/pool, NEW and HOT and no impersonation case, so H.marketRow (Trending, Movers, Trade hub) shows nothing; H.feedItem (home.js:68-84) renders the ticker as a plain button with no mark. The strong copy exists only in ui.js:90 verified(), ui.js:103 warnings() and home.js:21 H.risks. The "~90% of rows" claim is backed by .sim/ui-dec/02-markets.png: 22 of 24 rows wear the identical glyph (only ZEC row 21 and one BRW row do not).

---

### `UX·F4` — The wrap screen states that doing nothing pays a withdrawal; the contract has no such path

**P2 · honesty · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/wrap.js:337-338 — "There is no operator and no override. Every limit either empties by itself or expires into a payment: doing nothing releases a withdrawal, it never cancels one." app/wrap.js:386 — "payable now, since block " + x.rel + ". The signers pay it without anyone asking; nothing here needs a person." contracts/vault.bas: the only functions that change `w_<j>_st` are Released (line 394, requires `issigner(s)` and a k-of-n vote via votek on a foreign txid) and Veto (line 439, also k signers). There is no height-triggered or permissionless release, no timeout refund, and no caller-initiated claim. Confirmed by enumerating every Function in the file.

**Problem.** The release delay bounds the earliest block at which k signers *may* pay. It does not make payment automatic or inevitable. If the federation stops acting, the user's units are burned into the contract, the claim stays in `sup`, and there is no recourse in the code. The screen states the opposite as a settled fact, in the panel headed "What you are trusting" — the one place the app promises to be exact about the failure modes. This is precisely the class of claim the directive §1.5 forbids.

**Failure scenario.** Precondition: a withdrawal past its release height with fewer than k signers operating (a partial outage, a slashing cascade, or simple abandonment — there is no operator to restart them). Steps: the user burns, reads "doing nothing releases a withdrawal", and waits. Impact: nothing happens, ever. The user's units are gone from their wallet and no foreign coins arrive, while the app told them inaction was sufficient. A user who had been told the truth would have weighed the k-of-n liveness assumption before burning.

**Root cause.** The sentence was written about the veto/halt design (limits that expire rather than persist), and was over-generalised into a claim about settlement liveness, which the contract does not provide.

**Fix.** app/wrap.js:337-338 — replace with the accurate statement: "There is no operator and no override. The delay is the earliest block a withdrawal can be paid, not a guarantee that it will be: paying it takes " + w.k + " signers recording the foreign transaction, and stopping it takes " + w.k + " signers voting to veto or halt. If fewer than " + w.k + " signers act, nothing in this contract can pay you." app/wrap.js:386 — replace "The signers pay it without anyone asking; nothing here needs a person" with "Payable from this block. " + w.k + " signers must record the foreign payment before it settles." Add the same fact as a trustLine(false, …) row so it sits with the other trust assumptions.

**Verifier.** I enumerated every Function in contracts/vault.bas. The only writers of `w_<j>_st` are Released (file:394, requires issigner(s) and votek reaching k on a foreign txid) and Veto (file:439, also k). There is no height-triggered release, no timeout refund, no permissionless claim. wrap.js:337-338 and :386 assert the opposite as fact, inside the "What you are trusting" panel, and .sim/ui-dec/02-wrap.png renders it: "doing nothing releases a withdrawal, it never cancels one."

---

### `UX·F5` — A vault that cannot mint still reports "Open" and invites a deposit — headroom, bond ratio and the mint window are read and never used

**P2 · missing-state · auditor confidence Confirmed · verifier PLAUSIBLE**

**Evidence.** app/wrap.js:233 the Level 1 flag is binary: `flag(halted ? "down" : "acc", halted ? "Halted" : "Open")`. contracts/vault.bas Mint refuses on three further conditions: line 41 `IF sup + amount < sup || sup + amount > LOAD("cap") THEN GOTO 900` (no headroom), line 42 `IF LOAD("cap") / LOAD("capratio") > LOAD("bondtot") THEN GOTO 900` (under-bonded), line 59 `IF LOAD("winmax") != 0 && wm + pm >= LOAD("winmax") THEN GOTO 900` (mint window full). app/wrap.js:21-22 fetches capratio, minbond and winmax; grep confirms capratio, minbond and winmax appear nowhere else in the file, and `headroom` (computed at wrap.js:42) is only printed as a number at wrap.js:239. tools/vaultsigner/main.go:771-774 shows the daemon side already treats over-cap as a silent wait: "deposit … waits for headroom".

**Problem.** Three readable reasons a deposit will never mint are invisible to the user. Two of them (headroom, bond ratio) are computable from data the screen has already fetched. The screen shows "Open", walks the user through registering an index for a fee, and publishes a deposit address, when the contract will refuse the resulting mint. The signer daemon's own behaviour is to wait indefinitely and print to its local console — which nobody is reading, because there is no operator.

**Failure scenario.** Precondition: a vault at or near cap, or one whose signers have unbonded below cap/capratio. Steps: user opens #wrap, sees "Open", pays `regfee` to Register, waits for attestation, sends Monero to the published address. Impact: the signers see the deposit and refuse to vote. The coins sit in the federation's reserve with no wrapped claim issued and no on-chain record the user can point at. The user paid a registration fee and shipped real Monero against a screen that said the vault was open.

**Root cause.** The Open/Halted flag was modelled on the single `halted` key rather than on "can this vault accept my deposit right now", and the extra keys were added to the fetch list without being wired into the state.

**Fix.** In app/wrap.js compute a mintable state next to `halted`: `const noRoom = w.headroom === Z; const underBonded = w.capratio !== Z && w.cap / w.capratio > w.bondtot;` Render the Level 1 flag as one of Halted / At capacity / Under-bonded / Open, and when it is not Open, replace the deposit warning box with a statement of which condition is blocking and disable the Register button the same way the halted path does. For winmax, fetch `mw_<BLOCK_HEIGHT/2000>` and the previous bucket in loadVault and add a "Mint window full — resets at block N" state; that is one extra key read on a screen that already makes several.

**Verifier.** Two of the three claims hold. app/wrap.js:233 really is `flag(halted ? "down" : "acc", …)`, and grep confirms capratio, minbond and winmax are fetched at wrap.js:21-22 and used nowhere; headroom (wrap.js:42) is only printed at :239. contracts/vault.bas Mint line 41 (file:317) refuses over cap and line 59 (file:328) refuses a full mint window. tools/vaultsigner/main.go:771-774 does print "waits for headroom" to a console nobody reads.

---

### `UX·F7` — There is no pending-transaction state anywhere: the sheet closes and a transaction that can take four and a half minutes is reported only by toasts that expire

**P2 · missing-state · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/token.js:216 `const go = async () => { … closeSheet(); await V.app.trade(...) }` — the sheet is dismissed before the wallet is even asked. app/app.js:151-165: the flow is toast("Confirm in your wallet…", 4s) → toast("… sent", 4.2s) → `await app.waitTx(txid)` → a final toast. app/app.js:177 `for (let i = 0; i < 180; i++) { await new Promise(r => setTimeout(r, 1500)); … }` — up to 270 seconds of silence. app/ui.js:41 `setTimeout(() => t.remove(), ms)` and .toast markup has no dismiss control (app/ui.css:117). app/app.js:139 `if (inFlight) { toast("A transaction is already in progress", "err"); return null; }` names nothing and gives no elapsed time. The directive §5.7 explicitly enumerates Awaiting approval / Executing / Pending / Completed / Failed / Expired as states to design; none of them has a persistent surface.

**Problem.** For up to four and a half minutes after signing, the interface is indistinguishable from one where nothing happened. Every other action is blocked with an error toast that does not say what is blocking it. The failure toasts — "not in a block after 4.5 minutes", "included but did not execute: only the network fee was spent" — carry the most important information the app ever produces, are shown for 8–9 seconds, cannot be paused or dismissed deliberately, and are recoverable nowhere: there is no transaction list in the app.

**Failure scenario.** Precondition: any trade, burn, register, mint or post. Steps: user signs in the wallet, switches tabs or looks away for fifteen seconds, comes back. Impact: no indication a transaction exists. If they retry, they get "A transaction is already in progress" with no context. If the transaction reverted while they were away, the explanation has already vanished and there is no way to retrieve it — on a platform whose own notice page says there is no support and nobody can act on your behalf.

**Root cause.** The transaction lifecycle was built as a sequence of ephemeral notifications rather than as state the UI renders. Nothing owns "a transaction is in flight".

**Fix.** Add a single persistent in-flight strip owned by app.js — one row under the header showing the describe string, the txid, elapsed time and the current phase (awaiting approval → sent → waiting for a block → checking the outcome), cleared only on a terminal state. It reuses .toast styling and costs well under the DOC headroom (app.js is 27KB raw, roughly 11KB DOC against an 18,432 cap). Give app/ui.js:39 toast() a close button and stop auto-removing kind==="err" toasts. Keep the last ten outcomes in a bounded array and render them at the foot of Portfolio, where the cost-basis ledger already lives.

**Verifier.** app/token.js:216 closes the sheet before app.trade is awaited. app/app.js:151-165 is toast → toast → `await app.waitTx(txid)` → toast, and app.js:177 loops 180 × 1500 ms = 270 s. app/ui.js:41 removes every toast on a timer and .toast (app/ui.css:117) has no dismiss control. app.js:139 blocks concurrency with a message that names nothing. I grepped: there is no transaction list, no persistent in-flight surface, and no state machine anywhere in app/.

---

### `UX·P8` — Two buttons pass a boolean where the helper expects an attributes object, so they are never disabled — including Burn on a halted vault

**P2 · correctness · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/ui.js:84 `function button(label, cls, onclick, attrs) { return h("button", Object.assign({ class: …, type: "button", onclick }, attrs || {}), label); }` — Object.assign with a boolean primitive copies no own enumerable properties. app/wrap.js:369 `button("Burn and withdraw", "primary", () => doBurn(w, amount, dest), halted)` passes the boolean. app/chat.js:176 `button("Send", "small primary", send, !V.wallet.connected)` does the same. The correct form is used two lines earlier at app/wrap.js:280 `halted ? { disabled: true } : null` and at app/nft.js:45 `{ disabled: !ready }`. The halted warning box (app/wrap.js:248-252) states that burning is refused while halted, and contracts/vault.bas:368 confirms it: `IF LOAD("halted") != 0 || LOAD("sealed") == 0 THEN GOTO 900`.

**Problem.** On a halted vault the Register button greys out and the Burn button stays a full-strength lime primary — the loudest control on the screen — directly beneath a box explaining that burning is refused. The affordance contradicts the copy. The chat instance is masked because the input is separately disabled and send() returns on empty text, but it is the same latent defect.

**Failure scenario.** Precondition: a vault whose signers have voted a halt (contracts/vault.bas:473 Halt). Steps: user reads "This vault is halted… burning are refused", sees an enabled Burn button, fills in an amount and destination, and presses it. Impact: at minimum a confusing dead end; the call is built and sent to the gas estimator and then to the wallet, so depending on how the daemon simulates a RETURN 1 the user may be asked to sign, and may pay a network fee for a call that cannot execute.

**Root cause.** An untyped fourth parameter with two incompatible call conventions in the same file.

**Fix.** app/wrap.js:369 — `halted ? { disabled: true } : null`. app/chat.js:176 — `!V.wallet.connected ? { disabled: true } : null`. Additionally disable the amount and destination inputs in burnForm when halted, so the form does not invite entry. Harden app/ui.js:84: `Object.assign({…}, (attrs && typeof attrs === "object") ? attrs : attrs === true ? { disabled: true } : {})` so the boolean convention at least does the obvious thing.

**Regression test.** tools/uitest/ui.mjs already exercises the wrap screen; with a halted vault in the simulator, assert `document.querySelector('.btn.primary') .disabled === true` on #wrap.

**Verifier.** app/ui.js:84 `Object.assign({...}, attrs || {})` — a boolean primitive contributes no own enumerable properties, so `disabled` is never set. app/wrap.js:369 and app/chat.js:176 are the only two call sites passing a boolean; I grepped every button() call in app/ to confirm there are no others. The correct form is used two lines earlier at wrap.js:280 (`halted ? { disabled: true } : null`) and at nft.js:45. contracts/vault.bas Burn line 22 (file:368) does refuse while halted, and the warnbox at wrap.js:248-252 does say so.

---

### `ARCH·A1` — dupTicker and pool naming are implemented twice; the copy in token.js runs on the deep-link route and has already drifted — a shared link to the genuine wrapped market shows a hex ticker and a false "Ticker shared" warning

**P3 · duplicated-logic/drift · auditor confidence Confirmed · verifier PLAUSIBLE**

**Evidence.** Authoritative copy: `app/feed.js:203-220` (`decorate()`) — assetVerified naming, then the vault-record naming (`s.ticker = clean(bv.sym…); s.wrappedBase = true; s.vaultRec = bv;`), then `nameQuote`, then the tickerKey groups, then `for (const g of groups.values()) if (g.length > 1 && g.every((s) => s.wrappedBase)) for (const s of g) s.dupTicker = false;`
Second copy: `app/token.js:35` — one statement, 1003 characters long, reached whenever a market is opened by id before the registry has it. It reproduces the assetVerified branch and an inline `D.list().some(...)` dupTicker scan including the guard `!(s.wrappedBase && x.wrappedBase && x.asset === s.asset)` — but it never runs the vault-record naming that SETS `s.wrappedBase`, and never sets `s.vaultRec`.
A third copy of the warning prose: `app/markets.js:144` repeats the `warnings()` titles from `app/ui.js:130-134` verbatim.

**Problem.** On the deep-link path `s.wrappedBase` is always undefined, so the exemption the guard was written for can never fire, and `s.ticker` stays whatever `loadContract` left it: `app/feed.js:186` `s.ticker = s.asset.slice(0, 4).toUpperCase()`. `app/ui.js:126` also keys the "Wrapped asset · verified" badge on `s.wrappedBase && s.vaultRec`, so that is lost too. The route this affects is the one a shared link always takes — the comment at `app/token.js:31` says exactly that about a different bug fixed on the same line.

**Failure scenario.** Precondition: the app is cold (`D.assets` empty) or the wrapped market is not yet in the registry. Steps: (1) someone shares `#token/<wrapped pool scid>`; (2) the recipient opens it; (3) `D.assets.get(scid)` misses, `loadContract` runs, the token.js branch executes. Impact: the page titles the genuine wrapped-Monero market "D69C" instead of "XMR", drops the "Wrapped asset · verified" badge for the generic "Verified", and — if a second wrapped pool exists — stamps it "Ticker shared". The scam-detection UI points at the real asset. `tools/uitest/ui.mjs:134-137` asserts precisely that this must not happen, but only via the Markets list, never via a deep link.

**Root cause.** `decorate()` operates on the whole map; the deep-link case needs it for one market, so it was reimplemented inline instead of being factored into a per-market function the map loop also calls.

**Fix.** Extract `data.decorateOne(s)` from `decorate()` — assetVerified, vault-record naming, nameQuote, lookalike/impersonates — and have `decorate()` call it in a loop and `token.js:35` call it once. The only genuinely map-wide rule is the dupTicker grouping; give that its own `data.markDuplicates()` that token.js can call with `[...D.list(), s]`.

**Regression test.** Extend `tools/uitest/ui.mjs`'s impersonation block: reload the page at `#token/<wrapped pool scid>` with an empty cache and assert the h1 is the vault symbol and that no "Ticker shared" pill is rendered.

**Verifier.** The code duplication is real: app/feed.js:200-222 decorate() does assetVerified naming, then the vault-record naming at 207 (`s.wrappedBase = true; s.vaultRec = bv;`), nameQuote, the tickerKey groups, and the all-wrapped exemption at 219. app/token.js:35 is a single 1000+ character statement that reimplements the assetVerified branch and an inline dupTicker scan carrying the guard `!(s.wrappedBase && x.wrappedBase && x.asset === s.asset)` — and it genuinely never runs the vault-record naming, so s.wrappedBase and s.vaultRec are never set on that path, and app/ui.js:94 (not :126) keys the 'Wrapped asset · verified' badge on exactly those two fields. But the stated scenario does not follow. app/token.js:23 is `let s = D.assets.get(scid)` — when the registry already holds the market the whole block at 25-35 is skipped. When it does not, line 30 waits up to 15 s for D.lastRefresh, and line 34 re-reads `D.assets.get(scid)`; after a successful refresh that finds the decorated object, so wrappedBase/vaultRec survive and the else-branch only overwrites lookalike/dupTicker, never ticker.

---

### `ARCH·C2` — Nothing asserts app/templates == Canon(contracts); the tests test one file and the app ships the other, and the only bridge needs a live simulator and a wallet

**P3 · config-pipeline/untested-invariant · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** Tests read `contracts/`: `tests/pool_test.go:38` `os.ReadFile("../contracts/" + name)` then `ref.Canon(...)`; `tests/canon_test.go:16,61` glob `../contracts/*.bas`. `tests/run.sh` copies `reference`, `tests` and `contracts` into vendor — never `app/templates`.
The app ships `app/templates/`: `app/forms.js:15` fetches `t.file` = `templates/<kind>.bas`.
The only writer of `app/templates/` is `tools/simdeploy/main.go:104-108`, inside a `main()` that first requires a reachable daemon (`c.Info()`), an owner wallet and a treasury wallet, and installs three contracts.
`tools/genconfig/main.go` compares `app/templates` ↔ `networks.json` only, and only in `-dist` mode; the `-network X` path that writes `app/config.js` performs no check at all.
Measured today they do agree (Canon(contracts/pool.bas) = 33a126a8… = sha256(app/templates/pool.bas)), so this is a latent hole, not a live break.

**Problem.** The repo's stated source of truth (`contracts/*.bas`) and the artifact users actually execute (`app/templates/*.bas`) are two files kept equal by remembering to run a deployment tool. A contract edit made without a running simulator — the normal case for a review fix, a comment rewrite, a one-line guard — leaves the tested source and the shipped source different, and both `go test` and `genconfig -dist` pass, because the stale templates still match the stale hashes recorded beside them.

**Failure scenario.** Precondition: no simulator running (a plain `git clone` and `tests/run.sh`). Steps: (1) fix a bug in `contracts/pool.bas`; (2) `tests/run.sh` — all green, because the tests read contracts/; (3) `go run ./genconfig -dist` — green, because app/templates and networks.json still agree with each other; (4) ship. Impact: the fix is tested and not deployed. Every market installed by `forms.js` runs the old code, and `feed.js verify()` blesses it as matching the shipped template, because it does.

**Root cause.** The derivation `app/templates = Canon(contracts)` is performed as a side effect of a deployment action rather than expressed as a checkable invariant, and it lives in the one tool that cannot run without a chain.

**Fix.** Add to `tests/canon_test.go` (which already has the contracts glob and imports `ref`): for each of pool, launch, nft, founders, assert `ref.Canon(read("../contracts/"+k+".bas")) == read("../app/templates/"+k+".bas")` and that its sha256 equals `deploy/networks.json` → the current network's `templates[k].sha256`. Two files must be copied by `tests/run.sh` for this (`app/templates`, `deploy/networks.json`).

**Regression test.** The test described above. Deleting a line from `contracts/pool.bas` must turn it red.

**Verifier.** tests/run.sh copies only reference, tests and contracts into vendor/derohe/veil — I read the file; app/templates is never copied. tests/pool_test.go:71 `readContract(t, "pool.bas")` reads ../contracts. app/forms.js:15 fetches templates/<kind>.bas. tools/simdeploy/main.go:57 `func read(p string) ... ref.Canon(...)` and lines 97-101 write app/templates from contracts, inside a main() that requires a reachable daemon and two wallets. tools/genconfig/main.go compares app/templates to networks.json only, and only inside bundle() — the `-network X` branch (lines 44-56) writes app/config.js with no check at all. I verified the derivation currently holds: comment-and-blank-stripped sha256 of contracts/{pool,launch,nft,founders}.bas equals sha256 of the matching app/templates file (33a126a8, 09cb53d2, f8b85449, 49009d2c). So this is a missing guard, not a live break — which is exactly how the finding states it.

---

### `ARCH·D1` — networks.json's vaultAsset block is dead data that contradicts the live record for the same contract — 5 decimals against 12 — and netcfg.Save silently deletes any field the struct does not know

**P3 · configuration/dead-data · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `deploy/networks.json` simulator: `"vault": "d69c933f…"` with `"vaultAsset": { "name": "Monero", "sym": "XMR", "chain": "Monero — simulated on this network", "dec": 5, "conf": 10 }`, and immediately below `"vaults": [{ "scid": "d69c933f…", …, "dec": 12, "conf": 10 }]` — same SCID, different decimals.
`tools/netcfg/netcfg.go` `ConfigJS` builds `recs` from `n.Vaults`, appends `{n.Vault, n.VaultAsset}` only if that SCID is not already present, then emits `vaultAsset:` from `recs[0]`. Since vaults[0] has the same SCID, `n.VaultAsset` is never read. Confirmed in output: `app/config.js` line 13 carries `dec: 12`, not 5.
`app/feed.js:29-36` `vaults()` applies the same precedence, so the app also ignores it.
`tools/netcfg/netcfg.go` `File.Save` writes `out[k] = v` from the parsed `*Network` struct — any JSON key not on the struct is dropped on the next `simdeploy` run. `tools/simdeploy/main.go:100` documents that this class of bug already bit once ("silently dropped every field it did not know about — the vault and its asset record among them").

**Problem.** The file's own `_doc` calls it "Source of truth for per-network deployment", and it holds a wrong decimal count for a live vault in a field that looks authoritative. Anyone reading it to answer "how many decimals does the XMR vault have" gets 5 — the answer that produces the Q1 class of bug — and only reading netcfg.go reveals the field is ignored. The fix that was applied for the dropped-field problem moved the drop from ConfigJS into netcfg.Save; the hazard is relocated, not removed.

**Failure scenario.** Precondition: a maintainer changing vault decimals or adding a chain. Steps: (1) they edit `simulator.vaultAsset.dec` because it is the field named after the thing; (2) regenerate; (3) nothing changes, because vaults[0] wins. Or: (1) they add a field to networks.json by hand; (2) run simdeploy for an unrelated reason; (3) `f.Save` writes the struct back and the field is gone with no message.

**Root cause.** A single-vault schema was superseded by a list and kept for compatibility, but kept as writable duplicate state rather than as a rendered-only alias, and the reader/writer asymmetry is documented nowhere in the file itself.

**Fix.** Delete the `vaultAsset` key from every network block in `deploy/networks.json` (`ConfigJS` already synthesises it from `recs[0]` for the app's back-compat), and drop `VaultAsset` from `netcfg.Network`, keeping only `Vaults []VaultRec`. If it must stay for one release, correct `simulator.vaultAsset.dec` to 12 so the file is at least not self-contradicting.

**Regression test.** A netcfg unit test that loads a fixture with an unknown key, saves it, and asserts the key survives; plus one asserting that no network block carries both `vault` and a `vaults` entry with the same scid and different `dec`.

**Verifier.** deploy/networks.json simulator block: `"vault": "d69c933f..."` with `"vaultAsset": { ... "dec": 5, "conf": 10 }` and immediately below `"vaults": [{ "scid": "d69c933f...", ... "dec": 12, "conf": 10 }]` — same SCID, dec 5 against dec 12. Confirmed. tools/netcfg/netcfg.go ConfigJS builds `recs` from n.Vaults, appends {n.Vault, n.VaultAsset} only when that SCID is absent (it is present), and emits `vaultAsset:` from recs[0] — so n.VaultAsset is dead. Output confirms it: app/config.js line 15 carries `dec: 12`. app/feed.js:29-32 vaults() applies the same precedence. File.Save writes `out[k] = v` from the *Network struct, so any JSON key not on the struct is dropped silently on the next simdeploy run, and tools/simdeploy/main.go:112-114 documents that this class of bug already bit once.

---

### `ARCH·G1` — The gas-estimate RPC is built twice and the two copies have already drifted on the field that matters: sheets.js estimates with a decoy signer for privacy, app.tx then estimates the same call with the real address

**P3 · duplicated-logic/privacy · auditor confidence Confirmed · verifier PLAUSIBLE**

**Evidence.** `app/sheets.js:23-35` `Sh.gas`: `if (!gasAddr) gasAddr = await V.node.randomAddress();` … `V.node.gasEstimate({ scid, ringsize: 2, signer: gasAddr, sc_rpc: rpc, transfers })`, with the comment "asking 'what would this cost' must not tell the node who is about to act".
`app/app.js:122`: `V.node.call("DERO.GetGasEstimate", Object.assign({ transfers, sc_rpc: estRPC, ringsize: ring }, ring === 2 ? { signer: W.address } : {}, base))`, with its own comment about not naming the payer at ring 16.
Both independently build the `SC_ACTION` / `SC_ID` argument tail and both hard-code the same `+ 200` fee pad (`app/app.js:122` `gs + 200n`; `app/sheets.js:34` `BigInt(r.gasstorage) + 200n`).
`app/token.js:224` calls `V.sheets.gas` for the same entrypoint that `app/token.js:255` → `V.app.trade` → `app.tx` then estimates again.

**Problem.** The privacy property sheets.js is written to protect is undone seconds later by app.tx on the identical call, so the decoy buys nothing while costing a second round trip and a divergence in what the two estimates measure (a different signer can produce a different storage-gas figure, so the number shown before the button and the number the fee is computed from need not agree). Beyond that, `gasAddr` is cached for the session, so every estimate this browser makes uses one decoy — a linkable per-session identifier handed to the node.

**Failure scenario.** Precondition: a trade sheet open with an amount typed. Steps: (1) `schedGas` fires `Sh.gas` with `signer: <session decoy>`; (2) the user presses Buy; (3) `app.tx` estimates the same entrypoint with `signer: W.address`. Impact: the node learns the real address anyway; and if the two estimates differ the user is quoted one fee in the cost block and charged another. A maintainer changing the pad, the argument shape or the ring rule has to find and change two places that no test compares.

**Root cause.** `Sh.gas` was added for the pre-commit cost display without extracting the estimate construction already inside `app.tx`, so the same protocol call now has two spellings maintained by different files.

**Fix.** Extract `app.estimate(opts)` from `app/app.js:115-127`, returning `{fee, gasstorage}`, and have `Sh.gas` call it and format the result. Decide the signer question once — either both use the real address (honest, and matches what the transaction will do) or both use a decoy and app.tx accepts the resulting fee variance.

**Regression test.** Assert in the Playwright pass that the DERO figure in the sheet's "Network fee (gas)" row equals the fee named in the "Confirm in your wallet… network fee X DERO" toast for the same trade.

**Verifier.** The duplication is real and verified: app/sheets.js:26-36 Sh.gas builds its own SC_ACTION/SC_ID tail, caches `gasAddr` from V.node.randomAddress() for the session, calls gasEstimate with `signer: gasAddr`, and pads `BigInt(r.gasstorage) + 200n`. app/app.js:123 builds estRPC independently and app.js:130 calls DERO.GetGasEstimate with `ring === 2 ? { signer: W.address } : {}` and the same `+ 200n` pad. app/token.js:224 does call Sh.gas for the same entrypoint that token.js:255 -> V.app.trade -> app.tx re-estimates. So a maintainer must change two places, and no test compares them.

---

### `ARCH·M1` — Protocol constants are unnamed literals in four to twelve places each, and one of them (the thin-market threshold) is compared against a figure whose units now vary per market

**P3 · magic-numbers/protocol-constants · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** Fee split: `deploy/networks.json` ×2 blocks; hard-coded again at `tools/simdeploy/main.go:137` `n.FeeBps, n.PfeeBps, n.LaunchPfeeBps = 50, 5000, 5000` (which overrides whatever the file said); again at `tools/simsmoke/main.go:61`, `tools/simwrapmkt/main.go:85`, `tools/simlaunch/main.go:168`, `tools/simnft/main.go:35`. `app/feed.js:172` rejects a listing whose fee differs from `cfg.feeBps`, so any tool left behind produces markets the app refuses.
Reward accumulator scale 2³²: `contracts/founders.bas:247,249,255,273,277` and independently in JS at `app/founders.js` `myFounders` — `(hu * (f.acc - hd)) / 4294967296n`.
Vote bucket 2000 blocks: `contracts/vault.bas:286,312,319,325,350,404,447,480` and `tools/vaultsigner/main.go:622,631`.
AMM invariants: named in `reference/pool.go` (`MinLiq = 1000`, `FirstDepositMin = 100000`), bare literals in `app/amm.js:27,33`.
Block time 18s: `app/stats.js:73,122`, `app/nft.js:49`, `app/wrap.js:118`.
Thresholds: `app/home.js:24` `if (s.m.liq < 20)`; `app/home.js:12` `s.m.hot > 12`; `app/stats.js:73` `m.age < 400`.

**Problem.** The vaultsigner case has the worst failure mode: `scope := func(h int64) string { return se + "_" + he + "_" + strconv.FormatInt(h/2000, 10) }` must agree with `BLOCK_HEIGHT() / 2000` in the contract. If the contract's bucket ever changes, `vote()` reads a key the contract never wrote, reports "included but the contract did not record our vote: will vote again", and loops forever paying fees — on a daemon with no operator. The `liq < 20` case is already wrong today: `m.liq` is denominated in the market's quote (`app/stats.js:70` divides by `qunit(s)`), so "thin market" means 20 DERO on one market and 20 XMR — four orders of magnitude more value — on another.

**Failure scenario.** Precondition: a wXMR-quoted market with 5 XMR of liquidity. Steps: (1) `enrich` computes `m.liq = 5`; (2) `H.risks` at home.js:24 fires; (3) the token page and the trade sheet both display "Thin market: about 5 XMR of liquidity. Small orders move the price a lot." Impact: a deep market is labelled thin, so the warning that is supposed to mean something stops meaning anything — and the converse (a genuinely thin market above the threshold in a low-decimal quote) carries no warning at all.

**Root cause.** DVM-BASIC has no constants, so the contracts have an excuse; the Go and JS sides copied the literals rather than naming them, and one threshold was written when every market was quoted in DERO.

**Fix.** Immediately: name the vault bucket once (`const voteBucket = 2000` in `tools/vaultsigner/main.go`, used at :622 and :631, with a comment pointing at `contracts/vault.bas:286`). Delete `tools/simdeploy/main.go:137` and read the fee values from the loaded network block instead of overwriting them. Make `H.risks`' thin test relative — compare `m.liq` against the market's own 24h volume, or express the threshold per quote in the vault record — rather than against a bare 20.

**Regression test.** A Go test that greps `contracts/vault.bas` for `BLOCK_HEIGHT() / <n>` and asserts `n` equals the vaultsigner constant — the same trick `tests/run.sh` already uses for the no-floating-point rule.

**Verifier.** Verified each cited literal. Fee split: tools/simdeploy/main.go:137 `n.FeeBps, n.PfeeBps, n.LaunchPfeeBps = 50, 5000, 5000` does override the file; the same 50/5000 appears in simsmoke:61, simwrapmkt:85, simlaunch, simnft; app/feed.js:156 rejects a listing whose fee differs from cfg.feeBps, so a stale tool does produce markets the app refuses. 2^32: contracts/founders.bas:247,249,255,273,277 (plus the muldiv helper at 333-346) and app/founders.js. Vote bucket: '2000' occurs 15 times in contracts/vault.bas, and tools/vaultsigner/main.go:622 `strconv.FormatInt(h/2000, 10)` plus :631 must match it by hand. reference/pool.go:7-8 names MinLiq = 1000 and FirstDepositMin = 100000; app/amm.js:27 and :33 carry them as bare 100000n / 1000n. Block time 18 s at stats.js:73,122, nft.js:49, wrap.js:118. The headline case checks out exactly: app/home.js:24 `if (s.m.liq < 20)` against stats.js:70 where m.liq is divided by qunit(s), i.e. the market's own quote — so 'thin' is 20 DERO on one market and 20 XMR on another. The message text does name the quote (D.qsym(s)), so the label is honest; the threshold is not value-comparable.

---

### `ARCH·Q2` — "Quote decimals" exists in two incompatible types (BigInt from D.qdec, Number on s.quoteDec) and A.price silently defaults to 5 when handed the BigInt one

**P3 · api-design/silent-fallback · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `app/feed.js:65` sets the Number spelling: `s.quoteDec = !s.quote ? 5 : v && Number.isInteger(v.dec) ? v.dec : 5;`
`app/feed.js:70` exposes the BigInt spelling: `qdec(s) { return BigInt(Number.isInteger(s && s.quoteDec) ? s.quoteDec : 5); }`
`app/amm.js:99` guards with `Number.isInteger(qdec) ? qdec : 5` — and `Number.isInteger(12n)` is `false`.
`D.qdec` is the accessor everything else reaches for: `app/token.js:78,88,131,140`, `app/screens.js:118`, `app/home.js:60`, `app/sheets.js:47`.

**Problem.** `D.qdec(s)` is the documented way to ask a market for its quote decimals, and it is the one value `A.price` cannot accept. A maintainer fixing Q1 by writing `price(pt, s.dec, D.qdec(s))` — the obvious spelling, using the public accessor — reintroduces the exact bug with no error, no warning and no test failure.

**Failure scenario.** Precondition: Q1 is fixed by anyone who has not read amm.js. Steps: (1) developer writes `A.price(p, s.dec, D.qdec(s))` because `D.qdec` is what every neighbouring line uses; (2) `Number.isInteger(12n)` is false; (3) the function falls back to 5. Impact: prices silently revert to being 10⁷ out, the fix appears to have been applied, and the only place the discrepancy shows is a chart nobody has an automated check for.

**Root cause.** One quantity, two representations, chosen per call site by whichever module needed BigInt arithmetic. `A.fmtUnits` coerces with `dec = Number(dec)` and so tolerates both; `A.price` does not, and treats the mismatch as "unspecified" rather than as an error.

**Fix.** Make `A.price` coerce like `A.fmtUnits` does — `const q = qdec === undefined ? 5 : Number(qdec)` — so both spellings work, or better, drop the parameter per Q1 and take the market.

**Regression test.** A unit assertion that `A.price({rd:1n,ra:1n}, 5n, 12n) === A.price({rd:1n,ra:1n}, 5n, 12)`. There is no JS test runner in the repo at all, which is itself finding X1.

**Verifier.** Verified both spellings exist: app/feed.js:65 `s.quoteDec = ... v.dec : 5` (Number) and app/feed.js:70 `qdec(s) { return BigInt(...) }` (BigInt). app/amm.js:99 guards with `Number.isInteger(qdec)`, which is false for any BigInt per spec. D.qdec is indeed the accessor everything reaches for (token.js:63,105,113,141,143,151; sheets.js:40,99; home.js:57,76; markets.js:50; screens.js:108; app.js:88). A.fmtUnits does coerce (`dec = Number(dec)`), A.price does not. So the described trap is real as a code fact.

---

### `ARCH·X1` — amm.js is a third implementation of the AMM maths with no test of any kind; the contract and the Go reference are fuzz-differentialed against each other, and the JS that picks min_out is not

**P3 · test-coverage/false-assurance · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `app/amm.js:1` describes itself as "BigInt mirror of reference/*.go". Contract ↔ reference is genuinely tested: `tests/pool_test.go:355 TestPoolFuzz`, `tests/launch_test.go:405 TestLaunchFuzz`, plus the live-chain differentials in `tools/simsmoke` and `tools/simlaunch` (`check("AddLiquidity", c.GetSC(...), m)`).
There is no JS test runner, no package.json, no node test target. The only JS-side check is `tools/uitest/ui.mjs`, and every one of its ~40 assertions is a substring match on rendered prose — `if (!/minimum deposit/i.test(wt)) fail(...)`, `if (!t.includes("BRW")) fail(...)`, `if (!/16×/.test(pv)) fail(...)`. None compares a computed quote, price, fee or minimum-received against an independently derived value.

**Problem.** `app/amm.js` is what chooses `min_out` on every trade (`app/token.js:255` `A.minOut(q.out, slip)`), what disables the confirm button, and what the impact warnings are computed from. If it drifts from `pool.bas`/`launch.bas`, the symptom is a `min_out` that is systematically too high (trades revert and the user pays gas for nothing) or too low (the slippage guard stops guarding). Q1 and F1 are both instances of that drift and both went unnoticed. Meanwhile the UI test's shape means a wording change breaks the build and a wrong number does not — the assurance runs opposite to the risk.

**Failure scenario.** Precondition: any change to the fee or curve arithmetic in `pool.bas`. Steps: (1) update the contract; (2) update `reference/pool.go`; (3) `tests/run.sh` goes green because those two now agree; (4) `app/amm.js` is untouched. Impact: every quote in the app is wrong and every trade reverts on `min_out`, with the full Go suite passing.

**Root cause.** The differential testing strategy stops at the language boundary, and there is no JS test target to extend, so extending it means introducing tooling into a repo whose stated constraint is no build step and no runtime dependencies.

**Fix.** Add a dependency-free `tests/amm.mjs` run by `node --test` (or plain `node tests/amm.mjs`): load `app/amm.js` into a stub `window`, and assert `poolBuy`/`poolSell`/`launchBuy`/`launchSell` against a table of vectors exported from the Go fuzz run. Generating the table is a `t.Logf` in `TestPoolFuzz` writing JSON — no new dependency on either side, and node is already required for `tools/uitest`.

**Regression test.** The vector test itself. Changing a single `bpsUp` to `bps` in amm.js must fail it.

**Verifier.** app/amm.js:1 does describe itself as a BigInt mirror of reference/*.go. No package.json anywhere outside research/ and vendor/ (checked with find -maxdepth 2), no node test target, so there is no JS test runner. Contract-vs-reference IS covered (tests/pool_test.go TestPoolFuzz, tests/launch_test.go TestLaunchFuzz, plus the live differentials in simsmoke/simlaunch). app/token.js:255 does select min_out via A.minOut(q.out, slip). tools/uitest/ui.mjs has 44 fail() calls; I read them all and none compares a computed quote, price, fee or minimum-received against an independently derived value — the substantive claim holds.

---

### `DATA·D3-06` — The Markets table ranks and prints Vol / Liquidity / FDV across quotes on raw magnitude, and on desktop those three columns carry no currency at all

**P3 · correctness / cross-quote · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/markets.js:81 `const key = { hot: s => s.m.hot, vol: s => s.m.vol24, chg: ..., liq: s => s.m.liq, mcap: s => s.m.mcap || 0, age: ... }[sort]` and :86 `out.sort((a, b) => key(b) - key(a) || b.m.vol24 - a.m.vol24)` — the tiebreaker is cross-quote too. app/markets.js:166 `p.vol.textContent = S.fmtDeroShort(s.m.vol24); p.liq.textContent = S.fmtDeroShort(s.m.liq); p.mcap.textContent = ...` — the `.mcol` cells, which are the desktop grid's columns, get bare numbers; only the phone-only `.msub` line (markets.js:165) prefixes `D.qsym(s)`. app/token.js:69 `band(mini("FDV", ...), mini("Liquidity", ...), mini("Volume 24h", ...))` — the token page's Market band is unlabelled the same way, on a page whose price eyebrow *is* labelled 'Price · XMR' (token.js:54). feed.js:267 D.trending sorts on m.hot then m.vol24; stats.js:72 m.hot includes `Math.log1p(f1.vol)`, a quote-denominated term.

**Problem.** docs/TODO.md §1.3 records the cross-quote *sorting* as a known open item, so the ranking is not new. What is not recorded, and is worse, is that the desktop columns print these numbers with no unit: a column headed 'Liquidity' shows 0.0000 for a wXMR market on the row above 5,412 for a DERO one, and the head is a sort button that orders them against each other. Portfolio (screens.js:172) tells the user in prose that DMZ has no oracle and must never have one; the Markets table performs the comparison the Portfolio refuses.

**Failure scenario.** Precondition: markets exist in more than one quote. Steps: Markets → sort by Liquidity or FDV on a desktop width. Impact: the ranking is an implicit exchange rate the app does not have, and a reader has no way to tell which rows are XMR and which are DERO from the columns alone. Home does the same at home.js:104-107, picking the headline 'Flow · last 24 hours' figure as whichever quote has the largest raw number.

**Root cause.** m.vol24, m.liq and m.mcap are stored as bare floats with the quote recorded only on the market record, so any consumer can compare them without noticing they are different currencies.

**Fix.** Sort within a quote and interleave by rank, not by magnitude (or default the table to one quote with a chip to switch); and print `D.qsym(s)` in each of the three .mcol cells and in the token page's Market band.

**Regression test.** UI-test assertion over synthetic markets in two quotes: every rendered Vol/Liquidity/FDV cell must contain a currency symbol, and sorting by liq must not place a market of quote A between two of quote B.

**Verifier.** app/markets.js:81 is the sort-key map exactly as quoted, and markets.js:87 `out.sort((a, b) => key(b) - key(a) || b.m.vol24 - a.m.vol24)` — the tiebreaker is cross-quote too. markets.js:166 sets `p.vol`, `p.liq`, `p.mcap` (the `.mcol` desktop cells, created at markets.js:64) from S.fmtDeroShort with no symbol; only the phone-only `.msub` line at markets.js:165 prefixes `D.qsym(s)`. app/token.js:69 `band(mini("FDV", ...), mini("Liquidity", S.fmtDeroShort(m.liq)), mini("Volume 24h", ...))` is unlabelled on a page whose price eyebrow at token.js:54 is `"Price · " + D.qsym(s)`. feed.js:267 trending sorts on m.hot then m.vol24, and stats.js:72 folds `Math.log1p(f1.vol)` into m.hot.

---

### `DATA·D3-07` — Graduation and Buyback are counted as trading volume and as directional pressure, because the LP-move filter only runs on pools

**P3 · correctness / derived stats · auditor confidence High · verifier PLAUSIBLE**

**Evidence.** app/stats.js:25 `function lpMove(s, a, b) { if (s.kind !== "pool") return false; ... }` — every reserve move on a launch is treated as a trade. contracts/launch.bas:71-73, at graduation, replaces the virtual reserve: `STORE("phase", 1) / STORE("rd", real + deff) / STORE("ra", LOAD("Lq"))`, dropping rd by vd0 = grad/3 (launch.bas:44) in one step. contracts/launch.bas:33-34, Buyback: `STORE("rd", rd + d) / STORE("ra", ra - out)` with no `post()` call. Both land in the next price point via tick() (launch.bas), so flow() at stats.js:46 sees them as |Δrd|.

**Problem.** A graduating launch books a phantom sell of grad/3 in the block it graduates: for a 10,000 DERO target that is 3,333 DERO of volume that nobody traded, plus one 'down' block. Because the curve is built so price is continuous across graduation (price before = price after = 4G/c), the candle is a doji with a large red volume bar — volume with no price move. Buyback books a phantom buy of up to 1% of the reserve every 20 blocks. Both flow into m.vol24, the Volume 24h column, the candle volume pane, upBlocks24/downBlocks24 ('Buy pressure' on Home, 'Block pressure · 24h' on the token page) and m.hot — where `Math.log1p(3333) * 8` saturates the 50-point cap, so a graduating token is pushed to the top of Trending by an event that is not a trade.

**Failure scenario.** Precondition: a launch reaching 100% of its curve, or any tmode-2 launch with accrued tax. Steps: watch the token page across the graduating block. Impact: Volume 24h jumps by grad/3 with no corresponding trade; the token page reports the block as sell pressure; Trending re-ranks on it. The same reserve move also makes the 24h change window straddle a discontinuity in `ra` that is not a market event.

**Root cause.** lpMove encodes 'this reserve move is not a trade' as a pool-only concept, but launch.bas performs two non-trade reserve moves of its own that the app has no rule for.

**Fix.** In flow() and candles(), skip the boundary where `phase` changes from 0 to 1 (detectable from `gh`, which loadContract already reads, versus the point's height), and skip a point whose ra falls while rd rises with no matching t_ record and with `locked` increasing — or, more robustly, have launch.bas write a marker point so the app does not have to infer it.

**Regression test.** Go test that drives a launch to graduation, then asserts the app-side flow() over that window returns the traded volume only, not traded + vd0.

**Verifier.** app/stats.js:25 `if (s.kind !== "pool") return false;` — lpMove is pool-only, so every reserve move on a launch counts as flow (stats.js:45) and as candle volume (stats.js:92). contracts/launch.bas:71-74 replaces the reserves at graduation. I checked the price continuity the finding asserts: vt0 = c(G+G/3)/G = 4c/3, so at full sale ra = 4c/3 − c = c/3 and rd = G/3 + G = 4G/3, giving 4G/c; after, rd = G and ra = Lq = rest/5 = c/4, giving 4G/c. Identical — so the block really is a doji carrying a large volume bar. m.hot saturates: log1p(3333)*8 = 64.9 against the 50 cap at stats.js:72.

---

### `DATA·D3-08` — A market that fails to load stays on screen indefinitely with no staleness marker, and switching nodes invalidates nothing

**P3 · staleness / fail-open · auditor confidence Confirmed · verifier PLAUSIBLE**

**Evidence.** app/feed.js:235 `} catch (e) { console.warn("load", id, e); if (cached) next.set(id, cached); }` — a failed read silently re-adopts the previous record; no timestamp is stored on it and nothing in the UI distinguishes it. feed.js:237 does the same for NFTs. There is no per-market `loadedAt`. Meanwhile app/screens.js:248 ('Save endpoint') and :247 ('Use it this session') call `V.app.connectNode()` and `router.dispatch()` and nothing else: D.assets, D.verified (feed.js:107, the code-hash check that gates whether a market is listed at all), D.heights (feed.js:101) and the per-market localStorage caches all survive. `app.refresh(true)` is called only once, at boot (app.js:281).

**Problem.** Two ways a number can be older or from a different chain than the frame around it says. (1) If a market's contract read keeps failing while the block clock keeps ticking, the token page shows last-known prices under 'block N · 3s ago' (screens.js:17) and the chart caption 'Exact at block resolution'. (2) After a node switch, verification verdicts and install heights are never re-derived, and a market whose PROBE signature happens to match is carried over from the old node's view. connectNode does warn when `i.network` differs from the config (app.js:31), which catches an obviously wrong chain but not a forked or lagging peer on the same network.

**Failure scenario.** Precondition: a node that intermittently errors on DERO.GetSC, or a user changing the endpoint in Settings. Steps: leave the app open / save a new endpoint. Impact: prices, liquidity and 'Verified' badges that were true on another node or at another height are presented as current, with a live block number beside them. The standing project rule is fail closed; this fails open silently.

**Root cause.** Freshness is tracked globally (D.lastRefresh, L.height) but the data is per market, and the endpoint is treated as a transport detail rather than as the identity of the data set.

**Fix.** Stamp each record with the height it was read at; when that lags L.height by more than a few blocks, mark the row and the token page. On any endpoint change (settings save, session confirm, wallet connect/disconnect), clear D.assets, D.verified, D.heights and the veil.m.* cache keys and call app.refresh(true).

**Regression test.** Point the app at node A, load, switch to node B whose factory lists different markets, and assert the list and every 'Verified' badge are re-derived rather than carried over.

**Verifier.** app/feed.js:235 `} catch (e) { console.warn("load", id, e); if (cached) next.set(id, cached); }` and feed.js:237 for NFTs — a failed read silently re-adopts the previous record. I grepped for a per-market freshness stamp: there is no `loadedAt` anywhere, and nothing in markets.js, token.js or home.js distinguishes a re-adopted record. Meanwhile screens.js:17-19 prints "block N · Xs ago" from L.height and token.js:46 says "Exact at block resolution". app/screens.js:247-248 call only `V.app.connectNode()` and `router.dispatch()`; app.refresh(true) appears once, at app.js:281.

---

### `DATA·D3-12` — The Portfolio omits every token whose only market is a pool contract, and prints an unvaluable holding as zero

**P3 · correctness / portfolio · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/app.js:83 `for (const s of D.list()) { if (s.kind !== "launch") continue; ... rows.push({ s, units, value: q.out }); }` — balances are read for launch contracts only. Same line: `const q = A.launchSell(s, units) || { out: Z }` — A.launchSell returns null when `a > s.sold` or `dout > s.real` (amm.js:55-56), and that null becomes a value of 0. app/screens.js:118 accounts only for holdings whose market is missing entirely ('unplaced'), not for these two cases.

**Problem.** Two silent omissions on a screen whose stated job (screens.js:2) is 'what do I hold'. A token traded on a pool.bas market is never balance-checked, so it is absent from the list, the subtotal and the equity curve. And a holder whose position exceeds what the curve can buy back — the creator's allocation is the standard case (home.js:15) — is listed with its unit count and a value of 0.00000, which reads as worthless rather than as unvaluable at this size.

**Failure scenario.** Precondition (a): a user holding an asset that trades only on a pool.bas contract. Steps: open Portfolio. Impact: the holding does not appear anywhere and the subtotal is short by its value, with no note — while the screen does explain the 'unplaced' case, which makes the silence here read as 'you hold nothing'. Precondition (b): a creator holding 5% of supply on a phase-0 launch. Impact: 'Value 0.00000' with no explanation, and a subtotal that excludes it.

**Root cause.** loadHoldings values a position by simulating a full-size sale on a launch contract, which is neither a mark-to-market nor defined for pools. The `|| { out: Z }` turns 'cannot be valued' into 'is worth nothing'.

**Fix.** Read balances for pool assets too and value them with A.poolSell. Where the sell quote is null, carry `value: null` and render 'not valuable at this size' rather than 0, and exclude it from the subtotal with a note saying so. Label the value column as what it is — proceeds of selling the whole position now, net of fees and impact — since that is materially below price × units in a thin market.

**Regression test.** Portfolio test with (a) a pool-only asset held and (b) a launch holding larger than `sold`: assert (a) appears with a value and (b) does not render '0.00000'.

**Verifier.** app/app.js:83 `for (const s of D.list()) { if (s.kind !== "launch") continue; ... const q = A.launchSell(s, units) || { out: Z }; rows.push({ s, units, value: q.out }); }`. app/amm.js:54-56: in phase 0 launchSell returns null when `a > s.sold` or `dout > s.real`, and `|| { out: Z }` turns that into a value of zero. app/screens.js:143 then renders it as `mask(fixed(r.value, g.dec, g.q))` — "0.00000" with no note — and screens.js:148 adds it to the subtotal as zero. app/screens.js:114 counts only assets whose market is missing entirely (`unplaced`), and screens.js:177-179 explains that case in prose while these two get nothing.

---

### `DATA·D3-13` — An unknown quote asset and an unknown base asset both silently fall back to DERO's five decimals

**P3 · correctness / units · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/feed.js:64-65 `s.quoteSym = !s.quote ? "DERO" : v ? clean(v.sym...) : s.quote.slice(0, 6); s.quoteDec = !s.quote ? 5 : v && Number.isInteger(v.dec) ? v.dec : 5;` — when `qt` names an SCID that is not in config.vaults, the symbol becomes six hex characters and the decimals become 5. app/feed.js:166 `s.asset = hex("asset"); s.dec = 5n;` — a pool over an asset that is not a shipped launch and not a configured vault is priced as if the asset had 5 decimals. feed.js:70 `qdec(s)` repeats the same fallback.

**Problem.** The app cannot know a foreign asset's decimals, and instead of saying so it assumes DERO's. Price, liquidity, volume, FDV and every trade-sheet amount on such a market are then wrong by 10^(true − 5) with no indication. token.js:117 does badge the case ('The asset is not a DMZ launch: verify its id yourself'), but that warns about identity, not about the arithmetic — the numbers beside it are presented normally.

**Failure scenario.** Precondition: anyone installs a pool over a DERO asset with, say, 8 decimals — F.pool accepts any SCID (forms.js:132) — or a launch is installed with a `qt` that is a real asset not in the shipped vault list. Steps: open the market. Impact: every figure on the page is off by three orders of magnitude, the trade sheet's parseUnits converts the user's typed amount with the wrong scale, and nothing on screen is wrong-looking.

**Root cause.** The decimals are treated as a display nicety with a sensible default rather than as a required fact without which no amount can be rendered.

**Fix.** When the decimals are unknown, render atomic units with an explicit 'base units' label — wrap.js:111-114 already does exactly this (`dec() === 0 → String(u) + " base units"`) and is the right pattern — and disable the trade sheet's amount conversion rather than guessing.

**Regression test.** Construct a market whose qt is an SCID absent from config.vaults; assert no rendered amount on the token page is scaled by 1e5.

**Verifier.** app/feed.js:64-65 `s.quoteSym = !s.quote ? "DERO" : v ? V.ui.clean(...) : s.quote.slice(0, 6); s.quoteDec = !s.quote ? 5 : v && Number.isInteger(v.dec) ? v.dec : 5;` — an unrecognised qt yields six hex characters as a symbol and DERO's five decimals. app/feed.js:166 `s.asset = hex("asset"); s.dec = 5n;` for every pool; decorate() at feed.js:201 and :207 overrides s.dec only when the base is a listed launch or a configured vault, so an unknown base keeps 5n. feed.js:70 `qdec(s)` repeats the fallback. Every downstream figure (stats.js:57/64, the trade sheet's A.parseUnits at token.js:194) then carries a 10^(true−5) error with nothing on screen saying so.

---

### `DATA·D3-14` — The trade sheet mis-states fee, tax and refund on the buy that completes a bonding curve

**P3 · correctness / quote mirror · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/amm.js:47 `if (out >= rem) { out = rem; const cost = mulDivUp(rem, s.rd, s.ra - rem); if (cost === null || cost > deff) return null; refund = deff - cost; deff = cost; }` — fee and tax were computed at amm.js:41 on the full `d` and are never recomputed. contracts/launch.bas:50-55 does the opposite: it derives `used = muldivup(cost, 10000, 10000 - fee - tax) + 1`, refunds `d - used`, and recomputes fee and tax on `used`. token.js:161-164 renders amm.js's fee, tax and refund as the cost breakdown.

**Problem.** On the graduating buy the sheet prints a fee and a tax charged on money the contract gives back, and a refund smaller than the real one. Replicating both code paths on a curve with fee 50 bps, tax 200 bps, rd 1,000,000, ra 400,000, remainder 101,000 and d = 900,000: the sheet shows fee 4,500 / tax 18,000 / refund 539,707; the contract charges fee 1,733 / tax 6,929 and refunds 553,544. `out` matches, so min_out is not endangered.

**Failure scenario.** Precondition: a buy large enough to complete the curve. Steps: type an overshooting amount; the sheet shows 'This buy completes the curve' (token.js:167). Impact: the 'What this trade costs' block — which the file's own comment (token.js:135) says exists so that every cost stands in front of the button — overstates the fee and tax by ~2.6× and understates the refund. The same wrong `amount` is then written to the local ledger (see D3-10).

**Root cause.** The JS mirror models the overshoot by capping the output and treating the leftover as a refund of post-fee money, where the contract re-derives the gross input and re-charges on that.

**Fix.** amm.js:47 → follow launch.bas exactly: compute `used = mulDivUp(cost, 10000n, 10000n - s.fee - s.tax) + 1n`, clamp to `d`, recompute `fee = bpsUp(used, s.fee)` and `tax = bps(used, s.tax)`, and return `refund = d - used`.

**Regression test.** Differential test of A.launchBuy against reference/launch.go across random states with `out >= rem`, asserting fee, tax, refund and out all agree.

**Verifier.** I ran both code paths. app/amm.js:41 computes fee and tax on the full d and amm.js:47 never recomputes them: `if (out >= rem) { out = rem; const cost = mulDivUp(rem, s.rd, s.ra - rem); ... refund = deff - cost; deff = cost; }`. contracts/launch.bas:50-55 does the opposite — `used = muldivup(cost, 10000, 10000 - fee - tax) + 1`, then re-derives fee and tax on `used`, and refunds d − used at line 59. On the finding's exact inputs (fee 50 bps, tax 200 bps, rd 1e6, ra 4e5, rem 101000, d 900000) the shipped amm.js returns {out: 101000n, fee: 4500n, tax: 18000n, refund: 539707n} and the contract path gives cost 337793, used 346456, fee 1733, tax 6929, refund 553544. Every number in the finding matches to the unit. `out` is identical on both sides, so min_out is genuinely not at risk. app/token.js:161-164 renders amm.js's fee, tax and refund as the cost breakdown, under the comment at token.js:135-138 saying every cost must stand in front of the button.

---

### `DATA·D3-16` — Block-derived counts are labelled as trade counts, and 'all' range charts show only the retained window

**P3 · correctness / labelling · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/home.js:127 `mini("Trades / h", String(blocks1h))` where `blocks1h = act.reduce((a, s) => a + s.m.flow1h.blocks, 0)` — flow().blocks (stats.js:46) counts *blocks in which rd changed*, not trades. home.js:130 and token.js:87 derive 'Buy pressure' / 'Block pressure' from upBlocks24/downBlocks24, the same block counts. app/feed.js:10 `const CHUNK = 250, MAXP = 4000, MAXT = 2000;` with feed.js:189 `if (points.length > MAXP) points = points.slice(-MAXP)`; stats.js:104 `const first = s.points.length ? s.points[0].ts : now()` then rangeFor's `all` branch uses that as the market's birth.

**Problem.** Two places where the label claims more than the data. (1) Several trades in one block net into one count, so 'Trades / h' is a lower bound that is exact only for quiet markets — token.js:90 does carry the honest wording ('Reserve direction per active block'), Home does not. (2) Once a market exceeds 4,000 price points the chart's 'all' chip shows the retained window and calls it all, and rangeFor computes the market's 'life' from the first retained point.

**Failure scenario.** Precondition (1): two or more trades landing in one 18-second block. Impact: Home under-reports activity under a label that reads as a trade count. Precondition (2): a market with more than MAXP recorded blocks. Steps: token page → 'all'. Impact: the earliest history is silently absent and the axis begins at the truncation boundary.

**Root cause.** flow() deliberately measures per-block net movement — which is the right measure and is what makes unpublished trades visible at all — but its output is surfaced under a word that means something else. The MAXP cap is documented in feed.js:10 as 'older history stays on chain' and then not surfaced to the reader.

**Fix.** home.js:127 → 'Active blocks / h', or state the caveat as token.js:90 does. stats.js/chart.js → when `points.length === MAXP`, label the range chip 'max' rather than 'all', or note that history is truncated.

**Regression test.** Assert that a market with MAXP points renders a range control that does not claim 'all'.

**Verifier.** (1) app/home.js:125 `mini("Trades / h", String(blocks1h))` with blocks1h summed from `s.m.flow1h.blocks` at home.js:100. stats.js:37-48 flow() increments `blocks` once per point pair where rd changed, skipping equal-rd pairs entirely, so several trades in one block net to one count and a block whose buys and sells cancel counts as none — a lower bound under a label that reads as a count. token.js:80 carries the honest wording ("Reserve direction per active block"); home.js:126 "Buy pressure" does not. (2) app/feed.js:10 `MAXP = 4000` and feed.js:189 `if (points.length > MAXP) points = points.slice(-MAXP)`; stats.js:104 `const first = s.points.length ? s.points[0].ts : now()` and stats.js:105 uses that as the market's life for the `all` range, so past the cap the chip shows the retained window and the axis starts at the truncation boundary.

---

### `PERF·F8` — Two avoidable RPC round trips per contract on cold start, and a probe concurrency of 4 that is the wrong default

**P3 · performance · auditor confidence High · verifier CONFIRMED**

**Evidence.** app/feed.js:106-110 `verify()` issues `getSC(scid, { code: true })`; app/feed.js:148 `loadContract()` then issues a separate `getSC(scid, { keys: [...NUMKEYS, ...STRKEYS] })`. vendor/derohe/cmd/derod/rpc/rpc_dero_getsc.go serves `p.Code` and `p.KeysString` from the same tree in the same call, and app/rpc.js:136-139 already builds `{scid, variables, code}` plus `keysstring`, so one call can carry both. app/feed.js:100-103 `installHeight()` adds a third round trip (DERO.GetTransaction). app/feed.js:230 and :237 both call `pmap(ids, 4, ...)`; pmap is at feed.js:292.

**Problem.** Cold start per market is 3 sequential round trips before history even starts (code, keys, tx), and the whole registry is walked 4 at a time. Steady state is fine — 143 calls per block at 120 markets, mostly 18-key probes — but the startup path is latency-bound and nothing overlaps.

**Failure scenario.** Precondition: first load, no localStorage cache (which after F2 is every load past ~40 markets). Steps: open the app. Impact: 120 markets x 3 round trips = 360 calls before history, plus up to 24 history chunks each; at concurrency 4 and 60 ms remote-node latency the pre-history phase alone is ~5.4 s of a blank Markets list.

**Root cause.** verify() was written as a standalone helper with its own cache, so it fetches independently of the read that immediately follows it.

**Fix.** In loadContract, when `this.verified` has no entry for the scid, issue ONE `getSC(scid, { code: true, keys: [...NUMKEYS, ...STRKEYS] })` and hand the code to a verify path that takes the code rather than fetching it. Keep the separate verify() for callers that only want the kind (founders.js:12). Raise pmap's width to 8-12 for the probe pass — it is one number and probes are tiny.

**Regression test.** Playwright against the simulator, counting JSON-RPC frames on the WebSocket during a cold load: assert calls-per-market before first paint drops from 3 to 2, and that a contract whose on-chain code does not hash to a template is still rejected.

**Verifier.** app/feed.js:106-109 `verify()` issues `getSC(scid, { code: true })`; feed.js:148 then issues a separate `getSC(scid, { keys: [...NUMKEYS, ...STRKEYS] })`; feed.js:152 calls `installHeight(scid)`, which at feed.js:102 is a third round trip (`V.node.getTx`). The claim that one call could carry both holds: vendor/derohe/cmd/derod/rpc/rpc_dero_getsc.go serves `p.Code` at line 86 and `p.KeysString` at line 158 from the same tree in the same handler, and app/rpc.js:137-138 already builds `{scid, variables, code}` plus `keysstring`. Concurrency 4 confirmed at feed.js:230 and feed.js:237, with pmap at feed.js:292. The latency arithmetic is a projection, not a measurement, but it follows from the constants.

---

### `PERF·F9` — flash() forces a synchronous layout per changed number, so a Markets redraw can thrash layout ~120 times in one tick

**P3 · performance · auditor confidence Medium · verifier *(unverified — id collision)***

**Evidence.** app/live.js:32 — `flash(el, dir) { el.classList.remove(...); void el.offsetWidth; el.classList.add(...); setTimeout(...) }`. `void el.offsetWidth` is a deliberate forced reflow to restart the CSS animation. It is reached from `live.num()` (live.js:30) whenever a value changed, and `num()` is called once per row from app/markets.js:180 and once per tape item from live.js:49.

**Problem.** The reflow is interleaved with the DOM writes of the surrounding draw loop, so each one invalidates and re-resolves layout for the whole document. On a 120-row Markets table after an active block, that is up to 120 read-after-write layout flushes inside a single synchronous pass, plus 120 concurrent requestAnimationFrame number animations (live.js:28) each writing textContent for 500 ms.

**Failure scenario.** Precondition: Markets tab open, many markets moved in one block. Steps: watch a busy block land. Impact: layout thrashing proportional to row count on top of the draw itself. I could not measure this without a browser, so the magnitude is unverified — but the read-in-a-write-loop pattern is unambiguous in the source.

**Root cause.** The animation-restart idiom is correct in isolation and wrong when called N times inside one layout pass.

**Fix.** Batch the restart. Collect elements needing a flash during the draw, then in a single `requestAnimationFrame` remove the classes on all of them, read `document.body.offsetWidth` once, and add the classes on all of them. One forced layout instead of N.

**Regression test.** Playwright with a performance trace over one block on a 100-row Markets table; assert the number of forced layout ("Layout" under a script task) events stays constant rather than scaling with row count. Marking this NOT VERIFIED for magnitude until that trace exists.

**Verifier.** app/wrap.js:53-56 reads `uc_<addr>` then only `ul_<addr>_<n-1>`, and builds `dep` from that single index. app/wrap.js:279 offers "Register another index" with the copy at :275 encouraging it. Once a second index exists, `dep.addr` is empty until attestation, and wrap.js:196 collapses that to `dep = null`, so step 3's `full("Your deposit address on …")` disappears and the previous address is unreachable from the app. app/wrap.js:83 SCAN=60 with no paging and no truncation notice is confirmed verbatim.

---

### `PROD·P7-07` — "Creator allocation: none" is only the install-time grant; the curve has no per-address cap, no cooldown and no launch delay, and DERO's encrypted balances make the resulting concentration unobservable to anyone

**P3 · rug disclosure · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** contracts/launch.bas has no height or address guard anywhere on the buy path — `grep -n BLOCK_HEIGHT` returns only the graduation stamp (`74 STORE("gh", ...)`), the buyback's 20-block cooldown (`29 IF BLOCK_HEIGHT() < LOAD("bbh") + 20`), and the tick/post timestamps. `buyto` enforces `min_out` and the remaining curve only. app/home.js `H.allocation(s) = s.supply - s.cs - s.Lq`, which is exactly the install-time `alloc = bps(supply, cbps)` and nothing else; app/token.js Info renders `kv("Creator allocation", H.allocation(s) > Z ? ... : "none")`. app/home.js `H.risks` raises the alloc line only from that same install-time figure. app/token.js's Holders tab is captioned honestly — "Balances are private on DERO. Listed: accounts that published trades" — which confirms that no holder-concentration figure is obtainable.

**Problem.** The one number the product offers as a rug check measures the only form of creator concentration that is capped at 5%, and reports "none" for the unbounded form. A creator who sets cbps = 0 and then buys the first 60% of their own curve from a fresh address shows "Creator allocation: none", no risk flag, and a Traders count of 1. Every mitigation the rest of the industry uses here — dev-holding percentages, bundle detection, sniper analytics — is structurally impossible on DERO because balances are encrypted, which the app itself states. So the launchpad has no anti-sniping mechanism and no way to observe sniping after the fact.

**Failure scenario.** Precondition: any launch. Steps: creator installs with cbps=0 (so the page reads 'none'), and in the next block buys the cheap early section of their own curve from an unrelated address; real buyers fill the rest and push the price up 16x to graduation; the creator sells into the AMM. Impact: a textbook soft rug with a clean-looking token page. app/forms.js:95's "Nothing to run, nothing to rug" is the copy a creator reads while choosing these parameters.

**Root cause.** The rug checks were built from what the contract stores. Because DERO stores no readable balances, the design has no way to see the dominant risk, and the UI presents the observable half as though it were the whole.

**Fix.** Two changes, both cheap. In the contract: add a per-address cap on the early curve (e.g. no single address may take more than X bps of `cs` within the first N blocks after install) — the state needed is one `b_<addr>` counter, and it expires by itself so nothing needs a human to lift it. In the UI: relabel the row "Creator allocation at launch" and add a permanent caption saying balances are private on DERO, so this number cannot show what the creator bought on the curve. Remove "nothing to rug" from app/forms.js:95.

**Regression test.** A DVM test asserting a single address cannot take more than the cap from a fresh curve inside the guard window; a UI assertion that a launch with cbps=0 renders the caption rather than a bare 'none'.

**Verifier.** I grepped BLOCK_HEIGHT across contracts/launch.bas: five hits, all accounted for — the graduation stamp (file:144), the buyback's 20-block cooldown (file:290, 298), and the tick/post timestamps (file:421, 436). buyto enforces min_out and the remaining curve and nothing else; there is no per-address ledger, cooldown or launch delay anywhere on the buy path. app/home.js:14 H.allocation is `s.supply - s.cs - s.Lq`, which is exactly the install-time `alloc = bps(supply, cbps)` (launch.bas file:24-27), and token.js:106 renders it as "none" when zero. The Holders tab caption honestly states balances are private, which is the same fact that makes concentration unobservable.

---

### `PROD·P7-10` — The bond can be worth arbitrarily less than what a quorum could steal, and even when slashed it pays the treasury rather than the victims — the wrap screen's trust panel says 'slashable' and stops there

**P3 · vault economics / disclosure · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** contracts/vault.bas enforces exactly one relation, in three places (`Seal` line 40, `Mint` line 42, `SetCap` line 23): `IF LOAD("cap") / LOAD("capratio") > LOAD("bondtot") THEN GOTO 900`. `cap` is in the foreign asset's base units, `bondtot` is in DERO atomic units, and `capratio` is a plain integer chosen at install (tools/simvault/main.go:51 `capratio = flag.Uint64("capratio", 3, "foreign base units of cap each DERO atomic unit of bond supports")`) with no price input and no re-evaluation. `Slash` ends `49 SEND_DERO_TO_ADDRESS(LOAD("treasury"), b)` — the bond goes to the treasury; there is no compensation path to burners anywhere in the file. `Slash` also requires `24 IF na < LOAD("k") + 1 THEN GOTO 900`. app/wrap.js:330-332 renders `"Signers have bonded " + A.fmtDero(w.bondtot, 2) + " DERO, slashable by a quorum of the others."` alongside `w.sup` and `w.cap` in foreign units, and says nothing about where a slashed bond goes. deploy/README.md:202 and :249 state the bound as an operational checkbox with no guidance on choosing capratio in value terms.

**Problem.** The bond-to-custody relation is fixed in nominal units at install and drifts with the DERO/XMR exchange rate forever, with no oracle and nobody left to call SetCap. Worse, k colluding signers in an n = 2k federation can use their quorum to Slash the honest signers one at a time down to `na == k` — at which point `na < k + 1` and no further slash is possible — sending the honest bonds to the treasury and leaving themselves unslashable before they touch the foreign coins. Their own bonds are then simply stranded (Unbond is blocked after Seal), so the deterrent is a burn, not a payout. RISKS.md R-14 states the last part honestly ("the bonds are not a payout to holders"); the panel a depositor actually reads before sending real Monero does not.

**Failure scenario.** Precondition: a sealed 2k-of-n vault holding meaningful reserve. Steps: k signers collude, call Slash on each honest signer in turn until na == k, then move the foreign reserve. Impact: users hold wrapped units backed by nothing; the slashed bonds sit with the treasury; the colluders' own bonds are stranded rather than paid to anyone harmed. Separately and more likely: DERO falls or XMR rises and the cap, fixed at install, quietly comes to exceed the bond's value with no mechanism that notices.

**Root cause.** capratio was specified as a unit ratio because that is what the DVM can check — it has no prices. That is a reasonable engineering choice, but the trust surface presents the resulting number as if it were an economic guarantee, and the slash proceeds were routed to the treasury by analogy with fees rather than by asking who the loss falls on.

**Fix.** Two disclosure changes and one design question. Disclosure: in app/wrap.js's trust panel, state plainly that a slashed bond is paid to the protocol treasury and is not compensation for depositors, and state the bond and the reserve as what they are — two amounts in two assets with no rate between them, exactly as the rest of the app refuses to sum quotes. Also state that `Slash` cannot reduce the active set below k, so a quorum can make itself unslashable. Design: route slashed bonds to a claim fund the burners of unpaid withdrawals can draw on, rather than to the treasury; and add capratio guidance to deploy/README.md that names the DERO/foreign price assumption it encodes and the price move at which the cap must be lowered.

**Regression test.** A DVM test that drives a 2-of-4 vault: two signers slash the other two, assert the third slash reverts and that `na == k`; assert the treasury received the bonds and no burner did. A doc test asserting the wrap panel's slash line mentions the treasury.

**Verifier.** The single relation is enforced in exactly the three places named — Seal (vault.bas file:243), Mint (file:318) and SetCap (file:497) — always as `cap / capratio > bondtot`, mixing foreign base units against DERO atomic units with capratio a plain install-time integer (tools/simvault/main.go:51). Slash ends at file:532 `SEND_DERO_TO_ADDRESS(LOAD("treasury"), b)` and there is no compensation path to burners anywhere in the file. I walked the collusion path: Slash file:518 refuses when `na < k + 1`, so from na = 2k a colluding k can slash each honest signer in turn, with the final slash legal at na = k+1, ending at na = k where no further slash is possible — and their own bonds are then stranded, because Unbond (file:214) refuses any active signer after Seal. wrap.js:330-332 renders "slashable by a quorum of the others" and says nothing about where a slashed bond goes. docs/TODO.md lines 125-129 independently records that capratio's default of 3 is only meaningful for a five-decimal asset.

---

### `PROD·P7-11` — Founders Pass rewards are pro-rata to whoever is registered at the instant of a deposit, with no lock and no time weighting, so a just-in-time registrant captures a share a long-term holder earned

**P3 · fee-share economics · auditor confidence High · verifier CONFIRMED**

**Evidence.** contracts/founders.bas `Deposit()` calls `distribute(d)` immediately, which credits `acc` across `hst` — the units registered at that block. `Register()` settles then adds units and calls `distribute(0)` again. `Withdraw(u)` settles and returns the passes with no minimum holding period and no cooldown anywhere in the file. tools/treasurybot/main.go sweeps on a predictable loop (`interval` default 60 s) and its deposits are ordinary public transactions.

**Problem.** The pass is sold as an income instrument (tools/simdeploy/main.go:44: "Registered holders receive the promised share of every fee the exchange earns"), but the accrual has no time dimension. A holder who registers a minute before a sweep and withdraws a minute after receives the same per-unit share as one registered for a month, and the marketplace built into the same contract makes acquiring units for that window easy.

**Failure scenario.** Precondition: passes obtainable on the built-in marketplace or already held. Steps: watch `fd` accumulate across markets (public), or simply watch for the bot's claim transactions; call Register just before the Deposit lands; call Claim and Withdraw after. Impact: a mechanical extraction from passive registered holders, repeatable every sweep, at the cost of two transactions.

**Root cause.** The accumulator pattern (acc x 2^32) is the right O(1) shape for the payout maths and carries no notion of duration; nothing was added on top to price the duration.

**Fix.** Add a minimum registration age before `settle` will pay — record `hr_<addr> = BLOCK_HEIGHT()` in Register and require `BLOCK_HEIGHT() >= hr_<addr> + N` in settle, forfeiting to `und` (which redistributes) otherwise. N must be a constant, not a parameter anyone can change, and it must expire by itself so nothing needs a human. Failing that, say on app/nfts.js's instrument that rewards accrue only while registered at the moment of a deposit, so a buyer prices the instrument correctly.

**Regression test.** A DVM test: holder A registered for 1000 blocks, holder B registers one block before a Deposit; assert B's settled amount is zero (or reduced) under the new rule and that A's is correspondingly larger.

**Verifier.** contracts/founders.bas Deposit (file:137-146) calls distribute(d) in the same call, crediting acc across whatever hst is at that block. Register (file:77-102) settles, adds units, and calls distribute(0) again. Withdraw (file:105-124) settles and returns the passes with no minimum holding period and no cooldown — I read the whole function and there is no height check anywhere in the file. tools/treasurybot/main.go sweeps on a predictable `interval` (default 60 s) with ordinary public transactions, and simdeploy's own text sells the pass as an income instrument.

---

### `PROTO·BC-7` — nft.bas Unlock does not delete mt_<addr>, so an edition is one-per-address-for-ever, not one-per-lock as documented

**P3 · correctness / doc mismatch · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\contracts\nft.bas header line 100: "Lock >= min_hold of the token for >= hold_blocks blocks, then mint one edition (one per lock)". Mint line 162 (DVM line 24): `IF LOAD("lm_" + a) != 0 || EXISTS("mt_" + a) THEN GOTO 900`, and line 175 sets `STORE("mt_" + a, 1)`. Unlock lines 190-192 delete lk_, lh_ and lm_ but not mt_. So after unlocking and re-locking, lm_ is gone but mt_ persists and Mint is refused for ever.

**Problem.** The contract enforces a stricter rule than its header and than the app will describe: a holder who locks, mints, unlocks and later re-locks can never mint again. Whichever behaviour is intended, one of the two is wrong, and the contract is immutable so it cannot be corrected after deployment.

**Failure scenario.** Precondition: a deployed nft.bas collection with maxsupply > number of distinct holders.
1. Alice locks minhold tokens, waits holdblocks, mints edition 0, unlocks.
2. Months later Alice locks again for the next drop from the same collection and waits.
3. Mint refuses at line 162 on `EXISTS("mt_" + a)`. Alice's tokens are locked for nothing (she can Unlock them, so no funds are lost) and the collection can never reach maxsupply if it has fewer distinct holders than editions.

**Root cause.** mt_ was added as a permanent one-per-address guard on top of lm_, which is the per-lock guard, and the header was not updated. Two guards for one rule, with different lifetimes and no comment saying which is authoritative.

**Fix.** Decide the rule and keep one guard. If one-per-lock is intended, delete `mt_` entirely (lm_ already does that job and Unlock already clears it). If one-per-address-for-ever is intended, delete `lm_` and correct the header line 100 to say so.

**Regression test.** tests/nft_test.go: Lock, advance holdblocks, Mint, Unlock, Lock again, advance, Mint -- assert the second Mint succeeds (one-per-lock) or is refused with the header corrected to match.

**Verifier.** contracts/nft.bas Mint guards on `24 IF LOAD("lm_" + a) != 0 || EXISTS("mt_" + a) THEN GOTO 900` and sets `37 STORE("mt_" + a, 1)`. Unlock deletes lk_, lh_ and lm_ but not mt_. Lock re-creates lk_/lh_/lm_ but nothing clears mt_, and there is no other writer or deleter of mt_ in the file. So the enforced rule is one edition per address for ever, while the header states 'one per lock'. The contract is immutable, so whichever behaviour was intended, it cannot be corrected after deployment, and a collection with fewer distinct holders than editions can never reach maxsupply. No funds are lost: Unlock (lines 86-98) still returns the tokens.

---

### `REQ·A8-10` — The only build identity a user is shown is a hand-edited constant, and it is already wrong

**P3 · ux / security-requirement-missing · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/index.html:9 `<meta name="veil-commit" content="14">`; app/screens.js:238 reads it (`document.querySelector('meta[name="veil-commit"]')`) and :265 renders `monoKv("Build", "TELA commit " + commit)` inside the Security & privacy panel. The number is not derived from the INDEX: tools/telainstall/main.go:272 prints the real on-chain `dep.Commit`, and the recorded installs are deploy/tela-deployment.json commit 14 (simulator, 2026-09-05T21:08Z) and deploy/tela-testnet.json commit 0 (testnet, 2026-09-06). RISKS.md R-3 makes the on-chain TELA app the trust anchor ('its SCID is the domain'); R-9 recommends 'a documented process for INDEX updates (who signs, what changed, which commit TXID)' — no such document exists in the repo.

**Problem.** The app's trust anchor is an updatable on-chain INDEX, and the only thing the app tells the user about which version they are running is a constant typed into the file being verified — which proves nothing, is not per-network (it reads 14 on a testnet install whose commit is 0), and is stale by three days and fifteen commits. There is also no published process by which a user could match a commit number to a source revision.

**Failure scenario.** Precondition: the app is updated on chain via `telainstall -index`. Step 1: the user opens Settings and reads 'Build: TELA commit 14'. Step 2: the real INDEX commit counter has advanced; the meta tag has not, because nothing updates it. Step 3: the user has no way to notice a substituted or rolled-back app, which is precisely the risk R-3 names. Impact: the one user-facing integrity signal is decorative.

**Root cause.** The commit meta was added as a build stamp before the INDEX commit chain existed as a verifiable source, and no step in genconfig or telainstall writes it.

**Fix.** Have `tools/genconfig -dist` write the meta tag (or a field in config.js) from the target network's recorded INDEX commit, and have the app read the INDEX's own `commit` from the node at runtime and display both, flagging a mismatch. Add the INDEX-update process document R-9 asks for: who signs, what changed, which commit txid, published alongside each update.

**Regression test.** A genconfig test asserting the emitted bundle's build stamp equals the network record's commit; a UI-test assertion that the Settings build line is non-empty and matches the config value.

**Verifier.** app/index.html:9 is `<meta name="veil-commit" content="14">`; app/screens.js:238 reads it and :265 renders it as 'Build: TELA commit N'. deploy/tela-deployment.json records commit 14 for the simulator; deploy/tela-testnet.json records `"commit": 0` with `"updates": null` for testnet — so the same constant reads 14 on an install whose real counter is 0. Nothing in genconfig or telainstall writes the tag (tools/telainstall/main.go prints the on-chain dep.Commit but does not write back into the bundle). Confirmed.

---

### `REQ·A8-12` — chat.bas ships as a top-level tab with a deployed contract and has no entry in the contract reference, the README or the architecture

**P3 · documentation-missing · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/index.html:37 renders a `chat` tab; app/index.html:68 loads chat.js (9 805 B); app/config.js:17 carries a deployed chat SCID; deploy/networks.json:62 records it on the simulator; tests/chat_test.go exists. But `grep -in chat contracts/README.md` returns nothing — while swap.bas (line 180) and board.bas (line 193), neither of which is deployed or reachable, both have full sections. README.md §1 'What it does' has no Chat row and §2's contract list omits it; docs/ARCHITECTURE.md's contract box omits it. There is also no chat template hash: app/config.js `templates` covers founders/launch/nft/pool only, so the app trusts the chat SCID from config and validates only the `kind` and `v` state keys (app/chat.js:35-38) — the same gap docs/TODO.md §0c documents for the vault ('a wrong config entry would go unnoticed'), which applies equally to chat, social and factory.

**Problem.** A permanent, unmoderated, on-chain user-generated-content surface — the project's own docs call it 'permanent and unmoderated by design' — is the least documented thing in the repository. House rule README §12 requires every change to ship with its doc line in contracts/README.md; this one did not. And the config-trust gap that TODO §0c writes up carefully for the vault is silently the same for three other contracts.

**Failure scenario.** Precondition: an auditor or a new agent works from contracts/README.md, which is the stated reference for 'every entrypoint, key, invariant, size and gas'. Step 1: chat.bas is absent, so its entrypoints, its fee, its length caps and its storage growth are unreviewed. Step 2: docs/TODO.md §3.2 records that chat 'has never been attacked' and lists the attacks to try — hostile room names, separator injection, unicode in the nick column, 100 000 messages. Step 3: none of that is discoverable from the reference. Impact: the surface most likely to carry an injection or a rendering bug is the one with no written specification.

**Root cause.** chat.bas was added late and the doc line was skipped; the config-trust statement in TODO §0c was scoped to the vault because that is what was being fixed at the time.

**Fix.** Add a chat.bas section to contracts/README.md (entrypoints, keys, fee, maxlen, size, install gas, invariants), a Chat row to README §1 and the contract list in README §2 and docs/ARCHITECTURE.md, and widen TODO §0c's trust-root paragraph to name all four config-supplied SCIDs (factory, social, chat, vault) as unverified against any template.

**Regression test.** A docs lint asserting every file in contracts/*.bas has a `## <name>.bas` heading in contracts/README.md.

**Verifier.** `grep -in chat contracts/README.md` returns nothing, while swap.bas has a section at line 180 and board.bas at line 193 — neither of which is deployed. app/index.html:37 renders a chat tab and :68 loads chat.js; app/config.js line 17 carries a deployed chat SCID; deploy/networks.json simulator records it; tests/chat_test.go exists. app/config.js `templates` covers founders/launch/nft/pool only, so there is no chat template hash and app/chat.js validates only the kind/v state keys — the same config-trust gap docs/TODO.md 0c writes up for the vault, unaddressed for chat, social and factory. All verified.

---

### `REQ·A8-13` — No guard that app/templates matches contracts/, and the UI test still covers three of the five required widths

**P3 · testing-requirement-missing · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** Template drift: docs/PLATFORM_BACKLOG.md B0.1 records that stale templates once caused every launch to install code without the private-refund and init fixes; docs/TODO.md §5.3 asks for 'a test that fails when the two directories differ'. The only check that exists is tools/genconfig/main.go:154-158, which compares app/templates to networks.json — not to contracts/. I verified by hand that they currently agree (md5 of contracts/<x>.bas with whole-line comments and blanks stripped equals md5 of app/templates/<x>.bas, for launch, pool, nft and founders), so the guard is absent, not the property. Widths: docs/DESIGN_DIRECTIVE.md §8 requires the UI test green at 390, 430, 768, 1024 and 1280; tools/uitest/ui.mjs opens viewports at 430 (:26,:38), 1280 (:197) and 1024 (:210) only. docs/TODO.md §4.4 records it; docs/PLATFORM_BACKLOG.md B2.10 asks for the same five.

**Problem.** Two named regression gates do not exist. The template gate protects against a failure mode that has already happened once and whose consequence is users installing contracts that differ from the audited source; the width gate is an unticked box in the design programme's own definition of done, so the phone width the product is designed for (390) and the tablet width are unasserted.

**Failure scenario.** Template drift: precondition — a contract is edited and simdeploy is not re-run, or is run against one network. Step 1: nothing compares contracts/ to app/templates/. Step 2: genconfig still passes, because templates and networks.json agree with each other. Step 3: launches from the app install the older code, and the app verifies them happily against the older hash. That is B0.1 verbatim. Width: a layout regression at 390 or 768 ships unnoticed because no pass renders those widths.

**Root cause.** The 2026-09-08 Canon change means templates are the comment-stripped form of contracts/, so a naive byte comparison no longer works and the guard was left unwritten; the width extension was scheduled into the design programme and the programme's definition of done was never re-checked.

**Fix.** Add a Go test in tests/ asserting `reference.Canon(contracts/<x>.bas) == app/templates/<x>.bas` for all four templates (reference.Canon is already there, reference/canon.go:22). Add 390 and 768 viewports to tools/uitest/ui.mjs with the same overflow and console-error assertions used at 1024.

**Regression test.** The two additions above are themselves the tests; verify the template test fails when a comment-free line is changed in contracts/ without regenerating templates.

**Verifier.** Template guard: the only comparison in tools/genconfig/main.go is app/templates against networks.json (inside bundle(), and only in -dist mode); nothing compares app/templates to contracts/. I independently verified the property currently holds — comment-and-blank-stripped sha256 of contracts/{pool,launch,nft,founders}.bas equals sha256 of the shipped templates (33a126a8, 09cb53d2, f8b85449, 49009d2c) — so the guard is absent, not the property, exactly as the finding says. Widths: `grep -n viewport tools/uitest/ui.mjs` shows 430 (lines 26, 38), 1280 (197) and 1024 (210) only; 390 and 768 are never rendered, against docs/DESIGN_DIRECTIVE.md section 8's five required widths.

---

### `REQ·A8-14` — Five documents that the project's own plans and gates require do not exist

**P3 · documentation-missing · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** LICENSE: absent (`ls LICENSE*` — none); README.md status block says 'No licence has been chosen yet: all rights reserved'; docs/HANDOVER.md §6.2 makes it a wrap-up item. SECURITY.md: absent; HANDOVER §6.2 requires it with a disclosure address the operator owns; README §12 instead asks for findings as public issues, which is the wrong channel for the wrapper. docs/RANKING.md: absent; it is the stated done-criterion of PLATFORM_BACKLOG B1.1, the top P1 item, and README §10 lists dust-trade ranking as a known live weakness. research/wrapper/MECHANISM_REVIEW.md: absent; docs/TODO.md §2.1 names it as the required output of the federation-versus-atomic-swaps verdict — 'the standing question and it has never been answered properly'. deploy/federation.md: absent, yet it is named in deploy/README.md's mainnet go/no-go table as the evidence artefact for the federation gate.

**Problem.** Two of these are release blockers the project set itself (licence, disclosure channel); one is the deliverable of the single unanswered design question at the heart of the product; one is the evidence artefact for a mainnet gate that therefore cannot be checked. A gate whose evidence file does not exist cannot fail, which makes it worse than no gate.

**Failure scenario.** Precondition: the operator moves toward the mainnet go/no-go. Step 1: the federation gate row is read; deploy/federation.md does not exist, so the reviewer either skips it or invents its contents. Step 2: a security finding arrives with no private channel, so it lands in a public issue on a repository with no licence. Impact: the deployment decision is made against gates that have no artefacts, and vulnerability disclosure has no non-public path.

**Root cause.** Wrap-up items were enumerated in HANDOVER §6 and never executed; the mainnet gate table was written aspirationally, naming artefacts as though they existed.

**Fix.** Create the five: choose and add LICENSE; add SECURITY.md with a disclosure address and the wrapper-specific instruction already in README §12; write docs/RANKING.md when B1.1 lands (or record explicitly that ranking v2 is deferred and that dust-trade gaming is accepted); answer TODO §2.1 into research/wrapper/MECHANISM_REVIEW.md with what would change the answer; create deploy/federation.md as a template the gate fills in (signer set, bonds, capratio assumption, viewing keys, backend versions).

**Regression test.** A docs lint asserting every path named in deploy/README.md's go/no-go table exists in the tree.

**Verifier.** Checked each path directly: `ls LICENSE* SECURITY* docs/RANKING.md research/wrapper/MECHANISM_REVIEW.md` returns 'No such file or directory' for all four. `ls deploy/` shows README.md, binaries.lock, networks.json, patches, tela-deployment.json, tela-testnet-reuse.txt, tela-testnet.json, testnet-deploy.sh, testnet-redeploy-when-synced.sh, testnet-tela.sh — no federation.md, and deploy/README.md:202 names `deploy/federation.md` as the evidence artefact for the federation go/no-go row. The point that a gate whose evidence file does not exist cannot fail is fair.

---

### `REQ·A8-15` — The published file and DOC counts — which the mainnet funding estimate rests on — are two files and three DOCs out of date across four documents

**P3 · documentation-drift · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** Measured with `go run ./genconfig -dist -network simulator`: 31 files in the bundle; `ls app/*.js | wc -l` = 22, `ls app/*.css | wc -l` = 4. Documents say otherwise: README.md §2 '20 plain-JS files, 4 CSS files, one HTML file… Served from a TELA INDEX (29 DOCs on chain)'; docs/ARCHITECTURE.md 'index.html · styles/ui/parts/desk.css · 20 JS modules… ← 29 TELA-DOC-1'; docs/DESIGN_DIRECTIVE.md §1.1 'loads ~20 scripts' and quotes per-file DOC sizes (styles.css 4 160, app.js 12 440, token.js 8 860) that are now 4 820, 13 168 and 13 076; deploy/README.md (TELA) 'The app is 29 files (25 in app/ — 20 JS, 4 CSS, index.html)'; docs/PLATFORM_BACKLOG.md's measured baseline says 19 JS files and 28 DOCs. deploy/README.md already flags the derived install cost as STALE and tells the operator to fund the owner wallet with 'at least 4 DERO and re-measure'. Separately docs/TODO.md §5.4 calls wrap.js 'the largest file in the bundle' at 13 156 DOC bytes — app.js is now larger at 13 168, with token.js 13 076 and screens.js 12 132, i.e. four files within ~5.3 KB of the 18 432 cap.

**Problem.** Every count that feeds the on-chain install cost — the number the mainnet wallet is funded against — is stale in four places, and the cap-headroom warning names the wrong file. None of it is dangerous on its own; together it means the one number an operator must get right before spending real DERO has no current source.

**Failure scenario.** Precondition: mainnet install. Step 1: the operator reads '29 DOCs, ≈1.64 DERO' and funds accordingly. Step 2: the bundle is 31 DOCs and materially larger (wrap.js and chat.js postdate the measurement). Step 3: the install runs out of funds partway; telainstall's `-reuse` recovers it, so the cost is time rather than loss. Impact: an avoidable failed deployment step, plus a size budget being watched on the wrong file.

**Root cause.** The counts were written once and the app has grown by chat.js, wrap.js and the founders/notice splits since; nothing derives them from the tree.

**Fix.** Regenerate the counts and per-file DOC sizes from `genconfig -dist` output into README §2, docs/ARCHITECTURE.md, deploy/README.md (TELA) and docs/PLATFORM_BACKLOG.md's baseline; re-measure the simulator install cost and replace the STALE figure; correct TODO §5.4 to name app.js and list all four files near the cap.

**Regression test.** Have genconfig write the file count, total DOC bytes and the top five file sizes into dist/<net>/manifest.json (it already writes the manifest) and cite that file from the docs rather than restating numbers.

**Verifier.** Measured: `ls app/*.js | wc -l` = 22, `ls app/*.css | wc -l` = 4. Bundle contents (from dist/simulator, which is the successful build): 27 files in the app root plus 4 templates = 31 DOCs. Documents disagree: README.md:74-75 says '20 plain-JS files, 4 CSS files' and '29 DOCs on chain'; deploy/README.md:288 says '29 files (25 in app/ — 20 JS, 4 CSS, index.html)'; deploy/README.md:172 and :176 both say 29 DOCs; docs/ARCHITECTURE.md:6 says '20 JS modules'. deploy/README.md already flags the derived install cost as STALE and tells the operator to re-measure. The counts are two JS files and two DOCs out of date across four documents, as claimed.

---

### `SEC·RT-10` — 'Verified' is a claim about what one untrusted node reported, and the intro and Settings copy state it as absolute

**P3 · trust model / overstated claim · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\app\feed.js verify(): `const r = await V.node.getSC(scid, { code: true }); const kind = (await this.templateHashes()).map[await V.sha256Hex(r.code)] || null;` — the code being hashed is whatever the node returns for that scid. registry_() likewise takes the list of scids from the node. There is no second source and no cross-check; V.node.call falls back to the wallet's DERO.* proxy only when the socket errors, never to compare answers. C:\Users\Wilma\Desktop\fomo\app\app.js intro() step 3: 'Every listing is verified. The app checks each contract's code against the shipped templates before it shows it.' C:\Users\Wilma\Desktop\fomo\app\screens.js Settings: can(true, 'Every listed contract's code is checked against the shipped templates before it is shown or traded.'). Mitigations that do exist: rpc.js endpointURL restricts to loopback or wss://, and app.js requires a per-session confirmation for a stored remote endpoint (app.endpointConfirmed / confirmEndpoint).

**Problem.** Both the code and the registry that names the code come from the same party. A node that lies about both produces a market that passes every check the app performs and renders with the green shield; the user's wallet prompt shows only an SCID and a burn amount, not what the contract does. The copy does not qualify the claim, so a user who confirms a remote node in Settings has no way to know that the badge now means 'this node says so'.

**Failure scenario.** Precondition: the user has confirmed a wss:// node for this session (Settings offers this, and app.saveSettings auto-confirms an endpoint typed into the field). 1) The node returns a factory registry containing an scid the attacker controls. 2) For that scid it returns genuine pool.bas source, so the sha256 matches config.templates.pool, plus fee/pfee equal to the config values and a treasury value it also controls (or an unparsable factory tstr, see RT-05). 3) The market renders as Verified with an attractive fabricated chart (the node also supplies the p_ history). 4) The user buys; app.tx builds a burn to that scid and the wallet signs it. Impact: the full trade amount is burned into a contract the app vouched for. min_out does not help — the contract is the attacker's, and the app's outcome probe reads the same node.

**Root cause.** The verification is genuinely useful against a hostile CONTRACT but was described as if it were useful against a hostile NODE, which is a different adversary and the one the endpoint-confirmation gate exists for.

**Fix.** Change both strings to say what is true: the app checks each listed contract's code against the shipped templates as reported by the node you are connected to, and a node you do not control can lie about all of it. Show the current endpoint (and whether it is loopback) next to the badge or in the trade sheet whenever the node is remote.

**Regression test.** A test with a stub node that returns template source for an arbitrary scid and asserts the app either refuses it or renders it with a node-qualified badge.

**Verifier.** The mechanism is exactly as described. app/feed.js verify() line 108 hashes `r.code` from `V.node.getSC(scid, { code: true })`, and registry_() lines 79-85 take the scid list from the same node. app/rpc.js node.call (line 128) tries the WebSocket and falls back to the wallet's DERO.* proxy only when the socket throws — never to compare two answers. There is no second source anywhere in the app. So a node that lies about both the registry and the code produces a market that passes every check the app performs, and app.tx's outcome probe (app.js lines 145-160) reads the same node.

---

### `SEC·RT-11` — Three leftovers from the quote-decimals fix: a cross-quote volume sum on the profile screen, a DERO-scaled ClaimTax button, and a DERO-worded tax risk line

**P3 · correctness / money display · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\app\screens.js lines 195 and 209: `const vol = trades.reduce((a, t) => a + (t.side === 1 ? t.in : t.out), 0n);` over D.tradesOf(addr), which spans every market, then `stat("Published vol", A.fmtDero(vol, 1))` — 12-decimal wXMR amounts added to 5-decimal DERO amounts and printed at 5 decimals. C:\Users\Wilma\Desktop\fomo\app\token.js line 114: `button("Claim creator tax (" + A.fmtDero(s.tacc, 2) + ")", ...)` — tacc is in the market's quote units; the Buyback button two lines above correctly uses `A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s)`. C:\Users\Wilma\Desktop\fomo\app\home.js H.risks(): 'High tax: X% of every trade's DERO leg' — hard-coded, and the thin-market test `s.m.liq < 20` is applied to quote units regardless of quote. C:\Users\Wilma\Desktop\fomo\app\app.js line 228: `b.avg = ... (Number(b.cost) / 1e5) / ...` in app.pnl.

**Problem.** The portfolio screen goes out of its way to forbid cross-quote sums ('a sum of wrapped XMR and DERO: a number that is not a quantity of anything'), and the profile screen then does exactly that. The ClaimTax button shows a creator a number 10^7 too small on a wXMR launch, on the control that moves their money. The tax risk line names the wrong currency and the thin-market threshold means something different per quote.

**Failure scenario.** Precondition: an address has traded on both a DERO market and a wXMR market. 1) Any user opens that profile. 2) 'Published vol' shows the sum of 12-decimal wXMR base units and 5-decimal DERO base units divided by 1e5 — a number that corresponds to no quantity, presented as a statistic about the trader. Separately, a creator of a wXMR-quoted launch with tmode 1 sees 'Claim creator tax (0.00)' when 1,000,000 base units of wXMR are waiting, and may conclude there is nothing to claim.

**Root cause.** The quote-decimals change was applied where the tests and the portfolio screen looked, and these four call sites were not in that path. A.fmtDero's fixed 5 makes each one silent.

**Fix.** screens.js: group the profile's published volume by quote the way the portfolio groups holdings, or drop the stat. token.js:114: use `A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s)` to match the Buyback button. home.js: replace 'DERO leg' with D.qsym(s), and scale the thin-market threshold by the quote (or express it as a fraction of the market's own reserves). app.js:228: divide by 10 ** Number(D.qdec(s)) or delete b.avg, since screens.js recomputes it.

**Regression test.** A test that builds one DERO market and one wXMR market for the same address and asserts the profile screen never prints a single combined volume figure.

**Verifier.** Two of the four sub-items are exactly right. app/screens.js line 195 `const vol = trades.reduce((a, t) => a + (t.side === 1 ? t.in : t.out), 0n);` over D.tradesOf(addr), which feed.js line 282 builds across every market, then line 209 `stat("Published vol", A.fmtDero(vol, 1))` — and A.fmtDero is `fmtUnits(v, 5, ...)` (amm.js line 88). The same file's portfolio section goes out of its way to forbid this: the comment at screens.js line 100 says a number from one quote is 'never added to, averaged with, or ranked against a number from the other', and groupFor (line 103) partitions by s.quote. app/token.js line 114 is `button("Claim creator tax (" + A.fmtDero(s.tacc, 2) + ")", ...)` while the Buyback button one line above (line 113) correctly uses `A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s)`. app/home.js line 23 hard-codes 'of every trade's DERO leg'.

---

### `SEC·RT-12` — The trade sheet's refund figure for a graduating buy does not match what launch.bas refunds

**P3 · correctness / contract mismatch · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** C:\Users\Wilma\Desktop\fomo\app\amm.js launchBuy(): on the capped branch, `const cost = mulDivUp(rem, s.rd, s.ra - rem); ... refund = deff - cost; deff = cost;` — the refund is computed on the fee-and-tax-reduced amount. C:\Users\Wilma\Desktop\fomo\contracts\launch.bas buyto() lines 47-59: `LET cost = muldivup(rem, rd, ra - rem)` then `LET used = muldivup(cost, 10000, 10000 - LOAD("fee") - LOAD("tax")) + 1` (capped at d) and `SEND_ASSET_TO_ADDRESS(s, d - used, q)` — the refund is d - used, gross of fee and tax. The file header of amm.js states its purpose: 'BigInt mirror of reference/*.go: exact integer quotes so what the sheet shows is what the contract will do.'

**Problem.** For the buy that completes the curve, the sheet's 'Refund, the curve ends here' row understates what the contract actually returns by roughly the fee-and-tax fraction. The mirror is also what app.trade uses to refuse a private (ring 16) buy that overshoots — `if (priv && q.refund > Z)` — so the gate is driven by a number that is not the contract's.

**Failure scenario.** Precondition: a launch is one buy away from graduation. 1) The user enters an amount that overshoots. 2) The sheet shows a refund of deff - cost. 3) The contract refunds d - used, which is larger. The user is not harmed financially (they receive more than shown), but the sheet's stated guarantee that it mirrors the contract is false at exactly the moment the maths is hardest to check by hand.

**Root cause.** The contract's line 50 grossing-up step (used = ceil(cost / (1 - feeRate)) + 1) has no counterpart in the JS mirror; the mirror stops at the effective amount.

**Fix.** Mirror lines 50-59 in amm.js launchBuy: compute `used = mulDivUp(cost, 10000n, 10000n - s.fee - s.tax) + 1n`, cap at d, recompute fee/tax/deff from used, and return `refund = d - used` along with the recomputed fee and tax so the cost rows match too.

**Regression test.** A differential test over the graduating buy: for a range of overshoot amounts, assert A.launchBuy(s, d).refund equals the quote refunded on chain.

**Verifier.** app/amm.js launchBuy line 47: `if (out >= rem) { out = rem; const cost = mulDivUp(rem, s.rd, s.ra - rem); if (cost === null || cost > deff) return null; refund = deff - cost; deff = cost; }` — the refund is taken from the fee-and-tax-reduced amount. contracts/launch.bas buyto lines 120-129 do the grossing-up the mirror omits: `50 LET used = muldivup(cost, 10000, 10000 - LOAD("fee") - LOAD("tax")) + 1`, capped at d, then fee/tx/deff are recomputed on `used` (lines 123-125) and `59 SEND_ASSET_TO_ADDRESS(s, d - used, q)`. Worked through with fee 100 bps + tax 1000 bps, d = 1,000,000 and cost = 100,000: the mirror shows a refund of 790,000 where the contract pays 887,639. The Go model is not at fault — reference/launch.go lines 113-129 implement the grossing-up correctly — so the divergence is JS-only, against amm.js's own header claim that it is an exact mirror.

---

### `UX·F17` — Meaningless badges, decorative statistics and a launch-sheet promise the app's own risk text contradicts

**P3 · copywriting · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/home.js:118 and app/home.js:192 both render `h("span", { class: "pill live" }, h("i", { class: "dot" }), "LIVE")` in lime on the Level 1 surface; the Trade hero has no non-live state and nothing on it updates, and the badge is drawn regardless of whether the node is reachable (app/app.js:31 only recolours the header pill). docs/DESIGN_DIRECTIVE.md §5.2 says "No glowing LIVE badge" and DESIGN_SYSTEM principle 9 says state is stated, not glowed. app/home.js:194-197 renders "Ordinary swap: Ring 2" and "Private mode: Ring 16" as Level 3 read-outs — constants dressed as telemetry, in the strip whose grammar means "a live measurement". app/home.js:128 "Buy pressure 66%" with a progress bar is derived from counting blocks whose reserve moved up. app/markets.js:133 numbers every row 1..24 with r1/r2/r3 emphasis, a leaderboard convention from the products §3.4 says not to clone, and the number is only the row's index under the current sort so it changes meaning with every filter. Section-head icons are flame for Trending, rocket for Launch pipeline, sparkle for Create, gem for NFTs (home.js:154,159, nfts.js:115) — the memecoin icon set. app/forms.js:95 "Nothing to run, nothing to rug" sits in the same sheet whose creator-allocation field permits 5%, which app/home.js:22 correctly describes as "received … of the supply at launch, sellable at any time".

**Problem.** These are the elements that make the app interchangeable with any other crypto dashboard, and each fails the directive's own test (§4.10: does it communicate information, hierarchy, state or affordance?). The launch-sheet line is worse than decorative — it is a safety claim the app elsewhere contradicts, on the screen where a creator is deciding how much of the supply to keep.

**Failure scenario.** Precondition: a creator opens the launch wizard. Steps: they read "Nothing to run, nothing to rug", open Advanced, and set creator allocation to 5%. Impact: they have been told the format is rug-proof by the same sheet that is handing them the mechanism, and buyers of that token will later see H.risks warn them about exactly it. The promise and the warning are in the same product.

**Root cause.** Copy and ornament written per-screen rather than against the system's stated principles, and never re-read against each other.

**Fix.** Remove, do not replace. Delete both LIVE pills — the block clock in the read-out strip already says the app is live, and it says it with a number. Delete the Ring 2 / Ring 16 read-outs from the Trade hero; the same facts are already in the prose above them and in the trade sheet's cost block, where they are actionable. Drop the rank column from Markets or make it a stable identifier rather than a sort index. Replace flame/rocket/sparkle/gem with geometric marks from the existing P map. Rewrite forms.js:95 to "Fixed supply on a bonding curve that graduates into a locked AMM. The liquidity is locked; the creator allocation is not."

**Verifier.** app/home.js:118 and :192 both render `pill live` with a dot on the Level 1 surface, against docs/DESIGN_DIRECTIVE.md:308 ("No glowing LIVE badge") and DESIGN_SYSTEM principle 9 ("State is stated, not glowed", line ~40). home.js:194-197 renders "Ordinary swap · Ring 2" and "Private mode · Ring 16" through the same mini() read-out primitive as live measurements. home.js:128 derives "Buy pressure" from upBlocks24/downBlocks24. markets.js:133 numbers every row and emphasises the top three, and the number is the index under the current sort. forms.js:95 "Nothing to run, nothing to rug" sits in the sheet whose cbps field accepts up to 5%, which home.js:22 correctly warns about on the token page.

---

### `UX·F18` — The page the app calls the one that outranks every other piece of copy is unreachable on mobile, and so is the build stamp

**P3 · reachability · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/app.js:52-54 renders the "A message from the dev" link and the network/TELA-commit stamp into `#sideNote`, which lives inside `.side-foot` (app/index.html:40). app/styles.css:102 `.side-brand,.side-foot{display:none}` and app/desk.css:33 restores it only inside `@media (min-width:960px)`. Grep confirms app.js:53 is the only link to router.go("notice") anywhere in the app. app/notice.js:1-5 describes the page as "the plainest statement of what this thing is: unregulated, unowned, unchangeable and unsupported… Every other screen has to be careful not to contradict it", and app/notice.js:44-46 says "If any other screen in this app appears to contradict it, this page is the one that is true."

**Problem.** On every phone and tablet below 960px there is no route to #notice except typing the hash by hand. The page that states there is no operator, no support, no moderation and no refund — the disclosure a user most needs before signing anything — is desktop-only. The build/commit stamp, which is how a user verifies which version of the on-chain site they are running, is hidden the same way.

**Failure scenario.** Precondition: a user on a phone, which is the majority case for the 430px-first layout this app is built around. Steps: they look for terms, a disclaimer, or who is behind this. Impact: they find nothing. They also cannot see which TELA commit they are on, so they cannot tell whether they are running the current build or a stale cached one.

**Root cause.** The link was placed in a desktop-only chrome region rather than in a route or a screen.

**Fix.** Add the link and the build stamp to the foot of the Settings screen (app/screens.js, which already has a Build row at monoKv("Build", …) — put the notice link directly beneath it), and add it to the intro sheet (app/app.js:266) so it is seen once on first run. Settings is reachable from Profile and Portfolio on every width.

**Verifier.** app/app.js:52-54 renders the "A message from the dev" button and the network/TELA-commit stamp into #sideNote, which index.html:40 places inside .side-foot. styles.css:102 `.side-brand,.side-foot{display:none}` and desk.css:33 restores .side-foot only inside `@media (min-width:960px)`. I grepped the whole app: app.js:53 is the only `router.go("notice")` anywhere. The two renders confirm it — desk-markets.png shows "A message from the dev" and "SIMULATOR · TELA COMMIT 14" in the rail foot; 02-markets.png at 430px has neither. notice.js:1-5 and :44-46 do claim the page outranks every other screen.

---

### `UX·F19` — Vetoed withdrawals are described as a benign delivery failure, and the veto/Paying window is invisible

**P3 · honesty · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/wrap.js:379-386 maps `x.st === 2` to "Returned" with the status text "the destination could not be paid, so the units were returned to you", and app/wrap.js:414 says "The quorum returned the units and the fee to the address that burned them." contracts/vault.bas:439 Veto records no reason: it is a k-of-n vote with no predicate beyond the Paying freshness check at line 24-25, so k signers may return any withdrawal for any reason. The `w_<j>_p` Paying mark (contracts/vault.bas:426-436), which freezes a withdrawal against veto for 2000 blocks, is never fetched or shown — app/wrap.js:93 requests only _v, _f, _to, _h, _rel, _st and _ftx.

**Problem.** The screen states a cause the chain does not record. The trust panel two sections down does say correctly that "Stopping one takes k signers voting to veto or halt", so the app knows the truth and then narrates a friendlier one at the point the user actually meets it. The Paying mark is a real state — a withdrawal in the process of being paid abroad — with a real user-facing meaning, and it is dropped.

**Failure scenario.** Precondition: k signers veto a withdrawal for any reason, including censoring a destination. Steps: the user opens #wrap and reads their withdrawal's status. Impact: they are told their destination could not be paid, and will go looking for a fault in their own address rather than understanding that the federation declined them. That is the difference between a technical hiccup and the trust assumption they were asked to accept.

**Root cause.** The st=2 branch was written from the intended use of Veto rather than from what the contract records.

**Fix.** app/wrap.js:379-386 — "Returned: " + w.k + " signers voted to return this withdrawal. The contract does not record why; the units and the fee went back to the address that burned them." Fetch `w_<j>_p` alongside the other keys and, when it is set and st is 0, add a step reading "A signer has marked this as being paid on " + chain + " — it cannot be vetoed until block " + (p + 2000) — which is useful, true, and already on chain.

**Verifier.** app/wrap.js:379-386 maps st===2 to "the destination could not be paid, so the units were returned to you" and wrap.js:414 to "The quorum returned the units and the fee…". contracts/vault.bas Veto (file:439-455) records no reason and validates no destination: it is issigner + votek reaching k, with the only extra predicate being the Paying-freshness check at DVM lines 24-25 (file:446-447). I confirmed the fetch list at wrap.js:93 requests _v, _f, _to, _h, _rel, _st, _ftx and not _p, so the Paying mark — a real state with a real user-facing meaning, documented at length in vault.bas:417-425 — is never read. The trust panel does state the truth ("Stopping one takes 2 signers voting to veto or halt", visible in the render).

---

### `UX·F20` — Fee and unit disclosure gaps: the token page's Market read-outs carry no quote symbol, the withdrawal fee names no recipient, and the contract id the copy tells you to check is truncated

**P3 · consistency · auditor confidence Confirmed · verifier PLAUSIBLE**

**Evidence.** app/token.js:69 `band(mini("FDV", S.fmtDeroShort(m.mcap)), mini("Liquidity", S.fmtDeroShort(m.liq)), mini("Volume 24h", S.fmtDeroShort(m.vol24)), …)` — no symbol; app/stats.js:121 fmtDeroShort appends none. The same figures on the Markets row do carry it (app/markets.js:163 `D.qsym(s) + " V " + …`) and the price above the band is labelled "Price · " + D.qsym(s) (token.js:57). app/wrap.js:357 states the withdrawal fee amount but never says the fee pools to the vault treasury (contracts/vault.bas:459 ClaimFees pays `LOAD("treasury")`), while the registration fee three steps earlier does say "to the treasury" (app/wrap.js:276). app/token.js:13 `cpy()` renders ids via `V.shortAddr(v)` under copy reading "Click a value to copy it in full. A ticker is not an identity: check the contract id before you trade" (token.js:88) — visible in .sim/ui-dec/04-token.png as "fd86b611…15630". docs/DESIGN_SYSTEM.md §Technical data: "A value the user must read in full … is never truncated".

**Problem.** Three small breaks in the app's own best rule. A number without its unit is exactly the failure the codebase argues against everywhere else, and on a wXMR market the FDV, liquidity and volume read as bare figures beside a price explicitly labelled XMR. The fee that comes out of a withdrawal is the only fee in the app whose destination is unnamed. And the id a user is told to check is shown as 13 of its 64 characters, which is not enough to check anything.

**Failure scenario.** Precondition: a market quoted in wrapped XMR. Steps: the user reads the Market band. Impact: they read "Liquidity 4.24" and have to infer the asset. On the contract id: a user following the instruction to check the id against a source they trust cannot do so from a middle-truncated string; they must copy it and paste it elsewhere, which the copy does not say.

**Root cause.** Three independent omissions in files that otherwise thread D.qsym/D.qdec carefully; the shortAddr default was applied to the one id the surrounding copy singles out as needing verification.

**Fix.** app/token.js:69 — append `" " + D.qsym(s)` to FDV, Liquidity and Volume, or put the symbol in each label. app/wrap.js:357 — "after a " + amt(w.wfee, 8) + " fee, which the vault treasury claims". app/token.js — render the Contract id row in full with `word-break:break-all`, the way app/wrap.js:170 full() already does for the vault id and deposit addresses; keep shortAddr for Creator and Treasury, which are not what the sentence asks the user to check.

**Verifier.** Two of three sub-claims hold. app/token.js:69 renders FDV / Liquidity / Volume 24h through S.fmtDeroShort (stats.js:121), which appends no symbol, on a page whose price eyebrow is explicitly "Price · " + D.qsym(s) (token.js:57) and whose Markets-row equivalent does carry the symbol (markets.js:163). app/wrap.js:357 states the withdrawal fee amount and never names its destination, while the registration fee at wrap.js:276 says "to the treasury" — and contracts/vault.bas ClaimFees (file:463-467) does pay LOAD("treasury").

---

### `UX·F21` — An irreversible local wipe has no confirmation, the shoulder-surfing control is two hops from the screen it protects, and the reduce-motion setting does not do what it says

**P3 · controls · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/screens.js:276 `button("Clear local trade history, watchlist and cache", "", () => { for (const k of Object.keys(localStorage)) if (k.startsWith("veil.") && k !== "veil.settings") localStorage.removeItem(k); U.toast("Cleared", "ok"); })` — one click, no confirmation, no undo. app/screens.js:181 tells the user this data is "kept in this browser. Nothing is uploaded", i.e. it exists nowhere else. The "Hide balances" toggle is at app/screens.js:221 on the Profile screen; the screen it affects is Portfolio (app/screens.js:40), and Profile is removed from the mobile tab bar by app/styles.css:84. app/live.js:5 `const reduced = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches` ignores `V.app.settings.reduceMotion`, so the rolling-number animation in L.num (live.js:23-31), which is JavaScript rather than CSS and therefore untouched by the `html.reduce` rule at styles.css:104, keeps running — while the toggle's own help text (screens.js:275) promises it "turns off rolling numbers, flashes and the tape".

**Problem.** The app is scrupulous about on-chain irreversibility and careless about the one irreversible action it owns: the cost basis behind every P&L figure on Portfolio is deleted by a single unmarked click. The privacy control for a privacy product is filed under identity rather than beside the balances, and hidden from mobile navigation. And a setting states a behaviour it does not deliver.

**Failure scenario.** Precondition: a user with months of local trade history exploring Settings. Steps: they tap the clear button to see what it does. Impact: every realised and unrealised figure on Portfolio resets permanently; there is no backup and no operator. On reduce-motion: a user who ticks the toggle because motion makes them unwell still sees every price interpolate on every block.

**Root cause.** Three separate placements made per-screen. The reduce-motion case is a split source of truth: CSS honours the class, JavaScript honours only the media query.

**Fix.** Wrap screens.js:276 in a confirmation sheet naming what will be lost ("N trades of cost basis and M watched markets — this cannot be undone; there is no copy anywhere else"), and style it as the app's only destructive action. Move or mirror the Hide balances toggle onto Portfolio, next to the wrapped-asset hero it masks. app/live.js:5 — `const reduced = () => (V.app && V.app.settings.reduceMotion) || window.matchMedia("(prefers-reduced-motion: reduce)").matches`.

**Verifier.** app/screens.js:276 is one unconfirmed click that removes every veil.* key except veil.settings, and screens.js:181 tells the user this data exists nowhere else. The Hide balances toggle is at screens.js:221 on Profile; styles.css:84 `.tabs .tab[data-tab="profile"]{display:none}` removes Profile from the phone bar (desk.css:20 restores it above 960px), while the screen it masks is Portfolio (screens.js:40). app/live.js:5 `reduced()` reads only the media query and is the sole gate on L.num's interpolation (live.js:25); app.js:274 applies the `reduce` class, and styles.css:104 kills only CSS animations and transitions, which a JS textContent loop is not.

---

### `UX·F22` — Every on-chain document is permanently described as "Veil", and the install is unfixable after deployment

**P3 · branding · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** tools/telainstall/main.go:203 — `Headers: tela.Headers{NameHdr: storedName, DescrHdr: "Veil " + rel, IconHdr: *icon}` for every DOC in app/. The INDEX is correct (tools/telainstall/main.go:81 name defaults to "DMZ") but each of the ~30 documents that make up the site carries "Veil app.js", "Veil wrap.js", "Veil index.html". docs/DESIGN_DIRECTIVE.md §1.5 says the product name is DMZ and that `window.VEIL_CONFIG` and the `V`/`Veil` namespace are internal identifiers that stay — a TELA document header is not an internal identifier, it is public metadata rendered by any TELA browser.

**Problem.** The old product name is written into public, immutable chain state on every file, and the project's own standing rule is that any state needing a human to leave it is a defect — after deployment the operator has revoked their access and no one can correct it. Every future reader of the on-chain document set sees a product called Veil.

**Failure scenario.** Precondition: deployment to mainnet or any lasting testnet. Steps: run telainstall. Impact: thirty immutable documents each labelled "Veil …". Anyone inspecting the site's composition on chain — which is the whole point of shipping as TELA — reads a different product name than the one the INDEX advertises.

**Root cause.** A default string that was correct before the rename and is only reached at install time, so no test or render exercises it.

**Fix.** tools/telainstall/main.go:203 — `DescrHdr: "DMZ " + rel`. While there, tools/telainstall/main.go:82 — make the INDEX description name Monero and Zcash (see F12). Both must land before any install; neither can be changed afterwards.

**Verifier.** tools/telainstall/main.go:203 is `Headers: tela.Headers{NameHdr: storedName, DescrHdr: "Veil " + rel, IconHdr: *icon}` for every DOC, and line 243 shows the INDEX correctly taking *descr (which defaults to "DMZ: …" at line 82). So every one of the ~30 documents that compose the site carries the retired product name in public, immutable chain metadata, while the INDEX above them carries the current one. docs/HANDOVER.md §0.0 states the operator intends to revoke their own access after deployment and that any state needing a privileged party to leave it is a defect.

---

### `UX·F9` — Only the newest deposit index is readable, and the screen actively encourages creating one — hiding the address a deposit may already be in flight to

**P3 · data-loss · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/wrap.js:51-76 myVault(): `const n = Number(c["uc_" + addr])` then `const idk = "ul_" + addr + "_" + (n - 1)` — only the last index is fetched, and `dep` is built from it alone. app/wrap.js:279 offers "Register another index (… DERO)" with app/wrap.js:275 explaining "Registering again gives you a fresh index, so two deposits of yours are not linked on chain." app/wrap.js:83 `const SCAN = 60` — vaultWithdrawals only scans the newest 60 withdrawals across all users before filtering to this address.

**Problem.** The address a user has already paid to disappears from the app the moment they take the action the app recommends. The chain still credits it — the signers watch every attested index — but the user can no longer see it, verify it, or copy it. Separately, on a vault with more than 60 withdrawals in total, a user's own pending withdrawal silently vanishes from "Your withdrawals" while it is still waiting, taking its release height and destination with it.

**Failure scenario.** Precondition: a user has registered index 0, the signers have published its address, and the user has sent Monero to it. Steps: before the mint lands, they follow the screen's own suggestion and press "Register another index". Impact: the walkthrough resets to index 1 with no address yet; step 3's copied address is gone. If they need to check what they paid to — against their Monero wallet's history, or to send a second payment — the app cannot show it. Nothing is lost on chain, but the user's only view of it is.

**Root cause.** myVault was written for the single-index case and the multi-index affordance was added on top without extending the read. The 60-withdrawal scan is a fixed RPC budget with no paging and no statement that it is truncated.

**Fix.** app/wrap.js:51-76 — read all indexes for the address (`ul_<addr>_0 … n-1` in one fetchKeys call; n is small and already known) and render every one that has a live address, newest first, with the newest marked as current. app/wrap.js:83 — when `wn > SCAN`, say so: append a line to "Your withdrawals" reading "Only the most recent 60 withdrawals on this vault were scanned; an older one of yours is not listed here" rather than silently omitting rows. Better, paginate backwards from `wn` until the address's expected count is found.

**Verifier.** app/wrap.js:53-56 reads `uc_<addr>` then only `ul_<addr>_<n-1>`, and builds `dep` from that single index. app/wrap.js:279 offers "Register another index" with the copy at :275 encouraging it. Once a second index exists, `dep.addr` is empty until attestation, and wrap.js:196 collapses that to `dep = null`, so step 3's `full("Your deposit address on …")` disappears and the previous address is unreachable from the app. app/wrap.js:83 SCAN=60 with no paging and no truncation notice is confirmed verbatim.

---

### `ARCH·N1` — window.Veil hides three real couplings: feed.js calls a method defined in another file, V.markets exists only while its screen is mounted, and app.js writes into the data layer's fields

**P4 · module-boundaries/global-namespace · auditor confidence High · verifier PLAUSIBLE**

**Evidence.** `app/founders.js:7` `Object.assign(V.data, { founders: null, async loadFounders(prev) {...}, async myFounders(addr) {...} })` — adding state and methods to the data object from a different file. `app/feed.js:236` calls it: `try { this.founders = await this.loadFounders(this.founders); } catch (e) { console.warn("founders", e); }` — feed.js has no reference to founders.js and no way to know the method exists; the catch turns a missing module into a per-block console warning.
`app/markets.js:230` assigns `V.markets = { search(v) {...} }` INSIDE `router.on("markets", (el) => {...})`, so the namespace member does not exist until the Markets screen has rendered once. `app/app.js:255` is written around that: `gs.oninput = () => { if (!(...markets)) router.go("markets"); setTimeout(() => V.markets && V.markets.search(gs.value), 0); }` — a `setTimeout(0)` racing a `hashchange`, guarded by `V.markets &&` so the failure is a dropped query rather than an error.
`app/app.js:61` `D.follows = new Set();` and `app/app.js:71` `D.height = L.height || D.height;` — the app module reassigning the data module's fields.

**Problem.** There are no imports, so the dependency graph is whatever `index.html`'s script order happens to produce and whatever each file assigns at load. `feed.js`'s call to `loadFounders` is a forward reference across a file boundary that only works because `app.js` (loaded last) is what triggers `refresh()`. The `V.markets` case is worse because the member's existence is a function of navigation state, not load order: a global search typed from Home depends on a hashchange task beating a 0ms timer, and if it loses, the search box silently does nothing on the first keystroke.

**Failure scenario.** Precondition: a cold page on Home. Steps: (1) type into the global search at `#gsearch`; (2) `router.go("markets")` sets `location.hash`; (3) the `setTimeout(0)` fires before the `hashchange` handler in some browsers/task orderings; (4) `V.markets` is still undefined and the `&&` swallows it. Impact: the query is dropped and the user lands on an unfiltered Markets list. Separately, if `founders.js` were ever dropped from a bundle (genconfig walks `app/` so it would not be today), `refresh()` would log a TypeError every block and the NFTs screen would render empty with no error state.

**Root cause.** A flat mutable global used both as a module registry and as shared state, with no rule about who may add to it or when. Files are free to attach members at load time, at first render, or never.

**Fix.** Move `V.markets = { search }` to module top level in `markets.js` (the closure it needs — `q`, `draw` — can live at module scope beside `sort` and `compact`, which already do), so the member exists as soon as the script has run. Have `founders.js` register itself explicitly — `V.data.registerFounders(loadFounders, myFounders)` — or move the two methods into feed.js if the DOC budget allows (feed.js is 10,980 doc bytes of an 18,432 cap; founders.js is 2,992 raw, so it fits).

**Regression test.** In `tools/uitest/ui.mjs`, from `#home`, type into `#gsearch` and assert the Markets screen renders with the query applied on the first keystroke. The suite already has a listener-leak assertion at :157, which is the same class of check.

**Verifier.** The three couplings are all real. app/founders.js:7 `Object.assign(V.data, { founders: null, async loadFounders(...), async myFounders(...) })` adds state and methods to the data object from another file; app/feed.js:236 calls `this.loadFounders(this.founders)` inside a try/catch that turns a missing module into a per-block console.warn. app/markets.js:230 assigns `V.markets = { search(v) {...} }` inside the router.on("markets") body, so the member does not exist until Markets has rendered once, and app/app.js:273 is written around that with `setTimeout(() => V.markets && V.markets.search(gs.value), 0)`. app/index.html:48-69 confirms there are no imports — the graph is script order plus load-time assignment.

---

### `ARCH·X2` — Two test files marked temporary ship and run as if permanent, and the Paying non-refresh property — the one the contract comment calls load-bearing — is stated and untested

**P4 · test-suite-hygiene · auditor confidence Confirmed · verifier REFUTED**

**Evidence.** `tests/zzquote_test.go:3`: "// TEMPORARY measurement + smoke test for the quote-asset-parameter study. Delete after reading." It runs on every `tests/run.sh`. `tests/zz_quoteprobe_test.go` is the same shape ("Probe A: does SEND_ASSET_TO_ADDRESS…"), a DVM behaviour probe kept as a test.
`contracts/vault.bas:424-428` states the property: "The mark cannot be refreshed (line 24 returns if it is already set): a renewable mark would let one signer freeze a withdrawal for ever, and nothing here may need a human to leave it." Line 24 is `IF EXISTS("w_" + j + "_p") != 0 THEN GOTO 100` — note it returns 0, i.e. success, so a caller cannot tell a refresh was refused.
`tests/vault_paying_test.go` has exactly two tests: `TestVetoCannotPayAWithdrawalTwice` and `TestAMarkedWithdrawalThatIsNeverPaidUnfreezesItself`. Neither calls `Paying` twice and asserts `w_<j>_p` is unchanged; neither calls `Paying` from a non-signer; neither calls it on a withdrawal with `st != 0`.

**Problem.** The suite reads as if the Paying fix is covered — two tests, both named after the risk. The property that makes the fix safe against a hostile or buggy signer (non-renewability) is asserted only by a comment. A refactor that turned line 24 into an unconditional STORE would keep both existing tests green while handing any single signer an indefinite freeze on any withdrawal, in a system explicitly designed to need no human.
Separately, the temporary probes make the suite's contents an unreliable signal of what is deliberately guarded: a reader cannot tell `TestZZQuoteInstallGas`'s gas budget assertion (which is load-bearing) from the probe scaffolding around it.

**Failure scenario.** Precondition: someone hardening `Paying` and reading line 24's early return as a bug (it returns success without doing anything). Steps: (1) they change `GOTO 100` to fall through to the STORE; (2) `tests/run.sh` is green — both Paying tests still pass, since neither calls Paying twice; (3) ship. Impact: one signer calling `Paying(j)` every 1999 blocks freezes withdrawal j permanently. `Veto` at vault.bas:25 blocks while the mark is fresh, and the mark never goes stale.

**Root cause.** Tests were written against the scenario the fix was for (double payment) rather than against each invariant the fix's implementation relies on; and probe scripts were left in the package because a Go test file is the cheapest way to reach the DVM harness.

**Fix.** Add `TestPayingMarkCannotBeRefreshed`: Paying(0) from sg[0], record `w_0_p`, advance `c.Height` by 1500, Paying(0) from sg[1], assert `c.U(scid, "w_0_p")` is unchanged and Veto is still refused at the ORIGINAL mark + 1999 and permitted at + 2000. Add `TestPayingRejectsNonSigner` (one `c.Call` from `user`, assert `!r.OK()`). Move the two `zz*` probes to `tools/` or delete them, keeping only the gas-budget assertion, which belongs in `pool_test.go`.

**Regression test.** The two tests above are themselves the regression tests; verify by making line 24 store unconditionally and confirming the new test goes red while the existing two stay green.

**Verifier.** I read tests/vault_paying_test.go in full. It contains, verbatim: `// The mark cannot be refreshed, or one signer could hold a withdrawal open for ever by re-marking it.` followed by `c.Height += 500; c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 0)}, nil, sg[1]); if c.U(scid, "w_0_p") != at { t.Fatalf("the mark was refreshed: %d then %d", at, c.U(scid, "w_0_p")) }`. That is precisely 'calls Paying twice and asserts w_<j>_p is unchanged', which the finding says neither test does. The same test also calls Paying from a non-signer (`c.Call(scid, "Paying", ..., user)` guarded by `if r.OK() { t.Fatal("a stranger marked a withdrawal as being paid") }`), which the finding also says is absent. The proposed regression scenario — turn line 24's `GOTO 100` into a fall-through and watch both tests stay green — is therefore false: the refresh assertion fails immediately.

---

### `ARCH·Z1` — Dead exports and one dead field that is also wrong: app.pnl computes a per-asset average cost at hard-coded DERO decimals that nobody reads

**P4 · dead-code · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** `app/app.js:219` documents it — "Cost basis per asset comes out as byAsset[asset].avg (DERO per token)" — and `app/app.js:228` computes it: `b.avg = s && b.units > Z ? (Number(b.cost) / 1e5) / (Number(b.units) / 10 ** Number(s.dec)) : null;`. `grep -rn "\.avg\b" app/*.js` returns only those two lines. The only consumer recomputes it: `app/screens.js:137` — "avg cost in the quote's own decimals. V.app.pnl still accumulates cost in atomic units, so the division belongs here where the group's quote is known" — `const avg = b && b.units > Z ? (Number(b.cost) / 10 ** g.dec) / (Number(b.units) / 10 ** Number(r.s.dec)) : null;`.
Other dead surface: `app/forms.js:17` passes `install: true` to `V.app.tx`, which never reads `opts.install` (`grep -rn "opts\.install"` is empty; app.tx branches on `!!opts.code`). `app/amm.js:103` exports `fmtPrice` (no caller), `mulDiv`, `mulDivUp`, `bpsUp` and `B` (used only inside amm.js).
`app/markets.js:116` `const rows = new Map();` caches one DOM subtree per scid and is never pruned; `app/home.js:83` `feedEls` does the same job and is bounded at 400 (`if (feedEls.size > 400) feedEls.delete(...)`).

**Problem.** `b.avg` is the dangerous one: it is a plausible-looking, documented field carrying a number that is only correct on DERO markets, sitting one property away from the correct value. The next screen that wants an average cost will read `pnl.byAsset[x].avg` — it is right there and the comment says what it is — and inherit the exact fault that `screens.js:137` exists to work around. `install: true` reads as a meaningful flag and is not.

**Failure scenario.** Precondition: a new screen or widget needing cost basis. Steps: (1) developer reads `app/app.js:219`, finds `byAsset[asset].avg`, uses it; (2) on a wXMR market the divisor is 1e5 instead of 1e12. Impact: an average cost 10⁷ too high, presented next to a correct current price.

**Root cause.** A field left behind when its computation moved to the caller that knows the units, and the moving comment was written in screens.js rather than as a deletion in app.js.

**Fix.** Delete `b.avg = ...` from `app/app.js:228` and the sentence about it at :219 (keep `b.unreal = Z`, which is used). Delete `install: true` at `app/forms.js:17`. Drop `fmtPrice` and the unused arithmetic helpers from the `V.amm` export list at `app/amm.js:103`, keeping them local. Bound `markets.js`'s `rows` map the way `home.js` bounds `feedEls`.

**Regression test.** None warranted for the deletions. For the unbounded map, extend the existing `tools/uitest/ui.mjs` listener-leak check (:157) to also assert `Veil.markets` retains no more DOM rows than markets listed after navigating away and back.

**Verifier.** app/app.js:219 does document 'Cost basis per asset comes out as byAsset[asset].avg (DERO per token)' and :228 computes `b.avg = ... (Number(b.cost) / 1e5) / (Number(b.units) / 10 ** Number(s.dec))`. `grep -rn '\.avg' app/*.js` returns only those two lines — the field is written and never read. app/screens.js:137 recomputes it with `10 ** g.dec` under a comment saying the division belongs where the group's quote is known. app/forms.js:17 passes `install: true`; `grep -rn 'opts\.install'` is empty and app.tx branches on `!!opts.code` (app.js:123-124), so the flag is inert. app/amm.js:100 exports fmtPrice with no caller outside amm.js. app/markets.js:117 `const rows = new Map()` is never pruned while app/home.js:87 bounds feedEls at 400.

---

### `DATA·D3-17` — The in-memory incremental path never re-verifies existing history, so a reorg that keeps pc and tc unchanged is never corrected in-session

**P4 · correctness / reorg · auditor confidence High · verifier CONFIRMED**

**Evidence.** app/feed.js:180 `const chk = cached && base && (points.length || trades.length) ? [...] : [];` — the re-read set is built only when `cached` is true, and feed.js:173 sets `const cached = !(prev && prev.points)`, so whenever an in-memory record exists the verification array is empty. feed.js:175 guards only against the counters going backwards: `if (base && Number(base.pc) <= Number(s.pc) && Number(base.tc) <= Number(s.tc))`.

**Problem.** A reorg that replaces the last block or blocks with a different set producing the same p_/t_ counts leaves every retained point and trade untouched for the life of the session, because nothing is re-read and the counter guard sees no change. The localStorage path is strictly stronger — it re-reads the newest VERIFY_N = 4 records (feed.js:12, 180-186) — so a reload would correct what a running session will not.

**Failure scenario.** Precondition: a chain reorganisation of one or more blocks in which the replacement blocks produce the same number of price points and published trades. Steps: keep the tab open across it. Impact: the retained history keeps the orphaned reserves; candles, change windows and volume over that window stay on the abandoned chain until the tab is reloaded. Low probability on 18-second blocks with equal tick counts, but the app cannot detect it at all today.

**Root cause.** The counters pc/tc are used as a monotonic watermark, which detects rollback but not replacement.

**Fix.** Build `chk` on both paths — drop the `cached &&` condition at feed.js:180 — so the newest few records are re-read against the chain on every incremental sync, not only on the first load from cache.

**Regression test.** Simulate a base record whose newest point differs from the chain at the same index with equal pc/tc; assert loadContract discards and re-reads the window.

**Verifier.** app/feed.js:173 `const cached = !(prev && prev.points), base = cached ? this.fromCache(scid) : prev;` and feed.js:180 `const chk = cached && base && (points.length || trades.length) ? [...] : [];` — with an in-memory record, `cached` is false and chk is empty, so nothing is re-read. feed.js:175 guards only `Number(base.pc) <= Number(s.pc) && Number(base.tc) <= Number(s.tc)`, which detects rollback but not replacement. The localStorage path really is stronger: feed.js:181-186 fetches the chk keys and, on mismatch, removes the cache and re-reads the window. One nuance the finding does not mention that supports it: PROBE (feed.js:9) includes lh and lt, so the refresh short-circuit at feed.js:233 will notice the reorg and call loadContract — which then declines to re-verify.

---

### `REQ·A8-16` — DESIGN_SYSTEM.md says six tokens are deleted; they are still defined as aliases, and one dead computation remains in live.js

**P4 · documentation-drift · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** docs/DESIGN_SYSTEM.md, Geometry section: '`--pill`, `--r2`, `--r3`, `--sh`, `--glow-acc` and `--mood` are **deleted**. Nothing is a pill; nothing glows.' app/styles.css:25-26 still defines all six as legacy aliases (`--mood:var(--acc);--mood-a:0; --r2:var(--r1);--r3:var(--r1);--pill:var(--r1);--sh:none;--glow-acc:none`), with a comment saying older rules still name them 'while the component sheets are brought across one at a time'. I counted usages: `var(--r2)`, `var(--r3)`, `var(--pill)`, `var(--sh)`, `var(--glow-acc)`, `var(--mood)`, `var(--solid1)`, `var(--solid2)`, `var(--hair)`, `var(--s4)` — zero occurrences across app/*.css and app/*.js. Separately app/live.js:57 computes `const hue = t >= 0 ? 150 + (1 - t) * 90 : 350 + t * 15;` and never uses it; the function only sets `documentElement.dataset.mood`.

**Problem.** The migration the aliases were left for is finished — nothing references any of them — so the design system document is right about the intent and wrong about the file, and the file carries a comment describing a transition that is over. The unused `hue` is the residue of the glow the directive asked to remove.

**Failure scenario.** Precondition: a designer reads DESIGN_SYSTEM.md as the settled answer and then opens styles.css. Step 1: they find six 'deleted' tokens defined. Step 2: they cannot tell whether the aliases are load-bearing without grepping. Step 3: either the tokens survive another cycle or someone deletes them without checking. Impact: minor, but the design system is meant to be the authority and is contradicted by the first file it points at.

**Root cause.** Aliases were introduced deliberately to allow a staged conversion; the conversion completed and the scaffolding was not removed, and the document was written as if it already had been.

**Fix.** Delete the seven legacy alias lines from app/styles.css:24-26 and the stale comment (verified zero references), and remove the unused `hue` computation in app/live.js:57. If any alias is wanted, keep it and correct DESIGN_SYSTEM.md instead.

**Regression test.** Re-run the read-only UI pass at all widths after deletion; a grep assertion in the UI test that no `var(--pill|--r2|--r3|--sh|--glow-acc|--mood)` appears in any shipped stylesheet.

**Verifier.** app/styles.css:25-26 does still define `--mood:var(--acc);--mood-a:0;` and `--r2:var(--r1);--r3:var(--r1);--pill:var(--r1);--sh:none;--glow-acc:none`, under a comment at :20-21 about component sheets being brought across one at a time. I counted `var(--X)` across app/*.css and app/*.js for --r2, --r3, --pill, --sh, --glow-acc, --mood, --solid1, --hair and --s4: every one is zero. app/live.js:60 (the finding says :57) computes `const hue = t >= 0 ? 150 + (1 - t) * 90 : 350 + t * 15;` and the function only goes on to set documentElement.dataset.mood from `t` — hue is never read. Confirmed; the migration is finished and the scaffolding is dead.

---

### `UX·F23` — Dead code and dead markup left behind by the redesign

**P4 · dead-code · auditor confidence Confirmed · verifier CONFIRMED**

**Evidence.** app/live.js:56-62 mood(): computes `hue` and never uses it; sets `documentElement.dataset.mood`, and grep finds no `[data-mood]` selector in any stylesheet. It is still called every refresh from app/app.js:71. app/styles.css:25-26 still defines `--mood`, `--mood-a`, `--sh:none`, `--glow-acc:none`, `--r2/--r3/--pill` aliases, which docs/DESIGN_SYSTEM.md §Geometry says are "deleted". app/styles.css:69 `.rise{animation:none}` while app/ui.js:106 rise() still computes and writes an animation-delay per row, called from home.js:88. app/ui.css:69 `.empty .ico{display:none}` while U.emptyState (ui.js:105) still builds an icon SVG for every empty state. app/wrap.js:21-22 fetches `capratio`, `minbond`, `winmax` and `feepool` and uses none of them (see F5).

**Problem.** Bytes on a platform with an 18,432-byte-per-file DOC cap, plus per-refresh work and per-row DOM for nothing. More importantly it makes the code lie about the system: a reader of styles.css sees a `--mood` token the design system says was deleted.

**Failure scenario.** Not a runtime failure. It is maintenance cost and a false record of what the system contains.

**Root cause.** A large redesign that neutralised behaviour with `animation:none` / `display:none` rather than removing the code that produces it.

**Fix.** Delete live.js mood() and its call site in app.js:71; delete the legacy alias block in styles.css:25-26 after grepping for the remaining users of --r2/--r3/--pill/--sh/--glow-acc; delete rise() and its call in home.js:88 along with `.rise`; drop the icon argument path in emptyState and the `.empty .ico` rule; drop the four unused keys from the wrap NUM list, or better, use them per F5.

**Verifier.** live.js:57-62 mood(): `hue` is computed and never read, and it writes documentElement.dataset.mood — I grepped every stylesheet for data-mood and there is no selector. It is still called on every refresh from app.js:71. styles.css:25-26 still defines --mood, --mood-a, --sh:none, --glow-acc:none and the --r2/--r3/--pill aliases, and docs/DESIGN_SYSTEM.md:112 says those exact tokens "are **deleted**". styles.css:69 `.rise{animation:none}` while ui.js:106 rise() still writes a per-row animation-delay. ui.css:69 `.empty .ico{display:none}` while ui.js emptyState() still builds an icon SVG. wrap.js:21-22 fetches capratio, minbond, winmax and feepool and uses none.

---
