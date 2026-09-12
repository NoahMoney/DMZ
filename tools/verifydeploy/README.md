# verifydeploy — check a DMZ deployment against this repository, from a DERO node alone

`verifydeploy` answers two questions without help from anyone who deployed DMZ:

1. Is the code on chain exactly this repository's source?
2. Does any privileged role survive the revocation ceremony?

It needs a DERO daemon RPC and nothing else: no wallet, no key, no operator. It reads with `DERO.GetInfo` and
`DERO.GetSC` and sends nothing.

## Commands a third party runs

```
git clone https://github.com/NoahMoney/DMZ && cd DMZ
git checkout <the commit the release names>
cd tools

# every check; -app compares each TELA DOC with this checkout's app/ (config.js rendered from the record)
go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102 -app ../app

# contracts the record does not carry yet, and the published TELA record, if you have them
go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102 -app ../app \
    -extra htlc=<scid>,swap=<scid>,board=<scid> -tela-record <published telainstall record>

# a bundle you downloaded instead of app/: every DOC must equal its file byte for byte
go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102 -dist <bundle dir>

go run ./verifydeploy -network mainnet -json > report.json    # the same report, for machines
go run ./verifydeploy -roles                                    # the role table it applies, with citations
```

Point `-daemon` at a node **you run and have synced yourself**. Every answer comes from that node, and a node you
do not control can lie about the chain. The exit status is 0 when nothing failed, 1 when at least one check failed,
and 2 on bad usage. Each check prints `PASS`, `FAIL`, `WARN` or `INFO`, and every failure says exactly what differs.

| flag | meaning |
|---|---|
| `-network` | the block of `-networks` to verify. The daemon must agree: mainnet is `testnet=false`, the simulator says "Simulator". |
| `-networks` | the deployment record (default `../deploy/networks.json`). |
| `-contracts` | the sources to compare with (default `../contracts`). |
| `-extra kind=scid,…` | more contracts: `htlc`, `swap`, `board`, `vault`, any `contracts/<kind>.bas`, and `tela=<INDEX>`. |
| `-dist <dir>` / `-app <dir>` | what the DOCs must serve: a built bundle, or the app source (see TELA below). |
| `-tela-record <file>` | a `telainstall` deployment record (path → DOC SCID). The INDEX's DOC list must equal it. |
| `-allow <kind>.<role>=<reason>` | accept one live must-end role, **with a written reason**, printed in the report. Repeatable. |
| `-registry=false` | skip the factory registry. |
| `-market-roles` | also print the roles of every registered market (their creators and treasuries). |
| `-history=false` | do not ask git which commit a hash came from. |

## What each check proves, and what it does not

**record.** The template hashes the app accepts, `templates.<kind>.sha256` in the record, equal
`sha256(reference.Canon(contracts/<kind>.bas))`. That is exactly what `tools/simdeploy` installs and records: this
tool calls the same `reference.Canon`, through `srchash`, which `tools/release` shares. Each legacy hash is traced to
the commit whose file produces it, raw or canonical. *Proves:* the app accepts this source, or named earlier
committed versions of it. *Does not prove:* that an earlier version is free of defects. `simdeploy -revoke`
exists because some are not.

**identity.** For the factory, social, founders, chat, every vault and every `-extra`: sha256 of the code
`DERO.GetSC` returns, against `sha256(reference.Canon(<source>))`. On a mismatch it prints the first differing
line, both as installed and as the source line number, and the commit the installed code comes from if git knows
it. *Proves:* the code at that SCID, on the chain your node follows, is byte for byte this checkout's source.
*Does not prove:* that the source is safe or correct; that a contract the record does not name is DMZ's; or
anything about install parameters. Those are read under roles.

**templates.** Every slot of the factory registry (`pool_<i>`, `launch_<i>`, `nft_<i>` / `nfa_<i>`, each
`HEX(scid)`, `contracts/factory.bas` Register*) is hashed against the record's current and legacy template hashes,
as `app/feed.js verify()` does. Anything unknown is named, along with the source it does match, if any. *Proves:*
every registered market runs an accepted template. *Does not prove:* its fees or treasury. Registration is a paid
hint that anyone can make. Use `-market-roles` to see them.

**roles.** The table in `roles.go` lists, for every contract kind, each storage key that holds a role: its current
value, and whether the role can still act, judged from state by the gates the code has. `-roles` prints the table.
Three tests hold it to `contracts/`. Every cited line must still read what it is cited for. The functions that
STORE or DELETE a role's keys must be exactly the listed ones, so a new setter fails the tests. And every contract
must have an entry. Policies:

| policy | meaning | when live |
|---|---|---|
| must end | a deployer power (an older factory's owner, vault owner before Seal, TELA INDEX owner) | **FAIL**, unless `-allow` names it with a reason |
| guard | the re-initialisation guard (`initd`; for an NFA, `owner` exists) | **FAIL**: Initialize could run again |
| by design | a market creator's power, or a k-of-n federation | listed; a sealed vault's signers are a WARN |
| payee | receives funds; at most pulls what is already its own | listed |
| parameter | fixed at install, or changeable only as printed | listed |
| inert | stored, and read by no function | listed |

A role only an older version of a contract had is marked legacy: the current source writes none of its keys, so it is
reported only when the contract's state carries one. `factory.bas` v2 (MT-J15(b)) has no owner, so a v2 factory shows
no owner row and needs no `-allow`, while a v1 factory whose `owner` is still stored fails on it, and one whose
`renounced` is stored passes.

*Proves:* by the contract's own gates, no must-end role can act now. *Does not prove:* anything about keys held off
chain. A treasury wallet is still someone's wallet, and a vault federation is still trusted with custody.

**tela.** The INDEX's code must be the standard TELA-INDEX-1, compared token for token with the template, because
the tela library re-prints templates. The templates are copied verbatim from `vendor/tela` into
`tela_index_1.bas` and `tela_doc_1.bas` (civilware/tela, MIT, `TELA-LICENSE.txt`), since `vendor/` is not part of
a clone. Who can update it:

- **Installed at ring 2**, the INDEX stores its installer's address as `owner`. That key can call `UpdateCode` (at
  ring 2) for ever and replace the whole app. No TELA-INDEX-1 function changes or clears `owner`. So destroying
  the key is the only revocation, and the chain cannot show it. This is a FAIL.
- **Installed above ring 2**, `SIGNER()` is 33 zero bytes: `vendor/derohe/blockchain/transaction_execute.go`
  `Extract_signer` extracts a signer only at `RingSize == 2`. `IS_ADDRESS_VALID` rejects the zero key, so
  `address()` stores `"anon"`, and `UpdateCode` line 10 refuses every caller for ever. The report then also
  requires `commit` 0 and `hash` (and uint64 key 0) equal to the INDEX's own SCID. This passes.

Every DOC must be a standard TELA-DOC-1: it has no `UpdateCode` and no `UPDATE_SC_CODE`, so it is immutable at any
ring. Its content block, gunzipped as a host serves it, must equal the bundle's file. With `-dist`, all files are
compared byte for byte. With `-app`, the files of `app/` are compared as `genconfig` selects them, and `config.js`
is compared, after its first line, with `netcfg.ConfigJS` of the record. `dist/` is not tracked, and `genconfig`
stamps `config.js` with the time it ran, so a bundle cannot be rebuilt identically. Whatever the bundle, the served
`config.js` must be the record's rendering, `templates/<kind>.bas` must hash to the record's template, and
`manifest.json` must list exactly the served files and the record's contracts. That is what the app trusts. *Proves:*
the app your host serves from this INDEX is these files, and nobody can change it (at ring > 2). *Does not prove:*
that the app's code is safe, or what a TELA host does with it.

## Evidence

- `research/release/verifydeploy-sim.log`: a full run on the simulator. Contracts installed from wallets 30017-30019
  into a scratch record. The check before the ceremony fails on the live factory owner, the unsealed vault owner and
  the ring-2 INDEX owner. After `Seal` and `Renounce`, only the ring-2 INDEX owner fails, and it is accepted with
  `-allow`. A ring-16 INDEX passes clean. A contract that differs from its source by one character fails, and the
  report names the line. (That run used `factory.bas` v1, which had an owner until `Renounce`. A v2 factory has no
  owner to fail on.)
- `research/release/tela-anon-index.log`: the ring-16 INDEX as `DERO.GetSC` reports it (owner `616e6f6e` =
  "anon", commit 0, hash = SCID). The daemon refuses `UpdateCode` from the installer's own wallet, and the call,
  sent anyway, changes nothing. By contrast, the ring-2 INDEX's owner replaces that app in one call.

## Files

`main.go` flags and the run · `chain.go` the two RPCs · `identity.go` code identity, record, git history ·
`registry.go` factory registry · `roles.go` the role table · `tela.go`, `bundle.go` TELA · `basic.go` a small
DVM-BASIC reader · `srchash/` the source hash shared with `tools/release` · `simdrive/` simulator-only test driver
(it refuses any other network) · `testdata/` two INDEXes read back from the simulator.
