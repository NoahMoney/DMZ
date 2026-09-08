package tests

import (
	"sort"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/dvm"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// Every shipped contract must parse with derohe's parser, expose exactly the documented entrypoints, and survive
// the store/load round trip of an install (the code is read back and re-parsed on every call).
func TestContractsParse(t *testing.T) {
	want := map[string][]string{
		"pool.bas":     {"InitializePrivate", "AddLiquidity", "RemoveLiquidity", "SwapDeroForAsset", "SwapDeroForAssetTo", "SwapAssetForDero", "SwapAssetForDeroTo", "ClaimFees"},
		"launch.bas":   {"InitializePrivate", "SwapDeroForAsset", "SwapDeroForAssetTo", "SwapAssetForDero", "SwapAssetForDeroTo", "ClaimFees", "ClaimTax", "Buyback", "Distribute", "Stake", "Unstake", "Claim"},
		"factory.bas":  {"Initialize", "SetTreasury", "SetRegFee", "TransferOwnership", "ClaimOwnership", "RegisterPool", "RegisterLaunch", "RegisterNFT"},
		"social.bas":   {"Initialize", "Profile", "Follow", "Unfollow"},
		"nft.bas":      {"InitializePrivate", "Lock", "Mint", "Unlock", "ClaimCreator", "ClaimFees"},
		"nfa.bas":      {"InitializePrivate", "ClaimOwnership", "Update", "Start", "BuyItNow", "Bid", "CloseListing", "CancelListing"},
		"founders.bas": {"InitializePrivate", "Mint", "Register", "Withdraw", "Claim", "Deposit", "Distribute", "PayBatch", "List", "Unlist", "Buy"},
		"swap.bas":     {"Initialize", "Lock", "Claim", "Refund"},
		"htlc.bas":     {"Initialize", "Lock", "Claim", "Refund", "Punish"},
		"board.bas":    {"Initialize", "Post", "Accept", "Cancel"},
		"vault.bas":    {"InitializePrivate", "SetLimits", "Tighten", "SetViewKey", "AddSigner", "AddSigner2", "Bond", "Unbond", "Seal", "Register", "Attest", "Mint", "Refunded", "Burn", "Released", "Paying", "Reclaim", "Veto", "ClaimFees", "Halt", "SetCap", "Slash"},
	}
	for name, entries := range want {
		code := readContract(t, name)
		sc, pos, err := dvm.ParseSmartContract(code)
		if err != nil {
			t.Fatalf("%s: parse error at %s: %v", name, pos, err)
		}
		var got []string
		for fn := range sc.Functions {
			if fn[0] >= 'A' && fn[0] <= 'Z' {
				got = append(got, fn)
			}
		}
		sort.Strings(got)
		w := append([]string{}, entries...)
		sort.Strings(w)
		if len(got) != len(w) {
			t.Fatalf("%s: entrypoints %v want %v", name, got, w)
		}
		for i := range w {
			if got[i] != w[i] {
				t.Fatalf("%s: entrypoints %v want %v", name, got, w)
			}
		}
		// round trip through the store exactly as the node does it
		v := dvm.Variable{Type: dvm.String, ValueString: code}
		b, err := v.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		var back dvm.Variable
		if err := back.UnmarshalBinary(b); err != nil {
			t.Fatal(err)
		}
		if back.ValueString != code {
			t.Fatalf("%s: code changed in serialisation (%d vs %d bytes)", name, len(back.ValueString), len(code))
		}
		t.Logf("%s: %d bytes, %d functions (%d entrypoints)", name, len(code), len(sc.Functions), len(got))
		// Reserved storage keys: "C" holds the contract code (dvm.SC_Code_Key); raw 32-byte keys are asset balances
		// and raw 33-byte keys are account balances of the contract's own asset. A contract must never STORE to them.
		if strings.Contains(code, `STORE("C"`) || strings.Contains(code, `DELETE("C"`) {
			t.Fatalf("%s writes the reserved code key C", name)
		}
	}
}

// The installed code must be readable and identical after install (graviton value size etc.).
func TestInstalledCodeRoundTrip(t *testing.T) {
	c := harness.New()
	owner := c.NewAccount()
	code := readContract(t, "social.bas")
	scid, res := c.Install(code, nil, nil, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	stored := c.Var(scid, "C")
	if s, _ := stored.(string); s != code {
		t.Fatalf("stored code differs: %d vs %d bytes", len(s), len(code))
	}
	var zero crypto.Hash
	_ = zero
}
