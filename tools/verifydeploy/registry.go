package main

import (
	"fmt"

	"veil/tools/verifydeploy/srchash"
)

// The factory registry (contracts/factory.bas): counters pc, lc and nc, and one slot per registration —
// pool_<i> (i < pc, RegisterPool line 72), launch_<i> (i < lc, RegisterLaunch line 80), and nft_<i> or nfa_<i>
// (i < nc, RegisterNFT line 88 / RegisterNFA line 99: the two kinds share the counter and a slot holds exactly one
// spelling). Each slot stores HEX(scid), a 64-character string, which DERO.GetSC hex-encodes once more.
//
// A registration is a paid hint, not a certificate: anyone may register any SCID. The app lists a registered
// contract only if its code hashes to a template in the deployment's config (app/feed.js verify(): current hash or
// any legacy hash of any template). This check applies the same rule to every slot and names each one that fails.

type slot struct {
	Key    string // pool_3
	Prefix string // the kind the slot's spelling claims
}

func registrySlots(f vars) (slots []slot, counts string, err error) {
	pc, ok1 := f.u("pc")
	lc, ok2 := f.u("lc")
	nc, ok3 := f.u("nc")
	if !ok1 || !ok2 || !ok3 {
		return nil, "", fmt.Errorf("the factory has no pc/lc/nc counters (pc %s, lc %s, nc %s)", f.num("pc"), f.num("lc"), f.num("nc"))
	}
	for i := uint64(0); i < pc; i++ {
		slots = append(slots, slot{fmt.Sprintf("pool_%d", i), "pool"})
	}
	for i := uint64(0); i < lc; i++ {
		slots = append(slots, slot{fmt.Sprintf("launch_%d", i), "launch"})
	}
	for i := uint64(0); i < nc; i++ {
		switch nft, nfa := fmt.Sprintf("nft_%d", i), fmt.Sprintf("nfa_%d", i); {
		case f.has(nft) && f.has(nfa):
			slots = append(slots, slot{nft, "nft"}, slot{nfa, "nfa"})
		case f.has(nfa):
			slots = append(slots, slot{nfa, "nfa"})
		default:
			slots = append(slots, slot{nft, "nft"})
		}
	}
	return slots, fmt.Sprintf("pc=%d lc=%d nc=%d", pc, lc, nc), nil
}

// slotSCID decodes a slot: the stored string is HEX(scid).
func slotSCID(f vars, key string) (string, error) {
	b, ok := f.raw(key)
	if !ok {
		return "", fmt.Errorf("slot %s is absent", key)
	}
	if s := string(b); is64hex(s) {
		return s, nil
	}
	if len(b) == 32 {
		return hexOf(b), nil
	}
	return "", fmt.Errorf("slot %s holds %q, which is not a SCID", key, string(b))
}

func (v *verifier) checkRegistry() {
	if v.rec.Factory == "" {
		return
	}
	fc, err := v.n.getSC(v.rec.Factory)
	if err != nil {
		return // reported by the identity check
	}
	slots, counts, err := registrySlots(fc.Vars)
	if err != nil {
		v.r.add("templates", "registry", v.rec.Factory, sFail, "%v", err)
		return
	}
	set := templateSet(v.rec)
	v.r.add("templates", "registry", v.rec.Factory, sInfo, "%s: %d registered contracts, checked against %d accepted template hashes (current and legacy)", counts, len(slots), len(set))
	for _, s := range slots {
		scid, err := slotSCID(fc.Vars, s.Key)
		if err != nil {
			v.r.add("templates", s.Key, "", sFail, "%v", err)
			continue
		}
		mc, err := v.n.getSC(scid)
		if err != nil {
			v.r.add("templates", s.Key, scid, sFail, "DERO.GetSC: %v", err)
			continue
		}
		h := srchash.Sum(mc.Code)
		t, ok := set[h]
		if !ok {
			msg := fmt.Sprintf("code sha256 %s matches no template the record accepts (current or legacy): the app will not list it", h)
			if k := v.whichSource(h); k != "" {
				msg += fmt.Sprintf("; it is reference.Canon(contracts/%s.bas), which the record does not accept as a template", k)
			}
			v.r.add("templates", s.Key, scid, sFail, "%s", msg)
			continue
		}
		st, msg := sPass, fmt.Sprintf("code sha256 %s = the record's current %s template", h, t.Kind)
		if !t.Current {
			msg = fmt.Sprintf("code sha256 %s = a legacy %s template (an earlier version the app still accepts, shown as legacy)", h, t.Kind)
			if hv, ok := v.hist.find(t.Kind+".bas", h); ok {
				msg += fmt.Sprintf(": contracts/%s.bas as of commit %s (%s)", t.Kind, short(hv.Commit), hv.Date)
			} else if v.hist.enabled {
				st = sWarn
				msg += "; no committed version of contracts/" + t.Kind + ".bas produces it"
			}
		}
		if t.Kind != s.Prefix {
			st = sWarn
			msg += fmt.Sprintf("; registered as %s but its code is the %s template", s.Prefix, t.Kind)
		}
		v.r.add("templates", s.Key, scid, st, "%s", msg)
		if v.opts.marketRoles {
			v.r.Roles = append(v.r.Roles, evalRoles(s.Key, t.Kind, scid, mc.Vars, v.allow)...)
		}
	}
}

// whichSource names the contracts/*.bas whose canonical hash is h, if any.
func (v *verifier) whichSource(h string) string {
	cs, err := srchash.Contracts(v.contracts)
	if err != nil {
		return ""
	}
	for _, c := range cs {
		if c.CanonSha256 == h {
			return c.File[:len(c.File)-len(".bas")]
		}
	}
	return ""
}
