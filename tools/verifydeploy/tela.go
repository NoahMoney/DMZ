package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"veil/tools/netcfg"
	"veil/tools/verifydeploy/srchash"
)

// TELA. The app is published as one TELA-DOC-1 contract per file and one TELA-INDEX-1 that lists them. Both
// templates are copied here verbatim from vendor/tela (civilware/tela aa892d2, MIT — TELA-LICENSE.txt), because
// vendor/ is not part of the repository a third party clones. TestTelaTemplatesAreVendored holds the copies to
// the vendored files whenever those are present.
//
// Who can change the INDEX (tela_index_1.bas):
//   init 30        STORE("owner", address())             — once, at install
//   address 20-50  LET s = SIGNER(); IF IS_ADDRESS_VALID(s) THEN GOTO 50; RETURN "anon"; RETURN ADDRESS_STRING(s)
//   UpdateCode 10  IF LOAD("owner") == "anon" THEN GOTO 20 (RETURN 1)
//   UpdateCode 16  IF LOAD("owner") == address() THEN GOTO 30 (UPDATE_SC_CODE)
// and no function writes owner again. What SIGNER() returns depends on the install's ring size:
//   vendor/derohe/blockchain/transaction_execute.go 424-443  Extract_signer yields a signer only for a payload
//       with RingSize == 2; otherwise it errors, and 295-299 run the contract anyway with the zero value
//       ("allow anonymous SC transactions ... otherwise SC receives signer as all zeroes").
//   vendor/derohe/dvm/dvm_functions.go 307-310  SIGNER() returns that value; 324-339 IS_ADDRESS_VALID decodes it
//       as a compressed point, and 33 zero bytes do not decode (research/ringprobe.log: ring 16 and 32, valid=0).
// So an INDEX installed at ring 2 stores its installer's address and that key can call UpdateCode — at ring 2,
// since a larger ring makes address() "anon" too — for ever, replacing the whole app. Installed at ring > 2 it
// stores "anon" and UpdateCode refuses every caller for ever. A DOC (tela_doc_1.bas) has no UpdateCode and no
// UPDATE_SC_CODE at all: its content can never change, at any ring size.

//go:embed tela_index_1.bas
var telaIndex1 string

//go:embed tela_doc_1.bas
var telaDoc1 string

const telaIndexFile = "tools/verifydeploy/tela_index_1.bas"

var telaOwnerCites = []cite{
	{telaIndexFile, 21, `STORE("owner", address())`},
	{telaIndexFile, 33, `LET s = SIGNER()`},
	{telaIndexFile, 34, `IF IS_ADDRESS_VALID(s) THEN GOTO 50`},
	{telaIndexFile, 35, `RETURN "anon"`},
	{telaIndexFile, 53, `IF LOAD("owner") == "anon" THEN GOTO 20`},
	{telaIndexFile, 55, `IF LOAD("owner") == address() THEN GOTO 30`},
	{telaIndexFile, 57, `UPDATE_SC_CODE(code)`},
	{"vendor/derohe/blockchain/transaction_execute.go", 430, `RingSize == 2`},
	{"vendor/derohe/dvm/dvm_functions.go", 309, `return true, dvm.State.Chain_inputs.Signer`},
}

type telaStd struct {
	index   map[string]*bfunc // v1.1.0
	upd100  *bfunc            // UpdateCode as it was in v1.0.0
	init100 *bfunc            // init with telaVersion "1.0.0"
	doc     map[string]*bfunc
}

var (
	stdOnce sync.Once
	std     *telaStd
	stdErr  error
)

// telaStandard parses the embedded templates once. v1.0.0 differs from v1.1.0 exactly as tela's own
// createContractVersions (vendor/tela/parse.go 899-910) derives it: UpdateCode takes (code String) only and has no
// line 70, and init stores telaVersion "1.0.0".
func telaStandard() (*telaStd, error) {
	stdOnce.Do(func() {
		ip, err := parseBasic(telaIndex1)
		if err != nil {
			stdErr = fmt.Errorf("embedded TELA-INDEX-1: %v", err)
			return
		}
		dp, err := parseBasic(telaDoc1)
		if err != nil {
			stdErr = fmt.Errorf("embedded TELA-DOC-1: %v", err)
			return
		}
		s := &telaStd{index: ip.Funcs, doc: dp.Funcs}
		u := *ip.Funcs["UpdateCode"]
		u.Header = []string{"Function", "UpdateCode", "(", "code", "String", ")", "Uint64"}
		u.Lines = nil
		for _, l := range ip.Funcs["UpdateCode"].Lines {
			if l.No != "70" {
				u.Lines = append(u.Lines, l)
			}
		}
		s.upd100 = &u
		in := *ip.Funcs["init"]
		in.Lines = nil
		for _, l := range ip.Funcs["init"].Lines {
			t := append([]string{}, l.Toks...)
			for i := range t {
				if t[i] == `"1.1.0"` {
					t[i] = `"1.0.0"`
				}
			}
			in.Lines = append(in.Lines, bline{No: l.No, Toks: t, Src: l.Src})
		}
		s.init100 = &in
		std = s
	})
	return std, stdErr
}

type docRef struct {
	Key  string // DOC1
	N    int
	SCID string
}

type indexInfo struct {
	Version     string
	Problems    []string
	DOCs        []docRef
	Headers     map[string]string
	CallsUpdate bool
}

var indexHeaderKeys = map[string]bool{"var_header_name": true, "var_header_description": true, "var_header_icon": true, "dURL": true, "mods": true, "nameHdr": true, "descrHdr": true, "iconURLHdr": true}
var docHeaderKeys = map[string]bool{"var_header_name": true, "var_header_description": true, "var_header_icon": true, "dURL": true, "docType": true, "subDir": true, "fileCheckC": true, "fileCheckS": true, "nameHdr": true, "descrHdr": true, "iconURLHdr": true}

// storeLiteral reads `STORE("k", "v")`.
func storeLiteral(t []string) (k, v string, ok bool) {
	if len(t) != 6 || !strings.EqualFold(t[0], "STORE") || t[1] != "(" || t[3] != "," || t[5] != ")" {
		return "", "", false
	}
	k, ok1 := unquote(t[2])
	v, ok2 := unquote(t[4])
	return k, v, ok1 && ok2
}

// checkInitPrivate reads a TELA InitializePrivate: the standard's lines 10 and 20, header STOREs of string literals,
// and RETURN 0. It returns the headers it found; anything else is a problem.
func checkInitPrivate(std, got *bfunc, allowed map[string]bool, docs *[]docRef) (map[string]string, []string) {
	var probs []string
	hdr := map[string]string{}
	if strings.Join(got.Header, " ") != strings.Join(std.Header, " ") {
		probs = append(probs, fmt.Sprintf("InitializePrivate is declared %q", strings.Join(got.Header, " ")))
	}
	for i, l := range got.Lines {
		switch {
		case i < 2:
			if i >= len(std.Lines) || l.No != std.Lines[i].No || strings.Join(l.Toks, " ") != strings.Join(std.Lines[i].Toks, " ") {
				probs = append(probs, fmt.Sprintf("InitializePrivate line %s is %q, not the standard's", l.No, strings.Join(l.Toks, " ")))
			}
		case i == len(got.Lines)-1:
			if strings.Join(l.Toks, " ") != "RETURN 0" {
				probs = append(probs, fmt.Sprintf("InitializePrivate ends with %q, not RETURN 0", strings.Join(l.Toks, " ")))
			}
		default:
			k, val, ok := storeLiteral(l.Toks)
			if !ok {
				probs = append(probs, fmt.Sprintf("InitializePrivate line %s is %q: only header STOREs belong there", l.No, strings.Join(l.Toks, " ")))
				continue
			}
			if docs != nil && strings.HasPrefix(k, "DOC") {
				n, err := strconv.Atoi(k[3:])
				if err == nil && n > 0 {
					*docs = append(*docs, docRef{Key: k, N: n, SCID: val})
					continue
				}
			}
			if !allowed[k] {
				probs = append(probs, fmt.Sprintf("InitializePrivate stores %q, which is not a TELA header", k))
				continue
			}
			hdr[k] = val
		}
	}
	return hdr, probs
}

func analyseIndex(code string) indexInfo {
	var ii indexInfo
	s, err := telaStandard()
	if err != nil {
		ii.Problems = append(ii.Problems, err.Error())
		return ii
	}
	p, err := parseBasic(code)
	if err != nil {
		ii.Problems = append(ii.Problems, "does not parse: "+err.Error())
		return ii
	}
	ii.CallsUpdate = p.calls("UPDATE_SC_CODE")
	for _, name := range p.Order {
		if _, ok := s.index[name]; !ok {
			ii.Problems = append(ii.Problems, "function "+name+" is not part of TELA-INDEX-1 (a TELA-MOD, or not TELA)")
		}
	}
	for _, name := range []string{"InitializePrivate", "init", "address", "Rate", "UpdateCode"} {
		if p.Funcs[name] == nil {
			ii.Problems = append(ii.Problems, "no function "+name)
		}
	}
	if len(ii.Problems) > 0 {
		return ii
	}
	switch {
	case sameLines(s.index["UpdateCode"], p.Funcs["UpdateCode"]) == "" && sameLines(s.index["init"], p.Funcs["init"]) == "":
		ii.Version = "1.1.0"
	case sameLines(s.upd100, p.Funcs["UpdateCode"]) == "" && sameLines(s.init100, p.Funcs["init"]) == "":
		ii.Version = "1.0.0"
	default:
		ii.Problems = append(ii.Problems, "UpdateCode: "+sameLines(s.index["UpdateCode"], p.Funcs["UpdateCode"]))
		if d := sameLines(s.index["init"], p.Funcs["init"]); d != "" {
			ii.Problems = append(ii.Problems, "init: "+d)
		}
	}
	for _, name := range []string{"address", "Rate"} {
		if d := sameLines(s.index[name], p.Funcs[name]); d != "" {
			ii.Problems = append(ii.Problems, name+": "+d)
		}
	}
	hdr, probs := checkInitPrivate(s.index["InitializePrivate"], p.Funcs["InitializePrivate"], indexHeaderKeys, &ii.DOCs)
	ii.Headers = hdr
	ii.Problems = append(ii.Problems, probs...)
	if m := hdr["mods"]; m != "" {
		ii.Problems = append(ii.Problems, fmt.Sprintf("TELA-MODs %q are installed: their functions are not analysed", m))
	}
	// tela orders DOCs by sorting the "DOC<n>" keys as strings (vendor/tela/parse.go parseINDEXForDOCs); only DOC1
	// matters (the entrypoint), so this report orders them by number.
	sort.Slice(ii.DOCs, func(i, j int) bool { return ii.DOCs[i].N < ii.DOCs[j].N })
	return ii
}

type docInfo struct {
	Problems []string
	Name     string // the stored name header, e.g. app.js.gz
	Path     string // subDir/name without the compression suffix: the bundle path it serves
	DocType  string
	DURL     string
	Stored   string // the comment block as tela reads it
	Content  []byte // what a TELA host serves
}

// docContent mirrors tela's parseDocCode (vendor/tela/tela.go 285-297): from the first "/*" to the end, less a final
// "*/", trimmed. A host serves exactly this, gunzipped when the name ends in .gz (tela Decompress).
func docContent(code string) (string, error) {
	start, end := strings.Index(code, "/*"), strings.Index(code, "*/")
	if start == -1 || end == -1 {
		return "", fmt.Errorf("no /* */ content block")
	}
	return strings.TrimSpace(strings.TrimSuffix(code[start+2:], "*/")), nil
}

func gunzipB64(s string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("gzip: %v", err)
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func analyseDoc(c *contract) docInfo {
	var d docInfo
	s, err := telaStandard()
	if err != nil {
		d.Problems = append(d.Problems, err.Error())
		return d
	}
	p, err := parseBasic(c.Code)
	if err != nil {
		d.Problems = append(d.Problems, "does not parse: "+err.Error())
		return d
	}
	if p.calls("UPDATE_SC_CODE") {
		d.Problems = append(d.Problems, "calls UPDATE_SC_CODE: a TELA-DOC-1 never does")
	}
	for _, name := range p.Order {
		if _, ok := s.doc[name]; !ok {
			d.Problems = append(d.Problems, "function "+name+" is not part of TELA-DOC-1")
		}
	}
	for _, name := range []string{"init", "address", "Rate"} {
		if p.Funcs[name] == nil {
			d.Problems = append(d.Problems, "no function "+name)
		} else if diff := sameLines(s.doc[name], p.Funcs[name]); diff != "" {
			d.Problems = append(d.Problems, name+": "+diff)
		}
	}
	if p.Funcs["InitializePrivate"] == nil {
		d.Problems = append(d.Problems, "no function InitializePrivate")
	} else {
		_, probs := checkInitPrivate(s.doc["InitializePrivate"], p.Funcs["InitializePrivate"], docHeaderKeys, nil)
		d.Problems = append(d.Problems, probs...)
	}
	if !p.HasTail {
		d.Problems = append(d.Problems, "no content block after the functions")
	}
	// Headers as a host reads them: from storage, the V2 names first (tela GetDOCInfo).
	get := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := c.Vars.str(k); ok {
				return s
			}
		}
		return ""
	}
	d.Name, d.DocType, d.DURL = get("var_header_name", "nameHdr"), get("docType"), get("dURL")
	sub := get("subDir")
	name := strings.TrimSuffix(d.Name, ".gz")
	d.Path = name
	if sub != "" {
		d.Path = path.Join(sub, name)
	}
	stored, err := docContent(c.Code)
	if err != nil {
		d.Problems = append(d.Problems, err.Error())
		return d
	}
	d.Stored = stored
	if strings.HasSuffix(d.Name, ".gz") {
		d.Content, err = gunzipB64(stored)
		if err != nil {
			d.Problems = append(d.Problems, "content does not decompress: "+err.Error())
		}
	} else {
		d.Content = []byte(stored)
	}
	return d
}

type servedDoc struct {
	ref  docRef
	info docInfo
}

func (v *verifier) checkTela(extras []target) {
	idx := v.rec.Tela.Index
	for _, e := range extras {
		if e.Kind == "tela" {
			if idx != "" && idx != e.SCID {
				v.r.add("tela", "index", e.SCID, sInfo, "-extra tela= replaces the record's INDEX %s for this run", short(idx))
			}
			idx = e.SCID
		}
	}
	if idx == "" {
		v.r.add("tela", "index", "", sInfo, "the record names no TELA INDEX on %s (tela.index is empty)", v.network)
		return
	}
	c, err := v.n.getSC(idx)
	if err != nil {
		v.r.add("tela", "index", idx, sFail, "DERO.GetSC: %v", err)
		return
	}
	ii := analyseIndex(c.Code)
	owner, _ := c.Vars.str("owner")
	commit := c.Vars.num("commit")
	if len(ii.Problems) > 0 {
		v.r.add("tela", "index", idx, sFail, "not the standard TELA-INDEX-1, so its update rule cannot be read from the standard:\n%s", strings.Join(ii.Problems, "\n"))
	} else {
		v.r.add("tela", "index", idx, sPass, "code is the standard TELA-INDEX-1 v%s, token for token (%s); %d DOCs; commit %s (UpdateCode has replaced the code %s times)", ii.Version, telaIndexFile, len(ii.DOCs), commit, commit)
	}
	v.r.Roles = append(v.r.Roles, v.telaOwnerRow(idx, owner, ii))
	v.checkIndexState(idx, c, owner)

	// every DOC the INDEX lists
	var served []servedDoc
	byPath := map[string][]string{}
	durl := ii.Headers["dURL"]
	docOK := 0
	for _, ref := range ii.DOCs {
		dc, err := v.n.getSC(ref.SCID)
		if err != nil {
			v.r.add("tela", ref.Key, ref.SCID, sFail, "DERO.GetSC: %v", err)
			continue
		}
		di := analyseDoc(dc)
		if len(di.Problems) > 0 {
			v.r.add("tela", ref.Key+" "+di.Path, ref.SCID, sFail, "not a standard TELA-DOC-1:\n%s", strings.Join(di.Problems, "\n"))
		} else {
			docOK++
		}
		if durl != "" && di.DURL != durl {
			v.r.add("tela", ref.Key+" "+di.Path, ref.SCID, sWarn, "dURL %q differs from the INDEX's %q", di.DURL, durl)
		}
		served = append(served, servedDoc{ref, di})
		byPath[di.Path] = append(byPath[di.Path], ref.Key)
	}
	if len(ii.DOCs) > 0 && docOK == len(ii.DOCs) {
		v.r.add("tela", "docs", "", sPass, "all %d DOCs are the standard TELA-DOC-1, which has no UpdateCode and no UPDATE_SC_CODE: no key can change what they serve", docOK)
	}
	for p, keys := range byPath {
		if len(keys) > 1 {
			v.r.add("tela", "docs", "", sFail, "%s is served by %d DOCs (%s): a host keeps one of them", p, len(keys), strings.Join(keys, ", "))
		}
	}
	if len(served) > 0 && served[0].ref.Key == "DOC1" && served[0].info.Path != "index.html" {
		v.r.add("tela", "DOC1", served[0].ref.SCID, sFail, "DOC1 is the entrypoint a host opens, and it serves %q, not index.html", served[0].info.Path)
	} else if len(served) > 0 && served[0].ref.Key == "DOC1" {
		v.r.add("tela", "DOC1", served[0].ref.SCID, sPass, "the entrypoint serves index.html")
	}
	v.checkServedAgainstRecord(served)
	v.checkServedAgainstBundle(served)
	v.checkTelaRecord(idx, served)
}

// checkIndexState reads what the INDEX records about its own history. init (tela_index_1.bas 23-25) stores commit 0,
// hash = HEX(TXID()) — the install's txid, which is the SCID — and the uint64 key 0 = the same; each UpdateCode
// (58-60) bumps commit and rewrites hash. An INDEX whose owner is "anon" can never run UpdateCode, so it must read
// exactly commit 0 and hash = its own SCID; anything else means the chain holds a history the code cannot produce.
func (v *verifier) checkIndexState(idx string, c *contract, owner string) {
	commit, okc := c.Vars.u("commit")
	hash, _ := c.Vars.str("hash")
	first := ""
	if x, ok := c.U64["0"]; ok && !x.num {
		if b, err := hex.DecodeString(x.text); err == nil {
			first = string(b)
		}
	}
	switch {
	case owner == "anon" && okc && commit == 0 && hash == idx && first == idx:
		v.r.add("tela", "index state", idx, sPass, "owner %q (DERO.GetSC hex %s), commit 0, hash and commit-0 hash both equal to its own SCID: the code is the code installed, and UpdateCode can never replace it", owner, hexOf([]byte(owner)))
	case owner == "anon":
		v.r.add("tela", "index state", idx, sFail, "owner \"anon\" but commit %s, hash %q, commit-0 hash %q: an INDEX nobody can update must read commit 0 and hash = its SCID %s", c.Vars.num("commit"), hash, first, idx)
	default:
		msg := fmt.Sprintf("commit %s, hash %s", c.Vars.num("commit"), hash)
		if okc && commit > 0 {
			msg += fmt.Sprintf(": UpdateCode has replaced this INDEX's code %d times, and default TELA hosts refuse to serve an INDEX whose hash is not its SCID", commit)
		} else if hash == idx {
			msg += " = its SCID: not updated yet"
		}
		v.r.add("tela", "index state", idx, sInfo, "%s", msg)
	}
}

func (v *verifier) telaOwnerRow(idx, owner string, ii indexInfo) roleRow {
	row := roleRow{Contract: "tela index", SCID: idx, Kind: "tela", Role: "owner", ID: "tela.owner", Keys: []string{"owner"}, Policy: pRevoke, Value: owner, Cites: compactCites(telaOwnerCites)}
	_, isAddr := rawFromAddr(owner)
	switch {
	case len(ii.Problems) > 0 && !ii.CallsUpdate:
		row.State = "the installed code is not the standard, but contains no UPDATE_SC_CODE: it cannot change"
	case len(ii.Problems) > 0:
		row.Live, row.State = true, "the installed code is not the standard and can replace itself (UPDATE_SC_CODE): who may do so is not analysed"
	case owner == "anon":
		row.State = "installed at ring size > 2: SIGNER() was 33 zero bytes, address() stored \"anon\", and UpdateCode refuses every caller at line 10. The INDEX can never change"
	case isAddr:
		row.Live, row.State = true, "installed at ring size 2: this address can call UpdateCode (at ring 2) for ever and replace the whole app; no TELA-INDEX-1 function changes or clears owner, so destroying the key is the only revocation, and the chain cannot show that"
	default:
		row.Live, row.State = true, fmt.Sprintf("owner %q is neither \"anon\" nor an address: UpdateCode's gate is not understood", owner)
	}
	row.Status = sPass
	if row.Live {
		row.Status = sFail
		if why, ok := v.allow["tela.owner"]; ok {
			row.Status, row.Reason = sAllowed, why
		}
	}
	return row
}

// checkServedAgainstRecord needs no bundle: what the app trusts is in two files it serves. config.js names the
// contracts and the template hashes the app verifies every market against; templates/<kind>.bas are the sources
// the app shows. Both must be the record's, which the record checks tie to this repository.
func (v *verifier) checkServedAgainstRecord(served []servedDoc) {
	want := netcfg.ConfigJS(v.network, v.rec, "tools/genconfig -dist")
	for _, s := range served {
		p := s.info.Path
		switch {
		case p == "config.js":
			if d := diffAfterFirstLine([]byte(want), s.info.Content); d != "" {
				v.r.add("tela", s.ref.Key+" config.js", s.ref.SCID, sFail, "the config.js the app runs with is not netcfg.ConfigJS of the record (compared after its first line, which carries the generation time): %s", d)
			} else {
				v.r.add("tela", s.ref.Key+" config.js", s.ref.SCID, sPass, "equals netcfg.ConfigJS(%s record) after its first line: the app trusts exactly the record's contracts and template hashes", v.network)
			}
		case strings.HasPrefix(p, "templates/") && strings.HasSuffix(p, ".bas"):
			k := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".bas")
			t, ok := v.rec.Templates[k]
			h := srchash.Sum(string(s.info.Content))
			switch {
			case !ok:
				v.r.add("tela", s.ref.Key+" "+p, s.ref.SCID, sWarn, "serves a %s template the record does not list", k)
			case h != t.Sha256:
				v.r.add("tela", s.ref.Key+" "+p, s.ref.SCID, sFail, "serves sha256 %s; the record's current %s template is %s", h, k, t.Sha256)
			default:
				v.r.add("tela", s.ref.Key+" "+p, s.ref.SCID, sPass, "serves the record's current %s template (sha256 %s)", k, h)
			}
		case p == "manifest.json":
			v.checkManifest(s, served)
		}
	}
}

// checkManifest reads the manifest.json genconfig wrote into the bundle, as served: every file it lists must be
// served with that sha256, and it must name the record's contracts.
func (v *verifier) checkManifest(m servedDoc, served []servedDoc) {
	var mf struct {
		Factory  string `json:"factory"`
		Social   string `json:"social"`
		Founders string `json:"founders"`
		Vault    string `json:"vault"`
		Htlc     string `json:"htlc"`
		Swap     string `json:"swap"`
		Board    string `json:"board"`
		Files    map[string]struct {
			Sha256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(m.info.Content, &mf); err != nil {
		v.r.add("tela", m.ref.Key+" manifest.json", m.ref.SCID, sFail, "does not parse: %v", err)
		return
	}
	var bad []string
	for _, x := range []struct{ k, got, want string }{{"factory", mf.Factory, v.rec.Factory}, {"social", mf.Social, v.rec.Social}, {"founders", mf.Founders, v.rec.Founders}, {"vault", mf.Vault, v.rec.Vault},
		{"htlc", mf.Htlc, v.rec.Htlc}, {"swap", mf.Swap, v.rec.Swap}, {"board", mf.Board, v.rec.Board}} {
		if x.got != x.want {
			bad = append(bad, fmt.Sprintf("%s %q, the record %q", x.k, x.got, x.want))
		}
	}
	have := map[string]string{}
	for _, s := range served {
		have[s.info.Path] = srchash.Sum(string(s.info.Content))
	}
	for p, e := range mf.Files {
		switch h, ok := have[p]; {
		case !ok:
			bad = append(bad, p+" is listed but not served")
		case h != e.Sha256:
			bad = append(bad, fmt.Sprintf("%s is served as sha256 %s, listed as %s", p, short(h), short(e.Sha256)))
		}
	}
	for p := range have {
		if _, ok := mf.Files[p]; !ok && p != "manifest.json" {
			bad = append(bad, p+" is served but not listed")
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		v.r.add("tela", m.ref.Key+" manifest.json", m.ref.SCID, sFail, "disagrees with what is served or recorded:\n%s", strings.Join(bad, "\n"))
		return
	}
	v.r.add("tela", m.ref.Key+" manifest.json", m.ref.SCID, sPass, "lists exactly the %d other served files with their sha256, and the record's contracts", len(mf.Files))
}

// checkTelaRecord compares the INDEX with a telainstall deployment record (path -> DOC scid), if one was given.
func (v *verifier) checkTelaRecord(idx string, served []servedDoc) {
	if v.opts.telaRecord == "" {
		return
	}
	b, err := os.ReadFile(v.opts.telaRecord)
	if err != nil {
		v.r.add("tela", "tela-record", "", sFail, "%v", err)
		return
	}
	var rec struct {
		Index string            `json:"index"`
		Docs  map[string]string `json:"docs"`
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		v.r.add("tela", "tela-record", "", sFail, "%s: %v", v.opts.telaRecord, err)
		return
	}
	var bad []string
	if rec.Index != idx {
		bad = append(bad, fmt.Sprintf("the record's index is %s, this INDEX is %s", rec.Index, idx))
	}
	on := map[string]string{}
	for _, s := range served {
		on[s.info.Path] = s.ref.SCID
	}
	for p, scid := range rec.Docs {
		if on[p] != scid {
			bad = append(bad, fmt.Sprintf("%s: recorded DOC %s, the INDEX serves %q", p, short(scid), short(on[p])))
		}
	}
	for p := range on {
		if _, ok := rec.Docs[p]; !ok {
			bad = append(bad, p+": served by the INDEX, absent from the record")
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		v.r.add("tela", "tela-record", idx, sFail, "the INDEX's DOC list is not the published one (%s):\n%s", v.opts.telaRecord, strings.Join(bad, "\n"))
		return
	}
	v.r.add("tela", "tela-record", idx, sPass, "the INDEX's %d DOCs are exactly the ones %s publishes", len(on), v.opts.telaRecord)
}
