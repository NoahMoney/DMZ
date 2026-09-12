// DMZ vault.js — the wrapped-asset vault's data layer, split out of wrap.js (DOC size, MT-E10). Adds vault,
// loadVault, myVault and vaultWithdrawals to V.data for the Wrap screen (wrap.js), which loads after it. Every
// read is from the vault contract by exact key; nothing is assumed.
(function () {
  const V = window.Veil, U = V.ui, D = V.data, B = BigInt;
  const Z = 0n;

  // ---- data ----

  const NUM = ["k", "n", "na", "sup", "cap", "capratio", "bondtot", "minbond", "minburn", "wfee", "mindep",
    "regfee", "wdelay", "winmax", "bigbps", "bigdelay", "halted", "sealed", "un", "wn", "feepool", "v"];
  const isNum = (x) => typeof x === "string" && /^\d+$/.test(x);
  const num = (vals, k) => (isNum(vals[k]) ? B(vals[k]) : Z);

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
      const r = await V.node.getSC(scid, { keys: [...NUM, "kind", "viewkey", "treasury"] });
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

    // The connected wallet's own deposit index and withdrawals. Read by exact key: the app never enumerates
    // other users, and an index that is registered but not yet attested has no foreign address to show.
    async myVault(addr) {
      const w = this.vault;
      if (!w || !addr) return null;
      const c = await this.fetchKeys(w.scid, ["uc_" + addr]);
      const n = isNum(c["uc_" + addr]) ? Number(c["uc_" + addr]) : 0;
      let dep = null;
      if (n > 0) {
        const idk = "ul_" + addr + "_" + (n - 1);
        const got = await this.fetchKeys(w.scid, [idk]);
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

    // The last SCAN withdrawals, filtered to this address. w_<j>_by is the burner as ADDRESS_STRING, which is
    // the same canonical dero1 form the app uses everywhere.
    async vaultWithdrawals(addr) {
      const w = this.vault;
      if (!w || !addr) return [];
      const SCAN = 60;
      const wn = Number(w.wn), from = Math.max(0, wn - SCAN);
      if (wn === 0) return [];
      const keys = [];
      for (let j = from; j < wn; j++) keys.push("w_" + j + "_by");
      const by = await this.fetchKeys(w.scid, keys);
      const mine = [];
      for (let j = from; j < wn; j++) if (V.hexToStr(by["w_" + j + "_by"] || "") === addr) mine.push(j);
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
