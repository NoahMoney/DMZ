// verifyvendor checks every vendored checkout against deploy/vendor.lock: the commit is the pinned one, the only
// modified tracked files are the listed ones and carry exactly the stored patch, the only deleted files are the
// listed ones, and every untracked file sits under a listed directory. With -write-binaries it records sha256 of
// vendor/bin/*.exe into deploy/binaries.lock; otherwise, when that file exists, the binaries are checked too.
//
// A checkout that builds in module mode is pinned under "modules" too (GAP-017). vendor/tela is one: its go.mod
// replaces derohe with the civilware fork, which the go command fetches from the module proxy. Its go.mod must carry
// exactly the pinned replace directives, and its go.sum exactly the pinned hashes of each replacement and no other
// version of it. Every copy of a go.sum module in the local module cache must hash to its go.sum line, because go
// checks a module only when it downloads it. Each binary listed must embed only go.sum's modules and hashes, and the
// pinned replacement.
//
//	go run ./verifyvendor [-root ..] [-write-binaries] [-modules-only]
//
// -modules-only runs just the "modules" checks, without the binaries' build info; tools/telainstall/build.sh runs it
// before it builds. Exit 1 on any finding. CR at end of line is ignored when comparing patches (Windows checkouts).
package main

import (
	"archive/zip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type repo struct {
	URL       string            `json:"url"`
	Commit    string            `json:"commit"`
	Modified  map[string]string `json:"modified"`
	Deleted   []string          `json:"deleted"`
	Untracked []string          `json:"untracked"`
}

// crate pins a crates.io source tree that a build in this repository uses as a path dependency. The tree lives in an
// ignored directory, so git cannot vouch for it: it must be the published crate plus the one committed patch. Its
// fetch script rebuilds it from the network and checks the crate's sha256 (GAP-013); here the tree is checked offline
// against the hash it was pinned with once the script had confirmed it.
type crate struct {
	Crate   string `json:"crate"`
	Version string `json:"version"`
	Sha256  string `json:"sha256"` // of the .crate file: the "cksum" crates.io's own index records for this version
	Patch   string `json:"patch"`  // the one allowed change, a repository path
	Fetch   string `json:"fetch"`  // the script that rebuilds the tree and checks it against the crate
	Tree    string `json:"tree"`   // treeHash of the crate plus the patch
}

// modPin pins what a module-mode build inside a vendored checkout takes from outside it (GAP-017).
type modPin struct {
	Replace  []replacePin `json:"replace"`
	Binaries []string     `json:"binaries,omitempty"` // built from this checkout; their build info is checked
}

// replacePin is one replace directive of the checkout's go.mod, and the go.sum hashes of its replacement.
type replacePin struct {
	Old      string `json:"old"`      // the module path replaced
	New      string `json:"new"`      // the replacement module path
	Version  string `json:"version"`  // the replacement version
	Commit   string `json:"commit"`   // the upstream commit that version names (the module proxy's .info Origin)
	Sum      string `json:"sum"`      // go.sum h1 of the module
	GoModSum string `json:"gomodSum"` // go.sum h1 of its go.mod
}

type lock struct {
	Repos   map[string]repo   `json:"repos"`
	Modules map[string]modPin `json:"modules,omitempty"`
	Crates  map[string]crate  `json:"crates,omitempty"`
}

// treeHash is sha256 over the sorted (path, bytes) of every file under dir, skipping what a build writes into it
// (target/, a top-level Cargo.lock) and .git; "" when dir holds nothing.
func treeHash(dir string) string {
	var files []string
	top := filepath.Clean(dir)
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && (info.Name() == "target" || info.Name() == ".git") {
			return filepath.SkipDir
		}
		if !info.IsDir() && !(info.Name() == "Cargo.lock" && filepath.Dir(p) == top) {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return ""
		}
		rel, _ := filepath.Rel(dir, f)
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		h.Write([]byte(norm(string(b))))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// binLock pins vendor/bin/*.exe by sha256, the source trees of the tools built here, and the Go toolchain.
type binLock struct {
	Binaries map[string]string `json:"binaries"`
	Sources  map[string]string `json:"sources,omitempty"`
	Go       string            `json:"go,omitempty"`
}

// dirHash is sha256 over the sorted (path, bytes) of every .go file under dir, "" when there are none.
func dirHash(dir string) string {
	var files []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return ""
		}
		rel, _ := filepath.Rel(dir, f)
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		h.Write([]byte(norm(string(b))))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

var (
	root          = flag.String("root", "..", "repository root (holds deploy/ and vendor/)")
	writeBinaries = flag.Bool("write-binaries", false, "record sha256 of vendor/bin/*.exe into deploy/binaries.lock")
	modulesOnly   = flag.Bool("modules-only", false, "check only the \"modules\" pins (go.mod, go.sum, module cache), not the binaries")
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func norm(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// hash1 is go.sum's "h1:" hash (golang.org/x/mod/sumdb/dirhash.Hash1): sha256 over the sorted lines
// "<sha256 of the file>  <name>\n".
func hash1(names []string, open func(string) ([]byte, error)) (string, error) {
	names = append([]string(nil), names...)
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		if strings.Contains(n, "\n") {
			return "", fmt.Errorf("file name with a newline: %q", n)
		}
		b, err := open(n)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%x  %s\n", sha256.Sum256(b), n)
	}
	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// hashModDir is the h1 of an extracted module directory, its files named "<path>@<version>/<file>" as in the zip.
func hashModDir(dir, prefix string) (string, error) {
	var names []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		names = append(names, prefix+"/"+filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hash1(names, func(n string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(n, prefix+"/"))))
	})
}

// hashModZip is the h1 of a module zip.
func hashModZip(file string) (string, error) {
	z, err := zip.OpenReader(file)
	if err != nil {
		return "", err
	}
	defer z.Close()
	byName := map[string]*zip.File{}
	var names []string
	for _, f := range z.File {
		byName[f.Name] = f
		names = append(names, f.Name)
	}
	return hash1(names, func(n string) ([]byte, error) {
		rc, err := byName[n].Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	})
}

// escape is the module cache's case encoding: an upper-case letter becomes '!' and the letter in lower case.
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if 'A' <= r && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// replaces lists a go.mod's replace directives as "old[ oldVersion] => new[ newVersion]", from both the one-line and
// the block form.
func replaces(gomod string) []string {
	var out []string
	block := false
	for _, line := range strings.Split(norm(gomod), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case block && f[0] == ")":
			block = false
		case block:
			out = append(out, strings.Join(f, " "))
		case f[0] == "replace(" || (f[0] == "replace" && len(f) == 2 && f[1] == "("):
			block = true
		case f[0] == "replace":
			out = append(out, strings.Join(f[1:], " "))
		}
	}
	return out
}

// moduleLine is the path a go.mod's module directive names.
func moduleLine(gomod string) string {
	for _, line := range strings.Split(norm(gomod), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "module" {
			return strings.Trim(f[1], "\"")
		}
	}
	return ""
}

// goSum maps "<path> <version>" (the version ending in /go.mod for a go.mod hash) to its hash, and lists any key that
// appears with two different hashes.
func goSum(s string) (map[string]string, []string) {
	sums, dups := map[string]string{}, []string(nil)
	for _, line := range strings.Split(norm(s), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		k := f[0] + " " + f[1]
		if old, ok := sums[k]; ok && old != f[2] {
			dups = append(dups, k)
		}
		sums[k] = f[2]
	}
	return sums, dups
}

// checkModules applies the "modules" pins (GAP-017). withBinaries also checks the build info of the listed binaries.
func checkModules(lk lock, withBinaries bool, finding func(string, ...interface{})) {
	if len(lk.Modules) == 0 {
		return
	}
	cacheRoot := ""
	if out, err := exec.Command("go", "env", "GOMODCACHE").Output(); err == nil {
		cacheRoot = strings.TrimSpace(string(out))
	}
	names := make([]string, 0, len(lk.Modules))
	for n := range lk.Modules {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		mp := lk.Modules[name]
		dir := filepath.Join(*root, filepath.FromSlash(name))
		gm, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			finding("%s: go.mod unreadable: %v", name, err)
			continue
		}
		want := map[string]bool{}
		for _, p := range mp.Replace {
			want[p.Old+" => "+p.New+" "+p.Version] = true
			if len(p.Commit) < 12 || !strings.HasSuffix(p.Version, "-"+p.Commit[:12]) {
				finding("vendor.lock: %s %s does not name commit %s", p.New, p.Version, p.Commit)
			}
		}
		got := map[string]bool{}
		for _, r := range replaces(string(gm)) {
			got[r] = true
			if !want[r] {
				finding("%s/go.mod: replace %q is not a pinned one", name, r)
			}
		}
		for r := range want {
			if !got[r] {
				finding("%s/go.mod: the pinned replace %q is missing", name, r)
			}
		}
		gs, err := os.ReadFile(filepath.Join(dir, "go.sum"))
		if err != nil {
			finding("%s: go.sum unreadable: %v", name, err)
			continue
		}
		sums, dups := goSum(string(gs))
		for _, d := range dups {
			finding("%s/go.sum: %s is listed with two different hashes", name, d)
		}
		for _, p := range mp.Replace {
			k := p.New + " " + p.Version
			if sums[k] != p.Sum {
				finding("%s/go.sum: %s is %q, pinned %s", name, k, sums[k], p.Sum)
			}
			if sums[k+"/go.mod"] != p.GoModSum {
				finding("%s/go.sum: %s/go.mod is %q, pinned %s", name, k, sums[k+"/go.mod"], p.GoModSum)
			}
			for other := range sums {
				if strings.HasPrefix(other, p.New+" ") && other != k && other != k+"/go.mod" {
					finding("%s/go.sum: another version of the pinned replacement: %s", name, other)
				}
			}
		}
		// the module cache: what the build actually compiles
		checked := 0
		if cacheRoot == "" {
			fmt.Printf("%s: go env GOMODCACHE failed, so the module cache is not checked\n", name)
		} else {
			keys := make([]string, 0, len(sums))
			for k := range sums {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				path, ver, _ := strings.Cut(k, " ")
				dl := filepath.Join(cacheRoot, "cache", "download", filepath.FromSlash(escape(path)), "@v")
				if v, ok := strings.CutSuffix(ver, "/go.mod"); ok {
					if b, err := os.ReadFile(filepath.Join(dl, escape(v)+".mod")); err == nil {
						if h, _ := hash1([]string{"go.mod"}, func(string) ([]byte, error) { return b, nil }); h != sums[k] {
							finding("module cache: %s %s/go.mod hashes to %s, go.sum says %s", path, v, h, sums[k])
						}
						checked++
					}
					continue
				}
				// the extracted directory is what a build compiles; the zip only matters when there is none, because go
				// extracts it again then
				if d := filepath.Join(cacheRoot, filepath.FromSlash(escape(path)+"@"+escape(ver))); isDir(d) {
					if h, err := hashModDir(d, path+"@"+ver); err != nil || h != sums[k] {
						finding("module cache: %s %s (extracted) hashes to %s (%v), go.sum says %s", path, ver, h, err, sums[k])
					}
					checked++
				} else if z := filepath.Join(dl, escape(ver)+".zip"); isFile(z) {
					if h, err := hashModZip(z); err != nil || h != sums[k] {
						finding("module cache: %s %s (zip) hashes to %s (%v), go.sum says %s", path, ver, h, err, sums[k])
					}
					checked++
				}
			}
			for _, p := range mp.Replace {
				dl := filepath.Join(cacheRoot, "cache", "download", filepath.FromSlash(escape(p.New)), "@v")
				b, err := os.ReadFile(filepath.Join(dl, escape(p.Version)+".info"))
				if err != nil {
					fmt.Printf("%s: %s %s is not in the module cache yet; go checks it against go.sum when it downloads it\n", name, p.New, p.Version)
					continue
				}
				var info struct {
					Origin struct{ URL, Hash string }
				}
				if json.Unmarshal(b, &info) == nil && info.Origin.Hash != "" && info.Origin.Hash != p.Commit {
					finding("module cache: %s %s came from commit %s (%s), pinned %s", p.New, p.Version, info.Origin.Hash, info.Origin.URL, p.Commit)
				}
			}
		}
		fmt.Printf("%s: %d replace(s) as pinned (%s); go.sum agrees; %d module-cache copies hash to go.sum\n", name, len(mp.Replace), pinList(mp.Replace), checked)
		if !withBinaries {
			continue
		}
		for _, b := range mp.Binaries {
			bi, err := buildinfo.ReadFile(filepath.Join(*root, filepath.FromSlash(b)))
			if err != nil {
				fmt.Printf("%s: build info unreadable (%v), not checked\n", b, err)
				continue
			}
			if m := moduleLine(string(gm)); bi.Main.Path != m {
				finding("%s: main module %s, not %s's %s", b, bi.Main.Path, name, m)
			}
			for _, d := range bi.Deps {
				if d.Replace != nil {
					ok := false
					for _, p := range mp.Replace {
						ok = ok || (p.Old == d.Path && p.New == d.Replace.Path && p.Version == d.Replace.Version && p.Sum == d.Replace.Sum)
					}
					if !ok {
						finding("%s: built with %s => %s %s %s, not the pinned replace", b, d.Path, d.Replace.Path, d.Replace.Version, d.Replace.Sum)
					}
				} else if s := sums[d.Path+" "+d.Version]; s == "" || s != d.Sum {
					finding("%s: built with %s %s %s, which %s/go.sum does not list with that hash", b, d.Path, d.Version, d.Sum, name)
				}
			}
			fmt.Printf("%s: %d modules in its build info, each go.sum's or the pinned replace\n", b, len(bi.Deps))
		}
	}
}

func pinList(ps []replacePin) string {
	var s []string
	for _, p := range ps {
		s = append(s, p.New+"@"+p.Commit[:min(12, len(p.Commit))])
	}
	return strings.Join(s, ", ")
}

func isDir(p string) bool  { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
func isFile(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }

func main() {
	flag.Parse()
	b, err := os.ReadFile(filepath.Join(*root, "deploy", "vendor.lock"))
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	var lk lock
	if err := json.Unmarshal(b, &lk); err != nil {
		fmt.Println("vendor.lock:", err)
		os.Exit(2)
	}
	findings := 0
	finding := func(f string, a ...interface{}) { findings++; fmt.Printf("FINDING: "+f+"\n", a...) }
	if *modulesOnly {
		checkModules(lk, false, finding)
		if findings > 0 {
			fmt.Printf("%d finding(s)\n", findings)
			os.Exit(1)
		}
		fmt.Println("module pins match deploy/vendor.lock")
		return
	}
	names := make([]string, 0, len(lk.Repos))
	for n := range lk.Repos {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r := lk.Repos[name]
		dir := filepath.Join(*root, name)
		head, err := git(dir, "rev-parse", "HEAD")
		if err != nil {
			finding("%s: not a git checkout (%v)", name, err)
			continue
		}
		head = strings.TrimSpace(head)
		if head != r.Commit {
			finding("%s: HEAD %s, pinned %s", name, head, r.Commit)
		}
		url, _ := git(dir, "remote", "get-url", "origin")
		if strings.TrimSpace(url) != r.URL {
			finding("%s: origin %q, pinned %q", name, strings.TrimSpace(url), r.URL)
		}
		// --ignored: a file matching the checkout's own .gitignore (a planted binary, a patched vendor package) must
		// not hide from this check; it has to sit under a listed directory like any untracked file
		st, err := git(dir, "status", "--porcelain", "--untracked-files=all", "--ignored")
		if err != nil {
			finding("%s: git status failed: %v", name, err)
			continue
		}
		counts := map[string]int{}
		for _, line := range strings.Split(st, "\n") {
			if len(line) < 4 {
				continue
			}
			code, path := strings.TrimSpace(line[:2]), strings.Trim(strings.TrimSpace(line[3:]), "\"")
			switch code {
			case "M", "MM", "AM":
				patch, ok := r.Modified[path]
				if !ok {
					finding("%s: modified tracked file not in the lock: %s", name, path)
					continue
				}
				diff, err := git(dir, "diff", "HEAD", "--ignore-cr-at-eol", "--", path)
				if err != nil {
					finding("%s: diff %s: %v", name, path, err)
					continue
				}
				want, err := os.ReadFile(filepath.Join(*root, filepath.FromSlash(patch)))
				if err != nil {
					finding("%s: stored patch %s unreadable: %v", name, patch, err)
					continue
				}
				if norm(diff) != norm(string(want)) {
					finding("%s: %s differs from the stored patch %s", name, path, patch)
				}
				counts["modified"]++
			case "D":
				ok := false
				for _, d := range r.Deleted {
					if strings.EqualFold(d, path) || strings.HasPrefix(path, strings.TrimSuffix(d, "????.md")) {
						ok = true
					}
				}
				if !ok {
					finding("%s: deleted tracked file not in the lock: %s", name, path)
				}
				counts["deleted"]++
			case "??", "!!":
				ok := false
				for _, u := range r.Untracked {
					if strings.HasPrefix(path, u) {
						ok = true
					}
				}
				if !ok {
					finding("%s: %s file outside the allowed directories: %s", name, map[string]string{"??": "untracked", "!!": "ignored"}[code], path)
				}
				counts["untracked"]++
			default:
				finding("%s: unexpected status %q for %s", name, code, path)
			}
		}
		fmt.Printf("%s @ %s: %d modified (patched), %d deleted, %d untracked in allowed dirs\n", name, head[:12], counts["modified"], counts["deleted"], counts["untracked"])
	}
	// modules: what a module-mode build inside a checkout fetches from outside it (GAP-017)
	checkModules(lk, true, finding)
	// crates: ignored source trees that a build uses as path dependencies (GAP-013). An absent tree is not a finding —
	// a clone that never builds the prover has nothing to check — but a present one must be exactly what was pinned.
	cnames := make([]string, 0, len(lk.Crates))
	for n := range lk.Crates {
		cnames = append(cnames, n)
	}
	sort.Strings(cnames)
	for _, name := range cnames {
		c := lk.Crates[name]
		dir := filepath.Join(*root, filepath.FromSlash(name))
		if _, err := os.Stat(dir); err != nil {
			fmt.Printf("%s: not present; rebuild it with %s\n", name, c.Fetch)
			continue
		}
		if _, err := os.Stat(filepath.Join(*root, filepath.FromSlash(c.Patch))); err != nil {
			finding("%s: its patch %s is missing from the repository", name, c.Patch)
		}
		if got := treeHash(dir); got != c.Tree {
			finding("%s: tree %s, pinned %q: it is not %s %s plus %s (compare it with %s -check)", name, got, c.Tree, c.Crate, c.Version, c.Patch, c.Fetch)
		} else {
			fmt.Printf("%s: %s %s plus %s (tree %s)\n", name, c.Crate, c.Version, filepath.Base(c.Patch), got[:12])
		}
	}
	// binaries
	binDir := filepath.Join(*root, "vendor", "bin")
	sums := map[string]string{}
	if entries, err := os.ReadDir(binDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".exe") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(binDir, e.Name()))
			if err != nil {
				continue
			}
			h := sha256.Sum256(data)
			sums[e.Name()] = hex.EncodeToString(h[:])
		}
	}
	// the tools built from this repository: their source tree hash is pinned next to the binary, so a source edit
	// without a rebuild (or a binary nobody can trace to a source) is a finding too
	sources := map[string]string{}
	for _, tool := range []string{"genesistx", "netwallet", "telainstall", "xswdwallet", "simrpc"} {
		if h := dirHash(filepath.Join(*root, "tools", tool)); h != "" {
			sources[tool] = h
		}
	}
	lockPath := filepath.Join(*root, "deploy", "binaries.lock")
	if *writeBinaries {
		b, _ := json.MarshalIndent(binLock{Binaries: sums, Sources: sources, Go: runtime.Version()}, "", "  ")
		if err := os.WriteFile(lockPath, b, 0o644); err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s (%d binaries, %d source trees, %s)\n", lockPath, len(sums), len(sources), runtime.Version())
	} else if b, err := os.ReadFile(lockPath); err == nil {
		var want binLock
		if err := json.Unmarshal(b, &want); err != nil || want.Binaries == nil {
			fmt.Printf("%s is unreadable (%v): refusing to pass\n", lockPath, err)
			os.Exit(2)
		}
		for n, s := range want.Binaries {
			if sums[n] == "" {
				finding("binary missing: %s", n)
			} else if sums[n] != s {
				finding("binary changed since the lock: %s", n)
			}
		}
		for n := range sums {
			if want.Binaries[n] == "" {
				finding("binary not in the lock: %s", n)
			}
		}
		for n, s := range want.Sources {
			if sources[n] != s {
				finding("tools/%s source changed since its binary was pinned: rebuild and -write-binaries", n)
			}
		}
		if want.Go != "" && want.Go != runtime.Version() {
			fmt.Printf("note: binaries were pinned with %s, this is %s (a rebuild would not be byte-identical)\n", want.Go, runtime.Version())
		}
		fmt.Printf("binaries: %d checked against %s (%d source trees)\n", len(want.Binaries), lockPath, len(want.Sources))
	}
	if findings > 0 {
		fmt.Printf("%d finding(s)\n", findings)
		os.Exit(1)
	}
	fmt.Println("vendor tree matches deploy/vendor.lock")
}
