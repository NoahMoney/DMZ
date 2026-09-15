package main

import (
	"strings"
	"testing"
)

// rate.bas: no role can act, so a well-formed contract's report has nothing that fails; the treasury is a payee, the
// fee a parameter read from storage; and a contract whose init guard is missing fails.
func TestRateRoles(t *testing.T) {
	v := vars{"treasury": sv(key(2)), "fee": nv(10000), "initd": nv(1), "kind": ss("rate"), "v": nv(1), "n": nv(3)}
	rows := evalRoles("rate", "rate", "r", v, nil)
	if len(rows) != 3 {
		t.Fatalf("%d rows: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Status == sFail || r.Status == sAllowed || r.Live {
			t.Fatalf("a well-formed rate contract has a live role: %+v", r)
		}
	}
	if r := find(rows, "treasury"); r.Policy != pPayee || !strings.Contains(r.State, "vote's fee") {
		t.Fatalf("treasury: %+v", r)
	}
	if r := find(rows, "fee"); r.Policy != pFixed || r.Value != "fee=10000" {
		t.Fatalf("fee: %+v", r)
	}
	delete(v, "initd")
	if r := find(evalRoles("rate", "rate", "r", v, nil), "initd"); r.Status != sFail {
		t.Fatalf("a rate contract without initd does not fail: %+v", r)
	}
}
