# DERO ↔ Monero trustless swap — build specification

**Status 2026-09-08: the build.** The design is `research/crosschain/RESEARCH.md` §4.4 and the verdict is
`research/wrapper/MECHANISM_REVIEW.md` §3. The proof that makes it possible exists (`poc/proof-xmr`: SHA-256
preimage ↔ ed25519 key share, Bulletproofs, no trusted setup, 8.51 s prove / 0.74 s verify, about 10.1 s / 2.3 s with generators / 1,601 bytes).
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
3. Bob watches the joint address through a view-only wallet. At **≥ 20 confirmations** of the right amount (the
   default; never fewer than 10) he sends Alice `k_b` over the channel.
4. Alice **claims** DERO: `Claim(id, s_a, k_b)` — reveals `s_a` on chain.
5. Bob reads `s_a`, forms `s_a + s_b`, imports the joint wallet with that spend key and **sweeps** the XMR to
   his own address.

### The four exits, and who ends with what

| Path | Condition | Who calls | Reveals | Result |
|---|---|---|---|---|
| **Claim** | height < T1, `SHA256(s_a)=H_a`, `SHA256(k_b)=H_k`, caller is the taker | Alice | `s_a` | Alice: DERO. Bob: XMR (via `s_a+s_b`). The swap. |
| **Refund** | T1 ≤ height < T2, `SHA256(s_b)=H_b`, caller is the maker | Bob | `s_b` | Bob: DERO back. Alice, if she locked: XMR back (via `s_a+s_b`). |
| **Punish** | T2 ≤ height < T2 + g2; anyone may send it, and it pays the taker (v2, LIV-07); refused from T2 + g2 (v3) | Alice, or anyone | nothing | Alice: the DERO. Bob: nothing — he had [T1,T2) to refund and did not. Any XMR Alice locked stays locked to the joint key forever (she is compensated in DERO; Bob cannot take it without `s_a`). |
| **Reclaim** | height ≥ T2 + g2, no end; `SHA256(s_b)=H_b`, caller is the maker (v3) | Bob | `s_b`, in `xb_<id>` as Refund stores it | Bob: DERO back, late. Alice, if she locked: XMR back (via `s_a+s_b`), with no deadline. Nobody punished in time. |

Why the gate secret: without `k_b`, Alice could claim DERO the moment Bob locked, never having locked XMR. Bob
hands over `k_b` only after Monero has confirmed her lock. A claim delay instead of a gate secret was reviewed
and rejected on 2026-09-06 — a delay lets Bob *notice* a claim without an XMR lock, not stop it.

Why Refund reveals `s_b`: if Alice locked XMR and Bob then refunds (say Bob never sent `k_b`), Alice must be able
to recover her XMR. `s_b` on chain gives her `s_a + s_b`.

Why Punish exists: if Bob neither sends `k_b` nor refunds, Alice would otherwise be stuck with locked XMR and no
DERO. Punish makes doing nothing cost Bob the DERO, so he refunds (revealing `s_b`) instead.

Why Punish ends and Reclaim follows (v3, MT-J13(b), `docs/ATOMIC_SWAP_STUDY.md` §5 decision 17 (a)): in v2 a lock
nobody punished was closed by anyone's Punish, for ever. In v3 Punish is open for `g2` blocks from T2 (an install
argument; 4,800, as the operator answered on 2026-09-12), and from T2 + g2 the lock is the maker's to Reclaim with `s_b`. Bob
loses the lock only to a timely Punish; a Reclaim reveals `s_b` exactly as a Refund does, so an Alice who was away for
the whole window recovers her XMR once Bob reclaims. **What v3 costs the taker is that her Punish has a deadline, and
that past it her XMR depends on Bob.** Her companion sends it at once from T2 and none from T2 + g2 − (11 + 8 +
margin); anyone may send it for her; the recovery card prints the window's last block and wakes her machine before T2.
If nobody punishes by T2 + g2 and Bob never reclaims — hostile, or having lost his state or `s_b` — her XMR and his DERO
stay locked for ever; under v2 she could Punish at any time after T2 (`docs/XMR_SWAP.md` §2, §10).

### Timelocks (DERO blocks, 18 s)

| Constant | Value | Reason |
|---|---|---|
| `T1_MIN` | 400 blocks (~2 h) after the lock; Bob's default is lock + 420, and Alice refuses a `t1` further out than her trade's tier's T1Max: the tier's quote plus 60, 480 blocks at the lowest tier (MT-B32; 1,200 for every trade before) | Alice must lock XMR, wait 20 Monero confirmations by default (~40 min; ten is the floor, and the 18-block reorganisation reported on Monero is why the default is twice it), receive `k_b`, and claim — all before T1 |
| `T2_MIN` | T1 + 400 blocks (~2 h more), at most T1 + 1,200 | Bob's refund window |
| Monero confirmations before `k_b` | 20 by default (`DefaultConfirmations`); refused below 10, the `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE` | below ten the output cannot be spent anyway; above it, a reorganisation deeper than the depth Bob chose could still unwind Alice's lock after he has released the gate, and Monero has no bound on that depth (invariant 10) |

The companion refuses to participate in a lock whose T1/T2 are below these. The contract enforces ordering
(`t1 > now + 1`, `t2 > t1`, both ≤ now + 1,000,000) and, since v3 (MT-A03(a)), the minimum widths too: a Lock is
refused when `t1 < height + w1min` or `t2 < t1 + w2min`, with `w1min` and `w2min` fixed at install (the profile's 400
and 400) and `height` the block the Lock *executes* in. DERO mines a transaction at most 10 blocks after it is built
(`vendor/derohe/blockchain/blockchain.go:748`, `miner_block.go:275`: a block takes one only while its height minus the
transaction's is below `TX_VALIDITY_HEIGHT` = 11); the companion counts 11, one block conservative. The daemon's
estimator runs the contract at height 0 and cannot see a narrow Lock. So Bob's companion accepts no t1 width below
max(T1_MIN, w1min + 11) — 411 at the profile — and builds no Lock at all from `t1 − w1min − 10`. 411 is the contract's
least with no slack: at exactly 411 the Lock can be built only at the height the windows were decided at, so one block
arriving before the Lock reads the height makes his companion refuse (`ErrLockTooLate`, nothing sent), and Alice's
first-sight check (`t1 − now ≥ T1_MIN`) leaves the Lock 11 blocks to be mined and read. The profile's default of 420 is
what leaves room: 10 heights to build at, 20 blocks for her check. The companion's own floors still apply on top of the
contract's.

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
Initialize(w1min Uint64, w2min Uint64, g2 Uint64)   kind = "htlc", v = 3, n = 0, and the three stored (v3)
    each 1..1000000 and w1min + w2min <= 999999, or nothing is installed. The same code with other values hashes
    the same, so each companion reads all three with the code and refuses an install outside its policy.
Lock(ha String, hk String, hb String, taker String, t1 Uint64, t2 Uint64, asset String) Uint64
    ha, hk, hb: 64 LOWER-CASE hex chars each (v2: HEX(HEXDECODE(x)) == x, as swap.bas and board.bas).
    taker: REQUIRED (an address; "" is refused — Punish pays it).
    now + 1 < t1 < t2 <= now + 1000000, and (v3) t1 >= now + w1min, t2 >= t1 + w2min, "now" being the height the
    Lock executes at. asset: "" = the DERO sent, else 64-hex SCID and ASSETVALUE(a) > 0 with DEROVALUE() == 0.
    Stores m_<id> t_<id> ha_<id> hk_<id> hb_<id> a_<id> v_<id> t1_<id> t2_<id> st_<id>=0; n++.
Claim(id Uint64, sa String, kb String) Uint64
    st == 0, height < t1, SIGNER() == taker, both 64-hex, HEX(SHA256(HEXDECODE(sa))) == ha, same for kb/hk,
    no DERO and none of the lock's asset attached. st = 1, xa_<id> = sa, then pay taker.
Refund(id Uint64, sb String) Uint64
    st == 0, t1 <= height < t2, SIGNER() == maker, HEX(SHA256(HEXDECODE(sb))) == hb, nothing attached.
    st = 2, xb_<id> = sb, pay maker.
Punish(id Uint64) Uint64
    st == 0, t2 <= height < t2 + g2 (v3), a valid ring-2 signer — ANY signer (v2, LIV-07) — nothing attached.
    st = 3, pay the stored taker t_<id>.
Reclaim(id Uint64, sb String) Uint64                (v3)
    st == 0, height >= t2 + g2 with no end, SIGNER() == maker, HEX(SHA256(HEXDECODE(sb))) == hb, nothing attached.
    st = 4, xb_<id> = sb (where Refund stores it), pay maker.
```

v3 (2026-09-12; MT-J13(b), MT-A03(a)) adds the install arguments, the Lock width refusal, Punish's end at t2 + g2 and
Reclaim, and nothing else: Claim and Refund are v2's byte for byte, so their gas and the stipend are unchanged. Install
storage gas is 5,177 at 400/400/2,400; a Reclaim is 231 storage gas, the size of a Refund (`tests/htlc_v3_test.go`).
The companion pins v3's canonical hash `b785028d…` and, with it, reads `w1min`, `w2min` and `g2` from every configured
node (`Deployment`, `checkDeployment`).

v2 (2026-09-11) changes three rules and nothing else. **Punish pays `t_<id>` whoever sends it** (LIV-07: in v1 only
the taker could, and a lock she never punished was frozen for ever). **Lock refuses a hash that is not lower-case**
(GAP-023: an upper-case hash is one no preimage's `HEX(SHA256(...))` can match). **Every exit refuses the lock's own
asset attached** (GAP-027: v1 refused only DERO, and anything else attached was kept for ever). Install storage gas is
3,907 for 3,870 canonical bytes, against the 20,000 cap; a Claim is 304 storage gas, a Punish 88
(`tests/htlc_test.go`, `tests/htlc_v2_test.go`). The companion pins the canonical code's SHA-256 (`HTLCCodeSHA256`,
`tools/xmrswap/pin.go`) and refuses any other.

DVM rules that will bite: line numbers strictly ascending within a function, `DIM` first, only `IF c THEN GOTO n`,
uint64 wraps silently, `LOAD` of a missing key panics (guard with `EXISTS`), `SIGNER()` is real only at ring size 2,
install gas is charged on source bytes against 20,000 (comments are stripped by `reference.Canon` at install; the
harness measures the canonical form). **Every `SEND_*` must come after the last `GOTO 900` on its path** —
`tests/sendorder_test.go` fails the suite otherwise, and that ordering was a reproduced fund drain elsewhere.
`DEROVALUE() != 0`, and `ASSETVALUE` of the lock's asset, are refused on Claim/Refund/Punish/Reclaim so nothing is stranded; an
asset of any other SCID cannot be seen by the DVM, so the companion never attaches one.

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
reduces, so the two would disagree). The circuit itself does not enforce it (L1 of the proof audit): a peer with
another prover can prove and reveal `s + kℓ`, so the companion reduces a revealed share after checking its hash and
its point (MT-B25, `keys.go` `RecoverShare`). Proof bytes: `r1cs.to_bytes()` ‖ commitment (32) ‖ sigma `a1`(32) `a2`(32)
`z1`(32) `z2`(32); document the layout in the README with a worked vector. `cargo test` must cover: round trip;
wrong H; wrong S; tampered sigma; tampered r1cs; truncated proof; the "reduced scalar" refusal. Rebuild in WSL
(`research/crosschain/poc/wsl/build.sh proof-xmr build --release`), re-record `run.log` with the measurements
and the vector. That development build's hash depends on where the checkout sits; the build the companion pins
(`ProverSHA256`) is made by `research/crosschain/poc/proof-xmr/build-reproducible.sh`, which builds the committed
sources at a fixed path and gives sha256 `7293e9c97e0330966499fff397a0488ef40d38f65da49423429c8c548c83d39d`,
1,340,640 bytes, from any checkout with the same toolchain (MT-B26). No new dependency beyond what `Cargo.toml` already has except `serde_json` if needed for output —
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
    GenerateFromKeys(ctx, name, address, viewKey, spendKey /* "" = view-only */, password string, restoreHeight uint64) error
                                            // the client checks v·G and s·G against the address first: the server does not
    OpenWallet(ctx, name, password string) error
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
func NewProverCLI(command []string) Prover   // unpinned, for drivers and tests: {"proof-xmr"} or {"wsl.exe","-e","<path to proof-xmr>"}
func NewPinnedProver(path, wantSHA256 string) (Prover, error) // the companion's: an absolute path, hashed before every run
func NewPinnedWSLProver(command []string, wantSHA256 string) (Prover, error) // Windows: wsl.exe [-d <distro>] -e <path>; the binary itself is hashed (X-17)
func PinnedProverFor(v, wantSHA256 string) (Prover, error) // a -prover value: a wsl.exe command, or an absolute path
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

**The prover on Windows (X-17).** proof-xmr is a Linux program. A Windows holder builds it inside WSL
(`build-reproducible.sh`) and gives it to the companion as `-prover "wsl.exe [-d <distribution>] -e <absolute path
inside WSL>"`. Hashing the command's first program would hash `wsl.exe`, so the pin is checked on the binary itself
instead (`NewPinnedWSLProver`, `tools/xmrswap/proof_wsl.go`):
- The file is read through the distribution's share, `\\wsl.localhost\<distribution>\<path>`, or `\\wsl$\...` on a
  Windows that has only the older name. It is hashed before every run, as a native binary is. Then Windows' own
  `%SystemRoot%\System32\wsl.exe` runs it with `-d <distribution> -e <path>`.
- A command that names no distribution runs in the default one. That is read once from the registry
  (`HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss`, `DefaultDistribution`, as `wsl.exe` reads it) and named on
  every run, so the distribution hashed is the one run.
- It fails closed. Each of these is refused:
  - a path that no share root can read, or that is not a regular file;
  - a first readable copy that hashes wrong (no later root is searched for one that matches);
  - a launcher that is not System32's `wsl.exe`, or a default distribution that cannot be read;
  - any form but the one above: without `-e`, `wsl.exe` runs the command through the user's shell, which finds and
    expands it; there is no other option and nothing after the path;
  - a path that is not absolute and clean, or that has a character outside `A-Z a-z 0-9 . _ + @ ~ - /`, because WSL's
    share shows some characters as others.
- The gap between the hash and the run is the native path's. The file is hashed and then run by name, so a
  replacement inside the gap is caught on neither path. Closing it would take running the very bytes that were
  hashed, which neither does.

So the pinned build runs on mainnet from Windows as it does from Linux, with no `-prover-sha256`, which mainnet refuses.
Tests:
- `TestAWSLProverCommandIsParsedStrictly`, `TestPinnedWSLProverHashesTheBinaryNotTheLauncher` and
  `TestPinnedWSLProverFailsClosed` run a stand-in `wsl.exe` over a stand-in share, and check that the launcher's hash
  is not what is pinned;
- `TestAProverInsideWSLIsNamedByItsCommand` checks the command line;
- `TestPinnedWSLProverRunsTheRealProver`, run with `XMRSWAP_TEST_WSL_PROVER=<its path inside WSL>`, runs the
  reproducible build through the real `wsl.exe`: keygen, prove, verify, and a tampered proof refused. It was run on
  2026-09-12 against the build pinned by `ProverSHA256`, read through `\\wsl.localhost` from the default distribution.
  The proof took 16.1 s through `wsl.exe` and was 1,601 bytes; it verified, and the tampered copy was refused.

### 3.D `tools/xmrswap` — the state machine, DERO side, channel, CLI, simulator driver (agent D)

```go
// dero.go — an error-returning DERO client. Do NOT use tools/simrpc (its policy is log.Fatal; the audit's V1).
// Copy its request shapes (Transfer with sc_rpc args at ring size 2, DERO.GetSC with variables, gas estimate).
type Dero interface {
    Height(ctx) (uint64, error)
    StableHeight(ctx) (uint64, error)
    Lock(ctx, ha, hk, hb, taker string, t1, t2 uint64, asset string, amount, cutoff uint64) (txid string, id uint64, err error) // asset "" = DERO (U1); no new Lock at h >= cutoff = t1 − w1min − 10 (MT-A03(a))
    Claim(ctx, id uint64, sa, kb string, cutoff uint64) (string, error) // nothing sent at h + 1 >= cutoff (XMR-06)
    Refund(ctx, id uint64, sb string, cutoff uint64) (string, error) // nothing sent at h >= cutoff = t2 − 11 (MT-B27)
    Punish(ctx, id uint64, cutoff uint64) (string, error) // nothing sent at h >= cutoff = t2 + g2 − (11 + 8 + margin) (MT-J13)
    Reclaim(ctx, id uint64, sb string) (string, error)    // v3: the maker, from t2 + g2, at FixedFee
    ReadLock(ctx, id uint64) (Lock, error)   // every field, including xa/xb when revealed and st
    Deployment(ctx) (Deployment, error)       // U4 + MT-A03(a): the code's hash and w1min, w2min, g2, in one DERO.GetSC
    Registered(ctx, addr string) (bool, error)
    Balance(ctx, asset string) (uint64, error)
    Pay(ctx, to string, amount, port uint64) (string, error)                            // the stipend, port-tagged
    FindPayment(ctx, incoming bool, port, minHeight uint64) (Payment, bool, error)
}
// channel.go — the rendezvous; spore is the intended production transport, a directory of JSON files is the test one
type Channel interface { Send(ctx, to string, msg Message) error; Recv(ctx) (Message, error) }
type Message struct { Kind string; Body json.RawMessage }   // kinds: offer, alice_setup, bob_setup, dero_locked, xmr_locked, gate, done
// state.go — durable, fsync+rename, loaded strictly (mirror tools/vaultsigner's memory/intents)
// swap.go — two state machines
//   Bob:   setup → verified_pi_a(code pin + install policy, taker registered, funds; recovery card, wakes before t1 and t2 + g2) → dero_locking(no Lock past its cutoff) → dero_locked → stipend_sending → watching_xmr → xmr_confirmed(≥20)
//          → gate_sending → gate_sent → claimed(read s_a, stable) → sweep_ready → swept | refund_window → refunding(before t2 − 11) → refund_confirming(until stable) → refunded
//          | punish_window(from t2 − 11 with the lock open; sends no Punish of his own) → reclaiming(from t2 + g2) → reclaim_confirming(until stable) → reclaimed | punished
//   Alice: setup(registered) → offer_checked → verified_pi_b → lock_announced(code pin + g2 floor) → saw_dero_lock(stable, stipend stable) → xmr_locking
//          → xmr_locked → gate_received(before t1 − G) → claiming → claim_confirming(until stable) → claimed
//          | await_refund → refund_seen(read s_b; st 2 or st 4) → sweep_ready → swept
//          | punish_window(at once from t2) → punishing → punish_confirming(until stable) → punished | await_reclaim(past the punish cutoff) → refund_seen
```

**Exits watched to stable depth.** Alice counts her Claim and her Punish, and Bob his Refund and his Reclaim, only once
the block holding it is 8 deep (`claim_confirming`, `punish_confirming`, `refund_confirming`, `reclaim_confirming`). One
undone by a replaced block and not mined again is sent again while its window allows: a Claim before the claim cutoff, a
Punish before the punish cutoff, a Refund before the refund cutoff t2 − 11 (MT-B27), a Reclaim at any time, since from
t2 + g2 only the maker can move the lock (`TestTakerWatchesHerClaimUntilStable`, `TestTakerWatchesHerPunishUntilStable`,
`TestMakerWatchesHisRefundUntilStable`, `TestMakerWatchesHisReclaimUntilStable`). The Refund's watch reads the lock on
every pass rather than only once its height is stable, because its window closes 11 blocks before t2.

**Bob's refund cutoff (MT-B27).** A Refund built at height h executes by h + 10 or never, and one executing at or after
t2 is refused, which leaves the lock open. So Bob's companion sends no Refund, first time or fee re-send, from t2 − 11
(counting 11, one block conservative, as the Lock and Punish cutoffs do), and refuses at start a t2 − t1 of 11 or less,
which would leave no height to send one at (`TestBobRefundsAtHisLastHeight`, `TestBobSendsNoRefundAtTheCutoff`,
`TestDeroClientRefundStopsAtItsCutoff`). Alice's companion refuses the same window from a maker whose own does not, at the
offer and at each sight of the lock, whatever her T2Min (X-23, `TestTheTakerRefusesAWindowWithNoRefundHeight`). A Refund not sent by then, or undone by a replaced block once the height is
past the cutoff, is not sent at all. What that means for Bob, and his companion says it when it gets there: the lock
stays open, from t2 until t2 + g2 anyone may Punish it, which pays Alice his DERO and returns him nothing, and if nobody
does, his Reclaim from t2 + g2 returns it and puts `s_b` on the chain for her
(`TestARefundUndonePastItsCutoffIsLeftToPunishOrReclaim`). A Refund mined in the last 8 blocks before t2 can still be
undone by a replaced block and mined again at t2 or later, and refused; no cutoff removes that, it only keeps the
companion from sending one that cannot execute in time.

**A crash between a sent Punish or Reclaim and its record costs a fee, not coins.** `punishing` and `reclaiming` are
saved before the call and its result after it. A companion killed in between, or whose call returned an error after the
transaction went out, finds the lock still open on its next pass while that transaction is in the mempool, and sends
the call again: one executes, the other is mined and refused (st is no longer 0), and its fee, 3,000 atomic units
(`FixedFee`), is lost. The Lock's guard does not fit these two. Its `pending` memory lives in the process and does not
survive a crash; what protects the Lock across a restart — a settle wait (`LockSettleBlocks`), then the idempotent
lookup — would spend blocks of Alice's Punish window, which has a deadline, so that a Punish which never went out could
miss it. The exits' fee re-send already puts a second transaction out while the first may be pending (§4, R24), and
those re-sends are made inside the client with no durable record, so no restart could know when the last of them might
still be mined; for Bob's Reclaim, which has no deadline, a wait would cost only time, but it would need that record.
Nothing but the fee is at stake: every exit requires st 0, so at most one executes.

**A swap begun on htlc.bas v2 is not resumed by this build.** The command line refuses at start any contract whose code
is not v3's (U4), and a state file in `closing` — v2's maker closing an abandoned lock with his own Punish, a phase v3
does not have — is refused by name (`v2Phases`, `TestAV2SwapInFlightIsRefusedByName`), and `xmrswap status` reports it
as a divergence. v2 was installed only on the simulator (`deploy/networks.json` never recorded a testnet or mainnet
htlc), so this strands no testnet or mainnet swap; finish a simulator swap begun on v2 with the build that started it.

Invariants (§4) are asserted in tests, not assumed. `cmd/xmrswap` runs one side: `VEIL_RPC_LOGIN=<u>:<p>
XMRSWAP_MONERO_LOGIN=<u>:<p> xmrswap bob|alice -state <file> -net <network> -daemon <rpc> -dero <wallet-rpc>
-monero <wallet-rpc> -htlc <scid> -prover <absolute path> -channel <dir> -amount-dero N -amount-xmr M`, plus, on
mainnet, `-mainnet -monerod <loopback> -monero-wallet-dir <dir>`; `-net`, `-daemon`, `-dero`, `-monero` and
`-prover` have no defaults. `-prover` is the binary's absolute path, or on Windows the `wsl.exe` command that runs it
inside WSL (X-17, §3.C). `-daemon` may be given more than once: the first is the party's own node, and alice
locks no XMR until every further one, on the same DERO network, shows her the same lock (MT-B09). Both companions
read the swap's channel key from `XMRSWAP_CHANNEL_KEY` (`xmrswap channel-key` makes one; GAP-019) and do not start
without it. `xmrswap status -dir <dir> -daemon <rpc> [-monero <wallet-rpc>]` reconciles every state
file with the chains. `tools/simhtlc` drives a whole swap on the local DERO simulator, two companions in-process,
with the Monero side either the fake wallet (`-fake-monero`) or two real `monero-wallet-rpc` on a regtest `monerod`
(`-regtest`, `tools/regtest`), and exercises **all four exits**: happy path; Bob refunds and Alice recovers XMR with
`s_b`; Bob vanishes and Alice punishes; and (`-paths reclaim`) nobody punishes, Bob Reclaims from t2 + g2, and Alice,
restarted after it, recovers her XMR with the `s_b` it reveals. It prints every txid, looks every real Monero txid up on the `monerod`, and
reads every contract key back. With `-regtest`, `-paths reorg-before,reorg-after` makes a real reorganisation of the
Monero chain under a live swap (MT-B11): `pop_blocks` takes Alice's lock off the chain below Bob's depth, or after
he has released the gate, a longer branch is mined in its place, and in the second case Alice spends the lock's
inputs again on it (`research/regtest/xmrswap-reorg.log`). A DERO-side reorganisation cannot be made on the
simulator, which is one node mining its own chain.

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
2. **Bob never sends `k_b`** until the joint address shows ≥ the agreed amount at the confirmation depth of invariant 10 (twenty by
   default, never fewer than ten), read from his own view-only wallet, not from anything Alice says.
3. **Alice never locks XMR** until she has read Bob's lock from the chain and checked: `ha == SHA256(s_a)`,
   `hk` equals the `H_k` Bob committed to over the channel, `hb` equals Bob's committed `H_b` and `π_b` verified
   against it, `taker` is her address, `t1 − now ≥ T1_MIN`, `t2 − t1 ≥ T2_MIN`, amount ≥ agreed, asset as agreed.
4. **Bob never locks DERO** until `π_a` verifies against `(H_a, S_a)`.
5. **Durable state precedes every irreversible action** (lock, gate send, claim, refund, sweep); a restart
   resumes; a sweep is attempted at most once per swap (idempotent by swap id).
6. **The contract sends only after its last check** (mechanically enforced by `tests/sendorder_test.go`).
7. **Nothing is proxied and nothing is hosted**: both companions talk to their own wallets; the channel carries
   only what the table in §1 lists.

Added 2026-09-08 from the adversarial review in `docs/XMR_SWAP.md` §9 — each was a hole in the seven above:

8. **The gate requires an output Bob can actually spend, not merely a confirmed one** (review R5d, R6). Every
   Monero transaction carries `unlock_time`; Alice could lock the agreed amount with `unlock_time` years ahead
   and a companion that checked only confirmations would release `k_b`, claim the DERO, and leave Bob's XMR
   frozen at no cost to her. Bob's companion sends `k_b` only when his own view-only wallet reports the joint
   address's **unlocked** balance ≥ the agreed amount, every contributing transfer has `unlock_time == 0`, and
   no duplicate-output ("burnt") warning was raised — read from the wallet's balance and transfer entries, never
   summed from Alice's claims. `Incoming.Unlocked` and an `UnlockTime` field carry this; `FakeWallet` must be
   able to produce a locked-time output so the test can prove the gate refuses it.
9. **A Claim Bob acts on must be one DERO can no longer undo** (review R2). DERO may replace a block shallower
   than `STABLE_LIMIT = 8`. If Alice's Claim is mined in the last eight blocks before T1 and re-mined at or after
   T1, it is refused, the lock reopens, and Bob — already holding `s_a` — can Refund and reveal `s_b`: both then
   hold `s_a + s_b` and the XMR goes to whoever sweeps first. Two rules: Alice's companion never sends a Claim
   later than `t1 − 8 − margin` (default margin 8 blocks) and otherwise waits for Refund or Punish; Bob's
   companion never sweeps until the Claim's block is at or below the daemon's stable height.
10. **Confirmation depth before the gate is a parameter, defaulted above ten** (review R3). Monero has no
    consensus bound on reorganisation depth and an 18-block reorganisation has been reported; below ten
    confirmations nobody can spend the output anyway, so ten buys Bob nothing but time. Default 20; the
    companion refuses a lower value unless the operator sets it explicitly. This is the one assumption in the
    protocol that is policy rather than proof, and `docs/XMR_SWAP.md` §10 says so.
11. **No fee spent on a call the contract will refuse** (review R1). Alice's companion stops attempting Claim
    once `now + 1 ≥ t1 − 8 − margin`; Bob's stops attempting Refund once `now ≥ t2 − 11`, from where one could
    execute at or after t2 and be refused (MT-B27; the rule was once `now ≥ t2`); each moves to the exit that remains.
12. **Every contract call is sent at ring size 2, set by the companion itself** (review R16). At any other ring
    size `SIGNER()` is 33 zero bytes and line 21 of each entrypoint refuses the call — and for a **Lock**, which
    carries the DERO, derohe returns a refused call's value only when it can identify the sender, which it
    cannot above ring 2: `transaction_execute.go:397–404` — *"we could not extract signer, we burn all the
    funds"*. A Lock at the wrong ring size loses the DERO to nobody. The DERO client never inherits a wallet
    default; a test asserts the ring size on every request it builds.
13. **Every secret is fresh per swap** (review R20, R23): `s_a`, `s_b`, `k_b` and the view key pair are generated
    for one swap id and never reused. Reusing `s_a` publishes the second swap's key share at the first Claim;
    reusing `k_b` lets one counterparty claim another swap's DERO. The state file records the swap id each secret
    belongs to, and a companion refuses to start a swap whose secrets it has seen before. The same register holds,
    per role, the public identities a swap takes as its own — its joint address, the lock the taker accepted, every
    Monero payment the maker's gate counted — and a second swap from the same directory that is handed one of them
    is refused (`TestALockAlreadySeenFailsClosed`, `TestOneMoneroPaymentOpensOneGate`).

Added 2026-09-11 from `docs/ATOMIC_SWAP_STUDY.md` and the mainnet gap analysis:

14. **The taker's gas is a stipend from the maker** (Path 0). Bob sends it once his lock is stable and before Alice
    locks anything, at most once (a settle wait past DERO's 11-block transaction validity, then his own wallet's
    history, before any second attempt); Alice locks no XMR until it is in her wallet at stable depth and she can
    spend at least `StipendMin`. Default 14,016 atomic units: (3,000 + 504) × 4.
15. **A gate after `t1 − G` is not used** (U2). Bob releases `k_b` only while `height + G + 2 < t1`; Alice treats one
    that reaches her at or after `t1 − G` as never received. Default G = 100 (operator decision 5, re-confirmed 2026-09-12). Bob's
    side is tested on its own (`TestTheMakersGateMarginIsGPlusTwo`).
16. **The taker watches her Claim until it is stable** (U3), and a Claim undone by a replaced block sends her back to
    claiming (before the cutoff) or to waiting for the refund.
17. **Only the pinned contract and the pinned prover** (U4, GAP-003). The companion refuses an `-htlc` whose code does
    not hash to `HTLCCodeSHA256`, and a prover whose binary does not hash to `ProverSHA256`, named by an absolute path or, on Windows,
    by the `wsl.exe` command that runs it inside WSL, whose binary is read through the distribution's share (X-17); a verifier that
    prints a verdict must print it for the very statement asked about.
18. **Networks, endpoints and credentials fail closed** (GAP-007 to GAP-010). No network or endpoint has a default;
    mainnet is named and never mixed; on mainnet both wallet logins come from the environment and each wallet must
    refuse a call without them, and the wallets and the `monerod` are loopback, the `monerod` named to the wallet-rpc
    after every open.
19. **Every reply is bounded and read one way** (bounded input): a size cap and a timeout on every daemon, wallet and
    prover answer; trailing data, or neither a result nor an error, is refused. A Monero send counts only when the
    wallet's own record lists it: 0.18.5.1 answers a view-only wallet's transfer with a hash it never broadcast.
20. **The rendezvous is sealed** (GAP-019). Every message is AES-256-GCM under a per-direction key derived from the
    swap's channel key, agreed out of band; a message that does not open is dropped before the state machine sees it.
21. **A sweep interrupted by a kill is resumed, never abandoned** (MT-B12, XMR-R1). After a restart in `sweeping`
    the joint spend wallet's own record decides: a transfer out of it is the sweep, and is recorded; none, with
    something to spend, and the sweep is made again, which can only pay the party's own address.
22. **No secret reaches the output** (MT-L02). s_a, s_b, k_b, the view key, the joint spend key and the wallet
    passwords appear in no log line, error or outcome; the DERO and Monero clients withhold their own secret arguments
    from any error a server makes of the request (`redact.go`; `TestNoSecretReachesTheCompanionsOutput`,
    `TestTheClientsWithholdSecretsFromTheirErrors`).
23. **The taker gets a recovery card before her XMR moves, and a wake** (U9, MT-B15). The card names the state file as
    a secret, prints the resume command, and gives every height — the claim cutoff, t1 − G, t1, t2, the last height
    her companion sends Punish at and the Punish window's last block (t2 + g2 − 1) — as a clock time at 18 s a block,
    and says that past t2 + g2, with no Punish on the chain, her XMR comes back only if the maker Reclaims, and that
    anyone she trusts may send the Punish for her. The companion then asks the OS to wake the machine ten minutes before the first window closes and again ten
    minutes before t2, when her Punish window opens (MT-J13): a Windows Task Scheduler task with WakeToRun (run here,
    `TestRegisterWakeOnThisOS`), a systemd user timer with WakeSystem on Linux (registered, found
    and removed inside WSL's systemd 255 on 2026-09-12, X-23: `TestRegisterWakeOnThisOS`, `TestRegisterTheMakersWakesOnThisOS`
    with `XMRSWAP_TEST_WAKE=1`, leaving no `xmrswap-*` unit), and on macOS nothing,
    because only root can schedule a wake there. A finished swap removes both. The maker gets his own card before his
    DERO is locked (MT-B28): the state file as a secret, the resume command, and t1, the last height his companion
    sends a Refund at (t2 − 12), t2 and t2 + g2 as clock times, with a wake ten minutes before t1, when his Refund
    window opens, and another before t2 + g2, when, nobody having punished, only his Reclaim returns his DERO and her
    XMR. The same mechanism registers them, under task names of his own, and a finished swap removes both
    (`TestTheMakersRecoveryCardComesBeforeHisLock`, `TestRegisterTheMakersWakesOnThisOS`,
    `TestAFinishedSwapRemovesItsWake`).
24. **`-htlc` defaults to the record, never past the pin** (MT-A18). Omitted, it is the scid deploy/networks.json
    records for the daemon's DERO network (compiled in by `go generate ./xmrswap`, checked against the file by
    `TestRecordedHTLCIsTheDeployRecord`); given or recorded, the companion refuses it at start unless its code is the
    pinned htlc.bas v3 and every `-daemon` shows the same code and install arguments.
25. **The mainnet policy is one file** (MT-B16, BL-27). `tools/xmrswap/mainnet_profile.json` holds T1Min, the default
    t1, T1Max per confirmation tier (the tier's quote plus a 60-block slack, MT-B32), T2Min, the default T2 − T1, T2Max, the confirmation floor and tiers, G, k, the stipend floor, and
    htlc.bas v3's install arguments w1min, w2min and g2; the companion's defaults are generated from it
    (`go generate ./xmrswap` writes `profile_gen.go`), and `profile_test.go` fails when the file, the generated
    constants and the defaults disagree, checks the t1 rule at every tier, and checks that every tier's t1 meets
    max(T1Min, w1min + 11). Each value an operator decision still holds open is marked there with its MT-J number,
    and each answered one records the answer and its date (G 100 and g2 4,800, both answered 2026-09-12).
26. **htlc.bas v3's install is read and held to policy, and its windows are kept** (MT-A03(a), MT-J13). Each party
    reads the code's hash and `w1min`, `w2min`, `g2` in one `DERO.GetSC` (`Deployment`), from every configured node,
    before anything of its own goes in (`checkDeployment`). Bob refuses an install his agreed widths cannot meet
    (T1Delta below max(T1Min, w1min + 11), or T2Delta below w2min) and builds no Lock from `t1 − w1min − 10`, since the
    height-0 estimator would pass one the chain refuses (`TestMakerRefusesAnInstallHisWidthsCannotMeet`,
    `TestBobBuildsNoLockPastTheCutoff`). Alice refuses a `g2` below her floor (`TestTakerRefusesAShortPunishWindow`),
    punishes at once from t2, sends none from t2 + g2 − (11 + 8 + margin), and counts it only once stable
    (`TestTakerPastHerPunishCutoffWaitsForTheReclaim`, `TestTakerWatchesHerPunishUntilStable`). Bob sends no Punish of
    his own, Reclaims from t2 + g2 and counts it only once stable (`TestMakerReclaimsALockNobodyPunished`,
    `TestMakerWatchesHisReclaimUntilStable`); Alice treats st 4 exactly as st 2, which returns her XMR only if Bob
    reclaims: past t2 + g2 with no Punish, a Bob who never does leaves it, and his DERO, locked for ever.

Policy the companion states rather than proves (review R18, R19, R24): it warns loudly when `-daemon` or
`-monero` is not loopback, because a lying node is the one loss the protocol cannot see; it converts remaining
DERO blocks to minutes at the rate it has actually observed since the lock and refuses to lock XMR when the
remaining budget is below what its own path needs; and a height-gated call whose fixed fee does not mine is
re-sent with a higher fee — a Claim while `now + 1 < t1 − 8 − margin`, the claim cutoff itself (XMR-06), a Refund
while `now < t2 − 11` (MT-B27), a Punish while `now < t2 + g2 − (11 + 8 + margin)` (MT-J13) — then abandoned for the
exit that remains. A Reclaim has no end and is re-sent until Resends is spent.

---

## 5. Tests that must exist before "done"

- **A.** `tests/htlc_test.go` against the real DVM: happy path with both preimages; Claim without `kb`, with wrong
  `sa`, with wrong `kb`, by a non-taker, at height ≥ t1 — all refused; Refund before t1, at ≥ t2, by a non-maker,
  with a wrong `sb` — refused; Refund reveals `sb` and pays the maker; Punish before t2, after a Refund, after a
  Claim — refused; Punish pays the taker, whoever sends it (v2); a Lock with `taker == ""`, with `t1 <= now+1`, with
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
wsl.exe -e bash -lc 'REPO=<repository path in WSL> research/crosschain/poc/wsl/build.sh proof-xmr test --release'
cd tools && go run ./simhtlc -fake-monero -prover "<proof-xmr command>"   # all three exits, fake Monero
cd tools && go run ./simhtlc -regtest -prover "<proof-xmr command>"       # all three exits, real monero-wallet-rpc on tools/regtest
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
