package xmrswap

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"strings"

	// The ed25519 group as a library: scalars mod l, point decompression, addition and fixed-base
	// multiplication. The standard library's crypto/ed25519 exposes signing only, never the scalars and points
	// the joint key S_a + S_b is made of, and it is itself built on this package (crypto/internal/edwards25519).
	"filippo.io/edwards25519"
	// NewLegacyKeccak256. Monero's cn_fast_hash is the pre-standard Keccak-256 (pad byte 0x01); the standard
	// library's crypto/sha3 implements only FIPS-202 SHA3 (pad byte 0x06) and would checksum every address wrongly.
	"golang.org/x/crypto/sha3"
)

// Scalar is an ed25519 scalar the way Monero stores a secret key: 32 little-endian bytes, reduced mod
// l = 2^252 + 27742317777372353535851937790883648493 (monero src/crypto/crypto.cpp: generate_keys calls
// sc_reduce32, and secret_key_to_public_key refuses anything sc_check rejects). Every constructor here —
// NewScalar, ParseScalar, ReduceScalar, AddScalars — returns a reduced value. A Scalar built any other way is a
// programming error: Public and AddScalars panic on it rather than reduce it silently, because the DERO hash is
// of these exact bytes and a silent reduction would make H_a the hash of something that is not the key.
type Scalar [32]byte

// Point is a compressed ed25519 point: a Monero public key, or one party's key share.
type Point [32]byte

var (
	ErrNotReduced = errors.New("scalar is not reduced mod l")
	ErrZeroScalar = errors.New("scalar is zero")
	ErrBadPoint   = errors.New("not a prime-order ed25519 point")
)

// lMinusOne is l-1 as a scalar. l·P = (l-1)·P + P, and l·P is the identity exactly when P has no torsion
// component; edwards25519 cannot multiply by l itself (l ≡ 0 as a scalar), hence the split.
var lMinusOne = func() *edwards25519.Scalar {
	b := [32]byte{0xec, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14}
	b[31] = 0x10
	s, err := edwards25519.NewScalar().SetCanonicalBytes(b[:])
	if err != nil {
		panic(err)
	}
	return s
}()

// NewScalar draws a uniformly random non-zero scalar from rand. 512 random bits reduced mod l are uniform to
// within 2^-256 (the route RFC 8032 §5.1.5 takes); Monero's random32_unbiased (crypto.cpp) rejects 32-byte
// samples at or above 15·l and reduces, a different route to the same distribution. Zero is rejected as
// Monero's sc_isnonzero check does: a zero share would hand the whole joint key to the other party.
func NewScalar(rand io.Reader) (Scalar, error) {
	var wide [64]byte
	for {
		if _, err := io.ReadFull(rand, wide[:]); err != nil {
			return Scalar{}, fmt.Errorf("xmrswap: random scalar: %w", err)
		}
		s, err := edwards25519.NewScalar().SetUniformBytes(wide[:])
		if err != nil {
			return Scalar{}, err
		}
		var out Scalar
		copy(out[:], s.Bytes())
		if out != (Scalar{}) {
			return out, nil
		}
	}
}

// ParseScalar reads 64 hex characters as a scalar and refuses one that is not reduced or is zero. It is how a
// share read from the chain (xa_/xb_ on the escrow) or from the proof tool becomes a Scalar: the chain stores
// exact bytes, and bytes that are not a reduced scalar are not a Monero key.
func ParseScalar(h string) (Scalar, error) {
	b, err := hex32(h)
	if err != nil {
		return Scalar{}, fmt.Errorf("scalar: %w", err)
	}
	if _, err := edwards25519.NewScalar().SetCanonicalBytes(b[:]); err != nil {
		return Scalar{}, ErrNotReduced
	}
	if b == [32]byte{} {
		return Scalar{}, ErrZeroScalar
	}
	return Scalar(b), nil
}

// ReduceScalar is sc_reduce32: the 256-bit little-endian integer b taken mod l. It exists for tests and for
// callers that hold raw entropy; a share that must match a DERO hash is never reduced after the fact.
func ReduceScalar(b [32]byte) Scalar {
	var wide [64]byte
	copy(wide[:32], b[:])
	s, err := edwards25519.NewScalar().SetUniformBytes(wide[:])
	if err != nil {
		panic(err) // 64 bytes is always accepted
	}
	var out Scalar
	copy(out[:], s.Bytes())
	return out
}

// Hex is the 64-character lowercase form: what the escrow hashes (HEXDECODE of exactly these characters) and
// what monero-wallet-rpc takes as a spendkey.
func (s Scalar) Hex() string { return hex.EncodeToString(s[:]) }

// MarshalText lets a Scalar sit in the companion's durable state as hex. It is deliberately not a String
// method: a Scalar must never look like something to print.
func (s Scalar) MarshalText() ([]byte, error) { return []byte(s.Hex()), nil }

func (s *Scalar) UnmarshalText(b []byte) error {
	v, err := ParseScalar(string(b))
	if err != nil {
		return err
	}
	*s = v
	return nil
}

func (s Scalar) must() *edwards25519.Scalar {
	v, err := edwards25519.NewScalar().SetCanonicalBytes(s[:])
	if err != nil {
		panic("xmrswap: Scalar is not reduced mod l; build scalars with NewScalar, ParseScalar or ReduceScalar")
	}
	return v
}

// Public is s·G, the Monero public key of a secret key (ge_scalarmult_base in crypto.cpp).
func (s Scalar) Public() Point {
	var p Point
	copy(p[:], edwards25519.NewIdentityPoint().ScalarBaseMult(s.must()).Bytes())
	return p
}

// AddScalars is (a+b) mod l: the joint spend key s_a + s_b once both shares are known.
func AddScalars(a, b Scalar) Scalar {
	var out Scalar
	copy(out[:], edwards25519.NewScalar().Add(a.must(), b.must()).Bytes())
	return out
}

// Hex is the 64-character lowercase form the channel and the proof tool carry a point in.
func (p Point) Hex() string { return hex.EncodeToString(p[:]) }

func (p Point) MarshalText() ([]byte, error) { return []byte(p.Hex()), nil }

func (p *Point) UnmarshalText(b []byte) error {
	v, err := ParsePoint(string(b))
	if err != nil {
		return err
	}
	*p = v
	return nil
}

// ParsePoint reads 64 hex characters as a key share and requires a prime-order point that is not the identity —
// the same check the proof verifier makes on S (poc/proof-xmr: "S must be a prime-order point"). A share with a
// torsion component cannot be any scalar times G, so no proof could have been made for it; refusing it here
// means the companion never forms a joint key from one.
func ParsePoint(h string) (Point, error) {
	b, err := hex32(h)
	if err != nil {
		return Point{}, fmt.Errorf("point: %w", err)
	}
	if _, err := decodeKeyShare(Point(b)); err != nil {
		return Point{}, err
	}
	return Point(b), nil
}

// decodePoint accepts what Monero's check_key accepts (ge_frombytes_vartime in src/crypto/crypto-ops.c: a
// canonical encoding of a curve point); it is the check for keys inside an address. edwards25519 follows
// RFC 7748 in reducing a y at or above p, where Monero's fe_frombytes_vartime ("Validate the number to be
// canonical", crypto-ops.c) and RFC 8032 §5.1.3 refuse it, so that refusal is made here: the same 32 bytes
// must be a key to Monero and to this package alike.
func decodePoint(p Point) (*edwards25519.Point, error) {
	if !canonicalY(p) {
		return nil, fmt.Errorf("%w: non-canonical encoding", ErrBadPoint)
	}
	v, err := edwards25519.NewIdentityPoint().SetBytes(p[:])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPoint, err)
	}
	return v, nil
}

// canonicalY says whether the low 255 bits of the encoding, as a little-endian integer, are below
// p = 2^255 - 19, whose bytes are ed ff .. ff 7f.
func canonicalY(p Point) bool {
	if p[31]&0x7f != 0x7f || p[0] < 0xed {
		return true
	}
	for i := 1; i < 31; i++ {
		if p[i] != 0xff {
			return true
		}
	}
	return false
}

// decodeKeyShare is decodePoint plus prime order and not the identity: the check for a share.
func decodeKeyShare(p Point) (*edwards25519.Point, error) {
	v, err := decodePoint(p)
	if err != nil {
		return nil, err
	}
	id := edwards25519.NewIdentityPoint()
	if v.Equal(id) == 1 {
		return nil, fmt.Errorf("%w: identity", ErrBadPoint)
	}
	lp := edwards25519.NewIdentityPoint().ScalarMult(lMinusOne, v)
	if lp.Add(lp, v).Equal(id) != 1 {
		return nil, fmt.Errorf("%w: torsion component", ErrBadPoint)
	}
	return v, nil
}

// AddPoints is a + b: the joint spend public key S_a + S_b. Both inputs must be key shares (see ParsePoint) and
// the sum must not be the identity — s_a = -s_b would make a joint key nobody can spend from.
func AddPoints(a, b Point) (Point, error) {
	pa, err := decodeKeyShare(a)
	if err != nil {
		return Point{}, fmt.Errorf("first point: %w", err)
	}
	pb, err := decodeKeyShare(b)
	if err != nil {
		return Point{}, fmt.Errorf("second point: %w", err)
	}
	sum := edwards25519.NewIdentityPoint().Add(pa, pb)
	if sum.Equal(edwards25519.NewIdentityPoint()) == 1 {
		return Point{}, fmt.Errorf("%w: the sum is the identity", ErrBadPoint)
	}
	var out Point
	copy(out[:], sum.Bytes())
	return out, nil
}

// SHA256Hex is the hash the escrow compares against: HEX(SHA256(raw)) in htlc.bas, lowercase.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hex32(h string) ([32]byte, error) {
	var out [32]byte
	if len(h) != 64 {
		return out, fmt.Errorf("expected 64 hex characters, got %d", len(h))
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return out, errors.New("not hex")
	}
	copy(out[:], b)
	return out, nil
}

// --- addresses ---

// Network is a Monero network. The address prefixes are monero src/cryptonote_config.h:
// CRYPTONOTE_PUBLIC_ADDRESS_BASE58_PREFIX = 18 (mainnet, "4..."), 24 (stagenet, "5..."), 53 (testnet, "9...");
// CRYPTONOTE_PUBLIC_SUBADDRESS_BASE58_PREFIX = 42 / 36 / 63; the integrated prefixes are 19 / 25 / 54.
type Network int

const (
	Mainnet Network = iota
	Stagenet
	Testnet
)

func (n Network) String() string {
	switch n {
	case Mainnet:
		return "mainnet"
	case Stagenet:
		return "stagenet"
	case Testnet:
		return "testnet"
	}
	return fmt.Sprintf("Network(%d)", int(n))
}

// ParseNetwork reads the -net flag's spelling.
func ParseNetwork(s string) (Network, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "mainnet":
		return Mainnet, nil
	case "stagenet":
		return Stagenet, nil
	case "testnet":
		return Testnet, nil
	}
	return 0, fmt.Errorf("unknown Monero network %q (mainnet, stagenet, testnet)", s)
}

func (n Network) prefixes() (standard, sub uint64, err error) {
	switch n {
	case Mainnet:
		return 18, 42, nil
	case Stagenet:
		return 24, 36, nil
	case Testnet:
		return 53, 63, nil
	}
	return 0, 0, fmt.Errorf("unknown Monero network %d", int(n))
}

// Address is the standard address of a spend and a view public key on net, exactly as
// get_account_address_as_str builds it (monero src/cryptonote_basic/cryptonote_basic_impl.cpp):
// base58::encode_addr(prefix, spend ‖ view), where encode_addr (src/common/base58.cpp) is
// base58(varint(prefix) ‖ spend ‖ view ‖ cn_fast_hash(varint(prefix) ‖ spend ‖ view)[0:4]).
// Both keys must decompress: Monero's parser rejects an address whose keys do not (check_key), so no such
// address is ever made here.
func Address(spend, view Point, net Network) (string, error) {
	prefix, _, err := net.prefixes()
	if err != nil {
		return "", err
	}
	if _, err := decodePoint(spend); err != nil {
		return "", fmt.Errorf("spend key: %w", err)
	}
	if _, err := decodePoint(view); err != nil {
		return "", fmt.Errorf("view key: %w", err)
	}
	data := make([]byte, 0, 64)
	data = append(data, spend[:]...)
	data = append(data, view[:]...)
	return encodeAddr(prefix, data), nil
}

// ParsedAddress is what a Monero address string says.
type ParsedAddress struct {
	Net        Network
	Subaddress bool
	Spend      Point
	View       Point
}

// ParseAddress decodes a standard or sub- address of any network (get_account_address_from_str, without the
// nettype argument: the prefix says which network it is). An integrated address is refused by name: the swap
// never pays one, and silently dropping its payment id would misdirect a payment.
func ParseAddress(addr string) (ParsedAddress, error) {
	tag, data, err := decodeAddr(addr)
	if err != nil {
		return ParsedAddress{}, fmt.Errorf("monero address: %w", err)
	}
	var out ParsedAddress
	switch tag {
	case 18, 24, 53, 42, 36, 63:
	case 19, 25, 54:
		return out, errors.New("monero address: integrated addresses are not accepted")
	default:
		return out, fmt.Errorf("monero address: unknown prefix %d", tag)
	}
	if len(data) != 64 {
		return out, fmt.Errorf("monero address: %d key bytes, expected 64", len(data))
	}
	for _, n := range []Network{Mainnet, Stagenet, Testnet} {
		std, sub, _ := n.prefixes()
		if tag == std || tag == sub {
			out.Net, out.Subaddress = n, tag == sub
		}
	}
	copy(out.Spend[:], data[:32])
	copy(out.View[:], data[32:])
	if _, err := decodePoint(out.Spend); err != nil {
		return out, fmt.Errorf("monero address: spend key: %w", err)
	}
	if _, err := decodePoint(out.View); err != nil {
		return out, fmt.Errorf("monero address: view key: %w", err)
	}
	return out, nil
}

func keccak256(b []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(b)
	return h.Sum(nil)
}

// encodeAddr is tools::base58::encode_addr (src/common/base58.cpp): varint tag, data, then the first four bytes
// of cn_fast_hash over both, all base58-encoded.
func encodeAddr(tag uint64, data []byte) string {
	buf := append(putVarint(tag), data...)
	buf = append(buf, keccak256(buf)[:4]...)
	return base58Encode(buf)
}

// decodeAddr is tools::base58::decode_addr: base58 decode, split off and verify the 4-byte checksum, read the
// varint tag, return the rest.
func decodeAddr(s string) (uint64, []byte, error) {
	raw, err := base58Decode(s)
	if err != nil {
		return 0, nil, err
	}
	if len(raw) <= 4 {
		return 0, nil, errors.New("too short")
	}
	body, sum := raw[:len(raw)-4], raw[len(raw)-4:]
	if string(keccak256(body)[:4]) != string(sum) {
		return 0, nil, errors.New("bad checksum")
	}
	tag, n, err := readVarint(body)
	if err != nil {
		return 0, nil, fmt.Errorf("prefix: %w", err)
	}
	return tag, body[n:], nil
}

// putVarint is tools::get_varint_data (src/common/varint.h): seven bits per byte, least significant first,
// 0x80 marking a continuation.
func putVarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

// readVarint is tools::read_varint with its two refusals: a value that overflows 64 bits, and a zero byte after
// the first (a non-canonical encoding; Monero's base58 test "handles_non_correct_tag" relies on it).
func readVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i, shift := 0, uint(0); ; i, shift = i+1, shift+7 {
		if i >= len(b) {
			return 0, 0, errors.New("truncated varint")
		}
		c := b[i]
		if shift >= 64 || (shift+7 >= 64 && uint64(c) >= 1<<(64-shift)) {
			return 0, 0, errors.New("varint overflow")
		}
		if c == 0 && shift != 0 {
			return 0, 0, errors.New("non-canonical varint")
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, i + 1, nil
		}
	}
}

// --- Monero base58 (src/common/base58.cpp) ---
//
// Not Bitcoin's base58. The input is cut into 8-byte blocks; each block is a big-endian integer written in 11
// base58 digits, left-padded with '1' (the zero digit), and a shorter last block gets the digit count the table
// below gives its length. Decoding therefore knows every block's byte length from its character count, and
// rejects a block whose digits exceed that many bytes.

const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// b58EncodedSize[n] is the digit count of an n-byte block: encoded_block_sizes = {0, 2, 3, 5, 6, 7, 9, 10, 11}.
var b58EncodedSize = [9]int{0, 2, 3, 5, 6, 7, 9, 10, 11}

// b58DecodedSize inverts the table; -1 for a digit count no block has (1, 4, 8).
func b58DecodedSize(digits int) int {
	for n, d := range b58EncodedSize {
		if d == digits {
			return n
		}
	}
	return -1
}

func b58EncodeBlock(block []byte, out []byte) {
	var num uint64
	for _, c := range block {
		num = num<<8 | uint64(c)
	}
	for i := len(out) - 1; num > 0; i-- {
		out[i] = b58Alphabet[num%58]
		num /= 58
	}
}

func b58DecodeBlock(digits string) ([]byte, error) {
	n := b58DecodedSize(len(digits))
	if n <= 0 {
		return nil, fmt.Errorf("invalid block length %d", len(digits))
	}
	var num, order uint64 = 0, 1
	for i := len(digits) - 1; i >= 0; i-- {
		d := strings.IndexByte(b58Alphabet, digits[i])
		if d < 0 {
			return nil, fmt.Errorf("invalid symbol %q", digits[i])
		}
		hi, lo := bits.Mul64(order, uint64(d))
		sum, carry := bits.Add64(num, lo, 0)
		if hi != 0 || carry != 0 {
			return nil, errors.New("block overflow")
		}
		num = sum
		order *= 58 // wraps after the last digit of an 11-digit block, unused from then on, as in the original
	}
	if n < 8 && num >= 1<<(8*uint(n)) {
		return nil, errors.New("block overflow")
	}
	out := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		out[i] = byte(num)
		num >>= 8
	}
	return out, nil
}

func base58Encode(data []byte) string {
	full, last := len(data)/8, len(data)%8
	out := make([]byte, full*11+b58EncodedSize[last])
	for i := range out {
		out[i] = b58Alphabet[0]
	}
	for i := 0; i < full; i++ {
		b58EncodeBlock(data[i*8:i*8+8], out[i*11:i*11+11])
	}
	if last > 0 {
		b58EncodeBlock(data[full*8:], out[full*11:])
	}
	return string(out)
}

func base58Decode(s string) ([]byte, error) {
	full, last := len(s)/11, len(s)%11
	lastBytes := b58DecodedSize(last)
	if lastBytes < 0 {
		return nil, fmt.Errorf("invalid length %d", len(s))
	}
	out := make([]byte, 0, full*8+lastBytes)
	for i := 0; i < full; i++ {
		block, err := b58DecodeBlock(s[i*11 : i*11+11])
		if err != nil {
			return nil, err
		}
		out = append(out, block...)
	}
	if last > 0 {
		block, err := b58DecodeBlock(s[full*11:])
		if err != nil {
			return nil, err
		}
		out = append(out, block...)
	}
	return out, nil
}
