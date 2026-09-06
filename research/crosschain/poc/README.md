# Cross-chain proofs of concept

Everything here runs from WSL (Ubuntu 24.04) because the Rust crates need a C toolchain that the Windows side
lacks. Helper scripts live in `wsl/`; targets are built under `~/target/<crate>` inside WSL to keep the repo clean.

| directory | what it proves | run | result |
|---|---|---|---|
| `swap.bas`, `board.bas`, `*_test.go` | DERO hash-time-locked escrow and offer board (Phase 1) | `tests/run.sh -run 'Swap|Board'` | `simswap-run.log` (simulator txids) |
| `proof-xmr` | SHA256(s)=H ∧ s·G=S on ed25519, Bulletproofs + sigma protocol | `wsl/build.sh proof-xmr build --release` then `wsl/run.sh proof-xmr proof-xmr` | `proof-xmr/run.log`: 37,460 gates, 14.8 s / 1.15 s, 1,601 B |
| `vendor/bulletproofs` | dalek bulletproofs 5.0.0 with the 3-line `CtOption` fix for the R1CS feature | vendored by `wsl/vendor-bulletproofs.sh` | — |
| `vendor/bp-pallas` | the same R1CS system on the Pallas curve (types, MSM, generators, parallel fold) | `wsl/bp-pallas-test.sh` | 2 unit tests |
| `proof-zec` | the same statement for an Orchard `ask` share; joint `ak`; RedPallas signature with the sum | `wsl/proof-zec-run.sh` | `proof-zec/run.log`: 12.2 s / 0.47 s |
| `frost-zec` | 2-of-3 re-randomized FROST RedPallas under a valid Orchard `ak`; per-index deposit addresses | `wsl/run-zec-demos.sh` | `frost-zec/run.log` |
| `zec-t-htlc` | transparent-Zcash HTLC (SHA256 + CLTV, P2SH) under the consensus interpreter | `wsl/run-zec-demos.sh` | `zec-t-htlc/run.log` |
| `../../../contracts/vault.bas` | k-of-n federation wrapper on DERO | `tests/run.sh -run Vault -v` | `tests/vault_test.go` |

Toolchain notes: `rustup` in WSL (`curl https://sh.rustup.rs | sh -s -- -y`); `MSYS_NO_PATHCONV=1` before `wsl`
when calling from Git Bash so `/mnt/c/...` paths survive; Windows-side `cargo` cannot link (no `cl.exe`, and the
GNU toolchain lacks `gcc.exe` for the `cc` build script of `clear_on_drop`).
