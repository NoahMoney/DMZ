package zecswap

import (
	"context"
	"errors"
	"fmt"
)

// BoardCodeSHA256 and EscrowCodeSHA256 are the SHA-256 of contracts/board.bas v3 and contracts/swap.bas v2 as they
// are installed: the bytes reference.Canon makes of each source, which are exactly the code DERO.GetSC returns for
// the contract. Both companions check both before the first thing either party puts on the board or into the
// escrow, and refuse a -board or -escrow whose code hashes to anything else (GAP-011): a look-alike escrow whose
// storage passes every read — the same keys, the same values — but which carries an owner exit would let the maker
// claim the ZEC with the secret and walk off with the DERO too, and the SCIDs reach the companion from a flag the
// operator typed or a page filled in. pin_test.go recomputes both from the tree, so an edit to either contract that
// leaves these lines behind fails the tests.
//
// The pin is only as good as the binary that carries it: a reproducible, signed build (BL-14) is what turns it from
// a claim into a check, and until one exists whoever built this binary is trusted to have pinned the right code.
const (
	BoardCodeSHA256  = "162f8d46ad400b1fef981d029ba99f61c9646d6453c3bf9f0cc4bd9e63bc5ab1"
	EscrowCodeSHA256 = "b9830de831dbaf34f61ea15b484c8f25105e41dc790aa06412db421a990fd6e3"
)

// ErrCodeNotPinned is a board or escrow whose code is not the one this build was made for.
var ErrCodeNotPinned = errors.New("zecswap: the contract's code is not the one this companion was built for")

// ErrEscrowWidth is an escrow whose install-time wmin is outside what this party's policy accepts.
var ErrEscrowWidth = errors.New("zecswap: the escrow's wmin is outside this party's policy")

// CheckEscrowWidth reads swap.bas v2's wmin from every DERO node and requires lo ≤ wmin ≤ hi on each. The same code
// installed with another wmin has the same code hash, so the pin alone does not say how wide a lock the escrow
// allows: the taker refuses one narrower than her policy (a lock too short for the Zcash leg), the maker one wider
// than his T_dero window (his capital held longer than he agreed to).
func CheckEscrowWidth(ctx context.Context, srcs []DeroSource, lo, hi uint64) error {
	for _, src := range srcs {
		w, err := src.Node.EscrowWMin(ctx)
		if err != nil {
			return fmt.Errorf("zecswap: %s: the escrow's wmin: %w", src.Name, err)
		}
		if w < lo || w > hi {
			return fmt.Errorf("%w: %s says wmin is %d, and this party accepts %d to %d", ErrEscrowWidth, src.Name, w, lo, hi)
		}
	}
	return nil
}

// CheckPins reads the board's and the escrow's code from every DERO node and refuses any that is not pinned.
func CheckPins(ctx context.Context, srcs []DeroSource, board, escrow string) error {
	for _, src := range srcs {
		for _, c := range []struct{ what, scid, want string }{{"board", board, BoardCodeSHA256}, {"escrow", escrow, EscrowCodeSHA256}} {
			got, err := src.Node.CodeSHA256(ctx, c.scid)
			if err != nil {
				return fmt.Errorf("zecswap: %s: the %s's code: %w", src.Name, c.what, err)
			}
			if got != c.want {
				return fmt.Errorf("%w: %s says the %s %s has code hashing to %s, and this build was made for %s", ErrCodeNotPinned, src.Name, c.what, c.scid, got, c.want)
			}
		}
	}
	return nil
}
