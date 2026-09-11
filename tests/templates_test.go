package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// goStrings joins every double-quoted Go string literal on one line, so a constant written as a `"a" + "b" +`
// chain reads back as the sentence it will become. It splits on the quote rather than parsing Go: an escaped
// quote would make that wrong, so a line carrying one is refused instead of misread.
func goStrings(t *testing.T, line string) string {
	t.Helper()
	if strings.Contains(line, `\"`) {
		t.Fatalf("this reader cannot parse an escaped quote: %s", line)
	}
	var out strings.Builder
	parts := strings.Split(line, `"`)
	for i := 1; i < len(parts); i += 2 {
		out.WriteString(parts[i])
	}
	return out.String()
}

// toolDefault reads the compiled-in value of `const <name>` out of a Go source file. The test asserts on the
// constant rather than on the file, because the file also quotes the sentence this repair removed.
func toolDefault(t *testing.T, path, name string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	head := "const " + name + " = "
	for i, l := range lines {
		if !strings.HasPrefix(l, head) {
			continue
		}
		var out strings.Builder
		for ; i < len(lines); i++ {
			out.WriteString(goStrings(t, lines[i]))
			if !strings.HasSuffix(strings.TrimSpace(lines[i]), "+") {
				break
			}
		}
		return out.String()
	}
	t.Fatalf("%s declares no const %s", path, name)
	return ""
}

// UX-02. founders.bas stores name and descr once at InitializePrivate and defines no setter, so the description
// simdeploy passes is on chain for the life of the contract and app/nfts.js renders it under the mint button.
// The tool default used to read "Registered holders receive the promised share of every fee the exchange earns,
// paid in DERO to their wallets", and deploy/README.md's mainnet command line did not pass -pass-descr, so
// mainnet would have inherited it. Three claims in it are false and none of them is correctable afterwards: the
// treasury bot sweeps DERO-quoted pool and launch fees only (tools/treasurybot/main.go:130-145), a wrapped-asset
// fee can never be paid out in DERO (founders.bas Deposit reads DEROVALUE only), and no contract obliges the
// treasury to deposit anything — which is what app/notice.js, the page the app declares authoritative, says.
//
// Two things are asserted, because either alone leaves the defect reachable: the default may not carry the two
// claims, and the runbook may not inherit the default.
func TestFoundersPassDescriptionPromisesOnlyWhatTheContractsDeliver(t *testing.T) {
	root := repoRoot(t)
	cannot := []string{"every fee", "paid in DERO"}

	b, err := os.ReadFile(filepath.Join(root, "deploy", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := mainnetSimdeploy(t, string(b))
	for _, f := range []string{"-pass-name", "-pass-descr"} {
		if !strings.Contains(cmd, f) {
			t.Errorf("the mainnet simdeploy command line does not pass %s, so mainnet inherits the tool default of a string no contract can change afterwards:\n%s", f, cmd)
		}
	}
	for _, s := range cannot {
		if strings.Contains(cmd, s) {
			t.Errorf("the mainnet simdeploy command line says %q, which no contract delivers", s)
		}
	}

	descr := toolDefault(t, filepath.Join(root, "tools", "simdeploy", "main.go"), "passDescrDefault")
	if descr == "" {
		t.Fatal("passDescrDefault is empty")
	}
	for _, s := range cannot {
		if strings.Contains(descr, s) {
			t.Errorf("the -pass-descr default still says %q, which no contract delivers: %s", s, descr)
		}
	}
}

// mainnetSimdeploy returns the fenced block of deploy/README.md that holds the mainnet simdeploy invocation.
func mainnetSimdeploy(t *testing.T, doc string) string {
	t.Helper()
	i := strings.Index(doc, "go run ./simdeploy -network mainnet")
	if i < 0 {
		t.Fatal("deploy/README.md has no mainnet simdeploy command line")
	}
	rest := doc[i:]
	if j := strings.Index(rest, "```"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// XMR-1. The vault signer's -backend flag used to default to `sim`, a JSON file it reads deposits from and
// appends releases to, and nothing tied that backend to the simulator: a mainnet signer would have voted Mint on
// rows anyone with write access to that file could add. The guard is unit-tested in
// tools/vaultsigner/backend_guard_repair_test.go; what is checked here is that the runbook an operator follows
// says so, because the previous text ("-backend <sim|zcash|monero|solana>") positively invited the default.
func TestTheMainnetRunbookDoesNotOfferTheFileBackend(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "deploy", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	// No vault is deployed on mainnet, so the runbook's signer step is the paragraph that says there is none to
	// sign for, and it must still refuse the file backend in the words below if a vault is ever installed.
	i := strings.Index(doc, "there is no vault to sign for")
	if i < 0 {
		t.Fatal("deploy/README.md has no signer step")
	}
	step := doc[i:]
	if j := strings.Index(step, "\n4."); j >= 0 {
		step = step[:j]
	}
	flat := strings.Join(strings.Fields(step), " ") // the runbook wraps; the sentences are what matter
	for _, want := range []string{
		"refused on any network but the simulator",
		"no configuration in which a mainnet vault can be signed for today",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the signer step does not say %q:\n%s", want, step)
		}
	}
}
