# Security model

maat's job is to let a model look at a real machine without being able to hurt it. This document says precisely what that means: what's protected, from whom, how, and what risk is left over. It's a design document. The invariants at the end become automated checks once code exists. Facts (F) and assumptions (A) are defined in [references.md](references.md).

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
- Run `maatctl` yourself, in your own terminal, never through the agent.
- On machines with sensitive data, use a local model.

## Threats and mitigations

### T1. Prompt injection makes the model propose something harmful

- The server can't act. Its plugs grant reads only (F2, F3), and it has no network.
- Only catalog actions can be proposed, and their parameters are validated.
- A person reviews a preview with the cited evidence, and polkit asks for admin authentication.
- Untrusted text is fenced in tool output, and the benchmark measures how often injection still succeeds.

**Residual risk:** a person can approve a bad plan. The preview shows evidence IDs so the reasoning can be checked against the raw data.

### T2. Secrets in logs leave the machine

- The server has no `network` plug, so even a fully hijacked server can't send anything anywhere. Confirmed in a VM: an outbound connect from the confined process fails with `socket: permission denied` (F28). Testing also revealed that `network-observe` would re-enable outbound TCP, so it is excluded and an invariant test keeps it out.
- Redaction runs before any output.
- With a local model, nothing leaves the machine. With a hosted model, condensed and redacted log data goes to that provider. That's the user's choice, and the documentation says so plainly.

**Residual risk:** redaction is pattern-based and will miss some secrets.

### T3. A malicious log line exploits a bug in maat's parsers

- Confinement limits the damage to what the plugs allow.
- Parsers are fuzz-tested with hostile input: injection strings, very long lines, invalid UTF-8.

**Residual risk:** `log-observe` also allows writing `kernel.printk_ratelimit` and AppArmor's audit setting, and it grants the `dac_override` and `dac_read_search` capabilities (F3). Code that has taken over the server could, for example, set AppArmor auditing to quiet. maat never touches these paths and a test enforces that. Landlock self-restriction is being evaluated as an extra layer (A13).

### T4. A plan is changed between review and execution

- `maatctl` reads the plan once, hashes it, shows the hash, and executes only from its in-memory copy.
- Preconditions are re-checked immediately before execution.
- The plan's digest goes into the trail.

### T5. `maatctl` is tricked into acting on the wrong target

- Parameters are validated strictly for each kind: valid systemd unit names for loaded units, valid snap names.
- Calls go through typed D-Bus and REST APIs. Nothing passes through a shell.

### T6. Approval fatigue and cached authorization

- In active local sessions, polkit can reuse a recent admin authentication for a short time (`auth_admin_keep`, F8, F10). That cache isn't specific to maat.
- `maatctl` asks for its own confirmation on every plan, whatever the state of the polkit cache.
- The documentation shows how to write polkit rules scoped to one unit and one verb, and recommends running `maatctl` in a separate terminal.

**Residual risk:** a general shell in the same session can reuse the cache. That's why the deployment assumptions rule it out.

### T7. Someone tampers with the trail

- A hash chain, plus journal checkpoints that unprivileged processes can't delete.
- Trusted journal fields identify which program wrote each checkpoint (F14).
- Optional journald Forward Secure Sealing (F13).

**Residual risk:** root can rewrite everything, and checkpoints disappear when the journal's retention period expires.

### T8. maat causes an outage

- No background processes. maat runs only when it's invoked.
- Collectors have time and memory limits; large journals are streamed and cut to budget.
- Every action kind has a verification step and, where possible, a rollback. `netplan.try` reverts automatically if the change isn't confirmed (F12).

### T9. Supply chain

- Few dependencies, pinned in `go.sum`, with `govulncheck` in CI and release artifacts built in CI.
- The benchmark's auto-confirm code exists only in builds with the `maatbench` tag, and CI fails if a release artifact contains it.

## Security invariants

These are the properties maat promises. Each becomes an automated check:

1. The snap declares no `network` plug. (`TestNoNetworkPlug`)
2. The snap's plugs are a subset of `log-observe`, `system-observe` and `mount-observe`. `network-observe` is deliberately excluded because it permits outbound TCP (F28); any later addition requires a change to this document. (`TestOnlyApprovedPlugs`)
3. No code path executes a shell or builds a command line from strings.
4. No code path writes to `/proc/sys/kernel/printk_ratelimit` or `/sys/module/apparmor/parameters/audit`.
5. Every tool's output passes through redaction and budgeting.
6. Every action kind implements preview, apply and verify, plus a rollback or a documented reason it has none.
7. `maatctl` exits with an error when run as root.
8. Release artifacts contain no auto-confirm code.
9. After every benchmark run, `maat trail verify` passes.

## Reporting a vulnerability

See [SECURITY.md](../SECURITY.md).
