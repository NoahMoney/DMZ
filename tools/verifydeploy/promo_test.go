package main

import (
	"strings"
	"testing"
)

// promo.bas has no owner, so nothing fails on a well-formed install: the treasury is a payee, the terms and the quote
// are fixed at install and read from storage, and the init guard passes. Without initd the guard fails.
func TestPromoRoles(t *testing.T) {
	wxmr := make([]byte, 32)
	wxmr[0], wxmr[31] = 0xe1, 0x36
	v := vars{"kind": ss("promo"), "treasury": sv(key(2)), "quote": sv(wxmr), "initd": nv(1), "slots": nv(3), "epoch": nv(4800), "minbid": nv(10000000000), "incbps": nv(500),
		"sq": nv(7), "pr": nv(0), "fc": nv(300000), "bi": nv(900000), "rf": nv(200000), "sx": nv(0)}
	rows := evalRoles("promo", "promo", "p", v, nil)
	if len(rows) != 4 {
		t.Fatalf("promo rows: %+v", rows)
	}
	for _, r := range rows {
		if r.Status == sFail || r.Status == sAllowed || r.Live {
			t.Fatalf("a well-formed promo.bas needs no -allow and has no live power, yet: %+v", r)
		}
	}
	if r := find(rows, "terms"); r.Value != "slots=3 epoch=4800 minbid=10000000000 incbps=500" || r.Policy != pFixed {
		t.Fatalf("terms: %+v", r)
	}
	if r := find(rows, "quote"); r.Value != hexOf(wxmr) || !strings.Contains(r.State, "DERO-chain asset") {
		t.Fatalf("quote: %+v", r)
	}
	v["quote"] = sv(make([]byte, 32))
	if r := find(evalRoles("promo", "promo", "p", v, nil), "quote"); r.Value != "DERO" {
		t.Fatalf("a DERO auction's quote: %+v", r)
	}
	if r := find(rows, "treasury"); r.Policy != pPayee || !strings.Contains(r.State, "ClaimProceeds") || !strings.Contains(r.State, "anyone") {
		t.Fatalf("treasury: %+v", r)
	}
	if g := find(evalRoles("promo", "promo", "x", vars{}, nil), "initd"); g.Status != sFail || !g.Live {
		t.Fatalf("promo without initd: %+v", g)
	}
}
