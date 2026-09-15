// statuscheck.mjs: the status answers the companions' Go tests write (tools/xmrswap and tools/zecswap, statusv1_test.go)
// run through the app's own parser, app/track.js, as the swap screens run them (docs/SWAP_STATUS.md). Every answer must
// parse; every step's state and chain must be one the app reads; every amount, count, deadline and estimate the
// companion sent must survive the parse, and every label and sentence whole; and the tracker must draw it. It is not a
// *.test.mjs: the Go tests run it on the directory of answers they wrote, and fail when it exits 1.
//
//   node tests/js/statuscheck.mjs <dir>
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const window = { Veil: { ui: { clean: (s, n) => String(s || "").slice(0, n), h: () => ({}), button: () => ({}), toast: () => {} }, shortAddr: (a) => a.slice(0, 8) } };
new Function("window", readFileSync(join(ROOT, "app", "track.js"), "utf8"))(window);
const T = window.Veil.track;

const dir = process.argv[2];
if (!dir) { console.error("usage: node tests/js/statuscheck.mjs <dir>"); process.exit(2); }
const files = readdirSync(dir).filter((f) => f.endsWith(".json")).sort();
if (files.length === 0) { console.log("no status answers in " + dir); process.exit(1); }
const bad = [];
const has = (v) => v !== null && v !== undefined;
for (const f of files) {
  const j = JSON.parse(readFileSync(join(dir, f), "utf8"));
  const why = [];
  const r = T.parse(j, { program: j.program, network: j.network, contracts: [j.contract] });
  if (!r.ok) why.push("refused: " + r.why);
  else {
    const s = r.s;
    if (s.outcome !== j.outcome) why.push("outcome " + s.outcome);
    if (s.next !== j.next) why.push("the next sentence was cut");
    if (s.updated === null) why.push("updated is unreadable");
    if (s.lock !== (has(j.lock) ? j.lock : null)) why.push("lock " + s.lock);
    if (has(j.deroAmount) && s.dero === null) why.push("deroAmount unreadable");
    if (has(j.foreignAmount) && s.foreign === null) why.push("foreignAmount unreadable");
    if (has(j.fee) && s.fee === null) why.push("fee unreadable");
    s.steps.forEach((x, i) => {
      const w = j.steps[i];
      if (x.state === "unreadable" || x.chain === "?") why.push(w.id + ": state or chain unreadable");
      if (x.label !== w.label) why.push(w.id + ": the label was cut");
      if (has(w.conf) !== (x.conf !== null) || has(w.need) !== (x.need !== null)) why.push(w.id + ": a count was lost");
      if (has(w.deadline) !== (x.deadline !== null)) why.push(w.id + ": the deadline was lost");
      if (x.deadline && (x.deadline.height === null || has(w.deadline.eta) !== (x.deadline.eta !== null))) why.push(w.id + ": the deadline's height or estimate was lost");
      if (w.txid && x.txid !== w.txid) why.push(w.id + ": the txid was lost");
      if (x.deadline) T.left(x.deadline, 0, s.updated);
    });
    if (j.outcome && T.nextDeadline(s.steps) !== null && ["active", "waiting"].includes(T.current(s.steps)?.state) === false) why.push("an ended swap counts a deadline down");
    T.render(s.steps, 0);
  }
  if (why.length) bad.push(f + ": " + why.join("; "));
}
if (bad.length) { console.log(bad.join("\n")); process.exit(1); }
console.log("app/track.js read all " + files.length + " status answers in " + dir.split(/[\\/]/).slice(-1)[0] + " whole");
