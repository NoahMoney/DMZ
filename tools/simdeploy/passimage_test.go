package main

import (
	"strings"
	"testing"
)

func TestPassImageAcceptsAProfileDocOrHttps(t *testing.T) {
	scid := strings.Repeat("ab", 32)
	for _, ok := range []string{"", "dmz:" + scid, "https://example.invalid/pass.webp", "HTTPS://example.invalid/p.png"} {
		if !passImageOK(ok) {
			t.Errorf("refused %q", ok)
		}
	}
	for _, bad := range []string{
		"dmz:" + scid[:63], "dmz:" + strings.ToUpper(scid), "dmz:" + scid + " ", "DMZ:" + scid, "dmz:" + scid + "00",
		"http://example.invalid/p.png", "javascript:alert(1)", "data:image/svg+xml;base64,PHN2Zz4=", "https://a.invalid/\"x",
		scid, "ipfs://x",
	} {
		if passImageOK(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
}
