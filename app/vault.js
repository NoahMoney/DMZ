// DMZ vault.js — the wrapped-asset vault's data layer, split out of wrap.js (DOC size, MT-E10). Adds vault,
// loadVault, vaultIndexes, myVault and vaultWithdrawals to V.data for the Wrap screen (wrap.js), which loads after
// it. Every read is from the vault contract by exact key, in the encoding the contract stores; nothing is assumed.
//
// contracts/vault.bas keys the per-address records by the RAW 33-byte key, never by the dero1 text: Register stores
// uc_<SIGNER()> and ul_<SIGNER()>_<c>, and Burn stores w_<j>_by = ADDRESS_RAW(refund). A raw key cannot be named
// through DERO.GetSC keysstring (JSON text), so uc_ and ul_ are read through keysbytes (V.node.getSC raw), and a raw
// value comes back as 66 hex characters and is compared as those bytes (tests/js/vaultkeys.test.mjs).
(function () {
  const V = window.Veil, U = V.ui, D = V.data, B = BigInt;
  const Z = 0n;

  // ---- data ----

  const NUM = ["k", "n", "na", "sup", "cap", "capratio", "bondtot", "minbond", "minburn", "wfee", "mindep",
    "regfee", "wdelay", "winmax", "bigbps", "bigdelay", "halted", "sealed", "un", "wn", "feepool", "v"];
  const isNum = (x) => typeof x === "string" && /^\d+$/.test(x);
  const num = (vals, k) => (isNum(vals[k]) ? B(vals[k]) : Z);
  const hx = (s) => V.bytesToHex(new TextEncoder().encode(s));
  // the raw key of an address, or an error: an address the app cannot encode is never read as "nothing registered"
  const rawOf = (addr) => { const r = V.addressRaw(addr); if (!r) throw new Error("not a DERO address: " + U.clean(String(addr || ""), 20)); return r; };

  Object.assign(D, {
    vault: null,
    // The vault is not a factory listing: its id comes from the deployment record baked into config.js, and the
    // contract is asked to confirm what it is (kind, v). A vault whose kind or version does not match is not
    // shown at all, because every key name below would mean something else.
    async loadVault(pick) {
      const list = this.vaults();
      const scid = pick || (list[0] && list[0].scid) || this.cfg.vault;
      // the record is the deployment's own for THIS id, superseded vaults included: borrowing another vault's
      // record painted a retired 5-decimal vault, or any stranger's v3 contract, as the live XMR one (MISS-01)
      const rec = this.allVaults().find((v) => v.scid === scid);
      if (!rec) return null;
      const r = await V.node.getSC(scid, { keys: [...NUM, "kind", "viewkey"] });
      const kind = V.hexToStr(r.vals.kind || "");
      if (kind !== "vault") { console.warn("configured vault is not a vault contract"); return null; }
      const w = { scid, asset: rec, viewkey: U.clean(V.hexToStr(r.vals.viewkey || ""), 200) };
      for (const k of NUM) w[k] = num(r.vals, k);
      if (w.v !== 3n) { console.warn("vault version " + w.v + " is not the version this app reads"); return null; }
      w.headroom = w.cap > w.sup ? w.cap - w.sup : Z;
      // the size above which a burn earns the extra delay: bigbps of the current supply, contract arithmetic
      w.bigAt = w.bigbps === Z ? Z : (w.sup / 10000n) * w.bigbps + ((w.sup % 10000n) * w.bigbps) / 10000n;
      this.vault = w;
      return w;
    },

    // How many deposit indexes addr has registered in vault scid: uc_<raw key> (vault.bas Register, line 278). The
    // Wrap screen's Register reads it before and after, because un counts everyone's. Throws when it cannot be read.
    async vaultIndexes(scid, addr) {
      const k = hx("uc_") + rawOf(addr), r = await V.node.getSC(scid, { raw: [k] });
      return isNum(r.raws[k]) ? Number(r.raws[k]) : 0;
    },

    // The connected wallet's own deposit index and withdrawals. Read by exact key: the app never enumerates
    // other users, and an index that is registered but not yet attested has no foreign address to show.
    async myVault(addr) {
      const w = this.vault;
      if (!w || !addr) return null;
      const n = await this.vaultIndexes(w.scid, addr);
      let dep = null;
      if (n > 0) {
        // ul_<raw key>_<c> holds index j of this address's c-th registration (vault.bas Register, line 277)
        const idk = hx("ul_") + rawOf(addr) + hx("_" + (n - 1));
        const got = (await V.node.getSC(w.scid, { raw: [idk] })).raws;
        if (isNum(got[idk])) {
          const j = got[idk];
          const d = await this.fetchKeys(w.scid, ["da_" + j, "dh_" + j, "dver_" + j]);
          dep = {
            j,
            addr: U.clean(V.hexToStr(d["da_" + j] || ""), 200),
            since: isNum(d["dh_" + j]) ? B(d["dh_" + j]) : Z,
            ver: isNum(d["dver_" + j]) ? B(d["dver_" + j]) : Z,
          };
        }
      }
      // what this wallet actually holds of the wrapped asset, so the walkthrough can mark the last step done
      // rather than leaving the user to check another screen
      let units = 0n;
      try { const bal = await V.wallet.getBalance(w.scid, true); units = V.toBig(bal && bal.balance) || 0n; } catch (e) {}
      return Object.assign({ units }, { indexes: n, dep, withdrawals: await this.vaultWithdrawals(addr) });
    },

    // The last SCAN withdrawals, filtered to this address. w_<j>_by is ADDRESS_RAW(refund): the node returns those
    // 33 bytes as 66 hex characters, which are compared with this address's own raw key, never as text.
    async vaultWithdrawals(addr) {
      const w = this.vault;
      if (!w || !addr) return [];
      const raw = rawOf(addr), SCAN = 60;
      const wn = Number(w.wn), from = Math.max(0, wn - SCAN);
      if (wn === 0) return [];
      const keys = [];
      for (let j = from; j < wn; j++) keys.push("w_" + j + "_by");
      const by = await this.fetchKeys(w.scid, keys);
      const mine = [];
      for (let j = from; j < wn; j++) if (by["w_" + j + "_by"] === raw) mine.push(j);
      if (!mine.length) return [];
      const want = [];
      for (const j of mine) want.push("w_" + j + "_v", "w_" + j + "_f", "w_" + j + "_to", "w_" + j + "_h", "w_" + j + "_rel", "w_" + j + "_st", "w_" + j + "_ftx", "w_" + j + "_p");
      const got = await this.fetchKeys(w.scid, want);
      const g = (j, s) => got["w_" + j + "_" + s];
      return mine.reverse().map((j) => ({
        j,
        units: isNum(g(j, "v")) ? B(g(j, "v")) : Z,
        fee: isNum(g(j, "f")) ? B(g(j, "f")) : Z,
        dest: U.clean(V.hexToStr(g(j, "to") || ""), 200),
        burned: isNum(g(j, "h")) ? Number(g(j, "h")) : 0,
        rel: isNum(g(j, "rel")) ? Number(g(j, "rel")) : 0,
        st: isNum(g(j, "st")) ? Number(g(j, "st")) : 0,
        ftx: U.clean(V.hexToStr(g(j, "ftx") || ""), 128),
        // a signer has marked this withdrawal as being paid: only k votes can settle it after that, because the
        // coins may already have left. Unmarked, the burner may Reclaim it once the window has passed.
        marked: isNum(g(j, "p")) ? Number(g(j, "p")) : 0,
      }));
    },
  });
})();
