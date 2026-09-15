package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Founders Pass headers simdeploy sends by default must be ones founders.bas accepts. A default description of
// 258 bytes against the contract's 240 made every founders install from 59a5851 onwards fail at the gas estimate,
// after the factory and social contracts had already been installed and paid for.
func TestPassDefaultsFitTheContract(t *testing.T) {
	if err := checkPassHeaders(); err != nil {
		t.Fatalf("the default Founders Pass headers are refused: %v", err)
	}
}

// checkPassHeaders is only worth having while its limits are the contract's own. If founders.bas changes a bound,
// this fails until the tool is changed with it.
func TestPassLimitsAreTheContracts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "contracts", "founders.bas"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"STRLEN(name) == 0 || STRLEN(name) > 64 || STRLEN(descr) > 240 || STRLEN(icon) > 200 || STRLEN(file) > 200",
		"maxsupply == 0 || maxsupply > 100000 || price > 10000000000000 || reserve > maxsupply / 5",
		"share > 10000 || mfee > 1000",
		// v2 refuses DERO attached to the install; simdeploy's install sends no transfer
		"IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("founders.bas InitializePrivate no longer checks %q; update checkPassHeaders to match", want)
		}
	}
}

func TestPassHeadersRefusedBeforeInstall(t *testing.T) {
	saved, savedReserve := *passDescr, *passReserve
	defer func() { *passDescr, *passReserve = saved, savedReserve }()

	*passDescr = strings.Repeat("x", 241)
	if checkPassHeaders() == nil {
		t.Error("a 241-byte description was accepted")
	}
	*passDescr = strings.Repeat("x", 240)
	if err := checkPassHeaders(); err != nil {
		t.Errorf("a 240-byte description was refused: %v", err)
	}
	*passDescr = saved

	// the runbook's own trap: a reserve of 50 cannot install at 20 editions (MX-02)
	savedMax := *passMax
	defer func() { *passMax = savedMax }()
	*passMax, *passReserve = 20, 50
	if checkPassHeaders() == nil {
		t.Error("a reserve of 50 at 20 editions was accepted; founders.bas refuses reserve > maxsupply / 5")
	}
}

// The mainnet runbook's command line is what an operator will paste. Its description has to install.
func TestRunbookPassDescriptionFits(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`-pass-descr "([^"]*)"`).FindSubmatch(b)
	if m == nil {
		t.Fatal("deploy/README.md passes no -pass-descr")
	}
	if n := len(m[1]); n > 240 {
		t.Fatalf("the mainnet runbook's -pass-descr is %d bytes; founders.bas refuses more than 240", n)
	}
}

// The operator's numbers (MT-J16, 2026-09-13) are the defaults, and they install: 1 DERO, 20 editions, 4 reserved.
func TestPassDefaultsAreTheOperatorsNumbers(t *testing.T) {
	if *passPrice != 100000 || *passMax != 20 || *passReserve != 4 || *passShare != 10000 || *passMfee != 250 || *passImage != "" {
		t.Fatalf("defaults price %d max %d reserve %d share %d mfee %d image %q", *passPrice, *passMax, *passReserve, *passShare, *passMfee, *passImage)
	}
	if len(passDescrDefault) != 234 {
		t.Fatalf("the approved description is 234 bytes, the default is %d", len(passDescrDefault))
	}
}

// A changed founders template replaces the recorded hash and keeps no legacy one: a v1 pass must not verify in an app
// that reads v2's keys. Every other template still demotes its previous hash to legacy.
func TestFoundersTemplateKeepsNoLegacy(t *testing.T) {
	h := func(c byte) string { return strings.Repeat(string(c), 64) }
	p := filepath.Join(t.TempDir(), "networks.json")
	rec := map[string]interface{}{"simulator": map[string]interface{}{"daemon": "d", "templates": map[string]interface{}{
		"founders": map[string]interface{}{"sha256": h('a'), "legacy": []string{h('b')}},
		"pool":     map[string]interface{}{"sha256": h('c')},
	}}}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	savedN, savedNet := *networks, *network
	defer func() { *networks, *network = savedN, savedNet }()
	*networks, *network = p, "simulator"
	n := record(map[string]string{"founders": h('d'), "pool": h('e')}, nil, nil, "")
	if f := n.Templates["founders"]; f.Sha256 != h('d') || len(f.Legacy) != 0 {
		t.Fatalf("founders template %+v: the old hashes must go", f)
	}
	if pl := n.Templates["pool"]; pl.Sha256 != h('e') || len(pl.Legacy) != 1 || pl.Legacy[0] != h('c') {
		t.Fatalf("pool template %+v: the old hash must stay as legacy", pl)
	}
	if err := checkHashFlags(map[string]map[string]bool{"founders": {h('a'): true}}, nil); err == nil {
		t.Fatal("-legacy founders:... was accepted")
	}
}
