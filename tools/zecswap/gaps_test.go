package zecswap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	ref "veil/reference"
)

// The rows of docs/MAINNET_GAP_ANALYSIS.md that are zecswap's, each with the test that holds it closed.

// ---- the fakes' code hashes ----

var (
	lookAlikeMu sync.Mutex
	lookAlike   = map[*fakeDero]map[string]string{}
)

// setCode makes a fake chain report another code hash for a contract: a look-alike installed at that SCID.
func setCode(c *fakeDero, scid, hash string) {
	lookAlikeMu.Lock()
	defer lookAlikeMu.Unlock()
	if lookAlike[c] == nil {
		lookAlike[c] = map[string]string{}
	}
	lookAlike[c][scid] = hash
}

// CodeSHA256 answers the pinned hashes for the test board and escrow, unless the test installed a look-alike.
func (d *memDero) CodeSHA256(ctx context.Context, scid string) (string, error) {
	lookAlikeMu.Lock()
	h, ok := lookAlike[d.c][scid]
	lookAlikeMu.Unlock()
	if ok {
		return h, nil
	}
	switch scid {
	case testBoard:
		return BoardCodeSHA256, nil
	case testEscrow:
		return EscrowCodeSHA256, nil
	}
	return "", fmt.Errorf("DERO.GetSC: no code at %s", scid)
}

func lookAlikeHash() string {
	sum := sha256.Sum256([]byte("Function Refund(id Uint64) Uint64\n10 SEND_DERO_TO_ADDRESS(SIGNER(), 1)\nEnd Function\n"))
	return hex.EncodeToString(sum[:])
}

// ---- the fakes' wmin and registrations ----

var (
	wminOf       = map[*fakeDero]uint64{}
	unregistered = map[*fakeDero]map[string]bool{}
)

// wminFor is the fake escrow's install-time wmin: the test policy's T_dero window, unless a test installed another.
func wminFor(c *fakeDero) uint64 {
	lookAlikeMu.Lock()
	defer lookAlikeMu.Unlock()
	if w, ok := wminOf[c]; ok {
		return w
	}
	return testPolicy().TDeroWindow
}

func setWMin(c *fakeDero, w uint64) {
	lookAlikeMu.Lock()
	defer lookAlikeMu.Unlock()
	wminOf[c] = w
}

func (d *memDero) EscrowWMin(ctx context.Context) (uint64, error) { return wminFor(d.c), nil }

func (d *memDero) Registered(ctx context.Context, addr string) (bool, error) {
	lookAlikeMu.Lock()
	defer lookAlikeMu.Unlock()
	return !unregistered[d.c][addr], nil
}

// ---- swap.bas v2: wmin and the registered taker ----

// swap.bas v2 pays the named taker in Claim, and a transfer to an unregistered address reverts the whole Claim: the
// maker's companion never locks for a taker the chain has no account for.
func TestMakerRefusesAnUnregisteredTaker(t *testing.T) {
	w := newWorld(t, 27)
	lookAlikeMu.Lock()
	unregistered[w.dero] = map[string]bool{takerAddr: true}
	lookAlikeMu.Unlock()
	w.stepN(RoleMaker, 2)
	w.stepN(RoleTaker, 2)
	w.stepN(RoleMaker, 3)
	if st := w.state(RoleMaker); st.Phase != "cancel_wait" || len(w.dero.Escrows()) != 0 || !strings.Contains(st.Note, "registered") {
		t.Fatalf("maker %s (%s); %d escrows", st.Phase, st.Note, len(w.dero.Escrows()))
	}
}

// The same swap.bas code installed with a smaller wmin has the same code hash; the taker reads wmin and refuses a
// deployment narrower than her policy.
func TestTakerRefusesANarrowEscrowDeployment(t *testing.T) {
	w := newWorld(t, 28)
	setWMin(w.dero, 100)
	w.stepN(RoleMaker, 2)
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "declined" || !strings.Contains(st.Outcome, "wmin") {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}

// A deployment wider than the maker's T_dero window would lock his capital longer than his policy says: refused.
func TestMakerRefusesAWideEscrowDeployment(t *testing.T) {
	w := newWorld(t, 29)
	setWMin(w.dero, 1000)
	w.stepN(RoleMaker, 1)
	if st := w.state(RoleMaker); st.Phase != "refused" || !strings.Contains(st.Outcome, "wmin") || len(w.dero.offers) != 0 {
		t.Fatalf("maker %s: %s", st.Phase, st.Outcome)
	}
}

// DeroRPC reads wmin by its key and asks derod for an address's registration the way derod answers it.
func TestDeroReadsWMinAndRegistration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		env := map[string]interface{}{"jsonrpc": "2.0", "id": "1"}
		switch req.Method {
		case "DERO.GetSC":
			env["result"] = map[string]interface{}{"valuesstring": []string{"1600"}}
		case "DERO.GetEncryptedBalance":
			if jsonS(req.Params["address"]) == takerAddr {
				env["result"] = map[string]interface{}{"registration": 42, "status": "OK"}
			} else {
				env["error"] = map[string]interface{}{"code": -32098, "message": "Account Unregistered"}
			}
		}
		json.NewEncoder(w).Encode(env)
	}))
	defer srv.Close()
	c := NewDeroRPC(srv.URL, "", testBoard, testEscrow)
	ctx := context.Background()
	if w, err := c.EscrowWMin(ctx); err != nil || w != 1600 {
		t.Fatalf("wmin %d %v", w, err)
	}
	if ok, err := c.Registered(ctx, takerAddr); err != nil || !ok {
		t.Fatalf("a registered address: %v %v", ok, err)
	}
	if ok, err := c.Registered(ctx, otherAddr(3)); err != nil || ok {
		t.Fatalf("an unregistered address: %v %v", ok, err)
	}
}

// ---- GAP-011: the board and the escrow are pinned ----

// A taker handed a look-alike escrow — the same keys, the same values, and an exit of its owner's — never accepts.
func TestLookAlikeEscrowIsRefused(t *testing.T) {
	w := newWorld(t, 21)
	w.stepN(RoleMaker, 2)
	setCode(w.dero, testEscrow, lookAlikeHash())
	w.stepN(RoleTaker, 1)
	st := w.state(RoleTaker)
	if st.Phase != "declined" || !strings.Contains(st.Outcome, "not the one this companion was built for") || w.dero.Offer(0).State != OfferOpen {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}

// A maker handed a look-alike board never posts to it.
func TestLookAlikeBoardIsRefusedByTheMaker(t *testing.T) {
	w := newWorld(t, 22)
	setCode(w.dero, testBoard, lookAlikeHash())
	w.stepN(RoleMaker, 1)
	if st := w.state(RoleMaker); st.Phase != "refused" || len(w.dero.offers) != 0 {
		t.Fatalf("maker %s: %s", st.Phase, st.Outcome)
	}
}

// Every DERO node must report the pinned code: a witness that sees a look-alike at the SCID stops the taker.
func TestPinsAreCheckedOnEveryDeroNode(t *testing.T) {
	w := newWorld(t, 23)
	other := newFakeDero(startDero)
	setCode(other, testEscrow, lookAlikeHash())
	w.cfgHook = func(role string, c *Config) {
		if role == RoleTaker {
			c.DeroWitnesses = []DeroSource{{Name: "witness", Node: newMemDero(other, takerAddr)}}
		}
	}
	w.stepN(RoleMaker, 2)
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "declined" || !strings.Contains(st.Outcome, "witness") {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}

// DeroRPC reads the code the daemon stores and hashes it; the tree's canonical board.bas and swap.bas are the pins.
func TestDeroCodeHashIsReadFromTheDaemon(t *testing.T) {
	code := map[string]string{}
	for scid, file := range map[string]string{testBoard: "../../contracts/board.bas", testEscrow: "../../contracts/swap.bas"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		code[scid] = ref.Canon(string(b))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		text := code[jsonS(req.Params["scid"])]
		json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": "1", "result": map[string]interface{}{"code": text, "balance": 0}})
	}))
	defer srv.Close()
	c := NewDeroRPC(srv.URL, "", testBoard, testEscrow)
	if err := CheckPins(context.Background(), []DeroSource{{Name: "daemon", Node: c}}, testBoard, testEscrow); err != nil {
		t.Fatalf("the tree's own contracts: %v", err)
	}
	code[testEscrow] += "\nFunction Drain() Uint64\n10 RETURN 0\nEnd Function\n"
	if err := CheckPins(context.Background(), []DeroSource{{Name: "daemon", Node: c}}, testBoard, testEscrow); !errors.Is(err, ErrCodeNotPinned) {
		t.Fatalf("an escrow with one extra function: %v", err)
	}
}

// ---- GAP-008: the DERO wallet's login ----

// withLogin puts HTTP basic auth in front of a fake daemon's wallet methods, as a wallet RPC started with
// --rpc-login does (derohe walletapi/rpcserver hasbasicauthfailed: 401, "Authorization Required").
func withLogin(user, pass string, next http.Handler) http.Handler {
	wallet := map[string]bool{"GetAddress": true, "GetHeight": true, "GetBalance": true, "transfer": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		json.Unmarshal(body, &req)
		if wallet[req.Method] && user != "" {
			if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, "Authorization Required")
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func TestDeroWalletLoginIsSent(t *testing.T) {
	ctx := context.Background()
	c := newFakeDero(startDero)
	srv := httptest.NewServer(withLogin("swapper", "s3cret", &deroDaemon{c: c, addr: makerAddr}))
	defer srv.Close()
	with := NewDeroRPC(srv.URL, srv.URL, testBoard, testEscrow)
	with.WalletUser, with.WalletPass, with.Poll = "swapper", "s3cret", 1
	if a, err := with.Address(ctx); err != nil || a != makerAddr {
		t.Fatalf("with the login: %q %v", a, err)
	}
	var h [32]byte
	if _, _, err := with.Post(ctx, PostArgs{Chain: ZcashChainLabel, GiveAmount: 1, WantAmount: 200_000, WantAddr: "tm", Hash: hex.EncodeToString(h[:]), Expiry: startDero + 10}); err != nil {
		t.Fatalf("a Post with the login: %v", err)
	}
	without := NewDeroRPC(srv.URL, srv.URL, testBoard, testEscrow)
	if _, err := without.Address(ctx); !errors.Is(err, ErrWalletAuth) {
		t.Fatalf("without the login: %v", err)
	}
	if anon, err := with.WalletAnswersWithoutLogin(ctx); err != nil || anon {
		t.Fatalf("a wallet with a login answered without one: %v %v", anon, err)
	}
	open := httptest.NewServer(&deroDaemon{c: c, addr: makerAddr})
	defer open.Close()
	if anon, err := NewDeroRPC(open.URL, open.URL, testBoard, testEscrow).WalletAnswersWithoutLogin(ctx); err != nil || !anon {
		t.Fatalf("a wallet with no login was not seen as open: %v %v", anon, err)
	}
}

// ---- GAP-021: the build's own upgrade schedule counts, not only the node's ----

// An out-of-date node reports no pending upgrade; this build knows of one inside the swap's life. No funding.
func TestBuildScheduleUpgradeBeforeTDeroDeclinesFunding(t *testing.T) {
	saved := Upgrades
	Upgrades = append(append([]Upgrade(nil), saved...), Upgrade{Name: "NU7", BranchID: 0x77777777, Mainnet: 99_999_999, Testnet: startZec + 40})
	t.Cleanup(func() { Upgrades = saved })
	w := newWorld(t, 24)
	w.zc.override = func(method string) ([]byte, int, bool) {
		if method != "getblockchaininfo" {
			return nil, 0, false
		}
		tip := w.zc.tipLocked()
		body := fmt.Sprintf(`{"result":{"chain":"test","blocks":%d,"bestblockhash":%q,"estimatedheight":%d,"initial_block_download_complete":true,"upgrades":{},"consensus":{"chaintip":"37a5165b","nextblock":"37a5165b"}},"error":null,"id":1}`, tip, w.zc.blocks[len(w.zc.blocks)-1].hash, tip)
		return []byte(body), 200, true
	}
	if !w.drive(400) {
		t.Fatalf("did not finish:\n%s", w.dump())
	}
	tk := w.state(RoleTaker)
	if tk.Phase != "declined" || !strings.Contains(tk.Outcome, "this build's schedule") || len(w.fundings) != 0 {
		t.Fatalf("taker %s: %s", tk.Phase, tk.Outcome)
	}
}

// ---- GAP-039: a funding's id is computed from its bytes, never taken on the node's word ----

// ZIP 244's Sapling digest is implemented (sapling.go), so a v5 funding with Sapling parts — a counterparty's wallet
// adding shielded change — is identified from its own bytes and credited like any other. A v6 transaction with
// shielded parts is the one kind whose id this package cannot compute (no ZIP 229 vectors for them here), and it is
// never credited.
func TestSaplingFundingIsCreditedOnlyByItsComputedID(t *testing.T) {
	ctx := context.Background()
	h, _ := htlcFor(startZec + 500)
	confirm := func(f *fakeZcash, id string) error {
		f.Mine(4)
		f.mu.Lock()
		height, _ := f.minedAtLocked(id)
		bh := f.blocks[height-f.base].hash
		f.mu.Unlock()
		return ConfirmFunding(ctx, []ZcashSource{{Name: "node", Node: f.client()}}, "test", h, Funding{TxID: id, Value: 200_000, Height: height, BlockHash: bh}, 3, func(string, ...interface{}) {})
	}
	// a Sapling output: its bytes are digested, never validated, here
	change := make([]byte, saplingOutputSize)
	for i := range change {
		change[i] = byte(i)
	}
	sap := &SaplingBundle{Outputs: [][]byte{change}, OutputProofs: [][]byte{make([]byte, saplingProofSize)}, ValueBalance: -1000}
	pay := []TxOut{{Value: 200_000, ScriptPubKey: h.ScriptPubKey()}}

	// v5 with Sapling change: its id is computed from its bytes, and it is credited
	f := newFakeZcash(t, "test", startZec)
	tx5 := &Tx{BranchID: f.branch, LockTime: 1, Sapling: sap, Out: pay}
	back, err := ParseV5(tx5.Serialize())
	if err != nil || back.Sapling == nil || back.TxID() != tx5.TxID() || !bytes.Equal(back.Serialize(), tx5.Serialize()) {
		t.Fatalf("a v5 transaction with a Sapling bundle does not round-trip: %v", err)
	}
	id5 := f.MineTx(tx5)
	if got, ok := ComputeTxID(tx5.Serialize()); !ok || got != id5 {
		t.Fatalf("ComputeTxID %q %v, want %s", got, ok, id5)
	}
	if err := confirm(f, id5); err != nil {
		t.Fatalf("a v5 funding with Sapling change, whose id is computed, was refused: %v", err)
	}

	// v6 with Sapling parts: its id cannot be computed here, and it is refused
	g := newFakeZcash(t, "test", startZec)
	tx6 := &Tx{Version: 6, BranchID: g.branch, LockTime: 2, Sapling: sap, Out: pay}
	if _, err := ParseV6(tx6.Serialize()); !errors.Is(err, ErrShielded) {
		t.Fatalf("ParseV6 on a Sapling-bearing v6 transaction: %v", err)
	}
	id6 := g.MineTx(tx6)
	if err := confirm(g, id6); err == nil || !strings.Contains(err.Error(), "cannot be checked") {
		t.Fatalf("a v6 funding with shielded parts was credited: %v", err)
	}
}

// ---- MT-C08: the lock must be the board's ----

// The lock must hold the board's asset and at least its amount. The taker funds nothing against one that does not:
// a maker who locked less, or another asset, or a DERO node showing the lock changed after she first saw it.
func TestTakerRefusesALockOfTheWrongAssetOrAmount(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(e *Escrow)
		want   string
	}{
		{"less than the board's amount", func(e *Escrow) { e.Amount = testGive - 1 }, "the lock holds"},
		{"another asset", func(e *Escrow) { e.Asset = strings.Repeat("ab", 32) }, "the lock holds asset"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, 61)
			for i := 0; i < 200 && w.state(RoleTaker).Phase != "lock_seen"; i++ {
				w.round()
			}
			tk := w.state(RoleTaker)
			if tk.Phase != "lock_seen" {
				t.Fatalf("taker %s\n%s", tk.Phase, w.dump())
			}
			w.dero.mu.Lock()
			c.mutate(&w.dero.escrows[tk.EscrowID])
			w.dero.mu.Unlock()
			for i := 0; i < 40 && !w.done(RoleTaker); i++ {
				w.step(RoleTaker)
				w.dero.Advance(4)
			}
			tk = w.state(RoleTaker)
			if tk.Phase != "declined" || !strings.Contains(tk.Outcome, c.want) || len(w.fundings) != 0 {
				t.Fatalf("taker %s: %s; fundings %d", tk.Phase, tk.Outcome, len(w.fundings))
			}
		})
	}
}

// ---- board.bas v3's acceptance deadline ----

// A lock first seen at or after the board's acceptance deadline is refused: it may have been mined there, and the
// board says the offer is dead to its taker from then.
func TestTakerRefusesALockAfterTheAcceptanceDeadline(t *testing.T) {
	w := newWorld(t, 25)
	w.stepN(RoleMaker, 2)
	w.stepN(RoleTaker, 2)
	if st := w.state(RoleTaker); st.Phase != "accepted" {
		t.Fatalf("taker in %s", st.Phase)
	}
	o := w.dero.Offer(0)
	w.dero.mu.Lock()
	w.dero.height = o.AcceptDeadline
	takerRaw, _ := DeroRaw(takerAddr)
	if _, err := w.dero.lock(w.dero.height, o.Maker, o.Hash, takerRaw, w.dero.height+300, "", testGive); err != nil {
		w.dero.mu.Unlock()
		t.Fatal(err)
	}
	w.dero.mu.Unlock()
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "declined" || !strings.Contains(st.Outcome, "acceptance deadline") {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}

// ---- Z-C: the maker completes the taker's DERO leg ----

// The taker is away from the moment she funds. The maker claims the ZEC and, once his claim is deep, sends her
// DERO Claim for her: swap.bas v2 pays her whoever sends it. When she comes back the swap is already complete.
func TestMakerSendsTheTakersClaim(t *testing.T) {
	w := newWorld(t, 26)
	w.offline = func(w *world, role string) bool {
		if role != RoleTaker {
			return false
		}
		tk := w.state(RoleTaker)
		if tk.Phase != "funded" || !tk.HaveEscrow {
			return false
		}
		return w.dero.Escrows()[tk.EscrowID].State == EscrowOpen
	}
	if !w.drive(400) {
		t.Fatalf("did not finish:\n%s", w.dump())
	}
	mk := w.state(RoleMaker)
	if _, ok := mk.DeroTxs["relay"]; !ok {
		t.Fatalf("the maker did not send the taker's claim:\n%s", w.dump())
	}
	w.check("maker relays the claim", []string{"swapped"}, []string{"swapped"})
}

// ---- GAP-028: where the files go ----

func TestWrittenFilesAndTheDefaultDirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "maker.json")
	files := strings.Join(WrittenFiles(p), "\n")
	for _, want := range []string{p, p + ".*.tmp", p + ".lock", p + ".refund.txt", UsedPath(p), ConsumedPath(p), UsedPath(p) + ".*.tmp"} {
		if !strings.Contains(files, want) {
			t.Errorf("WrittenFiles does not name %s:\n%s", want, files)
		}
	}
	d, err := DefaultDir()
	if err != nil || filepath.Base(d) != "zecswap" {
		t.Fatalf("DefaultDir %q %v", d, err)
	}
	if !InsideGitRepo("../../contracts/board.bas") {
		t.Fatal("a file of this repository was not seen as inside a git work tree")
	}
	if InsideGitRepo(p) {
		t.Fatal("a temporary directory was seen as inside a git work tree")
	}
}
