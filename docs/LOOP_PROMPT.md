# The 20-minute loop — the prompt, verbatim

Recreate with: `/loop 20m` followed by the text below (or CronCreate with cron `*/20 * * * *`). A cron job is
session-only and fires **only while the session is idle** — the three earlier loops never fired because the turn
never ended. Every cycle must end its turn. Take the job down (CronDelete) while a workflow is running, or a cycle
will run the test suite on top of it; put it back when the workflow's result is integrated.

---

AUTONOMOUS R&D CYCLE for DMZ (C:\Users\Wilma\Desktop\fomo). Run ONE full cycle now, then STOP AND END THE TURN — the next cycle fires only while the session is idle, and the previous three loops never fired because the turn never ended. Never pause mid-cycle, never ask permission. Leave the repo green and pushed at the end of every cycle.

NEW THREADS (operator, 2026-09-08 evening) — read research/wrapper/MECHANISM_REVIEW.md first; it is the verdict:
(a) The operator will NOT host anything 24/7. Any design that needs the operator to run a signer, relay or node after deployment is disqualified unless someone else runs it, and the docs must say who. The one trustless pair that works on mainnet DERO today with no host is transparent Zcash via HTLC (contracts/swap.bas + board.bas). Monero on DMZ today means the federation, run by third-party signers, never called trustless; the trustless XMR path is the key-share proof (research/crosschain/poc/proof-xmr) — now built as contracts/htlc.bas + tools/xmrswap, see docs/XMR_SWAP_BUILD.md.
(b) liqdmetal/spore (formerly mycelium; HEAD 254beba) is a no-relay E2E messenger over DERO tx payloads, live on DERO mainnet. Adopt it as the maker/taker rendezvous channel for board.bas offers and swap negotiation. Do NOT adopt its Monero backend (mock-only, deprecated payment-id seam). It is not a bridge.
(c) DERO NFA standard, civilware/artificer-nfa-standard ART-NFA-MS1 (HEAD d286b19): replace contracts/nft.bas with an NFA-compliant contract — exact headers (nameHdr, descrHdr, typeHdr, iconURLHdr, tagsHdr), ownership as one unit of the contract's own asset via ClaimOwnership, the built-in marketplace (Start/BuyItNow/Bid/CloseListing/CancelListing), royalty split — extended with a quote parameter (zero hash = DERO, standard-compatible). Keep headers exact so Engram/Gnomon/dReams index DMZ assets. Spec: docs/NFA_BUILD.md.
(d) Mainnet sequence: 1. transparent-ZEC swap market (board.bas + swap.bas + spore rendezvous, privacy cost of the ZEC leg stated plainly); 2. NFA-compliant assets; 3. federation only with named third-party signers, n >= 2k; 4. the trustless Monero path. OPERATOR'S RULING 2026-09-08: "we want it trustless" — the Monero swap is the active track.

THIS IS A ZCASH AND MONERO EXCHANGE. That is the product, not a framing. Every cycle is judged by whether an XMR or ZEC holder can arrive, see their balance, understand the fees, trade their coin against tokens and NFTs, and withdraw — with DERO visible only as gas. If a change does not serve that, it waits.

THE FIVE STANDING REQUIREMENTS, from the operator. Check every one every cycle and record the evidence:
1. XMR/ZEC FIRST, OBVIOUSLY. The app must read as a Monero and Zcash venue on first glance — home, markets, trade, wrap, NFTs, empty states, copy. Audit each screen by rendering it and reading what it actually says.
2. BALANCES EASY TO SEE. A holder must always know what they hold, of which asset, without hunting: wrapped XMR, wrapped ZEC, DERO for gas, and each token. Say where each number comes from. Never show a total that adds different currencies together.
3. FEES AND COSTS OBVIOUS BEFORE ACTING. Trading fee, protocol share, withdrawal fee, registration fee, gas, minimum deposit, and the price impact — each stated in the asset actually charged, before the button.
4. NOBODY CAN EXTRACT MORE THAN WAS DEPOSITED. The vault invariant is reserve >= sup - inflight. Attack it every cycle: mint, burn, release delay, veto, halt lease, reclaim, slash, the rolling window, and wrapped units used as a QUOTE inside pools and launches. Contract tests and a live simulator run, not reasoning alone.
5. NO OVERLAPPING OR DRAINABLE MARKETS. Duplicate pairs (same base AND same quote) are detected on the data layer (s.splitWith), surfaced everywhere including the trade sheet, ranked deepest first, and the thin half never ranks. Also consider: a token with markets in several quotes, a pool whose quote vault is halted, and what an LP holds when a federation fails. It must always remain fair and accurate.

WORK QUEUE, in order — docs/TODO.md has the current state (read the RESUME section and §0a–0g first; several earlier items are DONE): docs/BACKLOG.md is the reservoir behind it (BL-numbered, priority A → B → C; R items are research that gates a build) — when the queue below is empty or blocked, pull the highest A from the backlog, move it into TODO.md, and build it.
- Finish the swap companion against invariants 8–13 of docs/XMR_SWAP_BUILD.md and re-run tools/simhtlc for all three exits (see docs/TODO.md RESUME).
- Finish the NFA build per docs/NFA_BUILD.md (see docs/TODO.md RESUME).
- The transparent-ZEC swap market on board.bas + swap.bas with spore as the rendezvous (thread a/b/d), starting with a design note and a simulator run of the DERO leg.
- Founders Pass fees earned in a wrapped asset cannot reach holders at all (founders.bas reads DEROVALUE()). Design and build the real answer; the app must never show a fee total that mixes assets.
- The Monero backend for tools/vaultsigner against recorded wallet-rpc fixtures, with a real VerifyRelease (subaddresses, not payment ids).
- deploy/federation.md naming who runs signers, and n >= 2k at Seal (PROTO·BC-4 — a decision for the operator, so write the disclosure the app shows when na < 2k).
- The remaining deferred items in docs/audit/DMZ_FABLE_5_1_FOLLOWUP.md.

TEST LIKE A USER, NOT LIKE A HARNESS. Use a persistent browser profile, load deep links directly, check cold AND warm. Look at the PNGs; do not trust an exit code.

VALIDATE: bash tests/run.sh; (cd tools && go build ./xmrswap/... ./simhtlc/ ./vaultsigner/ ./simdeploy/ ./genconfig/ ./simnfa/ && go vet (same) && go test ./vaultsigner ./xmrswap); node --test tests/js; (cd tools && go run ./genconfig -dist -network simulator); the UI pass: PW_DIR=/c/Users/Wilma/Desktop/ami/web node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-<n>; a live simulator run for anything touching the wrapper (tools/simvault, simwrapmkt) or the swap (tools/simhtlc). Every app file under the TELA DOC cap; vault.bas, pool.bas and launch.bas each under 20000 install storage gas, measured. After any contract change, re-run tools/simdeploy with the existing SCIDs so the outgoing template hash is demoted to legacy.

RECORD: append to docs/history/HISTORY.md with SHIPPED, ALSO FIXED, VERIFIED, UNCONSIDERED (at least 3), NEXT; update docs/TODO.md; append primary sources to research/crosschain/SOURCES.md; commit in focused pieces and push to origin main.

HARD RULES: never touch mainnet; never store private keys in the browser; never proxy transactions through a server; never claim anything is trustless unless its trust assumptions justify it; document every trusted party; fail closed; no machine-specific paths in the repository. LIVENESS BY CONSTRUCTION: the operator revokes their own access after deployment, so any state needing a human to leave it is a defect. Never contradict app/notice.js.
