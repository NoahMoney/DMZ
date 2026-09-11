package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type status string

const (
	sPass    status = "PASS"
	sFail    status = "FAIL"
	sWarn    status = "WARN"
	sInfo    status = "INFO"
	sAllowed status = "ALLOWED" // a failing role the command line allowed, with the reason it gave
)

// check is one finding. Message says exactly what was compared and, on failure, exactly what differs.
type check struct {
	Section string `json:"section"`
	Subject string `json:"subject"`
	SCID    string `json:"scid,omitempty"`
	Status  status `json:"status"`
	Message string `json:"message"`
}

// roleRow is one privileged role of one deployed contract, read from chain now.
type roleRow struct {
	Contract string   `json:"contract"`
	SCID     string   `json:"scid"`
	Kind     string   `json:"kind"`
	Role     string   `json:"role"`
	ID       string   `json:"id"` // <kind>.<role>, the name -allow takes
	Keys     []string `json:"keys"`
	Policy   policy   `json:"policy"`
	Value    string   `json:"value"`
	Live     bool     `json:"live"`
	State    string   `json:"state"`
	Status   status   `json:"status"`
	Reason   string   `json:"allowedReason,omitempty"`
	Cites    []string `json:"cites"`
}

type report struct {
	Tool       string         `json:"tool"`
	Network    string         `json:"network"`
	Daemon     string         `json:"daemon"`
	Chain      string         `json:"daemonNetwork"`
	Height     int64          `json:"height"`
	Stable     int64          `json:"stableHeight"`
	Contracts  string         `json:"contracts"`
	SourceNote string         `json:"source,omitempty"`
	Checks     []check        `json:"checks"`
	Roles      []roleRow      `json:"roles"`
	Summary    map[status]int `json:"summary"`
}

func (r *report) add(section, subject, scid string, st status, format string, a ...interface{}) {
	r.Checks = append(r.Checks, check{Section: section, Subject: subject, SCID: scid, Status: st, Message: fmt.Sprintf(format, a...)})
}

func (r *report) finish() {
	r.Summary = map[status]int{}
	for _, c := range r.Checks {
		r.Summary[c.Status]++
	}
	for _, x := range r.Roles {
		r.Summary[x.Status]++
	}
}

func (r *report) failed() bool { return r.Summary[sFail] > 0 }

var sections = []struct{ id, title string }{
	{"record", "Deployment record against this repository"},
	{"identity", "Code identity: installed code against reference.Canon(contracts/<kind>.bas)"},
	{"templates", "Market templates: every contract in the factory registry"},
	{"roles", "Roles"},
	{"tela", "TELA: the app's INDEX and DOCs"},
}

func (r *report) writeText(w io.Writer) {
	fmt.Fprintf(w, "DMZ deployment verification (%s)\n", r.Tool)
	fmt.Fprintf(w, "network %s · daemon %s (%s, height %d, stable %d)\n", r.Network, r.Daemon, r.Chain, r.Height, r.Stable)
	fmt.Fprintf(w, "source  %s", r.Contracts)
	if r.SourceNote != "" {
		fmt.Fprintf(w, " · %s", r.SourceNote)
	}
	fmt.Fprintln(w)
	for _, s := range sections {
		var cs []check
		for _, c := range r.Checks {
			if c.Section == s.id {
				cs = append(cs, c)
			}
		}
		if len(cs) == 0 && !(s.id == "roles" && len(r.Roles) > 0) {
			continue
		}
		fmt.Fprintf(w, "\n== %s\n", s.title)
		for _, c := range cs {
			subj := c.Subject
			if c.SCID != "" {
				subj += " " + short(c.SCID)
			}
			lines := strings.Split(c.Message, "\n")
			fmt.Fprintf(w, "%-7s %s: %s\n", c.Status, subj, lines[0])
			for _, l := range lines[1:] {
				fmt.Fprintf(w, "        %s\n", l)
			}
		}
		if s.id == "roles" {
			r.writeRoles(w)
		}
	}
	keys := make([]string, 0, len(r.Summary))
	for k, n := range r.Summary {
		keys = append(keys, fmt.Sprintf("%d %s", n, k))
	}
	sort.Strings(keys)
	verdict := "no failure"
	if r.failed() {
		verdict = "FAILED"
	}
	fmt.Fprintf(w, "\nsummary: %s — %s\n", strings.Join(keys, ", "), verdict)
}

func (r *report) writeRoles(w io.Writer) {
	last := ""
	for _, x := range r.Roles {
		head := x.Contract + " " + x.SCID
		if head != last {
			fmt.Fprintf(w, "%s %s (%s)\n", x.Contract, short(x.SCID), x.Kind)
			last = head
		}
		fmt.Fprintf(w, "  %-7s %-14s %-6s %-9s %s\n", x.Status, x.Role, liveWord(x.Policy, x.Live), x.Policy, x.Value)
		fmt.Fprintf(w, "          %s\n", x.State)
		if x.Reason != "" {
			fmt.Fprintf(w, "          allowed on the command line: %s\n", x.Reason)
		}
		if len(x.Cites) > 0 {
			fmt.Fprintf(w, "          reads: %s\n", strings.Join(x.Cites, "; "))
		}
	}
}

// liveWord is the report's one-word answer to "can this role still act?", worded per policy.
func liveWord(p policy, live bool) string {
	switch p {
	case pRevoke:
		if live {
			return "LIVE"
		}
		return "ended"
	case pGuard:
		if live {
			return "OPEN"
		}
		return "closed"
	case pDesign, pPayee:
		if live {
			return "acts"
		}
		if p == pDesign {
			return "idle"
		}
	}
	return "-"
}

func (r *report) writeJSON(w io.Writer) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(r)
}
