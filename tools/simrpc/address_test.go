package simrpc

import "testing"

// The DVM always writes the mainnet spelling, so a testnet address must convert to the same string the contract
// stores. Vectors are the simulator wallets used by tools/simvault, taken from a live vault's vote keys.
func TestDVMAddress(t *testing.T) {
	cases := []struct{ in, want string }{
		{"deto1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgmat5s8", "dero1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgpyhv40"},
		{"deto1qyzj2ldw7cjpprhf84ljmxdw54w72jekfkwt0nqsyyzgatttg37d2qgefmama", "dero1qyzj2ldw7cjpprhf84ljmxdw54w72jekfkwt0nqsyyzgatttg37d2qgrs8974"},
	}
	for _, c := range cases {
		if got := DVMAddress(c.in); got != c.want {
			t.Fatalf("DVMAddress(%s)\n got %s\nwant %s", c.in, got, c.want)
		}
		// already in the DVM spelling: unchanged
		if got := DVMAddress(c.want); got != c.want {
			t.Fatalf("DVMAddress is not idempotent: %s -> %s", c.want, got)
		}
	}
	for _, bad := range []string{"", "nonsense", "dero1", "deto1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgmat5s9"} {
		if got := DVMAddress(bad); got != "" {
			t.Fatalf("DVMAddress(%q) = %q, want an empty string", bad, got)
		}
	}
}
