package zcashvault

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	rpc "veil/tools/simrpc"
	"veil/tools/zcashtx"
)

// TestRegtestEndToEnd is the wrapped-ZEC loop on real software: a real zebrad 6.3.0 on regtest (tools/regtest), the
// DERO simulator, a vault.bas installed on it through tools/simvault, and three tools/vaultsigner daemons (2-of-3)
// running this backend. It walks register → attest the address → deposit ZEC → mint → burn → pay → verify →
// Released, then a second withdrawal with one signer offline, and appends every id to
// research/regtest/vault-zcash-e2e.log. Skipped unless ZCASHVAULT_REGTEST=1.
//
//	ZCASHVAULT_REGTEST=1 ZCASHVAULT_REGTEST_COOKIE=<repo with .sim>/.sim/regtest/zcash/.cookie go test -run TestRegtestEndToEnd -timeout 120m ./vaultsigner/zcashvault
//
// Wallets: owner 30007, treasury 30002, signers 30003-30005, user 30021. Run it under tools/withlock.sh zecreg and
// vaultsim: it mines the shared regtest chain and uses the signer wallets.
func TestRegtestEndToEnd(t *testing.T) {
	if os.Getenv("ZCASHVAULT_REGTEST") == "" {
		t.Skip("set ZCASHVAULT_REGTEST=1 to run against the regtest zebrad and the DERO simulator")
	}
	e := newVaultE2E(t)
	defer e.log.Close()
	e.logf("== wrapped ZEC end to end: zebrad regtest %s, DERO simulator %s, 2-of-3 transparent P2SH", e.zurl, e2eDaemon)

	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil || !ci.Regtest {
		t.Fatalf("not the regtest zebrad: %+v %v", ci, err)
	}
	e.logf("zcash: chain %s (genesis is regtest's), height %d, next block branch %08x", ci.Chain, ci.Height, ci.NextBranch)
	e.loadFunders(depositZat + 100_000) // before anything is installed: a run that cannot fund its deposit stops here

	// ---- keys: three seeds outside the repository, three published account keys ----
	var xpubs []string
	for i := 0; i < 3; i++ {
		p := filepath.Join(e.keys, fmt.Sprintf("signer%d.seed", i+1))
		if err := GenerateSeed(p); err != nil {
			t.Fatal(err)
		}
		x, err := AccountXPub(p, "regtest", 0)
		if err != nil {
			t.Fatal(err)
		}
		e.seeds, xpubs = append(e.seeds, p), append(xpubs, x)
		e.logf("signer %d: seed written owner-only outside the repository (never printed); account key %s", i+1, x)
	}

	// ---- the vault on DERO ----
	out := e.run(e.simvault, "-install", "-owner", "127.0.0.1:30007", "-treasury", "127.0.0.1:30002", "-user", e2eUser,
		"-cap", "100000000000", "-capratio", "2000000", "-minburn", "1000000", "-wfee", "10000", "-mindep", "100000",
		"-bigbps", "2000", "-bigdelay", "500", "-sim", filepath.Join(e.tmp, "sim.json"), "-contract", filepath.Join(e.repo, "contracts", "vault.bas"))
	m := regexp.MustCompile(`VAULT=([0-9a-f]{64})`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("simvault did not install the vault:\n%s", out)
	}
	e.scid = m[1]
	d, err := NewDescriptor("regtest", 2, xpubs, e.scid)
	if err != nil {
		t.Fatal(err)
	}
	e.d = d
	ch, _ := d.Change()
	e.logf("vault %s installed (2-of-3; cap 1000 ZEC, capratio 2e6, minburn 0.01 ZEC, wfee 0.0001 ZEC, mindep 0.001 ZEC)", e.scid)
	e.logf("descriptor: %s", d.Text())
	e.logf("change address %s", ch.Address)
	e.run(e.simvault, "-vault", e.scid, "-viewkey", d.ViewKey())
	if got := e.sc().Str("viewkey"); got != d.ViewKey() {
		t.Fatalf("the published view key is %q", got)
	}
	e.logf("SetViewKey: the descriptor is published on chain under viewkey (%d characters); anyone can derive every address", len(d.ViewKey()))
	coordDir := filepath.Join(e.tmp, "coord")
	for i := range e.seeds {
		cfg := Config{Network: "regtest", K: 2, XPubs: xpubs, Seed: e.seeds[i], Nodes: []NodeConfig{{Name: "zebrad-regtest", URL: e.zurl, Cookie: e.cookie}},
			Ledger: filepath.Join(e.tmp, fmt.Sprintf("ledger%d.json", i+1)), CoordinatorDir: coordDir}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(e.cfg(i), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// ---- register (done by the install, index 0) and attest ----
	e.passes("attest", 0, 1, 2)
	a0, _ := d.DepositAddress(0)
	if st := e.sc(); !st.Has("da_0") || st.Str("da_0") != a0 {
		t.Fatalf("index 0 is not attested as %s", a0)
	}
	e.logf("index 0 attested by the quorum: %s (the address every party derives from the descriptor)", a0)

	// ---- deposit 2 ZEC ----
	dep := e.fund(a0, depositZat)
	e.logf("zcash: deposit %s pays %d zatoshi to index 0's address", dep, uint64(depositZat))
	e.mine(1)
	e.mine(111) // final (tip - 100) and past an epoch boundary
	e.until("mint", func() bool { return e.sc().U("sup") == depositZat }, 0, 1, 2)
	id := dep + ":0"
	e.logf("minted: sup %d; mint_%s… closed at DERO height %d", e.sc().U("sup"), id[:16], e.sc().U("mh_"+id))

	// ---- withdrawal 0: burn, pay, verify, Released ----
	dest0 := e.freshAddress()
	e.burn(3_000_000, dest0) // under bigbps (20 %) of the supply, so payable at once
	ftx0 := e.settle(0, dest0, 0, 1, 2)
	// ---- withdrawal 1 with signer 3 offline ----
	dest1 := e.freshAddress()
	e.burn(2_000_000, dest1)
	ftx1 := e.settle(1, dest1, 0, 1)
	if tx := e.ztx(ftx1); zcashtx.DisplayHex(tx.In[0].PrevOut.Hash) != ftx0 || tx.In[0].PrevOut.Index != 1 {
		t.Fatalf("the second release does not spend the first one's change first")
	}
	e.logf("withdrawal 1 was signed by signers 1 and 2 with signer 3 offline; its input 0 is withdrawal 0's change (%s:1)", ftx0)

	// ---- the reserve, as anyone can count it from the published descriptor ----
	st := e.sc()
	var reserve uint64
	for _, addr := range []string{a0, ch.Address} {
		us, err := e.zc.AddressUTXOs(e.ctx, addr)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range us {
			reserve += u.Value
		}
	}
	e.logf("reserve %d zatoshi at the vault's addresses; sup %d, feepool %d, open withdrawals %d", reserve, st.U("sup"), st.U("feepool"), st.U("wn")-2)
	if reserve != st.U("sup") {
		t.Fatalf("reserve %d is not sup %d: what leaves the reserve for a withdrawal must be exactly what Released removes from sup", reserve, st.U("sup"))
	}
	if st.U("halted") > uint64(e.dero.Height()) {
		t.Fatal("the vault halted")
	}
	e.logf("== done: deposit %s, releases %s and %s; the reserve equals sup exactly and the vault never halted", dep, ftx0, ftx1)
}

const (
	e2eDaemon = "127.0.0.1:20000"
	e2eUser   = "127.0.0.1:30021"
	e2eLog    = "research/regtest/vault-zcash-e2e.log"
	// depositZat is the run's deposit: 0.2 ZEC, above minburn for both withdrawals and within what the regtest
	// harness keys hold
	depositZat = 20_000_000
)

var e2eSigners = []string{"127.0.0.1:30003", "127.0.0.1:30004", "127.0.0.1:30005"}

type vaultE2E struct {
	t         *testing.T
	ctx       context.Context
	zc        *zcashtx.ZcashRPC
	zurl      string
	cookie    string
	auth      [2]string
	dero      *rpc.Client
	log       *os.File
	repo, tmp string
	keys      string
	vaultsign string
	simvault  string
	seeds     []string
	scid      string
	d         *Descriptor
	funders   []funderCoin
}

func newVaultE2E(t *testing.T) *vaultE2E {
	repo, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	e := &vaultE2E{t: t, ctx: context.Background(), repo: repo, tmp: t.TempDir(), keys: t.TempDir(), zurl: "http://127.0.0.1:28232/",
		dero: &rpc.Client{Daemon: e2eDaemon, Timeout: 180 * time.Second}}
	e.cookie = os.Getenv("ZCASHVAULT_REGTEST_COOKIE")
	if e.cookie == "" {
		e.cookie = filepath.Join(repo, ".sim", "regtest", "zcash", ".cookie")
	}
	u, p, err := zcashtx.ReadCookie(e.cookie)
	if err != nil {
		t.Fatalf("the regtest zebrad's cookie: %v", err)
	}
	e.auth = [2]string{u, p}
	e.zc = zcashtx.NewZcashRPC(e.zurl, u, p)
	f, err := os.OpenFile(filepath.Join(repo, e2eLog), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	e.log = f
	bin := t.TempDir()
	e.vaultsign, e.simvault = filepath.Join(bin, "vaultsigner.exe"), filepath.Join(bin, "simvault.exe")
	for _, b := range [][2]string{{e.vaultsign, "./vaultsigner"}, {e.simvault, "./simvault"}} {
		cmd := exec.Command("go", "build", "-o", b[0], b[1])
		cmd.Dir = filepath.Join(repo, "tools")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", b[1], err, out)
		}
	}
	return e
}

// scrub writes every machine path as a placeholder: the log is committed evidence.
func (e *vaultE2E) scrub(s string) string {
	for _, p := range []string{e.keys, e.tmp, filepath.Dir(e.cookie), e.repo, os.TempDir()} {
		if p == "" {
			continue
		}
		for _, v := range []string{p, filepath.ToSlash(p), strings.ReplaceAll(p, `\`, `\\`)} {
			s = strings.ReplaceAll(s, v, "<tmp>")
		}
	}
	if h, err := os.UserHomeDir(); err == nil {
		s = strings.ReplaceAll(s, h, "<home>")
		s = strings.ReplaceAll(s, filepath.ToSlash(h), "<home>")
	}
	return s
}

func (e *vaultE2E) logf(format string, a ...interface{}) {
	line := e.scrub(time.Now().UTC().Format("2006-01-02T15:04:05Z") + " " + fmt.Sprintf(format, a...))
	fmt.Fprintln(e.log, line)
	e.t.Log(line)
}

func (e *vaultE2E) cfg(i int) string { return filepath.Join(e.tmp, fmt.Sprintf("zcash%d.json", i+1)) }

// run runs a built tool from the tools directory and returns its output.
func (e *vaultE2E) run(bin string, args ...string) string {
	cmd := exec.Command(bin, args...)
	cmd.Dir = filepath.Join(e.repo, "tools")
	out, err := cmd.CombinedOutput()
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			e.logf("  %s: %s", filepath.Base(bin), l)
		}
	}
	if err != nil {
		e.t.Fatalf("%s %v: %v", filepath.Base(bin), args, err)
	}
	return string(out)
}

// pass runs signer i's daemon once.
func (e *vaultE2E) pass(i int) {
	cmd := exec.Command(e.vaultsign, "-network", "simulator", "-backend", "zcash", "-zcash-config", e.cfg(i),
		"-wallet", e2eSigners[i], "-vault", e.scid, "-state", filepath.Join(e.tmp, fmt.Sprintf("state%d.json", i+1)), "-once")
	cmd.Dir = e.tmp
	out, _ := cmd.CombinedOutput()
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" && !strings.HasPrefix(l, "network simulator;") {
			e.logf("  signer %d: %s", i+1, l)
		}
	}
}

func (e *vaultE2E) passes(what string, who ...int) {
	e.logf("-- %s: signers %v run one pass each", what, plus1(who))
	for _, i := range who {
		e.pass(i)
	}
}

func plus1(w []int) []int {
	var o []int
	for _, i := range w {
		o = append(o, i+1)
	}
	return o
}

// until runs passes of the named signers until done, mining a Zcash block between rounds.
func (e *vaultE2E) until(what string, done func() bool, who ...int) {
	for round := 1; round <= 40; round++ {
		if done() {
			return
		}
		e.passes(fmt.Sprintf("%s, round %d", what, round), who...)
		e.mine(1)
	}
	e.t.Fatalf("%s did not complete", what)
}

func (e *vaultE2E) sc() rpc.SCState { return e.dero.GetSC(e.scid, false) }

func (e *vaultE2E) zcall(method string, params ...interface{}) json.RawMessage {
	body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "1.0", "id": "e2e", "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, e.zurl, bytes.NewReader(body))
	req.SetBasicAuth(e.auth[0], e.auth[1])
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		e.t.Fatalf("zebrad %s: %v", method, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &env) != nil || env.Error != nil {
		e.t.Fatalf("zebrad %s: %s", method, b)
	}
	return env.Result
}

func (e *vaultE2E) mine(n int) {
	for n > 0 {
		k := min(n, 8)
		e.zcall("generate", k)
		n -= k
	}
}

func (e *vaultE2E) freshAddress() string {
	var h [20]byte
	rand.Read(h[:])
	return zcashtx.EncodeAddress(zcashtx.Testnet, zcashtx.P2PKH, h)
}

// The deposit is paid from the regtest harness's throwaway keys (ZCASHVAULT_REGTEST_FUNDER: comma-separated key files
// of 64 hex characters, as tools/regtest's zfund writes them). A fresh coinbase cannot fund it: regtest's subsidy
// halves every few hundred blocks, and at the shared chain's height (about 4,700 on 2026-09-15) a coinbase is 9,536
// zatoshi (measured: the first run of this test built a deposit whose change underflowed, and zebrad refused it).
// The keys are read into this process only, never printed, and are regtest keys.
type funderCoin struct {
	u   zcashtx.UTXO
	key *zcashtx.Key
}

func (e *vaultE2E) loadFunders(need uint64) {
	env := os.Getenv("ZCASHVAULT_REGTEST_FUNDER")
	if env == "" {
		e.t.Fatal("set ZCASHVAULT_REGTEST_FUNDER to the regtest harness's key files (<repo with .sim>/.sim/regtest/zcash/keys/k1.key,…)")
	}
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	var total uint64
	for _, p := range strings.Split(env, ",") {
		b, err := os.ReadFile(strings.TrimSpace(p))
		if err != nil {
			e.t.Fatal(err)
		}
		raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
		wipe(b)
		if err != nil || len(raw) != 32 {
			e.t.Fatalf("a funder key file is not 64 hex characters")
		}
		var kb [32]byte
		copy(kb[:], raw)
		wipe(raw)
		key, err := zcashtx.ParseKey(kb)
		wipe(kb[:])
		if err != nil {
			e.t.Fatal(err)
		}
		addr := zcashtx.EncodeAddress(zcashtx.Testnet, zcashtx.P2PKH, key.PubKeyHash())
		us, err := e.zc.AddressUTXOs(e.ctx, addr)
		if err != nil {
			e.t.Fatal(err)
		}
		var here uint64
		for _, u := range us {
			// mature (coinbase needs 100 confirmations; the rule is applied to every coin, which costs nothing)
			if u.Height+100 <= ci.Height && string(u.Script) == string(zcashtx.P2PKHScript(key.PubKeyHash())) {
				e.funders = append(e.funders, funderCoin{u, key})
				here += u.Value
			}
		}
		total += here
		e.logf("funder %s holds %d zatoshi in mature outputs (a regtest harness key; never printed)", addr, here)
	}
	if total < need {
		e.t.Fatalf("the funders hold %d zatoshi, and the run needs %d", total, need)
	}
}

// fund pays value to addr from the funders' largest outputs, with ZIP 317's fee and the change back to the first.
func (e *vaultE2E) fund(addr string, value uint64) string {
	sort.Slice(e.funders, func(a, b int) bool { return e.funders[a].u.Value > e.funders[b].u.Value })
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	to, _, _ := zcashtx.AddressScript(addr, zcashtx.Testnet)
	change := zcashtx.P2PKHScript(e.funders[0].key.PubKeyHash())
	tx := &zcashtx.Tx{Version: 5, BranchID: ci.NextBranch, ExpiryHeight: ci.Height + 40,
		Out: []zcashtx.TxOut{{Value: value, ScriptPubKey: to}, {ScriptPubKey: change}}}
	sigP, _ := zcashtx.Push(make([]byte, 73))
	var used []funderCoin
	var total uint64
	for _, c := range e.funders {
		prev, _ := zcashtx.ParseDisplayHex(c.u.TxID)
		pubP, _ := zcashtx.Push(c.key.PubKey())
		tx.In = append(tx.In, zcashtx.TxIn{PrevOut: zcashtx.OutPoint{Hash: prev, Index: c.u.Index}, ScriptSig: append(append([]byte{}, sigP...), pubP...), Sequence: zcashtx.SequenceFinal, Value: c.u.Value, ScriptPubKey: c.u.Script})
		used = append(used, c)
		total += c.u.Value
		if total >= value+tx.ConventionalFee()+zcashtx.MinimumFee || len(tx.In) == 50 {
			break
		}
	}
	fee := tx.ConventionalFee()
	if total < value+fee+zcashtx.MinimumFee {
		e.t.Fatalf("the funders' largest %d outputs hold %d zatoshi, not %d plus a fee of %d", len(tx.In), total, value, fee)
	}
	tx.Out[1].Value = total - value - fee
	for i, c := range used {
		d, err := tx.SigDigest(i, zcashtx.SigHashAll)
		if err != nil {
			e.t.Fatal(err)
		}
		s, _ := zcashtx.Push(c.key.Sign(d))
		p, _ := zcashtx.Push(c.key.PubKey())
		tx.In[i].ScriptSig = append(s, p...)
	}
	e.funders = e.funders[len(used):]
	id, err := zcashtx.Broadcast(e.ctx, e.zc, tx.Serialize())
	if err != nil {
		e.t.Fatalf("zebrad refused the deposit: %v", err)
	}
	return id
}

// burn sends the user's burn. A freshly minted asset is unspendable for about two DERO blocks (the wallet answers
// "Account Unregistered"; DESIGN.md A3, DVM quirks), so it waits three blocks first and tries up to three times,
// each time checking the contract recorded a new withdrawal.
func (e *vaultE2E) burn(amount uint64, dest string) {
	wn := e.sc().U("wn")
	for attempt := 1; ; attempt++ {
		e.dero.WaitBlocks(3)
		cmd := exec.Command(e.simvault, "-vault", e.scid, "-user", e2eUser, "-burn", strconv.FormatUint(amount, 10)+":"+dest)
		cmd.Dir = filepath.Join(e.repo, "tools")
		out, err := cmd.CombinedOutput()
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l != "" {
				e.logf("  simvault.exe: %s", l)
			}
		}
		if err == nil && e.sc().U("wn") == wn+1 {
			break
		}
		if attempt == 3 {
			e.t.Fatalf("the burn of %d recorded no withdrawal after %d attempts: %v", amount, attempt, err)
		}
	}
	e.logf("burn: withdrawal %d, %d wrapped units less the 10000 fee, to %s", wn, amount, dest)
}

func (e *vaultE2E) ztx(id string) *zcashtx.Tx {
	rt, err := e.zc.RawTx(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	tx, err := zcashtx.ParseTx(rt.Raw)
	if err != nil {
		e.t.Fatal(err)
	}
	return tx
}

// settle drives withdrawal j to Released with the named signers, mining Zcash as the payment needs: one block to
// include it, then the finality depth before any signer certifies it.
func (e *vaultE2E) settle(j uint64, dest string, who ...int) string {
	js := strconv.FormatUint(j, 10)
	net := e.sc().U("w_" + js + "_v")
	supBefore := e.sc().U("sup")
	var ftx string
	for round := 1; round <= 40 && e.sc().U("w_"+js+"_st") == 0; round++ {
		e.passes(fmt.Sprintf("withdrawal %d, round %d", j, round), who...)
		// the payment, once some signer has broadcast it
		for i := range who {
			l, err := loadLedger(filepath.Join(e.tmp, fmt.Sprintf("ledger%d.json", who[i]+1)), e.scid, e.xpub(who[i]))
			if err != nil || l.Entries[js] == nil {
				continue
			}
			for _, lt := range l.Entries[js].Txs {
				if lt.Final != "" && lt.State != "abandoned" {
					ftx = lt.TxID
				}
			}
		}
		if ftx != "" {
			if s, _ := (&nodes{list: []node{{"z", e.zc}}}).seen(e.ctx, ftx); !s.MinedAll {
				e.mine(1)
				e.logf("zcash: release %s for withdrawal %d mined; mining the finality depth", ftx, j)
				e.mine(101)
			}
		}
	}
	st := e.sc()
	if st.U("w_"+js+"_st") != 1 || st.Str("w_"+js+"_ftx") != ftx {
		e.t.Fatalf("withdrawal %d: status %d, ftx %q, the payment %s", j, st.U("w_"+js+"_st"), st.Str("w_"+js+"_ftx"), ftx)
	}
	tx := e.ztx(ftx)
	var in uint64
	for _, x := range tx.In {
		prev := e.ztx(zcashtx.DisplayHex(x.PrevOut.Hash))
		in += prev.Out[x.PrevOut.Index].Value
	}
	fee := in - tx.Out[0].Value - tx.Out[1].Value
	us, _ := e.zc.AddressUTXOs(e.ctx, dest)
	var got uint64
	for _, u := range us {
		got += u.Value
	}
	if got != net-fee || tx.Out[0].Value != net-fee || st.U("sup") != supBefore-net {
		e.t.Fatalf("withdrawal %d: the destination holds %d, the release pays %d with a fee of %d, w_v %d; sup %d -> %d", j, got, tx.Out[0].Value, fee, net, supBefore, st.U("sup"))
	}
	e.logf("withdrawal %d Released on DERO with ftx %s: %d inputs, %d zatoshi to %s (w_v %d less the ZIP 317 fee %d), %d back to the vault's change; lock-time marker %d; sup %d -> %d",
		j, ftx, len(tx.In), tx.Out[0].Value, dest, net, fee, tx.Out[1].Value, tx.LockTime, supBefore, st.U("sup"))
	return ftx
}

func (e *vaultE2E) xpub(i int) string {
	x, err := AccountXPub(e.seeds[i], "regtest", 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return x
}
