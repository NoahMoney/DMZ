package coord

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/edwards25519"

	rpc "veil/tools/simrpc"
)

// PublicPrefix begins every public identity line.
const PublicPrefix = "dmzcoord1:"

const (
	identityHeader  = "dmz coord identity v1"
	seedLen         = 32
	kemKeyLen       = 32
	publicLen       = ed25519.PublicKeySize + kemKeyLen + ed25519.SignatureSize
	maxIdentityFile = 4096
	maxIDLen        = 128
	ctxIdentity     = "DMZ coord identity v1"
	hpkeIKMInfo     = "DMZ coord hpke x25519 v1"
)

// kem is the key-encapsulation half of the suite: DHKEM(X25519, HKDF-SHA256), RFC 9180, from the standard library.
var kem = hpke.DHKEM(ecdh.X25519())

// CanonicalID is the spelling every signer id takes inside this package: the DERO address as the DVM writes it with
// ADDRESS_STRING (mainnet "dero1…" prefix, no integrated arguments), which is the string vaultsigner calls dvmMe and
// the one vault.bas names vote keys with. A testnet "deto1…" spelling of the same key maps to the same id.
func CanonicalID(addr string) (string, error) {
	c := rpc.DVMAddress(strings.TrimSpace(addr))
	if c == "" || len(c) > maxIDLen {
		return "", fmt.Errorf("coord: %q is not a DERO address", clip(addr, 80))
	}
	return c, nil
}

// PublicIdentity is what every signer pins for every other signer at setup: the Ed25519 key that authenticates its
// messages, the X25519 key payloads are encrypted to, and a proof that the holder of the Ed25519 key made both for
// this DERO address. The proof catches the setup mistake that matters most — an identity line pasted against the
// wrong address — at load time instead of as a signer whose messages nobody accepts.
type PublicIdentity struct {
	ID   string            // canonical DERO address (CanonicalID)
	Sign ed25519.PublicKey // verifies this signer's messages
	KEM  []byte            // X25519 public key payloads are encrypted to
	pop  []byte            // Ed25519ctx signature by Sign over (ID, Sign, KEM)
}

// String is the identity line: PublicPrefix and 128 bytes of hex (Ed25519 key, X25519 key, proof).
func (p PublicIdentity) String() string {
	return PublicPrefix + hex.EncodeToString(p.Sign) + hex.EncodeToString(p.KEM) + hex.EncodeToString(p.pop)
}

// Fingerprint is a short digest of the address and the identity line, to be read aloud or compared on a second
// channel during the ceremony: 96 bits, six groups of four hex digits.
func (p PublicIdentity) Fingerprint() string {
	return fingerprint("DMZ coord identity fingerprint v1\n" + p.ID + "\n" + p.String())
}

func fingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	x := strings.ToUpper(hex.EncodeToString(h[:12]))
	var b strings.Builder
	for i := 0; i < len(x); i += 4 {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(x[i : i+4])
	}
	return b.String()
}

func popMessage(id string, sign, kemPub []byte) []byte {
	m := make([]byte, 0, 1+len(id)+len(sign)+len(kemPub))
	m = append(m, byte(len(id)))
	m = append(m, id...)
	m = append(m, sign...)
	return append(m, kemPub...)
}

// ParsePublicIdentity reads an identity line pinned for the signer id. It refuses a line whose proof was not made
// for that address, an Ed25519 key that is not a canonical point or has small order (signatures under such a key can
// be forged by anyone), and an X25519 key of low order (it would make every payload readable to nobody).
func ParsePublicIdentity(id, line string) (PublicIdentity, error) {
	cid, err := CanonicalID(id)
	if err != nil {
		return PublicIdentity{}, err
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, PublicPrefix) {
		return PublicIdentity{}, fmt.Errorf("coord: the identity pinned for %s does not start with %s", cid, PublicPrefix)
	}
	raw, err := hex.DecodeString(line[len(PublicPrefix):])
	if err != nil || len(raw) != publicLen {
		return PublicIdentity{}, fmt.Errorf("coord: the identity pinned for %s is not %d bytes of hex after %s", cid, publicLen, PublicPrefix)
	}
	sign, kemPub, pop := raw[:ed25519.PublicKeySize], raw[ed25519.PublicKeySize:ed25519.PublicKeySize+kemKeyLen], raw[ed25519.PublicKeySize+kemKeyLen:]
	if err := checkSignKey(sign); err != nil {
		return PublicIdentity{}, fmt.Errorf("coord: the identity pinned for %s: %v", cid, err)
	}
	if err := checkKEMKey(kemPub); err != nil {
		return PublicIdentity{}, fmt.Errorf("coord: the identity pinned for %s: %v", cid, err)
	}
	if ed25519.VerifyWithOptions(sign, popMessage(cid, sign, kemPub), pop, &ed25519.Options{Context: ctxIdentity}) != nil {
		return PublicIdentity{}, fmt.Errorf("coord: the identity pinned for %s was not made for that address, or was altered: its proof does not verify", cid)
	}
	return PublicIdentity{ID: cid, Sign: bytes.Clone(sign), KEM: bytes.Clone(kemPub), pop: bytes.Clone(pop)}, nil
}

// checkSignKey refuses an Ed25519 public key that is not a canonical encoding of a point, or whose point has small
// order. The standard library verifies against any 32 bytes; under a small-order key a signature on a chosen message
// can be produced without any private key, so a pinned key of that kind would let a relay forge that signer.
func checkSignKey(pub []byte) error {
	p, err := new(edwards25519.Point).SetBytes(pub)
	if err != nil || !bytes.Equal(p.Bytes(), pub) {
		return errors.New("the Ed25519 key is not a canonical curve point")
	}
	if new(edwards25519.Point).MultByCofactor(p).Equal(edwards25519.NewIdentityPoint()) == 1 {
		return errors.New("the Ed25519 key has small order, and signatures under it can be forged")
	}
	return nil
}

// checkKEMKey refuses an X25519 key the KEM cannot use, including the low-order points whose shared secret is zero.
func checkKEMKey(pub []byte) error {
	pk, err := ecdh.X25519().NewPublicKey(pub)
	if err != nil {
		return errors.New("the X25519 key is malformed")
	}
	probe, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if _, err := probe.ECDH(pk); err != nil {
		return errors.New("the X25519 key has low order")
	}
	if _, err := kem.NewPublicKey(pub); err != nil {
		return errors.New("the X25519 key is not usable by the KEM")
	}
	return nil
}

// Identity is this signer's coordination key pair, loaded from its identity file. It never prints, formats or
// serialises its private half: every fmt verb renders the address and the fingerprint, and JSON/text marshalling fail.
type Identity struct {
	pub  PublicIdentity
	sign ed25519.PrivateKey
	kem  hpke.PrivateKey
}

// ID is the canonical DERO address this identity belongs to.
func (i Identity) ID() string { return i.pub.ID }

// Public is the identity line other signers pin.
func (i Identity) Public() PublicIdentity { return i.pub }

func (i Identity) String() string {
	return "coord identity of " + i.pub.ID + " (" + i.pub.Fingerprint() + ")"
}

// Format renders String for every verb, so neither %x nor %#v can reach the key bytes.
func (i Identity) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, i.String()) }

// GoString is String.
func (i Identity) GoString() string { return i.String() }

// MarshalJSON refuses: a private key is never serialised.
func (i Identity) MarshalJSON() ([]byte, error) {
	return nil, errors.New("coord: an identity's private key is never serialised")
}

// MarshalText refuses, for the same reason.
func (i Identity) MarshalText() ([]byte, error) {
	return nil, errors.New("coord: an identity's private key is never serialised")
}

// fromSeed derives both key pairs from one 32-byte seed: Ed25519 from the seed as RFC 8032 defines, and the X25519
// KEM key from HKDF-SHA256(seed, info "DMZ coord hpke x25519 v1") through RFC 9180 DeriveKeyPair, so the two are
// independent keys that one backup restores.
func fromSeed(id string, seed []byte) (*Identity, error) {
	if len(seed) != seedLen {
		return nil, errors.New("coord: an identity seed is 32 bytes")
	}
	sign := ed25519.NewKeyFromSeed(seed)
	ikm, err := hkdf.Key(sha256.New, seed, nil, hpkeIKMInfo, 32)
	if err != nil {
		return nil, err
	}
	defer clear(ikm)
	k, err := kem.DeriveKeyPair(ikm)
	if err != nil {
		return nil, err
	}
	signPub := sign.Public().(ed25519.PublicKey)
	kemPub := k.PublicKey().Bytes()
	pop, err := sign.Sign(nil, popMessage(id, signPub, kemPub), &ed25519.Options{Context: ctxIdentity})
	if err != nil {
		return nil, err
	}
	return &Identity{pub: PublicIdentity{ID: id, Sign: signPub, KEM: kemPub, pop: pop}, sign: sign, kem: k}, nil
}

// GenerateIdentityFile makes a fresh identity for the signer id and writes it to path, which must not exist, readable
// by this account alone from the moment it exists (0600 with O_EXCL; on Windows a protected owner-only DACL set at
// creation). It returns the public half, which is all a caller may show. The seed is never returned or printed.
func GenerateIdentityFile(path, id string) (PublicIdentity, error) {
	cid, err := CanonicalID(id)
	if err != nil {
		return PublicIdentity{}, err
	}
	seed := make([]byte, seedLen)
	defer clear(seed)
	if _, err := rand.Read(seed); err != nil {
		return PublicIdentity{}, err
	}
	idn, err := fromSeed(cid, seed)
	if err != nil {
		return PublicIdentity{}, err
	}
	body := make([]byte, 0, 256)
	body = append(body, identityHeader+"\nid "+cid+"\nseed "...)
	hexSeed := make([]byte, 2*seedLen)
	hex.Encode(hexSeed, seed)
	body = append(body, hexSeed...)
	body = append(body, '\n')
	clear(hexSeed)
	defer clear(body)
	f, err := createPrivate(path)
	if err != nil {
		return PublicIdentity{}, fmt.Errorf("coord: creating %s: %w (an existing identity file is never overwritten)", path, err)
	}
	_, werr := f.Write(body)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(path)
		return PublicIdentity{}, fmt.Errorf("coord: writing %s: %w", path, err)
	}
	return idn.pub, nil
}

// LoadIdentityFile reads an identity file written by GenerateIdentityFile. It refuses a file other accounts can read
// (fail closed: a key that was readable must be treated as exposed and replaced, not used), a symbolic link, and
// anything that is not exactly the three lines it writes. No error repeats the file's contents.
func LoadIdentityFile(path string) (*Identity, error) {
	if err := checkPrivate(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxIdentityFile+1))
	defer clear(b)
	if err != nil {
		return nil, err
	}
	if len(b) > maxIdentityFile {
		return nil, fmt.Errorf("coord: %s is not an identity file (too large)", path)
	}
	lines := bytes.Split(bytes.TrimRight(b, "\r\n"), []byte("\n"))
	for i := range lines {
		lines[i] = bytes.TrimRight(lines[i], "\r")
	}
	if len(lines) != 3 || string(lines[0]) != identityHeader || !bytes.HasPrefix(lines[1], []byte("id ")) || !bytes.HasPrefix(lines[2], []byte("seed ")) {
		return nil, fmt.Errorf("coord: %s is not a %q file", path, identityHeader)
	}
	id := string(lines[1][len("id "):])
	cid, err := CanonicalID(id)
	if err != nil || cid != id {
		return nil, fmt.Errorf("coord: %s names an id that is not a canonical DERO address", path)
	}
	hexSeed := lines[2][len("seed "):]
	if len(hexSeed) != 2*seedLen {
		return nil, fmt.Errorf("coord: %s holds a seed of the wrong length", path)
	}
	seed := make([]byte, seedLen)
	defer clear(seed)
	if _, err := hex.Decode(seed, hexSeed); err != nil {
		return nil, fmt.Errorf("coord: %s holds a seed that is not hex", path)
	}
	return fromSeed(cid, seed)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
