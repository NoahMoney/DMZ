package zecswap

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// testdata/zip_0244.json is the Zcash project's own vector file (see testdata/README.txt). Each row: tx, txid,
// auth_digest, amounts, script_pubkeys, transparent_input, sighash_shielded, sighash_all, ... Every row is read,
// Sapling and Orchard bundles included (sapling.go, orchard.go; GAP-039), and each pins the transaction id and the
// SIGHASH_ALL digest of the named input to the byte.
func loadVectors(t *testing.T) [][]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("testdata/zip_0244.json")
	if err != nil {
		t.Skip("no testdata/zip_0244.json")
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

func TestZIP244Vectors(t *testing.T) {
	rows := loadVectors(t)
	checked, sapling, withSpends, withOutputs := 0, 0, 0, 0
	for i, row := range rows {
		raw, err := hex.DecodeString(str(t, row[0]))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := ParseV5(raw)
		if err != nil {
			t.Fatalf("vector %d: %v", i, err)
		}
		if tx.Sapling != nil {
			sapling++
			if len(tx.Sapling.Spends) > 0 {
				withSpends++
			}
			if len(tx.Sapling.Outputs) > 0 {
				withOutputs++
			}
		}
		// the path RawTx takes to verify a node's answer agrees, in display order
		if want, _ := hex.DecodeString(str(t, row[1])); len(want) == 32 {
			if got, ok := ComputeTxID(raw); !ok || got != DisplayHex([32]byte(want)) {
				t.Fatalf("vector %d: ComputeTxID %q %v", i, got, ok)
			}
		}
		// the parser and the serializer are inverses on every vector they accept
		if !bytes.Equal(tx.Serialize(), raw) {
			t.Fatalf("vector %d: serialize(parse(tx)) differs", i)
		}
		wantID, _ := hex.DecodeString(str(t, row[1]))
		if id := tx.TxID(); !bytes.Equal(id[:], wantID) {
			t.Fatalf("vector %d: txid %x, want %x", i, id, wantID)
		}
		// the signature digest of the named transparent input, with the spent amounts and scripts the row gives
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
				tx.In[j].ScriptPubKey, _ = hex.DecodeString(scripts[j])
			}
			want, _ := hex.DecodeString(str(t, row[7]))
			got, err := tx.SigDigest(*input, SigHashAll)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got[:], want) {
				t.Fatalf("vector %d: sighash_all for input %d %x, want %x", i, *input, got, want)
			}
		}
		checked++
	}
	t.Logf("%d of %d vectors checked (txid, ComputeTxID and, where given, SIGHASH_ALL); %d carry a Sapling bundle, %d with spends and %d with outputs", checked, len(rows), sapling, withSpends, withOutputs)
	if checked != len(rows) || withSpends == 0 || withOutputs == 0 {
		t.Fatalf("%d of %d vectors checked, %d with Sapling spends, %d with Sapling outputs: every row must be read, both kinds included", checked, len(rows), withSpends, withOutputs)
	}
}

// A swap claim end to end at the byte level: the taker funded the script; the maker builds the spend, signs
// its digest with the claim key, and the signature verifies against the compressed key whose HASH160 the
// script names. No node, no broadcast — the spend's validity under the interpreter is the PoC's and BL-42's.
func TestClaimSpendSigns(t *testing.T) {
	var kb [32]byte
	kb[31] = 7
	key, err := ParseKey(kb)
	if err != nil {
		t.Fatal(err)
	}
	h := vector(t)
	h.ClaimPkh = key.PubKeyHash()
	var prev [32]byte
	prev[0] = 0xaa
	tx := &Tx{BranchID: BranchNU6, ExpiryHeight: 3_000_500, In: []TxIn{{PrevOut: OutPoint{Hash: prev, Index: 1}, Sequence: 0xffffffff, Value: 25_000_000, ScriptPubKey: h.ScriptPubKey()}},
		Out: []TxOut{{Value: 25_000_000 - 10_000, ScriptPubKey: append(append([]byte{opDup, opHash160}, push(h.ClaimPkh[:])...), opEqualVerify, opCheckSig)}}}
	digest, err := tx.SigDigest(0, SigHashAll)
	if err != nil {
		t.Fatal(err)
	}
	var secret [32]byte
	copy(secret[:], bytes.Repeat([]byte{0x11}, 32))
	sig := key.Sign(digest)
	if !VerifySig(sig, key.PubKey(), digest) {
		t.Fatal("the claim signature does not verify")
	}
	if VerifySig(sig, key.PubKey(), [32]byte{}) {
		t.Fatal("a signature verified against the wrong digest")
	}
	tx.In[0].ScriptSig = h.ClaimScriptSig(sig, key.PubKey(), secret)
	back, err := ParseV5(tx.Serialize())
	if err != nil || !bytes.Equal(back.In[0].ScriptSig, tx.In[0].ScriptSig) || back.TxID() != tx.TxID() {
		t.Fatalf("round trip of the signed claim: %v", err)
	}
	// the secret is readable from the serialized claim, which is how the taker learns it (§3 step 6)
	if !bytes.Contains(back.In[0].ScriptSig, secret[:]) {
		t.Fatal("the secret is not in the claim's scriptSig")
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
	// the well-known key 1: compressed public key 0279be…f81798, HASH160 751e76e8199196d454941c45d1b3a323f1433bd6
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
}

// RFC 7693 Appendix A: BLAKE2b-512 of "abc", unkeyed, no personalization.
func TestBlake2b(t *testing.T) {
	want := "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d17d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923"
	if got := hex.EncodeToString(blake2bSum(64, nil, []byte("abc"))); got != want {
		t.Fatalf("blake2b-512(abc) = %s", got)
	}
	// an empty message is one zero block with the final flag; a message of exactly 128 bytes is one full block
	// compressed as the last one; 129 bytes are two blocks. Each must differ from the others.
	a, b, c := blake2bSum(32, nil, nil), blake2bSum(32, nil, bytes.Repeat([]byte{1}, 128)), blake2bSum(32, nil, bytes.Repeat([]byte{1}, 129))
	if bytes.Equal(a, b) || bytes.Equal(b, c) {
		t.Fatal("block boundary handling")
	}
	// personalization changes the digest and nothing else does
	if bytes.Equal(blake2bSum(32, []byte("ZTxIdHeadersHash"), nil), blake2bSum(32, []byte("ZTxIdPrevoutHash"), nil)) {
		t.Fatal("personalization ignored")
	}
}
