// netwallet: a headless DERO wallet with the standard wallet JSON-RPC server, for any network. Creates the wallet
// file on first use and prints the address (and, on creation, the recovery seed to a .seed file next to the wallet).
// Used to run owner / treasury / signer wallets for testnet and mainnet deployments the same way the simulator
// wallets are used: tools talk to them over -rpc-bind. Built inside vendor/derohe (see build.sh).
//
//	netwallet -network testnet -daemon 127.0.0.1:40402 -wallet ../.testnet/owner.db -password '' -rpc-bind 127.0.0.1:40403 [-create]
package main

import (
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deroproject/derohe/cryptography/bn256"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/transaction"
	"github.com/deroproject/derohe/walletapi"
	"github.com/deroproject/derohe/walletapi/rpcserver"
)

func main() {
	file := flag.String("wallet", "", "wallet file")
	pass := flag.String("password", "", "wallet password")
	daemon := flag.String("daemon", "127.0.0.1:40402", "daemon endpoint")
	network := flag.String("network", "testnet", "simulator, testnet or mainnet")
	bind := flag.String("rpc-bind", "127.0.0.1:40403", "wallet rpc bind address")
	create := flag.Bool("create", false, "create the wallet if the file does not exist")
	writeSeed := flag.Bool("write-seed", false, "on -create, also write the recovery seed to <wallet>.seed (mode 0600); refused on mainnet. Without it the seed is printed once and stored nowhere")
	rpcLogin := flag.String("rpc-login", "", "user:password the wallet RPC requires (basic auth); or VEIL_RPC_LOGIN. Required on mainnet: every local process can otherwise drive this wallet")
	flag.Parse()
	if *file == "" {
		fmt.Println("-wallet required")
		os.Exit(2)
	}
	var testnet, simulator bool
	switch *network {
	case "simulator":
		testnet, simulator = true, true
	case "testnet":
		testnet = true
	case "mainnet":
	default:
		fmt.Println("-network must be simulator, testnet or mainnet")
		os.Exit(2)
	}
	// the password may come from the environment instead of the command line (argv is visible to every process)
	if *pass == "" {
		*pass = os.Getenv("VEIL_WALLET_PASSWORD")
	}
	if *network == "mainnet" && *pass == "" {
		fmt.Println("a mainnet wallet needs a password: -password or VEIL_WALLET_PASSWORD")
		os.Exit(2)
	}
	if *network == "mainnet" && *writeSeed {
		fmt.Println("-write-seed is refused on mainnet: write the words down from the console")
		os.Exit(2)
	}
	if *rpcLogin == "" {
		*rpcLogin = os.Getenv("VEIL_RPC_LOGIN")
	}
	if *rpcLogin != "" && !strings.Contains(*rpcLogin, ":") {
		fmt.Println("-rpc-login must be user:password")
		os.Exit(2)
	}
	if *network == "mainnet" && *rpcLogin == "" {
		fmt.Println("a mainnet wallet RPC needs -rpc-login user:password (or VEIL_RPC_LOGIN)")
		os.Exit(2)
	}
	// the seed is shown once: only on a console, never into a pipe or a log file
	onTerminal := func() bool { fi, err := os.Stdout.Stat(); return err == nil && fi.Mode()&os.ModeCharDevice != 0 }
	globals.Arguments = map[string]interface{}{"--testnet": testnet, "--simulator": simulator, "--daemon-address": *daemon, "--debug": false, "--rpc-bind": *bind, "--rpc-server": true}
	if *rpcLogin != "" {
		globals.Arguments["--rpc-login"] = *rpcLogin
	}
	globals.Initialize()
	walletapi.Initialize_LookupTable(1, 1<<23) // 8 M entries: a balance decode walks 2^23 units per step (a 100 M DERO premine took minutes per block at 2^21)

	var w *walletapi.Wallet_Disk
	var err error
	if _, statErr := os.Stat(*file); os.IsNotExist(statErr) {
		if !*create {
			fmt.Println("wallet file does not exist; pass -create to make one")
			os.Exit(1)
		}
		if !*writeSeed && !onTerminal() {
			fmt.Println("stdout is not a terminal: the recovery seed would land in a file or a log. Run -create from a console (or, off mainnet, pass -write-seed)")
			os.Exit(2)
		}
		w, err = walletapi.Create_Encrypted_Wallet_Random(*file, *pass)
		if err != nil {
			fmt.Println("create wallet:", err)
			os.Exit(1)
		}
		w.SetNetwork(!testnet)
		seed := w.GetSeed()
		if *writeSeed {
			seedFile := strings.TrimSuffix(*file, filepath.Ext(*file)) + ".seed"
			if err := os.WriteFile(seedFile, []byte(seed+"\n"), 0o600); err != nil {
				fmt.Println("write seed:", err)
				os.Exit(1)
			}
			fmt.Println("created wallet; recovery seed written to", seedFile)
		} else {
			fmt.Println("created wallet. Recovery seed, shown once and stored nowhere else:")
			fmt.Println(seed)
		}
	} else {
		w, err = walletapi.Open_Encrypted_Wallet(*file, *pass)
		if err != nil {
			fmt.Println("open wallet:", err)
			os.Exit(1)
		}
		w.SetNetwork(!testnet)
	}
	w.SetDaemonAddress(*daemon)
	w.SetOnlineMode()
	go walletapi.Keep_Connectivity()
	for i := 0; i < 60 && !walletapi.IsDaemonOnline(); i++ {
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("network:", *network)
	fmt.Println("address:", w.GetAddress().String())
	fmt.Println("daemon online:", walletapi.IsDaemonOnline())
	srv, err := rpcserver.RPCServer_Start(w, "netwallet")
	if err != nil {
		fmt.Println("rpc server:", err)
		os.Exit(1)
	}
	fmt.Println("wallet rpc on", *bind)
	// a DERO account must be registered on chain before it can receive anything (mining rewards included);
	// the registration is a proof-of-work transaction the wallet computes itself (RPC is already up: reads work,
	// transfers fail until the registration is mined)
	if walletapi.IsDaemonOnline() && !w.IsRegistered() {
		fmt.Println("account not registered: computing the registration proof of work (this can take minutes)")
		// the daemon accepts a registration only when the tx hash starts with three zero bytes (what
		// dero-wallet-cli searches for); every attempt is a fresh signature, so search on all cores
		// same construction as walletapi.GetRegistrationTX / sign(), but with the constant parts hoisted and no
		// shared state, so the search actually scales across cores
		keys := w.Get_Keys()
		pubG1 := keys.Public.G1().String()
		pubCompressed := keys.Public.EncodeCompressed()
		secret := keys.Secret.BigInt()
		var mu sync.Mutex
		var found *transaction.Transaction
		var done atomic.Bool
		var tries atomic.Int64
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < runtime.NumCPU(); i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var tmppoint bn256.G1
				for !done.Load() {
					tmpsecret := crypto.RandomScalar()
					tmppoint.ScalarMult(crypto.G, tmpsecret)
					c := crypto.ReducedHash([]byte(pubG1 + tmppoint.String()))
					s := new(big.Int).Mul(c, secret)
					s.Mod(s, bn256.Order)
					s.Add(s, tmpsecret)
					s.Mod(s, bn256.Order)
					t := new(transaction.Transaction)
					t.Version = 1
					t.TransactionType = transaction.REGISTRATION
					copy(t.MinerAddress[:], pubCompressed)
					crypto.FillBytes(c, t.C[:])
					crypto.FillBytes(s, t.S[:])
					tries.Add(1)
					h := t.GetHash()
					if h[0] == 0 && h[1] == 0 && h[2] == 0 && t.IsRegistrationValid() {
						mu.Lock()
						if found == nil {
							found = t
							done.Store(true)
						}
						mu.Unlock()
						return
					}
				}
			}()
		}
		go func() {
			for !done.Load() {
				time.Sleep(30 * time.Second)
				if !done.Load() {
					fmt.Printf("registration search: %d attempts, %.0f/s\n", tries.Load(), float64(tries.Load())/time.Since(start).Seconds())
				}
			}
		}()
		wg.Wait()
		tx := found
		fmt.Printf("registration proof found in %s\n", time.Since(start).Round(time.Second))
		if err := w.SendTransaction(tx); err != nil {
			fmt.Println("registration failed:", err)
		} else {
			fmt.Println("registration sent:", tx.GetHash().String())
		}
	}
	go func() {
		for {
			time.Sleep(60 * time.Second)
			w.Save_Wallet()
		}
	}()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	srv.RPCServer_Stop()
	w.Save_Wallet()
	w.Close_Encrypted_Wallet()
}
