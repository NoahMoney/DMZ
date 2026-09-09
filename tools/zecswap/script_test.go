package zecswap

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// The vector is research/crosschain/poc/zec-t-htlc/run.log: the script that Zcash's consensus interpreter
// accepted for a claim with the secret and a refund after the lock time, and refused for the five wrong ways.
// The PoC names its claim key "taker" and its refund key "maker"; docs/ZEC_SWAP.md gives the secret to the
// maker, so the names swap and the bytes do not.
const (
	vecHash      = "9156797605b5272d9b2dc688e5d64d05311ce1768f7044850e75d9b2a9eb7bdb"
	vecClaimPkh  = "07a60d31b51e41efe6301c676a3179416aea28bb"
	vecRefundPkh = "e3556bb30898a8b88fcef586864b9e408685eb09"
	vecCLTV      = 3_000_000
	vecRedeem    = "63a8209156797605b5272d9b2dc688e5d64d05311ce1768f7044850e75d9b2a9eb7bdb8876a91407a60d31b51e41efe6301c676a3179416aea28bb88ac6703c0c62db17576a914e3556bb30898a8b88fcef586864b9e408685eb0988ac68"
	vecSPK       = "a91446bac160b08d5c3529060ea67e2b5bc51168f1d387"
	vecAddress   = "t2CznzyM1SiyHb5CLc46GkAgoiMPnBLvuQ2"
)

func vector(t *testing.T) HTLC {
	t.Helper()
	var h HTLC
	mustHex(t, h.Hash[:], vecHash)
	mustHex(t, h.ClaimPkh[:], vecClaimPkh)
	mustHex(t, h.RefundPkh[:], vecRefundPkh)
	h.CLTV = vecCLTV
	return h
}

func mustHex(t *testing.T, dst []byte, s string) {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(dst) {
		t.Fatalf("bad vector %s", s)
	}
	copy(dst, b)
}

func TestVector(t *testing.T) {
	h := vector(t)
	if got := hex.EncodeToString(h.RedeemScript()); got != vecRedeem {
		t.Fatalf("redeem script\n got %s\nwant %s", got, vecRedeem)
	}
	if got := hex.EncodeToString(h.ScriptPubKey()); got != vecSPK {
		t.Fatalf("scriptPubKey got %s want %s", got, vecSPK)
	}
	if got := h.Address(Testnet); got != vecAddress {
		t.Fatalf("testnet address got %s want %s", got, vecAddress)
	}
	if len(h.RedeemScript()) != 94 {
		t.Fatalf("redeem script is %d bytes, the PoC's is 94", len(h.RedeemScript()))
	}
	// the mainnet form of the same script hash carries the t3 prefix and decodes back to the same hash
	main := h.Address(Mainnet)
	net, kind, hash, err := ParseAddress(main)
	if err != nil || net != Mainnet || kind != P2SH || hash != h.ScriptHash() || main[:2] != "t3" {
		t.Fatalf("mainnet address %s: %v %v %v", main, net, kind, err)
	}
}

func TestParseAddress(t *testing.T) {
	net, kind, hash, err := ParseAddress(vecAddress)
	if err != nil || net != Testnet || kind != P2SH || hex.EncodeToString(hash[:]) != "46bac160b08d5c3529060ea67e2b5bc51168f1d3" {
		t.Fatalf("parse %s: %v %v %x %v", vecAddress, net, kind, hash, err)
	}
	// a P2PKH address made from a key hash round-trips on both networks, and its prefix reads as documented
	var pkh [20]byte
	mustHex(t, pkh[:], vecClaimPkh)
	for _, c := range []struct {
		net    Network
		prefix string
	}{{Mainnet, "t1"}, {Testnet, "tm"}} {
		a := EncodeAddress(c.net, P2PKH, pkh)
		if a[:2] != c.prefix {
			t.Fatalf("%s p2pkh address %s does not start with %s", c.net, a, c.prefix)
		}
		n, k, h, err := ParseAddress(a)
		if err != nil || n != c.net || k != P2PKH || h != pkh {
			t.Fatalf("round trip %s: %v %v %v", a, n, k, err)
		}
	}
	// a shielded address, a corrupted checksum and garbage all fail, with the reason a screen can show
	for _, bad := range []string{"zs1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq", vecAddress[:len(vecAddress)-1] + "3", "u1notanaddress", "", "0OIl"} {
		if _, _, _, err := ParseAddress(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestScriptNum(t *testing.T) {
	cases := map[int64]string{0: "", 1: "01", 16: "10", 127: "7f", 128: "8000", 255: "ff00", 256: "0001", -1: "81", -128: "8080", 3_000_000: "c0c62d", 2147483647: "ffffff7f", 500_000: "20a107"}
	for n, want := range cases {
		if got := hex.EncodeToString(ScriptNum(n)); got != want {
			t.Errorf("ScriptNum(%d) = %s, want %s", n, got, want)
		}
	}
}

func TestSpendLayouts(t *testing.T) {
	h := vector(t)
	sig := bytes.Repeat([]byte{0x30}, 71)
	pub := bytes.Repeat([]byte{0x02}, 33)
	var secret [32]byte
	copy(secret[:], bytes.Repeat([]byte{0xaa}, 32))
	redeem := h.RedeemScript()
	claim := h.ClaimScriptSig(sig, pub, secret)
	// <71 sig> <33 pub> <32 secret> OP_TRUE PUSHDATA1 94 <redeem>
	want := append([]byte{71}, sig...)
	want = append(want, 33)
	want = append(want, pub...)
	want = append(want, 32)
	want = append(want, secret[:]...)
	want = append(want, opTrue, opPushData1, 94)
	want = append(want, redeem...)
	if !bytes.Equal(claim, want) {
		t.Fatalf("claim scriptSig layout wrong:\n%x\n%x", claim, want)
	}
	refund := h.RefundScriptSig(sig, pub)
	want = append([]byte{71}, sig...)
	want = append(want, 33)
	want = append(want, pub...)
	want = append(want, opFalse, opPushData1, 94)
	want = append(want, redeem...)
	if !bytes.Equal(refund, want) {
		t.Fatalf("refund scriptSig layout wrong:\n%x\n%x", refund, want)
	}
	if lt := h.LockTimeBytes(); lt != [4]byte{0xc0, 0xc6, 0x2d, 0x00} {
		t.Fatalf("nLockTime bytes %x", lt)
	}
}

func TestBase58Check(t *testing.T) {
	for _, payload := range [][]byte{{0x1c, 0xba, 0x46, 0xba}, {0, 0, 0, 1}, {0xff}, bytes.Repeat([]byte{0x00}, 22)} {
		s := Base58Check(payload)
		back, err := DecodeBase58Check(s)
		if err != nil || !bytes.Equal(back, payload) {
			t.Fatalf("round trip %x → %s → %x %v", payload, s, back, err)
		}
	}
	if _, err := DecodeBase58Check("1111"); err == nil {
		t.Fatal("a four-byte string has no room for a checksum")
	}
}
