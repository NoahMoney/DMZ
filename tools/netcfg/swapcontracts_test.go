package netcfg

import (
	"encoding/json"
	"strings"
	"testing"
)

// GAP-002: the swap contracts had no field in the per-network record, so no bundle could name them and every
// companion took its contract id from whatever flag it was given. The record now carries them, config.js renders
// them only when a 64-hex id is recorded, and a network without them renders exactly as before.
func TestConfigJSCarriesTheSwapContracts(t *testing.T) {
	id := func(c byte) string { return strings.Repeat(string(c), 64) }
	n := &Network{Daemon: "127.0.0.1:20000", Factory: id('f'), Social: id('s'), Founders: id('o'),
		Htlc: id('a'), Swap: id('b'), Board: id('c'), Templates: map[string]Template{}}
	js := ConfigJS("simulator", n, "test")
	for _, want := range []string{`htlc: "` + id('a') + `"`, `swap: "` + id('b') + `"`, `board: "` + id('c') + `"`} {
		if !strings.Contains(js, want) {
			t.Errorf("config.js does not carry %s:\n%s", want, js)
		}
	}

	n.Htlc, n.Swap, n.Board = "", "", ""
	js = ConfigJS("mainnet", n, "test")
	for _, k := range []string{"htlc:", "swap:", "board:"} {
		if strings.Contains(js, k) {
			t.Errorf("a network with no %s recorded still renders one:\n%s", k, js)
		}
	}
}

// The three keys survive a load and save of the record, so simdeploy's write is what the next genconfig reads.
func TestSwapContractsRoundTrip(t *testing.T) {
	in := []byte(`{"_doc":"x","simulator":{"daemon":"d","factory":"","social":"","founders":"","vault":"","vaults":null,"chat":"","htlc":"h","swap":"s","board":"b","feeBps":0,"pfeeBps":0,"launchPfeeBps":0,"templates":{},"tela":{"index":"","durl":""}}}`)
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(in, &raw); err != nil {
		t.Fatal(err)
	}
	var n Network
	if err := json.Unmarshal(raw["simulator"], &n); err != nil {
		t.Fatal(err)
	}
	if n.Htlc != "h" || n.Swap != "s" || n.Board != "b" {
		t.Fatalf("the swap contract ids did not load: %+v", n)
	}
	out, err := json.Marshal(&n)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"htlc":"h"`, `"swap":"s"`, `"board":"b"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the saved record lost %s: %s", want, out)
		}
	}
}
