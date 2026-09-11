package xmrswap

// Named regressions for GAP-009, GAP-010 and GAP-020 (docs/MAINNET_GAP_ANALYSIS.md).

import (
	"context"
	"crypto/rand"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestJointWalletsAreEncryptedAndRemovedAfterTheSweep (GAP-020): the joint wallets are encrypted under a password of
// this swap's own, so the wallet-rpc opens them with it and with no other; and once the sweep has gone out, the
// companion deletes both wallets' files from the wallet-dir it was told about.
func TestJointWalletsAreEncryptedAndRemovedAfterTheSweep(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 141})
	dir := t.TempDir()
	with := func(c *Config) { c.MoneroWalletDir = dir }
	r.bob = startWith(r, "bob", with)
	r.alice = startWith(r, "alice", with)
	r.until(400, func() bool { p, _ := r.phase("bob"); return p == "claimed" }, nil)
	bob := r.loadState("bob")
	if len(bob.JointPassword) != 64 {
		t.Fatalf("joint wallet password %q", bob.JointPassword)
	}
	view := jointWalletName(testHTLC, bob.SwapID, bob.Lock.ID, "view")
	if err := r.w.OpenWallet(r.ctx, view, ""); err == nil {
		t.Fatal("the joint view wallet opened without its password")
	}
	if err := r.w.OpenWallet(r.ctx, view, bob.JointPassword); err != nil {
		t.Fatalf("the joint view wallet with its password: %v", err)
	}
	// stand-ins for the files monero-wallet-rpc writes into its --wallet-dir (the fake keeps its wallets in memory)
	var files []string
	for _, kind := range []string{"spend", "view"} {
		for _, sfx := range []string{"", ".keys", ".address.txt"} {
			p := filepath.Join(dir, jointWalletName(testHTLC, bob.SwapID, bob.Lock.ID, kind)+sfx)
			if err := os.WriteFile(p, []byte("wallet"), 0o600); err != nil {
				t.Fatal(err)
			}
			files = append(files, p)
		}
	}
	r.until(400, r.bothDone, nil)
	if b := r.loadState("bob"); b.Phase != "swept" {
		t.Fatalf("bob ended %s", b.Phase)
	}
	for _, p := range files {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s is still there after the sweep", p)
		}
	}
}

// TestWalletAskedForCredentials (GAP-009): the client knows whether the wallet-rpc it talks to demanded --rpc-login
// digest credentials, which is what the command line refuses a mainnet wallet-rpc for not doing.
func TestWalletAskedForCredentials(t *testing.T) {
	ctx := context.Background()
	fw := NewFakeWallet(Stagenet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	open := NewWalletRPC(srv.URL, "", "")
	if _, err := open.Height(ctx); !isRPC(err, -13) || WalletAskedForCredentials(open) {
		t.Fatalf("a wallet-rpc with no login: err %v, asked %v", err, WalletAskedForCredentials(open))
	}
	fw.RequireAuth("wallet", "pw")
	if _, err := NewWalletRPC(srv.URL, "", "").Height(ctx); err == nil {
		t.Fatal("a wallet-rpc that demands a login answered without one")
	}
	login := NewWalletRPC(srv.URL, "wallet", "pw")
	if _, err := login.Height(ctx); !isRPC(err, -13) || !WalletAskedForCredentials(login) {
		t.Fatalf("a wallet-rpc with a login: err %v, asked %v", err, WalletAskedForCredentials(login))
	}
}

// TestSetDaemonNamesTheMonerod (GAP-010): the companion can tell its wallet-rpc which monerod to use — for the wallet
// open at the time, as 0.18.5.1 does it: -13 with no wallet open, and forgotten when another wallet is opened
// (research/regtest/monero-smoke.log), which is why the swap sends it again after every open.
func TestSetDaemonNamesTheMonerod(t *testing.T) {
	ctx := context.Background()
	fw := NewFakeWallet(Stagenet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	w := NewWalletRPC(srv.URL, "", "")
	ds, ok := w.(DaemonSetter)
	if !ok {
		t.Fatal("the wallet-rpc client cannot set a daemon")
	}
	if err := ds.SetDaemon(ctx, "127.0.0.1:38081"); !isRPC(err, -13) {
		t.Fatalf("set_daemon with no wallet open: %v", err)
	}
	spend, _ := NewScalar(rand.Reader)
	view, _ := NewScalar(rand.Reader)
	addr, _ := Address(spend.Public(), view.Public(), Stagenet)
	if err := w.GenerateFromKeys(ctx, "mine", addr, view.Hex(), spend.Hex(), "", 0); err != nil {
		t.Fatal(err)
	}
	if err := ds.SetDaemon(ctx, "127.0.0.1:38081"); err != nil || fw.Daemon() != "127.0.0.1:38081" {
		t.Fatalf("set_daemon: %v, the wallet-rpc uses %q", err, fw.Daemon())
	}
	if err := w.OpenWallet(ctx, "mine", ""); err != nil || fw.Daemon() != "" {
		t.Fatalf("after a reopen the monerod is still named (%q): the fake does not model the server", fw.Daemon())
	}
}

// TestStateFilesAreOwnerOnlyOnWindows (GAP-020): with HardenFile set, as the command line sets it on Windows, the
// state file carries no inherited entry and grants its owner alone, and the operator is not warned about it.
func TestStateFilesAreOwnerOnlyOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the ACL is Windows'; elsewhere the file's 0600 is the guard (TestStateFileIsPrivate)")
	}
	old := HardenFile
	HardenFile = OwnerOnlyACL
	defer func() { HardenFile = old }()
	st := NewState(filepath.Join(t.TempDir(), "s.json"), "bob", "ab")
	st.Own.Secret = strings.Repeat("11", 32)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("icacls", st.Path()).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls: %v %s", err, out)
	}
	text := string(out)
	if !strings.Contains(text, os.Getenv("USERNAME")+":(F)") || strings.Contains(text, "(I)") || strings.Contains(text, "Everyone") ||
		strings.Contains(text, `BUILTIN\Users`) || strings.Contains(text, "Authenticated Users") {
		t.Fatalf("the state file's ACL is not owner-only:\n%s", text)
	}
	if w := StateFileWarning(st.Path()); w != "" {
		t.Fatalf("still warned: %s", w)
	}
	t.Logf("icacls: %s", strings.TrimSpace(text))
}
