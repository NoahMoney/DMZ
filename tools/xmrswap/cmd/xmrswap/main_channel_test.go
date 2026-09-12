package main

import (
	"path/filepath"
	"strings"
	"testing"

	"veil/tools/xmrswap"
)

// TestTheChannelKeyComesFromTheEnvironmentOnly (GAP-019): a companion needs the swap's channel key and takes it from
// XMRSWAP_CHANNEL_KEY alone. Without it, or with a malformed one, it does not start, on any network; and there is no
// flag through which a key could be given on a command line every local process can read.
func TestTheChannelKeyComesFromTheEnvironmentOnly(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "XMRSWAP_CHANNEL_KEY" {
				return v
			}
			return ""
		}
	}
	if _, err := channelKeyFrom(env("")); err == nil {
		t.Fatal("a companion started without a channel key")
	}
	if _, err := channelKeyFrom(env(strings.Repeat("ab", 16))); err == nil {
		t.Fatal("a 16-byte channel key was accepted")
	}
	if _, err := channelKeyFrom(env(strings.Repeat("zz", 32))); err == nil {
		t.Fatal("a channel key that is not hex was accepted")
	}
	k, err := xmrswap.NewChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	if b, err := channelKeyFrom(env(k)); err != nil || len(b) != xmrswap.ChannelKeyLen {
		t.Fatalf("a fresh key: %v (%d bytes)", err, len(b))
	}
	args := []string{"-net", "stagenet", "-daemon", "127.0.0.1:40402", "-dero", "127.0.0.1:40403", "-monero", "http://127.0.0.1:38083",
		"-prover", filepath.Join(t.TempDir(), "proof-xmr"), "-htlc", strings.Repeat("ab", 32), "-amount-dero", "1", "-amount-xmr", "1", "-channel-key", k}
	if _, err := parseOptions("alice", args, env(k)); err == nil {
		t.Fatal("a -channel-key flag was accepted")
	}
}
