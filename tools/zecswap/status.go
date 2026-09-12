package zecswap

import (
	"context"
	"fmt"
	"strings"
)

// CheckNetworks is the command line's network rule, and it fails closed. network is the -network flag, which has
// no default: mainnet, testnet or regtest. The Zcash node must be on that network (getblockchaininfo's chain "main",
// "test" or "regtest") and the DERO daemon on the matching kind (DERO.GetInfo's testnet flag false for mainnet, true
// for testnet and the simulator). A mainnet leg is never paired with a test one in either direction (the GAP-007
// class).
func CheckNetworks(network, zchain string, deroTestnet bool) error {
	want := map[string]string{"mainnet": "main", "testnet": "test", "regtest": "regtest"}[network]
	switch {
	case network == "":
		return fmt.Errorf("-network is required (mainnet, testnet or regtest); it has no default")
	case want == "":
		return fmt.Errorf("-network %q is not mainnet, testnet or regtest", network)
	case zchain != want:
		return fmt.Errorf("-network %s, but the Zcash node is on %q", network, zchain)
	case network == "mainnet" && deroTestnet:
		return fmt.Errorf("-network mainnet, but the DERO daemon is not on mainnet: refusing to pair a mainnet leg with a test one")
	case network != "mainnet" && !deroTestnet:
		return fmt.Errorf("-network %s, but the DERO daemon is on mainnet: refusing to pair a test leg with a mainnet one", network)
	}
	return nil
}

// PolicyFor is the policy the command line runs a network under: §5's everywhere, except regtest, which has its own
// scale (RegtestPolicy) and which CheckNetworks admits only from a node whose genesis block is regtest's.
func PolicyFor(network string) Policy {
	if network == "regtest" {
		return RegtestPolicy()
	}
	return DefaultPolicy()
}

// ChainFor is the getblockchaininfo chain (as ChainInfo reports it) a -network value must find.
func ChainFor(network string) string {
	return map[string]string{"mainnet": "main", "testnet": "test", "regtest": "regtest"}[network]
}

// ResolveDeroLogin decides where the DERO wallet's login comes from (GAP-008). flag is -dero-login, env is
// VEIL_RPC_LOGIN. On mainnet the login is taken only from the environment — a flag is visible in the process list and
// the shell history — and a mainnet swap without one is refused. Elsewhere either works, and none means the wallet
// has no login (the simulator's).
func ResolveDeroLogin(flag, env string, mainnet bool) (user, pass string, err error) {
	if mainnet && flag != "" {
		return "", "", fmt.Errorf("on mainnet the DERO wallet's login is read from VEIL_RPC_LOGIN, never from the command line, where the process list and the shell history show it")
	}
	v := flag
	if v == "" {
		v = env
	}
	if v == "" {
		if mainnet {
			return "", "", fmt.Errorf("a mainnet swap needs the DERO wallet's login: start the wallet RPC with --rpc-login user:password and export VEIL_RPC_LOGIN with the same")
		}
		return "", "", nil
	}
	i := strings.IndexByte(v, ':')
	if i <= 0 || i == len(v)-1 {
		return "", "", fmt.Errorf("the DERO wallet's login must be user:password")
	}
	return v[:i], v[i+1:], nil
}

// Status is the `status` command's report on one swap: what the state file says, next to what every node says now,
// with a DIVERGENCE line wherever the two cannot both be true — and what happens if the companion stops here.
func Status(ctx context.Context, st *State, zsrc []ZcashSource, dsrc []DeroSource) []string {
	var out []string
	add := func(f string, a ...interface{}) { out = append(out, fmt.Sprintf(f, a...)) }
	diverge := func(f string, a ...interface{}) { out = append(out, "  DIVERGENCE: "+fmt.Sprintf(f, a...)) }
	info := Phases(st.Role)[st.Phase]
	add("%s: the %s, phase %q", st.Path(), st.Role, st.Phase)
	if st.Done {
		add("  ended: %s", st.Outcome)
	} else {
		add("  deadline: %s", info.Deadline)
		add("  if it stops now: %s", info.IfStopped)
	}
	if st.Note != "" {
		add("  note: %s", st.Note)
	}
	add("  board %s offer %d, escrow %s id %d (T_dero %d); Zcash %s, cltv %d, hash %s", st.Board, st.OfferID, st.Escrow, st.EscrowID, st.TDero, st.ZcashChain, st.CLTV, st.Hash)

	for _, d := range dsrc {
		h, err := d.Node.Height(ctx)
		if err != nil {
			add("  DERO %s: cannot be read: %v", d.Name, err)
			continue
		}
		line := fmt.Sprintf("  DERO %s (a trusted party): height %d", d.Name, h)
		if st.TDero > 0 {
			line += ", " + countdown(int64(st.TDero), int64(h), "T_dero")
		}
		add("%s", line)
		if st.HaveOffer {
			o, err := d.Node.ReadOffer(ctx, st.OfferID)
			if err != nil {
				add("    offer %d: %v", st.OfferID, err)
			} else {
				add("    offer %d: state %d, expiry %d", o.ID, o.State, o.Expiry)
				if st.Hash != "" && o.Hash != st.Hash {
					diverge("%s: board row %d carries hash %s, this swap's is %s", d.Name, o.ID, o.Hash, st.Hash)
				}
				if st.Phase == "cancelled" && o.State != OfferCancelled {
					diverge("%s: the swap ended cancelled and the board row is in state %d", d.Name, o.State)
				}
			}
		}
		if st.HaveEscrow {
			e, err := d.Node.ReadEscrow(ctx, st.EscrowID)
			if err != nil {
				add("    escrow %d: %v", st.EscrowID, err)
				continue
			}
			add("    escrow %d: state %d, %d of %q, expiry %d", e.ID, e.State, e.Amount, e.Asset, e.Expiry)
			if e.Hash != st.Hash {
				diverge("%s: escrow %d carries hash %s, this swap's is %s", d.Name, e.ID, e.Hash, st.Hash)
			}
			if allowed := escrowStates(st); allowed != nil && !containsU(allowed, e.State) {
				diverge("%s: in phase %q the escrow can be in state %v, and it is in %d", d.Name, st.Phase, allowed, e.State)
			}
		}
	}

	var spk []byte
	if h, err := (&Swap{st: st}).htlc(); err == nil {
		spk = h.ScriptPubKey()
		add("  script address %s", h.Address(netOf(st.ZcashChain)))
	}
	for _, z := range zsrc {
		ci, err := z.Node.ChainInfo(ctx)
		if err != nil {
			add("  Zcash %s: cannot be read: %v", z.Name, err)
			continue
		}
		if st.ZcashChain != "" && ci.Chain != st.ZcashChain {
			diverge("Zcash %s is on %s and this swap lives on %s", z.Name, ci.Chain, st.ZcashChain)
			continue
		}
		line := fmt.Sprintf("  Zcash %s (a trusted party): %s height %d", z.Name, ci.Chain, ci.Height)
		if st.CLTV > 0 {
			line += ", " + countdown(int64(st.CLTV), int64(ci.Height), "cltv")
		}
		add("%s", line)
		tx := func(what, txid string) (RawTx, bool) {
			rt, known, err := TxKnown(ctx, z.Node, txid)
			switch {
			case err != nil:
				add("    %s %s: %v", what, txid, err)
			case !known:
				add("    %s %s: unknown to this node", what, txid)
			case rt.Mined:
				add("    %s %s: mined at %d, %d confirmations", what, txid, rt.Height, rt.Confirmations)
			default:
				add("    %s %s: in the mempool", what, txid)
			}
			return rt, known && err == nil
		}
		if st.FundingTxID != "" {
			_, known := tx("funding", st.FundingTxID)
			if !known && ci.Height+1 <= st.FundingExpiry {
				// still mineable: the companion re-sends it
			} else if !known && st.Role == RoleTaker && st.Phase != "funding_expired" && st.Phase != "funding" {
				diverge("%s does not know the funding %s, and the swap is in %q", z.Name, st.FundingTxID, st.Phase)
			}
		}
		if st.Funding != nil && spk != nil {
			out, err := z.Node.TxOut(ctx, st.Funding.TxID, st.Funding.Index)
			switch {
			case err != nil:
				add("    funding output %s:%d: %v", st.Funding.TxID, st.Funding.Index, err)
			case out == nil:
				add("    funding output %s:%d: spent (or not on this node's chain)", st.Funding.TxID, st.Funding.Index)
			default:
				add("    funding output %s:%d: unspent, %d confirmations", st.Funding.TxID, st.Funding.Index, out.Confirmations)
				if st.Done && (st.Phase == "swapped" || st.Phase == "refunded" || st.Phase == "claimed_refunded" || st.Phase == "lost") {
					diverge("%s: the swap ended %q and the funding output is still unspent", z.Name, st.Phase)
				}
			}
		}
		if st.ClaimTxID != "" {
			rt, known := tx("claim", st.ClaimTxID)
			if st.Done && st.Phase == "swapped" && st.Role == RoleMaker && !(known && rt.Mined) {
				diverge("%s: the swap ended swapped and the claim %s is not mined", z.Name, st.ClaimTxID)
			}
			if st.ClaimBlock != "" && !(known && rt.Mined && rt.BlockHash == st.ClaimBlock) {
				diverge("%s: the claim %s was seen mined at %d in block %s, and this node's chain does not carry it there: a reorganisation (docs/ZEC_SWAP.md §9 Z5)", z.Name, st.ClaimTxID, st.ClaimHeight, st.ClaimBlock)
			}
		}
		for i, r := range st.Refunds {
			tx(fmt.Sprintf("refund level %d", i+1), r.TxID)
		}
	}
	return out
}

// escrowStates is what the escrow may be in while this machine is in its phase; nil is "anything".
func escrowStates(st *State) []uint64 {
	if st.Role == RoleMaker {
		switch st.Phase {
		case "locked", "claiming":
			return []uint64{EscrowOpen}
		case "swapped":
			return []uint64{EscrowClaimed}
		case "refunded", "claimed_refunded":
			return []uint64{EscrowRefunded}
		case "lost":
			return []uint64{EscrowClaimed}
		}
		return nil
	}
	switch st.Phase {
	case "lock_seen", "funding", "funded", "refunding":
		// the maker may refund after T_dero; nobody but this wallet can claim
		return []uint64{EscrowOpen, EscrowRefunded}
	case "dero_claimed":
		return []uint64{EscrowOpen, EscrowClaimed}
	case "swapped":
		return []uint64{EscrowClaimed}
	case "lost":
		return []uint64{EscrowOpen, EscrowRefunded}
	}
	return nil
}

func containsU(xs []uint64, x uint64) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// countdown is how a height stands against a deadline, in words that are never a negative number: "12 blocks to
// cltv" before it, "at cltv" on it, "3 blocks past cltv" after it.
func countdown(deadline, now int64, what string) string {
	blocks := func(n int64) string {
		if n == 1 {
			return "1 block"
		}
		return fmt.Sprintf("%d blocks", n)
	}
	switch d := deadline - now; {
	case d > 0:
		return blocks(d) + " to " + what
	case d == 0:
		return "at " + what
	default:
		return blocks(-d) + " past " + what
	}
}

func netOf(chain string) Network {
	if strings.EqualFold(chain, "main") {
		return Mainnet
	}
	return Testnet
}
