package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"veil/tools/verifydeploy/srchash"
)

func sample() *Manifest {
	return &Manifest{
		Schema: schema, Go: "go1.26.2", Commit: "0fba2c6", Dirty: true, DirtyInputs: []string{"contracts/factory.bas"},
		Build:     buildSpec([]string{"reference", "tools/xmrswap"}),
		Binaries:  []Binary{{Name: "xmrswap", Package: "./xmrswap/cmd/xmrswap", GOOS: "linux", GOARCH: "amd64", File: "xmrswap-linux-amd64", Sha256: strings.Repeat("a", 64), Bytes: 9}},
		Absent:    []Absent{{Name: "zecswap", Package: "./zecswap/cmd/zecswap", Reason: "not a main package"}},
		Contracts: []srchash.Contract{{File: "factory.bas", SourceSha256: strings.Repeat("b", 64), CanonSha256: strings.Repeat("c", 64)}},
		Dist:      map[string]map[string]string{"simulator": {"index.html": strings.Repeat("d", 64)}},
	}
}

func TestManifestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "manifest.json")
	m := sample()
	if err := m.Write(p); err != nil {
		t.Fatal(err)
	}
	got, err := ReadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m, got) {
		t.Fatalf("round trip changed the manifest:\n%+v\n%+v", m, got)
	}
	// two writes of one manifest are the same bytes: no time, no path
	p2 := filepath.Join(t.TempDir(), "manifest.json")
	_ = got.Write(p2)
	a, _ := os.ReadFile(p)
	b, _ := os.ReadFile(p2)
	if string(a) != string(b) {
		t.Fatal("writing the same manifest twice gave different bytes")
	}
}

func TestCompare(t *testing.T) {
	want := sample()
	same := sample()
	for _, r := range Compare(want, same) {
		if r.Status == "FAIL" {
			t.Fatalf("identical manifests: %v", r)
		}
	}
	got := sample()
	got.Binaries[0].Sha256 = strings.Repeat("e", 64)
	got.Contracts[0].CanonSha256 = strings.Repeat("f", 64)
	got.Dist["simulator"]["index.html"] = strings.Repeat("0", 64)
	got.Go = "go1.25.0"
	var fails []string
	for _, r := range Compare(want, got) {
		if r.Status == "FAIL" {
			fails = append(fails, r.What)
		}
	}
	joined := strings.Join(fails, "\n")
	for _, s := range []string{"xmrswap-linux-amd64: manifest sha256 aaaa", "contracts/factory.bas: manifest canonical sha256 cccc", "dist/simulator/index.html", "go version"} {
		if !strings.Contains(joined, s) {
			t.Fatalf("Compare did not report %q:\n%s", s, joined)
		}
	}
}

func TestReadManifestRefusesOtherSchemas(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.json")
	_ = os.WriteFile(p, []byte(`{"schema":"something-else"}`), 0o644)
	if _, err := ReadManifest(p); err == nil {
		t.Fatal("a manifest of another schema was accepted")
	}
}

// The contract hashes in a release are the ones verifydeploy compares with the chain: sha256 of reference.Canon.
func TestManifestContractHashesAreVerifydeploys(t *testing.T) {
	cs, err := srchash.Contracts("../../contracts")
	if err != nil || len(cs) == 0 {
		t.Fatalf("contracts: %v", err)
	}
	for _, c := range cs {
		b, _ := os.ReadFile(filepath.Join("../../contracts", c.File))
		if c.CanonSha256 != srchash.ContractSum(string(b)) {
			t.Fatalf("%s", c.File)
		}
	}
}

func TestBinName(t *testing.T) {
	if binName("xmrswap", "windows", "amd64") != "xmrswap-windows-amd64.exe" || binName("xmrswap", "darwin", "arm64") != "xmrswap-darwin-arm64" {
		t.Fatal("binName")
	}
}
