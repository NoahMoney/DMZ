// Command xmrsmoke proves, against a REAL monerod --regtest and monero-wallet-rpc (tools/regtest/monero-up.sh),
// every wallet-rpc call tools/xmrswap makes, with the parameters it sends, and measures the facts the swap relies
// on: coinbase unlock, the 10-block unlock of an incoming transfer, what a transfer and a sweep pay in fees, and how
// long a refresh takes. It speaks raw JSON-RPC and imports nothing from tools/xmrswap, so it is a second reading of
// the protocol, not the companion's client talking to itself.
//
// The joint-key path is exercised the way the swap uses it: two random shares per key, the spend key s = s_a + s_b
// and view key v = v_a + v_b summed mod l, the address built from s·G and v·G (so the view key is NOT derived from
// the spend key, as it is for a wallet made from a seed). The joint address is funded, watched view-only with
// generate_from_keys(address, viewkey), then restored spendable with the spend key and swept.
//
// Every response's JSON shape is written to -shapes for tools/regtest/xmrshape to compare with the fake wallet.
// Private keys never reach the transcript: they are redacted from both requests and responses.
//
// REGTEST ONLY: it refuses a daemon whose get_info nettype is not "fakechain".
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/sha3"
)

const (
	xmr         = uint64(1_000_000_000_000) // piconero per XMR
	mainnetTag  = 18                        // standard address prefix a regtest daemon uses (mainnet's)
	stagenetTag = 24
)

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type smoke struct {
	daemon, wallet string
	hc             *http.Client
	out            io.Writer
	shapes         map[string]interface{}
	errs           map[string]rpcErr
	facts          []string
	fails          int
}

func (s *smoke) logf(format string, a ...interface{}) { fmt.Fprintf(s.out, format+"\n", a...) }

func (s *smoke) check(ok bool, format string, a ...interface{}) bool {
	if ok {
		s.logf("  PASS  "+format, a...)
	} else {
		s.fails++
		s.logf("  FAIL  "+format, a...)
	}
	return ok
}

func (s *smoke) fact(format string, a ...interface{}) {
	f := fmt.Sprintf(format, a...)
	s.facts = append(s.facts, f)
	s.logf("  FACT  %s", f)
}

// redact hides every private key a request or response carries: the transcript is tracked evidence.
func redact(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := map[string]interface{}{}
		for k, x := range t {
			switch k {
			case "viewkey", "spendkey", "key", "seed":
				out[k] = "<redacted>"
			case "password":
				// a wallet file's password: hidden too; an empty one stays visible, since "no password" is a fact
				if p, ok := x.(string); ok && p != "" {
					out[k] = "<redacted>"
				} else {
					out[k] = x
				}
			default:
				out[k] = redact(x)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, x := range t {
			out[i] = redact(x)
		}
		return out
	}
	return v
}

func compact(v interface{}) string {
	b, _ := json.Marshal(redact(v))
	if len(b) > 1200 {
		return string(b[:1200]) + fmt.Sprintf("...(%d bytes)", len(b))
	}
	return string(b)
}

// call posts one JSON-RPC 2.0 request to base/json_rpc and returns the decoded result or the RPC error.
func (s *smoke) call(base, method string, params interface{}) (map[string]interface{}, *rpcErr, time.Duration, error) {
	req := map[string]interface{}{"jsonrpc": "2.0", "id": "0", "method": method}
	if params != nil {
		req["params"] = params
	}
	body, _ := json.Marshal(req)
	t0 := time.Now()
	resp, err := s.hc.Post(base+"/json_rpc", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	d := time.Since(t0)
	if err != nil {
		return nil, nil, d, err
	}
	var env struct {
		Result map[string]interface{} `json:"result"`
		Error  *rpcErr                `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil {
		return nil, nil, d, fmt.Errorf("http %d, not JSON-RPC: %.200s", resp.StatusCode, raw)
	}
	return env.Result, env.Error, d, nil
}

// w is a wallet-rpc call that must succeed; its shape is recorded under variant.
func (s *smoke) w(method string, params interface{}, variant string) map[string]interface{} {
	s.logf("  -> wallet %s %s", method, compact(params))
	r, e, d, err := s.call(s.wallet, method, params)
	switch {
	case err != nil:
		s.check(false, "%s: transport: %v", method, err)
		return nil
	case e != nil:
		s.check(false, "%s: rpc error %d %q", method, e.Code, e.Message)
		return nil
	}
	s.logf("  <- (%s) %s", d.Round(time.Millisecond), compact(r))
	if variant != "" {
		if _, seen := s.shapes[variant]; !seen {
			s.shapes[variant] = shapeOf(r)
		}
	}
	return r
}

// werr is a wallet-rpc call that must be refused; the refusal is recorded under scenario.
func (s *smoke) werr(method string, params interface{}, scenario string) *rpcErr {
	s.logf("  -> wallet %s %s   (expect a refusal: %s)", method, compact(params), scenario)
	r, e, _, err := s.call(s.wallet, method, params)
	if err != nil {
		s.check(false, "%s: transport: %v", scenario, err)
		return nil
	}
	if e == nil {
		s.check(false, "%s: the wallet accepted it: %s", scenario, compact(r))
		return nil
	}
	s.logf("  <- error %d %q", e.Code, e.Message)
	s.errs[scenario] = *e
	return e
}

// poolHashes is the daemon's mempool (monerod's /get_transaction_pool_hashes, a non-JSON-RPC endpoint).
func (s *smoke) poolHashes() map[string]bool {
	out := map[string]bool{}
	resp, err := s.hc.Post(s.daemon+"/get_transaction_pool_hashes", "application/json", strings.NewReader("{}"))
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	var r struct {
		TxHashes []string `json:"tx_hashes"`
	}
	json.NewDecoder(resp.Body).Decode(&r)
	for _, h := range r.TxHashes {
		out[h] = true
	}
	return out
}

func hashesOf(r map[string]interface{}) []string {
	var hs []string
	if h := str(r, "tx_hash"); h != "" {
		hs = append(hs, h)
	}
	if l, ok := r["tx_hash_list"].([]interface{}); ok {
		for _, x := range l {
			if h, ok := x.(string); ok {
				hs = append(hs, h)
			}
		}
	}
	return hs
}

// observeSpend sends a spend the fake refuses, records what the real server answers, and checks whether anything
// reached the daemon's pool.
func (s *smoke) observeSpend(method string, params interface{}, scenario string) {
	s.logf("  -> wallet %s %s   (observe: %s)", method, compact(params), scenario)
	r, e, _, err := s.call(s.wallet, method, params)
	if err != nil {
		s.check(false, "%s: transport: %v", scenario, err)
		return
	}
	if e != nil {
		s.logf("  <- error %d %q", e.Code, e.Message)
		s.errs[scenario] = *e
		s.fact("%s is refused: %d %q", scenario, e.Code, e.Message)
		return
	}
	s.logf("  <- %s", compact(r))
	if _, seen := s.shapes[scenario]; !seen {
		s.shapes[scenario] = shapeOf(r)
	}
	hs := hashesOf(r)
	pool := s.poolHashes()
	relayed := false
	for _, h := range hs {
		relayed = relayed || pool[h]
	}
	unsigned := len(str(r, "unsigned_txset"))
	s.errs[scenario] = rpcErr{Code: 0, Message: fmt.Sprintf("ACCEPTED: tx hash %v, unsigned_txset of %d hex chars, tx_blob %q, in the daemon's pool: %v", hs, unsigned, str(r, "tx_blob"), relayed)}
	s.fact("%s is NOT refused: it answers tx hash(es) %v and an unsigned_txset of %d hex chars (for offline signing); in the daemon's pool: %v", scenario, hs, unsigned, relayed)
	s.check(!relayed, "%s: nothing was broadcast, whatever tx hash it answered", scenario)
}

// observeGen makes a wallet from keys the fake refuses, records the server's answer, and if it accepts, shows what
// that wallet then sees and — for a spend key — what a sweep from it does.
func (s *smoke) observeGen(params map[string]interface{}, scenario, sweepTo string) {
	s.logf("  -> wallet generate_from_keys %s   (observe: %s)", compact(params), scenario)
	r, e, _, err := s.call(s.wallet, "generate_from_keys", params)
	if err != nil {
		s.check(false, "%s: transport: %v", scenario, err)
		return
	}
	if e != nil {
		s.logf("  <- error %d %q", e.Code, e.Message)
		s.errs[scenario] = *e
		s.fact("%s is refused: %d %q", scenario, e.Code, e.Message)
		return
	}
	s.logf("  <- %s", compact(r))
	echo := str(r, "address") == params["address"]
	msg := fmt.Sprintf("ACCEPTED: %q, echoes the address: %v", str(r, "info"), echo)
	if _, re, _, _ := s.call(s.wallet, "refresh", map[string]interface{}{"start_height": params["restore_height"]}); re != nil {
		msg += fmt.Sprintf("; refresh then fails: %d %q", re.Code, re.Message)
	}
	b, _, _, _ := s.call(s.wallet, "get_balance", map[string]interface{}{"account_index": 0})
	msg += fmt.Sprintf("; get_balance: balance %d unlocked %d", u64(b, "balance"), u64(b, "unlocked_balance"))
	if _, hasSpend := params["spendkey"]; hasSpend {
		sr, se, _, _ := s.call(s.wallet, "sweep_all", map[string]interface{}{"address": sweepTo, "account_index": 0})
		if se != nil {
			msg += fmt.Sprintf("; sweep_all from it: %d %q", se.Code, se.Message)
		} else {
			pool := s.poolHashes()
			in := false
			for _, h := range hashesOf(sr) {
				in = in || pool[h]
			}
			msg += fmt.Sprintf("; sweep_all from it answered %v, in the daemon's pool: %v", hashesOf(sr), in)
		}
	}
	s.errs[scenario] = rpcErr{Code: 0, Message: msg}
	s.fact("%s: %s", scenario, msg)
	s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "")
}

func (s *smoke) d(method string, params interface{}) map[string]interface{} {
	s.logf("  -> daemon %s %s", method, compact(params))
	r, e, dur, err := s.call(s.daemon, method, params)
	switch {
	case err != nil:
		s.check(false, "daemon %s: transport: %v", method, err)
		return nil
	case e != nil:
		s.check(false, "daemon %s: rpc error %d %q", method, e.Code, e.Message)
		return nil
	}
	s.logf("  <- (%s) %s", dur.Round(time.Millisecond), compact(r))
	return r
}

func str(m map[string]interface{}, k string) string {
	if m == nil {
		return ""
	}
	v, _ := m[k].(string)
	return v
}

func u64(m map[string]interface{}, k string) uint64 {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case json.Number:
		var n uint64
		fmt.Sscan(v.String(), &n)
		return n
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

func boolean(m map[string]interface{}, k string) (bool, bool) {
	if m == nil {
		return false, false
	}
	v, ok := m[k].(bool)
	return v, ok
}

func list(m map[string]interface{}, k string) []map[string]interface{} {
	if m == nil {
		return nil
	}
	a, _ := m[k].([]interface{})
	out := []map[string]interface{}{}
	for _, x := range a {
		if e, ok := x.(map[string]interface{}); ok {
			out = append(out, e)
		}
	}
	return out
}

// chainHeight is the daemon's height: the number of blocks, the next block's index.
func (s *smoke) chainHeight() uint64 {
	r, _, _, err := s.call(s.daemon, "get_info", nil)
	if err != nil || r == nil {
		s.check(false, "daemon get_info: %v", err)
		return 0
	}
	return u64(r, "height")
}

// mine produces n blocks paying addr, quietly, and returns the new chain height.
func (s *smoke) mine(n uint64, addr string) uint64 {
	r, e, d, err := s.call(s.daemon, "generateblocks", map[string]interface{}{"amount_of_blocks": n, "wallet_address": addr})
	if err != nil || e != nil {
		s.check(false, "generateblocks %d: %v %v", n, err, e)
		return 0
	}
	h := s.chainHeight()
	s.logf("  .. mined %d block(s) in %s, chain height %d", n, d.Round(time.Millisecond), h)
	_ = r
	return h
}

// shapeOf reduces a JSON value to its structure: object keys with their shapes, arrays by their first element.
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
	case json.Number, float64:
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

// scrubber writes through a replacer: the transcript is tracked, and must not carry a machine-specific path (the
// wallet-rpc names its wallet dir in errors).
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

// --- keys and addresses, independently of tools/xmrswap/keys.go ---

func randScalar() *edwards25519.Scalar {
	var b [64]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	s, err := edwards25519.NewScalar().SetUniformBytes(b[:])
	if err != nil {
		panic(err)
	}
	return s
}

func pub(s *edwards25519.Scalar) []byte {
	return edwards25519.NewIdentityPoint().ScalarBaseMult(s).Bytes()
}

const b58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var b58Size = [9]int{0, 2, 3, 5, 6, 7, 9, 10, 11}

// moneroBase58 is src/common/base58.cpp encode: 8-byte blocks, each 11 digits (fewer for the last).
func moneroBase58(data []byte) string {
	var out strings.Builder
	for i := 0; i < len(data); i += 8 {
		end := i + 8
		if end > len(data) {
			end = len(data)
		}
		block := data[i:end]
		var num uint64
		for _, c := range block {
			num = num<<8 | uint64(c)
		}
		digits := make([]byte, b58Size[len(block)])
		for j := len(digits) - 1; j >= 0; j-- {
			digits[j] = b58[num%58]
			num /= 58
		}
		out.Write(digits)
	}
	return out.String()
}

// moneroAddress is base58(varint(tag) ‖ spend ‖ view ‖ keccak256(...)[:4]).
func moneroAddress(tag byte, spend, view []byte) string {
	buf := append([]byte{tag}, spend...) // tags 18 and 24 are one-byte varints
	buf = append(buf, view...)
	h := sha3.NewLegacyKeccak256()
	h.Write(buf)
	return moneroBase58(append(buf, h.Sum(nil)[:4]...))
}

// --- the run ---

func main() {
	daemon := flag.String("daemon", "http://127.0.0.1:28081", "monerod RPC")
	wallet := flag.String("wallet", "http://127.0.0.1:28083", "monero-wallet-rpc RPC (no login)")
	logPath := flag.String("log", "", "also write the transcript to this file")
	shapesPath := flag.String("shapes", "", "write the real response shapes and refusals here (JSON)")
	repo := flag.String("repo", "", "the repository root (C:/...); the transcript writes it as <repo> in every form")
	flag.Parse()

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
	s := &smoke{daemon: *daemon, wallet: *wallet, hc: &http.Client{Timeout: 10 * time.Minute}, out: out,
		shapes: map[string]interface{}{}, errs: map[string]rpcErr{}}
	tag := fmt.Sprintf("smoke-%d", time.Now().Unix())
	started := time.Now()
	s.logf("xmrsmoke — tools/xmrswap's wallet-rpc calls against a real regtest monerod + monero-wallet-rpc")
	s.logf("run %s at %s; daemon %s, wallet-rpc %s", tag, started.UTC().Format(time.RFC3339), *daemon, *wallet)

	// S0 — what we are talking to
	s.logf("\n== S0 the nodes")
	info := s.d("get_info", nil)
	if !s.check(str(info, "nettype") == "fakechain", "daemon nettype %q is fakechain (regtest)", str(info, "nettype")) {
		s.logf("refusing to continue against a daemon that is not regtest")
		os.Exit(1)
	}
	s.fact("monerod %s, nettype %s, offline %v, height %d", str(info, "version"), str(info, "nettype"), info["offline"], u64(info, "height"))
	hf := s.d("hard_fork_info", nil)
	s.fact("hard fork version at the tip: %d", u64(hf, "version"))
	ver := s.w("get_version", nil, "get_version")
	s.fact("monero-wallet-rpc get_version %d (release %v)", u64(ver, "version"), ver["release"])

	// S1 — a funding wallet; its address shows which prefix regtest uses
	s.logf("\n== S1 create_wallet / get_address / get_height")
	s.w("create_wallet", map[string]interface{}{"filename": tag + "-fund", "password": "", "language": "English"}, "create_wallet")
	fund := str(s.w("get_address", map[string]interface{}{"account_index": 0}, "get_address"), "address")
	s.check(strings.HasPrefix(fund, "4") && len(fund) == 95, "a regtest wallet's address is a mainnet-prefix (4...) standard address: %s", fund)
	wh := u64(s.w("get_height", nil, "get_height"), "height")
	s.fact("a new wallet's get_height is %d while the daemon is at %d: get_height is the wallet's own scanned height (wallet2 m_blockchain.size()), not the chain's", wh, s.chainHeight())

	// S2 — coinbase unlock, one block at a time
	s.logf("\n== S2 coinbase unlock (CRYPTONOTE_MINED_MONEY_UNLOCK_WINDOW)")
	cbHeight := s.chainHeight() // the block about to be mined has this index
	s.mine(1, fund)
	s.w("refresh", map[string]interface{}{"start_height": 0}, "refresh")
	bal := s.w("get_balance", map[string]interface{}{"account_index": 0}, "get_balance")
	s.check(u64(bal, "balance") > 0 && u64(bal, "unlocked_balance") == 0, "one coinbase: balance %d, unlocked %d, blocks_to_unlock %d",
		u64(bal, "balance"), u64(bal, "unlocked_balance"), u64(bal, "blocks_to_unlock"))
	firstReward := u64(bal, "balance")
	var unlockedAt uint64
	for i := 0; i < 100; i++ {
		h := s.mine(1, fund)
		s.call(s.wallet, "refresh", map[string]interface{}{"start_height": 0})
		b, _, _, _ := s.call(s.wallet, "get_balance", map[string]interface{}{"account_index": 0})
		if u64(b, "unlocked_balance") > 0 {
			unlockedAt = h
			s.logf("  .. unlocked_balance %d at chain height %d (blocks_to_unlock now %d)", u64(b, "unlocked_balance"), h, u64(b, "blocks_to_unlock"))
			break
		}
	}
	if s.check(unlockedAt > cbHeight, "the first coinbase unlocked") {
		s.fact("coinbase: mined in block %d (reward %d piconero), first in unlocked_balance at chain height %d = block + %d",
			cbHeight, firstReward, unlockedAt, unlockedAt-cbHeight)
	}

	// S3 — more unlocked coinbases (a transfer's change is itself locked for 10 blocks, so each transfer in a row
	// needs an unlocked output of its own), enough outputs on chain for 16-member rings, and a refresh over them
	s.logf("\n== S3 more unlocked outputs and ring decoys")
	t0 := time.Now()
	s.mine(80, fund)
	s.fact("generateblocks 80 blocks took %s", time.Since(t0).Round(time.Millisecond))
	t0 = time.Now()
	s.w("refresh", map[string]interface{}{"start_height": 0}, "")
	s.fact("refresh of the funding wallet to height %d took %s", s.chainHeight(), time.Since(t0).Round(time.Millisecond))
	gh := u64(s.w("get_height", nil, ""), "height")
	s.check(gh == s.chainHeight(), "after a refresh, get_height %d equals the daemon's height", gh)

	// S4 — the joint key, as the swap makes it: shares summed mod l
	s.logf("\n== S4 joint key s = s_a + s_b, v = v_a + v_b (view key not derived from the spend key)")
	sa, sb, va, vb := randScalar(), randScalar(), randScalar(), randScalar()
	sj := edwards25519.NewScalar().Add(sa, sb)
	vj := edwards25519.NewScalar().Add(va, vb)
	joint := moneroAddress(mainnetTag, pub(sj), pub(vj))
	s.logf("  joint address %s (keys redacted)", joint)
	s.w("create_wallet", map[string]interface{}{"filename": tag + "-recv", "password": "", "language": "English"}, "")
	recv := str(s.w("get_address", map[string]interface{}{"account_index": 0}, ""), "address")

	// S5 — transfers exactly as xmrswap sends them: destinations + account_index, nothing else
	s.logf("\n== S5 transfer (xmrswap Transfer: destinations + account_index)")
	s.w("open_wallet", map[string]interface{}{"filename": tag + "-fund", "password": ""}, "open_wallet")
	s.w("refresh", map[string]interface{}{"start_height": 0}, "")
	restoreFrom := s.chainHeight()
	tj := s.w("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr, "address": joint}}, "account_index": 0}, "transfer")
	tr := s.w("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr / 2, "address": recv}}, "account_index": 0}, "")
	s.check(len(str(tj, "tx_hash")) == 64 && len(str(tr, "tx_hash")) == 64, "both transfers returned a 64-hex tx_hash")
	s.fact("transfer fee (1 destination + change, default priority, 16-member ring): %d and %d piconero", u64(tj, "fee"), u64(tr, "fee"))
	fe := s.d("get_fee_estimate", nil)
	s.fact("daemon get_fee_estimate: fee %d piconero/byte, quantization_mask %d", u64(fe, "fee"), u64(fe, "quantization_mask"))

	// S6 — the incoming transfer, block by block: confirmations, locked, unlocked_balance
	s.logf("\n== S6 incoming: confirmations and the 10-block unlock (CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE)")
	s.w("open_wallet", map[string]interface{}{"filename": tag + "-recv", "password": ""}, "")
	s.w("refresh", map[string]interface{}{"start_height": 0}, "")
	pool := s.w("get_transfers", map[string]interface{}{"in": true, "pool": true, "account_index": 0}, "get_transfers(pool)")
	s.check(len(list(pool, "in")) == 0 && len(list(pool, "pool")) == 1, "unmined: nothing under in, one entry under pool (in %d, pool %d)", len(list(pool, "in")), len(list(pool, "pool")))
	txHeight := s.chainHeight()
	s.mine(1, fund)
	var unlockConf, balanceConf uint64
	for k := 1; k <= 13; k++ {
		s.call(s.wallet, "refresh", map[string]interface{}{"start_height": 0})
		// xmrswap Incoming: min_height is exclusive, so it sends minHeight-1
		gt := s.w("get_transfers", map[string]interface{}{"in": true, "filter_by_height": true, "min_height": txHeight - 1, "account_index": 0}, "get_transfers")
		gb, _, _, _ := s.call(s.wallet, "get_balance", map[string]interface{}{"account_index": 0})
		for _, e := range list(gt, "in") {
			if str(e, "txid") != str(tr, "tx_hash") {
				continue
			}
			locked, hasLocked := boolean(e, "locked")
			s.logf("  .. chain %d  tx block %d  confirmations %d  locked %v (field present %v)  unlocked_balance %d  blocks_to_unlock %d",
				s.chainHeight(), u64(e, "height"), u64(e, "confirmations"), locked, hasLocked, u64(gb, "unlocked_balance"), u64(gb, "blocks_to_unlock"))
			if hasLocked && !locked && unlockConf == 0 {
				unlockConf = u64(e, "confirmations")
			}
			if u64(gb, "unlocked_balance") > 0 && balanceConf == 0 {
				balanceConf = u64(e, "confirmations")
			}
		}
		if unlockConf > 0 && balanceConf > 0 {
			break
		}
		s.mine(1, fund)
	}
	s.fact("incoming transfer in block %d: locked=false first at confirmations=%d; unlocked_balance first > 0 at confirmations=%d", txHeight, unlockConf, balanceConf)
	s.check(unlockConf == 10, "an incoming transfer unlocks at 10 confirmations, as xmrswap assumes (measured %d)", unlockConf)
	excl := s.w("get_transfers", map[string]interface{}{"in": true, "filter_by_height": true, "min_height": txHeight, "account_index": 0}, "")
	incl := s.w("get_transfers", map[string]interface{}{"in": true, "filter_by_height": true, "min_height": txHeight - 1, "account_index": 0}, "")
	s.check(len(list(excl, "in")) == 0 && len(list(incl, "in")) == 1, "get_transfers min_height is exclusive: min_height=%d lists %d, min_height=%d lists %d",
		txHeight, len(list(excl, "in")), txHeight-1, len(list(incl, "in")))

	// S7 — the joint address, watched view-only
	s.logf("\n== S7 generate_from_keys view-only (address + viewkey), then refresh")
	vk := hex.EncodeToString(vj.Bytes())
	gv := s.w("generate_from_keys", map[string]interface{}{"restore_height": restoreFrom, "filename": tag + "-view", "address": joint,
		"viewkey": vk, "password": "", "autosave_current": true}, "generate_from_keys(view)")
	s.check(str(gv, "address") == joint, "generate_from_keys echoes the joint address")
	b0 := s.w("get_balance", map[string]interface{}{"account_index": 0}, "")
	s.fact("view-only wallet right after generate_from_keys, before refresh: balance %d (xmrswap expects 0 until refresh)", u64(b0, "balance"))
	t1 := time.Now()
	rf := s.w("refresh", map[string]interface{}{"start_height": restoreFrom}, "")
	s.fact("refresh of a new view-only wallet from height %d to %d: %s, blocks_fetched %d, received_money %v",
		restoreFrom, s.chainHeight(), time.Since(t1).Round(time.Millisecond), u64(rf, "blocks_fetched"), rf["received_money"])
	vt := s.w("get_transfers", map[string]interface{}{"in": true, "filter_by_height": true, "min_height": restoreFrom - 1, "account_index": 0}, "get_transfers(view)")
	vb2 := s.w("get_balance", map[string]interface{}{"account_index": 0}, "get_balance(view)")
	s.check(len(list(vt, "in")) == 1 && u64(list(vt, "in")[0], "amount") == xmr, "the view-only wallet sees the 1 XMR paid to the joint address")
	s.fact("view-only get_balance: balance %d unlocked_balance %d", u64(vb2, "balance"), u64(vb2, "unlocked_balance"))
	s.observeSpend("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr / 10, "address": fund}}, "account_index": 0}, "transfer(view-only)")
	s.observeSpend("sweep_all", map[string]interface{}{"address": fund, "account_index": 0}, "sweep_all(view-only)")
	s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "close_wallet")

	// S8 — the joint address, restored spendable with s_a + s_b, swept
	s.logf("\n== S8 generate_from_keys with the joint spend key, refresh, sweep_all")
	sk := hex.EncodeToString(sj.Bytes())
	gs := s.w("generate_from_keys", map[string]interface{}{"restore_height": restoreFrom, "filename": tag + "-sweep", "address": joint,
		"viewkey": vk, "spendkey": sk, "password": "", "autosave_current": true}, "generate_from_keys(spend)")
	s.check(str(gs, "address") == joint, "generate_from_keys (spendable) echoes the joint address")
	s.w("refresh", map[string]interface{}{"start_height": restoreFrom}, "")
	sbal := s.w("get_balance", map[string]interface{}{"account_index": 0}, "")
	s.check(u64(sbal, "unlocked_balance") == xmr, "the restored joint wallet can spend exactly 1 XMR (unlocked %d)", u64(sbal, "unlocked_balance"))
	sw := s.w("sweep_all", map[string]interface{}{"address": fund, "account_index": 0}, "sweep_all")
	hl := sw["tx_hash_list"]
	s.check(hl != nil && len(hl.([]interface{})) >= 1, "sweep_all returned tx_hash_list %v", hl)
	if fl, ok := sw["fee_list"].([]interface{}); ok && len(fl) > 0 {
		s.fact("sweep_all (1 input, 1 output) fee %v piconero, amount %v", fl[0], sw["amount_list"])
	}
	s.werr("sweep_all", map[string]interface{}{"address": fund, "account_index": 0}, "sweep_all(nothing unlocked)")
	s.werr("generate_from_keys", map[string]interface{}{"restore_height": restoreFrom, "filename": tag + "-sweep", "address": joint,
		"viewkey": vk, "password": "", "autosave_current": true}, "generate_from_keys(exists)")
	s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "")
	// keys that do not belong to the address: the fake refuses them ("... does not match main address"); observe
	// what the real server does, and what a wallet made that way then shows and does
	s.observeGen(map[string]interface{}{"restore_height": restoreFrom, "filename": tag + "-badview", "address": joint,
		"viewkey": hex.EncodeToString(randScalar().Bytes()), "password": "", "autosave_current": true}, "generate_from_keys(view mismatch)", fund)
	s.observeGen(map[string]interface{}{"restore_height": restoreFrom, "filename": tag + "-badspend", "address": joint,
		"viewkey": vk, "spendkey": hex.EncodeToString(randScalar().Bytes()), "password": "", "autosave_current": true}, "generate_from_keys(spend mismatch)", fund)

	// S9 — the refusals xmrswap reads by code
	s.logf("\n== S9 refusals")
	s.werr("get_address", map[string]interface{}{"account_index": 0}, "get_address(no wallet)")
	s.werr("get_balance", map[string]interface{}{"account_index": 0}, "get_balance(no wallet)")
	s.werr("get_transfers", map[string]interface{}{"in": true, "account_index": 0}, "get_transfers(no wallet)")
	s.werr("refresh", map[string]interface{}{"start_height": 0}, "refresh(no wallet)")
	s.werr("close_wallet", map[string]interface{}{"autosave_current": true}, "close_wallet(no wallet)")
	if r, e, _, _ := s.call(s.wallet, "get_height", nil); e != nil {
		s.errs["get_height(no wallet)"] = *e
		s.logf("  get_height with no wallet open: error %d %q", e.Code, e.Message)
	} else {
		s.logf("  get_height with no wallet open answers %s", compact(r))
	}
	s.werr("open_wallet", map[string]interface{}{"filename": tag + "-missing", "password": ""}, "open_wallet(missing)")
	s.w("open_wallet", map[string]interface{}{"filename": tag + "-fund", "password": ""}, "")
	s.w("refresh", map[string]interface{}{"start_height": 0}, "")
	stagenet := moneroAddress(stagenetTag, pub(sj), pub(vj))
	s.werr("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr, "address": "4notanaddress"}}, "account_index": 0}, "transfer(bad address)")
	s.werr("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr, "address": stagenet}}, "account_index": 0}, "transfer(stagenet address)")
	s.werr("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 0, "address": recv}}, "account_index": 0}, "transfer(zero amount)")
	s.werr("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 1_000_000 * xmr, "address": recv}}, "account_index": 0}, "transfer(more than the balance)")

	// S10 — a sender-chosen unlock_time (xmrswap review R6: Bob's gate must not count a frozen output)
	s.logf("\n== S10 unlock_time set by the sender")
	h := s.chainHeight()
	ut, e, _, _ := s.call(s.wallet, "transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 3 * xmr / 10, "address": recv}}, "account_index": 0, "unlock_time": h + 50})
	if e != nil {
		s.fact("transfer with unlock_time=%d refused: %d %q", h+50, e.Code, e.Message)
		s.errs["transfer(unlock_time)"] = *e
		// not enough unlocked money: recv holds 0.5 XMR unlocked; pay it 0.2 more, mine one block, spend 0.6
		s.w("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr / 5, "address": recv}}, "account_index": 0}, "")
		s.mine(1, fund)
		s.w("open_wallet", map[string]interface{}{"filename": tag + "-recv", "password": ""}, "")
		s.w("refresh", map[string]interface{}{"start_height": 0}, "")
		rb := s.w("get_balance", map[string]interface{}{"account_index": 0}, "")
		s.logf("  recv: balance %d, unlocked %d", u64(rb, "balance"), u64(rb, "unlocked_balance"))
		s.werr("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 6 * xmr / 10, "address": fund}}, "account_index": 0}, "transfer(not enough unlocked)")
		s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "")
	} else {
		s.logf("  transfer with unlock_time %d accepted: %s", h+50, compact(ut))
		s.mine(12, fund)
		s.w("open_wallet", map[string]interface{}{"filename": tag + "-recv", "password": ""}, "")
		s.w("refresh", map[string]interface{}{"start_height": 0}, "")
		gt := s.w("get_transfers", map[string]interface{}{"in": true, "filter_by_height": true, "min_height": h - 1, "account_index": 0}, "")
		gb := s.w("get_balance", map[string]interface{}{"account_index": 0}, "")
		for _, en := range list(gt, "in") {
			if str(en, "txid") == str(ut, "tx_hash") {
				locked, _ := boolean(en, "locked")
				s.fact("a transfer with unlock_time %d, at %d confirmations: get_transfers unlock_time=%d locked=%v; wallet unlocked_balance %d of balance %d",
					h+50, u64(en, "confirmations"), u64(en, "unlock_time"), locked, u64(gb, "unlocked_balance"), u64(gb, "balance"))
				s.check(locked && u64(en, "unlock_time") == h+50, "the frozen output is listed with its unlock_time and stays locked past 10 confirmations")
			}
		}
		s.werr("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 7 * xmr / 10, "address": fund}}, "account_index": 0}, "transfer(not enough unlocked)")
		s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "")
	}

	// S11 — a brand-new wallet scanning the whole chain
	s.logf("\n== S11 refresh timing from height 0")
	s.w("generate_from_keys", map[string]interface{}{"restore_height": 0, "filename": tag + "-rescan", "address": joint,
		"viewkey": vk, "password": "", "autosave_current": true}, "")
	t2 := time.Now()
	rr := s.w("refresh", map[string]interface{}{"start_height": 0}, "")
	s.fact("refresh of a new view-only wallet over the whole %d-block chain: %s (blocks_fetched %d)", s.chainHeight(), time.Since(t2).Round(time.Millisecond), u64(rr, "blocks_fetched"))
	s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "")

	// S12 — xmrswap's newer calls: set_daemon (untrusted, its own monerod) and password-protected wallet files
	s.logf("\n== S12 set_daemon {address, trusted:false} and wallet passwords")
	daemonAddr := strings.TrimPrefix(strings.TrimPrefix(*daemon, "http://"), "https://")
	setD := func(addr string) map[string]interface{} {
		return map[string]interface{}{"address": addr, "trusted": false}
	}
	s.werr("set_daemon", setD(daemonAddr), "set_daemon(no wallet)")
	pw := "pw-" + tag
	s.w("create_wallet", map[string]interface{}{"filename": tag + "-pw", "password": pw, "language": "English"}, "")
	s.w("set_daemon", setD(daemonAddr), "set_daemon")
	s.w("refresh", map[string]interface{}{"start_height": 0}, "")
	gd := u64(s.w("get_height", nil, ""), "height")
	s.check(gd == s.chainHeight(), "after set_daemon (untrusted) the wallet refreshes to the daemon's height %d", gd)
	s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "")
	s.werr("open_wallet", map[string]interface{}{"filename": tag + "-pw", "password": "wrong"}, "open_wallet(wrong password)")
	s.w("open_wallet", map[string]interface{}{"filename": tag + "-pw", "password": pw}, "")
	// does the daemon a companion names survive a wallet switch? point at a dead port, switch, refresh
	dead := "127.0.0.1:1"
	s.w("set_daemon", setD(dead), "")
	_, e1, _, _ := s.call(s.wallet, "refresh", map[string]interface{}{"start_height": 0})
	s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "")
	s.w("open_wallet", map[string]interface{}{"filename": tag + "-pw", "password": pw}, "")
	_, e2, _, _ := s.call(s.wallet, "refresh", map[string]interface{}{"start_height": 0})
	s.w("generate_from_keys", map[string]interface{}{"restore_height": s.chainHeight(), "filename": tag + "-pw-view", "address": joint,
		"viewkey": vk, "password": pw, "autosave_current": true}, "")
	_, e3, _, _ := s.call(s.wallet, "refresh", map[string]interface{}{"start_height": 0})
	desc := func(e *rpcErr) string {
		if e == nil {
			return "refresh works (the rpc's own --daemon-address is back)"
		}
		return fmt.Sprintf("refresh fails %d %q (the named daemon still applies)", e.Code, e.Message)
	}
	s.fact("set_daemon to a dead %s: on that wallet %s; after close_wallet + open_wallet %s; on a wallet then made by generate_from_keys %s",
		dead, desc(e1), desc(e2), desc(e3))
	s.w("set_daemon", setD(daemonAddr), "") // leave the rpc pointed at the real daemon
	s.w("refresh", map[string]interface{}{"start_height": 0}, "")
	s.w("close_wallet", map[string]interface{}{"autosave_current": true}, "")

	// summary
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
		s.logf("    %-36s %d %q", k, s.errs[k].Code, s.errs[k].Message)
	}
	if *shapesPath != "" {
		b, _ := json.MarshalIndent(map[string]interface{}{"shapes": s.shapes, "errors": s.errs}, "", "  ")
		if err := os.WriteFile(*shapesPath, b, 0o644); err != nil {
			s.check(false, "write %s: %v", *shapesPath, err)
		}
	}
	if s.fails > 0 {
		s.logf("RESULT: %d check(s) FAILED", s.fails)
		os.Exit(1)
	}
	s.logf("RESULT: every check passed")
}
