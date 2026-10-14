// Package logbuf keeps the last lines of the process's log in memory, with the secrets cut
// out, for the panel's Diagnostics page and for support: the admin of a server does not need
// SSH to show what the panel or a node said.
package logbuf

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultLines is how many lines the buffer keeps.
const DefaultLines = 2000

// Entry is one line of the log.
type Entry struct {
	At    time.Time
	Level string
	// Text is the whole line as slog's text handler writes it, redacted.
	Text string
}

// Buffer is a ring of the last lines. Its zero value is not ready: use New.
type Buffer struct {
	mu   sync.Mutex
	ring []Entry
	next int
	full bool
	// cur is the record being written; recWriter reads it with mu held.
	cur Entry
}

// New makes a buffer for lines lines (DefaultLines when not positive).
func New(lines int) *Buffer {
	if lines <= 0 {
		lines = DefaultLines
	}
	return &Buffer{ring: make([]Entry, lines)}
}

// Tail returns the last n lines, oldest first; n beyond what is kept (or not positive)
// returns all of them.
func (b *Buffer) Tail(n int) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	have := b.next
	if b.full {
		have = len(b.ring)
	}
	if n <= 0 || n > have {
		n = have
	}
	out := make([]Entry, 0, n)
	for i := have - n; i < have; i++ {
		idx := i
		if b.full {
			idx = (b.next + i) % len(b.ring)
		}
		out = append(out, b.ring[idx])
	}
	return out
}

// Len is how many lines are kept.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.full {
		return len(b.ring)
	}
	return b.next
}

// add stores one finished line with the metadata of the record being handled.
func (b *Buffer) add(line []byte) {
	e := b.cur
	e.Text = Redact(string(bytes.TrimRight(line, "\r\n")))
	b.ring[b.next] = e
	b.next++
	if b.next == len(b.ring) {
		b.next, b.full = 0, true
	}
}

// recWriter is where the text handler writes; Handle holds b.mu around it.
type recWriter struct{ b *Buffer }

func (w recWriter) Write(p []byte) (int, error) {
	w.b.add(p)
	return len(p), nil
}

// Handler passes every record to next (which may be nil) and keeps a redacted copy of the
// ones at Info and above, or at whatever next takes when that is lower.
func (b *Buffer) Handler(next slog.Handler) slog.Handler {
	return &handler{next: next, buf: b, rec: slog.NewTextHandler(recWriter{b}, &slog.HandlerOptions{Level: slog.LevelDebug})}
}

type handler struct {
	next slog.Handler
	buf  *Buffer
	rec  slog.Handler
}

func (h *handler) nextOn(ctx context.Context, l slog.Level) bool {
	return h.next != nil && h.next.Enabled(ctx, l)
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelInfo || h.nextOn(ctx, l)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	on := h.nextOn(ctx, r.Level)
	if on {
		err = h.next.Handle(ctx, r.Clone())
	}
	if on || r.Level >= slog.LevelInfo {
		h.buf.mu.Lock()
		h.buf.cur = Entry{At: r.Time, Level: r.Level.String()}
		if h.buf.cur.At.IsZero() {
			h.buf.cur.At = time.Now()
		}
		_ = h.rec.Handle(ctx, r)
		h.buf.mu.Unlock()
	}
	return err
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := &handler{buf: h.buf, rec: h.rec.WithAttrs(attrs)}
	if h.next != nil {
		n.next = h.next.WithAttrs(attrs)
	}
	return n
}

func (h *handler) WithGroup(name string) slog.Handler {
	n := &handler{buf: h.buf, rec: h.rec.WithGroup(name)}
	if h.next != nil {
		n.next = h.next.WithGroup(name)
	}
	return n
}
