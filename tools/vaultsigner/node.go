package main

import (
	"fmt"
	"time"

	rpc "veil/tools/simrpc"
)

// A signer must survive its node being briefly unreachable. simrpc's original helpers end the process on any RPC
// error, which is right for a one-shot deploy tool and wrong for a daemon: a restart is not available on every
// signer host, and a vault loses liveness with every signer that retires itself. signerNode is the daemon's view of
// the same client: reads retry a few times with backoff and then return an error the pass logs, and sends return
// their error so the next pass can decide whether repeating them is safe.
type signerNode struct{ c *rpc.Client }

var (
	// Variables rather than constants so a test can shorten the backoff.
	nodeRetryTries = 4
	nodeRetryBase  = 250 * time.Millisecond
)

func retryRead(what string, fn func() error) error {
	var err error
	for i := 0; i < nodeRetryTries; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i+1 < nodeRetryTries {
			time.Sleep(time.Duration(i+1) * nodeRetryBase)
		}
	}
	return fmt.Errorf("%s after %d attempts: %w", what, nodeRetryTries, err)
}

// awaitNode is for startup: a node that is down while the signer boots is given a few minutes to come back instead
// of the process exiting and waiting for a supervisor that may not exist. A configuration error is retried too and
// then reported; the operator sees it and fixes the flag, rather than the daemon disappearing without a trace.
func awaitNode(what string, fn func() error) error {
	deadline := time.Now().Add(5 * time.Minute)
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: %w", what, err)
		}
		if attempt == 1 || attempt%12 == 0 {
			fmt.Printf("%s is not ready (%v); retrying while the signer stays up\n", what, err)
		}
		time.Sleep(5 * time.Second)
	}
}

func (n *signerNode) getSC(scid string) (rpc.SCState, error) {
	var st rpc.SCState
	err := retryRead("vault read", func() (e error) { st, e = n.c.GetSCE(scid, false); return e })
	return st, err
}

func (n *signerNode) height() (int64, error) {
	var h int64
	err := retryRead("daemon height", func() (e error) { h, e = n.c.HeightE(); return e })
	return h, err
}

func (n *signerNode) stableHeight() (int64, error) {
	var h int64
	err := retryRead("daemon stable height", func() (e error) { h, e = n.c.StableHeightE(); return e })
	return h, err
}

// estimate is a read of the daemon's simulator; retrying it is safe, and a refusal is a normal answer.
func (n *signerNode) estimate(signer, code, scid string, xfers []rpc.Transfer, scRPC []rpc.Arg) (rpc.Gas, error) {
	var g rpc.Gas
	err := retryRead("gas estimate", func() (e error) { g, e = n.c.Estimate(signer, code, scid, xfers, scRPC); return e })
	return g, err
}

// sendWithFee submits a call once. A wallet transfer is not retried here: a lost reply may mean the transaction
// was accepted, and only the caller knows whether repeating it is safe (votes and marks are idempotent; a foreign
// release is looked up before it is paid). The next pass does that deciding.
func (n *signerNode) sendWithFee(signer, scid string, xfers []rpc.Transfer, scRPC []rpc.Arg, fee uint64) (string, int64, error) {
	return n.c.SendWithFeeE(*wallet, signer, scid, xfers, scRPC, fee)
}

func (n *signerNode) info() (testnet bool, network string, err error) {
	err = retryRead("daemon info", func() (e error) { testnet, network, e = n.c.InfoE(); return e })
	return testnet, network, err
}

func (n *signerNode) address() (string, error) {
	var a string
	err := retryRead("wallet address", func() (e error) { a, e = rpc.AddressE(*wallet); return e })
	return a, err
}
