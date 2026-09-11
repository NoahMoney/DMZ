package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	rpc "veil/tools/simrpc"
)

// node is a DERO daemon's JSON-RPC endpoint and the only thing the verifier talks to, through two read-only
// methods: DERO.GetInfo and DERO.GetSC. There is no wallet here, no key and nothing that can sign.
//
// Requests go through simrpc.Call, the repository's one JSON-RPC client. Its GetSC is not used: it exits the
// process on any error, and a verifier must report an unreadable contract as a finding and carry on.
type node struct{ addr string }

type daemonInfo struct {
	Testnet      bool   `json:"testnet"`
	Network      string `json:"network"`
	Height       int64  `json:"height"`
	StableHeight int64  `json:"stableheight"`
	TopoHeight   int64  `json:"topoheight"`
}

func (n node) info() (daemonInfo, error) {
	var r daemonInfo
	err := rpc.Call(n.addr, "DERO.GetInfo", nil, &r)
	return r, err
}

// value is one contract variable exactly as the daemon returned it: a uint64, or a string, which DERO.GetSC always
// hex-encodes (derohe cmd/derod/rpc/rpc_dero_getsc.go, "%x" of the stored bytes).
type value struct {
	num  bool
	u    uint64
	text string // the number's digits, or the daemon's hex for a string
}

func decodeValue(m json.RawMessage) (value, error) {
	m = bytes.TrimSpace(m)
	if len(m) > 0 && m[0] == '"' {
		var s string
		if err := json.Unmarshal(m, &s); err != nil {
			return value{}, err
		}
		return value{text: s}, nil
	}
	u, err := strconv.ParseUint(string(m), 10, 64)
	if err != nil {
		return value{}, fmt.Errorf("neither a string nor a uint64: %s", m)
	}
	return value{num: true, u: u, text: string(m)}, nil
}

// vars is a contract's string-keyed storage.
type vars map[string]value

func (v vars) has(k string) bool { _, ok := v[k]; return ok }

// u reads a Uint64 variable exactly (never through float64).
func (v vars) u(k string) (uint64, bool) {
	x, ok := v[k]
	if !ok || !x.num {
		return 0, false
	}
	return x.u, true
}

// raw reads a String variable's stored bytes.
func (v vars) raw(k string) ([]byte, bool) {
	x, ok := v[k]
	if !ok || x.num {
		return nil, false
	}
	b, err := hex.DecodeString(x.text)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (v vars) str(k string) (string, bool) { b, ok := v.raw(k); return string(b), ok }

// num renders a Uint64 variable, or says it is absent.
func (v vars) num(k string) string {
	if n, ok := v.u(k); ok {
		return strconv.FormatUint(n, 10)
	}
	if v.has(k) {
		return "(not a number)"
	}
	return "(absent)"
}

// keyVal renders a variable holding a raw 33-byte key, or says it is absent.
func (v vars) keyVal(k string) string {
	b, ok := v.raw(k)
	if !ok {
		return "(absent)"
	}
	return showKey(b)
}

// contract is one DERO.GetSC answer.
type contract struct {
	SCID     string
	Code     string
	Vars     vars
	U64      map[string]value // uint64-keyed storage (a TELA INDEX's commit history)
	Balances map[string]uint64
}

var errNoContract = errors.New("no contract at this SCID on this daemon: DERO.GetSC returned no code and no variables")

func (n node) getSC(scid string) (*contract, error) {
	if !is64hex(scid) {
		return nil, fmt.Errorf("%q is not a 64-character lower-case hex SCID", scid)
	}
	var r struct {
		Code       string                     `json:"code"`
		StringKeys map[string]json.RawMessage `json:"stringkeys"`
		Uint64Keys map[string]json.RawMessage `json:"uint64keys"`
		Balances   map[string]uint64          `json:"balances"`
	}
	if err := rpc.Call(n.addr, "DERO.GetSC", map[string]interface{}{"scid": scid, "code": true, "variables": true}, &r); err != nil {
		return nil, err
	}
	c := &contract{SCID: scid, Code: r.Code, Vars: vars{}, U64: map[string]value{}, Balances: r.Balances}
	for k, m := range r.StringKeys {
		v, err := decodeValue(m)
		if err != nil {
			return nil, fmt.Errorf("key %q: %v", k, err)
		}
		c.Vars[k] = v
	}
	for k, m := range r.Uint64Keys {
		v, err := decodeValue(m)
		if err != nil {
			return nil, fmt.Errorf("uint64 key %s: %v", k, err)
		}
		c.U64[k] = v
	}
	if c.Code == "" && len(c.Vars) == 0 {
		return nil, errNoContract
	}
	return c, nil
}

func is64hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func hexOf(b []byte) string { return hex.EncodeToString(b) }

// short is the first 12 characters of an id.
func short(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}
