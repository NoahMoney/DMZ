package zcashvault

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"veil/tools/zcashtx"
)

// Spending a vault output. The scriptSig is
//
//	OP_0 <sig_1> … <sig_k> <redeem script>
//
// OP_0 is CHECKMULTISIG's extra stack item (NULLDUMMY: it must be empty), and the signatures appear in the order
// of the keys they belong to, because the interpreter walks the keys once, forward, looking for each signature in
// turn. Each signature is DER, low-S, with SIGHASH_ALL appended, over the ZIP 244 digest of that input — whose
// txin digest commits to the P2SH scriptPubKey being spent, its value, and every other input's value and script, so a
// signature is valid for exactly one transaction and one input of it. That is what makes a signature relayed through
// the coordinator safe to accept from anyone: a forged, replayed or misattributed one does not verify here.

// signInput signs input i of tx, whose every input carries the value and scriptPubKey it spends.
func signInput(tx *zcashtx.Tx, i int, key *zcashtx.Key) ([]byte, error) {
	d, err := tx.SigDigest(i, zcashtx.SigHashAll)
	if err != nil {
		return nil, err
	}
	sig := key.Sign(d)
	if !checkSig(sig, key.PubKey(), d) {
		return nil, errors.New("zcashvault: a signature did not verify against its own key")
	}
	return sig, nil
}

// checkSig is zcashtx.VerifySig plus relay policy's LOW_S: a high-S signature is valid by consensus but zebrad's
// mempool may refuse the transaction that carries it, which would let one co-signer stall a release.
func checkSig(sigWithType, pub []byte, d [32]byte) bool {
	if !zcashtx.VerifySig(sigWithType, pub, d) {
		return false
	}
	sig, err := ecdsa.ParseDERSignature(sigWithType[:len(sigWithType)-1])
	if err != nil {
		return false
	}
	s := sig.S()
	return !s.IsOverHalfOrder()
}

// multisigScriptSig assembles OP_0 <sigs…> <redeem> with minimal pushes.
func multisigScriptSig(sigs [][]byte, redeem []byte) ([]byte, error) {
	b := []byte{zcashtx.OpFalse}
	for _, s := range sigs {
		p, err := zcashtx.Push(s)
		if err != nil {
			return nil, err
		}
		b = append(b, p...)
	}
	p, err := zcashtx.Push(redeem)
	if err != nil {
		return nil, err
	}
	return append(b, p...), nil
}

// checkMultisigInput verifies input i of tx the way the interpreter would spend vs: the scriptSig is exactly
// OP_0, k signatures and vs's redeem script, in canonical minimal pushes; the input spends vs's P2SH script; and
// the k signatures match k of vs's keys in increasing key order, each valid, low-S and SIGHASH_ALL over this input's
// digest. It is what every signer runs on a transaction it did not assemble before it treats it as a payment.
func checkMultisigInput(tx *zcashtx.Tx, i int, vs *VaultScript, k int) error {
	in := tx.In[i]
	if !bytes.Equal(in.ScriptPubKey, vs.ScriptPubKey) {
		return fmt.Errorf("input %d does not spend the vault script it is checked against", i)
	}
	items, err := zcashtx.ScriptPushes(in.ScriptSig)
	if err != nil {
		return fmt.Errorf("input %d: %v", i, err)
	}
	if len(items) != k+2 || len(items[0]) != 0 || !bytes.Equal(items[len(items)-1], vs.Redeem) {
		return fmt.Errorf("input %d: the scriptSig is not OP_0, %d signatures and the vault's redeem script", i, k)
	}
	sigs := items[1 : k+1]
	if canon, err := multisigScriptSig(sigs, vs.Redeem); err != nil || !bytes.Equal(canon, in.ScriptSig) {
		return fmt.Errorf("input %d: the scriptSig is not in canonical form", i)
	}
	d, err := tx.SigDigest(i, zcashtx.SigHashAll)
	if err != nil {
		return err
	}
	// CHECKMULTISIG: each signature, in order, must match a key at or after the one the previous signature used
	pos := 0
	for si, s := range sigs {
		for pos < len(vs.Pubs) && !checkSig(s, vs.Pubs[pos], d) {
			pos++
		}
		if pos == len(vs.Pubs) {
			return fmt.Errorf("input %d: signature %d matches none of the remaining keys", i, si)
		}
		pos++
	}
	return nil
}
