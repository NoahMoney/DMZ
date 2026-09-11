module veil/tools

go 1.22

require veil v0.0.0

require (
	filippo.io/edwards25519 v1.1.1
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.3.0
	golang.org/x/crypto v0.33.0
	golang.org/x/sys v0.30.0 // indirect
)

replace veil => ../
