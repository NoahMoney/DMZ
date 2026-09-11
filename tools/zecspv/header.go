package zecspv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// powHeaderLen is the part of the header Equihash commits to: every field before solutionSize (§7.7.1).
	powHeaderLen = 140
	// SolutionLen is the size of an Equihash (200, 9) solution, the only one on Mainnet and Testnet (§7.6).
	SolutionLen = 1344
	// HeaderLen is a serialized header: 140 bytes of fields, the 3-byte compactSize 1344, and the solution.
	HeaderLen = powHeaderLen + 3 + SolutionLen
)

// ErrParse is returned for a header that is not exactly one well-formed serialized header.
var ErrParse = errors.New("zecspv: malformed header")

// Header is a parsed Zcash block header (§7.6).
type Header struct {
	Version    int32
	PrevBlock  Hash
	MerkleRoot Hash
	// Commitments is hashReserved before Sapling, hashFinalSaplingRoot through Blossom, hashLightClientRoot
	// (hashChainHistoryRoot) in Heartwood and Canopy, and hashBlockCommitments from NU5 (§7.6). It is carried,
	// not checked: checking it needs the block's trees.
	Commitments Hash
	Time        uint32
	Bits        uint32
	Nonce       [32]byte
	Solution    []byte

	raw  []byte
	hash Hash
}

// ParseHeader parses exactly one serialized header, with nothing after it.
func ParseHeader(b []byte) (*Header, error) {
	if len(b) != HeaderLen {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrParse, len(b), HeaderLen)
	}
	// solutionSize must be the minimal compactSize, 0xfd then 1344 little-endian; any other encoding of the same
	// length would let a miner try several header hashes per solution (§7.6, the note on solutionSize)
	if b[powHeaderLen] != 0xfd || binary.LittleEndian.Uint16(b[powHeaderLen+1:]) != SolutionLen {
		return nil, fmt.Errorf("%w: solutionSize is not the 3-byte compactSize 1344", ErrParse)
	}
	raw := bytes.Clone(b)
	h := &Header{raw: raw}
	h.Version = int32(binary.LittleEndian.Uint32(raw[0:]))
	copy(h.PrevBlock[:], raw[4:36])
	copy(h.MerkleRoot[:], raw[36:68])
	copy(h.Commitments[:], raw[68:100])
	h.Time = binary.LittleEndian.Uint32(raw[100:])
	h.Bits = binary.LittleEndian.Uint32(raw[104:])
	copy(h.Nonce[:], raw[108:140])
	h.Solution = raw[powHeaderLen+3:]
	h.hash = sha256d(raw)
	return h, nil
}

// HeaderFromBlock parses the header at the start of a serialized block, such as getblock with verbosity 0 returns.
// The rest of the block is not read.
func HeaderFromBlock(block []byte) (*Header, error) {
	if len(block) < HeaderLen {
		return nil, fmt.Errorf("%w: a block of %d bytes cannot hold a header", ErrParse, len(block))
	}
	return ParseHeader(block[:HeaderLen])
}

// Hash is the block hash: SHA-256d of the whole serialized header, solution included (§7.7.2).
func (h *Header) Hash() Hash { return h.hash }

// Bytes returns a copy of the serialized header.
func (h *Header) Bytes() []byte { return bytes.Clone(h.raw) }

// powHeader is the Equihash input I: version through nNonce.
func (h *Header) powHeader() []byte { return h.raw[:powHeaderLen] }
