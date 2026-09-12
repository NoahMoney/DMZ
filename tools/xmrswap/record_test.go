package xmrswap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRecordedHTLCIsTheDeployRecord (MT-A18): the scids the command line defaults -htlc to are deploy/networks.json's,
// network for network, and the lookup refuses a network the record lacks or has no htlc.bas on.
func TestRecordedHTLCIsTheDeployRecord(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "networks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for k, v := range rec {
		if strings.HasPrefix(k, "_") {
			continue
		}
		var n struct {
			HTLC string `json:"htlc"`
		}
		if err := json.Unmarshal(v, &n); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		want[k] = n.HTLC
	}
	if !reflect.DeepEqual(want, recordedHTLC) {
		t.Fatalf("record_gen.go is not deploy/networks.json: run `go generate ./xmrswap` from tools/\ncompiled: %v\nrecord:   %v", recordedHTLC, want)
	}
	if _, err := RecordedHTLC("nowhere"); err == nil {
		t.Fatal("a network the record does not have gave a scid")
	}
	for net, scid := range want {
		got, err := RecordedHTLC(net)
		switch {
		case scid == "" && err == nil:
			t.Fatalf("%s: no htlc.bas recorded, yet %q was returned", net, got)
		case scid != "" && (err != nil || got != scid):
			t.Fatalf("%s: recorded %s, got %q (%v)", net, scid, got, err)
		}
		t.Logf("%s: %q", net, scid)
	}
}
