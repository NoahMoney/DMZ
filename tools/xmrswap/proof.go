package xmrswap

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
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
// leading arguments, e.g. {"wsl.exe", "-e", "/home/wilma/target/proof-xmr/release/proof-xmr"} or {"proof-xmr"}.
//
// The tool's contract: `keygen` prints {"secret","h","s"}; `prove -` reads the secret's hex from stdin and prints
// {"h","s","proof"}; `verify <h> <s> <proof>` exits 0 for valid, 1 for invalid with one line why on stderr; exit
// 2 is a refused invocation (usage, non-hex, an unreduced secret). Only stdout carries JSON. The secret goes
// over stdin, never argv, so it is not in the process list.
func NewProverCLI(command []string) Prover {
	return &proverCLI{command: append([]string(nil), command...)}
}

type proverCLI struct{ command []string }

// run executes the tool with args, feeding stdin, and returns stdout, the exit status (-1 when it did not run
// to an exit status) and the first line of stderr.
func (p *proverCLI) run(ctx context.Context, stdin []byte, args ...string) (stdout []byte, exit int, stderr string, err error) {
	if len(p.command) == 0 {
		return nil, -1, "", errors.New("proof-xmr: empty command")
	}
	cmd := exec.CommandContext(ctx, p.command[0], append(append([]string(nil), p.command[1:]...), args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
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
	_, exit, stderr, err := p.run(ctx, nil, "verify", strings.ToLower(h), s.Hex(), hex.EncodeToString(proof))
	if err != nil {
		return false, err
	}
	switch exit {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, fmt.Errorf("proof-xmr verify: exit %d: %s", exit, stderr)
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
