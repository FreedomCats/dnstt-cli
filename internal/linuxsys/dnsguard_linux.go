package linuxsys

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/FreedomCats/dnstt-cli/internal/app"
)

// dnsGuardTable is the nftables table of DNS protection (`nft list table inet dnstt-cli`).
const dnsGuardTable = app.Name

// DNSGuard is DNS leak protection on Linux, the counterpart of winsys.DNSGuard: nftables
// rules forbid traffic to port 53, UDP and TCP, to all addresses except the tunnel
// resolvers, loopback and dnstt-cli's own sockets.
//
// The table inet dnstt-cli is created with the owner flag: the kernel keeps it while the
// netlink socket that created it is open and deletes it together with that socket,
// including when dnstt-cli crashes. So no cleanup is needed at startup. Only that socket
// may change a table with owner, so everything goes through one long-lived connection
// (nftables.AsLasting).
//
// The tunnel core's sockets (Go code in the same dnstt-cli process) are recognised by
// the SO_MARK mark (netguard.SocketMark), which netguard's Control hook sets on them: a
// filter by uid would let through every root process under sudo. The mark only
// identifies; there is no routing by it.
//
// The forward chain is there for containers and VMs: their queries to a resolver on the
// local network (a route more specific than /1) would otherwise bypass the tunnel, and,
// with IPv6 closed, so would their IPv6 traffic outward.
//
// The table and chains are created once (OpenDNSGuard), while rules are installed and
// removed along the way (Enable, Lockdown, Disable): this way a refusal is found out
// before connecting, not in the middle of it. The kill switch lives in the same chains
// too: the "closed" set, see Lockdown.
type DNSGuard struct {
	mu        sync.Mutex
	conn      *nftables.Conn
	table     *nftables.Table
	output    *nftables.Chain
	forward   *nftables.Chain
	mark      uint32
	blockIPv6 bool
	state     guardState
}

// guardState is which rule set is installed in the table.
type guardState int

const (
	guardOpen     guardState = iota // no rules: nothing is forbidden
	guardTunnel                     // tunnel up: DNS protection (Enable)
	guardLockdown                   // closed: kill switch (Lockdown)
)

// OpenDNSGuard creates the table inet dnstt-cli with empty chains; there are no rules
// yet. mark is the mark of dnstt-cli's sockets; they may send DNS to any address.
// Requires CAP_NET_ADMIN and kernel 5.12 or newer (the owner flag).
func OpenDNSGuard(mark uint32) (*DNSGuard, error) {
	conn, err := nftables.New(nftables.AsLasting())
	if err != nil {
		return nil, fmt.Errorf("nftables: %w", err)
	}
	g := &DNSGuard{conn: conn, mark: mark}
	g.table = conn.CreateTable(&nftables.Table{
		Name:   dnsGuardTable,
		Family: nftables.TableFamilyINet,
		Flags:  nftables.TableFlagOwner,
	})
	accept := nftables.ChainPolicyAccept
	g.output = conn.AddChain(&nftables.Chain{
		Name:     "output",
		Table:    g.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookOutput,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &accept,
	})
	g.forward = conn.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    g.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &accept,
	})
	if err := conn.Flush(); err != nil {
		conn.CloseLasting()
		return nil, dnsGuardTableError(err)
	}
	return g, nil
}

// dnsGuardTableError explains a failure to create the table. If a table with the same
// name already exists, the kernel answers EEXIST, or EPERM if it has an owner, the same
// as for missing privileges; so on EPERM the table is looked up.
func dnsGuardTableError(err error) error {
	switch {
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("nftables: table inet %s already exists and was not created by dnstt-cli; "+
			"if it is not needed, delete it: sudo nft delete table inet %s", dnsGuardTable, dnsGuardTable)
	case errors.Is(err, unix.EPERM):
		if owner, ok := dnsGuardTableOwner(); ok {
			return fmt.Errorf("nftables: table inet %s is held by another process (netlink port %d, "+
				"usually its pid); another dnstt-cli seems to be running already", dnsGuardTable, owner)
		}
	}
	return fmt.Errorf("nftables: table inet %s: %w", dnsGuardTable, err)
}

// dnsGuardTableOwner returns the owner of the existing table inet dnstt-cli, if the
// table exists and has an owner.
func dnsGuardTableOwner() (uint32, bool) {
	conn, err := nftables.New()
	if err != nil {
		return 0, false
	}
	table, err := conn.ListTableOfFamily(dnsGuardTable, nftables.TableFamilyINet)
	if err != nil || table.Flags&nftables.TableFlagOwner == 0 {
		return 0, false
	}
	return table.Owner, true
}

// BlockIPv6 sets whether the next Enable closes the system's IPv6 outward: the tunnel
// does not carry IPv6, there are no IPv6 routes into the adapter, and without the block
// IPv6 would bypass the tunnel. What stays open is loopback, dnstt-cli's sockets and, as
// in "closed", the IPv6 local network, ND, RA and DHCPv6; DNS to the local network is
// closed (dnsGuardRules).
func (g *DNSGuard) BlockIPv6(block bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.blockIPv6 = block
}

// Enable installs the rules: DNS only to the resolvers dns. The previous rules are
// replaced in one nftables batch, so calling it again is harmless, and if the kernel
// refuses it applies nothing. The rules are then removed so that the old ones do not
// stay either, except for the "closed" set: it is stricter than DNS protection, and
// there is no reason to open the system because of a refusal.
func (g *DNSGuard) Enable(dns []netip.Addr) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	err := g.applyLocked(guardTunnel, dnsGuardRules(dns, g.blockIPv6, false), dnsGuardRules(dns, g.blockIPv6, true))
	if err == nil || g.state == guardLockdown {
		return err
	}
	return errors.Join(err, g.disableLocked())
}

// applyLocked replaces the rules of the chains in one batch and, on success, records
// the state state.
func (g *DNSGuard) applyLocked(state guardState, output, forward []guardRule) error {
	if g.conn == nil {
		return errors.New("DNS protection: nftables table is closed")
	}
	g.conn.FlushTable(g.table)
	for _, r := range output {
		g.conn.AddRule(&nftables.Rule{Table: g.table, Chain: g.output, Exprs: r.exprs(g.mark)})
	}
	for _, r := range forward {
		g.conn.AddRule(&nftables.Rule{Table: g.table, Chain: g.forward, Exprs: r.exprs(g.mark)})
	}
	if err := g.conn.Flush(); err != nil {
		return fmt.Errorf("DNS protection: nftables rules: %w", err)
	}
	g.state = state
	return nil
}

// Disable removes the rules; the table and chains stay empty and forbid nothing:
// neither DNS nor (after Lockdown) other traffic. Calling it again is harmless.
func (g *DNSGuard) Disable() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.disableLocked()
}

func (g *DNSGuard) disableLocked() error {
	if g.conn == nil {
		return nil
	}
	g.conn.FlushTable(g.table)
	if err := g.conn.Flush(); err != nil {
		return fmt.Errorf("DNS protection: removing nftables rules: %w", err)
	}
	g.state = guardOpen
	return nil
}

// Close closes the connection, and the kernel deletes the table together with the rules.
func (g *DNSGuard) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn == nil {
		return nil
	}
	err := g.conn.CloseLasting()
	g.conn = nil
	if err != nil {
		return fmt.Errorf("DNS protection: closing nftables: %w", err)
	}
	return nil
}

// guardRule is one protection rule in a form convenient for testing; exprs turns it
// into nftables expressions. The conditions are combined with "and".
type guardRule struct {
	mark     bool         // dnstt-cli socket: meta mark == mark
	loopback bool         // oifname "lo"
	family   byte         // unix.NFPROTO_IPV4 or NFPROTO_IPV6; 0 means any (required with daddr)
	daddr    netip.Prefix // destination address or subnet; empty means any
	proto    byte         // unix.IPPROTO_*; 0 means any
	sport    uint16       // UDP or TCP source port; 0 means any
	dport    uint16       // UDP or TCP destination port; 0 means any
	ndp      bool         // ICMPv6 types 133–137: RS, RA, NS, NA, Redirect
	accept   bool         // otherwise drop
}

// dnsGuardRules returns the rules of a chain in order: first the accepts for the
// resolvers, then the port 53 ban. forward is the chain of transit traffic: there is no
// mark or loopback there.
//
// With blockIPv6, after the port 53 ban local IPv6 is accepted, as in "closed"
// (lockdownRules): the host's service protocols (DHCPv6, ND and RA) and the local
// network, and all other IPv6 is dropped. Transit IPv6 of containers and VMs gets the
// same, without the host's service protocols: there is no tunnel outward for it either.
// The port 53 ban comes first: DNS to the IPv6 local network is closed, and a tunnel
// resolver with an IPv6 address is not accepted at all, since there is no IPv6 route
// into the tunnel and a query to it would bypass it.
func dnsGuardRules(dns []netip.Addr, blockIPv6, forward bool) []guardRule {
	var rules []guardRule
	if !forward {
		rules = append(rules,
			guardRule{mark: true, accept: true},
			guardRule{loopback: true, accept: true})
	}
	for _, addr := range dns {
		addr = addr.Unmap()
		if !addr.IsValid() || (blockIPv6 && addr.Is6()) {
			continue
		}
		family := byte(unix.NFPROTO_IPV4)
		if addr.Is6() {
			family = unix.NFPROTO_IPV6
		}
		for _, p := range []byte{unix.IPPROTO_UDP, unix.IPPROTO_TCP} {
			rules = append(rules, guardRule{family: family, daddr: netip.PrefixFrom(addr, addr.BitLen()),
				proto: p, dport: 53, accept: true})
		}
	}
	rules = append(rules, dnsDenyRules()...)
	if blockIPv6 {
		if !forward {
			rules = append(rules, hostServiceRules(unix.NFPROTO_IPV6)...)
		}
		rules = append(rules, localNetRules(unix.NFPROTO_IPV6)...)
		rules = append(rules, guardRule{family: unix.NFPROTO_IPV6})
	}
	return rules
}

// exprs returns the nftables expressions of rule r; mark is the mark of dnstt-cli's
// sockets.
func (r guardRule) exprs(mark uint32) []expr.Any {
	var e []expr.Any
	match := func(load expr.Any, data []byte) {
		e = append(e, load, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: data})
	}
	if r.mark {
		match(&expr.Meta{Key: expr.MetaKeyMARK, Register: 1}, binaryutil.NativeEndian.PutUint32(mark))
	}
	if r.loopback {
		match(&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1}, ifname("lo"))
	}
	if r.family != 0 {
		match(&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, []byte{r.family})
	}
	if r.daddr.IsValid() {
		// Destination address: offset 16 in the IPv4 header, 24 in IPv6.
		offset, size := uint32(16), uint32(4)
		if r.daddr.Addr().Is6() {
			offset, size = 24, 16
		}
		e = append(e, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: size})
		if !r.daddr.IsSingleIP() {
			// Subnet: the address is masked first.
			e = append(e, &expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: size,
				Mask: prefixMask(r.daddr), Xor: make([]byte, size)})
		}
		e = append(e, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: r.daddr.Masked().Addr().AsSlice()})
	}
	if r.proto != 0 {
		match(&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1}, []byte{r.proto})
	}
	// Ports are offsets 0 and 2 in both UDP and TCP.
	if r.sport != 0 {
		match(&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 2},
			binaryutil.BigEndian.PutUint16(r.sport))
	}
	if r.dport != 0 {
		match(&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
			binaryutil.BigEndian.PutUint16(r.dport))
	}
	if r.ndp {
		match(&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1}, []byte{unix.IPPROTO_ICMPV6})
		// The ICMPv6 type is the first byte of the header.
		e = append(e,
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1},
			&expr.Cmp{Op: expr.CmpOpGte, Register: 1, Data: []byte{133}},
			&expr.Cmp{Op: expr.CmpOpLte, Register: 1, Data: []byte{137}})
	}
	verdict := expr.VerdictDrop
	if r.accept {
		verdict = expr.VerdictAccept
	}
	return append(e, &expr.Verdict{Kind: verdict})
}

// prefixMask returns the mask of subnet p in network byte order.
func prefixMask(p netip.Prefix) []byte {
	mask := make([]byte, p.Addr().BitLen()/8)
	for i := 0; i < p.Bits(); i++ {
		mask[i/8] |= 0x80 >> (i % 8)
	}
	return mask
}

// ifname returns an interface name the way the kernel compares it: IFNAMSIZ bytes,
// padded with zeros.
func ifname(name string) []byte {
	b := make([]byte, unix.IFNAMSIZ)
	copy(b, name)
	return b
}
