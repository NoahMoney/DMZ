# DMZ

**A Monero and Zcash exchange.** Its markets, its assets and its escrows are DERO smart contracts, and the
application that drives them is a set of static files served from the DERO chain itself. DERO is the execution
and hosting layer — contracts, gas, delivery. XMR and ZEC are what people come to trade.

There is no server, no database, no API and no account. The application signs nothing and holds nothing; every
write is a contract call the user's own wallet signs. Two things are run on other people's behalf, and both are
named wherever a user reads about them: the treasury's bot, which pays the Founders Pass share, and — on the
simulator only — the federation vault, which is custody by named third parties and is never called trustless.

---

## 1. What runs today

"Runs" means driven end to end, with the transaction ids in `docs/history/HISTORY.md` or the log named. **Nothing
here has run on DERO, Monero or Zcash mainnet.**

| Capability | State | Detail |
|---|---|---|
| Token markets (AMM and bonding curve) | **Simulator** | `pool.bas`, `launch.bas`, priced in DERO or in a DERO token |
| Assets (NFA, ART-NFA-MS1) | **Simulator** | `nfa.bas`: one contract per asset, its own marketplace, priced in any quote |
| Founders Pass | **Simulator** | `founders.bas` v2: 20 passes, a reward pool per asset, payout addresses for XMR and ZEC (§6) |
| DERO ↔ XMR atomic swap | **Real Monero node software, regtest** | `htlc.bas` v3 and `tools/xmrswap` against monerod and monero-wallet-rpc 0.18.5.1 in regtest, with the DERO simulator. All four exits (Claim, Refund, Punish, Reclaim), a real reorganisation, and a whole swap from the command line as two separate processes (`research/regtest/`). **Not yet on Monero stagenet, and its proof is unaudited** |
| DERO ↔ transparent ZEC swap | **Public Zcash testnet** | `swap.bas` and `board.bas` with `tools/zecswap`. A whole swap ran on the public Zcash testnet on 2026-09-13 (DERO leg on the simulator), about 17 minutes from offer to both sides paid. Before that: three swaps and three real reorganisations against zebrad 6.3.0 in regtest, and a swap and a refund from the command line |
| Wrapped XMR / ZEC (the federation) | **Simulator only, not deployed on mainnet** | `vault.bas` is tested; `tools/vaultsigner`'s only working backend is a JSON file (§4) |
| Chat, profiles, follows | **Simulator** | `chat.bas`, `social.bas` |

**Verdict: NOT MAINNET READY** (`docs/MAINNET_SECURITY_GATE.md`). Every defect found that the repository could fix
is fixed with a regression test. What stands between this and mainnet is not fixable from here:

1. **The Monero swap's cross-curve proof is unaudited.** It proves `SHA256(s) = H ∧ s·G = S` with dalek's R1CS API,
   which upstream marks unaudited.
2. **No external review of the contracts.**
3. **The Monero swap has not run on a public network**, and the current contracts are not deployed on the DERO
   testnet.

`docs/MAINNET_TODO.md` is the list of everything open for mainnet, with the operator's rulings; `docs/BACKLOG.md`
is everything else; `docs/audit/FINDINGS_2026-09-10.md` is the adversarial audit, each finding with its status.

---

## 2. How it is put together

```
    a person's browser                     the DERO chain                    that person's own machines
 ┌──────────────────────┐            ┌───────────────────────┐          ┌──────────────────────────┐
 │  app/  (static)      │  reads     │  factory  social      │          │  xmrswap  (Monero side)  │
 │  vanilla JS, no      │──────────► │  pool · launch        │          │  zecswap  (Zcash side)   │
 │  framework, no       │            │  nfa · nft            │          │                          │
 │  dependency, no      │  signs     │  founders             │◄─────────│  the treasury runs:      │
 │  key, no storage of  │──────────► │  swap · board · htlc  │  calls   │  treasurybot             │
 │  anything secret     │  via XSWD  │  chat                 │          │                          │
 │                      │  wallet    │  (vault: simulator)   │          │  each holds its own keys │
 └──────────────────────┘            └───────────────────────┘          └──────────────────────────┘
```

**The application** is 27 JavaScript files (one, `config.js`, generated) and four stylesheets under `app/`, with no
framework, no bundler and no dependency. Its Content-Security-Policy is `default-src 'none'` with
`script-src 'self'`, so it cannot load an outside script. It is delivered as TELA documents, each installed in one
DERO transaction under the 20,000-unit storage-gas cap, so `genconfig` holds every file under 18,250 bytes after
gzip and base64. It reads the DERO node the user names in Settings, and hands every write to the user's own wallet
over XSWD on `localhost`. It never sees a key, never proxies a transaction, and stores nothing secret.

On mainnet the TELA INDEX that serves the application is installed at ring size 16, so its owner is recorded as
`anon` and no key can update it; `tools/verifydeploy` checks that on chain. A new version is a new INDEX with its
own id. A development build may be served from an INDEX someone can update, and the app says which it is.

**The contracts** are 12 DVM-BASIC files under `contracts/`. DVM-BASIC has uint64 arithmetic, no floating point, no
cross-contract calls and a 20,000-unit ceiling on install storage gas. `reference/canon.go` strips comments at
install, so the repository keeps the prose and the chain gets the code; the hash the application checks is the hash
of what executes. No contract's code can change once installed, and none deployed on mainnet has an admin; a fix is
a new contract at a new id.

**The companions** are Go programs under `tools/`, each run by the person it belongs to, on their own machine,
against their own nodes. None is a service and none is hosted. They depend on three pinned modules
(`filippo.io/edwards25519`, `golang.org/x/crypto`, and decred's `secp256k1` for the Zcash signatures) and the
standard library. The Monero proof is a Rust prover under `research/`, built and run in WSL.

---

## 3. Ring size 2, and what that costs

Every contract entrypoint that pays or authorises somebody reads `SIGNER()`, and DERO only reveals the signer at
ring size 2. Above it, `SIGNER()` is 33 zero bytes and every such entrypoint fails closed. So almost every call
this application makes is a ring-2 call, and **the address that made it is computable by anyone**.

The application says so on the intro screen and in Settings. Balances on DERO stay encrypted. But a pool records
its reserves at the end of every block they move in, so the size and direction of a trade are public, and the
ring-2 signer can be joined to it. Trading here is pseudonymous, not private.

The exceptions are the `...To` wrappers on `pool.bas` and `launch.bas`, which pay a named address instead of the
signer and can therefore run at ring 16, while the receiving address is public. That hides the payer among eight
accounts, not sixteen: a public bit in every DERO ring proof says which half of the ring the payer is in.

---

## 4. The ways value moves

### XMR ↔ a DERO-side asset, without a custodian

Bob holds DERO and locks it in `htlc.bas` for a named taker under three hashes; Alice holds XMR and locks it into
a joint Monero address whose two key shares are tied to those hashes by a client-side Bulletproofs proof. Bob hands
Alice his gate secret off chain only once her Monero lock has confirmed. The lock has four exits, each at its own
heights, so no two are open at once:

| Exit | Who, when | Who ends with what |
|---|---|---|
| **Claim** | Alice, before `t1`, with `s_a` and Bob's gate secret | Alice takes the DERO; `s_a` is now on chain, so Bob sweeps the XMR |
| **Refund** | Bob, in `[t1, t2)`, with `s_b` | Bob takes his DERO back; `s_b` is now on chain, so Alice recovers her XMR |
| **Punish** | anyone, in `[t2, t2 + g2)`, with no secret | pays Alice Bob's DERO. Her XMR stays under the joint key unless Bob ever reveals `s_b` |
| **Reclaim** | Bob, from `t2 + g2`, with `s_b` | a lock nobody punished returns to Bob; revealing `s_b` lets Alice recover her XMR at any time after |

On mainnet `htlc.bas` is installed with `w1min` = `w2min` = 400 blocks and `g2` = 4,800 blocks (24 hours), fixed for
the contract's life. No state needs a third party to leave it: before `t2 + g2` time alone opens an exit someone may
send, and after it the only party whose inaction keeps the lock open is the one it pays.

What each side trusts is its own nodes' answers and its own companion being alive at its window. `docs/XMR_SWAP.md`
states the invariants and the adversarial cases; `tools/xmrswap` asserts each invariant by name in its tests and
kills both companions at random points in `TestCrashFuzz` to show the state machines resume.

The two parties talk through a sealed channel: every message is AES-256-GCM under a per-swap key the maker hands the
taker out of band, over a directory transport. A reader of the directory learns that messages pass and their sizes,
not their contents, and nobody without the key can write one that opens. So today the swap works between two people
who can already exchange that key; there is no way to find a stranger to swap with.

### Transparent ZEC ↔ a DERO-side asset, without a custodian

Transparent Zcash is Bitcoin script, so this is a two-chain hash-timelock with no new cryptography. The maker posts
an offer on `board.bas` carrying the hash of a secret only they hold; the taker accepts, naming the key that will
refund them and the height at which it may; the maker locks DERO in `swap.bas`; the taker funds a
pay-to-script-hash output on Zcash; the maker claims it with the secret, publishing it; the taker claims the DERO
with it. Three contract rules keep either side from holding the other up:

- **Anyone who reads the secret may send the Claim**, and it pays only the taker.
- **An accepted offer the maker has not locked for is dead 200 blocks after the Accept.**
- **Every lock names its taker** and is at least 1,600 blocks wide, fixed at install.

The maker holds the secret and locks first on purpose: whoever locks first carries the free option, and the Zcash
holder is this market's customer, not its liquidity provider.

**What this reveals.** Zcash's transparent side is public: the amount, both transparent addresses, and the hash,
which also appears on DERO, joining the two chains and, through the ring-2 calls, both DERO addresses. ZEC that came
from the shielded pool shows its unshielding. Shielded Zcash cannot be used here. `docs/ZEC_SWAP.md` §1 says this
first, and the application says it above the fields.

`tools/zecswap` builds and signs v5 and v6 transactions whose ids and digests match Zcash's test vectors and
real zebrad transactions. It checks every contract's code before trusting it, reads from as many nodes as it is
given (all must agree before an irreversible step), can check the Zcash headers itself (`-verify-headers`), and
signs the taker's refund in advance at three fee levels so anyone holding it can send it after `cltv`.

### XMR or ZEC → wrapped units → back, with a custodian (simulator only)

The federation is **not deployed on mainnet**: the mainnet record names no vault, and the mainnet build's Wrap
screen says wrapped assets are not offered. On the simulator, coins go to an address that k of n bonded signers
control; the signers vote and `vault.bas` mints an equal number of DERO-side units, and burning units records a
withdrawal the signers pay out. **This is custody**: a colluding quorum can take the whole reserve, invisibly to
DERO. No state in the contract needs a human to leave it — a halt is a lease that expires, `Reclaim` is
permissionless and pays only the burner, and after `Seal` the installer has no powers. `tools/vaultsigner`'s
`monero`, `zcash` and `solana` backends are not implemented and exit; its `sim` backend is a JSON file and is
refused on any network but the simulator. No vault has ever held a real coin.

### DERO → tokens, NFAs and the rest

`pool.bas` is a constant-product AMM owned by its liquidity providers. `launch.bas` is a bonding curve that
graduates into a locked AMM, with optional creator tax, buybacks or staking. `nfa.bas` is one contract per asset
following ART-NFA-MS1 — the headers Engram, Gnomon and dReams index — extended with a `quote` parameter.
`factory.bas` is a paid registry with no owner: its treasury and registration fee are fixed at install. Anyone can
register any contract id, and the application decides what is verified by hashing the code against the templates
it ships with.

---

## 5. What you are trusting, per thing

| For | You trust | If it is wrong |
|---|---|---|
| Every screen's numbers | The DERO node you named in Settings | You see a false price or balance; it cannot move your funds by itself |
| Every signature | Your own wallet, over XSWD on localhost | The application never holds a key and cannot sign |
| A market is what it claims | The template hash the app checks, and the treasury it names | A contract whose code does not match is shown as unverified |
| An atomic swap | Your own nodes, your own companion, and being online (or having a machine online) at your window | Miss your window and the other side's exit opens. A node that lies to you **and** works with your counterparty can cost you your side of the swap, so run your own (`docs/CROSS_CHAIN_TRUST_MODEL.md` §5). No lock is ever frozen |
| Founders Pass payouts | The treasury and its bot | If the bot stops, nothing new is deposited and no XMR or ZEC is paid; what was deposited stays claimable from the contract |
| Wrapped XMR / ZEC (simulator) | **k of n named signers** | A quorum can take the reserve |
| Your first transaction on DERO | Whoever sells you DERO for its fee | Every DERO fee is paid in DERO, so someone arriving with only XMR or ZEC needs a little DERO first — today, from an exchange |
| An NFA staying what you bought | Its creator, and its owner while `ownerCanUpdate` is set | They can rewrite its file, signature, cover, icon and tag URLs (`nfa.bas`, `Update`); its name, hashes and collection cannot change |
| The registry's settings | Nobody | `factory.bas` has no owner and no setter |
| The application | On mainnet, nobody: the INDEX has no owner. On a development build, whoever holds its INDEX key | A republished development build could show anything |

---

## 6. Fees

A protocol fee is charged in the asset traded; the network fee is DERO and separate, and every screen states the
two apart.

| Fee | Charged in | Goes to |
|---|---|---|
| Trading fee (pool, launch) | the market's quote | the liquidity providers, minus the protocol share |
| Protocol share | the market's quote (and the asset leg) | accrues in the market; anyone may claim it, and it pays only the treasury |
| NFA sale | the NFA's quote | the creator's royalty and the owner's remainder at once; the artificer, charity and protocol shares wait until anyone calls `PayOwed` |
| Founders Pass mint | DERO | the treasury |
| Founders Pass resale | DERO | a 2.5% market fee, deposited by the contract for the registered passes (to the seller while none is registered) |
| Registration, chat | DERO | the treasury |
| XMR ↔ DERO and ZEC ↔ DERO swaps | **none today** | see below |
| Network fee | **always DERO** | miners |

**The Founders Pass.** Twenty passes (four reserved), 1 DERO each, with a share of 5,000 bps fixed at install. The
treasury's bot claims each market's protocol share and deposits the pass share of it into `founders.bas` **in the
asset the fee was taken in** — DERO, or the DERO token the market is quoted in — never converted. Each registered
pass then claims its part of each asset from the contract. A deposit is shared by the passes registered when it is
made. While no pass is registered the bot deposits nothing and holds those fees until one registers. The bot makes
every deposit and no contract forces it. Registration, chat and NFA fees are not shared.

**The swap fee, decided and not built.** The operator's ruling is 0.5% of each XMR or ZEC swap, taken in XMR or ZEC:
the party locking XMR or ZEC adds an output to the treasury's address in the same transaction, and the other side's
companion refuses to finish without it (Monero checks it with the transaction key, Zcash sees it as a second output).
That is enforced by the companion software, not by a contract, so a modified client can leave it out. The pass share
of it is to be paid by the treasury's bot in XMR and ZEC, directly on those chains, to the payout address each holder
stores with `SetXmr` or `SetZec`; a stored address is public on DERO, so a subaddress is the one to store. **Neither
the fee in `xmrswap`/`zecswap` nor the bot's XMR and ZEC payments exists yet**, so no swap fee is taken and none is
paid.

---

## 7. Repository layout

```
app/            the application: 27 JS files (config.js generated), 4 stylesheets, index.html, templates/
contracts/      12 DVM-BASIC contracts — the whole on-chain surface — and their design notes in README.md
reference/      Go mirrors of the contract arithmetic (canon, pool, launch, uint128) the app is checked against
tests/          51 Go test files run against derohe's own DVM, and tests/js for the browser arithmetic
tools/          the companions, the treasury bot, the simulator drivers, deployment and verification, the UI pass
docs/           the specs, the mainnet list and gate, the audit, and docs/history/HISTORY.md with the evidence
deploy/         per-network records, pinned binaries and vendor trees, the deployment runbook
research/       the Monero proof, the regtest and stage-1 run logs, and the design research
vendor/derohe/  a local copy of DERO's own node, not in git — the tests and several tools build against it
```

## 8. Build and run

Everything here runs against a local DERO simulator.

```bash
# once: DERO's own source at the pinned commit, where the tests and several tools build against it
git -c core.longpaths=true clone https://github.com/deroproject/derohe vendor/derohe
git -C vendor/derohe checkout e9df1205b6603c62f0651d0e18e5e77a2584b15e   # deploy/vendor.lock
cd tools && go run ./verifyvendor -root ..             # checks it, and every other pinned tree, against deploy/vendor.lock

# the app, the contracts, and something to look at
cd tools && go run ./serve -dir ../app -addr 127.0.0.1:8082
cd tools && go run ./simdeploy -network simulator      # installs the contracts, writes app/config.js
cd tools && go run ./simwrapmkt                        # seeds vaults, pools and launches
```

The simulator daemon and its wallets come from derohe itself; `deploy/README.md` has the exact invocation and the
port map (daemon 20000, wallets 30000–30021). The application then loads at `http://127.0.0.1:8082/index.html`. To
sign anything, run a DERO wallet with XSWD enabled on port 44326 and press Connect.

The regtest and testnet harnesses for the swaps are in `tools/regtest/`; `docs/MAINNET_TEST_PLAN.md` is the staged
plan they follow.

## 9. Tests

```bash
tools/check.sh                          # everything: dvm gofmt vet test fuzz js genconfig hygiene verifyvendor
tools/check.sh --only=dvm               # one step: every contract, against derohe's own DVM
tools/check.sh --ui                     # also both UI passes: needs PW_DIR, the simulator and tools/serve on :8082
```

Each step prints PASS or FAIL with its log under `.sim/check/`. The contract suite and the UI pass take repository
locks (`tools/withlock.sh`), because the suite rebuilds a directory from the tree and the UI pass owns a single
browser profile. The UI pass writes PNGs — read them, because an exit code is not evidence that a screen says what
it should. `genconfig` enforces the TELA size cap on every file, and `hygiene` fails on a machine-specific path.

## 10. Deploying

`tools/simdeploy` installs the contracts and writes `app/config.js` from `deploy/networks.json`; `genconfig`
rebuilds that config and the bundle; `telainstall` publishes the bundle as TELA documents; `verifydeploy` reads the
result back from the chain and checks the hashes, parameters and roles. `deploy/README.md` is the runbook for the
simulator and testnet.

A mainnet deployment is the operator's, by hand, from `docs/MAINNET_DEPLOYMENT.md`: what is deployed and with which
parameters (the federation is not), the sequence, and how anyone checks the final state. Before it, know that:

- the Founders Pass image is fixed at install, with no setter, so it must be supplied then;
- the Monero proof is unaudited and the contracts have had no external review (§1);
- no swap fee is collected yet, and the bot pays no XMR or ZEC (§6);
- the runbook's smallest amounts are the ones to start with.

## 11. House rules

No tool or agent in this repository touches mainnet on its own. Never a private key in the browser. Never a
transaction proxied through a server. Never the words trustless, atomic or non-custodial unless the trust
assumptions justify them — and every trusted party named where a user will read it. Fail closed: an amount that
cannot be read is shown as unreadable, never as zero, and a button whose precondition cannot be checked is not
offered. No machine-specific path in any file. No new dependency without a written answer to what it provides that
a smaller primitive cannot. `app/notice.js` is the plainest statement of what DMZ is; nothing else may contradict it.

And the one that shapes the contracts more than any other: **liveness by construction.** The operator revokes
their own access after deployment, so any state that needs a human to leave it is a defect, and is treated as one.
