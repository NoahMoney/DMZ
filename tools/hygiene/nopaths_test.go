package hygiene

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A path into someone's home directory names the machine and the person, and it is wrong on every other machine.
// The 2026-09-10 audit found them in committed documents and scripts, and this pass found more in evidence logs and
// in the audit's own findings, so the rule is enforced by the suite rather than by review. It checks every file git
// tracks, plus every untracked file git would add (ignored ones are local by definition). Placeholders such as
// <repo>, <user>, <tmp> and <playwright>, and generic forms such as /c/Users/..., do not match.
var homePath = regexp.MustCompile(`(?i)(c:(?:\\{1,4}|/)users(?:\\{1,4}|/)|/c/users/|/mnt/c/users/|/home/|c--users-)([a-z0-9._]+)`)

func TestNoMachinePathInTheTree(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Skipf("git is not available here, so the tree cannot be listed: %v", err)
	}
	var hits []string
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil || len(b) > 8<<20 || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			continue // deleted in the work tree, too large to be prose, or binary
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range homePath.FindAllStringSubmatch(line, -1) {
				if name := m[2]; name == "..." || strings.Trim(name, ".") == "" {
					continue // a generic form written as an example, not a real directory
				}
				hits = append(hits, f+":"+itoa(i+1)+": "+m[0])
			}
		}
	}
	if len(hits) > 0 {
		t.Errorf("%d machine-specific path(s) in the tree; write <repo>, <user> or <tmp> instead:\n  %s", len(hits), strings.Join(hits, "\n  "))
	}
}

func itoa(n int) string {
	var b []byte
	for {
		b = append([]byte{byte('0' + n%10)}, b...)
		if n /= 10; n == 0 {
			return string(b)
		}
	}
}
