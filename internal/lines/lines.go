// Package lines is the shared reader of input lines (stdin) for everyone who waits for a
// person's answer: CLI questions and the pause before exit.
//
// There is one reader per process. A read from stdin cannot be interrupted: a goroutine
// that started Read for a question whose time ran out keeps hanging on it, and a second
// reader of the same stream would get lines interleaved with it. So an unfinished read is
// not abandoned here but stays shared: the line it brings goes to the next one who asks.
//
// Reading happens only on request: while nobody waits for a line, stdin is not touched.
// Hidden input of secrets relies on this — it reads the terminal past Reader
// (golang.org/x/term), and nobody may read the same terminal at the same time. While
// Reader has not started reading, or the last read ended with an answer, there is no
// such reader.
package lines

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
)

// Reader is the shared line reader.
type Reader struct {
	br *bufio.Reader

	mu sync.Mutex
	// cur is the line read: in progress, or finished but nobody has taken the line yet.
	// nil means there is no read.
	cur *read
	// err means the input ended (io.EOF) or broke: there will be no more lines.
	err error
}

// read is one line read. line and err are written before close(done).
type read struct {
	done chan struct{}
	line string
	err  error
}

// New returns a line reader from r.
func New(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r)}
}

// Read waits for a line or for ctx to be cancelled and returns the line without the line
// break. The end of input is io.EOF: a last line without a line break is still returned,
// and the next call gets io.EOF. A cancelled call does not abandon the read: the line
// goes to the next call (or is thrown away by DropStale).
func (r *Reader) Read(ctx context.Context) (string, error) {
	for {
		r.mu.Lock()
		if r.err != nil {
			err := r.err
			r.mu.Unlock()
			return "", err
		}
		cur := r.cur
		if cur == nil {
			cur = r.start()
		}
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-cur.done:
		}

		r.mu.Lock()
		if r.cur != cur {
			// Another call took the line while this one was waking up.
			r.mu.Unlock()
			continue
		}
		r.cur = nil
		if cur.err != nil {
			r.err = cur.err
		}
		r.mu.Unlock()

		line := strings.TrimRight(cur.line, "\r\n")
		if cur.err != nil && (line == "" || !errors.Is(cur.err, io.EOF)) {
			return "", cur.err
		}
		return line, nil
	}
}

// DropStale throws away the line brought by a read left behind by an earlier call: it
// was typed when nobody was waiting for an answer — it is the answer to the earlier
// question. The end of input is remembered meanwhile.
func (r *Reader) DropStale() {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := r.cur
	if cur == nil {
		return
	}
	select {
	case <-cur.done:
		r.cur = nil
		if cur.err != nil {
			r.err = cur.err
		}
	default:
		// No line yet: it will be typed in answer to the new question.
	}
}

// start begins a line read. Under mu. One goroutine reads at a time: a new one starts
// only after the previous one has finished.
func (r *Reader) start() *read {
	cur := &read{done: make(chan struct{})}
	r.cur = cur
	go func() {
		cur.line, cur.err = r.br.ReadString('\n')
		close(cur.done)
	}()
	return cur
}
