# Founders Pass v2: what adopting it changes

founders.bas v2 implements the operator's rulings of 2026-09-13 (per-asset pools; fees paid to holders in the asset
they were taken in; XMR and ZEC shares paid by the treasury's bot to addresses the holders store; the bot deposits
with any number of registered passes; 20 editions, 4 reserved). Its design, maths and tests are in
`contracts/README.md`, "founders.bas". This page listed what the tools, the app and the runbook had to change for v2
to replace v1; the status below says what is done.

## Status: adopted 2026-09-13 (branch `founders-wire`)

v2 is `contracts/founders.bas`, and `contracts/v2/` is gone. Section by section:
- **§1, done.** The move; the template and the `app/config.js` pin (`348f872e…`); the tests (v1's retired, v2's on
  `founders.bas`, the parse, canon, send-order and helper-entrypoint pins). The founders template keeps no legacy
  hash: simdeploy drops the old one and refuses `-legacy founders:…`, and the app shows a pass only when its storage
  says `v` = 2, so a v1 pass is never read with v2's keys. **Not done:** `docs/audit/CONTRACT_REVIEW_PACKAGE.md`
  still describes v1 (§1.1, §3.9).
- **§2, done for DERO and DERO tokens; the XMR and ZEC payouts are built (branch `swap-payouts`).**
  - The bot claims both legs of every market, whatever its quote, and keeps a journal per asset (layout `v` 2; a
    v1 journal with records in it is refused, not converted).
  - It deposits `share` bps of each asset with `Deposit(asset)` and reconciles against `ad_<i>`.
  - While no pass is registered it holds everything and deposits it after one registers (§8's first question,
    answered).
  - No `Distribute`, no `PayBatch`, no `minhst` wait, and the bot pushes no `Claim`.
  - Two limits of the counters. A claim is sent only when the quote leg holds something, since only `fc` proves a
    claim. The asset leg is counted at the `fa` read before the send, since no counter exists for it, so asset-leg
    fees arriving in the blocks the claim waits for reach the treasury uncounted.
  - **XMR and ZEC payouts** (`tools/treasurybot/payout*.go`, off unless `-xmr-wallet` / `-zec-node` is given):
    - income: unlocked incoming transfers to `xmrTreasury` (deploy/networks.json) read from the treasury's own
      monero-wallet-rpc, and outputs paying `zecTreasury` read from the treasury's own Zcash node(s), each counted
      once, at 10 confirmations;
    - a period ends at every `-period-blocks` DERO height, read once stable. `share` bps of its income is divided
      equally among the pass units registered at that height (`hu_<addr>` over `hst`, read from storage at that
      height's last topoheight). The remainder below `hst` is carried; with no pass registered all of it is carried to
      the first period with one. The rest stays in the treasury's wallet;
    - payout addresses (`xm_`/`zc_`) are read at the snapshot and checked fully (checksum, network prefix; Zcash
      transparent only; integrated Monero addresses refused). A holder with none keeps its part owed, and a part below
      `-xmr-min-part` / `-zec-min-part` is carried. The network fee is the treasury's;
    - XMR is paid with one `transfer` per batch of up to 15 holders (built with `do_not_relay`, relayed from the
      recorded `tx_metadata`); ZEC with a transparent transaction built and signed with zecswap's builder and signer;
    - an append-only journal per asset records each period (income, snapshot, split) and each payout's intent before
      it is built, its signed transaction before broadcast, then sent and paid. On restart a signed payout is looked
      up and the same bytes are broadcast again, never rebuilt; only a Zcash payout past its expiry that no node
      knows is voided and repaid. Tests inject a crash after every step for both coins;
    - on a mainnet config each asset refuses to pay, and says why, unless every check passes (fee address in
      networks.json, the network, the address, the wallet's login and loopback, the key file outside the repository
      and controlling `zecTreasury`, the node's login).
    - The bot is a trusted party: no contract forces a single XMR or ZEC payment. Its journals and the two chains
      are how anyone checks it.
    - Not done: the treasury's mainnet addresses are not chosen (`xmrTreasury` is empty in every block of
      `deploy/networks.json`, and `zecTreasury` everywhere but the simulator's), so a mainnet bot refuses both assets;
      and the bot's run line in `deploy/README.md` does not carry the new flags yet.
- **§3, done.**
  - simdeploy: the operator's numbers as defaults, the approved description, v2's install refusals checked before
    anything is paid.
  - verifydeploy: v2's lines; `creator` inert; the treasury a payee of mint proceeds only; the numbers and `v` read
    from storage; a payout-address row; no `minhst`.
  - testnetcheck needed no change (it sends Mint and Register only).
- **§4, done.**
  - Per-position, per-asset claims, with the hints found by binary search.
  - Withdraw by position and count.
  - The payout-address form.
  - The v1 buttons are gone.
  - Only DERO and tokens with a DMZ market are shown.
  - `notice.js`.
- **§5.**
  - Done: the runbook's founders flags, the bot's lines, and `deploy/networks.json`, which records the simulator's
    v2 pass. `docs/MAINNET_TODO.md` MT-K04, MT-K05, MT-E11 and X-29 are updated.
  - Not done here: MT-K02's swap fee, which is `tools/xmrswap` and `tools/zecswap`'s.
- **§6.**
  - Run: the DVM suite, the tool tests, verifydeploy against the simulator record, and both UI passes.
  - Not yet driven on the simulator: the on-chain flow (the bot depositing a token, claims from the app, the payout
    addresses).
- **§8, answered by the operator.**
  - Fees collected with no pass registered are held by the bot and paid in the first deposit after one registers.
  - A sale with no pass registered pays its whole price to the seller, as the contract does.
  - The app shows only tokens with a DMZ market.

**Why v2 waited in `contracts/v2/`** until its adoption. `contracts/founders.bas` was v1, and three things pinned it:
- `tests/templates_test.go` requires `app/templates/founders.bas` to be `Canon(contracts/founders.bas)`, with the
  sha256 in `app/config.js`;
- `tools/verifydeploy/roles.go` cites v1's lines;
- `tools/simdeploy/passheaders_test.go` reads v1's install checks.

A second top-level file would also have broken `tools/verifydeploy` `TestRoleKinds`, which wants a role-table entry for every
`contracts/*.bas`. The suite's static checks (`canon_test.go`, `sendorder_test.go`) read `contracts/*/*.bas` as
well, so v2 was held to every rule the deployed contracts are held to.

Canonical sha256 of v2, `reference.Canon`: **`348f872ed49ec218f361c4a70a5f6cd96ada38c3d3d8a0b960fba473c02746d3`**
(9,250 installed bytes; pinned by `tests/founders_test.go` `TestFoundersV2CanonicalHash`). Install storage gas
measured: 10,239 at the mainnet shape (the 236-byte description, no image), 11,144 with every header at its maximum,
against the 20,000 cap.

## 1. The adoption commit itself

- Move `contracts/v2/founders.bas` to `contracts/founders.bas`. v1's file becomes history, with git as its record.
- Rewrite `app/templates/founders.bas` (`simdeploy` does it) and set `app/config.js`
  `templates.founders.sha256` to the hash above. Keep v1's hash in `legacy` only if the app must still recognise a
  v1 pass; a v1 pass is not a v2 pass, and the app must not read one with v2's keys.
- Tests:
  - point the v2 tests at `founders.bas`;
  - retire the v1 tests whose subject is gone (`founders_test.go`, `founders_attack_test.go`,
    `founders_supply_test.go`, `saturation_test.go` `TestFoundersAccSaturation`, the `refute_founders_*` and
    `zzrefute_founders_*` tests, `zzfoundersapp_test.go`, the founders case of `zzconservation_test.go`,
    `reentry_test.go`'s founders row);
  - move `parse_test.go`'s v2 row onto `founders.bas`;
  - drop the founders `PayBatch` allow-list entry in `sendorder_test.go`: v2 has no loop that pays anyone.
- `docs/audit/CONTRACT_REVIEW_PACKAGE.md` §1.1 and §3.9 describe v1 at `e06c28a`. The reviewer needs a v2 section,
  or a new package.

## 2. `tools/treasurybot`

Today the bot claims DERO fees, deposits `share` bps of them with `Deposit()`, and pushes payouts with `PayBatch`
(`tools/treasurybot/main.go:155-230`). It skips every market whose quote is not DERO (`main.go:166-181`), and it waits
for `minhst` (MT-K05). For v2:

- **Deposit per asset.**
  - Claim each market's fees in both legs: `fd` in its quote and `fa` in its asset.
  - Keep one journal record per (market, asset). Today's journal (`journal.go`) is DERO-only: `Units`, `Base` and
    `PassDep()`.
  - Deposit `share` bps of each asset with `Deposit(asset)`, the asset's 32-byte id as an `H` argument, 32 zero bytes
    for DERO.
  - Reconcile each deposit against the pass's per-asset counter `ad_<i>`, where `i` is `ai_<hex id> − 1`. `dep` no
    longer exists. `ad_0` also counts market fees, so the test stays "grew by at least the deposit".
  - Stop skipping non-DERO quotes.
- **Any number of passes (MT-J14).**
  - Deposit whenever there is something to deposit and at least one pass is registered.
  - With none registered, `Deposit` is refused and refunded at ring 2. The bot keeps those fees in its journal until
    a pass registers. Whether fees earned while no pass was registered are owed to the passes at all is the
    operator's to say (§8).
  - MT-K05 (a number N of registrations before the bot deposits) is withdrawn by the ruling, and so is the `minhst`
    wait.
- **No `PayBatch`.**
  - Holders are paid by `Claim(p, i, ka, kb)`: one position, one asset, one transfer.
  - Anyone may send it, at any ring size, and the owner is paid.
  - If the bot pushes payouts, for each open or closed position `p` and asset `i` with something owed:
    - compute `ka`/`kb` by binary search over `rt_<i>_<k>` (§4);
    - compute the amount as `pu_<p> × (to − from)` exactly as `contracts/README.md` states it;
    - send only when it is non-zero.
  - Budget the sends: they are (positions × assets) calls, not ⌈holders/15⌉.
- **XMR and ZEC.**
  - The 0.5 % swap fees are taken in XMR and ZEC off the DERO chain, and the bot pays holders `share` bps of them
    directly.
  - The split is proportional to registered passes: `hu_<addr>` over `hst`.
  - The bot keeps its own records:
    - a snapshot per payout period: the fee total, `hst`, every `hu_<addr>`, the stored `xm_<addr>` / `zc_<addr>`,
      and the block height it read them at;
    - then each payment: txid, amount, address.
  - Taking the snapshot at the period's end, rather than at payment time, keeps a holder who leaves between the two
    paid for the period they were registered.
  - Validate each stored address fully before paying, which the contract cannot do:
    - base58 checksum;
    - the network byte of the chain the bot pays on (Monero mainnet `4`/`8`; Zcash mainnet `t1`/`t3`);
    - for Monero, a length of 95 (standard or subaddress) or 106 (integrated).
  - A holder with no address, or one that fails, is reported and their share is kept in the bot's records as owed.
    It is never sent anywhere else.
  - No contract forces any of this: it is the treasury's promise, and the records are how anyone checks it.
- **Rounding.**
  - `share` bps is floored per asset, as today (`journal.go:190`).
  - The contract splits exactly, with a remainder below `hst` carried per asset. The bot needs no dust handling of
    its own on chain.
  - Off chain it must floor each holder's XMR/ZEC share and carry the remainder to the next period, or state where it
    goes.
- Tests to write, against its `chain` interface fake:
  - per-asset claim → deposit → reconcile;
  - a crash between claim and deposit, per asset;
  - no deposit at `hst` 0;
  - a period snapshot and payout to stored addresses, including a malformed one;
  - payouts proportional to `hu_`.

## 3. `tools/simdeploy`, `tools/verifydeploy`, the rest of `tools/`

- **simdeploy.**
  - The install arguments are v1's: name, descr, icon, file, price, maxsupply, start, reserve, share, mfee,
    treasury.
  - `checkPassHeaders` (`main.go:139-160`) keeps every bound: v2 checks exactly v1's.
  - `TestPassLimitsAreTheContracts` must read the adopted file.
  - `passDescrDefault` and `deploy/README.md`'s `-pass-descr` become the approved v2 description (236 bytes):
    "Register a pass for a fixed share of DMZ's trading and swap fees, paid in the asset each fee was taken in: DERO and DERO
    tokens through this contract, XMR and ZEC by the treasury's bot. The bot makes each payment; no contract forces
    it."
  - The mainnet flags: `-pass-max 20 -pass-reserve 4` (4 ≤ 20/5).
  - The install needs no DERO attached. v2 refuses DERO at install, where v1 kept it.
- **verifydeploy roles** (`roles.go:212-228`):
  - re-cite every line against v2;
  - `creator` stays inert (no function reads it; `TestFoundersV2HasNoOwnerAndNoSetter`);
  - `treasury` receives `Mint` proceeds only;
  - the numbers (`share`, `mfee`, `price`, `maxsupply`, `start`, `reserve`) are written only by `InitializePrivate`;
  - the `initd` guard is line 5;
  - there is no `minhst`, and no role or row should look for one;
  - add a row for the payout addresses: data only, written by their holder, read by no function.
- **verifydeploy identity and release** compare the canonical hash above, through `srchash`.
- **testnetcheck/exercise** (`-mode social`) calls `Register` with one pass, which is unchanged. Any step that calls
  `Claim()`, `PayBatch` or `Deposit()` must take v2's arguments.
- **uitest** (`--pass-only`) drives the pass screen and changes with it (§4).

## 4. The app

- **Pass screen** (`app/nfts.js`, `app/founders.js`).
  - Show per-asset accruals.
  - For each of the wallet's positions (`po_<p>` equal to its raw key; the page reads positions by exact key, newest
    first, in a bounded window as it reads listings) and each asset `i < an` it recognises (`ah_<i>`), show:
    - what `Claim` would pay, `pu_<p> × (to − from)`;
    - `from` = `pd_<i>_<p>` if present, else the accumulator at `pt_<p>`;
    - `to` = `ac_<i>` if `pc_<p>` is 0, else the accumulator at `pc_<p>`;
    - "the accumulator at tick t" = `ra_<i>_<k>` for the first record `k` with `rt_<i>_<k> ≥ t`, found by binary
      search with exact-key reads, else `ac_<i>`.
  - The same search gives `ka` and `kb`.
  - List assets by what the app can name: anyone can deposit a token and create an index. An unknown token is shown
    by its id, or hidden behind a toggle, never labelled as a fee asset.
  - One "Claim" per (position, asset) with something owed.
  - "Withdraw" takes a position and a count.
  - The v1 buttons go: "Release waiting DERO" (`Distribute`) and "Push rewards" (`PayBatch`).
- **Payout-address form.**
  - Two fields, Monero and Zcash transparent, shown to a registered holder. They call `SetXmr(addr)` / `SetZec(addr)`
    at ring 2; an empty value clears.
  - Check in the page before sending, as the contract does: 95 or 106 base58 characters; 35 starting with `t`.
  - Show what is stored (`xm_<addr>`, `zc_<addr>`) and let a former holder clear it.
  - Say plainly, next to the field:
    - the address is public on chain for good;
    - a fresh subaddress (Monero) or a fresh transparent address (Zcash) is the one to use;
    - the treasury's bot pays these, and no contract forces it.
- **`app/notice.js` wording.** Today it says fees in a wrapped asset reach no holder (`notice.js:43`, `:93`). It must
  say:
  - the passes receive `share` of the fees the treasury claims, each in the asset the fee was taken in;
  - DERO and DERO tokens arrive through the contract, where each holder claims them;
  - XMR and ZEC are paid by the treasury's bot to the address the holder stored;
  - the bot makes every deposit and every XMR/ZEC payment, and no contract forces either;
  - a deposit is shared by the passes registered at that moment;
  - nothing is deposited while no pass is registered.
- **`app/config.js` and `app/templates/founders.bas`**: §1.

## 5. The runbook and the records

- `deploy/README.md`:
  - the mainnet `simdeploy` command gets the v2 description and `-pass-max 20 -pass-reserve 4`;
  - `docs/MAINNET_DEPLOYMENT.md` §1 and §5 describe v2: per-asset rewards, XMR/ZEC addresses, no `PayBatch`;
  - the treasury bot's run line gains whatever per-asset and XMR/ZEC flags it grows, and its journal path.
- `deploy/networks.json`: the new template hash in each network's `templates.founders`, once installed.
- `docs/MAINNET_TODO.md`:
  - MT-K01/MT-K04: per-asset pools, answered and built here;
  - MT-J14/MT-A08: "any number", FEE-01/FEE-02 closed by refusing deposits at `hst` 0;
  - MT-K05: withdrawn;
  - MT-J16: the v2 description and a reserve of 4;
  - MT-K02: the 2026-09-13 ruling puts a 0.5 % fee on the swaps, taken off the DERO chain. MT-K02 still reads "no
    fee on the swaps" and should be re-recorded. Where that fee is taken is `tools/xmrswap` and `tools/zecswap`'s
    business, not this contract's.
  - The rows cited as X-29 are not in this worktree's `docs/MAINNET_TODO.md`; add or reconcile them there.

## 6. The test plan

- The DVM suite covers the contract (`tests/founders_test.go`):
  - hand-worked shares in DERO and two tokens across registrations and withdrawals;
  - a seeded fuzz with per-asset conservation after every accepted and refused call;
  - flat gas against assets and holders;
  - payout-address bounds;
  - v1's kept behaviours;
  - FEE-01, FEE-02 and FEE-07;
  - hint checking and stray value;
  - install gas, the hash pin, and no owner.
- After adoption:
  - the same tests on `contracts/founders.bas`, with `VEIL_CONS_N=4000` once;
  - `TestShippedTemplatesAreTheTestedContracts` with the new template;
  - `tools/verifydeploy` `-roles` and its tests;
  - `simdeploy` `TestPassLimitsAreTheContracts`.
- On the simulator:
  - install with `simdeploy`, mint, register;
  - the bot deposits DERO and a wrapped token;
  - claim both from the app;
  - withdraw, deposit again, claim the closed position;
  - store and clear both payout addresses;
  - `verifydeploy` against the record;
  - the gas the wallet estimates for `Claim`, `Deposit` and `SetXmr` against what the tests measure (`DERO.GetGasEstimate`
    runs at height 0, which changes nothing here: no v2 path reads the height except `Mint`).
- Off chain: the bot's XMR/ZEC payout records against a hand-worked period, on stagenet and Zcash testnet.

## 7. What the contract does not do

- It does not pay XMR or ZEC.
- It does not check that a stored address is valid beyond length and alphabet.
- It does not know the swap fees.
- It cannot oblige the treasury to deposit anything.

Every payment outside `Claim` is the treasury bot's, trusted as the treasury key is trusted today
(`docs/audit/CONTRACT_REVIEW_PACKAGE.md` §7).

## 8. For the operator

- **Fees earned while no pass is registered.** The contract refuses to hold them. Does the bot deposit them when the
  first pass registers (that pass takes them all), or keep them as the treasury's?
- **A market fee on a sale with no pass registered** goes to the seller. The alternative is refusing such sales.
- **FEE-07.** A pass registered just before a deposit still takes a full share of it. v2 keeps v1's behaviour. A
  registration lock is possible (settle nothing until L blocks after `pt_`), at a small cost per `Claim`.
- **Tokens anyone can deposit** create an asset index each. That is harmless to the contract, but the app needs a
  rule for what it shows.
