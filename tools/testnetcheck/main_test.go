package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	rpc "veil/tools/simrpc"
)

const tTreasury = "dero1qy067pxeld72w0s7kqe99kwal28j54mchpnsj3wlm083wgju6e3uzqgnjpaz3"

func hx(s string) string             { return hex.EncodeToString([]byte(s)) }
func jn(x uint64) json.Number        { return json.Number(strconv.FormatUint(x, 10)) }
func writeTemp(p, body string) error { return os.WriteFile(p, []byte(body), 0o644) }

// factoryState is a factory's DERO.GetSC variables as testnetcheck decodes them (numbers as json.Number, strings
// hex-encoded), as factory.bas v2 Initialize writes them; extra overrides a key, or deletes it when nil.
func factoryState(v uint64, extra map[string]interface{}) rpc.SCState {
	st := rpc.SCState{StringKeys: map[string]interface{}{
		"kind": hx("factory"), "v": jn(v), "tstr": hx(tTreasury), "treasury": hx("raw33bytes"), "regfee": jn(10000),
		"pc": jn(1), "lc": jn(1), "nc": jn(0), "initd": jn(1),
	}}
	for k, x := range extra {
		if x == nil {
			delete(st.StringKeys, k)
			continue
		}
		st.StringKeys[k] = x
	}
	return st
}

func TestJudgeFactory(t *testing.T) {
	fee, other := uint64(10000), uint64(1)
	want := factoryRec{Scid: strings.Repeat("ab", 32), Treasury: tTreasury, Regfee: &fee, MinPools: 1, MinLaunches: 1}
	cases := []struct {
		name   string
		st     rpc.SCState
		want   factoryRec
		status string
		says   string
	}{
		{"v2 with no owner is the deployed state", factoryState(2, nil), want, "PASS", "no owner key"},
		{"v2 with an owner key fails", factoryState(2, map[string]interface{}{"owner": hx("deto1someone")}), want, "FAIL", "owner key"},
		{"v1 with a live owner fails", factoryState(1, map[string]interface{}{"owner": hx("deto1someone")}), want, "FAIL", "owner key"},
		{"v1 renounced is not the deployed contract", factoryState(1, map[string]interface{}{"renounced": jn(6000)}), want, "FAIL", "renounced"},
		{"no v key fails", factoryState(2, map[string]interface{}{"v": nil}), want, "FAIL", "no v"},
		{"v2 with renounced stored fails", factoryState(2, map[string]interface{}{"renounced": jn(1)}), want, "FAIL", "never writes"},
		{"not a factory", factoryState(2, map[string]interface{}{"kind": hx("pool")}), want, "FAIL", "not a factory"},
		{"another treasury fails", factoryState(2, map[string]interface{}{"tstr": hx("dero1elsewhere")}), want, "FAIL", "pays treasury"},
		{"another regfee fails", factoryState(2, nil), factoryRec{Scid: want.Scid, Regfee: &other}, "FAIL", "regfee"},
		{"missing regfee fails", factoryState(2, map[string]interface{}{"regfee": nil}), want, "FAIL", "lacks"},
		{"fewer listings than registered fails", factoryState(2, map[string]interface{}{"pc": jn(0)}), want, "FAIL", "at least"},
		{"no expectations beyond v2", factoryState(2, nil), factoryRec{Scid: want.Scid}, "PASS", "v2"},
	}
	for _, c := range cases {
		status, text := judgeFactory(c.st, c.want)
		if status != c.status || !strings.Contains(text, c.says) {
			t.Errorf("%s: got %s %q, want %s containing %q", c.name, status, text, c.status, c.says)
		}
	}
}

func TestJudgeLock(t *testing.T) {
	refund := lockRec{Contract: "htlc", Scid: strings.Repeat("cd", 32), ID: 1, Want: 2, What: "refunded", Opens: 7400, Closes: 7800}
	swapRefund := lockRec{Contract: "swap", Scid: strings.Repeat("ef", 32), ID: 1, Want: 2, What: "refunded", Opens: 8600}
	claimed := lockRec{Contract: "htlc", Scid: strings.Repeat("cd", 32), ID: 0, Want: 1, What: "claimed"}
	cases := []struct {
		name   string
		l      lockRec
		got    uint64
		has    bool
		height int64
		status string
	}{
		{"claimed as wanted", claimed, 1, true, 7000, "PASS"},
		{"claimed lock found refunded", claimed, 2, true, 7000, "FAIL"},
		{"claimed lock still open", claimed, 0, true, 7000, "FAIL"},
		{"no such lock", claimed, 0, false, 7000, "FAIL"},
		{"left open before its window", refund, 0, true, 7399, "INFO"},
		{"window open, call not landed", refund, 0, true, 7400, "WARN"},
		{"window closed unused", refund, 0, true, 7800, "FAIL"},
		{"refunded inside the window", refund, 2, true, 7500, "PASS"},
		{"punished instead of refunded", refund, 3, true, 7900, "FAIL"},
		{"swap open before expiry", swapRefund, 0, true, 8000, "INFO"},
		{"swap expired, refund not landed", swapRefund, 0, true, 9000, "WARN"},
		{"swap refunded", swapRefund, 2, true, 9000, "PASS"},
		{"height unknown", refund, 0, true, -1, "FAIL"},
	}
	for _, c := range cases {
		if status, text := judgeLock(c.l, c.got, c.has, c.height); status != c.status {
			t.Errorf("%s: got %s %q, want %s", c.name, status, text, c.status)
		}
	}
}

func TestTelaIndexFailsClosed(t *testing.T) {
	dir := t.TempDir()
	idx := strings.Repeat("0a", 32)
	ok := filepath.Join(dir, "ok.json")
	if err := writeTemp(ok, `{"network":"testnet","index":"`+idx+`"}`); err != nil {
		t.Fatal(err)
	}
	if got, err := telaIndex(ok); err != nil || got != idx {
		t.Errorf("a testnet record with an INDEX: got %q, %v", got, err)
	}
	for name, body := range map[string]string{
		"empty.json":  `{"network":"testnet","index":""}`,
		"sim.json":    `{"network":"simulator","index":"` + idx + `"}`,
		"upper.json":  `{"network":"testnet","index":"` + strings.ToUpper(idx) + `"}`,
		"broken.json": `{`,
	} {
		p := filepath.Join(dir, name)
		if err := writeTemp(p, body); err != nil {
			t.Fatal(err)
		}
		if got, err := telaIndex(p); err == nil {
			t.Errorf("%s: accepted INDEX %q", name, got)
		}
	}
	if got, err := telaIndex(filepath.Join(dir, "missing.json")); err == nil {
		t.Errorf("a missing record: accepted INDEX %q", got)
	}
}
