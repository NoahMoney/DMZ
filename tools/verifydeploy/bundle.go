package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"veil/tools/netcfg"
	"veil/tools/verifydeploy/srchash"
)

// The app bundle the DOCs must equal. Two sources:
//
//	-dist <dir>  a bundle as built by `genconfig -dist` (or downloaded as published): every file telainstall would
//	             install from it — all but dotfiles and .png (tools/telainstall/main.go 137) — byte for byte.
//	-app <dir>   the app's source directory, for someone who has only the repository: genconfig's selection (all
//	             but dotfiles, .png, .md and config.js; tools/genconfig/main.go 128) byte for byte, plus config.js
//	             rendered from the record, compared after its first line. manifest.json cannot be rebuilt byte for
//	             byte (config.js's generation time feeds its hashes), so in this mode it is checked by content:
//	             checkManifest, which runs in both modes.
//
// dist/ is not tracked in git and genconfig stamps config.js with the time it ran, so a bundle cannot be
// regenerated identically from the repository; -app is how a third party checks every DOC without one.

type bundle struct {
	mode  string // "dist" or "app"
	dir   string
	files map[string][]byte
	skip  map[string]string // path -> why it is not compared byte for byte
}

func loadBundle(dist, app, network string, rec *netcfg.Network) (*bundle, error) {
	b := &bundle{files: map[string][]byte{}, skip: map[string]string{}}
	var keep func(rel string) bool
	switch {
	case dist != "":
		b.mode, b.dir = "dist", dist
		keep = func(rel string) bool { return !strings.HasPrefix(rel, ".") && !strings.HasSuffix(rel, ".png") }
	case app != "":
		b.mode, b.dir = "app", app
		keep = func(rel string) bool {
			return !strings.HasPrefix(rel, ".") && rel != "config.js" && !strings.HasSuffix(rel, ".png") && !strings.HasSuffix(rel, ".md")
		}
	default:
		return nil, nil
	}
	err := filepath.Walk(b.dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(b.dir, p)
		rel = filepath.ToSlash(rel)
		if !keep(rel) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		b.files[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	if b.mode == "app" {
		b.files["config.js"] = []byte(netcfg.ConfigJS(network, rec, "tools/genconfig -dist"))
		b.skip["config.js"] = "rendered from the record: compared after its first line"
		b.files["manifest.json"] = nil
		b.skip["manifest.json"] = "cannot be rebuilt byte for byte from source: checked by content"
	}
	return b, nil
}

func (v *verifier) checkServedAgainstBundle(served []servedDoc) {
	b, err := loadBundle(v.opts.dist, v.opts.app, v.network, v.rec)
	if err != nil {
		v.r.add("tela", "bundle", "", sFail, "cannot read the bundle: %v", err)
		return
	}
	if b == nil {
		v.r.add("tela", "bundle", "", sInfo, "DOC contents not compared with a bundle: pass -dist <bundle dir> or -app <app source dir>")
		return
	}
	on := map[string]bool{}
	same := 0
	for _, s := range served {
		p := s.info.Path
		on[p] = true
		want, ok := b.files[p]
		if !ok {
			v.r.add("tela", s.ref.Key+" "+p, s.ref.SCID, sFail, "served by the INDEX but not in the %s bundle %s", b.mode, b.dir)
			continue
		}
		switch why := b.skip[p]; {
		case p == "config.js" && why != "":
			if d := diffAfterFirstLine(want, s.info.Content); d != "" {
				v.r.add("tela", s.ref.Key+" "+p, s.ref.SCID, sFail, "differs from the record's rendering: %s", d)
			} else {
				same++
			}
		case why != "":
			same++ // checked elsewhere, as why says
		default:
			if d := diffBytes(want, s.info.Content); d != "" {
				v.r.add("tela", s.ref.Key+" "+p, s.ref.SCID, sFail, "the DOC does not serve the bundle's %s: %s", p, d)
			} else {
				same++
			}
		}
	}
	var missing []string
	for p := range b.files {
		if !on[p] {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	for _, p := range missing {
		v.r.add("tela", p, "", sFail, "in the %s bundle but served by no DOC of the INDEX", b.mode)
	}
	if same == len(served) && len(missing) == 0 {
		note := ""
		if len(b.skip) > 0 {
			var ks []string
			for k, why := range b.skip {
				ks = append(ks, k+" "+why)
			}
			sort.Strings(ks)
			note = " (" + strings.Join(ks, "; ") + ")"
		}
		v.r.add("tela", "bundle", "", sPass, "the INDEX serves exactly the %d files of the %s bundle %s, each byte for byte%s", len(b.files), b.mode, b.dir, note)
	}
}

// diffBytes says how two files differ: sizes, hashes and the first differing byte with its line.
func diffBytes(want, got []byte) string {
	if bytes.Equal(want, got) {
		return ""
	}
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	line := bytes.Count(want[:i], []byte("\n")) + 1
	return fmt.Sprintf("bundle %d bytes sha256 %s, DOC %d bytes sha256 %s; first difference at byte %d (line %d): bundle %q, DOC %q",
		len(want), short(srchash.Sum(string(want))), len(got), short(srchash.Sum(string(got))), i, line, snippet(want, i), snippet(got, i))
}

func snippet(b []byte, i int) string {
	end := i + 40
	if end > len(b) {
		end = len(b)
	}
	if i >= len(b) {
		return "(end of file)"
	}
	return string(b[i:end])
}

// diffAfterFirstLine compares two files ignoring their first line (config.js's "generated by ... on <time>").
func diffAfterFirstLine(want, got []byte) string {
	cut := func(b []byte) []string {
		ls := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		if len(ls) > 0 {
			ls = ls[1:]
		}
		return ls
	}
	w, g := cut(want), cut(got)
	for i := 0; i < len(w) || i < len(g); i++ {
		switch {
		case i >= len(w):
			return fmt.Sprintf("line %d is extra: %q", i+2, g[i])
		case i >= len(g):
			return fmt.Sprintf("line %d is missing: %q", i+2, w[i])
		case w[i] != g[i]:
			return fmt.Sprintf("line %d reads %q, the record gives %q", i+2, g[i], w[i])
		}
	}
	return ""
}
