// profiledoc builds a DMZ profile doc: a DVM contract with no entrypoint and no storage whose code carries a logo
// (or the Founders Pass image) and a links JSON, byte for byte what app/profile.js builds and reads. It takes an
// image that is already encoded, WebP or PNG (the app's browser build encodes one, at #profiledoc), prints the
// doc's size, sha256 and storage gas, and can install it through a wallet's RPC and read it back through the node.
// Standard library only. docs/PROFILE_DOCS.md is the format and the runbook.
//
//	go run ./profiledoc -image logo.webp [-website URL] [-x URL] [-telegram URL] [-discord URL] [-github URL] [-out doc.bas]
//	go run ./profiledoc -image pass.webp -kind pass -install -network simulator -wallet 127.0.0.1:30008
//	go run ./profiledoc -read <scid>
//
// The wallet's RPC login, when it asks for one, comes from VEIL_RPC_LOGIN (user:password), as for simdeploy. An
// install on -network mainnet is refused without -yes, without that login, or with a wallet not on this machine.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	rpc "veil/tools/simrpc"
)

var (
	image    = flag.String("image", "", "the encoded image: WebP or PNG (logo: at most 8,192 bytes and 256x256; pass: 14,000 bytes and 384x384)")
	kind     = flag.String("kind", "logo", "logo (a token's or NFA's logo, with links) or pass (the Founders Pass image, no links)")
	website  = flag.String("website", "", "https link, any host")
	xLink    = flag.String("x", "", "https link on x.com or twitter.com")
	telegram = flag.String("telegram", "", "https link on t.me")
	discord  = flag.String("discord", "", "https link on discord.gg or discord.com")
	github   = flag.String("github", "", "https link on github.com")
	out      = flag.String("out", "", "also write the doc's contract code to this file")
	daemon   = flag.String("daemon", "127.0.0.1:20000", "daemon rpc: for -install and -read")
	install  = flag.Bool("install", false, "install the doc through -wallet, then read it back through -daemon")
	wallet   = flag.String("wallet", "", "installer wallet rpc, host:port; its login comes from VEIL_RPC_LOGIN")
	network  = flag.String("network", "", "simulator, testnet or mainnet: required with -install and checked against the daemon")
	yes      = flag.Bool("yes", false, "required to install on mainnet: the install spends real DERO")
	readScid = flag.String("read", "", "read the doc at this scid through -daemon, check it as the app does, and print it")
	timeout  = flag.Duration("timeout", 180*time.Second, "install confirmation timeout")
)

func main() {
	flag.Parse()
	log.SetFlags(0)
	c := &rpc.Client{Daemon: *daemon, Timeout: *timeout}
	if *readScid != "" {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(*readScid) {
			log.Fatal("-read takes a contract id: 64 lowercase hex")
		}
		readBack(c, *readScid)
		return
	}
	var img []byte
	if *image != "" {
		b, err := os.ReadFile(*image)
		rpc.Must(err)
		img = b
	}
	code, err := Compose(*kind, img, Links{*website, *xLink, *telegram, *discord, *github})
	if err != nil {
		log.Fatal(err)
	}
	d, _ := Parse(code)
	describe(d, code)
	if *out != "" {
		rpc.Must(os.WriteFile(*out, []byte(code), 0o644))
		fmt.Println("written  ", *out)
	}
	if !*install {
		return
	}
	if err := guard(c); err != nil {
		log.Fatal(err)
	}
	scid, gas, height := c.Send(*wallet, rpc.Address(*wallet), code, "", nil, nil)
	fmt.Printf("installed %s at block %d: storage gas %d (node estimate), fee %s DERO\n", scid, height, gas.GasStorage, dero(gas.GasStorage+200))
	fmt.Println("reference dmz:" + scid)
	readBack(c, scid)
}

func guard(c *rpc.Client) error {
	switch *network {
	case "simulator", "testnet", "mainnet":
	default:
		return errors.New("-install needs -network simulator, testnet or mainnet")
	}
	if *wallet == "" {
		return errors.New("-install needs -wallet host:port")
	}
	if *network == "mainnet" {
		if !*yes {
			return errors.New("mainnet: this install spends real DERO; pass -yes to run it")
		}
		if err := rpc.RequireWalletLogin(*wallet); err != nil {
			return err
		}
		if err := rpc.RequireLoopbackWallets(*wallet); err != nil {
			return err
		}
	}
	if testnet, name := c.Info(); (*network == "mainnet") == testnet {
		return fmt.Errorf("-network %s, but the daemon at %s reports %q (testnet %v)", *network, *daemon, name, testnet)
	}
	return nil
}

func describe(d *Doc, code string) {
	fmt.Println("kind     ", d.Kind)
	if d.Image != nil {
		s := sha256.Sum256(d.Image)
		fmt.Printf("image     %s %dx%d, %d bytes, sha256 %s\n", d.Type, d.W, d.H, len(d.Image), hex.EncodeToString(s[:]))
	}
	if d.Links != nil {
		j, _ := json.Marshal(d.Links)
		fmt.Println("links    ", string(j))
	}
	s := sha256.Sum256([]byte(code))
	gas := uint64(len(code)) + 25
	fmt.Printf("code      %d bytes, sha256 %s\n", len(code), hex.EncodeToString(s[:]))
	fmt.Printf("gas       storage %d of 20000 (code length + 25, measured on the simulator); fee %s DERO (storage gas + 200)\n", gas, dero(gas+200))
}

func readBack(c *rpc.Client, scid string) {
	st := c.GetSC(scid, true)
	d, err := Parse(st.Code)
	if err != nil {
		log.Fatalf("read back %s: not a valid profile doc: %v", scid, err)
	}
	fmt.Println("read back", scid, "through", *daemon+": valid")
	describe(d, st.Code)
}

func dero(atomic uint64) string { return fmt.Sprintf("%d.%05d", atomic/100000, atomic%100000) }
