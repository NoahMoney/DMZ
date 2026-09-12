package xmrswap

// MT-G06: fuzz targets for every parser of an answer the companion does not control — the DERO daemon's and wallet's
// JSON-RPC replies, monero-wallet-rpc's replies and its digest challenge, the prover's verdict, the addresses and keys
// those answers carry, and the sealed channel's envelope. A plain `go test` runs each on its seeds; each is also run
// for a fixed time, one at a time:
//
//	cd tools && go test -run '^$' -fuzz '^FuzzDeroReplies$' -fuzztime 30s ./xmrswap
//
// The property is the one a parser of hostile input owes: it returns, with a value or an error, and never panics or
// hangs.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// replyServer answers every request with whatever body the fuzzer last stored.
type replyServer struct {
	body atomic.Value
	url  string
}

func newReplyServer(f *testing.F) *replyServer {
	rs := &replyServer{}
	rs.body.Store([]byte(nil))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rs.body.Load().([]byte))
	}))
	f.Cleanup(srv.Close)
	rs.url = srv.URL
	return rs
}

var fuzzReplySeeds = []string{
	`{"jsonrpc":"2.0","id":"1","result":{"height":1000,"stableheight":992,"topoheight":1000,"status":"OK"}}`,
	`{"jsonrpc":"2.0","id":"1","result":{"testnet":true,"network":"Simulator"}}`,
	`{"jsonrpc":"2.0","id":"1","result":{"address":"` + aliceAddr + `"}}`,
	`{"jsonrpc":"2.0","id":"1","result":{"balance":18446744073709551615,"unlocked_balance":1}}`,
	`{"jsonrpc":"2.0","id":"1","result":{"stringkeys":{"n":1,"st_0":0,"t1_0":1080,"ha_0":"` + testHTLC + `"},"balance":5000,"code":"Function X"}}`,
	`{"jsonrpc":"2.0","id":"1","result":{"entries":[{"height":5,"txid":"ab","amount":1,"incoming":true,"dstport":7}]}}`,
	`{"jsonrpc":"2.0","id":"1","error":{"code":-32098,"message":"Discarded knowingly"}}`,
	`{"jsonrpc":"2.0","id":"1","result":null}`,
	`{"jsonrpc":"2.0","id":"1","result":{}} trailing`,
	`{"id":"0","jsonrpc":"2.0","result":{"in":[{"txid":"aa","amount":1000000000000,"height":10,"confirmations":12,"locked":false,"unlock_time":0}],"out":[{"txid":"bb"}],"pending":[{"txid":"cc"}]}}`,
	`{"id":"0","jsonrpc":"2.0","result":{"tx_hash":"cc","tx_hash_list":["cc"],"unsigned_txset":""}}`,
	`[]`, `null`, ``, `{"result":{"height":-1}}`, `{"result":{"height":1e400}}`,
}

// FuzzDecodeEnvelope: the one reader of every DERO JSON-RPC reply.
func FuzzDecodeEnvelope(f *testing.F) {
	for _, s := range fuzzReplySeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var out struct {
			Height uint64 `json:"height"`
		}
		_ = decodeEnvelope("m", raw, &out)
		_ = decodeEnvelope("m", raw, nil)
		var anything map[string]interface{}
		_ = decodeEnvelope("m", raw, &anything)
	})
}

// FuzzDeroReplies: every DERO read the companion makes, against a daemon and wallet answering with the fuzzer's bytes.
func FuzzDeroReplies(f *testing.F) {
	for _, s := range fuzzReplySeeds {
		f.Add([]byte(s))
	}
	rs := newReplyServer(f)
	f.Fuzz(func(t *testing.T, raw []byte) {
		rs.body.Store(raw)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d := NewDeroRPC(rs.url, rs.url, testHTLC)
		d.HTTP = &http.Client{Timeout: 5 * time.Second}
		_, _ = d.Height(ctx)
		_, _ = d.StableHeight(ctx)
		_, _, _ = d.Info(ctx)
		_, _ = d.Address(ctx)
		_, _ = d.Balance(ctx, "")
		_, _ = d.Balance(ctx, testHTLC)
		_, _ = d.ReadLock(ctx, 0)
		_, _ = d.CodeSHA256(ctx)
		_, _ = d.Registered(ctx, aliceAddr)
		_, _, _ = d.FindPayment(ctx, true, 7, 0)
		_, _ = d.WalletAcceptsAnonymous(ctx)
	})
}

// FuzzWalletReplies: every monero-wallet-rpc call the companion makes, against a wallet-rpc answering with the
// fuzzer's bytes.
func FuzzWalletReplies(f *testing.F) {
	for _, s := range fuzzReplySeeds {
		f.Add([]byte(s))
	}
	rs := newReplyServer(f)
	spend, _ := NewScalar(rand.Reader)
	view, _ := NewScalar(rand.Reader)
	addr, _ := Address(spend.Public(), view.Public(), Stagenet)
	f.Fuzz(func(t *testing.T, raw []byte) {
		rs.body.Store(raw)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		w := NewWalletRPC(rs.url, "", "")
		_, _ = w.Height(ctx)
		_, _ = w.Address(ctx)
		_, _ = w.Incoming(ctx, 0)
		_, _ = w.Incoming(ctx, 10)
		_, _ = w.UnlockedBalance(ctx)
		_, _ = w.Outgoing(ctx)
		_, _ = w.Transfer(ctx, addr, 1)
		_, _ = w.SweepAll(ctx, addr)
		_ = w.OpenWallet(ctx, "n", "")
		_ = w.Refresh(ctx, 0)
		_ = w.GenerateFromKeys(ctx, "n", addr, view.Hex(), spend.Hex(), "", 0)
		_ = w.GenerateFromKeys(ctx, "n", addr, view.Hex(), "", "", 0)
		if ds, ok := w.(DaemonSetter); ok {
			_ = ds.SetDaemon(ctx, "127.0.0.1:1")
		}
	})
}

// FuzzDigestChallenge: monero-wallet-rpc's WWW-Authenticate header, as the digest client reads it.
func FuzzDigestChallenge(f *testing.F) {
	for _, s := range []string{
		`Digest realm="monero-rpc", qop="auth", algorithm=MD5, nonce="4fd2a", stale=false`,
		`Digest qop="auth",algorithm=MD5-sess,realm="monero-rpc",nonce="a=b,c",stale=true`,
		`Basic realm="x"`, `Digest `, `Digest realm="unterminated`, `Digest ,,,=,"`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseDigestChallenge([]string{s})
		_, _ = parseDigestChallenge([]string{s, s})
		_ = parseAuthParams(s)
	})
}

// FuzzProverVerdict: the verdict proof-xmr prints, read against the statement asked about.
func FuzzProverVerdict(f *testing.F) {
	sc, _ := NewScalar(rand.Reader)
	s := sc.Public()
	h := SHA256Hex([]byte("statement"))
	for _, v := range []string{
		`{"valid":true,"h":"` + h + `","s":"` + s.Hex() + `"}`, `{"valid":false}`, ``, `{"valid":true}`, "valid\n", `{"valid":"true"}`,
	} {
		f.Add([]byte(v))
	}
	f.Fuzz(func(t *testing.T, out []byte) {
		_ = checkVerdict(out, h, s)
	})
}

// FuzzMoneroValues: the addresses and keys a wallet-rpc answer or a peer carries.
func FuzzMoneroValues(f *testing.F) {
	spend, _ := NewScalar(rand.Reader)
	view, _ := NewScalar(rand.Reader)
	addr, _ := Address(spend.Public(), view.Public(), Stagenet)
	for _, v := range []string{addr, spend.Hex(), spend.Public().Hex(), "4", "", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = ParseAddress(s)
		_, _ = ParseScalar(s)
		_, _ = ParsePoint(s)
		_ = DeroNetwork(s)
		_, _ = DeroRaw(s)
	})
}

// captureChannel keeps what is sent on it.
type captureChannel struct{ sent []Message }

func (c *captureChannel) Send(_ context.Context, _ string, m Message) error {
	c.sent = append(c.sent, m)
	return nil
}
func (c *captureChannel) Recv(ctx context.Context) (Message, error) {
	<-ctx.Done()
	return Message{}, ctx.Err()
}

// FuzzSealedEnvelope: what the sealed channel reads out of a box a stranger can write to.
func FuzzSealedEnvelope(f *testing.F) {
	key := make([]byte, ChannelKeyLen)
	_, _ = rand.Read(key)
	sink := &captureChannel{}
	alice, _ := NewSealedChannel(sink, key, "alice", "bob")
	_ = alice.Send(context.Background(), "bob", Message{Kind: KindXmrLocked, Body: json.RawMessage(`{"tx":"aa"}`)})
	for _, m := range sink.sent {
		f.Add([]byte(m.Body))
	}
	f.Add([]byte(`{"n":"000000000000000000000000","c":"AAAA"}`))
	f.Add([]byte(`{"n":1,"c":null}`))
	bob, err := NewSealedChannel(&captureChannel{}, key, "bob", "alice")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = bob.open(Message{Kind: KindSealed, Body: body})
		_, _ = bob.open(Message{Kind: "gate", Body: body})
	})
}
