# Regtest harness — pinned artifacts

Every binary the harness runs is pinned here and in `pins.env` (which the scripts read). A script refuses to start a
binary whose sha256 differs. `tools/regtest/fetch.sh` repeats every verification below from scratch.

Verified 2026-09-11 on the operator's Windows 10 machine (Git Bash gpg 2.4.8; WSL Ubuntu 24.04).

## Monero 0.18.5.1 "Fluorine Fermi" — official CLI release

| | |
|---|---|
| Version | `v0.18.5.1-release` (`monerod --version`, `monero-wallet-rpc --version`) |
| Hash list | https://www.getmonero.org/downloads/hashes.txt — sha256 `6d302c2becbd529822e8f6ef422e3f7a42045f8386e85251dbbf4b1e913266c4` |
| Signer key | https://raw.githubusercontent.com/monero-project/monero/master/utils/gpg_keys/binaryfate.asc |
| Signer fingerprint | `81AC 591F E9C4 B65C 5806  AFC3 F0AF 4D46 2A0B DF92` (binaryFate <binaryfate@getmonero.org>, rsa4096, 2019-12-12) |
| Artifact used | https://downloads.getmonero.org/cli/monero-linux-x64-v0.18.5.1.tar.bz2 — sha256 `22a7dda7b0cb699fdd6b7674c3b4a4465b337cc98a54983523b759e1e7cc9958` |
| Extracted `monerod` | sha256 `9b3b2676ea7868c1a7186feea9569c2cf7683ae79d2fcc769c846a91c810a1f5` |
| Extracted `monero-wallet-rpc` | sha256 `c1e3aff7c72837e6f29045c439b772a82b5cd7324c8b831fa825a6ce2019a656` |
| Also downloaded and verified | `monero-win-x64-v0.18.5.1.zip` — sha256 `cf2ae8273977697d9ef2031c7337b781e6e5936578f602444b2990a173a2437d` (matches the signed list; not used, see below) |

Signature transcript (isolated GNUPGHOME `vendor/regtest/gnupg`, key imported from the repository file above):

```
gpg: Signature made Tue Jul 21 05:50:38 2026 AUSEST
gpg:                using RSA key 81AC591FE9C4B65C5806AFC3F0AF4D462A0BDF92
gpg: Good signature from "binaryFate <binaryfate@getmonero.org>" [unknown]
gpg: WARNING: This key is not certified with a trusted signature!
Primary key fingerprint: 81AC 591F E9C4 B65C 5806  AFC3 F0AF 4D46 2A0B DF92
```

The WARNING is GnuPG's web-of-trust notice: the key is not signed by a key this keyring trusts. The trust anchor is
instead the key file in monero-project/monero, fetched over HTTPS from GitHub, and its fingerprint, which equals the
one Monero publishes. `fetch.sh` checks the `VALIDSIG` status line against the pinned fingerprint.

**Why the Linux build and not the Windows one.** The Windows x64 zip verified, but Windows Defender quarantined
`monero-wallet-rpc.exe` seconds after extraction (`Trojan:Win32/Wacatac.A!ml`, a machine-learning heuristic). It then
quarantined the Linux `monerod` too, wherever it sat on the Windows drive (`Trojan:Linux/CoinMiner.T!MTB`,
2026-09-11 15:23 and 15:25). Both files are byte-for-byte the ones binaryFate's signed list names, so these are false
positives. The harness does not add antivirus exclusions — that is the operator's decision. So `monero-up.sh`
extracts the verified tarball into the WSL distro's own ext4 filesystem (`$HOME/.dmz-regtest/monero-v0.18.5.1`), which
Defender's real-time scan of Windows drives does not reach, and re-checks both binaries' hashes on every start. The
pinned artifact itself stays in `vendor/regtest/dl/`. With an exclusion for `vendor/regtest`, the Windows build would
work the same way.

## Zebra 6.3.0 — Zcash Foundation release binary

| | |
|---|---|
| Version | `zebrad 6.3.0` (`zebrad --version`); released 2026-08-10 |
| Hash list | https://github.com/ZcashFoundation/zebra/releases/download/v6.3.0/SHA256SUMS — sha256 `3eb5de0634f637e793d0411b6c7108802a36e1219f9151803ecc6108fd0f59f6` |
| Signature | https://github.com/ZcashFoundation/zebra/releases/download/v6.3.0/SHA256SUMS.sigstore.json (sigstore bundle v0.3, keyless) |
| Signer identity | `https://github.com/ZcashFoundation/zebra/.github/workflows/zfnd-release-binaries.yml@refs/tags/v6.3.0`, OIDC issuer `https://token.actions.githubusercontent.com`, source commit `f5c5277fe41eba9c74f37098738f93f35dd70d60`, Rekor log index 2412071838 |
| Artifact used | https://github.com/ZcashFoundation/zebra/releases/download/v6.3.0/zebrad-6.3.0-x86_64-unknown-linux-gnu.tar.gz — sha256 `86326f5324f4e59cc2008c15f94407cc8d5feacf75d64942164bb5f08eca8c5e` (also equals the release's own `.sha256` file) |
| Extracted `zebrad` | sha256 `5e74b3b0fae286394e9c0ddd70d2014360816fdebf01b39dd3e3a6691d6a6e63` (runs from `vendor/regtest/zebra/zebrad` inside WSL) |

Zebra does not sign its releases with PGP. It signs `SHA256SUMS` keylessly with sigstore from its release workflow.
The bundle was verified with sigstore-python in WSL:

```
$ python3 -m sigstore verify identity --bundle SHA256SUMS.sigstore.json \
    --cert-identity https://github.com/ZcashFoundation/zebra/.github/workflows/zfnd-release-binaries.yml@refs/tags/v6.3.0 \
    --cert-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS
OK: SHA256SUMS                                    (sigstore 4.5.0)
```

This checks the Fulcio certificate chain to the sigstore trust root, the certificate identity and issuer above, the
Rekor transparency-log inclusion, and the signature over the file's digest (`PrXe…` = `3eb5de06…59f6`, which equals
the sha256 of the downloaded `SHA256SUMS`). An earlier attempt with sigstore 3.6.7 also printed `OK: SHA256SUMS`, but
logged a failed RFC 3161 timestamp check and "Failed to load a trusted root key: unsupported key type: 7": that
version is too old for the current trust root. The clean 4.5.0 result is the one relied on. The harness did not
build zebrad from source, because a signed release binary exists.

zcashd was not used. It reached its end-of-support halt on 2026-07-18 and does not follow NU6.3.

## zfund — the harness's own Zcash key/transaction tool (built, not downloaded)

`tools/regtest/zfund` is built from source with `cargo build --release --locked` in WSL (rustc/cargo 1.98.1). Its
`Cargo.lock` pins every crate with its crates.io checksum. The ones that matter as an independent implementation:

| crate | version |
|---|---|
| zcash_primitives | 0.30.1 |
| zcash_transparent | 0.10.0 |
| zcash_protocol | 0.10.6 |
| zcash_address | 0.13.0 |
| sapling-crypto | 0.7.0 (its mock provers only; a transparent-only build never calls them) |
| orchard | 0.15.5 (linked, unused) |
| secp256k1 | 0.29.1 |

The built binary is copied to `vendor/regtest/bin/zfund`. It is a test tool with no release to pin against, so its
hash is not pinned: the build is locked, but not reproducible byte for byte.

## Zallet 0.1.0-beta.3 — the Zcash testnet faucet wallet (an operator's tool)

A shielded wallet that receives faucet TAZ and sends it on, transparent, to the taker's `tm…` key. It is never in a
DMZ runtime path and never on mainnet; why it and not a smaller tool is in `docs/ZEC_SWAP.md` §11.1. No harness script
runs it. Its launcher, `run-zallet.sh`, lives in the wallet's datadir (README.md) and refuses a `zallet-zaino` whose
sha256 is not the one below. Verified 2026-09-12 in WSL Ubuntu 24.04.

| | |
|---|---|
| Version | `zallet 0.1.0-beta.3` (`zallet-zaino --version`, `zallet --version`); released 2026-08-26, marked beta |
| Source | https://github.com/zcash/zallet (formerly `zcash/wallet`), tag `v0.1.0-beta.3` = commit `987382f67e622915228686e9f956c6a9c9a7514c` |
| Artifact used | https://github.com/zcash/zallet/releases/download/v0.1.0-beta.3/zallet-v0.1.0-beta.3-linux-amd64.tar.gz — sha256 `1df2398df016ae6e9a1cba3f8ef0cc86ca0d7b48a0934b34df6f68114ebe9306` (equals the digest GitHub's release API lists) |
| Extracted `zallet-zaino` | sha256 `e602e4a3d4e919c155b8f05dae29f0b9311631f167eaf25455166edf268208a7` — the one that runs (static-pie, x86-64) |
| Extracted `zallet` (launcher) | sha256 `523bf86a14cba2dfb1ea048c3a3d6ccc68bde0dbb92da60d44ce12568dea1c5a` — not used |
| Extracted `zallet-zebra` | sha256 `82cca947c0616a18ec884a73e986f30f87c160be5102de976182509b4cdb964a` — not used: that backend needs a zebrad built with the `indexer` feature |
| Signature | `….tar.gz.asc` — sha256 `b8c8c71c0fe7db0bf6dba825a19a929864994b2c257d4719bb50e6aec9373755` |
| Signer key | https://apt.z.cash/zcash.asc (sha256 `7ff4e13eadd162eaaf165407704a9bfd2cdf32ffc7e51a667d8017802cd84da5` when fetched; it also holds the ECC master key) |
| Signer fingerprint | `0338 34DD 49DE CF9D BB99  34BC 6C93 CA8E 58E2 6AB1` (Zcash Open Development Lab (ZODL) <sysadmin@zodl.com>, rsa4096), signing subkey `1FE9 9324 758F 2967 18B4  5706 7F4B BBBA 23F0 617F` |
| Provenance | `….tar.gz.intoto.jsonl` — sha256 `e39503675b98e79c48ba22973fb25b1ea1af1c7faa22d0f301c14db0cb1843c2`; a sigstore bundle v0.3 holding an in-toto SLSA v1 statement, Rekor log index 2596324254 |

Signature transcript (throwaway GNUPGHOME, key imported from the file above):

```
gpg: Signature made Wed Aug 26 15:37:46 2026 AEST
gpg:                using RSA key 1FE99324758F296718B457067F4BBBBA23F0617F
gpg:                issuer "sysadmin@zodl.com"
gpg: Good signature from "Zcash Open Development Lab (ZODL) (Dallas, Texas) <sysadmin@zodl.com>" [unknown]
[GNUPG:] VALIDSIG 1FE99324758F296718B457067F4BBBBA23F0617F 2026-08-26 1787722666 0 4 0 1 8 00 033834DD49DECF9DBB9934BC6C93CA8E58E26AB1
Primary key fingerprint: 0338 34DD 49DE CF9D BB99  34BC 6C93 CA8E 58E2 6AB1
```

The trust anchor is the key file on apt.z.cash, fetched over HTTPS; Zallet's book (*Supply Chain Security*) names it
as the key to verify against. keys.openpgp.org serves a key under the same fingerprint, but with no verified user ID,
so that is a weak second source. The provenance does not depend on the PGP key. It was verified with the same
sigstore-python as zebrad's:

```
$ python3 -m sigstore verify identity --bundle zallet-v0.1.0-beta.3-linux-amd64.tar.gz.intoto.jsonl \
    --cert-identity https://github.com/zcash/zallet/.github/workflows/binaries-and-deb-release.yml@refs/tags/v0.1.0-beta.3 \
    --cert-oidc-issuer https://token.actions.githubusercontent.com zallet-v0.1.0-beta.3-linux-amd64.tar.gz
OK: zallet-v0.1.0-beta.3-linux-amd64.tar.gz      (sigstore 4.5.0)
```

The statement's subject is the tarball's sha256 above. Its builder is the `binaries-and-deb-release.yml` workflow at
the tag, and its one resolved dependency is `git+https://github.com/zcash/zallet@refs/tags/v0.1.0-beta.3` at commit
`987382f6…514c`. The tag is lightweight (it points at the commit, with no tag signature), so the commit is known from
the provenance and GitHub's ref, not from a signed tag. Zallet was not built from source, because a signed,
attested release exists. The book states that the amd64 release is a reproducible StageX build; that was not
re-checked here.
