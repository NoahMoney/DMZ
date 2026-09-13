package zecswap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"veil/tools/netcfg"
)

const feeLog = "../../research/regtest/zecswap-fee.log"

// TestRegtestFee runs the zecswap command line for one whole swap and one refund on the real regtest zebrad and the
// DERO simulator, as TestRegtestCLI does, and reads the Zcash side back from zebrad. The claim must carry a treasury
// output of exactly SwapFee of the funding, paid to the simulator block's zecTreasury in deploy/networks.json (the
// block regtest reads), and the refund must carry none. DERO wallets 30018 (maker), 30019 (taker), 30020 (install).
// Evidence in research/regtest/zecswap-fee.log. Skipped unless ZECSWAP_REGTEST=1.
func TestRegtestFee(t *testing.T) {
	if os.Getenv("ZECSWAP_REGTEST") == "" {
		t.Skip("set ZECSWAP_REGTEST=1 to run the swap fee against the real regtest zebrad and the DERO simulator")
	}
	e := newE2EAt(t, feeLog)
	defer e.close()
	record := filepath.Join(e.repo, "deploy", "networks.json")
	e.cn = &cliNet{network: "regtest", zcash: e2eZcash, mine: true, wmin: 20, wantZat: 200_000, interval: "2s", wait: 75 * time.Minute,
		dero: e2eDaemon, makerWallet: "127.0.0.1:30018", takerWallet: "127.0.0.1:30019", installWallet: "127.0.0.1:30020", networks: record}
	f, err := netcfg.Load(record)
	if err != nil {
		t.Fatal(err)
	}
	treasury := f.Networks["simulator"].ZecTreasury
	tspk, err := TreasuryScript(treasury, Testnet)
	if err != nil {
		t.Fatalf("the record's simulator zecTreasury: %v", err)
	}
	cookie, err := filepath.Abs(regtestCookie())
	if err != nil {
		t.Fatal(err)
	}
	e.cookie, e.bin = cookie, buildCLI(t)
	e.logf("== the 0.5%% swap fee on real nodes: the zecswap command line (binary <tmp>/zecswap), zebrad 6.3.0 regtest %s, DERO simulator %s; DERO wallets %s maker, %s taker, %s install",
		e2eZcash, e2eDaemon, e.cn.makerWallet, e.cn.takerWallet, e.cn.installWallet)
	e.logf("== the treasury: deploy/networks.json simulator.zecTreasury = %s, scriptPubKey %x. Its key was made for regtest outside the repository; no file here holds it", treasury, tspk)
	if note := os.Getenv("ZECSWAP_REGTEST_NOTE"); note != "" {
		e.logf("== note: %s", note)
	}
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ci.Regtest || CheckNetworks("regtest", ci.Chain, true) != nil {
		t.Fatalf("not a regtest node: %+v", ci)
	}
	e.logf("zcash: chain %q (block 0 is regtest's genesis), height %d, nextblock %08x", ci.Chain, ci.Height, ci.NextBranch)
	e.install()

	held := func() uint64 {
		us, err := e.zc.AddressUTXOs(e.ctx, treasury)
		if err != nil {
			t.Fatalf("getaddressutxos for the treasury: %v", err)
		}
		var sum uint64
		for _, u := range us {
			sum += u.Value
		}
		return sum
	}

	// F: a whole swap. The claim pays the fee.
	before := held()
	e.logf("F: zebrad getaddressutxos: the treasury holds %d zatoshi before the swap", before)
	e.cliSwap("F", false)
	mk := e.cliState(filepath.Join(e.dir, "F"), RoleMaker)
	if mk == nil || mk.Funding == nil || mk.ClaimTxID == "" || mk.Treasury != treasury {
		t.Fatalf("F: the maker's state has no funding, claim or treasury: %+v", mk)
	}
	claim := e.readBack("F", "claim", mk.ClaimTxID)
	want := SwapFee(mk.Funding.Value)
	if len(claim.Out) != 2 || claim.Out[1].Value != want || !bytes.Equal(claim.Out[1].ScriptPubKey, tspk) {
		t.Fatalf("F: the claim's outputs %+v; want output 1 of %d zatoshi to %x", claim.Out, want, tspk)
	}
	netFee := mk.Funding.Value - claim.Out[0].Value - claim.Out[1].Value
	e.logf("F: the claim %s pays the treasury exactly floor(%d × %d / %d) = %d zatoshi (output 1, to %s); the maker's output 0 is %d = %d − %d swap fee − %d network fee (ZIP 317's conventional fee for its %d logical actions: %d)",
		mk.ClaimTxID, mk.Funding.Value, SwapFeeBps, BpsDenominator, want, treasury, claim.Out[0].Value, mk.Funding.Value, want, netFee, claim.LogicalActions(), claim.ConventionalFee())
	e.gettxout("F", mk.ClaimTxID, 1)
	after := held()
	e.logf("F: zebrad getaddressutxos: the treasury holds %d zatoshi after the swap, %d more", after, after-before)
	if after-before != want {
		t.Fatalf("F: the treasury gained %d, want %d", after-before, want)
	}

	// N: a refund. No fee.
	e.cliSwap("N", true)
	tk := e.cliState(filepath.Join(e.dir, "N"), RoleTaker)
	if tk == nil || tk.SpendTxID == "" || tk.Phase != "refunded" {
		t.Fatalf("N: the taker did not refund: %+v", tk)
	}
	refund := e.readBack("N", "refund", tk.SpendTxID)
	var payout [20]byte
	if err := decodeHexInto(payout[:], tk.Payout); err != nil {
		t.Fatal(err)
	}
	if len(refund.Out) != 1 || !bytes.Equal(refund.Out[0].ScriptPubKey, P2PKHScript(payout)) {
		t.Fatalf("N: the refund's outputs %+v", refund.Out)
	}
	e.logf("N: the refund %s has one output, %d zatoshi to the taker's own key (%s); %d zatoshi funded − %d = a network fee of %d; no output pays the treasury",
		tk.SpendTxID, refund.Out[0].Value, EncodeAddress(Testnet, P2PKH, payout), tk.WantAmount, refund.Out[0].Value, tk.WantAmount-refund.Out[0].Value)
	if end := held(); end != after {
		t.Fatalf("N: the treasury went from %d to %d over a refund", after, end)
	} else {
		e.logf("N: zebrad getaddressutxos: the treasury still holds %d zatoshi after the refund: nothing was taken", end)
	}
	e.logf("== done: the claim paid the fee to the treasury, and the refund paid none")
}

// readBack reads a transaction back from zebrad twice: its raw bytes, parsed and checked against their txid by this
// package (RawTx), and zebrad's own decoding of it (getrawtransaction verbose), each output logged as zebrad gives it.
func (e *e2e) readBack(tag, what, txid string) *Tx {
	rt, err := e.zc.RawTx(e.ctx, txid)
	if err != nil {
		e.t.Fatalf("%s: the %s %s: %v", tag, what, txid, err)
	}
	tx, err := ParseTx(rt.Raw)
	if err != nil {
		e.t.Fatalf("%s: the %s %s does not parse: %v", tag, what, txid, err)
	}
	var v struct {
		Height *int64 `json:"height"`
		Vout   []struct {
			Value        float64      `json:"value"`
			ValueZat     *json.Number `json:"valueZat"`
			N            int          `json:"n"`
			ScriptPubKey struct {
				Hex       string   `json:"hex"`
				Addresses []string `json:"addresses"`
			} `json:"scriptPubKey"`
		} `json:"vout"`
	}
	if err := json.Unmarshal(e.zrpc("getrawtransaction", txid, 1), &v); err != nil {
		e.t.Fatalf("%s: zebrad's decoding of the %s: %v", tag, what, err)
	}
	e.logf("%s: zebrad getrawtransaction %s 1: the %s, v%d, mined at height %d, %d outputs", tag, txid, what, versionOf(e, txid), rt.Height, len(v.Vout))
	if len(v.Vout) != len(tx.Out) {
		e.t.Fatalf("%s: zebrad decodes %d outputs, the bytes carry %d", tag, len(v.Vout), len(tx.Out))
	}
	for _, o := range v.Vout {
		zat := uint64(math.Round(o.Value * 1e8))
		src := "value × 10^8"
		if o.ValueZat != nil {
			n, err := o.ValueZat.Int64()
			if err != nil {
				e.t.Fatal(err)
			}
			zat, src = uint64(n), "valueZat"
		}
		e.logf("%s:   vout %d: %d zatoshi (%s), to %v, scriptPubKey %s", tag, o.N, zat, src, o.ScriptPubKey.Addresses, o.ScriptPubKey.Hex)
		if o.N < 0 || o.N >= len(tx.Out) || zat != tx.Out[o.N].Value || o.ScriptPubKey.Hex != fmt.Sprintf("%x", tx.Out[o.N].ScriptPubKey) {
			e.t.Fatalf("%s: zebrad's output %d differs from the bytes", tag, o.N)
		}
	}
	return tx
}

// gettxout logs zebrad's view of one output of txid in its UTXO set.
func (e *e2e) gettxout(tag, txid string, n int) {
	var o struct {
		Confirmations int64   `json:"confirmations"`
		Value         float64 `json:"value"`
	}
	raw := e.zrpc("gettxout", txid, n)
	if string(raw) == "null" {
		e.t.Fatalf("%s: zebrad has no unspent output %s:%d", tag, txid, n)
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		e.t.Fatal(err)
	}
	e.logf("%s: zebrad gettxout %s %d: unspent, %d zatoshi, %d confirmations", tag, txid, n, uint64(math.Round(o.Value*1e8)), o.Confirmations)
}
