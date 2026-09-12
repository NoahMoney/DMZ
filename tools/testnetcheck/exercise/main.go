// exercise drives DMZ's contracts on the PRIVATE DERO testnet for W-01 / MT-F05, and only there: it refuses the
// simulator's port, any daemon that is not a testnet, and any chain whose block 0 is not -genesis. Unlike
// tools/testnetcheck it SENDS transactions, from the testnet wallets named by -lp and -trader.
//
//	-mode market   launch.bas install + RegisterLaunch + one launch buy; pool.bas install + RegisterPool + AddLiquidity
//	               + three trades. Every step is compared with veil/reference.
//	-mode htlc     htlc.bas v3: lock A, claimed by the taker at once; lock B left for the maker's Refund in [t1, t2);
//	               lock C left for Punish in [t2, t2 + g2). B and C go into -state for -mode wait.
//	-mode swap     swap.bas: lock 1, claimed by the taker with the secret; lock 2 left to expire, then refunded.
//	-mode board    board.bas: offer 0 posted and cancelled; offer 1 posted and accepted by the taker.
//	-mode social   social.bas Profile, chat.bas Post, founders.bas Mint(1) + Register.
//	-mode wait     sends each exit in -state once its window opens (htlc Refund, htlc Punish, swap Refund), from the
//	               -trader wallet. Runs for days, detached; a transient RPC error is logged and retried, never fatal.
//
// Every transaction is printed as "TX <step> <txid> height <h>", and every lock or market the daily check must read
// as "RECORD <json>" (research/testnet/exercised.json). The maker's Refund secret of an htlc lock is written to -state
// only, which lives in the git-ignored .testnet/ and is never printed.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	ref "veil/reference"
	"veil/tools/netcfg"
	rpc "veil/tools/simrpc"
)

var (
	daemon    = flag.String("daemon", "127.0.0.1:40402", "testnet daemon rpc")
	genesis   = flag.String("genesis", "44292f0c65e41c141995df75df8e6826ea1d3e04cedd0702b090e14649a5b267", "block 0 of the private testnet; any other chain is refused")
	networks  = flag.String("networks", "../deploy/networks.json", "deployment record (testnet block)")
	contracts = flag.String("contracts", "../contracts", "contract sources")
	stateFile = flag.String("state", "../.testnet/exercise-state.json", "scheduled exits (git-ignored: holds an htlc maker's Refund secret)")
	lpW       = flag.String("lp", "127.0.0.1:40405", "creator / liquidity / taker wallet rpc (the deployer)")
	traderW   = flag.String("trader", "127.0.0.1:40406", "trader / maker wallet rpc")
	mode      = flag.String("mode", "", "market | htlc | swap | board | social | wait")
	amount    = flag.Uint64("amount", 1000, "atomic units per swap/htlc lock (0.01 DERO)")
	fee       = flag.Uint64("fee", 5000, "fee for calls the estimator cannot run (height-gated exits)")
	timeout   = flag.Duration("timeout", 45*time.Minute, "per-tx timeout")
)

func tx(step, txid string, h int64) {
	fmt.Printf("TX %-28s %s height %d  %s\n", step, txid, h, time.Now().UTC().Format(time.RFC3339))
}

func record(v interface{}) { b, _ := json.Marshal(v); fmt.Printf("RECORD %s\n", b) }

// secret returns 32 random bytes as 64 hex characters and the hex of their sha256, as the contracts hash them.
func secret() (hexs, hash string) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		log.Fatal(err)
	}
	h := sha256.Sum256(b[:])
	return hex.EncodeToString(b[:]), hex.EncodeToString(h[:])
}

func readSrc(name string) string {
	b, err := os.ReadFile(*contracts + "/" + name)
	rpc.Must(err)
	return ref.Canon(string(b))
}

func zero() string { return strings.Repeat("0", 64) }

// guard refuses anything but the private testnet: never the simulator, never mainnet, never another chain.
func guard() error {
	if strings.HasSuffix(*daemon, ":20000") {
		return errors.New("refusing the simulator port")
	}
	var info struct {
		Testnet bool   `json:"testnet"`
		Network string `json:"network"`
	}
	if err := rpc.Call(*daemon, "DERO.GetInfo", nil, &info); err != nil {
		return err
	}
	if !info.Testnet || strings.EqualFold(info.Network, "simulator") {
		return fmt.Errorf("daemon %s reports testnet=%v network %q: this driver runs on the private testnet only", *daemon, info.Testnet, info.Network)
	}
	var g struct {
		Header struct {
			Hash string `json:"hash"`
		} `json:"block_header"`
	}
	if err := rpc.Call(*daemon, "DERO.GetBlockHeaderByTopoHeight", map[string]interface{}{"topoheight": 0}, &g); err != nil {
		return err
	}
	if g.Header.Hash != *genesis {
		return fmt.Errorf("block 0 is %s, not the private testnet's %s", g.Header.Hash, *genesis)
	}
	return nil
}

func main() {
	flag.Parse()
	log.SetFlags(0)
	http.DefaultClient.Timeout = 2 * time.Minute // simrpc posts with the default client
	if err := guard(); err != nil {
		log.Fatal(err)
	}
	c := &rpc.Client{Daemon: *daemon, Timeout: *timeout}
	f, err := netcfg.Load(*networks)
	rpc.Must(err)
	n := f.Networks["testnet"]
	if n == nil || n.Factory == "" {
		log.Fatal("no testnet block with a factory in " + *networks)
	}
	fmt.Printf("daemon %s height %d  factory %s  htlc %s  swap %s  board %s\n", *daemon, c.Height(), n.Factory, n.Htlc, n.Swap, n.Board)
	switch *mode {
	case "market":
		market(c, n)
	case "htlc":
		htlc(c, n.Htlc)
	case "swap":
		swap(c, n.Swap)
	case "board":
		board(c, n.Board)
	case "social":
		social(c, n)
	case "wait":
		wait(c)
	default:
		log.Fatal("-mode market | htlc | swap | board | social | wait")
	}
}

// ---- scheduled exits -------------------------------------------------------------------------------------------

type exit struct {
	Kind   string `json:"kind"` // htlc-refund | htlc-punish | swap-refund
	Scid   string `json:"scid"`
	ID     uint64 `json:"id"`
	Opens  uint64 `json:"opens"`
	Closes uint64 `json:"closes"` // 0: no end
	Sb     string `json:"sb,omitempty"`
	Txid   string `json:"txid,omitempty"`
	Height int64  `json:"height,omitempty"`
	State  uint64 `json:"state,omitempty"`
	Missed bool   `json:"missed,omitempty"`
}

func loadExits() []exit {
	var xs []exit
	if b, err := os.ReadFile(*stateFile); err == nil {
		rpc.Must(json.Unmarshal(b, &xs))
	} else if !os.IsNotExist(err) {
		log.Fatal(err)
	}
	return xs
}

func saveExits(xs []exit) {
	b, err := json.MarshalIndent(xs, "", "  ")
	rpc.Must(err)
	tmp := *stateFile + ".tmp"
	rpc.Must(os.WriteFile(tmp, append(b, '\n'), 0o600))
	rpc.Must(os.Rename(tmp, *stateFile))
}

func schedule(x exit) {
	xs := loadExits()
	saveExits(append(xs, x))
	fmt.Printf("  scheduled %s of %s… lock %d from height %d (window end %d) in %s\n", x.Kind, x.Scid[:12], x.ID, x.Opens, x.Closes, *stateFile)
}

// ---- markets ---------------------------------------------------------------------------------------------------

func checkLaunch(step string, st rpc.SCState, m *ref.Launch) {
	fields := map[string]uint64{"phase": m.Phase, "sold": m.Sold, "real": m.Real, "rd": m.RD, "ra": m.RA, "fd": m.FD, "fa": m.FA,
		"tacc": m.TAcc, "rpool": m.RPool, "locked": m.Locked, "stot": m.STot, "sacc": m.SAcc, "tc": m.TC, "pc": m.PC}
	var diff []string
	for k, want := range fields {
		if got := st.U0(k); got != want {
			diff = append(diff, fmt.Sprintf("%s chain %d model %d", k, got, want))
		}
	}
	verdict := "matches model"
	if len(diff) > 0 {
		verdict = "DIFFERS from model: " + strings.Join(diff, "; ")
	}
	fmt.Printf("  launch %-12s phase=%d rd=%d ra=%d sold=%d real=%d fd=%d tc=%d  %s\n", step, st.U0("phase"), st.U0("rd"), st.U0("ra"), st.U0("sold"), st.U0("real"), st.U0("fd"), st.U0("tc"), verdict)
}

func checkPool(step string, st rpc.SCState, m *ref.Pool) {
	got := [5]uint64{st.U0("rd"), st.U0("ra"), st.U0("lp"), st.U0("fd"), st.U0("fa")}
	want := [5]uint64{m.RD, m.RA, m.LP, m.FD, m.FA}
	verdict := "matches model"
	if got != want {
		verdict = fmt.Sprintf("DIFFERS from model %v", want)
	}
	fmt.Printf("  pool %-18s rd/ra/lp/fd/fa=%v tc=%d  %s\n", step, got, st.U0("tc"), verdict)
}

func registered(c *rpc.Client, factory, count, prefix, scid string) {
	st := c.GetSC(factory, false)
	k := st.U0(count)
	for i := uint64(0); i < k; i++ {
		key := fmt.Sprintf("%s%d", prefix, i)
		if st.Has(key) && st.Str(key) == scid {
			fmt.Printf("  registered as %s (%s = %d)\n", key, count, k)
			return
		}
	}
	log.Fatalf("  NOT REGISTERED: %s not among %s0..%d", scid, prefix, k)
}

func market(c *rpc.Client, n *netcfg.Network) {
	lp, tr := rpc.Address(*lpW), rpc.Address(*traderW)
	fst := c.GetSC(n.Factory, false)
	treasury, regfee := fst.Str("tstr"), fst.U("regfee")
	fmt.Printf("lp %s\ntrader %s\ntreasury %s\nregfee %d\n", lp, tr, treasury, regfee)
	pay := []rpc.Transfer{{Destination: c.RandomAddress(), Burn: regfee}}

	fmt.Println("1. launch.bas: DMZ Testnet Coin (DTC), supply 1e9 units, graduation 10 DERO, creator 3%, tax 5% to stakers")
	p := struct{ supply, grad, cbps, tax, tmode, fee, pfee uint64 }{1000000000, 1000000, 300, 500, 3, 50, 5000}
	launch, gas, h := c.Send(*lpW, lp, readSrc("launch.bas"), "", nil, []rpc.Arg{
		rpc.S("name", "DMZ Testnet Coin"), rpc.S("ticker", "DTC"), rpc.S("descr", "private testnet exercise"), rpc.S("icon", ""), rpc.S("tags", "#testnet"), rpc.H("quote", zero()),
		rpc.U("dec", 3), rpc.U("supply", p.supply), rpc.U("grad", p.grad), rpc.U("cbps", p.cbps), rpc.U("tax", p.tax), rpc.U("tmode", p.tmode),
		rpc.S("treasury", treasury), rpc.U("fee", p.fee), rpc.U("pfee", p.pfee)})
	tx("launch install", launch, h)
	fmt.Printf("  launch scid %s gas %d/%d\n", launch, gas.GasCompute, gas.GasStorage)
	m, _ := ref.NewLaunch(p.supply, p.grad, p.cbps, p.tax, p.tmode, p.fee, p.pfee)
	if b := rpc.Balance(*lpW, launch); b != m.Alloc {
		fmt.Printf("  creator allocation in wallet %d, model %d\n", b, m.Alloc)
	} else {
		fmt.Printf("  creator allocation %d units (matches model)\n", b)
	}
	checkLaunch("install", c.GetSC(launch, false), m)

	fmt.Println("2. RegisterLaunch on the factory")
	txid, _, h := c.Send(*lpW, lp, "", n.Factory, pay, []rpc.Arg{rpc.S("entrypoint", "RegisterLaunch"), rpc.H("scid", launch)})
	tx("RegisterLaunch", txid, h)
	registered(c, n.Factory, "lc", "launch_", launch)

	fmt.Println("3. launch buy: trader buys with 1 DERO (published)")
	d := uint64(100000)
	q, _ := m.QuoteBuy(d)
	txid, _, h = c.Send(*traderW, tr, "", launch, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", q), rpc.U("publish", 1)})
	tx("launch buy 1 DERO", txid, h)
	m.Buy(ref.In{D: d}, q, 1, tr, uint64(h), 0)
	checkLaunch("buy", c.GetSC(launch, false), m)
	fmt.Printf("  quoted %d, trader holds %d DTC units\n", q, rpc.Balance(*traderW, launch))

	fmt.Println("4. pool.bas: DTC/DERO, fee 50 bps, protocol share 50%")
	pool, gas, h := c.Send(*lpW, lp, readSrc("pool.bas"), "", nil, []rpc.Arg{rpc.H("asset", launch), rpc.H("quote", zero()), rpc.S("treasury", treasury), rpc.U("fee", 50), rpc.U("pfee", 5000)})
	tx("pool install", pool, h)
	fmt.Printf("  pool scid %s gas %d/%d\n", pool, gas.GasCompute, gas.GasStorage)
	pm := ref.NewPool(50, 5000)
	checkPool("install", c.GetSC(pool, false), pm)

	fmt.Println("5. RegisterPool on the factory")
	txid, _, h = c.Send(*lpW, lp, "", n.Factory, pay, []rpc.Arg{rpc.S("entrypoint", "RegisterPool"), rpc.H("scid", pool)})
	tx("RegisterPool", txid, h)
	registered(c, n.Factory, "pc", "pool_", pool)

	fmt.Println("6. lp adds 2 DERO + 1,000,000 DTC units")
	d, a := uint64(200000), uint64(1000000)
	txid, _, h = c.Send(*lpW, lp, "", pool, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}, {SCID: launch, Burn: a}}, []rpc.Arg{rpc.S("entrypoint", "AddLiquidity"), rpc.U("min_lp", 0)})
	tx("AddLiquidity", txid, h)
	pm.AddLiquidity(ref.In{D: d, A: a}, 0, uint64(h), 0)
	checkPool("AddLiquidity", c.GetSC(pool, false), pm)

	fmt.Println("7. trades: trader buys 0.5 DERO (published), sells 20,000 units, buys 0.2 DERO")
	d = 50000
	q, _ = pm.Quote(d)
	txid, _, h = c.Send(*traderW, tr, "", pool, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", q), rpc.U("publish", 1)})
	tx("pool buy 0.5 DERO", txid, h)
	pm.SwapDeroForAsset(ref.In{D: d}, q, 1, tr, uint64(h), 0)
	checkPool("buy", c.GetSC(pool, false), pm)
	a = 20000
	txid, _, h = c.Send(*traderW, tr, "", pool, []rpc.Transfer{{SCID: launch, Burn: a}}, []rpc.Arg{rpc.S("entrypoint", "SwapAssetForDero"), rpc.U("min_out", 0), rpc.U("publish", 0)})
	tx("pool sell 20000 DTC", txid, h)
	pm.SwapAssetForDero(ref.In{A: a}, 0, 0, tr, uint64(h), 0)
	checkPool("sell", c.GetSC(pool, false), pm)
	d = 20000
	q, _ = pm.Quote(d)
	txid, _, h = c.Send(*traderW, tr, "", pool, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", q), rpc.U("publish", 0)})
	tx("pool buy 0.2 DERO", txid, h)
	pm.SwapDeroForAsset(ref.In{D: d}, q, 0, tr, uint64(h), 0)
	checkPool("buy", c.GetSC(pool, false), pm)
	record(map[string]interface{}{"kind": "launch", "scid": launch, "minTrades": 1})
	record(map[string]interface{}{"kind": "pool", "scid": pool, "minTrades": 1})
	fmt.Printf("MARKET DONE launch %s pool %s\n", launch, pool)
}

// ---- htlc.bas v3 -----------------------------------------------------------------------------------------------

func htlc(c *rpc.Client, scid string) {
	if scid == "" {
		log.Fatal("no htlc in the testnet record")
	}
	st := c.GetSC(scid, false)
	if st.U("v") != 3 {
		log.Fatalf("htlc %s is v%d; this driver speaks v3", scid, st.U("v"))
	}
	w1, w2, g2 := st.U("w1min"), st.U("w2min"), st.U("g2")
	mk, tk := rpc.Address(*traderW), rpc.Address(*lpW)
	fmt.Printf("maker %s\ntaker %s\nw1min %d w2min %d g2 %d\n", mk, tk, w1, w2, g2)
	// derohe mines a transaction at most 10 blocks after it is built, and Lock refuses t1 < its height + w1min:
	// 12 blocks of margin keep the Lock valid wherever it lands.
	lock := func(step string) (id, t1, t2 uint64, sa, kb, sb string) {
		var ha, hk, hb string
		sa, ha = secret()
		kb, hk = secret()
		sb, hb = secret()
		id = c.GetSC(scid, false).U0("n")
		h := uint64(c.Height())
		t1 = h + w1 + 12
		t2 = t1 + w2 + 2
		txid, _, mh := c.Send(*traderW, mk, "", scid, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: *amount}}, []rpc.Arg{rpc.S("entrypoint", "Lock"),
			rpc.S("ha", ha), rpc.S("hk", hk), rpc.S("hb", hb), rpc.S("taker", tk), rpc.U("t1", t1), rpc.U("t2", t2), rpc.S("asset", "")})
		tx(step, txid, mh)
		st := c.GetSC(scid, false)
		if st.U0("n") != id+1 || st.U0(fmt.Sprintf("st_%d", id)) != 0 || st.U0(fmt.Sprintf("v_%d", id)) != *amount || st.U0(fmt.Sprintf("t1_%d", id)) != t1 {
			log.Fatalf("  lock %d not stored as expected (n=%d)", id, st.U0("n"))
		}
		fmt.Printf("  lock %d: %d units, t1 %d, t2 %d, Punish until %d (open)\n", id, *amount, t1, t2, t2+g2)
		return
	}
	fmt.Println("1. lock A, claimed by the taker at once with s_a and k_b")
	idA, _, _, saA, kbA, _ := lock("htlc Lock A")
	txid, _, h := c.Send(*lpW, tk, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Claim"), rpc.U("id", idA), rpc.S("sa", saA), rpc.S("kb", kbA)})
	tx("htlc Claim A (taker)", txid, h)
	st = c.GetSC(scid, false)
	if st.U0(fmt.Sprintf("st_%d", idA)) != 1 || st.Str(fmt.Sprintf("xa_%d", idA)) != saA {
		log.Fatalf("  lock %d: state %d after Claim, s_a stored %v", idA, st.U0(fmt.Sprintf("st_%d", idA)), st.Has(fmt.Sprintf("xa_%d", idA)))
	}
	fmt.Printf("  lock %d state 1 (claimed), s_a on chain for the maker\n", idA)
	record(map[string]interface{}{"contract": "htlc", "scid": scid, "id": idA, "want": 1, "what": "claimed by the taker"})

	fmt.Println("2. lock B, left for the maker's Refund in [t1, t2)")
	idB, t1B, t2B, _, _, sbB := lock("htlc Lock B")
	schedule(exit{Kind: "htlc-refund", Scid: scid, ID: idB, Opens: t1B, Closes: t2B, Sb: sbB})
	record(map[string]interface{}{"contract": "htlc", "scid": scid, "id": idB, "want": 2, "what": "refunded by the maker", "opens": t1B, "closes": t2B})

	fmt.Println("3. lock C, left for Punish in [t2, t2 + g2), which pays the taker")
	idC, _, t2C, _, _, _ := lock("htlc Lock C")
	schedule(exit{Kind: "htlc-punish", Scid: scid, ID: idC, Opens: t2C, Closes: t2C + g2})
	record(map[string]interface{}{"contract": "htlc", "scid": scid, "id": idC, "want": 3, "what": "punished (taker paid)", "opens": t2C, "closes": t2C + g2})
	fmt.Printf("HTLC DONE contract DERO balance %d\n", c.GetSC(scid, false).Balance)
}

// ---- swap.bas --------------------------------------------------------------------------------------------------

func swap(c *rpc.Client, scid string) {
	if scid == "" {
		log.Fatal("no swap in the testnet record")
	}
	mk, tk := rpc.Address(*traderW), rpc.Address(*lpW)
	wmin := c.GetSC(scid, false).U("wmin")
	fmt.Printf("maker %s\ntaker %s\nwmin %d\n", mk, tk, wmin)
	lock := func(step string) (uint64, uint64, string) {
		s, hash := secret()
		id := c.GetSC(scid, false).U0("n")
		e := uint64(c.Height()) + wmin + 20
		txid, _, mh := c.Send(*traderW, mk, "", scid, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: *amount}}, []rpc.Arg{rpc.S("entrypoint", "Lock"),
			rpc.S("hash", hash), rpc.S("taker", tk), rpc.U("expiry", e), rpc.S("asset", "")})
		tx(step, txid, mh)
		st := c.GetSC(scid, false)
		if st.U0("n") != id+1 || st.U0(fmt.Sprintf("v_%d", id)) != *amount {
			log.Fatalf("  lock %d not stored as expected", id)
		}
		fmt.Printf("  lock %d: %d units, expiry %d (open)\n", id, *amount, e)
		return id, e, s
	}
	fmt.Println("1. lock 1, claimed by the taker with the secret")
	id1, _, s1 := lock("swap Lock 1")
	txid, _, h := c.Send(*lpW, tk, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Claim"), rpc.U("id", id1), rpc.S("secret", s1)})
	tx("swap Claim 1 (taker)", txid, h)
	st := c.GetSC(scid, false)
	if st.U0(fmt.Sprintf("st_%d", id1)) != 1 || !st.Has(fmt.Sprintf("x_%d", id1)) {
		log.Fatalf("  lock %d state %d after Claim", id1, st.U0(fmt.Sprintf("st_%d", id1)))
	}
	fmt.Printf("  lock %d state 1 (claimed), secret on chain\n", id1)
	record(map[string]interface{}{"contract": "swap", "scid": scid, "id": id1, "want": 1, "what": "claimed by the taker"})
	fmt.Println("2. lock 2, left to expire; its secret is discarded, so only Refund can close it")
	id2, e2, _ := lock("swap Lock 2")
	schedule(exit{Kind: "swap-refund", Scid: scid, ID: id2, Opens: e2})
	record(map[string]interface{}{"contract": "swap", "scid": scid, "id": id2, "want": 2, "what": "refunded after expiry", "opens": e2})
	fmt.Println("SWAP DONE")
}

// ---- board.bas -------------------------------------------------------------------------------------------------

func board(c *rpc.Client, scid string) {
	if scid == "" {
		log.Fatal("no board in the testnet record")
	}
	mk, tk := rpc.Address(*traderW), rpc.Address(*lpW)
	post := func(step string) uint64 {
		_, hash := secret()
		id := c.GetSC(scid, false).U0("n")
		e := uint64(c.Height()) + 2000
		txid, _, h := c.Send(*traderW, mk, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Post"), rpc.S("chain", "ZEC"), rpc.S("giveAsset", ""),
			rpc.U("giveAmount", *amount), rpc.U("wantAmount", 100000), rpc.S("wantAddr", "testnet-exercise-no-zcash-leg"), rpc.S("hash", hash), rpc.U("expiry", e)})
		tx(step, txid, h)
		if c.GetSC(scid, false).U0("n") != id+1 {
			log.Fatalf("  offer %d not stored", id)
		}
		fmt.Printf("  offer %d posted, expiry %d\n", id, e)
		return id
	}
	fmt.Println("1. offer 0 posted, then cancelled by its maker")
	id0 := post("board Post 0")
	txid, _, h := c.Send(*traderW, mk, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Cancel"), rpc.U("id", id0)})
	tx("board Cancel 0", txid, h)
	if st := c.GetSC(scid, false).U0(fmt.Sprintf("st_%d", id0)); st != 2 {
		log.Fatalf("  offer %d state %d after Cancel", id0, st)
	}
	record(map[string]interface{}{"contract": "board", "scid": scid, "id": id0, "want": 2, "what": "cancelled by its maker"})
	fmt.Println("2. offer 1 posted, accepted by the taker")
	id1 := post("board Post 1")
	_, pkh := secret()
	txid, _, h = c.Send(*lpW, tk, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Accept"), rpc.U("id", id1), rpc.S("refundPkh", pkh[:40]), rpc.U("cltv", 3000000)})
	tx("board Accept 1 (taker)", txid, h)
	if st := c.GetSC(scid, false).U0(fmt.Sprintf("st_%d", id1)); st != 1 {
		log.Fatalf("  offer %d state %d after Accept", id1, st)
	}
	record(map[string]interface{}{"contract": "board", "scid": scid, "id": id1, "want": 1, "what": "accepted by the taker"})
	fmt.Println("BOARD DONE")
}

// ---- social, chat, founders ------------------------------------------------------------------------------------

func social(c *rpc.Client, n *netcfg.Network) {
	tr := rpc.Address(*traderW)
	txid, _, h := c.Send(*traderW, tr, "", n.Social, nil, []rpc.Arg{rpc.S("entrypoint", "Profile"), rpc.S("name", "testnet trader"), rpc.S("bio", "W-01 private testnet exercise"), rpc.S("avatar", "")})
	tx("social Profile", txid, h)
	if n.Chat != "" {
		cf := c.GetSC(n.Chat, false).U("fee")
		txid, _, h = c.Send(*traderW, tr, "", n.Chat, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: cf}}, []rpc.Arg{rpc.S("entrypoint", "Post"), rpc.S("room", "testnet"), rpc.S("text", "day 0 of the W-01 private testnet exercise")})
		tx("chat Post", txid, h)
		fmt.Printf("  chat messages %d\n", c.GetSC(n.Chat, false).U0("n"))
	}
	fst := c.GetSC(n.Founders, false)
	price := fst.U("price")
	txid, _, h = c.Send(*traderW, tr, "", n.Founders, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: price}}, []rpc.Arg{rpc.S("entrypoint", "Mint"), rpc.U("n", 1)})
	tx("founders Mint 1", txid, h)
	fmt.Printf("  trader holds %d pass(es); minted %d\n", rpc.Balance(*traderW, n.Founders), c.GetSC(n.Founders, false).U0("minted"))
	txid, _, h = c.Send(*traderW, tr, "", n.Founders, []rpc.Transfer{{SCID: n.Founders, Burn: 1}}, []rpc.Arg{rpc.S("entrypoint", "Register")})
	tx("founders Register 1", txid, h)
	st := c.GetSC(n.Founders, false)
	fmt.Printf("  registered holders %d, registered units %d\n", st.U0("hn"), st.U0("hst"))
	fmt.Println("SOCIAL DONE")
}

// ---- wait: the scheduled exits ---------------------------------------------------------------------------------

func height(c *rpc.Client) (int64, error) {
	var r struct {
		Height int64 `json:"height"`
	}
	err := rpc.Call(c.Daemon, "DERO.GetHeight", nil, &r)
	return r.Height, err
}

func stateOf(c *rpc.Client, scid string, id uint64) (uint64, error) {
	var r struct {
		StringKeys map[string]interface{} `json:"stringkeys"`
	}
	if err := rpc.Call(c.Daemon, "DERO.GetSC", map[string]interface{}{"scid": scid, "variables": true, "code": false}, &r); err != nil {
		return 0, err
	}
	v, ok := r.StringKeys[fmt.Sprintf("st_%d", id)].(float64)
	if !ok {
		return 0, fmt.Errorf("no st_%d in %s", id, scid)
	}
	return uint64(v), nil
}

// send submits one exit and waits for it to be mined, returning errors instead of exiting: this runs for days.
func send(c *rpc.Client, scid string, args []rpc.Arg) (string, int64, error) {
	var r struct {
		TXID string `json:"txid"`
	}
	params := map[string]interface{}{"transfers": []rpc.Transfer{}, "sc_rpc": args, "ringsize": 2, "fees": *fee, "scid": scid}
	if err := rpc.Call(*traderW, "transfer", params, &r); err != nil {
		return "", 0, err
	}
	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		var t struct {
			Txs []struct {
				ValidBlock  string `json:"valid_block"`
				BlockHeight int64  `json:"block_height"`
				InPool      bool   `json:"in_pool"`
			} `json:"txs"`
		}
		err := rpc.Call(c.Daemon, "DERO.GetTransaction", map[string]interface{}{"txs_hashes": []string{r.TXID}}, &t)
		if err == nil && len(t.Txs) == 1 && t.Txs[0].ValidBlock != "" && !t.Txs[0].InPool {
			return r.TXID, t.Txs[0].BlockHeight, nil
		}
		time.Sleep(15 * time.Second)
	}
	return r.TXID, 0, fmt.Errorf("tx %s not mined within %v", r.TXID, *timeout)
}

func wait(c *rpc.Client) {
	maker := ""
	for {
		xs := loadExits()
		pending := 0
		h, err := height(c)
		if err != nil {
			fmt.Printf("%s height: %v (retrying)\n", time.Now().UTC().Format(time.RFC3339), err)
			time.Sleep(time.Minute)
			continue
		}
		for i := range xs {
			x := &xs[i]
			if x.State != 0 || x.Missed {
				continue
			}
			pending++
			st, err := stateOf(c, x.Scid, x.ID)
			switch {
			case err != nil:
				fmt.Printf("%s %s lock %d: %v (retrying)\n", time.Now().UTC().Format(time.RFC3339), x.Kind, x.ID, err)
				continue
			case st != 0:
				x.State = st
				fmt.Printf("%s %s lock %d is closed (state %d)\n", time.Now().UTC().Format(time.RFC3339), x.Kind, x.ID, st)
				saveExits(xs)
				continue
			case uint64(h) < x.Opens:
				continue
			case x.Closes > 0 && uint64(h) >= x.Closes:
				x.Missed = true
				fmt.Printf("%s %s lock %d: its window [%d, %d) closed at height %d with the lock open: MISSED\n", time.Now().UTC().Format(time.RFC3339), x.Kind, x.ID, x.Opens, x.Closes, h)
				saveExits(xs)
				continue
			case x.Txid != "":
				continue // sent, waiting for the state to change
			}
			if maker == "" {
				var a struct {
					Address string `json:"address"`
				}
				if err := rpc.Call(*traderW, "GetAddress", nil, &a); err != nil {
					fmt.Printf("%s maker wallet %s: %v (retrying)\n", time.Now().UTC().Format(time.RFC3339), *traderW, err)
					continue
				}
				maker = a.Address
			}
			var args []rpc.Arg
			switch x.Kind {
			case "htlc-refund":
				args = []rpc.Arg{rpc.S("entrypoint", "Refund"), rpc.U("id", x.ID), rpc.S("sb", x.Sb)}
			case "htlc-punish":
				args = []rpc.Arg{rpc.S("entrypoint", "Punish"), rpc.U("id", x.ID)}
			case "swap-refund":
				args = []rpc.Arg{rpc.S("entrypoint", "Refund"), rpc.U("id", x.ID)}
			default:
				fmt.Printf("unknown exit kind %q: skipped\n", x.Kind)
				x.Missed = true
				saveExits(xs)
				continue
			}
			txid, mh, err := send(c, x.Scid, args)
			if err != nil {
				fmt.Printf("%s %s lock %d: %v (retrying)\n", time.Now().UTC().Format(time.RFC3339), x.Kind, x.ID, err)
				continue
			}
			x.Txid, x.Height = txid, mh
			tx(x.Kind+fmt.Sprintf(" %d", x.ID), txid, mh)
			if st, err := stateOf(c, x.Scid, x.ID); err == nil && st != 0 {
				x.State = st
				fmt.Printf("  lock %d state %d\n", x.ID, st)
			} else {
				x.Txid = "" // mined but the lock is still open: the call reverted; try again next round
				fmt.Printf("  lock %d still open after %s: the call reverted, retrying\n", x.ID, txid)
			}
			saveExits(xs)
		}
		if pending == 0 {
			fmt.Printf("%s every scheduled exit is closed\n", time.Now().UTC().Format(time.RFC3339))
			return
		}
		time.Sleep(time.Minute)
	}
}
