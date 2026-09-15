package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	rpc "veil/tools/simrpc"
)

// deroPasses reads founders.bas v2 as it stood at a past DERO height: DERO.GetSC at the last topoheight whose block
// is at that height or below, which is the chain's state once that height is complete. The daemon is trusted for it,
// as it is for every other read this bot makes.
type deroPasses struct{ daemon, founders string }

func (d deroPasses) heights() (height, stable, topo int64, err error) {
	var r struct {
		Height int64 `json:"height"`
		Stable int64 `json:"stableheight"`
		Topo   int64 `json:"topoheight"`
	}
	err = rpc.Call(d.daemon, "DERO.GetHeight", nil, &r)
	return r.Height, r.Stable, r.Topo, err
}

func (d deroPasses) Stable() (uint64, error) {
	_, s, _, err := d.heights()
	if err != nil {
		return 0, err
	}
	if s <= 0 {
		return 0, errors.New("the daemon reports no stable height")
	}
	return uint64(s), nil
}

func (d deroPasses) heightAtTopo(topo int64) (int64, error) {
	var r struct {
		H struct {
			Height int64 `json:"height"`
			Topo   int64 `json:"topoheight"`
		} `json:"block_header"`
	}
	if err := rpc.Call(d.daemon, "DERO.GetBlockHeaderByTopoHeight", map[string]interface{}{"topoheight": topo}, &r); err != nil {
		return 0, err
	}
	if r.H.Topo != topo {
		return 0, fmt.Errorf("asked for topoheight %d, the daemon answered %d", topo, r.H.Topo)
	}
	return r.H.Height, nil
}

// topoAt is the last topoheight t with height(t) <= h < height(t+1).
func (d deroPasses) topoAt(h uint64) (int64, error) {
	height, _, top, err := d.heights()
	if err != nil {
		return 0, err
	}
	if int64(h) > height {
		return 0, fmt.Errorf("height %d is above the tip %d", h, height)
	}
	t := min(int64(h)+(top-height), top)
	for i := 0; i < 10000 && t >= 0; i++ {
		ht, err := d.heightAtTopo(t)
		if err != nil {
			return 0, err
		}
		if ht > int64(h) {
			t--
			continue
		}
		if t == top {
			return t, nil
		}
		next, err := d.heightAtTopo(t + 1)
		if err != nil {
			return 0, err
		}
		if next > int64(h) {
			return t, nil
		}
		t++
	}
	return 0, fmt.Errorf("no topoheight closes height %d", h)
}

// Snapshot reads hst, every hu_<addr>, and key+<addr> for every holder and every name in extra, at height h.
func (d deroPasses) Snapshot(h uint64, key string, extra []string) (snapshot, error) {
	topo, err := d.topoAt(h)
	if err != nil {
		return snapshot{}, err
	}
	var raw json.RawMessage
	if err := rpc.Call(d.daemon, "DERO.GetSC", map[string]interface{}{"scid": d.founders, "variables": true, "code": false, "topoheight": topo}, &raw); err != nil {
		return snapshot{}, err
	}
	var st struct {
		Keys map[string]interface{} `json:"stringkeys"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // a uint64 above 2^53 is never rounded through a 64-bit float
	if err := dec.Decode(&st); err != nil {
		return snapshot{}, err
	}
	num := func(k string) (uint64, bool, error) {
		v, ok := st.Keys[k]
		if !ok {
			return 0, false, nil
		}
		n, isNum := v.(json.Number)
		if !isNum {
			return 0, true, fmt.Errorf("%s is not a number", k)
		}
		u, err := strconv.ParseUint(n.String(), 10, 64)
		return u, true, err
	}
	if v, ok, err := num("v"); err != nil || !ok || v != 2 {
		return snapshot{}, fmt.Errorf("the founders contract is not a Founders Pass v2 at topoheight %d", topo)
	}
	hst, ok, err := num("hst")
	if err != nil || !ok {
		return snapshot{}, fmt.Errorf("the pass has no readable hst at topoheight %d", topo)
	}
	s := snapshot{Height: h, Topo: topo, Hst: hst, Units: map[string]uint64{}, Addrs: map[string]string{}}
	for k := range st.Keys {
		if !strings.HasPrefix(k, "hu_") {
			continue
		}
		u, _, err := num(k)
		if err != nil {
			return snapshot{}, err
		}
		if u > 0 {
			s.Units[k[3:]] = u
		}
	}
	names := append([]string{}, extra...)
	for a := range s.Units {
		names = append(names, a)
	}
	for _, a := range names {
		v, ok := st.Keys[key+a]
		if !ok {
			continue
		}
		hx, isStr := v.(string)
		b, err := hex.DecodeString(hx)
		if !isStr || err != nil {
			return snapshot{}, fmt.Errorf("%s%s is not a stored string", key, a)
		}
		s.Addrs[a] = string(b)
	}
	return s, nil
}
