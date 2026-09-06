// verifyvendor checks every vendored checkout against deploy/vendor.lock: the commit is the pinned one, the only
// modified tracked files are the listed ones and carry exactly the stored patch, the only deleted files are the
// listed ones, and every untracked file sits under a listed directory. With -write-binaries it records sha256 of
// vendor/bin/*.exe into deploy/binaries.lock; otherwise, when that file exists, the binaries are checked too.
//
//	go run ./verifyvendor [-root ..] [-write-binaries]
//
// Exit 1 on any finding. CR at end of line is ignored when comparing patches (Windows checkouts).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
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

type lock struct {
	Repos map[string]repo `json:"repos"`
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
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func norm(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

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
