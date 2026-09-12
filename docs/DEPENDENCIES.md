# DMZ dependencies and their known vulnerabilities

*2026-09-11, W-05 (GAP-017, GAP-018). This page lists every third-party dependency of every DMZ binary: its version,
where the version came from, whether it is pinned, and the known vulnerabilities, each with whether it is reachable
from DMZ's use of the node, the wallets and the tools. Vulnerability data comes from the primary databases:
- the Go vulnerability database (vuln.go.dev, updated 2026-09-10 14:48 UTC), downloaded whole and matched offline,
  and queried by govulncheck v1.6.0;
- osv.dev for crates.io (RustSec) and npm.*

---

## 0. Findings that need action

| # | Finding | Where | Action |
|---|---|---|---|
| D-1 | **go1.26.2's standard-library vulnerabilities are reachable in every binary.** Symbol-level counts: derod 10, simulator 13, the wallets 7, telainstall and tela-cli 7, the tools 9–10 (§7). All are fixed in go1.26.3–1.26.6. | all | **Closed 2026-09-12.** `vendor/bin` and the tools are rebuilt with go1.26.8, and govulncheck finds 0 reachable in derod, the simulator, netwallet and the tools (`research/release/govulncheck-2026-09-12.log`). |
| D-2 | **gorilla/websocket GO-2026-6278 is reachable in every node, wallet and installer binary.** The mask key for client-to-server frames comes from a weak PRNG. derohe and civilware vendor v1.4.2 (called in derod, the simulator, both wallet CLIs, dero-miner, netwallet and xswdwallet); tela uses v1.5.0 (called in telainstall and tela-cli). The fix is v1.5.3. | §2, §3 | **Accepted 2026-09-11 (coordinator), for the reason in §2.3.**<br>• WebSocket masking protects intermediaries' caches and gives no secrecy.<br>• Every affected connection here is on loopback.<br>• No binary DMZ ships to its users links gorilla. `tools/go.mod` does not require it, the companions speak HTTP JSON-RPC, and the app uses the browser's own WebSocket.<br>Revisit if a DMZ-shipped binary ever links it, or when derohe or tela next bumps it. A patch would be a `require`/`replace` bump in `vendor/tela/go.mod`, and a re-vendored copy in derohe pinned as a `modified` path set. |
| D-3 | **derohe's vendored x/net is from 2017-12-12 and its x/crypto from 2021-11-17.** 28 and 26 advisories apply to those versions. Nothing reaches them today, because none of the affected packages is compiled in. Any new derohe import of x/net/http2, html, idna, or x/crypto/ssh or openpgp would bring them in. | §2 | Keep this inventory, and re-run §8 on every derohe or civilware commit bump. |
| D-4 | **miekg/dns v1.0.5 (GO-2020-0008 predictable DNS transaction ids, GO-2020-0028 zone-file panic) is in derod.** It is imported, not called: its only user, the update check, is never started (`vendor/derohe/cmd/derod/main.go:183`, `//go check_update_loop ()`). | §2 | Watch item. If an upstream bump or a DMZ patch re-enables the update check, the spoofable DNS query becomes reachable. |
| D-5 | **derohe and civilware changed vendored third-party code, and no version-based scanner can see it.** The changed modules are graviton, logr, x/net/proxy and x/sync; civilware also changed readline. The diffs were read (§2.2) and none is security-relevant. The checkouts are pinned by commit, so the changes are covered for integrity. | §2.2 | A future re-vendor must carry them or drop them on purpose. Record the decision with the bump. |
| D-6 | **tela-cli's documented build still says `-mod=mod`** (`deploy/README.md`, the bootstrap). | §3 | **Done 2026-09-11.** `deploy/README.md` and the go1.26.8 rebuild both build tela-cli with `-mod=readonly -trimpath`. verifyvendor checks `tela-cli.exe`'s build info against go.sum and the pinned replace, so a build that drifts is caught. |
| D-7 | **Two unused libraries are linked into production binaries.** testify/assert, and gopkg.in/yaml.v2 through it, are in derod and the wallets: `cryptography/bn256/changes_more_tests.go` is not a `_test.go` file. yaml.v2 v2.2.5 carries GO-2020-0036, and it is not called. | §2 | None needed for security. Note it for an upstream report. |
| D-8 | **Node is unpinned for `tests/js`** (22.10.0 was used). | §6 | Test-only. Pin it in `TESTING_AND_VERIFICATION.md` if the JS results are to be reproducible. |

Nothing else found needs action. The Rust prover's 47 crates.io packages have no applicable advisory (§5). Playwright
1.62.0 has none (§6). The x/crypto and x/net advisories in the tools and tela sit in packages that are not compiled in,
or are imported but not called (§3, §4).

---

## 1. The binaries and where their code comes from

| Binary | Built from | Third-party code comes from | Pinned by |
|---|---|---|---|
| `derod.exe`, `simulator.exe`, `dero-wallet-cli.exe`, `dero-miner.exe`, `genesistx.exe`, `netwallet.exe` (and `netwallet-next.exe`) | `vendor/derohe` @ `e9df1205` (+ `deploy/patches/derod-private-genesis.patch`; `tools/genesistx`, `tools/netwallet` copied in) | derohe's own `vendor/` directory: GOPATH style, no `modules.txt`, so the go command and govulncheck see no versions (§2) | `deploy/vendor.lock` (commit, origin, patch, allowed paths); `deploy/binaries.lock` (sha256) |
| `xswdwallet.exe`, `dero-wallet-cli-xswd.exe` | `vendor/derohe-civilware` @ `433c4559` (`tools/xswdwallet` copied in) | the fork's own `vendor/`, the same as derohe's except readline, websocket and gmsm (§2) | as above |
| `telainstall.exe`, `tela-cli.exe` | `vendor/tela` @ `aa892d2f` (`tools/telainstall` copied in), module mode, **`-mod=readonly`** (GAP-017) | the module cache, checked against `vendor/tela/go.sum`. derohe is replaced by `github.com/civilware/derohe v0.0.0-20240909003240-fa76d6016cc6` (commit `fa76d6016cc6b6158847c420455dc7d8dc4ab885`), which no checkout here holds (§3) | `vendor/tela/go.sum` (in the pinned commit); `deploy/vendor.lock` `modules` (the replace, its go.sum hashes, the cache copies, the binaries' build info); `deploy/binaries.lock` |
| `xmrswap`, `zecswap` (release builds), `vaultsigner`, `treasurybot`, `simswap`, `simhtlc`, `simvault`, `simwrapmkt`, `simscam`, `simsmoke`, `genconfig`, `verifydeploy`, `serve`, … | `tools/` (`veil/tools`) | `tools/go.mod` and `tools/go.sum` (§4) | go.sum; the release manifest (`tools/release`) for the companions |
| `proof-xmr` (the Monero swap's prover) | `research/crosschain/poc/proof-xmr` | crates.io through `Cargo.lock`; `bulletproofs` 5.0.0 as a patched path dependency (§5) | `Cargo.lock` checksums; `deploy/vendor.lock` `crates` (GAP-013) |
| the app (`app/`, installed as TELA DOCs) | `app/` | none: plain JS/CSS with no third-party code, no CDN, no package manager | the bundle's `manifest.json` sha256 per file |
| test only: `tests/js`, `tools/uitest` | — | Node built-ins; Playwright from `$PW_DIR` (§6) | Playwright pinned in `tools/uitest/ui.mjs` (MT-H03) |

The binaries were scanned as built with **go1.26.2**, and §7 records that scan. On 2026-09-12 every one was rebuilt
with **go1.26.8** (`deploy/binaries.lock`). govulncheck then found no reachable standard-library vulnerability
(`research/release/govulncheck-2026-09-12.log`).

---

## 2. derohe's vendored third-party code (GAP-018)

### 2.1 How the versions were found, and how sure each one is

derohe vendors GOPATH-style: the files, but no record of which version they are. Each module's version was recovered
from the source:
1. Clone the upstream repository, bare and blob-less. Hash every vendored file of the module as a git blob. Compare
   with the tree of every upstream commit.
2. The commit whose tree matches the most vendored files byte for byte is the source. Ties are broken towards fewer
   upstream Go files missing from the copy.
3. The module proxy names that commit's version (`/@v/<commit>.info`).

Confidence, as used in the table:
- **exact**: every vendored file of the module is byte-identical to that one commit, and to no other.
- **exact, range**: every file is identical to each commit of a consecutive run whose later commits changed only
  files derohe did not vendor. The code is that code; the version label is the range.
- **patched**: every file but the ones named is identical, and those carry local edits (§2.2).

The two trees agree module for module except where the table says otherwise. Modules that are vendored but compiled into
no DMZ binary were not inferred (§2.4).

"Linked into": **d** derod, **s** simulator, **w** dero-wallet-cli, **m** dero-miner, **g** genesistx, **n** netwallet,
**X** xswdwallet and dero-wallet-cli-xswd (civilware).

| Module | Version (inferred) | Upstream commit, date | Confidence | Linked into | Known vulnerabilities at this version, and reachability |
|---|---|---|---|---|---|
| github.com/VictoriaMetrics/metrics | v1.17.3 | tag, 2021-07-07 | exact (53 files) | d s g | none |
| github.com/beevik/ntp | v0.3.0 | `6f90c4d0` 2020-03-19 … `6fc207d8` 2023-03-10 | exact, range | d s | none |
| github.com/blang/semver/v4 | v4.0.0 | `af3461a9` 2020-05-24 (+1 commit) | exact, range | all | none |
| github.com/caarlos0/env/v6 | v6.7.3-0.20211109131905-2cf397a58a36 | `2cf397a5` 2021-11-09 | exact | all | none |
| github.com/cenkalti/hub | v1.0.2-0.20201007105119-82396bff75b2 | `82396bff` 2020-10-07 | exact | d s | none |
| github.com/cenkalti/rpc2 | v0.0.0-20210604223624-c1acbc6ec984 (= v1.0.0's files) | `c1acbc6e` 2021-06-04 … v1.0.0 | exact, range | d s | none |
| github.com/cespare/xxhash | commit `aa1a74e0bbdf` (2022-01-10), v2 code vendored under the **v1** import path. The proxy refuses to name it (its go.mod says `/v2`); it is on the v2.1.2 → v2.1.3 line | — | exact | all | none |
| github.com/chzyer/readline | derohe: v0.0.0-20171208011716-f6d7a1f6fbf3 · civilware: **v1.5.1, patched** | 2017-12-08 · 2022-07-15 | exact · patched (`operation.go`, `std.go`) | d s w m · X | none |
| github.com/coder/websocket (civilware only) | v1.8.12 | `77b2e157`/`e4379472` 2024-08-09 | exact, range | X | none |
| github.com/creachadair/jrpc2 | v0.35.5-0.20220204185425-1a458a92adf6 | 2022-02-04 | exact | d s w g n X | none |
| github.com/davecgh/go-spew | v1.1.2-0.20180830191138-d8f796af33cc | 2018-08-30 | exact | all | none |
| github.com/dchest/siphash | v1.2.4-0.20220321115102-722c5ea45189 | 2022-03-21 | exact | all | none |
| github.com/deroproject/graviton | v0.0.0-20220130070622-2c248a53b2e1 (the version tela's go.sum names) | `de74d821` … `2c248a53` 2022-01-30 | **patched** (`extra.go`, `README.md`) | all | none |
| github.com/docopt/docopt-go | v0.0.0-20180111231733-ee0de3bc6815 | 2018-01-09 … 01-11 | exact, range | d s w m X | none |
| github.com/dustin/go-humanize | v0.0.0-20171111073723-bb3d318650d4 | 2017-11-11 | exact | d s | none |
| github.com/fxamacker/cbor/v2 | v2.3.0 | tag | exact | all | none |
| github.com/go-logr/logr | v1.1.0 | 2021-08-23 | **patched** (`logr.go`) | all | none |
| github.com/go-logr/zapr | v1.1.0 | tag | exact | all | none |
| **github.com/gorilla/websocket** | **v1.4.2** | `b65e6290` 2020-03-19 | exact (55 files) | all | **GO-2026-6278** (weak mask-key PRNG), fixed v1.5.3. **Called** in d s w m n X: derod `rpc.ws_handler`; `walletapi/rpcserver.Run`; `dero.getwork` in dero-miner; `xswd.Connection.Close`. Imported only in g. See §2.3 |
| github.com/hashicorp/golang-lru | v0.0.0-20180201235237-0fb14efe8c47 | 2018-02-01 | exact | d s g | none |
| github.com/klauspost/compress | v1.11.3 (8 identical commits) | 2020-11-10 … 2020-12-16 | exact, range | d s w g n | none |
| github.com/klauspost/cpuid/v2 | v2.0.12 | tag | exact | all | none |
| github.com/klauspost/reedsolomon | v1.9.14-0.20210901165502-7761c8f7cd99 | 2021-09-01 | exact | d s g | none |
| github.com/lesismal/llib | v1.1.4 | tag | exact (171 files) | d s | none |
| github.com/lesismal/nbio | v1.2.14-0.20220301150822-22a5357345f3 | 2022-03-01 | exact | d s | none |
| **github.com/miekg/dns** | **v1.0.5** | tag | exact (990 files) | d | **GO-2020-0008** (math/rand DNS ids, fixed 1.1.25) and **GO-2020-0028** (TA zone-file panic, fixed 1.0.10): **imported, not called**. The update check that uses it is disabled (D-4) |
| github.com/minio/sha256-simd | v1.0.1-0.20210617151322-99e45fae3395 | 2021-06-17 … 2022-10-18 | exact, range | all | none |
| github.com/pkg/errors | v0.8.1-0.20180311214515-816c9085562c | 2018-03-11 | exact | d s g | none |
| github.com/pmezard/go-difflib | v1.0.1-0.20181226105442-5d4384ee4fb2 | 2018-12-26 | exact | all | none |
| github.com/robfig/cron/v3 | v3.0.2-0.20210106135023-bc59245fe10e | 2021-01-06 | exact | all | none |
| github.com/satori/go.uuid | v1.1.1-0.20170321142108-1d9318df9bdf | 2017-03-21 | exact, range | all | none |
| github.com/segmentio/fasthash | v1.0.3 (range v1.0.2 … 2023-07) | 2020-04-21 … 2023-07-03 | exact, range | all | none |
| github.com/stretchr/testify | v1.4.1-0.20191106224347-f1bd0923b832 | 2019-11-06 | exact | all (assert only: D-7) | none |
| github.com/templexxx/cpu | v0.0.9 | tag | exact | d s g | none |
| github.com/templexxx/xorsimd | v0.4.1 | tag | exact | d s g | none |
| github.com/tjfoc/gmsm | v1.4.2-0.20211011113701-3c9466052c78 (20 identical commits to 2022-01-14) | 2021-10-11 … | exact, range. derohe lacks `API????.md`, which Windows cannot check out (`vendor.lock` lists it) | d s g (sm4 only) | none |
| github.com/valyala/fastrand | v1.0.0 | tag | exact | d s g | none |
| github.com/valyala/histogram | v1.1.2 | tag | exact | d s g | none |
| github.com/x448/float16 | v0.8.5-0.20200203145343-e05feda6110a | 2020-02-03 | exact | all | none |
| github.com/xtaci/kcp-go/v5 | v5.6.2-0.20211022122318-bd59cf8a523b | 2021-10-22 | exact | d s g | none |
| go.uber.org/atomic | v1.9.0 | 2021-07-15 | exact, range | all | none |
| go.uber.org/multierr | v1.7.1-0.20210712175728-d49c2ba57443 | 2021-07-12 | exact | all | none |
| go.uber.org/zap | v1.19.2-0.20210910224926-10d89a76cc8b | 2021-09-10 | exact (151 files) | all | none |
| **golang.org/x/crypto** | **v0.0.0-20211117183948-ae814b36b871** | `ae814b36` 2021-11-17 | exact (364 files) | all | 26 advisories apply to this version. **None reachable**: all are in ssh, ssh/agent, ssh/knownhosts, openpgp (and its subpackages) and acme/autocert, which no DMZ binary compiles. Compiled: blake2s, blowfish, cast5, chacha20, chacha20poly1305, cryptobyte, curve25519, ed25519, hkdf, poly1305, pbkdf2, salsa20, sha3, tea, twofish, xtea |
| **golang.org/x/net** | **v0.0.0-20171212005608-d866cfc389ce** | `d866cfc3` 2017-12-12 | **patched** (`proxy/direct.go`; a stray `.directory` file) | all | 28 advisories apply to this version. Compiled: bpf, internal/iana, internal/socket, ipv4, ipv6, proxy. **GO-2025-3503** (proxy bypass via IPv6 zone ids) is **imported, not called**: derohe calls `proxy.FromURL` and `proxy.Direct` for `--socks-proxy`, never `FromEnvironment` or `PerHost`. GO-2026-5026, -4918 and -5942 are reached through the standard library's copy, not this one (§7). The rest are in http2, html, idna, websocket and the like, which are not compiled |
| golang.org/x/sync | v0.0.0-20200317015054-43a5402ce75a | 2020-03-17 | patched: the one vendored file has a trailing blank line | d s w g n X | none |
| golang.org/x/sys | v0.0.0-20211025201205-69cdffdb9359 | 2021-10-25 | exact (477 files) | all (cpu only) | GO-2022-0493 (unix Faccessat) and GO-2026-5024 (windows NewNTUnicodeString): the packages are not compiled |
| golang.org/x/time | v0.0.0-20180314180208-26559e0f760e | 2018-03-14 | exact | d s g X | none |
| golang.org/x/xerrors | v0.0.0-20191204190536-9bdfabe68543 | 2019-12-04 | exact | all | none |
| gopkg.in/natefinch/lumberjack.v2 | commit `47ffae23317c` (2020-10-21); the proxy refuses to name it because its go.mod says `github.com/natefinch/lumberjack`. Between v2.0.0 and v2.2.1 | — | exact | d s | none |
| **gopkg.in/yaml.v2** | **v2.2.5** | tag | exact | all (through testify: D-7) | **GO-2020-0036** (alias-expansion DoS, fixed 2.2.8): **imported, not called** |
| nhooyr.io/websocket (derohe only) | v1.8.8-0.20210410000328-8dee580a7f74: v1.8.7 plus one commit. The proxy names it `v0.0.0-…` because it no longer resolves the old path's tags | 2021-04-09 | exact | d s w g n | none |

Reachability in this table comes from govulncheck run on a copy of each tree with a synthesized `vendor/modules.txt`
that names the inferred versions (§8), one binary at a time. It builds and type-checks exactly the packages the real
build compiles: nested `vendor/` directories are ignored in module mode, and `go list` confirms that every package
resolves to the top-level copy. An advisory that versions alone flag was checked against the call graph.

### 2.2 Local edits in the vendored code

None of these is in any upstream commit. All were read in full:

| File | Change | Security relevance |
|---|---|---|
| `github.com/deroproject/graviton/extra.go` | the tree-size estimate returns `len(depth_array)` when there are ≤ 19 samples (upstream: ≤ 4, `count`) | none: a statistic |
| `github.com/deroproject/graviton/README.md` | an older README | none |
| `github.com/go-logr/logr/logr.go` | `Logger.Error` does nothing unless `l.Enabled()` | none: logging only |
| `golang.org/x/net/proxy/direct.go` | `Direct.Dial` uses `net.DialTimeout(…, 5 s)` instead of `net.Dial` | none. Every direct outbound dial through `globals.Dialer` gives up after 5 s |
| `golang.org/x/net/.directory` | a KDE Dolphin settings file | none: not code |
| `golang.org/x/sync/semaphore/semaphore.go` | one trailing blank line | none |
| civilware `github.com/chzyer/readline/operation.go`, `std.go` | adds `Operation.KickReader`; `FillableStdin` takes an `io.ReadCloser` and closes it | none: the XSWD wallet's prompt handling |

### 2.3 GO-2026-6278 in DMZ's use

The gorilla/websocket advisory is the only third-party finding reachable from the node and the wallets (D-2).
- **What it breaks.** A WebSocket client must mask each frame it sends to the server with a random 32-bit key.
  gorilla took the key from `math/rand`, so the next key can be predicted.
- **What masking is for.** Masking exists to stop a hostile page from steering the bytes a proxy sees and poisoning
  its cache (RFC 6455 §10.3). It hides nothing from anyone watching, and it authenticates nothing.
- **Where DMZ uses it.** The client side is where it applies: the wallets and telainstall talking to derod, the miner
  talking to derod, and tela's connections. derod's and the XSWD server's own frames are unmasked by design.
- **Why the practical effect is small.** Every documented DMZ setup connects to a node on loopback, or over TLS to
  one the user chose. No proxy sits in the path whose cache could be poisoned.
- **The residual.** A plain `ws://` path through a caching proxy. DMZ's docs already refuse such a setup for other
  reasons.
- **The fix.** v1.5.3, where the key comes from `crypto/rand`.

### 2.4 Vendored, but compiled into no DMZ binary

Present in `vendor/derohe/vendor` (and the civilware copy), not linked by any of the eight binaries, so not inferred:
- bitbucket.org/creachadair/stringset;
- AndreasBriese/bbloom, aead/skein, armon/go-radix, beorn7/perks, fatih/color, go-stack/stack, holiman/uint256,
  lucasb-eyer/go-colorful, mattn/go-colorable, mattn/go-isatty, mattn/go-runewidth, minio/highwayhash,
  osamingo/jsonrpc, patrickmn/go-cache, pkg/profile, randall77/makefat, txthinking/runnergroup, txthinking/socks5,
  txthinking/x and ybbus/jsonrpc (all under github.com);
- go.etcd.io/bbolt;
- the nested `vendor/` copies inside VictoriaMetrics/metrics, graviton, miekg/dns (an old x/crypto and x/net), testify
  and valyala/histogram.

Adding an import of any of them brings in an uninferred version; re-run §8 first.

---

## 3. tela: telainstall and tela-cli (GAP-017)

`vendor/tela` @ `aa892d2f` builds in module mode against `vendor/tela/go.sum`. telainstall is built by
`tools/telainstall/build.sh` with `-mod=readonly`: go.mod and go.sum are never rewritten, and a module missing from
go.sum stops the build. Before it builds, `verifyvendor -modules-only` checks these:
- the go.mod `replace` is exactly the pinned one;
- go.sum lists exactly the pinned hashes of `github.com/civilware/derohe v0.0.0-20240909003240-fa76d6016cc6`
  (`h1:hcCFU5eXd7CPu4AXJnaihhzOgC0SNscmQ+nrgDjKaWo=`; its go.mod `h1:EWHh1VkXRnCHvyGML98kXhngDFYebmOhk/9kZ1ATJ1c=`),
  and no other version of the fork;
- every copy of a go.sum module in the local module cache still hashes to its go.sum line (212 copies on the day);
- the fork's proxy `.info` names commit `fa76d6016cc6b6158847c420455dc7d8dc4ab885`.

A plain `verifyvendor` run also reads the build info of `vendor/bin/telainstall.exe` and `tela-cli.exe`: every module
in them must be go.sum's, with go.sum's hash, or the pinned replace. It passed for both (32 and 39 modules).
A test against a scratch root and a scratch module cache made each of these a finding:
- a planted line in the fork's extracted `walletapi/wallet.go`;
- the same change inside the zip, with no extracted copy;
- go.mod pointing at another fork commit;
- a changed go.sum hash;
- a second fork version added to go.sum.

The fork at `fa76d601` (2024-09-09) is **not** the commit checked out as `vendor/derohe-civilware` (`433c4559`,
2025-08-13), which builds the XSWD wallet. The installer's wallet code and the XSWD wallet's are two different
revisions of the same fork.

| Module | Version | go.sum h1 | Known vulnerabilities, reachability (govulncheck, source mode) |
|---|---|---|---|
| github.com/civilware/derohe (replaces deroproject/derohe) | v0.0.0-20240909003240-fa76d6016cc6 | `hcCFU5eX…aWo=` | none in the database for the fork. Its code is derohe's wallet API (§2's versions do not apply: in module mode the fork's own `vendor/` is not used) |
| **github.com/gorilla/websocket** | **v1.5.0** | `PPwGk2jz…mnc=` | **GO-2026-6278, called**: `tela.GetGasEstimate` → `websocket.Dialer.Dial`, `tela.ShutdownTELA` → `Conn.Close`. Fixed v1.5.3 (D-2) |
| golang.org/x/net | v0.19.0 | `zTwKpTd2…Vr1c=` | GO-2026-5942 (dnsmessage) and GO-2026-5026 (idna): **imported, not called**. 12 more apply to the version, in packages not compiled |
| golang.org/x/sys | v0.15.0 | `h48lPFYp…g9pc=` | GO-2026-5024 (windows NewNTUnicodeString): **imported, not called** |
| golang.org/x/crypto | v0.17.0 | `r8bRNjWL…hM4k=` | 22 apply to the version; none in a compiled package |
| github.com/blang/semver/v4 v4.0.0 · caarlos0/env/v6 v6.10.1 · cespare/xxhash v1.1.0 · chzyer/readline v1.5.1 (tela-cli) · civilware/Gnomon v0.0.0-20240403103529-8b2fdb2b3106 · coder/websocket v1.8.12 · creachadair/jrpc2 v0.35.4 · davecgh/go-spew v1.1.1 · dchest/siphash v1.2.3 · deroproject/graviton v0.0.0-20220130070622-2c248a53b2e1 · fxamacker/cbor/v2 v2.4.0 · go-logr/logr v1.2.3 · go-logr/zapr v1.2.3 · klauspost/cpuid/v2 v2.2.6 · mattn/go-colorable v0.1.13 · mattn/go-isatty v0.0.16 · mgutz/ansi v0.0.0-20200706080929-d51e80ef957d · minio/sha256-simd v1.0.0 · pmezard/go-difflib v1.0.0 · robfig/cron/v3 v3.0.1 · satori/go.uuid v1.2.0 · segmentio/fasthash v1.0.3 · sirupsen/logrus v1.9.3 · stretchr/testify v1.8.4 · x-cray/logrus-prefixed-formatter v0.5.2 · x448/float16 v0.8.4 · go.etcd.io/bbolt v1.3.7 · go.uber.org/atomic v1.10.0 · multierr v1.9.0 · zap v1.24.0 · x/sync v0.5.0 · x/term v0.15.0 · x/xerrors v0.0.0-20231012003039-104605ab7028 · gopkg.in/yaml.v3 v3.0.1 | as in go.sum | none |

---

## 4. tools/go.mod (`veil/tools`)

| Module | Version | Pinned | Known vulnerabilities, reachability (govulncheck, source mode, every package except genesistx and netwallet) |
|---|---|---|---|
| filippo.io/edwards25519 | v1.1.1 | go.sum | none. GO-2026-4503 (invalid result or undefined behaviour) affects versions below v1.1.1 |
| github.com/decred/dcrd/dcrec/secp256k1/v4 | v4.3.0 (marked `// indirect` but imported by `zecswap/sign.go`) | go.sum | none |
| github.com/decred/dcrd/crypto/blake256 | v1.0.1 | go.sum | none |
| golang.org/x/crypto | v0.33.0 | go.sum | 21 apply to the version, all in ssh (with ssh/agent and ssh/knownhosts) or openpgp. The tools compile only `ripemd160` and `sha3`: **not reachable** |
| golang.org/x/net | v0.21.0 | go.sum | 14 apply. The standard library vendors its own copy; this module's packages are not compiled: **not reachable** |
| golang.org/x/sys | v0.30.0 | go.sum | GO-2026-5024: not compiled (`cpu` only) |
| golang.org/x/text | v0.22.0 | go.sum | GO-2026-5970 (norm infinite loop): not compiled |
| golang.org/x/term | v0.29.0 | go.sum | none |

The tools' only reachable findings are the standard library's (§7). GAP-015 already asks for the x/crypto and x/net
bumps. They are hygiene, not a reachable fix.

---

## 5. The Rust prover (`research/crosschain/poc/proof-xmr/Cargo.lock`)

49 packages in all:
- 47 come from crates.io, each with the checksum `Cargo.lock` records;
- `proof-xmr` is the crate itself;
- `bulletproofs` 5.0.0 is a path dependency: the published crate plus the committed r1cs patch, pinned by
  `deploy/vendor.lock` (`crates`: crate sha256 and tree hash) and checked by verifyvendor (GAP-013).

Every package was matched against osv.dev's crates.io data (RustSec and GHSA; downloaded 2026-09-11):
**no advisory applies to any pinned version.** The advisories that exist for these crates at other versions are:

| Crate @ pinned | Advisory | Why it does not apply |
|---|---|---|
| curve25519-dalek 4.1.3 | RUSTSEC-2024-0344 (timing in `Scalar::sub`) | fixed in 4.1.3 itself |
| generic-array 0.14.7 | RUSTSEC-2020-0146 | affects the 0.8–0.13 lines only |
| keccak 0.1.6 | RUSTSEC-2026-0012 (opt-in ARMv8 asm) | fixed in 0.1.6 itself |
| rand 0.8.8 | RUSTSEC-2026-0097 (`rand::rng()` with a custom logger) | fixed on the 0.8 line in 0.8.6 |
| rand_core 0.6.4 | RUSTSEC-2019-0035, RUSTSEC-2021-0023 | fixed in 0.3.1/0.4.2 and in 0.6.2 |
| sha2 0.10.9 | RUSTSEC-2021-0100 (AVX2 backend) | affected 0.9.7 only |
| shlex 2.0.1 | RUSTSEC-2024-0006 | fixed in 1.3.0 (build-time only, via `cc`) |
| zerocopy 0.8.56 | RUSTSEC-2023-0074 | the 0.2–0.7 lines only (fixed in 0.7.31) |
| zeroize_derive 1.5.0 | RUSTSEC-2021-0115 | fixed in 1.1.1 |

`tools/regtest/zfund` (a regtest funding helper) and the other `research/` Cargo projects are not DMZ binaries and were
not checked.

---

## 6. Test-only: Playwright and Node

| Component | Version | Pinned | Known vulnerabilities |
|---|---|---|---|
| playwright / playwright-core (`$PW_DIR`) | **1.62.0**, driving Chromium 151.0.7922.34 (revision 1234) | `tools/uitest/ui.mjs` refuses any other unless `--any-playwright` is given, and prints both at start; `tools/uitest/README.md` (MT-H03) | osv.dev npm: none for 1.62.0. GHSA-7mvr-c777-76hp (CVE-2025-59288: browser downloads without certificate checks) is fixed in 1.55.1 |
| Node | 22.10.0 used | **not pinned** (D-8) | not assessed: it runs only `node --test tests/js` and the Playwright driver against loopback |

The app itself has no third-party JavaScript. `tests/js` uses Node built-ins only.

---

## 7. The Go standard library (go1.26.2)

govulncheck, binary mode, on the binaries as built. "Called" means reachable symbols; "imported" means the package is
linked and the symbol is not called:

| Binary | Standard-library findings, called / imported |
|---|---|
| derod | 10 / 4 |
| simulator | 13 / 4 |
| dero-wallet-cli, dero-wallet-cli-xswd, xswdwallet, netwallet | 7 / 7 |
| dero-miner | 6 / 8 |
| genesistx | 2 / 12 |
| telainstall, tela-cli | 7 / 8–9 (plus gorilla, §3) |
| treasurybot, simswap, simvault, simwrapmkt, simscam, simsmoke | 9 / 5 |
| simhtlc | 10 / 4 |

For example, GO-2026-6090 and -5856 (crypto/tls), -6089 and -5026 (net/http), -6218 (net/url), -5972
(encoding/asn1), -5037 (crypto/x509), -4971 (net) and -4918 (net/http). All are fixed in go1.26.3–1.26.6. A rebuild
with **go1.26.8** closes every one (D-1, GAP-015). The fix is the toolchain, not DMZ code.

---

## 8. How to repeat this

1. **Inventory.** For each module under `vendor/derohe/vendor` (and the civilware copy), clone upstream with
   `git clone --bare --filter=blob:none`. For every commit, compare `git ls-tree -r` blob ids with the git blob hash of
   each vendored file. Name the best commit with `https://proxy.golang.org/<module>/@v/<commit>.info`. The versions
   above are the result; the program that produced them is not committed.
2. **Reachability for the GOPATH trees.** Copy the tree without `.git`. Add a `require` block naming §2's versions to
   its go.mod, and write a `vendor/modules.txt` listing each module (`# <path> <version>`, `## explicit`) and its
   packages. Check that `go build -mod=vendor ./cmd/derod` still builds, then run
   `GOFLAGS=-mod=vendor govulncheck -show verbose ./cmd/<binary>` for each binary.
3. **Module-mode builds.** `cd vendor/tela && GOFLAGS=-mod=readonly govulncheck ./cmd/telainstall ./cmd/tela-cli`,
   and `cd tools && govulncheck <every package but genesistx and netwallet>`.
4. **Binaries.** `govulncheck -mode binary vendor/bin/<name>.exe`: it sees the standard library in every binary. It
   sees modules only in tela's and the tools', since derohe's GOPATH builds record no dependencies.
5. **Offline cross-check.** Download `https://vuln.go.dev/vulndb.zip` and match each (module, version) above against
   the entries' SEMVER ranges. Crates: `https://osv-vulnerabilities.storage.googleapis.com/crates.io/all.zip` against
   `Cargo.lock`. npm: `POST https://api.osv.dev/v1/query` for playwright and playwright-core.

Re-run all five after any change to a vendored commit, `vendor/tela/go.sum`, `tools/go.sum`, `Cargo.lock`, the
Playwright pin or the toolchain.
