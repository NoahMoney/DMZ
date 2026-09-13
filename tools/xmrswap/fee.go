package xmrswap

import (
	"errors"
	"fmt"
	"math/bits"
)

// XmrFeeBps is DMZ's swap fee, in basis points of the XMR the taker locks: 0.5% of every swap that ends in a Claim,
// taken in XMR and never in DERO (the operator's ruling of 2026-09-13). The maker pays it when he sweeps the XMR from
// the joint address after Alice's Claim: his sweep is ONE transaction from the joint wallet with two outputs, the fee
// to the treasury and the rest to himself, the network fee out of his own output (Swap.sweep). Nobody pays it on a
// Refund, a Punish or a Reclaim: the maker sweeps only after a Claim, and Alice's own recovery sweep pays no fee.
//
// Nothing but this software enforces it. htlc.bas knows nothing of it, the Monero side has no contract at all, and a
// modified maker client can leave it out (docs/XMR_SWAP.md §2.2).
const XmrFeeBps = 50

// SwapFee is the treasury's share of a swap whose taker locked amountXmr piconero: floor(amountXmr × XmrFeeBps / 10000),
// in 128 bits so that no amount overflows. It is 0 below 200 piconero, and the sweep then makes no fee output.
func SwapFee(amountXmr uint64) uint64 {
	hi, lo := bits.Mul64(amountXmr, XmrFeeBps)
	q, _ := bits.Div64(hi, lo, 10000) // hi < XmrFeeBps < 10000, so the quotient fits and Div64 cannot panic
	return q
}

// CheckTreasury decides whether a swap may start with this treasury, deploy/networks.json's xmrTreasury for the swap's
// DERO network. A treasury that is given must be a standard address or a subaddress on the swap's Monero network (a
// regtest chain uses mainnet's prefixes, so a regtest treasury is written as a mainnet address, and nothing in an
// address tells the two apart); an integrated address is refused, as everywhere in the companion. On mainnet an empty
// one is refused too: the fee would have nowhere to go, and a build with no mainnet treasury is not a release. Off
// mainnet an empty one means no fee is taken, which the command line says out loud.
func CheckTreasury(addr string, net Network, mainnet bool) error {
	if addr == "" {
		if mainnet {
			return errors.New("deploy/networks.json records no xmrTreasury for mainnet: refusing to start a swap, because the 0.5% swap fee the maker's sweep pays would have nowhere to go")
		}
		return nil
	}
	pa, err := ParseAddress(addr)
	if err != nil {
		return fmt.Errorf("deploy/networks.json's xmrTreasury %q is not a Monero standard address or subaddress (%v): refusing to start a swap", addr, err)
	}
	if pa.Net != net {
		return fmt.Errorf("deploy/networks.json's xmrTreasury %s is a %s address, and this swap's Monero network is %s: refusing to start a swap", addr, pa.Net, net)
	}
	return nil
}

// moneroMainnet is whether this swap's XMR is real: DERO mainnet, or Monero mainnet anywhere but against the DERO
// simulator (where "mainnet" is a regtest chain's address form, checkNetworks in the command line). The second case is
// refused elsewhere; it is counted here so that the treasury check fails closed whatever let it through.
func (c Config) moneroMainnet() bool {
	return c.DeroNet == "mainnet" || (c.Net == Mainnet && c.DeroNet != "simulator")
}

// swapFee is what this party's sweep pays the treasury: the maker's, after a Claim, and nobody else's. Alice sweeps only
// after a Refund or a Reclaim, when no swap happened and no fee is owed; a test network with no treasury takes none.
func (s *Swap) swapFee() uint64 {
	if s.st.Role != "bob" || s.cfg.XmrTreasury == "" {
		return 0
	}
	return SwapFee(s.st.Terms.AmountXmr)
}
