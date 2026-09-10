package xmrswap

import (
	"os"
	"path/filepath"
	"runtime"
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
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
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

// TestStateFileIsPrivate: the file that holds a key share is created for its owner only.
func TestStateFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	st := NewState(path, "alice", "aa")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows reports no Unix mode bits and does not enforce them: the file inherits the directory's ACL, so
	// there the protection the comment claims does not exist and the operator has to be told so instead of the
	// assertion being skipped in silence.
	if runtime.GOOS == "windows" {
		w := StateFileWarning(path)
		if w == "" || !strings.Contains(w, path) {
			t.Fatalf("no warning naming %s on a platform that does not enforce the mode: %q", path, w)
		}
		t.Logf("windows: %s", w)
		return
	}
	if w := StateFileWarning(path); w != "" {
		t.Fatalf("a warning on a platform that does enforce the mode: %q", w)
	}
	if m := fi.Mode().Perm(); m&0o077 != 0 {
		t.Fatalf("state file mode %o", m)
	}
}
