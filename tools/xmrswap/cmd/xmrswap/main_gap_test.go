package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// baseArgs is a complete test-network command line: every flag parseOptions requires, and nothing more.
func baseArgs(t *testing.T) []string {
	t.Helper()
	return []string{"-htlc", strings.Repeat("ab", 32), "-amount-dero", "1", "-amount-xmr", "1",
		"-net", "stagenet", "-daemon", "127.0.0.1:20000", "-dero", "127.0.0.1:30006", "-monero", "http://127.0.0.1:38082",
		"-prover", filepath.Join(t.TempDir(), "proof-xmr")}
}

// mainnetArgs is baseArgs for mainnet: -mainnet, -net mainnet, a loopback monerod and the wallet-dir.
func mainnetArgs(t *testing.T) []string {
	t.Helper()
	return append(set(baseArgs(t), "-net", "mainnet"), "-mainnet", "-monerod", "127.0.0.1:18081", "-monero-wallet-dir", t.TempDir())
}

func set(args []string, flag, value string) []string {
	out := append([]string(nil), args...)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == flag {
			out[i+1] = value
			return out
		}
	}
	return append(out, flag, value)
}

func without(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

var mainnetEnv = map[string]string{"VEIL_RPC_LOGIN": "dero:pw", "XMRSWAP_MONERO_LOGIN": "monero:pw"}

// TestNetworkAndEndpointFlagsHaveNoDefaults (GAP-007): which network, whose daemon and wallets, and which prover are
// named on every run; leaving any out is refused before anything is contacted.
func TestNetworkAndEndpointFlagsHaveNoDefaults(t *testing.T) {
	if _, err := parseOptions("bob", baseArgs(t), env(nil)); err != nil {
		t.Fatalf("a complete command line: %v", err)
	}
	for _, f := range []string{"-net", "-daemon", "-dero", "-monero", "-prover"} {
		if _, err := parseOptions("bob", without(baseArgs(t), f), env(nil)); err == nil || !strings.Contains(err.Error(), f) {
			t.Fatalf("without %s: %v", f, err)
		}
	}
}

// TestTheProverIsAnAbsolutePath (GAP-003): a bare name would run whatever PATH finds first.
func TestTheProverIsAnAbsolutePath(t *testing.T) {
	if _, err := parseOptions("alice", set(baseArgs(t), "-prover", "proof-xmr"), env(nil)); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("a bare prover name: %v", err)
	}
}

// TestMainnetTakesCredentialsFromTheEnvironmentOnly (GAP-008, GAP-009): on mainnet both wallet logins are required
// and come from the environment; on the command line, where every local process can read them, they are refused.
// Off mainnet the command line is still accepted.
func TestMainnetTakesCredentialsFromTheEnvironmentOnly(t *testing.T) {
	o, err := parseOptions("bob", mainnetArgs(t), env(mainnetEnv))
	if err != nil {
		t.Fatalf("a complete mainnet command line: %v", err)
	}
	if o.deroLogin != "dero:pw" || o.moneroUser != "monero" || o.moneroPass != "pw" {
		t.Fatalf("logins from the environment: %q %q %q", o.deroLogin, o.moneroUser, o.moneroPass)
	}
	for _, c := range []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"a DERO login on the command line", append(mainnetArgs(t), "-dero-login", "dero:pw"), mainnetEnv, "-dero-login is refused"},
		{"a Monero password on the command line", append(mainnetArgs(t), "-monero-user", "m", "-monero-pass", "pw"), mainnetEnv, "-monero-pass is refused"},
		{"no DERO login", mainnetArgs(t), map[string]string{"XMRSWAP_MONERO_LOGIN": "monero:pw"}, "VEIL_RPC_LOGIN"},
		{"no Monero login", mainnetArgs(t), map[string]string{"VEIL_RPC_LOGIN": "dero:pw"}, "XMRSWAP_MONERO_LOGIN"},
	} {
		if _, err := parseOptions("bob", c.args, env(c.env)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
	if o, err := parseOptions("bob", append(baseArgs(t), "-dero-login", "dero:pw"), env(nil)); err != nil || o.deroLogin != "dero:pw" {
		t.Fatalf("a DERO login on a test network's command line: %v", err)
	}
}

// TestMainnetNeedsLoopbackEndpointsAndItsOwnMonerod (GAP-010, GAP-020, GAP-003): on mainnet the monerod is named and
// on loopback, as are the Monero and DERO wallets; the wallet-dir is given so the joint wallets can be deleted; and
// no prover but the pinned one runs.
func TestMainnetNeedsLoopbackEndpointsAndItsOwnMonerod(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no monerod", without(mainnetArgs(t), "-monerod"), "-monerod"},
		{"a remote monerod", set(mainnetArgs(t), "-monerod", "203.0.113.5:18081"), "-monerod 203.0.113.5:18081 is not loopback"},
		{"a remote wallet-rpc over plain http", set(mainnetArgs(t), "-monero", "http://203.0.113.5:18082"), "-monero http://203.0.113.5:18082 is not loopback"},
		{"a remote DERO wallet", set(mainnetArgs(t), "-dero", "203.0.113.5:10103"), "-dero 203.0.113.5:10103 is not loopback"},
		{"no wallet-dir", without(mainnetArgs(t), "-monero-wallet-dir"), "-monero-wallet-dir"},
		{"another prover build", append(mainnetArgs(t), "-prover-sha256", strings.Repeat("00", 32)), "-prover-sha256 is refused"},
	} {
		if _, err := parseOptions("alice", c.args, env(mainnetEnv)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
}

// TestAnOpenWalletIsRefusedOnMainnet (GAP-008, GAP-009): once the wallets have answered, a DERO wallet that took a
// call with no credentials, or a monero-wallet-rpc that never asked for any, is refused on mainnet and said aloud
// elsewhere.
func TestAnOpenWalletIsRefusedOnMainnet(t *testing.T) {
	if _, err := authVerdict(true, true, true); err == nil || !strings.Contains(err.Error(), "DERO wallet") {
		t.Fatalf("mainnet, an open DERO wallet: %v", err)
	}
	if _, err := authVerdict(true, false, false); err == nil || !strings.Contains(err.Error(), "monero-wallet-rpc") {
		t.Fatalf("mainnet, a monero-wallet-rpc with no login: %v", err)
	}
	if w, err := authVerdict(true, false, true); err != nil || len(w) != 0 {
		t.Fatalf("mainnet, both wallets with logins: %v %v", w, err)
	}
	if w, err := authVerdict(false, true, false); err != nil || len(w) != 2 {
		t.Fatalf("a test network, both open: %v %v", w, err)
	}
}
