// Command zecsmoke proves, against a REAL zebrad on regtest (tools/regtest/zcash-up.sh), the operations the Zcash
// leg of DMZ needs, and records the JSON shape of every answer tools/zecswap reads.
//
// The node has no wallet, so keys and transactions come from tools/regtest/zfund (librustzcash, run in WSL): a
// throwaway key is paid a coinbase with generatetoaddress, the coinbase is spent once it matures (the maturity is
// measured, not assumed) into a v5 transaction under the node's current consensus branch id that pays a second key
// and a P2SH output, and the second key spends its output again. Along the way it calls exactly the six methods
// zecswap uses — getblockchaininfo, getblockhash, getaddressutxos, getaddresstxids, getrawtransaction (verbose) and
// sendrawtransaction — with the parameters zecswap sends, plus the answers zecswap has to interpret: a duplicate
// broadcast, an unknown txid, a double spend, a mempool transaction read back.
//
// REGTEST ONLY: it refuses a node whose block 0 is not the regtest genesis block. Private keys (the WIF zfund prints
// on keygen) never reach the transcript.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // HASH160 is what a P2SH address commits to
)

const (
	regtestGenesis = "029f11d80ef9765602235e1bc9727e3eb6ba20839319f761fee920d63401e327"
	branchNU63     = "37a5165b"
	zec            = uint64(100_000_000)
)

// expectedHeights is the activation table zcash-up.sh writes; zfund's `params` must print the same.
var expectedHeights = map[string]uint64{
	"5ba81b19": 1, "76b809bb": 1, "2bb40e60": 1, "f5b9230b": 1, "e9ff75a6": 1,
	"c2d6d0b4": 2, "c8e71055": 3, "4dec4df0": 4, "5437f330": 5, "37a5165b": 6,
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type smoke struct {
	url, user, pass string
	distro, zfund   string
	keyWin, keyWSL  string
	hc              *http.Client
	out             io.Writer
	shapes          map[string]interface{}
	errs            map[string]rpcErr
	refs            map[string]interface{} // txids and addresses of this run, for tools/regtest/zecshape
	facts           []string
	fails           int
}

func (s *smoke) logf(f string, a ...interface{}) { fmt.Fprintf(s.out, f+"\n", a...) }

func (s *smoke) check(ok bool, f string, a ...interface{}) bool {
	if ok {
		s.logf("  PASS  "+f, a...)
	} else {
		s.fails++
		s.logf("  FAIL  "+f, a...)
	}
	return ok
}

func (s *smoke) fact(f string, a ...interface{}) {
	x := fmt.Sprintf(f, a...)
	s.facts = append(s.facts, x)
	s.logf("  FACT  %s", x)
}

func short(v interface{}) string {
	b, _ := json.Marshal(v)
	if len(b) > 900 {
		return string(b[:900]) + fmt.Sprintf("...(%d bytes)", len(b))
	}
	return string(b)
}

// call is one JSON-RPC request as zecswap sends it ("jsonrpc": "1.0", basic auth from the cookie).
func (s *smoke) call(method string, params ...interface{}) (interface{}, *rpcErr, int, error) {
	if params == nil {
		params = []interface{}{}
	}
	body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "1.0", "id": "zecsmoke", "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(s.user, s.pass)
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Result interface{} `json:"result"`
		Error  *rpcErr     `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil {
		return nil, nil, resp.StatusCode, fmt.Errorf("HTTP %d, not JSON-RPC: %.200s", resp.StatusCode, raw)
	}
	return env.Result, env.Error, resp.StatusCode, nil
}

func (s *smoke) ok(variant, method string, params ...interface{}) interface{} {
	s.logf("  -> %s %s", method, short(params))
	r, e, code, err := s.call(method, params...)
	switch {
	case err != nil:
		s.check(false, "%s: %v", method, err)
		return nil
	case e != nil:
		s.check(false, "%s: rpc error %d %q (HTTP %d)", method, e.Code, e.Message, code)
		return nil
	}
	s.logf("  <- %s", short(r))
	if variant != "" {
		if _, seen := s.shapes[variant]; !seen {
			s.shapes[variant] = shapeOf(r)
		}
	}
	return r
}

// refuse expects an RPC error and records it (with the HTTP status, which zecswap's client reads).
func (s *smoke) refuse(scenario, method string, params ...interface{}) *rpcErr {
	s.logf("  -> %s %s   (expect a refusal: %s)", method, short(params), scenario)
	r, e, code, err := s.call(method, params...)
	if err != nil {
		s.check(false, "%s: %v", scenario, err)
		return nil
	}
	if e == nil {
		s.logf("  <- ACCEPTED (HTTP %d): %s", code, short(r))
		s.errs[scenario] = rpcErr{Code: 0, Message: "accepted: " + short(r)}
		return nil
	}
	s.logf("  <- error %d %q (HTTP %d)", e.Code, e.Message, code)
	s.errs[scenario] = *e
	return e
}

func shapeOf(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := map[string]interface{}{}
		for k, x := range t {
			out[k] = shapeOf(x)
		}
		return out
	case []interface{}:
		if len(t) == 0 {
			return []interface{}{}
		}
		return []interface{}{shapeOf(t[0])}
	case json.Number:
		return "number"
	case string:
		return "string"
	case bool:
		return "bool"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

func obj(v interface{}) map[string]interface{} { m, _ := v.(map[string]interface{}); return m }

func arr(v interface{}) []interface{} { a, _ := v.([]interface{}); return a }

func num(v interface{}) uint64 {
	n, ok := v.(json.Number)
	if !ok {
		return 0
	}
	var x uint64
	fmt.Sscan(n.String(), &x)
	return x
}

func text(v interface{}) string { t, _ := v.(string); return t }

// zf runs zfund inside WSL and decodes its one-line JSON answer. "wif" is dropped before anything is logged.
func (s *smoke) zf(args ...string) (map[string]interface{}, error) {
	cmd := exec.Command("wsl.exe", append([]string{"-d", s.distro, "--exec", s.zfund}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	shown := strings.Join(args, " ")
	if err != nil {
		s.logf("  zfund %s -> %v: %s", shown, err, strings.TrimSpace(stderr.String()))
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	var m map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("zfund output: %v", err)
	}
	logged := map[string]interface{}{}
	for k, v := range m {
		switch k {
		case "wif":
			logged[k] = "<redacted>"
		case "hex":
			logged[k] = fmt.Sprintf("<%d hex chars>", len(text(v)))
		default:
			logged[k] = v
		}
	}
	s.logf("  zfund %s -> %s", shown, short(logged))
	return m, nil
}

func (s *smoke) keygen(name string) string {
	os.Remove(s.keyWin + "/" + name) // a fresh key each run; the old one was throwaway
	m, err := s.zf("keygen", s.keyWSL+"/"+name)
	if err != nil {
		s.check(false, "keygen %s: %v", name, err)
		return ""
	}
	return text(m["address"])
}

func (s *smoke) tip() uint64 { r, _, _, _ := s.call("getblockcount"); return num(r) }

func (s *smoke) generate(n int) {
	t0 := time.Now()
	if _, e, _, err := s.call("generate", n); err != nil || e != nil {
		s.check(false, "generate %d: %v %v", n, err, e)
		return
	}
	s.logf("  .. generate %d in %s, tip %d", n, time.Since(t0).Round(time.Millisecond), s.tip())
}

// utxo finds the output of txid in getaddressutxos for addr.
func (s *smoke) utxos(variant, addr string) []map[string]interface{} {
	r := s.ok(variant, "getaddressutxos", map[string]interface{}{"addresses": []string{addr}})
	var out []map[string]interface{}
	for _, u := range arr(r) {
		out = append(out, obj(u))
	}
	return out
}

// scrubber writes through a replacer: the transcript is tracked, and must not carry a machine-specific path (zfund
// is handed key files by path).
type scrubber struct {
	w io.Writer
	r *strings.Replacer
}

func (s scrubber) Write(p []byte) (int, error) {
	if _, err := io.WriteString(s.w, s.r.Replace(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// repoScrubber replaces the repository root given as C:/..., in each form a path to it takes here — JSON-escaped
// C:\\..., C:\..., C:/..., WSL /mnt/c/..., Git Bash /c/... — with <repo>. Longer forms come first.
func repoScrubber(repo string) *strings.Replacer {
	repo = strings.TrimRight(strings.ReplaceAll(repo, `\`, "/"), "/")
	if len(repo) < 3 || repo[1] != ':' {
		return nil
	}
	drive, rest := strings.ToLower(repo[:1]), repo[2:]
	var pairs []string
	for _, d := range []string{strings.ToUpper(drive), drive} {
		fwd := d + ":" + rest
		back := strings.ReplaceAll(fwd, "/", `\`)
		pairs = append(pairs, strings.ReplaceAll(back, `\`, `\\`), "<repo>", back, "<repo>", fwd, "<repo>")
	}
	pairs = append(pairs, "/mnt/"+drive+rest, "<repo>", "/"+drive+rest, "<repo>")
	return strings.NewReplacer(pairs...)
}

// --- base58check and HASH160, for a P2SH address the smoke makes itself ---

const b58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func b58check(payload []byte) string {
	a := sha256.Sum256(payload)
	b := sha256.Sum256(a[:])
	data := append(append([]byte{}, payload...), b[:4]...)
	n := new(big.Int).SetBytes(data)
	var out []byte
	mod, base := new(big.Int), big.NewInt(58)
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, b58[mod.Int64()])
	}
	for _, c := range data {
		if c != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func hash160(b []byte) []byte {
	sh := sha256.Sum256(b)
	r := ripemd160.New()
	r.Write(sh[:])
	return r.Sum(nil)
}

func main() {
	rpcURL := flag.String("rpc", "http://127.0.0.1:28232/", "zebrad RPC")
	cookie := flag.String("cookie", "", "zebrad's .cookie file")
	distro := flag.String("distro", "Ubuntu-24.04", "WSL distro zfund runs in")
	zfund := flag.String("zfund", "", "zfund's path inside WSL")
	keyWin := flag.String("keydir", "", "throwaway key directory (this side)")
	keyWSL := flag.String("keydir-wsl", "", "the same directory as WSL sees it")
	logPath := flag.String("log", "", "also write the transcript here")
	shapesPath := flag.String("shapes", "", "write the real response shapes and refusals here (JSON)")
	repo := flag.String("repo", "", "the repository root (C:/...); the transcript writes it as <repo> in every form")
	flag.Parse()

	c, err := os.ReadFile(*cookie)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cookie:", err)
		os.Exit(2)
	}
	up := strings.SplitN(strings.TrimSpace(string(c)), ":", 2)
	var out io.Writer = os.Stdout
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer f.Close()
		out = io.MultiWriter(os.Stdout, f)
	}
	if r := repoScrubber(*repo); r != nil {
		out = scrubber{out, r}
	}
	s := &smoke{url: *rpcURL, user: up[0], pass: up[1], distro: *distro, zfund: *zfund, keyWin: *keyWin, keyWSL: *keyWSL,
		hc: &http.Client{Timeout: 5 * time.Minute}, out: out, shapes: map[string]interface{}{}, errs: map[string]rpcErr{}, refs: map[string]interface{}{}}
	started := time.Now()
	s.logf("zecsmoke — the Zcash leg's node operations against a real regtest zebrad")
	s.logf("run at %s; node %s", started.UTC().Format(time.RFC3339), *rpcURL)

	// Z0 — which node, which chain, which rules
	s.logf("\n== Z0 the node")
	g := text(s.ok("getblockhash", "getblockhash", 0))
	if !s.check(g == regtestGenesis, "block 0 is the regtest genesis block %s", regtestGenesis) {
		os.Exit(1)
	}
	if s.tip() < 1 {
		s.generate(1) // the upgrade table reads "pending" until a block exists
	}
	ci := obj(s.ok("getblockchaininfo", "getblockchaininfo"))
	s.fact("getblockchaininfo chain=%q blocks=%d estimatedheight=%d initial_block_download_complete=%v", text(ci["chain"]), num(ci["blocks"]), num(ci["estimatedheight"]), ci["initial_block_download_complete"])
	s.check(num(ci["estimatedheight"]) == num(ci["blocks"]), "estimatedheight equals blocks (debug_force_finished_sync)")
	ups := obj(ci["upgrades"])
	mismatch := []string{}
	for id, h := range expectedHeights {
		u := obj(ups[id])
		if u == nil || num(u["activationheight"]) != h {
			mismatch = append(mismatch, id)
		}
	}
	s.check(len(mismatch) == 0, "the node's activation heights are the harness's (mismatched: %v)", mismatch)
	cons := obj(ci["consensus"])
	s.check(text(cons["nextblock"]) == branchNU63 && text(cons["chaintip"]) == branchNU63, "consensus chaintip %s, nextblock %s: NU6.3 (ZIP 258) is current", text(cons["chaintip"]), text(cons["nextblock"]))
	if zp, err := s.zf("params"); err == nil {
		bad := []string{}
		for id, h := range expectedHeights {
			if num(zp[id]) != h {
				bad = append(bad, id)
			}
		}
		s.check(len(bad) == 0, "zfund signs under the same activation heights (mismatched: %v)", bad)
	}
	for _, m := range []string{"getinfo", "getblockcount", "getbestblockhash", "getrawmempool", "getmininginfo", "getnetworkinfo", "getpeerinfo"} {
		s.ok("", m)
	}
	s.ok("", "getblock", fmt.Sprint(s.tip()), 1)
	s.ok("", "z_gettreestate", fmt.Sprint(s.tip()))

	// Z1 — a throwaway key paid a coinbase; the maturity measured
	s.logf("\n== Z1 coinbase to a throwaway key, and its maturity")
	a1 := s.keygen("k1.key")
	a2 := s.keygen("k2.key")
	s.check(strings.HasPrefix(a1, "tm") && strings.HasPrefix(a2, "tm"), "regtest P2PKH addresses carry the testnet prefix tm: %s %s", a1, a2)
	s.ok("", "validateaddress", a1)
	s.ok("generatetoaddress", "generatetoaddress", 1, a1)
	cbHeight := s.tip()
	var cb map[string]interface{}
	for _, u := range s.utxos("getaddressutxos", a1) {
		if num(u["height"]) == cbHeight {
			cb = u
		}
	}
	if !s.check(cb != nil, "getaddressutxos lists the coinbase at height %d", cbHeight) {
		s.finish(*shapesPath, started)
	}
	cbTx, cbIdx, cbVal := text(cb["txid"]), num(cb["outputIndex"]), num(cb["satoshis"])
	s.refs["coinbase_txid"], s.refs["coinbase_index"], s.refs["a1"], s.refs["a2"] = cbTx, cbIdx, a1, a2
	s.fact("regtest coinbase at height %d pays the miner address %d zatoshi (output %d); no funding-stream outputs are configured", cbHeight, cbVal, cbIdx)
	cbTxRaw := obj(s.ok("", "getrawtransaction", cbTx, 1))
	s.logf("  coinbase tx: version %v, %d vout", cbTxRaw["version"], len(arr(cbTxRaw["vout"])))
	// <8 random bytes> OP_DROP OP_TRUE: a script anyone can satisfy, enough to watch a P2SH output, and a new
	// address on every run, so what the index lists under it is this run's alone
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	p2shScript := append(append([]byte{0x08}, nonce...), 0x75, 0x51)
	p2sh := b58check(append([]byte{0x1c, 0xba}, hash160(p2shScript)...))
	s.logf("  P2SH(OP_TRUE) address %s", p2sh)
	spend := func(height uint64, version string) (map[string]interface{}, error) {
		return s.zf("send", "--key", s.keyWSL+"/k1.key", "--height", fmt.Sprint(height), "--version", version,
			"--in", fmt.Sprintf("%s:%d:%d", cbTx, cbIdx, cbVal), "--to", fmt.Sprintf("%s:%d", a2, zec), "--to", fmt.Sprintf("%s:%d", p2sh, zec/2))
	}
	// immature: the block after the tip is cbHeight+99
	s.generate(int(cbHeight + 98 - s.tip()))
	tx0, err := spend(s.tip()+1, "5")
	if s.check(err == nil, "zfund built the coinbase spend") {
		s.refuse("sendrawtransaction(immature coinbase)", "sendrawtransaction", text(tx0["hex"]))
	}
	s.generate(1)
	tx1, err := spend(s.tip()+1, "5")
	if !s.check(err == nil, "zfund built the coinbase spend at the next height") {
		s.finish(*shapesPath, started)
	}
	s.check(text(tx1["branch_id"]) == branchNU63 && num(tx1["version"]) == 5, "a v5 (ZIP 225) transaction under branch id %s", text(tx1["branch_id"]))
	sent := text(s.ok("sendrawtransaction", "sendrawtransaction", text(tx1["hex"])))
	if s.check(sent == text(tx1["txid"]), "sendrawtransaction accepted it and names the txid zfund computed (%s)", sent) {
		s.fact("a transparent coinbase output mined at height %d is refused while the next block is %d and accepted when it is %d: maturity %d blocks; spent straight to transparent outputs (no shielding: regtest's should_allow_unshielded_coinbase_spends)",
			cbHeight, cbHeight+99, cbHeight+100, 100)
		s.fact("funding tx: %d bytes, fee %d zatoshi (ZIP 317, 1 input 3 outputs), expiry height %d", num(tx1["size"]), num(tx1["fee"]), num(tx1["expiry_height"]))
	}

	// Z2 — the transaction in the mempool, then mined, as zecswap's RawTx and Broadcast read it
	s.logf("\n== Z2 getrawtransaction and duplicate broadcasts, before and after mining")
	s.refs["funding_txid"], s.refs["p2sh"] = sent, p2sh
	mp := obj(s.ok("getrawtransaction(mempool)", "getrawtransaction", sent, 1))
	_, hasH := mp["height"]
	_, hasC := mp["confirmations"]
	s.fact("getrawtransaction of a mempool tx: height present %v (%v), confirmations present %v (%v), blockhash %q", hasH, mp["height"], hasC, mp["confirmations"], text(mp["blockhash"]))
	s.refuse("sendrawtransaction(duplicate, in mempool)", "sendrawtransaction", text(tx1["hex"]))
	s.generate(1)
	minedAt := s.tip()
	s.refs["funding_height"] = minedAt
	rt := obj(s.ok("getrawtransaction(mined)", "getrawtransaction", sent, 1))
	bh := text(s.ok("", "getblockhash", minedAt))
	s.check(num(rt["height"]) == minedAt && num(rt["confirmations"]) == 1 && text(rt["blockhash"]) == bh,
		"mined: height %d, confirmations %d, blockhash equals getblockhash(%d)", num(rt["height"]), num(rt["confirmations"]), minedAt)
	s.check(text(rt["hex"]) == text(tx1["hex"]), "the node returns the exact bytes that were sent")
	s.refuse("sendrawtransaction(duplicate, mined)", "sendrawtransaction", text(tx1["hex"]))

	// Z3 — the address indexes zecswap's watcher reads
	s.logf("\n== Z3 getaddressutxos / getaddresstxids (the watcher's view of a P2SH)")
	pu := s.utxos("", p2sh)
	want := "a914" + hex.EncodeToString(hash160(p2shScript)) + "87"
	var p2shIdx uint64
	if s.check(len(pu) == 1, "getaddressutxos lists one output for the P2SH address") {
		u := pu[0]
		p2shIdx = num(u["outputIndex"])
		s.check(text(u["address"]) == p2sh && text(u["txid"]) == sent && text(u["script"]) == want && num(u["satoshis"]) == zec/2 && num(u["height"]) == minedAt,
			"P2SH utxo: address, txid, script %s, satoshis %d, height %d as zecswap reads them", text(u["script"]), num(u["satoshis"]), num(u["height"]))
	}
	s.refs["p2sh_index"] = p2shIdx

	// gettxout — zecswap's seventh method (TxOut), asked against the best chain only (include_mempool false)
	to := obj(s.ok("gettxout", "gettxout", sent, p2shIdx, false))
	s.check(to != nil && num(to["confirmations"]) == 1 && text(obj(to["scriptPubKey"])["hex"]) == want && len(text(to["bestblock"])) == 64,
		"gettxout of the unspent P2SH output: confirmations %d, scriptPubKey.hex, bestblock (value %v is in ZEC, not zatoshi)", num(to["confirmations"]), to["value"])
	for _, c := range []struct {
		what string
		txid string
		n    uint64
	}{{"the spent coinbase output", cbTx, cbIdx}, {"an output index the transaction does not have", sent, 9}} {
		r, e, _, err := s.call("gettxout", c.txid, c.n, false)
		s.check(err == nil && e == nil && r == nil, "gettxout of %s answers result null, error null (got %v, %v)", c.what, short(r), e)
	}
	ids := arr(s.ok("getaddresstxids", "getaddresstxids", map[string]interface{}{"addresses": []string{p2sh}, "start": minedAt, "end": minedAt}))
	s.check(len(ids) == 1 && text(ids[0]) == sent, "getaddresstxids over [%d, %d] (inclusive) lists the funding", minedAt, minedAt)
	// a range above the tip: zebra-rpc build_height_range clamps start and end to the tip instead of refusing
	above := arr(s.ok("", "getaddresstxids", map[string]interface{}{"addresses": []string{p2sh}, "start": minedAt + 1, "end": minedAt + 5}))
	s.fact("getaddresstxids over [%d, %d], above the tip %d, answers %d txid(s): Zebra clamps both ends to the tip (so it lists the tip block's transactions) rather than refusing", minedAt+1, minedAt+5, minedAt, len(above))
	all := arr(s.ok("", "getaddresstxids", map[string]interface{}{"addresses": []string{p2sh}, "start": 0, "end": 0}))
	s.fact("getaddresstxids with start 0 and end 0 lists the whole chain (%d txid)", len(all))
	s.refuse("getaddresstxids(start above end)", "getaddresstxids", map[string]interface{}{"addresses": []string{p2sh}, "start": minedAt, "end": minedAt - 1})
	a1u := s.utxos("", a1)
	spentGone := true
	for _, u := range a1u {
		if text(u["txid"]) == cbTx {
			spentGone = false
		}
	}
	s.check(spentGone, "the spent coinbase is gone from getaddressutxos (it lists unspent outputs only)")

	// Z4 — depth
	s.logf("\n== Z4 confirmations")
	s.generate(9)
	rt10 := obj(s.ok("", "getrawtransaction", sent, 1))
	s.check(num(rt10["confirmations"]) == 10 && s.tip()-minedAt+1 == 10, "after 9 more blocks: confirmations %d = tip − height + 1", num(rt10["confirmations"]))
	none := arr(s.ok("", "getaddresstxids", map[string]interface{}{"addresses": []string{p2sh}, "start": minedAt + 1, "end": s.tip()}))
	s.check(len(none) == 0, "getaddresstxids over [%d, %d], below the tip and after the funding, lists nothing (inclusive bounds, no spend yet)", minedAt+1, s.tip())

	// Z5 — a second key spends what it was paid (a non-coinbase transparent spend), and a v6 transaction
	s.logf("\n== Z5 second hop (v5) and a v6 transaction")
	var a2out map[string]interface{}
	for _, u := range s.utxos("", a2) {
		if text(u["txid"]) == sent {
			a2out = u
		}
	}
	if s.check(a2out != nil, "k2's output is listed") {
		tx2, err := s.zf("send", "--key", s.keyWSL+"/k2.key", "--height", fmt.Sprint(s.tip()+1),
			"--in", fmt.Sprintf("%s:%d:%d", sent, num(a2out["outputIndex"]), num(a2out["satoshis"])), "--to", fmt.Sprintf("%s:%d", a1, zec/2))
		if s.check(err == nil, "zfund built k2's spend") {
			id2 := text(s.ok("", "sendrawtransaction", text(tx2["hex"])))
			s.refs["hop2_txid"] = id2
			s.check(id2 == text(tx2["txid"]), "k2's v5 spend accepted (%s), fee %d", id2, num(tx2["fee"]))
			// the output k2 just spent, while that spend is only in the mempool
			idx := num(a2out["outputIndex"])
			chainOnly, _, _, _ := s.call("gettxout", sent, idx, false)
			withPool, _, _, _ := s.call("gettxout", sent, idx, true)
			s.fact("gettxout of an output spent by a mempool transaction: include_mempool=false answers %s; include_mempool=true answers %s",
				map[bool]string{true: "the output", false: "null"}[chainOnly != nil], map[bool]string{true: "the output", false: "null"}[withPool != nil])
			// the same coin spent again: a double spend of a mempool input
			tx2b, err := s.zf("send", "--key", s.keyWSL+"/k2.key", "--height", fmt.Sprint(s.tip()+1),
				"--in", fmt.Sprintf("%s:%d:%d", sent, num(a2out["outputIndex"]), num(a2out["satoshis"])), "--to", fmt.Sprintf("%s:%d", a1, zec/4))
			if err == nil {
				s.refuse("sendrawtransaction(double spend of a mempool input)", "sendrawtransaction", text(tx2b["hex"]))
			}
			s.generate(1)
		}
	}
	var change map[string]interface{}
	for _, u := range s.utxos("", a1) {
		if text(u["txid"]) == sent {
			change = u
		}
	}
	if s.check(change != nil, "k1's change output is listed") {
		tx6, err := s.zf("send", "--key", s.keyWSL+"/k1.key", "--height", fmt.Sprint(s.tip()+1), "--version", "6",
			"--in", fmt.Sprintf("%s:%d:%d", sent, num(change["outputIndex"]), num(change["satoshis"])), "--to", fmt.Sprintf("%s:%d", a2, zec))
		if err == nil {
			r, e, _, _ := s.call("sendrawtransaction", text(tx6["hex"]))
			if e != nil {
				s.fact("a v6 (ZIP 229) transparent transaction under NU6.3 is refused: %d %q", e.Code, e.Message)
			} else {
				s.refs["v6_txid"] = text(r)
				s.fact("a v6 (ZIP 229) transparent transaction under NU6.3 is accepted (%s); zecswap signs v5, which the node also accepts", text(r))
				s.generate(1)
			}
		}
	}
	// a coin already spent in a block
	if tx3, err := spend(s.tip()+1, "5"); err == nil {
		s.refuse("sendrawtransaction(input spent in a block)", "sendrawtransaction", text(tx3["hex"]))
	}
	s.refuse("getrawtransaction(unknown txid)", "getrawtransaction", strings.Repeat("00", 31)+"01", 1)
	s.refuse("sendrawtransaction(not a transaction)", "sendrawtransaction", "00")
	s.finish(*shapesPath, started)
}

func (s *smoke) finish(shapesPath string, started time.Time) {
	s.logf("\n== summary (%s)", time.Since(started).Round(time.Second))
	for _, f := range s.facts {
		s.logf("  %s", f)
	}
	keys := make([]string, 0, len(s.errs))
	for k := range s.errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s.logf("  refusals:")
	for _, k := range keys {
		s.logf("    %-52s %d %q", k, s.errs[k].Code, s.errs[k].Message)
	}
	s.logf("  shapes of the answers zecswap reads:")
	vs := make([]string, 0, len(s.shapes))
	for k := range s.shapes {
		vs = append(vs, k)
	}
	sort.Strings(vs)
	for _, k := range vs {
		s.logf("    %-28s %s", k, short(s.shapes[k]))
	}
	if shapesPath != "" {
		b, _ := json.MarshalIndent(map[string]interface{}{"shapes": s.shapes, "errors": s.errs, "refs": s.refs}, "", "  ")
		os.WriteFile(shapesPath, b, 0o644)
	}
	if s.fails > 0 {
		s.logf("RESULT: %d check(s) FAILED", s.fails)
		os.Exit(1)
	}
	s.logf("RESULT: every check passed")
	os.Exit(0)
}
