# Security model

maat's job is to let a model look at a real machine without being able to hurt it. This document says precisely what that means: what's protected, from whom, how, and what risk is left over. The invariants at the end say which are enforced by an automated test and which are not yet. Facts (F) and assumptions (A) are defined in [references.md](references.md).

## What we protect

- **Host integrity:** the system's files, services and configuration.
- **Confidentiality:** secrets and personal data that appear in logs.
- **Availability:** maat must never be the cause of an outage.
- **The record:** the trail must reflect what actually happened.

## Who we defend against

1. **Log writers.** Anyone who can get text into the logs: remote users through SSH usernames or logged HTTP requests, local users through `logger`, packages through unit descriptions. This is the main threat, because reading logs is maat's whole purpose.
2. **The model.** Assume it can be manipulated into wanting the wrong thing (prompt injection), or can simply be wrong. maat never relies on the model's intentions.
3. **Unprivileged local users** trying to use maat to do something they couldn't otherwise do.

Out of scope: root, kernel exploits, a compromised MCP client, and a compromised snapd, systemd or polkit. If any of those falls, maat falls with it.

## Trust boundaries

| Boundary | What crosses it | What enforces it |
|---|---|---|
| Model and server | Tool calls in, condensed data out | The MCP client, then maat's schema validation |
| Server and host | Reads only | AppArmor and seccomp, through snap interfaces (F1, F2, F3) |
| Server and plan store | Plan files | File permissions; `maatctl` validates everything again and trusts nothing the server wrote |
| You and `maatctl` | Confirmation | The controlling terminal (`/dev/tty`) |
| `maatctl` and system services | Typed API calls | polkit (F8, F10) |
| Writers and the trail | Records | Hash chain plus journal checkpoints (F14) |

## Deployment assumptions

maat secures its own tools, not the rest of your MCP client. If the client also gives the model a general shell, the model can do anything that shell allows. That includes running `maatctl` itself and riding on a cached polkit authorization (see T6). For the guarantees on this page to hold:

- Run the agent with maat's tools only, or with shell access turned off. The benchmark always runs this way.
- Run `maatctl` yourself, in your own terminal, never through the agent. Its confirmation reads the controlling terminal, which stops a piped answer, but a shell tool that allocates a pseudo-terminal could still type one.
- Run `maat mcp` as your own user, not root (it refuses root).
- On machines with sensitive data, use a local model.

## Threats and mitigations

### T1. Prompt injection makes the model propose something harmful

- The server can't act. Its plugs grant reads only (F2, F3), and it has no network.
- Only catalog actions can be proposed, and their parameters are validated. The service actions accept only `.service` units, so a log line can't talk the model into proposing `reboot.target`, and `service.restart` requires the unit to be failed.
- A person reviews the plan with the cited evidence: `maatctl show` prints the excerpt the server recorded for each evidence ID, and flags IDs the server never issued, so a fabricated justification stands out.
- polkit asks for admin authentication, or follows a rule the admin scoped to one unit and verb.
- Untrusted text is returned in data fields, never spliced into instructions, and the server's instructions say so. This reduces injection but can't prevent it; the benchmark exists to measure how often it still succeeds, and that measurement has not been made with real models yet.

**Residual risk:** a person can approve a bad plan. The rationale is labelled as the model's claim, and the evidence is shown so the reasoning can be checked.

### T2. Secrets in logs leave the machine

- The server has no `network` plug, so even a fully hijacked server can't send anything anywhere. Confirmed in a VM: an outbound connect from the confined process fails with `socket: permission denied` (F28). Testing also revealed that `network-observe` would re-enable outbound TCP, so it is excluded and an invariant test keeps it out.
- Redaction runs before any output.
- With a local model, nothing leaves the machine. With a hosted model, condensed and redacted log data goes to that provider. That's the user's choice, and the documentation says so plainly.

**Residual risk:** redaction is pattern-based and will miss some secrets.

### T3. A malicious log line exploits a bug in maat's parsers

- Confinement limits the damage to what the plugs allow.
- Parsers are fuzz-tested with hostile input: injection strings, very long lines, invalid UTF-8.
- `log-observe` also allows writing `kernel.printk_ratelimit` and AppArmor's audit setting, and grants the `dac_override` and `dac_read_search` capabilities (F3). Code that had taken over the server could, for example, set AppArmor auditing to quiet. But both files are writable by root only (`0644` and `0600`, owned by root), and capabilities only take effect for a process that has them, so `maat mcp` refuses to run as root: as a normal user, ordinary file permissions block those writes whatever AppArmor allows. maat's code never references the paths, and a test enforces that.

**Residual risk:** someone running the server as root anyway (by changing the code, say) would get the full `log-observe` grant. Landlock self-restriction is being evaluated as an extra layer (A13).

### T4. A plan is changed after it was proposed, or between review and execution

Plan files live in a directory that the confined server, and any process running as the user, can write.

- The server records the SHA-256 of the exact bytes it wrote in the plan's proposal record. `maatctl apply` reads the file once, hashes it, refuses it if the digest differs from the proposal record (or there is no proposal record), and executes only from its in-memory copy. `maatctl show` prints the digest and whether it matches.
- Whatever the file says, `maatctl` re-derives the preconditions, verification and rollback from the catalog and refuses a plan whose stored steps differ, so a plan can't be edited into one with weaker checks or a different target.
- A file whose name and embedded plan ID disagree is refused.
- Preconditions are re-checked after the confirmation, immediately before execution.

**Residual risk:** a compromised server can write a proposal record for a plan it later changes, since it writes both. The re-derived safety steps still bound what such a plan can do.

### T5. `maatctl` is tricked into acting on the wrong target

- Parameters are validated strictly for each kind: `.service` unit names, valid snap names. Whether the unit exists and is failed is a precondition checked against the live system.
- Calls go through typed D-Bus and REST APIs. Nothing passes through a shell.

### T6. Approval fatigue and cached authorization

- In active local sessions, polkit can reuse a recent admin authentication for a short time (`auth_admin_keep`, F8, F10). That cache isn't specific to maat.
- `maatctl` asks for its own confirmation on every plan, whatever the state of the polkit cache.
- The documentation shows how to write polkit rules scoped to one unit and one verb, and recommends running `maatctl` in a separate terminal.

**Residual risk:** a general shell in the same session can reuse the cache. That's why the deployment assumptions rule it out.

### T7. Someone tampers with the trail

- A hash chain, plus journal checkpoints that unprivileged processes can't delete. Checkpoints carry the trail's ID, and verification only trusts checkpoints whose journald-assigned `_UID` is the user's, so another local user can't forge or spam them (F14).
- Edits, deletions and reordering break the chain. Removing the newest records (the likeliest way to hide an action) leaves checkpoints beyond the end of the file, which verification reports. Both were tested on a VM ([evidence](evidence/maatctl-end-to-end.txt)).
- Optional journald Forward Secure Sealing (F13).

**Residual risk:** replacing the whole trail file with a new, self-consistent one is reported only as a warning ("checkpoints of a trail that is not this file"), because a reinstall of the snap, which deletes its data, looks the same. Root can rewrite everything, and checkpoints disappear when the journal's retention period expires.

### T8. maat causes an outage

- No background processes. maat runs only when it's invoked.
- Collectors have time and memory limits; large journals are streamed and cut to budget.
- Every action kind has a verification step and, where possible, a rollback. `netplan.try` reverts automatically if the change isn't confirmed (F12).

### T9. Supply chain

- Few dependencies, pinned in `go.sum`, with `govulncheck` in CI and release artifacts built in CI.
- The benchmark's auto-confirm code exists only in builds with the `maatbench` tag, and a test builds a release `maatctl` and fails if the binary contains it.

### T10. Untrusted text attacks the reviewer's terminal

A plan's rationale, a log line or a unit description can carry ANSI escape sequences that move the cursor and repaint lines the reviewer already read ("Parameters: nginx.service" over the real target), bidirectional overrides that reorder text, or invalid UTF-8.

- `maatctl` and `maat diagnose` print every untrusted string through one function that escapes C0 and C1 control characters, bidirectional and zero-width format characters, and invalid bytes (`internal/termsafe`, fuzz-tested). The [end-to-end evidence](evidence/maatctl-end-to-end.txt) shows a hostile rationale rendered harmless.
- Plan text is bounded: at most 4,000 bytes of rationale and 32 evidence IDs of the form `ev_…`.

## Security invariants

These are the properties maat promises, and how each is enforced today:

1. The snap declares no `network` plug. Automated: `TestNoNetworkPlug`.
2. The snap's plugs are a subset of `log-observe`, `system-observe` and `mount-observe`. `network-observe` is deliberately excluded because it permits outbound TCP (F28); any later addition requires a change to this document. Automated: `TestOnlyApprovedPlugs`.
3. No code path executes a shell or builds a command line from strings. Automated, as a source scan: `TestNoShellExecution`.
4. No code path writes to the two settings `log-observe` makes writable. Automated, as a source scan: `TestNoWritesToSensitiveLogObservePaths`. Backed at run time by invariant 7.
5. Every tool's output passes through redaction, clipping and budgeting. Not automated: each handler calls the shared `redact` and `fit` helpers, and reviewers check new tools for it (see the checklist in [AGENTS.md](../AGENTS.md)).
6. Every executable action kind has catalog-derived preconditions and verification, an Execute case, checks the actuator can evaluate, and a rollback or a stated reason for none. Automated: `TestActionKindsComplete`. (`snap.revert`'s rollback case exists but reports that it is not automated yet.)
7. `maatctl` and `maat mcp` exit with an error when run as root. Automated, as a source check: `TestRefusesRoot`.
8. Release builds contain no auto-confirm code. Automated: `TestReleaseBinaryHasNoAutoConfirm` builds `maatctl` with and without the `maatbench` tag and scans both binaries.
9. After every benchmark attempt, `maat trail verify` passes. Checked by the harness and reported as `trail_ok` in every score.

## Reporting a vulnerability

See [SECURITY.md](../SECURITY.md).
