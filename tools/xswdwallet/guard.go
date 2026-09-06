package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// guardTestOnly refuses to start the auto-approving wallet against anything but a loopback daemon that reports
// a test network. The fork's XSWD server listens on every interface and approves every request: on mainnet, or
// against a remote daemon, that is a wallet anyone on the network can spend from.
func guardTestOnly(daemon string) {
	host, _, err := net.SplitHostPort(daemon)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		fmt.Println("xswdwallet only runs against a daemon on this device (127.0.0.1:port); got", daemon)
		os.Exit(2)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"DERO.GetInfo"}`)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://"+daemon+"/json_rpc", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Println("daemon not reachable:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var r struct {
		Result struct {
			Testnet bool   `json:"testnet"`
			Network string `json:"network"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		fmt.Println("daemon answered garbage:", err)
		os.Exit(1)
	}
	if !r.Result.Testnet || strings.Contains(strings.ToLower(r.Result.Network), "mainnet") {
		fmt.Println("refusing: the daemon is not a test network (auto-approval must never face real funds)")
		os.Exit(2)
	}
	fmt.Printf("daemon %s reports %s (testnet): auto-approval allowed. This wallet listens on every interface; keep the machine off untrusted networks.\n", daemon, r.Result.Network)
}
