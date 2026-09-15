package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"veil/tools/netcfg"
	"veil/tools/verifydeploy/srchash"
)

// Code identity. What tools/simdeploy installs is reference.Canon(<contracts/kind.bas>) (its read(), line 73), and
// the template hashes it records are sha256 of that same string (its sha(), line 74). The verifier recomputes both
// through srchash, which calls reference.Canon itself, and compares against the exact code DERO.GetSC returns.

// target is one contract the verifier reads.
type target struct {
	Label  string // what the report calls it
	Kind   string // contracts/<Kind>.bas
	SCID   string
	Legacy bool // the record marks it superseded (a legacy vault)
}

// recordContracts is every single-instance contract the per-network record names, as contracts/<label>.bas. The swap
// contracts are here because they settle value: a verifier that checked them only when asked with -extra would pass
// a deployment whose escrows run code this repository does not contain. TestRecordContractsCoverTheRecord holds this
// list to netcfg.Network, so a contract added to the record cannot be left out of it.
func recordContracts(n *netcfg.Network) []struct{ label, scid string } {
	return []struct{ label, scid string }{
		{"factory", n.Factory}, {"social", n.Social}, {"founders", n.Founders}, {"chat", n.Chat},
		{"htlc", n.Htlc}, {"swap", n.Swap}, {"board", n.Board},
		{"rate", n.Rate},
		{"promo", n.Promo},
	}
}

func (v *verifier) targets(extras []target) []target {
	var out []target
	seen := map[string]bool{}
	add := func(t target) {
		if t.SCID == "" || seen[t.SCID] {
			return
		}
		seen[t.SCID] = true
		out = append(out, t)
	}
	n := v.rec
	for _, x := range recordContracts(n) {
		if x.scid == "" {
			v.r.add("identity", x.label, "", sInfo, "the record names no %s on %s", x.label, v.network)
			continue
		}
		add(target{Label: x.label, Kind: x.label, SCID: x.scid})
	}
	for _, vr := range n.Vaults {
		add(target{Label: "vault " + vr.Sym, Kind: "vault", SCID: vr.Scid, Legacy: vr.Legacy})
	}
	add(target{Label: "vault", Kind: "vault", SCID: n.Vault})
	for _, e := range extras {
		if e.Kind != "tela" {
			add(e)
		}
	}
	return out
}

func (v *verifier) checkTarget(t target) {
	srcPath := filepath.Join(v.contracts, t.Kind+".bas")
	b, err := os.ReadFile(srcPath)
	if err != nil {
		v.r.add("identity", t.Label, t.SCID, sFail, "no source to compare with: %v", err)
		return
	}
	src := string(b)
	c, err := v.n.getSC(t.SCID)
	if err != nil {
		v.r.add("identity", t.Label, t.SCID, sFail, "DERO.GetSC: %v", err)
		return
	}
	want, got := srchash.ContractSum(src), srchash.Sum(c.Code)
	matches := want == got
	if matches {
		extra := ""
		if strings.Contains(c.Code, "UPDATE_SC_CODE") {
			extra = "; note: the code calls UPDATE_SC_CODE"
		}
		v.r.add("identity", t.Label, t.SCID, sPass, "installed code sha256 %s = sha256(reference.Canon(contracts/%s.bas)), %d lines%s",
			got, t.Kind, len(srchash.CanonLines(src)), extra)
	} else {
		msg := fmt.Sprintf("installed code sha256 %s ≠ sha256(reference.Canon(contracts/%s.bas)) %s\n%s", got, t.Kind, want, describeDiff(srchash.FirstDiff(src, c.Code), t.Kind))
		st := sFail
		if hv, ok := v.hist.find(t.Kind+".bas", got); ok {
			msg += fmt.Sprintf("\nit is contracts/%s.bas as of commit %s (%s, %q), %s", t.Kind, short(hv.Commit), hv.Date, hv.Subject, hv.form(got))
			if t.Legacy {
				st = sWarn
				msg += "\nthe record marks this contract superseded (legacy): an earlier version, not the current source"
			}
		} else if v.hist.enabled {
			msg += fmt.Sprintf("\nno committed version of contracts/%s.bas hashes to it either (raw or canonical)", t.Kind)
		}
		v.r.add("identity", t.Label, t.SCID, st, "%s", msg)
	}
	rows := evalRoles(t.Label, t.Kind, t.SCID, c.Vars, v.allow)
	if !matches {
		for i := range rows {
			rows[i].State += " [read with contracts/" + t.Kind + ".bas's key layout, but the installed code differs: not authoritative]"
		}
	}
	v.r.Roles = append(v.r.Roles, rows...)
}

func describeDiff(d *srchash.Diff, kind string) string {
	switch {
	case d == nil:
		return "the lines are identical"
	case d.WantEOF && d.GotEOF:
		return "every line is identical; the code differs only in its final newline"
	case d.WantEOF:
		return fmt.Sprintf("the installed code continues after the source ends, from installed line %d: %s", d.Index, d.Got)
	case d.GotEOF:
		return fmt.Sprintf("the installed code ends at installed line %d; the source continues at contracts/%s.bas:%d: %s", d.Index, kind, d.SourceLine, d.Want)
	}
	return fmt.Sprintf("first difference at installed line %d, which is contracts/%s.bas:%d\n  source: %s\n  chain:  %s", d.Index, kind, d.SourceLine, d.Want, d.Got)
}

// checkRecord ties the deployment record to this repository: the template hashes the app accepts must be the
// hashes of these sources (current) or of earlier committed versions of them (legacy).
func (v *verifier) checkRecord() {
	kinds := make([]string, 0, len(v.rec.Templates))
	for k := range v.rec.Templates {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		t := v.rec.Templates[k]
		b, err := os.ReadFile(filepath.Join(v.contracts, k+".bas"))
		if err != nil {
			v.r.add("record", "templates."+k, "", sFail, "the record lists template %s but contracts/%s.bas cannot be read: %v", k, k, err)
			continue
		}
		cur := srchash.ContractSum(string(b))
		if t.Sha256 == cur {
			v.r.add("record", "templates."+k, "", sPass, "sha256 %s = sha256(reference.Canon(contracts/%s.bas))", cur, k)
		} else {
			msg := fmt.Sprintf("the record's current %s template %s is not this repository's contracts/%s.bas (%s)", k, t.Sha256, k, cur)
			if hv, ok := v.hist.find(k+".bas", t.Sha256); ok {
				msg += fmt.Sprintf("; it is the file as of commit %s (%s): the record is behind the source", short(hv.Commit), hv.Date)
			}
			v.r.add("record", "templates."+k, "", sFail, "%s", msg)
		}
		for _, l := range t.Legacy {
			switch hv, ok := v.hist.find(k+".bas", l); {
			case !v.hist.enabled:
				v.r.add("record", "templates."+k+".legacy", "", sInfo, "%s not traced (git history not consulted)", l)
			case ok:
				v.r.add("record", "templates."+k+".legacy", "", sInfo, "%s = contracts/%s.bas as of commit %s (%s), %s", l, k, short(hv.Commit), hv.Date, hv.form(l))
			default:
				v.r.add("record", "templates."+k+".legacy", "", sWarn, "%s is the hash of no committed version of contracts/%s.bas: the app accepts code this repository's history does not contain", l, k)
			}
		}
	}
	for _, k := range []string{"pool", "launch", "nft", "nfa", "founders"} {
		if _, ok := v.rec.Templates[k]; !ok {
			v.r.add("record", "templates."+k, "", sWarn, "the record has no %s template: no %s market can verify in the app", k, k)
		}
	}
}

// templateSet maps every accepted hash to its kind and tier, the way app/feed.js templateHashes() does: the
// current hash and every legacy hash of every template.
type tier struct {
	Kind    string
	Current bool
}

func templateSet(n *netcfg.Network) map[string]tier {
	kinds := make([]string, 0, len(n.Templates))
	for k := range n.Templates {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	m := map[string]tier{}
	for _, k := range kinds {
		t := n.Templates[k]
		for _, l := range t.Legacy {
			if _, dup := m[l]; !dup {
				m[l] = tier{k, false}
			}
		}
		if t.Sha256 != "" {
			m[t.Sha256] = tier{k, true}
		}
	}
	return m
}

// history finds which committed version of a contract a hash belongs to. It reads git only; a checkout without
// git (a source archive) just reports that it could not trace the hash.
type history struct {
	dir     string
	enabled bool
	commit  string
	dirty   bool
	err     error
	cache   map[string][]histVersion
}

type histVersion struct{ Commit, Date, Subject, Raw, Canon string }

// form says whether h is the canonical or the raw-file hash of the version (templates recorded before
// reference.Canon existed hashed the file with its comments).
func (hv histVersion) form(h string) string {
	if h == hv.Canon {
		return "canonical (reference.Canon)"
	}
	return "raw file bytes, comments included (recorded before reference.Canon)"
}

func newHistory(dir string, enabled bool) *history {
	h := &history{dir: dir, enabled: enabled, cache: map[string][]histVersion{}}
	if !enabled {
		return h
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		h.enabled, h.err = false, fmt.Errorf("%s is not in a git checkout, or git is missing (%s)", dir, strings.TrimSpace(string(out)))
		return h
	}
	h.commit = strings.TrimSpace(string(out))
	st, _ := exec.Command("git", "-C", dir, "status", "--porcelain", "--", ".").Output()
	h.dirty = len(bytes.TrimSpace(st)) > 0
	return h
}

func (h *history) describe() string {
	switch {
	case h.err != nil:
		return "git history unavailable: " + h.err.Error()
	case !h.enabled:
		return "git history not consulted (-history=false)"
	case h.dirty:
		return "git " + short(h.commit) + ", contracts/ has uncommitted changes"
	}
	return "git " + short(h.commit) + ", contracts/ clean"
}

func (h *history) versions(file string) []histVersion {
	if vs, ok := h.cache[file]; ok {
		return vs
	}
	var vs []histVersion
	out, err := exec.Command("git", "-C", h.dir, "log", "--format=%H%x09%cs%x09%s", "--", file).Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.SplitN(line, "\t", 3)
			if len(f) != 3 {
				continue
			}
			blob, err := exec.Command("git", "-C", h.dir, "show", f[0]+":./"+file).Output()
			if err != nil {
				continue
			}
			vs = append(vs, histVersion{Commit: f[0], Date: f[1], Subject: f[2], Raw: srchash.Sum(string(blob)), Canon: srchash.ContractSum(string(blob))})
		}
	}
	h.cache[file] = vs
	return vs
}

func (h *history) find(file, hash string) (histVersion, bool) {
	if !h.enabled {
		return histVersion{}, false
	}
	for _, hv := range h.versions(file) {
		if hv.Canon == hash || hv.Raw == hash {
			return hv, true
		}
	}
	return histVersion{}, false
}
