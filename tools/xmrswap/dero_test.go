package xmrswap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDaemon serves DERO.* for a fakeChain and one wallet's methods (GetAddress, GetBalance, GetHeight,
// transfer) for a named signer, with the shapes derohe's RPC returns: string variables hex-encoded, numbers as
// numbers, the estimator running the contract at height 0.
type fakeDaemon struct {
	chain    *fakeChain
	addr     string
	mu       sync.Mutex
	requests []map[string]interface{} // every request body, for the shape assertions
}

type rpcReq struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}

func (d *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req rpcReq
	// numbers as the wallet and daemon decode them, exactly: a uint64 through float64 is another number
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	d.mu.Lock()
	d.requests = append(d.requests, map[string]interface{}{"method": req.Method, "params": req.Params})
	d.mu.Unlock()
	res, rpcErr := d.handle(req)
	env := map[string]interface{}{"jsonrpc": "2.0", "id": "1"}
	if rpcErr != "" {
		env["error"] = map[string]interface{}{"code": -32000, "message": rpcErr}
	} else {
		env["result"] = res
	}
	json.NewEncoder(w).Encode(env)
}

func args(p map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	list, _ := p["sc_rpc"].([]interface{})
	for _, a := range list {
		m, _ := a.(map[string]interface{})
		out[m["name"].(string)] = m["value"]
	}
	return out
}

func u64(v interface{}) uint64 {
	switch x := v.(type) {
	case float64:
		return uint64(x)
	case json.Number:
		n, _ := strconv.ParseUint(x.String(), 10, 64)
		return n
	}
	return 0
}

func str(v interface{}) string { s, _ := v.(string); return s }

// run executes an entrypoint at height h for the signer, returning an error string on refusal.
func (d *fakeDaemon) run(h uint64, p map[string]interface{}) (Lock, string) {
	a := args(p)
	c := d.chain
	raw, _ := DeroRaw(d.addr)
	switch str(a["entrypoint"]) {
	case "Lock":
		// what the call carries, per asset: htlc.bas takes exactly one of DERO or the named asset
		burns := map[string]uint64{}
		if xf, ok := p["transfers"].([]interface{}); ok {
			for _, t := range xf {
				m, _ := t.(map[string]interface{})
				burns[str(m["scid"])] += u64(m["burn"])
			}
		}
		asset := str(a["asset"])
		for k, v := range burns {
			if k != asset && v != 0 {
				return Lock{}, "Lock: value sent in another asset than the one named"
			}
		}
		l, err := c.lock(h, raw, str(a["ha"]), str(a["hk"]), str(a["hb"]), str(a["taker"]), u64(a["t1"]), u64(a["t2"]), asset, burns[asset])
		if err != nil {
			return Lock{}, "Lock: " + err.Error()
		}
		return l, ""
	case "Claim":
		if err := c.claim(h, raw, u64(a["id"]), str(a["sa"]), str(a["kb"])); err != nil {
			return Lock{}, "Claim: " + err.Error()
		}
	case "Refund":
		if err := c.refund(h, raw, u64(a["id"]), str(a["sb"])); err != nil {
			return Lock{}, "Refund: " + err.Error()
		}
	case "Punish":
		if err := c.punish(h, raw, u64(a["id"])); err != nil {
			return Lock{}, "Punish: " + err.Error()
		}
	case "Reclaim":
		if err := c.reclaim(h, raw, u64(a["id"]), str(a["sb"])); err != nil {
			return Lock{}, "Reclaim: " + err.Error()
		}
	default:
		return Lock{}, "unknown entrypoint"
	}
	return Lock{}, ""
}

func (d *fakeDaemon) handle(req rpcReq) (interface{}, string) {
	c := d.chain
	switch req.Method {
	case "DERO.GetHeight":
		return map[string]interface{}{"height": c.Height(), "stableheight": c.StableHeight()}, ""
	case "DERO.GetInfo":
		return map[string]interface{}{"testnet": true, "network": "Simulator"}, ""
	case "DERO.GetRandomAddress":
		return map[string]interface{}{"address": []string{aliceAddr}}, ""
	case "DERO.GetGasEstimate":
		// the estimator runs at height 0 and refuses what the contract would refuse there
		c.mu.Lock()
		saved := append([]Lock(nil), c.locks...)
		_, refused := d.run(0, req.Params)
		c.locks = saved // an estimate changes nothing
		c.mu.Unlock()
		if refused != "" {
			return nil, refused
		}
		return map[string]interface{}{"gascompute": 100, "gasstorage": 383}, ""
	case "DERO.GetTransaction":
		hashes, _ := req.Params["txs_hashes"].([]interface{})
		var txs []map[string]interface{}
		c.mu.Lock()
		for _, h := range hashes {
			if at, ok := c.mined[str(h)]; ok {
				txs = append(txs, map[string]interface{}{"valid_block": "block", "block_height": at, "in_pool": false})
			} else if c.pool[str(h)] {
				txs = append(txs, map[string]interface{}{"valid_block": "", "block_height": 0, "in_pool": true})
			}
		}
		c.mu.Unlock()
		return map[string]interface{}{"txs": txs}, ""
	case "DERO.GetSC":
		c.mu.Lock()
		defer c.mu.Unlock()
		var keys map[string]interface{}
		if v, _ := req.Params["variables"].(bool); v {
			keys = map[string]interface{}{"n": len(c.locks), "kind": hex.EncodeToString([]byte("htlc")), "v": 1}
			for _, l := range c.locks {
				i := strconv.FormatUint(l.ID, 10)
				for _, k := range []string{"m_", "t_", "ha_", "hk_", "hb_", "a_", "v_", "t1_", "t2_", "st_", "xa_", "xb_"} {
					if v, ok := c.scKey(k + i); ok {
						keys[k+i] = v
					}
				}
			}
		}
		var bal uint64
		for _, l := range c.locks {
			if l.State == LockOpen {
				bal += l.Amount
			}
		}
		// derod answers what was asked for: the whole variable set only under variables:true, and one value per
		// keysstring entry, uint64 as decimal text and a string as hex of its bytes, with a key the contract has
		// not set reported as "NOT AVAILABLE" (rpc_dero_getsc.go).
		res := map[string]interface{}{"balance": bal}
		if keys != nil {
			res["stringkeys"] = keys
		}
		if asked, ok := req.Params["keysstring"].([]interface{}); ok {
			values := make([]string, 0, len(asked))
			for _, k := range asked {
				// one key at a time out of the tree, as derod's graviton lookup does: the cost of a keyed read
				// is set by what was asked for and not by how much the contract holds
				v, ok := c.scKey(str(k))
				if !ok {
					values = append(values, "NOT AVAILABLE err: leaf not found")
					continue
				}
				switch x := v.(type) {
				case string:
					values = append(values, x)
				case int:
					values = append(values, strconv.Itoa(x))
				case uint64:
					values = append(values, strconv.FormatUint(x, 10))
				default:
					values = append(values, "UNKNOWN Data type")
				}
			}
			res["valuesstring"] = values
		}
		if v, _ := req.Params["code"].(bool); v {
			res["code"] = c.codeText
		}
		return res, ""
	case "DERO.GetEncryptedBalance":
		// derod answers errormsg.ErrAccountUnregistered for an address that never registered
		raw, err := DeroRaw(str(req.Params["address"]))
		if err != nil {
			return nil, "panic occured: " + err.Error()
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.unregistered[raw] {
			return nil, "Account Unregistered"
		}
		return map[string]interface{}{"registration": 1, "status": "OK"}, ""
	case "GetAddress":
		return map[string]interface{}{"address": d.addr}, ""
	case "GetBalance":
		c.mu.Lock()
		defer c.mu.Unlock()
		b := c.balanceOf(mustRaw(d.addr), str(req.Params["scid"]))
		return map[string]interface{}{"balance": b, "unlocked_balance": b}, ""
	case "GetTransfers":
		// Show_Transfers filters by direction and height and ignores the port filter it is given
		in, _ := req.Params["in"].(bool)
		out, _ := req.Params["out"].(bool)
		min := u64(req.Params["min_height"])
		me := mustRaw(d.addr)
		c.mu.Lock()
		defer c.mu.Unlock()
		entries := []map[string]interface{}{}
		for _, p := range c.payments {
			if p.height < min {
				continue
			}
			if in && p.toRaw == me {
				entries = append(entries, map[string]interface{}{"height": p.height, "txid": p.txid, "amount": p.amount, "incoming": true, "dstport": p.port})
			}
			if out && p.fromRaw == me {
				entries = append(entries, map[string]interface{}{"height": p.height, "txid": p.txid, "amount": p.amount, "incoming": false, "dstport": p.port})
			}
		}
		return map[string]interface{}{"entries": entries}, ""
	case "GetHeight":
		return map[string]interface{}{"height": c.Height()}, ""
	case "transfer":
		// the wallet submits at the real height; a refused call is still mined (the fee is spent)
		c.mu.Lock()
		defer c.mu.Unlock()
		if u64(req.Params["ringsize"]) != 2 {
			return nil, "ringsize"
		}
		if c.stuckBelowFee != 0 && u64(req.Params["fees"]) < c.stuckBelowFee {
			// accepted into the pool and never mined: what a fee below what the miners take looks like
			c.txSeq++
			sum := sha256.Sum256([]byte("fake dero stuck tx " + strconv.FormatUint(c.txSeq, 10)))
			id := hex.EncodeToString(sum[:])
			c.pool[id] = true
			c.height += c.advanceOnStuck
			return map[string]interface{}{"txid": id}, ""
		}
		if _, sc := req.Params["sc_rpc"]; !sc {
			// a plain transfer: the stipend, its port in the payload's "D" argument
			xf, _ := req.Params["transfers"].([]interface{})
			if len(xf) != 1 {
				return nil, "one plain transfer at a time"
			}
			m, _ := xf[0].(map[string]interface{})
			var port uint64
			pl, _ := m["payload_rpc"].([]interface{})
			for _, a := range pl {
				arg, _ := a.(map[string]interface{})
				if str(arg["name"]) == "D" && str(arg["datatype"]) == "U" {
					port = u64(arg["value"])
				}
			}
			id, err := c.pay(mustRaw(d.addr), str(m["destination"]), u64(m["amount"]), port)
			if err != nil {
				return nil, err.Error()
			}
			return map[string]interface{}{"txid": id}, ""
		}
		d.run(c.height, req.Params)
		return map[string]interface{}{"txid": c.txid()}, ""
	}
	return nil, "no such method: " + req.Method
}

// scKey is one of the contract's variables as derod's tree lookup finds it, with the RPC's own encoding:
// raw bytes and hex text as hex of their bytes, numbers as numbers. It is called under c.mu.
func (c *fakeChain) scKey(k string) (interface{}, bool) {
	switch k {
	case "n":
		return len(c.locks), true
	case "kind":
		return strings.ToUpper(hex.EncodeToString([]byte("htlc"))), true
	case "v":
		return 3, true
	case "w1min": // a v3 install stores all three; 0 stands for an install that stores none (v2)
		return c.w1min, c.w1min != 0
	case "w2min":
		return c.w2min, c.w2min != 0
	case "g2":
		return c.g2, c.g2 != 0
	}
	cut := strings.LastIndex(k, "_")
	if cut < 0 {
		return nil, false
	}
	id, err := strconv.ParseUint(k[cut+1:], 10, 64)
	if err != nil || id >= uint64(len(c.locks)) {
		return nil, false
	}
	l := c.locks[id]
	hx := func(s string) string { return strings.ToUpper(hex.EncodeToString([]byte(s))) }
	raw := func(s string) string { b, _ := hex.DecodeString(s); return strings.ToUpper(hex.EncodeToString(b)) }
	switch k[:cut+1] {
	case "m_":
		return raw(l.Maker), true
	case "t_":
		return raw(l.Taker), true
	case "ha_":
		return hx(l.HA), true
	case "hk_":
		return hx(l.HK), true
	case "hb_":
		return hx(l.HB), true
	case "a_":
		return strings.ToUpper(l.Asset), true // the asset's raw 32 bytes, hex as the RPC gives them
	case "v_":
		return l.Amount, true
	case "t1_":
		return l.T1, true
	case "t2_":
		return l.T2, true
	case "st_":
		return l.State, true
	case "xa_":
		if l.XA == "" {
			return nil, false
		}
		return hx(l.XA), true
	case "xb_":
		if l.XB == "" {
			return nil, false
		}
		return hx(l.XB), true
	}
	return nil, false
}

func (d *fakeDaemon) last(method string) map[string]interface{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.requests) - 1; i >= 0; i-- {
		if d.requests[i]["method"] == method {
			return d.requests[i]["params"].(map[string]interface{})
		}
	}
	return nil
}

func (d *fakeDaemon) count(method string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, r := range d.requests {
		if r["method"] == method {
			n++
		}
	}
	return n
}

func newClient(t *testing.T, d *fakeDaemon) *DeroRPC {
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	c := NewDeroRPC(srv.URL, srv.URL, strings.Repeat("ab", 32))
	c.Poll, c.Timeout = time.Millisecond, 5*time.Second
	return c
}

func secretPair(t *testing.T) (string, string) {
	s, err := NewScalar(strings.NewReader(strings.Repeat("x", 64) + strings.Repeat("y", 64)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Hex(), SHA256Hex(s[:])
}

// TestDeroClientShapesAndExits drives every entrypoint through the HTTP client against the fake daemon and
// checks the request shapes (sc_rpc names, burn, ring size, fees), the lookup-before-lock, the fixed fee for
// the height-gated exits, and that an included-but-refused call is reported as a refusal.
func TestDeroClientShapesAndExits(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	bob := &fakeDaemon{chain: chain, addr: bobAddr}
	alice := &fakeDaemon{chain: chain, addr: aliceAddr}
	cb, ca := newClient(t, bob), newClient(t, alice)

	sa, ha := secretPair(t)
	kb, hk := strings.Repeat("11", 32), SHA256Hex(mustHex(strings.Repeat("11", 32)))
	sb, hb := strings.Repeat("22", 32), SHA256Hex(mustHex(strings.Repeat("22", 32)))

	h, err := cb.Height(ctx)
	if err != nil || h != 1000 {
		t.Fatalf("height %d %v", h, err)
	}
	txid, id, err := cb.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, "", 5000, noCutoff)
	if err != nil || id != 0 || len(txid) != 64 {
		t.Fatalf("lock: %s %d %v", txid, id, err)
	}
	p := bob.last("transfer")
	a := args(p)
	if u64(p["ringsize"]) != 2 || u64(p["fees"]) != 383+200 || str(p["scid"]) != cb.SCID {
		t.Fatalf("lock transfer shape: %v", p)
	}
	if str(a["entrypoint"]) != "Lock" || str(a["ha"]) != ha || str(a["hk"]) != hk || str(a["hb"]) != hb || str(a["taker"]) != aliceAddr || u64(a["t1"]) != h+50 || u64(a["t2"]) != h+100 || str(a["asset"]) != "" {
		t.Fatalf("lock args: %v", a)
	}
	xf := p["transfers"].([]interface{})[0].(map[string]interface{})
	if u64(xf["burn"]) != 5000 || str(xf["destination"]) == "" || str(xf["scid"]) != "" {
		t.Fatalf("lock burn: %v", xf)
	}
	est := args(bob.last("DERO.GetGasEstimate"))
	if u64(est["SC_ACTION"]) != 0 || str(est["SC_ID"]) != cb.SCID {
		t.Fatalf("estimate args: %v", est)
	}
	// the same lock again is found, not sent
	before := bob.count("transfer")
	txid2, id2, err := cb.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, "", 5000, noCutoff)
	if err != nil || id2 != 0 || txid2 != "" || bob.count("transfer") != before {
		t.Fatalf("second lock: %q %d %v, transfers %d -> %d", txid2, id2, err, before, bob.count("transfer"))
	}
	l, err := ca.ReadLock(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	bobRaw, _ := DeroRaw(bobAddr)
	aliceRaw, _ := DeroRaw(aliceAddr)
	if l.Maker != bobRaw || l.Taker != aliceRaw || l.HA != ha || l.HK != hk || l.HB != hb || l.Amount != 5000 || l.T1 != h+50 || l.T2 != h+100 || l.State != LockOpen || l.Asset != "" || l.XA != "" {
		t.Fatalf("read lock: %+v", l)
	}
	if _, err := ca.ReadLock(ctx, 7); err != ErrNoSuchLock {
		t.Fatalf("missing lock: %v", err)
	}
	if bal, err := ca.ContractBalance(ctx); err != nil || bal != 5000 {
		t.Fatalf("contract balance %d %v", bal, err)
	}

	// a wrong preimage is refused at the estimate, so no transaction is sent
	before = alice.count("transfer")
	cutoff := claimCutoff(h+50, DefaultClaimMargin)
	if _, err := ca.Claim(ctx, 0, strings.Repeat("00", 32), kb, cutoff); err == nil || alice.count("transfer") != before {
		t.Fatalf("wrong sa: err %v, transfers %d -> %d", err, before, alice.count("transfer"))
	}
	// the maker cannot refund before t1: the fixed-fee send is mined and the contract's refusal is reported
	before = bob.count("transfer")
	if _, err := cb.Refund(ctx, 0, sb, refundCutoff(h+100)); err == nil || !strings.Contains(err.Error(), "refused") || bob.count("transfer") != before+1 {
		t.Fatalf("early refund: %v, transfers %d -> %d", err, before, bob.count("transfer"))
	}
	if u64(bob.last("transfer")["fees"]) != cb.FixedFee {
		t.Fatalf("refund fee %v, want the fixed fee %d", bob.last("transfer")["fees"], cb.FixedFee)
	}
	// the claim, estimated (fee from the estimator), reveals s_a
	estBefore := alice.count("DERO.GetGasEstimate")
	txid, err = ca.Claim(ctx, 0, sa, kb, cutoff)
	if err != nil || len(txid) != 64 || alice.count("DERO.GetGasEstimate") != estBefore+1 {
		t.Fatalf("claim: %s %v", txid, err)
	}
	if u64(alice.last("transfer")["fees"]) != 383+200 {
		t.Fatalf("claim fee %v", alice.last("transfer")["fees"])
	}
	l, _ = cb.ReadLock(ctx, 0)
	if l.State != LockClaimed || l.XA != sa {
		t.Fatalf("after claim: %+v", l)
	}

	// second lock: refund in the window, with the fixed fee and no estimate
	h, _ = cb.Height(ctx)
	// t1 at least w1min + DeroTxValidity ahead, or the client's own cutoff holds the Lock back; t2 far enough past t1 that
	// the refund cutoff, t2 − DeroTxValidity, is still ahead once the chain is in [t1, t2)
	_, id, err = cb.Lock(ctx, hb, hk, ha, aliceAddr, h+21, h+40, "", 700, lockCutoff(h+21, chain.w1min))
	if err != nil || id != 1 {
		t.Fatalf("lock 2: %d %v", id, err)
	}
	chain.Advance(21)
	estBefore = bob.count("DERO.GetGasEstimate")
	if _, err := cb.Refund(ctx, 1, sa, refundCutoff(h+40)); err != nil || bob.count("DERO.GetGasEstimate") != estBefore {
		t.Fatalf("refund: %v (estimates %d -> %d)", err, estBefore, bob.count("DERO.GetGasEstimate"))
	}
	l, _ = ca.ReadLock(ctx, 1)
	if l.State != LockRefunded || l.XB != sa {
		t.Fatalf("after refund: %+v", l)
	}

	// third lock: punish in [t2, t2 + g2). htlc.bas pays the taker whoever sends it (LIV-07), so any client can send
	// it; a second Punish is refused because the lock is no longer open
	h, _ = cb.Height(ctx)
	_, id, err = cb.Lock(ctx, hk, hb, ha, aliceAddr, h+21, h+27, "", 900, lockCutoff(h+21, chain.w1min))
	if err != nil || id != 2 {
		t.Fatalf("lock 3: %d %v", id, err)
	}
	if _, err := ca.Punish(ctx, 2, noCutoff); err == nil {
		t.Fatal("punish before t2 accepted")
	}
	chain.Advance(30)
	if _, err := cb.Punish(ctx, 2, punishCutoff(h+27, chain.g2, 0)); err != nil {
		t.Fatalf("punish by the maker: %v", err)
	}
	// a Punish on a lock that has moved is not even sent: it could only be mined, refused and paid for
	before = alice.count("transfer")
	if _, err := ca.Punish(ctx, 2, noCutoff); err == nil || !strings.Contains(err.Error(), "already moved") || alice.count("transfer") != before {
		t.Fatalf("a second punish: %v, transfers %d -> %d", err, before, alice.count("transfer"))
	}
	l, _ = ca.ReadLock(ctx, 2)
	if l.State != LockPunished {
		t.Fatalf("after punish: %+v", l)
	}

	// fourth lock (htlc.bas v3, MT-J13): nobody punishes in [t2, t2 + g2). From t2 + g2 the taker's client sends no
	// Punish, one sent anyway is mined and refused, and the maker Reclaims with s_b, stored in xb_ as a Refund stores
	// it, at the fixed fee and with no estimate (at the estimator's height 0 a Reclaim is always refused)
	h, _ = cb.Height(ctx)
	t1, t2 := h+21, h+26
	_, id, err = cb.Lock(ctx, hk, ha, hb, aliceAddr, t1, t2, "", 800, lockCutoff(t1, chain.w1min))
	if err != nil || id != 3 {
		t.Fatalf("lock 4: %d %v", id, err)
	}
	if _, err := cb.Reclaim(ctx, 3, sb); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a Reclaim before t2 + g2: %v", err)
	}
	for chain.Height() < t2+chain.g2 {
		chain.Advance(1)
	}
	before = alice.count("transfer")
	if _, err := ca.Punish(ctx, 3, punishCutoff(t2, chain.g2, 0)); err == nil || !strings.Contains(err.Error(), "not sent") || alice.count("transfer") != before {
		t.Fatalf("a Punish past its cutoff: %v, transfers %d -> %d", err, before, alice.count("transfer"))
	}
	if _, err := ca.Punish(ctx, 3, noCutoff); err == nil || !strings.Contains(err.Error(), "refused") || alice.count("transfer") != before+1 {
		t.Fatalf("a Punish sent past t2 + g2: %v, transfers %d -> %d", err, before, alice.count("transfer"))
	}
	if _, err := ca.Reclaim(ctx, 3, sb); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("the taker's Reclaim: %v", err)
	}
	estBefore = bob.count("DERO.GetGasEstimate")
	if _, err := cb.Reclaim(ctx, 3, sb); err != nil || bob.count("DERO.GetGasEstimate") != estBefore || u64(bob.last("transfer")["fees"]) != cb.FixedFee {
		t.Fatalf("reclaim: %v (estimates %d -> %d, fee %v)", err, estBefore, bob.count("DERO.GetGasEstimate"), bob.last("transfer")["fees"])
	}
	if a := args(bob.last("transfer")); str(a["entrypoint"]) != "Reclaim" || u64(a["id"]) != 3 || str(a["sb"]) != sb {
		t.Fatalf("reclaim args: %v", a)
	}
	l, _ = ca.ReadLock(ctx, 3)
	if l.State != LockReclaimed || l.XB != sb {
		t.Fatalf("after reclaim: %+v", l)
	}
	if bal, _ := ca.ContractBalance(ctx); bal != 0 {
		t.Fatalf("contract still holds %d", bal)
	}

	// MT-A03(a): a Lock narrower than w1min passes the daemon's estimate, which runs the contract at height 0, and is
	// refused on chain with its fee spent (seen live, docs/HTLC_V3_COMPANION_NOTES.md §8). The client's cutoff is the
	// only check that keeps it from being sent.
	h, _ = cb.Height(ctx)
	narrow := h + chain.w1min - 1
	before = bob.count("transfer")
	if _, _, err := cb.Lock(ctx, hb, ha, hk, aliceAddr, narrow, narrow+10, "", 600, noCutoff); err == nil || !strings.Contains(err.Error(), "recorded no lock") || bob.count("transfer") != before+1 {
		t.Fatalf("a narrow Lock with no cutoff: %v, transfers %d -> %d", err, before, bob.count("transfer"))
	}
	before = bob.count("transfer")
	if _, _, err := cb.Lock(ctx, hb, ha, hk, aliceAddr, narrow, narrow+10, "", 600, lockCutoff(narrow, chain.w1min)); !errors.Is(err, ErrLockTooLate) || bob.count("transfer") != before {
		t.Fatalf("a narrow Lock past its cutoff: %v, transfers %d -> %d", err, before, bob.count("transfer"))
	}
}

// TestTheCutoffsAreTheContractsWindows (MT-A03(a), MT-J13): a Lock built at the last height before lockCutoff and
// executed DeroTxValidity blocks later still meets w1min, and one built at the cutoff might not; a Punish built at the
// last height before punishCutoff executes, and is stable with the margin to spare, before t2 + g2.
func TestTheCutoffsAreTheContractsWindows(t *testing.T) {
	for _, w1min := range []uint64{1, 10, 400} {
		for _, t1 := range []uint64{w1min + 11, w1min + 12, 5000} {
			cut := lockCutoff(t1, w1min)
			if last := cut - 1; last+DeroTxValidity+w1min > t1 {
				t.Fatalf("w1min %d, t1 %d: a Lock built at %d may execute at %d, less than w1min before t1", w1min, t1, last, last+DeroTxValidity)
			}
			if cut+DeroTxValidity+w1min <= t1 {
				t.Fatalf("w1min %d, t1 %d: the cutoff %d holds back a Lock that could not have been refused", w1min, t1, cut)
			}
		}
		if lockCutoff(w1min+10, w1min) != 0 {
			t.Fatalf("w1min %d: a t1 only w1min + 10 ahead of height 0 left a height to build at", w1min)
		}
	}
	for _, g2 := range []uint64{22, 30, 400, 4800} {
		for _, margin := range []uint64{0, 2, 8} {
			const t2 = 10000
			cut := punishCutoff(t2, g2, margin)
			if cut == 0 {
				if g2 > DeroTxValidity+DefaultStableBlocks+margin {
					t.Fatalf("g2 %d margin %d: no height to punish at", g2, margin)
				}
				continue
			}
			if last := cut - 1; last+DeroTxValidity+DefaultStableBlocks+margin >= t2+g2 {
				t.Fatalf("g2 %d margin %d: a Punish built at %d is stable only at %d, past t2 + g2 = %d", g2, margin, last, last+DeroTxValidity+DefaultStableBlocks, t2+g2)
			}
		}
	}
}

// TestDeroClientAssetLockAndStipendShapes (U1, Path 0, U4): an asset lock burns the asset alone, at ring 2, and
// names it in the call; the lock reads back with that asset; the stipend is a ring-2 transfer carrying its port in
// the payload, found again by that port in both wallets; balances are read per asset; registration and the code
// hash come from the daemon.
func TestDeroClientAssetLockAndStipendShapes(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	bob := &fakeDaemon{chain: chain, addr: bobAddr}
	alice := &fakeDaemon{chain: chain, addr: aliceAddr}
	cb, ca := newClient(t, bob), newClient(t, alice)
	asset := strings.Repeat("9a", 32)
	_, ha := secretPair(t)
	hk, hb := SHA256Hex(mustHex(strings.Repeat("11", 32))), SHA256Hex(mustHex(strings.Repeat("22", 32)))

	h, _ := cb.Height(ctx)
	if _, _, err := cb.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, strings.ToUpper(asset), 5000, noCutoff); err == nil {
		t.Fatal("an asset id in capitals was sent")
	}
	txid, id, err := cb.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, asset, 5000, noCutoff)
	if err != nil || len(txid) != 64 {
		t.Fatalf("asset lock: %v", err)
	}
	p := bob.last("transfer")
	xf := p["transfers"].([]interface{})[0].(map[string]interface{})
	if u64(p["ringsize"]) != 2 || str(xf["scid"]) != asset || u64(xf["burn"]) != 5000 || u64(xf["amount"]) != 0 || str(args(p)["asset"]) != asset {
		t.Fatalf("asset lock shape: %v", p)
	}
	est := bob.last("DERO.GetGasEstimate")
	if ex := est["transfers"].([]interface{})[0].(map[string]interface{}); str(ex["scid"]) != asset {
		t.Fatalf("the estimate did not carry the asset: %v", est)
	}
	l, err := ca.ReadLock(ctx, id)
	if err != nil || l.Asset != asset || l.Amount != 5000 {
		t.Fatalf("asset lock read back: %+v %v", l, err)
	}

	chain.setBalance(bobAddr, asset, 777)
	chain.setBalance(bobAddr, "", 888)
	if b, err := cb.Balance(ctx, asset); err != nil || b != 777 || str(bob.last("GetBalance")["scid"]) != asset {
		t.Fatalf("asset balance %d %v", b, err)
	}
	if b, err := cb.Balance(ctx, ""); err != nil || b != 888 {
		t.Fatalf("DERO balance %d %v", b, err)
	}

	port := stipendPort(hk)
	txid, err = cb.Pay(ctx, aliceAddr, 14016, port)
	if err != nil || len(txid) != 64 {
		t.Fatalf("stipend: %v", err)
	}
	p = bob.last("transfer")
	xf = p["transfers"].([]interface{})[0].(map[string]interface{})
	pl := xf["payload_rpc"].([]interface{})[0].(map[string]interface{})
	if u64(p["ringsize"]) != 2 || str(xf["destination"]) != aliceAddr || u64(xf["amount"]) != 14016 || str(pl["name"]) != "D" || str(pl["datatype"]) != "U" || u64(pl["value"]) != port {
		t.Fatalf("stipend shape: %v", p)
	}
	// both wallets find it by the port alone; another port finds nothing
	for _, c := range []struct {
		cl       *DeroRPC
		incoming bool
	}{{ca, true}, {cb, false}} {
		got, found, err := c.cl.FindPayment(ctx, c.incoming, port, h)
		if err != nil || !found || got.TxID != txid || got.Amount != 14016 {
			t.Fatalf("find (incoming %v): %+v %v %v", c.incoming, got, found, err)
		}
		if _, found, _ := c.cl.FindPayment(ctx, c.incoming, port+1, h); found {
			t.Fatal("another port found the stipend")
		}
	}

	if ok, err := cb.Registered(ctx, aliceAddr); err != nil || !ok {
		t.Fatalf("registered: %v %v", ok, err)
	}
	chain.setUnregistered(aliceAddr, true)
	if ok, err := cb.Registered(ctx, aliceAddr); err != nil || ok {
		t.Fatalf("unregistered: %v %v", ok, err)
	}
	if _, err := cb.Pay(ctx, aliceAddr, 1, port); err == nil {
		t.Fatal("a transfer to an unregistered address went through")
	}
	if got, err := cb.CodeSHA256(ctx); err != nil || got != SHA256Hex([]byte(chain.codeText)) {
		t.Fatalf("code hash %s %v", got, err)
	}
	// MT-A03(a): the install's three arguments come back with the code, in one DERO.GetSC, so both describe one state
	before := bob.count("DERO.GetSC")
	d, err := cb.Deployment(ctx)
	if err != nil || d.CodeSHA256 != SHA256Hex([]byte(chain.codeText)) || d.W1Min != chain.w1min || d.W2Min != chain.w2min || d.G2 != chain.g2 || bob.count("DERO.GetSC") != before+1 {
		t.Fatalf("deployment %+v %v, %d reads", d, err, bob.count("DERO.GetSC")-before)
	}
	if p := bob.last("DERO.GetSC"); p["code"] != true || fmt.Sprint(p["keysstring"]) != "[w1min w2min g2]" {
		t.Fatalf("deployment read shape: %v", p)
	}
	// an install that stores none of them (htlc.bas v2's shape) reads as zeros, which checkDeployment refuses
	chain.mu.Lock()
	chain.w1min, chain.w2min, chain.g2 = 0, 0, 0
	chain.mu.Unlock()
	if d, err := cb.Deployment(ctx); err != nil || d.W1Min != 0 || d.W2Min != 0 || d.G2 != 0 {
		t.Fatalf("a v2-shaped install: %+v %v", d, err)
	}
}

// TestCanResendStopsAtTheClaimCutoff is XMR-06's regression. The client's Claim re-send rule and the state
// machine's claim cutoff are one rule now: at every height where the cutoff forbids a Claim, the client will not
// re-send one either, for every margin. The old rule (h + 8 + 1 < t1) allowed a re-send at eight heights inside
// [t1 − 16, t1) with the default margin.
func TestCanResendStopsAtTheClaimCutoff(t *testing.T) {
	l := Lock{T1: 1000, T2: 1400}
	for _, margin := range []uint64{0, 8, 16} {
		cut := claimCutoff(l.T1, margin)
		for h := uint64(900); h < l.T1+5; h++ {
			if got, want := canResend("Claim", h, l, cut), h+1 < cut; got != want {
				t.Fatalf("margin %d, height %d: canResend %v, the cutoff says %v", margin, h, got, want)
			}
		}
	}
	// and the client sends nothing, not even a first attempt, inside the band
	ctx := context.Background()
	chain := newFakeChain(1000)
	bob := &fakeDaemon{chain: chain, addr: bobAddr}
	alice := &fakeDaemon{chain: chain, addr: aliceAddr}
	cb, ca := newClient(t, bob), newClient(t, alice)
	sa, ha := secretPair(t)
	kb, hk := strings.Repeat("11", 32), SHA256Hex(mustHex(strings.Repeat("11", 32)))
	hb := SHA256Hex(mustHex(strings.Repeat("22", 32)))
	h, _ := cb.Height(ctx)
	if _, _, err := cb.Lock(ctx, ha, hk, hb, aliceAddr, h+40, h+80, "", 5000, noCutoff); err != nil {
		t.Fatal(err)
	}
	now, _ := ca.Height(ctx)
	before := alice.count("transfer")
	if _, err := ca.Claim(ctx, 0, sa, kb, now+1); err == nil || !strings.Contains(err.Error(), "cutoff") || alice.count("transfer") != before {
		t.Fatalf("a Claim at the cutoff: %v, transfers %d -> %d", err, before, alice.count("transfer"))
	}
}

// TestCanResendPunishStopsAtThePunishCutoff (MT-J13): the client's Punish re-send rule and the state machine's punish
// cutoff are one rule, as XMR-06 made them for the Claim: a re-send at cutoff − 1, none at the cutoff or after it, for
// every g2 and margin. A Refund is re-sent while height < its cutoff, t2 − DeroTxValidity (MT-B27); a Reclaim has no end.
func TestCanResendPunishStopsAtThePunishCutoff(t *testing.T) {
	l := Lock{T1: 1000, T2: 1400}
	for _, g2 := range []uint64{30, 400, 4800} {
		for _, margin := range []uint64{0, 2, 8} {
			cut := punishCutoff(l.T2, g2, margin)
			if !canResend("Punish", cut-1, l, cut) || canResend("Punish", cut, l, cut) || canResend("Punish", l.T2+g2, l, cut) {
				t.Fatalf("g2 %d margin %d: canResend at cutoff − 1 %v, at the cutoff %d %v, at t2 + g2 %v", g2, margin, canResend("Punish", cut-1, l, cut), cut, canResend("Punish", cut, l, cut), canResend("Punish", l.T2+g2, l, cut))
			}
			for h := l.T2; h < l.T2+g2+2; h++ {
				if got, want := canResend("Punish", h, l, cut), h < cut; got != want {
					t.Fatalf("g2 %d margin %d, height %d: canResend %v, the cutoff %d says %v", g2, margin, h, got, cut, want)
				}
			}
		}
	}
	// MT-B27: the rule was h < t2, which let a re-send out at heights from which it could execute at or after t2
	rcut := refundCutoff(l.T2)
	if rcut != l.T2-DeroTxValidity || !canResend("Refund", rcut-1, l, rcut) || canResend("Refund", rcut, l, rcut) || canResend("Refund", l.T2-1, l, rcut) {
		t.Fatalf("a Refund's re-send does not stop at its cutoff %d (t2 %d − %d)", rcut, l.T2, DeroTxValidity)
	}
	for h := l.T1; h < l.T2+2; h++ {
		if got, want := canResend("Refund", h, l, rcut), h < rcut; got != want {
			t.Fatalf("height %d: canResend(Refund) %v, the cutoff %d says %v", h, got, rcut, want)
		}
	}
	for _, h := range []uint64{l.T2, l.T2 + 4800, 1 << 40} {
		if !canResend("Reclaim", h, l, 0) {
			t.Fatalf("a Reclaim's re-send stopped at height %d", h)
		}
	}
}

// TestDeroClientPunishStopsAtItsCutoff (MT-J13): DeroRPC's own Punish check (exit, h >= cutoff) with the cutoff
// punishCutoff gives. At cutoff − 1 the Punish is sent and executes inside [t2, t2 + g2); at the cutoff nothing is sent;
// and a Punish sent at cutoff − 1 that sits in the pool is not re-sent once the height has reached the cutoff.
func TestDeroClientPunishStopsAtItsCutoff(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	bob := &fakeDaemon{chain: chain, addr: bobAddr}
	alice := &fakeDaemon{chain: chain, addr: aliceAddr}
	cb, ca := newClient(t, bob), newClient(t, alice)
	ca.Timeout = 20 * time.Millisecond
	// a fresh lock for each case (fresh hashes, or the idempotent Lock finds the last one), and its punish cutoff
	lock := func(n int) (id, t2, cut uint64) {
		h, _ := cb.Height(ctx)
		t1 := h + 21
		t2 = t1 + 6
		hash := func(s string) string { return SHA256Hex([]byte(fmt.Sprintf("%s %d", s, n))) }
		_, id, err := cb.Lock(ctx, hash("ha"), hash("hk"), hash("hb"), aliceAddr, t1, t2, "", 900, lockCutoff(t1, chain.w1min))
		if err != nil {
			t.Fatalf("lock %d: %v", n, err)
		}
		return id, t2, punishCutoff(t2, chain.g2, testClaimMargin)
	}

	id, t2, cut := lock(0)
	for chain.Height() < cut-1 {
		chain.Advance(1)
	}
	before := alice.count("transfer")
	if _, err := ca.Punish(ctx, id, cut); err != nil || alice.count("transfer") != before+1 {
		t.Fatalf("a Punish at cutoff − 1 = %d: %v, transfers %d -> %d", cut-1, err, before, alice.count("transfer"))
	}
	if l, _ := ca.ReadLock(ctx, id); l.State != LockPunished || cut-1 < t2 || cut-1 >= t2+chain.g2 {
		t.Fatalf("the Punish at %d (t2 %d, g2 %d) left the lock in state %d", cut-1, t2, chain.g2, l.State)
	}

	id, _, cut = lock(1)
	for chain.Height() < cut {
		chain.Advance(1)
	}
	before = alice.count("transfer")
	if _, err := ca.Punish(ctx, id, cut); err == nil || !strings.Contains(err.Error(), "not sent") || alice.count("transfer") != before {
		t.Fatalf("a Punish at the cutoff %d: %v, transfers %d -> %d", cut, err, before, alice.count("transfer"))
	}
	if l, _ := ca.ReadLock(ctx, id); l.State != LockOpen {
		t.Fatalf("a Punish refused by the client moved the lock to %d", l.State)
	}

	id, _, cut = lock(2)
	for chain.Height() < cut-1 {
		chain.Advance(1)
	}
	chain.stuckBelowFee, chain.advanceOnStuck = 1<<40, 1 // the chain reaches the cutoff while the first send sits in the pool
	before = alice.count("transfer")
	_, err := ca.Punish(ctx, id, cut)
	chain.stuckBelowFee, chain.advanceOnStuck = 0, 0
	if err == nil || !strings.Contains(err.Error(), "window") || alice.count("transfer") != before+1 {
		t.Fatalf("a stuck Punish past the cutoff: %v, transfers %d -> %d", err, before, alice.count("transfer"))
	}
}

// TestDeroClientRefundStopsAtItsCutoff (MT-B27): DeroRPC's own Refund check (exit, h >= cutoff) with the cutoff
// refundCutoff gives. At cutoff − 1 the Refund is sent and executes before t2; at the cutoff nothing is sent, since one
// built there could execute at t2, where htlc.bas refuses it and the lock stays open to Punish; and a Refund sent at
// cutoff − 1 that sits in the pool is not re-sent once the height has reached the cutoff.
func TestDeroClientRefundStopsAtItsCutoff(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	bob := &fakeDaemon{chain: chain, addr: bobAddr}
	cb := newClient(t, bob)
	cb.Timeout = 20 * time.Millisecond
	sb := strings.Repeat("22", 32)
	// a fresh lock for each case (fresh hashes, or the idempotent Lock finds the last one), and its refund cutoff
	lock := func(n int) (id, t2, cut uint64) {
		h, _ := cb.Height(ctx)
		t1 := h + 21
		t2 = t1 + 20
		hash := func(s string) string { return SHA256Hex([]byte(fmt.Sprintf("%s %d", s, n))) }
		_, id, err := cb.Lock(ctx, hash("ha"), hash("hk"), SHA256Hex(mustHex(sb)), aliceAddr, t1, t2, "", 900, lockCutoff(t1, chain.w1min))
		if err != nil {
			t.Fatalf("lock %d: %v", n, err)
		}
		return id, t2, refundCutoff(t2)
	}

	id, t2, cut := lock(0)
	for chain.Height() < cut-1 {
		chain.Advance(1)
	}
	before := bob.count("transfer")
	if _, err := cb.Refund(ctx, id, sb, cut); err != nil || bob.count("transfer") != before+1 {
		t.Fatalf("a Refund at cutoff − 1 = %d: %v, transfers %d -> %d", cut-1, err, before, bob.count("transfer"))
	}
	if l, _ := cb.ReadLock(ctx, id); l.State != LockRefunded || l.XB != sb || cut+DeroTxValidity != t2 {
		t.Fatalf("the Refund at %d (t2 %d) left the lock in state %d", cut-1, t2, l.State)
	}

	id, _, cut = lock(1)
	for chain.Height() < cut {
		chain.Advance(1)
	}
	before = bob.count("transfer")
	if _, err := cb.Refund(ctx, id, sb, cut); err == nil || !strings.Contains(err.Error(), "not sent") || bob.count("transfer") != before {
		t.Fatalf("a Refund at the cutoff %d: %v, transfers %d -> %d", cut, err, before, bob.count("transfer"))
	}
	if l, _ := cb.ReadLock(ctx, id); l.State != LockOpen {
		t.Fatalf("a Refund refused by the client moved the lock to %d", l.State)
	}

	id, _, cut = lock(2)
	for chain.Height() < cut-1 {
		chain.Advance(1)
	}
	chain.stuckBelowFee, chain.advanceOnStuck = 1<<40, 1 // the chain reaches the cutoff while the first send sits in the pool
	before = bob.count("transfer")
	_, err := cb.Refund(ctx, id, sb, cut)
	chain.stuckBelowFee, chain.advanceOnStuck = 0, 0
	if err == nil || !strings.Contains(err.Error(), "window") || bob.count("transfer") != before+1 {
		t.Fatalf("a stuck Refund past the cutoff: %v, transfers %d -> %d", err, before, bob.count("transfer"))
	}
}

// TestDeroClientResendsWithHigherFee (§4 policy, review R24): an exit whose transaction sits in the pool past
// Timeout is sent again at double the fee while its window is open — a Claim while height + 1 < its cutoff, a
// Refund while height < its cutoff, t2 − DeroTxValidity (MT-B27) — and abandoned, with the error saying so, once it is
// not. Here Punish is given no cutoff.
func TestDeroClientResendsWithHigherFee(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	bob := &fakeDaemon{chain: chain, addr: bobAddr}
	alice := &fakeDaemon{chain: chain, addr: aliceAddr}
	cb, ca := newClient(t, bob), newClient(t, alice)
	cb.Timeout, ca.Timeout = 20*time.Millisecond, 20*time.Millisecond
	var logs []string
	ca.Log = func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }
	sa, ha := secretPair(t)
	kb, hk := strings.Repeat("11", 32), SHA256Hex(mustHex(strings.Repeat("11", 32)))
	sb, hb := strings.Repeat("22", 32), SHA256Hex(mustHex(strings.Repeat("22", 32)))

	h, _ := cb.Height(ctx)
	if _, _, err := cb.Lock(ctx, ha, hk, hb, aliceAddr, h+40, h+80, "", 5000, noCutoff); err != nil {
		t.Fatal(err)
	}
	// the estimator prices the Claim at 583; the miners take nothing under 1000: the first send sits in the
	// pool, the second (1166) mines
	chain.stuckBelowFee = 1000
	before := alice.count("transfer")
	txid, err := ca.Claim(ctx, 0, sa, kb, claimCutoff(h+40, DefaultClaimMargin))
	if err != nil || len(txid) != 64 || alice.count("transfer") != before+2 {
		t.Fatalf("claim: %s %v, transfers %d -> %d", txid, err, before, alice.count("transfer"))
	}
	resent := 0
	for _, l := range logs {
		if strings.Contains(l, "re-sending at 1166") {
			resent++
		}
	}
	if u64(alice.last("transfer")["fees"]) != 2*(383+200) || resent != 1 {
		t.Fatalf("re-send fee %v, logs %v", alice.last("transfer")["fees"], logs)
	}
	if l, _ := ca.ReadLock(ctx, 0); l.State != LockClaimed {
		t.Fatalf("after the re-sent claim: %+v", l)
	}

	// a Claim stuck in the pool is not re-sent once the height reaches the claim cutoff: one send, and the error
	// names the window (the cutoff here is the current height + 1, reached by the time the re-send is weighed)
	chain.stuckBelowFee = 0
	h, _ = cb.Height(ctx)
	if _, _, err := cb.Lock(ctx, hb, hk, ha, aliceAddr, h+12, h+80, "", 700, noCutoff); err != nil {
		t.Fatal(err)
	}
	chain.stuckBelowFee, chain.advanceOnStuck = 1<<40, 1 // the chain moves on while the first send sits in the pool
	before = alice.count("transfer")
	now, _ := ca.Height(ctx)
	_, err = ca.Claim(ctx, 1, sb, kb, now+2)
	chain.advanceOnStuck = 0
	if err == nil || !strings.Contains(err.Error(), "window") || alice.count("transfer") != before+1 {
		t.Fatalf("claim past the re-send window: %v, transfers %d -> %d", err, before, alice.count("transfer"))
	}
	// a Refund is re-sent until Resends is spent, then given up with the not-mined error
	chain.Advance(20) // into [t1, t2)
	before = bob.count("transfer")
	rcut := refundCutoff(h + 80)
	_, err = cb.Refund(ctx, 1, sa, rcut)
	if err == nil || !errors.Is(err, errNotMined) || bob.count("transfer") != before+1+cb.Resends {
		t.Fatalf("refund with every re-send stuck: %v, transfers %d -> %d", err, before, bob.count("transfer"))
	}
	if u64(bob.last("transfer")["fees"]) != cb.FixedFee<<uint(cb.Resends) {
		t.Fatalf("last refund fee %v, want %d", bob.last("transfer")["fees"], cb.FixedFee<<uint(cb.Resends))
	}
	// and not at all, not even a first attempt, once height ≥ t2 (past the refund cutoff), where only Punish can move
	// the lock (MT-B27: this Refund was once sent, mined and refused)
	chain.Advance(60)
	before = bob.count("transfer")
	if _, err := cb.Refund(ctx, 1, sa, rcut); err == nil || !strings.Contains(err.Error(), "not sent") || bob.count("transfer") != before {
		t.Fatalf("refund past t2: %v, transfers %d -> %d", err, before, bob.count("transfer"))
	}
	// Punish keeps re-sending: the fee climbs past what the miners take and the lock is punished
	chain.stuckBelowFee = 3*cb.FixedFee + 1
	before = alice.count("transfer")
	if _, err := ca.Punish(ctx, 1, noCutoff); err != nil || alice.count("transfer") != before+3 {
		t.Fatalf("punish: %v, transfers %d -> %d", err, before, alice.count("transfer"))
	}
	if l, _ := ca.ReadLock(ctx, 1); l.State != LockPunished {
		t.Fatalf("after the re-sent punish: %+v", l)
	}
}

// TestDeroClientErrorsAreErrors: an rpc error, an unreachable host and a malformed reply come back as errors —
// never as an exit, never as a zero that looks like a value.
func TestDeroClientErrorsAreErrors(t *testing.T) {
	ctx := context.Background()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"no such method"}}`))
	}))
	defer bad.Close()
	c := NewDeroRPC(bad.URL, bad.URL, strings.Repeat("ab", 32))
	if _, err := c.Height(ctx); err == nil || !strings.Contains(err.Error(), "no such method") {
		t.Fatalf("rpc error: %v", err)
	}
	if _, err := c.ReadLock(ctx, 0); err == nil {
		t.Fatal("rpc error on ReadLock ignored")
	}
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) }))
	defer junk.Close()
	c = NewDeroRPC(junk.URL, junk.URL, strings.Repeat("ab", 32))
	if _, err := c.Height(ctx); err == nil {
		t.Fatal("malformed reply accepted")
	}
	c = NewDeroRPC("127.0.0.1:1", "127.0.0.1:1", strings.Repeat("ab", 32))
	c.HTTP.Timeout = 500 * time.Millisecond
	if _, err := c.Height(ctx); err == nil {
		t.Fatal("unreachable host accepted")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Height(cctx); err == nil {
		t.Fatal("cancelled context accepted")
	}
}

// TestParseLockExactNumbers: a uint64 above 2^53 survives DERO.GetSC intact (json.Number, never float64).
func TestParseLockExactNumbers(t *testing.T) {
	big := "18446744073709551615"
	raw := `{"stringkeys":{"m_0":"01","t_0":"02","ha_0":"6161","hk_0":"6262","hb_0":"6363","a_0":"","v_0":` + big + `,"t1_0":9007199254740993,"t2_0":9007199254740995,"st_0":0},"balance":0}`
	var st scState
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&st); err != nil {
		t.Fatal(err)
	}
	l, err := parseLock(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if l.Amount != 18446744073709551615 || l.T1 != 9007199254740993 || l.T2 != 9007199254740995 || l.HA != "aa" || l.Maker != "01" {
		t.Fatalf("parsed %+v", l)
	}
}

// TestDeroRaw: the raw form the contract stores is the same for the testnet and mainnet spellings of one
// account, is 33 bytes, and is refused for anything that is not a DERO address.
func TestDeroRaw(t *testing.T) {
	pairs := [][2]string{
		{"deto1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgmat5s8", "dero1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgpyhv40"},
		{"deto1qyzj2ldw7cjpprhf84ljmxdw54w72jekfkwt0nqsyyzgatttg37d2qgefmama", "dero1qyzj2ldw7cjpprhf84ljmxdw54w72jekfkwt0nqsyyzgatttg37d2qgrs8974"},
	}
	for _, p := range pairs {
		a, err := DeroRaw(p[0])
		if err != nil {
			t.Fatal(err)
		}
		b, err := DeroRaw(p[1])
		if err != nil {
			t.Fatal(err)
		}
		if a != b || len(a) != 66 || a != strings.ToLower(a) {
			t.Fatalf("raw forms differ or are malformed: %s %s", a, b)
		}
	}
	if x, _ := DeroRaw(bobAddr); x == mustRaw(aliceAddr) {
		t.Fatal("two accounts share a raw form")
	}
	for _, bad := range []string{"", "nonsense", "dero1", "deto1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgmat5s9", "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"} {
		if _, err := DeroRaw(bad); err == nil {
			t.Fatalf("DeroRaw(%q) accepted", bad)
		}
	}
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func mustRaw(addr string) string {
	r, err := DeroRaw(addr)
	if err != nil {
		panic(err)
	}
	return r
}
