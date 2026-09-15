# Profile docs: logos, the pass image and project links, on chain

A token's logo, an NFA's icon, the Founders Pass image and a project's links are stored on chain, in a contract
the creator installs: a **profile doc**. A launch or an NFA names one in its icon header as `dmz:<scid>`. The app
reads the doc only through the user's own node, and checks every byte of it before it shows anything. Nothing is
fetched from a creator's host or from any other third party. The CSP stays `img-src 'self' data:`.

- `app/profile.js`: the reader, the lazy loader and cache, the in-browser builder, and the editor at `#profiledoc`
  (a logo and links) and `#profiledoc/pass` (the pass image).
- `app/ui.js`: `U.avatar` shows the logo when a doc is named and readable, and the text plate otherwise.
  `U.safeUrl` accepts `dmz:<64 hex>` alongside https.
- `tools/profiledoc`: the same format in Go, standard library only. It builds a doc from an image that is already
  encoded, prints its size, sha256 and storage gas, can install it through a wallet's RPC, and reads one back.
- `tools/simdeploy -pass-image dmz:<scid>`: the Founders Pass takes its image from a pass-image doc.

## Trust: what the creator chooses, and what the app does

- **The content is the creator's.** Nobody checks that a logo is the project's or that a link goes where it
  claims. The app shows what the creator stored, and nothing on screen says it is true.
- **A doc is immutable.** Its contract has no entrypoint and no storage, and the wrapper allows no other code, so
  no function can change or delete it. A launch's icon header is set at install and never changes, so once a
  launch names a doc, that market shows those bytes for good. An NFA whose creator kept `Update` can later point
  its icon at a different doc. Neither can change the doc itself.
- **No third-party request.** The app reads the doc with `DERO.GetSC` (with code) from the node the user
  connected, the same way it reads every contract. The image is rendered from a `data:` URI.
- **No active content.** An image is shown only when its magic bytes and structure are a still PNG or WebP.
  SVG, animation, trailing data and anything else are refused. A link is only an anchor the user can click
  (`target=_blank`, `rel="noopener noreferrer nofollow"`, no referrer), and the app never requests it.
- **Fail closed.** One failed check refuses the whole doc: the image, the links, all of it. The avatar keeps its
  text plate, and no partial doc is ever shown.

## The format (v1)

The contract code is exactly the following, in LF text:

```
Function InitializePrivate() Uint64
10 RETURN 0
End Function
// DMZ profile doc v1. Data only: no entrypoint, no storage, no update path.
// <kind> <image, base64>
// links <links JSON, base64>
```

The first four lines are the wrapper: 138 bytes, compared byte for byte. The DVM parser skips `//` comments, and
the chain keeps the code as installed, so the payload costs storage and nothing else. `InitializePrivate` runs
once at install and stores nothing, and no function can be called afterwards.

- `kind` is `logo` or `pass`. Both payloads are canonical standard base64: no whitespace, no base64url, no
  non-zero padding bits. Either may be empty where its kind allows it.
- **Image.** Either a PNG or a WebP.
  - PNG: the signature, then `IHDR` first. Chunks must tile the file exactly, and `IEND` comes last with nothing
    after it. At least one `IDAT`. No second `IHDR`, `acTL`, `fcTL` or `fdAT`.
  - WebP: `RIFF` whose size is the file's, then `WEBP`. The first chunk is `VP8 ` (with its start code), `VP8L`
    (with its signature) or `VP8X` (with the animation flag clear). There is exactly one bitstream, chunks tile the
    file exactly, and there is no `ANIM` or `ANMF`.
  - The image type in the `data:` URI comes from the magic bytes, never from a claim in the doc.
- **Links.** A UTF-8 JSON object (a byte-order mark is refused) whose keys are among `website`, `x`, `telegram`,
  `discord` and `github`, each a string of at most 200 characters.
  - Each link is `https://` on a plain ASCII host. It has no user name, no port, no `xn--` label, no IP address,
    none of `"'<>\` and backtick, no `%` without two hex digits, and printable ASCII only.
  - `website` may be on any host.
  - `x` must be on x.com or twitter.com (or their www), `telegram` on t.me, `discord` on discord.gg or
    discord.com (or www.discord.com), and `github` on github.com (or www.github.com). The host must match exactly.
  - An unknown key, a non-string value or one bad link refuses the whole doc.

| | image bytes | dimensions | links | note |
|---|---|---|---|---|
| `logo` | ≤ 8,192 | ≤ 256×256 | ≤ 1,024 bytes of JSON | the image, the links, or both |
| `pass` | ≤ 14,000 | ≤ 384×384 | none | an image is required |
| any doc | code ≤ 19,500 bytes | | | |

A reference is `dmz:` followed by the doc's scid in 64 lowercase hex. Nothing else in an icon header is ever
looked up.

## Cost, measured on the simulator (2026-09-15)

Install storage gas is **the code's length + 25**. `DERO.GetGasEstimate` gave exactly that for codes from 157 to
19,657 bytes, with compute gas 5,000. The fee the wallet pays is storage gas + 200 atomic units (1 DERO =
100,000). DERO refuses a transaction above 20,000 storage gas, so a doc can be at most 19,975 bytes. The largest
image that could still install is 14,862 bytes. The pass cap of 14,000 leaves 1,125 gas of margin.

| doc | image | code | storage gas | fee, from the wallet's balance |
|---|---|---|---|---|
| test logo, 5 links | WebP 160×160, 3,136 B, quality 0.95 | 4,577 B | 4,602 | **0.04802 DERO** |
| Founders Pass image | WebP 384×384, 13,976 B, quality 0.54 | 18,793 B | 18,818 | **0.19018 DERO** |
| largest logo doc the format admits | 8,192 B + 1,024 B of links | 12,449 B | 12,474 | 0.12674 DERO |
| largest pass doc the format admits | 14,000 B | 18,825 B | 18,850 | 0.19050 DERO |

## Building a doc

**In the app** (`#profiledoc`, `#profiledoc/pass`), nothing is uploaded anywhere:

1. The creator's file is checked by its magic bytes (PNG, JPEG, WebP or GIF; SVG is never taken) and decoded.
2. It is cropped to its centre square and drawn to a canvas: 160, 144 or 128 px for a logo, 384 down to 256 px for
   a pass image.
3. The canvas is encoded as WebP, or as PNG where the browser cannot encode WebP. The highest quality that fits
   the cap is found by binary search, with a floor of 0.5.
4. The result must pass the app's own reader before it is offered.
5. The screen shows a preview at the three avatar sizes, the links as they will render, and the byte counts. It
   also shows the node's gas estimate (or the size formula when the node gives none), the fee and the code's
   sha256.
6. **Install** sends the code through the wallet over XSWD, the way every contract in the app is installed. It
   uses `app.tx`, with its network gate and its 1 DERO fee ceiling. The doc is then read back.

**From the command line**, from `tools/`:

```
go run ./profiledoc -image logo.webp -website https://example.org -x https://x.com/handle   # prints size, sha256, gas
go run ./profiledoc -image logo.webp -install -network simulator -wallet 127.0.0.1:30008  # installs and reads back
go run ./profiledoc -read <scid>                                                           # checks a doc as the app does
```

The wallet's RPC login comes from `VEIL_RPC_LOGIN`, as for simdeploy. On `-network mainnet` an install is
refused without `-yes`, without that login, or with a wallet that is not on this machine. The network named must
match what the daemon reports. For the same image and links, `profiledoc` composes the same bytes as the browser:
the test logo and the pass image were built in both and compared with `cmp`.

## The Founders Pass image

The canonical pass image is **`docs/brand/founders-pass.webp`**. It was encoded in the browser pipeline above,
at `#profiledoc/pass`, from the operator's `founders.png` (1254×1254, 2,683,803 bytes).

- WebP, 384×384, **13,976 bytes**, quality 0.54.
- sha256 **`a253c043f17fb9f7f29b37e474d17b1f45ff54511a1e86445d4be634bf3194ce`**.
- Its doc's code is 18,793 bytes, sha256 `bfecb90cb6a0642e2cca46cfc00e0c4ca380f1b923e6a4ab79771c22b1d25a88`,
  with 18,818 storage gas.

"FOUNDERS PASS" and "5% FEE SHARE" were read on the rendered preview at 192 px. The pass screen shows the image
at up to 176 px.

The runbook step, before the pass contract is installed:

1. From `tools/`, install the image doc:
   `go run ./profiledoc -image ../docs/brand/founders-pass.webp -kind pass -install -network <net> -wallet 127.0.0.1:<port>`
   On mainnet, add `-yes` and set `VEIL_RPC_LOGIN`. It prints `reference dmz:<scid>` and reads the doc back.
2. Pass that reference to simdeploy: `-pass-image dmz:<scid>`. founders.bas stores it as both `iconURLHdr` and
   `fileURL` and never changes it. simdeploy refuses a `-pass-image` that is neither `dmz:<64 hex>` nor https.

The doc must be installed on the same network as the pass: a mainnet pass needs a mainnet doc.

## Reading, at scale

- **Lazy.** An avatar that names a doc renders its text plate at once and registers with one shared
  `IntersectionObserver` (240 px margin). The doc is read only when the plate nears the viewport. Plates that
  left the page are released every 5 s, because lists redraw every block.
- **Bounded.** At most 3 code reads are in flight, and one read per scid however many cards name it. A node
  failure is retried after 30 s, not at once. An invalid doc is not read again that session.
- **Cached as immutable, by scid.**
  - In memory: up to 400 docs, the least recently used dropped first.
  - In localStorage (`dmz.pd.<scid>`, index `dmz.pd.lru`): the code of valid docs, up to 600,000 characters in
    all, least recently used out first. Orphaned keys are swept on first use.
  - Stored code is parsed again on every load, never trusted as stored.
- **Rendered** as `<img src="data:image/webp|png;base64,…" width height alt decoding="async">` inside the avatar
  box. If the browser cannot decode the image, the text plate comes back.

## The interface (`V.profile`)

| call | returns |
|---|---|
| `build({image: File\|null, links: {website, x, telegram, discord, github}, kind?: "logo"\|"pass"})` | `Promise<{code, bytes, gasEstimate, gasSource, kind, preview, width, height, type, imageBytes, quality, links, sha256}>` |
| `install(build)` | `Promise<scid>`: a wallet install over XSWD, then read back |
| `ref(scid)` | `"dmz:<scid>"`, for a launch's or NFA's icon header |
| `logo(asset)` | `Promise<dataURI\|null>`; `asset` is `{icon}` or a `dmz:` string |
| `links(asset)` | `Promise<{website?, x?, telegram?, discord?, github?}\|null>`: validated, normalised hrefs |
| `anchors(links)` | an element of external anchors (label and domain), or null |
| `linksEl(asset)` | an element that fills with `anchors` once the doc is read |
| `isRef(s)`, `mount(el, asset)`, `parse(code)`, `link(kind, url)` | the pieces `U.avatar` and the tests use |

`U.avatar(asset)` calls `mount`, so every card, row, market page and pass screen that already draws an avatar
shows the logo with no change of its own.

`U.safeUrl` now passes `dmz:<scid>`, so the launch form's image check takes a reference. A screen that puts
`safeUrl`'s result in an `href` (an NFA's file link, for one) would render a `dmz:` link that goes nowhere. It
should show such a value through `V.profile` instead.

## Tests

- `tests/js/profile.test.mjs` feeds the reader hostile docs, against real PNG and WebP bytes built in
  `tests/js/profileimg.mjs`:
  - oversize images and code;
  - SVG, as the image, after an `IEND`, or as a link;
  - wrong magic bytes, broken structure, and animation;
  - a changed wrapper, CRLF, an added entrypoint;
  - non-canonical base64;
  - huge, nested, non-object or BOM-prefixed JSON;
  - `javascript:`, `data:` and `http:` links, look-alike, userinfo, IDN, port and IP hosts.

  It also covers the concurrency cap and the cache, and checks that a header which is not a reference never
  reaches the node.
- `tests/js/fixtures/profiledoc-vectors.json` holds the verdicts and composed bytes written from `app/profile.js`.
  `tools/profiledoc`'s tests must give the same verdicts and build the same bytes.
- `tools/simdeploy/passimage_test.go` covers the `-pass-image` check.
- `tools/uitest/profile-docs.mjs` runs under the app's CSP with no bypass, and fails on any request outside the
  app's origin and the loopback node.
  - `--encode` builds a logo and the pass image in the browser.
  - `--launch=<scid> [--founders=<scid>]` checks the logo on a market card and on the market page, and the pass
    image on the pass screen.

## On the simulator (2026-09-15)

| what | scid |
|---|---|
| logo doc (test logo, five example.* links) | `c81f1370eee72dfeaa1c4ff32a033a26644842e871397b37fd894683adb2849d` |
| pass-image doc (docs/brand/founders-pass.webp) | `c5029e9aff4806011247456463501267196bcf0de3e32a5946367889a322f9f3` |
| test market PDOC, icon `dmz:c81f…849d`, registered on the factory | `99e9c16276c3e99c1e34929c00a79306b92c503b436d7b541e19de298122709a` |
| demo founders.bas, icon and file `dmz:c502…f9f3` (not the build's pass) | `ef99fd1bb8ea183e0359ad2aa503d83338ce88845aa2f7f50f63f8bb0944d7c1` |
