# References

This file is the project's memory. The other documents cite verified facts as **F**-numbers and untested beliefs as **A**-numbers.

How to use it:

- A fact needs a primary source (official documentation, source code or a specification) and the date it was checked.
- If you can't find a primary source, it's an assumption. Write down how to test it and which milestone tests it.
- When something changes upstream, update it here first, then fix the documents that cite it.
- AI agents: if a claim you need isn't here, verify it and add it, or add it as an assumption. Never fill the gap from memory.

All facts below were checked on **2026-10-25**.

## Verified facts

### Snap confinement and interfaces

**F1. The interfaces maat plugs are manual-connect and not super-privileged.**
The interface reference lists `log-observe` ("read system logs"), `system-observe` ("read process and system information"), `network-observe` ("query network status information"), `mount-observe` ("read mount table and quota information"), `hardware-observe` and `network-setup-observe` with auto-connect "no" and without the "Super privileged" category. `network` and `network-bind` auto-connect. `network-setup-control` ("change network settings via Netplan") is manual-connect. The reference also lists `packagekit-control` and `polkit` interfaces.
Sources: [Interfaces reference](https://snapcraft.io/docs/reference/interfaces/), [log-observe](https://snapcraft.io/docs/reference/interfaces/log-observe-interface/), [system-observe](https://snapcraft.io/docs/reference/interfaces/system-observe-interface/), [network-observe](https://snapcraft.io/docs/reference/interfaces/network-observe-interface/).

**F2. What `system-observe` grants.**
snapd's AppArmor rules for this interface allow `ptrace (read)`; reads across `/proc`; D-Bus `Get` and `GetAll` on `org.freedesktop.DBus.Properties` for `/org/freedesktop/systemd1` and everything below it; and `GetUnit` and `ListUnits` on `org.freedesktop.systemd1.Manager`. No rule allows starting, stopping or restarting units. The rules' own comment says the interface "should only be used with trusted apps". Base declaration: `deny-auto-connection: true`.
Source: [snapd `interfaces/builtin/system_observe.go`](https://github.com/canonical/snapd/blob/master/interfaces/builtin/system_observe.go).

**F3. What `log-observe` grants, including writes.**
Read access to `/var/log/**`, `/run/log/journal/**` and `/dev/kmsg`, and permission to execute `journalctl` from the base snap or the host. It *also* grants read-write access to `/proc/sys/kernel/printk_ratelimit` and `/sys/module/apparmor/parameters/audit` (the comment lists values such as `quiet`, which turns off AppArmor auditing), plus the capabilities `dac_override` and `dac_read_search`. Base declaration: `deny-auto-connection: true`.
Source: [snapd `interfaces/builtin/log_observe.go`](https://github.com/canonical/snapd/blob/master/interfaces/builtin/log_observe.go).

**F4. What `network-setup-control` grants.**
D-Bus calls to `io.netplan.Netplan` (`Generate`, `Apply`, `Info`, `Config`) and to its configuration objects (`Get`, `Set`, `Try`, `Cancel`, `Apply`), plus read-write access to `/etc/netplan`. Base declaration: `deny-auto-connection: true`.
Source: [snapd `interfaces/builtin/network_setup_control.go`](https://github.com/canonical/snapd/blob/master/interfaces/builtin/network_setup_control.go).

**F5. Super-privileged interfaces need Snap Store approval.**
Snaps that plug a super-privileged interface need specific approval from the Store before they can be distributed. `snapd-control` and `snap-refresh-observe` are in that category. `snapd-control` "is primarily intended to be used by brand store owners", and global-store uploads that use it are rejected until a request is approved; the Landscape team's request shows the process.
Sources: [Super-privileged interfaces](https://snapcraft.io/docs/explanation/interfaces/super-privileged-interfaces/), [Interfaces reference](https://snapcraft.io/docs/reference/interfaces/), [The snapd-control interface](https://forum.snapcraft.io/t/the-snapd-control-interface/7915), [landscape-client request](https://forum.snapcraft.io/t/request-for-snapd-control-interface-and-auto-connection-of-interfaces-for-landscape-client/36963).

**F6. Classic confinement is reviewed case by case, and management snaps are listed as unsupported.**
The Store's process requires a forum request, a technical justification and publisher vetting. Its list of known categories puts "management snaps" under "Unsupported".
Source: [Process for reviewing classic confinement snaps](https://forum.snapcraft.io/t/process-for-reviewing-classic-confinement-snaps/1460).

**F7. `SNAP_USER_COMMON`.**
Per-user data shared across revisions, typically `~/snap/<snap>/common`. Unlike `SNAP_USER_DATA`, it isn't backed up or restored by `snap refresh` and `snap revert`. For strictly confined snaps, `HOME` is rewritten to `SNAP_USER_DATA`.
Sources: [Environment variables](https://snapcraft.io/docs/reference/development/environment-variables/), [Data locations](https://snapcraft.io/docs/reference/administration/data-locations/).

### systemd, snapd and polkit

**F8. systemd's polkit action for managing units.**
`org.freedesktop.systemd1.manage-units` defaults to `auth_admin` for any session, `auth_admin` for inactive local sessions and `auth_admin_keep` for active local sessions. systemd passes two details to polkit: `unit` (the unit's primary name) and `verb` (one of `start`, `stop`, `reload`, `restart`, `try-restart`, `reload-or-restart`, `reload-or-try-restart`, `kill`, `reset-failed`, `set-property`). Rules can therefore target a single unit and a single operation.
Sources: [systemd `org.freedesktop.systemd1.policy.in`](https://github.com/systemd/systemd/blob/master/src/core/org.freedesktop.systemd1.policy.in), [systemd commit 88ced61](https://github.com/systemd/systemd/commit/88ced61bf9673407f4b15bf51b1b408fd78c149d).

**F9. systemd Manager D-Bus methods.**
On object `/org/freedesktop/systemd1`, interface `org.freedesktop.systemd1.Manager`: `GetUnit(in s name, out o unit)`, `ListUnits`, `RestartUnit(in s name, in s mode, out o job)` and `ResetFailedUnit(in s name)`.
Source: [org.freedesktop.systemd1(5)](https://www.freedesktop.org/software/systemd/man/latest/org.freedesktop.systemd1.html).

**F10. snapd's REST API and polkit.**
`POST /v2/snaps/{name}` accepts actions including `install`, `refresh`, `revert`, `remove`, `enable` and `disable`. Write access is `authenticatedAccess{Polkit: "io.snapcraft.snapd.manage"}`. Callers outside any snap get open read access; callers inside a snap need one of a few specific interfaces (`snap-refresh-observe`, `desktop-launch`, and for a single snap also `snap-interfaces-requests-control`). The default policy for `io.snapcraft.snapd.manage` is `auth_admin_keep`, and on Ubuntu members of the `sudo` or `admin` groups count as administrators.
Sources: [snapd `daemon/api_snaps.go`](https://github.com/canonical/snapd/blob/master/daemon/api_snaps.go), [snapd `daemon/api.go`](https://github.com/canonical/snapd/blob/master/daemon/api.go), [API authentication and authorization](https://snapcraft.io/docs/explanation/security/api-authentication-and-authorization/), [Sudo-less snap commands](https://forum.snapcraft.io/t/sudo-less-snap-commands/25441).

### netplan

**F11. netplan's D-Bus API.**
`netplan-dbus` provides `io.netplan.Netplan` on the system bus with `Apply`, `Generate`, `Info` and `Config`. `Config()` creates a configuration object with `Get`, `Set(delta, origin_hint)`, `Try(timeout_seconds)`, `Cancel` and `Apply`. Calling `Apply` while a `Try` is running accepts it; `Cancel` rejects it.
Sources: [netplan-dbus](https://netplan.readthedocs.io/en/latest/netplan-dbus/), [How to use the D-Bus configuration API](https://netplan.readthedocs.io/en/stable/dbus-config/), [netplan `src/dbus.c`](https://github.com/canonical/netplan/blob/main/src/dbus.c).

**F12. `netplan try`.**
Applies a configuration and rolls it back automatically unless it's confirmed within the timeout, which defaults to 120 seconds. Documented known issue: after a timeout or cancel, check that the configuration really was reverted.
Source: [netplan-try](https://netplan.readthedocs.io/en/latest/netplan-try/).

### The systemd journal

**F13. Forward Secure Sealing.**
`journalctl --setup-keys` generates a sealing key, which stays on the host, and a verification key, which should be stored elsewhere. `journalctl --verify --verify-key=KEY` then checks that sealed journal files are authentic.
Source: [journalctl(1)](https://www.freedesktop.org/software/systemd/man/latest/journalctl.html).

**F14. Trusted journal fields.**
"Fields prefixed with an underscore are trusted fields, i.e. fields that are implicitly added by the journal and cannot be altered by client code." They include `_PID`, `_UID`, `_EXE`, `_CMDLINE`, `_SYSTEMD_CGROUP` and `_SYSTEMD_UNIT`.
Source: [systemd.journal-fields(7)](https://www.freedesktop.org/software/systemd/man/latest/systemd.journal-fields.html).

### MCP and MCP clients

**F15. The official Go SDK.**
`github.com/modelcontextprotocol/go-sdk` v1.8.0 was released on 2026-09-14. v1.7.0 and later support protocol revision 2026-07-28 (the latest) as well as 2025-11-25, 2025-06-18, 2025-03-26 and 2024-11-05. Revision 2026-07-28 deprecates roots, sampling and logging (SEP-2577). The streamable HTTP transport serves 2026-07-28 only when `Stateless` is true.
Sources: [go-sdk releases](https://github.com/modelcontextprotocol/go-sdk/releases), [v1.7.0 release notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0), [pkg.go.dev](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk).

**F16. Tool annotations are hints, not guarantees.**
`readOnlyHint`, `destructiveHint`, `idempotentHint` and `openWorldHint` must be treated as untrusted unless they come from a trusted server. In the maintainers' words: "If you need a guarantee that a tool can't exfiltrate data, that's a job for network controls or sandboxing, not a boolean hint."
Source: [Tool Annotations as Risk Vocabulary (MCP blog, 2026-03-16)](https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/).

**F17. Registering a local stdio server.**
Claude Code: `claude mcp add [options] <name> -- <command> [args...]`, with stdio as the default transport for local servers. OpenCode (v2 documentation): `opencode mcp add <name> -- <command>`. OpenCode's configuration file format differs between v1 and v2.
Sources: [Claude Code MCP docs](https://code.claude.com/docs/en/mcp), [OpenCode v2 MCP servers](https://opencode.ai/v2/docs/mcp-servers), [OpenCode MCP servers](https://opencode.ai/docs/mcp-servers/).

### Inference snaps

**F18. Inference snaps expose OpenAI-compatible endpoints.**
`<inference-snap> status` reports the active engine, its endpoints and the model name; the documented example for `gemma4` shows `openai: http://localhost:8336/v1` and model `gemma4-e4b-q4-k-m`. "Some inference snaps expose an OpenAI compliant network API." If a client requires an API key, it can be left blank or set to a placeholder. Each snap has a reserved default port (for example Nemotron 3 Nano 8330, Gemma4 8336, Qwen 3.6 8342).
Sources: [Use an inference snap via its OpenAI API](https://documentation.ubuntu.com/inference-snaps/how-to/usage/openai-api/), [Use inference snaps in OpenCode](https://documentation.ubuntu.com/inference-snaps/how-to/integration/opencode/), [Network ports registry](https://documentation.ubuntu.com/inference-snaps/reference/network-ports/), [CLI reference](https://documentation.ubuntu.com/inference-snaps/reference/models-cli/).

### Go

**F19. Supported Go releases.**
The latest stable release is Go 1.27.3 (2026-10-19). Each major release is supported until two newer major releases exist, so 1.27 and 1.26 are currently supported. The MCP Go SDK targets only supported Go versions.
Sources: [Go release history](https://go.dev/doc/devel/release), [pkg.go.dev for go-sdk](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk).

### Context and prior art

**F20. Canonical's stated direction for AI in Ubuntu.**
From "The future of AI in Ubuntu" (Ubuntu Discourse, 2026-04-27): a bias toward local inference and open-source harnesses; inference snaps run under normal snap confinement; and the aim "for Ubuntu to expose the primitives needed for agents to operate within existing boundaries, whether that be read-only analysis, tightly scoped permissions for any actions, and full auditability of decisions and outcomes." In the same thread, the author mentions "the new prompting capability we landed in 26.04 LTS" for managing snap permissions.
Source: [The future of AI in Ubuntu](https://discourse.ubuntu.com/t/the-future-of-ai-in-ubuntu/81130).

**F21. Canonical Workshop.**
"Secure, fast, and composable development environments that come agent-ready", defined in YAML and built on LXD (6.8 or later). Licensed GPL-3.0. Installed with `sudo snap install --classic workshop`.
Sources: [canonical/workshop](https://github.com/canonical/workshop/), [Workshop and AI agents](https://ubuntu.com/workshop/docs/reference/ai-agents/).

**F22. NVIDIA OpenShell as a snap.**
Announced 2026-06-01: an open-source runtime that runs each agent in an isolated sandbox and enforces policies, published as a snap.
Source: [Securing AI agent workflows on Ubuntu with the new NVIDIA OpenShell snap](https://ubuntu.com/blog/nvidia-openshell-ubuntu-announcement).

**F23. Red Hat's linux-mcp-server.**
A Python MCP server for read-only diagnostics on RHEL-family systems, local or over SSH, with pre-vetted commands, a log-path allowlist and an opt-in "Guarded Command Execution" mode. Two advisories were published on 2026-09-04: GHSA-gx3q-f66w-jhp9 ("Sandbox not properly enabled for read-only commands") and GHSA-7f2q-wwpq-mmhq ("Gatekeeper prompt injection via unsanitized description field").
Sources: [rhel-lightspeed/linux-mcp-server](https://github.com/rhel-lightspeed/linux-mcp-server/), [security page](https://github.com/rhel-lightspeed/linux-mcp-server/security), [Red Hat blog announcement](https://www.redhat.com/en/blog/smarter-troubleshooting-new-mcp-server-red-hat-enterprise-linux-now-developer-preview).

**F24. MCPGuard-Dynamic.**
Kernel-level sandboxing of MCP tool calls, implemented as a proxy plus eBPF programs. It requires a kernel with the BPF LSM enabled (`CONFIG_BPF_LSM=y` and `lsm=bpf` on the kernel command line) and ships a 14-server, 82-case benchmark.
Source: [facebook/mcpguard-dynamic](https://github.com/facebook/mcpguard-dynamic).

**F25. Drain.**
Pinjia He, Jieming Zhu, Zibin Zheng and Michael R. Lyu, "Drain: An Online Log Parsing Approach with Fixed Depth Tree", IEEE ICWS 2017. An online log parser that groups messages into templates using a fixed-depth parse tree.
Sources: [DOI 10.1109/ICWS.2017.13](https://doi.org/10.1109/icws.2017.13), [author PDF](https://pinjiahe.github.io/files/pdf/research/ICWS17.pdf).

**F26. Canonical's trademark policy.**
"You cannot use the Trademarks in software titles. If you are producing software for use with or on Ubuntu you may reference Ubuntu, but must avoid: (i) any implication of endorsement, or (ii) any attempt to unfairly or confusingly capitalise on the goodwill of Canonical or Ubuntu." Canonical's trademarks include UBUNTU, SNAPCRAFT and LXD.
Sources: [Intellectual property rights policy](https://canonical.com/legal/intellectual-property-policy), [Trademarks](https://canonical.com/legal/trademarks).

**F27. Ma'at and the weighing of the heart.**
In the judgment scene associated with Book of the Dead spell 125, the deceased's heart is weighed against the feather of Ma'at, the principle of order, right and truth, and Thoth records the result.
Sources: [British Museum, EA 10554,80](https://www.britishmuseum.org/collection/object/Y_EA10554-80), [Fitzwilliam Museum](https://www.fitzmuseum.cam.ac.uk/explore-our-collection/highlights/E921904), [UCLA Encyclopedia of Egyptology](https://escholarship.org/content/qt07s1t6kj/qt07s1t6kj.pdf).

## Validation results (checked 2026-10-26 on an Ubuntu 24.04 VM)

These were produced by building the strict snap and running `maat selftest` and the benchmark on a real Ubuntu 24.04.3 LTS (kernel 6.8, arm64) VM. The captured output is in [evidence/](evidence/).

**F28. `network-observe` permits outbound TCP.** With `network-observe` connected, the confined server completed an outbound TCP connection; with it removed, the same connect failed with `socket: permission denied`. Because that would give a hijacked model an exfiltration channel, maat does not plug `network-observe`. Consequence: the `network_state` collector (which needs netlink) fails closed under the strict profile. Enforced by the invariant test `TestOnlyApprovedPlugs`.

**F29. AppArmor blocks systemd control from the confined server.** A direct `RestartUnit` call from inside the snap is refused by AppArmor itself, not by polkit: `An AppArmor policy prevents this sender from sending this message ... label="snap.maat.maat (enforce)" ... member="RestartUnit"`, with D-Bus error name `org.freedesktop.DBus.Error.AccessDenied`. This holds even though `system-observe` lets the same process list every unit (502 on that VM). Evidence: [selftest-confined.txt](evidence/selftest-confined.txt).

**F30. `log-observe` grants journal access only with group membership.** The AppArmor side of `log-observe` is necessary but not sufficient: because the server runs as the invoking user (not root), reading the journal also requires that user to be in the `systemd-journal` (or `adm`) group, exactly as Red Hat's linux-mcp-server documents. Without it, `journalctl` reports "No journal files were opened due to insufficient permissions"; with the group, the read succeeds.

The following were checked on **2026-10-26** on the same VM (systemd 255.4, polkitd 124, snapd 2.77.1, AppArmor 4.0.1).

**F31. `system-observe` does not grant the `/proc/net` socket tables.** Inside the snap, reading `/proc/net/tcp` fails with "Permission denied", and the kernel logs `apparmor="DENIED" operation="open" profile="snap.maat.maat" name="/proc/<pid>/net/tcp"` (likewise `tcp6`, `udp`, `udp6`, `route`, `ipv6_route`). `/proc/net/dev` is readable; `/sys/class/net/` can be listed, but its attributes, `/etc/resolv.conf` and `/run/systemd/resolve/resolv.conf` are denied. Consequence: `listening_sockets` returns an explanatory error under the snap and `network_state` reports interface names and counters only. An earlier version of the socket collector swallowed the error and returned an empty list, which the self-test then reported as "yes: 0 sockets", a pass.

**F32. The confined server can write journal checkpoints, but trusted program fields may be missing.** From inside the snap, `logger --journald` wrote an entry whose custom fields (`MAAT_PROBE=1`) arrived intact, with `_UID=502`, `_COMM=logger` and `_TRANSPORT=journal`. `_EXE`, `_CMDLINE` and `_SYSTEMD_CGROUP` were absent: `logger` exits before journald reads them. The trail therefore keys checkpoints on `_UID` and doesn't rely on the program fields. The [end-to-end run](evidence/maatctl-end-to-end.txt) verified checkpoints written both by the confined server and by `maatctl`.

**F33. A polkit text agent makes `maatctl` work outside a desktop session.** Without an agent, a `RestartUnit` call that allows interactive authorization fails with "Interactive authentication required." (`busctl call --allow-interactive-authorization=yes ...` on the VM). With `maatctl` starting `/usr/bin/pkttyagent --notify-fd 3 --fallback` on its terminal, polkit prompted there ("==== AUTHENTICATING FOR org.freedesktop.systemd1.manage-units ===="). `pkttyagent --help` documents `--notify-fd` ("Close FD when the agent is registered") and `--fallback` ("Don't replace existing agent if any"); systemd's own tools start the agent the same way ([systemd `src/shared/polkit-agent.c`](https://github.com/systemd/systemd/blob/main/src/shared/polkit-agent.c)).

**F34. Inside the snap, the mount table is the snap's, and the host's is under `hostfs`.** `/proc/self/mountinfo` in the snap shows the snap's mount namespace: `/` is a tmpfs, the base snap is mounted over `/usr`, and host directories such as `/etc` and `/home` are bind mounts of the host root filesystem. The host's mounts appear under `/var/lib/snapd/hostfs` (whose root mount is the host's `/`), and `statfs` works on them; `/proc/1/mountinfo` is denied. Both mount tables are committed as test fixtures in `internal/collect/testdata/`.

**F35. systemd keeps removed failed units until they are reset.** A unit that failed, whose unit file was then deleted and the daemon reloaded, stays listed as `not-found failed failed` until `reset-failed`. After `ResetFailedUnit`, systemd unloads it, and `GetUnit` returns `org.freedesktop.systemd1.NoSuchUnit`.

**F36. The `ubuntu:24.04` LXD image ships polkit with JavaScript rules.** It has `polkitd` 124 and `pkttyagent`. A rule in `/etc/polkit-1/rules.d/` matching `action.id == "org.freedesktop.systemd1.manage-units"`, `subject.user`, `action.lookup("unit")` and `action.lookup("verb")` authorizes `RestartUnit` and `ResetFailedUnit` for one user and unit without a password, while other units still prompt. Used by the benchmark and shown in the [end-to-end run](evidence/maatctl-end-to-end.txt).

Resolved assumptions:

- **A2 (confirmed):** no `network` plug ⇒ creating an internet socket (TCP or UDP, IPv4 or IPv6) fails with `EACCES` (`socket: permission denied`).
- **A3 (confirmed):** `system-observe` + godbus lists units and reads properties; `RestartUnit` is denied (F29).
- **A4 (confirmed):** an MCP client connects to `maat mcp` over stdio, bridged through `lxc exec` in the benchmark, and calls tools successfully.
- **A5 (confirmed, 2026-10-26):** the confined server can submit journal entries (F32).
- **A7 (confirmed, 2026-10-26):** `maatctl` gets polkit authentication in a terminal session by starting `pkttyagent` (F33).
- **A8 (refuted → design changed):** `mount-observe` covers disks once the host view is read from `hostfs` (F34), but neither the socket tables nor addresses and routes are readable without `network-observe`, which is excluded (F28, F31).
- **A10 (confirmed):** strict snaps run in the VM; all ten benchmark scenarios reproduce in unprivileged LXD containers.
- **A15 (decided):** base `core24`, built with the `go` build-snap (Go ≥ 1.26); builds and runs on 24.04.

Partly open:

- **A1:** journal access confirmed on 24.04 with the `systemd-journal` group (F30); 26.04 still to check.
- **A12:** an AppArmor denial was produced and read back during the self-test (F29); a dedicated end-to-end denial scenario is still to add.

## Open assumptions

Each assumption names how it will be tested and the milestone that tests it. When one is resolved, move the result into the facts above (or rewrite the design) and mark it resolved here with the date.

| ID | Assumption | How to test | Milestone |
|---|---|---|---|
| A1 | A strict snap with `log-observe` can read the host journal through `journalctl --output=json` with unit, priority and time filters, on 24.04 and 26.04. | Minimal snap that reads the last 100 entries for a unit; compare with host `journalctl`. | M0 |
| A2 | A strict snap app without the `network` plug can't open `AF_INET` or `AF_INET6` sockets, and the denial is logged. | Test binary attempts an outbound TCP connection; check the error and the AppArmor or seccomp log entry. | M0 |
| A3 | With `system-observe`, a Go D-Bus client (godbus) inside the snap can call `ListUnits`, `GetUnit` and property `Get`/`GetAll`, and calls to `RestartUnit` are denied. | Call each method from the snap; record results. | M0 |
| A4 | MCP clients (Claude Code, OpenCode) can launch the snap app over stdio, and the process runs under the snap's AppArmor label. | Register `maat mcp`, call a tool, inspect `/proc/<pid>/attr/current`. | M0 |
| A5 | ~~The confined server can submit entries to the journal.~~ Confirmed 2026-10-26 (F32). | | M0 |
| A6 | netplan's D-Bus API is restricted to root by its D-Bus policy and has no polkit integration. | Read `/usr/share/dbus-1/system.d/io.netplan.Netplan.conf`; call `Config` as an unprivileged user. | M4 |
| A7 | ~~A Go program can get polkit authentication in a terminal session the way `systemctl` does.~~ Confirmed 2026-10-26 (F33). | | M4 |
| A8 | ~~`network-observe` and `mount-observe` cover `network_state`, `listening_sockets` and `disk_usage`.~~ Refuted 2026-10-26 for the first two (F28, F31); `disk_usage` works through `hostfs` (F34). | | M0 |
| A9 | The inference snaps used in the benchmark support tool calling through their OpenAI-compatible endpoint. | Send a tool-call request to each snap and engine; record which work. | M2 |
| A10 | Snaps with strict confinement work inside LXD containers on an Ubuntu host, and each fault class can be reproduced in a container or needs a VM. | Install the spike snap in a container and a VM; reproduce each scenario's fault. | M2 |
| A11 | ~~The snap name `maat` is available to register.~~ Confirmed 2026-10-26: registered, and v0.1.0 published on `edge`. | | M0 |
| A12 | AppArmor denials caused by the snap appear in the journal with `apparmor="DENIED"`, readable through `log-observe`. | Trigger a denial from the spike snap and query for it. | M0 |
| A13 | Landlock can be applied by the server to itself inside the snap's seccomp profile, as an extra layer that blocks writes `log-observe` would allow. | Call the Landlock syscalls from the snap and check for seccomp denials. | Later |
| A14 | The LXD Go client's license is compatible with a GPL-3.0 harness; otherwise the harness drives the `lxc` command instead. | Check the license headers of the packages the harness would import. | M2 |
| A15 | Which snap base (core24 or newer) and which Go toolchain to use in the snap build. | Build the spike with candidate bases; pick the newest one that builds and runs on both supported releases. | M0 |
| A16 | Each benchmark injection vector delivers its payload into the journal verbatim. (OpenSSH, for example, may sanitize usernames before logging them.) | Inject a known payload through each vector and diff the journal entry. | M2 |
| A17 | `snap revert` works between two locally installed (`--dangerous`) revisions, so a broken-revision scenario can be built without the Store. | Install two local revisions of a test snap and revert. | M2 |
