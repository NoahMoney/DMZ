package reference

import "strings"

// Canon is the source a contract is actually installed with.
//
// The DVM charges install storage gas on SOURCE BYTES, comments included, against a cap of 20,000 units per
// transaction. vault.bas carries 4,393 bytes of comments explaining why its k-of-n rules are what they are, and
// those bytes were competing with the rules themselves for the same budget: the vault reached 19,400 of 20,000
// with its documentation, leaving 771 units — not enough for a safety feature. Documentation that costs gas is
// documentation that gets deleted under pressure, which is exactly backwards.
//
// So the repo keeps the comments and the chain gets the code. A whole-line comment and a blank line have no
// behaviour, so dropping them cannot change what the contract does. Nothing else is touched: no trailing
// comments are stripped (contracts/ has none, and "//" inside a string literal would need a real parser to tell
// apart), no line is reflowed, no whitespace inside a line is altered.
//
// This is also what template verification should attest. Before, a market's on-chain code was hashed against a
// template file that included prose — so rewording a comment invalidated every deployed market for no change in
// behaviour, and a user comparing hashes was being asked to trust paragraphs. Now the hash covers the executable
// source and only that.
func Canon(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		out = append(out, strings.TrimSuffix(l, "\r"))
	}
	return strings.Join(out, "\n") + "\n"
}
