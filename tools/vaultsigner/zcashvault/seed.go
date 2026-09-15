package zcashvault

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A signer's seed: 32 random bytes, written once as 64 hex characters to a file only this account can read, outside
// every git repository, and never printed, logged or sent anywhere. The account key the signer publishes is
//
//	m / 44' / coin' / account'        coin 133 on mainnet, 1 on testnet and regtest
//
// and nothing below it that is hardened: every vault address is derived from the published account keys alone.

// GenerateSeed writes a new seed file. It refuses a path that exists (never overwrite a key) and a path inside a git
// work tree (a key must never be one `git add` away from a public repository).
func GenerateSeed(path string) error {
	if insideGitRepo(path) {
		return fmt.Errorf("zcashvault: %s is inside a git work tree; a signer's seed must live outside every repository", path)
	}
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return err
	}
	defer wipe(seed[:])
	text := []byte(hex.EncodeToString(seed[:]) + "\n")
	defer wipe(text)
	f, err := createOwnerOnly(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(text); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// loadSeed reads a seed file and refuses one that is readable by anyone else, one inside a git work tree, and
// anything that is not 16 to 64 bytes of hex.
func loadSeed(path string) ([]byte, error) {
	if insideGitRepo(path) {
		return nil, fmt.Errorf("zcashvault: the seed file %s is inside a git work tree; move it out before this signer runs", path)
	}
	if err := checkOwnerOnly(path); err != nil {
		return nil, fmt.Errorf("zcashvault: the seed file %s: %w", path, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defer wipe(b)
	s := strings.TrimSpace(string(b))
	if len(s) < 32 || len(s) > 128 || len(s)%2 != 0 {
		return nil, errors.New("zcashvault: the seed file does not hold 16 to 64 bytes of hex")
	}
	seed, err := hex.DecodeString(s)
	if err != nil {
		return nil, errors.New("zcashvault: the seed file is not hex")
	}
	return seed, nil
}

// accountKey derives the signer's private account key from its seed file; the master key and the seed are wiped.
func accountKey(path string, net Net, account uint32) (*ExtKey, error) {
	if account >= HardenedOffset {
		return nil, fmt.Errorf("zcashvault: account %d is out of range", account)
	}
	seed, err := loadSeed(path)
	if err != nil {
		return nil, err
	}
	defer wipe(seed)
	m, err := NewMaster(seed, net != NetMainnet)
	if err != nil {
		return nil, err
	}
	defer m.Zero()
	return m.Path(44+HardenedOffset, net.coinType()+HardenedOffset, account+HardenedOffset)
}

// AccountXPub is the extended public key a signer publishes for the descriptor.
func AccountXPub(seedPath, net string, account uint32) (string, error) {
	nt, err := parseNet(net)
	if err != nil {
		return "", err
	}
	k, err := accountKey(seedPath, nt, account)
	if err != nil {
		return "", err
	}
	defer k.Zero()
	return k.XPub(), nil
}

// insideGitRepo says whether path lies inside a directory that has a .git entry.
func insideGitRepo(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return true // cannot tell: treat as inside, which refuses
	}
	for dir := filepath.Dir(abs); ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
