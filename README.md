# dnstt-cli

A desktop console client for [dnstt](https://www.bamsoftware.com/software/dnstt/): a local
SOCKS5 proxy or a system-wide VPN through a DNS tunnel, with reconnection, a list of
resolvers, profiles and secret storage. One Go binary for Linux and Windows (macOS — the
proxy mode only), the dnstt client core built in.

> **In development.** Nothing works yet: the repository is at its foundation stage. What
> is planned and in which order — [`doc/architecture.md`](doc/architecture.md) and
> [`doc/plan.md`](doc/plan.md).

This is only a client. The server side stays with dnstt itself: `dnstt-server` and
whatever its owner runs behind it (a SOCKS5 proxy with a login is the main case).

## Credits

The tunnel core is dnstt by David Fifield, public domain (CC0), carried into `core/` from
the [FreedomCats mirror](https://github.com/FreedomCats/dnstt) of the upstream. The shell —
profiles, secrets, routes, DNS protection, kill-switch, network tracking — is based on
[csqtt-cli](https://github.com/FreedomCats/csqtt-cli) (MIT).

## Building

Go 1.26 or newer:

    go build ./cmd/dnstt-cli
