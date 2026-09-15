package zcashvault

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Coordinator relays payloads between the signers. It is untrusted: a coordinator can withhold, delay, reorder,
// duplicate or forge payloads, and the only thing that follows is a release that waits. Every signature it carries
// is checked against the key it claims and the digest of the one transaction this signer built itself, and every
// "final" transaction it carries is checked for k valid signatures and against the contract's withdrawal before it
// is treated as the payment. Get never blocks.
//
// tools/vaultsigner's coordinator implementation (another package) satisfies this interface; DirCoordinator below is
// a shared-directory relay for the pilot and the tests.
type Coordinator interface {
	Put(ctx context.Context, round string, payload []byte) error
	Get(ctx context.Context, round string) (map[string][]byte, error)
}

// maxPayload bounds one payload read from the coordinator: a fully signed release of MaxInputs inputs is well under.
const maxPayload = 512 << 10

// The two kinds of round a release uses. The signature round is keyed by the transaction id, which ZIP 244 computes
// without the scriptSigs: only signers who built the same transaction meet in it.
func sigRound(vault string, j uint64, txid string) string {
	return "zcashvault/" + vault + "/" + strconv.FormatUint(j, 10) + "/" + txid
}

func finalRound(vault string, j uint64) string {
	return "zcashvault/" + vault + "/" + strconv.FormatUint(j, 10) + "/final"
}

// sigPayload is one signer's signatures for every input of one transaction.
type sigPayload struct {
	V      int      `json:"v"`
	Vault  string   `json:"vault"`
	J      uint64   `json:"j"`
	TxID   string   `json:"txid"`
	Signer string   `json:"signer"` // the signer's account xpub: which key each signature must verify under
	Sigs   []string `json:"sigs"`   // hex, DER + SIGHASH_ALL, one per input in input order
}

// finalPayload is a fully signed transaction for withdrawal j.
type finalPayload struct {
	V     int    `json:"v"`
	Vault string `json:"vault"`
	J     uint64 `json:"j"`
	TxID  string `json:"txid"`
	Hex   string `json:"hex"`
}

func decodePayload(b []byte, out interface{}) error {
	if len(b) > maxPayload {
		return fmt.Errorf("a payload of %d bytes", len(b))
	}
	return json.Unmarshal(b, out)
}

// DirCoordinator is a coordinator made of one directory every signer can write: a round is a subdirectory named by
// the SHA-256 of its name, and each signer's payload a file in it named by the SHA-256 of that signer's id. It is
// for the pilot and the tests; it adds nothing a network relay would not, and the directory's owner is exactly as
// untrusted as any coordinator.
type DirCoordinator struct {
	Dir string
	Me  string // this signer's id, e.g. its account xpub
}

func hashName(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:16])
}

func (c DirCoordinator) Put(ctx context.Context, round string, payload []byte) error {
	if c.Dir == "" || c.Me == "" {
		return errors.New("zcashvault: the directory coordinator needs a directory and an id")
	}
	d := filepath.Join(c.Dir, hashName(round))
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	name := filepath.Join(d, hashName(c.Me)+".json")
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

func (c DirCoordinator) Get(ctx context.Context, round string) (map[string][]byte, error) {
	d := filepath.Join(c.Dir, hashName(round))
	ents, err := os.ReadDir(d)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() > maxPayload {
			continue
		}
		b, err := os.ReadFile(filepath.Join(d, e.Name()))
		if err != nil {
			continue
		}
		out[e.Name()] = b
	}
	return out, nil
}
