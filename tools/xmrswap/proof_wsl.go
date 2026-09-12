package xmrswap

// X-17: the pinned prover on Windows, where proof-xmr is a Linux binary inside a WSL distribution.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// A Windows holder runs proof-xmr as `wsl.exe -d <distribution> -e <path>`. The pin cannot be checked on the command's
// first program, which is wsl.exe, so the companion checks it on the binary itself, before every run, as NewPinnedProver
// hashes a native binary. It reads the binary inside the distribution, with `wsl.exe -d <distribution> -e /bin/cat
// <path>`, and hashes the bytes that come back; then Windows' own wsl.exe runs the same path in the same distribution.
//
// Why inside the distribution (the review of 2026-09-12, findings 2 and 5). The binary used to be read through the
// distribution's share, \\wsl.localhost\<distribution>\<path>, and that had two holes:
//   - The file hashed was not always the file run. Windows rewrites a path on its way to a share: among other things it
//     strips a component's trailing dots and spaces. So /opt/dmz/proof-xmr. was hashed as the pinned proof-xmr while
//     wsl.exe ran the Linux file named proof-xmr., which the review confirmed live. Refusing every name Windows would
//     rewrite means listing every rewrite every Windows build makes; reading inside the distribution leaves nothing to
//     list, because the read and the run then resolve the one path through the one Linux kernel.
//   - \\wsl.localhost is a UNC server name. On a Windows without WSL's own network provider it is resolved like any other
//     server name, so a machine on the network could answer the read and hand over the pinned build while the local file
//     ran. A read inside the distribution never leaves this machine.
//
// /bin/cat is the distribution's own, named by its absolute path so that no PATH is searched. Trusting it is trusting
// the distribution's root, as the share already did (WSL's file server runs in the distribution as its root), and that
// root could replace the prover itself. Components that end in '.' are refused all the same (ParseWSLProverCommand): no
// build is installed under such a name, and it is the form that once named one file to the hash and another to the run.
// A read inside the distribution was checked live on 2026-09-13: what wsl.exe passes back hashes as the distribution's
// own sha256sum hashes the file, and /bin/true. is not /bin/true there.

var (
	// wslLauncher is the one wsl.exe the companion puts in front of the prover and its read: Windows' own, in the
	// directory GetSystemDirectoryW names, never one that PATH or %SystemRoot% finds. A test names a stand-in.
	wslLauncher = systemWSL
	// wslDefaultDistro names the distribution a command runs in when it names none. A test names its own.
	wslDefaultDistro = defaultWSLDistro
)

// maxPinnedProverBytes caps the read of the binary: a path naming a device such as /dev/zero would otherwise be read for
// ever. The pinned build is 1,340,640 bytes.
const maxPinnedProverBytes = 256 << 20

// wslReadTimeout bounds the read: starting a stopped distribution takes seconds, and a path naming a FIFO would otherwise
// hold it open for ever.
const wslReadTimeout = 2 * time.Minute

// wslCat reads the binary inside the distribution: the distribution's own cat, by its absolute path.
const wslCat = "/bin/cat"

var (
	wslDistroName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// The path is handed to wsl.exe twice, once to be read and once to be run, and is accepted only in characters that
	// no command line quotes, splits or expands, so the two are the same string to the same kernel.
	wslPathChars = regexp.MustCompile(`^[A-Za-z0-9._+@~/-]+$`)
)

// IsWSLProverCommand says whether a -prover value is a wsl.exe command rather than a binary's path: its first word is
// wsl.exe (or wsl, or a path ending in wsl.exe).
func IsWSLProverCommand(v string) bool {
	f := strings.Fields(v)
	return len(f) > 0 && isWSLName(f[0])
}

func isWSLName(s string) bool {
	base := s
	if i := strings.LastIndexAny(s, `\/`); i >= 0 {
		base = s[i+1:]
	}
	return strings.EqualFold(base, "wsl.exe") || strings.EqualFold(s, "wsl")
}

// ParseWSLProverCommand reads a prover command of the form `wsl.exe [-d <distribution>] -e <absolute Linux path>`
// (--distribution and --exec are the long forms) and refuses anything else:
//   - no shell: without -e, wsl.exe runs the command line through the user's shell, which searches PATH for it and
//     expands it;
//   - no other option, and nothing after the path, since the companion adds its own verb and arguments;
//   - a path that is absolute and clean, made only of characters no command line treats specially, with no component
//     that ends in '.'.
//
// distro is "" when the command names none. launcher is the first word as given.
func ParseWSLProverCommand(command []string) (launcher, distro, linuxPath string, err error) {
	bad := func(format string, a ...interface{}) (string, string, string, error) {
		return "", "", "", fmt.Errorf("proof-xmr: "+format+"; the form is `wsl.exe [-d <distribution>] -e <absolute path of proof-xmr in it>`", a...)
	}
	if len(command) == 0 || !isWSLName(command[0]) {
		return bad("%q is not a wsl.exe command", strings.Join(command, " "))
	}
	args := command[1:]
	if len(args) >= 2 && (args[0] == "-d" || args[0] == "--distribution") {
		distro, args = args[1], args[2:]
		if !wslDistroName.MatchString(distro) {
			return bad("%q is not a WSL distribution name", distro)
		}
	}
	if len(args) != 2 || (args[0] != "-e" && args[0] != "--exec") {
		return bad("wsl.exe is given -e and the binary's path, and nothing else: without -e it runs the command through a shell, which finds and expands it")
	}
	p := args[1]
	switch {
	case !strings.HasPrefix(p, "/") || p == "/":
		return bad("%q is not an absolute Linux path to a file", p)
	case path.Clean(p) != p:
		return bad("%q is not a clean path (no . or .. part, no doubled or trailing slash)", p)
	case !wslPathChars.MatchString(p):
		return bad("%q has a character outside the ones accepted (letters, digits, . _ + @ ~ - /)", p)
	case dotEndedComponent(p) != "":
		return bad("%q has the component %q, which ends in '.': Windows strips such a dot from a path it handles, which once let one file be hashed and another run, and no build is installed under such a name", p, dotEndedComponent(p))
	}
	return command[0], distro, p, nil
}

// dotEndedComponent is the first component of an absolute path that ends in '.', or "".
func dotEndedComponent(p string) string {
	for _, c := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if strings.HasSuffix(c, ".") {
			return c
		}
	}
	return ""
}

// PinnedProverFor is the companion's prover for a -prover value: a wsl.exe command (NewPinnedWSLProver) or a binary's
// absolute path (NewPinnedProver). Either way the file that runs is hashed before every run and must be wantSHA256.
func PinnedProverFor(v, wantSHA256 string) (Prover, error) {
	if IsWSLProverCommand(v) {
		return NewPinnedWSLProver(strings.Fields(v), wantSHA256)
	}
	return NewPinnedProver(v, wantSHA256)
}

// NewPinnedWSLProver is NewPinnedProver for a proof-xmr inside a WSL distribution (X-17). The command is
// `wsl.exe [-d <distribution>] -e <absolute path>` (ParseWSLProverCommand). The binary at that path, read inside the
// distribution, must hash to wantSHA256 now and before every later run, or nothing runs. It fails closed:
//   - a file the distribution's cat cannot read, one larger than any build, or one whose read does not end, is refused,
//     and so is one that hashes wrong;
//   - a launcher that is not Windows' own wsl.exe is refused, since whatever runs in front of the prover is trusted
//     with every proof it checks;
//   - a command that names no distribution runs in the default one, read once here and then named on every read and
//     run, so the distribution hashed is the one run even if the default changes meanwhile; a default that cannot be
//     read is refused.
func NewPinnedWSLProver(command []string, wantSHA256 string) (Prover, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("proof-xmr: a prover run through wsl.exe is for Windows; elsewhere give the binary's absolute path")
	}
	given, distro, linuxPath, err := ParseWSLProverCommand(command)
	if err != nil {
		return nil, err
	}
	launcher, err := wslLauncher()
	if err != nil {
		return nil, fmt.Errorf("proof-xmr: %w", err)
	}
	if !strings.EqualFold(given, "wsl.exe") && !strings.EqualFold(given, "wsl") && !strings.EqualFold(filepath.Clean(given), filepath.Clean(launcher)) {
		return nil, fmt.Errorf("proof-xmr: %s is not Windows' own wsl.exe (%s): the program in front of the prover is trusted with every proof it checks, and the companion runs only that one", given, launcher)
	}
	if distro == "" {
		if distro, err = wslDefaultDistro(); err != nil {
			return nil, fmt.Errorf("proof-xmr: the command names no WSL distribution and the default cannot be read (%v): name it with -d", err)
		}
		if !wslDistroName.MatchString(distro) {
			return nil, fmt.Errorf("proof-xmr: the default WSL distribution %q is not a name this companion accepts: name one with -d", distro)
		}
	}
	p := &proverCLI{command: []string{launcher, "-d", distro, "-e", linuxPath}, pin: strings.ToLower(wantSHA256),
		pinned: func() (string, []byte, error) { return readInWSL(launcher, distro, linuxPath) }}
	if err := p.checkPin(); err != nil {
		return nil, err
	}
	return p, nil
}

// readInWSL reads linuxPath inside distro with the distribution's own cat, which wsl.exe runs exactly as it will run
// the prover, and names what it read.
func readInWSL(launcher, distro, linuxPath string) (string, []byte, error) {
	name := linuxPath + " in the WSL distribution " + distro
	ctx, cancel := context.WithTimeout(context.Background(), wslReadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, launcher, "-d", distro, "-e", wslCat, linuxPath)
	out, errb := &capBuffer{max: maxPinnedProverBytes}, &capBuffer{max: 4096}
	cmd.Stdout, cmd.Stderr = out, errb
	err := cmd.Run()
	switch {
	case out.over:
		return "", nil, fmt.Errorf("%s is larger than %d bytes: it is not the pinned build, and it is not run", name, maxPinnedProverBytes)
	case ctx.Err() != nil:
		return "", nil, fmt.Errorf("%s was not read within %v, so it cannot be hashed, and it is not run", name, wslReadTimeout)
	case err != nil:
		return "", nil, fmt.Errorf("%s cannot be read (%v: %s), so it cannot be hashed, and it is not run", name, err, firstLine(errb.String()))
	}
	return name, out.Bytes(), nil
}

// firstLine is the first line of a program's error output, readable: wsl.exe writes its own messages in UTF-16, whose
// NULs are dropped, and at most 200 bytes are kept.
func firstLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// systemWSL is Windows' own wsl.exe, in the directory GetSystemDirectoryW names.
func systemWSL() (string, error) {
	dir, err := systemDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "wsl.exe")
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("there is no wsl.exe at %s: WSL is not installed", p)
	}
	return p, nil
}
