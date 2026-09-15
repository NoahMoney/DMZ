package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veil/tools/netcfg"
)

var promoXMR = strings.Repeat("e1", 32)

func withVault() *netcfg.Network {
	return &netcfg.Network{Vaults: []netcfg.VaultRec{{Scid: strings.Repeat("0a", 32), VaultAsset: netcfg.VaultAsset{Sym: "XMR", Dec: 12}, Legacy: true},
		{Scid: promoXMR, VaultAsset: netcfg.VaultAsset{Sym: "XMR", Dec: 12}}, {Scid: strings.Repeat("0b", 32), VaultAsset: netcfg.VaultAsset{Sym: "ZEC", Dec: 8}}}}
}

// The quote: unset, the record's current XMR vault (a wXMR auction); refused when the record names none; dero, or any
// asset id, when named. An id the record describes takes its decimals; any other has none.
func TestPromoQuoteResolution(t *testing.T) {
	defer func(q string) { *promoQuoteF = q }(*promoQuoteF)
	*promoQuoteF = ""
	if q, dec, err := promoQuote(withVault()); err != nil || q != promoXMR || dec != 12 {
		t.Fatalf("default quote: %q %d %v", q, dec, err)
	}
	// no vault: promo.bas is left out of the run (main skips it on this error), and the rest installs
	if _, _, err := promoQuote(&netcfg.Network{}); !errors.Is(err, errNoXMRVault) || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("a record with no vault: %v", err)
	}
	*promoQuoteF = "DERO"
	if q, dec, err := promoQuote(nil); err != nil || q != "" || dec != 5 {
		t.Fatalf("dero: %q %d %v", q, dec, err)
	}
	*promoQuoteF = strings.Repeat("0b", 32)
	if q, dec, err := promoQuote(withVault()); err != nil || q != *promoQuoteF || dec != 8 {
		t.Fatalf("the ZEC vault named: %q %d %v", q, dec, err)
	}
	*promoQuoteF = strings.Repeat("cc", 32)
	if q, dec, err := promoQuote(withVault()); err != nil || dec != -1 || checkPromoParams(q, dec) == nil {
		t.Fatalf("an undescribed asset must need -promo-minbid: %q %d %v", q, dec, err)
	}
	for _, bad := range []string{"xmr", strings.Repeat("e1", 31), strings.Repeat("zz", 32)} {
		*promoQuoteF = bad
		if _, _, err := promoQuote(withVault()); err == nil {
			t.Errorf("-promo-quote %q was accepted", bad)
		}
	}
}

// The terms simdeploy installs by default, on every network and for each kind of quote, are ones the contract accepts.
func TestPromoDefaultsFitTheContract(t *testing.T) {
	defer func(n string) { *network = n }(*network)
	for _, c := range []struct {
		net   string
		quote string
		dec   int
		want  [4]uint64
	}{
		{"mainnet", promoXMR, 12, [4]uint64{3, 4800, 10000000000, 500}},            // 0.01 wXMR
		{"mainnet", strings.Repeat("0b", 32), 8, [4]uint64{3, 4800, 1000000, 500}}, // 0.01 wZEC
		{"mainnet", "", 5, [4]uint64{3, 4800, 100000, 500}},                        // 1 DERO
		{"testnet", promoXMR, 12, [4]uint64{3, 4800, 10000000000, 500}},
		{"simulator", promoXMR, 12, [4]uint64{3, 40, 100, 500}},
		{"simulator", "", 5, [4]uint64{3, 40, 10000, 500}},
	} {
		*network = c.net
		if err := checkPromoParams(c.quote, c.dec); err != nil {
			t.Fatalf("%s %q: the default promo terms are refused: %v", c.net, c.quote, err)
		}
		k, e, minbid, inc := promoParams(c.quote, c.dec)
		if [4]uint64{k, e, minbid, inc} != c.want {
			t.Fatalf("%s %q: promo defaults %d %d %d %d, want %v", c.net, c.quote, k, e, minbid, inc, c.want)
		}
	}
}

// A flag overrides its default, and a value the contract would refuse is refused here, before anything is installed.
func TestPromoFlagsAreChecked(t *testing.T) {
	defer func() { *promoSlots, *promoEpoch, *promoMinBid, *promoInc = 0, 0, 0, 0 }()
	for _, bad := range []struct {
		p *uint64
		v uint64
	}{{promoSlots, 11}, {promoEpoch, 19}, {promoEpoch, 1000001}, {promoMinBid, 10000000000001}, {promoInc, 10001}} {
		*bad.p = bad.v
		if err := checkPromoParams(promoXMR, 12); err == nil {
			t.Errorf("a promo term of %d was accepted", bad.v)
		}
		*bad.p = 0
	}
	*promoSlots, *promoEpoch = 10, 1000000
	if k, e, _, _ := promoParams(promoXMR, 12); k != 10 || e != 1000000 || checkPromoParams(promoXMR, 12) != nil {
		t.Fatalf("flags at the bounds: %d %d %v", k, e, checkPromoParams(promoXMR, 12))
	}
}

// checkPromoParams is worth having only while its limits are the contract's own.
func TestPromoLimitsAreTheContracts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "contracts", "promo.bas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"slots == 0 || slots > 10 || epoch < 20 || epoch > 1000000",
		"minbid == 0 || minbid > 10000000000000 || incbps == 0 || incbps > 10000",
		"STRLEN(quote) != 64",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("promo.bas InitializePrivate no longer checks %q; update checkPromoParams and promoQuote to match", want)
		}
	}
}

// The promo template is recorded like founders: one current hash, no legacy, and -legacy promo:... is refused.
func TestPromoTemplateKeepsNoLegacy(t *testing.T) {
	found := false
	for _, k := range templateKinds {
		found = found || k == "promo"
	}
	if !found {
		t.Fatal("promo is not a template kind: the app could not verify the promotion contract's code")
	}
	h := strings.Repeat("a", 64)
	if err := checkHashFlags(parseHashes("promo:"+h), nil); err == nil || !strings.Contains(err.Error(), "no legacy hash") {
		t.Fatalf("-legacy promo was not refused: %v", err)
	}
}
