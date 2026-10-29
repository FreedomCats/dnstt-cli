# Project rules for agents

## Language

Everything in the repository is in English: code comments, documentation in `doc/`,
`README.md`, `AGENTS.md` and other project files, project memory and working notes
(`doc/local/`), commit messages, release notes. Comments in code carried over from other
projects (qwdtt-cli, csqtt-cli, the dnstt upstream) may stay as they are; translating them
is optional.

Conversation with the user is in the user's language (Russian), not in English.

## Personal data — never in the repository

**This is about the developer's safety, not tidiness.** If the developer is identified,
their life, health and freedom are at risk. Nothing that could identify them may get into
the repository: real name, email, IP addresses, profile names, employer, addresses of
servers and resolvers, details of the work environment and OS, user name — and in general
any detail that makes them easy to find. When in doubt, do not write it down.

The repository is public. Documentation (`README.md`, examples), code, tests, comments and
commit messages **must not** contain:

- the user's real name, their emails (personal and work), the name of their personal
  GitHub account and other accounts, their employer;
- the developer's environment: the distribution and machine the work is done on, user
  name, groups, home directories (`/home/…`, `C:\Users\…`), work networks;
- dates and times of work: when something was done, checked, compared;
- the user's external IP addresses: home address and VPS address;
- the name and address of the dnstt server: tunnel domain, the domain of its name server
  (NS), IP, port;
- the dnstt server's public key — neither in full nor shortened: it identifies the server
  just as well as its address;
- the resolvers through which the tunnel works for the user: DoH and DoT addresses, UDP
  resolvers, especially the ISP's and the router's resolvers — they reveal the ISP and
  the region. This includes lists of "working resolvers" from live runs;
- home network addresses (ULA prefix, local machine addresses) and corporate network
  addresses (corporate VPN server, corporate DNS, search domains);
- credentials of the tunnel exit on the server: logins, passwords and keys of the SOCKS5
  proxy or SSH;
- the user's profile names. The exception is `home`: it is the example name in code,
  tests and README and may stay;
- passwords, keys and any values from the user's secret and profile files.

Use words or placeholders instead: "VPS address", "home address", "dnstt server", "ISP
resolver", `<profile>`, `<key>`, `10.x`, `192.168.1.x`, `fd…`, user `user`. Domains — the
reserved `t.example.com`, `tns.example.com`, `doh.example`, `dot.example` (RFC 2606). In
tests — documentation addresses (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`,
`2001:db8::/32`) and made-up values; keys are generated for the test, never taken from a
live server.

These values may be shown to the user in chat. Not in repository files.

## Documents in `doc/`: what is published and what is not

Everything directly in `doc/` is in the repository. All the rules above apply to these
documents: no dates of work, no time of day ("at night", "late"), no environment or
machine, no names or paths — only the substance. Results of live checks may be written
down — without the environment, link speeds, resolvers and addresses; the day-by-day
course of work goes only into the logs.

Two subdirectories are outside git (`.gitignore`):

- `doc/local/` — stage plans and logs `plan-*.md` (they inevitably contain the
  environment and dates), working notes (the resume point for new sessions —
  `doc/local/resume.md`) and copies of documents before cleanup;
- `doc/runs/` — scripts and logs of live runs.

Check a new document for publication against this file before `git add`; put a draft
with environment details into `doc/local/`.

## Third-party code under an incompatible license

The dnstt-cli license is not chosen yet (`doc/analysis.md`, §6). Until it is, the strict
variant applies — as for a shell under MIT — so that neither option is closed off.

The implementer — whoever writes the code — does not open code under a license
incompatible with the shell's license. Currently that is:

- the GPL-3 parts of qwdtt-cli: `internal/core` (vendored qWDTT),
  `supervisor/restart.go`, `captcha/token.go`, `captcha/solver.go` and their tests;
- the Android clients of qWDTT and CSQTT in Kotlin and the CSQTT core (`core/` in
  csqtt-cli) — PolyForm Noncommercial.

The rest of csqtt-cli (MIT) and the dnstt core (public domain, CC0) may be read and
carried over.

If it is necessary to know what code closed to the implementer does, another participant
reads it — the coordinator or a separate agent who will not write the code afterwards. They
give the implementer a description of the behaviour in their own words: which requests
and fields, the order of steps, timings, conditions, observable results. The description
does not repeat the structure of the code, names of functions and variables, or the order
of lines; code fragments, even paraphrased, are not passed on. Protocols, formats and
behaviour are facts and are implemented anew.

Where the description came from (a file of the other project, without paths from the
machine) is recorded in the stage log (`doc/local/`).

## Commits

Author and committer — only `FreedomCats <noreply@freedomcats.invalid>` (set in the
repository's `.git/config`). The date is today's, but without the time: midnight UTC.
Neither the time of day nor the time zone gets into the history:

    d=$(date -u +%F)T00:00:00Z
    GIT_AUTHOR_DATE=$d GIT_COMMITTER_DATE=$d git commit …

The `.invalid` domain is reserved (RFC 2606): nobody has such an email, and GitHub will
not attribute the commits to someone else's account.

The same applies to `--amend`, `rebase` and `cherry-pick`: git sets the committer date to
the current time.

Release tags (`v*`) — lightweight only: `git tag v0.1.0`, without `-a`, `-s` or `-m`. An
annotated tag stores the name, email and date of whoever created it.

**Before every commit** check `git diff --cached` for everything listed above: addresses
obtained in live runs, values from the dnstt-cli config directory, the server's domain and
key, resolvers, exit credentials, paths and names from the developer's machine, dates.
After the commit —
`git log -1 --format='%an <%ae> %ad | %cn <%ce> %cd' --date=iso-strict`. The forbidden
values themselves are never written into this file or into check scripts.

If something like that does get into the history — tell the user immediately. Cleaning
the history (`git filter-repo`) rewrites all commit hashes and is done only with the
user's consent.
