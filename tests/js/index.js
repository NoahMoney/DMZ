// Makes `node --test tests/js` run this directory on every Node: from v21 the runner reads its arguments as glob
// patterns, and a bare directory is loaded as a module — this is the module it finds. The tests live in
// *.test.mjs; `node --test tests/js/*.test.mjs` runs the same files directly.
import("./amm.test.mjs");
import("./sanitize.test.mjs");
import("./registry.test.mjs");
import("./vaultkeys.test.mjs");
// The repair suites of the 2026-09-10 audit. Three fixers each asked for their file to be added here rather
// than adding it themselves, because three edits to the same four lines would have collided.
import("./appcore.repair.test.mjs");
import("./fees.repair.test.mjs");
import("./nfas.repair.test.mjs");
// the stage tracker's reading of a program's status (tools/*/statusv1_test.go)
import("./track.test.mjs");
// community sentiment: the display rule and the fail-closed reads (app/sentiment.js)
import("./sentiment.test.mjs");
import("./market.test.mjs"); // the market page: price formatter, candle intervals, chart extremes (ui-market)
// profile docs: the logo, pass-image and links parser against hostile docs (app/profile.js)
import("./profile.test.mjs");
// the creation flow's rules (app/create.js): each step's validation, the itemised cost, the unfinished-install record
import("./create.test.mjs");
import("./discovery.test.mjs");
import "./merge.regress.test.mjs";
// the Deposit & withdraw guide and its tracker of vault deposits and withdrawals, and the failure words
import("./move.test.mjs");
// the data layer at scale: refresh planner, backoff, localStorage LRU, incremental registry and decorate
import("./perfscale.test.mjs");
// the promotion auction (app/promo.js): the contract's increment and tie rule, and what slots() and statusOf() show
import("./promo.test.mjs");
