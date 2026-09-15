// Regressions found by the rendered-app inspection of 2026-09-15, both from merging branches together.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const app = (f) => readFileSync(join(ROOT, "app", f), "utf8");

test("index.html has exactly one navigation, and it is closed", () => {
  const html = app("index.html");
  const opens = (html.match(/<nav\b/g) || []).length, closes = (html.match(/<\/nav>/g) || []).length;
  // an unclosed old <nav> wrapped the new one, the sheet and the toasts: two bars on phones, over the Buy button
  assert.equal(opens, 1, "one <nav>");
  assert.equal(closes, 1, "one </nav>");
  assert.doesNotMatch(html, /data-tab="(zswap|xmr)"/, "no swap tab: the swaps are removed");
});

test("a pair named as text uses U.pairText, never String() of the U.pair element", () => {
  const src = app("market.js");
  assert.match(src, /U\.pairText\(s\)/, "market.js names the pair with U.pairText");
  assert.doesNotMatch(src, /String\(U\.pair\(/, "no String() of an element");
});
