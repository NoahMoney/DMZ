// telainstall: installs the Veil app on-chain as TELA-DOC-1 contracts (gzip-compressed, signed) plus a
// TELA-INDEX-1 entrypoint, exactly the way tela-cli does it but scriptable. It can also update an existing
// INDEX (new commit) and serve the installed app locally to verify the round trip.
// Built inside vendor/tela (see build.sh) because that module pins the civilware derohe fork.
//
//	telainstall -wallet=<file.db> -password= -daemon=127.0.0.1:20000 -app=../app -durl=dmz.tela
//	           [-index=<scid>]   update this INDEX instead of installing a new one (ringsize 2 owner only)
//	           [-serve]          after install/update, serve the INDEX with the tela package and print the URL
//	           [-out=deploy/tela-deployment.json]
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
	"sort"
	"strings"
	"time"

	"github.com/civilware/tela"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/walletapi"
)

type deployment struct {
	Network string            `json:"network"`
	DURL    string            `json:"durl"`
	Index   string            `json:"index"`
	Commit  int               `json:"commit"`
	Updates []string          `json:"updates"`
	Docs    map[string]string `json:"docs"`             // path → SCID
	Hashes  map[string]string `json:"hashes,omitempty"` // path → sha256 of the stored DOC code (reuse unchanged DOCs)
	Time    string            `json:"time"`
}

func must(err error, what string) {
	if err != nil {
		fmt.Println(what+":", err)
		os.Exit(1)
	}
}

// estimate retries the daemon gas estimate: dvm.Simulator.SCInstall names a graviton tree after a simulated SCID and
// panics ("Bucket cannot start with ':'") when its first byte happens to be 0x3a — about once in 256 calls.
func estimate(w *walletapi.Wallet_Disk, args rpc.Arguments) (gas uint64, err error) {
	for i := 0; i < 5; i++ {
		if gas, err = tela.GetGasEstimate(w, 2, nil, args); err == nil || !strings.Contains(err.Error(), "Bucket cannot start") {
			return
		}
		fmt.Println("estimate hit the graviton ':' bucket panic, retrying")
	}
	return
}

// installWithFee sends an install without asking the daemon for an estimate. Used when the estimator panics on a
// DOC (graviton refuses a simulated SCID starting with ':', and the simulated SCID is a function of the content, so
// retrying never helps). Storage gas for a DOC is its code size plus a small overhead (measured +348).
func installWithFee(w *walletapi.Wallet_Disk, args rpc.Arguments, fee uint64) (string, error) {
	tx, err := w.TransferPayload0(nil, 2, false, args, fee, false)
	if err != nil {
		return "", err
	}
	if err := w.SendTransaction(tx); err != nil {
		return "", err
	}
	return tx.GetHash().String(), nil
}

func main() {
	wfile := flag.String("wallet", "", "wallet file")
	pass := flag.String("password", "", "wallet password")
	daemon := flag.String("daemon", "127.0.0.1:20000", "daemon endpoint")
	appDir := flag.String("app", "../app", "app directory")
	durl := flag.String("durl", "dmz.tela", "dURL")
	name := flag.String("name", "DMZ", "app name header")
	descr := flag.String("descr", "DMZ: social DEX and launchpad for DERO assets", "app description header")
	icon := flag.String("icon", "", "icon URL")
	index := flag.String("index", "", "existing INDEX scid to update")
	serve := flag.Bool("serve", false, "serve the INDEX after install")
	out := flag.String("out", "tela-deployment.json", "deployment record")
	reuseFile := flag.String("reuse", "", "file of \"<relative path> <scid>\" lines: DOCs already installed from this exact bundle, pointed at instead of reinstalled")
	network := flag.String("network", "simulator", "simulator, testnet or mainnet (sets the wallet and TELA network flags)")
	yes := flag.Bool("yes", false, "required on mainnet: every DOC and the INDEX cost real DERO")
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
	globals.Arguments = map[string]interface{}{"--testnet": testnet, "--simulator": simulator, "--daemon-address": *daemon, "--debug": false}
	globals.Initialize()
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

	// collect files (index.html must be first: DOC1 is the entrypoint)
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
	sort.SliceStable(files, func(i, j int) bool {
		if files[i] == "index.html" {
			return true
		}
		if files[j] == "index.html" {
			return false
		}
		return files[i] < files[j]
	})

	dep := deployment{Network: *network, DURL: *durl, Docs: map[string]string{}, Hashes: map[string]string{}, Time: time.Now().UTC().Format(time.RFC3339)}
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
		if h := sha256.Sum256([]byte(live.Code)); hex.EncodeToString(h[:]) != codeHash {
			must(fmt.Errorf("on-chain code of %s… does not match this bundle (%x… vs %s…)", scid[:16], h[:8], codeHash[:16]), "reuse "+rel)
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
			fmt.Printf("DOC %-22s already on chain (-reuse, code hash verified), pointing at %s\n", rel, scid[:16])
			dep.Docs[rel] = scid
			docSCIDs = append(docSCIDs, scid)
			continue
		}
		if prevDocs[rel] != "" && prevHashes[rel] == codeHash { // an unchanged DOC is immutable on chain: point the INDEX at it again
			verifyDoc(prevDocs[rel], codeHash, rel)
			fmt.Printf("DOC %-22s unchanged (code hash verified on chain), reusing %s\n", rel, prevDocs[rel][:16])
			dep.Docs[rel] = prevDocs[rel]
			docSCIDs = append(docSCIDs, prevDocs[rel])
			continue
		}
		gas, err := estimate(w, args)
		var txid string
		if err != nil && strings.Contains(err.Error(), "Bucket cannot start") {
			gas = uint64(len(code)) + 1024 // measured overhead is a few hundred units; the margin is cheap, a reverted install is not
			fmt.Printf("estimate unavailable for %s (the daemon cannot simulate this DOC): sending with a size-based fee of %d\n", rel, gas)
			txid, err = installWithFee(w, args, gas)
		} else {
			must(err, "estimate "+rel)
			txid, err = tela.Installer(w, 2, doc)
		}
		must(err, "install "+rel)
		fmt.Printf("DOC %-22s %6d bytes → %6d on-chain (gzip)  gas %5d  scid %s\n", rel, len(data), len(code), gas, txid)
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
		gas, err := estimate(w, args)
		must(err, "index estimate")
		txid, err := tela.Installer(w, 2, idx)
		must(err, "index install")
		fmt.Printf("INDEX %s gas %d (%d DOCs, %.2f KB)\n", txid, gas, len(docSCIDs), tela.GetCodeSizeInKB(args.Value(rpc.SCCODE, rpc.DataString).(string)))
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
	dep.Commit = 0
	if v, _, err := tela.KeyExists(dep.Index, "commit", *daemon); err == nil {
		fmt.Sscanf(v, "%d", &dep.Commit)
	}
	fmt.Printf("INDEX %s dURL %s author %s docs %d commit %d\n", live.SCID, live.DURL, live.Author, len(live.DOCs), dep.Commit)
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
