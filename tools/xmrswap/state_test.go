package xmrswap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStateRoundTripAndStrictLoad: what is saved is what is loaded; a missing file is a fresh swap; a corrupt
// file, another role's file and another contract's file are refused rather than replayed.
func TestStateRoundTripAndStrictLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bob.json")
	fresh, err := LoadState(path, "bob", "aa")
	if err != nil || fresh.Phase != "" || fresh.Done {
		t.Fatalf("missing file: %+v %v", fresh, err)
	}
	st := NewState(path, "bob", "aa")
	st.Phase, st.Own.Secret, st.Gate = "gate_sent", strings.Repeat("ab", 32), strings.Repeat("cd", 32)
	st.Lock = LockRecord{ID: 3, T1: 100, T2: 200, Amount: 5}
	st.Consumed[KindAliceSetup] = true
	st.LastSent = &Message{Kind: KindGate, Body: []byte(`{"kb":"x"}`)}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(path + ".tmp*"); len(left) != 0 {
		t.Fatalf("temp file left behind: %v", left)
	}
	got, err := LoadState(path, "bob", "aa")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "gate_sent" || got.Own.Secret != st.Own.Secret || got.Gate != st.Gate || got.Lock != st.Lock || !got.Consumed[KindAliceSetup] || got.LastSent == nil || got.LastSent.Kind != KindGate {
		t.Fatalf("round trip lost something: %+v", got)
	}
	if _, err := LoadState(path, "alice", "aa"); err == nil {
		t.Fatal("bob's state loaded as alice")
	}
	if _, err := LoadState(path, "bob", "bb"); err == nil {
		t.Fatal("a state bound to contract aa loaded for bb")
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path, "bob", "aa"); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

// TestStateFileIsPrivate: the file that holds a key share is its owner's alone: 0600 where the mode is the guard, and
// on Windows a protected DACL with one entry, this account's, which the file has from its creation (assertPrivate).
func TestStateFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st := NewState(path, "alice", "aa")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, path)
	st.Phase = "setup"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, path) // and after it is replaced, since every save writes a new file
	used, err := LoadUsed(UsedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := used.Record([]byte("a secret"), "swap", "share"); err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, UsedPath(path)) // the register is written the same way
}

// TestAStateWriteNeverReusesAFileAlreadyThere (the review of 2026-09-12, finding 4): the write used to open path + ".tmp"
// with O_TRUNC, so a file of that name planted by anyone who can write the directory was reused — its planter's handle
// and, on Windows, its ACL with it. Now the temporary file has a name nobody can guess, is created new or not at all, and
// is its owner's alone from its creation; the planted file is never touched.
func TestAStateWriteNeverReusesAFileAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bob.json")
	planted := []byte("planted by another account")
	if err := os.WriteFile(path+".tmp", planted, 0o644); err != nil {
		t.Fatal(err)
	}
	st := NewState(path, "bob", "aa")
	st.Own.Secret = strings.Repeat("ab", 32)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path + ".tmp"); err != nil || string(b) != string(planted) {
		t.Fatalf("the planted %s.tmp was written through: %q %v", path, b, err)
	}
	if got, err := LoadState(path, "bob", "aa"); err != nil || got.Own.Secret != st.Own.Secret {
		t.Fatalf("the state did not round-trip: %v", err)
	}
	assertPrivate(t, path)
	// the temporary name is new every time, and ends in 64 random bits
	a, err := tempName(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := tempName(path)
	if a == b || !strings.HasPrefix(a, path+".tmp-") || len(a) != len(path)+len(".tmp-")+16 {
		t.Fatalf("temporary names %q and %q", a, b)
	}
	// a file already at the name is refused, never opened
	if err := os.WriteFile(a, planted, 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err := createPrivate(a); err == nil {
		f.Close()
		t.Fatal("createPrivate opened a file that was already there")
	}
	if got, _ := os.ReadFile(a); string(got) != string(planted) {
		t.Fatalf("the file already at %s was changed: %q", a, got)
	}
	if left, _ := filepath.Glob(path + ".tmp-*"); len(left) != 1 || left[0] != a {
		t.Fatalf("temporary files beside the state: %v", left)
	}
}
