// simdrive sends the transactions tools/verifydeploy's end-to-end run needs, on a DERO SIMULATOR and nowhere else:
// it refuses any daemon that does not call itself the simulator. It is test scaffolding, not a deployment tool.
//
//	go run ./verifydeploy/simdrive install    -wallet 127.0.0.1:30019 -src ../contracts/htlc.bas [-arg k=U:1]... [-ring 2]
//	go run ./verifydeploy/simdrive installraw -wallet 127.0.0.1:30019 -code <file>   # the file's bytes, not reference.Canon
//	go run ./verifydeploy/simdrive call       -wallet 127.0.0.1:30017 -scid <scid> -entry Seal [-arg ...] [-burn N] [-fee N]
//	go run ./verifydeploy/simdrive estimate   -wallet 127.0.0.1:30021 -scid <scid> -entry UpdateCode [-arg ...]
//	go run ./verifydeploy/simdrive getsc      -scid <scid> [-keys owner,commit,hash] [-code <file to write the code to>]
//	go run ./verifydeploy/simdrive addr       -wallet 127.0.0.1:30018
//
// -arg name=U:<uint64> | name=S:<string> | name=H:<hex> | name=W:<wallet rpc> (that wallet's address, as a string)
// | name=F:<file> (the file's contents, as a string). -fee sends a call without the daemon's estimate.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	rpc "veil/tools/simrpc"
	"veil/tools/verifydeploy/srchash"
)

type argList []rpc.Arg

func (a *argList) String() string { return fmt.Sprint(*a) }

func (a *argList) Set(s string) error {
	p := strings.SplitN(s, "=", 2)
	if len(p) != 2 || len(p[1]) < 2 || p[1][1] != ':' {
		return fmt.Errorf("-arg %q: want name=T:value", s)
	}
	name, typ, val := p[0], p[1][0], p[1][2:]
	switch typ {
	case 'U':
		n, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return err
		}
		*a = append(*a, rpc.U(name, n))
	case 'S':
		*a = append(*a, rpc.S(name, val))
	case 'H':
		*a = append(*a, rpc.H(name, val))
	case 'W':
		*a = append(*a, rpc.S(name, rpc.Address(val)))
	case 'F':
		b, err := os.ReadFile(val)
		if err != nil {
			return err
		}
		*a = append(*a, rpc.S(name, string(b)))
	default:
		return fmt.Errorf("-arg %q: type must be U, S, H, W or F", s)
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("simdrive install|installraw|call|estimate|getsc|addr [flags]")
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	daemon := fs.String("daemon", "127.0.0.1:20000", "simulator daemon rpc")
	wallet := fs.String("wallet", "", "wallet rpc host:port")
	src := fs.String("src", "", "contract source: installed as reference.Canon(src), as tools/simdeploy does")
	codeFile := fs.String("code", "", "installraw: the exact code to install; getsc: where to write the installed code")
	scid := fs.String("scid", "", "contract to call or read")
	entry := fs.String("entry", "", "entrypoint to call")
	ring := fs.Int("ring", 2, "ring size of the transaction")
	burn := fs.Uint64("burn", 0, "DERO to send into the call (DEROVALUE())")
	fee := fs.Uint64("fee", 0, "send the call with this fee and no daemon estimate")
	keys := fs.String("keys", "", "getsc: comma-separated keys to print (default all)")
	var args argList
	fs.Var(&args, "arg", "name=T:value (repeatable)")
	fs.Parse(os.Args[2:])

	var info struct {
		Network string `json:"network"`
		Testnet bool   `json:"testnet"`
		Height  int64  `json:"height"`
	}
	rpc.Must(rpc.Call(*daemon, "DERO.GetInfo", nil, &info))
	if !info.Testnet || !strings.Contains(strings.ToLower(info.Network), "simulator") {
		log.Fatalf("simdrive runs against the DERO simulator only; %s reports %q (testnet=%v)", *daemon, info.Network, info.Testnet)
	}
	c := &rpc.Client{Daemon: *daemon, Timeout: 240 * time.Second, RingSize: *ring}
	signer := func() string {
		if *wallet == "" {
			log.Fatal("-wallet required")
		}
		return rpc.Address(*wallet)
	}
	var xfers []rpc.Transfer
	if *burn > 0 {
		xfers = []rpc.Transfer{{Destination: c.RandomAddress(), Burn: *burn}}
	}
	switch cmd {
	case "addr":
		fmt.Println(signer())
	case "install", "installraw":
		var code, what string
		if cmd == "install" {
			b, err := os.ReadFile(*src)
			rpc.Must(err)
			code, what = srchash.Canon(string(b)), "reference.Canon("+*src+")"
		} else {
			b, err := os.ReadFile(*codeFile)
			rpc.Must(err)
			code, what = string(b), *codeFile+" as is"
		}
		id, gas, h := c.Send(*wallet, signer(), code, "", xfers, args)
		fmt.Printf("installed %s from %s at ring %d: scid %s height %d storage gas %d code sha256 %s\n", what, *wallet, *ring, id, h, gas.GasStorage, srchash.Sum(code))
	case "call", "estimate":
		if *scid == "" || *entry == "" {
			log.Fatal("-scid and -entry required")
		}
		call := append(argList{rpc.S("entrypoint", *entry)}, args...)
		if cmd == "estimate" {
			g, err := c.Estimate(signer(), "", *scid, xfers, call)
			if err != nil {
				fmt.Printf("DERO.GetGasEstimate %s.%s from %s: refused: %v\n", short(*scid), *entry, *wallet, err)
				return
			}
			fmt.Printf("DERO.GetGasEstimate %s.%s from %s: compute %d storage %d\n", short(*scid), *entry, *wallet, g.GasCompute, g.GasStorage)
			return
		}
		if *fee > 0 {
			tx, h := c.SendWithFee(*wallet, signer(), *scid, xfers, call, *fee)
			fmt.Printf("sent %s.%s from %s at ring %d with fee %d (no estimate): tx %s mined at height %d\n", short(*scid), *entry, *wallet, *ring, *fee, tx, h)
			return
		}
		tx, gas, h := c.Send(*wallet, signer(), "", *scid, xfers, call)
		fmt.Printf("called %s.%s from %s at ring %d: tx %s height %d gas compute %d storage %d\n", short(*scid), *entry, *wallet, *ring, tx, h, gas.GasCompute, gas.GasStorage)
	case "getsc":
		getsc(*daemon, *scid, *keys, *codeFile)
	default:
		log.Fatalf("unknown command %q", cmd)
	}
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}

// getsc prints the raw DERO.GetSC answer for the chosen keys: the daemon's hex for strings (and its decoding), the
// number for uint64s, and the code's sha256.
func getsc(daemon, scid, keys, codeOut string) {
	var r struct {
		Code       string                     `json:"code"`
		StringKeys map[string]json.RawMessage `json:"stringkeys"`
		Uint64Keys map[string]json.RawMessage `json:"uint64keys"`
	}
	rpc.Must(rpc.Call(daemon, "DERO.GetSC", map[string]interface{}{"scid": scid, "code": true, "variables": true}, &r))
	fmt.Printf("DERO.GetSC %s: code %d bytes sha256 %s\n", scid, len(r.Code), srchash.Sum(r.Code))
	if codeOut != "" {
		rpc.Must(os.WriteFile(codeOut, []byte(r.Code), 0o644))
	}
	want := map[string]bool{}
	for _, k := range strings.Split(keys, ",") {
		if k = strings.TrimSpace(k); k != "" {
			want[k] = true
		}
	}
	show := func(prefix string, m map[string]json.RawMessage) {
		ks := make([]string, 0, len(m))
		for k := range m {
			if len(want) == 0 || want[k] {
				ks = append(ks, k)
			}
		}
		sort.Strings(ks)
		for _, k := range ks {
			raw := string(m[k])
			line := fmt.Sprintf("  %s%-14s %s", prefix, k, raw)
			var s string
			if json.Unmarshal(m[k], &s) == nil {
				if b, err := hex.DecodeString(s); err == nil && isPrintable(b) {
					line += fmt.Sprintf("  = %q", string(b))
				}
			}
			fmt.Println(line)
		}
	}
	show("", r.StringKeys)
	show("uint64 key ", r.Uint64Keys)
}

func isPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
