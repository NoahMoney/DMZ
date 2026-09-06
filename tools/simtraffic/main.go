// simtraffic: keeps the simulator "alive" for demos and UI work — random buys/sells (some published) on the
// launches registered on the factory, from wallets 16..21, for -duration.
//
//	go run ./simtraffic -factory <scid> [-duration 40m] [-min 8s -max 20s]
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"regexp"
	"time"

	rpc "veil/tools/simrpc"
)

func main() {
	daemon := flag.String("daemon", "127.0.0.1:20000", "daemon")
	factory := flag.String("factory", "", "factory scid (default: read from ../app/config.js)")
	dur := flag.Duration("duration", 40*time.Minute, "how long to run")
	minGap := flag.Duration("min", 8*time.Second, "min pause between trades")
	maxGap := flag.Duration("max", 20*time.Second, "max pause between trades")
	first := flag.Int("first", 16, "first wallet index")
	last := flag.Int("last", 21, "last wallet index")
	flag.Parse()
	log.SetFlags(0)
	if *factory == "" {
		b, err := os.ReadFile("../app/config.js")
		rpc.Must(err)
		m := regexp.MustCompile(`factory:\s*"([0-9a-f]{64})"`).FindStringSubmatch(string(b))
		if m == nil {
			log.Fatal("no factory in config.js")
		}
		*factory = m[1]
	}
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	w := func(i int) string { return fmt.Sprintf("127.0.0.1:%d", 30000+i) }
	addr := map[int]string{}
	for i := *first; i <= *last; i++ {
		addr[i] = rpc.Address(w(i))
	}
	launches := func() []string {
		st := c.GetSC(*factory, false)
		n := st.U("lc")
		var out []string
		for i := uint64(0); i < n; i++ {
			if st.Has(fmt.Sprintf("launch_%d", i)) {
				out = append(out, st.Str(fmt.Sprintf("launch_%d", i)))
			}
		}
		return out
	}
	deadline := time.Now().Add(*dur)
	n := 0
	for time.Now().Before(deadline) {
		ls := launches()
		if len(ls) == 0 {
			log.Println("no launches yet; waiting")
			time.Sleep(20 * time.Second)
			continue
		}
		scid := ls[r.Intn(len(ls))]
		wi := *first + r.Intn(*last-*first+1)
		publish := uint64(0)
		if r.Intn(10) < 6 {
			publish = 1
		}
		func() {
			defer func() {
				if e := recover(); e != nil {
					log.Println("skip:", e)
				}
			}()
			hold := rpc.Balance(w(wi), scid)
			if hold > 0 && r.Intn(10) < 4 {
				a := hold / uint64(2+r.Intn(6))
				if a == 0 {
					return
				}
				txid, _ := c.SendWithFee(w(wi), addr[wi], scid, []rpc.Transfer{{SCID: scid, Burn: a}}, []rpc.Arg{rpc.S("entrypoint", "SwapAssetForDero"), rpc.U("min_out", 0), rpc.U("publish", publish)}, 1500)
				n++
				log.Printf("#%d w%d SELL %d units of %s… publish=%d tx %s…", n, wi, a, scid[:8], publish, txid[:8])
				return
			}
			d := uint64(2000 + r.Intn(20000)) // 0.02 – 0.22 DERO
			txid, _ := c.SendWithFee(w(wi), addr[wi], scid, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", 0), rpc.U("publish", publish)}, 1500)
			n++
			log.Printf("#%d w%d BUY %d units DERO of %s… publish=%d tx %s…", n, wi, d, scid[:8], publish, txid[:8])
		}()
		time.Sleep(*minGap + time.Duration(r.Int63n(int64(*maxGap-*minGap))))
	}
	log.Printf("done: %d trades", n)
}
