# tools/uitest: the headless browser passes

`ui.mjs` drives the app in headless Chromium through Playwright against a running simulator: `run.sh` (the read-only
and wallet passes), `ui.mjs --mainnet` (the mainnet-shaped pass) and `tools/check.sh --ui`. `capture.mjs` and
`xswdprobe.mjs` are helpers.

## The Playwright pin (MT-H03)

Playwright is not vendored. `PW_DIR` names a directory where it is installed, and the passes load it from there.
The version is pinned in `ui.mjs`:

| | Pinned | Recorded from |
|---|---|---|
| Playwright (`playwright`, `playwright-core`) | **1.62.0** | the install every recorded pass of 2026-09-11 used: `node_modules/playwright/package.json`; its `package-lock.json` gives integrity `sha512-Z14dG305…Sylw==` (playwright) and `sha512-nsNRyq0r…XljA==` (playwright-core) from registry.npmjs.org |
| Chromium | **151.0.7922.34**, Playwright browser revision 1234 (`chromium-1234`, `chromium_headless_shell-1234`) | `playwright-core/browsers.json` of 1.62.0 |
| Node | 22.10.0 used; not enforced | `node --version` |

At start `ui.mjs` prints the Playwright version and where it came from, then the Chromium build it launched:

```
• playwright 1.62.0 (pinned 1.62.0) from <playwright>
• browser: chromium 151.0.7922.34 (pinned 151.0.7922.34)
```

It exits 2 before opening a page when either differs from the pin. `--any-playwright` (or `PW_ANY=1`) runs it
anyway, and the two lines above then record what was used. A result from an unpinned run is not evidence for the
pinned one.

Setting up a pinned `PW_DIR`:

```
mkdir <playwright> && cd <playwright>
npm init -y && npm i --save-exact playwright@1.62.0
npx playwright install chromium          # fetches revision 1234; nothing else is needed
PW_DIR=<playwright> node <repo>/tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-1
```

Keep that directory's `package-lock.json`: it holds the registry integrity hashes of the exact packages.

To move the pin, change `PLAYWRIGHT_PIN` and `CHROMIUM_PIN` in `ui.mjs` together. Take the new Chromium build from
`playwright-core/browsers.json`, and update the table above in the same commit.

`capture.mjs` loads Playwright the same way but does not check the pin. It is a screenshot helper, not a pass.
