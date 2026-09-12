// testnetcheck is the daily check of DMZ on the private DERO testnet, run once a day for the seven days MT-F05 asks
// for (docs/MAINNET_TODO.md W-01). It is read-only: it needs no wallet, and it sends nothing. Each run appends one
// block to research/testnet/daily.log:
//
//  1. node: the daemon is a testnet on the private genesis (deploy/README.md "# testnet"), its height is above the
//     last logged run's, and its top block is younger than -stale;
//
//  2. verify: tools/verifydeploy against the testnet record, the installed bundle and the TELA record's INDEX
//     (-extra tela=<index>: the record's tela.index is not written by any deploy tool), with no -allow; its exit
//     status and summary line. No TELA INDEX in the TELA record is a FAIL: W-01 puts the app on the testnet;
//
//  3. the exercised contracts, read back with DERO.GetSC against research/testnet/exercised.json: the factory is
//     factory.bas v2 with no owner key and the treasury and registration fee it was installed with, the markets are
//     still registered and traded, and every htlc.bas / swap.bas lock is in the state it was left in. A lock left
//     open on purpose is open until the height its intended exit opens, must have taken that exit once the window
//     is open (WARN while it has not), and FAILs if the window closes with the lock still open.
//
//     cd tools && go run ./testnetcheck                 # exit 0 when nothing FAILed, 1 otherwise, 2 on bad usage
//     cd tools && go run ./testnetcheck -log -          # the same report on stdout only
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"veil/tools/netcfg"
	rpc "veil/tools/simrpc"
)

// factoryRec is what the factory must still be. factory.bas v2 (MT-J15(b)) has no owner, no setter and no
// Renounce: Initialize(treasury, regfee) is final, so both values are compared with what was installed.
type factoryRec struct {
	Scid        string  `json:"scid"`
	Treasury    string  `json:"treasury"` // tstr as the contract stores it (ADDRESS_STRING, dero1… on every network)
	Regfee      *uint64 `json:"regfee"`   // atomic units; absent = not compared
	MinPools    uint64  `json:"minPools"`
	MinLaunches uint64  `json:"minLaunches"`
}

// lockRec is one htlc.bas or swap.bas lock, or one board.bas offer, and the state it must reach.
type lockRec struct {
	Contract string `json:"contract"` // htlc | swap | board
	Scid     string `json:"scid"`
	ID       uint64 `json:"id"`
	Want     uint64 `json:"want"` // st_<id>: htlc 1 claimed 2 refunded 3 punished 4 reclaimed; swap 1 claimed 2 refunded; board 1 accepted 2 cancelled
	What     string `json:"what"` // a word for the log
	// A lock left open on purpose: open (st 0) is expected below Opens, the height its intended exit opens (a swap
	// lock's expiry; an htlc lock's t1 for Refund, t2 for Punish). Closes is the height that exit's window closes
	// (htlc Refund t2, Punish t2 + g2); 0 = it never closes (swap Refund, htlc Reclaim).
	Opens  uint64 `json:"opens"`
	Closes uint64 `json:"closes"`
}

// exercised is research/testnet/exercised.json: what was done on the testnet, and what state it must still be in.
type exercised struct {
	Genesis string     `json:"genesis"` // block hash at topoheight 0, as derod reports it
	Day0    string     `json:"day0"`    // YYYY-MM-DD, the first day of the window
	Days    int        `json:"days"`
	Factory factoryRec `json:"factory"`
	Markets []struct {
		Kind      string `json:"kind"` // launch | pool
		Scid      string `json:"scid"`
		MinTrades uint64 `json:"minTrades"`
	} `json:"markets"`
	Locks []lockRec `json:"locks"`
}

type line struct{ status, subject, text string }

type run struct {
	lines []line
	fails int
	warns int
}

func (r *run) add(status, subject, format string, a ...interface{}) {
	r.lines = append(r.lines, line{status, subject, fmt.Sprintf(format, a...)})
	switch status {
	case "FAIL":
		r.fails++
	case "WARN":
		r.warns++
	}
}

func main() {
	daemon := flag.String("daemon", "", "testnet daemon RPC (default: the record's testnet daemon)")
	networks := flag.String("networks", "../deploy/networks.json", "the deployment record")
	exFile := flag.String("exercised", "../research/testnet/exercised.json", "what was exercised, and the state it must be in")
	logFile := flag.String("log", "../research/testnet/daily.log", "append the report here; - for stdout only")
	dist := flag.String("dist", "../dist/testnet", "the installed bundle, for verifydeploy -dist (falls back to -app ../app when absent)")
	telaRecord := flag.String("tela-record", "../deploy/tela-testnet.json", "the TELA record, for verifydeploy -tela-record and the INDEX it checks")
	verify := flag.Bool("verifydeploy", true, "run tools/verifydeploy (go run, from this directory)")
	stale := flag.Duration("stale", 30*time.Minute, "the top block must be younger than this")
	flag.Parse()

	var ex exercised
	b, err := os.ReadFile(*exFile)
	if err == nil {
		err = json.Unmarshal(b, &ex)
	}
	if err != nil {
		fmt.Println("exercised record:", err)
		os.Exit(2)
	}
	f, err := netcfg.Load(*networks)
	if err != nil || f.Networks["testnet"] == nil {
		fmt.Println("no testnet block in", *networks, err)
		os.Exit(2)
	}
	if *daemon == "" {
		*daemon = f.Networks["testnet"].Daemon
	}
	if strings.HasSuffix(*daemon, ":20000") {
		fmt.Println("127.0.0.1:20000 is the simulator; this check reads the testnet only")
		os.Exit(2)
	}

	now := time.Now().UTC()
	r := &run{}
	height := checkNode(r, *daemon, ex, lastHeight(*logFile), now, *stale)
	if *verify {
		checkVerifydeploy(r, *daemon, *dist, *telaRecord)
	}
	checkFactory(r, *daemon, ex)
	checkMarkets(r, *daemon, ex)
	checkLocks(r, *daemon, ex, height)

	day := "?"
	if d0, err := time.Parse("2006-01-02", ex.Day0); err == nil {
		day = strconv.Itoa(int(now.Sub(d0).Hours() / 24))
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "== %s  day %s of %d  (tools/testnetcheck, read-only)\n", now.Format(time.RFC3339), day, ex.Days)
	for _, l := range r.lines {
		text := strings.ReplaceAll(l.text, "\n", "\n                  ")
		fmt.Fprintf(&out, "%-5s %-11s %s\n", l.status, l.subject, text)
	}
	verdict := "PASS"
	if r.fails > 0 {
		verdict = "FAIL"
	}
	fmt.Fprintf(&out, "result: %s (%d FAIL, %d WARN)\n\n", verdict, r.fails, r.warns)
	report := sanitize(out.String())
	fmt.Print(report)
	if *logFile != "-" {
		lf, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, err = io.WriteString(lf, report)
			lf.Close()
		}
		if err != nil {
			fmt.Println("log:", err)
			os.Exit(1)
		}
	}
	if r.fails > 0 {
		os.Exit(1)
	}
}

// sanitize writes the repository as <repo>: tracked logs must not name a machine (tools/hygiene).
func sanitize(s string) string {
	root, err := filepath.Abs("..")
	if err != nil {
		return s
	}
	for _, p := range []string{root, filepath.ToSlash(root)} {
		s = strings.ReplaceAll(s, p, "<repo>")
	}
	return s
}

var nodeHeight = regexp.MustCompile(`^PASS\s+node\s.*\bheight (\d+)`)

// lastHeight is the height the previous run logged, or -1.
func lastHeight(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer f.Close()
	h := int64(-1)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := nodeHeight.FindStringSubmatch(sc.Text()); m != nil {
			h, _ = strconv.ParseInt(m[1], 10, 64)
		}
	}
	return h
}

func checkNode(r *run, daemon string, ex exercised, last int64, now time.Time, stale time.Duration) int64 {
	var info struct {
		Height   int64   `json:"height"`
		Stable   int64   `json:"stableheight"`
		Topo     int64   `json:"topoheight"`
		Testnet  bool    `json:"testnet"`
		Network  string  `json:"network"`
		Avg50    float64 `json:"averageblocktime50"`
		Pool     int     `json:"tx_pool_size"`
		Peers    int     `json:"outgoing_connections_count"`
		Incoming int     `json:"incoming_connections_count"`
		Miners   int     `json:"connected_miners"`
	}
	if err := rpc.Call(daemon, "DERO.GetInfo", nil, &info); err != nil {
		r.add("FAIL", "node", "DERO.GetInfo at %s: %v", daemon, err)
		return -1
	}
	if !info.Testnet || strings.EqualFold(info.Network, "simulator") {
		r.add("FAIL", "node", "%s reports testnet=%v network %q: not the testnet", daemon, info.Testnet, info.Network)
		return -1
	}
	var g struct {
		Header struct {
			Hash string `json:"hash"`
		} `json:"block_header"`
	}
	if err := rpc.Call(daemon, "DERO.GetBlockHeaderByTopoHeight", map[string]interface{}{"topoheight": 0}, &g); err != nil || g.Header.Hash != ex.Genesis {
		r.add("FAIL", "node", "block 0 is %q (err %v), not the private genesis %s: another chain", g.Header.Hash, err, ex.Genesis)
		return -1
	}
	var top struct {
		Header struct {
			Timestamp int64 `json:"timestamp"`
		} `json:"block_header"`
	}
	age := time.Duration(-1)
	if err := rpc.Call(daemon, "DERO.GetBlockHeaderByTopoHeight", map[string]interface{}{"topoheight": info.Topo}, &top); err == nil {
		age = now.Sub(time.UnixMilli(top.Header.Timestamp)).Round(time.Second)
	}
	detail := fmt.Sprintf("testnet, genesis %s, height %d, stable %d, top block %v old, average of the last 50 blocks %.0f s, %d miner(s), %d peer(s), pool %d",
		short(ex.Genesis), info.Height, info.Stable, age, info.Avg50, info.Miners, info.Peers+info.Incoming, info.Pool)
	switch {
	case age < 0 || age > stale:
		r.add("FAIL", "node", "%s: the chain is not progressing (top block older than %v)", detail, stale)
	case last >= 0 && info.Height <= last:
		r.add("FAIL", "node", "%s: no higher than the last run's %d", detail, last)
	default:
		if last >= 0 {
			detail += fmt.Sprintf(" (+%d since the last run)", info.Height-last)
		}
		r.add("PASS", "node", "%s", detail)
	}
	return info.Height
}

// telaIndex is the INDEX a telainstall record names, or an error: the app on the testnet is part of W-01, so a record
// without one fails the run instead of letting verifydeploy skip the TELA checks.
func telaIndex(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var rec struct {
		Network string `json:"network"`
		Index   string `json:"index"`
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return "", fmt.Errorf("%s: %v", path, err)
	}
	if rec.Network != "" && rec.Network != "testnet" {
		return "", fmt.Errorf("%s is a %s record, not the testnet's", path, rec.Network)
	}
	if len(rec.Index) != 64 || strings.Trim(rec.Index, "0123456789abcdef") != "" {
		return "", fmt.Errorf("%s names no TELA INDEX (index %q)", path, rec.Index)
	}
	return rec.Index, nil
}

func checkVerifydeploy(r *run, daemon, dist, telaRecord string) {
	idx, err := telaIndex(telaRecord)
	if err != nil {
		r.add("FAIL", "verify", "the app: %v", err)
		return
	}
	args := []string{"run", "./verifydeploy", "-network", "testnet", "-daemon", daemon, "-tela-record", telaRecord, "-extra", "tela=" + idx}
	if st, err := os.Stat(dist); err == nil && st.IsDir() {
		args = append(args, "-dist", dist)
	} else {
		args = append(args, "-app", "../app")
		r.add("INFO", "verify", "%s is absent (dist/ is not tracked): comparing the DOCs with ../app instead", dist)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", args...).CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		r.add("FAIL", "verify", "go %s: %v", strings.Join(args, " "), err)
		return
	}
	summary := "(no summary line)"
	var fails []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(l, "summary:") {
			summary = l
		}
		if strings.HasPrefix(l, "FAIL") {
			fails = append(fails, l)
		}
	}
	cmd := fmt.Sprintf("verifydeploy -network testnet -dist -tela-record -extra tela=%s, no -allow", short(idx))
	if code == 0 {
		r.add("PASS", "verify", "%s: exit 0; %s", cmd, summary)
		return
	}
	r.add("FAIL", "verify", "%s: exit %d; %s\n%s", cmd, code, summary, strings.Join(fails, "\n"))
}

func getSC(daemon, scid string) (rpc.SCState, error) {
	var raw json.RawMessage
	if err := rpc.Call(daemon, "DERO.GetSC", map[string]interface{}{"scid": scid, "variables": true, "code": false}, &raw); err != nil {
		return rpc.SCState{}, err
	}
	var st rpc.SCState
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&st); err != nil {
		return st, err
	}
	if len(st.StringKeys) == 0 {
		return st, fmt.Errorf("no contract state at %s", scid)
	}
	return st, nil
}

func short(s string) string {
	if len(s) > 16 {
		return s[:16] + "…"
	}
	return s
}

// num reads a uint64 variable without the process exit simrpc's U takes on a bad value: a check that reads a
// hostile or unexpected state reports it and goes on.
func num(st rpc.SCState, k string) (uint64, bool) {
	v, ok := st.StringKeys[k]
	if !ok {
		return 0, false
	}
	var s string
	switch x := v.(type) {
	case json.Number:
		s = x.String()
	case float64:
		if x < 0 || x != float64(uint64(x)) || x > 9007199254740992 {
			return 0, false
		}
		return uint64(x), true
	case string:
		s = x
	default:
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

// str reads a string variable: DERO.GetSC returns stored strings hex-encoded.
func str(st rpc.SCState, k string) (string, bool) {
	v, ok := st.StringKeys[k].(string)
	if !ok {
		return "", false
	}
	b, err := hex.DecodeString(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// judgeFactory is the factory check: factory.bas v2 with no owner key, the treasury and registration fee it was
// installed with, and at least the listings the exercise registered. A factory holding an owner key fails whatever its
// version says: some key could still move its treasury or fee. So does an older factory, renounced or not: a v1
// renounced factory passes verifydeploy's roles check, but it is not the contract the testnet was deployed with.
func judgeFactory(st rpc.SCState, want factoryRec) (status, text string) {
	id := short(want.Scid)
	v, hasV := num(st, "v")
	kind, _ := str(st, "kind")
	pc, _ := num(st, "pc")
	lc, _ := num(st, "lc")
	nc, _ := num(st, "nc")
	tstr, hasT := str(st, "tstr")
	fee, hasFee := num(st, "regfee")
	switch {
	case st.Has("owner"):
		return "FAIL", fmt.Sprintf("%s has an owner key: a key can still change its treasury or registration fee (factory.bas v2 stores no owner)", id)
	case kind != "factory":
		return "FAIL", fmt.Sprintf("%s stores kind %q: not a factory", id, kind)
	case !hasV:
		return "FAIL", fmt.Sprintf("%s stores no v: not factory.bas v2", id)
	case v != 2 && st.Has("renounced"):
		return "FAIL", fmt.Sprintf("%s is factory.bas v%d, renounced: an older factory, not the v2 the testnet was deployed with", id, v)
	case v != 2:
		return "FAIL", fmt.Sprintf("%s is factory.bas v%d; the deployed contract is v2", id, v)
	case st.Has("renounced"):
		return "FAIL", fmt.Sprintf("%s is v2 but stores renounced, which factory.bas v2 never writes", id)
	case !hasT || !hasFee:
		return "FAIL", fmt.Sprintf("%s lacks tstr or regfee, which factory.bas v2 Initialize writes", id)
	case want.Treasury != "" && tstr != want.Treasury:
		return "FAIL", fmt.Sprintf("%s pays treasury %s, installed with %s", id, tstr, want.Treasury)
	case want.Regfee != nil && fee != *want.Regfee:
		return "FAIL", fmt.Sprintf("%s charges regfee %d, installed with %d", id, fee, *want.Regfee)
	case pc < want.MinPools || lc < want.MinLaunches:
		return "FAIL", fmt.Sprintf("%s lists %d pool(s) and %d launch(es); at least %d and %d were registered", id, pc, lc, want.MinPools, want.MinLaunches)
	}
	return "PASS", fmt.Sprintf("%s factory.bas v2, no owner key; treasury %s and regfee %d as installed; lists %d pool(s), %d launch(es), %d NFT/NFA(s)", id, short(tstr), fee, pc, lc, nc)
}

func checkFactory(r *run, daemon string, ex exercised) {
	st, err := getSC(daemon, ex.Factory.Scid)
	if err != nil {
		r.add("FAIL", "factory", "%s: %v", short(ex.Factory.Scid), err)
		return
	}
	status, text := judgeFactory(st, ex.Factory)
	r.add(status, "factory", "%s", text)
}

func checkMarkets(r *run, daemon string, ex exercised) {
	for _, m := range ex.Markets {
		st, err := getSC(daemon, m.Scid)
		if err != nil {
			r.add("FAIL", m.Kind, "%s: %v", short(m.Scid), err)
			continue
		}
		if tc, _ := num(st, "tc"); tc < m.MinTrades {
			r.add("FAIL", m.Kind, "%s records %d published trade(s); at least %d were made", short(m.Scid), tc, m.MinTrades)
			continue
		}
		g := func(k string) uint64 { n, _ := num(st, k); return n }
		extra := ""
		if m.Kind == "pool" {
			extra = fmt.Sprintf(", lp %d", g("lp"))
		} else {
			extra = fmt.Sprintf(", phase %d, sold %d", g("phase"), g("sold"))
		}
		r.add("PASS", m.Kind, "%s %d published trade(s), reserves %d DERO units / %d asset units%s", short(m.Scid), g("tc"), g("rd"), g("ra"), extra)
	}
}

// judgeLock is the check of one lock: in its wanted state, or left open and still inside the time it may be.
func judgeLock(l lockRec, got uint64, has bool, height int64) (status, text string) {
	id, noun := short(l.Scid), "lock"
	if l.Contract == "board" {
		noun = "offer"
	}
	switch {
	case !has:
		return "FAIL", fmt.Sprintf("%s has no %s %d", id, noun, l.ID)
	case got == l.Want:
		return "PASS", fmt.Sprintf("%s %s %d is %s (st %d)", id, noun, l.ID, l.What, got)
	case got != 0 || l.Opens == 0 || height < 0:
		return "FAIL", fmt.Sprintf("%s %s %d has st %d, expected %d (%s)", id, noun, l.ID, got, l.Want, l.What)
	case uint64(height) < l.Opens:
		return "INFO", fmt.Sprintf("%s %s %d open as intended until %d (%d blocks from %d); then it must be %s", id, noun, l.ID, l.Opens, l.Opens-uint64(height), height, l.What)
	case l.Closes > 0 && uint64(height) >= l.Closes:
		return "FAIL", fmt.Sprintf("%s %s %d is still open at %d: its window [%d, %d) to be %s closed unused", id, noun, l.ID, height, l.Opens, l.Closes, l.What)
	case l.Closes > 0:
		return "WARN", fmt.Sprintf("%s %s %d: its window [%d, %d) to be %s is open and the scheduled call has not landed yet (%d blocks left)", id, noun, l.ID, l.Opens, l.Closes, l.What, l.Closes-uint64(height))
	}
	return "WARN", fmt.Sprintf("%s %s %d opened for its exit at %d and is still open: the scheduled call to make it %s has not landed yet", id, noun, l.ID, l.Opens, l.What)
}

func checkLocks(r *run, daemon string, ex exercised, height int64) {
	for _, l := range ex.Locks {
		subject := fmt.Sprintf("%s %d", l.Contract, l.ID)
		st, err := getSC(daemon, l.Scid)
		if err != nil {
			r.add("FAIL", subject, "%s: %v", short(l.Scid), err)
			continue
		}
		got, has := num(st, fmt.Sprintf("st_%d", l.ID))
		status, text := judgeLock(l, got, has, height)
		r.add(status, subject, "%s", text)
	}
}
