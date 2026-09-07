# RISKS.md — DMZ

Dated register. "Mitigation" states what the code does; "Residual" what remains for the operator.

## R-1 uint64 wrap-around (2026-09-05) — highest risk
DVM arithmetic wraps silently (DECISIONS §1.1). Mitigation: every product goes through the audited 128-bit helpers;
every addition is guarded; every line is annotated with its bound; 10k-case fuzz against `math/big` is a release gate
(Phase 1). Residual: a helper mis-transcription would be caught only by the fuzz — keep it in CI.

## R-2 Refund on failure depends on ringsize 2
A failed call refunds burned value only when the signer is extractable (DECISIONS §2.3-2.4). Mitigation: every call
that is paid by `SIGNER()` is sent at ringsize 2 (`app/app.js:99`) and the app shows the privacy note; the ring-16
private-mode calls go to the `…To` entrypoints, which never read `SIGNER()` and which commit and refund instead of
reverting (`contracts/launch.bas:83-91,166-174`; `tests/ringswap_test.go`), so nothing is lost above ring 2 on the
app's own paths. The app rejects a private trade whose receive address is invalid (`app/app.js:186`) and one that
would overshoot the curve into a refund (`app/app.js:187`). Residual: a hand-crafted call to a `SIGNER()` entrypoint
above ring 2, or to a `…To` entrypoint with an unparseable `dest`, which fails the address guard at
`contracts/launch.bas:86,169` and reverts, loses the deposit.

## R-3 No cross-contract verification
The factory cannot check a registered pool (DECISIONS §8.2). Mitigation: registrations cost a fee; the app verifies
code equality with the shipped template and checks `asset`/`treasury`/`fee` before listing anything; templates have
no `UpdateCode`. Residual: a modified fork of the app could list malicious pools — the on-chain TELA app is the
trust anchor (its SCID is the "domain").

## R-4 First-liquidity inflation attack (pool.bas)
Mitigation: Uniswap-v2 style `MIN_LIQ = 1000` LP units locked at first deposit, first deposit must exceed it.

## R-5 Storage growth
`t_<n>` and `p_<n>` grow without bound. Mitigation: paid by the trader; app reads key ranges. Residual: `variables:true`
on a very active pool becomes slow — enable the Gnomon read cache behind the feature flag then.

## R-6 XSWD rate limit / single in-flight request
10 req/s, burst 20, one permission prompt at a time (DECISIONS §5). Mitigation: `xswd.js` queues and paces requests.

## R-7 Fork dependency
XSWD exists only in `civilware/derohe`. Users on the official CLI wallet have no XSWD; Engram is the supported host.

## R-8 Explicitly out of scope (do not build)
- Tokenised equities / synthetic stocks: no regulated issuer, no oracle on DERO → derivatives exposure. Placeholder only.
- Cross-chain memecoins / bridges: no production bridge; a custodial one contradicts the product.
- Fiat / card on-ramp.
- Any backend that holds keys or is required for the app to work.

## R-9 Regulatory exposure — plain-English summary for an operator (not legal advice)
What this is: a set of immutable contracts on a public, privacy-preserving blockchain, plus an app whose files are
also on that chain. There is no server, no account system, no custody: users hold their own keys and interact
through their own wallet. The operator's only on-chain powers are the factory's treasury pointer and registration
fee; protocol fees accrue to the treasury address recorded at each market's install, and anyone may trigger
`ClaimFees` to move them there (R-13), so the operator takes the revenue without controlling the claim.

What that implies:
- **Fees are revenue.** A 0.5 % cut of every trade flowing to an address the operator controls is a business, whether
  or not there is a company behind it. Many jurisdictions treat operating or profiting from a crypto exchange venue
  as a regulated activity (money transmission, VASP/CASP registration, securities/commodities rules for the assets
  traded). Non-custodial does not automatically mean unregulated.
- **No KYC, privacy by design.** DERO hides balances and, above ringsize 2, senders. Travel-rule and AML expectations
  cannot be met by the venue itself; some jurisdictions restrict privacy-coin trading outright.
- **Memecoin launches.** The operator does not create tokens, but hosts a launchpad with a fee. Depending on where
  the operator sits, that can look like promoting or facilitating unregistered offerings. Anti-rug defaults help
  users; they do not change the legal picture.
- **Immutability cuts both ways.** Once published, the operator cannot delist a pool from the chain; only the app's
  registry hints and the app itself can be changed (INDEX updates), and anyone can serve an old commit.
- **Tax.** Protocol-fee income and any tokens received are taxable events for the operator in most places.

Recommendation: obtain a legal opinion for the operator's jurisdiction before any public deployment, decide who the
treasury entity is, and keep the fee address, the app INDEX owner key and the factory owner key in separately
controlled wallets. Consider a jurisdiction-specific disclaimer in the app's Settings page and a documented process
for INDEX updates (who signs, what changed, which commit TXID).

## R-10 Large token balances are slow to decrypt (2026-09-05)
DERO wallets decrypt balances by search; the cost grows with the number of atomic units held, and while a wallet
decodes, XSWD blocks every other request (DECISIONS §5.2). Mitigation: launch defaults of 10⁹ total units, a warning
above 10¹¹, and the app never polls balances of unlisted assets. Residual: a creator can still choose a huge supply;
their holders' wallets will be slow.

## R-11 Client-side rendering and image privacy (2026-09-05)
Names, tickers, descriptions, tags, bios and image URLs are attacker-controlled strings stored on chain. The app
renders every one as a text node after `ui.clean` (control/bidi/zero-width characters removed, length bounded),
never as HTML, and ships a Content Security Policy that forbids inline and third-party script, so a rendering slip
cannot become code execution. Residual: image URLs. Loading a creator's icon reveals the viewer's IP address to
that host; the app sends no referrer and **ships with images off** (Settings → Security & privacy turns them on;
`app.js` `images: false`), so listings show generated avatars until the user opts in. Enabling images is the one
setting that makes the browser contact a host a creator chose.

## R-12 Read scalability without an indexer (2026-09-05)
Reads are incremental (only new `p_`/`t_` keys per block, bounded cache), so a market with a year of history costs
the same per block as a new one. What still grows: the first load of a market (capped at 4 000 points / 2 000
trades; older history stays on chain and is not shown), the factory registry read (one key per listing), and the
social contract (`variables:true`, fine to a few thousand profiles). Beyond that, the optional Gnomon cache path
(Settings) is the intended next step; it must never be the only path (DECISIONS D-7).

## R-13 Founders Pass fee hand-off is a wallet step (2026-09-05)
The market contracts pay their protocol fees to the treasury *address*; nothing on DERO lets them pay the pass
contract directly. So "holders get X % of every fee" is a promise the treasury keeps by running `tools/treasurybot`
(claim fees → deposit the share → push payouts). Mitigations: the share is stored in the pass contract and cannot be
changed; deposits, marketplace fees and payouts are public counters the app shows next to the promise; anyone can
deposit and anyone can push payouts; fee accrual per market (`fd`) is public, so under-delivery is visible to all.
Residual: an operator can stop the bot; holders would see deposits stall. Marketplace fees on pass trades need no
operator at all. Operational note: the bot must run from the wallet that the markets name as treasury (simulator:
wallet 2, RPC :30002), and it forwards only the DERO leg of the fees (asset-side fees stay with the treasury).

**Improved 2026-09-07:** `ClaimFees` on every market is now permissionless — anyone may trigger it, and it
always pays the treasury address recorded at install. So the *claiming* half of the fee path no longer depends on
any key being online: a pass holder who sees fees accruing can move them themselves. What still depends on the
treasury is the second half, depositing the promised share into the pass, because a DERO contract cannot pay
another contract. That remains the trusted step, and it is the one to design out next.

## R-14 Federation custody of wrapped assets (2026-09-06)
wXMR / wZEC / wSOL are backed by coins held under keys that are not the DERO contract. k colluding signers can steal
or freeze the reserve; fewer than k can only stall. The DERO side bounds the damage (`cap`, once-only ids, bonds,
slash, halt; see research/wrapper/README.md (security review)) and `SetViewKey` (k votes) publishes the foreign
viewing key, which is what lets anyone count the reserve, but the trust is real and is the same class as
Wormhole / Ronin / THORChain losses. Mitigations: threshold custody, a low starting cap tied to bonds, disclosure in
the app, and the atomic-swap exit hatch. Rotation is no longer the open item it was: `AddSigner2` (k votes) replaces
a slashed or lost signer, the candidate must already hold `minbond`, a key that was ever a signer never returns, and
a vault has at most 20 signer slots for its whole life. Residual: rotation itself needs a quorum, so losing k signers
is the case nothing recovers from — the reserve is unreachable and the bonds are not a payout to holders.

**Improved 2026-09-07:** `tools/vaultsigner` speaks vault v3 and refuses any other version. It verifies every
foreign payment on the foreign chain before voting `Released` (`Backend.VerifyRelease`), and its shortfall test is
`reserve >= sup - inflight`, where `inflight` is every burned withdrawal the contract has not closed. Comparing the
reserve to `sup` alone is not a stricter reading of the same invariant: it halted the vault on its first ordinary
withdrawal and could never clear, because `Released` is refused while halted. That bug was found and fixed on
2026-09-07. A halt is a self-clearing condition, not an operator decision: each signer votes `Halt(1)` while the
reserve is short of what is owed and `Halt(0)` once it is not, so no human lifts it and there is no override
anywhere.

## R-15 A freshly received asset cannot be spent immediately (2026-09-06)
On the simulator, a wallet that had just been minted 250,000 units of the vault asset could not spend them for
about two blocks ("could not obtain encrypted balance for self … Account Unregistered"); a retry 40 s later passed.
The Burn sheet (and any first spend of a new asset) must retry with a clear message rather than fail.

## R-16 graviton refuses tree names that start with a colon (2026-09-06)

graviton `snapshot.go:162` returns "Bucket cannot start with :" for a tree name whose first byte is 0x3a. derohe names a
contract tree by the raw SCID bytes (`dvm.Wrapped_tree`). Seen twice today: the test harness (random ids, one in 256
installs panicked; fixed by never generating such an id) and the real daemon estimator `DERO.GetGasEstimate`, which
simulates an install under an id derived from the content and fails every time for `templates/founders.bas` as a TELA
DOC. A real install is never affected: the wallet forces the first byte of every install txid to be ≥ 0x80
(`walletapi/transaction_build.go:293`), so no live contract tree can start with ':'. Only the estimator's simulated id
(a hash of the content) can. Mitigations: `telainstall`, `simrpc.Send` and the app send such installs with a size-based
fee (code length + 512, measured overhead is a few hundred units; `-reuse` resumes a partial TELA install); the harness
never generates such an id. Report upstream with the graviton line.

Addendum (2026-09-06 16:50, historical): the then-current `contracts/vault.bas` (14,010 bytes) was itself such a
content — `DERO.GetGasEstimate` failed on every install attempt (`dvm.Wrapped_tree` got id `3a 0a 1f df …`). The id
is a hash of the content, so the file as it stands today (18,895 bytes) is very unlikely to hit it again; the
mitigation is kept because any future revision can. `tools/simrpc` fees such installs by size (code length + 512);
the same rule sits in `telainstall` and in the app (`app/app.js:121`). A real install on mainnet whose txid starts
with 0x3a remains unverified.
