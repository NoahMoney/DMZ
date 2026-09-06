package reference

// Pool is the reference state machine for contracts/pool.bas. Methods mutate the receiver only on success
// and return the transfers the contract would emit to the signer, in emission order.

const (
	MinLiq          = 1000   // LP units locked forever at the first deposit
	FirstDepositMin = 100000 // 1 DERO
)

// Send is an outgoing transfer to the signer. Kind is "dero", "asset" or "lp".
type Send struct {
	Kind   string
	Amount uint64
}

// In is what the signer burned into the call.
type In struct {
	D uint64 // DERO
	A uint64 // pool asset
	L uint64 // LP (pool SCID)
}

// Point is a persisted price point p_<n>.
type Point struct{ H, T, RD, RA uint64 }

// Trade is a published trade t_<n>.
type Trade struct {
	Side    uint64
	Addr    string
	In, Out uint64
	H, T    uint64
}

type Pool struct {
	Fee, PFee          uint64
	RD, RA, LP, FD, FA uint64
	FC                 uint64 // cumulative fees claimed by the treasury (monotone)
	TC, PC, LH, LT     uint64
	Points             []Point
	Trades             []Trade
}

func NewPool(fee, pfee uint64) *Pool { return &Pool{Fee: fee, PFee: pfee} }

func (p *Pool) tick(h, t, rd, ra uint64) {
	if p.LH == h {
		return
	}
	if p.LH != 0 {
		p.Points = append(p.Points, Point{p.LH, p.LT, rd, ra})
		p.PC++
	}
	p.LH = h
	p.LT = t
}

func (p *Pool) post(side, in, out, publish uint64, addr string, h, t uint64) {
	if publish == 0 {
		return
	}
	p.Trades = append(p.Trades, Trade{side, addr, in, out, h, t})
	p.TC++
}

// AddLiquidity mirrors pool.bas AddLiquidity.
func (p *Pool) AddLiquidity(in In, minLP, h, t uint64) ([]Send, bool) {
	d, a := in.D, in.A
	if d == 0 || a == 0 || in.L != 0 {
		return nil, false
	}
	// guard the contract's real balances (reserve + accrued fees) against wrapping with the FULL amounts received
	if p.RD+p.FD+d < p.RD+p.FD || p.RA+p.FA+a < p.RA+p.FA {
		return nil, false
	}
	var sends []Send
	if p.LP == 0 {
		if d < FirstDepositMin {
			return nil, false
		}
		l := d - MinLiq
		if l < minLP {
			return nil, false
		}
		p.tick(h, t, p.RD, p.RA)
		p.RD, p.RA, p.LP = d, a, d
		return []Send{{"lp", l}}, true
	}
	ld, ok := MulDiv(d, p.LP, p.RD)
	if !ok {
		return nil, false
	}
	la, ok := MulDiv(a, p.LP, p.RA)
	if !ok {
		return nil, false
	}
	var l uint64
	if ld <= la {
		l = ld
		need, ok := MulDivUp(l, p.RA, p.LP)
		if !ok || need > a {
			return nil, false
		}
		if a != need {
			sends = append(sends, Send{"asset", a - need})
		}
		a = need
	} else {
		l = la
		need, ok := MulDivUp(l, p.RD, p.LP)
		if !ok || need > d {
			return nil, false
		}
		if d != need {
			sends = append(sends, Send{"dero", d - need})
		}
		d = need
	}
	if l == 0 || l < minLP {
		return nil, false
	}
	if p.LP+l < p.LP {
		return nil, false
	}
	p.tick(h, t, p.RD, p.RA)
	p.RD += d
	p.RA += a
	p.LP += l
	sends = append(sends, Send{"lp", l})
	return sends, true
}

// RemoveLiquidity mirrors pool.bas RemoveLiquidity.
func (p *Pool) RemoveLiquidity(in In, minD, minA, h, t uint64) ([]Send, bool) {
	l := in.L
	if l == 0 || in.D != 0 || in.A != 0 {
		return nil, false
	}
	if l > p.LP || p.LP-l < MinLiq {
		return nil, false
	}
	dout, ok := MulDiv(l, p.RD, p.LP)
	if !ok {
		return nil, false
	}
	aout, ok := MulDiv(l, p.RA, p.LP)
	if !ok {
		return nil, false
	}
	if dout < minD || aout < minA {
		return nil, false
	}
	p.tick(h, t, p.RD, p.RA)
	p.RD -= dout
	p.RA -= aout
	p.LP -= l
	return []Send{{"dero", dout}, {"asset", aout}}, true
}

// SwapDeroForAsset mirrors pool.bas SwapDeroForAsset.
func (p *Pool) SwapDeroForAsset(in In, minOut, publish uint64, addr string, h, t uint64) ([]Send, bool) {
	d := in.D
	if d == 0 || in.A != 0 || in.L != 0 {
		return nil, false
	}
	if p.RD == 0 || p.RA == 0 {
		return nil, false
	}
	fee := BpsUp(d, p.Fee)
	pf := Bps(fee, p.PFee)
	deff := d - fee
	if p.RD+p.FD+d < p.RD+p.FD {
		return nil, false
	}
	out, ok := MulDiv(deff, p.RA, p.RD+deff)
	if !ok || out == 0 || out < minOut {
		return nil, false
	}
	p.tick(h, t, p.RD, p.RA)
	p.RD = p.RD + d - pf
	p.RA -= out
	p.FD += pf
	p.post(1, d, out, publish, addr, h, t)
	return []Send{{"asset", out}}, true
}

// SwapAssetForDero mirrors pool.bas SwapAssetForDero.
func (p *Pool) SwapAssetForDero(in In, minOut, publish uint64, addr string, h, t uint64) ([]Send, bool) {
	a := in.A
	if a == 0 || in.D != 0 || in.L != 0 {
		return nil, false
	}
	if p.RD == 0 || p.RA == 0 {
		return nil, false
	}
	fee := BpsUp(a, p.Fee)
	pf := Bps(fee, p.PFee)
	aeff := a - fee
	if p.RA+p.FA+a < p.RA+p.FA {
		return nil, false
	}
	out, ok := MulDiv(aeff, p.RD, p.RA+aeff)
	if !ok || out == 0 || out < minOut {
		return nil, false
	}
	p.tick(h, t, p.RD, p.RA)
	p.RA = p.RA + a - pf
	p.RD -= out
	p.FA += pf
	p.post(2, a, out, publish, addr, h, t)
	return []Send{{"dero", out}}, true
}

// ClaimFees mirrors pool.bas ClaimFees (caller must be the treasury; the test decides that).
func (p *Pool) ClaimFees() []Send {
	s := []Send{{"dero", p.FD}, {"asset", p.FA}}
	p.FC += p.FD
	p.FD, p.FA = 0, 0
	return s
}

// Quote returns the asset out for d DERO without mutating (frontend parity check).
func (p *Pool) Quote(d uint64) (out uint64, ok bool) {
	fee := BpsUp(d, p.Fee)
	deff := d - fee
	if p.RD+p.FD+d < p.RD+p.FD {
		return 0, false
	}
	return MulDiv(deff, p.RA, p.RD+deff)
}
