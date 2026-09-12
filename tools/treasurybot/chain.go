package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	rpc "veil/tools/simrpc"
)

// deroChain is the journal's view of the DERO side through simrpc. Send waits for inclusion and stops the process
// on a wallet failure; a stop between a send and the journal write is exactly the crash the journal reconciles.
// Inclusion is not execution: the journal proves a claim by the market's fc counter and a deposit by the pass's
// per-asset ad_<i> counter, never by the txid.
type deroChain struct {
	c            *rpc.Client
	me, founders string
}

func (d *deroChain) Fees(scid string) (uint64, uint64) {
	st := d.c.GetSC(scid, false)
	return st.U("fd"), st.U("fa")
}

// Legs reports a market's two asset ids. The quote is the stored qt as 64 hex characters (a v1 market has no qt key
// and is DERO-quoted). The asset leg is a pool's stored asset, and a launch's own SCID, which is its token.
func (d *deroChain) Legs(scid string, launch bool) (string, string) {
	st := d.c.GetSC(scid, false)
	quote := deroID
	if st.Has("qt") {
		quote = hex.EncodeToString([]byte(st.Str("qt")))
	}
	asset := scid
	if !launch {
		if !st.Has("asset") {
			return quote, ""
		}
		asset = hex.EncodeToString([]byte(st.Str("asset")))
	}
	return quote, asset
}

func (d *deroChain) FC(scid string) uint64 { return d.c.GetSC(scid, false).U("fc") }

// PassDep is founders.bas v2's ad_<i> for the asset, i = ai_<hex id> - 1: every unit ever credited to that asset's
// pool, by Deposit or by a market fee. 0 while the pass has no index for the asset.
func (d *deroChain) PassDep(asset string) uint64 {
	st := d.c.GetSC(d.founders, false)
	k := "ai_" + asset
	if !st.Has(k) {
		return 0
	}
	i := st.U(k)
	if i == 0 {
		return 0
	}
	return st.U0("ad_" + strconv.FormatUint(i-1, 10))
}

func (d *deroChain) Registered() uint64 { return d.c.GetSC(d.founders, false).U0("hst") }

func (d *deroChain) SendClaim(scid string) (string, error) {
	txid, _, _ := d.c.Send(*wallet, d.me, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimFees")})
	if len(txid) < 8 {
		return "", errors.New("the wallet returned no txid")
	}
	return txid, nil
}

// SendDeposit calls Deposit(asset) with the units attached in that asset: DERO for the zero id, the token otherwise.
func (d *deroChain) SendDeposit(asset string, units uint64) (string, error) {
	x := rpc.Transfer{SCID: asset, Burn: units}
	if asset == deroID {
		x = rpc.Transfer{Destination: d.c.RandomAddress(), Burn: units}
	}
	txid, _, _ := d.c.Send(*wallet, d.me, "", d.founders, []rpc.Transfer{x}, []rpc.Arg{rpc.S("entrypoint", "Deposit"), rpc.H("asset", asset)})
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
func (d dryChain) SendDeposit(asset string, units uint64) (string, error) {
	fmt.Printf("dry run: would deposit %d units of %s into the founders pass\n", units, short(asset))
	return "", errDry
}
