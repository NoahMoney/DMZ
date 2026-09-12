package zecswap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Version-5 (ZIP 225) and version-6 (ZIP 229) transactions with transparent inputs and outputs only, and their
// ZIP 244 digests: the transaction id and the SIGHASH_ALL signature digest of a transparent input. That is all a swap
// needs — the funding of the script, the claim, the refund — and nothing shielded is ever built here (the design
// note says why: the market's Zcash leg is transparent by construction). The v5 digest tree follows ZIP 244 exactly
// and the official test vectors (testdata/zip_0244.json) pin every byte of it.
//
// Version 6 is what current wallets build under NU6.3 (librustzcash 0.30.1 proposes it by default, and zebrad 6.3.0
// mines its coinbase as v6), so a maker's claim may well be one. Its transparent layout is v5's; the differences for a
// transaction with no shielded parts are exactly three, taken from librustzcash 0.30.1 and the orchard crate 0.15.5
// (zcash_primitives/src/transaction/txid.rs to_hash_v6, orchard/src/bundle/commitments.rs), and pinned by
// transactions captured from a real zebrad 6.3.0 on regtest (testdata/regtest_v6.json):
//
//   - the header and version group are 0x80000006 and 0xD884B698, in the serialization and the header digest;
//   - the empty Orchard bundle's digest is personalized "ZTxIdOrchardH_v6" (v5: "ZTxIdOrchardHash");
//   - an empty Ironwood bundle digest, personalized "ZTxIdIronwd_H_v6", is the last child of the txid and of the
//     signature digest, and its action count (0) is the last byte of the serialization.
//
// A v6 transaction with any shielded part is refused by name (ErrShielded): its digests move the anchors and are not
// implemented here.

const (
	// V5Version is nVersion 5 with the fOverwintered bit set, as serialized.
	V5Version = 0x80000005
	// V5VersionGroupID is nVersionGroupId for v5 transactions.
	V5VersionGroupID = 0x26A7270A
	// V6Version and V6VersionGroupID are ZIP 229's.
	V6Version        = 0x80000006
	V6VersionGroupID = 0xD884B698
	// MaxExpiryHeight is ZIP 203's ceiling on nExpiryHeight; above it a transaction is invalid, and the value
	// is also where nLockTime stops meaning a height (BIP 65's LOCKTIME_THRESHOLD, one higher).
	MaxExpiryHeight = 499_999_999
)

// Consensus branch ids. The digest tree is personalized with the branch id ("ZcashTxHash_" + the id), so it is
// signed over: a signature made under the wrong id is invalid on every node, and there is no way to fix it
// after the fact except to sign again.
//
// The id to sign under belongs to the block the transaction will be MINED in, not the one at the tip, so it is
// getblockchaininfo's consensus.nextblock — never consensus.chaintip. The two differ at exactly one moment, an
// activation boundary, which is the only moment the choice matters.
//
// Do not read these constants as the answer. They are here so a build can tell whether it recognises the epoch
// the node reports and refuse to sign when it does not: a binary older than the chain it is talking to knows
// nothing about the id it would need. Ask the node, then CheckBranchID.
const (
	BranchNU5  = 0xC2D6D0B4 // ZIP 252
	BranchNU6  = 0xC8E71055 // ZIP 253
	BranchNU61 = 0x4DEC4DF0 // ZIP 255
	BranchNU62 = 0x5437F330 // ZIP 257
	BranchNU63 = 0x37A5165B // ZIP 258
)

// Upgrade is one network upgrade and the height it activates at on each network.
type Upgrade struct {
	Name     string
	BranchID uint32
	Mainnet  uint32
	Testnet  uint32
}

// Upgrades are the epochs this build knows, in ascending height order, from NU5 — the first that carries v5
// transactions, which is all this package builds or reads. Heights and ids are from the ZIPs named above.
var Upgrades = []Upgrade{
	{"NU5", BranchNU5, 1_687_104, 1_842_420},
	{"NU6", BranchNU6, 2_726_400, 2_976_000},
	{"NU6.1", BranchNU61, 3_146_400, 3_536_500},
	{"NU6.2", BranchNU62, 3_364_600, 4_052_000},
	{"NU6.3", BranchNU63, 3_428_143, 4_134_000},
}

func (u Upgrade) height(net Network) uint32 {
	if net == Mainnet {
		return u.Mainnet
	}
	return u.Testnet
}

// BranchIDForHeight is the branch id of the epoch containing the given height, as this build understands the
// upgrade schedule. A height before NU5 has no answer here: this package neither builds nor reads the
// transaction versions that came before it.
func BranchIDForHeight(net Network, height uint32) (uint32, error) {
	if height < Upgrades[0].height(net) {
		return 0, fmt.Errorf("zecswap: %s height %d is before NU5 (%d); this package handles v5 and v6 transactions only", net, height, Upgrades[0].height(net))
	}
	id := Upgrades[0].BranchID
	for _, u := range Upgrades {
		if height >= u.height(net) {
			id = u.BranchID
		}
	}
	return id, nil
}

// KnownBranchID says whether this build recognises a consensus branch id at all.
func KnownBranchID(id uint32) bool {
	for _, u := range Upgrades {
		if u.BranchID == id {
			return true
		}
	}
	return false
}

func upgradeIndex(id uint32) int {
	for i, u := range Upgrades {
		if u.BranchID == id {
			return i
		}
	}
	return -1
}

// CheckBranchID is what a caller runs before it signs: nodeBranchID is getblockchaininfo's consensus.nextblock
// branch id and nextBlockHeight is the height that block will have (the tip plus one). It fails closed in both
// directions — an id this build has never heard of means the binary is older than the chain and must not guess,
// and an id that disagrees with the schedule this build carries means one of the two is wrong and neither can
// be trusted to sign.
func CheckBranchID(net Network, nextBlockHeight, nodeBranchID uint32) error {
	if !KnownBranchID(nodeBranchID) {
		return fmt.Errorf("zecswap: the node reports consensus branch id %08x for %s height %d and this build does not know it; it is older than the chain — update it rather than sign under a guess", nodeBranchID, net, nextBlockHeight)
	}
	want, err := BranchIDForHeight(net, nextBlockHeight)
	if err != nil {
		return err
	}
	if want != nodeBranchID {
		return fmt.Errorf("zecswap: the node reports consensus branch id %08x for %s height %d, but this build's upgrade schedule says %08x; one of the two is wrong", nodeBranchID, net, nextBlockHeight, want)
	}
	return nil
}

// CheckBranchIDOnSchedule is CheckBranchID against a schedule the node itself reports: regtest's, whose heights are
// the operator's to choose (zebrad's regtest activates NU5 at 2 and NU6.3 at 6). It is reached only through a node
// whose genesis block is regtest's (ChainInfo.Regtest), which no mainnet or testnet node has; on those networks the
// schedule is always this build's own. The id must still be one this build knows, and it must be the one the node's
// own table puts at the next block.
func CheckBranchIDOnSchedule(schedule []NodeUpgrade, nextBlockHeight, nodeBranchID uint32) error {
	if !KnownBranchID(nodeBranchID) {
		return fmt.Errorf("zecswap: the regtest node reports consensus branch id %08x, which this build does not know", nodeBranchID)
	}
	want, best, bestIdx := uint32(0), uint32(0), -1
	for _, u := range schedule {
		idx := upgradeIndex(u.BranchID)
		if idx < 0 || u.Height > nextBlockHeight || u.Status == "disabled" {
			continue
		}
		if bestIdx < 0 || u.Height > best || (u.Height == best && idx > bestIdx) {
			want, best, bestIdx = u.BranchID, u.Height, idx
		}
	}
	if bestIdx < 0 {
		return fmt.Errorf("zecswap: the regtest node's upgrade table has no epoch this build knows at height %d", nextBlockHeight)
	}
	if want != nodeBranchID {
		return fmt.Errorf("zecswap: the regtest node's own upgrade table puts %08x at height %d, and its consensus.nextblock says %08x", want, nextBlockHeight, nodeBranchID)
	}
	return nil
}

// OutPoint names a transparent output being spent.
type OutPoint struct {
	Hash  [32]byte // the previous transaction's id, as serialized (not display order)
	Index uint32
}

// TxIn is a transparent input. Value and ScriptPubKey describe the output it spends; ZIP 244 commits to both
// in the signature digest, which is what lets a signer prove what it thought it was spending.
type TxIn struct {
	PrevOut      OutPoint
	ScriptSig    []byte
	Sequence     uint32
	Value        uint64
	ScriptPubKey []byte
}

// TxOut is a transparent output.
type TxOut struct {
	Value        uint64
	ScriptPubKey []byte
}

// Tx is a v5 or v6 transaction with transparent parts and, for v5 read from the chain, an Orchard bundle; never a
// Sapling one. The companion builds only transparent transactions (Orchard stays nil).
type Tx struct {
	// Version is 5 (also when 0) or 6.
	Version      uint32
	BranchID     uint32
	LockTime     uint32
	ExpiryHeight uint32
	// Sapling is a v5 transaction's Sapling bundle as read (sapling.go): nil when empty, and always nil for what this
	// package builds.
	Sapling *SaplingBundle
	In      []TxIn
	Out     []TxOut
	Orchard *OrchardBundle
}

func (t *Tx) v6() bool { return t.Version == 6 }

func (t *Tx) header() uint32 {
	if t.v6() {
		return V6Version
	}
	return V5Version
}

func (t *Tx) groupID() uint32 {
	if t.v6() {
		return V6VersionGroupID
	}
	return V5VersionGroupID
}

func blake256(person string, data []byte) [32]byte {
	if len(person) != 16 {
		panic("zecswap: personalization must be 16 bytes: " + person)
	}
	var out [32]byte
	copy(out[:], blake2bSum(32, []byte(person), data))
	return out
}

func le32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func le64(v uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }

// CompactSize is Bitcoin's variable-length count.
func CompactSize(n uint64) []byte {
	switch {
	case n < 0xfd:
		return []byte{byte(n)}
	case n <= 0xffff:
		return append([]byte{0xfd}, byte(n), byte(n>>8))
	case n <= 0xffffffff:
		return append([]byte{0xfe}, le32(uint32(n))...)
	}
	return append([]byte{0xff}, le64(n)...)
}

func withLen(b []byte) []byte { return append(CompactSize(uint64(len(b))), b...) }

// ---- ZIP 244 digests (and ZIP 229's for v6) --------------------------------------------------------------------

func (t *Tx) headerDigest() [32]byte {
	var b bytes.Buffer
	b.Write(le32(t.header()))
	b.Write(le32(t.groupID()))
	b.Write(le32(t.BranchID))
	b.Write(le32(t.LockTime))
	b.Write(le32(t.ExpiryHeight))
	return blake256("ZTxIdHeadersHash", b.Bytes())
}

func (t *Tx) prevoutsDigest() [32]byte {
	var b bytes.Buffer
	for _, in := range t.In {
		b.Write(in.PrevOut.Hash[:])
		b.Write(le32(in.PrevOut.Index))
	}
	return blake256("ZTxIdPrevoutHash", b.Bytes())
}

func (t *Tx) sequenceDigest() [32]byte {
	var b bytes.Buffer
	for _, in := range t.In {
		b.Write(le32(in.Sequence))
	}
	return blake256("ZTxIdSequencHash", b.Bytes())
}

func (t *Tx) outputsDigest() [32]byte {
	var b bytes.Buffer
	for _, out := range t.Out {
		b.Write(le64(out.Value))
		b.Write(withLen(out.ScriptPubKey))
	}
	return blake256("ZTxIdOutputsHash", b.Bytes())
}

func (t *Tx) transparentDigest() [32]byte {
	if len(t.In) == 0 && len(t.Out) == 0 {
		return blake256("ZTxIdTranspaHash", nil)
	}
	var b bytes.Buffer
	p, s, o := t.prevoutsDigest(), t.sequenceDigest(), t.outputsDigest()
	b.Write(p[:])
	b.Write(s[:])
	b.Write(o[:])
	return blake256("ZTxIdTranspaHash", b.Bytes())
}

func saplingEmptyDigest() [32]byte { return blake256("ZTxIdSaplingHash", nil) }

// shieldedTail is what follows the transparent digest in both the txid and the signature digest: the Sapling and
// Orchard digests, and for v6 the Ironwood one. A v6 transaction here has no shielded parts.
func (t *Tx) shieldedTail() []byte {
	var b bytes.Buffer
	if t.v6() {
		// ZIP 229: a v6 transaction is built and read here only without shielded parts, so all three are empty
		sa, or, iw := saplingEmptyDigest(), blake256("ZTxIdOrchardH_v6", nil), blake256("ZTxIdIronwd_H_v6", nil)
		b.Write(sa[:])
		b.Write(or[:])
		b.Write(iw[:])
		return b.Bytes()
	}
	sa, or := t.Sapling.digest(), t.Orchard.digest()
	b.Write(sa[:])
	b.Write(or[:])
	return b.Bytes()
}

func (t *Tx) txPerson() string { return "ZcashTxHash_" + string(le32(t.BranchID)) }

// TxID is the ZIP 244 transaction id: the digests under the branch-personalized hash. Explorers show it
// byte-reversed; this is the serialized order, the one a prevout carries.
func (t *Tx) TxID() [32]byte {
	var b bytes.Buffer
	h, tr := t.headerDigest(), t.transparentDigest()
	b.Write(h[:])
	b.Write(tr[:])
	b.Write(t.shieldedTail())
	return blake256(t.txPerson(), b.Bytes())
}

// SigDigest is the ZIP 244 signature digest for transparent input i under SIGHASH_ALL: what the input's key
// signs. Every input's amount and scriptPubKey must be filled in (the vectors and the node both supply them).
func (t *Tx) SigDigest(i int, hashType byte) ([32]byte, error) {
	var zero [32]byte
	if hashType != SigHashAll {
		return zero, errors.New("zecswap: only SIGHASH_ALL is signed")
	}
	if i < 0 || i >= len(t.In) {
		return zero, fmt.Errorf("zecswap: input %d of %d", i, len(t.In))
	}
	if t.Version != 0 && t.Version != 5 && t.Version != 6 {
		return zero, fmt.Errorf("zecswap: transaction version %d", t.Version)
	}
	if t.v6() && t.Orchard != nil {
		return zero, errors.New("zecswap: a v6 transaction with an Orchard bundle is not signed here")
	}
	if !KnownBranchID(t.BranchID) {
		return zero, fmt.Errorf("zecswap: will not sign under consensus branch id %08x, which this build does not know; read it from the node (getblockchaininfo → consensus.nextblock) and check it with CheckBranchID", t.BranchID)
	}
	if t.ExpiryHeight > MaxExpiryHeight {
		return zero, fmt.Errorf("zecswap: nExpiryHeight %d is above ZIP 203's maximum %d, so the transaction is invalid in every block", t.ExpiryHeight, MaxExpiryHeight)
	}
	var amounts, scripts bytes.Buffer
	for _, in := range t.In {
		if in.ScriptPubKey == nil {
			return zero, errors.New("zecswap: every input needs the scriptPubKey it spends")
		}
		amounts.Write(le64(in.Value))
		scripts.Write(withLen(in.ScriptPubKey))
	}
	in := t.In[i]
	var txin bytes.Buffer
	txin.Write(in.PrevOut.Hash[:])
	txin.Write(le32(in.PrevOut.Index))
	txin.Write(le64(in.Value))
	txin.Write(withLen(in.ScriptPubKey))
	txin.Write(le32(in.Sequence))

	var tb bytes.Buffer
	tb.WriteByte(hashType)
	p, a, s, q, o, x := t.prevoutsDigest(), blake256("ZTxTrAmountsHash", amounts.Bytes()), blake256("ZTxTrScriptsHash", scripts.Bytes()), t.sequenceDigest(), t.outputsDigest(), blake256("Zcash___TxInHash", txin.Bytes())
	tb.Write(p[:])
	tb.Write(a[:])
	tb.Write(s[:])
	tb.Write(q[:])
	tb.Write(o[:])
	tb.Write(x[:])
	transparentSig := blake256("ZTxIdTranspaHash", tb.Bytes())

	var b bytes.Buffer
	h := t.headerDigest()
	b.Write(h[:])
	b.Write(transparentSig[:])
	b.Write(t.shieldedTail())
	return blake256(t.txPerson(), b.Bytes()), nil
}

// ---- serialization (ZIP 225 / ZIP 229, no shielded bundle) ------------------------------------------------------

// Serialize is the wire form: header, transparent inputs and outputs, then the Sapling and Orchard bundles (empty
// for anything this package builds, as read for a transaction it parsed) — and for v6 the empty Orchard and Ironwood
// bundles.
func (t *Tx) Serialize() []byte {
	var b bytes.Buffer
	b.Write(le32(t.header()))
	b.Write(le32(t.groupID()))
	b.Write(le32(t.BranchID))
	b.Write(le32(t.LockTime))
	b.Write(le32(t.ExpiryHeight))
	b.Write(CompactSize(uint64(len(t.In))))
	for _, in := range t.In {
		b.Write(in.PrevOut.Hash[:])
		b.Write(le32(in.PrevOut.Index))
		b.Write(withLen(in.ScriptSig))
		b.Write(le32(in.Sequence))
	}
	b.Write(CompactSize(uint64(len(t.Out))))
	for _, out := range t.Out {
		b.Write(le64(out.Value))
		b.Write(withLen(out.ScriptPubKey))
	}
	t.Sapling.serialize(&b) // two zero counts when there is no Sapling bundle
	if t.v6() {
		b.Write(CompactSize(0)) // nActionsOrchard
		b.Write(CompactSize(0)) // nActionsIronwood
		return b.Bytes()
	}
	t.Orchard.serialize(&b)
	return b.Bytes()
}

// ErrShielded is returned by ParseV6 for a v6 transaction that carries any shielded bundle: ZIP 229's digests for
// them have no test vectors here, so this package does not read them and says so rather than misreading them. A v5
// transaction's Sapling and Orchard bundles are read (sapling.go, orchard.go).
var ErrShielded = errors.New("zecswap: the transaction has a shielded bundle this package does not read")

// ParseTx reads a serialized v5 or v6 transaction (ParseV5, ParseV6).
func ParseTx(raw []byte) (*Tx, error) {
	if len(raw) < 4 {
		return nil, errors.New("zecswap: transaction truncated")
	}
	switch binary.LittleEndian.Uint32(raw) {
	case V5Version:
		return ParseV5(raw)
	case V6Version:
		return ParseV6(raw)
	}
	return nil, fmt.Errorf("zecswap: transaction header %08x is neither v5 nor v6", binary.LittleEndian.Uint32(raw))
}

// parseCommon reads the header and the transparent parts, which v5 and v6 share.
func parseCommon(raw []byte, version, group uint32) (*Tx, *reader, error) {
	r := &reader{b: raw}
	if v := r.u32(); v != version {
		return nil, nil, fmt.Errorf("zecswap: header %08x is not %08x", v, version)
	}
	if g := r.u32(); g != group {
		return nil, nil, fmt.Errorf("zecswap: version group %08x is not %08x", g, group)
	}
	t := &Tx{BranchID: r.u32(), LockTime: r.u32(), ExpiryHeight: r.u32()}
	if r.err != nil {
		return nil, nil, r.err
	}
	if t.ExpiryHeight > MaxExpiryHeight {
		return nil, nil, fmt.Errorf("zecswap: nExpiryHeight %d is above ZIP 203's maximum %d", t.ExpiryHeight, MaxExpiryHeight)
	}
	nIn := r.compact()
	for i := uint64(0); i < nIn && r.err == nil; i++ {
		var in TxIn
		copy(in.PrevOut.Hash[:], r.bytes(32))
		in.PrevOut.Index = r.u32()
		in.ScriptSig = r.varBytes()
		in.Sequence = r.u32()
		t.In = append(t.In, in)
	}
	nOut := r.compact()
	for i := uint64(0); i < nOut && r.err == nil; i++ {
		var out TxOut
		out.Value = r.u64()
		out.ScriptPubKey = r.varBytes()
		t.Out = append(t.Out, out)
	}
	if r.err != nil {
		return nil, nil, r.err
	}
	sapling, err := parseSapling(r)
	if err != nil {
		return nil, nil, err
	}
	t.Sapling = sapling
	return t, r, nil
}

// ParseV5 reads a serialized v5 transaction: its transparent parts, and any Sapling and Orchard bundles. Inputs come
// back without Value and ScriptPubKey (the wire form does not carry them); fill them in from the outputs being
// spent before asking for a signature digest.
func ParseV5(raw []byte) (*Tx, error) {
	t, r, err := parseCommon(raw, V5Version, V5VersionGroupID)
	if err != nil {
		if err.Error() == fmt.Sprintf("zecswap: header %08x is not %08x", binaryHeader(raw), uint32(V5Version)) {
			return nil, fmt.Errorf("zecswap: version %08x is not v5", binaryHeader(raw))
		}
		return nil, err
	}
	t.Version = 5
	orchard, err := parseOrchard(r)
	if err != nil {
		return nil, err
	}
	t.Orchard = orchard
	if r.p != len(raw) {
		return nil, fmt.Errorf("zecswap: %d trailing bytes", len(raw)-r.p)
	}
	return t, nil
}

// ParseV6 reads a serialized v6 transaction with transparent parts only: any Orchard or Ironwood action is
// ErrShielded.
func ParseV6(raw []byte) (*Tx, error) {
	t, r, err := parseCommon(raw, V6Version, V6VersionGroupID)
	if err != nil {
		return nil, err
	}
	t.Version = 6
	if t.Sapling != nil {
		return nil, ErrShielded
	}
	nOrchard, nIronwood := r.compact(), r.compact()
	if r.err != nil {
		return nil, r.err
	}
	if nOrchard != 0 || nIronwood != 0 {
		return nil, ErrShielded
	}
	if r.p != len(raw) {
		return nil, fmt.Errorf("zecswap: %d trailing bytes", len(raw)-r.p)
	}
	return t, nil
}

func binaryHeader(raw []byte) uint32 {
	if len(raw) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(raw)
}

type reader struct {
	b   []byte
	p   int
	err error
}

// bytes takes the next n bytes. The bound is written against what is left rather than as r.p+n > len(r.b),
// because n comes from a length field in a transaction the counterparty chose: a CompactSize near 2^63 makes
// r.p+n overflow to a negative number, the check pass, and the slice panic — a crash of the watcher at exactly
// the moment it must act (finding ZEC-5 of the 2026-09-10 audit).
func (r *reader) bytes(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.b)-r.p {
		r.err = errors.New("zecswap: transaction truncated")
		return nil
	}
	out := r.b[r.p : r.p+n]
	r.p += n
	return out
}

// varBytes reads a CompactSize count and that many bytes. The count is compared with what is left while it is
// still a uint64, so no conversion to int can wrap.
func (r *reader) varBytes() []byte {
	n := r.compact()
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.b)-r.p) {
		r.err = errors.New("zecswap: transaction truncated")
		return nil
	}
	return r.bytes(int(n))
}
func (r *reader) u32() uint32 {
	b := r.bytes(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}
func (r *reader) u64() uint64 {
	b := r.bytes(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
func (r *reader) compact() uint64 {
	b := r.bytes(1)
	if b == nil {
		return 0
	}
	switch b[0] {
	case 0xfd:
		x := r.bytes(2)
		if x == nil {
			return 0
		}
		return uint64(binary.LittleEndian.Uint16(x))
	case 0xfe:
		return uint64(r.u32())
	case 0xff:
		return r.u64()
	}
	return uint64(b[0])
}
