package resolvers

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/FreedomCats/dnstt-cli/core/client"
)

// The bootstrap resolver gives the addresses of resolvers' host names that are not pinned
// (point 5 of architecture.md §06). It asks plain DNS over its own sockets, each through
// Control, so the queries go past the tunnel and through a closed kill-switch, and the
// system resolver, its hosts file and search domains are never involved. UDP first; a
// truncated answer is asked again over TCP. Taken from the tunnel DNS resolver of
// csqtt-cli (MIT), with AAAA and a socket hook.

// Where the bootstrap resolver asks, in order: the profile's own servers (bootstrap = […]),
// and only them, if it has any; otherwise the DNS servers the network gave the system — the
// ISP's resolver, which answers in a network that lets through only some resolvers — then
// the plain UDP resolvers of the list given by address. Public resolvers abroad are never
// asked unasked: in such a network they do not answer, and every attempt is a timeout.

// BootstrapServers are the servers to resolve the names of list with: own if there are
// any; otherwise network (port 53) and then the UDP resolvers of list given by address.
func BootstrapServers(own []netip.AddrPort, network []netip.Addr, list []Resolver) []netip.AddrPort {
	var servers []netip.AddrPort
	add := func(s netip.AddrPort) {
		s = netip.AddrPortFrom(s.Addr().Unmap(), s.Port())
		if !slices.Contains(servers, s) {
			servers = append(servers, s)
		}
	}
	if len(own) > 0 {
		for _, s := range own {
			add(s)
		}
		return servers
	}
	for _, a := range network {
		add(netip.AddrPortFrom(a, 53))
	}
	for _, r := range list {
		if r.Transport != client.UDP {
			continue
		}
		if s, err := netip.ParseAddrPort(r.Address); err == nil {
			add(s)
		}
	}
	return servers
}

// ParseBootstrap reads the bootstrap servers of a profile: plain DNS by address,
// udp:address[:port] or address[:port] (53 by default). A host name is refused: it would
// need a bootstrap of its own.
func ParseBootstrap(entries []string) ([]netip.AddrPort, error) {
	var servers []netip.AddrPort
	var errs []error
	for _, e := range entries {
		entry := strings.TrimSpace(e)
		if !strings.HasPrefix(strings.ToLower(entry), "udp:") {
			entry = "udp:" + entry
		}
		r, err := parse(entry)
		var s netip.AddrPort
		if err == nil {
			if s, err = netip.ParseAddrPort(r.Address); err != nil || len(r.Pins) > 0 {
				err = errors.New("a bootstrap server is given by its address")
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("bootstrap %q: %w", e, err))
			continue
		}
		if !slices.Contains(servers, s) {
			servers = append(servers, s)
		}
	}
	return servers, errors.Join(errs...)
}

// Errors of a lookup that another server would answer the same way.
var (
	// ErrNotFound: the name does not exist (NXDOMAIN).
	ErrNotFound = errors.New("no such name")
	// ErrNoAddress: the name has neither IPv4 nor IPv6 addresses.
	ErrNoAddress = errors.New("the name has no addresses")
)

const (
	// bootstrapTimeout is the wait for one server's answer to one query.
	bootstrapTimeout = 2 * time.Second
	// bootstrapRounds is how many times the servers are gone through before giving up.
	bootstrapRounds = 2
	// cacheMax is how many names are kept; a full cache is cleared at once. A profile
	// has a few resolvers.
	cacheMax = 64
	// cacheTTL is the longest an answer is kept, whatever TTL the server gave.
	cacheTTL = 5 * time.Minute
)

// Bootstrap is the bootstrap resolver: LookupIP is the fallback of Resolver.LookupIP.
type Bootstrap struct {
	servers func() []netip.AddrPort
	control func(network, address string, c syscall.RawConn) error
	timeout time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	addrs   []netip.Addr
	expires time.Time
}

// NewBootstrap asks the servers that servers returns, in turn; it is called on every lookup
// that is not in the cache, so the DNS of a new network is taken up without a restart
// (BootstrapServers over the system's DNS of the moment). control is put on every socket
// (netguard), nil leaves them as they are.
func NewBootstrap(servers func() []netip.AddrPort, control func(network, address string, c syscall.RawConn) error) *Bootstrap {
	return &Bootstrap{
		servers: servers,
		control: control,
		timeout: bootstrapTimeout,
		now:     time.Now,
		cache:   map[string]cached{},
	}
}

// Forget drops the answers kept: after a network change they may be another network's.
func (b *Bootstrap) Forget() {
	b.mu.Lock()
	clear(b.cache)
	b.mu.Unlock()
}

// LookupIP gives the IPv4 addresses of host, or its IPv6 addresses if it has no IPv4.
func (b *Bootstrap) LookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a.Unmap()}, nil
	}
	key := strings.ToLower(strings.TrimSuffix(host, "."))
	b.mu.Lock()
	c, ok := b.cache[key]
	b.mu.Unlock()
	if ok && b.now().Before(c.expires) {
		return append([]netip.Addr(nil), c.addrs...), nil
	}
	servers := b.servers()
	if len(servers) == 0 {
		return nil, fmt.Errorf("bootstrap resolver: no servers to resolve %s with: the network gave the "+
			"system no DNS; pin the resolver's address (@address) or set bootstrap in the profile", key)
	}
	name, err := dnsmessage.NewName(key + ".")
	if key == "" || err != nil {
		return nil, fmt.Errorf("bootstrap resolver: bad name %q", host)
	}

	var errs []error
	for range bootstrapRounds {
		for _, server := range servers {
			addrs, ttl, err := b.ask(ctx, server, name, dnsmessage.TypeA)
			if errors.Is(err, ErrNoAddress) {
				addrs, ttl, err = b.ask(ctx, server, name, dnsmessage.TypeAAAA)
			}
			switch {
			case err == nil:
				b.remember(key, addrs, ttl)
				return addrs, nil
			case errors.Is(err, ErrNotFound), errors.Is(err, ErrNoAddress):
				// The answer is final: another server knows the same zone.
				return nil, fmt.Errorf("bootstrap resolver: %s: %w", key, err)
			case ctx.Err() != nil:
				return nil, ctx.Err()
			}
			if !containsErr(errs, err) {
				errs = append(errs, err)
			}
		}
	}
	return nil, fmt.Errorf("bootstrap resolver: %s: %w", key, errors.Join(errs...))
}

func containsErr(errs []error, err error) bool {
	for _, e := range errs {
		if e.Error() == err.Error() {
			return true
		}
	}
	return false
}

func (b *Bootstrap) remember(key string, addrs []netip.Addr, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.cache) >= cacheMax {
		clear(b.cache)
	}
	b.cache[key] = cached{addrs, b.now().Add(min(ttl, cacheTTL))}
}

// ask sends one query of type qtype to one server.
func (b *Bootstrap) ask(ctx context.Context, server netip.AddrPort, name dnsmessage.Name, qtype dnsmessage.Type) ([]netip.Addr, time.Duration, error) {
	id := uint16(rand.Uint32())
	query, err := buildQuery(id, name, qtype)
	if err != nil {
		return nil, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	answer, err := b.askUDP(ctx, server, id, query)
	if err != nil {
		return nil, 0, fmt.Errorf("%v: %w", server, err)
	}
	var truncated bool
	addrs, ttl, err := parseAnswer(answer, id, name, qtype, &truncated)
	if truncated {
		if answer, err = b.askTCP(ctx, server, query); err != nil {
			return nil, 0, fmt.Errorf("%v over TCP: %w", server, err)
		}
		addrs, ttl, err = parseAnswer(answer, id, name, qtype, nil)
	}
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrNoAddress) {
		err = fmt.Errorf("%v: %w", server, err)
	}
	return addrs, ttl, err
}

func (b *Bootstrap) dialer() *net.Dialer {
	return &net.Dialer{Control: b.control}
}

func network(proto string, server netip.AddrPort) string {
	if server.Addr().Is4() {
		return proto + "4"
	}
	return proto + "6"
}

func (b *Bootstrap) askUDP(ctx context.Context, server netip.AddrPort, id uint16, query []byte) ([]byte, error) {
	c, err := b.dialer().DialContext(ctx, network("udp", server), server.String())
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 1232)
	for {
		n, err := c.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		// An answer to another query — late or forged — is skipped.
		if n >= 2 && binary.BigEndian.Uint16(buf) == id {
			return buf[:n], nil
		}
	}
}

func (b *Bootstrap) askTCP(ctx context.Context, server netip.AddrPort, query []byte) ([]byte, error) {
	c, err := b.dialer().DialContext(ctx, network("tcp", server), server.String())
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	framed := binary.BigEndian.AppendUint16(nil, uint16(len(query)))
	if _, err := c.Write(append(framed, query...)); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(c, size[:]); err != nil {
		return nil, err
	}
	answer := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(c, answer); err != nil {
		return nil, err
	}
	return answer, nil
}

func buildQuery(id uint16, name dnsmessage.Name, qtype dnsmessage.Type) ([]byte, error) {
	b := dnsmessage.NewBuilder(make([]byte, 0, 512),
		dnsmessage.Header{ID: id, RecursionDesired: true})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: qtype, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	return b.Finish()
}

var errWrongAnswer = errors.New("an answer to another query")

// parseAnswer reads the answer to a query of type qtype (A or AAAA). With truncated not
// nil, a truncated answer (the TC flag) is not read but marked in it.
func parseAnswer(msg []byte, id uint16, name dnsmessage.Name, qtype dnsmessage.Type, truncated *bool) ([]netip.Addr, time.Duration, error) {
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		return nil, 0, err
	}
	if h.ID != id || !h.Response {
		return nil, 0, errWrongAnswer
	}
	if h.Truncated && truncated != nil {
		*truncated = true
		return nil, 0, nil
	}
	q, err := p.Question()
	if err != nil || !strings.EqualFold(q.Name.String(), name.String()) || q.Type != qtype {
		return nil, 0, errWrongAnswer
	}
	switch h.RCode {
	case dnsmessage.RCodeSuccess:
	case dnsmessage.RCodeNameError:
		return nil, 0, ErrNotFound
	default:
		return nil, 0, fmt.Errorf("the server answered %v", h.RCode)
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, 0, err
	}
	var addrs []netip.Addr
	ttl := cacheTTL
	for {
		rh, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		if rh.Type != qtype || rh.Class != dnsmessage.ClassINET {
			if err := p.SkipAnswer(); err != nil {
				return nil, 0, err
			}
			continue
		}
		var a netip.Addr
		if qtype == dnsmessage.TypeA {
			r, err := p.AResource()
			if err != nil {
				return nil, 0, err
			}
			a = netip.AddrFrom4(r.A)
		} else {
			r, err := p.AAAAResource()
			if err != nil {
				return nil, 0, err
			}
			a = netip.AddrFrom16(r.AAAA).Unmap()
		}
		addrs = append(addrs, a)
		ttl = min(ttl, time.Duration(rh.TTL)*time.Second)
	}
	if len(addrs) == 0 {
		return nil, 0, ErrNoAddress
	}
	return addrs, ttl, nil
}
