package coord

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIdentityFileRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "signer.key")
	id := testAddr(1)
	pub, err := GenerateIdentityFile(p, id)
	if err != nil {
		t.Fatal(err)
	}
	idn, err := LoadIdentityFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if idn.Public().String() != pub.String() || idn.ID() != id {
		t.Fatal("the loaded identity is not the one generated")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(p)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %04o, want 0600", st.Mode().Perm())
		}
	}
	if err := checkPrivate(p); err != nil {
		t.Fatalf("the generated file is not private: %v", err)
	}
	before, _ := os.ReadFile(p)
	if _, err := GenerateIdentityFile(p, id); err == nil {
		t.Fatal("an existing identity file was overwritten")
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(before, after) {
		t.Fatal("a refused keygen changed the existing file")
	}
	if _, err := ParsePublicIdentity(id, pub.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePublicIdentity(testAddr(2), pub.String()); err == nil {
		t.Fatal("an identity line was accepted for an address it was not made for")
	}
	if !strings.HasPrefix(pub.Fingerprint(), pub.Fingerprint()[:4]+"-") || len(pub.Fingerprint()) != 29 {
		t.Fatalf("fingerprint %q", pub.Fingerprint())
	}
}

func TestAnIdentityNeverPrintsOrSerialisesItsKey(t *testing.T) {
	idn := newIdentity(t, testAddr(1))
	secrets := []string{hex.EncodeToString(idn.sign.Seed()), hex.EncodeToString(idn.sign[32:])}
	kb, _ := idn.kem.Bytes()
	secrets = append(secrets, hex.EncodeToString(kb))
	var outs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%q", "%d"} {
		outs = append(outs, fmt.Sprintf(verb, idn), fmt.Sprintf(verb, *idn),
			fmt.Sprintf(verb, struct{ I Identity }{*idn}), fmt.Sprintf(verb, []Identity{*idn}))
	}
	for _, o := range outs {
		for _, s := range secrets {
			if strings.Contains(strings.ToLower(o), s) {
				t.Fatalf("a formatted identity carries key material: %q", o)
			}
		}
	}
	if _, err := json.Marshal(idn); err == nil {
		t.Fatal("an identity serialised to JSON")
	}
	if _, err := json.Marshal(struct{ I Identity }{*idn}); err == nil {
		t.Fatal("a struct holding an identity serialised to JSON")
	}
}

func TestLoadRefusesMalformedFilesWithoutEchoingThem(t *testing.T) {
	dir := t.TempDir()
	id := testAddr(1)
	seed := strings.Repeat("ab", 32)
	cases := map[string]string{
		"extra line":     identityHeader + "\nid " + id + "\nseed " + seed + "\nmore\n",
		"wrong header":   "dmz coord identity v2\nid " + id + "\nseed " + seed + "\n",
		"not canonical":  identityHeader + "\nid " + strings.ToUpper(id) + "\nseed " + seed + "\n",
		"not an address": identityHeader + "\nid dero1nope\nseed " + seed + "\n",
		"seed not hex":   identityHeader + "\nid " + id + "\nseed " + strings.Repeat("zz", 32) + "\n",
		"seed short":     identityHeader + "\nid " + id + "\nseed " + seed[:62] + "\n",
		"empty":          "",
		"huge":           strings.Repeat(seed, 100),
	}
	for name, body := range cases {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		f, err := createPrivate(p)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(body)
		f.Close()
		_, err = LoadIdentityFile(p)
		if err == nil {
			t.Errorf("%s: loaded", name)
			continue
		}
		if strings.Contains(err.Error(), seed[:16]) || strings.Contains(err.Error(), "zzzz") {
			t.Errorf("%s: the error repeats the file: %v", name, err)
		}
	}
}

func TestLoadRefusesAKeyOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows ACL case is private_windows_test.go")
	}
	p := filepath.Join(t.TempDir(), "k")
	if _, err := GenerateIdentityFile(p, testAddr(1)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentityFile(p); err == nil || !strings.Contains(err.Error(), "exposed") {
		t.Fatalf("a world-readable key loaded: %v", err)
	}
	link := p + ".link"
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, link); err == nil {
		if _, err := LoadIdentityFile(link); err == nil {
			t.Fatal("a key loaded through a symbolic link")
		}
	}
}

func TestParsePublicIdentityRefusesWeakOrAlteredKeys(t *testing.T) {
	idn := newIdentity(t, testAddr(1))
	good := idn.Public().String()
	id := idn.ID()
	body := good[len(PublicPrefix):]
	smallOrder := "01" + strings.Repeat("00", 31)
	cases := map[string]string{
		"small-order Ed25519": PublicPrefix + smallOrder + body[64:],
		"non-canonical point": PublicPrefix + "edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f" + body[64:],
		"low-order X25519":    PublicPrefix + body[:64] + strings.Repeat("00", 32) + body[128:],
		"altered proof":       PublicPrefix + body[:len(body)-2] + flipHex(body[len(body)-2:]),
		"altered KEM key":     PublicPrefix + body[:64] + flipHex(body[64:66]) + body[66:],
		"wrong prefix":        "dmzcoord2:" + body,
		"short":               good[:len(good)-2],
		"not hex":             PublicPrefix + strings.Repeat("g", len(body)),
	}
	for name, line := range cases {
		if _, err := ParsePublicIdentity(id, line); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParsePublicIdentity(id, "  "+good+"\n"); err != nil {
		t.Fatalf("surrounding whitespace: %v", err)
	}
}

func flipHex(s string) string {
	b, _ := hex.DecodeString(s)
	b[0] ^= 1
	return hex.EncodeToString(b)
}

func TestCanonicalID(t *testing.T) {
	a := testAddr(5)
	if c, err := CanonicalID(a); err != nil || c != a {
		t.Fatalf("%v %s", err, c)
	}
	if c, err := CanonicalID(strings.ToUpper(a)); err != nil || c != a {
		t.Fatalf("upper case: %v %s", err, c)
	}
	for _, bad := range []string{"", "dero1", "not an address", "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"} {
		if _, err := CanonicalID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
