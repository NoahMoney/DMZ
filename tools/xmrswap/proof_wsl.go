package xmrswap

// X-17: the pinned prover on Windows, where proof-xmr is a Linux binary inside a WSL distribution.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// A Windows holder runs proof-xmr as `wsl.exe -d <distribution> -e <path>`. The pin cannot be checked on the command's
// first program, which is wsl.exe. So the companion checks it on the binary itself. It reads the file through the
// distribution's own share, \\wsl.localhost\<distribution>\<path>, or \\wsl$\<distribution>\<path> on a Windows that
// has only the older name. It hashes the file before every run, as NewPinnedProver hashes a native binary, and then
// has Windows' own wsl.exe run it.

var (
	// wslShareRoots are the roots a distribution's files are read through, in order. The first that reads the file is
	// the one hashed; a later root is tried only when an earlier one cannot read it. A test points them at its own
	// directories.
	wslShareRoots = []string{`\\wsl.localhost`, `\\wsl$`}
	// wslLauncher is the one wsl.exe the companion puts in front of the prover: Windows' own, in System32, never one
	// that PATH finds. A test names a stand-in.
	wslLauncher = systemWSL
	// wslDefaultDistro names the distribution a command runs in when it names none. A test names its own.
	wslDefaultDistro = defaultWSLDistro
)

// maxPinnedProverBytes caps the read of a binary through the share: a path naming a device such as /dev/zero would
// otherwise be read for ever. The pinned build is 1,340,640 bytes.
const maxPinnedProverBytes = 256 << 20

var (
	wslDistroName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// WSL's share shows some characters of a Linux file name as others (: * ? " < > | \ among them). A path with any of
	// them could name one file to the hash and another to wsl.exe, so only these are accepted.
	wslPathChars = regexp.MustCompile(`^[A-Za-z0-9._+@~/-]+$`)
	wslGUID      = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)
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
//   - a path that is absolute, clean, and made only of characters the share names the way wsl.exe does.
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
		return bad("%q has a character WSL's share may show differently from the name wsl.exe runs (accepted: letters, digits, . _ + @ ~ - /)", p)
	}
	return command[0], distro, p, nil
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
// `wsl.exe [-d <distribution>] -e <absolute path>` (ParseWSLProverCommand). The binary at that path, read through the
// distribution's share, must hash to wantSHA256 now and before every later run, or nothing runs. It fails closed:
//   - a file that no share root can read, or that is not a regular file, is refused, and so is a first readable copy
//     that hashes wrong (no later root is searched for one that matches);
//   - a launcher that is not Windows' own wsl.exe is refused, since whatever runs in front of the prover is trusted
//     with every proof it checks;
//   - a command that names no distribution runs in the default one, read once here and then named on every run, so
//     the distribution hashed is the one run even if the default changes meanwhile; a default that cannot be read is
//     refused.
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
		pinned: func() (string, []byte, error) { return readWSLFile(distro, linuxPath) }}
	if err := p.checkPin(); err != nil {
		return nil, err
	}
	return p, nil
}

// readWSLFile reads linuxPath in distro through the first share root that can read it, and names the file it read.
func readWSLFile(distro, linuxPath string) (string, []byte, error) {
	var why []string
	for _, root := range wslShareRoots {
		name := root + `\` + distro + strings.ReplaceAll(linuxPath, "/", `\`)
		b, err := readRegularFile(name, maxPinnedProverBytes)
		if err == nil {
			return name, b, nil
		}
		why = append(why, err.Error())
	}
	return "", nil, fmt.Errorf("%s in the WSL distribution %s cannot be read, so it cannot be hashed, and it is not run: %s", linuxPath, distro, strings.Join(why, "; "))
}

// readRegularFile reads a regular file of at most max bytes.
func readRegularFile(name string, max int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, max)
	}
	return b, nil
}

// systemDir is Windows' System32, from %SystemRoot%.
func systemDir() (string, error) {
	root := os.Getenv("SystemRoot")
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("%SystemRoot% is not an absolute path, so Windows' own programs cannot be found")
	}
	return filepath.Join(root, "System32"), nil
}

// systemWSL is Windows' own wsl.exe.
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

// lxssKey is where WSL records its distributions for this user: the default's GUID in DefaultDistribution, and each
// GUID's name in its own subkey's DistributionName.
const lxssKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss`

// defaultWSLDistro reads the default distribution's name from the registry, as wsl.exe does, with Windows' own reg.exe.
func defaultWSLDistro() (string, error) {
	dir, err := systemDir()
	if err != nil {
		return "", err
	}
	reg := filepath.Join(dir, "reg.exe")
	guid, err := regString(reg, lxssKey, "DefaultDistribution")
	if err != nil {
		return "", err
	}
	if !wslGUID.MatchString(guid) {
		return "", fmt.Errorf("%s DefaultDistribution is %q, not a GUID", lxssKey, guid)
	}
	return regString(reg, lxssKey+`\`+guid, "DistributionName")
}

// regString is one REG_SZ value, as `reg query <key> /v <value>` prints it: "    <value>    REG_SZ    <data>".
func regString(reg, key, value string) (string, error) {
	out, err := exec.Command(reg, "query", key, "/v", value).Output()
	if err != nil {
		return "", fmt.Errorf("reg query %s /v %s: %w", key, value, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) == 3 && strings.EqualFold(f[0], value) && f[1] == "REG_SZ" {
			return f[2], nil
		}
	}
	return "", fmt.Errorf("reg query %s /v %s: no REG_SZ value in the answer", key, value)
}
