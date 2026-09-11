package xmrswap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// selfCopy copies the running test binary into a temporary directory under the prover's name, so a test can pin
// it, and alter it, without touching the binary that is running. It answers verify as TestMain's
// XMRSWAP_FAKE_PROVER mode says.
func selfCopy(t *testing.T) (path, sum string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	name := "proof-xmr"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path = filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b, 0o700); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return path, hex.EncodeToString(h[:])
}

func statementForTest(t *testing.T) (string, Point) {
	t.Helper()
	sec, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return SHA256Hex(sec[:]), sec.Public()
}

// TestPinnedProverRefusesAnotherBinary (GAP-003): the companion runs one prover binary. A bare name is refused (PATH
// would choose it), a binary that does not hash to the pin is refused, and the pinned binary replaced after the
// companion started is refused at its next call, before it runs.
func TestPinnedProverRefusesAnotherBinary(t *testing.T) {
	ctx := context.Background()
	if _, err := NewPinnedProver("proof-xmr", ProverSHA256); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("a bare name was accepted: %v", err)
	}
	path, sum := selfCopy(t)
	if _, err := NewPinnedProver(path, ProverSHA256); err == nil || !strings.Contains(err.Error(), "pinned build") {
		t.Fatalf("a binary that is not the pinned build was accepted: %v", err)
	}
	p, err := NewPinnedProver(path, sum)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XMRSWAP_FAKE_PROVER", "silent")
	h, s := statementForTest(t)
	if ok, err := p.Verify(ctx, h, s, []byte{1}); err != nil || !ok {
		t.Fatalf("the pinned binary itself: %v %v", ok, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("x"))
	f.Close()
	if ok, err := p.Verify(ctx, h, s, []byte{1}); err == nil || ok || !strings.Contains(err.Error(), "pinned build") {
		t.Fatalf("a binary changed after the start was run: %v %v", ok, err)
	}
}

// TestPinnedProverRefusesAVerdictForAnotherStatement (GAP-003): exit 0 is not enough when the tool says what it
// verified. A verdict for another H and S is refused, and so is output that is not a verdict; a verdict for the very
// statement asked about is accepted, and so is silence, which is what the pinned build prints today.
func TestPinnedProverRefusesAVerdictForAnotherStatement(t *testing.T) {
	ctx := context.Background()
	path, sum := selfCopy(t)
	p, err := NewPinnedProver(path, sum)
	if err != nil {
		t.Fatal(err)
	}
	h, s := statementForTest(t)
	for _, c := range []struct {
		mode string
		ok   bool
	}{{"wrong-verdict", false}, {"not-a-verdict", false}, {"right-verdict", true}, {"silent", true}} {
		t.Setenv("XMRSWAP_FAKE_PROVER", c.mode)
		ok, err := p.Verify(ctx, h, s, []byte{1})
		if (ok && err == nil) != c.ok {
			t.Fatalf("%s: ok %v err %v, want accepted=%v", c.mode, ok, err, c.ok)
		}
	}
}
