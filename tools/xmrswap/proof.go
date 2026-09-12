package xmrswap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Prover is the setup proof (docs/XMR_SWAP_BUILD.md §3.B): "I know x with SHA256(x) = h and x·G = s". Each party
// proves it for their own share before anything is locked and verifies the other's (§4 invariants 3 and 4).
type Prover interface {
	Keygen(ctx context.Context) (secret Scalar, h string, s Point, err error)
	Prove(ctx context.Context, secret Scalar) (h string, s Point, proof []byte, err error)
	// Verify is (true, nil) for a valid proof, (false, nil) for one the tool rejects, and an error only when
	// the tool could not be run or answered outside its contract.
	Verify(ctx context.Context, h string, s Point, proof []byte) (bool, error)
}

// NewProverCLI wraps the proof-xmr binary (research/crosschain/poc/proof-xmr): command is the program and any
// leading arguments, e.g. {"proof-xmr"} for a build on PATH, or {"wsl.exe", "-e", "<path to proof-xmr>"} for one
// built inside WSL.
//
// The tool's contract: `keygen` prints {"secret","h","s"}; `prove -` reads the secret's hex from stdin and prints
// {"h","s","proof"}; `verify <h> <s> <proof>` exits 0 for valid, 1 for invalid with one line why on stderr; exit
// 2 is a refused invocation (usage, non-hex, an unreduced secret). Only stdout carries JSON. The secret goes
// over stdin, never argv, so it is not in the process list.
//
// NewProverCLI runs whatever the command names and pins nothing: it is for a driver on the simulator or a test.
// The companion runs the prover through NewPinnedProver.
func NewProverCLI(command []string) Prover {
	return &proverCLI{command: append([]string(nil), command...)}
}

// NewPinnedProver is the prover the companion runs (GAP-003): path must be absolute — a bare name is whatever PATH
// finds first — and the file there must hash to wantSHA256 (ProverSHA256), now and before every later run, so a
// binary replaced while the companion runs is refused rather than trusted. It is run directly, with no launcher
// in front of it. A prover inside WSL is NewPinnedWSLProver's (X-17): it hashes the binary in the distribution, not
// the wsl.exe that runs it, which is what hashing a command's first program would do.
func NewPinnedProver(path, wantSHA256 string) (Prover, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("proof-xmr: %q is not an absolute path: a bare name is whatever program PATH finds first", path)
	}
	p := &proverCLI{command: []string{path}, pin: strings.ToLower(wantSHA256),
		pinned: func() (string, []byte, error) { b, err := os.ReadFile(path); return path, b, err }}
	if err := p.checkPin(); err != nil {
		return nil, err
	}
	return p, nil
}

type proverCLI struct {
	command []string
	// pin, when set, is the SHA-256 the prover's binary must hash to before each run
	pin string
	// pinned reads that binary and names it: the file at command[0] for a native prover, the file inside the WSL
	// distribution for one that wsl.exe runs (X-17)
	pinned func() (name string, b []byte, err error)
}

// checkPin hashes the binary and compares it with the pin.
func (p *proverCLI) checkPin() error {
	if p.pin == "" {
		return nil
	}
	name, b, err := p.pinned()
	if err != nil {
		return fmt.Errorf("proof-xmr: %w", err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != p.pin {
		return fmt.Errorf("proof-xmr: %s hashes to sha256 %s, and the companion runs only the pinned build %s", name, got, p.pin)
	}
	return nil
}

// run executes the tool with args, feeding stdin, and returns stdout, the exit status (-1 when it did not run
// to an exit status) and the first line of stderr.
func (p *proverCLI) run(ctx context.Context, stdin []byte, args ...string) (stdout []byte, exit int, stderr string, err error) {
	if len(p.command) == 0 {
		return nil, -1, "", errors.New("proof-xmr: empty command")
	}
	// The binary is hashed, then run by name: by its path for a native prover, by wsl.exe inside the distribution for
	// one in WSL (X-17). The two paths are alike in the gap between the hash and the run: a file replaced inside it is
	// not caught on either. Closing it would take running the very bytes that were hashed (a private copy, or an open
	// handle), which neither does. The gap is the time a process takes to start, and replacing the binary needs write
	// access to it, which would already have let it be replaced before the first hash.
	if err := p.checkPin(); err != nil {
		return nil, -1, "", err
	}
	// bounded like every other answer the companion reads: a deadline when the caller gave none, and output past
	// a cap ends the call instead of filling memory (a proof is about 3 KB of hex)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, proverTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, p.command[0], append(append([]string(nil), p.command[1:]...), args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, errb := &capBuffer{max: maxProverOutput}, &capBuffer{max: maxProverOutput}
	cmd.Stdout, cmd.Stderr = out, errb
	err = cmd.Run()
	if out.over || errb.over {
		return nil, -1, "", fmt.Errorf("proof-xmr %s: output longer than %d bytes refused", args[0], maxProverOutput)
	}
	stderr = strings.TrimSpace(errb.String())
	if i := strings.IndexByte(stderr, '\n'); i >= 0 {
		stderr = stderr[:i]
	}
	if len(stderr) > 300 {
		stderr = stderr[:300]
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out.Bytes(), 0, stderr, nil
	case errors.As(err, &exitErr):
		if ctx.Err() != nil {
			return nil, -1, stderr, fmt.Errorf("proof-xmr %s: %w", args[0], ctx.Err())
		}
		return out.Bytes(), exitErr.ExitCode(), stderr, nil
	default:
		return nil, -1, stderr, fmt.Errorf("proof-xmr %s: %w", args[0], err)
	}
}

const (
	proverTimeout   = 5 * time.Minute // a proof takes about ten seconds (run.log); a tool that takes minutes is not answering
	maxProverOutput = 1 << 20
)

// capBuffer keeps at most max bytes and fails the write that would pass it, which ends the copy exec runs. The
// buffer is a field and not embedded: an embedded bytes.Buffer brings its ReadFrom along, io.Copy prefers
// ReadFrom to Write, and the cap would never be consulted.
type capBuffer struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.max {
		c.over = true
		return 0, errors.New("output cap reached")
	}
	return c.buf.Write(p)
}

func (c *capBuffer) Bytes() []byte  { return c.buf.Bytes() }
func (c *capBuffer) String() string { return c.buf.String() }

func (p *proverCLI) Keygen(ctx context.Context) (Scalar, string, Point, error) {
	out, exit, stderr, err := p.run(ctx, nil, "keygen")
	if err != nil {
		return Scalar{}, "", Point{}, err
	}
	if exit != 0 {
		return Scalar{}, "", Point{}, fmt.Errorf("proof-xmr keygen: exit %d: %s", exit, stderr)
	}
	var r struct {
		Secret string `json:"secret"`
		H      string `json:"h"`
		S      string `json:"s"`
	}
	// stdout holds the secret: a decode failure is reported without it
	if err := json.Unmarshal(out, &r); err != nil {
		return Scalar{}, "", Point{}, errors.New("proof-xmr keygen: output is not the expected JSON")
	}
	secret, err := ParseScalar(r.Secret)
	if err != nil {
		return Scalar{}, "", Point{}, fmt.Errorf("proof-xmr keygen: %w", err)
	}
	h, s, err := checkStatement(secret, r.H, r.S)
	if err != nil {
		return Scalar{}, "", Point{}, fmt.Errorf("proof-xmr keygen: %w", err)
	}
	return secret, h, s, nil
}

func (p *proverCLI) Prove(ctx context.Context, secret Scalar) (string, Point, []byte, error) {
	out, exit, stderr, err := p.run(ctx, []byte(secret.Hex()+"\n"), "prove", "-")
	if err != nil {
		return "", Point{}, nil, err
	}
	if exit != 0 {
		// the tool promises the secret never appears in its messages; this is the check that keeps a future
		// tool's slip out of an error a caller might log
		if strings.Contains(strings.ToLower(stderr), secret.Hex()) {
			stderr = "(stderr withheld)"
		}
		return "", Point{}, nil, fmt.Errorf("proof-xmr prove: exit %d: %s", exit, stderr)
	}
	var r struct {
		H     string `json:"h"`
		S     string `json:"s"`
		Proof string `json:"proof"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return "", Point{}, nil, errors.New("proof-xmr prove: output is not the expected JSON")
	}
	h, s, err := checkStatement(secret, r.H, r.S)
	if err != nil {
		return "", Point{}, nil, fmt.Errorf("proof-xmr prove: %w", err)
	}
	proof, err := hex.DecodeString(r.Proof)
	if err != nil || len(proof) == 0 {
		return "", Point{}, nil, errors.New("proof-xmr prove: proof is not hex")
	}
	return h, s, proof, nil
}

func (p *proverCLI) Verify(ctx context.Context, h string, s Point, proof []byte) (bool, error) {
	if _, err := hex32(h); err != nil {
		return false, fmt.Errorf("proof-xmr verify: h: %w", err)
	}
	out, exit, stderr, err := p.run(ctx, nil, "verify", strings.ToLower(h), s.Hex(), hex.EncodeToString(proof))
	if err != nil {
		return false, err
	}
	switch exit {
	case 0:
		if err := checkVerdict(out, h, s); err != nil {
			return false, err
		}
		return true, nil
	case 1:
		return false, nil
	}
	return false, fmt.Errorf("proof-xmr verify: exit %d: %s", exit, stderr)
}

// checkVerdict is GAP-003's statement binding. A verify that prints anything on stdout must print exactly one
// verdict, {"valid":true,"h":<hex>,"s":<hex>}, for the very statement it was asked about; a verdict for another H
// or S, or output that is not a verdict, is refused whatever the exit status. The pinned build prints nothing and
// its exit status is its verdict: printing the verdict is a change to research/crosschain/poc/proof-xmr, after
// which silence can be refused as well.
func checkVerdict(out []byte, h string, s Point) error {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil
	}
	var v struct {
		Valid bool   `json:"valid"`
		H     string `json:"h"`
		S     string `json:"s"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return errors.New("proof-xmr verify: exit 0 with output that is not a verdict")
	}
	if !v.Valid || !strings.EqualFold(v.H, h) || !strings.EqualFold(v.S, s.Hex()) {
		return fmt.Errorf("proof-xmr verify: exit 0 with a verdict for another statement (valid %v, h %s, s %s)", v.Valid, v.H, v.S)
	}
	return nil
}

// checkStatement is the companion's own check of the tool's statement — h is SHA-256 of the secret's exact
// bytes and s is its public key — so a tool that is not the one this package was written against cannot
// hand the state machine an H the escrow will never match or an S the joint key is not made of.
func checkStatement(secret Scalar, h, s string) (string, Point, error) {
	h = strings.ToLower(h)
	if want := SHA256Hex(secret[:]); h != want {
		return "", Point{}, errors.New("h is not SHA-256 of the secret")
	}
	pt, err := ParsePoint(s)
	if err != nil {
		return "", Point{}, fmt.Errorf("s: %w", err)
	}
	if pt != secret.Public() {
		return "", Point{}, errors.New("s is not the secret's public key")
	}
	return h, pt, nil
}
