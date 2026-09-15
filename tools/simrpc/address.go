package simrpc

import "strings"

// The DVM writes addresses with ADDRESS_STRING, which always uses the MAINNET human-readable part "dero", even on
// testnet and on the simulator where a wallet reports the same account as "deto1...". Any tool that has to look up
// a contract key named after an address (vault.bas names each signer's vote key that way) must convert its own
// address into the DVM's spelling first, or it will read a key that does not exist.
//
// This is the same bech32 the app uses (app/rpc.js canonAddress); an address payload is a version byte 1 followed
// by the 33-byte compressed key, and the canonical form drops any integrated-address arguments.

const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func polymod(values []int) int {
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

func hrpExpand(hrp string) []int {
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
	out := []int{}
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

func bech32Decode(s string) (string, []int) {
	s = strings.ToLower(s)
	pos := strings.LastIndex(s, "1")
	if pos < 1 || pos+7 > len(s) {
		return "", nil
	}
	hrp := s[:pos]
	data := make([]int, 0, len(s)-pos-1)
	for _, c := range s[pos+1:] {
		d := strings.IndexRune(charset, c)
		if d < 0 {
			return "", nil
		}
		data = append(data, d)
	}
	if polymod(append(hrpExpand(hrp), data...)) != 1 {
		return "", nil
	}
	return hrp, data[:len(data)-6]
}

func bech32Encode(hrp string, data []int) string {
	values := append(hrpExpand(hrp), data...)
	pm := polymod(append(values, 0, 0, 0, 0, 0, 0)) ^ 1
	var b strings.Builder
	b.WriteString(hrp)
	b.WriteByte('1')
	for _, d := range data {
		b.WriteByte(charset[d])
	}
	for i := 0; i < 6; i++ {
		b.WriteByte(charset[(pm>>uint(5*(5-i)))&31])
	}
	return b.String()
}

// AddressFromRaw spells a raw 33-byte compressed key — what ADDRESS_RAW stores, such as vault.bas sg_<i> — the way a
// contract writes it with ADDRESS_STRING. Returns "" when raw is not 33 bytes.
func AddressFromRaw(raw []byte) string {
	if len(raw) != 33 {
		return ""
	}
	data := make([]int, 0, 34)
	data = append(data, 1)
	for _, b := range raw {
		data = append(data, int(b))
	}
	return bech32Encode("dero", convertBits(data, 8, 5, true))
}

// DVMAddress converts any DERO address string into the exact spelling a contract writes with ADDRESS_STRING:
// the mainnet "dero" prefix, no integrated arguments. Returns "" when the input is not an address.
func DVMAddress(addr string) string {
	hrp, data := bech32Decode(addr)
	switch hrp {
	case "dero", "deto", "deroi", "detoi":
	default:
		return ""
	}
	bytes := convertBits(data, 5, 8, false)
	if len(bytes) < 34 || bytes[0] != 1 {
		return ""
	}
	return bech32Encode("dero", convertBits(bytes[:34], 8, 5, true))
}
