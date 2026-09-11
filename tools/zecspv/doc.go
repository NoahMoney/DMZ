// Package zecspv checks Zcash block headers and Merkle inclusion on the client, so that a node which lies about a
// transaction has to back the lie with real Equihash work.
//
// For a run of consecutive serialized headers it checks, per the Zcash protocol specification (§7.6, §7.7):
//
//   - the header's encoding, including the minimal compactSize of the 1344-byte solution, and version ≥ 4;
//   - nBits in range (not negative, zero or overflowing, and no easier than PoWLimit);
//   - the difficulty filter: SHA-256d of the whole header, read little-endian, at or below ToTarget(nBits);
//   - the Equihash (200, 9) solution (§7.7.1), with a BLAKE2b written here because golang.org/x/crypto/blake2b
//     cannot set a personalisation;
//   - the link: each header's hashPrevBlock is the previous header's hash;
//   - for every header with 28 headers below it: nBits equal to ThresholdBits(height) (§7.7.3, with Blossom's
//     spacing and Testnet's minimum-difficulty rule), and nTime after the median-time-past and no more than 90
//     minutes past it;
//   - a caller-supplied floor: no header may be easier than a target the caller knows the network to be at.
//
// It builds and verifies Bitcoin-style Merkle branches (duplicate-last on odd levels) over transaction ids, and
// VerifyFunding joins the two: a transaction id is in a block whose header chain carries at least a given number
// of further headers of valid work, and it returns the cumulative work.
//
// It does not import tools/zecswap, which will import it. zecswap computes transaction ids (the ZIP 244 txid for
// v5 and v6) and passes them in. docs/ZCASH_HEADER_VERIFICATION.md states what this proves, what it cannot, what a
// lie costs in hash power, and where zecswap calls it.
package zecspv
