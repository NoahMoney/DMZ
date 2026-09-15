package netcfg

import (
	"path/filepath"
	"strings"
	"testing"
)

// The promotion contract travels from the record to config.js, where app/promo.js and tools/treasurybot read it as
// `promo: "<scid>"`. A network with none renders no promo line, so the app says it has no promotion contract rather
// than read an empty id.
func TestPromoReachesConfigJS(t *testing.T) {
	id := strings.Repeat("ab", 32)
	js := ConfigJS("simulator", &Network{Promo: id, Templates: map[string]Template{}}, "test")
	if !strings.Contains(js, "  promo: \""+id+"\",\n") {
		t.Fatalf("config.js does not name the promo contract:\n%s", js)
	}
	if js := ConfigJS("mainnet", &Network{Templates: map[string]Template{}}, "test"); strings.Contains(js, "promo:") {
		t.Fatalf("a network with no promo contract rendered one:\n%s", js)
	}
	p := filepath.Join(t.TempDir(), "networks.json")
	f := &File{Networks: map[string]*Network{"simulator": {Promo: id, Templates: map[string]Template{}}}}
	if err := f.Save(p); err != nil {
		t.Fatal(err)
	}
	g, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if g.Networks["simulator"].Promo != id {
		t.Fatalf("the record lost the promo id: %q", g.Networks["simulator"].Promo)
	}
}
