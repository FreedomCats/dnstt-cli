// Added by dnstt-cli (scripts/vendorcore/_files): not part of the dnstt upstream.

package server

import (
	"net"

	"github.com/FreedomCats/dnstt-cli/core/dns"
	"github.com/FreedomCats/dnstt-cli/core/noise"
)

// TestServer is the upstream server run in-process on a loopback UDP socket, for tests of
// the client. Nothing outside tests uses this package.
type TestServer struct {
	// Addr is the UDP address the server answers queries on.
	Addr *net.UDPAddr
	// PubKey is the server's Noise public key, generated for this server.
	PubKey []byte

	privkey  []byte
	domain   dns.Name
	upstream string
	conn     net.PacketConn
	done     chan struct{}
	err      error
}

// StartTest starts the upstream server for domain on 127.0.0.1 with a new key. Every
// stream the server accepts is handed to the TCP address upstream, as by dnstt-server.
func StartTest(domain dns.Name, upstream string) (*TestServer, error) {
	privkey, err := noise.GeneratePrivkey()
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	s := &TestServer{
		Addr:     conn.LocalAddr().(*net.UDPAddr),
		PubKey:   noise.PubkeyFromPrivkey(privkey),
		privkey:  privkey,
		domain:   domain,
		upstream: upstream,
	}
	s.start(conn)
	return s, nil
}

func (s *TestServer) start(conn net.PacketConn) {
	s.conn, s.done = conn, make(chan struct{})
	go func() {
		s.err = run(s.privkey, s.domain, s.upstream, conn)
		close(s.done)
	}()
}

// Reopen starts a closed server again on the same address with the same key, as a
// restarted dnstt-server: the sessions of the old one are unknown to it.
func (s *TestServer) Reopen() error {
	conn, err := net.ListenUDP("udp", s.Addr)
	if err != nil {
		return err
	}
	s.start(conn)
	return nil
}

// Close stops the server: it stops answering queries. Sessions already open are not told;
// clients notice by their keepalive.
func (s *TestServer) Close() error {
	s.conn.Close()
	<-s.done
	return s.err
}
