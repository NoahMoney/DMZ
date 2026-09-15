package main

// Promotion proceeds (contracts/promo.bas). An auction takes bids in one asset, its quote: DERO, or a DERO-chain asset
// such as wXMR. A winning bid is final once its epoch has ended, and ClaimProceeds, which anyone may send, pays every
// final bid to the treasury the contract names, in the quote, and to nobody else; its counter fc grows by exactly what
// each claim pays and nothing else moves it. The bot claims them the way it claims a market's fees, through the same
// journal (a claim record proven by fc growth), and owes the passes `share` bps of them in that same asset, deposited
// with Deposit(asset) in depositShare like every other fee asset. No contract forces that deposit; the bot makes it.
//
// Fail closed: the contract must run the promo template config.js names, and must pay THIS treasury. A promotion
// contract paying some other address would grow fc for money this wallet never received, and the journal would owe the
// passes a share of it.

import (
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"time"

	rpc "veil/tools/simrpc"
)

// promoClaimFee is the fee ClaimProceeds is sent with. The daemon's estimator runs a contract at BLOCK_HEIGHT() 0, where
// no bid is final yet, so it refuses the estimate; the DVM suite measures the call at 132 to 135 storage gas with 3 slots
// and 219 with 10 (tests/promo_test.go TestPromoGasAtMainnetParameters).
const promoClaimFee = 1000

// promoReader is what the promotion pass needs from the DERO side, narrowed so a test can fake it.
type promoReader interface {
	CodeHash(scid string) string                   // sha256 of the contract's code, hex
	PromoTreasury(scid string) string              // its treasury in the spelling ADDRESS_STRING writes ("" unreadable)
	PromoQuote(scid string) string                 // the asset its bids are in, 64 hex (deroID for DERO; "" unreadable)
	PromoClaimable(scid string) uint64             // what ClaimProceeds would pay now: pr plus every final bid not yet swept
	SendClaimProceeds(scid string) (string, error) // ClaimProceeds(), nothing attached
}

// The promotion contract config.js names, the template hashes its code must match, and the chain it is read through.
// Set in main; promoScid "" means the build names none and the pass does nothing.
var (
	promoScid string
	promoTpl  map[string]bool
	pro       promoReader
)

// promoTemplates reads the promo template hash from config.js (templates.promo; it keeps no legacy hash).
func promoTemplates(src string) map[string]bool {
	out := map[string]bool{}
	if m := regexp.MustCompile(`promo:\s*\{[^}]*?sha256:\s*"([0-9a-f]{64})"`).FindStringSubmatch(src); m != nil {
		out[m[1]] = true
	}
	return out
}

// promoMarket shows the promotion contract to the journal as a market whose quote leg is DERO: its unclaimed fees are
// what ClaimProceeds would pay, its claim is ClaimProceeds, and its fc counter is read as every market's is.
type promoMarket struct {
	chain
	p    promoReader
	scid string
}

func (m promoMarket) Fees(scid string) (uint64, uint64) {
	if scid == m.scid {
		return m.p.PromoClaimable(scid), 0
	}
	return m.chain.Fees(scid)
}

func (m promoMarket) SendClaim(scid string) (string, error) {
	if scid == m.scid {
		return m.p.SendClaimProceeds(scid)
	}
	return m.chain.SendClaim(scid)
}

// claimPromo claims the promotion proceeds once at least min units of the quote are claimable, and returns the units the
// journal now owes the passes a share of, in the quote. It refuses a contract that is not the shipped template, does not
// pay treasury (the ADDRESS_STRING spelling of this wallet's address), or whose quote cannot be read.
func claimPromo(jn *journal, ch chain, p promoReader, scid, treasury string, tpl map[string]bool, min uint64) (uint64, error) {
	if scid == "" {
		return 0, nil
	}
	if !tpl[p.CodeHash(scid)] {
		return 0, fmt.Errorf("the promotion contract %s does not run the promo template config.js names: its proceeds are not claimed", short(scid))
	}
	if t := p.PromoTreasury(scid); t == "" || t != treasury {
		return 0, fmt.Errorf("the promotion contract %s pays %q, not this treasury %q: its proceeds are neither claimed nor counted here", short(scid), t, treasury)
	}
	q := p.PromoQuote(scid)
	if !isID(q) {
		return 0, fmt.Errorf("the promotion contract %s's quote cannot be read: its proceeds are not claimed", short(scid))
	}
	if min == 0 {
		min = 1
	}
	if p.PromoClaimable(scid) < min {
		return 0, nil
	}
	units, _, err := jn.claimMarket(promoMarket{ch, p, scid}, scid, q, q)
	return units, err
}

// promoPass is the sweep's promotion step. A refusal is logged and the sweep goes on: nothing about the promotion
// contract bears on the market claims and the pass deposit.
func promoPass(jn *journal, ch chain, me string, min uint64) {
	if promoScid == "" || pro == nil {
		return
	}
	if dry {
		fmt.Printf("dry run: the promotion contract %s… has %d units of %s claimable\n", promoScid[:8], pro.PromoClaimable(promoScid), short(pro.PromoQuote(promoScid)))
		return
	}
	units, err := claimPromo(jn, ch, pro, promoScid, rpc.DVMAddress(me), promoTpl, min)
	if err != nil {
		log.Print(err)
		return
	}
	if units > 0 {
		fmt.Printf("%s claimed %d units of %s of promotion proceeds from %s…\n", time.Now().Format("15:04:05"), units, short(pro.PromoQuote(promoScid)), promoScid[:8])
	}
}

func (d *deroChain) PromoQuote(scid string) string {
	st := d.c.GetSC(scid, false)
	if !st.Has("quote") {
		return ""
	}
	return hex.EncodeToString([]byte(st.Str("quote")))
}

func (d *deroChain) PromoTreasury(scid string) string {
	st := d.c.GetSC(scid, false)
	if !st.Has("treasury") {
		return ""
	}
	return rawAddress([]byte(st.Str("treasury")))
}

// PromoClaimable is promo.bas ClaimProceeds' sum at the node's current height: pr, and bd_<i> of every slot placed in
// an earlier epoch and not yet swept. A claim executes at that height or later, when it can only be more.
func (d *deroChain) PromoClaimable(scid string) uint64 {
	st := d.c.GetSC(scid, false)
	e := st.U0("epoch")
	h := d.c.Height()
	if e == 0 || h < 0 {
		return 0
	}
	cur := uint64(h) / e
	a := st.U0("pr")
	for i := uint64(0); i < 2*st.U0("slots"); i++ {
		k := strconv.FormatUint(i, 10)
		if !st.Has("ep_"+k) || st.U("ep_"+k) >= cur || st.U0("cl_"+k) != 0 {
			continue
		}
		a += st.U("bd_" + k)
	}
	return a
}

func (d *deroChain) SendClaimProceeds(scid string) (string, error) {
	txid, _ := d.c.SendWithFee(*wallet, d.me, scid, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimProceeds")}, promoClaimFee)
	if len(txid) < 8 {
		return "", fmt.Errorf("the wallet returned no txid")
	}
	return txid, nil
}

// rawAddress spells a raw 33-byte key the way ADDRESS_STRING does: bech32, prefix "dero", version byte 1, no arguments.
func rawAddress(raw []byte) string {
	if len(raw) != 33 {
		return ""
	}
	const cs = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	var data []int
	acc, bits := 0, 0
	for _, b := range append([]byte{1}, raw...) {
		acc = acc<<8 | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			data = append(data, (acc>>bits)&31)
		}
	}
	if bits > 0 {
		data = append(data, (acc<<(5-bits))&31)
	}
	gen := []int{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	polymod := func(v []int) int {
		chk := 1
		for _, x := range v {
			top := chk >> 25
			chk = (chk&0x1ffffff)<<5 ^ x
			for i := 0; i < 5; i++ {
				if (top>>i)&1 == 1 {
					chk ^= gen[i]
				}
			}
		}
		return chk
	}
	hrp := "dero"
	var v []int
	for _, c := range hrp {
		v = append(v, int(c)>>5)
	}
	v = append(v, 0)
	for _, c := range hrp {
		v = append(v, int(c)&31)
	}
	v = append(append(v, data...), 0, 0, 0, 0, 0, 0)
	pm := polymod(v) ^ 1
	out := hrp + "1"
	for _, d := range data {
		out += string(cs[d])
	}
	for i := 0; i < 6; i++ {
		out += string(cs[(pm>>(5*(5-i)))&31])
	}
	return out
}
