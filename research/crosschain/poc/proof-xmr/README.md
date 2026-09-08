# proof-xmr — the setup proof of the DERO ↔ Monero swap, as a tool

**Statement.** "I know `s` such that `SHA256(s) = H` and `s·G = S` on ed25519." Alice proves it for her share
`s_a`, Bob for `s_b`, before either locks anything (`docs/XMR_SWAP_BUILD.md` §1, §3.B; the design is
`research/crosschain/RESEARCH.md` §4.4). Without it, a party could reveal a DERO preimage that is not a Monero
key share and the other side's XMR would be unspendable. No trusted setup: a Bulletproofs R1CS proof that the
value under a Pedersen commitment `C` hashes to `H` (37,460 multiplication gates, `src/sha256_gadget.rs`), plus a
Chaum–Pedersen sigma proof that the same committed value is the discrete log of `S`. The statement, the transcript
labels (`veil.xmr.sha256-dl.v1`, `veil.xmr.sigma.v1`), `GENS = 2^16` and the sigma protocol are the prototype
measured on 2026-09-06 and are frozen: a proof made by one build must verify under another.

## Build and run (WSL)

```
wsl.exe -e bash -lc 'REPO=/mnt/c/Users/Wilma/Desktop/fomo research/crosschain/poc/wsl/build.sh proof-xmr build --release'
wsl.exe -e bash -lc 'REPO=/mnt/c/Users/Wilma/Desktop/fomo research/crosschain/poc/wsl/build.sh proof-xmr test --release'
```

The binary lands in `/home/wilma/target/proof-xmr/release/proof-xmr`; the companion invokes it as
`wsl.exe -e /home/wilma/target/proof-xmr/release/proof-xmr …` (`tools/xmrswap`, `NewProverCLI`). Run the tests
with `--release`: the prover is seconds in release and minutes in debug (`Cargo.toml` sets `opt-level = 3` for the
dev profile too, so an unflagged `cargo test` is merely slow rather than hopeless).

## The command line

```
proof-xmr keygen                              stdout {"secret":hex32,"h":hex32,"s":hex32}
proof-xmr prove  <secret_hex>                 stdout {"h":hex32,"s":hex32,"proof":hex}
proof-xmr verify <h_hex> <s_hex> <proof_hex>  exit 0 valid, exit 1 invalid with one line why on stderr
```

| exit | meaning |
|---|---|
| 0 | `keygen`/`prove`: the JSON is on stdout, one line. `verify`: the proof is valid for `(H, S)`; nothing is printed. |
| 1 | `verify` only: the proof is not valid for `(H, S)` — wrong length, unparsable, a point that does not decompress, `S` with torsion or the identity, the R1CS or a sigma equation failing. One line on stderr says which. |
| 2 | Refused invocation: usage, an argument that is not hex, `h`/`s`/`secret` not 32 bytes, or a secret that is not a reduced scalar. One line on stderr; nothing on stdout. |

Every value is 32 bytes as 64 lower-case hex characters except `proof`, which is 1,601 bytes (3,202 characters).
The JSON is exactly the shape shown, one line, newline-terminated, no whitespace; the fields are hex strings so no
escaping can arise, which is why the tool formats it by hand and `serde_json` is not a dependency.

- **`secret`** is the little-endian 32-byte scalar **already reduced mod ℓ** (Monero's `sc_check`: high bit clear,
  value below ℓ). `keygen` guarantees it (a uniformly random scalar). `prove` **refuses** an unreduced secret with
  exit 2 rather than reducing it: the DERO contract hashes the exact bytes (`HEX(SHA256(HEXDECODE(sa))) == ha`) and
  the Monero wallet reduces the spend key mod ℓ, so an unreduced secret would hash to one `H` and sign as a
  different key — the proof would be honest about a secret nobody holds. `ℓ − 1` is accepted; `ℓ`, `ℓ + 1`, all
  ones and anything with bit 255 set are refused. The refused value is never echoed.
- **`s`** is the compressed ed25519 point `s·G`; `verify` requires it to decompress to a prime-order, non-identity
  point, as Monero keys are.
- `prove -` reads the secret's hex from stdin instead of argv, for a caller that would rather not put it in the
  process list. Same output.
- `PROOF_XMR_TIMING=1` adds one `timing: …` line on stderr (generators, prove or verify, proof size); `run.log`
  is recorded with it. Off by default so a failing `verify` has exactly one line on stderr.
- Each invocation builds the 2^16 Bulletproofs generators first (≈ 1.6 s here); there is no daemon to keep them
  warm, by design — the tool holds nothing between calls.

Nothing but the JSON goes to stdout. A secret is written only by `keygen`, as its output, and never appears in a
message. `prove` prints `h`, `s` and the proof, not the secret.

## Proof byte layout

`proof` = `R1CSProof::to_bytes()` ‖ `C` ‖ `A1` ‖ `A2` ‖ `z1` ‖ `z2`. The circuit is fixed, so every length is fixed:
the R1CS part is `1 + 32·45 = 1,441` bytes (one version byte, 8 points, 3 scalars, 16 (L, R) pairs for 2^16
padded gates, 2 scalars) and the whole proof is `1,441 + 32 + 4·32 = 1,601` bytes. `verify` refuses any other
length before parsing anything. Ristretto points are the `bulletproofs` crate's compressed encoding; `A2` is a
compressed Edwards point (`S` lives on ed25519 proper); scalars are canonical little-endian and a non-canonical
`z1`/`z2` is refused at parse time.

| byte offset | size | field | encoding |
|---|---|---|---|
| 0 | 1 | version | `0x00` = one-phase R1CS proof (the gadget has no randomized phase) |
| 1 | 32 | `A_I1` | compressed Ristretto |
| 33 | 32 | `A_O1` | compressed Ristretto |
| 65 | 32 | `S1` | compressed Ristretto |
| 97 | 32 | `T_1` | compressed Ristretto |
| 129 | 32 | `T_3` | compressed Ristretto |
| 161 | 32 | `T_4` | compressed Ristretto |
| 193 | 32 | `T_5` | compressed Ristretto |
| 225 | 32 | `T_6` | compressed Ristretto |
| 257 | 32 | `t_x` | scalar |
| 289 | 32 | `t_x_blinding` | scalar |
| 321 | 32 | `e_blinding` | scalar |
| 353 | 1,024 | `L_0 R_0 … L_15 R_15` | 16 pairs of compressed Ristretto (the inner-product argument) |
| 1,377 | 32 | `a` | scalar |
| 1,409 | 32 | `b` | scalar |
| 1,441 | 32 | `C` | compressed Ristretto: the Pedersen commitment to `s` |
| 1,473 | 32 | `A1` | compressed Ristretto: sigma commitment `k1·B + k2·B_blind` |
| 1,505 | 32 | `A2` | compressed Edwards Y: sigma commitment `k1·G` |
| 1,537 | 32 | `z1` | scalar: `k1 + c·s` |
| 1,569 | 32 | `z2` | scalar: `k2 + c·r` |
| 1,601 | | end | |

The sigma challenge `c` is `veil.xmr.sigma.v1` over `H, S, C, A1, A2`; the R1CS transcript is `veil.xmr.sha256-dl.v1`
with `H` and `S` appended before the commitment, so a proof is bound to its `(H, S)` — replaying it against another
statement fails.

## Worked vector

Recorded by `record.sh` on 2026-09-08 (`vector.txt`: secret, `h`, `s`, proof — one per line; `tests/vector.rs`
verifies it and checks that this section shows the same bytes). Proofs are randomised (`r`, `k1`, `k2`), so a
fresh `prove` of the same secret gives different bytes that verify just the same.

```
secret = 055337b36a03cd1a8117ecb159f2eb5181f9ad24ff046c9f4ef285a58564d90d
h      = 34d34544729e37868b65aa6b51fba4177f0e46f1392ff1e31eb03af70735743d
s      = 6f5fa3e4b3fd55fa0a9a9a54b275aa163af0bc6d2adb96d5bbbb4bd588ba6b33
```

The proof, split by the table above:

| byte | field | hex |
|---|---|---|
| 0 | version | `00` |
| 1 | `A_I1` | `2ae767e5b58a2f4d814a8f504035863289ad9b4fcc4b466baeed81196cb2bc72` |
| 33 | `A_O1` | `2c69b64193d03ce20ea7d8c0bd438c58431e16947af615da249e9a893c9ab91a` |
| 65 | `S1` | `f016ce4a21f9d483fed442ecd30d0c6e177a01c1af766fa044ff74b3f3fb6f50` |
| 97 | `T_1` | `161c54f2e7ad2e7d0592ab6b7c3f5308f61d8fc97e4b8d342643dec68fb6f816` |
| 129 | `T_3` | `4e05880a934fb5946e421302c85ecf247de93458ca0ee3ec68677c42cc04f564` |
| 161 | `T_4` | `088160176077253ca623b831920a6863ab1d4ff0b5c9aefa8c26974aa8018f01` |
| 193 | `T_5` | `de45f3455949309511b35881463ca21311bbafcd8a9200e0870eb4c73407f21f` |
| 225 | `T_6` | `eae8ae31a3136ea8cd4f13cc7cb085b5cff8e83b3e410b6591a45fe68bd7ea4e` |
| 257 | `t_x` | `46d9a2f1b68af74c0f13c9fcb459ce488fdb0ac354b96e97736672a68225390d` |
| 289 | `t_x_blinding` | `cb2fec0ac4c1f9d61d5df88e13af425f1b9c122352a4bebac71113be77229d01` |
| 321 | `e_blinding` | `849ad9ae363251ab3ba1e2f09ff1d15f1930265d5d2fb0a650aff55cea03ed06` |
| 353 | `L_0` | `be2422f94c449bc3dc4b0ed34f3a3f57554f13815f649b27d29ed4492ee21633` |
| 385 | `R_0` | `8a16509aa12532d93a45790183b4a3fa13582fe59468c5c496ff012d23f33224` |
| 417 | `L_1` | `b42ff7579e955802a21fe7446a1280df2d8ce5d89b217d5a0b03c309dda7ea7e` |
| 449 | `R_1` | `4401f2af6ce1a8a2db51cf4726f487e959c2f548ae272f8b2c6c9b34ab497601` |
| 481 | `L_2` | `ead1a7102dbaac49fa49ec7ff42dbe3ef18adc7f8af5115cc5502990788fc808` |
| 513 | `R_2` | `c672952e43b919d769e2e0bcaa9d9465913eb0a3e6895a43002c05fb966d4c6e` |
| 545 | `L_3` | `026506d0ae0c1d4c12affac0171075f2bd82b307105cf3599e627f60436bfc46` |
| 577 | `R_3` | `12d4bf39492d1c0219ff133506d1bc11c058370b55a702406502ff358e97d802` |
| 609 | `L_4` | `28dd21cef78612005f38daf49e256e762cc5cacabf02fbae6792fa36c0fbb60e` |
| 641 | `R_4` | `9c2e6022a97412e4993ffb9f70697cd06a2dae1259ec11d690a922ea73e8d73a` |
| 673 | `L_5` | `cc230d102fa8f991bd3a26198c4c4a00a6f4b8eff9574382b375d2f6a7b9eb37` |
| 705 | `R_5` | `5adfca17ddb3e559e9a8ab435d8d48de8c08b24de36168c316e40d3df4a8df0c` |
| 737 | `L_6` | `da8502cdc5c727c918d38c303987618bfbe97d4fb5f66f9b334a877d6995a26f` |
| 769 | `R_6` | `ea16bdd3e353140937ede38145e7a0744c00a229157b212b861988b83c0fde1c` |
| 801 | `L_7` | `a0e5b6e57b0303949b36b9b85d659f218ebd9cc30422f10b3c3ae4596e22bd63` |
| 833 | `R_7` | `069a49d46d810dc4dcc208b3abd647e06a0f26ee41ab9c49e4a38cecfc4d981a` |
| 865 | `L_8` | `44a70c3d2cefdbe2274f4c8c1b472d775e4d58a25a6872addb61f29af933a05f` |
| 897 | `R_8` | `38a5857cf4fd63822836e1651124c8f4ca41b329e1d1493ddbc7bc274a167877` |
| 929 | `L_9` | `c60e9bcfa51bf0da3c4f6ff12aa133c28af78dc8923c372fcb3a43ef2e399d38` |
| 961 | `R_9` | `c4982ee3dd98f0999aea30d322d1718197596f0e0a5da67fdd180069abb27576` |
| 993 | `L_10` | `8cc705ee06053c6673837d3914224030c25b2c9a70e8057a77e2495e4f5c5959` |
| 1025 | `R_10` | `feda4ac890a64cee6851d9192d3db25f5d31dc3afb05d976c3e2d5a39bb7ec4d` |
| 1057 | `L_11` | `d21e05b09c2f693ad0f1619349036bcaeccf70fc199f7f0784cf7dbf8bfe6478` |
| 1089 | `R_11` | `5c20af2c8da32fb51d9924c488e4a51757e1cefed99eea4fb56c3d1f448c7d23` |
| 1121 | `L_12` | `860c0d248804d53fa1d2da2b1e08d7e3581c5d81a018ab9eaf1c7ef6b4085f30` |
| 1153 | `R_12` | `d8a3c7a886dd163dc57b38d7dc6859f5034e9fc74daac87b2274efe0c9eaa802` |
| 1185 | `L_13` | `866b45d5e809a54fe7935f9856bbb202f2b4d7c9bb7a061da3160e8764d81557` |
| 1217 | `R_13` | `c2bef4b064c11ba97c111bf7067c085071069aaaace14b9d109b1460a7138e3e` |
| 1249 | `L_14` | `50bd0ab508697274092b4a31dbac06f247b37c6fff833a334706a38e5778b50e` |
| 1281 | `R_14` | `467c2166fd1e60317e83dbdae82065c818b10b50bded5cbda04646eb1e6d531d` |
| 1313 | `L_15` | `f00d41cd75835e17dc7c4c1f8ec3c6bcd2c434e07f576fa4607484896a92395a` |
| 1345 | `R_15` | `8806fdf26ca0f5bdee0fb503258353a3e7f4a2a3b97258adf30e8b5ca7757421` |
| 1377 | `a` | `c61e98e4f4552ad424a0fd0826d4c8a904dc5b73c2378fc667aeed617d603d05` |
| 1409 | `b` | `25aee11e334527f0ffe373837aa27f69cbe088fe2f6f2519033ef8ded4bea200` |
| 1441 | `C` | `8aa590eb8e31657e5d3758023907e3a858e53d7f159e6c8dd20837a19bb5a54b` |
| 1473 | `A1` | `7a9807cdc987c9641385fe9a690736e0ecb2d23abf4e4ad91d5f83797f7d6858` |
| 1505 | `A2` | `a587553fff8f8ccdbd96953ca1ba76d4a95b3cf0d4c08c0ed351eb370d332a17` |
| 1537 | `z1` | `390b15b939237082731be74a7c09a0c82e28604930903329a0a834312957c90c` |
| 1569 | `z2` | `ef3d7b660605943ea3f157fbcfd618d271f1f1978b4d8c3d62ef9aa858d9430e` |

As one string, what `prove` prints and `verify` takes:

```
002ae767e5b58a2f4d814a8f504035863289ad9b4fcc4b466baeed81196cb2bc722c69b64193d03ce20ea7d8c0bd438c58431e16947af615da249e9a893c9ab91af016ce4a21f9d483fed442ecd30d0c6e177a01c1af766fa044ff74b3f3fb6f50161c54f2e7ad2e7d0592ab6b7c3f5308f61d8fc97e4b8d342643dec68fb6f8164e05880a934fb5946e421302c85ecf247de93458ca0ee3ec68677c42cc04f564088160176077253ca623b831920a6863ab1d4ff0b5c9aefa8c26974aa8018f01de45f3455949309511b35881463ca21311bbafcd8a9200e0870eb4c73407f21feae8ae31a3136ea8cd4f13cc7cb085b5cff8e83b3e410b6591a45fe68bd7ea4e46d9a2f1b68af74c0f13c9fcb459ce488fdb0ac354b96e97736672a68225390dcb2fec0ac4c1f9d61d5df88e13af425f1b9c122352a4bebac71113be77229d01849ad9ae363251ab3ba1e2f09ff1d15f1930265d5d2fb0a650aff55cea03ed06be2422f94c449bc3dc4b0ed34f3a3f57554f13815f649b27d29ed4492ee216338a16509aa12532d93a45790183b4a3fa13582fe59468c5c496ff012d23f33224b42ff7579e955802a21fe7446a1280df2d8ce5d89b217d5a0b03c309dda7ea7e4401f2af6ce1a8a2db51cf4726f487e959c2f548ae272f8b2c6c9b34ab497601ead1a7102dbaac49fa49ec7ff42dbe3ef18adc7f8af5115cc5502990788fc808c672952e43b919d769e2e0bcaa9d9465913eb0a3e6895a43002c05fb966d4c6e026506d0ae0c1d4c12affac0171075f2bd82b307105cf3599e627f60436bfc4612d4bf39492d1c0219ff133506d1bc11c058370b55a702406502ff358e97d80228dd21cef78612005f38daf49e256e762cc5cacabf02fbae6792fa36c0fbb60e9c2e6022a97412e4993ffb9f70697cd06a2dae1259ec11d690a922ea73e8d73acc230d102fa8f991bd3a26198c4c4a00a6f4b8eff9574382b375d2f6a7b9eb375adfca17ddb3e559e9a8ab435d8d48de8c08b24de36168c316e40d3df4a8df0cda8502cdc5c727c918d38c303987618bfbe97d4fb5f66f9b334a877d6995a26fea16bdd3e353140937ede38145e7a0744c00a229157b212b861988b83c0fde1ca0e5b6e57b0303949b36b9b85d659f218ebd9cc30422f10b3c3ae4596e22bd63069a49d46d810dc4dcc208b3abd647e06a0f26ee41ab9c49e4a38cecfc4d981a44a70c3d2cefdbe2274f4c8c1b472d775e4d58a25a6872addb61f29af933a05f38a5857cf4fd63822836e1651124c8f4ca41b329e1d1493ddbc7bc274a167877c60e9bcfa51bf0da3c4f6ff12aa133c28af78dc8923c372fcb3a43ef2e399d38c4982ee3dd98f0999aea30d322d1718197596f0e0a5da67fdd180069abb275768cc705ee06053c6673837d3914224030c25b2c9a70e8057a77e2495e4f5c5959feda4ac890a64cee6851d9192d3db25f5d31dc3afb05d976c3e2d5a39bb7ec4dd21e05b09c2f693ad0f1619349036bcaeccf70fc199f7f0784cf7dbf8bfe64785c20af2c8da32fb51d9924c488e4a51757e1cefed99eea4fb56c3d1f448c7d23860c0d248804d53fa1d2da2b1e08d7e3581c5d81a018ab9eaf1c7ef6b4085f30d8a3c7a886dd163dc57b38d7dc6859f5034e9fc74daac87b2274efe0c9eaa802866b45d5e809a54fe7935f9856bbb202f2b4d7c9bb7a061da3160e8764d81557c2bef4b064c11ba97c111bf7067c085071069aaaace14b9d109b1460a7138e3e50bd0ab508697274092b4a31dbac06f247b37c6fff833a334706a38e5778b50e467c2166fd1e60317e83dbdae82065c818b10b50bded5cbda04646eb1e6d531df00d41cd75835e17dc7c4c1f8ec3c6bcd2c434e07f576fa4607484896a92395a8806fdf26ca0f5bdee0fb503258353a3e7f4a2a3b97258adf30e8b5ca7757421c61e98e4f4552ad424a0fd0826d4c8a904dc5b73c2378fc667aeed617d603d0525aee11e334527f0ffe373837aa27f69cbe088fe2f6f2519033ef8ded4bea2008aa590eb8e31657e5d3758023907e3a858e53d7f159e6c8dd20837a19bb5a54b7a9807cdc987c9641385fe9a690736e0ecb2d23abf4e4ad91d5f83797f7d6858a587553fff8f8ccdbd96953ca1ba76d4a95b3cf0d4c08c0ed351eb370d332a17390b15b939237082731be74a7c09a0c82e28604930903329a0a834312957c90cef3d7b660605943ea3f157fbcfd618d271f1f1978b4d8c3d62ef9aa858d9430e
```

## Measurements

From `run.log` (release build, WSL Ubuntu 24.04, x86_64, 16 CPUs, rustc 1.98.1, 2026-09-08):

| step | time |
|---|---|
| generators (2^16, once per process) | 1.6 s |
| `prove` | 8.5 s |
| `verify` | 0.74 s |
| proof size | 1,601 bytes |

The 2026-09-06 prototype on the same statement measured 14.8 s / 1.15 s / 1,601 bytes; the circuit is unchanged
(37,460 gates), the machine was busier. `record.sh` re-records `run.log` (and writes `vector.txt` only if it is
missing, so the vector this README annotates survives a re-timing).

## Tests

`cargo test --release`: `tests/proof.rs` — round trip (and the DERO-side `SHA256(HEXDECODE(hex(s)))` and
Monero-side `(s mod ℓ)·G` checks), wrong `H`, wrong `S` (random, identity, torsioned, garbage), tampered sigma
(every field, and a non-canonical response), tampered R1CS (version byte, points, scalars, inner-product elements),
truncated and lengthened proofs, the reduced-scalar refusal (`ℓ`, `ℓ + 1`, all ones, `2^255`; `ℓ − 1` accepted;
the refusal returns before any proving); `tests/cli.rs` — the JSON shapes byte for byte, every exit code, one
line on stderr and an empty stdout on every refusal, the secret never echoed, `prove -`; `tests/vector.rs` — the
recorded vector above.

## Files

| file | what |
|---|---|
| `src/lib.rs` | the protocol (unchanged from the prototype), `Proof::to_bytes`/`from_bytes`, `is_reduced`, `keygen`, `statement`, `prove`, `verify`, `verify_bytes` |
| `src/sha256_gadget.rs` | SHA-256 over a committed 32-byte value as an R1CS gadget (unchanged) |
| `src/main.rs` | the command line |
| `tests/{proof,cli,vector}.rs` | above |
| `record.sh`, `run.log`, `vector.txt` | the measurement run and the worked vector |
