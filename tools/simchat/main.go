// simchat posts to and reads a chat.bas contract on the simulator, so a room can be exercised without a browser.
//
//	go run ./simchat -chat <scid> -post "#dmz:hello"        # post as the user wallet
//	go run ./simchat -chat <scid> -post "#dmz:hi" -w 30000  # post as another wallet
//	go run ./simchat -chat <scid> -read "#dmz"              # print a room the way the app reads it
package main

import (
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	rpc "veil/tools/simrpc"
)

var (
	daemon = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	wallet = flag.String("w", "127.0.0.1:30006", "wallet rpc to post from")
	chat   = flag.String("chat", "", "chat.bas scid")
	post   = flag.String("post", "", "room:text")
	read   = flag.String("read", "", "room to print")
	show   = flag.Int("n", 40, "how many messages to print")
)

func main() {
	flag.Parse()
	if len(*chat) != 64 {
		log.Fatal("-chat <scid> required")
	}
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	st := c.GetSC(*chat, false)

	switch {
	case *post != "":
		parts := strings.SplitN(*post, ":", 2)
		if len(parts) != 2 {
			log.Fatal(`-post "<room>:<text>"`)
		}
		room, text := parts[0], parts[1]
		fee := st.U("fee")
		me := rpc.Address(*wallet)
		// the fee is paid as a DERO burn, which is what DEROVALUE() reads; a burn needs a destination the daemon
		// will accept, and any registered account does
		var xf []rpc.Transfer
		if fee > 0 {
			xf = []rpc.Transfer{{Destination: c.RandomAddress(), Burn: fee}}
		}
		txid, gas, h := c.Send(*wallet, me, "", *chat, xf, []rpc.Arg{
			rpc.S("entrypoint", "Post"), rpc.S("room", room), rpc.S("text", text)})
		fmt.Printf("posted to %s at height %d, tx %s, compute gas %d\n", room, h, txid[:12], gas.GasCompute)

	case *read != "":
		room := *read
		after := c.GetSC(*chat, false)
		n := uint64(0)
		if after.Has("rn_" + room) {
			n = after.U("rn_" + room)
		}
		fmt.Printf("%s — %d message(s), %d in the contract overall\n", room, n, after.U("n"))
		from := uint64(0)
		if n > uint64(*show) {
			from = n - uint64(*show)
		}
		for j := from; j < n; j++ {
			raw := after.Str("rm_" + room + "_" + strconv.FormatUint(j, 10))
			// author|height|text: split on the first two separators only, because the text may contain them
			p := strings.SplitN(raw, "|", 3)
			if len(p) != 3 {
				fmt.Printf("%4d  (unreadable record)\n", j)
				continue
			}
			fmt.Printf("%6s  %-18s %s\n", p[1], p[0][:16]+"…", p[2])
		}

	default:
		fmt.Printf("chat %s: v%d, fee %d, maxlen %d, %d messages\n",
			(*chat)[:12], st.U("v"), st.U("fee"), st.U("maxlen"), st.U("n"))
		flag.Usage()
	}
}
