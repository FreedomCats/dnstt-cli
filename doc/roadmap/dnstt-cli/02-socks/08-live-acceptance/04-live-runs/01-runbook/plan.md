---
kind: task
status: done
detailed: true
---

# Runbook and evidence form

## Task

The checklist the agent fills during every check of [Live runs](../plan.md), and the form
that keeps the results comparable. The checklist itself lives in ignored
`doc/local/runbook-socks-acceptance.md`, next to the TUN runbook; this node carries only
its purpose and acceptance. Every value that is real or generated is a placeholder
(`<domain>`, `<server-key>`, `<http-login>`, `<doh-resolver>`, …), so the checklist can be
read without the user's environment; the filled copy and the raw evidence stay in ignored
`doc/local/` and `doc/runs/`.

The checklist covers the whole stage: the synthetic SOCKS5 run (browser, local server
restart, container network change, clean stop, host unchanged), the real-server HTTP exit
(DoH, DoT, UDP, first-CONNECT authentication, 407), the authenticated comparison, the dead
resolver, the presets, the fingerprints and the blocked Windows part. Each check has a
`Result:` line with `seen`, `not seen — <reason>` or `failed — <what happened>` and the
side (server, exit, resolver, network, product); no result line carries a domain, key,
address, resolver, login or date.

## Steps

1. Read [the stage](../plan.md), its parent and the accepted tooling and run nodes:
   `02-live-run-script` for the tool, `03-timeouts-and-danted/08-synthetic-danted` for the
   synthetic launcher, `03-tun-linux/06-live-acceptance/07-browser-container` for the
   container browser, and `doc/local/runbook-tun-linux.md` for the form and the privacy
   rules. Fix the layout: `doc/runs/live-socks/<run-id>/` for raw evidence, the filled
   checklist at `doc/local/runbook-socks-acceptance-filled.md`. `git check-ignore -v` must
   name both paths; never `git add -f` them.
2. Write the header: who runs the checks (the agent, not the user), what each scope means
   (synthetic local SOCKS5; the user's real server for HTTP; Windows blocked), the rule
   that a synthetic result never stands for the user's exit, and the privacy rule from
   `AGENTS.md` (no values, addresses, resolvers, logins, dates or environment details in
   tracked files; raw evidence ignored).
3. Write the placeholders table: `<profile>`, `<domain>`, `<server-key>`, `<http-login>`,
   `<http-password>`, `<doh-resolver>`, `<dot-resolver>`, `<udp-resolver>`,
   `<dead-resolver>`, `<site>`, `<target>`, `<local-port>`, `<server-ip>`, `<run-id>`,
   `<image-id>`, `<danted-version>`, `<keepalive>`, `<handshake>`, `<degraded>`. Mark which
   are real user values supplied in chat, which are synthetic, and which are fixed by the
   code; the real ones never enter this node.
4. Write the preconditions: a container runtime and the local `debian:trixie` image; the Go
   toolchain and the module cache; the upstream checkout for `compare` or a ready
   `dnstt-client`; the build commands (`GOPROXY=off CGO_ENABLED=0 go build ...`); the host
   baseline command set (IPv4 and IPv6 routes, resolv.conf checksum, links, Docker
   objects, `git status`); the permissions to obtain per run (Chromium install; the
   `danted` source and build dependencies) and the rule that each is requested before the
   fetch and a refusal stops the task.
5. Write the run design section from the stage: the two-container synthetic setup and why
   no local DoH or DoT is built; the browser and the product in one container with the
   proxy on `127.0.0.1:<local-port>` and Chromium's `--proxy-server`; the restart with the
   same key, domain and port; the network change as the client container's address change
   inside its own netns; the real-server client container and the temporary profile; the
   target `<target>`; the clean-stop and host-unchanged checks.
6. Write the check sections with the commands left as placeholders: S0 environment and
   permissions; S1 synthetic browser through the local SOCKS5 proxy; S2 restart; S3 network
   change; S4 clean stop and host unchanged; R0 the temporary real-server profile and a
   control fetch; R1 DoH; R2 DoT; R3 UDP; R4 first-CONNECT authentication and the
   wrong-password 407; R5 comparison over DoH; R6 comparison over UDP; R7 dead resolver;
   R8 presets; R9 fingerprints; R10 clean stop and host unchanged; W1 Windows blocked. Each
   section names the observation, the expected result, the failure side and the evidence
   file.
7. Write the evidence form: a header per run (run id, image id, versions, the permissions
   recorded) and the table `Check | Result | Side | Evidence | Notes`; a result is only
   `seen`, `not seen — <reason>` or `failed — <what happened>`. Add the cleanup line (the
   temporary profile and the stored secret deleted, the containers removed, the host
   compared with the baseline) and the publication rule (only sanitized outcomes go to the
   stage Outcome and to `05-record-results`).
8. Cross-check the runbook against this node and the stage body; the stage states the
   runbook path and the filled-copy path. Run the format, link and privacy pass
   (placeholder markers, no real values, `git diff --check`). The task is complete when
   the checklist is ready; the filled copy and the runs belong to the other children. No
   product code, test, profile schema or dependency changes.

## Done when

- `doc/local/runbook-socks-acceptance.md` exists, is ignored, and contains placeholders
  only; no real domain, key, address, resolver, login, date or environment detail is in it
  or in this node.
- It covers all items of [Live runs](../plan.md): the synthetic SOCKS5 browser, the local
  server restart, the container network change, the clean stop and host check; the real
  server's DoH, DoT and UDP; first-CONNECT authentication and the 407; the comparison over
  DoH and UDP; the dead resolver; the eight presets; the fingerprints; the blocked Windows
  part.
- The result form is fillable per check with `seen`/`not seen`/`failed`, the side and the
  evidence file; the cleanup and publication rules are written.
- The run design in the checklist matches the stage: no local DoH or DoT, the browser and
  the product in one container with the loopback proxy, the network change scoped to the
  container's address, the host-unchanged baseline and after-check.
- Links and headings are consistent, English, `git diff --check` passes, no tracked file
  gained a live value, and no code, test or dependency changed.

## Outcome

The checklist is written in ignored `doc/local/runbook-socks-acceptance.md`, and the filled
copy is `doc/local/runbook-socks-acceptance-filled.md`. Both are ignored by `git check-ignore`
and are never force-added.

The checklist covers the whole stage: S0 (environment and the one permission for both
container-internal fetches), S1–S4 (the synthetic browser, the local server restart, the
client container's address change, the clean stop and the host comparison), R0–R10 (the
temporary real-server profile and the control fetch, DoH, DoT and UDP, the first-CONNECT
authentication and the wrong-password 407, the comparison over DoH and UDP, the dead
resolver, the eight presets, the five `DefaultUTLS` fingerprints, and the real-server clean
stop and host check), and W1 (the blocked Windows part).

The design follows the stage: the two-container synthetic setup, the browser and the product
in one container with the loopback proxy, no local DoH or DoT, the restart with the same
key, domain, port and upstream, the address change inside the client's own network namespace,
the host baseline before and after. Every check has a `Result:` line with the `seen`,
`not seen — <reason>` or `failed — <what happened>` form, the side and the evidence file; the
result form has the per-run header and the `Check | Result | Side | Evidence | Notes` table,
and the cleanup and publication rules are written.

The values are placeholders only; the real ones are supplied in chat and written only to the
ignored temporary profile. The evidence goes to `doc/runs/live-socks/<run-id>/`; the filled
copy to `doc/local/`. No tracked file gained a live value, no code, test or dependency
changed, and `git diff --check` passes.
