// Command zecshape runs tools/zecswap's own node client and transaction code against the harness's real regtest
// zebrad, and reports every place they and the node disagree. It is read-only apart from one Broadcast of a
// transaction the chain already holds (zecswap's idempotence path), and it changes nothing in tools/zecswap.
//
// Two things are checked with zecswap's code rather than the smoke's:
//   - the six RPC methods, through zecswap.ZcashRPC exactly as the companion calls them;
//   - ZIP 244: zfund built and signed the smoke's transactions with librustzcash, and zebrad accepted them; here
//     zecswap parses those bytes (ParseV5), recomputes their txids, recomputes each input's signature digest
//     (SigDigest) and verifies the librustzcash signature against it (VerifySig). Agreement means the two
//     independent implementations compute the same ZIP 244 digests for these transactions.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"veil/tools/zecswap"
)

type refsFile struct {
	Refs map[string]interface{} `json:"refs"`
}

var fails int

func report(ok bool, f string, a ...interface{}) {
	tag := "  PASS  "
	if !ok {
		tag = "  DIFF  "
		fails++
	}
	fmt.Printf(tag+f+"\n", a...)
}

func ref(r map[string]interface{}, k string) string { return fmt.Sprint(r[k]) }

// pushes splits a push-only scriptSig into its data items (direct pushes and PUSHDATA1 only: a P2PKH scriptSig).
func pushes(s []byte) ([][]byte, error) {
	var out [][]byte
	for i := 0; i < len(s); {
		n := int(s[i])
		i++
		if n == 0x4c {
			if i >= len(s) {
				return nil, errors.New("truncated PUSHDATA1")
			}
			n = int(s[i])
			i++
		} else if n > 0x4b {
			return nil, fmt.Errorf("opcode %02x", n)
		}
		if i+n > len(s) {
			return nil, errors.New("push past the end")
		}
		out = append(out, s[i:i+n])
		i += n
	}
	return out, nil
}

// checkSigs verifies every input of the tx named txid with zecswap's ZIP 244 digest, reading each prevout's value
// and script from the transaction that created it.
func checkSigs(ctx context.Context, zc *zecswap.ZcashRPC, label, txid string) {
	rt, err := zc.RawTx(ctx, txid)
	if err != nil {
		report(false, "%s: RawTx %s: %v", label, txid, err)
		return
	}
	tx, err := zecswap.ParseV5(rt.Raw)
	if err != nil {
		report(false, "%s: zecswap.ParseV5 cannot read the librustzcash-built transaction: %v", label, err)
		return
	}
	report(zecswap.DisplayHex(tx.TxID()) == txid, "%s: zecswap's ZIP 244 txid of the bytes is the node's txid %s", label, txid)
	for i := range tx.In {
		prevID := zecswap.DisplayHex(tx.In[i].PrevOut.Hash)
		prt, err := zc.RawTx(ctx, prevID)
		if err != nil {
			report(false, "%s input %d: RawTx of the prevout %s: %v", label, i, prevID, err)
			return
		}
		prev, err := zecswap.ParseV5(prt.Raw)
		if err == nil && int(tx.In[i].PrevOut.Index) < len(prev.Out) {
			o := prev.Out[tx.In[i].PrevOut.Index]
			tx.In[i].Value, tx.In[i].ScriptPubKey = o.Value, o.ScriptPubKey
			continue
		}
		report(false, "%s input %d: zecswap.ParseV5 cannot read the transaction that created the prevout (%s): %v", label, i, prevID, err)
		// take the prevout from the node's own decoding instead, so the digest can still be checked
		v, spk, jerr := voutFromNode(ctx, prevID, tx.In[i].PrevOut.Index)
		if jerr != nil {
			report(false, "%s input %d: the node's verbose decoding of %s: %v", label, i, prevID, jerr)
			return
		}
		tx.In[i].Value, tx.In[i].ScriptPubKey = v, spk
	}
	for i := range tx.In {
		d, err := tx.SigDigest(i, zecswap.SigHashAll)
		if err != nil {
			report(false, "%s input %d: SigDigest: %v", label, i, err)
			continue
		}
		p, err := pushes(tx.In[i].ScriptSig)
		if err != nil || len(p) != 2 {
			report(false, "%s input %d: not a P2PKH scriptSig (%v)", label, i, err)
			continue
		}
		report(zecswap.VerifySig(p[0], p[1], d), "%s input %d: the librustzcash signature verifies against zecswap's SigDigest (branch %08x)", label, i, tx.BranchID)
	}
}

var rpcURL, rpcUser, rpcPass string

// voutFromNode reads one output of a transaction from getrawtransaction verbose (valueZat and scriptPubKey.hex):
// the node's decoding, for a transaction version zecswap does not parse.
func voutFromNode(ctx context.Context, txid string, n uint32) (uint64, []byte, error) {
	body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "1.0", "id": "zecshape", "method": "getrawtransaction", "params": []interface{}{txid, 1}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(rpcUser, rpcPass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var env struct {
		Result struct {
			Vout []struct {
				ValueZat     uint64 `json:"valueZat"`
				N            uint32 `json:"n"`
				ScriptPubKey struct {
					Hex string `json:"hex"`
				} `json:"scriptPubKey"`
			} `json:"vout"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, nil, err
	}
	for _, o := range env.Result.Vout {
		if o.N == n {
			spk, err := hex.DecodeString(o.ScriptPubKey.Hex)
			return o.ValueZat, spk, err
		}
	}
	return 0, nil, fmt.Errorf("no vout %d", n)
}

func main() {
	rpc := flag.String("rpc", "http://127.0.0.1:28232/", "zebrad RPC")
	cookie := flag.String("cookie", "", "zebrad's .cookie")
	refsPath := flag.String("refs", "", "the shapes file zecsmoke wrote (its refs block)")
	flag.Parse()
	user, pass, err := zecswap.ReadCookie(*cookie)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	b, err := os.ReadFile(*refsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var rf refsFile
	if err := json.Unmarshal(b, &rf); err != nil || rf.Refs == nil {
		fmt.Fprintln(os.Stderr, "no refs in", *refsPath, err)
		os.Exit(2)
	}
	r := rf.Refs
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rpcURL, rpcUser, rpcPass = *rpc, user, pass
	zc := zecswap.NewZcashRPC(*rpc, user, pass)

	fmt.Println("tools/zecswap's own client and transaction code against the real regtest zebrad:")
	ci, err := zc.ChainInfo(ctx)
	if err != nil {
		report(false, "ChainInfo refuses the node: %v", err)
	} else {
		report(true, "ChainInfo: chain %q regtest %v height %d nextblock %08x", ci.Chain, ci.Regtest, ci.Height, ci.NextBranch)
	}
	var h uint32
	fmt.Sscan(ref(r, "funding_height"), &h)
	if bh, err := zc.BlockHash(ctx, h); err != nil {
		report(false, "BlockHash(%d): %v", h, err)
	} else {
		report(true, "BlockHash(%d) = %s", h, bh)
	}
	us, err := zc.AddressUTXOs(ctx, ref(r, "p2sh"))
	report(err == nil && len(us) >= 1, "AddressUTXOs(P2SH %s): %d output(s), err %v", ref(r, "p2sh"), len(us), err)
	ids, err := zc.AddressTxIDs(ctx, ref(r, "p2sh"), h, h)
	report(err == nil && len(ids) == 1 && ids[0] == ref(r, "funding_txid"), "AddressTxIDs(P2SH, %d, %d): %v, err %v", h, h, ids, err)
	rt, err := zc.RawTx(ctx, ref(r, "funding_txid"))
	report(err == nil && rt.Mined && rt.Height == h && rt.Verified, "RawTx(funding): mined %v height %d confirmations %d verified %v, err %v", rt.Mined, rt.Height, rt.Confirmations, rt.Verified, err)
	_, err = zc.RawTx(ctx, strings.Repeat("00", 31)+"01")
	report(errors.Is(err, zecswap.ErrTxNotFound), "RawTx(unknown txid) is ErrTxNotFound: %v", err)
	var pidx uint32
	fmt.Sscan(ref(r, "p2sh_index"), &pidx)
	ti, terr := zc.TxOut(ctx, ref(r, "funding_txid"), pidx)
	report(terr == nil && ti != nil, "TxOut(the funding's P2SH output): %+v, err %v", ti, terr)
	ts, terr := zc.TxOut(ctx, ref(r, "coinbase_txid"), 0)
	report(terr == nil && ts == nil, "TxOut(a spent coinbase output) is nil: %+v, err %v", ts, terr)
	if v6 := ref(r, "v6_txid"); v6 != "<nil>" && v6 != "" {
		rt6, err := zc.RawTx(ctx, v6)
		report(err == nil, "RawTx(a v6 transaction): mined %v verified %v (zecswap computes ids for v1-v5 only), err %v", rt6.Mined, rt6.Verified, err)
		if err == nil {
			_, perr := zecswap.ParseV5(rt6.Raw)
			report(perr == nil, "zecswap.ParseV5 of a v6 transparent transaction (librustzcash's default under NU6.3; also Zebra's coinbase): %v", perr)
		}
	}
	if err == nil {
		id, berr := zecswap.Broadcast(ctx, zc, rt.Raw)
		report(berr == nil && id == ref(r, "funding_txid"), "Broadcast of a transaction the chain already holds is idempotent: %s, err %v", id, berr)
	}
	if err := zecswap.CheckBranchID(zecswap.Testnet, h+1, zecswap.BranchNU63); err != nil {
		report(false, "CheckBranchID(Testnet, %d, NU6.3): %v (the harness activates NU6.3 at 6; zecswap's schedule is testnet's)", h+1, err)
	}
	fmt.Println("ZIP 244 cross-check, librustzcash (zfund) vs tools/zecswap:")
	checkSigs(ctx, zc, "funding tx (spends a coinbase)", ref(r, "funding_txid"))
	if hop := ref(r, "hop2_txid"); hop != "<nil>" {
		checkSigs(ctx, zc, "second-hop tx", hop)
	}
	fmt.Printf("%d difference(s)\n", fails)
}
