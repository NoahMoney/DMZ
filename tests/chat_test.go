package tests

import (
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func installChat(t *testing.T, c *harness.Chain, owner, treasury *rpc.Address, fee, maxlen uint64) crypto.Hash {
	t.Helper()
	scid, res := c.Install(readContract(t, "chat.bas"), nil, rpc.Arguments{
		harness.S("treasury", treasury.String()), harness.U("fee", fee), harness.U("maxlen", maxlen),
	}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d of %d", res.GasStorage, harness.MaxStorageGas)
	}
	t.Logf("chat.bas install storage gas %d of %d", res.GasStorage, harness.MaxStorageGas)
	return scid
}

// A room is read by its own running index: rn_<room> then the last few rm_<room>_<j>. Nothing scans the whole
// contract, and one room's traffic never makes another room more expensive to read.
func TestChatRoomsAreIndependent(t *testing.T) {
	c := harness.New()
	owner, treasury, a, b := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installChat(t, c, owner, treasury, 1000, 280)

	post := func(who *rpc.Address, room, text string, fee uint64) harness.Result {
		return c.Call(scid, "Post", rpc.Arguments{harness.S("room", room), harness.S("text", text)},
			map[crypto.Hash]uint64{zero: fee}, who)
	}
	if r := post(a, "#dero", "first", 1000); !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	if r := post(b, "#dero", "second", 1000); !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	if r := post(a, "#xmr", "elsewhere", 1000); !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	if c.U(scid, "rn_#dero") != 2 || c.U(scid, "rn_#xmr") != 1 || c.U(scid, "n") != 3 {
		t.Fatalf("counts: dero %d xmr %d total %d", c.U(scid, "rn_#dero"), c.U(scid, "rn_#xmr"), c.U(scid, "n"))
	}
	// the record is author|height|text, and the author is the poster as ADDRESS_STRING
	rec := c.S(scid, "rm_#dero_0")
	parts := strings.SplitN(rec, "|", 3)
	if len(parts) != 3 || parts[2] != "first" {
		t.Fatalf("record %q", rec)
	}
	// the author is ADDRESS_STRING(SIGNER()), which the DVM always renders with the mainnet prefix whatever the
	// network is (DECISIONS §3), so assert the shape and that two posters differ rather than a literal string
	if !strings.HasPrefix(parts[0], "dero1") || len(parts[0]) < 60 {
		t.Fatalf("author %q is not an address string", parts[0])
	}
	if b0 := strings.SplitN(c.S(scid, "rm_#dero_1"), "|", 3)[0]; b0 == parts[0] {
		t.Fatalf("both messages recorded the same author %q", b0)
	}
	_ = b
	if c.S(scid, "rm_#xmr_0") == c.S(scid, "rm_#dero_0") {
		t.Fatal("rooms share storage")
	}
}

// The fee is the whole of the spam control, because a deployed DMZ has no moderator. It must be exact, and it
// must reach the treasury rather than sitting in the contract.
func TestChatFeeIsExactAndPaid(t *testing.T) {
	c := harness.New()
	owner, treasury, a := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installChat(t, c, owner, treasury, 1000, 280)
	post := func(text string, fee uint64) harness.Result {
		return c.Call(scid, "Post", rpc.Arguments{harness.S("room", "#dero"), harness.S("text", text)},
			map[crypto.Hash]uint64{zero: fee}, a)
	}
	if post("underpaid", 999).OK() {
		t.Fatal("a message posted for less than the fee")
	}
	if post("overpaid", 1001).OK() {
		t.Fatal("an overpayment was accepted: the fee is exact so nothing is silently kept")
	}
	r := post("paid", 1000)
	if !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	if got := r.Sent(zero, treasury); got != 1000 {
		t.Fatalf("treasury received %d of the 1000 fee", got)
	}
	if c.SCBalance(scid, zero) != 0 {
		t.Fatalf("the contract kept %d: fees must not accumulate where nobody can claim them", c.SCBalance(scid, zero))
	}
	if c.U(scid, "rn_#dero") != 1 {
		t.Fatalf("messages stored %d, want 1: the refused posts must not have been recorded", c.U(scid, "rn_#dero"))
	}
}

// Bounds the client cannot be trusted to enforce, because anyone can call the contract directly.
func TestChatBounds(t *testing.T) {
	c := harness.New()
	owner, treasury, a := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installChat(t, c, owner, treasury, 0, 64)
	post := func(room, text string) harness.Result {
		return c.Call(scid, "Post", rpc.Arguments{harness.S("room", room), harness.S("text", text)}, nil, a)
	}
	if post("", "hi").OK() {
		t.Fatal("an empty room name was accepted")
	}
	if post(strings.Repeat("r", 33), "hi").OK() {
		t.Fatal("a room name over 32 characters was accepted")
	}
	if post("#dero", "").OK() {
		t.Fatal("an empty message was accepted")
	}
	if post("#dero", strings.Repeat("x", 65)).OK() {
		t.Fatal("a message over maxlen was accepted")
	}
	if r := post("#dero", strings.Repeat("x", 64)); !r.OK() {
		t.Fatalf("a message of exactly maxlen was refused: %v", r.Err)
	}
	// a fee of zero means the call must carry nothing, not "anything"
	if c.Call(scid, "Post", rpc.Arguments{harness.S("room", "#dero"), harness.S("text", "hi")},
		map[crypto.Hash]uint64{zero: 1}, a).OK() {
		t.Fatal("value was accepted by a contract whose fee is zero")
	}
	// install-time bounds
	if _, res := c.Install(readContract(t, "chat.bas"), nil, rpc.Arguments{
		harness.S("treasury", treasury.String()), harness.U("fee", 0), harness.U("maxlen", 0)}, owner, harness.MaxStorageGas); res.OK() {
		t.Fatal("maxlen 0 installed: no message could ever be posted")
	}
	if _, res := c.Install(readContract(t, "chat.bas"), nil, rpc.Arguments{
		harness.S("treasury", treasury.String()), harness.U("fee", 0), harness.U("maxlen", 401)}, owner, harness.MaxStorageGas); res.OK() {
		t.Fatal("maxlen over 400 installed")
	}
}

// The text is stored verbatim, separators included. A client must split on the FIRST TWO separators only, or a
// message containing one is truncated — and a message is exactly where a hostile string would be put.
func TestChatTextKeepsItsSeparators(t *testing.T) {
	c := harness.New()
	owner, treasury, a := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installChat(t, c, owner, treasury, 0, 280)
	text := "a|b|c and a newline is not allowed by the client but the contract stores what it is given"
	if r := c.Call(scid, "Post", rpc.Arguments{harness.S("room", "#dero"), harness.S("text", text)}, nil, a); !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	parts := strings.SplitN(c.S(scid, "rm_#dero_0"), "|", 3)
	if len(parts) != 3 || parts[2] != text {
		t.Fatalf("text came back as %q", parts[len(parts)-1])
	}
}
