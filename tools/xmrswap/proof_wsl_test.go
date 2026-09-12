package xmrswap

// X-17: the pinned prover inside WSL, with a stand-in wsl.exe over a stand-in distribution.

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

// fakeWSLMain stands in for wsl.exe over a distribution whose files are under <root>\<distribution>:
//   - `-d <distribution> -e /bin/cat <path>` is the distribution's cat: the file's bytes on stdout, or cat's refusal;
//   - `-d <distribution> -e <path> <args>...` runs the file with the args, stdin and stdout passed through, and exits
//     with its status.
//
// Each call is a line in XMRSWAP_FAKE_WSL_LOG. The file it runs does not see XMRSWAP_FAKE_WSL_*, so it answers as a
// prover (XMRSWAP_FAKE_PROVER), not as another wsl.exe.
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
	file := func(p string) string { return root + `\` + args[1] + strings.ReplaceAll(p, "/", `\`) }
	if args[3] == wslCat {
		if len(args) != 5 {
			fmt.Fprintln(os.Stderr, "fake cat: one file")
			return 1
		}
		fi, err := os.Stat(file(args[4]))
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "/bin/cat: %s: No such file or directory\n", args[4])
			return 1
		case fi.IsDir():
			fmt.Fprintf(os.Stderr, "/bin/cat: %s: Is a directory\n", args[4])
			return 1
		}
		b, err := os.ReadFile(file(args[4]))
		if err != nil {
			return 1
		}
		os.Stdout.Write(b)
		return 0
	}
	cmd := exec.Command(file(args[3]), args[4:]...)
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

// fakeWSL is a stand-in WSL: the distribution FakeDistro, with a prover at /opt/dmz/proof-xmr.exe (the extension only
// because the stand-in wsl.exe runs it with CreateProcess), and a copy of this binary named wsl.exe standing in as the
// system's. FakeDistro is the default. The prover copy's hash differs from the launcher's, so a test can tell which of
// the two was hashed.
type fakeWSL struct {
	launcher, launcherSum string
	prover, proverSum     string
	log                   string
}

const fakeProverPath = "/opt/dmz/proof-xmr.exe"

func newFakeWSL(t *testing.T) *fakeWSL {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("a prover run through wsl.exe is Windows'")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "distributions")
	f := &fakeWSL{log: filepath.Join(dir, "wsl-calls.log")}
	f.launcher, f.launcherSum = copyTestBinary(t, filepath.Join(dir, "System32"), "wsl.exe", nil)
	f.prover, f.proverSum = copyTestBinary(t, filepath.Join(root, "FakeDistro", "opt", "dmz"), "proof-xmr.exe", []byte("the build this test pins"))
	launcher, def := wslLauncher, wslDefaultDistro
	t.Cleanup(func() { wslLauncher, wslDefaultDistro = launcher, def })
	wslLauncher = func() (string, error) { return f.launcher, nil }
	wslDefaultDistro = func() (string, error) { return "FakeDistro", nil }
	t.Setenv("XMRSWAP_FAKE_WSL_ROOT", root)
	t.Setenv("XMRSWAP_FAKE_WSL_LOG", f.log)
	t.Setenv("XMRSWAP_FAKE_PROVER", "silent")
	return f
}

// calls is every command line the stand-in wsl.exe was run with, split into the reads of a binary (the distribution's
// cat) and the runs of one.
func (f *fakeWSL) calls(t *testing.T) (reads, runs []string) {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if w := strings.Fields(line); len(w) >= 4 && w[3] == wslCat {
			reads = append(reads, line)
		} else {
			runs = append(runs, line)
		}
	}
	return reads, runs
}

// TestAWSLProverCommandIsParsedStrictly (X-17): the one form accepted is `wsl.exe [-d <distribution>] -e <absolute
// path>`. There is no shell, no other option and nothing after the path, and the path must be clean, use only characters
// no command line treats specially, and have no component ending in '.': the review of 2026-09-12 hashed the pinned
// proof-xmr for each of its paths while wsl.exe ran another file.
func TestAWSLProverCommandIsParsedStrictly(t *testing.T) {
	for _, c := range []struct {
		cmd, distro, path string
		ok                bool
	}{
		{"wsl.exe -e /opt/dmz/proof-xmr", "", "/opt/dmz/proof-xmr", true},
		{"wsl.exe -d Ubuntu-24.04 -e /opt/dmz/target/release/proof-xmr", "Ubuntu-24.04", "/opt/dmz/target/release/proof-xmr", true},
		{"wsl --distribution Debian --exec /usr/local/bin/proof-xmr", "Debian", "/usr/local/bin/proof-xmr", true},
		{`C:\Windows\System32\wsl.exe -e /opt/proof-xmr`, "", "/opt/proof-xmr", true}, // which wsl.exe is checked when the prover is made
		{"wsl.exe -e /opt/dmz.d/.build/proof-xmr-1.2", "", "/opt/dmz.d/.build/proof-xmr-1.2", true},
		{"wsl.exe /opt/proof-xmr", "", "", false}, // no -e: a shell would find and run it
		{"wsl.exe bash -c /opt/proof-xmr", "", "", false},
		{"wsl.exe -e proof-xmr", "", "", false}, // not absolute
		{"wsl.exe -e /", "", "", false},
		{"wsl.exe -e /opt/../bin/proof-xmr", "", "", false},
		{"wsl.exe -e /opt//proof-xmr", "", "", false},
		{"wsl.exe -e /opt/proof-xmr/", "", "", false},
		{"wsl.exe -e /opt/proof:xmr", "", "", false},
		{"wsl.exe -e /opt/proof xmr", "", "", false},
		// the review's paths: a component ending in '.', which Windows strips on its way to a share
		{"wsl.exe -e /srv/u/proof-xmr.", "", "", false},
		{"wsl.exe -e /srv/u/proof-xmr..", "", "", false},
		{"wsl.exe -e /opt./proof-xmr", "", "", false},
		{"wsl.exe -d Ubuntu -e /opt/.../proof-xmr", "", "", false},
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

// TestPinnedWSLProverHashesTheBinaryWhereItRuns (X-17, the review of 2026-09-12): the pin is checked on the prover, not
// on wsl.exe, and the binary is read inside the distribution by the distribution's own cat, which wsl.exe runs with the
// very distribution and path it then runs the prover with: every read and every run name the same file to the same
// kernel. A command that names no distribution runs in the default one, read once and named on every read and run. A
// binary replaced after the start is refused at its next call, before anything runs.
func TestPinnedWSLProverHashesTheBinaryWhereItRuns(t *testing.T) {
	ctx := context.Background()
	f := newFakeWSL(t)
	cmd := []string{"wsl.exe", "-e", fakeProverPath}
	if _, err := NewPinnedWSLProver(cmd, f.launcherSum); err == nil || !strings.Contains(err.Error(), "pinned build") {
		t.Fatalf("pinning the launcher's hash was accepted: %v", err)
	}
	p, err := NewPinnedWSLProver(cmd, f.proverSum)
	if err != nil {
		t.Fatalf("the pinned binary in the distribution: %v", err)
	}
	h, s := statementForTest(t)
	if ok, err := p.Verify(ctx, h, s, []byte{1}); err != nil || !ok {
		t.Fatalf("verify through wsl.exe: %v %v", ok, err)
	}
	// the default distribution was read once, and is named on every read and run even when the default changes
	wslDefaultDistro = func() (string, error) { return "", errors.New("the default changed") }
	if ok, err := p.Verify(ctx, h, s, []byte{1}); err != nil || !ok {
		t.Fatalf("a second verify: %v %v", ok, err)
	}
	reads, runs := f.calls(t)
	if len(runs) != 2 || len(reads) != 4 {
		t.Fatalf("wsl.exe read the binary %d times and ran it %d times: %q %q", len(reads), len(runs), reads, runs)
	}
	for _, r := range reads {
		if r != "-d FakeDistro -e "+wslCat+" "+fakeProverPath {
			t.Fatalf("the binary was read as %q", r)
		}
	}
	for _, r := range runs {
		if !strings.HasPrefix(r, "-d FakeDistro -e "+fakeProverPath+" verify "+strings.ToLower(h)+" ") {
			t.Fatalf("wsl.exe was run as %q", r)
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
	if _, runs := f.calls(t); len(runs) != 2 {
		t.Fatalf("wsl.exe ran the prover after the pin check failed (%d runs)", len(runs))
	}
	// the command line's form goes to the same prover
	if _, err := PinnedProverFor("wsl.exe -d FakeDistro -e "+fakeProverPath, f.proverSum); err == nil {
		t.Fatal("the changed binary was accepted through PinnedProverFor")
	}
}

// TestPinnedWSLProverFailsClosed (X-17): every way the binary cannot be shown to be the pinned build refuses the prover
// before it runs.
func TestPinnedWSLProverFailsClosed(t *testing.T) {
	f := newFakeWSL(t)
	refuse := func(name string, cmd []string, want string) {
		t.Helper()
		if _, err := NewPinnedWSLProver(cmd, f.proverSum); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want an error naming %q", name, err, want)
		}
	}
	refuse("a file the distribution does not have", []string{"wsl.exe", "-e", "/opt/dmz/no-such-file"}, "cannot be read")
	refuse("a distribution the machine does not have", []string{"wsl.exe", "-d", "Other", "-e", fakeProverPath}, "cannot be read")
	refuse("a directory", []string{"wsl.exe", "-e", "/opt/dmz"}, "Is a directory")
	refuse("a launcher that is not Windows' own", []string{`C:\elsewhere\wsl.exe`, "-e", fakeProverPath}, "not Windows' own wsl.exe")
	refuse("a shell", []string{"wsl.exe", "sh", "-c", fakeProverPath}, "without -e")
	refuse("a name Windows would rewrite", []string{"wsl.exe", "-e", fakeProverPath + "."}, "ends in '.'")

	wslDefaultDistro = func() (string, error) { return "", errors.New("no default") }
	refuse("no readable default", []string{"wsl.exe", "-e", fakeProverPath}, "name it with -d")
	if _, err := NewPinnedWSLProver([]string{"wsl.exe", "-d", "FakeDistro", "-e", fakeProverPath}, f.proverSum); err != nil {
		t.Fatalf("with the distribution named: %v", err)
	}
	wslDefaultDistro = func() (string, error) { return "Fake Distro; rm", nil }
	refuse("a default that is not a name", []string{"wsl.exe", "-e", fakeProverPath}, "not a name")
	wslLauncher = func() (string, error) { return "", errors.New("WSL is not installed") }
	refuse("no wsl.exe", []string{"wsl.exe", "-d", "FakeDistro", "-e", fakeProverPath}, "not installed")
	if _, runs := f.calls(t); len(runs) != 0 {
		t.Fatalf("the prover ran %d times while every prover was refused: %q", len(runs), runs)
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

// TestTheRealWSLReadIsTheFileItRuns (the review of 2026-09-12): through the real wsl.exe and the default distribution,
// the bytes the companion reads inside the distribution hash as the distribution's own sha256sum hashes the file; a
// prover pinned to that hash is accepted and runs (/bin/true, which exits 0 silently, answers verify as the pinned build
// does); and the review's form of the path is refused before anything is read. It starts the default distribution, so
// it runs only with XMRSWAP_TEST_WSL=1.
func TestTheRealWSLReadIsTheFileItRuns(t *testing.T) {
	if os.Getenv("XMRSWAP_TEST_WSL") != "1" || runtime.GOOS != "windows" {
		t.Skip("set XMRSWAP_TEST_WSL=1 to read and run /bin/true in the default WSL distribution through the real wsl.exe")
	}
	launcher, err := systemWSL()
	if err != nil {
		t.Fatal(err)
	}
	distro, err := defaultWSLDistro()
	if err != nil {
		t.Fatal(err)
	}
	name, b, err := readInWSL(launcher, distro, "/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	pin := hex.EncodeToString(sum[:])
	out, err := exec.Command(launcher, "-d", distro, "-e", "/usr/bin/sha256sum", "/bin/true").Output()
	if err != nil || len(strings.Fields(string(out))) == 0 || strings.Fields(string(out))[0] != pin {
		t.Fatalf("%s hashes to %s as read; the distribution's sha256sum says %q (%v)", name, pin, out, err)
	}
	p, err := NewPinnedWSLProver([]string{"wsl.exe", "-d", distro, "-e", "/bin/true"}, pin)
	if err != nil {
		t.Fatal(err)
	}
	h, s := statementForTest(t)
	if ok, err := p.Verify(context.Background(), h, s, []byte{1}); err != nil || !ok {
		t.Fatalf("/bin/true run as the prover: %v %v", ok, err)
	}
	if _, err := NewPinnedWSLProver([]string{"wsl.exe", "-d", distro, "-e", "/bin/true."}, pin); err == nil || !strings.Contains(err.Error(), "ends in '.'") {
		t.Fatalf("the review's form of the path: %v", err)
	}
	t.Logf("%s: %d bytes, sha256 %s, the same as the distribution's sha256sum", name, len(b), pin)
}
