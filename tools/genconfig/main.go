// genconfig renders deploy/networks.json into app/config.js (for development against one network) or into
// dist/<network>/ bundles ready for telainstall (one TELA install per network).
//
//	go run ./genconfig -network simulator                 # writes ../app/config.js
//	go run ./genconfig -dist                              # builds ../dist/<network>/ for every deployed network
//	go run ./genconfig -dist -network mainnet             # one bundle
//
// A bundle is the app directory with that network's config.js, the contract templates, a manifest.json
// (sha256 per file, gzip size per file, the DOC size limit check) and nothing else.
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"veil/tools/netcfg"
)

var (
	networksPath = flag.String("networks", "../deploy/networks.json", "networks file")
	appDir       = flag.String("app", "../app", "app directory")
	distDir      = flag.String("out", "../dist", "dist root")
	network      = flag.String("network", "", "network name (simulator, testnet, mainnet)")
	dist         = flag.Bool("dist", false, "build dist bundles instead of writing app/config.js")
	maxDoc       = flag.Int("max-doc", 18*1024, "maximum on-chain DOC size (gzip+base64 bytes) per file")
)

func main() {
	flag.Parse()
	f, err := netcfg.Load(*networksPath)
	if err != nil {
		log.Fatal(err)
	}
	if !*dist {
		if *network == "" {
			log.Fatal("-network required")
		}
		n, ok := f.Networks[*network]
		if !ok {
			log.Fatalf("unknown network %q", *network)
		}
		out := filepath.Join(*appDir, "config.js")
		if err := os.WriteFile(out, []byte(netcfg.ConfigJS(*network, n, "tools/genconfig")), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("wrote", out, "for", *network)
		return
	}
	names := netcfg.Names
	if *network != "" {
		names = []string{*network}
	}
	for _, name := range names {
		n, ok := f.Networks[name]
		if !ok {
			log.Fatalf("unknown network %q", name)
		}
		if !n.Deployed() {
			fmt.Printf("%s: contracts not deployed yet (factory/social/founders empty) — no bundle\n", name)
			continue
		}
		if err := bundle(name, n); err != nil {
			log.Fatalf("%s: %v", name, err)
		}
	}
}

type entry struct {
	Sha256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Doc    int    `json:"docBytes"` // gzip+base64 size, what TELA stores
}

func docSize(b []byte) int {
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	w.Write(b)
	w.Close()
	return (buf.Len() + 2) / 3 * 4
}

func bundle(name string, n *netcfg.Network) (err error) {
	out := filepath.Join(*distDir, name)
	// A bundle is on disk only if it was built whole. Whatever was there before goes first, the template record
	// is checked before a byte is written, and a failure anywhere below takes the directory with it. The earlier
	// order wrote all 31 files, then failed the hash check, and left dist/testnet/ looking installable with a
	// config.js that verified none of it — a stale bundle is not a fallback, it is the thing this guards against.
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(out)
		}
	}()
	// the template hashes the bundle ships must be the hashes the network's config verifies against
	for k, t := range n.Templates {
		b, err := os.ReadFile(filepath.Join(*appDir, "templates", k+".bas"))
		if err != nil {
			return fmt.Errorf("template %s missing from app/templates: %w", k, err)
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != t.Sha256 {
			return fmt.Errorf("template %s: app/templates hash %x differs from networks.json %s (redeploy or update the record)", k, h[:8], t.Sha256[:16])
		}
	}
	if err := os.MkdirAll(filepath.Join(out, "templates"), 0o755); err != nil {
		return err
	}
	manifest := map[string]entry{}
	var files []string
	err = filepath.Walk(*appDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(*appDir, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".") || rel == "config.js" || strings.HasSuffix(rel, ".png") || strings.HasSuffix(rel, ".md") {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	tooBig := 0
	add := func(rel string, b []byte) error {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(out, rel)), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, rel), b, 0o644); err != nil {
			return err
		}
		h := sha256.Sum256(b)
		e := entry{Sha256: hex.EncodeToString(h[:]), Bytes: len(b), Doc: docSize(b)}
		manifest[rel] = e
		flag := ""
		if e.Doc > *maxDoc {
			flag = "  TOO BIG for one DOC"
			tooBig++
		}
		fmt.Printf("  %-22s %6d B  doc %6d B%s\n", rel, e.Bytes, e.Doc, flag)
		return nil
	}
	fmt.Printf("%s → %s\n", name, out)
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(*appDir, rel))
		if err != nil {
			return err
		}
		if err := add(rel, b); err != nil {
			return err
		}
	}
	if err := add("config.js", []byte(netcfg.ConfigJS(name, n, "tools/genconfig -dist"))); err != nil {
		return err
	}
	mb, _ := json.MarshalIndent(map[string]interface{}{"network": name, "daemon": n.Daemon, "factory": n.Factory, "social": n.Social, "founders": n.Founders, "vault": n.Vault, "htlc": n.Htlc, "swap": n.Swap, "board": n.Board, "files": manifest}, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), mb, 0o644); err != nil {
		return err
	}
	if tooBig > 0 {
		return fmt.Errorf("%d file(s) exceed the DOC limit", tooBig)
	}
	fmt.Printf("  %d files, manifest.json written\n", len(manifest))
	return nil
}
