# The wrapper: operator runbooks

Signer daemon operation, cap and bonds, halt and resume, reserve-shortfall incident.


---

# README

## Wrapper runbooks

Operator procedures for `contracts/vault.bas` v3 and `tools/vaultsigner`. Each runbook names the exact
entrypoint or flag; nothing here moves funds on mainnet without `-yes`, and every mainnet action is the
operator's, never an agent's. Roles: **signer operator** (runs one `vaultsigner` with its own wallet and foreign
backend), **owner** (installs the vault, adds signers, seals; has no power after `Seal`), **anyone** (can read
every state key and call `Register`/`Burn`).

Read this before the rest: after `Seal` the vault has no operator, and nothing in it needs one. Every limit
either empties by itself (the rolling mint window) or expires into a payment (the withdrawal delay, longer for a
large withdrawal). A halt is a condition the daemons compute from public state and vote in both directions, not a
decision anyone takes or lifts. Where a procedure below reads like a lever, it is a k-of-n vote; there is no
manual override anywhere in the contract and no override flag in the daemon.

| runbook | when |
|---|---|
| [signer daemon](#signer-daemon) | starting, stopping, moving or recovering a signer |
| [halt and resume](#halt-and-resume) | what a halt is, what it blocks, and how it clears |
| [cap and bonds](#cap-and-bonds) | raising or lowering the cap, bonding, slashing, replacing a signer |
| [incident reserve shortfall](#incident-reserve-shortfall) | the foreign reserve is below the wrapped supply |
| [Zcash key ceremony and configuration](#zcash-key-ceremony-and-configuration) | a Zcash vault's seeds, descriptor, published view key, reserve audit and backend configuration |
| [`docs/SIGNER_RUNBOOK.md`](../../docs/SIGNER_RUNBOOK.md) | one signer's host, keys, ceremony, service, coordination channel, rotation and key-compromise response |

State keys worth knowing (all readable with `DERO.GetSC`): `sup` wrapped supply, `cap`, `capratio` the
cap-to-bond ratio fixed at install, `bondtot`, `na` active signers, `n` signer slots ever used, `k`, `halted`,
`he` halt epoch, `se` slash epoch, `ce` cap epoch, `te` tighten epoch, `ve` view-key epoch, `winmax`, `wdelay`,
`bigbps`, `bigdelay`, `viewkey`, `un` user indexes, `wn` withdrawals, `w_<j>_{to,v,f,by,h,rel,st,ftx}`,
`mw_<bucket>` units minted in that 2000-block bucket, `mint_<id>` deposit ids that are closed (1 minted,
2 refunded), `fu_<ftx>` foreign txids already used to settle a withdrawal, `feepool`.


---

# signer daemon

## Signer daemon

One `vaultsigner` per signer wallet. It attests deposit addresses, votes mints after the confirmation and
finality policy, pays withdrawals on the foreign chain through durable intents, votes releases, returns
withdrawals the foreign chain cannot pay (`Veto`), and votes to halt while the foreign reserve is below what the
vault owes — voting to resume by itself once it is not.

### Start

Three backends run. `zcash` holds the reserve in transparent P2SH k-of-n multisig addresses on Zcash
([Zcash key ceremony and configuration](#zcash-key-ceremony-and-configuration)); it has settled one deposit and one
withdrawal on zebrad regtest with three live daemons (`research/regtest/vault-zcash-e2e.log`) and nowhere else yet,
so a testnet pilot comes before anything that holds value. `monero` uses `monero-wallet-rpc` for deposits, reserve and
single-wallet releases; a multisig txset is refused until a Monero signing coordinator exists. `sim` is a JSON file on
the signer's own disk: a "deposit" is a line in it and a "payout" is an append; it is accepted only with `-network
simulator`. `-backend` has no default so that no one can reach either by omission. `solana` is not implemented.

A Zcash signer, on the network its vault lives on (never mainnet without the operator's go and `-yes`):

```
vaultsigner -network testnet -backend zcash -zcash-config <zcash.json> -coord <coord.json> \
            -daemon 127.0.0.1:<rpc> -wallet 127.0.0.1:<wallet rpc> -vault <scid> [-state <file>] [-interval 20s]
```

A Monero signer (the `monero.json` names `network: "stagenet"`; `walletRpc`, the vault's primary `address`,
`finalityDepth` at least 10, and optionally `retries`/`retryDelayMs`):

```
vaultsigner -network testnet -backend monero -monero-config <monero.json> -coord <coord.json> \
            -daemon 127.0.0.1:<rpc> -wallet 127.0.0.1:<wallet rpc> -vault <scid> [-state <file>] [-interval 20s]
```

Set `VEIL_MONERO_RPC_LOGIN=user:password` when monero-wallet-rpc was started with `--rpc-login`. A wallet that is
in a Monero multisig answers a release with a `multisig_txset`; this build refuses it until a Monero signing
coordinator exists.

The simulator command line:

```
vaultsigner -network simulator -backend sim -daemon 127.0.0.1:<rpc> -wallet 127.0.0.1:<wallet rpc> \
            -vault <scid> -sim <foreign json> [-confs N] [-state <file>] [-interval 20s] \
            [-verify-tries 20] [-once]
```

- The daemon speaks vault v3 and refuses to start against a vault whose `v` is anything else.
- `-backend sim` with `-network testnet` or `-network mainnet` exits: the file backend is a simulation and may
  not stand behind units anyone can trade.
- `-confs 0` takes the network table's confirmations for the backend (sim: 2). Never lower it on mainnet.
- `-state` defaults to `vaultsigner-<first 8 characters of the vault scid>-<last 12 of the signer address>.json`
  in the working directory. One file per (vault, signer) pair; never share it between two daemons. The daemon
  holds `<state>.lock` while it runs, so a second daemon on the same file will not start.
- `-verify-tries` bounds how many passes a recorded foreign payment may stay invisible before the backend is
  asked to pay that withdrawal again. It exists because not voting is right but never voting is a freeze, and
  there is nobody to unfreeze it.
- `-yes` is required on mainnet: the daemon pays real withdrawals.
- The wallet RPC must be the signer's own wallet, on this device, and must never be a mining address.

### What a pass does, in order

1. Reads the vault. Sealed? If not, waits. Then the reserve invariant. `inflight` is every burned withdrawal the
   contract has not closed, and what the federation must still be able to show is `sup - inflight`, not `sup`.
   Reserve below that? Votes `Halt(1)` once per direction per 2000-block bucket if the vault is not already
   halted, runs the veto pass, and stops there. Halted with the reserve covering it again? Votes `Halt(0)`, runs
   the veto pass, and stops. The veto pass runs in both cases: returning funds is the one thing a halted vault
   must still do.
2. Attests deposit addresses for new indexes (`Attest(j, addr)`); a refused vote means the quorum was already
   reached.
3. Votes `Mint(j, txid, vout, amount)` for deposits at or beyond the confirmation and finality policy whose id is
   not yet closed under `mint_<id>`. The id is `HEX(txid):vout` and the contract builds it from the raw 32-byte
   txid, so a signer cannot name it. A deposit over the cap or over the rolling window is a wait, not a refusal:
   the daemon retries every pass, and headroom from a later burn or from the window sliding credits it with
   nobody having to act.
4. Runs the veto pass. A withdrawal whose destination this chain cannot pay gets a `Veto(j)` vote, which returns
   the units to the burner. It never vetoes one this daemon has already begun paying: the units would go back
   while the coins had already gone out.
5. For each withdrawal `j` with `w_<j>_st == 0` whose burn height is at or below DERO's `stableheight` and whose
   recorded release height `w_<j>_rel` the tip has reached: writes an intent (paying), calls the backend's
   `Release` (idempotent per `j`), records the foreign txid (paid), re-reads the foreign chain to confirm that
   txid really pays that amount to that destination, then votes `Released(j, ftx)` (voted). A burn above the
   stable height, or below its release height, waits with a log line.

A deposit below `mindep` is never minted; the on-chain way to close it is `Refunded(txid, vout, ftx)`, k votes,
which records the return abroad and marks the id refunded so it can never later be minted. The daemon does not
cast that vote — it logs the deposit and the signers vote it.

### Stop and move

Stop the process any time; every step is durable before the next one starts. To move a signer, copy the state
file and the wallet file together; start on the new machine with the same `-state`. Never run the same signer
in two places: the second daemon would repeat votes (refused by the contract, harmless) and, with a backend that
is not idempotent, could pay twice.

### Crash recovery

On start the daemon loads its state strictly: a corrupt file, or one belonging to a different vault, stops it
with a message rather than starting empty. Every intent in `paying` is retried (the backend must look the payment
up by `j` before paying); every intent in `paid` is verified against the foreign chain and only then voted;
`voted` intents are skipped. A payment that stays invisible for `-verify-tries` passes is treated as one that did
not happen: the intent goes back to `paying` and the backend is asked to pay `j` again, which is safe only
because `Release` is idempotent per `j`. Edit the state file by hand only with the daemon stopped, and only after
reading both chains.

### Backups

The state file holds no keys, only votes cast and intents; back it up after every pass if the backend is not
idempotent. The wallet file and the foreign key shares are the real secrets: offline copies, never in the
repository, never in `.testnet/` or `.mainnet/` on a shared machine.

### Reading the log

```
withdrawal 2 waits for DERO finality (burned at 5805, stable height 5798)
withdrawal 3 is not releasable yet (tip 6100, releasable at 6300)
RESERVE SHORTFALL: foreign reserve 46000 < supply 71000 less 5000 in flight; voting Halt(1)
14:22:07 Mint [{j U 0} {txid H 9f3c…} {vout U 0} {amount U 25000}] tx d2ade4c4… height 5810
14:22:09 vault: indexes 1, supply 92000 / cap 100000, withdrawals 2, halted 0, reserve 97000 (in flight 5000)
```

The last line is the pass summary; compare it across signers to spot one that is behind. Read `reserve` against
the supply **less** the in-flight total, never against the supply alone: during any ordinary withdrawal a
perfectly correct vault is short of its own supply by exactly what is in flight. A signer that compared the
reserve to `sup` alone would halt on the first withdrawal, and the halt could never clear, because `Released` is
refused while halted so `sup` could never fall. That is not hypothetical — it is the bug found and fixed on
2026-09-07.

A large withdrawal earns its extra delay at burn time: `Burn` records `w_<j>_rel` as the height from which the
withdrawal may be released (`wdelay`, plus `bigdelay` when the burn exceeds `bigbps` of the supply), and
`Released` refuses before it. The daemon has no size flag and no override — it reads `w_<j>_rel` and waits, and
doing nothing pays the user. To stop a withdrawal the signers vote `Veto(j)`, which returns the units to the
burner, or `Halt(1)`, which blocks `Released` while the halt lasts ([halt and resume](#halt-and-resume)). The
daemon votes `Veto` on its own only when the destination is one the foreign chain cannot pay.


---

# cap and bonds

## Cap, bonds and slashing

### Cap

`cap` is the most wrapped supply the vault will mint. It is enforced at the k-th mint vote and bounded by the
bonds: the contract refuses whenever `cap / capratio > bondtot`, checked at `Seal`, at every `SetCap` and at
every mint. The on-chain test divides, so it is permissive by at most `capratio - 1` base units. `capratio` is
fixed at install and is the operator's written-down price assumption relating foreign base units to DERO atomic
units — there is no oracle, and nobody can change it after install. `SetCap(cap)` is a k-of-n vote keyed by the
cap epoch `ce`; k agreeing votes set the cap and bump the epoch, which voids older votes for other values.

To raise the cap: first raise the bonds (below), then k signers vote `SetCap(<new>)`. To lower it: k votes; a
cap below the current `sup` is allowed and stops further mints until burns bring `sup` under it.

### Limits set once, before Seal

`SetLimits(winmax, wdelay)` (owner, before `Seal`; refused afterwards by everyone, and only `Tighten(winmax,
wdelay)` — k votes, safe direction only — can move them later): the most that will be minted over any two
consecutive 2000-block buckets (a sliding window), and the number of blocks after a burn before any `Released`
vote counts. `winmax = 0` disables the window; `wdelay = 0` means no delay. The contract refuses `wdelay > 20000`
and a non-zero `winmax` below `mindep`. Choose them so that a colluding quorum's fastest possible theft (one
bucket of `winmax`) is less than the bonds, and so that `wdelay` covers the time an honest signer needs to notice
a bad burn and vote `Veto` or `Halt`. Note that a single deposit larger than `winmax` can never be minted
whatever the window state, so set `winmax` well above the largest deposit the vault expects to see.

There is deliberately no per-deposit maximum. A deposit above one could never be minted at all, and nobody is
guaranteed to exist who could send it back, so the limit would be a permanent freeze. Size is handled instead by
`bigbps`/`bigdelay`, both fixed at install: a burn above `bigbps` of the supply has `bigdelay` blocks added to
its release height at burn time. It is delayed, never refused.

### Bonds

`Bond()` with DERO from a signer wallet adds to that signer's bond, and to `bondtot` if that address is an active
signer. Every signer must hold at least `minbond` before `Seal`. Bonds are the federation's skin in the game: a
colluding quorum can take the foreign reserve, but the cap keeps the reserve at most `capratio` times what the
bonds are worth in DERO.

`Unbond()` returns a bond. **Before `Seal` anyone may call it**, including a signer the owner has already added:
if one signer address is mistyped, `Seal` can never succeed — nobody holds that key, so it can never bond — and
without this the honest signers who had already bonded would be locked out for good. Check `bondtot` and every
`bond_` before sealing, and if the set is wrong, unbond and install again.

**After `Seal` no signer, past or present, can unbond**: `Unbond` refuses anyone with an `isg_` key, and `Slash`
zeroes that key rather than deleting it. An active signer's bond leaves the contract only through `Slash`, and it
goes to the treasury, not back to the signer. Do not bond funds you may need.

### Slash

`Slash(target)` is a k-of-n vote that removes a signer, takes its bond out of `bondtot`, lowers `na`, and opens
a new slash epoch `se`, which voids every pending vote the removed signer cast (mint, attest, release, halt).
The contract refuses a slash that would leave fewer than `k` active signers, and a signer cannot slash itself.
The bond goes to the treasury.

`Slash` re-establishes the cap bound itself: if `cap / capratio` still exceeds `bondtot` once the bond is
removed, it sets `cap = bondtot × capratio` and bumps the cap epoch `ce`, so mints do not silently stop and no
vote is needed. Read `bondtot` and `cap` right after anyway. If the new `cap` is below the current `sup`, further
mints wait until burns bring `sup` under it — the same behaviour as a `SetCap` lowering.

### Replacing a signer

`AddSigner2(addr)` is a k-of-n vote that seats a signer after `Seal`. The candidate must already hold at least
`minbond` — they call `Bond()` first, and their bond is held but does not count in `bondtot` until the vote
executes. A key that was ever a signer never returns: `isg_` is checked, not deleted. Executing the vote bumps
the slash epoch `se`, so every pending vote is voided; expect the daemons to re-cast on their next pass.

Note the ceiling, and which number it is on. `AddSigner2` refuses at `na >= 20` — the **active** set — so
replacement never runs out however many rotations a vault lives through. It used to compare `n`, the monotonic
slot index that `Slash` never decrements, which made every replacement consume one of the twenty permanently: a
long-lived vault would eventually be unable to seat anyone, drift down to `na == k` as keys were lost, and then
stop for good. `AddSigner` still refuses at `n >= 20`, which is only reachable before `Seal`, where `n` and
`na` are equal by construction. Because `Seal` needs `n >= k + 1`, the largest quorum a vault can have is
`k = 19`, and the contract refuses anything larger at install.


---

# halt and resume

## Halt and resume

`Halt(want)` is a k-of-n vote. `want = 1` halts, `want = 0` resumes. Votes live in 2000-block buckets under the
current halt epoch `he`; k votes flip the state and open a new epoch. While halted: no registrations, no
attestations, no mints, no burns, no releases — `Register`, `Attest`, `Mint`, `Burn` and `Released` are the five
entrypoints a halt blocks, and the only ones that guard on `halted`. `Veto(j)` carries no such guard and still works, which is how a withdrawal that
cannot be paid is returned to its burner while the vault is halted; so do `Refunded`, `Slash`, `SetCap`,
`Tighten`, `Bond`, `Unbond`, `AddSigner2`, `SetViewKey` and `ClaimFees`. Wrapped units keep their claim.

### When a halt happens

A halt is a condition, not a decision. Every daemon votes `Halt(1)` while the foreign reserve is below what the
vault owes — `sup` less the withdrawals in flight — and votes `Halt(0)` as soon as the reserve covers it again,
so the quorum forms and dissolves with nobody to ask. A shortfall is therefore the only halt reason that holds.

A halt voted by hand for anything else — a suspected signer compromise, a foreign-chain incident, a mint the
federation cannot find on the foreign chain — will be voted back up by the other daemons on their next pass,
because they compute the halt only from the reserve against what is owed. For those causes the durable answers
are `Slash(target)` for a compromised signer (see [cap and bonds](#cap-and-bonds)) and `Veto(j)` for a withdrawal
that must not be paid. Any new halt reason must be computable from public state, or it cannot survive.

### How

Each signer operator, from their signer wallet, against the vault:

```
entrypoint Halt, want = 1        # k signers within the same 2000-block bucket
```

Confirm with `halted == 1` and a new `he`. The daemons stop attesting, minting and releasing on their own once
they read `halted`; they keep running, and they keep running the veto pass.

### While halted

These are not preconditions for resuming — the resume is not yours to hold — but the work that has to happen
anyway:

1. The cause is written down (what, when, which blocks, which signers saw it).
2. The foreign reserve is counted (until this vault has published a `viewkey` this is the signers' statement;
   publish the count anyway).
3. Every intent file is reconciled: no `paying` or `paid` intent left behind.
4. If a signer was compromised: `Slash(target)` ([cap and bonds](#cap-and-bonds)). Slashing is what removes it;
   the halt will not hold it out.

### Resume

The halt clears itself. Every honest daemon votes `Halt(0)` on its next pass once the reserve covers what is
owed, so a resume needs no human and cannot be withheld by one. There is nothing to restart: the daemons never
stopped. If you want the resume sooner, the only honest move is to make the reserve whole — `sup` cannot be
lowered while halted, because `Released` is one of the entrypoints a halt blocks.


---

# incident reserve shortfall

## Incident: the foreign reserve is below the wrapped supply

The daemons detect it (`Reserve()` from the backend against `sup` less the withdrawals in flight) and vote
`Halt(1)`; with k honest daemons the vault halts within one pass interval. Everything below is what people do
next.

1. **Confirm the numbers.** Every signer counts the foreign reserve independently (wallet balance, minus
   pending outgoing payments in `paying`/`paid` intents) and reads `sup` and the in-flight total from the vault
   (every `w_<j>` with `_st == 0`). Write the numbers down with block heights on both chains.
2. **Find the cause.** In order of likelihood: a release paid twice (a non-idempotent backend after a crash: the
   intents show it); a deposit minted for a transaction that was reorganised out on the foreign chain (compare
   `mint_<id>` ids against the foreign chain at policy depth); a foreign-side theft (a signer's share
   compromised); a mint vote that lied (compare the minted `(j, id, amount)` tuples against the foreign chain).
3. **Stop the bleeding.** If a signer is compromised, `Slash(target)` (k votes). If a backend paid twice, stop
   that daemon and fix the backend's lookup before restarting.
4. **Make the reserve whole, or say the supply exceeds it.** The federation tops up the reserve from the bonds'
   owners, or accepts that `sup` exceeds the reserve and says so publicly. `sup` cannot be lowered while halted,
   because `Released` is blocked, so publish the numbers while the shortfall stands rather than after it closes.
5. **The halt clears itself** once the reserve covers what is owed: the daemons vote `Halt(0)` on their next
   pass. Nobody signs off. Publish the count regardless.

Never mint against the shortfall, never change the cap to hide it, never describe the halt as "maintenance".


---

# zcash key ceremony and configuration

## Zcash key ceremony and configuration

The `zcash` backend holds the reserve in transparent pay-to-script-hash k-of-n multisig addresses (docs/HANDOVER.md
§5.1, §5.4). Each signer holds one BIP 32 seed; the vault is defined by the n signers' **account keys** and k. The
ceremony order and the custody rules around it are `docs/SIGNER_RUNBOOK.md` §3–§4; these are the Zcash commands.

### 1. Each signer, on its offline machine

```
zcashkey -new <seed file> -network <testnet|mainnet>
```

- The seed is 32 random bytes written as hex to a file only this account can read (0600, or a protected owner-only
  DACL on Windows). The tool refuses a path that exists and any path inside a git work tree, and prints only the
  account key, `m/44'/133'/0'` on mainnet or `m/44'/1'/0'` on a test network.
- Back the seed file up like the coordination identity file. The account key (`xpub…`/`tpub…`) is public: send it.
- Never type the seed anywhere; `zcashkey -xpub <seed file> -network …` prints the account key again when needed.

### 2. Everyone, once the vault is installed and every account key is in

```
zcashkey -descriptor -network <net> -k <k> -xpubs <key1,key2,…,keyn> -vault <scid> -indexes 3
```

Every signer runs it and reads out the `change` address and deposit addresses 0–2 on the call; all must read the
same strings. The tool refuses k ≥ n, a repeated account key, an extended private key and a key for the other
network. The `view key` line is the descriptor (or its SHA-256 when it is longer than the contract's 400 characters);
after `Seal` the signers vote it with `SetViewKey`, and from then on anyone can derive every vault address and count
the reserve:

```
zcashkey -audit -viewkey "<the vault's viewkey>" -vault <scid> -indexes <the vault's un> -zcash <node url> [-cookie <file>]
```

The reserve it prints must be at least `sup` less the open withdrawals (`w_<j>_v` with `w_<j>_st == 0`). Each signer's
daemon checks the same thing every pass and votes `Halt(1)` while it does not hold.

### 3. Each signer's backend configuration

`zcash.json`, beside the daemon's state, readable by the signer's account only (it names the seed file's path, not the
seed):

```json
{
  "network": "testnet",
  "k": 2,
  "xpubs": ["tpub…", "tpub…", "tpub…"],
  "seed": "<path to this signer's seed file>",
  "account": 0,
  "nodes": [
    {"name": "own-zebrad", "url": "http://127.0.0.1:18232/", "cookie": "<path to its cookie>"},
    {"name": "second-zebrad", "url": "http://127.0.0.1:18242/", "cookie": "<path>"}
  ],
  "ledger": "<path to this signer's release ledger>"
}
```

- Every signer of a vault must use the same `network`, `k`, `xpubs` and the defaults for `finalityDepth` (100),
  `epoch` (10), `expiryWindow` (40) and `maxInputs` (200), or their transactions differ and nothing is signed.
- Name at least two nodes the signer runs, on different machines if it can: deposits are credited only when every node
  shows them identically, and the reserve is the lowest any node shows.
- `coordinatorDir` (a shared directory) exists for the regtest pilot only; on testnet and mainnet the signatures
  travel through `-coord` (authenticated and encrypted, DESIGN §C7), and the backend refuses both at once.
- The **release ledger** records every transaction the signer has signed for each withdrawal, before the signature
  leaves the process. Back it up with the state file. If it is lost, the backend finds a payment already made by its
  lock-time marker on the chain, but a signer that lost it must not sign until its peers' transactions are visible.

### 4. Withdrawals, as the daemon sees them

A withdrawal is paid by a transaction every signer builds identically (HANDOVER §5.4). The log shows
`withdrawal j: … 1 of 2 signatures … waiting for the co-signers` until k signers have signed, then
`… assembled from 2 signatures and broadcast`; the `Released` vote follows once the payment is 100 blocks deep, which
is about two hours on mainnet. The destination receives `w_<j>_v` less the release's ZIP 317 fee. A withdrawal to a
shielded or unified address is vetoed by every signer and the burn returns to its sender.
