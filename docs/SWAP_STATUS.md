# The swap status contract: a companion tells the app where its swap stands

**State: both halves are built.** The app half is `app/track.js`, used by `app/xmr.js` and `app/zswap.js`. The
companion half is `-status 127.0.0.1:PORT` in `tools/xmrswap` and `tools/zecswap`, on the shared package
`tools/swapstatus` (§8). Unpaired, the app shows the DERO-side stages it reads from the chain, and says that the Monero
or Zcash side is visible only to the user's own swap program. The app's pairing form still says the flag "is not yet
built" (`app/track.js:125`); that sentence is the app's to change.

## 1. Why

A pending deposit or withdrawal has stages on two chains. The app reads DERO, so it can show the DERO side: the offer,
the lock and its depth, which exit is open, and the next deadline. It cannot read Monero or Zcash, and the swap's
secrets never reach it. The user's companion sees both sides. This contract lets the companion report its stages to
the app on the same machine, and nothing else.

Words the app uses, in the user's terms:

| Direction | The user | Who that is |
|---|---|---|
| **Deposit** | gives XMR or ZEC, receives the DERO-side asset | the XMR taker (`xmrswap alice`); the ZEC taker |
| **Withdrawal** | gives DERO (or a DERO token), receives XMR or ZEC | the XMR maker (`xmrswap bob`); the ZEC maker |

## 2. The companion

- A flag `-status 127.0.0.1:PORT`, **off by default**. The host must be `127.0.0.1`; any other is refused at start.
- At start it prints a **one-time pairing code** to its terminal: 16 random bytes from the OS generator, as 32
  lower-case hex characters. A new code every start. It is never written to a file or a log.
- It serves one resource and writes nothing: the status is read-only, and no request can change the swap.

## 3. The request

```
GET http://127.0.0.1:PORT/v1/status
Authorization: Bearer <pairing code>
```

The companion checks, in this order, and stops at the first failure:

1. **Host.** The `Host` header must be exactly `127.0.0.1:PORT` or `localhost:PORT`. Anything else gets `403` with
   no body. This blocks DNS rebinding: a page on another site that points its own name at 127.0.0.1 still sends its
   own name as the Host.
2. **Preflight.** `OPTIONS` gets `204` with no body and no swap data: `Access-Control-Allow-Origin: *`,
   `Access-Control-Allow-Methods: GET`, `Access-Control-Allow-Headers: Authorization`, `Access-Control-Max-Age: 600`,
   and `Access-Control-Allow-Private-Network: true` when the request asks for it. A wildcard origin is safe here
   because the credential is the header, not a cookie: a page without the code gets nothing.
3. **Code.** A missing or wrong `Authorization` gets `401` with **no body**. The comparison is constant-time.
4. **Answer.** `200`, `Content-Type: application/json`, `Cache-Control: no-store`, `Access-Control-Allow-Origin: *`,
   and the body below. Any other method or path gets `404` with no body.

## 4. The response (JSON, version 1)

It carries **no secret**: no key share, no spend or view key, no gate secret, no channel key, no pairing code, and no
preimage before it is public on chain. The app refuses an answer with any field whose name looks like one (`secret`,
`preimage`, `priv…`, `seed`, `mnemonic`, `spend key`, `view key`, `channel key`, `pairing`), however deep, and shows
nothing from it.

| Field | Type | Meaning |
|---|---|---|
| `v` | 1 | this version |
| `program` | `"xmrswap"` \| `"zecswap"` | which companion |
| `role` | `"maker"` \| `"taker"` | the user's side |
| `direction` | `"withdrawal"` \| `"deposit"` | maker is always withdrawal, taker always deposit; any other pairing is refused |
| `network` | string | the DERO network, as the app's build names it (`simulator`, `testnet`, `mainnet`) |
| `contract` | 64 hex | xmrswap: the `htlc.bas` id; zecswap: the `swap.bas` escrow id. Must be the one the app's build names |
| `lock` | integer \| null | the lock's id in that contract; null before it exists |
| `asset` | `""` \| 64 hex | the DERO-side asset; `""` is DERO |
| `deroAmount` | decimal string | atomic units of that asset |
| `foreignAmount` | decimal string | piconero or zatoshi |
| `fee` | decimal string | the 0.5% swap fee, in foreign atomic units; the maker pays it |
| `phase` | string | the companion's own phase name, for support of itself only |
| `outcome` | `""` \| `"swapped"` \| `"refunded"` \| `"punished"` \| `"reclaimed"` \| `"lost"` | empty while running; the app stops asking once it is set |
| `next` | string | one sentence: what happens next, and who must act |
| `updated` | RFC 3339 | when the companion last looked |
| `steps` | array, at most 32 | below |

Each step:

| Field | Type | Meaning |
|---|---|---|
| `id` | string | stable within a program (`dero_lock`, `xmr_lock`, `gate`, `claim`, `refund`, …) |
| `label` | string | in the user's words, from the user's side ("You lock 1 XMR at the joint address") |
| `chain` | `"DERO"` \| `"Monero"` \| `"Zcash"` | where the step happens |
| `state` | `"done"` \| `"active"` \| `"waiting"` \| `"skipped"` \| `"failed"` | |
| `txid` | 64 hex \| `""` | the step's transaction, once there is one |
| `conf`, `need` | integer \| null | confirmations it has and needs; the app shows "3 of 10" |
| `deadline` | `{chain, height, eta}` \| null | the height the step must happen by (or from), and the companion's estimate of when |

The four fixtures in `tests/js/fixtures/` are complete examples: each program on its happy path and its refund path.

The app treats every value it cannot read as unreadable, never as zero: an amount that is not a decimal string, a
count that is not a whole number, an unknown state, a deadline on an unknown chain.

## 5. The app

- **Pairing.** A "Pair with your swap program" form on each swap screen takes the port and the code. The code is held
  in the page's memory and in `sessionStorage` (so a reload keeps it for that tab), **never in `localStorage`**, and is
  sent only to `http://127.0.0.1:PORT`. Unpair forgets it.
- **Polling.** Every 10 s while that swap screen is open; none while it is not; never again once `outcome` is set.
  Each request has a 5-second limit, no cookies (`credentials: "omit"`), no referrer and no redirects.
- **Showing.** A tracker: each step with a mark whose shape carries its state (a tick, a filled dot, a dashed ring, a
  dash, a cross) as well as the word, confirmations as "3 of 10", and each deadline as a countdown. A DERO deadline
  counts down from the tip the app itself read, at 18 s a block; a Monero or Zcash one uses the companion's `eta`
  and says it is the companion's estimate. The answer is shown only if its program, network and contract match.
- **CSP.** No change was needed: `connect-src` already allowed `http://127.0.0.1:*` and `http://localhost:*` for the
  node and XSWD, and the tracker uses only `http://127.0.0.1:PORT`.

## 6. The privacy model

- **Loopback only.** The companion listens on 127.0.0.1 and nowhere else; the app asks only 127.0.0.1.
- **The code.** A page on another site can send a request to 127.0.0.1, but without the code it gets a 401 and no
  body; it cannot read the preflight's answer for data because there is none.
- **The Host check** blocks DNS rebinding.
- **No secrets in the payload.** The worst an answer can reveal to someone who has the code is what the user's own
  screen shows: amounts, heights, txids and the swap's stage. All of the DERO side is public on chain already.

## 7. Testing

`tests/js/track.test.mjs` runs the four fixtures through the app's parser and its words, and the refusals.
`tools/uitest/statusstub.mjs` is a stub status server that exists only for the UI pass: it serves a fixture behind the
same Host check, code and preflight, and the pass checks those gates (401 with no body, 403 for a foreign Host)
before it pairs both swap screens with it. It is never shipped in `app/`.

## 8. The companions, as built

This section adds to the contract above and changes none of it.

**The flag.** `xmrswap bob|alice ... -status 127.0.0.1:PORT` and `zecswap maker|taker ... -status 127.0.0.1:PORT`.
It is off unless given. It is refused with the other flags, before any node is contacted, unless the host is exactly
`127.0.0.1` and the port is from 1024 to 65535; `localhost`, `0.0.0.0`, `::1` and every other address are refused.
A bind that fails stops the companion before the swap takes a step.

**The code.** 16 bytes from `crypto/rand`, as 32 lower-case hex characters, new at every start. It is printed once,
to the companion's standard error, with the port and what the code is for, and is never written to a file or a log by
the program. An operator who redirects standard error to a file puts the code there; it is worth nothing once the
program has stopped.

**The server** (`tools/swapstatus/server.go`) follows §3 in order:
- a Host that is not `127.0.0.1:PORT` or `localhost:PORT` gets 403;
- `OPTIONS` gets 204 with the CORS headers of §3, and `Access-Control-Allow-Private-Network: true` when asked;
- any other method, any other path, or any query gets 404;
- a missing, wrong or repeated `Authorization` gets 401, compared with `crypto/subtle`;
- before the first answer, or after an answer that breaks the contract, 503;
- otherwise 200 with the answer.

Every refusal has no body. Every answer carries `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`. The
server has read, write and header timeouts, an 8 KiB header limit, and an error log that goes nowhere.

**The answer** is built after every pass of the swap, in the goroutine that runs it, from the state file's own
record, the configuration and the DERO height the companion last read. It reads nothing more and invents nothing:
- A value the companion does not know is left out, or is `null`: the lock before it exists, a count nobody has read,
  a deadline before its height is decided.
- **The fee.** It is checked against the contract before it is served (`Payload.Validate`, the app's own rules), and
  an answer that fails is not served. It is the 0.5 % the maker's companion pays: `SwapFee` of the XMR, or of the
  Zcash funding. It is "0" where the record names no treasury, or where zebrad's dust rule leaves the Zcash fee
  output out.
- **DERO depths** (`conf` against `need` 8, DERO's stable depth) are counted from the height at which the companion
  first read the lock, the stipend, the Claim, the Refund, the Punish or the Reclaim. That height is at or above the
  block, so the count is never more than the truth.
- **Monero confirmations** are what the maker's gate last counted at the joint address, against his `-confs`. The
  taker's companion reads no Monero confirmations, so hers are `null`.
- **Zcash confirmations** are counted from the highest tip the swap has read, and from the block the funding or the
  claim was last seen mined in.
- **The estimate** in a deadline's `eta` is the blocks left at the chain's target spacing: 18 s for DERO, 75 s for
  Zcash. There is none on the DERO simulator, none on Zcash regtest, where blocks come when mined, and none before a
  tip is read.
- A deadline is given only on a step still to happen.
- `txid` is a transaction's id once the companion has one. It is never an assumed one: xmrswap's
  "(… txid not recorded)" becomes "".
- `next` is a fixed sentence per phase. It never quotes the state's `outcome` or `note`, which can quote an error.

**Nothing secret is read into the answer.** For xmrswap that means no key share, gate secret, view key, joint
password, share read back from the chain or channel key. For zecswap it means neither the maker's secret nor the
taker's copy of it, and no key. `statusv1_test.go` in both programs covers this in two ways. First, it builds the
answer at every save of the crash fuzz's fake worlds, with and without process deaths, on every exit, plus a refusal,
and looks for each of those values in it. Second, it builds the answer for every phase of both machines from a state
that holds them all. It then runs every answer through `app/track.js` with `tests/js/statuscheck.mjs`.

**The outcome.** The contract has no word for a swap that ended with nothing locked or funded: refused, cancelled,
expired, walked, declined, taken, a funding that expired unmined, or a maker who took his DERO back before any XMR
moved (xmrswap `aborted`). Each side has what it started with, so the answer says `refunded`, and `next` says that
nothing moved. Other cases:
- A zecswap maker who claimed the ZEC and also had his DERO refunded (`claimed_refunded`) is `refunded`, and `next`
  says he has both.
- An xmrswap maker whose sweep did not finish (`sweep_unknown`) is `swapped`, with the sweep step failed: the Claim
  is stable and the XMR is his.
- An xmrswap taker whose recovery sweep did not finish is `refunded`, with that step failed.

A later version of the contract could add a distinct word for each; the app refuses any outcome it does not know,
so version 1 cannot.

**After the end** the companion keeps serving the final answer for ten minutes (`swapstatus.Linger`), or until it is
stopped, so that a screen asking every 10 s reads the outcome. Then it exits as before.
