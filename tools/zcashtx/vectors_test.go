package zcashtx

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// testdata/zip_0244.json is the Zcash project's own vector file (see testdata/README.txt). Each row: tx, txid,
// auth_digest, amounts, script_pubkeys, transparent_input, sighash_shielded, sighash_all, ... Every row is read,
// Sapling and Orchard bundles included, and each pins the transaction id and the SIGHASH_ALL digest of the named
// input to the byte. This is the test that makes the vault's signatures the network's signatures.
func loadVectors(t *testing.T) [][]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("testdata/zip_0244.json")
	if err != nil {
		t.Fatalf("testdata/zip_0244.json is part of this package and must be present: %v", err)
	}
	var rows [][]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	return rows[2:] // two header rows
}

func str(t *testing.T, m json.RawMessage) string {
	var s string
	if err := json.Unmarshal(m, &s); err != nil {
		t.Fatalf("not a string: %s", m)
	}
	return s
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestZIP244Vectors(t *testing.T) {
	rows := loadVectors(t)
	checked, digests := 0, 0
	for i, row := range rows {
		raw := mustDecode(t, str(t, row[0]))
		tx, err := ParseV5(raw)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		wantID := mustDecode(t, str(t, row[1]))
		if got, ok := ComputeTxID(raw); !ok || got != DisplayHex([32]byte(wantID)) {
			t.Fatalf("vector %d: ComputeTxID %q %v", i, got, ok)
		}
		if !bytes.Equal(tx.Serialize(), raw) {
			t.Fatalf("vector %d: serialize(parse(tx)) differs", i)
		}
		if id := tx.TxID(); !bytes.Equal(id[:], wantID) {
			t.Fatalf("vector %d: txid %x, want %x", i, id, wantID)
		}
		var amounts []uint64
		var scripts []string
		var input *int
		json.Unmarshal(row[3], &amounts)
		json.Unmarshal(row[4], &scripts)
		json.Unmarshal(row[5], &input)
		if input != nil {
			if len(amounts) != len(tx.In) || len(scripts) != len(tx.In) {
				t.Fatalf("vector %d: %d amounts, %d scripts, %d inputs", i, len(amounts), len(scripts), len(tx.In))
			}
			for j := range tx.In {
				tx.In[j].Value = amounts[j]
				tx.In[j].ScriptPubKey = mustDecode(t, scripts[j])
			}
			got, err := tx.SigDigest(*input, SigHashAll)
			if err != nil {
				t.Fatal(err)
			}
			if want := mustDecode(t, str(t, row[7])); !bytes.Equal(got[:], want) {
				t.Fatalf("vector %d: sighash_all for input %d %x, want %x", i, *input, got, want)
			}
			digests++
		}
		checked++
	}
	t.Logf("%d of %d ZIP 244 vectors checked, %d SIGHASH_ALL digests", checked, len(rows), digests)
	if checked != len(rows) || digests == 0 {
		t.Fatalf("%d of %d vectors checked, %d digests", checked, len(rows), digests)
	}
}

// Transactions captured from a real zebrad 6.3.0 on regtest (testdata/regtest_v6.json): this package's ids are
// zebrad's for a v6 coinbase, a v6 P2PKH spend and a v5 spend built by librustzcash, and the v6 spend's signature
// verifies against this package's v6 digest — and not against the v5 one.
func TestRealTransactionsFromZebra(t *testing.T) {
	b, err := os.ReadFile("testdata/regtest_v6.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		V6Coinbase struct{ TxID, Hex string } `json:"v6_coinbase"`
		V6Spend    struct {
			TxID          string `json:"txid"`
			Hex           string `json:"hex"`
			PrevoutValue  uint64 `json:"prevout_value"`
			PrevoutScript string `json:"prevout_script"`
		} `json:"v6_p2pkh_spend"`
		V5Spend struct{ TxID, Hex string } `json:"v5_spend"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ id, hx string }{"v6 coinbase": {v.V6Coinbase.TxID, v.V6Coinbase.Hex}, "v6 spend": {v.V6Spend.TxID, v.V6Spend.Hex}, "v5 spend": {v.V5Spend.TxID, v.V5Spend.Hex}} {
		raw := mustDecode(t, c.hx)
		if id, ok := ComputeTxID(raw); !ok || id != c.id {
			t.Fatalf("%s: computed %q (%v), zebrad says %s", name, id, ok, c.id)
		}
		if _, _, err := TransparentParts(raw); err != nil {
			t.Fatalf("%s: the transparent reader: %v", name, err)
		}
	}
	raw := mustDecode(t, v.V6Spend.Hex)
	tx, err := ParseV6(raw)
	if err != nil || tx.Version != 6 || len(tx.In) != 1 {
		t.Fatalf("ParseV6: %+v %v", tx, err)
	}
	if !bytes.Equal(tx.Serialize(), raw) {
		t.Fatal("the v6 transaction does not serialize back to its bytes")
	}
	tx.In[0].Value, tx.In[0].ScriptPubKey = v.V6Spend.PrevoutValue, mustDecode(t, v.V6Spend.PrevoutScript)
	pushes, err := ScriptPushes(tx.In[0].ScriptSig)
	if err != nil || len(pushes) != 2 {
		t.Fatal("the spend's scriptSig is not <sig> <pubkey>")
	}
	d, err := tx.SigDigest(0, SigHashAll)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySig(pushes[0], pushes[1], d) {
		t.Fatal("librustzcash's v6 signature does not verify against this package's v6 signature digest")
	}
	tx.Version = 5
	if d5, _ := tx.SigDigest(0, SigHashAll); VerifySig(pushes[0], pushes[1], d5) {
		t.Fatal("the v6 signature verified against the v5 digest: the two are not being told apart")
	}
}

func TestParseKey(t *testing.T) {
	var zero, over [32]byte
	for i := range over {
		over[i] = 0xff
	}
	if _, err := ParseKey(zero); err == nil {
		t.Fatal("zero key accepted")
	}
	if _, err := ParseKey(over); err == nil {
		t.Fatal("key above the curve order accepted")
	}
	var one [32]byte
	one[31] = 1
	k, err := ParseKey(one)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(k.PubKey()) != "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798" {
		t.Fatalf("pubkey %x", k.PubKey())
	}
	if pkh := k.PubKeyHash(); hex.EncodeToString(pkh[:]) != "751e76e8199196d454941c45d1b3a323f1433bd6" {
		t.Fatalf("pkh %x", pkh)
	}
	// Base58Check against Bitcoin's well-known address of the same key (version byte 0x00): Zcash's transparent
	// addresses are the same construction with a two-byte prefix
	pkh := k.PubKeyHash()
	if got := Base58Check(append([]byte{0}, pkh[:]...)); got != "1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH" {
		t.Fatalf("Base58Check %s", got)
	}
}

// RFC 7693 Appendix A: BLAKE2b-512 of "abc", unkeyed, no personalization.
func TestBlake2b(t *testing.T) {
	want := "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923"
	if got := hex.EncodeToString(blake2bSum(64, nil, []byte("abc"))); got != want {
		t.Fatalf("blake2b-512(abc) = %s", got)
	}
}

// Every address form reads back as what it was written as, on the network and of the kind it was written for, and
// the four prefixes are the ones the protocol specification names.
func TestAddresses(t *testing.T) {
	var h [20]byte
	copy(h[:], mustDecode(t, "751e76e8199196d454941c45d1b3a323f1433bd6"))
	for _, c := range []struct {
		net    Network
		kind   Kind
		prefix string
	}{{Mainnet, P2PKH, "t1"}, {Mainnet, P2SH, "t3"}, {Testnet, P2PKH, "tm"}, {Testnet, P2SH, "t2"}} {
		a := EncodeAddress(c.net, c.kind, h)
		if a[:2] != c.prefix {
			t.Fatalf("%s %s encodes as %s", c.net, c.kind, a)
		}
		n, k, got, err := ParseAddress(a)
		if err != nil || n != c.net || k != c.kind || got != h {
			t.Fatalf("%s: %v %v %v %v", a, n, k, got, err)
		}
		spk, kind, err := AddressScript(a, c.net)
		if err != nil || kind != c.kind {
			t.Fatal(err)
		}
		if _, _, err := AddressScript(a, 1-c.net); err == nil {
			t.Fatalf("%s accepted on the other network", a)
		}
		if c.kind == P2SH {
			if sh, ok := P2SHHash(spk); !ok || sh != h {
				t.Fatal("P2SHHash")
			}
		} else if pk, ok := P2PKHHash(spk); !ok || pk != h {
			t.Fatal("P2PKHHash")
		}
		// one changed character is a bad checksum, never another address
		bad := []byte(a)
		bad[10] ^= 1
		if _, _, _, err := ParseAddress(string(bad)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	for _, s := range []string{"", "zs1abcdefghijklmnopqrstuvwxyz", "utest1qqqqqqqqqqqqqqqqqqqqqqqq", "1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH"} {
		if _, _, _, err := ParseAddress(s); err == nil {
			t.Fatalf("%q accepted as a transparent Zcash address", s)
		}
	}
}

// Push: the forms a redeem script of up to fifteen keys needs, and nothing past the element limit.
func TestPush(t *testing.T) {
	for n, prefix := range map[int][]byte{0: {0}, 20: {20}, 75: {75}, 76: {OpPushData1, 76}, 255: {OpPushData1, 255}, 256: {OpPushData2, 0, 1}, 520: {OpPushData2, 8, 2}} {
		d := bytes.Repeat([]byte{0xaa}, n)
		got, err := Push(d)
		if err != nil || !bytes.Equal(got[:len(prefix)], prefix) || len(got) != len(prefix)+n && n != 0 {
			t.Fatalf("Push(%d bytes) = %x… %v", n, got[:min(len(got), 4)], err)
		}
		if n > 0 {
			items, err := ScriptPushes(got)
			if err != nil || len(items) != 1 || !bytes.Equal(items[0], d) {
				t.Fatalf("%d bytes do not read back", n)
			}
		}
	}
	if _, err := Push(make([]byte, 521)); err == nil {
		t.Fatal("a 521-byte push was made")
	}
	if got, _ := Push([]byte{3}); !bytes.Equal(got, []byte{0x53}) {
		t.Fatalf("3 is OP_3 under MINIMALDATA, got %x", got)
	}
}
