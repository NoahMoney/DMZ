package main

import (
	"errors"
	"flag"
	"fmt"
	"log"

	"veil/tools/vaultsigner/monerovault"
)

// -backend monero: the vault's reserve in a Monero wallet served by monero-wallet-rpc. The wallet is the operator's
// (a multisig share for a federation, a single-key wallet for a pilot); this file adapts its records to the daemon's
// Backend interface. Observation and single-wallet payments are real RPC calls; a multisig txset is refused rather
// than recorded as a payment, because this build has no Monero signing coordinator.

var moneroConfig = flag.String("monero-config", "", "-backend monero: the wallet-rpc configuration (JSON: network, walletRpc, address, account, finalityDepth, timeoutSeconds, retries, retryDelayMs)")

var moneroNetFor = map[string]string{"simulator": "stagenet", "testnet": "stagenet", "mainnet": "mainnet"}

func newMoneroBackend(network string) (Backend, error) {
	if *moneroConfig == "" {
		return nil, errors.New("-backend monero needs -monero-config <file>: network, walletRpc, the vault's primary address and the finality policy")
	}
	cfg, err := monerovault.LoadConfig(*moneroConfig)
	if err != nil {
		return nil, err
	}
	if want := moneroNetFor[network]; cfg.Network != want {
		return nil, fmt.Errorf("a %s vault holds Monero %s coins, and -monero-config says %q", network, want, cfg.Network)
	}
	if len(*vault) != 64 {
		return nil, errors.New("-backend monero needs -vault: the vault id names the payment id space")
	}
	be, err := monerovault.New(cfg, *vault, log.Printf)
	if err != nil {
		return nil, err
	}
	fmt.Println(be.Summary())
	return moneroBackend{be}, nil
}

type moneroBackend struct{ *monerovault.Backend }

func (m moneroBackend) Deposits() ([]Deposit, error) {
	ds, err := m.Backend.Deposits()
	if err != nil {
		return nil, err
	}
	out := make([]Deposit, len(ds))
	for i, d := range ds {
		out[i] = Deposit{TxID: d.TxID, Vout: d.Vout, Index: d.Index, Amount: d.Amount, Height: d.Height}
	}
	return out, nil
}

func (m moneroBackend) Verify(d Deposit) (bool, error) {
	return m.Backend.Verify(monerovault.Deposit{TxID: d.TxID, Vout: d.Vout, Index: d.Index, Amount: d.Amount, Height: d.Height})
}
