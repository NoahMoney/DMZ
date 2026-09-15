// Package zcashtx is Zcash's transparent layer as the vault's signers need it, and nothing more: v5 (ZIP 225) and
// v6 (ZIP 229) transactions with transparent inputs and outputs, their ZIP 244 digests (the transaction id and the
// SIGHASH_ALL signature digest), ECDSA over secp256k1, the transparent address forms, ZIP 317's conventional fee, and
// a JSON-RPC client for a zebrad or zcashd node.
//
// It was copied on 2026-09-15 from tools/zecswap at commit c58e3e3 (git show c58e3e3:tools/zecswap/<file>), which the operator's ruling of that day removes from the tree
// with the atomic swaps; the code is zecswap's, vector for vector, without the hash-time-locked script, the swap fee
// and the header gate. Its tests carry the Zcash project's own ZIP 244 vectors and transactions captured from a real
// zebrad 6.3.0 on regtest, so the digests are pinned to the byte by two sources that are not this code.
package zcashtx

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/ripemd160"
)

// Script opcodes, Bitcoin's and so Zcash's (Zcash Protocol Specification §7.4).
const (
	OpFalse         = 0x00
	OpPushData1     = 0x4c
	OpPushData2     = 0x4d
	OpTrue          = 0x51
	OpDup           = 0x76
	OpEqual         = 0x87
	OpEqualVerify   = 0x88
	OpHash160       = 0xa9
	OpCheckSig      = 0xac
	OpCheckMultiSig = 0xae
	// MaxScriptElementSize is the interpreter's limit on one pushed item, and so on a P2SH redeem script.
	MaxScriptElementSize = 520
	// LockTimeThreshold is the boundary between the two things nLockTime can mean: below it a block height, at or
	// above it a Unix time (BIP 65). No height a node reports can be at or above it.
	LockTimeThreshold = 500_000_000
	// SequenceFinal is the nSequence of an input with nothing to wait for; when every input has it, nLockTime is
	// ignored by consensus.
	SequenceFinal = 0xffffffff
)

// Push encodes a data push: the small numbers 1..16 as their opcodes (MINIMALDATA, which Zcash's interpreter and
// relay policy both run), a direct length up to 75 bytes, PUSHDATA1 up to 255 and PUSHDATA2 up to the interpreter's
// 520-byte element limit. Anything longer is not a push any node would accept, and the caller is told so.
func Push(data []byte) ([]byte, error) {
	n := len(data)
	switch {
	case n == 0:
		return []byte{OpFalse}, nil
	case n == 1 && data[0] >= 1 && data[0] <= 16:
		return []byte{OpTrue - 1 + data[0]}, nil
	case n <= 75:
		return append([]byte{byte(n)}, data...), nil
	case n <= 255:
		return append([]byte{OpPushData1, byte(n)}, data...), nil
	case n <= MaxScriptElementSize:
		return append([]byte{OpPushData2, byte(n), byte(n >> 8)}, data...), nil
	}
	return nil, fmt.Errorf("zcashtx: a push of %d bytes is above the interpreter's %d-byte element limit", n, MaxScriptElementSize)
}

// push is Push for data whose length the caller has already bounded (keys, hashes, signatures).
func push(data []byte) []byte {
	b, err := Push(data)
	if err != nil {
		panic(err)
	}
	return b
}

// ScriptNum is the minimal script-number encoding: little-endian magnitude with the sign in the top bit of the
// last byte, an extra 0x00 when the magnitude's top bit is set, and no bytes at all for zero.
func ScriptNum(n int64) []byte {
	if n == 0 {
		return nil
	}
	neg := n < 0
	mag := uint64(n)
	if neg {
		mag = uint64(-(n + 1)) + 1
	}
	var out []byte
	for mag > 0 {
		out = append(out, byte(mag&0xff))
		mag >>= 8
	}
	if out[len(out)-1]&0x80 != 0 {
		if neg {
			out = append(out, 0x80)
		} else {
			out = append(out, 0x00)
		}
	} else if neg {
		out[len(out)-1] |= 0x80
	}
	return out
}

// Hash160 is RIPEMD160(SHA256(b)), the hash behind every transparent address and every P2SH output.
func Hash160(b []byte) (out [20]byte) {
	s := sha256.Sum256(b)
	r := ripemd160.New()
	r.Write(s[:])
	copy(out[:], r.Sum(nil))
	return out
}

// P2PKHScript is OP_DUP OP_HASH160 <pkh> OP_EQUALVERIFY OP_CHECKSIG.
func P2PKHScript(pkh [20]byte) []byte {
	out := []byte{OpDup, OpHash160}
	out = append(out, push(pkh[:])...)
	return append(out, OpEqualVerify, OpCheckSig)
}

// P2SHScript is OP_HASH160 <script hash> OP_EQUAL.
func P2SHScript(sh [20]byte) []byte {
	out := []byte{OpHash160}
	out = append(out, push(sh[:])...)
	return append(out, OpEqual)
}

// P2PKHHash reads the key hash out of a pay-to-public-key-hash script.
func P2PKHHash(spk []byte) ([20]byte, bool) {
	var h [20]byte
	if len(spk) != 25 || spk[0] != OpDup || spk[1] != OpHash160 || spk[2] != 20 || spk[23] != OpEqualVerify || spk[24] != OpCheckSig {
		return h, false
	}
	copy(h[:], spk[3:23])
	return h, true
}

// P2SHHash reads the script hash out of a pay-to-script-hash script.
func P2SHHash(spk []byte) ([20]byte, bool) {
	var h [20]byte
	if len(spk) != 23 || spk[0] != OpHash160 || spk[1] != 20 || spk[22] != OpEqual {
		return h, false
	}
	copy(h[:], spk[2:22])
	return h, true
}

// Network and Kind name a transparent address's two-byte version prefix (Zcash Protocol Specification §5.6.1.1).
// Regtest uses testnet's prefixes.
type Network int
type Kind int

const (
	Mainnet Network = iota
	Testnet
)
const (
	P2PKH Kind = iota
	P2SH
)

func (n Network) String() string {
	if n == Mainnet {
		return "mainnet"
	}
	return "testnet"
}

func (k Kind) String() string {
	if k == P2PKH {
		return "p2pkh"
	}
	return "p2sh"
}

// prefixes: mainnet t1 (P2PKH) and t3 (P2SH); testnet tm (P2PKH) and t2 (P2SH).
var prefixes = map[Network]map[Kind][2]byte{
	Mainnet: {P2PKH: {0x1c, 0xb8}, P2SH: {0x1c, 0xbd}},
	Testnet: {P2PKH: {0x1d, 0x25}, P2SH: {0x1c, 0xba}},
}

// EncodeAddress is Base58Check over the version prefix and the 20-byte hash.
func EncodeAddress(net Network, kind Kind, hash [20]byte) string {
	p := prefixes[net][kind]
	payload := append([]byte{p[0], p[1]}, hash[:]...)
	return Base58Check(payload)
}

// ParseAddress decodes a transparent address and says which network and kind it is. A shielded or unified address
// (z…, u…) is refused by name: nothing in this package can pay one.
func ParseAddress(s string) (net Network, kind Kind, hash [20]byte, err error) {
	if len(s) > 2 && (s[:2] == "zs" || s[:2] == "zc" || s[:2] == "zt" || s[:1] == "u") {
		return 0, 0, hash, errors.New("zcashtx: a shielded or unified address; only a transparent one (t1…, t3…, tm…, t2…) can be paid here")
	}
	if len(s) < 26 || len(s) > 40 {
		return 0, 0, hash, fmt.Errorf("zcashtx: %d characters is not the length of a transparent address", len(s))
	}
	payload, err := DecodeBase58Check(s)
	if err != nil {
		return 0, 0, hash, err
	}
	if len(payload) != 22 {
		return 0, 0, hash, fmt.Errorf("zcashtx: not a transparent address (%d payload bytes)", len(payload))
	}
	for n, kinds := range prefixes {
		for k, p := range kinds {
			if payload[0] == p[0] && payload[1] == p[1] {
				copy(hash[:], payload[2:])
				// the encoding must be the canonical one: Base58 has no second spelling of a payload, but a caller
				// comparing strings must be able to rely on that
				if EncodeAddress(n, k, hash) != s {
					return 0, 0, hash, errors.New("zcashtx: not the canonical spelling of that address")
				}
				return n, k, hash, nil
			}
		}
	}
	return 0, 0, hash, errors.New("zcashtx: not a transparent Zcash address prefix")
}

// AddressScript is the output script an address is paid with.
func AddressScript(addr string, want Network) ([]byte, Kind, error) {
	n, k, h, err := ParseAddress(addr)
	if err != nil {
		return nil, 0, err
	}
	if n != want {
		return nil, 0, fmt.Errorf("zcashtx: %s is a %s address, not %s", addr, n, want)
	}
	if k == P2SH {
		return P2SHScript(h), k, nil
	}
	return P2PKHScript(h), k, nil
}

const b58alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Base58Check appends the first four bytes of SHA256d(payload) and encodes in Base58.
func Base58Check(payload []byte) string {
	c1 := sha256.Sum256(payload)
	c2 := sha256.Sum256(c1[:])
	full := append(append([]byte{}, payload...), c2[:4]...)
	return base58Encode(full)
}

// DecodeBase58Check returns the payload of a Base58Check string, or an error on a bad checksum.
func DecodeBase58Check(s string) ([]byte, error) {
	full, err := base58Decode(s)
	if err != nil {
		return nil, err
	}
	if len(full) < 5 {
		return nil, errors.New("zcashtx: too short for a checksum")
	}
	payload, sum := full[:len(full)-4], full[len(full)-4:]
	c1 := sha256.Sum256(payload)
	c2 := sha256.Sum256(c1[:])
	if !bytes.Equal(sum, c2[:4]) {
		return nil, errors.New("zcashtx: bad checksum")
	}
	return payload, nil
}

func base58Encode(b []byte) string {
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}
	x := new(big.Int).SetBytes(b)
	mod, base := new(big.Int), big.NewInt(58)
	var out []byte
	for x.Sign() > 0 {
		x.DivMod(x, base, mod)
		out = append(out, b58alphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func base58Decode(s string) ([]byte, error) {
	if len(s) > 200 {
		return nil, errors.New("zcashtx: base58 string too long")
	}
	x := big.NewInt(0)
	base := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		d := bytes.IndexByte([]byte(b58alphabet), s[i])
		if d < 0 {
			return nil, fmt.Errorf("zcashtx: %q is not a base58 character", s[i])
		}
		x.Mul(x, base)
		x.Add(x, big.NewInt(int64(d)))
	}
	out := x.Bytes()
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	return append(make([]byte, zeros), out...), nil
}
