package zecswap

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// MT-L02: no secret reaches the companion's output or logs, beyond what is intended (the refund file). The three
// secrets are s (the maker's preimage), the Zcash spend key each side signs with, and the pre-signed refund hex.
// The companion's log lines are the same text the command line prints to stdout (cmd/zecswap sets Config.Log to a
// stdout logger), so checking the library's log covers the program's output; the extra lines the command adds (the
// files list, the trust warnings, the banner) carry no swap material.
//
// Two whole swaps are driven with freshly made, high-entropy keys — a random key's hex cannot collide with an
// address or hash by accident — one that ends in a claim (s goes on chain, but never through a log) and one that
// ends in the taker's refund (the pre-signed hex goes to the node, but never through a log).
func TestNoSecretReachesTheLogs(t *testing.T) {
	cases := []struct {
		name     string
		scenario string
		seed     int64
	}{
		{"a claim reveals s on chain", "happy", 201},
		{"a refund is broadcast", "maker_absent", 202},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, c.seed)
			makerKey, takerKey := randomKey(t), randomKey(t)
			tk, _ := ParseKey(takerKey)
			w.zc.Pay(P2PKHScript(tk.PubKeyHash()), takerCoins) // the taker funds from her own random key's coin
			w.cfgHook = func(role string, cfg *Config) {
				k := makerKey
				if role == RoleTaker {
					k = takerKey
				}
				cfg.Key = func(Network) (*Key, error) { return ParseKey(k) }
			}
			w.offline = offlineFor(c.scenario)
			if !w.drive(500) {
				t.Fatalf("%s did not finish: maker %s, taker %s", c.scenario, w.state(RoleMaker).Phase, w.state(RoleTaker).Phase)
			}
			dump := w.dump()
			mk, tstate := w.state(RoleMaker), w.state(RoleTaker)

			leaks := map[string]string{
				"the maker's secret s":        mk.Secret,
				"the maker's Zcash spend key": hex.EncodeToString(append([]byte(nil), makerKey[:]...)),
				"the taker's Zcash spend key": hex.EncodeToString(append([]byte(nil), takerKey[:]...)),
			}
			for name, secret := range leaks {
				if secret == "" {
					t.Fatalf("%s is empty in this run — the test is not exercising it", name)
				}
				if strings.Contains(dump, secret) {
					t.Fatalf("%s reached a log line:\n%s", name, dump)
				}
			}

			// the pre-signed refund hex is intended in the refund file and nowhere in the logs
			if len(tstate.Refunds) == 0 {
				t.Fatal("no pre-signed refunds were built")
			}
			fileBytes, err := os.ReadFile(tstate.RefundFile)
			if err != nil {
				t.Fatalf("the refund file: %v", err)
			}
			fileText := string(fileBytes)
			for i, r := range tstate.Refunds {
				if strings.Contains(dump, r.Hex) {
					t.Fatalf("the pre-signed refund hex (level %d) reached a log line", i+1)
				}
				if !strings.Contains(fileText, r.Hex) {
					t.Fatalf("the pre-signed refund hex (level %d) is missing from the refund file, its one intended home", i+1)
				}
			}
			// the refund file is the only file that carries a spend-ready secret; it must not carry s or a key
			for name, secret := range leaks {
				if strings.Contains(fileText, secret) {
					t.Fatalf("%s reached even the refund file", name)
				}
			}
		})
	}
}
