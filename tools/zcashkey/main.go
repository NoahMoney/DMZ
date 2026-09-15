// zcashkey is the Zcash vault's key ceremony and audit tool. It never prints, logs or sends a seed or a private key:
// the only secret it touches is the seed file it writes or reads, and what leaves it is public.
//
//	zcashkey -new <seed file> -network regtest|testnet|mainnet [-account 0]
//	    a new seed, written owner-only outside every repository; prints the signer's account xpub
//	zcashkey -xpub <seed file> -network … [-account 0]
//	    the account xpub of an existing seed
//	zcashkey -descriptor -network … -k 2 -xpubs A,B,C -vault <scid> [-indexes N]
//	    the vault's descriptor, the view key to publish with SetViewKey, the change address and N deposit addresses
//	zcashkey -audit -viewkey "<published view key>" -vault <scid> -indexes N -zcash <url> [-cookie <file>]
//	    every vault address's balance on a node of the auditor's choosing, and the reserve: what anyone can check
//	    against the vault's sup less its withdrawals in flight
//
// See research/wrapper/RUNBOOKS.md (the Zcash key ceremony).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"veil/tools/vaultsigner/zcashvault"
	"veil/tools/zcashtx"
)

var (
	newSeed    = flag.String("new", "", "write a new seed to this file and print its account xpub")
	xpubOf     = flag.String("xpub", "", "print the account xpub of this seed file")
	descriptor = flag.Bool("descriptor", false, "print the vault's descriptor, view key and addresses")
	audit      = flag.Bool("audit", false, "sum the vault's reserve on a Zcash node")
	network    = flag.String("network", "", "the Zcash network: regtest, testnet or mainnet (no default)")
	account    = flag.Uint("account", 0, "the BIP 44 account index (hardened)")
	k          = flag.Int("k", 0, "signatures required")
	xpubs      = flag.String("xpubs", "", "the signers' account xpubs, comma-separated, any order")
	vault      = flag.String("vault", "", "the vault's DERO scid")
	indexes    = flag.Uint64("indexes", 0, "deposit addresses to list or audit: the vault's un")
	viewkey    = flag.String("viewkey", "", "-audit: the descriptor as published with SetViewKey")
	zcash      = flag.String("zcash", "", "-audit: the Zcash node's RPC URL")
	cookie     = flag.String("cookie", "", "-audit: the node's cookie file")
)

func main() {
	flag.Parse()
	log.SetFlags(0)
	switch {
	case *newSeed != "":
		if err := zcashvault.GenerateSeed(*newSeed); err != nil {
			log.Fatal(err)
		}
		printXPub(*newSeed)
	case *xpubOf != "":
		printXPub(*xpubOf)
	case *descriptor:
		d, err := zcashvault.NewDescriptor(*network, *k, strings.Split(*xpubs, ","), *vault)
		if err != nil {
			log.Fatal(err)
		}
		show(d)
	case *audit:
		d, err := zcashvault.ParseDescriptorText(*viewkey, *vault)
		if err != nil {
			log.Fatal(err)
		}
		runAudit(d)
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func printXPub(seed string) {
	if *network == "" {
		log.Fatal("-network is required (regtest, testnet or mainnet); it has no default")
	}
	x, err := zcashvault.AccountXPub(seed, *network, uint32(*account))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(x)
}

func show(d *zcashvault.Descriptor) {
	ch, err := d.Change()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("descriptor %s\nview key   %s\nbranch     %d\nchange     %s\n", d.Text(), d.ViewKey(), d.Branch(), ch.Address)
	for j := uint64(0); j < *indexes; j++ {
		a, err := d.DepositAddress(j)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("deposit %d  %s\n", j, a)
	}
}

func runAudit(d *zcashvault.Descriptor) {
	var user, pass string
	if *cookie != "" {
		var err error
		if user, pass, err = zcashtx.ReadCookie(*cookie); err != nil {
			log.Fatal(err)
		}
	}
	zc := zcashtx.NewZcashRPC(*zcash, user, pass)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ci, err := zc.ChainInfo(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if want := map[zcashvault.Net]string{zcashvault.NetMainnet: "main", zcashvault.NetTestnet: "test", zcashvault.NetRegtest: "regtest"}[d.Net]; ci.Chain != want {
		log.Fatalf("the node is on Zcash %q; the vault is on %s", ci.Chain, d.Net)
	}
	var total uint64
	sum := func(label, addr string, spk []byte) {
		us, err := zc.AddressUTXOs(ctx, addr)
		if err != nil {
			log.Fatal(err)
		}
		var v uint64
		for _, u := range us {
			if string(u.Script) != string(spk) {
				log.Fatalf("the node lists an output of %s paying another script", addr)
			}
			v += u.Value
		}
		total += v
		fmt.Printf("%-10s %s  %d zatoshi in %d outputs\n", label, addr, v, len(us))
	}
	ch, err := d.Change()
	if err != nil {
		log.Fatal(err)
	}
	sum("change", ch.Address, ch.ScriptPubKey)
	for j := uint64(0); j < *indexes; j++ {
		vs, err := d.Script(zcashvault.ChainDeposit, uint32(j))
		if err != nil {
			log.Fatal(err)
		}
		sum(fmt.Sprintf("deposit %d", j), vs.Address, vs.ScriptPubKey)
	}
	fmt.Printf("reserve    %d zatoshi at Zcash height %d (block %s); compare with the vault's sup less its open withdrawals\n", total, ci.Height, ci.BestHash)
}
