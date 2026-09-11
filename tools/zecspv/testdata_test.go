package zecspv

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

// headerFile is a run of real headers in testdata, with where they came from (see its "source" and "pins").
type headerFile struct {
	path        string
	Network     string `json:"network"`
	Source      string `json:"source"`
	FirstHeight uint32 `json:"first_height"`
	Pins        []struct {
		Height uint32 `json:"height"`
		Hash   string `json:"hash"`
		Source string `json:"source"`
	} `json:"pins"`
	Headers []string `json:"headers"`
	Blocks  []struct {
		Height          uint32   `json:"height"`
		Hash            string   `json:"hash"`
		CoinbaseVersion string   `json:"coinbase_version"`
		TxIDs           []string `json:"txids"`
	} `json:"blocks"`
}

func loadHeaderFiles(t testing.TB) []*headerFile {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "*net-*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no header files in testdata (%v)", err)
	}
	var out []*headerFile
	for _, p := range paths {
		out = append(out, loadHeaderFile(t, filepath.Base(p)))
	}
	return out
}

func loadHeaderFile(t testing.TB, name string) *headerFile {
	t.Helper()
	p := filepath.Join("testdata", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	f := &headerFile{path: p}
	if err := json.Unmarshal(b, f); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return f
}

func (f *headerFile) params(t testing.TB) *Params {
	switch f.Network {
	case "mainnet":
		return Mainnet
	case "testnet":
		return Testnet
	}
	t.Fatalf("%s: network %q", f.path, f.Network)
	return nil
}

func (f *headerFile) raw(t testing.TB) [][]byte {
	t.Helper()
	out := make([][]byte, len(f.Headers))
	for i, s := range f.Headers {
		out[i] = mustHex(t, s)
	}
	return out
}

func (f *headerFile) headers(t testing.TB) []*Header {
	t.Helper()
	var out []*Header
	for _, b := range f.raw(t) {
		h, err := ParseHeader(b)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h)
	}
	return out
}

func (f *headerFile) index(height uint32) int { return int(height - f.FirstHeight) }

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func leavesOf(t testing.TB, txids []string) []Hash {
	t.Helper()
	out := make([]Hash, len(txids))
	for i, s := range txids {
		h, err := ParseHash(s)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = h
	}
	return out
}

func cloneRaw(raw [][]byte) [][]byte {
	out := make([][]byte, len(raw))
	for i, b := range raw {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

func setU32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }
func getU32(b []byte, off int) uint32    { return binary.LittleEndian.Uint32(b[off:]) }

// field offsets in a serialized header (§7.6)
const (
	offVersion = 0
	offTime    = 100
	offBits    = 104
	offNonce   = 108
	offSol     = 143
)

func mustTarget(t testing.TB, bits uint32) *big.Int {
	t.Helper()
	v, neg, over := CompactToTarget(bits)
	if neg || over {
		t.Fatalf("bits %08x", bits)
	}
	return v
}

// wantHeaderErr requires err to be a *HeaderError at height wrapping one of want.
func wantHeaderErr(t testing.TB, err error, height uint32, want ...error) {
	t.Helper()
	var he *HeaderError
	if !errors.As(err, &he) {
		t.Fatalf("got %v, want a HeaderError at %d", err, height)
	}
	if he.Height != height {
		t.Fatalf("failed at height %d (%v), want %d", he.Height, err, height)
	}
	for _, w := range want {
		if errors.Is(err, w) {
			return
		}
	}
	t.Fatalf("got %v, want one of %v", err, want)
}
