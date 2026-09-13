package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"veil/tools/netcfg"
	rpc "veil/tools/simrpc"
	"veil/tools/zecswap"
)

// TestLiveRegtestRound drives one XMR or ZEC payout period against the real regtest nodes of tools/regtest and the
// DERO simulator. It is skipped unless TREASURYBOT_LIVE names a step; research/regtest/treasurybot-payouts.log is its
// evidence. Run each asset step under the harness's lock (tools/withlock.sh xmrreg / zecreg).
//
//	TREASURYBOT_LIVE=setup    Mint and Register one pass from LIVE_DERO_WALLET (default the simulator's 30016)
//	TREASURYBOT_LIVE=xmr      treasury wallet on LIVE_XMR_TREASURY, payer (and the holder's subaddress) on LIVE_XMR_PAYER
//	TREASURYBOT_LIVE=zec      zebrad at LIVE_ZEC_NODE with cookie LIVE_ZEC_COOKIE; keys are made in LIVE_DIR
//	TREASURYBOT_LIVE=cleanup  clear both payout addresses and Withdraw the pass
//
// LIVE_DIR (outside the repository) keeps the journals, the Zcash keys and the position between steps.
func TestLiveRegtestRound(t *testing.T) {
	step := os.Getenv("TREASURYBOT_LIVE")
	if step == "" {
		t.Skip("live regtest round: set TREASURYBOT_LIVE=setup|xmr|zec|cleanup with tools/regtest up")
	}
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	dir := env("LIVE_DIR", "")
	if dir == "" {
		t.Fatal("LIVE_DIR must name a directory outside the repository")
	}
	daemon, holderWallet := env("LIVE_DERO_DAEMON", "127.0.0.1:20000"), env("LIVE_DERO_WALLET", "127.0.0.1:30016")
	nets, err := netcfg.Load(filepath.Join("..", "..", "deploy", "networks.json"))
	if err != nil {
		t.Fatal(err)
	}
	founders := nets.Networks["simulator"].Founders
	c := &rpc.Client{Daemon: daemon, Timeout: 240 * time.Second}
	me := rpc.Address(holderWallet)
	holder := rpc.DVMAddress(me)
	ctx := context.Background()
	say := func(format string, a ...interface{}) { fmt.Printf("live: "+format+"\n", a...) }
	call := func(entry string, xfers []rpc.Transfer, args ...rpc.Arg) int64 {
		txid, _, h := c.Send(holderWallet, me, "", founders, xfers, append([]rpc.Arg{rpc.S("entrypoint", entry)}, args...))
		say("DERO %s: tx %s, block %d", entry, txid, h)
		return h
	}
	posFile := filepath.Join(dir, "position")
	switch step {
	case "setup":
		st := c.GetSC(founders, false)
		if st.Has("hu_" + holder) {
			say("DERO: %s… already holds %d registered pass(es)", holder[:12], st.U("hu_"+holder))
			return
		}
		price, pn := st.U("price"), st.U("pn")
		say("DERO: founders %s… v%d share %d bps hst %d; minting one pass for %d atomic units", founders[:12], st.U("v"), st.U("share"), st.U("hst"), price)
		call("Mint", []rpc.Transfer{{Destination: c.RandomAddress(), Burn: price}}, rpc.U("n", 1))
		call("Register", []rpc.Transfer{{SCID: founders, Burn: 1}})
		os.WriteFile(posFile, []byte(strconv.FormatUint(pn, 10)), 0o600)
		say("DERO: position %d registered; hst now %d", pn, c.GetSC(founders, false).U("hst"))
		return
	case "cleanup":
		b, err := os.ReadFile(posFile)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := strconv.ParseUint(string(b), 10, 64)
		st := c.GetSC(founders, false)
		for _, e := range []string{"SetXmr", "SetZec"} {
			if st.Has(map[string]string{"SetXmr": "xm_", "SetZec": "zc_"}[e] + holder) {
				call(e, nil, rpc.S("addr", ""))
			}
		}
		call("Withdraw", nil, rpc.U("p", p), rpc.U("u", 1))
		say("DERO: position %d withdrawn; hst now %d", p, c.GetSC(founders, false).U("hst"))
		return
	}
	share := c.GetSC(founders, false).U("share")
	*periodBlocks = 5
	// waitPeriod waits until a period boundary at or above h is stable, so the snapshot sees what block h holds
	waitPeriod := func(h int64) {
		want := (h + 4) / 5 * 5
		for i := 0; c.StableHeight() < want; i++ {
			if i > 200 {
				t.Fatalf("the simulator's stable height did not reach %d", want)
			}
			time.Sleep(5 * time.Second)
		}
		say("DERO: stable height %d, past the period boundary %d", c.StableHeight(), want)
	}
	root, _ := filepath.Abs(filepath.Join("..", ".."))
	run := func(ps []*assetPayer) *assetPayer {
		if len(ps) != 1 {
			t.Fatal("the asset did not start")
		}
		return ps[0]
	}
	switch step {
	case "xmr":
		tw, pw := newMoneroRPC(env("LIVE_XMR_TREASURY", "127.0.0.1:28083"), ""), newMoneroRPC(env("LIVE_XMR_PAYER", "127.0.0.1:28084"), "")
		md := env("LIVE_XMR_DAEMON", "127.0.0.1:28081")
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		open := func(w *moneroRPC, name string) {
			if w.call(ctx, "open_wallet", map[string]interface{}{"filename": name, "password": ""}, nil) != nil {
				must(w.call(ctx, "create_wallet", map[string]interface{}{"filename": name, "password": "", "language": "English"}, nil))
			}
		}
		sub := func(w *moneroRPC) (primary, first string) {
			var r struct {
				Addresses []struct {
					Address string `json:"address"`
				} `json:"addresses"`
			}
			must(w.call(ctx, "get_address", map[string]interface{}{"account_index": 0}, &r))
			if len(r.Addresses) < 2 {
				var a struct {
					Address string `json:"address"`
				}
				must(w.call(ctx, "create_address", map[string]interface{}{"account_index": 0, "label": "treasurybot live"}, &a))
				return r.Addresses[0].Address, a.Address
			}
			return r.Addresses[0].Address, r.Addresses[1].Address
		}
		mine := func(n int, to string) {
			body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "0", "method": "generateblocks", "params": map[string]interface{}{"amount_of_blocks": n, "wallet_address": to}})
			resp, err := http.Post("http://"+md+"/json_rpc", "application/json", bytes.NewReader(body))
			must(err)
			resp.Body.Close()
		}
		open(tw, "treasurybot-live-treasury")
		open(pw, "treasurybot-live-payer")
		_, treasury := sub(tw)
		payer, holderAddr := sub(pw)
		say("XMR: treasury fee subaddress %s…, holder's subaddress %s…", treasury[:12], holderAddr[:12])
		mine(80, payer)
		must(pw.call(ctx, "refresh", map[string]interface{}{}, nil))
		h := call("SetXmr", nil, rpc.S("addr", holderAddr))
		var fee struct {
			TxHash string `json:"tx_hash"`
		}
		must(pw.call(ctx, "transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": uint64(2_000_000_000_000), "address": treasury}}}, &fee))
		say("XMR: a 2 XMR swap fee paid to the treasury subaddress, tx %s", fee.TxHash)
		mine(11, payer)
		waitPeriod(h)
		x, _, notes := planPayouts(payoutFlags{network: "simulator", root: root, xmrWallet: env("LIVE_XMR_TREASURY", "127.0.0.1:28083"), xmrNet: "regtest", xmrTreasury: treasury, xmrMin: 100_000_000})
		say("config: %v", notes)
		*xmrJournal = filepath.Join(dir, "live.xmr.jsonl")
		p := run(startPayouts(ctx, x, nil, deroPasses{daemon: daemon, founders: founders}, share, false, filepath.Join(dir, "live.json"), os.Stdout))
		defer p.release()
		must(p.round(ctx))
		o := p.l.open
		if o == nil || !o.Sent {
			t.Fatalf("no payout was sent (paid %d, due %v)", p.l.Paid, p.l.Due)
		}
		mine(10, payer)
		must(p.round(ctx))
		if p.l.open != nil || p.l.Paid != o.total() {
			t.Fatalf("the payout did not close as paid (open %v, paid %d)", p.l.open, p.l.Paid)
		}
		must(pw.call(ctx, "refresh", map[string]interface{}{}, nil))
		var got struct {
			T struct {
				Amount uint64 `json:"amount"`
				Type   string `json:"type"`
				Conf   uint64 `json:"confirmations"`
				Height uint64 `json:"height"`
			} `json:"transfer"`
		}
		must(pw.call(ctx, "get_transfer_by_txid", map[string]interface{}{"txid": o.TxID}, &got))
		want := o.Dests[0].Amt
		say("XMR: the holder's wallet lists payout %s as %q, %d piconero at block %d, %d confirmations (its part: %d)", o.TxID, got.T.Type, got.T.Amount, got.T.Height, got.T.Conf, want)
		if got.T.Type != "in" || got.T.Amount != want {
			t.Fatalf("the holder's wallet does not show its part")
		}
		p.say("ledger: income %d, treasury %d, paid %d, carried %d", p.l.Income, p.l.Treasury, p.l.Paid, p.l.Carry)
	case "zec":
		node, cookie := env("LIVE_ZEC_NODE", "127.0.0.1:28232"), env("LIVE_ZEC_COOKIE", "")
		user, pass, err := zecswap.ReadCookie(cookie)
		if err != nil {
			t.Fatal("LIVE_ZEC_COOKIE: ", err)
		}
		newKey := func(name string) (*zecswap.Key, string) {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err != nil {
				var b [32]byte
				rand.Read(b[:])
				os.WriteFile(path, []byte(hex.EncodeToString(b[:])), 0o600)
			}
			k, err := zecswap.LoadKey(path, zecswap.Testnet)
			if err != nil {
				t.Fatal(err)
			}
			return k, path
		}
		tk, keyPath := newKey("zec-treasury.key")
		hk, _ := newKey("zec-holder.key")
		treasury, holderAddr := tk.Address(zecswap.Testnet), hk.Address(zecswap.Testnet)
		say("ZEC: treasury %s, holder %s", treasury, holderAddr)
		gen := func(method string, params ...interface{}) {
			body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "1.0", "id": "live", "method": method, "params": params})
			req, _ := http.NewRequest(http.MethodPost, "http://"+node+"/", bytes.NewReader(body))
			req.SetBasicAuth(user, pass)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var r struct {
				Error interface{} `json:"error"`
			}
			json.NewDecoder(resp.Body).Decode(&r)
			resp.Body.Close()
			if r.Error != nil {
				t.Fatalf("%s: %v", method, r.Error)
			}
		}
		h := call("SetZec", nil, rpc.S("addr", holderAddr))
		gen("generatetoaddress", 1, treasury) // the fee: a coinbase to the treasury, spendable after 100 blocks on regtest
		say("ZEC: a coinbase paid to the treasury stands in for the swap fees")
		gen("generate", 100)
		waitPeriod(h)
		_, z, notes := planPayouts(payoutFlags{network: "simulator", root: root, zecNodes: []string{node}, zecNet: "regtest", zecTreasury: treasury, zecKey: keyPath, zecCookie: cookie, zecMin: 10_000})
		say("config: %v", notes)
		*zecJournal = filepath.Join(dir, "live.zec.jsonl")
		p := run(startPayouts(ctx, nil, z, deroPasses{daemon: daemon, founders: founders}, share, false, filepath.Join(dir, "live.json"), os.Stdout))
		defer p.release()
		if err := p.round(ctx); err != nil {
			t.Fatal(err)
		}
		o := p.l.open
		if o == nil || !o.Sent {
			t.Fatalf("no payout was sent (paid %d, due %v)", p.l.Paid, p.l.Due)
		}
		gen("generate", 10)
		if err := p.round(ctx); err != nil {
			t.Fatal(err)
		}
		if p.l.open != nil || p.l.Paid != o.total() {
			t.Fatalf("the payout did not close as paid (open %v, paid %d)", p.l.open, p.l.Paid)
		}
		zn := zecswap.NewZcashRPC(node, user, pass)
		rt, err := zn.RawTx(ctx, o.TxID)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := zecswap.ParseTx(rt.Raw)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := zecScript(holderAddr, zecswap.Testnet)
		paid := uint64(0)
		for _, out := range tx.Out {
			if bytes.Equal(out.ScriptPubKey, want) {
				paid += out.Value
			}
		}
		say("ZEC: payout %s mined at %d, %d confirmations, %d outputs, pays the holder %d zatoshi (its part: %d)", o.TxID, rt.Height, rt.Confirmations, len(tx.Out), paid, o.Dests[0].Amt)
		if paid != o.Dests[0].Amt || !rt.Mined {
			t.Fatal("the mined payout does not pay the holder its part")
		}
		p.say("ledger: income %d, treasury %d, paid %d, carried %d", p.l.Income, p.l.Treasury, p.l.Paid, p.l.Carry)
	default:
		t.Fatalf("unknown step %q", strings.TrimSpace(step))
	}
}
