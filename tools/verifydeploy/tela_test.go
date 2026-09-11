package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	rpc "veil/tools/simrpc"
)

// The embedded templates are the vendored ones, byte for byte, whenever vendor/tela is present.
func TestTelaTemplatesAreVendored(t *testing.T) {
	for _, x := range []struct{ file, embedded string }{
		{"vendor/tela/TELA-INDEX-1/TELA-INDEX-1.bas", telaIndex1},
		{"vendor/tela/TELA-DOC-1/TELA-DOC-1.bas", telaDoc1},
	} {
		b, err := os.ReadFile(filepath.Join(repoRoot, x.file))
		if err != nil {
			t.Logf("%s not present (vendor/ is not tracked): not compared", x.file)
			continue
		}
		if string(b) != x.embedded {
			t.Fatalf("the embedded copy of %s differs from the vendored file", x.file)
		}
	}
}

func TestAnalyseIndex(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, v := range []string{"1.1.0", "1.0.0"} {
		ii := analyseIndex(makeIndex(t, []string{a, b}, v))
		if len(ii.Problems) > 0 || ii.Version != v || len(ii.DOCs) != 2 || ii.DOCs[0].SCID != a || ii.DOCs[1].Key != "DOC2" || !ii.CallsUpdate {
			t.Fatalf("standard %s INDEX: %+v", v, ii)
		}
	}
	// formatting is not identity: the same tokens spaced differently are still the standard
	spaced := strings.ReplaceAll(makeIndex(t, []string{a}, "1.1.0"), `STORE("owner", address())`, `STORE( "owner",address() )`)
	if ii := analyseIndex(spaced); len(ii.Problems) > 0 {
		t.Fatalf("re-spaced INDEX refused: %v", ii.Problems)
	}
	for name, tamper := range map[string]func(string) string{
		"UpdateCode without the owner check": func(s string) string {
			return strings.Replace(s, `16 IF LOAD("owner") == address() THEN GOTO 30`, `16 GOTO 30`, 1)
		},
		"an extra function": func(s string) string { return s + "\nFunction Withdraw() Uint64\n10 RETURN 0\nEnd Function\n" },
		"a private owner writer": func(s string) string {
			return strings.Replace(s, `85 STORE("likes", 0)`, `85 STORE("owner", "anon")`, 1)
		},
		"a store in InitializePrivate": func(s string) string {
			return strings.Replace(s, `33 STORE("dURL", "dmz.tela")`, `33 STORE("owner", "x")`, 1)
		},
		"mods": func(s string) string {
			return strings.Replace(s, `34 STORE("mods", "")`, `34 STORE("mods", "txdwd")`, 1)
		},
	} {
		if ii := analyseIndex(tamper(makeIndex(t, []string{a}, "1.1.0"))); len(ii.Problems) == 0 {
			t.Errorf("%s: not detected", name)
		}
	}
}

// The analyser must accept the code the tela library actually writes (ParseHeaders re-prints the template), not
// only the template's own text. testdata holds two INDEXes read back with DERO.GetSC from the simulator on
// 2026-09-11: f807075f… (8 DOCs, installed by tools/telainstall at ring 16) and f4a2b4d1… (34 DOCs, ring 2).
func TestAnalyseIndexFromChain(t *testing.T) {
	for file, docs := range map[string]int{"testdata/tela_index_ring16.bas": 8, "testdata/tela_index_ring2.bas": 34} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		ii := analyseIndex(string(b))
		if len(ii.Problems) > 0 || ii.Version != "1.1.0" || len(ii.DOCs) != docs || ii.Headers["dURL"] != "dmz.tela" {
			t.Fatalf("%s: %+v", file, ii)
		}
		for i, d := range ii.DOCs {
			if d.N != i+1 || !is64hex(d.SCID) {
				t.Fatalf("%s: DOC %d is %+v", file, i+1, d)
			}
		}
	}
}

func TestTelaOwnerRow(t *testing.T) {
	v := &verifier{allow: map[string]string{}}
	std := indexInfo{Version: "1.1.0", CallsUpdate: true}
	if r := v.telaOwnerRow("i", "anon", std); r.Live || r.Status != sPass {
		t.Fatalf("anon: %+v", r)
	}
	if r := v.telaOwnerRow("i", "dero1qy2un79azg3lu39hjrfmfveyv39ugesw86l9r5x06s42txspapr52qq4mc5zm", std); !r.Live || r.Status != sFail {
		t.Fatalf("ring-2 owner: %+v", r)
	}
	if r := v.telaOwnerRow("i", "anon", indexInfo{Problems: []string{"x"}, CallsUpdate: true}); !r.Live || r.Status != sFail {
		t.Fatalf("non-standard self-updating code with owner anon must still fail: %+v", r)
	}
	v.allow["tela.owner"] = "testnet"
	if r := v.telaOwnerRow("i", "dero1qy2un79azg3lu39hjrfmfveyv39ugesw86l9r5x06s42txspapr52qq4mc5zm", std); r.Status != sAllowed {
		t.Fatalf("allowed: %+v", r)
	}
}

func TestAnalyseDoc(t *testing.T) {
	code, vs := makeDoc(t, "app.js", "templates", []byte("console.log('*/ is fine inside gzip')\n"))
	d := analyseDoc(&contract{Code: code, Vars: vs})
	if len(d.Problems) > 0 || d.Path != "templates/app.js" || string(d.Content) != "console.log('*/ is fine inside gzip')\n" {
		t.Fatalf("doc: %+v", d)
	}
	bad := strings.Replace(code, "Function Rate(r Uint64) Uint64", "Function UpdateCode(code String) Uint64\n10 UPDATE_SC_CODE(code)\n20 RETURN 0\nEnd Function\n\nFunction Rate(r Uint64) Uint64", 1)
	if d := analyseDoc(&contract{Code: bad, Vars: vs}); len(d.Problems) == 0 {
		t.Fatal("a DOC that can replace its code was accepted")
	}
}

// Raw keys render as the DVM writes them, the same spelling tools/simrpc produces.
func TestAddrRoundTrip(t *testing.T) {
	for _, a := range []string{
		"deto1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgmat5s8",
		"dero1qyzj2ldw7cjpprhf84ljmxdw54w72jekfkwt0nqsyyzgatttg37d2qgrs8974",
	} {
		raw, ok := rawFromAddr(a)
		if !ok {
			t.Fatalf("%s did not decode", a)
		}
		got, _ := addrFromRaw(raw)
		if got != rpc.DVMAddress(a) {
			t.Fatalf("addrFromRaw(%s) = %s, simrpc says %s", a, got, rpc.DVMAddress(a))
		}
	}
	if s := showKey(make([]byte, 33)); !strings.Contains(s, "zero") {
		t.Fatalf("zero key shown as %q", s)
	}
}

func TestTokenize(t *testing.T) {
	got, err := tokenize(`30 IF LOAD("owner")==address() THEN GOTO 50 // note "x"`)
	want := []string{"30", "IF", "LOAD", "(", `"owner"`, ")", "==", "address", "(", ")", "THEN", "GOTO", "50"}
	if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestDiffHelpers(t *testing.T) {
	if d := diffBytes([]byte("abc\ndef\n"), []byte("abc\ndXf\n")); !strings.Contains(d, "byte 5 (line 2)") {
		t.Fatalf("diffBytes: %s", d)
	}
	if d := diffAfterFirstLine([]byte("// at 10:00\na\nb\n"), []byte("// at 11:00\na\nb\n")); d != "" {
		t.Fatalf("first line should be ignored: %s", d)
	}
	if d := diffAfterFirstLine([]byte("x\na\nb\n"), []byte("x\na\nc\n")); !strings.Contains(d, "line 3") {
		t.Fatalf("diffAfterFirstLine: %s", d)
	}
}
