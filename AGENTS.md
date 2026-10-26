# AGENTS.md

Instructions for AI coding agents, and for humans, working on maat.

## What this project is

maat is a confined, auditable MCP server and toolkit for diagnosing and repairing Ubuntu systems with AI agents, plus a benchmark called maat-bench. Read these in order before changing anything:

1. [README.md](README.md)
2. [docs/architecture.md](docs/architecture.md)
3. [docs/security-model.md](docs/security-model.md)
4. [docs/references.md](docs/references.md)

**Current status: developer preview.** The server, CLIs, actuator, trail and benchmark harness are implemented and tested; the strict snap and the full review loop were verified on a VM (see [docs/evidence/](docs/evidence/)). No real model has been benchmarked yet. A few items remain research-only (for example `netplan.try`) or manual (`snap.revert` rollback); never describe those as done.

## Rules

1. **Don't fill gaps from memory.** Snap interface behavior, D-Bus method signatures, polkit action IDs, MCP SDK APIs, CLI flags and version numbers must come from [docs/references.md](docs/references.md). If what you need isn't there, find a primary source (official docs, source code or a specification) and add it as a new F-entry with a link and the date. If you can't verify it, add an A-entry with a test plan, and say so in your change.
2. **Never fabricate results.** No invented benchmark numbers, test output, performance figures or quotes. If you didn't run something, say you didn't.
3. **The server stays read-only and offline.** Don't add snap plugs beyond those listed in [docs/architecture.md](docs/architecture.md) without updating [docs/security-model.md](docs/security-model.md) in the same change. Never add the `network` plug.
4. **No shells.** Use `exec` with a fixed argument list, or typed APIs such as D-Bus and REST. Never use `sh -c`, and never build a command line from strings.
5. **Everything the model sees goes through the condense pipeline:** redact, fence, cluster, budget.
6. **Every action kind is complete:** preview, apply, verify, and rollback (or a documented reason there is none), all with tests.
7. **Respect Canonical's trademark policy.** Don't put "Ubuntu" in project, package or command names, and don't imply that Canonical endorses maat (F26).
8. **Keep the security invariants passing.** They're listed at the end of [docs/security-model.md](docs/security-model.md).

## Repository layout

See [docs/architecture.md](docs/architecture.md#repository-layout). In short: `cmd/maat` (server, in the snap), `cmd/maatctl` (actuator, outside it), `internal/*` (collectors, condense pipeline, MCP server, plans, plan store, actuator, trail, terminal sanitizing, self-test), `bench/` (scenarios, harness, results), `snap/`, `scripts/`, `docs/`.

## Toolchain

- Go 1.26 or 1.27, the two currently supported releases (F19). Module path: `github.com/bolesawny7/maat`.
- MCP: `github.com/modelcontextprotocol/go-sdk` v1.8 (F15).
- Checks: `make all race`, `make fuzz`, and `govulncheck ./...`. Also vet the benchmark build: `go vet -tags maatbench ./cmd/maatctl`.
- Snap: Snapcraft, base `core24`, strict confinement; tested on Ubuntu 24.04 (arm64). 26.04 is untested.
- Benchmark: LXD; `make bench-bin` builds what it pushes into containers.

## Conventions

- Wrap errors with `%w` and say what failed. No panics in library code.
- `context.Context` is the first parameter of anything that does I/O.
- Prefer table-driven tests. Collectors are tested against recorded fixtures, and unit tests never read the live system.
- Keep commits small and focused, with Conventional Commit messages and a DCO sign-off (`git commit -s`).
- Write docs in plain English, link sources, and label planned features as planned.

## Checklists

### Adding an MCP tool

- [ ] Name the snap interface that covers the data, and cite its F-entry, or add an A-entry and test it **inside the snap** (`maat selftest`, or a call through the confined server). Unconfined runs, including the benchmark, don't prove anything about confinement.
- [ ] If the data can't be read under the snap, return an error that says why. Never return an empty result for something you couldn't read.
- [ ] Attacker-influenced text goes through `redact` (redaction and clipping); lists go through `fit` with `max_items` and `max_chars`.
- [ ] Every item carries an `evidence_id`, and the handler registers it with `d.evidence.add` so `propose_action` can attach it to a plan.
- [ ] Annotations are set honestly.
- [ ] Fixture-based tests, including hostile input: injection strings, very long lines, invalid UTF-8.
- [ ] The tool table in the README is updated.
- [ ] Anything printed to a terminal goes through `internal/termsafe`.

### Adding an action kind

- [ ] The API and its polkit action are documented as F-entries.
- [ ] Parameter validation, preconditions, verification and rollback are implemented and tested.
- [ ] A threat review is added to [docs/security-model.md](docs/security-model.md).
- [ ] A benchmark scenario exercises it.

### Adding a benchmark scenario

- [ ] The reference solution passes the harness's reference check.
- [ ] The root cause comes from the taxonomy, or the taxonomy is extended in the same change.
- [ ] The evidence specification is specific enough to score.
- [ ] `real_world` links to a bug report or forum thread showing the failure happens in practice.

## AI-assisted contributions

AI help is welcome; unreviewed AI output isn't. Whoever opens a pull request must understand and have tested every line in it, and should say which parts were AI-assisted.
