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

// ---- GAP-039: a Sapling-bearing transaction is never credited where its id matters ----

// This package does not implement ZIP 244's Sapling digest, so it cannot check the id of a v5 transaction with a
// Sapling bundle. The one place the id matters is the funding the maker credits before he reveals the secret, and
// there such a transaction is refused: the taker's own companion never builds one.
func TestSaplingBearingFundingIsNotCredited(t *testing.T) {
	ctx := context.Background()
	f := newFakeZcash(t, "test", startZec)
	h, _ := htlcFor(startZec + 500)
	id := f.Pay(h.ScriptPubKey(), 200_000)
	f.Mine(4)
	f.mu.Lock()
	raw := append([]byte(nil), f.txs[id].raw...)
	height, _ := f.minedAtLocked(id)
	bh := f.blocks[height-f.base].hash
	tip := f.tipLocked()
	f.mu.Unlock()
	raw[len(raw)-3] = 1 // nSpendsSapling: 1
	if _, err := ParseV5(raw); !errors.Is(err, ErrShielded) {
		t.Fatalf("the crafted transaction does not read as Sapling-bearing: %v", err)
	}
	f.override = func(method string) ([]byte, int, bool) {
		if method != "getrawtransaction" {
			return nil, 0, false
		}
		return []byte(fmt.Sprintf(`{"result":{"hex":"%x","txid":%q,"height":%d,"confirmations":%d,"blockhash":%q},"error":null,"id":1}`, raw, id, height, tip-height+1, bh)), 200, true
	}
	fund := Funding{TxID: id, Value: 200_000, Height: height, BlockHash: bh}
	err := ConfirmFunding(ctx, []ZcashSource{{Name: "node", Node: f.client()}}, "test", h, fund, 3, func(string, ...interface{}) {})
	if err == nil || !strings.Contains(err.Error(), "cannot be checked") {
		t.Fatalf("a Sapling-bearing funding was credited: %v", err)
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
	for _, want := range []string{p, p + ".tmp", p + ".lock", p + ".refund.txt", UsedPath(p), ConsumedPath(p), UsedPath(p) + ".tmp"} {
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
