package zecspv

import "testing"

// One real NU6.3 Mainnet header: the difficulty filter and Equihash (200, 9).
func BenchmarkCheckHeader(b *testing.B) {
	h := loadHeaderFile(b, "mainnet-3436131.json").headers(b)[40]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Mainnet.CheckHeader(h, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// The whole 41-header run: parsing, links, times, 13 recomputed nBits and 41 Equihash solutions.
func BenchmarkVerifyHeaders(b *testing.B) {
	f := loadHeaderFile(b, "mainnet-3436131.json")
	raw := f.raw(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := VerifyHeaders(Mainnet, f.FirstHeight, raw, Options{}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(raw)), "ns/header")
}

// A 10-confirmation funding proof with 31 headers below: 41 headers and a Merkle branch.
func BenchmarkVerifyFunding(b *testing.B) {
	c := newFundingCase(b, "mainnet-3436131.json", 3436162, 3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := VerifyFunding(Mainnet, &c.proof, c.policy); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNextBits(b *testing.B) {
	hs := loadHeaderFile(b, "mainnet-3436131.json").headers(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Mainnet.NextBits(3436159, hs[:28], hs[28].Time); err != nil {
			b.Fatal(err)
		}
	}
}
