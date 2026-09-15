package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The profile doc format: the same bytes app/profile.js builds and reads (docs/PROFILE_DOCS.md). A change here is
// a change there, and tests/js/fixtures/profiledoc-vectors.json holds the verdicts both must give.
const head = "Function InitializePrivate() Uint64\n10 RETURN 0\nEnd Function\n// DMZ profile doc v1. Data only: no entrypoint, no storage, no update path.\n"

const (
	// storage gas for an install is len(code) + 25, measured on the simulator; DERO refuses a transaction above
	// 20,000, so no doc is longer than 19,500
	maxCode  = 19500
	linksMax = 1024
)

type caps struct {
	max, dim int
	links    bool
}

var kinds = map[string]caps{"logo": {8192, 256, true}, "pass": {14000, 384, false}}

var linkKeys = []string{"website", "x", "telegram", "discord", "github"}

var platformHosts = map[string][]string{
	"x":        {"x.com", "www.x.com", "twitter.com", "www.twitter.com"},
	"telegram": {"t.me"},
	"discord":  {"discord.gg", "discord.com", "www.discord.com"},
	"github":   {"github.com", "www.github.com"},
}

var (
	bodyRe  = regexp.MustCompile(`^// (logo|pass) ([A-Za-z0-9+/=]*)\n// links ([A-Za-z0-9+/=]*)\n$`)
	b64Re   = regexp.MustCompile(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`)
	urlRe   = regexp.MustCompile(`^https://([A-Za-z0-9.-]{1,253})([/?#][\x21-\x7e]*)?$`)
	labelRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	fourAZ  = regexp.MustCompile(`^[A-Za-z]{4}$`)
	fourCC  = regexp.MustCompile(`^[A-Z0-9 ]{4}$`)
	digits  = regexp.MustCompile(`^[0-9]+$`)
)

// Links are the creator's project links, in the order the JSON carries them.
type Links struct {
	Website  string `json:"website,omitempty"`
	X        string `json:"x,omitempty"`
	Telegram string `json:"telegram,omitempty"`
	Discord  string `json:"discord,omitempty"`
	Github   string `json:"github,omitempty"`
}

func (l Links) pairs() [][2]string {
	return [][2]string{{"website", l.Website}, {"x", l.X}, {"telegram", l.Telegram}, {"discord", l.Discord}, {"github", l.Github}}
}

// Doc is a parsed profile doc.
type Doc struct {
	Kind  string
	Type  string // png or webp; empty when the doc carries no image
	W, H  int
	Image []byte
	Links map[string]string
}

// Compose builds the doc's contract code, and refuses anything the app's reader would refuse.
func Compose(kind string, img []byte, l Links) (string, error) {
	k, ok := kinds[kind]
	if !ok {
		return "", fmt.Errorf("kind %q: logo or pass", kind)
	}
	var trimmed Links
	ps := l.pairs()
	for i := range ps {
		ps[i][1] = strings.TrimSpace(ps[i][1])
		if ps[i][1] == "" {
			continue
		}
		if !k.links {
			return "", errors.New("a pass image carries no links")
		}
		if err := checkLink(ps[i][0], ps[i][1]); err != nil {
			return "", err
		}
	}
	trimmed = Links{ps[0][1], ps[1][1], ps[2][1], ps[3][1], ps[4][1]}
	var lj []byte
	if trimmed != (Links{}) {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // JSON.stringify escapes only " and \ here, and a link may contain neither
		if err := enc.Encode(trimmed); err != nil {
			return "", err
		}
		lj = bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
		if len(lj) > linksMax {
			return "", fmt.Errorf("the links take %d bytes; the doc holds %d", len(lj), linksMax)
		}
	}
	code := head + "// " + kind + " " + base64.StdEncoding.EncodeToString(img) + "\n// links " + base64.StdEncoding.EncodeToString(lj) + "\n"
	if _, err := Parse(code); err != nil {
		return "", fmt.Errorf("the doc would not pass the app's reader: %w", err)
	}
	return code, nil
}

// Parse checks a doc's code the way app/profile.js parse() does. Every check fails the whole doc.
func Parse(code string) (*Doc, error) {
	if len(code) > maxCode {
		return nil, fmt.Errorf("the code is %d bytes; a doc is at most %d", len(code), maxCode)
	}
	if !strings.HasPrefix(code, head) {
		return nil, errors.New("not the profile doc wrapper")
	}
	m := bodyRe.FindStringSubmatch(code[len(head):])
	if m == nil {
		return nil, errors.New("after the wrapper: not one image line and one links line")
	}
	d := &Doc{Kind: m[1]}
	k := kinds[d.Kind]
	if m[2] != "" {
		b, err := unb64(m[2])
		if err != nil {
			return nil, fmt.Errorf("image: %w", err)
		}
		if len(b) > k.max {
			return nil, fmt.Errorf("the %s image is %d bytes; at most %d", d.Kind, len(b), k.max)
		}
		typ, w, h := imageInfo(b)
		if typ == "" {
			return nil, errors.New("the image is not a well-formed, still PNG or WebP")
		}
		if w > k.dim || h > k.dim {
			return nil, fmt.Errorf("the %s image is %dx%d; at most %dx%d", d.Kind, w, h, k.dim, k.dim)
		}
		d.Type, d.W, d.H, d.Image = typ, w, h, b
	} else if d.Kind == "pass" {
		return nil, errors.New("a pass doc needs an image")
	}
	if m[3] != "" {
		if !k.links {
			return nil, errors.New("a pass doc carries no links")
		}
		b, err := unb64(m[3])
		if err != nil {
			return nil, fmt.Errorf("links: %w", err)
		}
		if len(b) > linksMax {
			return nil, fmt.Errorf("the links are %d bytes; at most %d", len(b), linksMax)
		}
		links, err := parseLinks(b)
		if err != nil {
			return nil, err
		}
		if len(links) > 0 {
			d.Links = links
		}
	}
	if d.Image == nil && d.Links == nil {
		return nil, errors.New("the doc holds neither an image nor a link")
	}
	return d, nil
}

// canonical base64 only, as the app: one string per byte sequence
func unb64(s string) ([]byte, error) {
	if !b64Re.MatchString(s) {
		return nil, errors.New("not canonical base64")
	}
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || base64.StdEncoding.EncodeToString(b) != s {
		return nil, errors.New("not canonical base64")
	}
	return b, nil
}

func imageInfo(b []byte) (typ string, w, h int) {
	if w, h, ok := pngInfo(b); ok {
		return "png", w, h
	}
	if w, h, ok := webpInfo(b); ok {
		return "webp", w, h
	}
	return "", 0, 0
}

// PNG: the signature, IHDR first, chunks that tile the file exactly, IEND last with nothing after it, no second
// header and no animation.
func pngInfo(b []byte) (int, int, bool) {
	if len(b) < 57 || !bytes.Equal(b[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return 0, 0, false
	}
	o, w, h, idat := 8, 0, 0, false
	for o+12 <= len(b) {
		n := int(binary.BigEndian.Uint32(b[o:]))
		t, end := string(b[o+4:o+8]), o+12+n
		if end > len(b) || !fourAZ.MatchString(t) {
			return 0, 0, false
		}
		if o == 8 {
			if t != "IHDR" || n != 13 {
				return 0, 0, false
			}
			w, h = int(binary.BigEndian.Uint32(b[16:])), int(binary.BigEndian.Uint32(b[20:]))
		} else if t == "IHDR" || t == "acTL" || t == "fcTL" || t == "fdAT" {
			return 0, 0, false
		}
		if t == "IDAT" {
			idat = true
		}
		o = end
		if t == "IEND" {
			return w, h, o == len(b) && idat && w > 0 && h > 0
		}
	}
	return 0, 0, false
}

// WebP: RIFF whose size is the file's, one VP8 / VP8L bitstream (after VP8X when there is one), chunks that tile
// the file exactly, no animation.
func webpInfo(b []byte) (int, int, bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" || int(binary.LittleEndian.Uint32(b[4:])) != len(b)-8 {
		return 0, 0, false
	}
	o, w, h, bits := 12, 0, 0, 0
	for o < len(b) {
		if o+8 > len(b) {
			return 0, 0, false
		}
		t, n := string(b[o:o+4]), int(binary.LittleEndian.Uint32(b[o+4:]))
		d := o + 8
		end := d + n + n&1
		if !fourCC.MatchString(t) || end > len(b) || t == "ANIM" || t == "ANMF" {
			return 0, 0, false
		}
		switch {
		case o == 12 && t == "VP8 ":
			if n < 10 || b[d+3] != 0x9d || b[d+4] != 0x01 || b[d+5] != 0x2a {
				return 0, 0, false
			}
			w, h, bits = int(binary.LittleEndian.Uint16(b[d+6:])&0x3fff), int(binary.LittleEndian.Uint16(b[d+8:])&0x3fff), 1
		case o == 12 && t == "VP8L":
			if n < 5 || b[d] != 0x2f {
				return 0, 0, false
			}
			v := binary.LittleEndian.Uint32(b[d+1:])
			w, h, bits = int(v&0x3fff)+1, int((v>>14)&0x3fff)+1, 1
		case o == 12 && t == "VP8X":
			if n != 10 || b[d]&0x02 != 0 {
				return 0, 0, false
			}
			w = int(b[d+4]) | int(b[d+5])<<8 | int(b[d+6])<<16 + 1
			h = int(b[d+7]) | int(b[d+8])<<8 | int(b[d+9])<<16 + 1
		case o == 12, t == "VP8X":
			return 0, 0, false
		case t == "VP8 " || t == "VP8L":
			if bits++; bits > 1 {
				return 0, 0, false
			}
		}
		o = end
	}
	return w, h, bits == 1 && w > 0 && h > 0
}

func parseLinks(b []byte) (map[string]string, error) {
	t := bytes.TrimLeft(b, " \t\r\n")
	if !utf8.Valid(b) || len(t) == 0 || t[0] != '{' {
		return nil, errors.New("the links are not a UTF-8 JSON object")
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("the links are not a JSON object: %w", err)
	}
	out := map[string]string{}
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("link %q is not a string", k)
		}
		if err := checkLink(k, s); err != nil {
			return nil, err
		}
		out[k] = s
	}
	return out, nil
}

// checkLink: https on a plain ASCII host, no user name, port, xn-- label, IP address, attribute-breaking character
// or stray %; X, Telegram, Discord and GitHub on their own hosts exactly.
func checkLink(kind, s string) error {
	hosts, platform := platformHosts[kind]
	if kind != "website" && !platform {
		return fmt.Errorf("unknown link %q: website, x, telegram, discord or github", kind)
	}
	m := urlRe.FindStringSubmatch(s)
	if len(s) > 200 || m == nil || strings.ContainsAny(s, "\"'<>\\`") || strayPercent(s) {
		return fmt.Errorf("%s: %q is not an https link this app shows", kind, s)
	}
	host := strings.ToLower(m[1])
	labels := strings.Split(host, ".")
	if len(labels) < 2 || digits.MatchString(labels[len(labels)-1]) {
		return fmt.Errorf("%s: %q is not on a domain name", kind, s)
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) || strings.HasPrefix(l, "xn--") {
			return fmt.Errorf("%s: %q is not on a plain ASCII domain", kind, s)
		}
	}
	if platform {
		found := false
		for _, h := range hosts {
			found = found || h == host
		}
		if !found {
			return fmt.Errorf("%s: %q is not on %s", kind, s, strings.Join(hosts, ", "))
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || strings.ToLower(u.Hostname()) != host {
		return fmt.Errorf("%s: %q does not parse as that host", kind, s)
	}
	return nil
}

func strayPercent(s string) bool {
	hex := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && !(i+2 < len(s) && hex(s[i+1]) && hex(s[i+2])) {
			return true
		}
	}
	return false
}
