package swapstatus

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Linger is how long a companion keeps serving its last answer once the swap has ended, so that a screen asking every
// 10 s reads the outcome; it stops sooner when the program is stopped.
const Linger = 10 * time.Minute

// Feed is what a companion's command line does with -status: serve on the address the flag names, publish the answer
// after every pass of the swap, and keep the last one served for a while after the end. A nil Feed is -status off,
// and every method on it does nothing.
type Feed struct {
	srv    *Server
	answer func() Payload
	logf   func(string, ...interface{})
	said   string
}

// Start serves on addr ("" is off: no Feed, no error). The pairing code is written once, to w — the companion's own
// terminal — and nowhere else. answer is called only from the goroutine that calls Publish, the one that runs the swap.
func Start(addr string, answer func() Payload, w io.Writer, logf func(string, ...interface{})) (*Feed, error) {
	if addr == "" {
		return nil, nil
	}
	srv, err := Listen(addr)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(w, srv.Announcement())
	return &Feed{srv: srv, answer: answer, logf: logf}, nil
}

// Publish serves the swap's answer as it stands now. An answer the contract refuses is not served, and why is said
// once.
func (f *Feed) Publish() {
	if f == nil {
		return
	}
	err := f.srv.Publish(f.answer())
	why := ""
	if err != nil {
		why = err.Error()
	}
	if why != f.said {
		f.said = why
		if why != "" && f.logf != nil {
			f.logf("-status: this pass's answer is not served: %s", why)
		}
	}
}

// Linger publishes the final answer and keeps serving it for d, or until ctx ends.
func (f *Feed) Linger(ctx context.Context, d time.Duration) {
	if f == nil {
		return
	}
	f.Publish()
	if f.logf != nil {
		f.logf("-status: the swap has ended; its outcome stays served at %s for %v, or until you stop this program", f.srv.Addr(), d)
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// Close stops serving.
func (f *Feed) Close() {
	if f != nil {
		f.srv.Close()
	}
}
