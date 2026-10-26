# Roadmap

maat is built in small milestones. Each one has deliverables, acceptance criteria that can be checked, and something worth showing publicly. Durations assume roughly 8 to 10 hours a week alongside a full-time job. Facts (F) and assumptions (A) are defined in [references.md](references.md).

Mark items done only when their acceptance criteria pass.

## Status (2026-10-26)

| Milestone | State | Done | Not done |
|---|---|---|---|
| M0 | Mostly done | Strict snap built and run on 24.04; journal, units and the network and write denials confirmed; A2–A5, A7, A8, A10 and A15 resolved | 26.04; the snap name (A11); the screen recording |
| M1 | Mostly done | All read-only tools, the condense pipeline, `maat diagnose`, CI with fixture tests, fuzz tests, `govulncheck` and invariants 1–4 | The one-million-entry budget test; invariant 5 as an automated check; the `edge` channel |
| M2 | Partly done | Harness, deterministic scorer, scenario format, 10 scenarios (2 adversarial, 2 healthy), reference solutions run by the oracle, run configs and transcripts | 20 scenarios; 5 adversarial; **any real-model run**; the ablation; reproducibility across runs |
| M3 | Done | Plan schema v0, catalog, `propose_action`, the trail with locking and journal checkpoints, `maat trail verify`; tests for edits, deletions, reordering and truncation; plan validation fuzz-tested | |
| M4 | Partly done | `maatctl` with three action kinds; polkit from a terminal (A7); the benchmark's fix track through the real `maatctl` under a scoped polkit rule; invariants 6–9; packaging: the snap and static `maatctl` binaries for amd64 and arm64 ship as release assets, starting with v0.1.0 ([releasing](releasing.md)) | Automated `snap.revert` rollback (so the rollback acceptance criterion can't pass yet); the `netplan.try` note (A6) |
| M5 | Not started | | |

The single most important next step is M2's real-model runs, preceded by harder scenarios: a keyword baseline already scores 9 of 10 on the current set.

## M0: Prove the confinement story (1 to 2 weeks)

The whole design depends on a few things being true on real machines, so they get tested first.

Deliverables:

- A minimal Go program, packaged as a strict snap with the four planned plugs, that reads journal entries, lists failed units over D-Bus and serves one MCP tool over stdio.
- The same program tries to open an outbound TCP connection and to write under `/etc`, and records both denials.
- Tested from Claude Code, and from OpenCode with an inference snap, on Ubuntu 24.04 and 26.04.

Acceptance:

- A1 to A5, A8, A12 and A15 are each confirmed or refuted in `references.md`, and the design is updated wherever one was refuted.
- The snap name `maat` is registered (A11).

Show: a one-minute screen recording. An injected log line tells the agent to send data out, the agent tries, and the kernel refuses.

## M1: The read-only server (3 weeks)

Deliverables:

- The eleven read-only tools listed in the README.
- The condense pipeline: normalization, redaction, fencing, Drain clustering, ranking, budgets and evidence IDs.
- `maat diagnose`.
- The snap published to the `edge` channel.
- CI with fixture-based unit tests, parser fuzz tests, `govulncheck`, and security invariants 1 to 5.

Acceptance:

- Every tool stays within its budget on a journal of at least one million entries.
- Invariants 1 to 5 pass in CI.
- The snap installs and works on both supported releases.

Show: a local model diagnosing a crash-looping service, with token counts before and after condensing.

## M2: maat-bench v0 (3 weeks)

Deliverables:

- The harness, the deterministic scorer and the scenario format.
- At least 20 scenarios, including at least 5 adversarial ones and 2 healthy machines.
- A reference solution for every scenario, run by the harness to prove the scenario can be solved and its health check works.
- Published runs for at least three local models through inference snaps, and optionally one hosted model, with the methodology, redacted transcripts and scores.
- An ablation comparing condensed tools with raw tools on the same scenarios.

Acceptance:

- Re-running a published configuration reproduces its scores within the reported range.
- A9, A10, A14, A16 and A17 are resolved.

Show: the results chart and write-up, posted on Ubuntu Discourse and LinkedIn.

## M3: Plans and the trail (2 weeks)

Deliverables:

- Plan schema v0, the action catalog and `propose_action`.
- The trail, with locking and journal checkpoints.
- `maat trail verify`.

Acceptance:

- Tests show that any modification, deletion or reordering of trail records is detected.
- Plan parsing and validation are fuzz-tested.

## M4: The actuator (3 weeks)

Deliverables:

- `maatctl` with `service.restart`, `service.reset-failed` and `snap.revert`, each with preview, apply, verify and rollback.
- polkit authentication working from a terminal (A7).
- A packaging decision for `maatctl`.
- A research note on `netplan.try` and its authorization model (A6).
- The benchmark's fix track running through the real `maatctl` code, using the `maatbench` build.

Acceptance:

- Invariants 6 to 9 pass in CI.
- On a benchmark VM, a deliberately failing action triggers its rollback, and the trail shows the whole sequence.

Show: the full loop on video. An injected instruction is rejected by the catalog, then a legitimate fix is approved through polkit, verified, and recorded.

## M5: v0.1 release (1 to 2 weeks)

Deliverables:

- A documentation site.
- The snap on the `beta` channel.
- An announcement on Ubuntu Discourse.
- A talk proposal for the next Ubuntu Summit.
- A public invitation for security review.

Acceptance: someone new can go from install to a first diagnosis in under five minutes, using only the README.

## Later

- More action kinds, for example package operations through PackageKit (a `packagekit-control` snap interface exists, F1).
- arm64 support.
- Several machines at once: remote use over SSH, or integration with existing fleet tools.
- Interactive permission grants using the snap prompting capability that landed in Ubuntu 26.04 LTS (F20).
- Landlock as an extra confinement layer (A13).

## Working upstream

When maat runs into a bug or a documentation gap in snapd, netplan, systemd or LXD, the fix goes upstream with a minimal reproduction instead of becoming a workaround here. One idea worth raising with snapd maintainers, as a question rather than a demand, is whether a strictly read-only variant of `log-observe` would be useful, given what F3 shows.
