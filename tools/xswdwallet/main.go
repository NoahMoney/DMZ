// xswdwallet: a headless XSWD wallet for automated tests. Opens a wallet file, connects it to a daemon and starts
// the fork's XSWD server on 44326 with every application and request auto-approved (AlwaysAllow).
// TEST ONLY — never use with a wallet holding real funds. Built inside vendor/derohe-civilware (see build.sh).
//
//	xswdwallet --wallet=<file.db> --password= --daemon=127.0.0.1:20000 [--port=44326]
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/walletapi"
	"github.com/deroproject/derohe/walletapi/xswd"
)

func main() {
	file := flag.String("wallet", "", "wallet file")
	pass := flag.String("password", "", "wallet password")
	daemon := flag.String("daemon", "127.0.0.1:20000", "daemon endpoint")
	port := flag.Int("port", xswd.XSWD_PORT, "xswd port")
	flag.Parse()
	if *file == "" {
		fmt.Println("--wallet required")
		os.Exit(2)
	}
	if *pass == "" {
		*pass = os.Getenv("VEIL_WALLET_PASSWORD")
	}
	guardTestOnly(*daemon) // loopback daemon on a test network, or exit
	globals.Arguments = map[string]interface{}{"--testnet": true, "--simulator": true, "--daemon-address": *daemon, "--debug": false}
	globals.Initialize()
	walletapi.Initialize_LookupTable(1, 1<<21) // same table as dero-wallet-cli: balance decryption is a search
	globals.Logger.Info("xswdwallet", "wallet", *file, "daemon", *daemon)

	w, err := walletapi.Open_Encrypted_Wallet(*file, *pass)
	if err != nil {
		fmt.Println("open wallet:", err)
		os.Exit(1)
	}
	w.SetNetwork(false)
	w.SetDaemonAddress(*daemon)
	w.SetOnlineMode()
	go walletapi.Keep_Connectivity()
	for i := 0; i < 60 && !walletapi.IsDaemonOnline(); i++ {
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("address:", w.GetAddress().String())
	fmt.Println("daemon online:", walletapi.IsDaemonOnline())

	server := xswd.NewXSWDServerWithPort(*port, w, false, nil,
		func(app *xswd.ApplicationData) bool {
			fmt.Printf("APP %s (%s) -> allowed\n", app.Name, app.Url)
			return true
		},
		func(app *xswd.ApplicationData, r *jrpc2.Request) xswd.Permission {
			fmt.Printf("REQ %s %s -> allow\n", app.Name, r.Method())
			return xswd.AlwaysAllow
		})
	if !server.IsRunning() {
		fmt.Println("xswd did not start")
		os.Exit(1)
	}
	fmt.Printf("XSWD listening on :%d\n", *port)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	server.Stop()
	w.Close_Encrypted_Wallet()
}
