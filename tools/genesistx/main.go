// genesistx prints a serialized genesis transaction that registers one account and credits it a premine, for a
// private DERO testnet (derod reads it from DERO_PRIVATE_GENESIS_TX; see the Veil patch in cmd/derod/main.go).
// This is what derohe's own simulator tests do to bootstrap a chain. Built inside vendor/derohe (build.sh).
//
//	genesistx -seed-file ../.testnet/owner.seed -premine 10000000000000   # 100,000,000 DERO in atomic units? no: 1e13 = 100,000,000.00000
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deroproject/derohe/blockchain"
	"github.com/deroproject/derohe/config"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/transaction"
	"github.com/deroproject/derohe/walletapi"
)

func main() {
	seedFile := flag.String("seed-file", "", "file holding the recovery words of the genesis account")
	premine := flag.Uint64("premine", 100_000_000_00000, "atomic units credited to the account at genesis (default 100,000,000 DERO)")
	flag.Parse()
	if *seedFile == "" {
		fmt.Println("-seed-file required")
		os.Exit(2)
	}
	b, err := os.ReadFile(*seedFile)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	globals.Arguments = map[string]interface{}{"--testnet": true, "--simulator": false, "--debug": false}
	globals.Initialize()
	// the seed is restored into a private temporary directory under a random password and removed again;
	// a fixed path with a fixed password left the restored wallet readable by anyone on the machine
	dir, err := os.MkdirTemp("", "veil-genesis-")
	if err != nil {
		fmt.Println("tempdir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	var pw [32]byte
	if _, err := rand.Read(pw[:]); err != nil {
		fmt.Println("random:", err)
		os.Exit(1)
	}
	tmp := filepath.Join(dir, "genesis.db")
	w, err := walletapi.Create_Encrypted_Wallet_From_Recovery_Words(tmp, hex.EncodeToString(pw[:]), strings.TrimSpace(string(b)))
	if err != nil {
		fmt.Println("restore:", err)
		os.Exit(1)
	}
	w.SetNetwork(false)
	addr := w.GetAddress()
	w.Close_Encrypted_Wallet()
	os.RemoveAll(dir)

	tx := transaction.Transaction{Transaction_Prefix: transaction.Transaction_Prefix{Version: 1, Value: *premine}}
	copy(tx.MinerAddress[:], addr.PublicKey.EncodeCompressed())
	hexTx := fmt.Sprintf("%x", tx.Serialize())
	config.Testnet.Genesis_Tx = hexTx
	globals.Config.Genesis_Tx = hexTx // Generate_Genesis_Block reads the copy globals.Initialize made
	gb := blockchain.Generate_Genesis_Block()
	fmt.Printf("account   %s\npremine   %d atomic units\ngenesis   %s\n", addr.String(), *premine, gb.GetHash())
	fmt.Printf("DERO_PRIVATE_GENESIS_TX=%s\n", hexTx)
}
