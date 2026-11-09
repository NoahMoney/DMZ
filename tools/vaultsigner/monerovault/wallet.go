package monerovault

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Transfer is the subset of a wallet-rpc transfer this backend reads. Incoming transfers carry SubaddrIndex;
// outgoing ones carry PaymentID and Destinations (the destinations are only known to the wallet that built the
// transaction, which is the signer's own wallet).
type Transfer struct {
	TxID          string    `json:"txid"`
	TxHash        string    `json:"tx_hash"`
	Amount        uint64    `json:"amount"`
	Height        uint64    `json:"height"`
	Confirmations uint64    `json:"confirmations"`
	Locked        bool      `json:"locked"`
	UnlockTime    uint64    `json:"unlock_time"`
	PaymentID     string    `json:"payment_id"`
	Note          string    `json:"note"`
	Type          string    `json:"type"`
	SubaddrIndex  Subaddr   `json:"subaddr_index"`
	SubaddrList   []Subaddr `json:"subaddr_indices"`
	Destinations  []Dest    `json:"destinations"`
}

type Subaddr struct {
	Major uint64 `json:"major"`
	Minor uint64 `json:"minor"`
}

type Dest struct {
	Address string `json:"address"`
	Amount  uint64 `json:"amount"`
}

func (t Transfer) ID() string {
	if t.TxID != "" {
		return t.TxID
	}
	return t.TxHash
}

// subaddr is the subaddress an output pays. wallet-rpc writes subaddr_index on incoming transfers; a few versions
// write subaddr_indices only, and a single-element list is equivalent.
func (t Transfer) subaddr() Subaddr {
	if (t.SubaddrIndex.Major != 0 || t.SubaddrIndex.Minor != 0) || len(t.SubaddrList) == 0 {
		return t.SubaddrIndex
	}
	return t.SubaddrList[0]
}

func (c *Client) Height(ctx context.Context) (uint64, error) {
	var r struct {
		Height uint64 `json:"height"`
	}
	if err := c.call(ctx, "get_height", nil, &r); err != nil {
		return 0, err
	}
	return r.Height, nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var r struct {
		Version uint64 `json:"version"`
	}
	if err := c.call(ctx, "get_version", nil, &r); err != nil {
		return "", err
	}
	if r.Version == 0 {
		return "", errors.New("get_version: the wallet returned no version")
	}
	return fmt.Sprint(r.Version), nil
}

// Address returns an existing subaddress without creating one.
func (c *Client) Address(ctx context.Context, account, index uint64) (string, error) {
	var r struct {
		Address string `json:"address"`
	}
	params := map[string]interface{}{"account_index": account, "address_index": index}
	if err := c.call(ctx, "get_address", params, &r); err != nil {
		return "", err
	}
	if r.Address == "" {
		return "", errors.New("get_address: the wallet returned an empty address")
	}
	return r.Address, nil
}

// CreateAddress creates the next subaddress. The caller checks the returned index: a wallet that is ahead of this
// signer can create an index other than the one asked for, and the caller then reads that index back instead.
func (c *Client) CreateAddress(ctx context.Context, account uint64, label string) (string, uint64, error) {
	var r struct {
		Address      string `json:"address"`
		AddressIndex uint64 `json:"address_index"`
	}
	params := map[string]interface{}{"account_index": account, "label": label}
	if err := c.call(ctx, "create_address", params, &r); err != nil {
		return "", 0, err
	}
	if r.Address == "" {
		return "", 0, errors.New("create_address: the wallet returned an empty address")
	}
	return r.Address, r.AddressIndex, nil
}

func (c *Client) Balance(ctx context.Context, account uint64) (uint64, error) {
	var r struct {
		Balance uint64 `json:"balance"`
	}
	if err := c.call(ctx, "get_balance", map[string]interface{}{"account_index": account}, &r); err != nil {
		return 0, err
	}
	return r.Balance, nil
}

func (c *Client) Incoming(ctx context.Context, account uint64) ([]Transfer, error) {
	var r struct {
		In []Transfer `json:"in"`
	}
	params := map[string]interface{}{"in": true, "account_index": account, "pool": false}
	if err := c.call(ctx, "get_transfers", params, &r); err != nil {
		return nil, err
	}
	return r.In, nil
}

// Outgoing returns the wallet's own outgoing transfers, pending and mined. Failed transfers are left out: they are
// not payments, and a payment is only certified after VerifyRelease reads it back.
func (c *Client) Outgoing(ctx context.Context, account uint64) ([]Transfer, error) {
	var r struct {
		Out     []Transfer `json:"out"`
		Pending []Transfer `json:"pending"`
	}
	params := map[string]interface{}{"out": true, "pending": true, "failed": false, "account_index": account}
	if err := c.call(ctx, "get_transfers", params, &r); err != nil {
		return nil, err
	}
	return append(r.Out, r.Pending...), nil
}

// Transfer pays one destination. A multisig or view-only wallet answers with a txset and no sent transaction; that
// is returned as ErrNeedsSignatures by the caller, never treated as a payment.
func (c *Client) Transfer(ctx context.Context, account uint64, to string, amount uint64, paymentID string) (txid string, txset string, err error) {
	params := map[string]interface{}{
		"destinations":  []map[string]interface{}{{"address": to, "amount": amount}},
		"account_index": account,
		"priority":      1,
	}
	if paymentID != "" {
		params["payment_id"] = paymentID
	}
	var r struct {
		TxHash        string `json:"tx_hash"`
		MultisigTxset string `json:"multisig_txset"`
		UnsignedTxset string `json:"unsigned_txset"`
	}
	if err := c.call(ctx, "transfer", params, &r); err != nil {
		return "", "", err
	}
	if r.MultisigTxset != "" || r.UnsignedTxset != "" {
		return "", first(r.MultisigTxset, r.UnsignedTxset), nil
	}
	if r.TxHash == "" {
		return "", "", errors.New("transfer: no tx_hash in the result")
	}
	return r.TxHash, "", nil
}

// TransferByTxID returns the wallet's record of txid and whether it knows it. Both the singular and plural response
// shapes in the wallet-rpc versions this backend meets are read.
func (c *Client) TransferByTxID(ctx context.Context, txid string, account uint64) (Transfer, bool, error) {
	var r struct {
		Transfer  *Transfer  `json:"transfer"`
		Transfers []Transfer `json:"transfers"`
	}
	params := map[string]interface{}{"txid": txid, "account_index": account}
	if err := c.call(ctx, "get_transfer_by_txid", params, &r); err != nil {
		var re *RPCError
		if errors.As(err, &re) && (re.Code == -5 || re.Code == -8 || re.Code == -9 ||
			strings.Contains(strings.ToLower(re.Message), "not found") || strings.Contains(strings.ToLower(re.Message), "unknown")) {
			return Transfer{}, false, nil // not found in this wallet
		}
		return Transfer{}, false, err
	}
	if r.Transfer != nil {
		t := *r.Transfer
		sort.SliceStable(t.Destinations, func(i, j int) bool { return t.Destinations[i].Address < t.Destinations[j].Address })
		return t, true, nil
	}
	if len(r.Transfers) > 0 {
		t := r.Transfers[0]
		sort.SliceStable(t.Destinations, func(i, j int) bool { return t.Destinations[i].Address < t.Destinations[j].Address })
		return t, true, nil
	}
	return Transfer{}, false, nil
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
