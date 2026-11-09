# Signer runbook: running one federation signer

This runbook covers one signer of a DMZ wrapper vault, from an empty machine to a running daemon, and what to do
when something goes wrong. The design it implements is in `research/wrapper/DESIGN.md`:

- Controls, for the custody rules;
- §C3, for exactly-once release;
- §C7, for signer coordination.

The daemon's own procedures (start, stop, crash recovery, reading the log) are in `research/wrapper/RUNBOOKS.md`;
this file does not repeat them.

**Status on 2026-11-09.** The `sim`, `zcash` (transparent P2SH k-of-n, `tools/vaultsigner/zcashvault`; one deposit and
one withdrawal settled live on zebrad regtest: `research/regtest/vault-zcash-e2e.log`) and `monero`
(`monero-wallet-rpc`, `tools/vaultsigner/monerovault`) backends run. Monero deposits, reserve, address derivation and
single-wallet releases are implemented; a `multisig_txset` is refused because this build has no Monero signing
coordinator, so a Monero federation must not treat `-backend monero` as a multisig release path yet. Where this
runbook says "per the backend's procedure", the exact commands belong to that backend's own documentation (Zcash:
`research/wrapper/RUNBOOKS.md`, "Zcash key ceremony"; Monero: `docs/HANDOVER.md` §5.3), and a signer must not improvise
them. This runbook fixes the order, the custody rules and the coordination steps around them.

**Nothing here touches mainnet.** Mainnet is the operator's decision, behind the gates in `deploy/README.md`.

## 1. What a signer holds

| item | what it is for | secret? | if lost | if stolen |
|---|---|---|---|---|
| DERO signer wallet | casts this signer's votes (`Attest`, `Mint`, `Released`, `Veto`, `Halt`, `Slash`, …) and holds its bond | yes | the signer cannot vote; the others replace it (`AddSigner2`) | one vote of k: the others `Slash` it (§9) |
| coordination identity file | signs this signer's coordination messages; decrypts its peers' messages | yes | rotate (§7): the others re-pin a new identity | the thief reads coordination payloads and can speak as this signer, but cannot make anyone sign anything (§9) |
| Zcash key (HD seed) | this signer's key in the vault's P2SH redeem script | yes, custody | one of the n keys is gone: migrate the reserve (§7) | one of the k needed: migrate the reserve now (§9) |
| Monero multisig wallet (file + password) | this signer's multisig share of the vault wallet | yes, custody | as for Zcash | as for Zcash |
| vaultsigner state file | this signer's votes and release intents | no key, but it must not be lost | conservative restart (RUNBOOKS, crash recovery) | nothing, apart from privacy |
| coordination configuration | the vault, every signer's public identity, the relays | no (public keys only) | copy it again from any signer, and compare the set fingerprint | nothing |

k-of-n tolerates k − 1 compromised signers for safety: they cannot move the reserve or mint. It tolerates n − k lost
or offline signers for liveness: the rest still sign. Every loss spends margin, and some losses need a migration of
the reserve (§7). Examples:

| set | compromised signers who still cannot steal | signers that may be offline | margin after one permanent loss (before migrating) |
|---|---|---|---|
| 2-of-3 | 1 | 1 | 2-of-2: one more loss freezes the reserve |
| 3-of-5 | 2 | 2 | 3-of-4 |

## 2. Host requirements

- **A dedicated machine** (physical, or a VM on hardware this signer controls) that runs nothing else.
- **Diversity across signers.** Never two signers in one organisation, hosting provider, cloud account or
  jurisdiction (DESIGN Controls §5: Ronin, Multichain).
- **Operating system.** A supported Linux LTS or Windows 10/11 Pro or Server, with:
  - full-disk encryption;
  - security updates applied;
  - no browser, mail or desktop use.
- **No inbound ports.** The firewall's default is deny-inbound. The host makes outbound connections only:
  - to the coordination relays (HTTPS);
  - to its own DERO node, `monerod` and Zcash node, whose RPCs are bound to loopback when they run on the same host.

  A relay never runs on a signer host (§4 step 5).
- **Time synchronisation** (chrony, systemd-timesyncd or Windows Time). A coordination message dated more than 5
  minutes ahead is refused, and the daemon logs a clock that disagrees with a relay's.
- **Resources.** Enough CPU, memory and SSD for the full nodes it runs; check each project's current requirements.
  A UPS; and monitoring of disk and node sync.
- **Accounts.**
  - A dedicated unprivileged OS account (`vaultsigner` below) runs the daemon and owns the key files.
  - Operators log in with their own accounts, using multi-factor authentication (NIST SP 800-53 IA-2).
  - Nobody else has an account on the host.
- **An offline machine** for key generation: a laptop with its radios off, or a live USB system that never goes
  online.

## 3. Key generation, offline, with backups

Do all of this on the offline machine. Carry only public material off it, apart from the backup media and the one
copy each key needs on the signer host.

1. **Verify the binary.** Copy `vaultsigner` (and the backend's tools) onto the offline machine and check its SHA-256
   against the release manifest (`tools/release`).
2. **DERO signer wallet.**
   - Create it and write the seed on paper or steel.
   - Restore it on the signer host later (the wallet must be online to vote).
   - It is never a mining address. It holds the bond and fees only.
3. **Coordination identity.**

   ```
   vaultsigner keygen -id <this signer's DERO address> -out coord-identity.key
   ```

   It prints the `id`, the `identity` line and a `fingerprint`, and never the key. The file is created readable by
   this account alone (0600, or a protected owner-only DACL on Windows) and is never overwritten. Then:
   - Back the file up to two encrypted removable media (LUKS, VeraCrypt or BitLocker To Go), kept in different
     places. The passphrase is held by a different person from the media (CCSS 1.03).
   - The identity line is public: send it. The file is not: never paste it, photograph it, or put it in a ticket or
     chat.
4. **Zcash key**, per the backend's procedure: `zcashkey -new <seed file> -network <testnet|mainnet>`.
   - It writes a fresh 32-byte HD seed, owner-only, refusing any path inside a git work tree, and prints only the
     account key (`m/44'/133'/0'` on mainnet, `m/44'/1'/0'` on test networks). Back the seed file up exactly like
     the identity file.
   - Carry off only the account key (`xpub…`/`tpub…`): every vault address is derived from the n account keys by
     non-hardened steps, so no other public key ever needs to leave this machine.
5. **Monero multisig wallet.** It is created during the ceremony (§4), on the signer host, because multisig creation
   is interactive. Back up the wallet file and its password as soon as the ceremony completes.
6. **Wipe the offline machine**, or shut down the live system. Nothing stays on it.

## 4. The ceremony, in order

Hold the ceremony on one day, on a recorded call of every signer and the vault owner. Each step finishes, and is
confirmed by everyone, before the next begins.

1. **Install.** The owner installs the vault (k, cap, limits) and publishes the scid.
2. **Seat the signers.**
   - Each signer sends its DERO signer address to the owner.
   - The owner calls `AddSigner` for each.
   - Each signer calls `Bond()`. Check `bondtot` and every bond before sealing (RUNBOOKS, bonds).
3. **Identities.**
   - Each signer runs §3.3 and sends its `id` and `identity` lines to the others by any channel.
   - On the call, each signer reads its fingerprint aloud.
   - Every signer checks each fingerprint against the line it received. A mismatch means that line was altered or
     mispasted: stop and resend.
4. **The coordination configuration.** One signer drafts it, and every signer uses the same file apart from
   `identity_file` and, optionally, `stores` (placeholders shown):

   ```json
   {
     "version": 1,
     "vault": "<64-hex vault scid>",
     "identity_file": "coord-identity.key",
     "signers": [
       {"id": "<signer A's DERO address>", "identity": "dmzcoord1:<A's identity line>"},
       {"id": "<signer B's DERO address>", "identity": "dmzcoord1:<B's identity line>"},
       {"id": "<signer C's DERO address>", "identity": "dmzcoord1:<C's identity line>"}
     ],
     "stores": ["https://<relay one>:8445", "https://<relay two>:8445"],
     "ttl": "24h", "fetch_timeout": "10s", "max_clock_skew": "5m"
   }
   ```

   Each signer then runs `vaultsigner identity -key coord-identity.key -coord coord.json`. It prints the signer-set
   fingerprint, which everyone reads aloud; all must match. It also confirms that this signer's own identity is
   pinned. The daemon later refuses to start if the configuration names another vault, a signer the vault never
   seated, or an identity that is not this signer's.
5. **Relays.**
   - Run at least two, on hosts that hold no key, operated by different signers or organisations.
   - Each relay reads the same configuration (it needs no identity file):

     ```
     vaultsigner relay -config coord.json -listen 0.0.0.0:8445 -tls-cert <cert.pem> -tls-key <key.pem>
     ```

   - A relay is trusted with nothing. Payloads are signed and encrypted end to end, and the worst it can do is delay
     them. TLS hides from the network when signers talk.
6. **Zcash**, per the backend's procedure. Each signer runs
   `zcashkey -descriptor -network <net> -k <k> -xpubs <the n account keys> -vault <scid> -indexes 3` itself and reads
   out the change address and deposit addresses 0–2; everyone must read the same strings (the keys are sorted inside
   every script, so the order they were typed in does not matter). The descriptor's `view key` line is what the
   signers publish with `SetViewKey` in step 8.
7. **Monero**, per the backend's procedure (`prepare_multisig` → `make_multisig` → `exchange_multisig_keys` until
   `is_multisig` says ready). The rounds may travel over the coordination channel.
   - Each signer confirms that its multisig address equals everyone else's.
   - Record the address and the private view key; the view key is published with `SetViewKey`.
   - Then take the wallet backup (§3.5).
8. **Seal and publish.** The owner calls `SetLimits`, then `Seal`. The signers vote `SetViewKey`.
9. **Start the daemons** (§5). Compare the first pass summary lines, and the `coordination: … fingerprint …` line,
   across all signers.
10. **Rehearse.** Run a small deposit and a withdrawal on testnet, with the exact hardware (DESIGN Controls §4),
    before any real funds.

## 5. Running as a service

The daemon reads the wallet RPC login from `VEIL_RPC_LOGIN` (`user:password`), never from the command line. Every
path below is a placeholder.

### Linux (systemd)

Create `/etc/vaultsigner/env`, owned by root and mode 0600, containing `VEIL_RPC_LOGIN=<user>:<password>`. Then create
`/etc/systemd/system/vaultsigner.service`:

```ini
[Unit]
Description=DMZ vault signer
After=network-online.target
Wants=network-online.target

[Service]
User=vaultsigner
Group=vaultsigner
WorkingDirectory=/var/lib/vaultsigner
EnvironmentFile=/etc/vaultsigner/env
ExecStart=/usr/local/bin/vaultsigner -network testnet -backend <zcash|monero> -daemon 127.0.0.1:<rpc> \
  -wallet 127.0.0.1:<wallet rpc> -vault <scid> -coord /var/lib/vaultsigner/coord.json <backend flags>
Restart=on-failure
RestartSec=30
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/vaultsigner
UMask=0077

[Install]
WantedBy=multi-user.target
```

Enable it with `systemctl enable --now vaultsigner`, and read its output with `journalctl -u vaultsigner -f`. Keep
`coord-identity.key`, the state file and the wallet files in `/var/lib/vaultsigner`, owned by `vaultsigner`, mode
0600. A relay gets the same kind of unit, running `vaultsigner relay …` under its own account, on its own host.

### Windows (Task Scheduler; no third-party service wrapper)

Run the daemon as a dedicated local account (for example `vaultsigner`) that is not an administrator. Set
`VEIL_RPC_LOGIN` as a **user** environment variable of that account, never a machine-wide one. The data directory
must be readable by that account only. From an elevated PowerShell:

```powershell
$act = New-ScheduledTaskAction -Execute '<install dir>\vaultsigner.exe' -WorkingDirectory '<data dir>' `
  -Argument '-network testnet -backend <zcash|monero> -daemon 127.0.0.1:<rpc> -wallet 127.0.0.1:<wallet rpc> -vault <scid> -coord <data dir>\coord.json <backend flags>'
$trg = New-ScheduledTaskTrigger -AtStartup
$set = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) `
  -ExecutionTimeLimit ([TimeSpan]::Zero) -StartWhenAvailable
Register-ScheduledTask -TaskName 'vaultsigner' -Action $act -Trigger $trg -Settings $set `
  -User '<host>\vaultsigner' -Password (Read-Host 'password') -RunLevel Limited
```

Redirect output to a log file with a wrapper `cmd /c … >> <data dir>\vaultsigner.log 2>&1`, or collect it with the
monitoring agent. `vaultsigner keygen` makes the identity file owner-only when it creates it. The daemon refuses to
load an identity file whose ACL lets any account read it other than its owner, SYSTEM and Administrators.

### Both

- **One daemon per signer, ever.** The state lock refuses a second one on the same file. Never copy a running
  signer's files to a second machine and start both (RUNBOOKS, stop and move).
- **After an upgrade**, compare the binary's hash with the release manifest before restarting.

## 6. Monitoring

What to watch, and what it means:

- **The pass summary** (`vault: indexes …, supply …`). Compare it across signers: a signer that is behind shows it
  here (RUNBOOKS, reading the log).
- **`coordination: every store reachable`** is logged when the channel recovers. Any other `coordination:` line
  names the stores that are down. Alert when no store has been reachable for 30 minutes (DESIGN Controls §7).
  - Withdrawals wait while the channel is down. Deposits and mints do not.
  - A clock warning means fix time synchronisation now: beyond 5 minutes, messages are refused.
- **`coord: dropped a message from <store>: <reason>`**:
  - an occasional drop is noise;
  - repeated `bad signature`, or `signed by a key no signer pinned`, means junk or an attack on that relay: the
    channel is unaffected, but tell the relay's operator;
  - `dated in the future` means someone's clock is wrong;
  - `for another vault` or `for another round` from a pinned id means a misconfigured or misbehaving relay.
- **`… encrypts to a signer set that differs from this node's pinned set`** means the configurations have diverged.
  Compare set fingerprints (`vaultsigner identity -key … -coord …`) and fix the odd one.
- **`… replaced its payload for a round`** is normal after a peer restarts. Repeated replacements while nothing
  restarts are worth a question.
- **Relays** answer `GET /v1/health` with counts only; use it as an uptime probe.
- **Also watch** node sync on all three chains, disk, time synchronisation, and the backup dates.

## 7. Rotation and recovery

**Rotating the coordination identity**, yearly or on any suspicion:

1. Run `vaultsigner keygen -id <address> -out coord-identity.new`, on the offline machine or the host.
2. Send the new identity line, and read its fingerprint on a call.
3. Every signer replaces that signer's `identity` in its configuration, checks the set fingerprint with the others,
   and restarts.
4. Destroy the old file and its backups: overwrite, then delete; for media, destroy them (CCSS 2.02).

While configurations differ, messages between the two groups are refused. That is a delay only, so rotate between
withdrawals.

**Restoring a signer on a new host** (the old one is dead, and not suspected):

1. Build the host (§2).
2. Restore from backups: the DERO wallet seed, `coord-identity.key` (mode 0600 again), the Zcash seed and the Monero
   wallet file.
3. Copy the state file if it survived. If it did not, the daemon restarts conservatively (RUNBOOKS, crash recovery).
4. Make sure the old host can never start again before starting the new one.
5. Nothing changes for the other signers.

**Replacing a lost or removed signer**, when its keys are gone or it must leave:

1. The remaining signers keep running: n − k of them may be missing.
2. **DERO.** If the departing signer misbehaved, `Slash(target)` (k votes). Then `AddSigner2(<new address>)` (k
   votes, the candidate bonds first; RUNBOOKS, replacing a signer).
3. **Coordination.** Every configuration drops the old id and pins the new signer's identity. Check the set
   fingerprint again, and restart. The daemon warns about a pinned signer the vault has slashed until this is done.
4. **The foreign keys cannot be patched.** A P2SH redeem script and a Monero multisig wallet are fixed sets of n
   keys, and the replacement cannot sign for the old outputs. The federation must migrate:
   - create the new key set with the replacement, by the ceremony's steps 6 and 7;
   - k old signers sign a sweep of the entire reserve to the new address;
   - re-attest the deposit addresses (re-attestation bumps `dver_<j>`);
   - publish the new view key (`SetViewKey`).

   Until the migration is done, the old set runs one key short. With 2-of-3 that is 2-of-2, and one more loss freezes
   the reserve, so migrate within days, not weeks.

## 8. What a signer must never do

- Never run the same signer in two places, or two daemons on one state file.
- Never print, copy into chat, email, tickets, CI or a repository, or photograph:
  - the identity file, the wallet seed, the Zcash seed or the Monero wallet file;
  - `VEIL_RPC_LOGIN`.
- Never keep a key on a shared or synchronised folder. A dir: coordination store carries only sealed messages, never
  keys.
- Never pin an identity line you have not confirmed by fingerprint on a second channel, and never accept a
  configuration whose set fingerprint differs from everyone else's.
- Never run a relay, or anything that listens on the internet, on a signer host.
- Never sign or approve anything a peer sends that your own backend did not build and check. The channel carries
  claims, not orders.
- Never lower the confirmation policy, edit the state file with the daemon running, or restart it under a different
  `-state` to "unstick" it.
- Never hand signing rights to someone else's host "temporarily". Stale delegations are how Ronin lost its quorum.
- Never describe the wrapper as trustless (HANDOVER §5.7).

## 9. Incident response: a suspected key compromise

First, whatever the key:

- Tell the other signers on the out-of-band channel agreed at the ceremony.
- If the host itself is suspected, stop its daemon: a stopped signer is a delay, a compromised one may be worse.
- Preserve the logs and a disk image, and write down the timeline as it happens.

Then by key:

- **Coordination identity only.**
  - The thief can read payloads encrypted to the signer set. For Monero multisig info that means spend linkability;
    for txsets, amounts and destinations, which DERO already shows.
  - The thief can post messages as this signer. Every other backend checks what it signs against its own chain view,
    so the thief cannot make anyone sign a wrong payment.
  - Rotate the identity (§7), and treat past payloads as read.
- **DERO signer wallet.**
  - The thief can cast this signer's single vote.
  - The other signers `Slash(target)` (k votes). This removes the signer, voids its pending votes and sends its bond
    to the treasury.
  - Seat a replacement with `AddSigner2`. The foreign keys are unaffected.
- **A foreign key (Zcash seed or Monero wallet), fewer than k compromised.** Migrate the reserve now (§7): the thief
  needs only the difference to k.
  - Sweep with k honest signatures to a fresh key set.
  - `Slash` the compromised signer, re-attest, and vote `SetViewKey` for the new key.
- **k or more foreign keys compromised.** Assume the reserve may already be gone.
  - The daemons see the shortfall and vote `Halt(1)` on their own.
  - Follow RUNBOOKS, "incident: the foreign reserve is below the wrapped supply", and publish the numbers.
- **The host.** Assume every key on it is compromised, and apply all of the above.

Afterwards:

- Publish a post-mortem.
- Refresh every other signer's coordination identity if the relays' traffic may have been recorded alongside a stolen
  key.
- Review the offboarding and host controls (DESIGN Controls §5: Orbit).
