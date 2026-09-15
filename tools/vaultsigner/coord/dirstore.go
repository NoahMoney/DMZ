package coord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DirStore is a shared directory: a synchronised folder, a network share, or removable media carried between
// air-gapped hosts. Layout: <root>/<vault hex>/<tag hex>/<sender key hex>.<created>.<expires>.msg, each written under
// a temporary name and renamed into place. Anyone who can write the directory can delete or add files; that costs
// liveness only, because every file is re-verified as untrusted input. The root must already exist: a missing root
// usually means the share is not mounted, and silently writing to a local folder of the same name would partition
// this signer from the others.
type DirStore struct {
	root      string
	now       func() time.Time
	mu        sync.Mutex
	lastSweep time.Time
}

const (
	dirMaxEntries    = 4096 // entries read from one round directory
	dirPerSender     = 3    // newest candidates tried per sender
	dirSweepInterval = 10 * time.Minute
	msgNameLen       = 64 + 1 + 20 + 1 + 20 + 4
)

// NewDirStore returns the store rooted at root.
func NewDirStore(root string) (*DirStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("coord: a directory store needs a path")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &DirStore{root: abs, now: time.Now}, nil
}

func (d *DirStore) Name() string { return "dir:" + d.root }

func (d *DirStore) checkRoot() error {
	st, err := os.Stat(d.root)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%s: the shared directory is missing (not mounted?)", d.Name())
	}
	return nil
}

func msgName(sender [32]byte, created, expires int64) string {
	return fmt.Sprintf("%x.%020d.%020d.msg", sender[:], created, expires)
}

func parseMsgName(n string) (sender [32]byte, created, expires int64, ok bool) {
	if len(n) != msgNameLen || !strings.HasSuffix(n, ".msg") || n[64] != '.' || n[85] != '.' {
		return sender, 0, 0, false
	}
	if _, err := hex.Decode(sender[:], []byte(n[:64])); err != nil {
		return sender, 0, 0, false
	}
	c, err1 := strconv.ParseInt(n[65:85], 10, 64)
	e, err2 := strconv.ParseInt(n[86:106], 10, 64)
	if err1 != nil || err2 != nil {
		return sender, 0, 0, false
	}
	return sender, c, e, true
}

func (d *DirStore) roundDir(vault, tag [32]byte) string {
	return filepath.Join(d.root, hex.EncodeToString(vault[:]), hex.EncodeToString(tag[:]))
}

func (d *DirStore) Put(ctx context.Context, m Stored) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.checkRoot(); err != nil {
		return err
	}
	dir := d.roundDir(m.Vault, m.Tag)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	name := msgName(m.Sender, m.Created, m.Expires)
	var r [6]byte
	if _, err := rand.Read(r[:]); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".tmp-"+hex.EncodeToString(r[:]))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(m.Raw)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return err
	}
	// one copy per sender per round: remove this sender's older files
	for _, e := range d.list(dir) {
		if s, c, _, ok := parseMsgName(e); ok && s == m.Sender && c < m.Created {
			os.Remove(filepath.Join(dir, e))
		}
	}
	d.maybeSweep(m.Vault)
	return nil
}

// list returns at most dirMaxEntries names from dir, so a directory flooded with files costs bounded work.
func (d *DirStore) list(dir string) []string {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	names, _ := f.Readdirnames(dirMaxEntries)
	return names
}

func (d *DirStore) Get(ctx context.Context, vault, tag [32]byte, maxCount, maxBytes int) ([][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.checkRoot(); err != nil {
		return nil, err
	}
	dir := d.roundDir(vault, tag)
	now := d.now().UnixNano()
	type cand struct {
		name    string
		created int64
	}
	by := map[[32]byte][]cand{}
	for _, n := range d.list(dir) {
		s, c, e, ok := parseMsgName(n)
		if !ok {
			continue
		}
		if e <= now {
			os.Remove(filepath.Join(dir, n)) // expired: nobody may use it, so anybody may delete it
			continue
		}
		by[s] = append(by[s], cand{n, c})
	}
	senders := make([][32]byte, 0, len(by))
	for s := range by {
		senders = append(senders, s)
	}
	sort.Slice(senders, func(i, j int) bool { return hex.EncodeToString(senders[i][:]) < hex.EncodeToString(senders[j][:]) })
	var out [][]byte
	for _, s := range senders {
		cs := by[s]
		sort.Slice(cs, func(i, j int) bool { return cs[i].created > cs[j].created })
		if len(cs) > dirPerSender {
			cs = cs[:dirPerSender]
		}
		for _, c := range cs {
			if len(out) >= maxCount {
				return out, nil
			}
			if b := readRegular(filepath.Join(dir, c.name), maxBytes); b != nil {
				out = append(out, b)
			}
		}
	}
	return out, nil
}

// readRegular reads a regular file of at most maxBytes; a link, a directory or anything larger reads as nothing.
func readRegular(path string, maxBytes int) []byte {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > int64(maxBytes) || st.Size() == 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil || len(b) > maxBytes {
		return nil
	}
	return b
}

func (d *DirStore) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.checkRoot(); err != nil {
		return err
	}
	var r [6]byte
	if _, err := rand.Read(r[:]); err != nil {
		return err
	}
	probe := filepath.Join(d.root, ".probe-"+hex.EncodeToString(r[:]))
	f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%s: not writable: %w", d.Name(), err)
	}
	f.Close()
	return os.Remove(probe)
}

// maybeSweep removes expired messages, abandoned temporary files and empty round directories under one vault, at
// most once per dirSweepInterval.
func (d *DirStore) maybeSweep(vault [32]byte) {
	d.mu.Lock()
	now := d.now()
	if now.Sub(d.lastSweep) < dirSweepInterval {
		d.mu.Unlock()
		return
	}
	d.lastSweep = now
	d.mu.Unlock()
	vdir := filepath.Join(d.root, hex.EncodeToString(vault[:]))
	for _, t := range d.list(vdir) {
		rd := filepath.Join(vdir, t)
		if st, err := os.Lstat(rd); err != nil || !st.IsDir() {
			continue
		}
		for _, n := range d.list(rd) {
			p := filepath.Join(rd, n)
			if _, _, e, ok := parseMsgName(n); ok && e <= now.UnixNano() {
				os.Remove(p)
			} else if strings.HasPrefix(n, ".tmp-") {
				if st, err := os.Lstat(p); err == nil && now.Sub(st.ModTime()) > time.Hour {
					os.Remove(p)
				}
			}
		}
		os.Remove(rd) // succeeds only when empty
	}
}
