package coord

import (
	"bytes"
	"testing"
)

func TestNewLocalGroupGivesBackendsThreeWorkingSigners(t *testing.T) {
	ids := []string{testAddr(1), testAddr(2), testAddr(3)}
	nodes, _, err := NewLocalGroup(testVaultHex, ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	round := "zec/release/0/sig/0"
	for i, n := range nodes {
		if n.Self() != ids[i] {
			t.Fatalf("node %d is %s", i, n.Self())
		}
		mustPut(t, n, round, []byte{byte(i)})
	}
	for _, n := range nodes {
		got := mustGet(t, n, round)
		for i, id := range ids {
			if !bytes.Equal(got[id], []byte{byte(i)}) {
				t.Fatalf("%s sees %s", n.Self(), showMap(got))
			}
		}
	}
	if _, _, err := NewLocalGroup(testVaultHex, []string{ids[0], "nope"}, nil); err == nil {
		t.Fatal("a bad id was accepted")
	}
}
