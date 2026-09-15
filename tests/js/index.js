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
// the Monero swap screen's exit windows, the payout-address checks, and the swap stage tracker
import("./xmr.test.mjs");
import("./payout.test.mjs");
import("./track.test.mjs");
// community sentiment: the display rule and the fail-closed reads (app/sentiment.js)
import("./sentiment.test.mjs");
