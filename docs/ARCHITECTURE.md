# Veil architecture (as built)

```
┌──────────────────────────────────────────────────────────────────────┐
│ Browser (served by a TELA host: Engram / tela-cli / tools/telainstall) │
│  index.html styles.css ui.css parts.css desk.css config.js rpc.js xswd.js│
│  screens.js token.js app.js  templates/*.bas        ← 14 TELA-DOC-1   │
│        │ XSWD ws://localhost:44326/xswd  (wallet methods, prompts)     │
│        │ ws://<node>/ws  JSON-RPC  (DERO.GetSC, GetTransaction, …)     │
│        ▼                                                              │
│  User's wallet (Engram / civilware dero-wallet-cli) ── signs transfer  │
│        ▼                                                              │
│  User's node (or the wallet's node)                                   │
│        ▼                                                              │
│  Contracts (DVM-BASIC, immutable)                                     │
│   factory.bas  registry hints + treasury pointer (owner: operator)    │
│   pool.bas     LP-owned x·y=k pool per asset↔DERO, 50 bps fee         │
│   launch.bas   per-token: curve → locked AMM, fee, tax, staking       │
│   nft.bas      per-collection: hold-to-mint editions                  │
│   social.bas   profiles + follows                                     │
│   TELA-INDEX-1 app manifest, ringsize 2 (updatable, commit chain)     │
└──────────────────────────────────────────────────────────────────────┘
```

Reads: the app never trusts a listing. For every SCID the factory names it fetches the code, hashes it, requires a
match with a shipped template (`config.js`), and requires the contract's treasury to equal the factory's. Trades in
the feed are the pool's own `t_<n>` records, written only when the trader passed `publish = 1`; nobody can insert a
trade they did not make, and unpublished trades leave no record beyond the reserve change.

Writes: gas estimate on the node → `transfer` through XSWD (the wallet prompts) → poll `DERO.GetTransaction`.
Fees are the estimate's `gasstorage` plus a small margin. Everything is ringsize 2.

No server: an optional Gnomon read cache is feature-flagged off and the app falls back to the node.

## Why one contract per launch, with a built-in AMM
The DVM cannot call, read, install or fund other contracts (DECISIONS §1.2, §2). A launch therefore owns its whole
lifecycle: curve, graduation and the AMM are one contract, and the liquidity is literally unwithdrawable because no
entrypoint exists for it. `pool.bas` exists for assets that already exist on DERO.

## Placeholders — explicitly not built (RISKS R-8)
- **Tokenised equities / "stocks".** Would need a regulated issuer and an oracle; DERO has neither. A synthetic
  price-tracker is a derivative. If this is ever revisited it belongs in a separate contract family with its own
  legal basis, not in `launch.bas`.
- **Cross-chain memecoins (Solana / Base / …).** No trust-minimised bridge to DERO exists; a custodial bridge would
  contradict the product. The `asset` field of `pool.bas` is the only integration point a future bridge asset would need.
- **Fiat on-ramp.** Out of scope; wallets already handle DERO acquisition.

## Data growth and the indexer
`t_<n>` and `p_<n>` grow with activity and the app currently reads contracts with `variables:true`. When a contract's
variable set gets large, switch the app to `keysstring` ranges (already supported by `rpc.js`) or enable the Gnomon
cache in Settings. Neither changes any contract.

## Networks (2026-09-06)

The same contracts and app run on the simulator, a testnet and mainnet. `deploy/networks.json` records each
network's SCIDs, template hashes and TELA index; `tools/genconfig` renders `app/config.js` (development) or
`dist/<network>/` (one TELA install per network); `tools/telainstall`, `tools/netwallet` and `tools/vaultsigner` take
`-network`. The app checks the connected node's network name against its build and warns on a mismatch. The
testnet in this repo is a private chain (`deploy/README.md (Testnet)`): the vendored `derod` accepts a genesis transaction
from `DERO_PRIVATE_GENESIS_TX` so the owner account exists at genesis and can mine; everything else registers
through the normal proof-of-work registration.
