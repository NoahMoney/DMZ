package coord

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileConfig is the coordination configuration file, JSON, identical on every signer except identity_file and
// (optionally) stores. Example, with placeholders:
//
//	{
//	  "version": 1,
//	  "vault": "<64-hex vault scid>",
//	  "identity_file": "coord-identity.key",
//	  "signers": [
//	    {"id": "dero1…signer A", "identity": "dmzcoord1:…"},
//	    {"id": "dero1…signer B", "identity": "dmzcoord1:…"},
//	    {"id": "dero1…signer C", "identity": "dmzcoord1:…"}
//	  ],
//	  "stores": ["https://relay-a.example:8445", "https://relay-b.example:8445", "dir:<shared directory>"],
//	  "ttl": "24h", "max_payload_bytes": 4194304, "fetch_timeout": "10s", "max_clock_skew": "5m"
//	}
//
// Relative paths (identity_file, dir: stores) are resolved against the file's own directory. Unknown fields are
// refused, so a misspelt setting is an error rather than a default.
type FileConfig struct {
	Version         int        `json:"version"`
	Vault           string     `json:"vault"`
	IdentityFile    string     `json:"identity_file,omitempty"`
	Signers         []FilePeer `json:"signers"`
	Stores          []string   `json:"stores"`
	TTL             string     `json:"ttl,omitempty"`
	MaxPayloadBytes int        `json:"max_payload_bytes,omitempty"`
	FetchTimeout    string     `json:"fetch_timeout,omitempty"`
	MaxClockSkew    string     `json:"max_clock_skew,omitempty"`

	dir string
}

// FilePeer pins one signer: its DERO address and its identity line (`vaultsigner keygen` prints both).
type FilePeer struct {
	ID       string `json:"id"`
	Identity string `json:"identity"`
}

const maxConfigFile = 1 << 20

// ReadConfigFile reads and checks the shape of a configuration file; ParseConfig does the same for bytes.
func ReadConfigFile(path string) (*FileConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxConfigFile+1))
	if err != nil {
		return nil, err
	}
	c, err := ParseConfig(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.dir = filepath.Dir(path)
	return c, nil
}

// ParseConfig parses a configuration strictly: one JSON object, known fields only, version 1.
func ParseConfig(b []byte) (*FileConfig, error) {
	if len(b) > maxConfigFile {
		return nil, errors.New("coord: the configuration is over 1 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c FileConfig
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("coord: configuration: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("coord: configuration: data after the JSON object")
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("coord: configuration version %d (this signer reads version 1)", c.Version)
	}
	return &c, nil
}

// VaultID is the vault scid as bytes.
func (c *FileConfig) VaultID() ([32]byte, error) {
	var v [32]byte
	if !hex32(c.Vault, &v) || strings.ToLower(c.Vault) != c.Vault {
		return v, fmt.Errorf("coord: configuration: vault must be the 64 lowercase hex characters of the scid, not %q", clip(c.Vault, 80))
	}
	return v, nil
}

// Peers parses and checks every pinned identity: each proof must verify for its address, and no address or key may
// appear twice.
func (c *FileConfig) Peers() ([]PublicIdentity, error) {
	if len(c.Signers) < 2 || len(c.Signers) > MaxRecipients {
		return nil, fmt.Errorf("coord: configuration: pin 2 to %d signers, this one included (got %d)", MaxRecipients, len(c.Signers))
	}
	out := make([]PublicIdentity, 0, len(c.Signers))
	ids, keys := map[string]bool{}, map[string]string{}
	for i, s := range c.Signers {
		p, err := ParsePublicIdentity(s.ID, s.Identity)
		if err != nil {
			return nil, fmt.Errorf("coord: configuration: signers[%d]: %v", i, err)
		}
		if ids[p.ID] {
			return nil, fmt.Errorf("coord: configuration: %s is pinned twice", p.ID)
		}
		if other, dup := keys[string(p.Sign)]; dup {
			return nil, fmt.Errorf("coord: configuration: %s and %s are pinned with the same key", other, p.ID)
		}
		ids[p.ID], keys[string(p.Sign)] = true, p.ID
		out = append(out, p)
	}
	return out, nil
}

// Fingerprint is SetFingerprint of the file's vault and pins.
func (c *FileConfig) Fingerprint() (string, error) {
	v, err := c.VaultID()
	if err != nil {
		return "", err
	}
	peers, err := c.Peers()
	if err != nil {
		return "", err
	}
	return SetFingerprint(v, peers), nil
}

func parseDur(name, s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("coord: configuration: %s %q is not a positive duration", name, clip(s, 40))
	}
	return d, nil
}

// OpenStores opens every configured store.
func (c *FileConfig) OpenStores() ([]Store, error) {
	fetch, err := parseDur("fetch_timeout", c.FetchTimeout, DefaultFetchTimeout)
	if err != nil {
		return nil, err
	}
	skew, err := parseDur("max_clock_skew", c.MaxClockSkew, DefaultMaxSkew)
	if err != nil {
		return nil, err
	}
	if len(c.Stores) == 0 {
		return nil, errors.New("coord: configuration: no stores (a relay URL or dir:<path>)")
	}
	seen := map[string]bool{}
	var out []Store
	for _, spec := range c.Stores {
		s, err := OpenStore(spec, c.dir, fetch, skew)
		if err != nil {
			return nil, err
		}
		if seen[s.Name()] {
			return nil, fmt.Errorf("coord: configuration: store %s is listed twice", s.Name())
		}
		seen[s.Name()] = true
		out = append(out, s)
	}
	return out, nil
}

// OpenStore opens one store from its specification: an http:// or https:// relay URL, or dir:<path> (relative to
// baseDir when not absolute).
func OpenStore(spec, baseDir string, timeout, maxSkew time.Duration) (Store, error) {
	switch {
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"):
		return NewHTTPStore(spec, timeout, maxSkew)
	case strings.HasPrefix(spec, "dir:"):
		return NewDirStore(resolve(baseDir, strings.TrimPrefix(spec, "dir:")))
	}
	return nil, fmt.Errorf("coord: store %q: want an http(s):// relay URL or dir:<path>", clip(spec, 80))
}

func resolve(base, p string) string {
	if p == "" || filepath.IsAbs(p) || base == "" {
		return p
	}
	return filepath.Join(base, p)
}

// IdentityPath is the identity file this configuration names, resolved against the configuration's directory.
func (c *FileConfig) IdentityPath() string { return resolve(c.dir, c.IdentityFile) }

// NodeConfig loads the identity (identityFile overrides identity_file when not empty) and builds a Config.
func (c *FileConfig) NodeConfig(identityFile string) (Config, error) {
	if _, err := c.VaultID(); err != nil {
		return Config{}, err
	}
	path := identityFile
	if path == "" {
		if c.IdentityFile == "" {
			return Config{}, errors.New("coord: configuration: no identity_file, and none given on the command line")
		}
		path = c.IdentityPath()
	}
	id, err := LoadIdentityFile(path)
	if err != nil {
		return Config{}, err
	}
	peers, err := c.Peers()
	if err != nil {
		return Config{}, err
	}
	stores, err := c.OpenStores()
	if err != nil {
		return Config{}, err
	}
	ttl, err := parseDur("ttl", c.TTL, DefaultTTL)
	if err != nil {
		return Config{}, err
	}
	fetch, _ := parseDur("fetch_timeout", c.FetchTimeout, DefaultFetchTimeout)
	skew, _ := parseDur("max_clock_skew", c.MaxClockSkew, DefaultMaxSkew)
	return Config{Vault: c.Vault, Identity: id, Signers: peers, Stores: stores, TTL: ttl, MaxPayload: c.MaxPayloadBytes, MaxSkew: skew, FetchTimeout: fetch}, nil
}

// RelayGroup is the vault and pins a relay needs from this configuration (no identity file is read).
func (c *FileConfig) RelayGroup() (RelayGroup, error) {
	v, err := c.VaultID()
	if err != nil {
		return RelayGroup{}, err
	}
	peers, err := c.Peers()
	if err != nil {
		return RelayGroup{}, err
	}
	return RelayGroup{Vault: v, Signers: peers}, nil
}
