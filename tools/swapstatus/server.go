package swapstatus

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Path is the one resource a companion serves.
const Path = "/v1/status"

// Server is a companion's -status endpoint (docs/SWAP_STATUS.md §2-3): on 127.0.0.1 only, behind a Host check and a
// one-time pairing code, serving the last answer the companion published and nothing else. No request can change
// anything: the only way in is Publish, from the companion itself.
type Server struct {
	port  int
	hosts [2]string
	code  string
	auth  []byte // "Bearer " + code
	ln    net.Listener
	srv   *http.Server

	mu   sync.Mutex
	body []byte // the last valid answer, marshalled; nil until the first
}

// ParseAddr is the -status flag's rule: exactly 127.0.0.1:PORT, with PORT from 1024 to 65535 (the ports the app's
// pairing form takes). Every other host — localhost, 0.0.0.0, ::1, a LAN address, a name — is refused: the status is
// for this machine only, and "localhost" is a name that a hosts file can point elsewhere.
func ParseAddr(addr string) (int, error) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("-status %q is not 127.0.0.1:PORT: %v", addr, err)
	}
	if host != "127.0.0.1" {
		return 0, fmt.Errorf("-status %q: the status is served on 127.0.0.1 only, so that nothing but this machine can reach it; any other host is refused", addr)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1024 || port > 65535 || strconv.Itoa(port) != p {
		return 0, fmt.Errorf("-status %q: the port must be a number from 1024 to 65535", addr)
	}
	return port, nil
}

// NewCode is a pairing code: 16 bytes from the operating system's generator, as 32 lower-case hex characters.
func NewCode() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("swapstatus: no pairing code: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Listen binds addr, which ParseAddr must accept, with a fresh pairing code, and starts serving. It fails closed: a
// bind that is refused, or a listener that turns out not to be on the loopback address, serves nothing.
func Listen(addr string) (*Server, error) {
	port, err := ParseAddr(addr)
	if err != nil {
		return nil, err
	}
	code, err := NewCode()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("-status %s: %w", addr, err)
	}
	if ta, ok := ln.Addr().(*net.TCPAddr); !ok || !ta.IP.IsLoopback() || ta.Port != port {
		ln.Close()
		return nil, fmt.Errorf("-status %s: the listener is on %s, not the loopback address asked for", addr, ln.Addr())
	}
	s := newServer(port, code)
	s.ln = ln
	s.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
		// a malformed request is not the operator's business, and its headers are not written anywhere
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

func newServer(port int, code string) *Server {
	p := strconv.Itoa(port)
	return &Server{port: port, hosts: [2]string{"127.0.0.1:" + p, "localhost:" + p}, code: code, auth: []byte("Bearer " + code)}
}

// Code is the pairing code, for the companion to print once to its own terminal. It is never written to a file or a
// log by this package.
func (s *Server) Code() string { return s.code }

// Addr is where the status is served.
func (s *Server) Addr() string { return s.hosts[0] }

// Announcement is the one message that shows the pairing code: where the status is, the code, and what it is for.
func (s *Server) Announcement() string {
	return fmt.Sprintf("-status: this swap's stages are served to the DMZ app on this machine at http://%s%s. Pairing code: %s\n"+
		"  Type the port %d and this code into the swap screen's \"Pair with your swap program\" form. The code is new each time this\n"+
		"  program starts and is shown only here; without it the address answers nothing. The answer never carries a key or a secret.",
		s.hosts[0], Path, s.code, s.port)
}

// Close stops serving.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Close()
}

// Publish makes p the answer. It is validated first, and an answer that breaks the contract is not served: the last
// valid one is dropped too, so the app is never shown a stale stage as the current one, and the error says why.
func (s *Server) Publish(p Payload) error {
	err := p.Validate()
	var b []byte
	if err == nil {
		b, err = json.Marshal(p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.body = nil
		return err
	}
	s.body = b
	return nil
}

// ServeHTTP answers in the order of docs/SWAP_STATUS.md §3, stopping at the first failure: the Host, the preflight,
// the method and path, the code. Every refusal has no body.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// 1. DNS rebinding: a page elsewhere that points its own name at 127.0.0.1 still sends its own name
	if r.Host != s.hosts[0] && r.Host != s.hosts[1] {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// the credential is a header, not a cookie, so a wildcard origin gives a page without the code nothing
	h.Set("Access-Control-Allow-Origin", "*")
	// 2. the preflight: the headers a browser needs to send the code, and no swap data
	if r.Method == http.MethodOptions {
		h.Set("Access-Control-Allow-Methods", "GET")
		h.Set("Access-Control-Allow-Headers", "Authorization")
		h.Set("Access-Control-Max-Age", "600")
		if r.Header.Get("Access-Control-Request-Private-Network") != "" {
			h.Set("Access-Control-Allow-Private-Network", "true")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet || r.URL.Path != Path || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// 3. the code, compared in constant time; exactly one Authorization header
	got := r.Header.Values("Authorization")
	if len(got) != 1 || subtle.ConstantTimeCompare([]byte(got[0]), s.auth) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	body := s.body
	s.mu.Unlock()
	if body == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// 4. the answer
	h.Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
