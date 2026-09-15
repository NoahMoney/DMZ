package coord

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The multi-process test re-executes this test binary as a relay or as a signer (TestMain below), so the processes
// share nothing but loopback TCP and the files the parent wrote: three signers, two relays, round one through relay
// A, relay A killed, round two blocked while no relay runs, relay B started, round two completed through it.

const (
	envChild  = "COORD_TEST_CHILD"
	envConfig = "COORD_TEST_CONFIG"
	envKey    = "COORD_TEST_KEY"
	envListen = "COORD_TEST_LISTEN"
	envGo     = "COORD_TEST_GO"
)

var childRounds = []string{"zec/release/1/sig/0", "xmr/info/2"}

func TestMain(m *testing.M) {
	switch os.Getenv(envChild) {
	case "relay":
		os.Exit(childRelay())
	case "signer":
		os.Exit(childSigner())
	}
	os.Exit(m.Run())
}

func childRelay() int {
	fc, err := ReadConfigFile(os.Getenv(envConfig))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	g, err := fc.RelayGroup()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	r, err := NewRelay(RelayConfig{Groups: []RelayGroup{g}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ln, err := net.Listen("tcp", os.Getenv(envListen))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("listening", ln.Addr())
	if err := ServeRelay(context.Background(), ln, r, "", ""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func childPayload(id, round string) []byte { return []byte("payload of " + id + " for " + round) }

func digest(m map[string][]byte) string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		fmt.Fprintf(h, "%s=%x\n", id, m[id])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func childSigner() int {
	fc, err := ReadConfigFile(os.Getenv(envConfig))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cfg, err := fc.NodeConfig(os.Getenv(envKey))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	n, err := New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	deadline := time.Now().Add(3 * time.Minute)
	for i, round := range childRounds {
		for i > 0 {
			if _, err := os.Stat(os.Getenv(envGo)); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return 2
			}
			time.Sleep(50 * time.Millisecond)
		}
		for {
			if time.Now().After(deadline) {
				fmt.Fprintln(os.Stderr, "timed out in", round)
				return 2
			}
			_ = n.Put(context.Background(), round, childPayload(n.Self(), round))
			got, _ := n.Get(context.Background(), round)
			for id, v := range got {
				if !bytes.Equal(v, childPayload(id, round)) {
					fmt.Println("WRONG", round, id)
					return 3
				}
			}
			if len(got) == 3 {
				fmt.Println("done", round, digest(got))
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return 0
}

type child struct {
	name  string
	cmd   *exec.Cmd
	lines chan string
	errb  *bytes.Buffer
	emu   *sync.Mutex
}

func startChild(t *testing.T, name string, env ...string) *child {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &child{name: name, cmd: cmd, lines: make(chan string, 16), errb: &bytes.Buffer{}, emu: &sync.Mutex{}}
	cmd.Stderr = lockedWriter{c.errb, c.emu}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			c.lines <- sc.Text()
		}
		close(c.lines)
	}()
	t.Cleanup(func() { c.cmd.Process.Kill(); c.cmd.Wait() })
	return c
}

type lockedWriter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (c *child) stderr() string {
	c.emu.Lock()
	defer c.emu.Unlock()
	return c.errb.String()
}

func (c *child) waitLine(t *testing.T, prefix string, timeout time.Duration) string {
	t.Helper()
	tm := time.After(timeout)
	for {
		select {
		case l, ok := <-c.lines:
			if !ok {
				t.Fatalf("%s exited before %q; stderr:\n%s", c.name, prefix, c.stderr())
			}
			if strings.HasPrefix(l, "WRONG") {
				t.Fatalf("%s: %s", c.name, l)
			}
			if strings.HasPrefix(l, prefix) {
				return l
			}
		case <-tm:
			t.Fatalf("%s: no %q within %s; stderr:\n%s", c.name, prefix, timeout, c.stderr())
		}
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

func TestMultiProcessLoopback(t *testing.T) {
	if testing.Short() {
		t.Skip("starts five processes")
	}
	dir := t.TempDir()
	peers, keys := fileFleet(t, dir, 3)
	portA, portB := freePort(t), freePort(t)
	cfgPath := filepath.Join(dir, "coord.json")
	writeJSON(t, cfgPath, FileConfig{Version: 1, Vault: testVaultHex, Signers: peers,
		Stores: []string{"http://127.0.0.1:" + portA, "http://127.0.0.1:" + portB}, FetchTimeout: "3s"})
	goFile := filepath.Join(dir, "go-round-2")
	want := map[string]string{}
	for _, round := range childRounds {
		m := map[string][]byte{}
		for _, p := range peers {
			m[p.ID] = childPayload(p.ID, round)
		}
		want[round] = digest(m)
	}

	relayA := startChild(t, "relay A", envChild+"=relay", envConfig+"="+cfgPath, envListen+"=127.0.0.1:"+portA)
	relayA.waitLine(t, "listening", 30*time.Second)
	var signers []*child
	for i := range keys {
		signers = append(signers, startChild(t, fmt.Sprintf("signer %d", i), envChild+"=signer", envConfig+"="+cfgPath, envKey+"="+keys[i], envGo+"="+goFile))
	}
	for _, s := range signers {
		if l := s.waitLine(t, "done "+childRounds[0], 90*time.Second); !strings.HasSuffix(l, want[childRounds[0]]) {
			t.Fatalf("%s: %s, want digest %s", s.name, l, want[childRounds[0]])
		}
	}

	relayA.cmd.Process.Kill()
	relayA.cmd.Wait()
	if err := os.WriteFile(goFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// no relay runs: nobody can finish round two, and nobody reports a payload it should not have
	quiet := time.After(3 * time.Second)
	for waiting := true; waiting; {
		select {
		case <-quiet:
			waiting = false
		default:
			for _, s := range signers {
				select {
				case l, ok := <-s.lines:
					if ok {
						t.Fatalf("%s said %q with no relay running", s.name, l)
					}
				default:
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	relayB := startChild(t, "relay B", envChild+"=relay", envConfig+"="+cfgPath, envListen+"=127.0.0.1:"+portB)
	relayB.waitLine(t, "listening", 30*time.Second)
	for _, s := range signers {
		if l := s.waitLine(t, "done "+childRounds[1], 120*time.Second); !strings.HasSuffix(l, want[childRounds[1]]) {
			t.Fatalf("%s: %s, want digest %s", s.name, l, want[childRounds[1]])
		}
	}
	for _, s := range signers {
		if err := s.cmd.Wait(); err != nil {
			t.Fatalf("%s: %v\n%s", s.name, err, s.stderr())
		}
	}
}
