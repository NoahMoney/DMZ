// release builds DMZ's user-facing binaries reproducibly and records what it built in manifest.json, so that anyone
// can rebuild from the same source and compare hashes.
//
//	go run ./release -out <dir>                          build every binary for every platform, write <dir>/manifest.json
//	go run ./release -verify <dir>/manifest.json         rebuild from a fresh copy of the sources, at a new directory
//	                 [-binaries <dir>]                   path, and compare every hash; also hash published binaries
//
// Every binary is built with
//
//	CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> GOFLAGS= GOWORK=off GOTOOLCHAIN=local \
//	  go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o <file> <package>
//
// -trimpath drops the build directory from the binary, -buildvcs=false the git state, -buildid= the linker's
// build id, and CGO_ENABLED=0 the host C toolchain; what remains is a function of the source, the module graph
// (tools/go.sum) and the Go version, which the manifest records.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"veil/tools/verifydeploy/srchash"
)

// The platforms and the user-facing binaries. A binary whose package is not a main package (or does not exist) is
// recorded as absent, with the reason, and never built or faked.
var platforms = []struct{ GOOS, GOARCH string }{
	{"windows", "amd64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"},
}

var products = []struct{ Name, Package string }{
	{"xmrswap", "./xmrswap/cmd/xmrswap"},
	{"zecswap", "./zecswap/cmd/zecswap"},
}

const ldflags = "-s -w -buildid="

func buildArgs(out, pkg string) []string {
	return []string{"build", "-trimpath", "-buildvcs=false", "-ldflags=" + ldflags, "-o", out, pkg}
}

func buildEnv(goos, goarch string) []string {
	env := []string{}
	for _, e := range os.Environ() {
		k := strings.SplitN(e, "=", 2)[0]
		switch strings.ToUpper(k) {
		case "CGO_ENABLED", "GOOS", "GOARCH", "GOFLAGS", "GOWORK", "GOTOOLCHAIN", "GOAMD64", "GOARM64", "GOEXPERIMENT":
			continue
		}
		env = append(env, e)
	}
	return append(env, "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch, "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
}

func run(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String()), nil
}

func fileSum(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func binName(name, goos, goarch string) string {
	s := fmt.Sprintf("%s-%s-%s", name, goos, goarch)
	if goos == "windows" {
		s += ".exe"
	}
	return s
}

// layout finds the tools module and the repository around it.
type layout struct{ tools, root string }

func findLayout() (layout, error) {
	mod, err := run("", nil, "go", "env", "GOMOD")
	if err != nil {
		return layout{}, err
	}
	if filepath.Base(mod) != "go.mod" || filepath.Base(filepath.Dir(mod)) != "tools" {
		return layout{}, fmt.Errorf("run this from the tools module (go env GOMOD says %q)", mod)
	}
	t := filepath.Dir(mod)
	return layout{tools: t, root: filepath.Dir(t)}, nil
}

// build compiles every product for every platform from the tools module at l into outDir.
func build(l layout, outDir string, logf func(string, ...interface{})) ([]Binary, []Absent, error) {
	var bins []Binary
	var absent []Absent
	for _, p := range products {
		name, err := run(l.tools, buildEnv("linux", "amd64"), "go", "list", "-f", "{{.Name}}", p.Package)
		if err != nil || name != "main" {
			why := "not a main package"
			if err != nil {
				why = "no such package: " + firstLine(err.Error())
			}
			absent = append(absent, Absent{Name: p.Name, Package: p.Package, Reason: why})
			logf("absent  %-8s %s: %s", p.Name, p.Package, why)
			continue
		}
		for _, pl := range platforms {
			file := binName(p.Name, pl.GOOS, pl.GOARCH)
			out := filepath.Join(outDir, file)
			if _, err := run(l.tools, buildEnv(pl.GOOS, pl.GOARCH), "go", buildArgs(out, p.Package)...); err != nil {
				return nil, nil, err
			}
			sum, n, err := fileSum(out)
			if err != nil {
				return nil, nil, err
			}
			bins = append(bins, Binary{Name: p.Name, Package: p.Package, GOOS: pl.GOOS, GOARCH: pl.GOARCH, File: file, Sha256: sum, Bytes: n})
			logf("built   %-34s %s %9d B", file, sum, n)
		}
	}
	return bins, absent, nil
}

func firstLine(s string) string { return strings.SplitN(strings.TrimSpace(s), "\n", 2)[0] }

// buildInputs is every directory inside the repository that any product's package graph reads, on any platform.
func buildInputs(l layout) ([]string, error) {
	set := map[string]bool{}
	for _, pl := range platforms {
		for _, p := range products {
			out, err := run(l.tools, buildEnv(pl.GOOS, pl.GOARCH), "go", "list", "-deps", "-f", "{{if not .Standard}}{{.Dir}}{{end}}", p.Package)
			if err != nil {
				continue // an absent product has no graph
			}
			for _, d := range strings.Split(out, "\n") {
				if d = strings.TrimSpace(d); d == "" {
					continue
				}
				if rel, err := filepath.Rel(l.root, d); err == nil && !strings.HasPrefix(rel, "..") {
					set[filepath.ToSlash(rel)] = true
				}
			}
		}
	}
	var dirs []string
	for d := range set {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// copyInputs copies the build inputs (every regular file directly in each input directory, test files excepted)
// and the two modules' go.mod / go.sum into dst, which then builds exactly as the original does.
func copyInputs(l layout, dirs []string, dst string) error {
	files := []string{"go.mod", "go.sum", "tools/go.mod", "tools/go.sum"}
	for _, d := range dirs {
		ents, err := os.ReadDir(filepath.Join(l.root, filepath.FromSlash(d)))
		if err != nil {
			return err
		}
		for _, e := range ents {
			if e.Type().IsRegular() && !strings.HasSuffix(e.Name(), "_test.go") && !strings.HasSuffix(e.Name(), ".exe") {
				files = append(files, d+"/"+e.Name())
			}
		}
	}
	for _, f := range files {
		src := filepath.Join(l.root, filepath.FromSlash(f))
		b, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) && (f == "go.sum") {
				continue
			}
			return err
		}
		out := filepath.Join(dst, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// gitState is the checkout's commit, whether anything differs from it, and which build inputs do.
func gitState(l layout, inputs []string) (commit string, dirty bool, changed []string) {
	c, err := run(l.root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", false, nil
	}
	all, _ := run(l.root, nil, "git", "status", "--porcelain")
	dirty = all != ""
	// what differs from the commit (tracked) plus what the commit does not have (untracked), among the files that
	// feed a binary or a contract hash: non-test Go files and go.mod/go.sum of the inputs, and contracts/*.bas
	spec := append([]string{"--", "contracts", "go.mod", "tools/go.mod", "tools/go.sum"}, inputs...)
	tracked, _ := run(l.root, nil, "git", append([]string{"diff", "--name-only", "HEAD"}, spec...)...)
	untracked, _ := run(l.root, nil, "git", append([]string{"ls-files", "--others", "--exclude-standard"}, spec...)...)
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(tracked + "\n" + untracked))
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		isContract := strings.HasPrefix(p, "contracts/") && strings.HasSuffix(p, ".bas")
		isInput := !strings.HasPrefix(p, "contracts/") && !strings.HasSuffix(p, "_test.go") && (strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "go.mod") || strings.HasSuffix(p, "go.sum"))
		if p != "" && !seen[p] && (isContract || isInput) {
			seen[p] = true
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	return c, dirty, changed
}

// distSums hashes every file of every dist/<network>/ bundle present.
func distSums(root string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	ents, err := os.ReadDir(filepath.Join(root, "dist"))
	if os.IsNotExist(err) {
		return out, nil
	} else if err != nil {
		return nil, err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		base := filepath.Join(root, "dist", e.Name())
		m := map[string]string{}
		err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(base, p)
			sum, _, err := fileSum(p)
			m[filepath.ToSlash(rel)] = sum
			return err
		})
		if err != nil {
			return nil, err
		}
		out[e.Name()] = m
	}
	return out, nil
}

// assemble builds into outDir from the tools module at l and returns the manifest.
func assemble(l layout, outDir string, logf func(string, ...interface{})) (*Manifest, error) {
	goVersion, err := run(l.tools, buildEnv("linux", "amd64"), "go", "env", "GOVERSION")
	if err != nil {
		return nil, err
	}
	inputs, err := buildInputs(l)
	if err != nil {
		return nil, err
	}
	bins, absent, err := build(l, outDir, logf)
	if err != nil {
		return nil, err
	}
	m := &Manifest{Schema: schema, Go: goVersion, Build: buildSpec(inputs), Binaries: bins, Absent: absent}
	m.Commit, m.Dirty, m.DirtyInputs = gitState(l, inputs)
	if m.Contracts, err = srchash.Contracts(filepath.Join(l.root, "contracts")); err != nil {
		return nil, err
	}
	if m.Dist, err = distSums(l.root); err != nil {
		return nil, err
	}
	m.sort()
	return m, nil
}

func buildSpec(inputs []string) BuildSpec {
	return BuildSpec{
		Command: "go " + strings.Join(buildArgs("<file>", "<package>"), " "),
		Env:     []string{"CGO_ENABLED=0", "GOOS=<os>", "GOARCH=<arch>", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local"},
		Module:  "tools/go.mod (module veil/tools; replace veil => ../)",
		Inputs:  inputs,
	}
}

func main() {
	out := flag.String("out", "", "directory to build into; manifest.json is written there")
	verify := flag.String("verify", "", "a manifest.json to rebuild and compare against")
	bins := flag.String("binaries", "", "with -verify: a directory of published binaries to hash against the manifest")
	keep := flag.Bool("keep", false, "with -verify: keep the temporary source copy and build")
	flag.Parse()
	logf := func(f string, a ...interface{}) { fmt.Printf(f+"\n", a...) }
	l, err := findLayout()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	switch {
	case *out != "" && *verify == "":
		abs, _ := filepath.Abs(*out)
		if err := os.MkdirAll(abs, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		logf("building from %s into %s", l.tools, abs)
		m, err := assemble(l, abs, logf)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := m.Write(filepath.Join(abs, "manifest.json")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		logf("%s: %d binaries, %d absent, %d contracts, go %s, commit %s dirty=%v (%d build inputs changed)", filepath.Join(abs, "manifest.json"),
			len(m.Binaries), len(m.Absent), len(m.Contracts), m.Go, m.Commit, m.Dirty, len(m.DirtyInputs))
	case *verify != "" && *out == "":
		os.Exit(runVerify(l, *verify, *bins, *keep, logf))
	default:
		fmt.Fprintln(os.Stderr, "usage: release -out <dir> | release -verify <manifest.json> [-binaries <dir>]")
		os.Exit(2)
	}
}

func runVerify(l layout, manifestPath, binDir string, keep bool, logf func(string, ...interface{})) int {
	want, err := ReadManifest(manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	tmp, err := os.MkdirTemp("", "dmz-release-verify-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if !keep {
		defer os.RemoveAll(tmp)
	}
	inputs, err := buildInputs(l)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	src := filepath.Join(tmp, "src")
	if err := copyInputs(l, inputs, src); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	logf("copied %d build-input directories to %s and rebuilding there (a different path from %s)", len(inputs), src, l.root)
	outDir := filepath.Join(tmp, "out")
	_ = os.MkdirAll(outDir, 0o755)
	got, err := assemble(layout{tools: filepath.Join(src, "tools"), root: src}, outDir, logf)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// contracts and dist are read from this checkout, not the copy (the copy holds only build inputs)
	commit, dirty, changed := gitState(l, inputs)
	got.Commit, got.Dirty, got.DirtyInputs = commit, dirty, changed
	if got.Contracts, err = srchash.Contracts(filepath.Join(l.root, "contracts")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if got.Dist, err = distSums(l.root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	res := Compare(want, got)
	if binDir != "" {
		res = append(res, comparePublished(want, binDir)...)
	}
	fails := 0
	for _, r := range res {
		logf("%-4s %s", r.Status, r.What)
		if r.Status == "FAIL" {
			fails++
		}
	}
	if fails > 0 {
		logf("%d of %d comparisons FAILED", fails, len(res))
		return 1
	}
	logf("all %d comparisons match", len(res))
	return 0
}

// comparePublished hashes the binaries in dir against the manifest.
func comparePublished(m *Manifest, dir string) []Result {
	var out []Result
	for _, b := range m.Binaries {
		sum, _, err := fileSum(filepath.Join(dir, b.File))
		switch {
		case err != nil:
			out = append(out, Result{"FAIL", fmt.Sprintf("published %s: %v", b.File, err)})
		case sum != b.Sha256:
			out = append(out, Result{"FAIL", fmt.Sprintf("published %s: sha256 %s, manifest %s", b.File, sum, b.Sha256)})
		default:
			out = append(out, Result{"PASS", fmt.Sprintf("published %s: sha256 %s = manifest", b.File, sum)})
		}
	}
	return out
}
