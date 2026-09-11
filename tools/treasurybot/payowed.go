package main

// NFA-3. nfa.bas no longer sends its protocol fee at settlement: it credits it to ow_<treasury> and pays it out
// only through PayOwed(payee), which anyone may call and which pays the named payee, never the caller
// (contracts/nfa.bas, header note 5). Nothing else ever moves that balance, so without this pass every NFA
// protocol fee stays in its contract for good. The bot pays out the treasury's own share and nothing else, and
// deposits none of it into the Founders Pass: the pass counts DERO trading fees from pools and launches, and the
// app says that fees from NFA sales reach no holder.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	rpc "veil/tools/simrpc"
)

// owedChain is what the NFA pass needs from the DERO side, narrowed so a test can fake it.
type owedChain interface {
	CodeHash(scid string) string                    // sha256 of the contract's code, hex, as the app computes it
	Owed(scid, payee string) uint64                 // ow_<payee>: credited and not yet paid (0 when absent)
	Paid(scid, payee string) uint64                 // op_<payee>: paid so far; only PayOwed grows it, only upward
	SendPayOwed(scid, payee string) (string, error) // PayOwed(payee), nothing attached
}

// nfaTemplates reads from config.js the nfa template hashes the app verifies against, current and legacy. Only a
// contract whose code hashes to one of them is the shipped nfa.bas. The factory registers any contract anyone
// names, and one that keeps an ow_ key for the treasury while refusing PayOwed would otherwise cost a fee on every
// sweep, for ever.
func nfaTemplates(src string) map[string]bool {
	out := map[string]bool{}
	m := regexp.MustCompile(`nfa:\s*\{[^}]*?sha256:\s*"([0-9a-f]{64})"(?:[^}]*?legacy:\s*\[([^\]]*)\])?`).FindStringSubmatch(src)
	if m == nil {
		return out
	}
	out[m[1]] = true
	for _, h := range regexp.MustCompile(`[0-9a-f]{64}`).FindAllString(m[2], -1) {
		out[h] = true
	}
	return out
}

// registeredNFAs lists the factory's nfa_<i> entries. nft_ and nfa_ share the counter nc (factory.bas
// RegisterNFT/RegisterNFA), so a slot holds one spelling or the other and only the nfa_ ones are taken.
func registeredNFAs(reg rpc.SCState) []string {
	var out []string
	for i, n := uint64(0), reg.U0("nc"); i < n; i++ {
		k := "nfa_" + strconv.FormatUint(i, 10)
		if !reg.Has(k) {
			continue
		}
		if s := reg.Str(k); len(s) == 64 {
			out = append(out, s)
		}
	}
	return out
}

// payOwed calls PayOwed for payee on every listed NFA that runs the shipped template and owes payee at least min
// units of its own quote, and returns the units paid per NFA. A payment is proven by the NFA's op_<payee> counter,
// never by a txid. A call that was included but did not grow op_ is reported and left for the next sweep; it is
// not re-sent in this one. A dry run sends nothing and reports nothing as paid.
func payOwed(ch owedChain, nfas []string, payee string, templates map[string]bool, min uint64) (map[string]uint64, []error) {
	paid := map[string]uint64{}
	var errs []error
	if min == 0 {
		min = 1
	}
	for _, scid := range nfas {
		if !templates[ch.CodeHash(scid)] {
			continue // not the shipped nfa.bas: its keys mean nothing, and its PayOwed may never pay
		}
		owed := ch.Owed(scid, payee)
		if owed < min {
			continue
		}
		before := ch.Paid(scid, payee)
		txid, err := ch.SendPayOwed(scid, payee)
		if errors.Is(err, errDry) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("PayOwed on %s: %v", short(scid), err))
			continue
		}
		after := ch.Paid(scid, payee)
		if after <= before {
			errs = append(errs, fmt.Errorf("PayOwed on %s: tx %s was included but op_ did not grow: the %d units stay owed for the next sweep", short(scid), short(txid), owed))
			continue
		}
		paid[scid] = after - before
	}
	return paid, errs
}

func (d *deroChain) CodeHash(scid string) string {
	sum := sha256.Sum256([]byte(d.c.GetSC(scid, true).Code))
	return hex.EncodeToString(sum[:])
}
func (d *deroChain) Owed(scid, payee string) uint64 { return d.c.GetSC(scid, false).U0("ow_" + payee) }
func (d *deroChain) Paid(scid, payee string) uint64 { return d.c.GetSC(scid, false).U0("op_" + payee) }
func (d *deroChain) SendPayOwed(scid, payee string) (string, error) {
	args := []rpc.Arg{rpc.S("entrypoint", "PayOwed"), rpc.S("payee", payee)}
	// simrpc.Send stops the process when the node refuses its estimate. A PayOwed the node refuses is a reason to
	// skip one NFA this sweep, not to stop the bot, so the node is asked first.
	if _, err := d.c.Estimate(d.me, "", scid, nil, args); err != nil {
		return "", fmt.Errorf("the node refuses PayOwed: %v", err)
	}
	txid, _, _ := d.c.Send(*wallet, d.me, "", scid, nil, args)
	if len(txid) < 8 {
		return "", errors.New("the wallet returned no txid")
	}
	return txid, nil
}

// dryOwed reads the chain and refuses to send, like dryChain.
type dryOwed struct{ owedChain }

func (d dryOwed) SendPayOwed(scid, payee string) (string, error) {
	fmt.Printf("dry run: would send PayOwed(%s) to %s, owing %d\n", short(payee), short(scid), d.Owed(scid, payee))
	return "", errDry
}
