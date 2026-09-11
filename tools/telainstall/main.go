// telainstall: installs the Veil app on-chain as TELA-DOC-1 contracts (gzip-compressed, signed) plus a
// TELA-INDEX-1 entrypoint, exactly the way tela-cli does it but scriptable. It can also update an existing
// ring-2 INDEX (new commit) and serve the installed app locally to verify the round trip.
// Built inside vendor/tela (see build.sh) because that module pins the civilware derohe fork.
//
//	telainstall -wallet=<file.db> -password= -daemon=127.0.0.1:20000 -network <net> -app=../dist/<net> -durl=dmz.tela
//	           [-ringsize N]     ring size of the INDEX install. Above 2 the contract sees 33 zero bytes as SIGNER(),
//	                             stores owner "anon", and UpdateCode refuses every caller for ever: nobody can replace
//	                             the app, and each release is a new INDEX. At 2 the installer's address is the owner
//	                             and can call UpdateCode for ever. Default 16 on mainnet, 2 elsewhere.
//	           [-updatable]      accept a ring-2 INDEX on mainnet, and with it a key that stays a trusted party
//	           [-doc-ringsize N] ring size of each DOC install (default 2: a DOC is immutable at any ring, and its
//	                             fileCheckC/S signature identifies the signing key anyway)
//	           [-index=<scid>]   update this INDEX instead of installing a new one (a ring-2 INDEX, owner's wallet)
//	           [-serve]          after install/update, serve the INDEX with the tela package and print the URL
//	           [-out=deploy/tela-deployment.json]
//
// -app must be a genconfig bundle for -network: a directory named <network> holding a manifest.json whose network
// is -network and whose hashes are its files', and a config.js that names -network (GAP-032).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/civilware/tela"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/walletapi"
)

type deployment struct {
	Network     string            `json:"network"`
	DURL        string            `json:"durl"`
	Index       string            `json:"index"`
	RingSize    uint64            `json:"ringsize"`    // of the INDEX install: 2 = updatable by Owner, above 2 = owner "anon"
	DocRingSize uint64            `json:"docRingsize"` // of the DOC installs made by this run
	Owner       string            `json:"owner"`       // the INDEX's stored owner, read back from chain
	Commit      int               `json:"commit"`
	Updates     []string          `json:"updates"`
	Docs        map[string]string `json:"docs"`             // path → SCID
	Hashes      map[string]string `json:"hashes,omitempty"` // path → sha256 of the DOC's stored content block (reuse unchanged DOCs)
	Time        string            `json:"time"`
}

func must(err error, what string) {
	if err != nil {
		fmt.Println(what+":", err)
		os.Exit(1)
	}
}

func refuse(format string, a ...interface{}) {
	fmt.Printf(format+"\n", a...)
	os.Exit(2)
}

// estimate retries the daemon gas estimate: dvm.Simulator.SCInstall names a graviton tree after a simulated SCID and
// panics ("Bucket cannot start with ':'") when its first byte happens to be 0x3a — about once in 256 calls.
func estimate(w *walletapi.Wallet_Disk, ring uint64, args rpc.Arguments) (gas uint64, err error) {
	for i := 0; i < 5; i++ {
		if gas, err = tela.GetGasEstimate(w, ring, nil, args); err == nil || !strings.Contains(err.Error(), "Bucket cannot start") {
			return
		}
		fmt.Println("estimate hit the graviton ':' bucket panic, retrying")
	}
	return
}

// installWithFee sends an install without asking the daemon for an estimate. Used when the estimator panics on a
// DOC (graviton refuses a simulated SCID starting with ':', and the simulated SCID is a function of the content, so
// retrying never helps). Storage gas for a DOC is its code size plus a small overhead (measured +348).
func installWithFee(w *walletapi.Wallet_Disk, ring uint64, args rpc.Arguments, fee uint64) (string, error) {
	tx, err := w.TransferPayload0(nil, ring, false, args, fee, false)
	if err != nil {
		return "", err
	}
	if err := w.SendTransaction(tx); err != nil {
		return "", err
	}
	return tx.GetHash().String(), nil
}

// docBlock is what a DOC serves: its /* */ block as tela reads it (vendor/tela/tela.go parseDocCode) — from the
// first "/*" to the end, less a final "*/", trimmed. tela.GetDOCInfo returns the WHOLE contract in Code (template,
// headers and block), so the read-back used to hash that and could never equal the bundle's hash: every install
// stopped at its first DOC with "does not match this bundle".
func docBlock(code string) (string, error) {
	start, end := strings.Index(code, "/*"), strings.Index(code, "*/")
	if start == -1 || end == -1 {
		return "", fmt.Errorf("no /* */ content block")
	}
	return strings.TrimSpace(strings.TrimSuffix(code[start+2:], "*/")), nil
}

// validRing: DERO rings are powers of two from 2 to 128.
func validRing(n uint64) bool { return n >= 2 && n <= 128 && n&(n-1) == 0 }

var configNetwork = regexp.MustCompile(`(?m)^\s*network:\s*"([^"]*)"`)

// checkBundle refuses anything but a genconfig bundle for this network (GAP-032): -network mainnet with the raw
// app directory, or with another network's bundle, installs DOCs the app then refuses to use, and every DOC costs
// DERO.
func checkBundle(dir, network string, files []string) error {
	build := fmt.Sprintf("build it with: cd tools && go run ./genconfig -dist -network %s, and pass -app ../dist/%s", network, network)
	if filepath.Base(filepath.Clean(dir)) != network {
		return fmt.Errorf("-app %s is not a dist/%s bundle; %s", dir, network, build)
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("-app %s has no manifest.json, so it is not a genconfig bundle; %s", dir, build)
	}
	var m struct {
		Network string `json:"network"`
		Files   map[string]struct {
			Sha256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("%s/manifest.json: %v", dir, err)
	}
	if m.Network != network {
		return fmt.Errorf("%s/manifest.json is for network %q; -network is %q", dir, m.Network, network)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "config.js"))
	if err != nil {
		return fmt.Errorf("-app %s has no config.js: %v", dir, err)
	}
	if mm := configNetwork.FindSubmatch(cfg); mm == nil || string(mm[1]) != network {
		got := "(none)"
		if mm != nil {
			got = string(mm[1])
		}
		return fmt.Errorf("%s/config.js names network %q; -network is %q", dir, got, network)
	}
	listed := map[string]bool{}
	for rel, e := range m.Files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("manifest.json lists %s, which is missing from the bundle", rel)
		}
		if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != e.Sha256 {
			return fmt.Errorf("%s changed after genconfig wrote manifest.json (sha256 %x…, listed %.16s…): rebuild the bundle", rel, h[:8], e.Sha256)
		}
		listed[rel] = true
	}
	for _, rel := range files {
		if rel != "manifest.json" && !listed[rel] {
			return fmt.Errorf("%s is in the bundle but not in manifest.json: the bundle was edited after genconfig; rebuild it", rel)
		}
	}
	return nil
}

func main() {
	wfile := flag.String("wallet", "", "wallet file")
	pass := flag.String("password", "", "wallet password")
	daemon := flag.String("daemon", "127.0.0.1:20000", "daemon endpoint")
	appDir := flag.String("app", "", "the genconfig bundle to install: ../dist/<network>")
	durl := flag.String("durl", "dmz.tela", "dURL")
	name := flag.String("name", "DMZ", "app name header")
	descr := flag.String("descr", "DMZ: social DEX and launchpad for DERO assets", "app description header")
	icon := flag.String("icon", "", "icon URL")
	index := flag.String("index", "", "existing INDEX scid to update (a ring-2 INDEX only)")
	serve := flag.Bool("serve", false, "serve the INDEX after install")
	out := flag.String("out", "tela-deployment.json", "deployment record")
	reuseFile := flag.String("reuse", "", "file of \"<relative path> <scid>\" lines: DOCs already installed from this exact bundle, pointed at instead of reinstalled")
	network := flag.String("network", "simulator", "simulator, testnet or mainnet (sets the wallet and TELA network flags)")
	yes := flag.Bool("yes", false, "required on mainnet: every DOC and the INDEX cost real DERO")
	ringFlag := flag.Uint64("ringsize", 0, "ring size of the INDEX install: above 2 nobody can ever update it (owner \"anon\"); 0 = 16 on mainnet, 2 elsewhere")
	docRing := flag.Uint64("doc-ringsize", 2, "ring size of each DOC install")
	updatable := flag.Bool("updatable", false, "mainnet: accept a ring-2 INDEX whose installing key can replace the app for ever")
	flag.Parse()
	if *pass == "" { // argv is visible to every process; the environment is not
		*pass = os.Getenv("VEIL_WALLET_PASSWORD")
	}
	if *network == "mainnet" && !*yes {
		fmt.Println("mainnet: this spends real DERO on every DOC; pass -yes to run it")
		os.Exit(2)
	}
	if *wfile == "" {
		fmt.Println("-wallet required")
		os.Exit(2)
	}
	if *appDir == "" {
		refuse("-app required: the genconfig bundle for -network, ../dist/%s", *network)
	}
	ring := *ringFlag
	if ring == 0 {
		ring = 2
		if *network == "mainnet" {
			ring = 16
		}
	}
	if !validRing(ring) || !validRing(*docRing) {
		refuse("ring sizes are powers of two from 2 to 128 (-ringsize %d, -doc-ringsize %d)", ring, *docRing)
	}
	if *updatable && ring != 2 {
		refuse("-updatable asks for a ring-2 INDEX, and -ringsize %d installs one nobody can update: pass one or the other", ring)
	}
	if *index != "" && ring != 2 {
		refuse("-index updates an existing INDEX, and only a ring-2 INDEX can be updated: with -ringsize %d each release is a new INDEX, so omit -index", ring)
	}
	if *network == "mainnet" && ring < 16 && !*updatable {
		refuse("mainnet: an INDEX installed at ring %d is refused. Use -ringsize 16 (or more) for an INDEX nobody can update, or -ringsize 2 -updatable to accept that the installing key can replace the whole app for ever", ring)
	}
	if ring == 2 {
		fmt.Println("WARNING: ring-2 INDEX: the installing wallet becomes its owner and can call UpdateCode for ever, replacing the whole app; that key stays a trusted party until it is destroyed, which nobody else can verify")
	} else {
		fmt.Printf("INDEX ring %d: its owner will be \"anon\" and UpdateCode will refuse every caller; a later release is a new INDEX\n", ring)
	}
	var testnet, simulator bool
	switch *network {
	case "simulator":
		testnet, simulator = true, true
	case "testnet":
		testnet, simulator = true, false
	case "mainnet":
		testnet, simulator = false, false
	default:
		fmt.Println("-network must be simulator, testnet or mainnet")
		os.Exit(2)
	}

	// collect files (index.html must be first: DOC1 is the entrypoint), and refuse a bundle not built for -network
	// before the wallet is even opened
	var files []string
	must(filepath.Walk(*appDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(*appDir, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".") || strings.HasSuffix(rel, ".png") {
			return nil
		}
		files = append(files, rel)
		return nil
	}), "walk")
	if err := checkBundle(*appDir, *network, files); err != nil {
		refuse("%v", err)
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i] == "index.html" {
			return true
		}
		if files[j] == "index.html" {
			return false
		}
		return files[i] < files[j]
	})

	globals.Arguments = map[string]interface{}{"--testnet": testnet, "--simulator": simulator, "--daemon-address": *daemon, "--debug": false}
	globals.Initialize()
	if *index != "" { // before any DOC is paid for: an INDEX whose owner is "anon" can never be updated by anyone
		live, err := tela.GetINDEXInfo(*index, *daemon)
		must(err, "get index")
		if live.Author == "anon" {
			refuse("INDEX %s was installed above ring 2: its owner is \"anon\" and UpdateCode refuses every caller. Install a new INDEX instead", *index)
		}
	}
	w, err := walletapi.Open_Encrypted_Wallet(*wfile, *pass)
	must(err, "open wallet")
	w.SetNetwork(!testnet)
	fmt.Println("network", *network)
	w.SetDaemonAddress(*daemon)
	w.SetOnlineMode()
	go walletapi.Keep_Connectivity()
	for i := 0; i < 60 && !walletapi.IsDaemonOnline(); i++ {
		time.Sleep(500 * time.Millisecond)
	}
	must(w.Sync_Wallet_Memory_With_Daemon(), "sync")
	fmt.Println("wallet", w.GetAddress().String(), "height", w.Get_Height())

	dep := deployment{Network: *network, DURL: *durl, RingSize: ring, DocRingSize: *docRing, Docs: map[string]string{}, Hashes: map[string]string{}, Time: time.Now().UTC().Format(time.RFC3339)}
	prevDocs, prevHashes := map[string]string{}, map[string]string{}
	if *index != "" { // keep the update history of an existing deployment record
		if b, err := os.ReadFile(*out); err == nil {
			var prev deployment
			if json.Unmarshal(b, &prev) == nil && prev.Index == *index {
				dep.Updates = prev.Updates
				prevDocs, prevHashes = prev.Docs, prev.Hashes
			}
		}
	}
	reuse := map[string]string{}
	if *reuseFile != "" {
		b, err := os.ReadFile(*reuseFile)
		must(err, "reuse file")
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && len(f[1]) == 64 {
				reuse[f[0]] = f[1]
			}
		}
	}
	// a DOC is pointed at instead of installed only when the chain holds exactly this bundle's bytes under that scid:
	// neither the deployment record nor a -reuse file is trusted on its own
	verifyDoc := func(scid, codeHash, rel string) {
		live, err := tela.GetDOCInfo(scid, *daemon)
		must(err, "reuse "+rel)
		block, err := docBlock(live.Code)
		must(err, "reuse "+rel)
		if h := sha256.Sum256([]byte(block)); hex.EncodeToString(h[:]) != codeHash {
			must(fmt.Errorf("on-chain content of %s… does not match this bundle (%x… vs %s…)", scid[:16], h[:8], codeHash[:16]), "reuse "+rel)
		}
	}
	var docSCIDs []string
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(*appDir, filepath.FromSlash(rel)))
		must(err, rel)
		fileName := filepath.Base(rel)
		subDir := filepath.ToSlash(filepath.Dir(rel))
		if subDir == "." {
			subDir = ""
		}
		docType := tela.ParseDocType(fileName)
		compressed, err := tela.Compress(data, tela.COMPRESSION_GZIP)
		must(err, "compress "+rel)
		docCode := compressed
		storedName := fileName + tela.COMPRESSION_GZIP
		sig := w.SignData([]byte(docCode))
		_, c, s, err := tela.ParseSignature(sig)
		must(err, "signature")
		doc := &tela.DOC{DocType: docType, Code: docCode, SubDir: subDir, DURL: *durl,
			Signature: tela.Signature{CheckC: c, CheckS: s},
			Headers:   tela.Headers{NameHdr: storedName, DescrHdr: "Veil " + rel, IconHdr: *icon}}
		args, err := tela.NewInstallArgs(doc)
		must(err, "args "+rel)
		code := args.Value(rpc.SCCODE, rpc.DataString).(string)
		sum := sha256.Sum256([]byte(docCode)) // content only: the embedded wallet signature is randomised per run
		codeHash := hex.EncodeToString(sum[:])
		dep.Hashes[rel] = codeHash
		if scid := reuse[rel]; scid != "" {
			verifyDoc(scid, codeHash, rel)
			fmt.Printf("DOC %-22s already on chain (-reuse, content hash verified), pointing at %s\n", rel, scid[:16])
			dep.Docs[rel] = scid
			docSCIDs = append(docSCIDs, scid)
			continue
		}
		if prevDocs[rel] != "" && prevHashes[rel] == codeHash { // an unchanged DOC is immutable on chain: point the INDEX at it again
			verifyDoc(prevDocs[rel], codeHash, rel)
			fmt.Printf("DOC %-22s unchanged (content hash verified on chain), reusing %s\n", rel, prevDocs[rel][:16])
			dep.Docs[rel] = prevDocs[rel]
			docSCIDs = append(docSCIDs, prevDocs[rel])
			continue
		}
		gas, err := estimate(w, *docRing, args)
		var txid string
		if err != nil && strings.Contains(err.Error(), "Bucket cannot start") {
			gas = uint64(len(code)) + 1024 // measured overhead is a few hundred units; the margin is cheap, a reverted install is not
			fmt.Printf("estimate unavailable for %s (the daemon cannot simulate this DOC): sending with a size-based fee of %d\n", rel, gas)
			txid, err = installWithFee(w, *docRing, args, gas)
		} else {
			must(err, "estimate "+rel)
			txid, err = tela.Installer(w, *docRing, doc)
		}
		must(err, "install "+rel)
		fmt.Printf("DOC %-22s %6d bytes → %6d on-chain (gzip)  gas %5d  ring %d  scid %s\n", rel, len(data), len(code), gas, *docRing, txid)
		dep.Docs[rel] = txid
		docSCIDs = append(docSCIDs, txid)
		waitTx(txid, w)
		// inclusion is not execution: the DOC must be readable under its scid, with this bundle's bytes
		verifyDoc(txid, codeHash, rel+" (install confirmed but the DOC did not execute or holds other code)")
	}

	idx := &tela.INDEX{DURL: *durl, DOCs: docSCIDs, Headers: tela.Headers{NameHdr: *name, DescrHdr: *descr, IconHdr: *icon}}
	if *index == "" {
		args, err := tela.NewInstallArgs(idx)
		must(err, "index args")
		gas, err := estimate(w, ring, args)
		must(err, "index estimate")
		txid, err := tela.Installer(w, ring, idx)
		must(err, "index install")
		fmt.Printf("INDEX %s ring %d gas %d (%d DOCs, %.2f KB)\n", txid, ring, gas, len(docSCIDs), tela.GetCodeSizeInKB(args.Value(rpc.SCCODE, rpc.DataString).(string)))
		dep.Index = txid
		waitTx(txid, w)
	} else {
		live, err := tela.GetINDEXInfo(*index, *daemon)
		must(err, "get index")
		idx.SCID = *index
		idx.SCVersion = live.SCVersion
		txid, err := tela.Updater(w, idx)
		must(err, "index update")
		fmt.Printf("UPDATE %s → commit tx %s\n", *index, txid)
		dep.Index = *index
		dep.Updates = append(dep.Updates, txid)
		waitTx(txid, w)
	}
	live, err := tela.GetINDEXInfo(dep.Index, *daemon)
	must(err, "verify index")
	dep.Owner = live.Author
	dep.Commit = 0
	if v, _, err := tela.KeyExists(dep.Index, "commit", *daemon); err == nil {
		fmt.Sscanf(v, "%d", &dep.Commit)
	}
	fmt.Printf("INDEX %s dURL %s owner %s docs %d commit %d\n", live.SCID, live.DURL, live.Author, len(live.DOCs), dep.Commit)
	if ring > 2 && live.Author != "anon" {
		must(fmt.Errorf("installed at ring %d but the INDEX stores owner %q: it is updatable, contrary to what was asked", ring, live.Author), "verify index")
	}
	b, _ := json.MarshalIndent(dep, "", "  ")
	must(os.WriteFile(*out, b, 0o644), "write record")

	if *serve {
		tela.AllowUpdates(true)
		link, err := tela.ServeTELA(dep.Index, *daemon)
		must(err, "serve")
		fmt.Println("SERVING", link)
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		tela.ShutdownTELA()
	}
	w.Close_Encrypted_Wallet()
}

func waitTx(txid string, w *walletapi.Wallet_Disk) {
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var r rpc.GetTransaction_Result
		if err := walletapi.GetRPCClient().Call("DERO.GetTransaction", rpc.GetTransaction_Params{Tx_Hashes: []string{txid}}, &r); err == nil && len(r.Txs) == 1 && r.Txs[0].ValidBlock != "" && !r.Txs[0].In_pool {
			// wait one more block and for the wallet to catch up so the next TX builds on a fresh balance
			h := r.Txs[0].Block_Height
			for time.Now().Before(deadline) {
				var hr rpc.GetHeight_Result
				if err := walletapi.GetRPCClient().Call("DERO.GetHeight", nil, &hr); err == nil && int64(hr.Height) > h && w.Get_Height() > uint64(h) {
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("timeout waiting for", txid)
	os.Exit(1)
}
