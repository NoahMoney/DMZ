# Ring size and contract calls: what a Veil trade reveals, and the signerless path

Measured 2026-09-06 on the DERO simulator with `tools/ringprobe` (log: `research/ringprobe.log`). Facts carry a
file and line in the vendored derohe source; the design that follows is a proposal, not a decision.

## 1. Measurement

A probe contract whose `Pay(dest)` entrypoint stores `IS_ADDRESS_VALID(SIGNER())`, `STRLEN(SIGNER())` and
`DEROVALUE()`, then pays `DEROVALUE()` to `ADDRESS_RAW(dest)`. Called from wallet 30001 with 12,345 units and
`dest` = wallet 30006, at three ring sizes:

| ring | tx confirmed | DEROVALUE seen | SIGNER valid | SIGNER length | destination received | gas compute / storage |
|---|---|---|---|---|---|---|
| 2 | 22 s | 12,345 | 1 | 33 | 12,345 | 310,800 / 154 |
| 16 | 25 s | 12,345 | 0 | 33 | 12,345 | 310,800 / 154 |
| 32 | 21 s | 12,345 | 0 | 33 | 12,345 | 310,800 / 154 |

Wall-clock is block time on the simulator; gas does not depend on the ring. At ring 16 and 32 the contract ran,
saw the deposit, paid the named address, and received a SIGNER of 33 zero bytes.

## 2. Why (derohe source)

- `blockchain/transaction_execute.go:426-445` `Extract_signer`: the signer is derived only from a payload with
  `SCID == 0` and `RingSize == 2`, by the proof parity. Anyone with the chain can run this function on any
  ring-2 transaction; the "signer" of a Veil trade is public information, not something only the contract sees.
- `blockchain/transaction_execute.go:296`: when extraction fails, execution continues: "allow anonymous SC
  transactions with condition that SC will not call Signer … otherwise SC receives signer as all zeroes".
- `blockchain/transaction_verify.go:290-295`: consensus accepts rings from 2 to 128 for every payload.
- `walletapi/rpcserver/rpc_transfer.go:79`: the wallet RPC (XSWD uses the same handler) passes `p.Ringsize`
  through to `TransferPayload0`; only `/install_sc` pins ring 2 (`rpc_websocket_server.go:226`).
- `walletapi/wallet_transfer.go:360-380`: for rings above 2 the wallet draws decoys from its ring-member cache of
  the payload's asset, falling back to the DERO base tree when fewer than 40 candidates are known; the daemon
  initialises a zero encrypted balance for a decoy that has no entry in an asset tree
  (`transaction_execute.go:216-227`), so any registered address can be a decoy for any asset.

## 3. What a trade reveals today

Every Veil entrypoint pays `SIGNER()`, so the app builds every call at ring 2 (`app/app.js:98,105`,
`app/xswd.js:106`) and the address that paid is derivable by every observer. `DEROVALUE()` is the payload's
`BurnValue`, a plaintext field, at any ring size. Contract storage is public. So an "unpublished" trade is
pseudonymous (address, amount, block), not private. The app's settings text ("Ringsize 2 is required for
contract calls") states the mechanism but not the consequence; it needs the sentence "anyone can compute which
address paid a ring-2 call".

## 4. Signerless entrypoints (contracts implemented 2026-09-06 loop 7; app pending)

Add to `launch.bas` and `pool.bas`:

- `SwapDeroForAssetTo(dest String, min_out Uint64, publish Uint64)` and `SwapAssetForDeroTo(dest, min_out, publish)`:
  identical maths to the existing entrypoints, but every `SEND_*` goes to `ADDRESS_RAW(dest)` and `SIGNER()` is
  never read. `IS_ADDRESS_VALID(ADDRESS_RAW(dest))` guards the argument. The trade record stores `dest` (or
  nothing when `publish = 0`).
- The app builds these calls at ring 16 by default (the wallet needs 15 decoys with a balance entry; the base
  tree always has them).

What it hides: which of the 16 ring members paid. What it still shows: the amount (BurnValue), the destination
address (an SC argument), the block, and the contract state change. A user who names a fresh, unlinked address as
`dest` gets: hidden payer, pseudonymous receiver. A user who names their own address gets nothing over ring 2
except deniability of the payment source.

Cost: transaction size grows with the ring (each member adds a compressed key and an ElGamal pair); the DERO fee
is size-based and small; the wallet's build time grows with the ring (unmeasured beyond 32). Nothing else changes:
gas, contract logic, charts, reserves.

Fail-closed property (verified by the probe's `valid = 0`): every existing entrypoint checks
`IS_ADDRESS_VALID(SIGNER())` first, so a larger-ring call to a SIGNER-paying entrypoint reverts and refunds instead
of paying 33 zero bytes.

## 5. Identity without a signer: hash-bound claims (idea, unbuilt)

Staking, Founders registration and withdrawals need to know who may take funds out. A signerless design binds
the right to a secret instead of an address: `Stake(claim)` stores `claim = HEX(SHA256(secret + dest))`; later
`Unstake(secret, dest, amt)` recomputes the hash and pays `dest`. A mempool observer sees `secret` and `dest`
but cannot redirect the payout: changing `dest` changes the hash. Threats to test before adopting it: reuse of a
secret across stakes (forbid: one claim per stake record), loss of the secret (the position is stuck; the app
would keep it encrypted in the wallet's storage, which contradicts "never store secrets in the browser" unless
the secret is derived from the wallet's own key by signing a fixed message), and the ring-2 leak returning
through the deposit that funded the stake.

## 6. Verification (status 2026-09-06, loop 8)

1. Done: the harness emulates ring > 2 with a nil signer (33 zero bytes); `tests/ringswap_test.go` covers both
   contracts, refusal of the classic entrypoints, payment to `dest`, bad destinations, records naming `dest`.
2. Done in part: one private buy on the simulator through the app; the node reports a 16-member ring on the
   transaction (`tools/uitest/ui.mjs --wallet --dest=…`). Three payers to one dest and an explicit
   `Extract_signer` check on each tx are still to run.
3. Done: the app sends `ringsize: 16` through XSWD and the fork's wallet builds the ring (the same wallet pass).
4. Done (`research/ringprobe-sizes.log`, simulator, one DERO payload with a contract call): the node extracts a
   signer at ring 2 and none above it; every ring executed the contract and paid the destination.

   | ring | tx size | wall-clock to confirmation |
   |---|---|---|
   | 2 | 1,848 bytes | 22 s |
   | 8 | 2,720 bytes | 23 s |
   | 16 | 3,336 bytes | 22 s |
   | 32 | 4,240 bytes | 23 s |
   | 64 | 5,784 bytes | 25 s |

   About 63 bytes per extra ring member; the wallet's build time is invisible next to the simulator's block
   time. Ring 16 is the app's private-mode default (1.8× the size of a ring-2 call).
5. Done: settings copy and the sheet's "Ring size" line say what each mode reveals.

## 7. Trust statement

This does not make Veil a private exchange. It makes the payer of a trade one of N accounts instead of one, at
the user's choice, using only what DERO's consensus already permits. No new cryptography, no new trusted party.
