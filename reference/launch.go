package reference

// Launch is the reference state machine for contracts/launch.bas.

const Prec = uint64(1) << 32 // staking accumulator fixed point

type Stake struct{ ST, SD, SE uint64 }

type Launch struct {
	Fee, PFee, Tax, TMode uint64
	Supply, Alloc, C, Lq  uint64
	G, VD0, VT0           uint64
	Phase, Sold, Real     uint64
	RD, RA, FD, FA, FC    uint64 // FC: cumulative fees claimed by the treasury (monotone)
	TAcc, RPool, Locked   uint64
	BBH                   uint64 // height of the last buyback (bbh); BBSet says whether one has happened
	BBSet                 bool
	STot, SAcc            uint64
	SEra                  uint64            // accumulator era: bumped when sacc would overflow
	SEA                   map[uint64]uint64 // final sacc of each closed era
	Stakes                map[string]*Stake
	GH                    uint64
	TC, PC, LH, LT        uint64
	Points                []Point
	Trades                []Trade
}

// NewLaunch mirrors InitializePrivate; ok=false when the contract would reject the parameters.
func NewLaunch(supply, grad, cbps, tax, tmode, fee, pfee uint64) (*Launch, bool) {
	if supply < 1000000 || supply >= 1<<63 || grad < 1000000 || grad > 1000000000000 {
		return nil, false
	}
	if cbps > 500 || tax > 1000 || tmode > 3 || (tmode == 0 && tax != 0) || (tmode != 0 && tax == 0) || fee > 100 || pfee > 10000 {
		return nil, false
	}
	l := &Launch{Fee: fee, PFee: pfee, Tax: tax, TMode: tmode, Supply: supply, G: grad, Stakes: map[string]*Stake{}, SEA: map[uint64]uint64{}}
	l.Alloc = Bps(supply, cbps)
	rest := supply - l.Alloc
	l.Lq = rest / 5
	l.C = rest - l.Lq
	l.VD0 = grad / 3
	vt0, ok := MulDiv(grad+l.VD0, l.C, grad)
	if !ok {
		return nil, false
	}
	l.VT0 = vt0
	l.RD, l.RA = l.VD0, vt0
	return l, true
}

func (l *Launch) tick(h, t, rd, ra uint64) {
	if l.LH == h {
		return
	}
	if l.LH != 0 {
		l.Points = append(l.Points, Point{l.LH, l.LT, rd, ra})
		l.PC++
	}
	l.LH = h
	l.LT = t
}

func (l *Launch) post(side, in, out, publish uint64, addr string, h, t uint64) {
	if publish == 0 {
		return
	}
	l.Trades = append(l.Trades, Trade{side, addr, in, out, h, t})
	l.TC++
}

// deroBalance is what the contract holds in DERO according to its own accounting.
func (l *Launch) DeroBalance() uint64 {
	if l.Phase == 0 {
		return l.Real + l.FD + l.TAcc + l.RPool
	}
	return l.RD + l.FD + l.TAcc + l.RPool
}

// Buy mirrors SwapDeroForAsset. in.A is stray own-asset value.
func (l *Launch) Buy(in In, minOut, publish uint64, addr string, h, t uint64) ([]Send, bool) {
	d := in.D
	if d == 0 || in.A != 0 {
		return nil, false
	}
	fee := BpsUp(d, l.Fee)
	tx := Bps(d, l.Tax)
	if fee+tx >= d {
		return nil, false
	}
	deff := d - fee - tx
	var sends []Send
	used := d
	if l.Phase == 0 {
		if l.Real+l.FD+l.TAcc+l.RPool+d < l.Real {
			return nil, false
		}
		rem := l.C - l.Sold
		if rem == 0 {
			return nil, false
		}
		out, ok := MulDiv(deff, l.RA, l.RD+deff)
		if !ok {
			return nil, false
		}
		if out >= rem {
			out = rem
			cost, ok := MulDivUp(rem, l.RD, l.RA-rem)
			if !ok || cost > deff {
				return nil, false
			}
			if cost != deff {
				// fee and tax apply to the DERO the curve actually consumes; the overshoot goes back untaxed.
				// used is the smallest gross amount whose net covers cost (+1 absorbs the two roundings).
				u, ok := MulDivUp(cost, 10000, 10000-l.Fee-l.Tax)
				if !ok {
					return nil, false
				}
				used = u + 1
				if used > d {
					used = d
				}
				fee = BpsUp(used, l.Fee)
				tx = Bps(used, l.Tax)
				deff = used - fee - tx
				if deff < cost {
					return nil, false
				}
				if used != d {
					sends = append(sends, Send{"dero", d - used})
				}
			}
		}
		if out == 0 || out < minOut {
			return nil, false
		}
		l.tick(h, t, l.RD, l.RA)
		l.Real += deff
		l.RD += deff
		l.RA -= out
		l.Sold += out
		l.FD += fee
		l.TAcc += tx
		sends = append(sends, Send{"asset", out})
		l.post(1, used, out, publish, addr, h, t)
		if out == rem {
			l.Phase = 1
			l.RD = l.Real
			l.RA = l.Lq
			l.GH = h
		}
		return sends, true
	}
	if l.RD+l.FD+l.TAcc+l.RPool+d < l.RD {
		return nil, false
	}
	pf := Bps(fee, l.PFee)
	out, ok := MulDiv(deff, l.RA, l.RD+deff)
	if !ok || out == 0 || out < minOut {
		return nil, false
	}
	l.tick(h, t, l.RD, l.RA)
	l.RD = l.RD + deff + fee - pf
	l.RA -= out
	l.FD += pf
	l.TAcc += tx
	l.post(1, d, out, publish, addr, h, t)
	return []Send{{"asset", out}}, true
}

// Sell mirrors SwapAssetForDero.
func (l *Launch) Sell(in In, minOut, publish uint64, addr string, h, t uint64) ([]Send, bool) {
	a := in.A
	if a == 0 || in.D != 0 {
		return nil, false
	}
	if l.Phase == 0 {
		if a > l.Sold {
			return nil, false
		}
		dout, ok := MulDiv(a, l.RD, l.RA+a)
		if !ok || dout > l.Real {
			return nil, false
		}
		fee := BpsUp(dout, l.Fee)
		tx := Bps(dout, l.Tax)
		if fee+tx >= dout {
			return nil, false
		}
		out := dout - fee - tx
		if out < minOut {
			return nil, false
		}
		l.tick(h, t, l.RD, l.RA)
		l.Real -= dout
		l.RD -= dout
		l.RA += a
		l.Sold -= a
		l.FD += fee
		l.TAcc += tx
		l.post(2, a, out, publish, addr, h, t)
		return []Send{{"dero", out}}, true
	}
	if l.RA+l.FA+a < l.RA+l.FA {
		return nil, false
	}
	fee := BpsUp(a, l.Fee)
	pf := Bps(fee, l.PFee)
	aeff := a - fee
	dout, ok := MulDiv(aeff, l.RD, l.RA+aeff)
	if !ok {
		return nil, false
	}
	tx := Bps(dout, l.Tax)
	out := dout - tx
	if out == 0 || out < minOut {
		return nil, false
	}
	l.tick(h, t, l.RD, l.RA)
	l.RA = l.RA + a - pf
	l.RD -= dout
	l.FA += pf
	l.TAcc += tx
	l.post(2, a, out, publish, addr, h, t)
	return []Send{{"dero", out}}, true
}

func (l *Launch) ClaimFees() []Send {
	s := []Send{{"dero", l.FD}, {"asset", l.FA}}
	l.FC += l.FD
	l.FD, l.FA = 0, 0
	return s
}

func (l *Launch) ClaimTax() ([]Send, bool) {
	if l.TMode != 1 {
		return nil, false
	}
	s := []Send{{"dero", l.TAcc}}
	l.TAcc = 0
	return s, true
}

// Buyback spends at most 1 % of the DERO reserve per call and runs at most once per 20 blocks (launch.bas), so a
// trader who buys, triggers it and sells cannot move the price by more than the round-trip fees cost.
func (l *Launch) Buyback(h, t uint64) bool {
	if l.TMode != 2 || l.Phase != 1 || l.TAcc == 0 {
		return false
	}
	d := l.TAcc
	if d > l.RD/100 {
		d = l.RD / 100
	}
	if d == 0 {
		return false
	}
	if l.BBSet && h < l.BBH+20 {
		return false
	}
	out, ok := MulDiv(d, l.RA, l.RD+d)
	if !ok || out == 0 {
		return false
	}
	l.tick(h, t, l.RD, l.RA)
	l.RD += d
	l.RA -= out
	l.Locked += out
	l.TAcc -= d
	l.BBH, l.BBSet = h, true
	return true
}

func (l *Launch) dist() {
	if l.TAcc == 0 || l.STot == 0 {
		return
	}
	tacc := l.TAcc
	if Mulhi(tacc, Prec) >= l.STot {
		tacc = l.STot * (Prec - 1) // the most one increment can carry; the rest waits for the next call
	}
	inc, _ := MulDiv(tacc, Prec, l.STot)
	acc := l.SAcc
	if acc+inc < acc { // the accumulator would wrap: close the era and start a new one at zero
		l.SEA[l.SEra] = acc
		l.SEra++
		acc = 0
	}
	paid, _ := MulDiv(inc, l.STot, Prec)
	if inc*l.STot-paid*Prec != 0 { // ceil, exactly as muldivup: the residual must never be credited twice
		paid++
	}
	l.SAcc = acc + inc
	l.TAcc -= paid
	l.RPool += paid
}

func (l *Launch) settle(addr string) (pend uint64) {
	s, ok := l.Stakes[addr]
	if !ok {
		return 0
	}
	sd := s.SD
	for e := s.SE; e < l.SEra; e++ { // every closed era since the stake last settled
		p, ok := MulDiv(s.ST, l.SEA[e]-sd, Prec)
		if !ok {
			panic("settle overflow")
		}
		pend += p
		sd = 0
	}
	p, ok := MulDiv(s.ST, l.SAcc-sd, Prec)
	if !ok {
		panic("settle overflow") // cannot happen: st*(acc-sd) < 2^96 and result < 2^64 by construction
	}
	pend += p
	s.SD = l.SAcc
	s.SE = l.SEra
	if pend > l.RPool { // never pay more than the pool holds (rounding dust)
		pend = l.RPool
	}
	l.RPool -= pend
	return pend
}

func (l *Launch) Distribute() bool {
	if l.TMode != 3 {
		return false
	}
	l.dist()
	return true
}

func (l *Launch) StakeOp(addr string, in In) ([]Send, bool) {
	t := in.A
	if l.TMode != 3 || t == 0 || in.D != 0 || l.STot+t < t {
		return nil, false
	}
	l.dist()
	var sends []Send
	if p := l.settle(addr); p > 0 {
		sends = append(sends, Send{"dero", p})
	}
	s, ok := l.Stakes[addr]
	if !ok {
		s = &Stake{}
		l.Stakes[addr] = s
	}
	s.ST += t
	s.SD = l.SAcc
	s.SE = l.SEra
	l.STot += t
	return sends, true
}

func (l *Launch) Unstake(addr string, amt uint64, in In) ([]Send, bool) {
	if l.TMode != 3 || in.D != 0 || in.A != 0 || amt == 0 {
		return nil, false
	}
	s, ok := l.Stakes[addr]
	if !ok || amt > s.ST {
		return nil, false
	}
	l.dist()
	var sends []Send
	if p := l.settle(addr); p > 0 {
		sends = append(sends, Send{"dero", p})
	}
	l.STot -= amt
	sends = append(sends, Send{"asset", amt})
	if amt == s.ST {
		delete(l.Stakes, addr)
	} else {
		s.ST -= amt
	}
	return sends, true
}

func (l *Launch) Claim(addr string) ([]Send, bool) {
	if l.TMode != 3 {
		return nil, false
	}
	l.dist()
	if p := l.settle(addr); p > 0 {
		return []Send{{"dero", p}}, true
	}
	return nil, true
}

// QuoteBuy returns tokens out for d DERO (either phase), for frontend parity.
func (l *Launch) QuoteBuy(d uint64) (uint64, bool) {
	fee := BpsUp(d, l.Fee)
	tx := Bps(d, l.Tax)
	if fee+tx >= d {
		return 0, false
	}
	deff := d - fee - tx
	out, ok := MulDiv(deff, l.RA, l.RD+deff)
	if !ok {
		return 0, false
	}
	if l.Phase == 0 && out > l.C-l.Sold {
		out = l.C - l.Sold
	}
	return out, true
}
