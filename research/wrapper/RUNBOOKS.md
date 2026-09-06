# The wrapper: operator runbooks

Signer daemon operation, cap and bonds, halt and resume, reserve-shortfall incident.


---

# README

## Wrapper runbooks

Operator procedures for `contracts/vault.bas` v2 and `tools/vaultsigner`. Each runbook names the exact
entrypoint or flag; nothing here moves funds on mainnet without `-yes`, and every mainnet action is the
operator's, never an agent's. Roles: **signer operator** (runs one `vaultsigner` with its own wallet and foreign
backend), **owner** (installs the vault, adds signers, seals; has no power after `Seal`), **anyone** (can read
every state key and call `Register`/`Burn`).

| runbook | when |
|---|---|
| [signer-daemon.md](signer-daemon.md) | starting, stopping, moving or recovering a signer |
| [halt-and-resume.md](halt-and-resume.md) | anything smells wrong; after the cause is fixed |
| [cap-and-bonds.md](cap-and-bonds.md) | raising or lowering the cap, bonding, slashing |
| [incident-reserve-shortfall.md](incident-reserve-shortfall.md) | the foreign reserve is below the wrapped supply |

State keys worth knowing (all readable with `DERO.GetSC`): `sup` wrapped supply, `cap`, `bondtot`, `na` active
signers, `k`, `halted`, `he` halt epoch, `se` slash epoch, `ce` cap epoch, `un` user indexes, `wn` withdrawals,
`w_<j>_{to,v,by,h,st,ftx}`, `md_<id>` minted deposit ids, `feepool`.


---

# signer daemon

## Signer daemon

One `vaultsigner` per signer wallet. It attests deposit addresses, votes mints after the confirmation policy,
pays withdrawals on the foreign chain through durable intents, votes releases, and asks for a halt when the
foreign reserve is below the wrapped supply.

### Start

```
vaultsigner -network <simulator|testnet|mainnet> -daemon 127.0.0.1:<rpc> -wallet 127.0.0.1:<wallet rpc> \
            -vault <scid> -backend <sim|zcash|monero|solana> [-confs N] [-state <file>] [-interval 20s] [-yes]
```

- `-confs 0` takes the network table's confirmations for the backend (sim: 2). Never lower it on mainnet.
- `-state` defaults to `vaultsigner-<last 12 chars of the signer address>.json` in the working directory. One
  file per signer identity; never share it between two daemons.
- `-yes` is required on mainnet: the daemon pays real withdrawals.
- The wallet RPC must be the signer's own wallet, on this device, and must never be a mining address.

### What a pass does, in order

1. Reads the vault. Sealed? If not, waits. Reserve below `sup`? Votes `Halt(1)` once per 2000-block bucket and
   stops. Halted? Stops.
2. Attests deposit addresses for new indexes (`Attest(j, addr)`); a refused vote means the quorum was already
   reached.
3. Votes `Mint(j, id, amount)` for deposits at or beyond the confirmation policy that are not yet `md_<id>`.
4. For each withdrawal `j` with `w_<j>_st == 0` whose burn height is at or below DERO's `stableheight`: writes an
   intent (paying), calls the backend's `Release` (idempotent per `j`), records the foreign txid (paid), votes
   `Released(j, ftx)` (voted). A burn above the stable height waits with a log line.

### Stop and move

Stop the process any time; every step is durable before the next one starts. To move a signer, copy the state
file and the wallet file together; start on the new machine with the same `-state`. Never run the same signer
in two places: the second daemon would repeat votes (refused by the contract, harmless) and, with a backend that
is not idempotent, could pay twice.

### Crash recovery

On start the daemon loads its state strictly: a corrupt file stops it with a message rather than starting
empty. Every intent in `paying` is retried (the backend must look the payment up by `j` before paying); every
intent in `paid` is voted; `voted` intents are skipped. An intent whose `(dest, amount)` no longer matches the
chain stops that withdrawal with an operator message; read both, decide, edit the state file by hand only with
the daemon stopped.

### Backups

The state file holds no keys, only votes cast and intents; back it up after every pass if the backend is not
idempotent. The wallet file and the foreign key shares are the real secrets: offline copies, never in the
repository, never in `.testnet/` or `.mainnet/` on a shared machine.

### Reading the log

```
withdrawal 2 waits for DERO finality (burned at 5805, stable height 5798)
RESERVE SHORTFALL: foreign reserve 46000 < wrapped supply 71000; voting Halt(1)
Mint [{j U 0} {id S …} {amount U 25000}] tx d2ade4c4…
vault: users 1, supply 92000 / cap 100000, withdrawals 2, halted 0
```

The last line is the pass summary; compare it across signers to spot one that is behind.

A withdrawal larger than `-max-withdrawal-bps` (default 2000 = 20 %) of the foreign reserve is never paid by the
daemon: it logs `SUSPICIOUS WITHDRAWAL`, votes `Halt(1)` once per bucket, and leaves the intent untouched for a
human to decide (halt-and-resume.md). Lower the threshold as the reserve grows.


---

# cap and bonds

## Cap, bonds and slashing

### Cap

`cap` is the most wrapped supply the vault will mint. It is enforced at the k-th mint vote and bounded by the
bonds: `cap ≤ 3 × bondtot`, checked at `Seal` and at every `SetCap`. `SetCap(cap)` is a k-of-n vote keyed by the
cap epoch `ce`; k agreeing votes set the cap and bump the epoch, which voids older votes for other values.

To raise the cap: first raise the bonds (below), then k signers vote `SetCap(<new>)`. To lower it: k votes; a
cap below the current `sup` is allowed and stops further mints until burns bring `sup` under it.

### Limits set once, before Seal

`SetLimits(maxdep, winmax, wdelay)` (owner, before `Seal`; refused afterwards by everyone): the largest single
deposit that will be minted, the most that will be minted over any two consecutive 2000-block buckets (a sliding window), and the number of blocks after
a burn before any `Released` vote counts. Zero means no limit. Choose them so that a colluding quorum's fastest
possible theft (one bucket of `winmax`) is less than the bonds, and so that `wdelay` covers the time an honest
signer needs to notice a bad burn and halt.

### Bonds

`Bond()` with DERO from a signer wallet adds to that signer's bond and to `bondtot`. Every signer must hold at
least `minbond` before `Seal`. Bonds are the federation's skin in the game: a colluding quorum can take the
foreign reserve, but the cap keeps the reserve at most three times what the bonds are worth in DERO.

Unbonding is not implemented (S21): a bond stays until a slash. Do not bond funds you may need.

### Slash

`Slash(target)` is a k-of-n vote that removes a signer, takes its bond out of `bondtot`, lowers `na`, and opens
a new slash epoch `se`, which voids every pending vote the removed signer cast (mint, attest, release, halt).
The contract refuses a slash that would leave fewer than `k` active signers, and a signer cannot slash itself.

After a slash the cap bound `cap ≤ 3 × bondtot` may no longer hold; mints refuse until k signers lower the cap
or the remaining signers bond more. Check `bondtot` and `cap` right after.

Replacing a signer is not implemented (S21 rotation): the signer set only shrinks after `Seal`. Plan the set
with spare signers.


---

# halt and resume

## Halt and resume

`Halt(want)` is a k-of-n vote. `want = 1` halts, `want = 0` resumes. Votes live in 2000-block buckets under the
current halt epoch `he`; k votes flip the state and open a new epoch. While halted: no mints, no releases, no
burns, no attestations. Wrapped units keep their claim.

### When to halt

- A signer's daemon reports `RESERVE SHORTFALL` (it has already voted; the other signers' daemons will too on
  their next pass if they see the same shortfall).
- A signer key or machine is suspected compromised.
- The foreign chain is in an incident (deep reorg, consensus bug, halted network).
- A deposit was minted that the federation cannot find on the foreign chain.

### How

Each signer operator, from their signer wallet, against the vault:

```
entrypoint Halt, want = 1        # k signers within the same 2000-block bucket
```

Confirm with `halted == 1` and a new `he`. The daemons stop acting on their own once they read `halted`.

### Before resuming

1. The cause is written down (what, when, which blocks, which signers saw it).
2. The foreign reserve is counted and equals or exceeds `sup` (until viewing keys are published this is the
   signers' statement; publish the count anyway).
3. Every intent file is reconciled: no `paying` or `paid` intent left behind.
4. If a signer was compromised: slash it first (`cap-and-bonds.md`), then resume.

### Resume

```
entrypoint Halt, want = 0        # k signers, same bucket
```

Then start the daemons again and watch one full pass on each.


---

# incident reserve shortfall

## Incident: the foreign reserve is below the wrapped supply

The daemons detect it (`Reserve()` from the backend against `sup`) and vote `Halt(1)`; with k honest daemons the
vault halts within one pass interval. Everything below is what people do next.

1. **Confirm the numbers.** Every signer counts the foreign reserve independently (wallet balance, minus
   pending outgoing payments in `paying`/`paid` intents) and reads `sup` from the vault. Write the numbers down
   with block heights on both chains.
2. **Find the cause.** In order of likelihood: a release paid twice (a non-idempotent backend after a crash: the
   intents show it); a deposit minted for a transaction that was reorganised out on the foreign chain (compare
   `md_<id>` ids against the foreign chain at policy depth); a foreign-side theft (a signer's share compromised);
   a mint vote that lied (compare the minted `(j, id, amount)` tuples against the foreign chain).
3. **Stop the bleeding.** If a signer is compromised, `Slash(target)` (k votes). If a backend paid twice, stop
   that daemon and fix the backend's lookup before restarting.
4. **Make the reserve whole or lower the supply.** The federation tops up the reserve from the bonds' owners,
   or accepts that `sup` exceeds the reserve and says so publicly. Burns stay halted until the decision is
   public.
5. **Resume** only per `halt-and-resume.md`, with the count published.

Never mint against the shortfall, never change the cap to hide it, never describe the halt as "maintenance".
