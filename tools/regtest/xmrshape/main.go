// Command xmrshape compares what a REAL monero-wallet-rpc answered (the shapes and refusals tools/regtest/xmrsmoke
// recorded) with what tools/xmrswap's FakeWallet answers to the same requests, and prints every difference: a method
// one side does not serve, a field only one side has, a field whose JSON type differs, an array whose elements
// differ, and every refusal whose code or text differs.
//
// It drives the fake through the same scenario xmrsmoke drives the real wallet through, over HTTP, as the companion
// would. It reads tools/xmrswap (the fake and its key helpers) and never changes it.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"

	"veil/tools/xmrswap"
)

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type recorded struct {
	Shapes map[string]interface{} `json:"shapes"`
	Errors map[string]rpcErr      `json:"errors"`
}

type fakeRun struct {
	url    string
	shapes map[string]interface{}
	errs   map[string]rpcErr
	absent map[string]rpcErr // methods the fake refused where the real wallet succeeded
}

func (f *fakeRun) call(method string, params interface{}) (map[string]interface{}, *rpcErr) {
	req := map[string]interface{}{"jsonrpc": "2.0", "id": "0", "method": method}
	if params != nil {
		req["params"] = params
	}
	body, _ := json.Marshal(req)
	resp, err := http.Post(f.url+"/json_rpc", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, &rpcErr{Code: 0, Message: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Result map[string]interface{} `json:"result"`
		Error  *rpcErr                `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil {
		return nil, &rpcErr{Code: 0, Message: fmt.Sprintf("http %d: %.100s", resp.StatusCode, raw)}
	}
	return env.Result, env.Error
}

// ok records a success shape under variant (or, if the fake refuses, notes the refusal).
func (f *fakeRun) ok(method string, params interface{}, variant string) map[string]interface{} {
	r, e := f.call(method, params)
	if e != nil {
		if variant != "" {
			f.absent[variant] = *e
		}
		return nil
	}
	if variant != "" {
		if _, seen := f.shapes[variant]; !seen {
			f.shapes[variant] = shapeOf(r)
		}
	}
	return r
}

// refuse records the fake's answer to a request the real wallet refused.
func (f *fakeRun) refuse(method string, params interface{}, scenario string) {
	r, e := f.call(method, params)
	if e != nil {
		f.errs[scenario] = *e
		return
	}
	b, _ := json.Marshal(r)
	f.errs[scenario] = rpcErr{Code: 1, Message: "ACCEPTED by the fake: " + string(b)}
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

func key(net xmrswap.Network) (xmrswap.Scalar, xmrswap.Scalar, string) {
	s, err := xmrswap.NewScalar(rand.Reader)
	if err != nil {
		panic(err)
	}
	v, err := xmrswap.NewScalar(rand.Reader)
	if err != nil {
		panic(err)
	}
	a, err := xmrswap.Address(s.Public(), v.Public(), net)
	if err != nil {
		panic(err)
	}
	return s, v, a
}

// drive runs xmrsmoke's scenario against the fake.
func drive(fw *xmrswap.FakeWallet, f *fakeRun) {
	const xmr = uint64(1_000_000_000_000)
	fs, fv, fund := key(xmrswap.Mainnet)
	js, jv, joint := key(xmrswap.Mainnet)
	_, rv, recv := key(xmrswap.Mainnet)
	_ = rv
	fw.SetScanLag(true) // the real server's behaviour, which the smoke measures

	f.ok("get_version", nil, "get_version")
	f.ok("create_wallet", map[string]interface{}{"filename": "fund", "password": "", "language": "English"}, "create_wallet")
	// the fake has no create_wallet: a spendable wallet is made from keys instead
	f.ok("generate_from_keys", map[string]interface{}{"restore_height": 0, "filename": "fund", "address": fund, "viewkey": fv.Hex(), "spendkey": fs.Hex(), "password": "", "autosave_current": true}, "")
	f.ok("get_address", map[string]interface{}{"account_index": 0}, "get_address")
	f.ok("get_height", nil, "get_height")
	fw.Fund(fund, 100*xmr)
	fw.Mine(1)
	f.ok("refresh", map[string]interface{}{"start_height": 0}, "refresh")
	f.ok("get_balance", map[string]interface{}{"account_index": 0}, "get_balance")
	fw.Mine(60)
	f.ok("open_wallet", map[string]interface{}{"filename": "fund", "password": ""}, "open_wallet")
	restore := fw.Height()
	f.ok("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr, "address": joint}}, "account_index": 0}, "transfer")
	f.ok("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr / 2, "address": recv}}, "account_index": 0}, "")
	f.ok("generate_from_keys", map[string]interface{}{"restore_height": restore, "filename": "view", "address": joint, "viewkey": jv.Hex(), "password": "", "autosave_current": true}, "generate_from_keys(view)")
	f.ok("refresh", map[string]interface{}{"start_height": restore}, "")
	f.ok("get_transfers", map[string]interface{}{"in": true, "pool": true, "account_index": 0}, "get_transfers(pool)")
	fw.Mine(11)
	f.ok("get_transfers", map[string]interface{}{"in": true, "filter_by_height": true, "min_height": restore - 1, "account_index": 0}, "get_transfers")
	f.ok("get_transfers", map[string]interface{}{"in": true, "filter_by_height": true, "min_height": restore - 1, "account_index": 0}, "get_transfers(view)")
	f.ok("get_balance", map[string]interface{}{"account_index": 0}, "get_balance(view)")
	f.refuse("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr / 10, "address": fund}}, "account_index": 0}, "transfer(view-only)")
	f.refuse("sweep_all", map[string]interface{}{"address": fund, "account_index": 0}, "sweep_all(view-only)")
	f.ok("close_wallet", map[string]interface{}{"autosave_current": true}, "close_wallet")
	f.ok("generate_from_keys", map[string]interface{}{"restore_height": restore, "filename": "sweep", "address": joint, "viewkey": jv.Hex(), "spendkey": js.Hex(), "password": "", "autosave_current": true}, "generate_from_keys(spend)")
	f.ok("refresh", map[string]interface{}{"start_height": restore}, "")
	f.ok("sweep_all", map[string]interface{}{"address": fund, "account_index": 0}, "sweep_all")
	f.refuse("sweep_all", map[string]interface{}{"address": fund, "account_index": 0}, "sweep_all(nothing unlocked)")
	f.refuse("generate_from_keys", map[string]interface{}{"restore_height": restore, "filename": "sweep", "address": joint, "viewkey": jv.Hex(), "password": "", "autosave_current": true}, "generate_from_keys(exists)")
	other, _ := xmrswap.NewScalar(rand.Reader)
	f.refuse("generate_from_keys", map[string]interface{}{"restore_height": restore, "filename": "badview", "address": joint, "viewkey": other.Hex(), "password": "", "autosave_current": true}, "generate_from_keys(view mismatch)")
	f.refuse("generate_from_keys", map[string]interface{}{"restore_height": restore, "filename": "badspend", "address": joint, "viewkey": jv.Hex(), "spendkey": other.Hex(), "password": "", "autosave_current": true}, "generate_from_keys(spend mismatch)")
	f.ok("close_wallet", map[string]interface{}{"autosave_current": true}, "")
	f.refuse("get_address", map[string]interface{}{"account_index": 0}, "get_address(no wallet)")
	f.refuse("get_balance", map[string]interface{}{"account_index": 0}, "get_balance(no wallet)")
	f.refuse("get_transfers", map[string]interface{}{"in": true, "account_index": 0}, "get_transfers(no wallet)")
	f.refuse("refresh", map[string]interface{}{"start_height": 0}, "refresh(no wallet)")
	f.refuse("close_wallet", map[string]interface{}{"autosave_current": true}, "close_wallet(no wallet)")
	f.refuse("get_height", nil, "get_height(no wallet)")
	f.refuse("open_wallet", map[string]interface{}{"filename": "missing", "password": ""}, "open_wallet(missing)")
	f.refuse("set_daemon", map[string]interface{}{"address": "127.0.0.1:28081", "trusted": false}, "set_daemon(no wallet)")
	f.refuse("open_wallet", map[string]interface{}{"filename": "fund", "password": "wrong"}, "open_wallet(wrong password)")
	f.ok("open_wallet", map[string]interface{}{"filename": "fund", "password": ""}, "")
	f.ok("set_daemon", map[string]interface{}{"address": "127.0.0.1:28081", "trusted": false}, "set_daemon")
	_, _, stagenet := key(xmrswap.Stagenet)
	f.refuse("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr, "address": "4notanaddress"}}, "account_index": 0}, "transfer(bad address)")
	f.refuse("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": xmr, "address": stagenet}}, "account_index": 0}, "transfer(stagenet address)")
	f.refuse("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 0, "address": recv}}, "account_index": 0}, "transfer(zero amount)")
	f.refuse("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 1_000_000 * xmr, "address": recv}}, "account_index": 0}, "transfer(more than the balance)")
	f.refuse("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 3 * xmr / 10, "address": recv}}, "account_index": 0, "unlock_time": fw.Height() + 50}, "transfer(unlock_time)")
	// not enough unlocked: a wallet whose only money arrived a block ago
	ns, nv, naddr := key(xmrswap.Mainnet)
	f.ok("generate_from_keys", map[string]interface{}{"restore_height": fw.Height(), "filename": "fresh", "address": naddr, "viewkey": nv.Hex(), "spendkey": ns.Hex(), "password": "", "autosave_current": true}, "")
	f.ok("refresh", map[string]interface{}{"start_height": 0}, "")
	fw.Fund(naddr, xmr)
	fw.Mine(1)
	f.refuse("transfer", map[string]interface{}{"destinations": []map[string]interface{}{{"amount": 7 * xmr / 10, "address": fund}}, "account_index": 0}, "transfer(not enough unlocked)")
}

// diff walks two shapes and reports every difference under path.
func diff(path string, real, fake interface{}, out *[]string) {
	rm, rok := real.(map[string]interface{})
	fm, fok := fake.(map[string]interface{})
	if rok && fok {
		keys := map[string]bool{}
		for k := range rm {
			keys[k] = true
		}
		for k := range fm {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			rv, rin := rm[k]
			fv, fin := fm[k]
			p := path + "." + k
			switch {
			case rin && !fin:
				*out = append(*out, fmt.Sprintf("%-44s only in the REAL wallet (%s)", p, brief(rv)))
			case !rin && fin:
				*out = append(*out, fmt.Sprintf("%-44s only in the FAKE (%s)", p, brief(fv)))
			default:
				diff(p, rv, fv, out)
			}
		}
		return
	}
	ra, raok := real.([]interface{})
	fa, faok := fake.([]interface{})
	if raok && faok {
		switch {
		case len(ra) == 0 && len(fa) == 0:
		case len(ra) == 0:
			*out = append(*out, fmt.Sprintf("%-44s empty in the real run, elements %s in the fake", path+"[]", brief(fa[0])))
		case len(fa) == 0:
			*out = append(*out, fmt.Sprintf("%-44s elements %s in the real run, empty in the fake", path+"[]", brief(ra[0])))
		default:
			diff(path+"[]", ra[0], fa[0], out)
		}
		return
	}
	if brief(real) != brief(fake) {
		*out = append(*out, fmt.Sprintf("%-44s type: real %s, fake %s", path, brief(real), brief(fake)))
	}
}

func brief(v interface{}) string {
	switch t := v.(type) {
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return t
	}
	return fmt.Sprint(v)
}

func main() {
	realPath := flag.String("real", "", "the shapes file xmrsmoke wrote")
	flag.Parse()
	b, err := os.ReadFile(*realPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var real recorded
	if err := json.Unmarshal(b, &real); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fw := xmrswap.NewFakeWallet(xmrswap.Mainnet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	f := &fakeRun{url: srv.URL, shapes: map[string]interface{}{}, errs: map[string]rpcErr{}, absent: map[string]rpcErr{}}
	drive(fw, f)

	variants := map[string]bool{}
	for k := range real.Shapes {
		variants[k] = true
	}
	for k := range f.shapes {
		variants[k] = true
	}
	for k := range f.absent {
		variants[k] = true
	}
	names := make([]string, 0, len(variants))
	for k := range variants {
		names = append(names, k)
	}
	sort.Strings(names)
	total := 0
	fmt.Println("response shapes (success answers), real monero-wallet-rpc vs tools/xmrswap FakeWallet:")
	for _, v := range names {
		rs, rin := real.Shapes[v]
		fsh, fin := f.shapes[v]
		switch {
		case rin && !fin:
			if e, ok := f.absent[v]; ok {
				fmt.Printf("  %-28s the fake does not serve it: %d %q\n", v, e.Code, e.Message)
			} else {
				fmt.Printf("  %-28s answered by the real wallet, not exercised on the fake\n", v)
			}
			total++
		case !rin && fin:
			fmt.Printf("  %-28s answered by the fake, not recorded from the real wallet\n", v)
			total++
		case rin && fin:
			var d []string
			diff("", rs, fsh, &d)
			if len(d) == 0 {
				fmt.Printf("  %-28s same shape\n", v)
				continue
			}
			fmt.Printf("  %-28s %d difference(s):\n", v, len(d))
			for _, x := range d {
				fmt.Printf("      %s\n", strings.TrimPrefix(x, "."))
			}
			total += len(d)
		}
	}
	fmt.Println("refusals, real vs fake (code and message):")
	scen := map[string]bool{}
	for k := range real.Errors {
		scen[k] = true
	}
	for k := range f.errs {
		scen[k] = true
	}
	snames := make([]string, 0, len(scen))
	for k := range scen {
		snames = append(snames, k)
	}
	sort.Strings(snames)
	for _, k := range snames {
		re, rin := real.Errors[k]
		fe, fin := f.errs[k]
		switch {
		case rin && fin && re.Code == fe.Code && re.Message == fe.Message:
			fmt.Printf("  %-34s same: %d %q\n", k, re.Code, re.Message)
		case rin && fin && re.Code == fe.Code:
			fmt.Printf("  %-34s same code %d; text differs: real %q, fake %q\n", k, re.Code, re.Message, fe.Message)
			total++
		case rin && fin:
			fmt.Printf("  %-34s DIFFERENT: real %d %q, fake %d %q\n", k, re.Code, re.Message, fe.Code, fe.Message)
			total++
		case rin:
			fmt.Printf("  %-34s real %d %q; not exercised on the fake\n", k, re.Code, re.Message)
		default:
			fmt.Printf("  %-34s fake %d %q; not recorded from the real wallet\n", k, fe.Code, fe.Message)
		}
	}
	fmt.Printf("%d difference(s) in all\n", total)
}
