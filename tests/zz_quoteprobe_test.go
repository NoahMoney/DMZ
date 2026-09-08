package tests

import (
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// Probe A: does SEND_ASSET_TO_ADDRESS(addr, amt, DERO()) move real DERO,
// and does ASSETVALUE(DERO()) see an incoming DERO burn?
const probeSrc = `
Function InitializePrivate() Uint64
10 STORE("initd", 1)
100 RETURN 0
End Function

Function Echo() Uint64
10 DIM s, q as String
11 DIM v as Uint64
20 LET s = SIGNER()
21 LET q = DERO()
22 LET v = ASSETVALUE(q)
23 IF v == 0 THEN GOTO 900
24 STORE("seen", v)
25 SEND_ASSET_TO_ADDRESS(s, v / 2, q)
100 RETURN 0
900 RETURN 1
End Function
`

func TestProbeDeroAsAsset(t *testing.T) {
	c := harness.New()
	alice := c.NewAccount()
	scid, res := c.Install(probeSrc, nil, rpc.Arguments{}, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	var z crypto.Hash
	r := c.Call(scid, "Echo", rpc.Arguments{}, map[crypto.Hash]uint64{z: 1000}, alice)
	if !r.OK() {
		t.Fatalf("Echo: %v", r.Err)
	}
	t.Logf("ASSETVALUE(DERO()) saw %d; SEND_ASSET_TO_ADDRESS(...,DERO()) sent %d DERO to alice; compute %d storage %d",
		c.U(scid, "seen"), r.Sent(z, alice), r.GasCompute, r.GasStorage)
	if c.U(scid, "seen") != 1000 || r.Sent(z, alice) != 500 {
		t.Fatalf("DERO is NOT addressable as an asset")
	}
}

// Probe B: worst-case install gas for launch.bas with maximum-length metadata.
func TestProbeLaunchMaxMetadataInstallGas(t *testing.T) {
	code := readContract(t, "launch.bas")
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	args := rpc.Arguments{
		harness.S("name", strings.Repeat("N", 32)),
		harness.S("ticker", strings.Repeat("T", 10)),
		harness.S("descr", strings.Repeat("D", 240)),
		harness.S("icon", strings.Repeat("I", 200)),
		harness.S("tags", strings.Repeat("G", 100)),
		harness.H("quote", zeroHash),
		harness.U("dec", 5), harness.U("supply", 3689348814741910324), harness.U("grad", 1000000000000),
		harness.U("cbps", 500), harness.U("tax", 1000), harness.U("tmode", 3),
		harness.S("treasury", treasury.String()), harness.U("fee", 100), harness.U("pfee", 10000),
	}
	_, res := c.Install(code, nil, args, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("launch.bas MAX-METADATA install: code %d bytes, storage gas %d of %d, headroom %d, compute %d",
		len(code), res.GasStorage, harness.MaxStorageGas, harness.MaxStorageGas-res.GasStorage, res.GasCompute)
}
