package zecspv

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// zcashd's validator vectors: src/test/equihash_tests.cpp, validator_testvectors, zcash/zcash master at
// 558f686599586f55def3db86955d74d3be44605e. (96, 5), I = the sentence below, V = ArithToUint256(1), which is the
// byte 1 followed by 31 zero bytes. The index lists are copied as written there.
const zcashdInput = "Equihash is an asymmetric PoW based on the Generalised Birthday problem."

var zcashdNonce = append([]byte{1}, make([]byte, 31)...)

var zcashdValidator = []struct {
	name  string
	soln  []uint32
	valid bool
}{
	{"original valid solution", []uint32{2261, 15185, 36112, 104243, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 45858, 116805, 92842, 111026, 15972, 115059, 85191, 90330, 68190, 122819, 81830, 91132, 23460, 49807, 52426, 80391, 69567, 114474, 104973, 122568}, true},
	{"change one index", []uint32{2262, 15185, 36112, 104243, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 45858, 116805, 92842, 111026, 15972, 115059, 85191, 90330, 68190, 122819, 81830, 91132, 23460, 49807, 52426, 80391, 69567, 114474, 104973, 122568}, false},
	{"swap two arbitrary indices", []uint32{45858, 15185, 36112, 104243, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 2261, 116805, 92842, 111026, 15972, 115059, 85191, 90330, 68190, 122819, 81830, 91132, 23460, 49807, 52426, 80391, 69567, 114474, 104973, 122568}, false},
	{"reverse the first pair of indices", []uint32{15185, 2261, 36112, 104243, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 45858, 116805, 92842, 111026, 15972, 115059, 85191, 90330, 68190, 122819, 81830, 91132, 23460, 49807, 52426, 80391, 69567, 114474, 104973, 122568}, false},
	{"swap the first and second pairs of indices", []uint32{36112, 104243, 2261, 15185, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 45858, 116805, 92842, 111026, 15972, 115059, 85191, 90330, 68190, 122819, 81830, 91132, 23460, 49807, 52426, 80391, 69567, 114474, 104973, 122568}, false},
	{"swap the second-to-last and last pairs of indices", []uint32{2261, 15185, 36112, 104243, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 45858, 116805, 92842, 111026, 15972, 115059, 85191, 90330, 68190, 122819, 81830, 91132, 23460, 49807, 52426, 80391, 104973, 122568, 69567, 114474}, false},
	{"swap the first half and second half", []uint32{15972, 115059, 85191, 90330, 68190, 122819, 81830, 91132, 23460, 49807, 52426, 80391, 69567, 114474, 104973, 122568, 2261, 15185, 36112, 104243, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 45858, 116805, 92842, 111026}, false},
	{"sort the indices", []uint32{2261, 15185, 15972, 23460, 23779, 32642, 36112, 45858, 49807, 52426, 68190, 69567, 69878, 76925, 80080, 80391, 81830, 85191, 90330, 91132, 92842, 104243, 104973, 111026, 114474, 115059, 116805, 118332, 118390, 122568, 122819, 130041}, false},
	{"duplicate indices", []uint32{2261, 2261, 15185, 15185, 36112, 36112, 104243, 104243, 23779, 23779, 118390, 118390, 118332, 118332, 130041, 130041, 32642, 32642, 69878, 69878, 76925, 76925, 80080, 80080, 45858, 45858, 116805, 116805, 92842, 92842, 111026, 111026}, false},
	{"duplicate first half", []uint32{2261, 15185, 36112, 104243, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 45858, 116805, 92842, 111026, 2261, 15185, 36112, 104243, 23779, 118390, 118332, 130041, 32642, 69878, 76925, 80080, 45858, 116805, 92842, 111026}, false},
}

func mustEquihashParams(t testing.TB, n, k uint32) equihashParams {
	t.Helper()
	p, err := newEquihashParams(n, k)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEquihashZcashdValidatorVectors(t *testing.T) {
	p := mustEquihashParams(t, 96, 5)
	for _, v := range zcashdValidator {
		err := VerifyEquihash(96, 5, []byte(zcashdInput), zcashdNonce, minimalFromIndices(p, v.soln))
		if (err == nil) != v.valid {
			t.Errorf("%s: %v, want valid=%v", v.name, err, v.valid)
		}
	}
}

// librustzcash's all_bits_matter (components/equihash/src/verify.rs) spells out the minimal encoding of the valid
// solution above; zcashd's validator_allbitsmatter flips every bit of it.
var librustzcashMinimal = []byte{
	0x04, 0x6a, 0x8e, 0xd4, 0x51, 0xa2, 0x19, 0x73, 0x32, 0xe7, 0x1f, 0x39, 0xdb, 0x9c,
	0x79, 0xfb, 0xf9, 0x3f, 0xc1, 0x44, 0x3d, 0xa5, 0x8f, 0xb3, 0x8d, 0x05, 0x99, 0x17,
	0x21, 0x16, 0xd5, 0x55, 0xb1, 0xb2, 0x1f, 0x32, 0x70, 0x5c, 0xe9, 0x98, 0xf6, 0x0d,
	0xa8, 0x52, 0xf7, 0x7f, 0x0e, 0x7f, 0x4d, 0x63, 0xfc, 0x2d, 0xd2, 0x30, 0xa3, 0xd9,
	0x99, 0x53, 0xa0, 0x78, 0x7d, 0xfe, 0xfc, 0xab, 0x34, 0x1b, 0xde, 0xc8,
}

func TestEquihashAllBitsMatter(t *testing.T) {
	p := mustEquihashParams(t, 96, 5)
	if got := minimalFromIndices(p, zcashdValidator[0].soln); !bytes.Equal(got, librustzcashMinimal) {
		t.Fatalf("minimal encoding %x, librustzcash has %x", got, librustzcashMinimal)
	}
	if err := VerifyEquihash(96, 5, []byte(zcashdInput), zcashdNonce, librustzcashMinimal); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(librustzcashMinimal)*8; i++ {
		m := append([]byte(nil), librustzcashMinimal...)
		m[i/8] ^= 1 << (i % 8)
		if VerifyEquihash(96, 5, []byte(zcashdInput), zcashdNonce, m) == nil {
			t.Fatalf("flipping bit %d left the solution valid", i)
		}
	}
}

type equihashVectors struct {
	Source string `json:"source"`
	Valid  []struct {
		N         uint32     `json:"n"`
		K         uint32     `json:"k"`
		Input     string     `json:"input_hex"`
		Nonce     string     `json:"nonce_hex"`
		Solutions [][]uint32 `json:"solutions"`
	} `json:"valid"`
	Invalid []struct {
		N        uint32   `json:"n"`
		K        uint32   `json:"k"`
		Input    string   `json:"input_hex"`
		Nonce    string   `json:"nonce_hex"`
		Solution []uint32 `json:"solution"`
		Error    string   `json:"error"`
	} `json:"invalid"`
}

// librustzcash's equihash crate vectors (testdata/equihash-librustzcash.json says which commit): every valid
// solution, for (96, 5), (200, 9) and (144, 5), and the invalid ones, each failing for the crate's reason.
func TestEquihashLibrustzcashVectors(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "equihash-librustzcash.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v equihashVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	count := map[[2]uint32]int{}
	for _, tv := range v.Valid {
		p := mustEquihashParams(t, tv.N, tv.K)
		for _, s := range tv.Solutions {
			if err := VerifyEquihash(tv.N, tv.K, mustHex(t, tv.Input), mustHex(t, tv.Nonce), minimalFromIndices(p, s)); err != nil {
				t.Errorf("(%d, %d) input %s nonce %s: %v", tv.N, tv.K, tv.Input, tv.Nonce, err)
			}
			count[[2]uint32{tv.N, tv.K}]++
		}
	}
	if count[[2]uint32{200, 9}] < 5 || count[[2]uint32{96, 5}] < 10 {
		t.Fatalf("too few valid vectors: %v", count)
	}
	kinds := map[string]error{
		"Collision":       ErrEquihashCollision,
		"OutOfOrder":      ErrEquihashOrder,
		"DuplicateIdxs":   ErrEquihashDuplicate,
		"NonZeroRootHash": ErrEquihashNonZero,
	}
	for i, tv := range v.Invalid {
		want, ok := kinds[tv.Error]
		if !ok {
			t.Fatalf("unknown kind %s", tv.Error)
		}
		p := mustEquihashParams(t, tv.N, tv.K)
		err := VerifyEquihash(tv.N, tv.K, mustHex(t, tv.Input), mustHex(t, tv.Nonce), minimalFromIndices(p, tv.Solution))
		if !errors.Is(err, want) {
			t.Errorf("invalid vector %d: %v, want %v", i, err, want)
		}
	}
	t.Logf("librustzcash: %v valid solutions by (n, k), %d invalid", count, len(v.Invalid))
}

// §7.7.1's example: with i = [69, 42, 2^21, ...] (one-based), solution begins [0, 2, 32, 0, 10, 127, 255].
func TestEquihashSpecEncodingExample(t *testing.T) {
	p := mustEquihashParams(t, 200, 9)
	idx := make([]uint32, 512)
	idx[0], idx[1], idx[2] = 68, 41, 1<<21-1
	m := minimalFromIndices(p, idx)
	if len(m) != SolutionLen {
		t.Fatalf("length %d", len(m))
	}
	if want := []byte{0, 2, 32, 0, 10, 127, 255}; !bytes.Equal(m[:7], want) {
		t.Fatalf("first bytes %v, the spec has %v", m[:7], want)
	}
	back, err := indicesFromMinimal(p, m)
	if err != nil || back[0] != 68 || back[1] != 41 || back[2] != 1<<21-1 {
		t.Fatalf("decoded %v, %v", back[:3], err)
	}
}

// librustzcash minimal.rs array_compression_and_expansion: 21-bit chunks, one pattern alternating and one "based
// on example in the spec".
func TestDecodeBitsLibrustzcash(t *testing.T) {
	cases := []struct {
		compact []byte
		want    []uint32
	}{
		{[]byte{0xaa, 0xaa, 0xad, 0x55, 0x55, 0x6a, 0xaa, 0xab, 0x55, 0x55, 0x5a, 0xaa, 0xaa, 0xd5, 0x55, 0x56, 0xaa, 0xaa, 0xb5, 0x55, 0x55},
			[]uint32{0x155555, 0x155555, 0x155555, 0x155555, 0x155555, 0x155555, 0x155555, 0x155555}},
		{[]byte{0x00, 0x02, 0x20, 0x00, 0x0a, 0x7f, 0xff, 0xfe, 0x00, 0x12, 0x30, 0x22, 0xb3, 0x82, 0x26, 0xac, 0x19, 0xbd, 0xf2, 0x34, 0x56},
			[]uint32{0x000044, 0x000029, 0x1fffff, 0x000123, 0x004567, 0x0089ab, 0x00cdef, 0x123456}},
	}
	for _, c := range cases {
		got := decodeBits(c.compact, 21)
		if len(got) != len(c.want) {
			t.Fatalf("%d values", len(got))
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("value %d: %06x, want %06x", i, got[i], c.want[i])
			}
		}
	}
}

func TestEquihashRefusesBadParameters(t *testing.T) {
	for _, nk := range [][2]uint32{{0, 0}, {96, 2}, {201, 9}, {200, 10}, {200, 200}, {1024, 7}, {256, 7}} {
		if err := VerifyEquihash(nk[0], nk[1], nil, nil, nil); !errors.Is(err, ErrEquihashParams) {
			t.Errorf("(%d, %d): %v", nk[0], nk[1], err)
		}
	}
	for _, n := range []int{0, SolutionLen - 1, SolutionLen + 1} {
		if err := VerifyEquihash(200, 9, nil, nil, make([]byte, n)); !errors.Is(err, ErrEquihashParams) {
			t.Errorf("solution of %d bytes: %v", n, err)
		}
	}
}

// Every real header's solution verifies on its own, and fails once any part of the input changes.
func TestEquihashRealHeaders(t *testing.T) {
	var n int
	for _, f := range loadHeaderFiles(t) {
		for i, h := range f.headers(t) {
			pow := h.powHeader()
			if err := VerifyEquihash(200, 9, pow[:108], pow[108:], h.Solution); err != nil {
				t.Fatalf("%s height %d: %v", f.path, f.FirstHeight+uint32(i), err)
			}
			n++
		}
	}
	t.Logf("%d real (200, 9) solutions verified", n)
}

func TestEquihashTamperedRealHeader(t *testing.T) {
	f := loadHeaderFile(t, "mainnet-3436131.json")
	h := f.headers(t)[40]
	pow := append([]byte(nil), h.powHeader()...)
	sol := append([]byte(nil), h.Solution...)
	// a tampered solution: every 23rd bit, one at a time (the full sweep is in the (96, 5) test)
	for i := 0; i < len(sol)*8; i += 23 {
		m := append([]byte(nil), sol...)
		m[i/8] ^= 1 << (i % 8)
		if VerifyEquihash(200, 9, pow[:108], pow[108:], m) == nil {
			t.Fatalf("solution bit %d flipped and still valid", i)
		}
	}
	// a wrong nonce, and each other field of the header changed
	for _, off := range []int{offNonce, offNonce + 31, offVersion, 4, 36, 68, offTime, offBits} {
		m := append([]byte(nil), pow...)
		m[off] ^= 0x01
		if VerifyEquihash(200, 9, m[:108], m[108:], sol) == nil {
			t.Fatalf("header byte %d changed and the solution is still valid", off)
		}
	}
	// the index tree's own rules on a real solution
	p := mustEquihashParams(t, 200, 9)
	idx, _ := indicesFromMinimal(p, sol)
	swapped := append([]uint32(nil), idx...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if err := VerifyEquihash(200, 9, pow[:108], pow[108:], minimalFromIndices(p, swapped)); !errors.Is(err, ErrEquihashOrder) {
		t.Fatalf("first pair swapped: %v", err)
	}
	dup := append(append([]uint32(nil), idx[:256]...), idx[:256]...)
	if err := VerifyEquihash(200, 9, pow[:108], pow[108:], minimalFromIndices(p, dup)); !errors.Is(err, ErrEquihashDuplicate) {
		t.Fatalf("first half twice: %v", err)
	}
	if !bytes.Equal(minimalFromIndices(p, idx), sol) {
		t.Fatal("re-encoding a real solution does not give it back")
	}
}
