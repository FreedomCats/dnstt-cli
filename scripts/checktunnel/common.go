//go:build windows || linux

// Shared by the Windows and Linux snapshots: sections, /0 and /1 routes, the external
// address and the resolver.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	myaddrName = "o-o.myaddr.l.google.com"
	ipURL      = "https://ifconfig.me/ip"
)

// header prints the snapshot heading.
func header(w io.Writer, label string) {
	title := "snapshot " + time.Now().Format("2006-01-02 15:04:05")
	if label != "" {
		title += " — " + label
	}
	fmt.Fprintf(w, "=== %s ===\n", title)
}

func section(w io.Writer, name string, f func(io.Writer) error) {
	fmt.Fprintf(w, "\n-- %s\n", name)
	if err := f(w); err != nil {
		fmt.Fprintf(w, "   error: %v\n", err)
	}
}

// printNetwork prints the external address and the resolver: the sections that need
// the network.
func printNetwork(w io.Writer, timeout time.Duration) {
	section(w, "External address (no proxy)", func(w io.Writer) error {
		printExternalIP(w, timeout)
		return nil
	})
	section(w, "Resolver ("+myaddrName+")", func(w io.Writer) error {
		return printResolver(w, timeout)
	})
}

type route struct {
	prefix  netip.Prefix
	nextHop netip.Addr
	iface   string
	index   uint32
	metric  uint32 // of the route
	ifMet   string // of the interface (Windows)
	table   int    // routing table (Linux)
	proto   string // who set the route (Linux)
}

// interestingRoute is a default route or a half of a full tunnel.
func interestingRoute(p netip.Prefix) bool {
	return p.IsValid() && p.Bits() <= 1
}

// sortRoutes sorts IPv4 before IPv6, then by prefix and interface index.
func sortRoutes(rs []route) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.prefix.Addr().Is4() != b.prefix.Addr().Is4() {
			return a.prefix.Addr().Is4()
		}
		if c := a.prefix.Addr().Compare(b.prefix.Addr()); c != 0 {
			return c < 0
		}
		if a.prefix.Bits() != b.prefix.Bits() {
			return a.prefix.Bits() < b.prefix.Bits()
		}
		return a.index < b.index
	})
}

// hopString is the gateway of a route for output.
func hopString(nextHop netip.Addr) string {
	if nextHop.IsValid() && !nextHop.IsUnspecified() {
		return "via " + nextHop.String()
	}
	return "on-link"
}

func printExternalIP(w io.Writer, timeout time.Duration) {
	for _, network := range []string{"tcp4", "tcp6"} {
		ip, err := externalIP(network, timeout)
		label := "IPv4"
		if network == "tcp6" {
			label = "IPv6"
		}
		if err != nil {
			fmt.Fprintf(w, "   %s: none (%v)\n", label, shortErr(err))
			continue
		}
		fmt.Fprintf(w, "   %s: %s\n", label, ip)
	}
}

// externalIP asks ifconfig.me over the given family, bypassing the proxy from the
// environment: an address seen through a proxy says nothing about the tunnel.
func externalIP(network string, timeout time.Duration) (string, error) {
	d := &net.Dialer{Timeout: timeout}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return d.DialContext(ctx, network, addr)
			},
		},
	}
	req, err := http.NewRequest(http.MethodGet, ipURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "curl/8")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil {
		return "", fmt.Errorf("response is not an address: %q", body)
	}
	return ip.String(), nil
}

// printResolver prints TXT o-o.myaddr.l.google.com via the system resolver: the
// address of whoever actually asked Google.
func printResolver(w io.Writer, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	txt, err := net.DefaultResolver.LookupTXT(ctx, myaddrName)
	if err != nil {
		return errors.New(shortErr(err))
	}
	for _, t := range txt {
		fmt.Fprintf(w, "   %s\n", t)
	}
	return nil
}

func listOrNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

// shortErr cuts long Go network errors down to the point: "dial tcp6 …: connect: …"
// turns into the last part after a colon, if it is not empty.
func shortErr(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return "name not found"
		}
		if dnsErr.IsTimeout {
			return "DNS timeout"
		}
		return dnsErr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		return "timeout"
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		return s[i+2:]
	}
	return s
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
