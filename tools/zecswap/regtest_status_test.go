package zecswap

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"veil/tools/swapstatus"
)

// statusRun is TestRegtestCLIStatus's own: the port each role serves -status on, the pairing code each printed, and
// each process, so that a companion lingering after its end can be stopped once its outcome has been read. Empty, the
// command-line runs are as they always were.
var statusRun struct {
	sync.Mutex
	ports map[string]int
	codes map[string]string
	procs map[string]*proc
}

var pairingCode = regexp.MustCompile(`Pairing code: ([0-9a-f]{32})`)

func statusArgs(role string) []string {
	statusRun.Lock()
	defer statusRun.Unlock()
	if p := statusRun.ports[role]; p != 0 {
		return []string{"-status", fmt.Sprintf("127.0.0.1:%d", p)}
	}
	return nil
}

func statusNoteProc(role string, p *proc) {
	statusRun.Lock()
	defer statusRun.Unlock()
	if statusRun.procs != nil {
		statusRun.procs[role] = p
	}
}

// statusLine takes a pairing code from a companion's output and puts a placeholder in its place.
func statusLine(role, line string) string {
	m := pairingCode.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	statusRun.Lock()
	if statusRun.codes != nil {
		statusRun.codes[role] = m[1]
	}
	statusRun.Unlock()
	return strings.ReplaceAll(line, m[1], "<pairing code>")
}

// curlStatus is one request to a companion's -status with curl, as a person pairing by hand would make it: the code (or
// none), and a Host of the caller's choosing. It returns the HTTP status and the body.
func curlStatus(t *testing.T, port int, code, host string) (int, string) {
	args := []string{"-s", "--max-time", "5", "-w", "\n%{http_code}"}
	if code != "" {
		args = append(args, "-H", "Authorization: Bearer "+code)
	}
	if host != "" {
		args = append(args, "-H", "Host: "+host)
	}
	out, err := exec.Command("curl", append(args, fmt.Sprintf("http://127.0.0.1:%d%s", port, swapstatus.Path))...).Output()
	if err != nil {
		return 0, ""
	}
	s := strings.TrimRight(string(out), "\r\n")
	i := strings.LastIndex(s, "\n")
	var status int
	fmt.Sscan(s[i+1:], &status)
	return status, s[:i+1]
}

func freeLoopbackPort(t *testing.T) int {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestRegtestCLIStatus is one whole zecswap swap through the COMMAND LINE, maker and taker as separate processes, each
// started with -status (docs/SWAP_STATUS.md), against the real regtest zebrad and the DERO simulator's wallets
// 30018–30020. While it runs, curl reads each companion's answer with the pairing code it printed, and the answer is
// written to research/regtest/swapstatus.log each time its phase changes; the gates are tried live (no code, a wrong
// code, a rebinding Host). Once a companion has ended, its outcome is read while it lingers, and it is then stopped.
// Skipped unless ZECSWAP_REGTEST_STATUS=1.
func TestRegtestCLIStatus(t *testing.T) {
	if os.Getenv("ZECSWAP_REGTEST_STATUS") == "" {
		t.Skip("set ZECSWAP_REGTEST_STATUS=1 (and ZECSWAP_REGTEST_COOKIE) to run a zecswap swap with -status against the real regtest zebrad and the DERO simulator")
	}
	e := newE2EAt(t, "../../research/regtest/swapstatus.log")
	defer e.close()
	e.bin = buildCLI(t)
	cookie, err := filepath.Abs(regtestCookie())
	if err != nil {
		t.Fatal(err)
	}
	e.cookie = cookie
	e.cn = &cliNet{network: "regtest", zcash: e2eZcash, mine: true, wmin: 20, wantZat: 200_000, interval: "2s", wait: 75 * time.Minute,
		dero: e2eDaemon, makerWallet: "127.0.0.1:30018", takerWallet: "127.0.0.1:30019", installWallet: "127.0.0.1:30020"}
	statusRun.Lock()
	statusRun.ports = map[string]int{RoleMaker: freeLoopbackPort(t), RoleTaker: freeLoopbackPort(t)}
	statusRun.codes, statusRun.procs = map[string]string{}, map[string]*proc{}
	statusRun.Unlock()
	defer func() {
		statusRun.Lock()
		statusRun.ports, statusRun.codes, statusRun.procs = nil, nil, nil
		statusRun.Unlock()
	}()
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil || !ci.Regtest {
		t.Fatalf("not a regtest node: %+v %v", ci, err)
	}
	e.logf("== zecswap -status on a live swap (docs/SWAP_STATUS.md): the COMMAND LINE, maker and taker as separate processes, zebrad regtest %s at height %d, DERO simulator %s, wallets 30018 (maker), 30019 (taker), 30020 (install)", e2eZcash, ci.Height, e2eDaemon)
	e.logf("   the pairing codes are read from each process's own output and written here as <pairing code>; each answer below is what curl -H 'Authorization: Bearer <pairing code>' http://127.0.0.1:PORT/v1/status returned, whole, the first time its phase was seen")
	e.install()

	stop, polled := make(chan struct{}), make(chan struct{})
	seen := map[string][]string{}
	go func() {
		defer close(polled)
		last, gates, ended := map[string]string{}, map[string]bool{}, map[string]bool{}
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Second):
			}
			for _, role := range []string{RoleMaker, RoleTaker} {
				statusRun.Lock()
				code, port, pr := statusRun.codes[role], statusRun.ports[role], statusRun.procs[role]
				statusRun.Unlock()
				if code == "" || ended[role] {
					continue
				}
				if !gates[role] {
					gates[role] = true
					a, b := curlStatus(t, port, "", "")
					c, d := curlStatus(t, port, strings.Repeat("0", 32), "")
					f, g := curlStatus(t, port, code, fmt.Sprintf("rebound.example:%d", port))
					e.logf("S %s -status gates: no code %d (%d bytes), a wrong code %d (%d bytes), the right code with a rebinding page's Host %d (%d bytes)", role, a, len(b), c, len(d), f, len(g))
				}
				status, body := curlStatus(t, port, code, "")
				if status != 200 {
					continue
				}
				var p swapstatus.Payload
				if err := json.Unmarshal([]byte(body), &p); err != nil {
					e.logf("S %s -status: not JSON: %v", role, err)
					continue
				}
				if k := p.Phase + "|" + p.Outcome; k != last[role] {
					last[role] = k
					seen[role] = append(seen[role], p.Phase)
					e.logf("S %s -status %s| %s", role, p.Phase, strings.TrimSpace(body))
				}
				if p.Outcome != "" && pr != nil {
					ended[role] = true
					e.logf("S %s -status: the outcome %q was read while the ended companion lingers; it is stopped now, as a person would once the screen shows it", role, p.Outcome)
					pr.cancel()
				}
			}
		}
	}()
	e.cliSwap("S", false)
	close(stop)
	<-polled
	for _, role := range []string{RoleMaker, RoleTaker} {
		e.logf("S %s -status: %d distinct phases read: %s", role, len(seen[role]), strings.Join(seen[role], ", "))
		if len(seen[role]) < 3 || seen[role][len(seen[role])-1] != "swapped" {
			t.Errorf("the %s's answers were read at %v: want three phases or more, ending swapped", role, seen[role])
		}
	}
	e.logf("== done: both companions served their stages to the end")
}
