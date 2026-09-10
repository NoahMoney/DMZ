package zecswap

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// FuzzParseV5NeverPanics: ParseV5 is handed raw bytes that ultimately come off a chain anyone can write to.
// It must return an error, never panic.
func FuzzParseV5NeverPanics(f *testing.F) {
	tx := &Tx{BranchID: BranchNU5, LockTime: 1, ExpiryHeight: 2,
		In:  []TxIn{{PrevOut: OutPoint{Index: 0}, ScriptSig: []byte{1, 2, 3}, Sequence: 0xfffffffe}},
		Out: []TxOut{{Value: 1000, ScriptPubKey: []byte{0x76, 0xa9}}}}
	f.Add(tx.Serialize())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ParseV5 panicked on %d bytes: %v\nraw=%x", len(raw), r, raw)
			}
		}()
		_, _ = ParseV5(raw)
	})
}

// TestParseV5HugeCompactSize is the minimised case: a script length of 2^63-1 makes r.p+n overflow int.
func TestParseV5HugeCompactSize(t *testing.T) {
	var b bytes.Buffer
	b.Write(le32(V5Version))
	b.Write(le32(V5VersionGroupID))
	b.Write(le32(BranchNU5))
	b.Write(le32(0)) // locktime
	b.Write(le32(0)) // expiry
	b.Write(CompactSize(1))
	b.Write(make([]byte, 32)) // prevout hash
	b.Write(le32(0))          // prevout index
	// scriptSig length: 0xff followed by 2^63-1
	b.WriteByte(0xff)
	n := make([]byte, 8)
	binary.LittleEndian.PutUint64(n, uint64(math.MaxInt64))
	b.Write(n)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ParseV5 panicked instead of erroring: %v", r)
		}
	}()
	if _, err := ParseV5(b.Bytes()); err == nil {
		t.Fatal("expected an error")
	}
}

// FuzzCompactSizeRoundTrips: every uint64 encodes and reads back as itself.
func FuzzCompactSizeRoundTrips(f *testing.F) {
	for _, v := range []uint64{0, 1, 0xfc, 0xfd, 0xffff, 0x10000, 0xffffffff, 0x100000000, math.MaxUint64} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v uint64) {
		enc := CompactSize(v)
		r := &reader{b: enc}
		got := r.compact()
		if r.err != nil {
			t.Fatalf("CompactSize(%d) = %x did not read back: %v", v, enc, r.err)
		}
		if got != v {
			t.Fatalf("CompactSize round trip: %d -> %x -> %d", v, enc, got)
		}
		if r.p != len(enc) {
			t.Fatalf("CompactSize(%d) = %x consumed %d of %d bytes", v, enc, r.p, len(enc))
		}
	})
}

// FuzzScriptNumNeverPanics: ScriptNum is the only place a caller-supplied number becomes script bytes.
func FuzzScriptNumNeverPanics(f *testing.F) {
	for _, v := range []int64{0, 1, -1, 127, 128, -128, 3000000, math.MaxInt64, math.MinInt64, math.MinInt64 + 1} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, n int64) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ScriptNum(%d) panicked: %v", n, r)
			}
		}()
		out := ScriptNum(n)
		if len(out) > 0 && n != 0 {
			// minimal encoding: the last byte must carry data or a sign bit
			if out[len(out)-1] == 0x00 && len(out) > 1 && out[len(out)-2]&0x80 == 0 {
				t.Fatalf("ScriptNum(%d) = %x is not minimal", n, out)
			}
		}
	})
}
