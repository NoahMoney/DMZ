package coord

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestADirectoryCarriesMessagesAndIgnoresJunk(t *testing.T) {
	f := newFleet(t, 3)
	root := t.TempDir()
	ds, err := NewDirStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ds.now = f.clock.Now
	vault := mustVault(t, f.vault)
	round := "xmr/info/1"
	tag := roundTag(vault, round)
	a, b := f.node(t, 0, ds), f.node(t, 1, ds)
	mustPut(t, a, round, payloadOf(f.id(0), round))
	raw := only(t, ds, vault, round)
	m, _ := parse(raw, maxMessage(DefaultMaxPayload))
	dir := ds.roundDir(vault, tag)
	junk := map[string][]byte{
		"README.txt": []byte("not a message"),
		msgName(f.key(0), m.created+1, m.expires):      raw[:len(raw)/2],                // a newer name holding a truncated copy
		msgName(f.key(1), m.created, m.expires):        []byte("SECRET-PAYLOAD-MARKER"), // B's name, garbage inside
		msgName(f.key(2), m.created, m.expires):        nil,                             // empty
		msgName(f.key(0), 1, 2):                        raw,                             // expired by its name
		".tmp-abandoned":                               raw,
		msgName(f.key(2), m.created+5, m.expires)[:40]: raw,
	}
	for name, body := range junk {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, msgName(f.key(2), m.created+9, m.expires)), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, b, round); !sameMap(got, map[string][]byte{f.id(0): payloadOf(f.id(0), round)}) {
		t.Fatalf("B sees %s", showMap(got))
	}
	if _, err := os.Stat(filepath.Join(dir, msgName(f.key(0), 1, 2))); !os.IsNotExist(err) {
		t.Fatal("an expired message file was not removed")
	}
	f.clock.Advance(time.Second)
	mustPut(t, a, round, []byte("second"))
	count := 0
	for _, n := range ds.list(dir) {
		if s, _, _, ok := parseMsgName(n); ok && s == f.key(0) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("A has %d files for the round after replacing its payload, want 1", count)
	}
	if got := mustGet(t, b, round); !bytes.Equal(got[f.id(0)], []byte("second")) {
		t.Fatalf("B sees %q", got[f.id(0)])
	}
	if err := ds.Health(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAMissingSharedDirectoryIsAnErrorNotAFreshFolder(t *testing.T) {
	f := newFleet(t, 3)
	root := filepath.Join(t.TempDir(), "not-mounted")
	ds, err := NewDirStore(root)
	if err != nil {
		t.Fatal(err)
	}
	a := f.node(t, 0, ds)
	if err := a.Put(ctx, "r", []byte("p")); err == nil {
		t.Fatal("a put to a missing share succeeded")
	}
	if err := ds.Health(ctx); err == nil {
		t.Fatal("a missing share is healthy")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("the missing share was created")
	}
}
