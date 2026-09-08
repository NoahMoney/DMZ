// Loads app/amm.js the way the browser does — as a script that assigns to window.Veil — without a browser.
// The file is an IIFE over `window`, so a one-key object is the whole shim; nothing here is a dependency.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

export const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const window = { Veil: {} };
new Function("window", readFileSync(join(ROOT, "app", "amm.js"), "utf8"))(window);
export const A = window.Veil.amm;
export const B = BigInt;

// mulberry32: a deterministic generator so a differential run is reproducible from the seed in its failure.
export function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
// A BigInt in [0, max) built from two 32-bit draws, so amounts up to 2^64 come out without a float in between.
export function randU(r, max) {
  const hi = B(Math.floor(r() * 2 ** 32)), lo = B(Math.floor(r() * 2 ** 32));
  return ((hi << 32n) | lo) % max;
}
