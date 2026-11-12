---
kind: task
status: todo
---

# The MIT license

## Task

The shell's license is chosen: **MIT**. The user made the decision on `analysis.md` §6, which
recommends it: all the shell code dnstt-cli needs is in csqtt-cli under MIT, the GPL parts of
qwdtt-cli that would justify GPL are not needed, and the dnstt core is public domain (CC0). No
other choice was made, so the strict rule stays as it is in substance: the implementer does
not open code under a license incompatible with MIT, which keeps the GPL parts of qwdtt-cli
and the AGPL-3 code of SlipNet and SlipGate closed (`AGENTS.md`).

The record:

- `LICENSE` at the root with the exact MIT text; the copyright line names the project
  (`FreedomCats`, as in the commits), never the developer's real name (`AGENTS.md`);
- `README.md` gets a short "License" section naming MIT, saying what it permits and pointing
  at `LICENSE`;
- `AGENTS.md`'s third-party section stops saying the license is not chosen yet and that the
  strict variant applies until it is; it states that the shell is MIT (`LICENSE`), that the
  strict rule is the MIT one, and keeps its list of parts that stay closed — the GPL-3 parts
  of qwdtt-cli, the AGPL-3 SlipNet and SlipGate, the PolyForm core of csqtt-cli — and of parts
  that may be carried, csqtt-cli (MIT) and the dnstt core (CC0);
- `doc/analysis.md` §6 records the choice in place of the recommendation;
- `doc/architecture.md` §11 says the shell is MIT instead of "not chosen; MIT recommended" and
  its license table stays otherwise as it is.

Out of scope: source-file headers (there are none), `THIRD_PARTY_NOTICES.txt` and the
archives' copies of `LICENSE` (the release task), and the core's `core/COPYING`.

## Done when

- `LICENSE` at the root holds the exact MIT text; the copyright line names the project, not a
  person.
- `README.md`, `AGENTS.md`, `doc/analysis.md` §6 and `doc/architecture.md` §11 name MIT as the
  shell's license; no line says it is not chosen or recommends another.
- `AGENTS.md`'s rule is consistent: the same list of closed and open parts, with MIT as the
  license it is measured against, and no contradiction with `doc/analysis.md` §6 and
  `doc/architecture.md` §11.
- Link, whitespace and privacy checks pass; no date, environment or personal detail is in the
  changed files.
