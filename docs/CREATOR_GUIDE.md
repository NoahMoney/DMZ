# Launching on DMZ — a creator's guide

DMZ runs entirely on the DERO chain. There is no account to create and no server to trust: your wallet signs two
transactions and your token exists as an immutable contract anyone can verify. This page walks through the launch
form, what the numbers mean, and what happens afterwards.

## Before you start

- A DERO wallet with XSWD enabled (Engram, or `dero-wallet-cli` from the civilware fork, menu 16) and a little DERO
  for fees. A launch costs about 0.25 DERO in fees on the simulator; mainnet fees follow the same storage-gas rule.
- Open DMZ, press **Connect**, approve the connection in the wallet. DMZ never sees your keys; every transaction
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

The preview shows the first price, the price at graduation (always 16× the first price), the fully diluted
valuation at graduation, how many tokens are sold on the curve and how many are reserved for the AMM, and the fee
line.

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
  locks tokens when anyone triggers it, once the AMM has opened at graduation and at most 1 % of the DERO reserve
  every 20 blocks; staker tax accrues to whoever has staked the token in the contract, shared out pro rata to the
  stake whenever anyone stakes, unstakes, claims, or triggers the distribution.
- Anyone can create a **holder NFT** for your token (NFTs → Create): a collection whose editions are earned by
  locking a minimum amount of the token for a number of blocks. Useful for rewarding long-term holders.

## The Founders Pass

The exchange's own launch NFT. A pass has to be registered in the contract to earn; registered holders then receive
the promised share of the protocol fees in DERO, and the pass trades on its built-in marketplace. The share is
fixed when the contract is installed and nobody can change it.

One step is not on chain, and it is the one to know about: a DERO contract cannot pay another contract, so the
protocol treasury is the party that claims the fees from each market and deposits the promised share into the pass.
Claiming a market's fees is permissionless — anyone can trigger it, and it always pays the treasury address
recorded at install — but the deposit into the pass is the treasury's own move. Everything it does is public: the
NFTs tab shows the promise, what has been deposited and what has been paid, so anyone can check the promise is
kept. Anyone can deposit, anyone can push the payouts to registered holders, and a holder can claim theirs
directly.

## Privacy notes

- Balances are private on DERO whatever you do. What you hold is never revealed.
- A contract can only pay you back if it can read your address, and that needs ring size 2 — where the signer is
  public. So an ordinary call names you to the contract *and* to anyone reading the chain, not only to the
  contract. **Post this trade** is a separate choice: it writes your address and that trade into the contract as a
  record everyone can read and the feed can show.
- Private mode in the trade sheet sends the call at ring size 16 to an entrypoint that pays a named address instead
  of the signer: the payer is hidden among sixteen accounts and the receiving address is public. A private trade is
  never posted.
- Image URLs are fetched by viewers' browsers without a referrer; viewers can turn images off in Settings.

## Screens

Phone and desktop screenshots of the launch form and the token page live in `docs/history/screenshots/ui9/`
(`08-launch-sheet.png`, `04-token.png`, `desk-token.png`).
