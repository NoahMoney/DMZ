// Real PNG and WebP bytes for the profile doc tests (tests/js/profile.test.mjs) and for the vectors the Go side
// checks (tests/js/fixtures/profiledoc-vectors.json). Built here from the file formats, with no image library.
import { deflateSync } from "node:zlib";

const CRC = new Uint32Array(256).map((_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0; });
const crc = (b) => { let c = 0xffffffff; for (const x of b) c = CRC[(c ^ x) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
export function chunk(type, data) {
  const b = Buffer.alloc(12 + data.length); b.writeUInt32BE(data.length, 0); b.write(type, 4, "latin1"); Buffer.from(data).copy(b, 8);
  b.writeUInt32BE(crc(b.subarray(4, 8 + data.length)), 8 + data.length); return b;
}
// a decodable 8-bit greyscale PNG; extra chunks go between IHDR and IDAT
export function png(w, h, extra = []) {
  const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), ...extra, chunk("IDAT", deflateSync(Buffer.alloc((w + 1) * Math.min(h, 4096)))), chunk("IEND", Buffer.alloc(0))]);
}
// a RIFF/WEBP container around [fourcc, data] chunks, padded to even lengths as the format requires
export function riff(chunks) {
  const body = Buffer.concat(chunks.map(([t, d]) => { const hd = Buffer.alloc(8); hd.write(t, 0, "latin1"); hd.writeUInt32LE(d.length, 4); return Buffer.concat([hd, d, Buffer.alloc(d.length & 1)]); }));
  const hd = Buffer.alloc(12); hd.write("RIFF", 0, "latin1"); hd.writeUInt32LE(body.length + 4, 4); hd.write("WEBP", 8, "latin1");
  return Buffer.concat([hd, body]);
}
export const vp8l = (w, h) => { const d = Buffer.alloc(16); d[0] = 0x2f; d.writeUInt32LE(((w - 1) | ((h - 1) << 14)) >>> 0, 1); return d; };
export const vp8 = (w, h) => { const d = Buffer.alloc(20); d[3] = 0x9d; d[4] = 0x01; d[5] = 0x2a; d.writeUInt16LE(w, 6); d.writeUInt16LE(h, 8); return d; };
export const vp8x = (w, h, flags = 0x10) => { const d = Buffer.alloc(10); d[0] = flags; d.writeUIntLE(w - 1, 4, 3); d.writeUIntLE(h - 1, 7, 3); return d; };
