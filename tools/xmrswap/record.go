package xmrswap

import "fmt"

//go:generate go run gen_record.go

// RecordedHTLC is MT-A18: the htlc.bas scid deploy/networks.json records for a DERO network ("mainnet", "testnet",
// "simulator"), which the command line uses when -htlc is omitted. The record is compiled in — record_gen.go, which
// `go generate ./xmrswap` writes from deploy/networks.json — and TestRecordedHTLCIsTheDeployRecord fails the moment the
// two differ. It is a convenience, not a trust decision: whichever scid is used, given or recorded, the companion
// refuses it unless its code hashes to the pinned htlc.bas v3 (HTLCCodeSHA256), so a stale or wrong record fails
// closed.
func RecordedHTLC(deroNet string) (string, error) {
	scid, ok := recordedHTLC[deroNet]
	switch {
	case !ok:
		return "", fmt.Errorf("deploy/networks.json has no %q network", deroNet)
	case scid == "":
		return "", fmt.Errorf("deploy/networks.json records no htlc.bas on %s yet: give -htlc", deroNet)
	case !isHash(scid):
		return "", fmt.Errorf("deploy/networks.json records %q as htlc.bas on %s, which is not a scid", scid, deroNet)
	}
	return scid, nil
}

// RecordedXmrTreasury is deploy/networks.json's xmrTreasury for a DERO network: the Monero address the maker's sweep pays
// DMZ's swap fee to (fee.go, docs/XMR_SWAP.md §2.2), compiled in beside the htlc record by the same generator and held
// to the file by the same kind of test. "" is none recorded. It is not judged here: CheckTreasury decides, against the
// swap's own Monero network, whether a swap may start with it.
func RecordedXmrTreasury(deroNet string) (string, error) {
	t, ok := recordedXmrTreasury[deroNet]
	if !ok {
		return "", fmt.Errorf("deploy/networks.json has no %q network, so no xmrTreasury", deroNet)
	}
	return t, nil
}
