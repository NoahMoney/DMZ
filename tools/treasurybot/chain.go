package main

import (
	"errors"
	"fmt"

	rpc "veil/tools/simrpc"
)

// deroChain is the journal's view of the DERO side through simrpc. Send waits for inclusion and stops the process
// on a wallet failure; a stop between a send and the journal write is exactly the crash the journal reconciles.
// Inclusion is not execution: the journal proves a claim by the market's fc counter and a deposit by the pass's
// dep counter, never by the txid.
type deroChain struct {
	c            *rpc.Client
	me, founders string
}

func (d *deroChain) FD(scid string) uint64 { return d.c.GetSC(scid, false).U("fd") }
func (d *deroChain) FC(scid string) uint64 { return d.c.GetSC(scid, false).U("fc") }
func (d *deroChain) PassDep() uint64       { return d.c.GetSC(d.founders, false).U("dep") }
func (d *deroChain) SendClaim(scid string) (string, error) {
	txid, _, _ := d.c.Send(*wallet, d.me, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimFees")})
	if len(txid) < 8 {
		return "", errors.New("the wallet returned no txid")
	}
	return txid, nil
}
func (d *deroChain) SendDeposit(units uint64) (string, error) {
	txid, _, _ := d.c.Send(*wallet, d.me, "", d.founders, []rpc.Transfer{{Destination: d.c.RandomAddress(), Burn: units}}, []rpc.Arg{rpc.S("entrypoint", "Deposit")})
	if len(txid) < 8 {
		return "", errors.New("the wallet returned no txid")
	}
	return txid, nil
}

// errDry is what a dry run's sends return: the journal keeps its record and nothing leaves the wallet.
var errDry = errors.New("dry run: not sent")

// dryChain reads the chain and refuses to send. Reconcile on a dry run must not resend a deposit either.
type dryChain struct{ chain }

func (d dryChain) SendClaim(scid string) (string, error) {
	fmt.Printf("dry run: would send ClaimFees to %s\n", short(scid))
	return "", errDry
}
func (d dryChain) SendDeposit(units uint64) (string, error) {
	fmt.Printf("dry run: would deposit %d units into the founders pass\n", units)
	return "", errDry
}
