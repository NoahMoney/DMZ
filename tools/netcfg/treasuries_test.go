package netcfg

import (
	"encoding/json"
	"strings"
	"testing"
)

// GAP-005: a market's treasury is fixed at its install, but the app verified it against the factory's live tstr
// pointer, which the owner can move until Renounce, delisting every market in every client with one call. The
// record now carries the published treasury set, config.js renders it as `treasuries`, and app/feed.js checks
// markets against it. A record without the field renders exactly as before, so the app falls back to tstr.
func TestConfigJSCarriesTheTreasuries(t *testing.T) {
	id := func(c byte) string { return strings.Repeat(string(c), 64) }
	cur, old := "dero1qycurrenttreasury", "dero1qyearliertreasury"
	n := &Network{Daemon: "127.0.0.1:20000", Factory: id('f'), Social: id('s'), Founders: id('o'),
		Treasuries: []string{cur, old}, Templates: map[string]Template{}}
	js := ConfigJS("simulator", n, "test")
	if want := `treasuries: ["` + cur + `", "` + old + `"],`; !strings.Contains(js, want) {
		t.Errorf("config.js does not carry the treasury set in order (%s):\n%s", want, js)
	}

	n.Treasuries = nil
	if js := ConfigJS("mainnet", n, "test"); strings.Contains(js, "treasuries") {
		t.Errorf("a network with no treasury recorded still renders one:\n%s", js)
	}
}

// Older records load and save unchanged: the field is optional on the way in and omitted on the way out when
// empty, so a networks.json written before GAP-005 round-trips byte for byte on this key.
func TestTreasuriesRoundTripAndOlderRecords(t *testing.T) {
	var n Network
	if err := json.Unmarshal([]byte(`{"daemon":"d","factory":"","social":"","founders":"","vault":"","vaults":null,"chat":"","htlc":"","swap":"","board":"","feeBps":0,"pfeeBps":0,"launchPfeeBps":0,"templates":{},"tela":{"index":"","durl":""}}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.Treasuries != nil {
		t.Fatalf("a record without treasuries loaded as %v", n.Treasuries)
	}
	out, err := json.Marshal(&n)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "treasuries") {
		t.Fatalf("an older record gained a treasuries key on save: %s", out)
	}

	if err := json.Unmarshal([]byte(`{"daemon":"d","treasuries":["dero1qya","dero1qyb"],"templates":{}}`), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Treasuries) != 2 || n.Treasuries[0] != "dero1qya" {
		t.Fatalf("the treasury set did not load in order: %v", n.Treasuries)
	}
	out, _ = json.Marshal(&n)
	if !strings.Contains(string(out), `"treasuries":["dero1qya","dero1qyb"]`) {
		t.Fatalf("the saved record lost the treasury set: %s", out)
	}
}
