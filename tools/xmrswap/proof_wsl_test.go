package xmrswap

// X-17: the pinned prover inside WSL, with a stand-in wsl.exe and a stand-in share.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeWSLMain stands in for wsl.exe: `-d <distribution> -e <path> <args>...` runs the file at
// <root>\<distribution>\<path> with the args, stdin and stdout passed through, and exits with its status. Each call is a
// line in XMRSWAP_FAKE_WSL_LOG. The file it runs does not see XMRSWAP_FAKE_WSL_*, so it answers as a prover
// (XMRSWAP_FAKE_PROVER), not as another wsl.exe.
func fakeWSLMain(root string, args []string) int {
	if log := os.Getenv("XMRSWAP_FAKE_WSL_LOG"); log != "" {
		if f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, strings.Join(args, " "))
			f.Close()
		}
	}
	if len(args) < 4 || args[0] != "-d" || args[2] != "-e" {
		fmt.Fprintln(os.Stderr, "fake wsl.exe: want -d <distribution> -e <path> ...")
		return 1
	}
	cmd := exec.Command(root+`\`+args[1]+strings.ReplaceAll(args[3], "/", `\`), args[4:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	for _, e := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(e), "XMRSWAP_FAKE_WSL_") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "fake wsl.exe:", err)
		return 1
	}
	return 0
}

// copyTestBinary writes this test binary to dir/name with extra appended and returns its path and SHA-256. A PE file
// runs with bytes after its last section, so extra gives a copy a hash of its own.
func copyTestBinary(t *testing.T, dir, name string, extra []byte) (string, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, extra...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return p, hex.EncodeToString(sum[:])
}

// fakeWSL is a stand-in WSL. It has two share roots: the first reads nothing, and the second holds the distribution
// FakeDistro with a prover at /opt/dmz/proof-xmr.exe. The file has an extension only because the stand-in wsl.exe runs
// it with CreateProcess. A copy of this binary named wsl.exe stands in as the system's, and FakeDistro is the default.
// The prover copy's hash differs from the launcher's, so a test can tell which of the two was hashed.
type fakeWSL struct {
	launcher, launcherSum string
	prover, proverSum     string
	roots                 []string
	log                   string
}

const fakeProverPath = "/opt/dmz/proof-xmr.exe"

func newFakeWSL(t *testing.T) *fakeWSL {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("a prover run through wsl.exe is Windows'")
	}
	dir := t.TempDir()
	share := filepath.Join(dir, "share")
	f := &fakeWSL{roots: []string{filepath.Join(dir, "unreadable-share"), share}, log: filepath.Join(dir, "wsl-calls.log")}
	f.launcher, f.launcherSum = copyTestBinary(t, filepath.Join(dir, "System32"), "wsl.exe", nil)
	f.prover, f.proverSum = copyTestBinary(t, filepath.Join(share, "FakeDistro", "opt", "dmz"), "proof-xmr.exe", []byte("the build this test pins"))
	roots, launcher, def := wslShareRoots, wslLauncher, wslDefaultDistro
	t.Cleanup(func() { wslShareRoots, wslLauncher, wslDefaultDistro = roots, launcher, def })
	wslShareRoots = f.roots
	wslLauncher = func() (string, error) { return f.launcher, nil }
	wslDefaultDistro = func() (string, error) { return "FakeDistro", nil }
	t.Setenv("XMRSWAP_FAKE_WSL_ROOT", share)
	t.Setenv("XMRSWAP_FAKE_WSL_LOG", f.log)
	t.Setenv("XMRSWAP_FAKE_PROVER", "silent")
	return f
}

// calls is every command line the stand-in wsl.exe was run with.
func (f *fakeWSL) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// TestAWSLProverCommandIsParsedStrictly (X-17): the one form accepted is `wsl.exe [-d <distribution>] -e <absolute
// path>`. There is no shell, no other option and nothing after the path, and the path must be clean and use only
// characters WSL's share names as wsl.exe does.
func TestAWSLProverCommandIsParsedStrictly(t *testing.T) {
	for _, c := range []struct {
		cmd, distro, path string
		ok                bool
	}{
		{"wsl.exe -e /opt/dmz/proof-xmr", "", "/opt/dmz/proof-xmr", true},
		{"wsl.exe -d Ubuntu-24.04 -e /opt/dmz/target/release/proof-xmr", "Ubuntu-24.04", "/opt/dmz/target/release/proof-xmr", true},
		{"wsl --distribution Debian --exec /usr/local/bin/proof-xmr", "Debian", "/usr/local/bin/proof-xmr", true},
		{`C:\Windows\System32\wsl.exe -e /opt/proof-xmr`, "", "/opt/proof-xmr", true}, // which wsl.exe is checked when the prover is made
		{"wsl.exe /opt/proof-xmr", "", "", false},                                     // no -e: a shell would find and run it
		{"wsl.exe bash -c /opt/proof-xmr", "", "", false},
		{"wsl.exe -e proof-xmr", "", "", false}, // not absolute
		{"wsl.exe -e /", "", "", false},
		{"wsl.exe -e /opt/../bin/proof-xmr", "", "", false},
		{"wsl.exe -e /opt//proof-xmr", "", "", false},
		{"wsl.exe -e /opt/proof-xmr/", "", "", false},
		{"wsl.exe -e /opt/proof:xmr", "", "", false}, // the share shows ':' as another character
		{"wsl.exe -e /opt/proof xmr", "", "", false},
		{"wsl.exe -e /opt/proof-xmr verify", "", "", false}, // the companion adds the verb itself
		{"wsl.exe -u root -e /opt/proof-xmr", "", "", false},
		{"wsl.exe -e /opt/proof-xmr -d Ubuntu", "", "", false},
		{"wsl.exe -d Ubuntu -- /opt/proof-xmr", "", "", false},
		{"wsl.exe -d Ubu;ntu -e /opt/proof-xmr", "", "", false},
		{"wsl.exe", "", "", false},
		{"/opt/proof-xmr", "", "", false},
	} {
		_, d, p, err := ParseWSLProverCommand(strings.Fields(c.cmd))
		if (err == nil) != c.ok || (c.ok && (d != c.distro || p != c.path)) {
			t.Fatalf("%q: distribution %q path %q err %v; want accepted=%v", c.cmd, d, p, err, c.ok)
		}
	}
	for v, want := range map[string]bool{"wsl.exe -e /x": true, "WSL.EXE -e /x": true, `C:\Windows\System32\wsl.exe -e /x`: true,
		`C:\dmz\proof-xmr.exe`: false, "/opt/proof-xmr": false, "": false} {
		if IsWSLProverCommand(v) != want {
			t.Fatalf("IsWSLProverCommand(%q) = %v", v, !want)
		}
	}
}

// TestPinnedWSLProverHashesTheBinaryNotTheLauncher (X-17): the pin is checked on the prover inside the distribution,
// read through the share, and not on wsl.exe. A command that names no distribution runs in the default one, read
// once and named on every run. A binary replaced after the start is refused at its next call, before anything runs.
func TestPinnedWSLProverHashesTheBinaryNotTheLauncher(t *testing.T) {
	ctx := context.Background()
	f := newFakeWSL(t)
	cmd := []string{"wsl.exe", "-e", fakeProverPath}
	if _, err := NewPinnedWSLProver(cmd, f.launcherSum); err == nil || !strings.Contains(err.Error(), "pinned build") {
		t.Fatalf("pinning the launcher's hash was accepted: %v", err)
	}
	p, err := NewPinnedWSLProver(cmd, f.proverSum)
	if err != nil {
		t.Fatalf("the pinned binary in the distribution (read through the second root, the first reading nothing): %v", err)
	}
	h, s := statementForTest(t)
	if ok, err := p.Verify(ctx, h, s, []byte{1}); err != nil || !ok {
		t.Fatalf("verify through wsl.exe: %v %v", ok, err)
	}
	// the default distribution was read once, and is named on every run even when the default changes
	wslDefaultDistro = func() (string, error) { return "", errors.New("the default changed") }
	if ok, err := p.Verify(ctx, h, s, []byte{1}); err != nil || !ok {
		t.Fatalf("a second verify: %v %v", ok, err)
	}
	calls := f.calls(t)
	if len(calls) != 2 {
		t.Fatalf("wsl.exe ran %d times: %q", len(calls), calls)
	}
	for _, c := range calls {
		if !strings.HasPrefix(c, "-d FakeDistro -e "+fakeProverPath+" verify "+strings.ToLower(h)+" ") {
			t.Fatalf("wsl.exe was run as %q", c)
		}
	}
	// replaced while the companion runs: refused at the next call, and nothing runs
	w, err := os.OpenFile(f.prover, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("x"))
	w.Close()
	if ok, err := p.Verify(ctx, h, s, []byte{1}); err == nil || ok || !strings.Contains(err.Error(), "pinned build") {
		t.Fatalf("a binary changed after the start: %v %v", ok, err)
	}
	if n := len(f.calls(t)); n != 2 {
		t.Fatalf("wsl.exe ran after the pin check failed (%d calls)", n)
	}
	// the command line's form goes to the same prover
	if _, err := PinnedProverFor("wsl.exe -d FakeDistro -e "+fakeProverPath, f.proverSum); err == nil {
		t.Fatal("the changed binary was accepted through PinnedProverFor")
	}
}

// TestPinnedWSLProverFailsClosed (X-17): every way the binary cannot be shown to be the pinned build refuses the prover
// before anything runs.
func TestPinnedWSLProverFailsClosed(t *testing.T) {
	f := newFakeWSL(t)
	refuse := func(name string, cmd []string, want string) {
		t.Helper()
		if _, err := NewPinnedWSLProver(cmd, f.proverSum); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want an error naming %q", name, err, want)
		}
	}
	refuse("a file no root has", []string{"wsl.exe", "-e", "/opt/dmz/no-such-file"}, "cannot be read")
	refuse("a distribution the share does not have", []string{"wsl.exe", "-d", "Other", "-e", fakeProverPath}, "cannot be read")
	refuse("a directory", []string{"wsl.exe", "-e", "/opt/dmz"}, "not a regular file")
	refuse("a launcher that is not Windows' own", []string{`C:\elsewhere\wsl.exe`, "-e", fakeProverPath}, "not Windows' own wsl.exe")
	refuse("a shell", []string{"wsl.exe", "sh", "-c", fakeProverPath}, "without -e")

	// the first root that reads the file decides: a wrong copy there is refused, and the right one behind it is not
	// looked for
	wrong := filepath.Join(f.roots[0], "FakeDistro", "opt", "dmz")
	copyTestBinary(t, wrong, "proof-xmr.exe", []byte("another build"))
	refuse("a wrong copy in the first root", []string{"wsl.exe", "-e", fakeProverPath}, "pinned build")
	if err := os.RemoveAll(f.roots[0]); err != nil {
		t.Fatal(err)
	}

	wslDefaultDistro = func() (string, error) { return "", errors.New("no default") }
	refuse("no readable default", []string{"wsl.exe", "-e", fakeProverPath}, "name it with -d")
	if _, err := NewPinnedWSLProver([]string{"wsl.exe", "-d", "FakeDistro", "-e", fakeProverPath}, f.proverSum); err != nil {
		t.Fatalf("with the distribution named: %v", err)
	}
	wslDefaultDistro = func() (string, error) { return "Fake Distro; rm", nil }
	refuse("a default that is not a name", []string{"wsl.exe", "-e", fakeProverPath}, "not a name")
	wslLauncher = func() (string, error) { return "", errors.New("WSL is not installed") }
	refuse("no wsl.exe", []string{"wsl.exe", "-d", "FakeDistro", "-e", fakeProverPath}, "not installed")
	if n := len(f.calls(t)); n != 0 {
		t.Fatalf("wsl.exe ran %d times while every prover was refused", n)
	}
}

// TestPinnedWSLProverRunsTheRealProver (X-17): the reproducible proof-xmr inside WSL, pinned by ProverSHA256, run through
// Windows' own wsl.exe: a key, a proof of it, the proof verified, and a tampered one refused. It needs the build, so it
// runs only when XMRSWAP_TEST_WSL_PROVER names its absolute path inside WSL (and XMRSWAP_TEST_WSL_DISTRO its
// distribution, when that is not the default).
func TestPinnedWSLProverRunsTheRealProver(t *testing.T) {
	path := os.Getenv("XMRSWAP_TEST_WSL_PROVER")
	if path == "" || runtime.GOOS != "windows" {
		t.Skip("set XMRSWAP_TEST_WSL_PROVER to the reproducible proof-xmr's absolute path inside WSL to run it through wsl.exe")
	}
	cmd := []string{"wsl.exe"}
	if d := os.Getenv("XMRSWAP_TEST_WSL_DISTRO"); d != "" {
		cmd = append(cmd, "-d", d)
	}
	cmd = append(cmd, "-e", path)
	p, err := NewPinnedWSLProver(cmd, ProverSHA256)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	secret, h, s, err := p.Keygen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	h2, s2, proof, err := p.Prove(ctx, secret)
	if err != nil || h2 != h || s2 != s {
		t.Fatalf("prove: %v (h %v, s %v)", err, h2 == h, s2 == s)
	}
	t.Logf("prove through wsl.exe: %v, %d proof bytes", time.Since(start).Round(time.Millisecond), len(proof))
	if ok, err := p.Verify(ctx, h, s, proof); err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	proof[len(proof)/2] ^= 1
	if ok, err := p.Verify(ctx, h, s, proof); err != nil || ok {
		t.Fatalf("a tampered proof: %v %v", ok, err)
	}
}
