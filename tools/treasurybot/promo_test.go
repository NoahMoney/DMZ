package main

import (
	"path/filepath"
	"strings"
	"testing"

	rpc "veil/tools/simrpc"
)

// fakePromo is promo.bas as the bot sees it: a code hash, a treasury, what ClaimProceeds would pay, and fc (kept in the
// fake chain's market record for the promo id, where the journal reads every market's fc). A claim that lands moves
// the claimable amount into fc and to the treasury, as ClaimProceeds does; crash makes it land and then fail.
type fakePromo struct {
	f                     *fakeChain
	code, treasury, quote string
	claimable             uint64
	lands, crash          bool
	sent                  int
}

func (p *fakePromo) CodeHash(string) string       { return p.code }
func (p *fakePromo) PromoTreasury(string) string  { return p.treasury }
func (p *fakePromo) PromoQuote(string) string     { return p.quote }
func (p *fakePromo) PromoClaimable(string) uint64 { return p.claimable }
func (p *fakePromo) SendClaimProceeds(s string) (string, error) {
	p.sent++
	if p.lands {
		p.f.m[s].fc += p.claimable
		p.f.received[p.quote] += p.claimable
		p.claimable = 0
	}
	if p.crash {
		return "", errCrash
	}
	return "promotx", nil
}

const promoTplHash = "8cf7537d03fc2968d1ec5047cb8d0a31189245f7a2e7941c523e641dfe3f87a0"

func newPromoFake(t *testing.T) (*journal, *fakeChain, *fakePromo, string) {
	ch := newFakeChain(2)
	ps := scid('p')
	ch.m[ps] = &fakeMarket{quote: deroID, asset: deroID}
	return newJournal(filepath.Join(t.TempDir(), "j.json")), ch, &fakePromo{f: ch, code: promoTplHash, treasury: treasury, quote: deroID, claimable: 300000, lands: true}, ps
}

// A wXMR auction (the runbook's): its proceeds are owed to the passes in wXMR and deposited with Deposit(wXMR), like any
// other fee asset, never converted; a quote that cannot be read is refused.
func TestPromoProceedsInAWrappedAsset(t *testing.T) {
	jn, ch, p, ps := newPromoFake(t)
	ch.m[ps] = &fakeMarket{quote: wxmr, asset: wxmr}
	p.quote = wxmr
	units, err := claimPromo(jn, ch, p, ps, treasury, map[string]bool{promoTplHash: true}, 1)
	if err != nil || units != 300000 || jn.Owed[wxmr] != 300000 || jn.Owed[deroID] != 0 {
		t.Fatalf("claim: units %d err %v owed %v", units, err, jn.Owed)
	}
	if err := jn.depositShare(ch, 5000); err != nil {
		t.Fatal(err)
	}
	if d := ch.deposits[wxmr]; len(d) != 1 || d[0] != 150000 || len(ch.deposits[deroID]) != 0 {
		t.Fatalf("the passes' share: deposits %v", ch.deposits)
	}
	p.quote, p.claimable = "", 5000
	if _, err := claimPromo(jn, ch, p, ps, treasury, map[string]bool{promoTplHash: true}, 1); err == nil || p.sent != 1 {
		t.Fatalf("an unreadable quote: err %v sent %d", err, p.sent)
	}
}

// Claimed proceeds are counted by fc growth and owed to the passes in DERO; the next deposit pays their share. Below
// the floor nothing is sent; a claim that is included but does not execute is not counted.
func TestPromoProceedsAreClaimedAndShared(t *testing.T) {
	jn, ch, p, ps := newPromoFake(t)
	tpl := map[string]bool{promoTplHash: true}
	units, err := claimPromo(jn, ch, p, ps, treasury, tpl, 5000)
	if err != nil || units != 300000 || jn.Owed[deroID] != 300000 || ch.m[ps].fc != 300000 {
		t.Fatalf("claim: units %d err %v owed %d fc %d", units, err, jn.Owed[deroID], ch.m[ps].fc)
	}
	if err := jn.depositShare(ch, 5000); err != nil {
		t.Fatal(err)
	}
	if d := ch.deposits[deroID]; len(d) != 1 || d[0] != 150000 || len(jn.Owed) != 0 {
		t.Fatalf("the passes' DERO share of the proceeds: deposits %v, still owed %v", d, jn.Owed)
	}
	p.claimable = 4999
	if units, err := claimPromo(jn, ch, p, ps, treasury, tpl, 5000); units != 0 || err != nil || p.sent != 1 {
		t.Fatalf("below the floor: units %d err %v sent %d", units, err, p.sent)
	}
	p.claimable, p.lands = 9000, false
	if units, err := claimPromo(jn, ch, p, ps, treasury, tpl, 5000); err == nil || units != 0 || jn.Owed[deroID] != 0 || len(jn.Claims) != 0 {
		t.Fatalf("a claim that did not execute: units %d err %v owed %d records %d", units, err, jn.Owed[deroID], len(jn.Claims))
	}
}

// Fail closed: a contract that is not the shipped template, or that pays another treasury, is never called.
func TestPromoRefusesAnotherTemplateOrTreasury(t *testing.T) {
	jn, ch, p, ps := newPromoFake(t)
	if _, err := claimPromo(jn, ch, p, ps, treasury, map[string]bool{strings.Repeat("0", 64): true}, 1); err == nil || p.sent != 0 {
		t.Fatalf("another template: err %v sent %d", err, p.sent)
	}
	p.treasury = "dero1qysomeoneelse"
	if _, err := claimPromo(jn, ch, p, ps, treasury, map[string]bool{promoTplHash: true}, 1); err == nil || p.sent != 0 {
		t.Fatalf("another treasury: err %v sent %d", err, p.sent)
	}
	if u, err := claimPromo(jn, ch, p, "", treasury, nil, 1); u != 0 || err != nil {
		t.Fatal("a build with no promotion contract did something")
	}
}

// A crash between the send and the journal write: the record stays, and the next start counts it by fc growth.
func TestPromoClaimSurvivesACrash(t *testing.T) {
	jn, ch, p, ps := newPromoFake(t)
	p.crash = true
	if _, err := claimPromo(jn, ch, p, ps, treasury, map[string]bool{promoTplHash: true}, 1); err == nil || len(jn.Claims) != 1 {
		t.Fatalf("crash: err %v records %d", err, len(jn.Claims))
	}
	back, err := loadJournal(jn.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := back.reconcile(ch); err != nil {
		t.Fatal(err)
	}
	if back.Owed[deroID] != 300000 || len(back.Claims) != 0 {
		t.Fatalf("after reconcile: owed %d records %d", back.Owed[deroID], len(back.Claims))
	}
}

// rawAddress writes the spelling ADDRESS_STRING writes, which the bot compares with its own wallet's DVMAddress.
func TestRawAddressIsTheDVMSpelling(t *testing.T) {
	raw := make([]byte, 33)
	raw[0] = 2
	for i := 1; i < 33; i++ {
		raw[i] = byte(i * 7)
	}
	a := rawAddress(raw)
	if !strings.HasPrefix(a, "dero1") || rpc.DVMAddress(a) != a {
		t.Fatalf("rawAddress %q is not a DVM address (DVMAddress gives %q)", a, rpc.DVMAddress(a))
	}
	raw[5]++
	if rawAddress(raw) == a || rawAddress(raw[:32]) != "" {
		t.Fatal("rawAddress ignores its input")
	}
}

// config.js names the contract as `promo: "<scid>"` and its template under templates; each regex reads only its own.
func TestPromoConfigIsRead(t *testing.T) {
	id, h := strings.Repeat("ab", 32), strings.Repeat("cd", 32)
	src := "  board: \"" + strings.Repeat("ef", 32) + "\",\n  promo: \"" + id + "\",\n  templates: {\n    promo:    { file: \"templates/promo.bas\", sha256: \"" + h + "\", legacy: [] }\n  }"
	if cfgValue(src, "promo") != id {
		t.Fatalf("promo scid: %q", cfgValue(src, "promo"))
	}
	if tpl := promoTemplates(src); len(tpl) != 1 || !tpl[h] {
		t.Fatalf("promo template: %v", tpl)
	}
}
