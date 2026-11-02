---
kind: stage
status: todo
---

# System VPN on Windows

Goal (`architecture.md` §12, E3): the same as the Linux stage, on Windows — Wintun, WFP in
a dynamic session, one app ID for the whole program. `architecture.md` R6, R7, §05, §05a,
§09.

Detailed when its turn comes; below is the outline from `architecture.md`.

## Done when

- Wintun next to the exe; the adapter is created with the GUID from `winsys.AdapterGUID`.
- WFP filters in a dynamic session, so they disappear with the process even after `kill`;
  our own process passes DNS protection by app ID — one program, unlike csqtt-cli.
- IPv6 leads into the adapter and netstack rejects it, or WFP closes it when IPv6 is off on
  the adapter.
- The kill-switch has the same exceptions as on Linux.
- A mutex refuses a second instance.
- Elevation through UAC, as in csqtt-cli.
- `tun` live acceptance on Windows.

## Outline

1. Wintun and the adapter.
2. Netstack and the stream datapath, shared with Linux where the code allows.
3. DNS, UDP and IPv6 as on Linux, with the Windows mechanism.
4. WFP: DNS protection, kill-switch, one app ID.
5. Elevation, the single-instance mutex, the registry key left behind (removed on the next
   `connect`).
6. Live acceptance on Windows.
