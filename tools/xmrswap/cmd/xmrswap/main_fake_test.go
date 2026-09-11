package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheCompanionNeverUsesTheFakeWallet (GAP-037): xmrswap.FakeWallet, the in-memory monero-wallet-rpc, lives in the
// production package because the package's own tests, tools/simhtlc and tools/regtest/xmrshape all use it, and
// moving it to a package of its own would be an import cycle with those tests. What must never happen is that the
// companion itself wires it: no non-test file of this command may name it.
func TestTheCompanionNeverUsesTheFakeWallet(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"FakeWallet", "NewFakeWallet", "NewProverCLI"} {
			if strings.Contains(string(b), name) {
				t.Fatalf("%s names %s: the companion runs a real monero-wallet-rpc and the pinned prover, never a stand-in", f, name)
			}
		}
	}
}
