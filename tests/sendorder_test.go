package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The launch.bas drain was not an arithmetic mistake. buyto refunded a caller's overshoot at line 59 and only
// then checked min_out at line 60, and the ...To wrapper turned that ordering into a double payment: the
// entrypoint returned 1, refundto paid ASSETVALUE(q) — still the whole deposit, because the DVM never
// decrements Assets on a send — and the wrapper returned 0, so both transfers committed. 2,052,806 taken from a
// 2,150,000 pot on the real DVM.
//
// Nothing in the fuzz suite could see it, because it is not a property of the maths; it is a property of
// statement order. So this test reads control flow instead: from every external send, can execution still reach
// a non-zero RETURN? If it can, that value can be paid twice the moment the function gains a ...To wrapper.
// pool.bas AddLiquidity had the same shape and was safe only because no wrapper existed yet.
func TestNoExternalSendBeforeAFailureExit(t *testing.T) {
	// Helpers whose return value is a result rather than a revert. factory.bas reg returns 1 on SUCCESS and its
	// three callers read it as `IF reg(scid) == 0 THEN GOTO 900`, so its non-zero RETURN reverts nothing.
	allowed := map[string]string{
		"factory.bas:reg": "returns 1 on success; callers test `reg(scid) == 0` for failure",
	}
	files, err := filepath.Glob("../contracts/*.bas")
	if err != nil || len(files) == 0 {
		t.Fatalf("no contracts: %v", err)
	}
	num := regexp.MustCompile(`^(\d+) (.*)$`)
	goTo := regexp.MustCompile(`GOTO (\d+)`)
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(file)
		fn, body := "", map[int]string{}
		check := func() {
			if fn == "" || len(body) == 0 {
				return
			}
			nums := make([]int, 0, len(body))
			for n := range body {
				nums = append(nums, n)
			}
			sort.Ints(nums)
			next := map[int]int{}
			for i := 0; i+1 < len(nums); i++ {
				next[nums[i]] = nums[i+1]
			}
			succ := func(n int) []int {
				txt := strings.TrimSpace(body[n])
				var out []int
				for _, m := range goTo.FindAllStringSubmatch(txt, -1) {
					v, _ := strconv.Atoi(m[1])
					out = append(out, v)
				}
				// an unconditional GOTO or a RETURN ends the fall-through
				if !strings.HasPrefix(txt, "GOTO ") && !strings.HasPrefix(txt, "RETURN") {
					if v, ok := next[n]; ok {
						out = append(out, v)
					}
				}
				return out
			}
			fails := func(n int) bool {
				txt := strings.TrimSpace(body[n])
				if !strings.HasPrefix(txt, "RETURN ") {
					return false
				}
				return strings.TrimSpace(strings.TrimPrefix(txt, "RETURN")) != "0"
			}
			for _, n := range nums {
				if !strings.Contains(body[n], "SEND_ASSET_TO_ADDRESS") && !strings.Contains(body[n], "SEND_DERO_TO_ADDRESS") {
					continue
				}
				seen, stack := map[int]bool{}, succ(n)
				for len(stack) > 0 {
					c := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					if seen[c] {
						continue
					}
					seen[c] = true
					if fails(c) {
						key := base + ":" + fn
						if why, ok := allowed[key]; ok {
							t.Logf("%s: send at %d reaches a non-zero RETURN at %d — allowed: %s", key, n, c, why)
							break
						}
						t.Errorf("%s %s: line %d sends value out and can still reach the non-zero RETURN at %d.\n"+
							"  %d %s\n"+
							"  A ...To wrapper commits on a zero return, so this amount is paid once here and again by the\n"+
							"  refund path. Record it and send it past every exit, as buyto and AddLiquidity now do.",
							base, fn, n, c, n, strings.TrimSpace(body[n]))
						break
					}
					stack = append(stack, succ(c)...)
				}
			}
		}
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSuffix(l, "\r")
			if strings.HasPrefix(l, "Function ") {
				check()
				fn = strings.SplitN(strings.TrimPrefix(l, "Function "), "(", 2)[0]
				body = map[int]string{}
				continue
			}
			if m := num.FindStringSubmatch(l); m != nil && fn != "" {
				n, _ := strconv.Atoi(m[1])
				body[n] = m[2]
			}
		}
		check()
	}
}

// DVM-3 (FINDINGS_2026-09-10). The DVM checks a call's transfer destinations against the account registry but keeps
// only the LAST lookup's error (vendor/derohe/dvm/simulator.go SanityCheckExternalTransfers, 227-238). An unregistered
// destination anywhere earlier passes the check; ProcessExternal commits the state and then panics at that transfer
// (356-359), and the node keeps the block, so that transfer and every one after it are destroyed. That is how nfa.bas
// destroyed a seller's whole payout (LIV-01). A contract cannot ask whether an account exists; it can only know that
// an address signed a transaction. So this test reads every contract and fails when, on any path through a function
// and the functions it calls, a transfer to a destination not known to have signed is followed by a transfer to any
// other destination, or sits in a loop that can send again (to what may be another address).
//
// "Known to have signed" is decided mechanically, as a greatest fixpoint over the file: SIGNER() is; a local is if
// every LET that assigns it is; a parameter is if every call site passes one that is (an entrypoint's arguments come
// from the caller, so they are not); LOAD("k") with a literal key is if every STORE("k", v) in the file stores one that
// is. Anything else — ADDRESS_RAW of an argument, a LOAD of a computed key, a treasury given at install — is not.
// Deliberate exceptions go in allowed, with the reason they are safe.
func TestNoUnverifiedPayeeBeforeTheLastTransfer(t *testing.T) {
	allowed := map[string]string{
		"founders.bas:PayBatch:ADDRESS_RAW(a)": "a is h_<i>, written only by Register (35) as ADDRESS_STRING(SIGNER()) and moved only by remove (24) from another h_<j>: every holder the loop pays has signed Register",
	}
	files, err := filepath.Glob("../contracts/*.bas")
	if err != nil || len(files) == 0 {
		t.Fatalf("no contracts: %v", err)
	}
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range newPayeeCheck(filepath.Base(file), string(b), allowed).violations() {
			t.Error(v)
		}
	}
	for k, why := range allowed {
		t.Logf("allowed %s: %s", k, why)
	}
}

// The checker itself, on the three shapes it exists for: an unverified payee paid before a signer (the LIV-01 shape,
// as nfa.bas paid the charity before the owner), an unverified payee paid in a loop, and the safe order.
func TestPayeeCheckerFlagsTheLIV01Shape(t *testing.T) {
	src := `Function InitializePrivate(c String) Uint64
10 STORE("charity", ADDRESS_RAW(c))
20 RETURN 0
End Function
Function Settle() Uint64
10 SEND_ASSET_TO_ADDRESS(LOAD("charity"), 1, SCID())
20 pay(SIGNER())
30 RETURN 0
End Function
Function pay(to String) Uint64
10 SEND_ASSET_TO_ADDRESS(to, 1, SCID())
20 RETURN 0
End Function
Function Batch(n Uint64) Uint64
10 DIM i as Uint64
20 LET i = 0
30 IF i >= n THEN GOTO 100
40 SEND_ASSET_TO_ADDRESS(ADDRESS_RAW(LOAD("h_" + i)), 1, SCID())
50 LET i = i + 1
60 GOTO 30
100 RETURN 0
End Function
Function Fine() Uint64
10 DIM s as String
20 LET s = SIGNER()
30 pay(s)
40 SEND_ASSET_TO_ADDRESS(LOAD("charity"), 1, SCID())
50 RETURN 0
End Function
`
	got := newPayeeCheck("synthetic.bas", src, nil).violations()
	has := func(fn string) bool {
		for _, v := range got {
			if strings.HasPrefix(v, "synthetic.bas "+fn+":") {
				return true
			}
		}
		return false
	}
	if !has("Settle") || !has("Batch") || has("Fine") || has("pay") || len(got) != 2 {
		t.Fatalf("the checker found %d violations: %q", len(got), got)
	}
}

type basFn struct {
	params []string
	body   map[int]string
	nums   []int
}

type basCall struct {
	name string
	args []string
}

type storeSite struct{ fn, key, val string }

type callSite struct {
	caller string
	args   []string
}

type payEvent struct {
	expr, owner, id string
	verified         bool
}

var (
	basLine  = regexp.MustCompile(`^(\d+)\s+(.*)$`)
	callName = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\(`)
	loadLit  = regexp.MustCompile(`^LOAD\("([^"]*)"\)$`)
	strLit   = regexp.MustCompile(`^"([^"]*)"$`)
	identRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	letLine  = regexp.MustCompile(`^LET\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+)$`)
	gotoRe   = regexp.MustCompile(`GOTO (\d+)`)
)

func parseBas(src string) map[string]*basFn {
	fns := map[string]*basFn{}
	var cur *basFn
	for _, l := range strings.Split(src, "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.HasPrefix(l, "Function "):
			h := strings.TrimPrefix(l, "Function ")
			open, shut := strings.Index(h, "("), strings.LastIndex(h, ")")
			if open < 0 || shut < open {
				cur = nil
				continue
			}
			cur = &basFn{body: map[int]string{}}
			for _, p := range splitTop(h[open+1 : shut]) {
				if f := strings.Fields(p); len(f) > 0 {
					cur.params = append(cur.params, f[0])
				}
			}
			fns[h[:open]] = cur
		case strings.HasPrefix(l, "End Function"):
			cur = nil
		case cur != nil:
			if m := basLine.FindStringSubmatch(l); m != nil {
				n, _ := strconv.Atoi(m[1])
				cur.body[n] = strings.TrimSpace(m[2])
			}
		}
	}
	for _, f := range fns {
		for n := range f.body {
			f.nums = append(f.nums, n)
		}
		sort.Ints(f.nums)
	}
	return fns
}

// splitTop splits s at the commas outside parentheses and string literals.
func splitTop(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	depth, inq, start := 0, false, 0
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == '"':
			inq = !inq
		case inq:
		case ch == '(':
			depth++
		case ch == ')':
			depth--
		case ch == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// callsIn returns every call in a statement in textual order, with its top-level arguments.
func callsIn(line string) []basCall {
	var out []basCall
	for _, m := range callName.FindAllStringSubmatchIndex(line, -1) {
		if strings.Count(line[:m[0]], `"`)%2 == 1 {
			continue // inside a string literal
		}
		open, end, depth, inq := m[1]-1, -1, 0, false
		for i := open; i < len(line) && end < 0; i++ {
			switch ch := line[i]; {
			case ch == '"':
				inq = !inq
			case inq:
			case ch == '(':
				depth++
			case ch == ')':
				depth--
				if depth == 0 {
					end = i
				}
			}
		}
		if end > open {
			out = append(out, basCall{name: line[m[2]:m[3]], args: splitTop(line[open+1 : end])})
		}
	}
	return out
}

type payeeCheck struct {
	file    string
	fns     map[string]*basFn
	allowed map[string]string
	keyV    map[string]bool
	paramV  map[string][]bool
	lets    map[string]map[string][]string
	stores  []storeSite
	sites   map[string][]callSite
	memo    map[string][]payEvent
}

func newPayeeCheck(file, src string, allowed map[string]string) *payeeCheck {
	p := &payeeCheck{file: file, fns: parseBas(src), allowed: allowed, keyV: map[string]bool{}, paramV: map[string][]bool{},
		lets: map[string]map[string][]string{}, sites: map[string][]callSite{}, memo: map[string][]payEvent{}}
	for name, f := range p.fns {
		p.lets[name] = map[string][]string{}
		p.paramV[name] = make([]bool, len(f.params))
		for i := range f.params {
			p.paramV[name][i] = true
		}
		for _, n := range f.nums {
			line := f.body[n]
			if m := letLine.FindStringSubmatch(line); m != nil {
				p.lets[name][m[1]] = append(p.lets[name][m[1]], strings.TrimSpace(m[2]))
			}
			for _, cl := range callsIn(line) {
				if cl.name == "STORE" && len(cl.args) == 2 {
					if k := strLit.FindStringSubmatch(cl.args[0]); k != nil {
						p.stores = append(p.stores, storeSite{name, k[1], cl.args[1]})
						p.keyV[k[1]] = true
					}
				}
				if _, ok := p.fns[cl.name]; ok {
					p.sites[cl.name] = append(p.sites[cl.name], callSite{name, cl.args})
				}
			}
		}
	}
	for changed := true; changed; { // greatest fixpoint: start from "everything signed" and strike what is not
		changed = false
		for _, s := range p.stores {
			if p.keyV[s.key] && !p.isV(s.fn, s.val, map[string]bool{}) {
				p.keyV[s.key], changed = false, true
			}
		}
		for name, f := range p.fns {
			for i := range f.params {
				if !p.paramV[name][i] {
					continue
				}
				v := len(p.sites[name]) > 0
				for _, s := range p.sites[name] {
					if i >= len(s.args) || !p.isV(s.caller, s.args[i], map[string]bool{}) {
						v = false
						break
					}
				}
				if !v {
					p.paramV[name][i], changed = false, true
				}
			}
		}
	}
	return p
}

// isV: is expr, evaluated in fn, an address that has signed a transaction?
func (p *payeeCheck) isV(fn, expr string, seen map[string]bool) bool {
	e := strings.TrimSpace(expr)
	if _, ok := p.allowed[p.file+":"+fn+":"+e]; ok {
		return true
	}
	if e == "SIGNER()" {
		return true
	}
	if m := loadLit.FindStringSubmatch(e); m != nil {
		v, ok := p.keyV[m[1]]
		return ok && v
	}
	if !identRe.MatchString(e) {
		return false
	}
	if f := p.fns[fn]; f != nil {
		for i, prm := range f.params {
			if prm == e {
				return p.paramV[fn][i]
			}
		}
	}
	exprs := p.lets[fn][e]
	if len(exprs) == 0 {
		return false
	}
	if seen[fn+":"+e] {
		return true
	}
	seen[fn+":"+e] = true
	for _, x := range exprs {
		if !p.isV(fn, x, seen) {
			return false
		}
	}
	return true
}

func (p *payeeCheck) identity(fn, expr string) string {
	e := strings.TrimSpace(expr)
	if e == "SIGNER()" || loadLit.MatchString(e) {
		return e
	}
	return fn + ":" + e
}

// lineEvents lists the transfers one statement makes, its own and those of every function it calls, with a callee's
// parameter replaced by the argument this statement passes.
func (p *payeeCheck) lineEvents(fn, line string, depth int) []payEvent {
	if depth > 12 {
		return nil
	}
	var out []payEvent
	for _, cl := range callsIn(line) {
		if (cl.name == "SEND_ASSET_TO_ADDRESS" || cl.name == "SEND_DERO_TO_ADDRESS") && len(cl.args) > 0 {
			d := strings.TrimSpace(cl.args[0])
			out = append(out, payEvent{expr: d, owner: fn, id: p.identity(fn, d), verified: p.isV(fn, d, map[string]bool{})})
			continue
		}
		g, ok := p.fns[cl.name]
		if !ok {
			continue
		}
		for _, ev := range p.fnEvents(cl.name, depth+1) {
			if ev.owner == cl.name {
				for i, prm := range g.params {
					if prm == ev.expr && i < len(cl.args) {
						a := strings.TrimSpace(cl.args[i])
						ev = payEvent{expr: a, owner: fn, id: p.identity(fn, a), verified: p.isV(fn, a, map[string]bool{})}
						break
					}
				}
			}
			out = append(out, ev)
		}
	}
	return out
}

func (p *payeeCheck) fnEvents(fn string, depth int) []payEvent {
	if ev, ok := p.memo[fn]; ok {
		return ev
	}
	var out []payEvent
	f := p.fns[fn]
	for _, n := range f.nums {
		out = append(out, p.lineEvents(fn, f.body[n], depth)...)
	}
	p.memo[fn] = out
	return out
}

func basSucc(f *basFn, n int) []int {
	txt := f.body[n]
	var out []int
	for _, m := range gotoRe.FindAllStringSubmatch(txt, -1) {
		v, _ := strconv.Atoi(m[1])
		out = append(out, v)
	}
	if strings.HasPrefix(txt, "GOTO ") || strings.HasPrefix(txt, "RETURN") || strings.HasPrefix(txt, "PANIC") || strings.Contains(txt, "ELSE GOTO") {
		return out
	}
	for i, x := range f.nums {
		if x == n && i+1 < len(f.nums) {
			out = append(out, f.nums[i+1])
		}
	}
	return out
}

func (p *payeeCheck) violations() []string {
	var out []string
	names := make([]string, 0, len(p.fns))
	for name := range p.fns {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f := p.fns[name]
		ev := map[int][]payEvent{}
		for _, n := range f.nums {
			ev[n] = p.lineEvents(name, f.body[n], 0)
		}
		for _, n := range f.nums {
			for k, d := range ev[n] {
				if d.verified {
					continue
				}
				found := ""
				for _, e := range ev[n][k+1:] {
					if e.id != d.id {
						found = "the same statement then sends to " + e.expr
						break
					}
				}
				seen, stack := map[int]bool{}, basSucc(f, n)
				for len(stack) > 0 && found == "" {
					m := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					if seen[m] {
						continue
					}
					seen[m] = true
					if m == n {
						found = "the statement can run again in a loop, where it may name another address"
						break
					}
					for _, e := range ev[m] {
						if e.id != d.id {
							found = fmt.Sprintf("line %d then sends to %s", m, e.expr)
							break
						}
					}
					stack = append(stack, basSucc(f, m)...)
				}
				if found != "" {
					out = append(out, fmt.Sprintf("%s %s: line %d sends to %s, which is not known to have signed a transaction, and %s. "+
						"If that account does not exist the DVM commits the call and destroys that transfer and every one after it "+
						"(DVM-1, LIV-01): pay such an address last, or credit it and let a one-transfer call pay it.",
						p.file, name, n, d.expr, found))
				}
			}
		}
	}
	return out
}
