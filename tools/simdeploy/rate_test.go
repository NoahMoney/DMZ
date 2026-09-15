package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// simdeploy's rate.bas install names exactly the parameters InitializePrivate declares, in order and with their types,
// so the install cannot be refused at the gas estimate after the contracts before it were paid for. The fee default is
// the one docs/MAINNET_DEPLOYMENT.md and contracts/README.md state.
func TestRateArgsAreTheContracts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "contracts", "rate.bas"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`Function InitializePrivate\(([^)]*)\) Uint64`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("rate.bas declares no InitializePrivate")
	}
	params := strings.Split(m[1], ", ")
	args := rateArgs("deto1qyx7qyvrtrhvaszeej487k2g689fav7h38ay37fja9qf40ycgl0m2qg8ap2y9")
	if len(args) != len(params) {
		t.Fatalf("simdeploy sends %d arguments, InitializePrivate takes %v", len(args), params)
	}
	types := map[string]string{"String": "S", "Uint64": "U"}
	for i, p := range params {
		f := strings.Fields(p)
		if len(f) != 2 || args[i].Name != f[0] || args[i].DataType != types[f[1]] {
			t.Errorf("argument %d: simdeploy sends %s %s, the contract takes %q", i, args[i].Name, args[i].DataType, p)
		}
	}
	if *rateFee != 10000 {
		t.Errorf("-rate-fee defaults to %d; the documented default is 10000 (0.1 DERO)", *rateFee)
	}
}
