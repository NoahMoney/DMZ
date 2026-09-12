package zecswap

import (
	"errors"
	"fmt"
)

// The policy of docs/ZEC_SWAP.md §5, as numbers a machine checks rather than sentences a person must remember.
// Heights are what both contracts read; wall-clock time enters only through the two block times.
const (
	DeroBlockSeconds = 18 // BLOCK_TIME
	ZecBlockSeconds  = 75 // Zcash's target since Blossom
	// DeroStableDepth is DERO's STABLE_LIMIT: a block this deep is never replaced.
	DeroStableDepth = 8
	// DeroTxValidity is TX_VALIDITY_HEIGHT (vendor/derohe/blockchain/miner_block.go:43). A block takes a transaction
	// only while the block's height minus the transaction's height — the wallet's tip when it built it — is below it
	// (blockchain.go:748), so a DERO call built at height h executes by h + 10 or never. swap.bas and board.bas read
	// BLOCK_HEIGHT() as the call executes, so every height the companion sends them is judged up to 10 blocks after
	// the height it was decided at. The companion counts 11, one block conservative, as tools/xmrswap does.
	DeroTxValidity = 11
	// MaxEscrowWidth is swap.bas Lock's ceiling: it refuses an expiry more than this far above the height it executes
	// at (line 24).
	MaxEscrowWidth = 1_000_000
)

// Policy is one companion's timing and size rules. DefaultPolicy is §5's and is what the command line uses on
// mainnet and testnet; RegtestPolicy exists for a regtest Zcash node paired with the DERO simulator, and the command
// line reaches it only with -network regtest against a node whose genesis block is regtest's. A test may shrink the
// numbers to run a swap in a few hundred simulated blocks as long as Validate still holds.
type Policy struct {
	// ConfZec is C_zec: the maker claims only a funding this many blocks deep.
	ConfZec uint32
	// CLTVWindow and CLTVTolerance: cltv sits this far above the Zcash tip when the taker accepts, and both
	// parties refuse a cltv outside window ± tolerance when they act on it (the maker before he locks, the taker
	// before she funds).
	CLTVWindow    uint32
	CLTVTolerance uint32
	// ClaimCutoff is R: the maker sends his claim only while tip + 1 + R < cltv. After cltv both branches of the
	// script are live, and a claim still in a mempool there is a race he can lose with the secret already public
	// (docs/ATOMIC_SWAP_STUDY.md §2.2, "the claim near cltv is a race"; the study assumes R ≥ 20).
	ClaimCutoff uint32
	// TDeroWindow is T_dero − the height the maker builds his Lock at, unless the escrow's wmin needs more
	// (TDeroWidth).
	TDeroWindow uint64
	// TakerMinDeroLeft: the taker funds only while T_dero − now is at least this.
	TakerMinDeroLeft uint64
	// PLock is the maker's deadline, in DERO blocks after the accept, to lock; after it the taker walks.
	PLock uint64
	// FundingExpiry is how many Zcash blocks the taker's funding transaction stays valid for (its nExpiryHeight
	// is tip + this). A funding that never expired could be mined hours after the swap was abandoned; one that
	// expires is either mined in time or provably never mined, and the companion can tell which.
	FundingExpiry uint32
	// RefundBump is how many Zcash blocks past cltv each fee level of the pre-signed refund waits for before the
	// companion tries the next.
	RefundBump uint32
	// MinAmount is the smallest swap in zatoshi: the claim or refund fee (ZIP 317, 10,000 zatoshi for these
	// spends) is then at most a tenth of it (§9 Z10).
	MinAmount uint64
	// OfferLife is how long, in DERO blocks, a maker's offer stands on the board.
	OfferLife uint64
	// SettleBlocks is how long a companion waits, after a DERO call whose result it did not see, before it looks
	// for that call's effect and decides whether to send it again: a transaction in the mempool is invisible to
	// the contract's state.
	SettleBlocks uint64
	// MinEscrowWMin is the smallest swap.bas wmin the taker accepts; 0 means TakerMinDeroLeft.
	MinEscrowWMin uint64
	// DeroSeconds and ZecSeconds are the block times the wall-clock relations are judged in; 0 means the networks'
	// own (18 s and 75 s). On regtest Zcash blocks are mined on demand, so there the operator's pace is the answer.
	DeroSeconds uint64
	ZecSeconds  uint64
}

// DefaultPolicy is docs/ZEC_SWAP.md §5.
func DefaultPolicy() Policy {
	return Policy{
		ConfZec:          10,
		CLTVWindow:       CLTVWindow,
		CLTVTolerance:    CLTVTolerance,
		ClaimCutoff:      20,
		TDeroWindow:      1600,
		TakerMinDeroLeft: 1200,
		PLock:            200,
		FundingExpiry:    40,
		RefundBump:       6,
		MinAmount:        100_000,
		OfferLife:        400,
		SettleBlocks:     3,
	}
}

// RegtestPolicy is for a regtest zebrad beside the DERO simulator (about 14 s a block, measured), with the operator
// mining one Zcash block for each DERO block. The windows are §5's shape at a scale a person can watch: the swap is
// minutes, the refund path about half an hour, and each still holds a DERO call's execution delay (DeroTxValidity:
// hence T_dero 120, the taker's minimum 100 and P_lock 30). It is never used on mainnet or testnet: the command line
// selects it only when -network is regtest, and CheckNetworks accepts regtest only from a node whose genesis block is
// regtest's.
func RegtestPolicy() Policy {
	return Policy{
		ConfZec:          2,
		CLTVWindow:       50,
		CLTVTolerance:    20,
		ClaimCutoff:      4,
		TDeroWindow:      120,
		TakerMinDeroLeft: 100,
		PLock:            30,
		FundingExpiry:    12,
		RefundBump:       3,
		MinAmount:        100_000,
		OfferLife:        80,
		SettleBlocks:     2,
		MinEscrowWMin:    20,
		DeroSeconds:      14,
		ZecSeconds:       14,
	}
}

func (p Policy) dsec() uint64 {
	if p.DeroSeconds == 0 {
		return DeroBlockSeconds
	}
	return p.DeroSeconds
}

func (p Policy) zsec() uint64 {
	if p.ZecSeconds == 0 {
		return ZecBlockSeconds
	}
	return p.ZecSeconds
}

func (p Policy) minWMin() uint64 {
	if p.MinEscrowWMin == 0 {
		return p.TakerMinDeroLeft
	}
	return p.MinEscrowWMin
}

// TDeroWidth is T_dero − the height the maker builds his Lock at: this policy's window, or the escrow's wmin plus
// DeroTxValidity where that is wider. swap.bas refuses a Lock whose expiry is below height + wmin at the height it
// EXECUTES (line 24), up to DeroTxValidity blocks after the height it was built at. The companion's T_dero used to be
// the build height + TDeroWindow + SettleBlocks, 1,603 blocks against the wmin of 1,600 that
// docs/MAINNET_DEPLOYMENT.md installs, and was refused, its fee lost, by any Lock mined 4 or more blocks late. A width
// swap.bas's ceiling would refuse even for a Lock mined in the next block is refused here.
func (p Policy) TDeroWidth(wmin uint64) (uint64, error) {
	w := p.TDeroWindow
	if wmin+DeroTxValidity > w {
		w = wmin + DeroTxValidity
	}
	if w > MaxEscrowWidth {
		return 0, fmt.Errorf("zecswap: T_dero %d blocks above the Lock's build height is past swap.bas's ceiling of %d (wmin %d)", w, MaxEscrowWidth, wmin)
	}
	return w, nil
}

// Validate refuses a policy whose numbers contradict each other. Each rule is the reason a number exists.
func (p Policy) Validate() error {
	switch {
	case p.ConfZec == 0 || p.CLTVWindow == 0 || p.ClaimCutoff == 0 || p.TDeroWindow == 0 || p.TakerMinDeroLeft == 0 ||
		p.PLock == 0 || p.FundingExpiry == 0 || p.RefundBump == 0 || p.MinAmount == 0 || p.OfferLife == 0 || p.SettleBlocks == 0:
		return errors.New("zecswap: every policy number must be above zero")
	case p.CLTVTolerance >= p.CLTVWindow:
		return errors.New("zecswap: the cltv tolerance must be below the window, or a cltv at the tip passes")
	case uint64(p.ConfZec)+uint64(p.ClaimCutoff)+1 >= uint64(p.CLTVWindow-p.CLTVTolerance):
		// at the tightest cltv the taker still funds, the maker must be able to wait ConfZec blocks and still be
		// before his cutoff
		return fmt.Errorf("zecswap: C_zec %d + cutoff %d leaves the maker no block to claim in at cltv − tip = %d", p.ConfZec, p.ClaimCutoff, p.CLTVWindow-p.CLTVTolerance)
	case p.FundingExpiry >= p.CLTVWindow-p.CLTVTolerance:
		// a funding still mineable at cltv would give the maker a claim window nobody can see coming
		return errors.New("zecswap: the funding must expire before the earliest cltv the policy allows")
	case p.TDeroWindow > MaxEscrowWidth:
		return fmt.Errorf("zecswap: a T_dero window of %d blocks is past swap.bas's ceiling of %d", p.TDeroWindow, MaxEscrowWidth)
	case p.TakerMinDeroLeft+DeroTxValidity+DeroStableDepth > p.TDeroWindow:
		// T_dero is counted from the height the Lock is built at; it may execute DeroTxValidity blocks later, and the
		// taker funds only once it is DeroStableDepth deep. A lock that did both must still leave her minimum, or the
		// maker locks for a taker who can never fund.
		return fmt.Errorf("zecswap: the taker's minimum T_dero − now %d, plus a Lock's execution delay %d and DERO's stable depth %d, is wider than the maker's T_dero window %d: a lock that executes late is never fundable", p.TakerMinDeroLeft, DeroTxValidity, DeroStableDepth, p.TDeroWindow)
	case p.minWMin() > p.TDeroWindow:
		return errors.New("zecswap: the taker's minimum wmin is wider than the maker's T_dero window: no escrow suits both")
	case p.PLock <= DeroTxValidity+p.SettleBlocks:
		// the maker sends his Lock only while it would execute, and the taker could read it, before the deadline
		return fmt.Errorf("zecswap: P_lock %d leaves no height at which a Lock executes (%d blocks) and settles (%d) before the deadline", p.PLock, DeroTxValidity, p.SettleBlocks)
	case p.OfferLife <= 2*DeroTxValidity:
		// the offer must outlive its own Post's execution and then an Accept's
		return fmt.Errorf("zecswap: an offer life of %d blocks does not outlast a Post's and an Accept's execution (%d each)", p.OfferLife, DeroTxValidity)
	case p.TakerMinDeroLeft*p.dsec() <= uint64(p.CLTVWindow+p.CLTVTolerance)*p.zsec()+(2*DeroStableDepth+DeroTxValidity)*p.dsec():
		// the bad exit of §3: a maker who claims at the last Zcash block before cltv must still leave the taker
		// time on DERO, beyond two stable depths and her Claim's execution delay, to send and settle her Claim
		return errors.New("zecswap: T_dero − now at funding does not outlast cltv by two DERO stable depths and a Claim's execution delay")
	}
	return nil
}

// CheckCLTV is CheckCLTV with this policy's window.
func (p Policy) CheckCLTV(cltv, zcashTip uint32) error {
	if err := CheckCLTVHeight(cltv); err != nil {
		return err
	}
	if cltv <= zcashTip {
		return fmt.Errorf("zecswap: cltv %d is at or below the Zcash tip %d, so the refund branch is spendable already and the timelock binds nobody", cltv, zcashTip)
	}
	ahead := int64(cltv) - int64(zcashTip)
	if ahead < int64(p.CLTVWindow-p.CLTVTolerance) || ahead > int64(p.CLTVWindow+p.CLTVTolerance) {
		return fmt.Errorf("zecswap: cltv %d is %d blocks above the Zcash tip %d; policy is %d ± %d (docs/ZEC_SWAP.md §5)", cltv, ahead, zcashTip, p.CLTVWindow, p.CLTVTolerance)
	}
	return nil
}

// ClaimOpen says whether the maker may still send (or re-send) his claim at this Zcash tip.
func (p Policy) ClaimOpen(zcashTip, cltv uint32) bool {
	return uint64(zcashTip)+1+uint64(p.ClaimCutoff) < uint64(cltv)
}

// CheckTakerTimes is the relation the taker needs between the two deadlines before she funds: T_dero at least
// TakerMinDeroLeft ahead, and T_dero's wall-clock time after cltv's by more than two DERO stable depths and a Claim's
// execution delay — so that if the maker claims at the last moment he is allowed, she still has time to claim the DERO.
func (p Policy) CheckTakerTimes(deroLeft uint64, zecLeft uint32) error {
	if deroLeft < p.TakerMinDeroLeft {
		return fmt.Errorf("T_dero − now = %d DERO blocks is below the minimum %d (docs/ZEC_SWAP.md §5)", deroLeft, p.TakerMinDeroLeft)
	}
	if deroLeft*p.dsec() <= uint64(zecLeft)*p.zsec()+(2*DeroStableDepth+DeroTxValidity)*p.dsec() {
		return fmt.Errorf("T_dero is %d s away and cltv %d s: a claim at cltv would leave no time to claim the DERO", deroLeft*p.dsec(), uint64(zecLeft)*p.zsec())
	}
	return nil
}

// ZecBlocksFor is the number of Zcash blocks that span at least as long as the given number of DERO blocks.
func (p Policy) ZecBlocksFor(deroBlocks uint64) uint64 {
	return (deroBlocks*p.dsec() + p.zsec() - 1) / p.zsec()
}
