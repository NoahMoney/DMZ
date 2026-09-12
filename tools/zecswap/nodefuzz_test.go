package zecswap

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// MT-G06: a fuzz target for every node-response parser. Each is run for a fixed time (the suite runs them a moment;
// a campaign runs `go test -run x -fuzz FuzzName -fuzztime 30s`). The property is always the same: a node — honest,
// broken or hostile — can return any bytes, and the parser must return an error, never panic.
//
// Two kinds of response are parsed. The binary one is a raw transaction (getrawtransaction, getblock): the
// counterparty chooses the transaction that pays the script, so its bytes are adversarial. The JSON ones are the
// envelopes of the seven methods; each is fuzzed through the real ZcashRPC.call decode plus the method's own
// range and shape checks, by handing a fake node the fuzz bytes as that method's whole answer.

// ---- the binary parsers ----

func FuzzParseTxNeverPanics(f *testing.F) {
	for _, v := range []uint32{0, V5Version & 0x7fffffff, V6Version & 0x7fffffff} {
		tx := &Tx{Version: v, BranchID: BranchNU63, In: []TxIn{{PrevOut: OutPoint{Index: 0}, ScriptSig: []byte{1, 2}, Sequence: SequenceFinal}}, Out: []TxOut{{Value: 1, ScriptPubKey: []byte{0x76, 0xa9}}}}
		f.Add(tx.Serialize())
	}
	f.Add([]byte{})
	f.Add([]byte{5, 0, 0, 0})
	f.Fuzz(func(t *testing.T, raw []byte) {
		defer noPanic(t, "ParseTx", raw)
		_, _ = ParseTx(raw)
	})
}

func FuzzParseV6NeverPanics(f *testing.F) {
	tx := &Tx{Version: V6Version & 0x7fffffff, BranchID: BranchNU63, In: []TxIn{{Sequence: SequenceFinal}}}
	f.Add(tx.Serialize())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		defer noPanic(t, "ParseV6", raw)
		_, _ = ParseV6(raw)
	})
}

func FuzzTransparentPartsNeverPanics(f *testing.F) {
	tx := &Tx{Version: V5Version & 0x7fffffff, BranchID: BranchNU5, In: []TxIn{{ScriptSig: []byte{9}, Sequence: 1}}, Out: []TxOut{{Value: 7, ScriptPubKey: []byte{1, 2, 3}}}}
	f.Add(tx.Serialize())
	f.Add([]byte{1, 0, 0, 0}) // a pre-v5 header
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		defer noPanic(t, "transparentParts", raw)
		_, _, _ = transparentParts(raw)
	})
}

func FuzzClassifySpendNeverPanics(f *testing.F) {
	h := HTLC{CLTV: 1_000_000}
	for i := range h.Hash {
		h.Hash[i] = byte(i)
	}
	f.Add([]byte{})
	f.Add([]byte{0x20}) // a push of 32 bytes with nothing following
	f.Fuzz(func(t *testing.T, scriptSig []byte) {
		defer noPanic(t, "ClassifySpend", scriptSig)
		_, _ = ClassifySpend(scriptSig, h)
		_, _ = scriptPushes(scriptSig)
	})
}

func FuzzParseAddressNeverPanics(f *testing.F) {
	f.Add("t1Vt4nZ5Y6pQ7sT8uV9wXyZaBcDeFgHjKmN")
	f.Add("")
	f.Add("not an address")
	f.Fuzz(func(t *testing.T, s string) {
		defer noPanicStr(t, "ParseAddress", s)
		_, _, _, _ = ParseAddress(s)
	})
}

// ---- the seven JSON response parsers ----
//
// Each target hands the fuzz bytes to the node as one method's whole JSON-RPC answer and runs the real client path.

func fuzzResponse(f *testing.F, method string, seeds []string, call func(ctx context.Context, c *ZcashRPC) error) {
	z := newFakeZcash(f, "test", 5_000)
	var mu sync.Mutex
	var body []byte
	z.override = func(m string) ([]byte, int, bool) {
		if m != method {
			return nil, 0, false // genesisHash and the like fall through to a valid answer
		}
		mu.Lock()
		b := append([]byte(nil), body...)
		mu.Unlock()
		return b, 200, true
	}
	c := z.client()
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Add([]byte{})
	f.Add([]byte(`{"result":null,"error":null}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		mu.Lock()
		body = in
		mu.Unlock()
		defer noPanic(t, method, in)
		_ = call(context.Background(), c)
	})
}

func FuzzChainInfoResponse(f *testing.F) {
	fuzzResponse(f, "getblockchaininfo", []string{
		`{"result":{"chain":"main","blocks":100,"bestblockhash":"` + hex64(1) + `","consensus":{"chaintip":"37a5165b","nextblock":"37a5165b"}}}`,
		`{"result":{"chain":"test","blocks":"x"}}`,
	}, func(ctx context.Context, c *ZcashRPC) error { _, err := c.ChainInfo(ctx); return err })
}

func FuzzAddressUTXOsResponse(f *testing.F) {
	fuzzResponse(f, "getaddressutxos", []string{
		`{"result":[{"txid":"` + hex64(2) + `","outputIndex":0,"script":"76a914","satoshis":1000,"height":5}]}`,
		`{"result":[{"txid":"short"}]}`,
		`{"result":{}}`,
	}, func(ctx context.Context, c *ZcashRPC) error {
		_, err := c.AddressUTXOs(ctx, "t2Fftd1YywPRsMdiCbXPughqKJzMa3YLE5S")
		return err
	})
}

func FuzzAddressTxIDsResponse(f *testing.F) {
	fuzzResponse(f, "getaddresstxids", []string{
		`{"result":["` + hex64(3) + `","` + hex64(4) + `"]}`,
		`{"result":["nothex"]}`,
	}, func(ctx context.Context, c *ZcashRPC) error {
		_, err := c.AddressTxIDs(ctx, "t2Fftd1YywPRsMdiCbXPughqKJzMa3YLE5S", 1, 9)
		return err
	})
}

func FuzzRawTxResponse(f *testing.F) {
	fuzzResponse(f, "getrawtransaction", []string{
		`{"result":{"hex":"050000800a27a726","height":5,"confirmations":2,"blockhash":"` + hex64(5) + `"}}`,
		`{"result":{"hex":"zz"}}`,
		`{"result":"rawhexstring"}`,
	}, func(ctx context.Context, c *ZcashRPC) error { _, err := c.RawTx(ctx, hex64(6)); return err })
}

func FuzzTxOutResponse(f *testing.F) {
	fuzzResponse(f, "gettxout", []string{
		`{"result":{"bestblock":"` + hex64(7) + `","confirmations":3,"scriptPubKey":{"hex":"76a914"}}}`,
		`{"result":null,"error":null}`,
	}, func(ctx context.Context, c *ZcashRPC) error { _, err := c.TxOut(ctx, hex64(8), 0); return err })
}

func FuzzBlockHashResponse(f *testing.F) {
	fuzzResponse(f, "getblockhash", []string{
		`{"result":"` + hex64(9) + `"}`,
		`{"result":123}`,
	}, func(ctx context.Context, c *ZcashRPC) error { _, err := c.BlockHash(ctx, 1); return err })
}

func FuzzSendRawTxResponse(f *testing.F) {
	fuzzResponse(f, "sendrawtransaction", []string{
		`{"result":"` + hex64(10) + `"}`,
		`{"result":null,"error":{"code":-1,"message":"whatever"}}`,
	}, func(ctx context.Context, c *ZcashRPC) error {
		_, err := c.SendRawTx(ctx, []byte{5, 0, 0, 0})
		return err
	})
}

// the header gate's two methods (spv.go)
func FuzzBlockHeaderResponse(f *testing.F) {
	fuzzResponse(f, "getblockheader", []string{
		`{"result":"` + strings.Repeat("00", 1487) + `"}`,
		`{"result":"04"}`,
		`{"result":{"hash":"x"}}`,
	}, func(ctx context.Context, c *ZcashRPC) error { _, err := c.BlockHeader(ctx, hex64(11)); return err })
}

func FuzzBlockTxIDsResponse(f *testing.F) {
	fuzzResponse(f, "getblock", []string{
		`{"result":{"hash":"` + hex64(12) + `","tx":["` + hex64(13) + `"]}}`,
		`{"result":{"hash":"` + hex64(12) + `","tx":[]}}`,
		`{"result":{"tx":["nothex"]}}`,
	}, func(ctx context.Context, c *ZcashRPC) error { _, err := c.BlockTxIDs(ctx, hex64(12)); return err })
}

// ---- helpers ----

func noPanic(t *testing.T, what string, in []byte) {
	if r := recover(); r != nil {
		t.Fatalf("%s panicked on %d bytes (%x): %v", what, len(in), in, r)
	}
}

func noPanicStr(t *testing.T, what, in string) {
	if r := recover(); r != nil {
		t.Fatalf("%s panicked on %q: %v", what, in, r)
	}
}

func hex64(seed byte) string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return DisplayHex(*(*[32]byte)(b))
}
