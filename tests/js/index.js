// Makes `node --test tests/js` run this directory on every Node: from v21 the runner reads its arguments as glob
// patterns, and a bare directory is loaded as a module — this is the module it finds. The tests live in
// *.test.mjs; `node --test tests/js/*.test.mjs` runs the same files directly.
import("./amm.test.mjs");
