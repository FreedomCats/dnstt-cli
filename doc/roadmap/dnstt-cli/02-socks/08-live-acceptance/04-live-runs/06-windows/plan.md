---
kind: stage
status: blocked
depends: [01-runbook]
---

# Windows

The same acceptance on Windows, deferred by the user until a Windows machine is available.
The stage is `blocked` and has no children yet: it is split when the machine exists, along
the same result boundaries as the Linux children.

What the stage has to cover on Windows:

- the SOCKS5 datapath against the synthetic local exit (the local `dnstt-server` reachable
  over the network), with the browser and the local SOCKS5 proxy as on Linux;
- the HTTP exit with Basic authentication over DoH, DoT and UDP, including the first real
  application CONNECT and the wrong-password 407;
- the authenticated comparison with the upstream client and the independent HTTP CONNECT
  probe, over a working DoH and a working UDP resolver;
- the dead-resolver check, the eight presets and the `DefaultUTLS` fingerprints through
  DoH and DoT;
- the clean stop and the host-unchanged checks, with the same evidence and attribution
  rules as the Linux children.

The Windows store is DPAPI, so the tool's `secret` must run under the same account that
runs `connect`; the temporary profile is prepared on that machine and deleted after. The
user's real values are needed again for the real-server checks. No Windows item is counted
as seen before the stage runs; the Linux evidence stands on its own, and the published
result keeps the platforms separate.

## Children

None yet: the stage is blocked and is split when a Windows machine is available.

## Done when

- The stage is split into children that cover the checks above, once a Windows machine and
  the user's values are available; each child has its own result and criteria.
- All children are done: the same matrix as the Linux children is seen on Windows with the
  platform-specific secret store and the same attribution rules, or the reason each item
  cannot be run is recorded.
- The published result keeps the platforms separate and does not describe the Linux run as
  the Windows one.
