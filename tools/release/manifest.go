package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"veil/tools/verifydeploy/srchash"
)

const schema = "dmz-release-manifest/1"

// Manifest is what a release was built from and what it produced. It carries no time and no path, so two builds
// of the same source write the same bytes.
type Manifest struct {
	Schema      string                       `json:"schema"`
	Go          string                       `json:"go"`                    // go env GOVERSION of the toolchain that built
	Commit      string                       `json:"commit"`                // git rev-parse HEAD of the checkout ("" outside git)
	Dirty       bool                         `json:"dirty"`                 // the checkout differed from Commit anywhere
	DirtyInputs []string                     `json:"dirtyInputs,omitempty"` // build inputs and contracts that differ from Commit
	Build       BuildSpec                    `json:"build"`
	Binaries    []Binary                     `json:"binaries"`
	Absent      []Absent                     `json:"absent,omitempty"`
	Contracts   []srchash.Contract           `json:"contracts"` // canonSha256 is what tools/verifydeploy compares with the chain
	Dist        map[string]map[string]string `json:"dist,omitempty"`
}

type BuildSpec struct {
	Command string   `json:"command"`
	Env     []string `json:"env"`
	Module  string   `json:"module"`
	Inputs  []string `json:"inputs"` // repository directories the binaries are compiled from
}

type Binary struct {
	Name    string `json:"name"`
	Package string `json:"package"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
	File    string `json:"file"`
	Sha256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

type Absent struct {
	Name    string `json:"name"`
	Package string `json:"package"`
	Reason  string `json:"reason"`
}

func (m *Manifest) sort() {
	sort.Slice(m.Binaries, func(i, j int) bool { return m.Binaries[i].File < m.Binaries[j].File })
	sort.Slice(m.Contracts, func(i, j int) bool { return m.Contracts[i].File < m.Contracts[j].File })
	sort.Strings(m.DirtyInputs)
}

func (m *Manifest) Write(path string) error {
	m.sort()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func ReadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if m.Schema != schema {
		return nil, fmt.Errorf("%s: schema %q, this tool reads %q", path, m.Schema, schema)
	}
	return &m, nil
}

type Result struct {
	Status string // PASS, FAIL, WARN
	What   string
}

// Compare says, hash by hash, whether a rebuild (got) reproduces a manifest (want).
func Compare(want, got *Manifest) []Result {
	var out []Result
	// pass and fail share one argument list; the pass message uses only its first verbs' worth of it
	add := func(ok bool, pass, fail string, a ...interface{}) {
		if ok {
			out = append(out, Result{"PASS", fmt.Sprintf(pass, a[:strings.Count(pass, "%")]...)})
		} else {
			out = append(out, Result{"FAIL", fmt.Sprintf(fail, a...)})
		}
	}
	add(want.Go == got.Go, "go version %s", "go version: manifest %s, this toolchain %s — binaries will differ; build with the manifest's version", want.Go, got.Go)
	switch {
	case want.Commit == "" || got.Commit == "":
		out = append(out, Result{"WARN", fmt.Sprintf("commit not comparable (manifest %q, checkout %q)", want.Commit, got.Commit)})
	case want.Commit != got.Commit:
		out = append(out, Result{"WARN", fmt.Sprintf("commit: manifest %s, checkout %s (the hashes below decide)", want.Commit, got.Commit)})
	default:
		out = append(out, Result{"PASS", "commit " + want.Commit})
	}
	if len(want.DirtyInputs) > 0 {
		out = append(out, Result{"WARN", fmt.Sprintf("the manifest was built from uncommitted changes to %d build inputs or contracts: its binaries are reproducible only from that working tree, not from the commit", len(want.DirtyInputs))})
	}
	gb := map[string]Binary{}
	for _, b := range got.Binaries {
		gb[b.File] = b
	}
	for _, b := range want.Binaries {
		g, ok := gb[b.File]
		switch {
		case !ok:
			out = append(out, Result{"FAIL", fmt.Sprintf("%s: in the manifest, not produced by the rebuild", b.File)})
		default:
			add(g.Sha256 == b.Sha256, "%s sha256 %s", "%s: manifest sha256 %s, rebuilt %s", b.File, b.Sha256, g.Sha256)
		}
		delete(gb, b.File)
	}
	for f := range gb {
		out = append(out, Result{"FAIL", fmt.Sprintf("%s: produced by the rebuild, absent from the manifest", f)})
	}
	wa, ga := map[string]string{}, map[string]string{}
	for _, a := range want.Absent {
		wa[a.Name] = a.Reason
	}
	for _, a := range got.Absent {
		ga[a.Name] = a.Reason
	}
	for n := range wa {
		if _, ok := ga[n]; !ok {
			out = append(out, Result{"FAIL", fmt.Sprintf("%s: absent in the manifest, built now", n)})
		}
	}
	wc := map[string]srchash.Contract{}
	for _, c := range want.Contracts {
		wc[c.File] = c
	}
	for _, c := range got.Contracts {
		w, ok := wc[c.File]
		if !ok {
			out = append(out, Result{"FAIL", fmt.Sprintf("contracts/%s: not in the manifest", c.File)})
			continue
		}
		add(w.CanonSha256 == c.CanonSha256, "contracts/%s canonical sha256 %s", "contracts/%s: manifest canonical sha256 %s, checkout %s", c.File, w.CanonSha256, c.CanonSha256)
		delete(wc, c.File)
	}
	for f := range wc {
		out = append(out, Result{"FAIL", fmt.Sprintf("contracts/%s: in the manifest, missing from the checkout", f)})
	}
	for net, files := range want.Dist {
		for rel, sum := range files {
			g, ok := got.Dist[net][rel]
			switch {
			case !ok:
				out = append(out, Result{"FAIL", fmt.Sprintf("dist/%s/%s: in the manifest, missing here", net, rel)})
			default:
				add(g == sum, "dist/%s/%s sha256 %s", "dist/%s/%s: manifest %s, here %s", net, rel, sum, g)
			}
		}
	}
	for net, files := range got.Dist {
		for rel := range files {
			if _, ok := want.Dist[net][rel]; !ok {
				out = append(out, Result{"FAIL", fmt.Sprintf("dist/%s/%s: here, absent from the manifest", net, rel)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Status == "FAIL" && out[j].Status != "FAIL" })
	return out
}
