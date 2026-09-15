// tools/brand/export.mjs: the DMZ wordmark and mark, built from measured geometry, written to docs/brand/.
//
//   node tools/brand/export.mjs            SVGs only (no dependency)
//   PW_DIR=<playwright> node tools/brand/export.mjs --raster
//                                          also the PNG and WebP exports, rasterised by Chromium (tools/uitest/README.md)
//   node tools/brand/export.mjs --app      prints the index.html snippets (inline wordmark, favicon data: URI)
//
// The geometry is the operator's logo (1254 px PNG, black ground) measured by tracing each shape's contour and
// fitting its edges. The source drew the same idea at several nearly-equal values; this rebuild keeps the shapes and
// settles each family on one value (docs/brand/README.md, "Construction"):
//   one stroke t for every stem, bar and band; one slope 4:5 (38.7°) for the D and M diagonals, 45° for the Z;
//   one gap g for every cut inside a letter. Units are the source's pixels, origin at the M's peak.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const out = path.join(root, "docs", "brand");

export const C = {
  ink: "#E8E6E4",   // letterforms (median of interior pixels in the source)
  lime: "#C2FB55",  // the M diagonal
  grey: "#383D45",  // the Z diagonal
  ground: "#000000",
  inkOnLight: "#000000", // the light variant swaps ground and letterforms
};

const S = 0.8, K = Math.hypot(1, S); // D/M slope and its perpendicular factor

// The M: left stem + the arm falling from its peak, the lime band rising to the right edge, the right stem under it.
function letterM({ xL, xR, y0, yB, t, g, xLime }) {
  const arm = t * K, gap = g * K;
  const tipX = (xR + xL) / 2 - gap / (2 * S);                 // arm top edge meets the cut parallel to the lime
  const heelX = (S * (xR + xL) - gap - arm) / (2 * S);        // arm bottom edge meets the same cut
  const left = [[xL, y0], [tipX, y0 + (tipX - xL) * S], [heelX, y0 + arm + (heelX - xL) * S],
    [xL + t, y0 + arm + t * S], [xL + t, yB], [xL, yB]];
  const lime = [[xR, y0], [xR, y0 + arm], [xLime, y0 + arm + (xR - xLime) * S], [xLime, y0 + (xR - xLime) * S]];
  const top = y0 + arm + gap;
  const right = [[xR - t, top + t * S], [xR, top], [xR, yB], [xR - t, yB]];
  return { left, lime, right };
}

// The wordmark: D 0–214, M 238–543, Z 573–828; cap 16–264, the M rises to 0.
export function wordmark({ t = 52, g = 20 } = {}) {
  const T = 16, B = 264;
  // D: chamfered top and bottom, symmetric; the counter is the outline inset by t.
  const dR = 214, a = 110, rise = (dR - a) * S;
  const dOuter = [[0, T], [a, T], [dR, T + rise], [dR, B - rise], [a, B], [0, B]];
  const ox = a - (S * t) / K, oy = T + t / K;                 // a point on the inset top diagonal
  const cTop = T + t, cR = dR - t;
  const cx = ox + (cTop - oy) / S, cy = oy + (cR - ox) * S;
  const dCounter = [[t, cTop], [cx, cTop], [cR, cy], [cR, T + B - cy], [cx, B - t], [t, B - t]];
  const m = letterM({ xL: 238, xR: 543, y0: 0, yB: B, t, g, xLime: 350 });
  // Z: 45°. The grey band hangs from the top bar and continues its cut; it stops at x=zc, short of the M.
  const zL = 573, zR = 828, w = t * Math.SQRT2, gs = g * Math.SQRT2, zc = 557;
  const zTop = [[zL, T], [zR, T], [zR - t, T + t], [zL, T + t]];
  const grey = [[zR - w - t, T + t], [zR - t, T + t], [zR + T - B, B], [zc, B], [zc, zR + T - w - zc]];
  const zBot = [[zR + T + gs - B + t, B - t], [zR, B - t], [zR - t, B], [zR + T + gs - B, B]];
  return { w: 828, h: 264, ink: [dOuter, dCounter, m.left, m.right, zTop, zBot], lime: [m.lime], grey: [grey] };
}

// The mark: the M alone, heavier and more open so its cuts survive 16 px, on a black tile with the brand's cut
// corner. A 64-unit grid: every vertical edge is a multiple of 4, so at 16 px the stems land on whole pixels.
export function mark() {
  const m = letterM({ xL: 8, xR: 56, y0: 12, yB: 52, t: 10, g: 5, xLime: 26 });
  return { w: 64, h: 64, tile: [[0, 0], [54, 0], [64, 10], [64, 64], [0, 64]], ink: [m.left, m.right], lime: [m.lime], grey: [] };
}

const n = (v) => String(Math.round(v * 100) / 100);
export const d = (polys) => polys.map((p) => "M" + p.map(([x, y]) => n(x) + " " + n(y)).join(" ") + "Z").join("");

function svg(g, { ink, lime, grey, ground, label }) {
  const parts = [];
  if (ground) parts.push(`<rect width="${g.w}" height="${g.h}" fill="${ground}"/>`);
  if (g.tile) parts.push(`<path fill="${C.ground}" d="${d([g.tile])}"/>`);
  parts.push(`<path fill="${ink}" fill-rule="evenodd" d="${d(g.ink)}"/>`);
  parts.push(`<path fill="${lime}" d="${d(g.lime)}"/>`);
  if (g.grey.length) parts.push(`<path fill="${grey}" d="${d(g.grey)}"/>`);
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${g.w} ${g.h}" role="img" aria-label="${label}">${parts.join("")}</svg>\n`;
}

// The social avatar: the wordmark on the ground, sized to sit inside the circle most networks crop to.
function avatar() {
  const g = wordmark(), W = 1024, s = 700 / g.w, x = (W - g.w * s) / 2, y = (W - g.h * s) / 2;
  const inner = svg(g, { ink: C.ink, lime: C.lime, grey: C.grey, label: "DMZ" }).replace(/^<svg[^>]*>|<\/svg>\n$/g, "");
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${W}" role="img" aria-label="DMZ"><rect width="${W}" height="${W}" fill="${C.ground}"/><g transform="translate(${n(x)} ${n(y)}) scale(${n(s * 10000) / 10000})">${inner}</g></svg>\n`;
}

// The small cut: the same wordmark with its cuts opened from 20 to 28, for heights under about 40 px (the app's
// header and rail). At 19–26 px on a 1x screen a 20-unit cut is under 2 pixels and the M's three pieces fuse.
export const SMALL = { g: 28 };

export const files = {
  "logo.svg": () => svg(wordmark(), { ink: C.ink, lime: C.lime, grey: C.grey, label: "DMZ" }),
  "logo-on-light.svg": () => svg(wordmark(), { ink: C.inkOnLight, lime: C.lime, grey: C.grey, label: "DMZ" }),
  "logo-small.svg": () => svg(wordmark(SMALL), { ink: C.ink, lime: C.lime, grey: C.grey, label: "DMZ" }),
  "mark.svg": () => svg(mark(), { ink: C.ink, lime: C.lime, label: "DMZ" }),
  "avatar.svg": avatar,
};

async function raster() {
  const pwDir = process.env.PW_DIR;
  if (!pwDir) { console.error("--raster needs PW_DIR (a directory with playwright installed; tools/uitest/README.md)"); process.exit(2); }
  const { chromium } = createRequire(path.join(path.resolve(pwDir), "noop.js"))("playwright");
  const browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1024, height: 1024 }, deviceScaleFactor: 1 });
  // Chromium rasterises the SVG into a canvas at the exact size (the path a browser takes for an <img> or a
  // favicon), then encodes PNG, and WebP at quality 1, which Chromium encodes losslessly.
  const shots = [["mark.svg", "mark", [16, 32, 180, 512, 1024]], ["avatar.svg", "avatar", [1024]]];
  for (const [file, base, sizes] of shots) {
    const src = "data:image/svg+xml;base64," + Buffer.from(files[file]()).toString("base64");
    for (const px of sizes) {
      const enc = await page.evaluate(async ({ src, px }) => {
        const img = new Image(); img.src = src; await img.decode();
        const c = document.createElement("canvas"); c.width = c.height = px;
        c.getContext("2d").drawImage(img, 0, 0, px, px);
        return [c.toDataURL("image/png"), c.toDataURL("image/webp", 1)];
      }, { src, px });
      for (const [i, ext] of [[0, "png"], [1, "webp"]]) fs.writeFileSync(path.join(out, `${base}-${px}.${ext}`), Buffer.from(enc[i].split(",")[1], "base64"));
    }
  }
  await browser.close();
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  fs.mkdirSync(out, { recursive: true });
  for (const [f, make] of Object.entries(files)) fs.writeFileSync(path.join(out, f), make());
  if (process.argv.includes("--raster")) await raster();
  if (process.argv.includes("--app")) {
    // index.html: the small cut, its letterforms in currentColor and its two diagonals classed for app/parts.css
    const w = wordmark(SMALL);
    console.log(`<svg class="mark" viewBox="0 0 ${w.w} ${w.h}" role="img" aria-label="DMZ"><path fill-rule="evenodd" d="${d(w.ink)}"/><path class="mk-l" d="${d(w.lime)}"/><path class="mk-g" d="${d(w.grey)}"/></svg>`);
    const uri = "data:image/svg+xml," + encodeURIComponent(files["mark.svg"]().trim().replace(/"/g, "'")).replace(/%20/g, " ").replace(/%3D/g, "=").replace(/%3A/g, ":").replace(/%2F/g, "/").replace(/%27/g, "'");
    console.log(`<link rel="icon" href="${uri}" type="image/svg+xml">`);
    const png = path.join(out, "mark-32.png");
    if (fs.existsSync(png)) console.log(`<link rel="icon" href="data:image/png;base64,${fs.readFileSync(png).toString("base64")}" sizes="32x32">`);
  }
}
