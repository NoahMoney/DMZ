// app/profile.js is the whole defence against a hostile profile doc: a creator writes its bytes, and the app turns
// them into an <img> and into links on every card and market page that names it. These are the docs an attacker
// installs. tests/js/fixtures/profiledoc-vectors.json holds the same verdicts for tools/profiledoc (Go).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";
import { chunk, png, riff, vp8l, vp8, vp8x } from "./profileimg.mjs";

const window = { Veil: { ui: { router: { on() {} } } } };
new Function("window", readFileSync(join(ROOT, "app", "profile.js"), "utf8"))(window);
const P = window.Veil.profile;

const b64 = (b) => Buffer.from(b).toString("base64");
const lj = (l) => (l === undefined ? "" : b64(Buffer.isBuffer(l) ? l : Buffer.from(typeof l === "string" ? l : JSON.stringify(l))));
const doc = (kind, img, links) => P.compose(kind, img ? b64(img) : "", lj(links));
const SVG = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script></svg>');

test("a well-formed logo doc reads back: PNG and each WebP layout, from a data: URI whose type comes from the magic", () => {
  for (const [img, type, w, h] of [
    [png(160, 160), "png", 160, 160],
    [riff([["VP8L", vp8l(160, 160)]]), "webp", 160, 160],
    [riff([["VP8 ", vp8(128, 128)]]), "webp", 128, 128],
    [riff([["VP8X", vp8x(144, 144)], ["ALPH", Buffer.alloc(9)], ["VP8 ", vp8(144, 144)]]), "webp", 144, 144],
  ]) {
    const d = P.parse(doc("logo", img, { website: "https://example.org/" }));
    assert.ok(d, type);
    assert.deepEqual([d.kind, d.type, d.w, d.h, d.bytes], ["logo", type, w, h, img.length]);
    assert.equal(d.uri, "data:image/" + type + ";base64," + b64(img));
    assert.deepEqual(d.links, { website: "https://example.org/" });
  }
});

test("size caps: logo 8,192 bytes and 256 px, pass 14,000 bytes and 384 px, the code 19,500 bytes", () => {
  const mid = png(64, 64, [chunk("tEXt", Buffer.alloc(8300, 65))]);
  assert.ok(mid.length > 8192 && mid.length < 14000);
  assert.equal(P.parse(doc("logo", mid)), null, "a logo over 8,192 bytes");
  assert.ok(P.parse(doc("pass", mid)), "the same bytes as a pass image");
  const base = png(64, 64, [chunk("tEXt", Buffer.alloc(0))]).length, exact = png(64, 64, [chunk("tEXt", Buffer.alloc(14000 - base))]);
  assert.equal(exact.length, 14000);
  const full = doc("pass", exact);
  assert.ok(P.parse(full), "a pass image of exactly 14,000 bytes");
  assert.ok(full.length <= P.MAX_CODE && full.length + 25 <= 20000, "the largest doc installs under the 20,000 storage-gas cap: " + full.length);
  assert.equal(P.parse(doc("pass", png(64, 64, [chunk("tEXt", Buffer.alloc(14001 - base))]))), null, "14,001 bytes");
  assert.equal(P.parse(doc("logo", png(257, 16))), null); assert.ok(P.parse(doc("logo", png(256, 256))));
  assert.equal(P.parse(doc("pass", png(385, 385))), null); assert.ok(P.parse(doc("pass", png(384, 384))));
  assert.equal(P.parse(doc("logo", riff([["VP8L", vp8l(16384, 16384)]]))), null, "a 16k WebP header");
  assert.equal(P.parse(doc("logo", png(1, 1 << 30))), null, "a height of 2^30 in IHDR");
  assert.equal(P.parse(full + "\n"), null);
  assert.equal(P.parse(doc("logo", png(16, 16)) + "x".repeat(P.MAX_CODE)), null, "code over the cap");
});

test("SVG is refused however it is dressed: as the image, after a PNG, or as a link", () => {
  for (const img of [SVG, Buffer.concat([Buffer.from('<?xml version="1.0"?>'), SVG]), Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), SVG]),
    Buffer.concat([png(16, 16).subarray(0, 8), SVG]), Buffer.concat([png(16, 16), SVG]), Buffer.concat([Buffer.from("RIFF\0\0\0\0WEBP"), SVG])]) {
    assert.equal(P.parse(doc("logo", img)), null); assert.equal(P.parse(doc("pass", img)), null); assert.equal(P.imageInfo(img), null);
  }
  assert.equal(P.parse(doc("logo", png(16, 16), { website: "data:image/svg+xml;base64," + b64(SVG) })), null);
});

test("only PNG and WebP, by their magic bytes and a structure that tiles the file", () => {
  const p = png(16, 16), w = riff([["VP8L", vp8l(16, 16)]]);
  const set = (b, f) => { const c = Buffer.from(b); f(c); return c; };
  const bad = {
    jpeg: Buffer.concat([Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0, 16, 74, 70, 73, 70, 0]), Buffer.alloc(80), Buffer.from([0xff, 0xd9])]),
    gif: Buffer.concat([Buffer.from("GIF89a"), Buffer.alloc(80)]),
    bmp: Buffer.concat([Buffer.from("BM"), Buffer.alloc(80)]),
    "PNG signature off by one byte": set(p, (c) => { c[1] = 0x51; }),
    "no IEND": p.subarray(0, p.length - 12),
    "a byte after IEND": Buffer.concat([p, Buffer.from([0])]),
    "IHDR not first": Buffer.concat([p.subarray(0, 8), chunk("tEXt", Buffer.from("a")), p.subarray(8)]),
    "two IHDR": png(16, 16, [chunk("IHDR", Buffer.alloc(13))]),
    "no IDAT": Buffer.concat([p.subarray(0, 33), Buffer.alloc(12), chunk("IEND", Buffer.alloc(0))]),
    "a chunk longer than the file": set(p, (c) => { c.writeUInt32BE(1 << 30, 33); }),
    "a zero width": png(0, 16),
    "WEBX": set(w, (c) => c.write("WEBX", 8, "latin1")),
    "RIFF size off by 8": set(w, (c) => c.writeUInt32LE(w.length, 4)),
    "VP8 without its start code": riff([["VP8 ", set(vp8(16, 16), (c) => { c[3] = 0; })]]),
    "VP8L without its signature": riff([["VP8L", set(vp8l(16, 16), (c) => { c[0] = 0x2e; })]]),
    "two bitstreams": riff([["VP8X", vp8x(16, 16)], ["VP8 ", vp8(16, 16)], ["VP8L", vp8l(16, 16)]]),
    "VP8X and no bitstream": riff([["VP8X", vp8x(16, 16)]]),
    "a second VP8X": riff([["VP8X", vp8x(16, 16)], ["VP8X", vp8x(16, 16)], ["VP8 ", vp8(16, 16)]]),
    "a lower-case fourcc": riff([["VP8L", vp8l(16, 16)], ["exif", Buffer.alloc(4)]]),
    "a truncated chunk": w.subarray(0, w.length - 3),
  };
  for (const [name, img] of Object.entries(bad)) assert.equal(P.parse(doc("logo", img)), null, name);
});

test("animation is refused: APNG, the WebP animation flag, ANIM and ANMF", () => {
  for (const img of [png(16, 16, [chunk("acTL", Buffer.alloc(8))]), png(16, 16, [chunk("fcTL", Buffer.alloc(26))]),
    riff([["VP8X", vp8x(16, 16, 0x02)], ["VP8 ", vp8(16, 16)]]), riff([["VP8X", vp8x(16, 16, 0)], ["ANIM", Buffer.alloc(6)], ["VP8 ", vp8(16, 16)]]),
    riff([["VP8X", vp8x(16, 16, 0)], ["ANMF", Buffer.alloc(16)]])]) assert.equal(P.parse(doc("logo", img)), null);
});

test("the wrapper is fixed byte for byte: a doc with any other code is not a doc", () => {
  const good = doc("logo", png(16, 16), { x: "https://x.com/dmz" });
  assert.ok(P.parse(good));
  const bad = {
    "a changed RETURN": good.replace("10 RETURN 0", "10 RETURN 1"),
    "an Update entrypoint inside": good.replace("End Function\n", "End Function\nFunction Update(c String) Uint64\n10 UPDATE_SC_CODE(c)\n20 RETURN 0\nEnd Function\n"),
    "an entrypoint appended": good + "Function Drain() Uint64\n10 RETURN 0\nEnd Function\n",
    "an Initialize that stores": good.replace("10 RETURN 0", "5 STORE(\"a\", 1)\n10 RETURN 0"),
    CRLF: good.replace(/\n/g, "\r\n"),
    "no final newline": good.slice(0, -1),
    "a leading space": " " + good,
    "a byte-order mark": "﻿" + good,
    "another kind": good.replace("// logo ", "// icon "),
    "a capitalised kind": good.replace("// logo ", "// Logo "),
    "a second image line": good.replace("// links ", "// logo AAAA\n// links "),
    "a comment between": good.replace("\n// links", "\n// hi\n// links"),
    "two spaces": good.replace("// logo ", "// logo  "),
    "a tab": good.replace("// logo ", "// logo\t"),
    "the lines swapped": P.HEAD + good.slice(P.HEAD.length).split("\n").slice(0, 2).reverse().join("\n") + "\n",
    "another version": good.replace("doc v1", "doc v2"),
    "the head only": P.HEAD,
    empty: "",
  };
  for (const [name, code] of Object.entries(bad)) assert.equal(P.parse(code), null, name);
  for (const v of [null, undefined, 123, {}, ["x"]]) assert.equal(P.parse(v), null);
});

test("base64 must be canonical: no whitespace, no base64url, no stray padding bits, no bad length", () => {
  const img = b64(png(16, 16)), l = b64(Buffer.from('{"x":"https://x.com/a"}')); // 23 bytes: one '=' of padding
  assert.ok(P.parse(P.compose("logo", img, l)));
  const A = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/", i = l.length - 2;
  const stray = l.slice(0, i) + A[A.indexOf(l[i]) | 1] + "=";
  assert.notEqual(stray, l); assert.equal(Buffer.from(stray, "base64").toString(), Buffer.from(l, "base64").toString());
  for (const [im, lk] of [[img, stray], [img.slice(0, -1), l], [img + "====", l], [img.slice(0, 40) + " " + img.slice(40), l], [img, l.replace(/\+/g, "-").replace(/\//g, "_") + "-_"], [img, "=" + l], [img.replace(/=+$/, ""), l]])
    assert.equal(P.parse(P.compose("logo", im, lk)), null, JSON.stringify(lk).slice(0, 30));
});

test("links: https on the real hosts only; javascript:, data:, look-alike hosts and every trick are refused", () => {
  const ok = { website: "https://example.org/path?a=1&b=2#top", x: "https://x.com/dmz", telegram: "https://t.me/dmz", discord: "https://discord.gg/abc", github: "https://github.com/NoahMoney/DMZ" };
  const d = P.parse(doc("logo", null, ok));
  assert.deepEqual(d.links, ok);
  for (const [k, v] of [["x", "https://twitter.com/dmz"], ["x", "https://www.x.com/dmz"], ["discord", "https://discord.com/invite/abc"], ["github", "https://www.github.com/x"], ["website", "https://sub.example.co.uk/"], ["github", "https://GitHub.com/x"], ["website", "https://example.org/%20"]])
    assert.ok(P.link(k, v), k + " " + v);
  const bad = [
    ["website", "javascript:alert(1)"], ["website", "JAVASCRIPT:alert(1)"], ["website", " https://example.org/"], ["website", "data:text/html;base64,PHNjcmlwdD4="],
    ["website", "http://example.org/"], ["website", "//example.org"], ["website", "https:example.org"], ["website", "https://example.org/\"onmouseover=\"x"],
    ["website", "https://example.org/<svg>"], ["website", "https://example.org/a b"], ["website", "https://example.org/`x`"], ["website", "https://user:pw@example.org/"],
    ["website", "https://example.org:8443/"], ["website", "https://127.0.0.1/"], ["website", "https://[::1]/"], ["website", "https://localhost/"],
    ["website", "https://xn--80ak6aa92e.com/"], ["website", "https://exаmple.org/"], ["website", "https://example.org\\@evil.invalid/"], ["website", "https://example.org/%zz"],
    ["website", "https://example.org./"], ["website", "https://-example.org/"], ["website", "https://example.org/ "], ["website", "https://example.org/" + "a".repeat(190)],
    ["x", "https://x.com.evil.invalid/dmz"], ["x", "https://evil.invalid/x.com"], ["x", "https://x.com@evil.invalid/"], ["x", "https://evil.invalid@x.com/"],
    ["x", "https://x.co/dmz"], ["x", "https://mobile.twitter.com/dmz"], ["x", "http://x.com/dmz"], ["telegram", "https://t.me.evil.invalid/"], ["telegram", "https://telegram.me/dmz"],
    ["discord", "https://discord.gg.evil.invalid/"], ["discord", "https://discordapp.com/invite/x"], ["discord", "https://dlscord.gg/x"],
    ["github", "https://github.com.evil.invalid/"], ["github", "https://gіthub.com/x"], ["github", "https://xn--gthub-n4a.com/"], ["github", "https://github.io/x"],
    ["github", "https://github.com:443/x"], ["email", "https://example.org/"], ["website", 42], ["website", null],
  ];
  for (const [k, v] of bad) {
    assert.equal(P.link(k, v), "", k + " " + v);
    assert.equal(P.parse(doc("logo", png(16, 16), { [k]: v })), null, "the whole doc is refused for " + k + " " + v);
  }
});

test("links JSON: at most 1,024 bytes, an object of known keys with string values, or the doc is refused", () => {
  const img = png(16, 16);
  const bad = {
    "over 1,024 bytes": " ".repeat(1100) + '{"x":"https://x.com/a"}',
    array: '["https://x.com/a"]', null: "null", number: "1", string: '"https://x.com/a"', "unknown key": '{"email":"https://x.com/a"}',
    __proto__: '{"__proto__":{"x":"https://x.com/a"}}', constructor: '{"constructor":"https://x.com/a"}', nested: '{"x":{"href":"https://x.com/a"}}',
    "a number value": '{"x":1}', "deep nesting": "[".repeat(500) + "]".repeat(500), "not JSON": "{x: 'https://x.com/a'}", "trailing bytes": '{"x":"https://x.com/a"}x',
    "invalid UTF-8": Buffer.from([0x7b, 0x22, 0xff, 0x22, 0x3a, 0x22, 0x22, 0x7d]), "a byte-order mark": "﻿" + '{"x":"https://x.com/a"}',
    "one good, one bad": '{"x":"https://x.com/a","github":"https://evil.invalid/x"}',
  };
  for (const [name, l] of Object.entries(bad)) assert.equal(P.parse(doc("logo", img, l)), null, name);
});

test("kinds: a pass doc is an image and nothing else; a logo doc is an image, links, or both", () => {
  assert.equal(P.parse(doc("pass", null)), null);
  assert.equal(P.parse(doc("pass", png(16, 16), { x: "https://x.com/a" })), null);
  assert.equal(P.parse(doc("logo", null)), null);
  assert.equal(P.parse(doc("logo", null, {})), null);
  assert.equal(P.parse(doc("logo", png(16, 16), {})).links, null);
  assert.ok(P.parse(doc("logo", null, { x: "https://x.com/a" })));
  assert.equal(P.parse(doc("pass", png(300, 300))).kind, "pass");
});

test("references: dmz:<64 lowercase hex>, and nothing else is ever looked up", async () => {
  const s = "a1".repeat(32);
  assert.equal(P.ref(s), "dmz:" + s); assert.ok(P.isRef("dmz:" + s));
  for (const bad of ["dmz:" + s.slice(1), "dmz:" + s.toUpperCase(), "DMZ:" + s, " dmz:" + s, "dmz:" + s + "\n", "dmz://" + s, "https://x.invalid/dmz:" + s, s, null])
    assert.equal(P.isRef(bad), false, String(bad));
  assert.throws(() => P.ref("zz")); assert.throws(() => P.ref(s.toUpperCase()));
  let calls = 0; window.Veil.node = { getSC: async () => { calls++; return { code: "" }; } };
  for (const icon of ["https://evil.invalid/x.png", "http://127.0.0.1/x", "dmz:" + s.toUpperCase(), "", undefined])
    { assert.equal(await P.logo({ icon }), null); assert.equal(await P.links({ icon }), null); }
  assert.equal(calls, 0, "a header that is not a reference reached the node");
});

test("reads go through the node with code, at most three at a time, once per scid; invalid docs give null", async () => {
  const good = doc("logo", png(16, 16), { github: "https://github.com/x" }), ids = Array.from({ length: 10 }, (_, i) => String(i).repeat(64).slice(0, 64));
  const codes = Object.fromEntries(ids.map((id, i) => [id, i === 9 ? good.replace("// logo ", "// Logo ") : good]));
  let calls = 0, inflight = 0, most = 0;
  window.Veil.node = { getSC: async (scid, opt) => { assert.equal(opt.code, true); calls++; inflight++; most = Math.max(most, inflight); await new Promise((r) => setTimeout(r, 5)); inflight--; return { code: codes[scid] }; } };
  const got = await Promise.all(ids.map((id) => P.logo("dmz:" + id)));
  assert.equal(most, 3, "concurrency cap"); assert.equal(calls, 10);
  assert.ok(got.slice(0, 9).every((u) => u === "data:image/png;base64," + b64(png(16, 16)))); assert.equal(got[9], null);
  assert.deepEqual(await P.links({ icon: "dmz:" + ids[0] }), { github: "https://github.com/x" });
  await P.logo("dmz:" + ids[0]); await P.logo("dmz:" + ids[9]);
  assert.equal(calls, 10, "a read doc, valid or not, is not read again");
  const down = "f".repeat(64); window.Veil.node = { getSC: async () => { calls++; throw new Error("node down"); } };
  assert.equal(await P.logo("dmz:" + down), null); assert.equal(await P.logo("dmz:" + down), null);
  assert.equal(calls, 11, "a failed read is retried later, not at once, and not remembered as invalid");
});

// the shared verdicts for tools/profiledoc: every case here must give the same answer in Go
test("the Go vectors agree with this reader", () => {
  const v = JSON.parse(readFileSync(join(ROOT, "tests", "js", "fixtures", "profiledoc-vectors.json"), "utf8"));
  assert.ok(v.cases.length >= 20 && v.builds.length >= 2);
  for (const c of v.cases) { const d = P.parse(c.code); assert.equal(!!d, c.ok, c.name); if (d) assert.deepEqual([d.kind, d.w, d.h], [c.kind, c.w, c.h], c.name); }
  for (const b of v.builds) { const L = {}; for (const k of P.LINK_KEYS) if (b.links[k]) L[k] = b.links[k]; assert.equal(P.compose(b.kind, b.image, Object.keys(L).length ? b64(Buffer.from(JSON.stringify(L))) : ""), b.code, b.name); }
});

// ui.js's image-header check takes a profile doc reference alongside https, and nothing looser
test("safeUrl accepts dmz:<64 hex> and still refuses everything else", () => {
  const win = { Veil: { amm: {} }, addEventListener() {}, scrollTo() {} };
  const document = { createElement: () => ({ append() {}, setAttribute() {}, addEventListener() {}, classList: { add() {}, remove() {}, toggle() {} }, style: {} }), createElementNS: () => ({ append() {}, setAttribute() {} }), createTextNode: (s) => ({ nodeType: 3, s }), querySelector: () => null, querySelectorAll: () => [], body: { style: {} }, addEventListener() {} };
  new Function("window", "document", "location", readFileSync(join(ROOT, "app", "ui.js"), "utf8"))(win, document, { hash: "" });
  const U = win.Veil.ui, s = "0f".repeat(32);
  assert.equal(U.safeUrl("dmz:" + s), "dmz:" + s);
  for (const bad of ["dmz:" + s.toUpperCase(), "dmz:" + s.slice(2), "dmz:" + s + "\"", "dmz:javascript:alert(1)", "DMZ:" + s]) assert.equal(U.safeUrl(bad), "", bad);
  assert.equal(U.safeUrl("https://example.invalid/a.png"), "https://example.invalid/a.png");
});
