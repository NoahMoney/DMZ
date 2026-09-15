package coord

import (
	"crypto/rand"
	"fmt"
	"time"
)

// NewLocalGroup builds one node per id for one vault, sharing one in-memory store, with fresh identities that exist
// only in this process. It is for backend tests and rehearsals — three signers in one process — and never for a
// signer: a real identity comes from `vaultsigner keygen`, is kept in a private file and is pinned by every peer.
// ids are DERO addresses; now may be nil (time.Now).
func NewLocalGroup(vault string, ids []string, now func() time.Time) ([]*Node, *MemStore, error) {
	if now == nil {
		now = time.Now
	}
	store := NewMemStore("local", MemLimits{}, now)
	var idns []*Identity
	var pubs []PublicIdentity
	for _, id := range ids {
		cid, err := CanonicalID(id)
		if err != nil {
			return nil, nil, err
		}
		seed := make([]byte, seedLen)
		if _, err := rand.Read(seed); err != nil {
			return nil, nil, err
		}
		idn, err := fromSeed(cid, seed)
		clear(seed)
		if err != nil {
			return nil, nil, err
		}
		idns = append(idns, idn)
		pubs = append(pubs, idn.pub)
	}
	var nodes []*Node
	for _, idn := range idns {
		n, err := New(Config{Vault: vault, Identity: idn, Signers: pubs, Stores: []Store{store}, Now: now})
		if err != nil {
			return nil, nil, fmt.Errorf("coord: local group: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, store, nil
}
