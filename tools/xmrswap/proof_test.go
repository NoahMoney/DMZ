package xmrswap

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a stand-in proof-xmr: run with "--fake-prover [mode]" as its first argument it
// speaks the CLI contract of §3.B (keygen / prove - / verify, the exit codes, JSON on stdout only) using this
// package's own key arithmetic and a "proof" that is a hash of the statement. It tests the wrapper — argument
// passing, stdin, parsing, exit-code mapping, the statement check — not the proof, which cargo test covers.
// Mode "lying" answers with a public key that is not the secret's; mode "slow" never answers verify.
//
// A pinned prover (NewPinnedProver) is run as its path alone, so it cannot be handed a leading argument: with
// XMRSWAP_FAKE_PROVER set, the binary answers verify in that mode instead (pinnedFakeMain).
func TestMain(m *testing.M) {
	// copies of this binary named wake-child and wake stand in for the companion a wake starts and for a program planted
	// where a command line split at a space would find one (wake_test.go); either is started by the OS, with no
	// environment of this test's
	if exe, err := os.Executable(); err == nil {
		switch strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe") {
		case "wake-child":
			os.Exit(wakeChildMain(exe))
		case "wake":
			os.Exit(wakePlantedMain(exe))
		}
	}
	// a copy of this binary named wsl.exe stands in for WSL's launcher (proof_wsl_test.go)
	if root := os.Getenv("XMRSWAP_FAKE_WSL_ROOT"); root != "" {
		os.Exit(fakeWSLMain(root, os.Args[1:]))
	}
	if mode := os.Getenv("XMRSWAP_FAKE_PROVER"); mode != "" {
		os.Exit(pinnedFakeMain(mode, os.Args[1:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "--fake-prover" {
		mode, args := "", os.Args[2:]
		if len(args) > 0 && (args[0] == "lying" || args[0] == "slow") {
			mode, args = args[0], args[1:]
		}
		os.Exit(fakeProverMain(mode, args))
	}
	os.Exit(m.Run())
}

// pinnedFakeMain answers `verify <h> <s> <proof>` with exit 0 and, by mode: a verdict for another statement, a
// verdict for the one asked about, output that is not a verdict, or nothing (the pinned build's own behaviour).
func pinnedFakeMain(mode string, args []string) int {
	if len(args) != 4 || args[0] != "verify" {
		fmt.Fprintln(os.Stderr, "usage: verify <h> <s> <proof>")
		return 2
	}
	switch mode {
	case "wrong-verdict":
		fmt.Printf(`{"valid":true,"h":"%s","s":"%s"}`+"\n", strings.Repeat("00", 32), strings.Repeat("11", 32))
	case "right-verdict":
		fmt.Printf(`{"valid":true,"h":"%s","s":"%s"}`+"\n", args[1], args[2])
	case "not-a-verdict":
		fmt.Println("ok")
	case "silent":
	default:
		return 2
	}
	return 0
}

func fakeProof(h, s string) string {
	sum := sha256.Sum256([]byte("fake proof of " + h + " " + s))
	return hex.EncodeToString(sum[:])
}

func fakeProverMain(mode string, args []string) int {
	refuse := func(why string) int { fmt.Fprintln(os.Stderr, why); return 2 }
	statement := func(secret Scalar) (string, string) {
		s := secret.Public()
		if mode == "lying" {
			other, _ := NewScalar(rand.Reader)
			s = other.Public()
		}
		return SHA256Hex(secret[:]), s.Hex()
	}
	switch {
	case len(args) == 1 && args[0] == "keygen":
		secret, _ := NewScalar(rand.Reader)
		h, s := statement(secret)
		fmt.Printf(`{"secret":"%s","h":"%s","s":"%s"}`+"\n", secret.Hex(), h, s)
		return 0
	case len(args) == 2 && args[0] == "prove":
		in := args[1]
		if in == "-" {
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			in = strings.TrimSpace(line)
		}
		secret, err := ParseScalar(in)
		if errors.Is(err, ErrNotReduced) {
			return refuse("secret: not reduced mod l")
		}
		if err != nil {
			return refuse("secret: " + err.Error())
		}
		h, s := statement(secret)
		fmt.Printf(`{"h":"%s","s":"%s","proof":"%s"}`+"\n", h, s, fakeProof(h, s))
		return 0
	case len(args) == 4 && args[0] == "verify":
		if mode == "slow" {
			time.Sleep(30 * time.Second)
		}
		h, s, proof := args[1], args[2], args[3]
		if _, err := hex.DecodeString(h); err != nil || len(h) != 64 {
			return refuse("h: expected 64 hex characters")
		}
		if proof == fakeProof(h, s) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "r1cs: the committed value does not hash to H")
		return 1
	}
	return refuse("usage: proof-xmr keygen | prove <secret_hex|-> | verify <h_hex> <s_hex> <proof_hex>")
}

func fakeProver(t *testing.T, mode ...string) Prover {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return NewProverCLI(append([]string{exe, "--fake-prover"}, mode...))
}

func TestProverCLIRoundTrip(t *testing.T) {
	p := fakeProver(t)
	ctx := context.Background()
	secret, h, s, err := p.Keygen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if SHA256Hex(secret[:]) != h || secret.Public() != s {
		t.Fatal("Keygen returned an inconsistent statement")
	}
	h2, s2, proof, err := p.Prove(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if h2 != h || s2 != s || len(proof) != 32 {
		t.Fatalf("Prove: %s %s %d bytes", h2, s2, len(proof))
	}
	ok, err := p.Verify(ctx, h, s, proof)
	if err != nil || !ok {
		t.Fatalf("Verify: %v %v", ok, err)
	}
	// the same proof against another statement is invalid, not an error
	flipped := "f" + h[1:]
	if h[0] == 'f' {
		flipped = "0" + h[1:]
	}
	ok, err = p.Verify(ctx, flipped, s, proof)
	if err != nil || ok {
		t.Fatalf("Verify with a flipped h: %v %v", ok, err)
	}
	ok, err = p.Verify(ctx, strings.ToUpper(h), s, proof)
	if err != nil || !ok {
		t.Fatalf("Verify with upper-case h: %v %v", ok, err)
	}
	other, _ := NewScalar(rand.Reader)
	ok, err = p.Verify(ctx, h, other.Public(), proof)
	if err != nil || ok {
		t.Fatalf("Verify with another s: %v %v", ok, err)
	}
	if _, err := p.Verify(ctx, "not hex", s, proof); err == nil {
		t.Error("Verify with garbage h did not error")
	}
}

func TestProverCLIRefusesUnreducedSecret(t *testing.T) {
	var unreduced Scalar
	b, _ := hex.DecodeString(hexL)
	copy(unreduced[:], b)
	_, _, _, err := fakeProver(t).Prove(context.Background(), unreduced)
	if err == nil {
		t.Fatal("an unreduced secret was proven")
	}
	if !strings.Contains(err.Error(), "exit 2") || strings.Contains(err.Error(), hexL) {
		t.Errorf("error: %v", err)
	}
}

func TestProverCLIChecksTheStatement(t *testing.T) {
	p := fakeProver(t, "lying")
	ctx := context.Background()
	if _, _, _, err := p.Keygen(ctx); err == nil || !strings.Contains(err.Error(), "not the secret's public key") {
		t.Errorf("Keygen from a lying tool: %v", err)
	}
	secret, _ := NewScalar(rand.Reader)
	if _, _, _, err := p.Prove(ctx, secret); err == nil || !strings.Contains(err.Error(), "not the secret's public key") {
		t.Errorf("Prove from a lying tool: %v", err)
	}
}

func TestProverCLIFailures(t *testing.T) {
	ctx := context.Background()
	if _, _, _, err := NewProverCLI(nil).Keygen(ctx); err == nil {
		t.Error("empty command did not error")
	}
	if _, _, _, err := NewProverCLI([]string{filepath.Join(t.TempDir(), "no-such-proof-xmr")}).Keygen(ctx); err == nil {
		t.Error("missing binary did not error")
	}
	if _, err := fakeProver(t, "slow").Verify(ctx, "", Point{}, nil); err == nil {
		t.Error("a bad h did not error before running anything")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	secret, _ := NewScalar(rand.Reader)
	_, err := fakeProver(t, "slow").Verify(ctx, SHA256Hex(secret[:]), secret.Public(), []byte{1})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("slow verify: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("Verify outlived its context")
	}
}

// realProver is the proof-xmr build in WSL (or XMRSWAP_PROVER, a command line), if it answers keygen.
func realProver(t *testing.T) Prover {
	t.Helper()
	if testing.Short() {
		t.Skip("-short")
	}
	// the real prover is named by XMRSWAP_PROVER (a command line, e.g. "wsl.exe -e <path to proof-xmr>") or found as
	// proof-xmr on PATH; with neither, the test says loudly that it did not run rather than pass by absence
	cmd := strings.Fields(os.Getenv("XMRSWAP_PROVER"))
	if len(cmd) == 0 {
		if _, err := exec.LookPath("proof-xmr"); err != nil {
			t.Skip("THE REAL-PROVER TEST DID NOT RUN: set XMRSWAP_PROVER to the proof-xmr command, or put proof-xmr on PATH")
		}
		cmd = []string{"proof-xmr"}
	}
	p := NewProverCLI(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, _, _, err := p.Keygen(ctx); err != nil {
		t.Skipf("proof-xmr CLI not usable (%v): %v", cmd, err)
	}
	return p
}

// TestProofXMRBinary runs the real tool end to end: keygen, prove (about 15 s), verify, and a verify that
// must fail. It is the check that the wrapper's contract and agent B's binary agree.
func TestProofXMRBinary(t *testing.T) {
	p := realProver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	secret, h, s, err := p.Keygen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	h2, s2, proof, err := p.Prove(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("prove: %v, %d proof bytes", time.Since(start).Round(time.Millisecond), len(proof))
	if h2 != h || s2 != s {
		t.Fatal("prove and keygen disagree on the statement")
	}
	start = time.Now()
	ok, err := p.Verify(ctx, h, s, proof)
	t.Logf("verify: %v", time.Since(start).Round(time.Millisecond))
	if err != nil || !ok {
		t.Fatalf("Verify: %v %v", ok, err)
	}
	flipped := fmt.Sprintf("%02x", (proof[len(proof)-1] ^ 1))
	bad, _ := hex.DecodeString(hex.EncodeToString(proof[:len(proof)-1]) + flipped)
	if ok, err := p.Verify(ctx, h, s, bad); err != nil || ok {
		t.Fatalf("Verify of a tampered proof: %v %v", ok, err)
	}
	var unreduced Scalar
	b, _ := hex.DecodeString(hexL)
	copy(unreduced[:], b)
	if _, _, _, err := p.Prove(ctx, unreduced); err == nil || !strings.Contains(err.Error(), "exit 2") {
		t.Errorf("the real tool proved l: %v", err)
	}
}

// TestProofXMRVector reads the worked vector record.sh writes (secret, h, s, proof; one per line) and checks
// this package's key math against curve25519-dalek's, and the wrapper against the recorded proof.
func TestProofXMRVector(t *testing.T) {
	path := filepath.Join("..", "..", "research", "crosschain", "poc", "proof-xmr", "vector.txt")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no worked vector yet: %v", err)
	}
	lines := strings.Fields(string(raw))
	if len(lines) != 4 {
		t.Fatalf("vector.txt has %d fields, want 4", len(lines))
	}
	secret, err := ParseScalar(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	if SHA256Hex(secret[:]) != lines[1] {
		t.Errorf("h: %s != %s", SHA256Hex(secret[:]), lines[1])
	}
	if secret.Public().Hex() != lines[2] {
		t.Errorf("s: %s != %s", secret.Public().Hex(), lines[2])
	}
	proof, err := hex.DecodeString(lines[3])
	if err != nil {
		t.Fatal(err)
	}
	p := realProver(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ok, err := p.Verify(ctx, lines[1], secret.Public(), proof)
	if err != nil || !ok {
		t.Fatalf("recorded proof: %v %v", ok, err)
	}
}
