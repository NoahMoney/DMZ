package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The vectors are written from app/profile.js (tests/js/profile.test.mjs checks them against it too), so the Go
// builder and the app's reader cannot drift apart without one of the two suites failing.
type vectors struct {
	Cases []struct {
		Name string `json:"name"`
		Code string `json:"code"`
		OK   bool   `json:"ok"`
		Kind string `json:"kind"`
		W    int    `json:"w"`
		H    int    `json:"h"`
	} `json:"cases"`
	Builds []struct {
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Image string `json:"image"`
		Links Links  `json:"links"`
		Code  string `json:"code"`
	} `json:"builds"`
}

func load(t *testing.T) vectors {
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "js", "fixtures", "profiledoc-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) < 20 || len(v.Builds) < 2 {
		t.Fatalf("only %d cases and %d builds in the vectors", len(v.Cases), len(v.Builds))
	}
	return v
}

func TestParseGivesTheAppsVerdicts(t *testing.T) {
	for _, c := range load(t).Cases {
		d, err := Parse(c.Code)
		if (err == nil) != c.OK {
			t.Errorf("%s: Go says ok=%v (%v), the app says ok=%v", c.Name, err == nil, err, c.OK)
			continue
		}
		if c.OK && (d.Kind != c.Kind || d.W != c.W || d.H != c.H) {
			t.Errorf("%s: %s %dx%d, the app reads %s %dx%d", c.Name, d.Kind, d.W, d.H, c.Kind, c.W, c.H)
		}
	}
}

func TestComposeBuildsTheAppsBytes(t *testing.T) {
	for _, b := range load(t).Builds {
		img, err := base64.StdEncoding.DecodeString(b.Image)
		if err != nil {
			t.Fatal(err)
		}
		code, err := Compose(b.Kind, img, b.Links)
		if err != nil {
			t.Fatalf("%s: %v", b.Name, err)
		}
		if code != b.Code {
			t.Errorf("%s: Go composes different code from the app", b.Name)
		}
	}
}

func TestComposeRefusesWhatTheAppRefuses(t *testing.T) {
	v := load(t)
	var png []byte
	for _, b := range v.Builds {
		if b.Kind == "logo" {
			png, _ = base64.StdEncoding.DecodeString(b.Image)
		}
	}
	for name, l := range map[string]Links{
		"javascript":  {Website: "javascript:alert(1)"},
		"data":        {Website: "data:text/html;base64,PHNjcmlwdD4="},
		"http":        {Website: "http://example.org/"},
		"userinfo":    {Github: "https://github.com@evil.invalid/"},
		"look-alike":  {X: "https://x.com.evil.invalid/dmz"},
		"idn":         {Github: "https://xn--gthub-n4a.com/"},
		"port":        {Telegram: "https://t.me:8443/dmz"},
		"stray %":     {Website: "https://example.org/%zz"},
		"backslash":   {Website: "https://example.org\\@evil.invalid/"},
		"not discord": {Discord: "https://discordapp.com/invite/x"},
	} {
		if _, err := Compose("logo", png, l); err == nil {
			t.Errorf("%s: composed", name)
		}
	}
	if _, err := Compose("pass", png, Links{X: "https://x.com/dmz"}); err == nil {
		t.Error("a pass doc with links composed")
	}
	if _, err := Compose("logo", nil, Links{}); err == nil {
		t.Error("an empty doc composed")
	}
	if _, err := Compose("svg", png, Links{}); err == nil {
		t.Error("an unknown kind composed")
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if _, err := Compose("logo", svg, Links{}); err == nil {
		t.Error("an SVG composed")
	}
}

// The largest pass image the format admits still installs under DERO's 20,000 storage-gas cap.
func TestTheLargestDocInstalls(t *testing.T) {
	longest := len(head) + len("// pass ") + base64.StdEncoding.EncodedLen(kinds["pass"].max) + len("\n// links \n")
	if longest > maxCode || maxCode+25 > 20000 {
		t.Fatalf("a full pass doc is %d bytes (gas %d); the cap is %d bytes and 20,000 gas", longest, longest+25, maxCode)
	}
	logo := len(head) + len("// logo ") + base64.StdEncoding.EncodedLen(kinds["logo"].max) + len("\n// links ") + base64.StdEncoding.EncodedLen(linksMax) + 1
	if logo > maxCode {
		t.Fatalf("a full logo doc is %d bytes", logo)
	}
	if !strings.HasSuffix(head, "\n") || strings.ContainsAny(head, "\r\t") {
		t.Fatal("the wrapper must be plain LF text")
	}
}
