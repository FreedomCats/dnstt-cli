module github.com/FreedomCats/dnstt-cli

go 1.26.2

require (
	github.com/flynn/noise v1.0.0
	github.com/godbus/dbus/v5 v5.1.0
	github.com/google/nftables v0.3.1-0.20260430172505-f9b52ed2ba65
	github.com/refraction-networking/utls v1.7.3
	github.com/tailscale/wf v0.0.0-20240214030419-6fbb0a674ee6
	github.com/vishvananda/netlink v1.3.1
	github.com/xtaci/kcp-go/v5 v5.6.8
	github.com/xtaci/smux v1.5.24
	golang.org/x/crypto v0.54.0
	golang.org/x/net v0.57.0
	golang.org/x/sys v0.49.0
	golang.org/x/term v0.47.0
	golang.zx2c4.com/wireguard/windows v1.1.1
)

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/cloudflare/circl v1.5.0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/klauspost/compress v1.17.4 // indirect
	github.com/klauspost/cpuid/v2 v2.2.6 // indirect
	github.com/klauspost/reedsolomon v1.12.0 // indirect
	github.com/mdlayher/netlink v1.8.1-0.20251028132421-dcc6cab9a6eb // indirect
	github.com/mdlayher/socket v0.5.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/templexxx/cpu v0.1.0 // indirect
	github.com/templexxx/xorsimd v0.4.2 // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	go4.org/netipx v0.0.0-20220725152314-7e7bdc8411bf // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	golang.org/x/tools/go/expect v0.1.1-deprecated // indirect
	honnef.co/go/tools v0.5.1 // indirect
)

// tailscale/wf — from our fork, as in csqtt-cli: the library's arena handed out unaligned
// memory, and writing a WFP rule during GC marking crashed the process ("bulkBarrierPreWrite:
// unaligned arguments"). The fork has one change, malloc.go, with a test; upstream has not
// moved since 2024. Our regression test is TestDNSGuardUnderGC in internal/winsys.
replace github.com/tailscale/wf => github.com/FreedomCats/tailscale-wf v0.0.0-20261016000000-b03557658b85
