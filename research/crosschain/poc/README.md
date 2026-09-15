# Cross-chain proofs of concept

Everything here runs from WSL (Ubuntu 24.04) because the Rust crates need a C toolchain that the Windows side
lacks. Helper scripts live in `wsl/`; targets are built under `~/target/<crate>` inside WSL to keep the repo clean.

| directory | what it proves | run | result |
|---|---|---|---|
| `proof-xmr` | SHA256(s)=H ∧ s·G=S on ed25519, Bulletproofs + sigma protocol — since 2026-09-08 a tool: `keygen` / `prove <secret>` / `verify <h> <s> <proof>` with a fixed byte layout and a worked vector (`proof-xmr/README.md`); `prove` refuses an unreduced scalar | `wsl/build.sh proof-xmr build --release` then `wsl/run.sh proof-xmr proof-xmr keygen`; `cargo test --release` covers round trip, wrong H, wrong S, tampered sigma and R1CS, truncation, the reduced-scalar refusal | `proof-xmr/run.log`: 37,460 gates, prove 8.5 s / verify 0.74 s (release, this machine), 1,601 B; the companion invokes it through `tools/xmrswap` `NewProverCLI` |
| `vendor/bulletproofs` | dalek bulletproofs 5.0.0 with the 3-line `CtOption` fix for the R1CS feature | vendored by `wsl/vendor-bulletproofs.sh` | — |
| `frost-zec` | 2-of-3 re-randomized FROST RedPallas under a valid Orchard `ak`; per-index deposit addresses | `wsl/run-zec-demos.sh` | `frost-zec/run.log` |
| `zec-t-htlc` | transparent-Zcash HTLC (SHA256 + CLTV, P2SH) under the consensus interpreter | `wsl/run-zec-demos.sh` | `zec-t-htlc/run.log` |
| `../../../contracts/vault.bas` | k-of-n federation wrapper on DERO | `tests/run.sh -run Vault -v` | `tests/vault_test.go` |

**Removed 2026-09-15**, when the operator ruled that the ZEC and XMR atomic swaps are removed (`docs/MAINNET_TODO.md`
Phase Y):
- `swap.bas`, `board.bas` and their tests: the Phase 1 hash-time-locked escrow and offer board. `simswap-run.log`,
  their simulator run, went with them.
- `proof-zec`, the Orchard `ask`-share proof (12.2 s / 0.47 s), and the scripts that built it and its Pallas port:
  `wsl/proof-zec-run.sh`, `wsl/versions.sh` and `wsl/bp-pallas-*`. The port they generated, `vendor/bp-pallas`, was
  never tracked.

All of them are in history at `c58e3e3`, for example `git show c58e3e3:research/crosschain/poc/proof-zec/run.log`.
`proof-xmr` with `wsl/vendor-bulletproofs.sh`, and `zec-t-htlc`, go later with the swap companions (Y2-6).
`frost-zec` stays as research.

Toolchain notes: `rustup` in WSL (`curl https://sh.rustup.rs | sh -s -- -y`); `MSYS_NO_PATHCONV=1` before `wsl`
when calling from Git Bash so `/mnt/c/...` paths survive; Windows-side `cargo` cannot link (no `cl.exe`, and the
GNU toolchain lacks `gcc.exe` for the `cc` build script of `clear_on_drop`).
