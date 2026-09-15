package coord

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPStore is a relay reached over HTTP or HTTPS. Plain HTTP is safe for integrity and payload confidentiality —
// every message is signed and encrypted end to end — but it shows a network observer when signers talk and how
// much; use HTTPS or a tunnel across any network you do not control. Redirects are refused (a relay has no business
// sending a signer's requests elsewhere); a proxy is taken from the environment (HTTPS_PROXY, including socks5://).
type HTTPStore struct {
	base    string
	name    string
	client  *http.Client
	maxSkew time.Duration
	now     func() time.Time
}

// NewHTTPStore returns the relay at rawURL (http or https, an optional path prefix, no credentials, query or
// fragment). timeout bounds each request; maxSkew, when positive, makes Health report a clock that differs from
// the relay's by more than half of it.
func NewHTTPStore(rawURL string, timeout, maxSkew time.Duration) (*HTTPStore, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("coord: relay %q must be an http:// or https:// URL", clip(rawURL, 80))
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("coord: relay %s: no credentials, query or fragment in a relay URL", u.Host)
	}
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxResponseHeaderBytes = 64 << 10
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("coord: relays may not redirect")
		},
	}
	base := u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/")
	return &HTTPStore{base: base, name: u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/"), client: client, maxSkew: maxSkew, now: time.Now}, nil
}

func (s *HTTPStore) Name() string { return s.name }

// CloseIdle drops idle connections.
func (s *HTTPStore) CloseIdle() { s.client.CloseIdleConnections() }

func (s *HTTPStore) Put(ctx context.Context, m Stored) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.base+"/v1/m", bytes.NewReader(m.Raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", s.name, err)
	}
	defer drain(resp.Body)
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("%s refused the message: %s %s", s.name, resp.Status, shortBody(resp.Body))
}

func (s *HTTPStore) Get(ctx context.Context, vault, tag [32]byte, maxCount, maxBytes int) ([][]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/v1/m/"+hex.EncodeToString(vault[:])+"/"+hex.EncodeToString(tag[:]), nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}
	defer drain(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s %s", s.name, resp.Status, shortBody(resp.Body))
	}
	out, err := readFrames(resp.Body, maxCount, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}
	return out, nil
}

// Health asks the relay whether it is up, and compares its clock with ours: a node whose clock is off by more than
// the skew window has its messages refused as dated in the future, or refuses everyone else's as expired.
func (s *HTTPStore) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/v1/health", nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", s.name, err)
	}
	defer drain(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", s.name, resp.Status)
	}
	if s.maxSkew > 0 {
		if d, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
			diff := s.now().Sub(d)
			if diff < 0 {
				diff = -diff
			}
			if diff > s.maxSkew/2 {
				return fmt.Errorf("%s: this host's clock differs from the relay's by %s (messages are refused beyond %s): check time synchronisation", s.name, diff.Round(time.Second), s.maxSkew)
			}
		}
	}
	return nil
}

// readFrames reads (uint32 length | message)* from r, refusing a zero or oversized length, more than maxCount frames
// and a truncated frame. It allocates one message at a time, so a hostile relay cannot make it hold more than
// maxCount × maxBytes.
func readFrames(r io.Reader, maxCount, maxBytes int) ([][]byte, error) {
	var out [][]byte
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, errors.New("a truncated frame header")
		}
		n := int(binary.BigEndian.Uint32(hdr[:]))
		if n == 0 || n > maxBytes {
			return nil, fmt.Errorf("a frame of %d bytes (limit %d)", n, maxBytes)
		}
		if len(out) >= maxCount {
			return nil, fmt.Errorf("more than %d messages for one round", maxCount)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, errors.New("a truncated frame")
		}
		out = append(out, buf)
	}
}

func drain(b io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(b, 64<<10))
	b.Close()
}

// shortBody is at most 200 printable bytes of a refusal, for the log.
func shortBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 200))
	return strings.Map(func(c rune) rune {
		if c < 0x20 || c > 0x7e {
			return ' '
		}
		return c
	}, strings.TrimSpace(string(b)))
}
