package main

import "strings"

// Addresses. A contract stores a signer as the raw 33-byte compressed key (STORE("owner", SIGNER())) or as the
// string ADDRESS_STRING writes, which always carries the MAINNET prefix "dero1", whatever network the chain is
// (tools/simrpc/address.go explains the DVM's spelling). The report prints every raw key in that same spelling,
// so a value read from one contract can be compared by eye with one read from another.
//
// tools/simrpc has the bech32 primitives but exports only DVMAddress(string); this file adds the one missing
// direction, raw key -> string, and TestAddrRoundTrip holds it to simrpc's spelling.

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32Polymod(values []int) int {
	gen := []int{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := 1
	for _, v := range values {
		b := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ v
		for i := 0; i < 5; i++ {
			if (b>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func bech32HrpExpand(hrp string) []int {
	out := make([]int, 0, len(hrp)*2+1)
	for _, c := range hrp {
		out = append(out, int(c)>>5)
	}
	out = append(out, 0)
	for _, c := range hrp {
		out = append(out, int(c)&31)
	}
	return out
}

func convertBits(data []int, from, to uint, pad bool) []int {
	acc, bits := 0, uint(0)
	max := (1 << to) - 1
	var out []int
	for _, v := range data {
		acc = acc<<from | v
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, (acc>>bits)&max)
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, (acc<<(to-bits))&max)
		}
	} else if bits >= from || (acc<<(to-bits))&max != 0 {
		return nil
	}
	return out
}

// addrFromRaw renders a 33-byte compressed key as ADDRESS_STRING does. ok is false for anything that is not 33
// bytes. It does not check the point is on the curve: the DVM's IS_ADDRESS_VALID does that, and callers say so.
func addrFromRaw(raw []byte) (string, bool) {
	if len(raw) != 33 {
		return "", false
	}
	data := make([]int, 0, 34)
	data = append(data, 1) // address version byte
	for _, b := range raw {
		data = append(data, int(b))
	}
	five := convertBits(data, 8, 5, true)
	values := append(bech32HrpExpand("dero"), five...)
	pm := bech32Polymod(append(values, 0, 0, 0, 0, 0, 0)) ^ 1
	var b strings.Builder
	b.WriteString("dero1")
	for _, d := range five {
		b.WriteByte(bech32Charset[d])
	}
	for i := 0; i < 6; i++ {
		b.WriteByte(bech32Charset[(pm>>uint(5*(5-i)))&31])
	}
	return b.String(), true
}

// rawFromAddr is the inverse, for tests and for comparing an address a user typed. It accepts dero/deto and
// integrated forms and returns the 33-byte key.
func rawFromAddr(s string) ([]byte, bool) {
	s = strings.ToLower(s)
	pos := strings.LastIndex(s, "1")
	if pos < 1 || pos+7 > len(s) {
		return nil, false
	}
	switch s[:pos] {
	case "dero", "deto", "deroi", "detoi":
	default:
		return nil, false
	}
	data := make([]int, 0, len(s)-pos-1)
	for _, c := range s[pos+1:] {
		d := strings.IndexRune(bech32Charset, c)
		if d < 0 {
			return nil, false
		}
		data = append(data, d)
	}
	if bech32Polymod(append(bech32HrpExpand(s[:pos]), data...)) != 1 {
		return nil, false
	}
	b := convertBits(data[:len(data)-6], 5, 8, false)
	if len(b) < 34 || b[0] != 1 {
		return nil, false
	}
	out := make([]byte, 33)
	for i := range out {
		out[i] = byte(b[i+1])
	}
	return out, true
}

// isZero reports 33 zero bytes: what SIGNER() returns to a contract called at ring size above 2.
func isZero(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// showKey renders a stored raw key for the report.
func showKey(b []byte) string {
	if isZero(b) && len(b) == 33 {
		return "33 zero bytes (the anonymous signer of a ring > 2 call)"
	}
	if a, ok := addrFromRaw(b); ok {
		return a
	}
	return "not a key: " + hexOf(b)
}
