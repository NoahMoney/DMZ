// ringprobe measures what a DERO contract sees when it is called with a ring larger than 2. derohe extracts the
// signer only from a ring-size-2 DERO payload (blockchain/transaction_execute.go, Extract_signer) and explicitly
// allows anonymous contract calls otherwise ("allow anonymous SC transactions with condition that SC will not call
// Signer"). If DEROVALUE() and a destination argument still work at ring 16, a swap entrypoint that pays a named
// address instead of SIGNER() hides the payer among the ring.
//
//	go run ./ringprobe [-rings 2,16,32]
//
// Wallets: owner 30007 installs; payer 30001 calls; destination 30006 receives.
package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	rpc "veil/tools/simrpc"
)

const probe = `Function Initialize() Uint64
10 STORE("n", 0)
20 RETURN 0
End Function

Function Pay(dest String) Uint64
10 DIM s as String
20 LET s = SIGNER()
30 STORE("n", LOAD("n") + 1)
31 STORE("valid", IS_ADDRESS_VALID(s))
32 STORE("slen", STRLEN(s))
33 STORE("v", DEROVALUE())
40 SEND_DERO_TO_ADDRESS(ADDRESS_RAW(dest), DEROVALUE())
50 RETURN 0
End Function
`

var (
	daemon = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	rings  = flag.String("rings", "2,16,32", "ring sizes to try, in order")
	amount = flag.Uint64("amount", 12345, "DERO units sent through the contract on each call")
)

const (
	owner = "127.0.0.1:30007"
	payer = "127.0.0.1:30001"
	dest  = "127.0.0.1:30006"
)

func main() {
	flag.Parse()
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	scid, gas, h := c.Send(owner, rpc.Address(owner), probe, "", nil, nil)
	fmt.Printf("probe installed %s at height %d (storage gas %d)\n", scid, h, gas.GasStorage)
	to := rpc.Address(dest)
	for _, rs := range strings.Split(*rings, ",") {
		ring, err := strconv.Atoi(strings.TrimSpace(rs))
		rpc.Must(err)
		c.RingSize = ring
		before := rpc.Balance(dest, "")
		start := time.Now()
		txid, gas, h := c.Send(payer, rpc.Address(payer), "", scid, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: *amount}},
			[]rpc.Arg{rpc.S("entrypoint", "Pay"), rpc.S("dest", to)}, dest)
		st := c.GetSC(scid, false)
		after := rpc.Balance(dest, "")
		// size and signer extraction as the node reports them
		var tr struct {
			TxsAsHex []string `json:"txs_as_hex"`
			Txs      []struct {
				Signer string `json:"signer"`
			} `json:"txs"`
		}
		rpc.Must(rpc.Call(*daemon, "DERO.GetTransaction", map[string]interface{}{"txs_hashes": []string{txid}}, &tr))
		size, signer := 0, ""
		if len(tr.TxsAsHex) == 1 {
			size = len(tr.TxsAsHex[0]) / 2
		}
		if len(tr.Txs) == 1 {
			signer = tr.Txs[0].Signer
		}
		fmt.Printf("ring %2d: tx %s… height %d in %s; size %d bytes; node-extracted signer %q; contract saw DEROVALUE %d, SIGNER valid=%d len=%d; destination received %d (gas compute %d storage %d)\n",
			ring, txid[:8], h, time.Since(start).Round(time.Second), size, signer, st.U("v"), st.U("valid"), st.U("slen"), after-before, gas.GasCompute, gas.GasStorage)
	}
}
