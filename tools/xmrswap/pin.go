package xmrswap

// HTLCCodeSHA256 is the SHA-256 of contracts/htlc.bas v2 as it is installed: the bytes reference.Canon makes of the
// source, which are exactly the code DERO.GetSC returns for the contract (U4). The maker checks it before he locks
// and the taker before she reads the lock, and each refuses an -htlc whose code hashes to anything else: a
// look-alike contract whose storage passes every other check could otherwise take either side's coins, and the
// SCID reaches the companion from a flag the operator typed or a page filled in. pin_test.go recomputes it from the
// tree, so an edit to htlc.bas that leaves this line behind fails the tests.
//
// The pin is only as good as the binary that carries it: a reproducible, signed build (BL-14) is what turns it
// from a claim into a check, and until one exists whoever built this binary is trusted to have pinned the right
// contract.
const HTLCCodeSHA256 = "44864ec3cddac871286b9cfd18d18e0b1d2ef0e31960436cee3bacc3025fef06"

// ProverSHA256 is the SHA-256 of the one proof-xmr binary the companion runs (GAP-003): the reproducible build of the
// committed research/crosschain/poc/proof-xmr, made by its build-reproducible.sh (MT-B26), 1,340,640 bytes. Anyone with
// the same toolchain rebuilds it byte for byte (rustc and cargo 1.98.1, x86_64-unknown-linux-gnu, Ubuntu 24.04's cc;
// the script's own header and repro.log). The cross-curve proof's verdict is the tool's word, so the tool is pinned like
// the contract: an absolute path, hashed before every run, and any other binary refused — a stale build, another
// program that happens to be called proof-xmr, one swapped after the companion started.
//
// It replaced 4d3bde0a…, a release build of the same sources whose hash held only in the checkout that made it
// (absolute paths end up in the binary); proofs from either verify with the other. Pinning says which binary runs, not
// that it is sound: its Bulletproofs R1CS back end is upstream's unaudited feature (GAP-003, U8), and the companion
// binary that carries this pin is not itself reproducible or signed yet (BL-14).
const ProverSHA256 = "7293e9c97e0330966499fff397a0488ef40d38f65da49423429c8c548c83d39d"
