// Package zecswap is the Zcash side of the transparent-Zcash swap market (docs/ZEC_SWAP.md §7): the script both
// parties must agree on byte for byte, its pay-to-script-hash address on each network, the transparent address
// forms the board carries, and the two ways the script is spent. Nothing in this file talks to a node or holds a
// key; the watcher and the transaction builder (BL-40b) sit on top of it. The layout is the one
// research/crosschain/poc/zec-t-htlc proved under Zcash's consensus interpreter, and TestVector holds its bytes.
package zecswap

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/ripemd160"
)

// Script opcodes, Bitcoin's and so Zcash's (Zcash Protocol Specification §7.4; BIP 65 for CHECKLOCKTIMEVERIFY).
const (
	opFalse               = 0x00
	opPushData1           = 0x4c
	opTrue                = 0x51
	opIf                  = 0x63
	opElse                = 0x67
	opEndIf               = 0x68
	opDrop                = 0x75
	opDup                 = 0x76
	opEqual               = 0x87
	opEqualVerify         = 0x88
	opSHA256              = 0xa8
	opHash160             = 0xa9
	opCheckSig            = 0xac
	opCheckLockTimeVerify = 0xb1
)

// HTLC is the hash-time-locked script of docs/ZEC_SWAP.md §3 step 4:
//
//	IF SHA256 <Hash> EQUALVERIFY DUP HASH160 <ClaimPkh> EQUALVERIFY CHECKSIG
//	ELSE <CLTV> CHECKLOCKTIMEVERIFY DROP DUP HASH160 <RefundPkh> EQUALVERIFY CHECKSIG ENDIF
//
// The claim key is the maker's — they hold the secret and take the ZEC with it — and the refund key is the
// taker's, usable once the chain's height reaches CLTV. Hash is SHA256 of the 32-byte secret, the same hash
// contracts/swap.bas checks on DERO.
type HTLC struct {
	Hash      [32]byte
	ClaimPkh  [20]byte
	RefundPkh [20]byte
	CLTV      uint32
}

// RedeemScript is the script the P2SH output commits to; the maker computes it from the board alone (the
// offer's hash and address, the accept's refund key hash and height), so the two parties never need to send it.
func (h HTLC) RedeemScript() []byte {
	var b bytes.Buffer
	b.WriteByte(opIf)
	b.WriteByte(opSHA256)
	b.Write(push(h.Hash[:]))
	b.WriteByte(opEqualVerify)
	b.WriteByte(opDup)
	b.WriteByte(opHash160)
	b.Write(push(h.ClaimPkh[:]))
	b.WriteByte(opEqualVerify)
	b.WriteByte(opCheckSig)
	b.WriteByte(opElse)
	b.Write(push(ScriptNum(int64(h.CLTV))))
	b.WriteByte(opCheckLockTimeVerify)
	b.WriteByte(opDrop)
	b.WriteByte(opDup)
	b.WriteByte(opHash160)
	b.Write(push(h.RefundPkh[:]))
	b.WriteByte(opEqualVerify)
	b.WriteByte(opCheckSig)
	b.WriteByte(opEndIf)
	return b.Bytes()
}

// ScriptHash is HASH160 of the redeem script: what the funding output pays to.
func (h HTLC) ScriptHash() [20]byte { return Hash160(h.RedeemScript()) }

// ScriptPubKey is the P2SH output script: HASH160 <ScriptHash> EQUAL.
func (h HTLC) ScriptPubKey() []byte {
	sh := h.ScriptHash()
	out := []byte{opHash160}
	out = append(out, push(sh[:])...)
	return append(out, opEqual)
}

// Address is the P2SH address the taker funds, on the given network.
func (h HTLC) Address(net Network) string { return EncodeAddress(net, P2SH, h.ScriptHash()) }

// ClaimScriptSig spends the script by the claim branch: <sig> <pubkey> <secret> TRUE <redeem>. sig carries its
// SIGHASH byte; pubkey is the claim key in the form whose HASH160 is ClaimPkh.
func (h HTLC) ClaimScriptSig(sig, pubkey []byte, secret [32]byte) []byte {
	var b bytes.Buffer
	b.Write(push(sig))
	b.Write(push(pubkey))
	b.Write(push(secret[:]))
	b.WriteByte(opTrue)
	b.Write(push(h.RedeemScript()))
	return b.Bytes()
}

// RefundScriptSig spends the script by the refund branch: <sig> <pubkey> FALSE <redeem>. The spending transaction
// must carry nLockTime >= CLTV and a non-final nSequence on this input, or CHECKLOCKTIMEVERIFY fails (BIP 65;
// the PoC's fifth and sixth cases).
func (h HTLC) RefundScriptSig(sig, pubkey []byte) []byte {
	var b bytes.Buffer
	b.Write(push(sig))
	b.Write(push(pubkey))
	b.WriteByte(opFalse)
	b.Write(push(h.RedeemScript()))
	return b.Bytes()
}

// push encodes a data push: a direct length for up to 75 bytes, PUSHDATA1 up to 255 — the two sizes a swap
// script ever needs (a redeem script of 94 bytes is the largest thing pushed).
func push(data []byte) []byte {
	n := len(data)
	switch {
	case n == 0:
		return []byte{opFalse}
	case n <= 75:
		return append([]byte{byte(n)}, data...)
	case n <= 255:
		return append([]byte{opPushData1, byte(n)}, data...)
	}
	panic(fmt.Sprintf("zecswap: push of %d bytes is not a swap script", n))
}

// ScriptNum is the minimal script-number encoding: little-endian magnitude with the sign in the top bit of the
// last byte, an extra 0x00 when the magnitude's top bit is set, and no bytes at all for zero. The CLTV height
// 3,000,000 encodes as c0 c6 2d, as in the PoC's script.
func ScriptNum(n int64) []byte {
	if n == 0 {
		return nil
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var out []byte
	for n > 0 {
		out = append(out, byte(n&0xff))
		n >>= 8
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

// Network and Kind name a transparent address's two-byte version prefix (Zcash Protocol Specification §5.6.1.1).
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

// ParseAddress decodes a transparent address and says which network and kind it is. A shielded address (z…, u…)
// fails here, which is what the board's post and accept screens rely on to refuse one with the reason.
func ParseAddress(s string) (net Network, kind Kind, hash [20]byte, err error) {
	if len(s) > 2 && (s[:2] == "zs" || s[:2] == "zc" || s[:2] == "zt" || s[:1] == "u") {
		return 0, 0, hash, errors.New("zecswap: a shielded address; the swap script needs a transparent one (t1…, t3…, tm…, t2…)")
	}
	payload, err := DecodeBase58Check(s)
	if err != nil {
		return 0, 0, hash, err
	}
	if len(payload) != 22 {
		return 0, 0, hash, fmt.Errorf("zecswap: not a transparent address (%d payload bytes)", len(payload))
	}
	for n, kinds := range prefixes {
		for k, p := range kinds {
			if payload[0] == p[0] && payload[1] == p[1] {
				copy(hash[:], payload[2:])
				return n, k, hash, nil
			}
		}
	}
	return 0, 0, hash, errors.New("zecswap: not a transparent Zcash address prefix")
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
		return nil, errors.New("zecswap: too short for a checksum")
	}
	payload, sum := full[:len(full)-4], full[len(full)-4:]
	c1 := sha256.Sum256(payload)
	c2 := sha256.Sum256(c1[:])
	if !bytes.Equal(sum, c2[:4]) {
		return nil, errors.New("zecswap: bad checksum")
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
	x := big.NewInt(0)
	base := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		d := bytes.IndexByte([]byte(b58alphabet), s[i])
		if d < 0 {
			return nil, fmt.Errorf("zecswap: %q is not a base58 character", s[i])
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

// LockTimeBytes is the little-endian nLockTime a refund transaction must carry: at least CLTV, so the
// interpreter's "nLockTime >= the pushed height" holds (BIP 65).
func (h HTLC) LockTimeBytes() [4]byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], h.CLTV)
	return b
}
