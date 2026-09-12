package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veil/tools/netcfg"
	rpcpkg "veil/tools/simrpc"
	"veil/tools/verifydeploy/srchash"
)

// fakeDaemon answers DERO.GetInfo and DERO.GetSC from a map, the way derod does: string variables hex-encoded.
type fakeSC struct {
	code string
	vars vars
	u64  map[string]value
}

func fakeDaemon(t *testing.T, scs map[string]fakeSC) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result interface{}
		switch req.Method {
		case "DERO.GetInfo":
			result = map[string]interface{}{"testnet": true, "network": "Simulator", "height": 900, "stableheight": 892}
		case "DERO.GetSC":
			var p struct {
				SCID string `json:"scid"`
			}
			_ = json.Unmarshal(req.Params, &p)
			sc, ok := scs[p.SCID]
			if !ok {
				result = map[string]interface{}{"code": "", "balance": 0, "status": "OK"}
				break
			}
			enc := func(m map[string]value) map[string]json.RawMessage {
				out := map[string]json.RawMessage{}
				for k, v := range m {
					if v.num {
						out[k] = json.RawMessage(v.text)
					} else {
						b, _ := json.Marshal(v.text)
						out[k] = b
					}
				}
				return out
			}
			result = map[string]interface{}{"code": sc.code, "stringkeys": enc(sc.vars), "uint64keys": enc(sc.u64), "balances": map[string]uint64{}}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": "1", "result": result})
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func canonOf(t *testing.T, kind string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "contracts", kind+".bas"))
	if err != nil {
		t.Fatal(err)
	}
	return srchash.Canon(string(b))
}

const fScid = "f000000000000000000000000000000000000000000000000000000000000001"

func runVerifier(t *testing.T, scs map[string]fakeSC, rec *netcfg.Network, allow map[string]string, extras []target) *report {
	t.Helper()
	v := &verifier{n: node{fakeDaemon(t, scs)}, rec: rec, network: "simulator", contracts: filepath.Join(repoRoot, "contracts"),
		allow: allow, hist: newHistory(repoRoot, false), opts: options{registry: true}}
	return v.run(extras)
}

func failures(r *report) []string {
	var out []string
	for _, c := range r.Checks {
		if c.Status == sFail {
			out = append(out, c.Section+" "+c.Subject+": "+c.Message)
		}
	}
	for _, x := range r.Roles {
		if x.Status == sFail {
			out = append(out, "role "+x.ID+": "+x.State)
		}
	}
	return out
}

// factoryVars is a factory's state: "v2" as factory.bas v2 leaves it (no owner key), or with an older v1 factory's
// "owner" or "renounced" key added.
func factoryVars(state string) vars {
	v := vars{"treasury": sv(key(2)), "tstr": ss("dero1x"), "regfee": nv(10000), "initd": nv(1), "pc": nv(0), "lc": nv(0), "nc": nv(0), "kind": ss("factory"), "v": nv(2)}
	switch state {
	case "owner":
		v["owner"], v["v"] = sv(key(1)), nv(1)
	case "renounced":
		v["renounced"], v["v"] = nv(850), nv(1)
	}
	return v
}

// The whole pipeline against a fake GetSC: a v2 factory passes with no -allow and no owner row; the same code read
// with an older factory's live owner key fails on exactly that role; with its renounced key it passes.
func TestVerifierFactoryOwner(t *testing.T) {
	rec := &netcfg.Network{Factory: fScid, Templates: map[string]netcfg.Template{}}
	code := canonOf(t, "factory")

	r := runVerifier(t, map[string]fakeSC{fScid: {code: code, vars: factoryVars("v2")}}, rec, nil, nil)
	if r.failed() {
		t.Fatalf("a v2 factory failed: %v", failures(r))
	}
	for _, x := range r.Roles {
		if x.ID == "factory.owner" {
			t.Fatalf("a v2 factory shows an owner role: %+v", x)
		}
	}
	if r.Checks[len(r.Checks)-1].Section != "tela" { // sanity: every section ran
		t.Fatalf("unexpected last check %+v", r.Checks[len(r.Checks)-1])
	}

	r = runVerifier(t, map[string]fakeSC{fScid: {code: code, vars: factoryVars("owner")}}, rec, nil, nil)
	fs := failures(r)
	if !r.failed() || len(fs) != 1 || !strings.Contains(fs[0], "role factory.owner") {
		t.Fatalf("live owner: want exactly the owner to fail, got %v", fs)
	}

	r = runVerifier(t, map[string]fakeSC{fScid: {code: code, vars: factoryVars("renounced")}}, rec, nil, nil)
	if r.failed() {
		t.Fatalf("renounced factory failed: %v", failures(r))
	}
}

// One character of difference in the installed code is a failure that names the line.
func TestVerifierOneCharacter(t *testing.T) {
	rec := &netcfg.Network{Factory: fScid, Templates: map[string]netcfg.Template{}}
	code := strings.Replace(canonOf(t, "factory"), `STORE("kind", "factory")`, `STORE("kind", "factorY")`, 1)
	r := runVerifier(t, map[string]fakeSC{fScid: {code: code, vars: factoryVars("v2")}}, rec, nil, nil)
	var msg string
	for _, c := range r.Checks {
		if c.Section == "identity" && c.Status == sFail {
			msg = c.Message
		}
	}
	if !strings.Contains(msg, `source: 30 STORE("kind", "factory")`) || !strings.Contains(msg, `chain:  30 STORE("kind", "factorY")`) || !strings.Contains(msg, "contracts/factory.bas:") {
		t.Fatalf("identity failure does not say what differs:\n%s", msg)
	}
	for _, x := range r.Roles {
		if !strings.Contains(x.State, "not authoritative") {
			t.Fatalf("roles read from differing code are not flagged: %+v", x)
		}
	}
}

// A registry slot whose code matches no template is named; one that matches is not.
func TestVerifierRegistry(t *testing.T) {
	pool := canonOf(t, "pool")
	good := "a000000000000000000000000000000000000000000000000000000000000001"
	bad := "b000000000000000000000000000000000000000000000000000000000000002"
	fv := factoryVars("v2")
	fv["pc"] = nv(2)
	fv["pool_0"] = ss(good)
	fv["pool_1"] = ss(bad)
	rec := &netcfg.Network{Factory: fScid, Templates: map[string]netcfg.Template{"pool": {Sha256: srchash.Sum(pool)}}}
	r := runVerifier(t, map[string]fakeSC{fScid: {code: canonOf(t, "factory"), vars: fv}, good: {code: pool, vars: vars{"initd": nv(1)}},
		bad: {code: pool + "Function Drain() Uint64\n10 RETURN 0\nEnd Function\n", vars: vars{"initd": nv(1)}}}, rec, nil, nil)
	var okSeen, badSeen bool
	for _, c := range r.Checks {
		if c.Subject == "pool_0" && c.Status == sPass {
			okSeen = true
		}
		if c.Subject == "pool_1" && c.Status == sFail && strings.Contains(c.Message, "matches no template") {
			badSeen = true
		}
	}
	if !okSeen || !badSeen {
		t.Fatalf("registry checks: %+v", r.Checks)
	}
}

// TELA: an INDEX whose owner is an address fails; one whose owner is "anon", with commit 0 and hash = its SCID,
// passes, and every DOC's content is compared with the bundle.
func TestVerifierTela(t *testing.T) {
	idx := "c000000000000000000000000000000000000000000000000000000000000003"
	doc := "d000000000000000000000000000000000000000000000000000000000000004"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>hi</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	docCode, docVars := makeDoc(t, "index.html", "", []byte("<p>hi</p>\n"))
	indexCode := makeIndex(t, []string{doc}, "1.1.0")
	rec := &netcfg.Network{Templates: map[string]netcfg.Template{}}
	rec.Tela.Index = idx
	for _, tc := range []struct {
		owner    string
		commit   uint64
		hash     string
		wantFail bool
	}{
		{"dero1qy2un79azg3lu39hjrfmfveyv39ugesw86l9r5x06s42txspapr52qq4mc5zm", 0, idx, true},
		{"anon", 0, idx, false},
		{"anon", 1, "e000000000000000000000000000000000000000000000000000000000000005", true},
	} {
		iv := vars{"owner": ss(tc.owner), "commit": nv(tc.commit), "hash": ss(tc.hash), "dURL": ss("dmz.tela"), "telaVersion": ss("1.1.0")}
		v := &verifier{n: node{fakeDaemon(t, map[string]fakeSC{idx: {code: indexCode, vars: iv, u64: map[string]value{"0": ss(idx)}}, doc: {code: docCode, vars: docVars}})},
			rec: rec, network: "simulator", contracts: filepath.Join(repoRoot, "contracts"), hist: newHistory(repoRoot, false), opts: options{dist: dir}}
		r := v.run(nil)
		if r.failed() != tc.wantFail {
			t.Fatalf("owner %q commit %d: failed=%v, want %v: %v", tc.owner, tc.commit, r.failed(), tc.wantFail, failures(r))
		}
	}
}

// makeDoc builds a TELA-DOC-1 the way tela does: headers stored, content gzip+base64 in the comment block.
func makeDoc(t *testing.T, name, sub string, content []byte) (string, vars) {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(content)
	zw.Close()
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	code := strings.NewReplacer("\r\n", "\n", "<nameHdr>", name+".gz", "<descrHdr>", "d", "<iconURLHdr>", "", "<dURL>", "dmz.tela", "<language>", "TELA-HTML-1", "<c>", "c", "<s>", "s").Replace(telaDoc1)
	code = strings.Replace(code, "/*\ndocType code goes in this comment section\n*/", "/*\n"+b64+"\n*/", 1)
	return code, vars{"var_header_name": ss(name + ".gz"), "subDir": ss(sub), "docType": ss("TELA-HTML-1"), "dURL": ss("dmz.tela"), "owner": ss("anon")}
}

func makeIndex(t *testing.T, docs []string, version string) string {
	t.Helper()
	// the vendored template's line endings follow the checkout (CRLF on a Windows clone); tela prints LF
	code := strings.NewReplacer("\r\n", "\n", "<nameHdr>", "DMZ", "<descrHdr>", "d", "<iconURLHdr>", "", "<dURL>", "dmz.tela", "<modTags>", "").Replace(telaIndex1)
	lines := []string{}
	for i, s := range docs {
		lines = append(lines, "4"+string(rune('0'+i))+` STORE("DOC`+string(rune('1'+i))+`", "`+s+`")`)
	}
	code = strings.Replace(code, `40 STORE("DOC1", "<scid>") `+"\n"+`// 41 STORE("DOC2", "<scid>")`+"\n"+`// 42 STORE("DOC3", "<scid>")`, strings.Join(lines, "\n"), 1)
	if version == "1.0.0" {
		code = strings.Replace(code, `"1.1.0"`, `"1.0.0"`, 1)
		code = strings.Replace(code, "Function UpdateCode(code String, mods String) Uint64", "Function UpdateCode(code String) Uint64", 1)
		code = strings.Replace(code, "70 STORE(\"mods\", mods)\n", "", 1)
	}
	return code
}

// Keep the simrpc import honest: the verifier's JSON-RPC path is simrpc.Call.
var _ = rpcpkg.Call
var _ = io.EOF
