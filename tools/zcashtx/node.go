package zcashtx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Zcash node, over its JSON-RPC.
//
// TRUST. The node this client talks to is a trusted party for everything the companion knows about the Zcash
// chain: the height, the outputs paying the script, how deep they are, and the transactions that spend them.
// Nothing in the protocol can tell a lying node from an honest one (docs/ZEC_SWAP.md §9, Z14 and Z15). The
// intended node is one the person runs on their own machine; a remote node — a friend's, a provider's, a public
// one — is trusted for all of the above, and the command line says so every time it is given one. A lightwalletd
// is NOT supported: it speaks gRPC, which this module could reach only through a new dependency, and it would be
// a trusted party in exactly the same way; nothing in this package connects to one.
//
// A companion may be given more than one node for each chain (quorum.go): the step each side's safety rests on
// then waits for every node to agree. That makes one liar insufficient; it does not remove the trust.
//
// METHODS. Exactly nine, each named with what it needs. zcashd's pages are https://zcash.github.io/rpc/<method>.html;
// zebrad's are the RpcServer trait of the zebra-rpc crate (https://docs.rs/zebra-rpc), each method of which links
// the zcashd page it follows. lightwalletd decodes the replies of both nodes with one set of structures
// (lightwalletd common/common.go), which is why the field names below are the same for both. The first seven were
// run against a real zebrad 6.3.0 on regtest (tools/regtest, research/regtest/zcash-smoke.log and zecswap-e2e.log);
// the last two, which only the header gate calls (-verify-headers, spv.go), by TestRegtestHeaderPlumbing
// (research/regtest/zecswap-headers.log). tools/regtest/rpcgate serves exactly these nine (TestGateServesExactlyTheNodeClientsMethods).
//
//	getblockchaininfo   zcashd: no flag. zebrad: yes, "only the fields required for lightwalletd support", which
//	                    are chain, blocks, bestblockhash, estimatedheight, upgrades and consensus.{chaintip,nextblock}
//	                    — every field read here.
//	getblockhash        both, no flag. Height 0 is also asked, once per client, to tell regtest apart (below).
//	getaddressutxos     zcashd: only with the insight-explorer indexes — txindex=1, experimentalfeatures=1,
//	                    insightexplorer=1 (zcash.readthedocs.io, rtd_pages/insight_explorer.html; the method's page
//	                    also names lightwalletd=1 in place of insightexplorer=1). zebrad: always; its finalized state
//	                    keeps the transparent address indexes with no flag (zebra.zfnd.org/dev/state-db-upgrades.html).
//	getaddresstxids     the same as getaddressutxos, on both.
//	getrawtransaction   zcashd: a mined transaction only with txindex=1 ("If blockhash is not provided and the -txindex
//	                    option is not enabled, then this call only works for mempool transactions"). zebrad: any
//	                    transaction, no flag (it searches the mempool, then the state).
//	sendrawtransaction  both, no flag. zcashd answers a transaction it already holds in its mempool with the txid,
//	                    and one already mined with error -27, "transaction already in block chain"
//	                    (src/rpc/rawtransaction.cpp); zebrad 6.3.0 answers both with -1 (measured). Broadcast never
//	                    reads meaning into an error's text: after any error it asks the node whether it holds the
//	                    transaction.
//	gettxout            both, no flag (zcash.github.io/rpc/gettxout.html; zebra-rpc get_tx_out): null for an output
//	                    the chain has spent or never had, else scriptPubKey.hex, confirmations and bestblock.
//	getblock            both, no flag, for a block on the node's chain named by hash; verbosity 1 answers hash and
//	                    tx, the txids in the block's order. Only the header gate calls it, for the funding's Merkle
//	                    branch; a wrong list does not hash to the header's root, so it needs no trust.
//	getblockheader      both, no flag; verbose false answers the serialized header, 1,487 bytes (zebrad 6.3.0
//	                    measured). Only the header gate calls it.
//
// So: zebrad with its RPC enabled ([rpc] listen_addr) needs nothing else; zcashd needs txindex=1,
// experimentalfeatures=1 and insightexplorer=1 and a reindex. The 2026-09-10 audit (finding ZEC-8) records that
// zcashd reached end of support on 2026-07-18 and will not follow NU6.3; this client does not depend on which of
// the two it is talking to, and CheckBranchID refuses to sign when a node's view of the chain has fallen behind the
// schedule this build carries.
//
// REGTEST. zebrad 6.3.0 answers "chain": "test" on regtest. A node is taken for regtest when, and only when, its
// block 0 is regtest's genesis block (RegtestGenesis) — which no mainnet or testnet node has — and its upgrade
// heights are then the node's own (CheckBranchIDOnSchedule). A node that says "regtest" with any other genesis is
// refused. The command line reaches regtest only with -network regtest (CheckNetworks), so a person running on
// mainnet or testnet can never be moved onto the node's own schedule.
//
// Every field of every answer is checked for shape and range before anything reads it: hashes are 64 hex
// characters, heights are below LockTimeThreshold, amounts are at most MaxMoney, a raw transaction hashes to the id
// it was asked for (where its format lets this package compute the id), and a node whose height is implausible for
// its network, still syncing, or lower than one it reported before by more than a reorganisation can explain, is
// refused rather than believed.

// Zcash is the node as the state machines see it. ZcashRPC is the one implementation; the tests reach it through
// a fake node served over HTTP, or the real zebrad on regtest.
type Zcash interface {
	ChainInfo(ctx context.Context) (ChainInfo, error)
	BlockHash(ctx context.Context, height uint32) (string, error)
	AddressUTXOs(ctx context.Context, addr string) ([]UTXO, error)
	AddressTxIDs(ctx context.Context, addr string, start, end uint32) ([]string, error)
	RawTx(ctx context.Context, txid string) (RawTx, error)
	SendRawTx(ctx context.Context, raw []byte) (string, error)
	TxOut(ctx context.Context, txid string, index uint32) (*TxOutInfo, error)
}

// TxOutInfo is gettxout for an output the best chain has not spent; TxOut answers nil for one it has spent or
// never had.
type TxOutInfo struct {
	Script        []byte
	Confirmations uint32
	BestBlock     string
}

// RegtestGenesis is the hash of the genesis block of Zcash's regtest network (zcashd and zebrad alike).
const RegtestGenesis = "029f11d80ef9765602235e1bc9727e3eb6ba20839319f761fee920d63401e327"

// ChainInfo is getblockchaininfo, checked.
type ChainInfo struct {
	Chain       string // "main", "test", or "regtest" — the last only when block 0 is RegtestGenesis
	Net         Network
	Regtest     bool
	Height      uint32
	BestHash    string
	TipBranch   uint32 // consensus.chaintip: the epoch of the block at the tip
	NextBranch  uint32 // consensus.nextblock: the epoch of the block a transaction sent now will be mined in
	Estimated   uint32 // estimatedheight, or 0 when the node does not report it
	IBDComplete *bool  // initial_block_download_complete, when reported
	// Upgrades is the node's whole upgrade table, in ascending height; on regtest it is the schedule.
	Upgrades []NodeUpgrade
	// Pending are the upgrades the node knows of that have not activated at Height+1: the next consensus branch
	// ids, and the heights at which a signature made today stops being valid.
	Pending []NodeUpgrade
}

// NodeUpgrade is one entry of getblockchaininfo's upgrades object.
type NodeUpgrade struct {
	Name     string
	BranchID uint32
	Height   uint32
	Status   string
}

// UTXO is one entry of getaddressutxos: a confirmed, unspent output paying the address.
type UTXO struct {
	Address string
	TxID    string // display order, as every RPC writes it
	Index   uint32
	Script  []byte
	Value   uint64 // zatoshi
	Height  uint32
}

// RawTx is getrawtransaction with verbose = 1.
type RawTx struct {
	TxID string
	Raw  []byte
	// Mined is true when the node places the transaction in a block of its best chain; Height and BlockHash are
	// then that block's, and Confirmations is tip − Height + 1. A mempool transaction has none of the three
	// fields; one in a side-chain block has height −1 (zcashd TxToJSON; zebra-rpc TransactionObject.height).
	Mined         bool
	Height        uint32
	Confirmations uint32
	BlockHash     string
	// Verified is true when this package computed the transaction's id from Raw and it matched: every version
	// before 5, every v5 (its Sapling and Orchard bundles included, sapling.go and orchard.go), and v6 without
	// shielded parts. Only a v6 transaction with shielded parts is taken on the node's word — which is why the one
	// thing the companion reads out of a counterparty's transaction, the secret, is checked against the hash it must
	// match rather than trusted, and why a funding the maker credits must be Verified (quorum.go, GAP-039).
	Verified bool
}

// RPCError is an error the node returned, with its code: zcashd's are Bitcoin's (src/rpc/protocol.h), and
// Zebra's LegacyCode maps most of its errors onto the same numbers.
type RPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("zcash %s: rpc error %d: %s", e.Method, e.Code, e.Message)
}

// The codes this package reads.
const (
	RPCInvalidAddressOrKey       = -5  // getrawtransaction: "No such mempool or blockchain transaction"
	RPCTransactionError          = -25 // sendrawtransaction: missing inputs, and other failures that are not a verdict
	RPCTransactionRejected       = -26 // sendrawtransaction: the node's verdict against the transaction
	RPCTransactionAlreadyInChain = -27 // sendrawtransaction: "transaction already in block chain"
)

// ErrTxNotFound is RawTx's answer for a transaction the node has neither mined nor in its mempool.
var ErrTxNotFound = errors.New("zcashtx: the node does not know the transaction")

// errNullResult is a JSON-RPC answer whose result is null: an error for every method but gettxout, where it is
// the node's way of saying the output is spent or was never there.
var errNullResult = errors.New("the answer's result is null")

const (
	// MaxMoney is the Zcash supply cap in zatoshi: no amount a node reports can be above it.
	MaxMoney = 21_000_000 * 100_000_000
	// ReorgLimit is how far back a Zcash node will reorganise: zcashd refuses a new best chain that forked more
	// than 100 blocks below its tip, and Zebra keeps the same consensus limit (research/crosschain/SOURCES.md).
	// A node whose height drops by more than this since it was last read is not the node it was.
	ReorgLimit = 100
	// maxRawTx bounds a transaction the node hands back: Zcash's own limit since Sapling is the 2 MB block.
	maxRawTx = 2_000_000
	// maxTxIDs bounds one getaddresstxids answer. The watcher asks for one block at a time, and a 2 MB block
	// cannot hold this many transactions, so a longer answer is not a real one and is refused rather than read.
	maxTxIDs = 50_000
	// maxUTXOs bounds one getaddressutxos answer. Anyone can pay an address, so the list can be padded; past this
	// the answer is refused. Padding the script's address can then stop the maker from claiming — which costs him
	// time and the padder fees, never anyone's coins, because the taker's own reads never list the address
	// (OutputUnspent asks for one output, FindSpendStep one block at a time).
	maxUTXOs = 100_000
)

// ZcashRPC talks to one zcashd or zebrad. URL is "host:port" or a URL; User and Pass are HTTP basic auth
// (rpcuser/rpcpassword, or the two halves of the node's cookie file, "__cookie__:<secret>").
type ZcashRPC struct {
	URL  string
	User string
	Pass string
	HTTP *http.Client
	// MaxResponse bounds one answer's size; MaxBehind is how far estimatedheight may run ahead of blocks before
	// the node is treated as not synced.
	MaxResponse int64
	MaxBehind   uint32

	mu         sync.Mutex
	lastHeight uint32
	genesis    string
}

// NewZcashRPC returns a client with a 30-second timeout on every request.
func NewZcashRPC(url, user, pass string) *ZcashRPC {
	return &ZcashRPC{URL: url, User: user, Pass: pass, HTTP: &http.Client{Timeout: 30 * time.Second}, MaxResponse: 32 << 20, MaxBehind: 24}
}

// ReadCookie reads a node's cookie file ("user:password", as zcashd and zebrad both write it).
func ReadCookie(path string) (user, pass string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	s := strings.TrimSpace(string(b))
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("%s is not a cookie file (user:password)", path)
	}
	return s[:i], s[i+1:], nil
}

func (c *ZcashRPC) endpoint() string {
	if strings.Contains(c.URL, "://") {
		return c.URL
	}
	return "http://" + c.URL
}

// call posts one JSON-RPC request. It sends "jsonrpc": "1.0", the form lightwalletd's client sends and both nodes
// therefore serve. zcashd answers an RPC error with HTTP 500 and a JSON body, Zebra with 200 and the same body,
// so the body is read whatever the status, except for an authentication failure, which has none.
func (c *ZcashRPC) call(ctx context.Context, method string, params []interface{}, out interface{}) error {
	if params == nil {
		params = []interface{}{}
	}
	body, err := json.Marshal(map[string]interface{}{"jsonrpc": "1.0", "id": "zecswap", "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.User != "" || c.Pass != "" {
		req.SetBasicAuth(c.User, c.Pass)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("zcash %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("zcash %s: the node refused the credentials (HTTP %d)", method, resp.StatusCode)
	}
	max := c.MaxResponse
	if max <= 0 {
		max = 32 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return fmt.Errorf("zcash %s: read: %w", method, err)
	}
	if int64(len(raw)) > max {
		return fmt.Errorf("zcash %s: the answer is larger than %d bytes", method, max)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("zcash %s: HTTP %d, not a JSON-RPC answer: %w", method, resp.StatusCode, err)
	}
	if env.Error != nil {
		return &RPCError{Method: method, Code: env.Error.Code, Message: env.Error.Message}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("zcash %s: HTTP %d with no error in the body", method, resp.StatusCode)
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return fmt.Errorf("zcash %s: %w", method, errNullResult)
	}
	dec := json.NewDecoder(bytes.NewReader(env.Result))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("zcash %s: unexpected result: %w", method, err)
	}
	return nil
}

// ---- shape checks ----

// num reads a JSON number that must be a non-negative integer no larger than max; a fraction, an exponent, a sign
// or a string are all refused rather than rounded.
func num(what string, n json.Number, max uint64) (uint64, error) {
	s := n.String()
	if s == "" || len(s) > 20 {
		return 0, fmt.Errorf("zcashtx: the node's %s %q is not a number", what, s)
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("zcashtx: the node's %s %q is not a non-negative integer", what, s)
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v > max {
		return 0, fmt.Errorf("zcashtx: the node's %s %s is out of range (at most %d)", what, s, max)
	}
	return v, nil
}

func height32(what string, n json.Number) (uint32, error) {
	v, err := num(what, n, LockTimeThreshold-1)
	return uint32(v), err
}

// hash64 checks a 32-byte hash as hex and returns it lower-case.
func hash64(what, s string) (string, error) {
	if len(s) != 64 {
		return "", fmt.Errorf("zcashtx: the node's %s %q is not 64 hex characters", what, s)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("zcashtx: the node's %s %q is not hex", what, s)
	}
	return hex.EncodeToString(b), nil
}

func branch8(what, s string) (uint32, error) {
	if len(s) != 8 {
		return 0, fmt.Errorf("zcashtx: the node's %s %q is not a consensus branch id (8 hex characters)", what, s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("zcashtx: the node's %s %q is not hex", what, s)
	}
	return uint32(v), nil
}

// genesisHash is block 0's hash, asked once per client.
func (c *ZcashRPC) genesisHash(ctx context.Context) (string, error) {
	c.mu.Lock()
	g := c.genesis
	c.mu.Unlock()
	if g != "" {
		return g, nil
	}
	g, err := c.BlockHash(ctx, 0)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.genesis = g
	c.mu.Unlock()
	return g, nil
}

// ---- the seven methods ----

// ChainInfo reads getblockchaininfo and refuses a node it cannot believe (see Sane).
func (c *ZcashRPC) ChainInfo(ctx context.Context) (ChainInfo, error) {
	var r struct {
		Chain         string       `json:"chain"`
		Blocks        json.Number  `json:"blocks"`
		BestBlockHash string       `json:"bestblockhash"`
		Estimated     *json.Number `json:"estimatedheight"`
		IBD           *bool        `json:"initial_block_download_complete"`
		Upgrades      map[string]struct {
			Name             string      `json:"name"`
			ActivationHeight json.Number `json:"activationheight"`
			Status           string      `json:"status"`
		} `json:"upgrades"`
		Consensus *struct {
			ChainTip  string `json:"chaintip"`
			NextBlock string `json:"nextblock"`
		} `json:"consensus"`
	}
	if err := c.call(ctx, "getblockchaininfo", nil, &r); err != nil {
		return ChainInfo{}, err
	}
	var ci ChainInfo
	switch r.Chain {
	case "main":
		ci.Net, ci.Chain = Mainnet, "main"
	case "test", "regtest":
		ci.Net, ci.Chain = Testnet, "test"
		g, err := c.genesisHash(ctx)
		if err != nil {
			return ChainInfo{}, err
		}
		switch {
		case g == RegtestGenesis:
			ci.Chain, ci.Regtest = "regtest", true
		case r.Chain == "regtest":
			return ChainInfo{}, fmt.Errorf("zcashtx: the node calls itself regtest and its genesis block is %s, not regtest's %s", g, RegtestGenesis)
		}
	default:
		return ChainInfo{}, fmt.Errorf("zcashtx: the node reports chain %q, not main, test or regtest", r.Chain)
	}
	var err error
	if ci.Height, err = height32("blocks", r.Blocks); err != nil {
		return ChainInfo{}, err
	}
	if ci.BestHash, err = hash64("bestblockhash", r.BestBlockHash); err != nil {
		return ChainInfo{}, err
	}
	if r.Estimated != nil {
		if ci.Estimated, err = height32("estimatedheight", *r.Estimated); err != nil {
			return ChainInfo{}, err
		}
	}
	ci.IBDComplete = r.IBD
	if r.Consensus == nil {
		return ChainInfo{}, errors.New("zcashtx: the node's getblockchaininfo has no consensus object, so it cannot say which branch id a transaction must be signed under")
	}
	if ci.TipBranch, err = branch8("consensus.chaintip", r.Consensus.ChainTip); err != nil {
		return ChainInfo{}, err
	}
	if ci.NextBranch, err = branch8("consensus.nextblock", r.Consensus.NextBlock); err != nil {
		return ChainInfo{}, err
	}
	for key, u := range r.Upgrades {
		id, err := branch8("upgrade key", key)
		if err != nil {
			return ChainInfo{}, err
		}
		h, err := height32("upgrade activationheight", u.ActivationHeight)
		if err != nil {
			return ChainInfo{}, err
		}
		if len(u.Name) > 64 {
			return ChainInfo{}, errors.New("zcashtx: the node reports an upgrade with an implausible name")
		}
		up := NodeUpgrade{Name: u.Name, BranchID: id, Height: h, Status: u.Status}
		ci.Upgrades = append(ci.Upgrades, up)
		// "disabled" is an upgrade that will not activate on this network; anything else above the next block
		// — whatever its status says — is one a signature made now will not survive.
		if u.Status != "disabled" && h > ci.Height+1 {
			ci.Pending = append(ci.Pending, up)
		}
	}
	sort.Slice(ci.Upgrades, func(i, j int) bool { return ci.Upgrades[i].Height < ci.Upgrades[j].Height })
	if err := c.Sane(ci); err != nil {
		return ChainInfo{}, err
	}
	return ci, nil
}

// Sane is the chain-height check. A node is refused when it reports a height below NU5 on mainnet or testnet
// (it is not synced, or it is not on the chain its name says), when it says its initial download is not
// complete, when its own estimate of the height runs more than MaxBehind blocks ahead of its tip, and when its
// height has dropped by more than ReorgLimit since this client last read it — no reorganisation can do that, so
// it is a different node or a different chain.
func (c *ZcashRPC) Sane(ci ChainInfo) error {
	if !ci.Regtest && ci.Height < Upgrades[0].height(ci.Net) {
		return fmt.Errorf("zcashtx: the node is at %s height %d, below NU5 (%d): it is not synced, or not on %s", ci.Net, ci.Height, Upgrades[0].height(ci.Net), ci.Net)
	}
	if ci.IBDComplete != nil && !*ci.IBDComplete {
		return fmt.Errorf("zcashtx: the node is still in its initial block download (height %d)", ci.Height)
	}
	if ci.Estimated > ci.Height && ci.Estimated-ci.Height > c.MaxBehind {
		return fmt.Errorf("zcashtx: the node is at height %d and estimates the chain at %d: it is %d blocks behind, and a swap cannot be judged from a stale tip", ci.Height, ci.Estimated, ci.Estimated-ci.Height)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastHeight > ReorgLimit && ci.Height < c.lastHeight-ReorgLimit {
		return fmt.Errorf("zcashtx: the node's height fell from %d to %d, further than any Zcash reorganisation (%d blocks): it is not the node it was", c.lastHeight, ci.Height, ReorgLimit)
	}
	if ci.Height > c.lastHeight {
		c.lastHeight = ci.Height
	}
	return nil
}

// BlockHash is getblockhash: the hash of the best chain's block at height.
func (c *ZcashRPC) BlockHash(ctx context.Context, height uint32) (string, error) {
	var s string
	if err := c.call(ctx, "getblockhash", []interface{}{height}, &s); err != nil {
		return "", err
	}
	return hash64("block hash", s)
}

// AddressUTXOs is getaddressutxos for one address: its confirmed unspent outputs.
func (c *ZcashRPC) AddressUTXOs(ctx context.Context, addr string) ([]UTXO, error) {
	var r []struct {
		Address     string      `json:"address"`
		TxID        string      `json:"txid"`
		OutputIndex json.Number `json:"outputIndex"`
		Script      string      `json:"script"`
		Satoshis    json.Number `json:"satoshis"`
		Height      json.Number `json:"height"`
	}
	if err := c.call(ctx, "getaddressutxos", []interface{}{map[string]interface{}{"addresses": []string{addr}}}, &r); err != nil {
		return nil, err
	}
	if len(r) > maxUTXOs {
		return nil, fmt.Errorf("zcashtx: getaddressutxos answered %d outputs for %s; more than %d is refused rather than read", len(r), addr, maxUTXOs)
	}
	out := make([]UTXO, 0, len(r))
	for _, u := range r {
		if u.Address != addr {
			return nil, fmt.Errorf("zcashtx: getaddressutxos for %s answered an output of %q", addr, u.Address)
		}
		txid, err := hash64("utxo txid", u.TxID)
		if err != nil {
			return nil, err
		}
		idx, err := num("utxo outputIndex", u.OutputIndex, 1<<20)
		if err != nil {
			return nil, err
		}
		script, err := hex.DecodeString(u.Script)
		if err != nil || len(script) == 0 || len(script) > 10_000 {
			return nil, fmt.Errorf("zcashtx: getaddressutxos answered a script that is not hex: %q", u.Script)
		}
		val, err := num("utxo satoshis", u.Satoshis, MaxMoney)
		if err != nil {
			return nil, err
		}
		h, err := height32("utxo height", u.Height)
		if err != nil {
			return nil, err
		}
		if h == 0 {
			return nil, errors.New("zcashtx: getaddressutxos answered an output at height 0")
		}
		out = append(out, UTXO{Address: u.Address, TxID: txid, Index: uint32(idx), Script: script, Value: val, Height: h})
	}
	return out, nil
}

// AddressTxIDs is getaddresstxids for one address between two heights, inclusive. Both ends are always given:
// zcashd reads them only as a pair, and Zebra clamps rather than refuses.
func (c *ZcashRPC) AddressTxIDs(ctx context.Context, addr string, start, end uint32) ([]string, error) {
	if end < start {
		return nil, fmt.Errorf("zcashtx: getaddresstxids from %d to %d", start, end)
	}
	var r []string
	params := map[string]interface{}{"addresses": []string{addr}, "start": start, "end": end}
	if err := c.call(ctx, "getaddresstxids", []interface{}{params}, &r); err != nil {
		return nil, err
	}
	if len(r) > maxTxIDs {
		return nil, fmt.Errorf("zcashtx: getaddresstxids answered %d transactions for %s in %d blocks; more than %d is refused rather than read", len(r), addr, end-start+1, maxTxIDs)
	}
	out := make([]string, 0, len(r))
	for _, id := range r {
		h, err := hash64("txid", id)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// RawTx is getrawtransaction with verbose = 1. A transaction the node does not know is ErrTxNotFound.
func (c *ZcashRPC) RawTx(ctx context.Context, txid string) (RawTx, error) {
	want, err := hash64("requested txid", txid)
	if err != nil {
		return RawTx{}, err
	}
	var r struct {
		Hex           string       `json:"hex"`
		TxID          string       `json:"txid"`
		Height        *json.Number `json:"height"`
		Confirmations *json.Number `json:"confirmations"`
		BlockHash     string       `json:"blockhash"`
	}
	if err := c.call(ctx, "getrawtransaction", []interface{}{want, 1}, &r); err != nil {
		var re *RPCError
		if errors.As(err, &re) && re.Code == RPCInvalidAddressOrKey {
			return RawTx{}, fmt.Errorf("%w: %s (%v)", ErrTxNotFound, want, err)
		}
		return RawTx{}, err
	}
	raw, err := hex.DecodeString(r.Hex)
	if err != nil || len(raw) == 0 || len(raw) > maxRawTx {
		return RawTx{}, fmt.Errorf("zcashtx: getrawtransaction %s answered no usable hex", want)
	}
	if r.TxID != "" && !strings.EqualFold(r.TxID, want) {
		return RawTx{}, fmt.Errorf("zcashtx: asked for transaction %s, the node answered %s", want, r.TxID)
	}
	rt := RawTx{TxID: want, Raw: raw}
	if id, ok := ComputeTxID(raw); ok {
		if id != want {
			return RawTx{}, fmt.Errorf("zcashtx: asked for transaction %s, the node's bytes hash to %s", want, id)
		}
		rt.Verified = true
	}
	if r.Height != nil {
		hs := r.Height.String()
		switch {
		case hs == "-1":
			// in a block the node knows but not on its best chain: not mined, as far as this swap is concerned
		default:
			h, err := height32("transaction height", *r.Height)
			if err != nil {
				return RawTx{}, err
			}
			if h > 0 {
				if rt.BlockHash, err = hash64("transaction blockhash", r.BlockHash); err != nil {
					return RawTx{}, err
				}
				if r.Confirmations == nil {
					return RawTx{}, errors.New("zcashtx: getrawtransaction gave a height and no confirmations")
				}
				conf, err := height32("transaction confirmations", *r.Confirmations)
				if err != nil || conf == 0 {
					return RawTx{}, fmt.Errorf("zcashtx: getrawtransaction gave a mined transaction %d confirmations", conf)
				}
				rt.Mined, rt.Height, rt.Confirmations = true, h, conf
			}
		}
	}
	return rt, nil
}

// SendRawTx is sendrawtransaction. It returns the node's txid; Broadcast is what callers use.
func (c *ZcashRPC) SendRawTx(ctx context.Context, raw []byte) (string, error) {
	var s string
	if err := c.call(ctx, "sendrawtransaction", []interface{}{hex.EncodeToString(raw)}, &s); err != nil {
		return "", err
	}
	return hash64("sendrawtransaction txid", s)
}

// TxOut is gettxout for one output against the best chain only (include_mempool false): nil when the chain has
// spent it or never had it. zcashd answers with no flag; zebrad's RpcServer has get_tx_out with the same
// parameters. The fields read are scriptPubKey.hex, confirmations and bestblock.
func (c *ZcashRPC) TxOut(ctx context.Context, txid string, index uint32) (*TxOutInfo, error) {
	want, err := hash64("requested txid", txid)
	if err != nil {
		return nil, err
	}
	var r struct {
		BestBlock     string      `json:"bestblock"`
		Confirmations json.Number `json:"confirmations"`
		ScriptPubKey  struct {
			Hex string `json:"hex"`
		} `json:"scriptPubKey"`
	}
	if err := c.call(ctx, "gettxout", []interface{}{want, index, false}, &r); err != nil {
		if errors.Is(err, errNullResult) {
			return nil, nil
		}
		return nil, err
	}
	best, err := hash64("gettxout bestblock", r.BestBlock)
	if err != nil {
		return nil, err
	}
	conf, err := height32("gettxout confirmations", r.Confirmations)
	if err != nil {
		return nil, err
	}
	script, err := hex.DecodeString(r.ScriptPubKey.Hex)
	if err != nil || len(script) == 0 || len(script) > 10_000 {
		return nil, fmt.Errorf("zcashtx: gettxout answered a script that is not hex: %q", r.ScriptPubKey.Hex)
	}
	return &TxOutInfo{Script: script, Confirmations: conf, BestBlock: best}, nil
}

// ---- transaction ids ----

// ComputeTxID is the id of a serialized transaction in display order, when this package can compute it: SHA256d
// for every version before 5, ZIP 244 for every v5 transaction (Sapling and Orchard bundles included), and ZIP 229's
// variant for a v6 transaction without shielded parts. ok is false otherwise: a v6 transaction with shielded parts.
func ComputeTxID(raw []byte) (string, bool) {
	if len(raw) < 4 {
		return "", false
	}
	header := uint32(raw[0]) | uint32(raw[1])<<8 | uint32(raw[2])<<16 | uint32(raw[3])<<24
	if header&0x7fffffff < 5 {
		a := sha256.Sum256(raw)
		b := sha256.Sum256(a[:])
		return DisplayHex(b), true
	}
	tx, err := ParseTx(raw)
	if err != nil {
		return "", false
	}
	return DisplayHex(tx.TxID()), true
}

// DisplayHex writes a transaction id the way every RPC and explorer does: the serialized bytes reversed.
func DisplayHex(id [32]byte) string {
	var r [32]byte
	for i := range id {
		r[i] = id[31-i]
	}
	return hex.EncodeToString(r[:])
}

// ParseDisplayHex reads a display-order id back into the serialized order an OutPoint carries.
func ParseDisplayHex(s string) ([32]byte, error) {
	var out [32]byte
	h, err := hash64("txid", s)
	if err != nil {
		return out, err
	}
	b, _ := hex.DecodeString(h)
	for i := range out {
		out[i] = b[31-i]
	}
	return out, nil
}

// ---- reading any transaction, and sending one (from the swap watcher, unchanged) ----

// TransparentParts reads the transparent inputs and outputs of a serialized transaction of any version Zcash has
// had, 1 to 5, and nothing after them. A maker's claim is his own to build — his wallet may make it v4, or add
// Sapling or Orchard change — and the taker must read the secret out of it whatever it is; the transparent parts
// sit in the same place relative to the header in every version (ZIP 225 for v5; the protocol specification §7.1
// for the rest).
func TransparentParts(raw []byte) ([]TxIn, []TxOut, error) {
	r := &reader{b: raw}
	header := r.u32()
	if r.err != nil {
		return nil, nil, r.err
	}
	overwintered := header&0x80000000 != 0
	version := header & 0x7fffffff
	switch {
	case version == 0 || version > 6:
		return nil, nil, fmt.Errorf("zcashtx: transaction version %d, which this build cannot read", version)
	case overwintered != (version >= 3):
		return nil, nil, fmt.Errorf("zcashtx: transaction version %d with fOverwintered %v", version, overwintered)
	}
	if overwintered {
		r.u32() // nVersionGroupId
	}
	if version >= 5 {
		r.bytes(12) // nConsensusBranchId, nLockTime, nExpiryHeight: v5 (ZIP 225) and v6 (ZIP 229) alike
	}
	nIn := r.compact()
	if r.err != nil {
		return nil, nil, r.err
	}
	if nIn > uint64(len(raw)-r.p)/41 {
		return nil, nil, errors.New("zcashtx: more inputs than the transaction has bytes for")
	}
	ins := make([]TxIn, 0, nIn)
	for i := uint64(0); i < nIn && r.err == nil; i++ {
		var in TxIn
		copy(in.PrevOut.Hash[:], r.bytes(32))
		in.PrevOut.Index = r.u32()
		in.ScriptSig = append([]byte{}, r.varBytes()...)
		in.Sequence = r.u32()
		ins = append(ins, in)
	}
	nOut := r.compact()
	if r.err != nil {
		return nil, nil, r.err
	}
	if nOut > uint64(len(raw)-r.p)/9 {
		return nil, nil, errors.New("zcashtx: more outputs than the transaction has bytes for")
	}
	outs := make([]TxOut, 0, nOut)
	for i := uint64(0); i < nOut && r.err == nil; i++ {
		var out TxOut
		out.Value = r.u64()
		out.ScriptPubKey = append([]byte{}, r.varBytes()...)
		outs = append(outs, out)
	}
	if r.err != nil {
		return nil, nil, r.err
	}
	return ins, outs, nil
}

// Broadcast sends a transaction this package built and answers with its id. It is idempotent: a transaction the
// node already holds — in its mempool or in a block — is a success, whatever words the node chose to say so, so a
// restart that sends the same bytes again is harmless. The node's txid must be the one computed here.
func Broadcast(ctx context.Context, zc Zcash, raw []byte) (string, error) {
	id, ok := ComputeTxID(raw)
	if !ok {
		return "", errors.New("zcashtx: Broadcast sends only transactions whose id this package can compute")
	}
	got, err := zc.SendRawTx(ctx, raw)
	if err == nil {
		if got != id {
			return "", fmt.Errorf("zcashtx: the node calls the transaction %s; its bytes hash to %s", got, id)
		}
		return id, nil
	}
	var re *RPCError
	if errors.As(err, &re) && re.Code == RPCTransactionAlreadyInChain {
		return id, nil
	}
	if _, kerr := zc.RawTx(ctx, id); kerr == nil {
		return id, nil
	}
	return id, err
}

// TxKnown asks the node about a transaction by id: known is false for one it has never heard of or has dropped.
func TxKnown(ctx context.Context, zc Zcash, txid string) (RawTx, bool, error) {
	rt, err := zc.RawTx(ctx, txid)
	if errors.Is(err, ErrTxNotFound) {
		return RawTx{}, false, nil
	}
	if err != nil {
		return RawTx{}, false, err
	}
	return rt, true, nil
}

// ScriptPushes reads a push-only script (every P2SH scriptSig is one: SIGPUSHONLY is consensus for P2SH) as its
// stack items. OP_0 is the empty item, OP_1NEGATE and OP_1..OP_16 the one-byte numbers they push.
func ScriptPushes(s []byte) ([][]byte, error) {
	var out [][]byte
	for i := 0; i < len(s); {
		op := s[i]
		i++
		var n uint64
		switch {
		case op == 0x00:
			out = append(out, []byte{})
			continue
		case op <= 0x4b:
			n = uint64(op)
		case op == 0x4c:
			if i+1 > len(s) {
				return nil, errors.New("zcashtx: truncated PUSHDATA1")
			}
			n = uint64(s[i])
			i++
		case op == 0x4d:
			if i+2 > len(s) {
				return nil, errors.New("zcashtx: truncated PUSHDATA2")
			}
			n = uint64(binary.LittleEndian.Uint16(s[i:]))
			i += 2
		case op == 0x4e:
			if i+4 > len(s) {
				return nil, errors.New("zcashtx: truncated PUSHDATA4")
			}
			n = uint64(binary.LittleEndian.Uint32(s[i:]))
			i += 4
		case op == 0x4f:
			out = append(out, []byte{0x81})
			continue
		case op >= 0x51 && op <= 0x60:
			out = append(out, []byte{op - 0x50})
			continue
		default:
			return nil, fmt.Errorf("zcashtx: opcode %02x in a scriptSig that must be push-only", op)
		}
		if n > uint64(len(s)-i) {
			return nil, errors.New("zcashtx: a push runs past the end of the script")
		}
		out = append(out, s[i:i+int(n)])
		i += int(n)
	}
	return out, nil
}

// IsLoopback says whether an endpoint ("host:port", a URL, or a bare host) names this machine (from tools/zecswap/quorum.go at c58e3e3).
func IsLoopback(endpoint string) bool {
	host := endpoint
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return false
		}
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
