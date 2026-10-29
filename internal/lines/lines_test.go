package lines

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadLines(t *testing.T) {
	r := New(strings.NewReader("one\r\ntwo\nthree"))
	for _, want := range []string{"one", "two", "three"} {
		got, err := r.Read(context.Background())
		if err != nil || got != want {
			t.Fatalf("Read = %q, %v; want %q", got, err, want)
		}
	}
	for range 2 {
		if _, err := r.Read(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("after the end of input Read = %v, want io.EOF", err)
		}
	}
}

// Input is not read until someone asks for a line: hidden input of secrets reads the
// terminal past Reader, and a read behind its back would take its line.
func TestNoReadUntilAsked(t *testing.T) {
	src := &countingReader{r: strings.NewReader("line\n")}
	r := New(src)
	time.Sleep(20 * time.Millisecond)
	if src.calls != 0 {
		t.Fatalf("input read before the first Read: %d times", src.calls)
	}
	if got, err := r.Read(context.Background()); err != nil || got != "line" {
		t.Fatalf("Read = %q, %v", got, err)
	}
}

type countingReader struct {
	r     io.Reader
	calls int
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.calls++
	return c.r.Read(p)
}

// A cancelled call does not abandon the read: the next one gets the line.
func TestCancelledReadPassesLineOn(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := New(pr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read with a cancelled ctx = %v", err)
	}

	got := make(chan string, 1)
	go func() {
		line, _ := r.Read(context.Background())
		got <- line
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := io.WriteString(pw, "answer\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-got:
		if line != "answer" {
			t.Fatalf("second Read = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second Read got no line")
	}
}

// A line that came when nobody was waiting for it is thrown away by DropStale; the end
// of input is not lost meanwhile.
func TestDropStale(t *testing.T) {
	pr, pw := io.Pipe()
	r := New(pr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = r.Read(ctx)
	if _, err := io.WriteString(pw, "earlier answer\n"); err != nil {
		t.Fatal(err)
	}
	waitDone(t, r)
	r.DropStale()

	go func() {
		_, _ = io.WriteString(pw, "new answer\n")
		pw.Close()
	}()
	if got, err := r.Read(context.Background()); err != nil || got != "new answer" {
		t.Fatalf("Read = %q, %v; want the new answer", got, err)
	}

	// The abandoned read ran into the end of input: DropStale remembers it.
	_, _ = r.Read(ctx)
	waitDone(t, r)
	r.DropStale()
	if _, err := r.Read(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("after the end of input Read = %v", err)
	}
}

// DropStale does not touch a read still in progress: its line will be typed in answer to
// the new question.
func TestDropStaleKeepsPendingRead(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := New(pr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = r.Read(ctx)
	r.DropStale()

	got := make(chan string, 1)
	go func() {
		line, _ := r.Read(context.Background())
		got <- line
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := io.WriteString(pw, "fresh\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-got:
		if line != "fresh" {
			t.Fatalf("Read = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read got no line")
	}
}

// Two wait at the same time: one gets the line, the other waits for the next one.
func TestConcurrentReaders(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	r := New(pr)

	got := make(chan string, 2)
	for range 2 {
		go func() {
			line, err := r.Read(context.Background())
			if err == nil {
				got <- line
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := io.WriteString(pw, "a\nb\n"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case line := <-got:
			seen[line] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("not both lines arrived: %v", seen)
		}
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("lines: %v", seen)
	}
}

func TestReadError(t *testing.T) {
	boom := errors.New("failure")
	r := New(io.MultiReader(strings.NewReader("part"), errReader{boom}))
	if _, err := r.Read(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Read = %v, want the read error", err)
	}
	if _, err := r.Read(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("after the error Read = %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// waitDone waits until the abandoned read finishes.
func waitDone(t *testing.T, r *Reader) {
	t.Helper()
	r.mu.Lock()
	cur := r.cur
	r.mu.Unlock()
	if cur == nil {
		t.Fatal("no read")
	}
	select {
	case <-cur.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not finish")
	}
}
