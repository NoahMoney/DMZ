// Package coord is how the federation's signers pass each other the material a k-of-n foreign-chain signature needs:
// Zcash per-input signatures for a P2SH spend, and Monero multisig ceremony rounds, multisig info and transaction
// sets. The normative specification is research/wrapper/DESIGN.md §C7; the operator procedures are
// docs/SIGNER_RUNBOOK.md. In one paragraph:
//
// Transport: untrusted, dumb stores — HTTP relays and shared directories — any number of them at once. A signer only
// ever dials out, so a signer host keeps no inbound port (DESIGN.md Controls §5), and no store is trusted with
// anything: a message is signed by its sender's pinned Ed25519 identity, bound to the vault, the round and the
// sender, and encrypted to every pinned signer with HPKE (RFC 9180, from the standard library). A store can delay,
// drop, withhold, reorder or replay; it cannot forge, alter, redirect into another round or vault, or read. Losing
// every store is therefore a delay, never a wrong payment: a backend that cannot collect k payloads does not sign,
// its Release returns an error, and the daemon retries on its next pass.
//
// The interface the backends use is Coordinator: Put publishes this signer's payload for a round, Get returns every
// signer's newest authentic payload for it, keyed by the signer's DERO address (CanonicalID), without waiting.
//
// What the channel does not give: agreement. A signer may publish different payloads to different stores (or
// replace its payload), and two honest nodes can see different versions of one sender's payload for a while. A
// backend must therefore name its rounds by everything a payload is about (a sighash, a txset hash, an attempt) and
// must check every payload it uses against its own view of the chain, as it already must for everything else.
package coord
