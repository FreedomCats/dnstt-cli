package datapath

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"

	"github.com/FreedomCats/dnstt-cli/internal/exit"
)

// The stand of the TUN latency diagnosis. One HTTP/1.1 handler on loopback serves a fixed
// body; one exit sends every stream there through a shaper of its own, whatever the
// destination; the same client is driven through the real adapter, through the SOCKS5
// datapath and through the exit alone. The question is one of numbers: does the local path
// (the gVisor stack, the fdbased adapter, its queues) cost what the tunnel costs? The
// shaped exit is lossless, so a lost or retransmitted packet is the local path's.
const (
	// latencySmallBody and latencyLargeBody are the two page sizes of the stand.
	latencySmallBody = 2 << 10
	latencyLargeBody = 128 << 10

	// latencyChunk is the piece the shaper passes on at once, and the most it can be early
	// with the first one. The pipe between the shaper and the caller never holds more than
	// one chunk: the stand is a wire, not a buffer.
	latencyChunk = 4 << 10

	// latencyOneRTT is the round trip of the shaped exit: 50 ms one way, twice. The TUN
	// path may add nothing beyond it on top of the SOCKS5 path's own time.
	latencyOneRTT = 100 * time.Millisecond

	// latencyControlSlack is the allowance of the unshaped control: at a total of a few
	// milliseconds the relative ten percent says nothing, and the absolute offset under
	// load — a binary built with -race doubles the CPU cost of the stack — reaches about
	// 15 ms. The allowance stays far below the 100 ms round trip of the shaped wire, so it
	// still catches a local path that costs seconds.
	latencyControlSlack = 25 * time.Millisecond

	// latencyFetchTimeout bounds one fetch; the shaped 128 KiB takes about two seconds.
	latencyFetchTimeout = 60 * time.Second

	// latencyBudget bounds the whole test: eight cells of at most a few seconds each.
	latencyBudget = 2 * time.Minute
)

// latencyDestination is where the client asks to go: an address of the documentation range
// that the adapter's /1 routes carry into the tunnel. The exit replaces it with the
// loopback handler, so nothing leaves the container, and loopback keeps its own route.
var latencyDestination = netip.MustParseAddrPort("203.0.113.10:80")

// latencyShape is what the exit does to every stream: the one-way delay the first byte of
// each direction waits and the rate the rest is passed on at, per stream and per direction.
// 0 ms and 0 bytes per second is the control.
type latencyShape struct {
	name           string
	oneWay         time.Duration
	bytesPerSecond int
}

var latencyShapes = []latencyShape{
	{name: "shaped", oneWay: 50 * time.Millisecond, bytesPerSecond: 64 << 10},
	{name: "control"},
}

// latencyBodies are the bodies of the handler, keyed by path. The pattern repeats only every
// 256 bytes, as in the other tests of the package, so a lost, doubled or swapped block shows
// up in the comparison.
func latencyBodies() map[string][]byte {
	return map[string][]byte{
		"/" + strconv.Itoa(latencySmallBody): payload(latencySmallBody),
		"/" + strconv.Itoa(latencyLargeBody): payload(latencyLargeBody),
	}
}

// latencyHandler serves a fixed body with Connection: close: one fetch is one connection,
// as a page fetch is.
func latencyHandler(t *testing.T, bodies map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range bodies {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Header().Set("Connection", "close")
			_, _ = w.Write(body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// latencyStream is one stream of the exit through its phases: the dial that the forwarder
// started, the answer of the exit, the first byte the relay read back and the end of the
// relay, which closes the stream.
type latencyStream struct {
	target string

	mu         sync.Mutex
	dialStart  time.Time
	dialReturn time.Time
	firstByte  time.Time
	relayEnd   time.Time

	firstOnce sync.Once
	endOnce   sync.Once
}

func (s *latencyStream) set(field *time.Time) {
	s.mu.Lock()
	*field = time.Now()
	s.mu.Unlock()
}

// snapshot returns the four times under the lock: the relay records the last two from its
// own goroutine, while the test reads them.
func (s *latencyStream) snapshot() (start, returned, first, end time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dialStart, s.dialReturn, s.firstByte, s.relayEnd
}

// latencyExit is the exit of the stand. Every stream goes to the one loopback handler
// wrapped in a shaper of its own; the destination of every dial is recorded, so the number
// of streams per fetch is measured, not assumed.
type latencyExit struct {
	handler string // the loopback handler every stream is sent to

	mu      sync.Mutex
	shape   latencyShape
	streams []*latencyStream
}

var _ exit.Dialer = (*latencyExit)(nil)

func (e *latencyExit) setShape(shape latencyShape) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shape = shape
}

func (e *latencyExit) Dial(ctx context.Context, address string) (net.Conn, error) {
	stream := &latencyStream{target: address}
	stream.set(&stream.dialStart)
	e.mu.Lock()
	shape := e.shape
	e.streams = append(e.streams, stream)
	e.mu.Unlock()

	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", e.handler)
	stream.set(&stream.dialReturn)
	if err != nil {
		return nil, err
	}
	return &latencyConn{Conn: newShapedConn(raw, shape), stream: stream}, nil
}

func (e *latencyExit) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.streams)
}

func (e *latencyExit) streamsSince(n int) []*latencyStream {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.streams[n:])
}

// latencyConn records the two phases that only the relay sees: the first byte it reads from
// the exit and the end of the relay.
type latencyConn struct {
	net.Conn
	stream *latencyStream
}

func (c *latencyConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.stream.firstOnce.Do(func() { c.stream.set(&c.stream.firstByte) })
	}
	return n, err
}

func (c *latencyConn) Close() error {
	c.stream.endOnce.Do(func() { c.stream.set(&c.stream.relayEnd) })
	return c.Conn.Close()
}

// shapedConn is a stream with the shape of the tunnel: the first byte of each direction
// waits oneWay, and every chunk then takes its time at bytesPerSecond. It neither loses,
// duplicates nor reorders a byte: one goroutine per direction passes the data on in order,
// and a synchronous pipe bounds what is held between the caller and that goroutine.
type shapedConn struct {
	raw            net.Conn
	oneWay         time.Duration
	bytesPerSecond int

	upR   *io.PipeReader // the caller's writes arrive here; the up pump reads them
	upW   *io.PipeWriter
	downR *io.PipeReader
	downW *io.PipeWriter // the down pump writes here; the caller reads it

	closeOnce sync.Once
	closeErr  error
	done      sync.WaitGroup
}

var _ net.Conn = (*shapedConn)(nil)

func newShapedConn(raw net.Conn, shape latencyShape) *shapedConn {
	c := &shapedConn{raw: raw, oneWay: shape.oneWay, bytesPerSecond: shape.bytesPerSecond}
	c.upR, c.upW = io.Pipe()
	c.downR, c.downW = io.Pipe()
	c.done.Add(2)
	go c.pumpUp()
	go c.pumpDown()
	return c
}

// pumpUp passes the caller's writes on: the upload direction of the exit's stream.
func (c *shapedConn) pumpUp() {
	defer c.done.Done()
	if err := c.pass(c.upR, c.raw); err != nil {
		c.shutdown(err)
	}
}

// pumpDown passes what the exit sends back to the caller. The end of the stream is passed
// as the end of the pipe, so the caller sees io.EOF as it would from a socket.
func (c *shapedConn) pumpDown() {
	defer c.done.Done()
	err := c.pass(c.raw, c.downW)
	if errors.Is(err, io.EOF) {
		c.downW.Close()
		return
	}
	c.shutdown(err)
}

// pass reads src in chunks and writes them to dst on the schedule of a wire: the first
// chunk waits oneWay, and the last byte of every later chunk is due oneWay plus the bytes
// delivered so far at bytesPerSecond after the first one. The deadlines are absolute, so
// the total of a body does not depend on the size of a socket read, and a reader slower
// than the rate takes its time from the wait instead of adding to it. The bytes are passed
// on in order, one chunk at a time: nothing overtakes anything.
func (c *shapedConn) pass(src io.Reader, dst io.Writer) error {
	buf := make([]byte, latencyChunk)
	var delivered int64
	var start time.Time // the delivery of the first chunk: the zero of the rate
	for {
		n, err := src.Read(buf)
		if n > 0 {
			first := start.IsZero()
			if first {
				if c.oneWay > 0 {
					time.Sleep(c.oneWay)
				}
				start = time.Now()
			}
			delivered += int64(n)
			if !first && c.bytesPerSecond > 0 {
				due := start.Add(time.Duration(delivered * int64(time.Second) / int64(c.bytesPerSecond)))
				if wait := time.Until(due); wait > 0 {
					time.Sleep(wait)
				}
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			return err
		}
	}
}

func (c *shapedConn) Read(b []byte) (int, error)  { return c.downR.Read(b) }
func (c *shapedConn) Write(b []byte) (int, error) { return c.upW.Write(b) }

func (c *shapedConn) Close() error {
	err := c.shutdown(net.ErrClosed)
	c.done.Wait()
	return err
}

// shutdown ends both directions once: the raw stream is closed and the caller's reads and
// writes fail with err. Repeated calls are harmless.
func (c *shapedConn) shutdown(err error) error {
	c.closeOnce.Do(func() {
		if cerr := c.raw.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			c.closeErr = cerr
		}
		c.upR.CloseWithError(err)
		c.downR.CloseWithError(err)
	})
	return c.closeErr
}

func (c *shapedConn) LocalAddr() net.Addr                { return c.raw.LocalAddr() }
func (c *shapedConn) RemoteAddr() net.Addr               { return c.raw.RemoteAddr() }
func (c *shapedConn) SetDeadline(t time.Time) error      { return c.raw.SetDeadline(t) }
func (c *shapedConn) SetReadDeadline(t time.Time) error  { return c.raw.SetReadDeadline(t) }
func (c *shapedConn) SetWriteDeadline(t time.Time) error { return c.raw.SetWriteDeadline(t) }

// latencyFetch is one fetch: the time to the response and the time to the whole body.
type latencyFetch struct {
	ttfb  time.Duration
	total time.Duration
}

// latencyFetches drives n fetches at once through dial, all starting together, and returns
// their numbers or the errors joined.
func latencyFetches(n int, dial func(context.Context) (net.Conn, error), path string, want []byte) ([]latencyFetch, error) {
	fetches := make([]latencyFetch, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fetches[i], errs[i] = fetchLatency(dial, path, want)
			if errs[i] != nil {
				errs[i] = fmt.Errorf("fetch %d: %w", i+1, errs[i])
			}
		}()
	}
	close(start)
	wg.Wait()
	return fetches, errors.Join(errs...)
}

// fetchLatency opens one connection, sends the GET and reads the whole response: the body
// must be exactly want, so a loss or a swap is a failure and not a number.
func fetchLatency(dial func(context.Context) (net.Conn, error), path string, want []byte) (latencyFetch, error) {
	var f latencyFetch
	ctx, cancel := context.WithTimeout(context.Background(), latencyFetchTimeout)
	defer cancel()
	start := time.Now()
	conn, err := dial(ctx)
	if err != nil {
		return f, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(latencyFetchTimeout)); err != nil {
		return f, err
	}
	request := "GET " + path + " HTTP/1.1\r\nHost: " + latencyDestination.Addr().String() +
		"\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		return f, fmt.Errorf("request: %w", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		return f, fmt.Errorf("response: %w", err)
	}
	f.ttfb = time.Since(start)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return f, fmt.Errorf("body: %w", err)
	}
	f.total = time.Since(start)
	if resp.StatusCode != http.StatusOK {
		return f, fmt.Errorf("status %s", resp.Status)
	}
	if !bytes.Equal(body, want) {
		return f, fmt.Errorf("body of %d bytes differs from the %d wanted", len(body), len(want))
	}
	return f, nil
}

// socksDial is a program's SOCKS5 client, one message after the previous one, as socks.go
// serves them: the greeting, the CONNECT to the destination and the reply.
func socksDial(ctx context.Context, listen string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", listen)
	if err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(latencyFetchTimeout)
	}
	_ = conn.SetDeadline(deadline)
	fail := func(err error) (net.Conn, error) {
		conn.Close()
		return nil, err
	}

	if _, err := conn.Write([]byte{socksVersion, 1, methodNone}); err != nil {
		return fail(fmt.Errorf("greeting: %w", err))
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return fail(fmt.Errorf("greeting: %w", err))
	}
	if greeting != [2]byte{socksVersion, methodNone} {
		return fail(fmt.Errorf("greeting %v: want SOCKS5 without a login", greeting))
	}

	request := []byte{socksVersion, cmdConnect, 0, atypIPv4}
	request = append(request, latencyDestination.Addr().AsSlice()...)
	request = binary.BigEndian.AppendUint16(request, latencyDestination.Port())
	if _, err := conn.Write(request); err != nil {
		return fail(fmt.Errorf("request: %w", err))
	}
	var reply [10]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return fail(fmt.Errorf("reply: %w", err))
	}
	if reply[0] != socksVersion || reply[1] != repSucceeded {
		return fail(fmt.Errorf("reply %v: the destination was refused", reply[:4]))
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// latencyRun is what the stand measured for one datapath in one cell.
type latencyRun struct {
	name    string
	ttfb    []time.Duration
	total   []time.Duration
	streams int
}

func newLatencyRun(name string, fetches []latencyFetch, streams int) latencyRun {
	r := latencyRun{name: name, streams: streams}
	for _, f := range fetches {
		r.ttfb = append(r.ttfb, f.ttfb)
		r.total = append(r.total, f.total)
	}
	return r
}

func (r latencyRun) ttfbMeanMax() (mean, max time.Duration)  { return meanMax(r.ttfb) }
func (r latencyRun) totalMeanMax() (mean, max time.Duration) { return meanMax(r.total) }

// meanMax is the mean and the worst of the numbers: both are logged, and the comparison uses
// the worst, because the tail is what a live session shows as seconds and timeouts.
func meanMax(d []time.Duration) (mean, max time.Duration) {
	if len(d) == 0 {
		return 0, 0
	}
	max = d[0]
	var sum time.Duration
	for _, v := range d {
		sum += v
		if v > max {
			max = v
		}
	}
	return sum / time.Duration(len(d)), max
}

// phase is at relative to start, with one microsecond, or "-" when it did not happen.
func phase(start, at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	return "+" + at.Sub(start).Round(time.Microsecond).String()
}

// waitStreams waits until the relay of every stream of the run has ended: the exit's stream
// is closed when the relay finishes, and the per-stream timeline ends there.
func waitStreams(t *testing.T, streams []*latencyStream) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		open := 0
		for _, s := range streams {
			if _, _, _, end := s.snapshot(); end.IsZero() {
				open++
			}
		}
		if open == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d streams did not end", open, len(streams))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// stackCounts is one reading of the local path's loss counters and of the adapter's traffic.
// The values are taken here: Stats returns the counter objects, so a snapshot of the struct
// would keep reading the live numbers.
type stackCounts struct {
	retransmits  uint64
	timeouts     uint64
	forwardDrops uint64
	txNoBuffer   uint64
	txPackets    uint64
	rxPackets    uint64
	txBytes      uint64
	rxBytes      uint64
}

// readStackCounts reads the counters the diagnosis names: TCP's retransmits, RTO expiries
// and refused connection requests, and the NIC's TX drops for want of buffer space
// (gvisor.dev/gvisor pkg/tcpip/tcpip.go:2237, :2281, :2453-2458, :2488-2490), together with
// the adapter's packets and bytes.
func readStackCounts(t *testing.T, stats func() tcpip.Stats) stackCounts {
	t.Helper()
	s := stats()
	if s.TCP.Retransmits == nil || s.TCP.Timeouts == nil || s.TCP.ForwardMaxInFlightDrop == nil ||
		s.NICs.TxPacketsDroppedNoBufferSpace == nil || s.NICs.Tx.Packets == nil || s.NICs.Rx.Packets == nil {
		t.Fatal("the stack has no loss counters")
	}
	return stackCounts{
		retransmits:  s.TCP.Retransmits.Value(),
		timeouts:     s.TCP.Timeouts.Value(),
		forwardDrops: s.TCP.ForwardMaxInFlightDrop.Value(),
		txNoBuffer:   s.NICs.TxPacketsDroppedNoBufferSpace.Value(),
		txPackets:    s.NICs.Tx.Packets.Value(),
		rxPackets:    s.NICs.Rx.Packets.Value(),
		txBytes:      s.NICs.Tx.Bytes.Value(),
		rxBytes:      s.NICs.Rx.Bytes.Value(),
	}
}

func (c stackCounts) sub(prev stackCounts) stackCounts {
	return stackCounts{
		retransmits:  c.retransmits - prev.retransmits,
		timeouts:     c.timeouts - prev.timeouts,
		forwardDrops: c.forwardDrops - prev.forwardDrops,
		txNoBuffer:   c.txNoBuffer - prev.txNoBuffer,
		txPackets:    c.txPackets - prev.txPackets,
		rxPackets:    c.rxPackets - prev.rxPackets,
		txBytes:      c.txBytes - prev.txBytes,
		rxBytes:      c.rxBytes - prev.rxBytes,
	}
}

// latencyStand is what the cells share: the real adapter with the stack's counters, the exit
// and the handler's bodies. The adapter and its routes are set up once, as the run does, and
// each cell opens only the connections it measures.
type latencyStand struct {
	stats  func() tcpip.Stats
	exit   *latencyExit
	bodies map[string][]byte
}

// TestTunLatencyNetns measures the local path. The same client fetches the same bodies
// through the real adapter, through the SOCKS5 datapath on the same exit and through the
// exit alone, at 1 and 8 fetches at once, over a shaped exit (50 ms one way, 64 KiB/s per
// stream) and over a control one (0 ms, no limit).
//
// The TUN total must not exceed the SOCKS5 total by more than one round trip of the shape
// (and by more than ten percent) at both sizes and both concurrency levels; on the control
// only the fixed allowance of latencyControlSlack applies. A gap means the local path is
// the cause; no gap rules it out with a number. The stack's counters are logged for every
// TUN case and must be zero at 8 × 128 KiB under the lossless shaper: a nonzero one names
// the local loss.
func TestTunLatencyNetns(t *testing.T) {
	skipOutsideNetns(t)
	// dttun0…dttun3 belong to the other tests of the package; the run's reservation is held
	// for the whole test.
	const name = "dttun4"

	started := time.Now()
	bodies := latencyBodies()
	handler := latencyHandler(t, bodies)
	ex := &latencyExit{handler: handler.Listener.Addr().String(), shape: latencyShapes[0]}

	w, err := NewTun(TunOptions{Name: name, Dialer: ex, TunDNS: netnsDNS, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	release, err := w.Reserve()
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	defer release()
	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if w.adapter == nil || w.adapter.stats == nil {
		t.Fatal("the adapter exposes no stack counters")
	}
	stand := &latencyStand{stats: w.adapter.stats, exit: ex, bodies: bodies}

	for _, shape := range latencyShapes {
		for _, size := range []int{latencySmallBody, latencyLargeBody} {
			for _, concurrency := range []int{1, 8} {
				cell := fmt.Sprintf("%s/%d/%d", shape.name, size, concurrency)
				t.Run(cell, func(t *testing.T) {
					if elapsed := time.Since(started); elapsed > latencyBudget {
						t.Fatalf("the stand has run %v, more than its %v budget", elapsed, latencyBudget)
					}
					stand.run(t, shape, size, concurrency)
				})
			}
		}
	}
}

func (s *latencyStand) run(t *testing.T, shape latencyShape, size, concurrency int) {
	t.Helper()
	s.exit.setShape(shape)
	path := "/" + strconv.Itoa(size)
	want, ok := s.bodies[path]
	if !ok || len(want) != size {
		t.Fatalf("no body of %d bytes for %s", size, path)
	}

	// (a) The real adapter. The stack's counters are read around the case: a zero delta with
	// the lossless shaper rules the local path out, a nonzero one names the loss.
	before := readStackCounts(t, s.stats)
	start, tunStart := s.exit.count(), time.Now()
	tunFetches, err := latencyFetches(concurrency, func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", latencyDestination.String())
	}, path, want)
	if err != nil {
		t.Fatalf("tun: %v", err)
	}
	waitStreams(t, s.exit.streamsSince(start))
	tunStreams := s.exit.count() - start
	counts := readStackCounts(t, s.stats).sub(before)
	s.logStreams(t, "tun", start, tunStart)
	t.Logf("tun stack: %d retransmits, %d timeouts, %d refused connection requests, %d TX queue drops, "+
		"TX %d packets/%d bytes, RX %d packets/%d bytes",
		counts.retransmits, counts.timeouts, counts.forwardDrops, counts.txNoBuffer,
		counts.txPackets, counts.txBytes, counts.rxPackets, counts.rxBytes)

	// (b) The SOCKS5 datapath on the same exit.
	start, socksStart := s.exit.count(), time.Now()
	dp, err := NewSocks(SocksOptions{Listen: "127.0.0.1:0", Dialer: s.exit, Logf: t.Logf})
	if err != nil {
		t.Fatalf("socks: %v", err)
	}
	socksFetches, err := latencyFetches(concurrency, func(ctx context.Context) (net.Conn, error) {
		return socksDial(ctx, dp.ListenAddr().String())
	}, path, want)
	if err != nil {
		dp.Close()
		t.Fatalf("socks: %v", err)
	}
	if err := dp.Close(); err != nil {
		t.Errorf("socks: close: %v", err)
	}
	socksStreams := s.exit.count() - start
	s.logStreams(t, "socks", start, socksStart)

	// (c) The exit alone, without a datapath.
	start, directStart := s.exit.count(), time.Now()
	directFetches, err := latencyFetches(concurrency, func(ctx context.Context) (net.Conn, error) {
		return s.exit.Dial(ctx, latencyDestination.String())
	}, path, want)
	if err != nil {
		t.Fatalf("direct: %v", err)
	}
	waitStreams(t, s.exit.streamsSince(start))
	directStreams := s.exit.count() - start
	s.logStreams(t, "direct", start, directStart)

	tun := newLatencyRun("tun", tunFetches, tunStreams)
	socks := newLatencyRun("socks", socksFetches, socksStreams)
	directRun := newLatencyRun("direct", directFetches, directStreams)
	for _, r := range []latencyRun{tun, socks, directRun} {
		ttfbMean, ttfbMax := r.ttfbMeanMax()
		totalMean, totalMax := r.totalMeanMax()
		t.Logf("%s: ttfb %v mean/%v max, total %v mean/%v max, %d streams",
			r.name, ttfbMean, ttfbMax, totalMean, totalMax, r.streams)
		if r.streams != concurrency {
			t.Errorf("%s: the exit saw %d streams, want one per fetch (%d)", r.name, r.streams, concurrency)
		}
	}

	// The criterion: the TUN path may add no more than one round trip of the shape, and no
	// more than ten percent, to the SOCKS5 path on the same exit.
	_, tunTotal := tun.totalMeanMax()
	_, socksTotal := socks.totalMeanMax()
	worst := ""
	if shape.bytesPerSecond > 0 {
		if tunTotal > socksTotal+latencyOneRTT {
			worst = fmt.Sprintf("more than one round trip (%v)", latencyOneRTT)
		} else if tunTotal > time.Duration(float64(socksTotal)*1.10) {
			worst = "more than ten percent"
		}
	} else if tunTotal > socksTotal+latencyControlSlack {
		worst = fmt.Sprintf("more than %v with no shape", latencyControlSlack)
	}
	if worst != "" {
		t.Fatalf("TUN total %v vs SOCKS total %v: %v; the local path costs %v",
			tunTotal, socksTotal, worst, tunTotal-socksTotal)
	}

	// The local path's losses: zero with the lossless shaper at the worst case of the stage,
	// 8 × 128 KiB. A nonzero one names the source; a zero one removes the suspicion from the
	// local path.
	if shape.bytesPerSecond > 0 && size == latencyLargeBody && concurrency == 8 {
		if counts.retransmits != 0 || counts.txNoBuffer != 0 || counts.forwardDrops != 0 {
			t.Fatalf("the lossless shaper shows local loss: %d retransmits, %d timeouts, "+
				"%d refused connection requests, %d TX queue drops",
				counts.retransmits, counts.timeouts, counts.forwardDrops, counts.txNoBuffer)
		}
	}
}

// logStreams prints the phases of the streams of one run relative to its start: the dial the
// forwarder began, the exit's answer, the first byte back and the end of the relay.
func (s *latencyStand) logStreams(t *testing.T, name string, since int, start time.Time) {
	t.Helper()
	for _, st := range s.exit.streamsSince(since) {
		dialStart, dialReturn, firstByte, relayEnd := st.snapshot()
		if st.target != latencyDestination.String() {
			t.Errorf("%s: the exit was asked for %s, want %s", name, st.target, latencyDestination)
		}
		t.Logf("%s stream %s: dial %s, returned %s, first byte %s, relay end %s", name, st.target,
			phase(start, dialStart), phase(start, dialReturn), phase(start, firstByte), phase(start, relayEnd))
	}
}
