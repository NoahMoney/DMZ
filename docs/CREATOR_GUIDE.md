# Launching on Veil — a creator's guide

Veil runs entirely on the DERO chain. There is no account to create and no server to trust: your wallet signs two
transactions and your token exists as an immutable contract anyone can verify. This page walks through the launch
form, what the numbers mean, and what happens afterwards.

## Before you start

- A DERO wallet with XSWD enabled (Engram, or `dero-wallet-cli` from the civilware fork, menu 16) and a little DERO
  for fees. A launch costs about 0.25 DERO in fees on the simulator; mainnet fees follow the same storage-gas rule.
- Open Veil, press **Connect**, approve the connection in the wallet. Veil never sees your keys; every transaction
  is shown to you by the wallet before it is sent.

## The launch form (Trade → Launch a token)

One screen. The preview at the top updates as you type and is computed with the same maths the contract runs.

| Field | What it does | Default |
|---|---|---|
| Name, Ticker | Shown everywhere; the ticker is letters and digits, up to 10 | — |
| Graduation target | DERO that must be raised on the curve before the token graduates into its AMM; presets 10 / 50 / 100 / 500 | 100 DERO |
| Image | An `https://` URL, 100×100; optional (a generated gradient avatar is used otherwise) | — |
| Description | Optional, up to 240 characters | — |
| Advanced: supply, decimals | Fixed supply; keep supply × 10^decimals at or below 100 billion so wallets stay fast (DERO decrypts balances by search) | 1 000 000 × 10³ |
| Advanced: creator allocation | Up to 5 % minted to you at launch; shown on the token page | 0 % |
| Advanced: tax and where it goes | Up to 10 % of every trade's DERO leg to the creator, to buybacks that lock tokens forever, or to stakers | none |
| Advanced: tags | Free text shown on the token page | — |

The preview shows the first price, the price at graduation (always 16× the first price), the market cap at
graduation, how many tokens are sold on the curve and how many are reserved for the AMM, and the fee line.

## What the curve is

80 % of the supply (after your allocation) is sold on a bonding curve: the price starts low and rises smoothly as
DERO comes in; buys and sells both move it, and every trade pays the protocol fee (0.5 %). When the graduation
target is reached, the contract itself opens a constant-product AMM seeded with the remaining 20 % of the supply
and the DERO raised, at exactly the curve's final price. That liquidity is owned by the contract and can never be
withdrawn: there is no liquidity to pull and no admin key.

## After launch

- Your token appears in **Markets** and on **Home** within a block, marked **Verified**: the app checks that the
  contract's code is byte-for-byte the shipped template and that it pays the protocol treasury.
- The token page shows a live chart built from the contract's own price points, buy/sell pressure, holders who
  published trades, and an Info tab with every parameter you chose.
- If you chose a tax: creator tax accrues in the contract and you claim it from the Info tab; buyback tax buys and
  locks tokens when anyone triggers it; staker tax is shared block by block among holders who stake in the vault.
- Anyone can create a **holder NFT** for your token (NFTs → Create): a collection whose editions are earned by
  locking a minimum amount of the token for a number of blocks. Useful for rewarding long-term holders.

## The Founders Pass

The exchange's own launch NFT. Registered pass holders receive the promised share of the protocol fees from every
market, paid in DERO to their wallets, and the pass trades on its built-in marketplace. The NFTs tab shows the
promise, what has been deposited and paid, and lets anyone verify it on chain.

## Privacy notes

- Balances are private on DERO. Your trades are private unless you switch on **Post this trade** for a trade.
- Contract calls use ring size 2, so the contract sees your address on every call; nobody else does unless you
  publish.
- Image URLs are fetched by viewers' browsers without a referrer; viewers can turn images off in Settings.

## Screens

Phone and desktop screenshots of the launch form and the token page live in `docs/ui6/` (`08-launch-sheet.png`,
`shot-token.png`, `desk-token.png`).
