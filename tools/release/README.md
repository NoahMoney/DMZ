# release — reproducible builds of DMZ's companions, and a manifest anyone can check

`release` builds every user-facing binary for windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and
darwin/arm64, and writes `manifest.json`. `-verify` rebuilds a manifest in a fresh directory and compares every
hash.

| binary | package | status |
|---|---|---|
| xmrswap | `tools/xmrswap/cmd/xmrswap` | built |
| zecswap | `tools/zecswap/cmd/zecswap` | built: the package is a `main` today. A product whose package is missing or not `main` is listed under `absent` with the reason, never built and never faked. |

## Commands

```
cd tools
go run ./release -out <dir>                                  # build all, write <dir>/manifest.json
go run ./release -verify <dir>/manifest.json                 # rebuild at a new temporary path, compare
go run ./release -verify manifest.json -binaries <downloads> # also hash binaries you downloaded
```

A third party checks a release with the same Go version the manifest names (`"go"`): `go env GOVERSION` must
match. They check out the manifest's `commit` and run `-verify`. Every line must read `PASS`. Nothing about the
publisher is trusted except the manifest they published, and `-binaries` ties their files to it.

Every binary is built with:

```
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> GOFLAGS= GOWORK=off GOTOOLCHAIN=local \
  go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o <file> <package>
```

- `-trimpath` removes the build directory.
- `-buildvcs=false` removes the git state.
- `-buildid=` removes the linker's build id.
- `CGO_ENABLED=0` removes the host C toolchain.
- `GOFLAGS=`, `GOWORK=off` and `GOTOOLCHAIN=local` stop the environment from changing the build.

What remains is a function of the source, the module graph (`tools/go.mod`, `tools/go.sum`) and the Go version.

## manifest.json

It records:

- `go`: the toolchain version;
- `commit` and `dirty`: `git rev-parse HEAD` of the checkout, and whether anything differs from it;
- `dirtyInputs`: the build inputs and contracts that differ from that commit. If any are listed, the binaries are
  reproducible only from that working tree, and `-verify` says so;
- `build`: the command, the environment, and `inputs`, the repository directories the binaries compile from;
- `binaries`: file, sha256 and size for each;
- `absent`;
- `contracts`: for every `contracts/*.bas`, the raw sha256 and `canonSha256` = sha256(`reference.Canon`(file)).
  That is the hash `tools/simdeploy` installs and `tools/verifydeploy` compares with the chain; both tools use
  `tools/verifydeploy/srchash`;
- `dist`: sha256 of every file of every `dist/<network>/` present.

It carries no time and no path, so two builds of the same source write the same bytes.

`-verify` copies the build inputs (the package directories `go list -deps` names inside the repository, on every
platform, plus both `go.mod`/`go.sum`) into a new temporary directory, and builds there. Contracts and `dist` are
re-read from the checkout.

## Reproducibility, shown (2026-09-11, go1.26.2, windows/amd64 host)

One snapshot of the build inputs was copied to two different directory paths, `…/repro/a/fomo` and
`…/repro/deeper/path/number/two/fomo`, and `go run ./release -out` ran in each:

- The two `manifest.json` files are **byte-identical**.
- `-verify` of manifest A, run from copy B, rebuilt in a third path (`%TEMP%/dmz-release-verify-*/src`). **All 68
  comparisons match**: the Go version, 10 binaries, 12 contracts, 34 dist files, and copy B's 10 binaries through
  `-binaries`. The remaining line is a WARN that the copies carry no commit.
- Builds in the live checkout, made while other agents were editing the companions, show what drift looks like.
  - The first build, minutes after the snapshot, produced the same five xmrswap binaries and five different zecswap
    ones.
  - A later `-verify` of manifest A against the checkout failed on all ten binaries and passed every contract and
    dist file. By then `tools/xmrswap/cmd/xmrswap/main.go` and four `tools/zecswap` files had changed, and the log
    lists them.
  - That is source drift, not nondeterminism, and it is what `-verify` exists to report.

| file | sha256 (paths A and B, and the -verify rebuild) |
|---|---|
| xmrswap-windows-amd64.exe | `722b8d504a2150e65fbb1ec1bdb8f70764080be515228b42e2103ba50d6f85cd` |
| xmrswap-linux-amd64 | `3ae5f2ed729cd332f3ee02b0d734189e86d50d29cddea46f8069719631498ca8` |
| xmrswap-linux-arm64 | `8e4f5b65033c513cbd3cad9a796baa4a80f0236ce4baa1a03c0f15929e995941` |
| xmrswap-darwin-amd64 | `56e89cda4e9b500fe74eb6512d5d578af89014e3d9e67552c6379b323ed8fe23` |
| xmrswap-darwin-arm64 | `2a2aadf58a150b94fe7ccfef3cbd734bf5370afa11fa43a8258900690619ba97` |
| zecswap-windows-amd64.exe | `aeadf43824bd7811e103cea57409fd8d0e7c02147a5389f95757ed5571cfeea5` |
| zecswap-linux-amd64 | `b0686622ef1510bb633399ddbfc6d8505bc183a96e8cbc7cd9fb10d7eeb3b19a` |
| zecswap-linux-arm64 | `eb353a0aef9e07448722f41478bb6756214a03bb9c6d8f42d909e8d349b58378` |
| zecswap-darwin-amd64 | `f4cd0f13493cec7fab6d2a07d1223617767775345f6519000573c5af4acf6601` |
| zecswap-darwin-arm64 | `8b6a34fcbc813c50eec4e554af2bfd3d2e34d86e4f782b0a012e72ff0f6256bd` |

These hashes are of an uncommitted working tree (the copies carry no `.git`, so their manifests say
`"commit": ""`). They show the build is path-independent. They are not a release: a release is built from a
committed, clean tree, and its manifest names that commit. The full transcript is in
`research/release/reproducible-build.log`.

## What this proves, and what it does not

*Proves:* a binary with the manifest's hash is exactly what this source, this module graph and this Go version
compile to, whoever builds it and wherever. It also proves the contract and bundle hashes in the manifest are those
of the files in the checkout. *Does not prove:* that the source is safe. It says nothing about the tools in
`vendor/bin` (derod, tela-cli, telainstall). Those are pinned by `deploy/binaries.lock` and `tools/verifyvendor`.
And `dist/` hashes record the bundle that was present, not one rebuilt from source: `genconfig` stamps `config.js`
with the time it ran. To check what is on chain, use `tools/verifydeploy`.
