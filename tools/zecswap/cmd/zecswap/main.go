// zecswap is the Zcash-side companion of the transparent-Zcash swap market (docs/ZEC_SWAP.md §7). This build has
// the part that needs no node: the script both parties must agree on, its address, and what each transparent
// address form means. The watcher and the spends come next (BL-40b).
//
//	zecswap script  {-hash <64 lower-case hex> | -secret <64 lower-case hex>} -claim <t-address or 40-hex key hash>
//	                -refund <t-address or 40-hex key hash> -cltv <height> -tip <Zcash height> [-net testnet]
//	zecswap address <t-address>
//
// Nothing here holds a key or talks to anything.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"

	"veil/tools/zecswap"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "script":
		script(os.Args[2:])
	case "address":
		if len(os.Args) != 3 {
			usage()
		}
		net, kind, hash, err := zecswap.ParseAddress(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Printf("network %s\nkind    %s\nhash160 %x\n", net, kind, hash[:])
	default:
		usage()
	}
}

func script(args []string) {
	fs := flag.NewFlagSet("script", flag.ExitOnError)
	hashHex := fs.String("hash", "", "the board's h_<id>: SHA256 of the maker's 32-byte secret, 64 lower-case hex")
	secretHex := fs.String("secret", "", "the maker's own secret, 64 lower-case hex (32 bytes); the hash is derived from it. Use instead of -hash")
	claim := fs.String("claim", "", "the maker's transparent address (the offer's wantAddr) or its 40-hex key hash")
	refund := fs.String("refund", "", "the taker's transparent address or its 40-hex key hash (the accept's refundPkh)")
	cltv := fs.Uint("cltv", 0, "the Zcash height after which the taker may refund (the accept's cltv)")
	tip := fs.Uint("tip", 0, "the current Zcash height, from the node; -cltv is checked against it and the policy of docs/ZEC_SWAP.md §5. 0 means the swap is already funded and only the form of -cltv is checked")
	netName := fs.String("net", "testnet", "testnet or mainnet: only the address prefix depends on it")
	fs.Parse(args)
	var h zecswap.HTLC
	switch {
	case (*hashHex == "") == (*secretHex == ""):
		must(fmt.Errorf("give exactly one of them"), "-hash/-secret")
	case *secretHex != "":
		// The secret's length is not cosmetic: contracts/swap.bas Claim takes 32 bytes and nothing else, so a
		// secret of any other length opens the Zcash leg and never the DERO one.
		s, err := zecswap.ParseSecret(*secretHex)
		must(err, "-secret")
		h.Hash = sha256.Sum256(s[:])
		fmt.Printf("hash          %x (SHA256 of the secret; this is what Post and Lock carry)\n", h.Hash[:])
	default:
		var err error
		h.Hash, err = zecswap.ParseSwapHash(*hashHex)
		must(err, "-hash")
	}
	must(keyHash(&h.ClaimPkh, *claim), "-claim")
	must(keyHash(&h.RefundPkh, *refund), "-refund")
	if *cltv > 0xffffffff || *tip > 0xffffffff {
		must(fmt.Errorf("a Zcash height is a uint32"), "-cltv/-tip")
	}
	if *tip == 0 {
		must(zecswap.CheckCLTVHeight(uint32(*cltv)), "-cltv")
	} else {
		must(zecswap.CheckCLTV(uint32(*cltv), uint32(*tip)), "-cltv")
	}
	h.CLTV = uint32(*cltv)
	net := zecswap.Testnet
	switch *netName {
	case "testnet":
	case "mainnet":
		net = zecswap.Mainnet
	default:
		must(fmt.Errorf("testnet or mainnet"), "-net")
	}
	fmt.Printf("redeem script %x (%d bytes)\nscriptPubKey  %x\naddress       %s (%s)\nnLockTime     %d (the refund transaction's, at least)\n",
		h.RedeemScript(), len(h.RedeemScript()), h.ScriptPubKey(), h.Address(net), net, h.CLTV)
}

// keyHash takes either a transparent P2PKH address or a bare 40-hex key hash; a P2SH address is refused, since
// the script pays a key, not another script.
func keyHash(dst *[20]byte, s string) error {
	if len(s) == 40 {
		return decodeInto(dst[:], s)
	}
	_, kind, hash, err := zecswap.ParseAddress(s)
	if err != nil {
		return err
	}
	if kind != zecswap.P2PKH {
		return fmt.Errorf("%s is a script address; the swap script pays a key", s)
	}
	*dst = hash
	return nil
}

func decodeInto(dst []byte, s string) error {
	b, err := hex.DecodeString(s)
	if err != nil {
		return err
	}
	if len(b) != len(dst) {
		return fmt.Errorf("%d bytes, want %d", len(b), len(dst))
	}
	copy(dst, b)
	return nil
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "zecswap script {-hash H|-secret S} -claim <t-addr|pkh> -refund <t-addr|pkh> -cltv N -tip <Zcash height> [-net testnet|mainnet]\nzecswap address <t-addr>")
	os.Exit(2)
}
