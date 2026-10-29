//go:build windows

// Command checktunnel prints a snapshot of the system state for a live check of
// dnstt-cli: what dnstt-cli puts into the system and what must be gone after it.
//
//	go run ./scripts/checktunnel [-label "before start"] [-name dnstt-cli] [-timeout 10s] [-offline]
//
// The output before dnstt-cli starts, while it runs and after it is the report of the
// scenario. Sections:
//
//   - dnstt-cli.exe processes;
//   - adapters: enabled ones with an IP (except loopback and Teredo) and any with the
//     tunnel name or the permanent GUID of the wintun adapter, with addresses, MTU, the
//     interface metric (from winipcfg, not from Get-NetIPInterface: there it is
//     sometimes empty) and DNS;
//   - /0 and /1 routes of both families: the default route and the halves of a full
//     tunnel;
//   - external IPv4 and IPv6 address: HTTPS bypassing the proxy from the environment,
//     since an address seen through a proxy says nothing about the tunnel;
//   - resolver: TXT o-o.myaddr.l.google.com via the system resolver (DnsQuery), that
//     is, the address of whoever actually asked Google;
//   - WFP filters of the DNS protection provider (winsys.DNSGuard);
//   - NRPT and the Tcpip keys of the tunnel adapters in the registry.
//
// The command changes nothing in the system. Without administrator rights some sections
// (WFP) print an error instead of data; the rest works.
//
// The Linux snapshot is main_linux.go, the shared sections are common.go.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"
	"unsafe"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/FreedomCats/dnstt-cli/internal/app"
	"github.com/FreedomCats/dnstt-cli/internal/winsys"
)

const (
	nrptKey       = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
	nrptPolicyKey = `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`
	tcpipKey      = `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\`
	tcpip6Key     = `SYSTEM\CurrentControlSet\Services\Tcpip6\Parameters\Interfaces\`
)

type options struct {
	label   string
	name    string        // name of the tunnel adapter
	timeout time.Duration // for each network request
	offline bool          // without the external address and the resolver
}

func main() {
	var o options
	flag.StringVar(&o.label, "label", "", "snapshot label (for example, \"before start\")")
	flag.StringVar(&o.name, "name", app.Name, "name of the tunnel adapter")
	flag.DurationVar(&o.timeout, "timeout", 10*time.Second, "timeout of each network request")
	flag.BoolVar(&o.offline, "offline", false, "do not check the external address and the resolver")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	run(os.Stdout, o)
}

// run prints the snapshot. An error of one section is printed in that section and does
// not stop the others.
func run(w io.Writer, o options) {
	header(w, o.label)

	section(w, "dnstt-cli.exe processes", func(w io.Writer) error { return printProcesses(w) })
	var guids []windows.GUID
	section(w, "Adapters", func(w io.Writer) (err error) {
		guids, err = printAdapters(w, o.name)
		return err
	})
	section(w, "Routes /0 and /1", func(w io.Writer) error { return printRoutes(w) })
	if !o.offline {
		printNetwork(w, o.timeout)
	}
	section(w, "WFP: DNS protection", func(w io.Writer) error { return printWFP(w) })
	section(w, "Registry", func(w io.Writer) error {
		printRegistry(w, guids)
		return nil
	})
}

func printProcesses(w io.Writer) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	var pids []uint32
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), app.Name+".exe") {
			pids = append(pids, e.ProcessID)
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	if len(pids) == 0 {
		fmt.Fprintln(w, "   none")
		return nil
	}
	for _, pid := range pids {
		fmt.Fprintf(w, "   PID %d\n", pid)
	}
	return nil
}

// printAdapters prints enabled adapters with an IP (except loopback and Teredo) and the
// tunnel adapters in any state. It returns the GUIDs of the tunnel adapters (for the
// registry section); the permanent wintun GUID is always among them.
func printAdapters(w io.Writer, name string) ([]windows.GUID, error) {
	guids := []windows.GUID{winsys.AdapterGUID}
	addrs, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagDefault)
	if err != nil {
		return guids, err
	}
	tunnelSeen := false
	for _, a := range addrs {
		guid, _ := a.LUID.GUID()
		isWintun := guid != nil && *guid == winsys.AdapterGUID
		isTunnel := isWintun || strings.EqualFold(a.FriendlyName(), name)
		noise := a.IfType == winipcfg.IfTypeSoftwareLoopback || a.TunnelType == winipcfg.TunnelTypeTeredo
		if !isTunnel && (a.OperStatus != winipcfg.IfOperStatusUp || noise) {
			continue
		}
		mark := ""
		switch {
		case isWintun:
			mark = "  [dnstt-cli wintun]"
		case isTunnel:
			mark = "  [tunnel " + name + "]"
		}
		if isTunnel {
			tunnelSeen = true
			if guid != nil && *guid != winsys.AdapterGUID {
				guids = append(guids, *guid)
			}
		}
		fmt.Fprintf(w, "   %s (index %d, %s, MTU %d)%s\n",
			a.FriendlyName(), a.IfIndex, operStatus(a.OperStatus), a.MTU, mark)
		if isTunnel {
			fmt.Fprintf(w, "      %s — %s\n", a.Description(), guidString(guid))
		}
		var ips []string
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			ip := sockaddrAddr(u.Address)
			if ip.IsLinkLocalUnicast() && !isTunnel {
				continue
			}
			ips = append(ips, fmt.Sprintf("%s/%d", ip, u.OnLinkPrefixLength))
		}
		fmt.Fprintf(w, "      addresses: %s\n", listOrNone(ips))
		fmt.Fprintf(w, "      metric: IPv4 %s, IPv6 %s\n",
			interfaceMetric(a.LUID, windows.AF_INET), interfaceMetric(a.LUID, windows.AF_INET6))
		var dns []string
		for d := a.FirstDNSServerAddress; d != nil; d = d.Next {
			dns = append(dns, sockaddrAddr(d.Address).String())
		}
		fmt.Fprintf(w, "      DNS: %s\n", listOrNone(dns))
	}
	if !tunnelSeen {
		fmt.Fprintf(w, "   no tunnel adapter (%q or the wintun GUID)\n", name)
	}
	return guids, nil
}

// interfaceMetric is the interface metric of the family from the winipcfg table.
func interfaceMetric(luid winipcfg.LUID, family winipcfg.AddressFamily) string {
	row, err := luid.IPInterface(family)
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return "none"
	}
	if err != nil {
		return "error: " + err.Error()
	}
	if row.UseAutomaticMetric {
		return fmt.Sprintf("%d (auto)", row.Metric)
	}
	return fmt.Sprint(row.Metric)
}

func printRoutes(w io.Writer) error {
	names := interfaceNames()
	var routes []route
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		rows, err := winipcfg.GetIPForwardTable2(family)
		if err != nil {
			return err
		}
		for _, r := range rows {
			p := r.DestinationPrefix.Prefix()
			if !interestingRoute(p) {
				continue
			}
			routes = append(routes, route{
				prefix:  p,
				nextHop: r.NextHop.Addr(),
				iface:   names[r.InterfaceLUID],
				index:   r.InterfaceIndex,
				metric:  r.Metric,
				ifMet:   interfaceMetric(r.InterfaceLUID, family),
			})
		}
	}
	sortRoutes(routes)
	if len(routes) == 0 {
		fmt.Fprintln(w, "   none")
	}
	for _, r := range routes {
		fmt.Fprintf(w, "   %-14s %-22s %s (index %d), metric %d + interface %s\n",
			r.prefix, hopString(r.nextHop), r.iface, r.index, r.metric, r.ifMet)
	}
	return nil
}

func interfaceNames() map[winipcfg.LUID]string {
	names := map[winipcfg.LUID]string{}
	addrs, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagIncludeAllInterfaces)
	if err != nil {
		return names
	}
	for _, a := range addrs {
		names[a.LUID] = a.FriendlyName()
	}
	return names
}

func printWFP(w io.Writer) error {
	s, err := wf.New(&wf.Options{Name: "checktunnel", Dynamic: true})
	if err != nil {
		return fmt.Errorf("WFP session (administrator rights needed): %w", err)
	}
	defer s.Close()
	sublayers, err := s.Sublayers(winsys.DNSGuardProvider)
	if err != nil {
		return fmt.Errorf("sublayers: %w", err)
	}
	rules, err := s.Rules()
	if err != nil {
		return fmt.Errorf("filters: %w", err)
	}
	byLayer := map[string]int{}
	n := 0
	for _, r := range rules {
		if r.Provider != winsys.DNSGuardProvider {
			continue
		}
		n++
		byLayer[layerName(r.Layer)]++
	}
	fmt.Fprintf(w, "   provider sublayers: %d, filters: %d\n", len(sublayers), n)
	var layers []string
	for l, c := range byLayer {
		layers = append(layers, fmt.Sprintf("%s — %d", l, c))
	}
	sort.Strings(layers)
	for _, l := range layers {
		fmt.Fprintf(w, "      %s\n", l)
	}
	return nil
}

func layerName(l wf.LayerID) string {
	switch l {
	case wf.LayerALEAuthConnectV4:
		return "outbound IPv4"
	case wf.LayerALEAuthRecvAcceptV4:
		return "inbound IPv4"
	case wf.LayerALEAuthConnectV6:
		return "outbound IPv6"
	case wf.LayerALEAuthRecvAcceptV6:
		return "inbound IPv6"
	}
	return l.String()
}

func printRegistry(w io.Writer, guids []windows.GUID) {
	for _, k := range []string{nrptKey, nrptPolicyKey} {
		fmt.Fprintf(w, "   NRPT HKLM\\%s: %s\n", k, subkeyCount(k))
	}
	for _, g := range guids {
		for _, base := range []string{tcpipKey, tcpip6Key} {
			fmt.Fprintf(w, "   HKLM\\%s%s: %s\n", base, g.String(), nameServer(base+g.String()))
		}
	}
}

func subkeyCount(path string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "no key"
	}
	if err != nil {
		return "error: " + err.Error()
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("subkeys: %d", len(names))
}

func nameServer(path string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "no key"
	}
	if err != nil {
		return "error: " + err.Error()
	}
	defer k.Close()
	v, _, err := k.GetStringValue("NameServer")
	if errors.Is(err, registry.ErrNotExist) {
		return "no NameServer"
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("NameServer=%q", v)
}

func operStatus(s winipcfg.IfOperStatus) string {
	switch s {
	case winipcfg.IfOperStatusUp:
		return "Up"
	case winipcfg.IfOperStatusDown:
		return "Down"
	case winipcfg.IfOperStatusNotPresent:
		return "NotPresent"
	case winipcfg.IfOperStatusLowerLayerDown:
		return "LowerLayerDown"
	}
	return fmt.Sprintf("state %d", s)
}

func sockaddrAddr(sa windows.SocketAddress) netip.Addr {
	switch p := sa.Sockaddr; {
	case p == nil:
		return netip.Addr{}
	case p.Addr.Family == windows.AF_INET:
		return netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(p)).Addr)
	case p.Addr.Family == windows.AF_INET6:
		return netip.AddrFrom16((*windows.RawSockaddrInet6)(unsafe.Pointer(p)).Addr)
	}
	return netip.Addr{}
}

func guidString(g *windows.GUID) string {
	if g == nil {
		return "GUID unknown"
	}
	return g.String()
}
