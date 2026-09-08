// Package xmrswap is the DERO <-> Monero atomic swap companion (docs/XMR_SWAP_BUILD.md; the design is
// research/crosschain/RESEARCH.md §4.4). This half of it (§3.C) is the Monero side: the key arithmetic the joint
// address is made of, Monero's own address encoding, a client for monero-wallet-rpc, the wrapper around the
// proof-xmr tool, and a fake monero-wallet-rpc for tests and the simulator driver. The state machines, the DERO
// client, the rendezvous channel and the CLI (§3.D) live in the same package and build against the names here.
//
// Secrets. A Scalar is a Monero secret key or a key share, and k_b is a gate secret; nothing in this package
// logs, prints or formats one, and no error carries one. A secret leaves the process in exactly two places, both
// the protocol's own (§4 invariant 1): the wallet-RPC request that imports the joint spend key into the user's own
// wallet, and the proof tool's stdin. Everything else that crosses a process boundary — hashes, public points,
// addresses, proofs, transaction ids — is public by construction.
package xmrswap
