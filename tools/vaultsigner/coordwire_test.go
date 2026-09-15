package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rpc "veil/tools/simrpc"
	"veil/tools/vaultsigner/coord"
)

func TestCoordHandleFailsClosedUntilStarted(t *testing.T) {
	var l lazyCoord
	if err := l.Put(context.Background(), "zec/release/1/sig/0", []byte("p")); !errors.Is(err, errCoordOff) {
		t.Fatalf("Put before start: %v", err)
	}
	m, err := l.Get(context.Background(), "zec/release/1/sig/0")
	if m != nil || !errors.Is(err, errCoordOff) {
		t.Fatalf("Get before start: %v %v", m, err)
	}
	var _ Coordinator = coordHandle
}

// rawKey is a made-up 33-byte key with bytes above 0x7f, which JSON mangles in a key name.
func rawKey(b byte) []byte {
	raw := make([]byte, 33)
	raw[0] = 3
	for i := 1; i < 33; i++ {
		raw[i] = b + byte(i*29)
	}
	return raw
}

func TestSeatedSignersReadsTheVault(t *testing.T) {
	raws := [][]byte{rawKey(1), rawKey(2), rawKey(3)}
	st := rpc.SCState{StringKeys: map[string]interface{}{"n": json.Number("4")}}
	for i, raw := range raws {
		st.StringKeys[fmt.Sprintf("sg_%d", i)] = hex.EncodeToString(raw)
	}
	st.StringKeys[lossyName("isg_"+string(raws[0]))] = json.Number("1")
	st.StringKeys[lossyName("isg_"+string(raws[1]))] = json.Number("0")
	got := seatedSigners(st) // raws[2] has no readable isg_; sg_3 is absent
	want := map[string]seat{rpc.AddressFromRaw(raws[0]): seatActive, rpc.AddressFromRaw(raws[1]): seatSlashed, rpc.AddressFromRaw(raws[2]): seatUnknown}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for id, s := range want {
		if got[id] != s {
			t.Fatalf("%s: %v, want %v", id, got[id], s)
		}
	}
}

func TestCheckPinned(t *testing.T) {
	seated := map[string]seat{"a": seatActive, "b": seatSlashed, "c": seatUnknown}
	if _, err := checkPinned([]string{"a", "z"}, seated); err == nil || !strings.Contains(err.Error(), "never seated") {
		t.Fatalf("a signer the vault never seated was accepted: %v", err)
	}
	w, err := checkPinned([]string{"a", "b", "c"}, seated)
	if err != nil || len(w) != 1 || !strings.Contains(w[0], "b was slashed") {
		t.Fatalf("%v %v", w, err)
	}
}

func identityLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "identity ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "identity "))
		}
	}
	return ""
}

func TestKeygenWritesAPrivateFileAndPrintsOnlyThePublicHalf(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "signer.coord.key")
	addr := rpc.AddressFromRaw(rawKey(7))
	var so, se bytes.Buffer
	if ok, code := runSubcommand([]string{"keygen", "-id", addr, "-out", out}, &so, &se); !ok || code != 0 {
		t.Fatalf("keygen: %d %s", code, se.String())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	seed := strings.TrimPrefix(lines[len(lines)-1], "seed ")
	if len(seed) != 64 {
		t.Fatalf("unexpected key file shape")
	}
	if strings.Contains(so.String()+se.String(), seed) {
		t.Fatal("keygen printed the seed")
	}
	line := identityLine(so.String())
	if !strings.HasPrefix(line, coord.PublicPrefix) {
		t.Fatalf("keygen printed no identity line:\n%s", so.String())
	}
	var so2 bytes.Buffer
	if ok, code := runSubcommand([]string{"identity", "-key", out}, &so2, &se); !ok || code != 0 || identityLine(so2.String()) != line {
		t.Fatalf("identity: %d %q vs %q %s", code, identityLine(so2.String()), line, se.String())
	}
	if strings.Contains(so2.String(), seed) {
		t.Fatal("identity printed the seed")
	}
	if _, code := runSubcommand([]string{"keygen", "-id", addr, "-out", out}, &so, &se); code == 0 {
		t.Fatal("keygen overwrote an existing identity file")
	}
	if after, _ := os.ReadFile(out); !bytes.Equal(after, b) {
		t.Fatal("a refused keygen changed the file")
	}
	for _, args := range [][]string{{"keygen"}, {"keygen", "-id", addr}, {"keygen", "-out", out + "2"}, {"identity"}, {"relay"}} {
		if ok, code := runSubcommand(args, &so, &se); !ok || code != 2 {
			t.Errorf("%v: %v %d, want a usage error", args, ok, code)
		}
	}
	if _, code := runSubcommand([]string{"keygen", "-id", "not-an-address", "-out", out + "3"}, &so, &se); code == 0 {
		t.Fatal("keygen accepted an id that is not a DERO address")
	}
	for _, args := range [][]string{nil, {"-network", "simulator"}, {"help"}} {
		if ok, _ := runSubcommand(args, &so, &se); ok {
			t.Errorf("%v was taken for a subcommand", args)
		}
	}
}

func TestIdentitySubcommandChecksTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	var peers []coord.FilePeer
	var keys []string
	for i := 0; i < 4; i++ {
		k := filepath.Join(dir, fmt.Sprintf("k%d", i))
		var so, se bytes.Buffer
		if _, code := runSubcommand([]string{"keygen", "-id", rpc.AddressFromRaw(rawKey(byte(10 + i))), "-out", k}, &so, &se); code != 0 {
			t.Fatal(se.String())
		}
		idn, err := coord.LoadIdentityFile(k)
		if err != nil {
			t.Fatal(err)
		}
		peers = append(peers, coord.FilePeer{ID: idn.ID(), Identity: idn.Public().String()})
		keys = append(keys, k)
	}
	cfg := filepath.Join(dir, "coord.json")
	b, _ := json.Marshal(coord.FileConfig{Version: 1, Vault: strings.Repeat("ab", 32), Signers: peers[:3], Stores: []string{"http://127.0.0.1:1"}})
	if err := os.WriteFile(cfg, b, 0o600); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if _, code := runSubcommand([]string{"identity", "-key", keys[0], "-coord", cfg}, &so, &se); code != 0 || !strings.Contains(so.String(), "is pinned") {
		t.Fatalf("a pinned identity: %d %s %s", code, so.String(), se.String())
	}
	if _, code := runSubcommand([]string{"identity", "-key", keys[3], "-coord", cfg}, &so, &se); code == 0 {
		t.Fatal("an identity missing from the configuration passed")
	}
}
