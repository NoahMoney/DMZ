// simvault drives an end-to-end wrap/unwrap on the DERO simulator with a simulated foreign chain:
//
//	go run ./simvault -install [-winmax N -wdelay N]   # install vault.bas (2-of-3), add signers, bond, optional SetLimits, seal, register the user
//	go run ./simvault -vault <scid> -deposit 0:50000    # the user (index 0) "sends" 50000 foreign units (the default cap is 100000); a fake txid is recorded
//	go run ./simvault -vault <scid> -advance 3    # the foreign chain moves 3 blocks
//	go run ./vaultsigner -wallet 127.0.0.1:30003 -vault <scid> -once   (and again with 30004)
//	go run ./simvault -vault <scid> -burn 40000:4Axmr...   # the user burns wrapped units to withdraw
//	go run ./simvault -vault <scid> -status
//
// Wallets: owner 30007, treasury 30002, signers 30003/30004/30005, user 30006.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	rpc "veil/tools/simrpc"
)

var (
	daemon   = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	install  = flag.Bool("install", false, "install and set up the vault")
	vault    = flag.String("vault", "", "vault scid")
	deposit  = flag.String("deposit", "", "index:amount — record a foreign deposit for a user index")
	advance  = flag.Int64("advance", 0, "advance the simulated foreign chain by n blocks")
	burn     = flag.String("burn", "", "amount:dest — the user burns wrapped units to withdraw")
	status   = flag.Bool("status", false, "print vault state")
	simPath  = flag.String("sim", "../research/crosschain/poc/sim-foreign.json", "sim foreign chain file")
	contract = flag.String("contract", "../contracts/vault.bas", "vault source")
	cap      = flag.Uint64("cap", 100_000, "initial cap in foreign base units (must be <= 3 x total bonds)")
	minbond  = flag.Uint64("minbond", 20000, "minimum bond per signer (DERO atomic units)")
	minburn  = flag.Uint64("minburn", 1000, "minimum withdrawal in foreign base units")
	wfee     = flag.Uint64("wfee", 100, "withdrawal fee kept by the federation, in foreign base units")
	bigbps   = flag.Uint64("bigbps", 2000, "a withdrawal above this share of the supply (bps) waits bigdelay extra blocks")
	bigdelay = flag.Uint64("bigdelay", 500, "extra blocks a large withdrawal waits before it may be released")
	winmax   = flag.Uint64("winmax", 0, "SetLimits before Seal: most minted over two consecutive 2000-block buckets (0 = no limit)")
	wdelay   = flag.Uint64("wdelay", 0, "SetLimits before Seal: blocks after a burn before a Released vote counts")
	mindep   = flag.Uint64("mindep", 1, "smallest foreign deposit that may ever be minted")
	capratio = flag.Uint64("capratio", 3, "foreign base units of cap each DERO atomic unit of bond supports")
	vout     = flag.Uint64("vout", 0, "output index of the simulated deposit within its transaction")
	halt     = flag.String("halt", "", "operator: two signer wallets vote Halt(want), 1 to stop the vault or 0 to resume it")
	setcap   = flag.Uint64("setcap", 0, "k signer wallets vote SetCap to this value (the contract bounds it by capratio x bondtot)")
	fund     = flag.String("fund", "", "send this many DERO units from the funder wallet to owner, user and signers")
)

const (
	owner    = "127.0.0.1:30007"
	treasury = "127.0.0.1:30002"
	user     = "127.0.0.1:30006"
	funder   = "127.0.0.1:30001" // the richest simulator wallet
)

var signers = []string{"127.0.0.1:30003", "127.0.0.1:30004", "127.0.0.1:30005"}

type simFile struct {
	Height   int64             `json:"height"`
	Deposits []json.RawMessage `json:"deposits"`
	Releases []json.RawMessage `json:"releases"`
}

func loadSim() simFile {
	f := simFile{Height: 100}
	if b, err := os.ReadFile(*simPath); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	if f.Deposits == nil {
		f.Deposits = []json.RawMessage{}
	}
	if f.Releases == nil {
		f.Releases = []json.RawMessage{}
	}
	return f
}
func saveSim(f simFile) {
	b, _ := json.MarshalIndent(f, "", "  ")
	rpc.Must(os.WriteFile(*simPath, b, 0o644))
}

func main() {
	flag.Parse()
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	switch {
	case *halt != "":
		// operator tool: k signer wallets vote Halt(want). A halt is how the federation stops everything; a
		// resume is how it starts again once a human has judged what tripped it.
		if len(*vault) != 64 {
			log.Fatal("-vault required")
		}
		want, _ := strconv.ParseUint(*halt, 10, 64)
		for _, w := range signers[:2] {
			a := rpc.Address(w)
			txid, _, _ := c.Send(w, a, "", *vault, nil, []rpc.Arg{rpc.S("entrypoint", "Halt"), rpc.U("want", want)})
			fmt.Printf("Halt(%d) from %s…, tx %s…\n", want, a[:16], txid[:8])
		}
		fmt.Printf("halted = %d\n", c.GetSC(*vault, false).U("halted"))
	case *setcap > 0:
		// k signers raise the mint cap. The contract still refuses anything above capratio x bondtot, so this
		// cannot lift the bound that ties what may be minted to what is actually at stake.
		if len(*vault) != 64 {
			log.Fatal("-vault required")
		}
		for _, w := range signers[:2] {
			a := rpc.Address(w)
			txid, _, _ := c.Send(w, a, "", *vault, nil, []rpc.Arg{rpc.S("entrypoint", "SetCap"), rpc.U("cap", *setcap)})
			fmt.Printf("SetCap(%d) from %s, tx %s\n", *setcap, a[:16], txid[:8])
		}
		fmt.Printf("cap = %d\n", c.GetSC(*vault, false).U("cap"))
	case *fund != "":
		// plain DERO transfers from the treasury wallet to every wallet the demo uses
		amt, _ := strconv.ParseUint(*fund, 10, 64)
		var xf []rpc.Transfer
		for _, w := range append([]string{owner, user}, signers...) {
			xf = append(xf, rpc.Transfer{Destination: rpc.Address(w), Amount: amt})
		}
		var r struct {
			TXID string `json:"txid"`
		}
		rpc.Must(rpc.Call(funder, "transfer", map[string]interface{}{"transfers": xf, "ringsize": 2}, &r))
		c.WaitTx(r.TXID, funder, owner, user, signers[0], signers[1], signers[2])
		fmt.Printf("funded %d wallets with %d units each, tx %s\n", len(xf), amt, r.TXID)
	case *install:
		doInstall(c)
	case *deposit != "":
		parts := strings.SplitN(*deposit, ":", 2)
		idx, _ := strconv.ParseUint(parts[0], 10, 64)
		amt, _ := strconv.ParseUint(parts[1], 10, 64)
		var raw [32]byte
		rand.Read(raw[:])
		f := loadSim()
		d, _ := json.Marshal(map[string]interface{}{"txid": hex.EncodeToString(raw[:]), "vout": *vout, "index": idx, "amount": amt, "height": f.Height})
		f.Deposits = append(f.Deposits, d)
		saveSim(f)
		fmt.Printf("foreign deposit %s… index %d amount %d at foreign height %d\n", hex.EncodeToString(raw[:])[:8], idx, amt, f.Height)
	case *advance > 0:
		f := loadSim()
		f.Height += *advance
		saveSim(f)
		fmt.Printf("foreign height %d\n", f.Height)
	case *burn != "":
		if len(*vault) != 64 {
			log.Fatal("-vault required")
		}
		parts := strings.SplitN(*burn, ":", 2)
		amt, _ := strconv.ParseUint(parts[0], 10, 64)
		me := rpc.Address(user)
		before := rpc.Balance(user, *vault)
		// No destination on an asset burn, exactly as the app builds it. A random DERO account is not registered
		// in a fresh SCID's balance tree, so naming one makes the wallet refuse with "Account Unregistered" —
		// the driver has to send the transaction the users actually send, or it tests a different system.
		txid, gas, h := c.Send(user, me, "", *vault, []rpc.Transfer{{SCID: *vault, Burn: amt}}, []rpc.Arg{rpc.S("entrypoint", "Burn"), rpc.S("dest", parts[1]), rpc.S("refund", me)})
		fmt.Printf("burn %d → %s: tx %s height %d gas %d; wrapped balance %d → %d\n", amt, parts[1], txid, h, gas.GasCompute, before, rpc.Balance(user, *vault))
	case *status:
		if len(*vault) != 64 {
			log.Fatal("-vault required")
		}
		st := c.GetSC(*vault, false)
		fmt.Printf("k=%d n=%d sealed=%d halted=%d cap=%d sup=%d users=%d withdrawals=%d\n", st.U("k"), st.U("n"), st.U("sealed"), st.U("halted"), st.U("cap"), st.U("sup"), st.U("un"), st.U("wn"))
		for j := uint64(0); j < st.U("un"); j++ {
			js := strconv.FormatUint(j, 10)
			da := "(not attested)"
			if st.Has("da_" + js) {
				da = st.Str("da_" + js)
			}
			fmt.Printf("user %d: raw %s… deposit address %s\n", j, hex.EncodeToString([]byte(st.Str("ua_" + js)))[:12], da)
		}
		for j := uint64(0); j < st.U("wn"); j++ {
			js := strconv.FormatUint(j, 10)
			ftx := ""
			if st.Has("w_" + js + "_ftx") {
				ftx = " ftx " + st.Str("w_" + js + "_ftx")[:16] + "…"
			}
			fmt.Printf("withdrawal %d: %d → %s status %d%s\n", j, st.U("w_"+js+"_v"), st.Str("w_"+js+"_to"), st.U("w_"+js+"_st"), ftx)
		}
		fmt.Printf("user wrapped balance: %d\n", rpc.Balance(user, *vault))
		// bond keys embed the raw signer address, which the RPC returns hex-encoded
		for k, v := range st.StringKeys {
			if b, err := hex.DecodeString(k); err == nil && strings.HasPrefix(string(b), "bond_") {
				fmt.Printf("bond of %s…: %v\n", hex.EncodeToString(b[5:])[:12], v)
			}
		}
	default:
		flag.Usage()
	}
}

func doInstall(c *rpc.Client) {
	code, err := os.ReadFile(*contract)
	rpc.Must(err)
	own := rpc.Address(owner)
	tre := rpc.Address(treasury)
	scid, gas, h := c.Send(owner, own, string(code), "", nil, []rpc.Arg{rpc.U("k", 2), rpc.S("treasury", tre), rpc.U("cap", *cap), rpc.U("minbond", *minbond), rpc.U("minburn", *minburn), rpc.U("wfee", *wfee), rpc.U("mindep", *mindep), rpc.U("regfee", 0), rpc.U("capratio", *capratio), rpc.U("bigbps", *bigbps), rpc.U("bigdelay", *bigdelay)})
	fmt.Printf("vault installed: %s (height %d, storage gas %d)\n", scid, h, gas.GasStorage)
	for _, w := range signers {
		a := rpc.Address(w)
		txid, _, _ := c.Send(owner, own, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "AddSigner"), rpc.S("addr", a)})
		fmt.Printf("signer %s added, tx %s…\n", a[:16], txid[:8])
	}
	for _, w := range signers { // v2: every signer bonds at least minbond before the set can be sealed
		a := rpc.Address(w)
		txid, _, _ := c.Send(w, a, "", scid, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: *minbond}}, []rpc.Arg{rpc.S("entrypoint", "Bond")})
		fmt.Printf("bond %d from %s…, tx %s…\n", *minbond, a[:16], txid[:8])
	}
	if *winmax != 0 || *wdelay != 0 { // limits are owner-set once, before Seal, never again
		txid, _, _ := c.Send(owner, own, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "SetLimits"), rpc.U("winmax", *winmax), rpc.U("wdelay", *wdelay)})
		fmt.Printf("limits set (winmax %d, wdelay %d), tx %s…\n", *winmax, *wdelay, txid[:8])
	}
	txid, _, _ := c.Send(owner, own, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Seal")})
	fmt.Printf("sealed, tx %s…\n", txid[:8])
	u := rpc.Address(user)
	txid, _, _ = c.Send(user, u, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Register")})
	fmt.Printf("user %s… registered (index 0), tx %s…\n", u[:16], txid[:8])
	f := loadSim()
	f.Height = 100
	f.Deposits, f.Releases = []json.RawMessage{}, []json.RawMessage{}
	saveSim(f)
	fmt.Printf("sim foreign chain reset at height %d: %s\nVAULT=%s\n", f.Height, *simPath, scid)
}
