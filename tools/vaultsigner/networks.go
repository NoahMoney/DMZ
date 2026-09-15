package main

// Per-network parameters for the foreign chains a signer can watch. The `sim` backend ignores them; the real
// backends (zcash, monero, solana) take the block for the DERO network the vault lives on: mainnet vaults hold
// mainnet coins, testnet vaults hold test coins, the simulator uses whatever the operator points it at.
type chainParams struct {
	Name          string
	RPC           string // default local node / wallet-rpc endpoint
	Confirmations int64  // policy from RESEARCH.md (Specification) §3
	AddrPrefixes  []string
	Decimals      int
}

var chainTable = map[string]map[string]chainParams{
	"mainnet": {
		"monero": {"Monero mainnet", "127.0.0.1:18082", 10, []string{"4", "8"}, 12},
		"zcash":  {"Zcash mainnet", "127.0.0.1:8232", 10, []string{"t1", "t3"}, 8}, // transparent P2SH custody pays transparent addresses only (HANDOVER §5.1)
		"solana": {"Solana mainnet-beta", "https://api.mainnet-beta.solana.com", 32, nil, 9},
	},
	"testnet": {
		"monero": {"Monero stagenet", "127.0.0.1:38082", 10, []string{"5", "7"}, 12},
		"zcash":  {"Zcash testnet", "127.0.0.1:18232", 10, []string{"tm", "t2"}, 8},
		"solana": {"Solana devnet", "https://api.devnet.solana.com", 32, nil, 9},
	},
	"simulator": {
		"monero": {"Monero regtest / stagenet", "127.0.0.1:38082", 2, []string{"5", "7"}, 12},
		"zcash":  {"Zcash regtest", "127.0.0.1:18232", 2, []string{"tm", "t2"}, 8},
		"solana": {"Solana localnet", "http://127.0.0.1:8899", 1, nil, 9},
	},
}
