package main

import (
	"fmt"
	"sort"
	"strings"
)

// A small reader for DVM-BASIC, enough to split a contract into functions and compare them token by token. It is
// used where a byte comparison is the wrong test: a TELA contract is not installed as its template's text — the
// tela library re-parses the template and prints it again (tela ParseHeaders / FormatSmartContract) — so the
// question "is this the standard TELA-INDEX-1" is answered on tokens, which formatting cannot change. The DMZ
// contracts are compared by exact hash instead (identity.go).

type bline struct {
	No   string   // the statement's DVM line number
	Toks []string // the statement after its number, comments dropped
	Src  int      // 1-based line in the code it was read from
}

type bfunc struct {
	Name   string
	Header []string // tokens of the "Function ..." line
	Lines  []bline
}

type bprog struct {
	Funcs   map[string]*bfunc
	Order   []string
	HasTail bool   // a line starting with "/*" was reached: a TELA DOC's content follows
	Tail    string // from that line to the end of the code
}

func parseBasic(code string) (*bprog, error) {
	p := &bprog{Funcs: map[string]*bfunc{}}
	lines := strings.Split(code, "\n")
	var cur *bfunc
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		if strings.HasPrefix(t, "/*") {
			if cur != nil {
				return nil, fmt.Errorf("line %d: a comment block opens inside function %s", i+1, cur.Name)
			}
			p.HasTail, p.Tail = true, strings.Join(lines[i:], "\n")
			break
		}
		toks, err := tokenize(t)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", i+1, err)
		}
		if len(toks) == 0 {
			continue
		}
		switch {
		case strings.EqualFold(toks[0], "Function"):
			if cur != nil {
				return nil, fmt.Errorf("line %d: Function inside function %s", i+1, cur.Name)
			}
			if len(toks) < 2 {
				return nil, fmt.Errorf("line %d: Function without a name", i+1)
			}
			if _, dup := p.Funcs[toks[1]]; dup {
				return nil, fmt.Errorf("line %d: function %s defined twice", i+1, toks[1])
			}
			cur = &bfunc{Name: toks[1], Header: toks}
			p.Funcs[cur.Name] = cur
			p.Order = append(p.Order, cur.Name)
		case len(toks) == 2 && strings.EqualFold(toks[0], "End") && strings.EqualFold(toks[1], "Function"):
			if cur == nil {
				return nil, fmt.Errorf("line %d: End Function without a function", i+1)
			}
			cur = nil
		default:
			if cur == nil {
				return nil, fmt.Errorf("line %d: statement outside any function: %q", i+1, t)
			}
			cur.Lines = append(cur.Lines, bline{No: toks[0], Toks: toks[1:], Src: i + 1})
		}
	}
	if cur != nil {
		return nil, fmt.Errorf("function %s has no End Function", cur.Name)
	}
	return p, nil
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isIdent(c byte) bool      { return isIdentStart(c) || c >= '0' && c <= '9' }

// tokenize splits one line into string literals (kept with their quotes), identifiers, numbers and operators.
// Whitespace carries no meaning and "//" outside a string ends the line.
func tokenize(s string) ([]string, error) {
	var out []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			return out, nil
		case c == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string in %q", s)
			}
			out = append(out, s[i:j+1])
			i = j + 1
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdent(s[j]) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			out = append(out, s[i:j])
			i = j
		default:
			if i+1 < len(s) {
				switch two := s[i : i+2]; two {
				case "==", "!=", "<=", ">=", "&&", "||", "<<", ">>":
					out = append(out, two)
					i += 2
					continue
				}
			}
			out = append(out, s[i:i+1])
			i++
		}
	}
	return out, nil
}

// unquote strips a string literal's quotes; ok is false for any other token.
func unquote(tok string) (string, bool) {
	if len(tok) >= 2 && tok[0] == '"' && tok[len(tok)-1] == '"' {
		return tok[1 : len(tok)-1], true
	}
	return "", false
}

// sameLines compares two functions' bodies statement by statement: line numbers and tokens. It returns "" when
// they are the same, otherwise a description of the first difference.
func sameLines(want, got *bfunc) string {
	if strings.Join(want.Header, " ") != strings.Join(got.Header, " ") {
		return fmt.Sprintf("declaration is %q, the standard's is %q", strings.Join(got.Header, " "), strings.Join(want.Header, " "))
	}
	for i := 0; i < len(want.Lines) || i < len(got.Lines); i++ {
		switch {
		case i >= len(got.Lines):
			return fmt.Sprintf("missing line %s %s", want.Lines[i].No, strings.Join(want.Lines[i].Toks, " "))
		case i >= len(want.Lines):
			return fmt.Sprintf("extra line %s %s", got.Lines[i].No, strings.Join(got.Lines[i].Toks, " "))
		}
		w, g := want.Lines[i], got.Lines[i]
		if w.No != g.No || strings.Join(w.Toks, "\x00") != strings.Join(g.Toks, "\x00") {
			return fmt.Sprintf("line %s reads %q, the standard's line %s reads %q", g.No, strings.Join(g.Toks, " "), w.No, strings.Join(w.Toks, " "))
		}
	}
	return ""
}

// keySites lists, for every STORE / DELETE / LOAD whose key is a string literal, the functions containing one. A key
// built as "prefix" + something is recorded as "prefix*". Used by the role table's tests to prove the table names
// every function that can write a role's key.
func (p *bprog) keySites(op string) map[string][]string {
	sets := map[string]map[string]bool{}
	for _, name := range p.Order {
		for _, l := range p.Funcs[name].Lines {
			t := l.Toks
			for i := 0; i+2 < len(t); i++ {
				if !strings.EqualFold(t[i], op) || t[i+1] != "(" {
					continue
				}
				k, ok := unquote(t[i+2])
				if !ok {
					continue
				}
				if i+3 < len(t) && t[i+3] == "+" {
					k += "*"
				}
				if sets[k] == nil {
					sets[k] = map[string]bool{}
				}
				sets[k][name] = true
			}
		}
	}
	out := map[string][]string{}
	for k, s := range sets {
		for f := range s {
			out[k] = append(out[k], f)
		}
		sort.Strings(out[k])
	}
	return out
}

// calls reports whether any statement in the program calls fn (e.g. UPDATE_SC_CODE).
func (p *bprog) calls(fn string) bool {
	for _, f := range p.Funcs {
		for _, l := range f.Lines {
			for i, t := range l.Toks {
				if strings.EqualFold(t, fn) && i+1 < len(l.Toks) && l.Toks[i+1] == "(" {
					return true
				}
			}
		}
	}
	return false
}
