# DMZ: key management

*2026-09-11. Every key DMZ's deployment and use involves, who holds it, what it can do, where it is kept, and what
happens to it after deployment. The rule it serves: no DMZ server, service or operator holds a key that touches a
user's coins, and after deployment no key holds a privileged role in any contract.*

---

## 1. Principles

- **There is no server key.** DMZ runs no server, relay, signer or oracle, so it has no service key to protect.
- **The app holds no key.** Every write is a contract call handed to the user's own wallet over XSWD on `localhost`.
  The app stores no secret in `localStorage`, `sessionStorage`, IndexedDB, a cookie, the URL or a log. It was checked
  line by line in this pass: the app stores only settings, a session flag, a cache of public chain data, a local trade
  ledger, a watchlist, a mute list and a per-session XSWD id (`app/app.js:17, 20`; `app/feed.js:216, 221, 440`;
  `app/stats.js:158`; `app/chat.js:26`; `app/xswd.js:13`).
- **Each person holds their own keys.** The companions run on the person's own machine.
- **After deployment, no privileged key survives.** `factory.bas` v2 has no owner: its treasury and registration fee
  are fixed at install and no function changes them (MT-J15(b), 2026-09-11), so no key holds a role in it at any
  time and there is nothing to revoke. The TELA INDEX's key is dealt with as `MAINNET_DEPLOYMENT.md` states.
  `tools/verifydeploy` checks both, and still fails an older factory that has an owner.

---

## 2. Every key

| Key | Held by | Can do | Lifetime | Kept | After deployment |
|---|---|---|---|---|---|
| **Deployer wallet** (DERO) | the operator | installs the contracts and the app's TELA documents; receives the Founders Pass reserve editions. It holds no role in the factory, which stores no owner (`factory.bas` v2) | the deployment | `netwallet -create` on a machine used for nothing else; the seed is printed once, and `-write-seed` is refused on mainnet (`deploy/README.md`) | **no role, and nothing to revoke**: no contract stores the deployer as an owner. The wallet then holds only any reserve passes and leftover DERO |
| **Treasury wallet** (DERO) | the operator, or whoever the operator names | receives protocol fees; runs `treasurybot`, which claims fees and deposits the pass share | for ever: the treasury address is fixed in the factory at its install and in each market at its install | a separate wallet, never the deployer, backed up off the machine | a **fee recipient, not a privilege**. It cannot change any rule. Losing it strands the fees that reach it; a compromise gives the attacker those fees and nothing else |
| **TELA INDEX key** | the account that installed the INDEX | update the app's document list (`UpdateCode`), if the INDEX was installed at ring 2 (`vendor/tela/TELA-INDEX-1/TELA-INDEX-1.bas:57`) | until the INDEX's final state is reached | the deployer wallet | **must hold no power**: the INDEX is installed or finalised as `MAINNET_DEPLOYMENT.md` prescribes, and `verifydeploy` checks it |
| **Founders Pass installer** | the deployer | sets every pass parameter once, at install; there is no setter | install only | — | none |
| **Vault signers** | **none on mainnet** | — | — | — | — |
| **A user's DERO wallet** | the user | everything the user owns on DERO | the user's | the user's wallet file and seed | — |
| **A user's Monero wallet** | the user | the user's XMR | the user's | `monero-wallet-rpc`'s wallet files on the user's machine | — |
| **A Monero swap's key share** (`s_a` or `s_b`) and gate secret | that party's `xmrswap` | with the other share, the joint XMR | one swap | **in plaintext** in the companion's state file (`-state`, default `<role>.json`), which the `.gitignore` excludes | the recovery card marks the state file as a secret to back up **before** the XMR lock. Losing Alice's file after she sends XMR loses that XMR |
| **A Zcash swap's secret `s`** | the maker's `zecswap` | the maker's claim of the ZEC | one swap | the companion's state file | public once the maker claims, by design |
| **The Zcash spend key** for one spend | the user, handed to `zecswap` for that spend | that one transparent spend | one spend | memory only: loaded for one use and overwritten straight after (`tools/zecswap/swap.go:370-377`, `sign.go:49-50`). In Go this is best effort, because the collector may already have copied it | treat the key as exposed to the companion's process while it runs |

---

## 3. The production procedure

1. **Create wallets on a machine used for nothing else**, with `tools/netwallet -create` against your own `derod`.
   - The seed is printed once. Write it down; do not photograph it or store it on the machine.
   - Mainnet refuses `-write-seed`.
   - Pass the wallet password through `VEIL_WALLET_PASSWORD`, never on the command line.
   - `-rpc-login user:password` (or `VEIL_RPC_LOGIN`) is mandatory on mainnet: `netwallet` exits without it.
2. **Keep three wallets in three roles** (learned on the private testnet, `deploy/README.md`):
   - the deployer installs;
   - the treasury receives and rarely sends;
   - no wallet that mines or receives block rewards ever sits in a transaction path.
3. **Never point `xswdwallet` at a wallet with real funds.** It approves everything automatically, and it refuses any
   daemon that is not a loopback test network.
4. **Install and verify** in one session (`MAINNET_DEPLOYMENT.md`), then take the deployer wallet offline. There is
   no renounce step: the factory has no owner, so the treasury and the registration fee passed at install are final.
5. **Hardware-backed signing.** DERO has no hardware-wallet support that this project knows of, and a DERO wallet needs
   its node to build ring transactions. So the deployer key's protection is short life plus an isolated machine, not
   hardware. The treasury key lives for ever and carries only fees, so protect it by offline backup.

---

## 4. What is kept out of the repository

- **Ignored by `.gitignore`:**
  - chain state and wallets: `.sim/`, `.testnet/`, `.mainnet/`, `*.db`, `*.db.bak`, `*.seed`;
  - signer state: `tools/vaultsigner-*.json`, `signer-*.json`;
  - the Monero companion's files: `bob.json`, `alice.json`, `*.json.lock`, `xmrswap-used.json`, `swap-channel/`;
  - Monero wallet key files.
- **Removed from the index in this pass:** `testnet/signer-30003.json` and `testnet/signer-30004.json`. They held
  simulator signer state (vote memory and one release intent to a fake Monero destination, for a vault that no longer
  exists), with no key or seed, so nothing needs rotating. They remain in git history.
- **Test material that is public by design:**
  - the simulator's 22 wallets (derohe's own test accounts);
  - the Monero example keys in `tools/xmrswap` tests (the public example from docs.getmonero.org);
  - the ZIP 244 vectors.
- **In pushed history:** five Windows executables. They are a provenance problem, not a secret leak. Rewriting history
  is the operator's decision (MT-G08).
- **On this machine, never in git: the private testnet's seeds.** `.testnet/{deployer,owner,treasury}.seed` are
  plaintext 25-word seeds, and the matching `.db` wallets have empty passwords (GAP-030). The folder's Windows ACL lets
  every local user account modify it. **A DERO key is the same key on every network**, so each of these seeds also
  controls the `dero1…` mainnet form of its testnet address. Rules:
  - never send mainnet value to an address whose key was ever created with `-write-seed` or used on a test network;
  - create mainnet wallets fresh (§3);
  - delete those seed files, or move them off the machine, once the testnet is no longer needed. They are the
    operator's files and this pass did not delete them.
- **The full sweep** of the tree and of every commit is `MAINNET_GAP_ANALYSIS.md` §3.
