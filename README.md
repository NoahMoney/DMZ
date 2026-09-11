# DMZ

**A Monero and Zcash exchange.** Its markets, its assets and its escrows are DERO smart contracts, and the
application that drives them is a set of static files served from the DERO chain itself. DERO is the execution
and hosting layer — contracts, gas, delivery. XMR and ZEC are what people come to trade.

There is no server. There is no database, no backend worker, no API and no account. Nothing in the system holds
a user's coins on their behalf except the one component that says so in those words: the federation vault, which
is custody by named third parties and is never called trustless anywhere in this repository or in the app.

---

## 1. What is actually built, and what it can do today

Read this table before anything else. "Runs" means it has been driven end to end and the transaction ids are in
`docs/history/HISTORY.md`. Nothing here has touched a real Monero, Zcash or DERO mainnet.

| Capability | State | What that means |
|---|---|---|
| Token markets (AMM and bonding curve) | **Runs on the simulator** | `pool.bas`, `launch.bas`, priced in DERO or in a wrapped asset |
| Assets (NFA, ART-NFA-MS1) | **Runs on the simulator** | `nfa.bas`: one contract per asset, its own marketplace, priced in any quote |
| Founders Pass | **Runs on the simulator** | `founders.bas`: mint, register, rewards, its own marketplace — **DERO fees only**, see §6 |
| DERO ↔ XMR atomic swap | **Runs against a fake Monero wallet** | `htlc.bas` + `tools/xmrswap`; all three exits driven on the simulator; **no real Monero network, ever** |
| Transparent-Zcash swap market | **Contracts run; the Zcash side is partial** | `swap.bas` + `board.bas` run; `tools/zecswap` builds and signs but has no node client, no watcher and no broadcast |
| Wrapped XMR / ZEC (the federation) | **Contract complete; there is no foreign backend** | `vault.bas` is fully tested; the only backend that exists is a JSON file, see §5 |
| Chat, profiles, follows | **Runs on the simulator** | `chat.bas`, `social.bas` |

The two things that would have to be true for a mainnet launch and are not:

1. **No signer can custody a real coin.** `tools/vaultsigner` has one backend, `sim`, which is a JSON file on
   the signer's own disk: a deposit is a line in it and a payout is an append. `monero`, `zcash` and `solana`
   are names in a switch that exit. `-backend` has no default and `sim` is refused on any network but the
   simulator, so this cannot be reached by omission — but until a real backend exists and has settled a deposit
   and a withdrawal, a federation vault holds nothing.
2. **The Zcash and Monero legs have never met their own chains.** The Monero companion has only ever run against
   an in-process fake wallet, and the Zcash companion has never opened a socket at all.

`docs/audit/FINDINGS_2026-09-10.md` is the full adversarial audit: 125 findings, 94 reproduced with the commands
recorded. `docs/BACKLOG.md` is everything known to be undone, numbered, with the decisions that belong to the
operator collected at the end.

---

## 2. How it is put together

```
    a person's browser                     the DERO chain                    that person's own machines
 ┌──────────────────────┐            ┌───────────────────────┐          ┌──────────────────────────┐
 │  app/  (static)      │  reads     │  factory  registry    │          │  xmrswap  (Monero side)  │
 │  vanilla JS, no      │──────────► │  pool · launch        │          │  zecswap  (Zcash side)   │
 │  framework, no       │            │  nfa · nft            │          │  vaultsigner (a signer)  │
 │  dependency, no      │  signs     │  founders             │◄─────────│  treasurybot             │
 │  key, no storage of  │──────────► │  vault (federation)   │  calls   │                          │
 │  anything secret     │  via XSWD  │  swap · board · htlc  │          │  each holds its own keys │
 │                      │  wallet    │  chat · social        │          │  and talks to its own    │
 └──────────────────────┘            └───────────────────────┘          │  nodes; none is hosted   │
                                                                        └──────────────────────────┘
```

**The application** is 23 JavaScript files and three stylesheets under `app/`, with no framework, no bundler and
no dependency of any kind — the page loads zero external scripts, and its Content-Security-Policy is
`default-src 'none'` with `script-src 'self'`, so it could not load one. It is delivered as TELA documents,
which caps each file at 18,432 bytes after gzip and
base64; `feed.js` is the largest at 17,608. It reads a DERO node the user names in Settings, and it signs
nothing itself: every write is a contract call handed to the user's own wallet over XSWD, on `localhost`. It
never sees a key, never proxies a transaction, and stores nothing secret in the browser.

**The contracts** are 12 DVM-BASIC files under `contracts/`. DVM-BASIC has uint64 arithmetic and no floating
point, no cross-contract calls, and a hard 20,000-unit ceiling on install storage gas. `reference/canon.go`
strips comments at install, so the repository keeps the prose and the chain gets the code; the hash the
application verifies is the hash of what executes. A contract cannot be changed once installed — every fix is a
new contract at a new id.

**The companions** are Go programs under `tools/`, each run by the person it belongs to, on their own machine,
against their own nodes. None of them is a service and none is hosted. They depend on three pinned modules
(`filippo.io/edwards25519`, `golang.org/x/crypto`, and decred's `secp256k1` for the Zcash signatures) and the
standard library.

---

## 3. Ring size 2, and what that costs

Every contract entrypoint that pays or authorises somebody reads `SIGNER()`, and DERO only reveals the signer at
ring size 2. Above it, `SIGNER()` is 33 zero bytes and every such entrypoint fails closed. So almost every call
this application makes is a ring-2 call, and **the address that made it is computable by anyone**.

That is the honest privacy position, and the application says so on the intro screen and in Settings rather than
claiming otherwise. Balances on DERO are encrypted and stay encrypted. But a pool records its reserves at the end
of every block they move in, so the size and direction of a trade are public whether the trader chose to publish
it or not, and the ring-2 signer can be joined to it. Trading here is pseudonymous, not private.

The exceptions are the `...To` wrappers on `pool.bas` and `launch.bas`, which pay a named address instead of the
signer and can therefore run at ring 16, hiding the payer among sixteen accounts while the receiving address is
public.

---

## 4. The four ways value moves

### XMR → DERO-side asset → XMR, without a custodian

The trustless path, and the one the operator's ruling of 2026-09-08 made the active track. Bob holds DERO and
locks it in `htlc.bas` under three hashes; Alice holds XMR and locks it into a joint Monero address whose two
key shares are tied to those hashes by a client-side Bulletproofs proof of `SHA256(s) = H ∧ s·G = S`. There are
three exits and each leaves both parties whole or leaves the party who walked away worse off:

| Exit | Who ends with what |
|---|---|
| **Claim** | Alice takes the DERO with `s_a` and Bob's gate key; Bob reads `s_a` off the chain and sweeps the XMR |
| **Refund** | Bob takes his DERO back after `t1` and reveals `s_b`, which lets Alice recover her XMR |
| **Punish** | Alice takes Bob's DERO after `t2`; her XMR stays under the joint key unless Bob ever reveals `s_b` |

No third party is in this path at any point. What each side must trust is its own node's answer about its own
chain, and its own companion being alive at two specific windows. `docs/XMR_SWAP.md` states all thirteen
invariants and twenty-four adversarial cases; `tools/xmrswap` asserts every invariant by name in its tests, and
kills both companions at random points in `TestCrashFuzz` to prove the state machines resume.

**Not yet true of it:** the Monero side has only ever spoken to an in-process fake. The rendezvous between the
two parties is a shared directory, so today it works for two people who already know each other and for nobody
else.

### ZEC → DERO-side asset → ZEC, without a custodian

Transparent Zcash is Bitcoin script, so this is a classic two-chain hash-timelock and it needs no new
cryptography. The maker posts an offer on `board.bas` carrying the hash of a secret only they hold; the taker
accepts, naming the key that will refund them and the height at which it may; the maker locks in `swap.bas`; the
taker funds a pay-to-script-hash output on Zcash; the maker claims it with the secret, publishing the secret; the
taker claims the DERO with it.

The maker holds the secret and locks first **on purpose**: whoever locks first carries the free option, and the
Zcash holder is this market's customer, not its liquidity provider.

**What this reveals, and it is not small.** Zcash's transparent side is fully public: the amount, both
transparent addresses, and the hash — which also appears on DERO, joining the two chains and, through the ring-2
calls, both DERO addresses. If the ZEC came from the shielded pool, the unshielding is visible too. Shielded
Zcash cannot be used here at all. `docs/ZEC_SWAP.md` §1 states this before it states anything else, and the
application will say it above the fields rather than in a footnote.

**Not yet true of it:** `tools/zecswap` builds the script, derives its address, signs with secp256k1 and
computes ZIP 244 digests that reproduce Zcash's own published test vectors byte for byte — but it has no node
client, no watcher and no broadcast, so no Zcash transaction has ever been sent.

### XMR or ZEC → wrapped units → back, with a custodian

The federation. Coins go to an address that k of n bonded signers control; those signers vote, and the vault
contract mints an equal number of DERO-side units. Burning the units records a withdrawal that the signers pay
out abroad. Wrapped units are ordinary DERO assets, so they can be the quote of a pool, a launch or an NFA —
which is how a Monero holder trades against tokens at DERO's speed instead of a swap's.

**This is custody and the whole system says so.** A colluding quorum can take the entire reserve, invisibly to
DERO. Bonds are meant to make that irrational, and at the parameters shipped for the simulator they do not:
slashed bonds go to the treasury rather than to the people who lost coins, and slashing a colluding quorum needs
n ≥ 2k, which is a deployment rule enforced nowhere in the contract.

What the contract *does* guarantee is that no state needs a human to leave it. A halt is a lease that expires
rather than a switch someone must flip back; `Reclaim` is permissionless and pays only the burner, so griefing
it does the burner a favour; after `Seal` the installer has no powers at all, and the deployer key can be
destroyed. Two audit findings still break that promise and are recorded as open: a single signer's payment mark
permanently disables `Reclaim` for that withdrawal, and a burn naming an address the chain has never seen can
never be returned by any route.

**Not yet true of it:** no foreign backend exists, so no vault has ever held a coin.

### DERO → tokens, NFAs and the rest

`pool.bas` is a constant-product AMM owned by its liquidity providers. `launch.bas` is a bonding curve that
graduates into a locked AMM, with optional creator tax, buybacks or staking. `nfa.bas` is one contract per asset
following ART-NFA-MS1 exactly — the same headers Engram, Gnomon and dReams index — extended with a `quote`
parameter, so a DERO-priced DMZ asset is a standard asset to every other DERO tool and a wXMR-priced one is an
extension they will not settle. `factory.bas` is a paid registry: anyone can register any contract id, and the
application decides what is verified by hashing the code against the templates it ships with.

---

## 5. What you are trusting, per thing

| For | You trust | If it is wrong |
|---|---|---|
| Every screen's numbers | The DERO node you named in Settings | You see a false price or balance; you cannot lose funds by it alone |
| Every signature | Your own wallet, over XSWD on localhost | The application never holds a key and cannot sign |
| A market is what it claims | The template hash the app checks, and the treasury it names | A contract whose code does not match ships as unverified and is refused |
| Wrapped XMR / ZEC | **k of n named signers** | A quorum can take the reserve; you hold units backed by nothing |
| An atomic swap | Your own node's answer, and your own companion running at your window | Miss your window and the other side's exit opens; a lock past its last deadline that its taker never punishes is frozen for ever, until `htlc.bas` is replaced (LIV-07). Nobody else can take your coins |
| Your first transaction on DERO | Whoever sells you DERO for its fee | DERO takes every fee from a DERO payload, so a holder who arrives with only XMR or ZEC must get a little DERO first — today, from an exchange |
| An NFA staying what you bought | Its creator, for ever — and its owner too while `ownerCanUpdate` is set | They can rewrite its file, file-signature, cover, icon and tag URLs at any time (`contracts/nfa.bas`, `Update`); its name, its hashes and its collection cannot change |
| The registry's settings | The factory's owner key | `SetTreasury` and `SetRegFee` stay in its hands for ever: the key cannot be renounced, because a transfer completes only when the new owner signs (SEC-K-02) |
| The application itself | Whoever publishes the TELA index | A republished application could lie about anything on screen |

The last row is the one with no cryptographic answer today: the publisher key can republish for ever, and the
only version indicator on screen is a constant nothing checks against the chain. It is an open finding.

---

## 6. Fees

Fees are charged in the asset traded, and DERO gas is a separate thing from a protocol fee. A trade in a wXMR
market pays its fee in wXMR; the network fee for the transaction is DERO, and every screen states the two apart.

| Fee | Charged in | Goes to |
|---|---|---|
| Trading fee (pool, launch) | the market's quote | the pool's liquidity providers, minus the protocol share |
| Protocol share | the market's quote | the treasury |
| NFA sale | the NFA's quote | creator royalty, protocol share, optional charity, then the owner |
| Registration, chat | DERO | the treasury |
| Vault withdrawal | wrapped units | held in the contract, claimable by the treasury |
| Network fee | **always DERO** | miners |

**The gap, stated plainly because the application states it too:** the Founders Pass distributes DERO and only
DERO. A protocol fee earned in wXMR or wZEC reaches the treasury as wrapped units and reaches no pass holder at
all. Getting fees to holders in the asset they were earned in needs per-asset accounting in `founders.bas`,
which is a new contract; it is `BL-90` in the backlog and it is the largest single piece of unbuilt economics.

---

## 7. Repository layout

```
app/            the application: 23 JS files, 3 stylesheets, index.html, templates/, config.js (generated)
contracts/      12 DVM-BASIC contracts — the whole on-chain surface
reference/      Go mirrors of the contract arithmetic (canon, pool, launch, uint128) the app is checked against
tests/          49 Go test files run against derohe's own DVM, plus tests/js for the browser arithmetic
tools/          27 Go programs: the companions, the simulator drivers, deployment, the UI pass
docs/           the specs, the backlog, the audit, and docs/history/HISTORY.md — every change with its evidence
deploy/         per-network records, vault parameters, the deployment runbook
research/       the proofs of concept, the mechanism review, the wrapper design and runbooks
vendor/derohe/  a local copy of DERO's own node, not in git — the tests and several tools build against it
```

## 8. Build and run

Everything runs against a local DERO simulator. Nothing here should ever be pointed at mainnet.

```bash
# once: DERO's own source, where the tests and several tools build against it
git -c core.longpaths=true clone --depth 1 https://github.com/deroproject/derohe vendor/derohe
cd tools && go run ./verifyvendor -root ..             # checks it against deploy/vendor.lock

# the app, the contracts, and something to look at
cd tools && go run ./serve -dir ../app -addr 127.0.0.1:8082
cd tools && go run ./simdeploy -network simulator      # installs the contracts, writes app/config.js
cd tools && go run ./simwrapmkt                        # seeds vaults, pools and launches
```

The simulator daemon and its wallets come from derohe itself; `deploy/README.md` has the exact invocation and
the port map this repository assumes (daemon 20000, wallets 30000–30021).

The application then loads at `http://127.0.0.1:8082/index.html`. To sign anything, run a DERO wallet with XSWD
enabled on port 44326 and press Connect.

## 9. Tests

```bash
tools/withlock.sh dvm bash tests/run.sh                # every contract, against derohe's own DVM
cd tools && go build ./xmrswap/... ./zecswap/... ./simhtlc/ ./simswap/ ./vaultsigner/ ./simdeploy/ ./genconfig/ ./simnfa/
cd tools && go vet   (the same list)  &&  go test ./vaultsigner ./xmrswap ./zecswap
node --test tests/js                                   # the browser's arithmetic and sanitisers
cd tools && go run ./genconfig -dist -network simulator  # rebuilds the bundle and enforces the TELA size cap
tools/withlock.sh ui env PW_DIR=<playwright> node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-1
```

`tools/withlock.sh` exists because the contract suite rebuilds a directory from the tree and the UI pass owns a
single browser profile: two at once corrupt each other. The UI pass writes PNGs — read them, because an exit
code is not evidence that a screen says what it should.

Live drivers, each of which spends simulator DERO: `simvault` and `simwrapmkt` for the federation, `simhtlc` for
the Monero swap's three exits, `simswap` for the Zcash market's DERO leg, `simnfa` for an asset, `simlaunch`,
`simchat`, `simtraffic`, `simsmoke`.

## 10. Deploying

`tools/simdeploy` installs the contracts and writes `app/config.js` from `deploy/networks.json`; `genconfig`
rebuilds that config and the distributable bundle; `telainstall` publishes the bundle as TELA documents. The
runbook and its go/no-go gates are in `deploy/README.md`.

Two gates in that runbook are unmet and both are named in the backlog: `deploy/federation.md`, which must name
who runs the signers and their bonds before any vault is installed, does not exist; and the factory's owner key
cannot be renounced, because `TransferOwnership` completes only when the new owner signs, so a burn address can
never claim it. The intended revocation ceremony cannot be performed on the factory as written.

## 11. House rules

Never mainnet from this repository. Never a private key in the browser. Never a transaction proxied through a
server. Never the words trustless, atomic or non-custodial unless the trust assumptions justify them — and every
trusted party named where a user will read it. Fail closed: an amount that cannot be read is shown as unreadable,
never as zero, and a button whose precondition cannot be checked is not offered. No machine-specific path in any
file. No new dependency without a written answer to what it provides that a smaller primitive cannot.

And the one that shapes the contracts more than any other: **liveness by construction.** The operator revokes
their own access after deployment, so any state that needs a human to leave it is a defect, and is treated as
one.
