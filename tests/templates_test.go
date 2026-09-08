package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	ref "github.com/deroproject/derohe/veil/reference"
)

// repoRoot finds the checkout these tests came from. tests/run.sh runs the package from vendor/derohe/veil/tests
// and copies only reference, tests and contracts beside it; app/ stays where it is, four levels up. Walking up
// until both app/templates and contracts are present works in that layout and in place, without assuming either.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, errA := os.Stat(filepath.Join(dir, "app", "templates"))
		_, errC := os.Stat(filepath.Join(dir, "contracts"))
		if errA == nil && errC == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no directory above the tests holds both app/templates and contracts")
		}
		dir = parent
	}
}

// Every test here installs contracts/<k>.bas; the app installs app/templates/<k>.bas after checking it against the
// sha256 in app/config.js. Nothing between those two files ever asserted they were the same file, and once they
// were not: every launch installed code without the private-refund and init fixes the tests had passed
// (PLATFORM_BACKLOG B0.1). This fails the moment a contract is edited without tools/simdeploy rewriting the
// template and the record, for every template config.js names — and config.js must name all four.
func TestShippedTemplatesAreTheTestedContracts(t *testing.T) {
	root := repoRoot(t)
	cfg, err := os.ReadFile(filepath.Join(root, "app", "config.js"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`file: "templates/([a-z]+)\.bas", sha256: "([0-9a-f]{64})"`)
	named := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(cfg), -1) {
		k, want := m[1], m[2]
		named[k] = true
		src, err := os.ReadFile(filepath.Join(root, "contracts", k+".bas"))
		if err != nil {
			t.Fatal(err)
		}
		tpl, err := os.ReadFile(filepath.Join(root, "app", "templates", k+".bas"))
		if err != nil {
			t.Fatal(err)
		}
		if string(tpl) != ref.Canon(string(src)) {
			t.Errorf("%s: app/templates/%s.bas is not Canon(contracts/%s.bas): the app would install code these tests never ran (run tools/simdeploy)", k, k, k)
		}
		h := sha256.Sum256(tpl)
		if got := hex.EncodeToString(h[:]); got != want {
			t.Errorf("%s: app/templates/%s.bas hashes to %s… but config.js verifies %s…: the app would refuse its own template", k, k, got[:16], want[:16])
		}
	}
	for _, k := range []string{"pool", "launch", "nft", "founders"} {
		if !named[k] {
			t.Errorf("app/config.js names no %s template", k)
		}
	}
}
