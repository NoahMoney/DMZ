package xmrswap

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"

	"filippo.io/edwards25519"
)

// Every base58 and address vector below is copied from monero-project/monero tests/unit_tests/base58.cpp
// (fetched from master on 2026-09-08); the addresses from Monero's other tests and its published reference
// are cited where they appear. None was produced by the code under test.

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad test hex %q: %v", s, err)
	}
	return b
}

// TEST_encode_block: one block of 1..8 bytes and its digits; do_test_encode_block also decodes it back.
func TestBase58BlockVectors(t *testing.T) {
	vectors := []struct{ raw, enc string }{
		{"00", "11"}, {"39", "1z"}, {"ff", "5Q"},
		{"0000", "111"}, {"0039", "11z"}, {"0100", "15R"}, {"ffff", "LUv"},
		{"000000", "11111"}, {"000039", "1111z"}, {"010000", "11LUw"}, {"ffffff", "2UzHL"},
		{"00000039", "11111z"}, {"ffffffff", "7YXq9G"},
		{"0000000039", "111111z"}, {"ffffffffff", "VtB5VXc"},
		{"000000000039", "11111111z"}, {"ffffffffffff", "3CUsUpv9t"},
		{"00000000000039", "111111111z"}, {"ffffffffffffff", "Ahg1opVcGW"},
		{"0000000000000039", "1111111111z"}, {"ffffffffffffffff", "jpXCZedGfVQ"},
		{"0000000000000000", "11111111111"}, {"0000000000000001", "11111111112"},
		{"0000000000000008", "11111111119"}, {"0000000000000009", "1111111111A"},
		{"000000000000003a", "11111111121"}, {"00ffffffffffffff", "1Ahg1opVcGW"},
		{"06156013762879f7", "22222222222"}, {"05e022ba374b2a00", "1z111111111"},
	}
	for _, v := range vectors {
		if got := base58Encode(unhex(t, v.raw)); got != v.enc {
			t.Errorf("encode %s: got %q, want %q", v.raw, got, v.enc)
		}
		dec, err := base58Decode(v.enc)
		if err != nil || hex.EncodeToString(dec) != v.raw {
			t.Errorf("decode %q: got %x, %v; want %s", v.enc, dec, err, v.raw)
		}
	}
	// TEST_decode_block_neg: bad lengths (1, 4, 8 digits), overflow, symbols outside the alphabet
	for _, bad := range []string{"1", "z", "5R", "zz", "LUw", "zzz", "1111", "zzzz", "2UzHM", "zzzzz", "7YXq9H", "zzzzzz",
		"VtB5VXd", "zzzzzzz", "11111111", "zzzzzzzz", "3CUsUpv9u", "zzzzzzzzz", "Ahg1opVcGX", "zzzzzzzzzz",
		"jpXCZedGfVR", "zzzzzzzzzzz", "01111111111", "11111111110", "11111011111", "I1111111111", "O1111111111",
		"l1111111111", "_1111111111"} {
		if _, err := base58Decode(bad); err == nil {
			t.Errorf("decode %q: accepted, want refusal", bad)
		}
	}
}

// TEST_encode (multi-block) and TEST_decode_pos / TEST_decode_neg.
func TestBase58StringVectors(t *testing.T) {
	for n := 1; n <= 16; n++ {
		want := []int{0, 2, 3, 5, 6, 7, 9, 10, 11, 13, 14, 16, 17, 18, 20, 21, 22}[n]
		if got := base58Encode(make([]byte, n)); got != strings.Repeat("1", want) {
			t.Errorf("encode %d zero bytes: got %q, want %d ones", n, got, want)
		}
	}
	if got := base58Encode(unhex(t, "06156013762879f7ffffffffff")); got != "22222222222VtB5VXc" {
		t.Errorf("encode two blocks: got %q", got)
	}
	pos := []struct{ enc, raw string }{
		{"", ""}, {"5Q", "ff"}, {"LUv", "ffff"}, {"2UzHL", "ffffff"}, {"7YXq9G", "ffffffff"}, {"VtB5VXc", "ffffffffff"},
		{"3CUsUpv9t", "ffffffffffff"}, {"Ahg1opVcGW", "ffffffffffffff"}, {"jpXCZedGfVQ", "ffffffffffffffff"},
		{"jpXCZedGfVQ5Q", strings.Repeat("ff", 9)}, {"jpXCZedGfVQLUv", strings.Repeat("ff", 10)},
		{"jpXCZedGfVQ2UzHL", strings.Repeat("ff", 11)}, {"jpXCZedGfVQ7YXq9G", strings.Repeat("ff", 12)},
		{"jpXCZedGfVQVtB5VXc", strings.Repeat("ff", 13)}, {"jpXCZedGfVQ3CUsUpv9t", strings.Repeat("ff", 14)},
		{"jpXCZedGfVQAhg1opVcGW", strings.Repeat("ff", 15)}, {"jpXCZedGfVQjpXCZedGfVQ", strings.Repeat("ff", 16)},
	}
	for _, v := range pos {
		dec, err := base58Decode(v.enc)
		if err != nil || hex.EncodeToString(dec) != v.raw {
			t.Errorf("decode %q: got %x, %v; want %s", v.enc, dec, err, v.raw)
		}
	}
	neg := []string{
		// invalid length
		"1", "z", "1111", "zzzz", "11111111", "zzzzzzzz", "123456789AB1", "123456789ABz", "123456789AB1111",
		"123456789ABzzzz", "123456789AB11111111", "123456789ABzzzzzzzz",
		// overflow
		"5R", "zz", "LUw", "zzz", "2UzHM", "zzzzz", "7YXq9H", "zzzzzz", "VtB5VXd", "zzzzzzz", "3CUsUpv9u", "zzzzzzzzz",
		"Ahg1opVcGX", "zzzzzzzzzz", "jpXCZedGfVR", "zzzzzzzzzzz", "123456789AB5R", "123456789ABzz", "123456789ABLUw",
		"123456789ABzzz", "123456789AB2UzHM", "123456789ABzzzzz", "123456789AB7YXq9H", "123456789ABzzzzzz",
		"123456789ABVtB5VXd", "123456789ABzzzzzzz", "123456789AB3CUsUpv9u", "123456789ABzzzzzzzzz",
		"123456789ABAhg1opVcGX", "123456789ABzzzzzzzzzz", "123456789ABjpXCZedGfVR", "123456789ABzzzzzzzzzzz",
		"zzzzzzzzzzz11",
		// invalid symbols
		"10", "11I", "11O11", "11l111", "11_11111111", "1101111111111", "11I11111111111111",
		"11O1111111111111111111", "1111111111110", "111111111111l1111", "111111111111_111111111",
	}
	for _, bad := range neg {
		if _, err := base58Decode(bad); err == nil {
			t.Errorf("decode %q: accepted, want refusal", bad)
		}
	}
}

// TEST_encode_decode_addr: (tag, data) -> address, and back.
func TestEncodeAddrVectors(t *testing.T) {
	zeros, ffs := strings.Repeat("00", 64), strings.Repeat("ff", 64)
	ramp := strings.Repeat("00112233445566778899aabbccddeeff", 4)
	vectors := []struct {
		tag  uint64
		data string
		addr string
	}{
		{6, zeros, "21D35quxec71111111111111111111111111111111111111111111111111111111111111111111111111111116Q5tCH"},
		{6, ffs, "2Aui6ejTFscjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQVqegMoV"},
		{0, ramp, "1119XrkPuSmLzdHXgVgrZKjepg5hZAxffLzdHXgVgrZKjepg5hZAxffLzdHXgVgrZKjepg5hZAxffLzdHXgVgrZKVphZRvn"},
		{0, zeros, "111111111111111111111111111111111111111111111111111111111111111111111111111111111111111115TXfiA"},
		{0x1122334455667788, zeros, "PuT7GAdgbA83qvSEivPLYo11111111111111111111111111111111111111111111111111111111111111111111111111111169tWrH"},
		{0x1122334455667788, ffs, "PuT7GAdgbA841d7FXjswpJjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQjpXCZedGfVQVq4LL1v"},
		{0x1122334455667788, "11", "PuT7GAdgbA819VwdWVDP"},
		{0x1122334455667788, "2222", "PuT7GAdgbA81efAfdCjPg"},
		{0x1122334455667788, "333333", "PuT7GAdgbA83sryEt3YC8Q"},
		{0x1122334455667788, "44444444", "PuT7GAdgbA83tWUuc54PFP3b"},
		{0x1122334455667788, "5555555555", "PuT7GAdgbA83u9zaKrtRKZ1J6"},
		{0x1122334455667788, "666666666666", "PuT7GAdgbA83uoWF3eanGG1aRoG"},
		{0x1122334455667788, "77777777777777", "PuT7GAdgbA83vT1umSHMYJ4oNVdu"},
		{0x1122334455667788, "8888888888888888", "PuT7GAdgbA83w6XaVDyvpoGQBEWbB"},
		{0x1122334455667788, "999999999999999999", "PuT7GAdgbA83wk3FD1gW7J2KVGofA1r"},
		{0, "", "15p2yAV"},
		{0x7f, "", "FNQ3D6A"},
		{0x80, "", "26k9QWweu"},
		{0xff, "", "3BzAD7n3y"},
		{0, "11223344556677", "11efCaY6UjG7JrxuB"},
		{6, "11223344556677", "21rhHRT48LN4PriP9"},
	}
	for _, v := range vectors {
		if got := encodeAddr(v.tag, unhex(t, v.data)); got != v.addr {
			t.Errorf("encodeAddr(%#x, %s): got %q, want %q", v.tag, v.data, got, v.addr)
		}
		tag, data, err := decodeAddr(v.addr)
		if err != nil || tag != v.tag || hex.EncodeToString(data) != v.data {
			t.Errorf("decodeAddr(%q): got %#x %x %v; want %#x %s", v.addr, tag, data, err, v.tag, v.data)
		}
	}
	// TEST_decode_addr_neg
	for _, bad := range []string{
		"zuT7GAdgbA819VwdWVDP", "0uT7GAdgbA819VwdWVDP", "IuT7GAdgbA819VwdWVDP", "OuT7GAdgbA819VwdWVDP",
		"luT7GAdgbA819VwdWVDP", "\x00uT7GAdgbA819VwdWVDP", "PuT7GAdgbA819VwdWVD", "11efCaY6UjG7JrxuC",
		"jerj2e4mESo", "1", "1111", "11", "111", "11111", "111111", "999999", "ZZZZZZ",
	} {
		if _, _, err := decodeAddr(bad); err == nil {
			t.Errorf("decodeAddr(%q): accepted, want refusal", bad)
		}
	}
}

// get_account_address_as_str.works_correctly: test_serialized_keys (spend ‖ view) on MAINNET.
func TestAddressFromMoneroUnitTest(t *testing.T) {
	var spend, view Point
	copy(spend[:], unhex(t, "f724bc5c6cfbb9d97602c300423a2f28641874513a035778a0c1778d833201e9"))
	copy(view[:], unhex(t, "220939689edf1abd5bc1d031f73ecd6c993add66d6808870456afeb8e7eeb68d"))
	const want = "4AzKEX4gXdJdNeM6dfiBFL7kqund3HYGvMBF3ttsNd9SfzgYB6L7ep1Yg1osYJzLdaKAYSLVh6e6jKnAuzj3bw1oGy9kXCb"
	got, err := Address(spend, view, Mainnet)
	if err != nil || got != want {
		t.Fatalf("Address: got %q, %v; want %q", got, err, want)
	}
	pa, err := ParseAddress(want)
	if err != nil || pa.Net != Mainnet || pa.Subaddress || pa.Spend != spend || pa.View != view {
		t.Fatalf("ParseAddress: %+v, %v", pa, err)
	}
	// get_account_address_from_str.fails_on_invalid_address_format: the first character replaced by '0'
	if _, err := ParseAddress("0" + want[1:]); err == nil {
		t.Error("an address with '0' in it was accepted")
	}
	// fails_on_invalid_address_prefix: tag 0 with the same keys is not any network's address
	if _, err := ParseAddress(encodeAddr(0, append(append([]byte{}, spend[:]...), view[:]...))); err == nil {
		t.Error("prefix 0 was accepted")
	}
	// fails_on_invalid_address_content: one key byte short
	if _, err := ParseAddress(encodeAddr(18, append(append([]byte{}, spend[1:]...), view[:]...))); err == nil {
		t.Error("63 key bytes were accepted")
	}
}

// The reference's generate_from_keys example (https://docs.getmonero.org/rpc-library/wallet-rpc/): a private
// spend key, a private view key and the address they make. This is the one vector that runs Public and Address
// together against a value nobody here produced.
func TestAddressFromPrivateKeysReferenceExample(t *testing.T) {
	spend, err := ParseScalar("11d3fd247672c4cb29b6e38791dcf07629cd2d68d868f0b78811ce584a6b0d01")
	if err != nil {
		t.Fatal(err)
	}
	view, err := ParseScalar("97cf64f2cd6c930242e9bed5f14f8f16a33047229aca3eababf4af7e8d113209")
	if err != nil {
		t.Fatal(err)
	}
	const want = "42gt8cXJSHAL4up8XoZh7fikVuswDU7itAoaCjSQyo6fFoeTQpAcAwrQ1cs8KvFynLFSBdabhmk7HEe3HS7UsAz4LYnVPYM"
	got, err := Address(spend.Public(), view.Public(), Mainnet)
	if err != nil || got != want {
		t.Fatalf("Address from private keys: got %q, %v; want %q", got, err, want)
	}
}

// Network prefixes against addresses Monero itself published: testnet from tests/unit_tests/uri.cpp
// (TEST_ADDRESS, TEST_INTEGRATED_ADDRESS; the file builds a wallet2 on TESTNET), stagenet and a stagenet
// subaddress from the get_address example of the wallet-RPC reference, mainnet from
// https://docs.getmonero.org/public-address/standard-address/ .
func TestParseAddressNetworks(t *testing.T) {
	cases := []struct {
		addr string
		net  Network
		sub  bool
	}{
		{"9tTLtauaEKSj7xoVXytVH32R1pLZBk4VV4mZFGEh4wkXhDWqw1soPyf3fGixf1kni31VznEZkWNEza9d5TvjWwq5PaohYHC", Testnet, false},
		{"55LTR8KniP4LQGJSPtbYDacR7dz8RBFnsfAKMaMuwUNYX6aQbBcovzDPyrQF9KXF9tVU6Xk3K8no1BywnJX6GvZX8yJsXvt", Stagenet, false},
		{"7BnERTpvL5MbCLtj5n9No7J5oE5hHiB3tVCK5cjSvCsYWD2WRJLFuWeKTLiXo5QJqt2ZwUaLy2Vh1Ad51K7FNgqcHgjW85o", Stagenet, true},
		{"4AdUndXHHZ6cfufTMvppY6JwXNouMBzSkbLYfpAV5Usx3skxNgYeYTRj5UzqtReoS44qo9mtmXCqY45DJ852K5Jv2684Rge", Mainnet, false},
	}
	for _, c := range cases {
		pa, err := ParseAddress(c.addr)
		if err != nil {
			t.Errorf("ParseAddress(%s): %v", c.addr[:8], err)
			continue
		}
		if pa.Net != c.net || pa.Subaddress != c.sub {
			t.Errorf("ParseAddress(%s): net %v sub %v, want %v %v", c.addr[:8], pa.Net, pa.Subaddress, c.net, c.sub)
		}
		if c.sub {
			continue
		}
		if again, err := Address(pa.Spend, pa.View, pa.Net); err != nil || again != c.addr {
			t.Errorf("re-encode %s: got %q, %v", c.addr[:8], again, err)
		}
		for _, other := range []Network{Mainnet, Stagenet, Testnet} {
			if other == c.net {
				continue
			}
			if again, _ := Address(pa.Spend, pa.View, other); again == c.addr {
				t.Errorf("%s encodes the same on %v", c.addr[:8], other)
			}
		}
	}
	if _, err := ParseAddress("A4A1uPj4qaxj7xoVXytVH32R1pLZBk4VV4mZFGEh4wkXhDWqw1soPyf3fGixf1kni31VznEZkWNEza9d5TvjWwq5acaPMJfMbn3ReTsBpp"); err == nil || !strings.Contains(err.Error(), "integrated") {
		t.Errorf("integrated address: got %v, want a refusal by name", err)
	}
	for _, s := range []string{"mainnet", "Stagenet", " testnet "} {
		if _, err := ParseNetwork(s); err != nil {
			t.Errorf("ParseNetwork(%q): %v", s, err)
		}
	}
	if _, err := ParseNetwork("regtest"); err == nil {
		t.Error("ParseNetwork(regtest) accepted")
	}
}

// l and its neighbours, little-endian: l - 1 is the largest reduced scalar; l, l + 1 and 2^256 - 1 are not.
const (
	hexLMinus1 = "ecd3f55c1a631258d69cf7a2def9de1400000000000000000000000000000010"
	hexL       = "edd3f55c1a631258d69cf7a2def9de1400000000000000000000000000000010"
	hexLPlus1  = "eed3f55c1a631258d69cf7a2def9de1400000000000000000000000000000010"
	hexOnes    = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

func bigL() *big.Int {
	l, _ := new(big.Int).SetString("7237005577332262213973186563042994240857116359379907606001950938285454250989", 10)
	return l
}

// leBytes is the 32-byte little-endian form of n.
func leBytes(n *big.Int) [32]byte {
	var out [32]byte
	b := n.Bytes()
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out
}

func TestScalarReduction(t *testing.T) {
	if _, err := ParseScalar(hexLMinus1); err != nil {
		t.Errorf("l-1: %v", err)
	}
	for _, h := range []string{hexL, hexLPlus1, hexOnes} {
		if _, err := ParseScalar(h); !errors.Is(err, ErrNotReduced) {
			t.Errorf("ParseScalar(%s): got %v, want ErrNotReduced", h[:8], err)
		}
	}
	if _, err := ParseScalar(strings.Repeat("00", 32)); !errors.Is(err, ErrZeroScalar) {
		t.Errorf("zero: got %v, want ErrZeroScalar", err)
	}
	if _, err := ParseScalar("zz" + hexL[2:]); err == nil {
		t.Error("non-hex accepted")
	}
	if _, err := ParseScalar(hexL[:62]); err == nil {
		t.Error("62 hex characters accepted")
	}
	var raw [32]byte
	copy(raw[:], unhex(t, hexLMinus1))
	if got := ReduceScalar(raw); got.Hex() != hexLMinus1 {
		t.Errorf("reduce l-1: %s", got.Hex())
	}
	copy(raw[:], unhex(t, hexL))
	if got := ReduceScalar(raw); got != (Scalar{}) {
		t.Errorf("reduce l: %s, want 0", got.Hex())
	}
	copy(raw[:], unhex(t, hexLPlus1))
	if got := ReduceScalar(raw); got != (Scalar{1}) {
		t.Errorf("reduce l+1: %s, want 1", got.Hex())
	}
	copy(raw[:], unhex(t, hexOnes))
	ones := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	if want := Scalar(leBytes(ones.Mod(ones, bigL()))); ReduceScalar(raw) != want {
		t.Errorf("reduce 2^256-1: %s, want %s", ReduceScalar(raw).Hex(), want.Hex())
	}
	// an unreduced Scalar built by hand is a programming error, and Public says so instead of reducing it
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Public on an unreduced scalar did not panic")
			}
		}()
		var s Scalar
		copy(s[:], unhex(t, hexL))
		s.Public()
	}()
}

func TestPublicKeyVectors(t *testing.T) {
	// RFC 8032 §5.1: the base point B, compressed
	one := Scalar{1}
	if got := one.Public().Hex(); got != "5866666666666666666666666666666666666666666666666666666666666666" {
		t.Errorf("1·G = %s", got)
	}
	// research/crosschain/poc/proof-xmr/run.log, recorded 2026-09-06 with curve25519-dalek and sha2: an
	// implementation this package shares nothing with
	s, err := ParseScalar("567e07c35d9a3db81c1ea16c15a93723b5006850ab59edd48d283b456ecd5900")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Public().Hex(); got != "4bdbb6d4b2c14b0f96386526c2ab8b0851807aabb3da9dd68d1098ef74344a39" {
		t.Errorf("s·G = %s", got)
	}
	if got := SHA256Hex(s[:]); got != "4dea5fcbba8169ebf2df6bb21fa84d77d13f0d8acbcb7a81041e7f5052a3e136" {
		t.Errorf("sha256(s) = %s", got)
	}
	// -1·G is the base point with its x sign flipped: the same y, top bit set
	minus := Scalar{}
	copy(minus[:], unhex(t, hexLMinus1))
	if got := minus.Public().Hex(); got != "58666666666666666666666666666666666666666666666666666666666666e6" {
		t.Errorf("(l-1)·G = %s", got)
	}
}

func TestPointAddMatchesScalarAdd(t *testing.T) {
	for i := 0; i < 32; i++ {
		a, err := NewScalar(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		b, err := NewScalar(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		sum := AddScalars(a, b)
		if _, err := ParseScalar(sum.Hex()); err != nil {
			t.Fatalf("AddScalars result is not a reduced non-zero scalar: %v", err)
		}
		joint, err := AddPoints(a.Public(), b.Public())
		if err != nil {
			t.Fatal(err)
		}
		if joint != sum.Public() {
			t.Fatalf("(a+b)·G != a·G + b·G")
		}
		// the same, through the big integers, so the scalar addition is checked against something else
		ia, ib := new(big.Int).SetBytes(reverse(a[:])), new(big.Int).SetBytes(reverse(b[:]))
		if want := Scalar(leBytes(ia.Add(ia, ib).Mod(ia, bigL()))); want != sum {
			t.Fatalf("AddScalars disagrees with math/big")
		}
	}
	// s_a = -s_b: the joint key would be the identity, which nobody can spend from
	var minus1 Scalar
	copy(minus1[:], unhex(t, hexLMinus1))
	if got := AddScalars(minus1, Scalar{1}); got != (Scalar{}) {
		t.Errorf("(l-1)+1 = %s, want 0", got.Hex())
	}
	if _, err := AddPoints(minus1.Public(), Scalar{1}.Public()); !errors.Is(err, ErrBadPoint) {
		t.Errorf("AddPoints to the identity: got %v", err)
	}
}

func reverse(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[len(b)-1-i]
	}
	return out
}

func TestParsePointRejectsTorsionAndGarbage(t *testing.T) {
	g := Scalar{1}.Public()
	if _, err := ParsePoint(g.Hex()); err != nil {
		t.Fatalf("G refused: %v", err)
	}
	if _, err := ParsePoint(strings.ToUpper(g.Hex())); err != nil {
		t.Errorf("upper-case hex refused: %v", err)
	}
	bad := map[string]string{
		"identity":       "0100000000000000000000000000000000000000000000000000000000000000",
		"order 4 (y=0)":  "0000000000000000000000000000000000000000000000000000000000000000",
		"order 2 (y=-1)": "ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"y = p":          "edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f",
		"y = p, sign":    "edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"y = 2^255-1":    hexOnes,
		"not hex":        "zz" + g.Hex()[2:],
		"short":          g.Hex()[:62],
	}
	// G plus an order-2 point: on the curve, wrong subgroup
	t2, err := edwards25519.NewIdentityPoint().SetBytes(unhex(t, bad["order 2 (y=-1)"]))
	if err != nil {
		t.Fatal(err)
	}
	gp, _ := edwards25519.NewIdentityPoint().SetBytes(g[:])
	bad["G + torsion"] = hex.EncodeToString(edwards25519.NewIdentityPoint().Add(gp, t2).Bytes())
	for name, h := range bad {
		if _, err := ParsePoint(h); err == nil {
			t.Errorf("ParsePoint accepted %s", name)
		}
	}
	var mixed Point
	copy(mixed[:], unhex(t, bad["G + torsion"]))
	if _, err := AddPoints(g, mixed); !errors.Is(err, ErrBadPoint) {
		t.Errorf("AddPoints with a torsion point: got %v", err)
	}
	// an address, unlike a share, only needs keys that decompress: Monero's own check_key
	if _, err := Address(mixed, g, Mainnet); err != nil {
		t.Errorf("Address refused a decompressible key: %v", err)
	}
	// y = p is what Monero's ge_frombytes_vartime refuses as non-canonical; edwards25519 alone would reduce it
	for _, name := range []string{"y = p", "y = p, sign", "y = 2^255-1"} {
		var nonCanonical Point
		copy(nonCanonical[:], unhex(t, bad[name]))
		if _, err := Address(nonCanonical, g, Mainnet); err == nil {
			t.Errorf("Address accepted a key with %s", name)
		}
	}
	// and y = p - 1 is canonical: the order-2 point, an address key Monero decodes
	var minusOne Point
	copy(minusOne[:], unhex(t, bad["order 2 (y=-1)"]))
	if _, err := Address(minusOne, g, Mainnet); err != nil {
		t.Errorf("Address refused y = p-1: %v", err)
	}
}

func TestNewScalar(t *testing.T) {
	s, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseScalar(s.Hex()); err != nil {
		t.Errorf("NewScalar is not reduced and non-zero: %v", err)
	}
	if _, err := NewScalar(bytes.NewReader(make([]byte, 10))); err == nil {
		t.Error("a short reader did not fail")
	}
	// 64 zero bytes reduce to zero, which is refused; the next 64 bytes are taken instead
	src := append(make([]byte, 64), bytes.Repeat([]byte{7}, 64)...)
	s, err = NewScalar(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	var wide [64]byte
	copy(wide[:], src[64:])
	want, _ := edwards25519.NewScalar().SetUniformBytes(wide[:])
	if s.Hex() != hex.EncodeToString(want.Bytes()) {
		t.Errorf("zero was not skipped: %s", s.Hex())
	}
}

func TestTextMarshalling(t *testing.T) {
	s, _ := ParseScalar(hexLMinus1)
	type rec struct {
		S Scalar `json:"s"`
		P Point  `json:"p"`
	}
	b, err := json.Marshal(rec{S: s, P: s.Public()})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"s":"` + hexLMinus1 + `","p":"` + s.Public().Hex() + `"}`; string(b) != want {
		t.Errorf("json: %s", b)
	}
	var back rec
	if err := json.Unmarshal(b, &back); err != nil || back.S != s || back.P != s.Public() {
		t.Errorf("round trip: %+v, %v", back, err)
	}
	if err := json.Unmarshal([]byte(`{"s":"`+hexL+`"}`), &back); !errors.Is(err, ErrNotReduced) {
		t.Errorf("unreduced scalar in JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"p":"`+strings.Repeat("00", 32)+`"}`), &back); err == nil {
		t.Error("a small-order point in JSON was accepted")
	}
}

func TestVarint(t *testing.T) {
	for _, v := range []uint64{0, 1, 0x7f, 0x80, 0xff, 18, 24, 53, 0x1122334455667788, ^uint64(0)} {
		enc := putVarint(v)
		got, n, err := readVarint(append(enc, 0xaa))
		if err != nil || got != v || n != len(enc) {
			t.Errorf("varint %#x: %x -> %#x %d %v", v, enc, got, n, err)
		}
	}
	if _, _, err := readVarint([]byte{0x80}); err == nil {
		t.Error("truncated varint accepted")
	}
	if _, _, err := readVarint([]byte{0xff, 0x00}); err == nil {
		t.Error("non-canonical varint accepted")
	}
	if _, _, err := readVarint([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}); err == nil {
		t.Error("overflowing varint accepted")
	}
}
