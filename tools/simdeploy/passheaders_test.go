package main

import (
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
