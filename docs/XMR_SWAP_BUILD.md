# DERO ↔ Monero trustless swap — build specification

**Status 2026-09-08: the build.** The design is `research/crosschain/RESEARCH.md` §4.4 and the verdict is
`research/wrapper/MECHANISM_REVIEW.md` §3. The proof that makes it possible exists (`poc/proof-xmr`: SHA-256
preimage ↔ ed25519 key share, Bulletproofs, no trusted setup, 14.8 s prove / 1.15 s verify / 1,601 bytes).
This document is what gets built, how it is split so five people can build it at once without touching each
other's files, the interfaces they build against, and what must be true before any of it is called working.

Nothing here touches `contracts/vault.bas`, `pool.bas`, `launch.bas` or the app. The swap is a separate contract,
a separate daemon the *user* runs for the duration of one swap, and a proof tool. The operator hosts nothing.

---

## 1. The protocol, exactly

Roles follow COMIT/Farcaster with DERO in the scripted seat. **Bob has DERO and locks first. Alice has XMR and
locks second** (and so holds the free option for one lock window — see §7).

### Setup, off chain, over the rendezvous channel

| Who | Generates | Sends to the other |
|---|---|---|
| Alice | scalar `s_a` (32 bytes, < ℓ), `S_a = s_a·G` (ed25519), `H_a = SHA256(s_a)`, proof `π_a` of "SHA256(x)=H_a ∧ x·G=S_a"; the joint address's **view key pair** `(v, V)` | `S_a, H_a, π_a, V, v` (the private view key is shared: both must see the lock, neither can spend with it), her DERO address |
| Bob | `s_b, S_b, H_b, π_b` likewise; **gate secret** `k_b` (32 random bytes), `H_k = SHA256(k_b)` | `S_b, H_b, π_b, H_k` (never `k_b` yet) |

Each party verifies the other's proof before anything is locked. Without `π_a`, Alice could claim DERO with a
preimage that is not her Monero share (Bob's XMR would be unspendable). Without `π_b`, Bob could refund with a
non-share (Alice's XMR would be unspendable).

The **joint Monero address**: spend public key `P = S_a + S_b`, view public key `V`, standard address encoding
(network byte ‖ P ‖ V ‖ keccak256(…)[0:4], Monero base58). The spend private key is `s_a + s_b mod ℓ` — nobody
holds it until a reveal.

### On chain

1. **Bob locks DERO** in `htlc.bas`: `Lock(ha, hk, hb, taker=Alice, t1, t2, asset)` with the DERO (or asset).
2. Alice reads the lock, checks every field (§4 invariant 3), then **locks XMR** to the joint address.
3. Bob watches the joint address through a view-only wallet. At **≥ 10 confirmations** of the right amount he
   sends Alice `k_b` over the channel.
4. Alice **claims** DERO: `Claim(id, s_a, k_b)` — reveals `s_a` on chain.
5. Bob reads `s_a`, forms `s_a + s_b`, imports the joint wallet with that spend key and **sweeps** the XMR to
   his own address.

### The three exits, and who ends with what

| Path | Condition | Who calls | Reveals | Result |
|---|---|---|---|---|
| **Claim** | height < T1, `SHA256(s_a)=H_a`, `SHA256(k_b)=H_k`, caller is the taker | Alice | `s_a` | Alice: DERO. Bob: XMR (via `s_a+s_b`). The swap. |
| **Refund** | T1 ≤ height < T2, `SHA256(s_b)=H_b`, caller is the maker | Bob | `s_b` | Bob: DERO back. Alice, if she locked: XMR back (via `s_a+s_b`). |
| **Punish** | height ≥ T2, caller is the taker | Alice | nothing | Alice: the DERO. Bob: nothing — he had [T1,T2) to refund and did not. Any XMR Alice locked stays locked to the joint key forever (she is compensated in DERO; Bob cannot take it without `s_a`). |

Why the gate secret: without `k_b`, Alice could claim DERO the moment Bob locked, never having locked XMR. Bob
hands over `k_b` only after Monero has confirmed her lock. A claim delay instead of a gate secret was reviewed
and rejected on 2026-09-06 — a delay lets Bob *notice* a claim without an XMR lock, not stop it.

Why Refund reveals `s_b`: if Alice locked XMR and Bob then refunds (say Bob never sent `k_b`), Alice must be able
to recover her XMR. `s_b` on chain gives her `s_a + s_b`.

Why Punish exists: if Bob neither sends `k_b` nor refunds, Alice would otherwise be stuck with locked XMR and no
DERO. Punish makes doing nothing cost Bob the DERO, so he refunds (revealing `s_b`) instead.

### Timelocks (DERO blocks, 18 s)

| Constant | Value | Reason |
|---|---|---|
| `T1_MIN` | 400 blocks (~2 h) after the lock | Alice must lock XMR, wait 10 Monero confirmations (~20 min; the 18-block reorg observed on Monero argues for more on large amounts), receive `k_b`, and claim — all before T1 |
| `T2_MIN` | T1 + 400 blocks (~2 h more) | Bob's refund window |
| Monero confirmations before `k_b` | 10 (the `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE`) | the output cannot be spent before then anyway; below it a reorg could unwind Alice's lock after Bob has released the gate |

The companion refuses to participate in a lock whose T1/T2 are below these; the contract enforces ordering
(`t1 > now + 1`, `t2 > t1`, both ≤ now + 1,000,000) and leaves the minimums to the client — they are policy.

---

## 2. Ownership — who builds what, and nothing else

| Agent | Owns | Builds |
|---|---|---|
| **A `htlc`** | `contracts/htlc.bas`, `tests/htlc_test.go`, one line in `tests/parse_test.go` | the escrow (§3) and every test in §5.A |
| **B `proofcli`** | `research/crosschain/poc/proof-xmr/**` | the prover/verifier as a CLI with a fixed byte layout (§3.B), `cargo test`, measurements |
| **C `monero`** | `tools/xmrswap/keys.go, keys_test.go, monero.go, monero_test.go, proof.go, proof_test.go, fakewallet.go` | key math, address encoding, the wallet-RPC client, the proof-tool wrapper, the fake wallet-RPC server (§3.C) |
| **D `swap`** | `tools/xmrswap/` everything else: `swap.go, state.go, dero.go, channel.go, *_test.go` for those, `cmd/xmrswap/`, `tools/simhtlc/` | the state machine, the DERO client, the channel, the CLI, the simulator driver (§3.D) |
| **E `spec`** | `docs/XMR_SWAP.md` | the user-facing protocol spec, the adversarial review, the trust statement, the screen design (§3.E) |
| **verify** | nothing | builds and runs everything (§6) |

C and D share a package. D codes against the interfaces in §3.C **as written here**; if C must deviate, C
reports the exact signature change and the verifier reconciles. Neither edits the other's files.

---

## 3. Components

### 3.A `contracts/htlc.bas`

Extend `contracts/swap.bas` (read it first; keep its conventions: `Initialize`, `kind`/`v`/`n`, per-id keys,
`HEXDECODE` of a 64-hex secret to 32 raw bytes, `HEX(SHA256(raw))` compared to the stored hex hash).

```
Initialize()                                   kind = "htlc", v = 1, n = 0
Lock(ha String, hk String, hb String, taker String, t1 Uint64, t2 Uint64, asset String) Uint64
    ha, hk, hb: 64 hex chars each. taker: REQUIRED (an address; "" is refused — Punish pays it).
    now + 1 < t1 < t2 <= now + 1000000. asset: "" = the DERO sent, else 64-hex SCID and ASSETVALUE(a) > 0 with
    DEROVALUE() == 0. Stores m_<id> t_<id> ha_<id> hk_<id> hb_<id> a_<id> v_<id> t1_<id> t2_<id> st_<id>=0; n++.
Claim(id Uint64, sa String, kb String) Uint64
    st == 0, height < t1, SIGNER() == taker, both 64-hex, HEX(SHA256(HEXDECODE(sa))) == ha, same for kb/hk.
    st = 1, xa_<id> = sa, then pay taker.
Refund(id Uint64, sb String) Uint64
    st == 0, t1 <= height < t2, SIGNER() == maker, HEX(SHA256(HEXDECODE(sb))) == hb. st = 2, xb_<id> = sb, pay maker.
Punish(id Uint64) Uint64
    st == 0, height >= t2, SIGNER() == taker. st = 3, pay taker.
```

DVM rules that will bite: line numbers strictly ascending within a function, `DIM` first, only `IF c THEN GOTO n`,
uint64 wraps silently, `LOAD` of a missing key panics (guard with `EXISTS`), `SIGNER()` is real only at ring size 2,
install gas is charged on source bytes against 20,000 (comments are stripped by `reference.Canon` at install; the
harness measures the canonical form). **Every `SEND_*` must come after the last `GOTO 900` on its path** —
`tests/sendorder_test.go` fails the suite otherwise, and that ordering was a reproduced fund drain elsewhere.
`DEROVALUE() != 0` is refused on Claim/Refund/Punish so nothing is stranded.

### 3.B `poc/proof-xmr` as a tool

Keep the statement, transcripts (`veil.xmr.sha256-dl.v1`, `veil.xmr.sigma.v1`), generators (`GENS = 1<<16`) and
the sigma protocol exactly as they are — they are the prototype that was measured. Add:

```
proof-xmr prove  <secret_hex>                         → stdout JSON {"h":hex32,"s":hex32,"proof":hex}
proof-xmr verify <h_hex> <s_hex> <proof_hex>          → exit 0 valid / 1 invalid, one line why on stderr
proof-xmr keygen                                      → stdout JSON {"secret":hex32,"h":hex32,"s":hex32}
```

`secret` is the little-endian 32-byte scalar **already reduced mod ℓ** (`keygen` guarantees it; `prove` refuses
an unreduced input rather than silently reducing — the hash on DERO is of the exact bytes, and the Monero side
reduces, so the two would disagree). Proof bytes: `r1cs.to_bytes()` ‖ commitment (32) ‖ sigma `a1`(32) `a2`(32)
`z1`(32) `z2`(32); document the layout in the README with a worked vector. `cargo test` must cover: round trip;
wrong H; wrong S; tampered sigma; tampered r1cs; truncated proof; the "reduced scalar" refusal. Rebuild in WSL
(`research/crosschain/poc/wsl/build.sh proof-xmr build --release`), re-record `run.log` with the measurements
and the vector. No new dependency beyond what `Cargo.toml` already has except `serde_json` if needed for output —
say why if you add it.

### 3.C `tools/xmrswap` — the Monero side (agent C)

Package `xmrswap` in the tools module (`veil/tools/xmrswap`). Exported API D builds against:

```go
// keys.go
type Scalar [32]byte                       // little-endian, reduced mod ℓ
type Point  [32]byte                       // compressed ed25519
func NewScalar(rand io.Reader) (Scalar, error)          // rejection-sampled or reduced; must be < ℓ
func (s Scalar) Public() Point                          // s·G
func AddScalars(a, b Scalar) Scalar                     // (a+b) mod ℓ
func AddPoints(a, b Point) (Point, error)               // decompress, add, compress
func SHA256Hex(b []byte) string
type Network int; const ( Mainnet Network = iota; Stagenet; Testnet )
func Address(spend, view Point, net Network) (string, error)   // standard Monero address: netbyte‖P‖V‖keccak[0:4], base58-monero
// monero.go
type Wallet interface {                     // one monero-wallet-rpc
    Height(ctx) (uint64, error)
    Address(ctx) (string, error)                                // the currently open wallet's address
    Transfer(ctx, to string, amount uint64) (txid string, err error)
    GenerateFromKeys(ctx, name, address, viewKey string, spendKey string /* "" = view-only */, restoreHeight uint64) error
    OpenWallet(ctx, name string) error
    CloseWallet(ctx) error
    Incoming(ctx, minHeight uint64) ([]Incoming, error)         // get_transfers{in:true, filter_by_height}; each with Confirmations, Amount, TxID, Height
    SweepAll(ctx, to string) (txids []string, err error)
}
type Incoming struct { TxID string; Amount uint64; Height uint64; Confirmations uint64; Unlocked bool }
func NewWalletRPC(url, user, pass string) Wallet
// proof.go
type Prover interface {
    Keygen(ctx) (secret Scalar, h string, s Point, err error)
    Prove(ctx, secret Scalar) (h string, s Point, proof []byte, err error)
    Verify(ctx, h string, s Point, proof []byte) (bool, error)
}
func NewProverCLI(command []string) Prover   // e.g. {"wsl.exe","-e","/home/wilma/target/proof-xmr/release/proof-xmr"} or {"proof-xmr"}
// fakewallet.go — a fake monero-wallet-rpc HTTP server for tests and the simulator driver
func NewFakeWallet(net Network) *FakeWallet   // ServeHTTP; Mine(n) advances height; deterministic; supports the calls above
```

Primary sources for the key math, and cite them in comments: `monero-project/monero`
`src/cryptonote_basic/cryptonote_basic_impl.cpp` (address prefixes 18 / 24 / 53 and `get_account_address_as_str`),
`src/common/base58.cpp` (block sizes 8→11), `src/crypto/crypto.cpp` (scalar reduction), and the wallet-RPC
reference for `generate_from_keys`, `get_transfers`, `sweep_all`, `transfer`. **Address test vectors must come
from Monero's own tests** (`tests/unit_tests/base58.cpp`, `tests/unit_tests/address_from_url.cpp` or the
documented example addresses) — not from your own encoder. Use `filippo.io/edwards25519` for the curve and
`golang.org/x/crypto/sha3` (`NewLegacyKeccak256`) — say in the commit why each; no other new dependency.

### 3.D `tools/xmrswap` — the state machine, DERO side, channel, CLI, simulator driver (agent D)

```go
// dero.go — an error-returning DERO client. Do NOT use tools/simrpc (its policy is log.Fatal; the audit's V1).
// Copy its request shapes (Transfer with sc_rpc args at ring size 2, DERO.GetSC with variables, gas estimate).
type Dero interface {
    Height(ctx) (uint64, error)
    Lock(ctx, ha, hk, hb, taker string, t1, t2 uint64, amount uint64) (txid string, id uint64, err error)
    Claim(ctx, id uint64, sa, kb string) (string, error)
    Refund(ctx, id uint64, sb string) (string, error)
    Punish(ctx, id uint64) (string, error)
    ReadLock(ctx, id uint64) (Lock, error)   // every field, including xa/xb when revealed and st
}
// channel.go — the rendezvous; spore is the intended production transport, a directory of JSON files is the test one
type Channel interface { Send(ctx, to string, msg Message) error; Recv(ctx) (Message, error) }
type Message struct { Kind string; Body json.RawMessage }   // kinds: offer, alice_setup, bob_setup, dero_locked, xmr_locked, gate, done
// state.go — durable, fsync+rename, loaded strictly (mirror tools/vaultsigner's memory/intents)
// swap.go — two state machines
//   Bob:   setup → verified_pi_a → dero_locked → watching_xmr → xmr_confirmed(≥10) → gate_sent → claimed(read s_a) → swept | refund_window → refunded | punished
//   Alice: setup → verified_pi_b → saw_dero_lock(checked) → xmr_locked → waiting_gate → gate_received → claimed | refund_seen(read s_b) → swept | punish_window → punished
```

Invariants (§4) are asserted in tests, not assumed. `cmd/xmrswap` runs one side: `xmrswap bob|alice -state <file>
-dero <wallet-rpc> -daemon <rpc> -htlc <scid> -monero <wallet-rpc> -net stagenet -prover "<cmd>" -channel <dir>
-amount-dero N -amount-xmr M`. `tools/simhtlc` drives a whole swap on the local DERO simulator with the fake Monero
wallet (`-fake-monero`), two companions in-process, and exercises **all three exits**: happy path; Bob refunds and
Alice recovers XMR with `s_b`; Bob vanishes and Alice punishes. It prints every txid and reads every contract
key back.

### 3.E `docs/XMR_SWAP.md` (agent E)

The spec a user and a reviewer read: the protocol in plain words with the table above; what each party can lose
in each failure and why that is the best possible outcome; the free option and the bond idea (§7) — stated as
open, not implemented; the timelock table and why those numbers; what a watcher of DERO learns (maker, taker,
amount, hashes, the revealed shares — balances stay encrypted; the Monero leg is a normal Monero transaction);
what the companion is (a program the user runs for the length of one swap; it holds `s_a`/`s_b`/`k_b`; nothing
ever enters the browser); the rendezvous (spore, and why it is only a conversation); and an **adversarial
review** of the protocol as specified here — every race you can construct across the two chains' clocks (T1
passing while `k_b` is in flight; a Monero reorg after the gate; DERO's 8-block stability; a second lock to the
same address; Bob releasing `k_b` early on purpose; Alice claiming with a share that is not hers if the proof
were skipped) with the outcome of each. Then the screen: what the app shows and never holds. Cite primary
sources by URL; no invented numbers.

---

## 4. Invariants every implementation must hold (and tests must assert)

1. **A secret leaves the companion only by the protocol's own reveals**: `s_a` on DERO at Claim, `s_b` on DERO at
   Refund, `k_b` over the channel after confirmation. Never logged, never in a message of another kind, never in
   the browser.
2. **Bob never sends `k_b`** until the joint address shows ≥ the agreed amount at ≥ 10 confirmations, read from
   his own view-only wallet, not from anything Alice says.
3. **Alice never locks XMR** until she has read Bob's lock from the chain and checked: `ha == SHA256(s_a)`,
   `hk` equals the `H_k` Bob committed to over the channel, `hb` equals Bob's committed `H_b` and `π_b` verified
   against it, `taker` is her address, `t1 − now ≥ T1_MIN`, `t2 − t1 ≥ T2_MIN`, amount ≥ agreed, asset as agreed.
4. **Bob never locks DERO** until `π_a` verifies against `(H_a, S_a)`.
5. **Durable state precedes every irreversible action** (lock, gate send, claim, refund, sweep); a restart
   resumes; a sweep is attempted at most once per swap (idempotent by swap id).
6. **The contract sends only after its last check** (mechanically enforced by `tests/sendorder_test.go`).
7. **Nothing is proxied and nothing is hosted**: both companions talk to their own wallets; the channel carries
   only what the table in §1 lists.

---

## 5. Tests that must exist before "done"

- **A.** `tests/htlc_test.go` against the real DVM: happy path with both preimages; Claim without `kb`, with wrong
  `sa`, with wrong `kb`, by a non-taker, at height ≥ t1 — all refused; Refund before t1, at ≥ t2, by a non-maker,
  with a wrong `sb` — refused; Refund reveals `sb` and pays the maker; Punish before t2, by a non-taker, after a
  Refund, after a Claim — refused; Punish pays the taker; a Lock with `taker == ""`, with `t1 <= now+1`, with
  `t2 <= t1`, with both DERO and an asset, with neither — refused; asset locks round-trip; no path leaves value in
  the contract; install storage gas printed and < 20,000. Register `htlc.bas` in `TestContractsParse`.
- **B.** `cargo test` as listed in §3.B; `run.log` re-recorded.
- **C.** address vectors from Monero's tests; scalar reduction edge cases (ℓ−1, ℓ, ℓ+1, all-ones); point add
  matches scalar add (`(a+b)·G == a·G + b·G`); the fake wallet's `Incoming` confirmations advance with `Mine`;
  the wallet client's JSON matches the wallet-RPC reference (recorded fixtures).
- **D.** a crash fuzz over both state machines like `tools/vaultsigner/exactly_once_test.go` (kill after every
  durable step, restart, at most one sweep, never a gate before confirmation, never an XMR lock before the DERO
  lock check); a channel test; the DERO client against a fake daemon.
- **simhtlc on the simulator**: all three exits, txids printed, contract keys read back. This is the evidence.

---

## 6. Verification (the last agent, and me after it)

```
bash tests/run.sh                                   # DVM suite incl. htlc + sendorder + parse
cd tools && go build ./... && go vet ./... && go test ./xmrswap/ ./vaultsigner/
node --test tests/js
wsl.exe -e bash -lc 'REPO=/mnt/c/Users/Wilma/Desktop/fomo research/crosschain/poc/wsl/build.sh proof-xmr test --release'
cd tools && go run ./simhtlc -fake-monero -prover "wsl.exe -e /home/wilma/target/proof-xmr/release/proof-xmr"   # all three exits
```

The app is untouched, so the UI pass is not part of this gate; `genconfig -dist` is (nothing may push a file
over the DOC cap — nothing here should).

---

## 7. What this build deliberately does not do

- **No bond against the free option.** Alice holds a free option on the price for up to T1. RESEARCH.md §4.6
  sizes a 2–3 % DERO bond, slashed on DERO-observable failure. Not in this build; the spec (E) states it as open.
- **No spore integration.** The `Channel` interface exists and the directory transport is enough for the
  simulator and for two people who can exchange files; spore is the intended transport and is a later piece.
- **No app screen.** E designs it; nobody codes it until `simhtlc` has run all three exits.
- **No Monero stagenet run.** The fake wallet-RPC serves the fixture-recorded shapes; a stagenet run needs a node
  and is a one-off that follows.
- **No change to any existing contract.** `htlc.bas` is new; `swap.bas` and `board.bas` stay as they are.
