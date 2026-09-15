package coord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileFleet generates n identity files in dir and returns their pins and paths.
func fileFleet(t *testing.T, dir string, n int) ([]FilePeer, []string) {
	t.Helper()
	var peers []FilePeer
	var keys []string
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("signer%d.key", i))
		pub, err := GenerateIdentityFile(p, testAddr(byte(i+1)))
		if err != nil {
			t.Fatal(err)
		}
		peers = append(peers, FilePeer{ID: pub.ID, Identity: pub.String()})
		keys = append(keys, p)
	}
	return peers, keys
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAConfigFileBuildsWorkingNodes(t *testing.T) {
	dir := t.TempDir()
	peers, keys := fileFleet(t, dir, 3)
	if err := os.Mkdir(filepath.Join(dir, "shared"), 0o700); err != nil {
		t.Fatal(err)
	}
	fc := FileConfig{Version: 1, Vault: testVaultHex, IdentityFile: "signer0.key", Signers: peers, Stores: []string{"dir:shared"}, TTL: "2h", FetchTimeout: "3s"}
	path := filepath.Join(dir, "coord.json")
	writeJSON(t, path, fc)
	c, err := ReadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg0, err := c.NodeConfig("")
	if err != nil {
		t.Fatal(err)
	}
	n0, err := New(cfg0)
	if err != nil {
		t.Fatal(err)
	}
	cfg1, err := c.NodeConfig(keys[1]) // the command-line override
	if err != nil {
		t.Fatal(err)
	}
	n1, err := New(cfg1)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, n0, "xmr/info/1", []byte("hello"))
	if got := mustGet(t, n1, "xmr/info/1"); string(got[n0.Self()]) != "hello" {
		t.Fatalf("n1 sees %s", showMap(got))
	}
	fp, err := c.Fingerprint()
	if err != nil || fp != n0.Fingerprint() || fp != n1.Fingerprint() {
		t.Fatalf("fingerprints differ: file %s, nodes %s %s (%v)", fp, n0.Fingerprint(), n1.Fingerprint(), err)
	}
	fc.Signers = []FilePeer{peers[2], peers[0], peers[1]}
	c2, _ := ParseConfig(mustJSON(t, fc))
	if fp2, _ := c2.Fingerprint(); fp2 != fp {
		t.Fatal("the fingerprint depends on the order of the signers")
	}
	fc.Signers = peers[:2]
	c3, _ := ParseConfig(mustJSON(t, fc))
	if fp3, _ := c3.Fingerprint(); fp3 == fp {
		t.Fatal("the fingerprint does not change with the signer set")
	}
	g, err := c.RelayGroup()
	if err != nil || len(g.Signers) != 3 {
		t.Fatalf("relay group: %v", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAConfigWithAMistakeIsRefused(t *testing.T) {
	dir := t.TempDir()
	peers, _ := fileFleet(t, dir, 3)
	base := func() FileConfig {
		return FileConfig{Version: 1, Vault: testVaultHex, IdentityFile: "signer0.key", Signers: append([]FilePeer(nil), peers...), Stores: []string{"http://127.0.0.1:1"}}
	}
	cases := map[string]func(c *FileConfig){
		"version 2":        func(c *FileConfig) { c.Version = 2 },
		"vault upper case": func(c *FileConfig) { c.Vault = strings.ToUpper(testVaultHex) },
		"vault short":      func(c *FileConfig) { c.Vault = testVaultHex[:10] },
		"one signer":       func(c *FileConfig) { c.Signers = c.Signers[:1] },
		"signer twice":     func(c *FileConfig) { c.Signers = append(c.Signers, c.Signers[0]) },
		"identities swapped": func(c *FileConfig) {
			c.Signers[0].Identity, c.Signers[1].Identity = c.Signers[1].Identity, c.Signers[0].Identity
		},
		"not an address":  func(c *FileConfig) { c.Signers[2].ID = "someone" },
		"no stores":       func(c *FileConfig) { c.Stores = nil },
		"unknown store":   func(c *FileConfig) { c.Stores = []string{"ftp://h"} },
		"store twice":     func(c *FileConfig) { c.Stores = []string{"http://h", "http://h"} },
		"bad ttl":         func(c *FileConfig) { c.TTL = "soon" },
		"negative ttl":    func(c *FileConfig) { c.TTL = "-1h" },
		"bad timeout":     func(c *FileConfig) { c.FetchTimeout = "0s" },
		"no identity":     func(c *FileConfig) { c.IdentityFile = "" },
		"missing key":     func(c *FileConfig) { c.IdentityFile = "nope.key" },
		"payload too big": func(c *FileConfig) { c.MaxPayloadBytes = HardMaxPayload + 1 },
	}
	for name, mod := range cases {
		c := base()
		mod(&c)
		path := filepath.Join(dir, "c.json")
		writeJSON(t, path, c)
		fc, err := ReadConfigFile(path)
		if err == nil {
			var cfg Config
			if cfg, err = fc.NodeConfig(""); err == nil {
				_, err = New(cfg)
			}
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, raw := range map[string]string{
		"unknown field": `{"version":1,"vault":"` + testVaultHex + `","signers":[],"stores":[],"relays":[]}`,
		"trailing data": `{"version":1} {}`,
		"not json":      `version = 1`,
	} {
		if _, err := ParseConfig([]byte(raw)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}
