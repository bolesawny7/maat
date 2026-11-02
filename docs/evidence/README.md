# Evidence

Captured output from running maat on an Ubuntu 24.04.3 LTS VM (kernel 6.8, arm64; systemd 255.4, polkitd 124, snapd 2.77.1), so the claims in the README and security model can be checked rather than taken on trust. Everything here was produced by code and scripts in this repository on 2026-10-26, except the SSH run, which comes from a second VM on 2026-10-28 (described below). One edit was made after capture: the VM's mount of the developer's project directory was renamed to `/Users/dev/maat`.

## Confinement: `selftest-confined.txt` / `.json`

`maat selftest` inside the strict snap (revision x6), with `log-observe`, `system-observe` and `mount-observe` connected and the user in `systemd-journal`. Result: **6 passed, 0 failed, 3 informational.** Each "blocked" check passes only when the refusal comes from confinement:

- `network_blocked`: creating a TCP (IPv4 and IPv6) and a UDP socket fails with `socket: permission denied` (`EACCES`). The probes dial closed loopback ports, so nothing would have left the machine even if they hadn't been blocked.
- `home_write_blocked`: writing a file in the user's real home directory (`$SNAP_REAL_HOME`) is refused. Ordinary file permissions allow that write, so only the snap profile can refuse it. (The older check wrote to `/etc`, which a normal user can't write anyway, so it proved nothing.)
- `systemd_control_denied`: `RestartUnit` is refused by AppArmor (`An AppArmor policy prevents this sender ... label="snap.maat.maat (enforce)"`), not by polkit.
- `not_root`, `journal_readable` (with the `systemd-journal` group) and `units_listable` (502 units) pass.
- Informational: `disk_view` sees the host's filesystems (`/` is ext4 on `/dev/vda1`); `network_state` can read interface names and counters only; `listening_sockets` can't read the socket tables and the tool says so.

`selftest-unconfined.txt` is the same binary outside the snap, for contrast: the blocked checks become informational because nothing blocks them.

## Tools under confinement: `mcp-tools-confined.json`

Responses from the confined server to `recent_failures`, `disk_usage`, `listening_sockets`, `network_state`, `top_processes`, `apparmor_denials`, `journal_summary`, and a `propose_action` for `reboot.target`, which is rejected. It shows the host's real filesystems (no duplicate bind mounts, no read-only CD reported as a full disk), the explanatory error from `listening_sockets`, and the limited, labelled network view.

## The human loop: `maatctl-end-to-end.txt`

The output of [scripts/demo-e2e.sh](../../scripts/demo-e2e.sh): the confined server proposes, and the unconfined release `maatctl` handles everything after. In order:

1. The server rejects `reboot.target` and records two plans. One cites an evidence ID the server issued and one it never issued, and its rationale carries ANSI escape sequences meant to repaint the reviewer's screen with "plan VERIFIED by Canonical".
2. `maatctl show` prints the escapes as visible text, the real evidence with its excerpt, the invented ID flagged as "NOT ISSUED", and a digest that matches the proposal record.
3. A plan whose precondition was weakened on disk fails validation. A plan whose rationale alone was edited is refused because its digest no longer matches the proposal record.
4. With no polkit rule, `apply` brings up polkit's text prompt on the terminal; a wrong password fails the execution, and the plan is marked `failed`.
5. With a polkit rule scoped to one unit and one verb, `apply` runs without a password, and verification passes even though systemd unloaded the unit (F35).
6. `maat trail verify` passes, with every journal checkpoint matching. Its warnings are real: earlier test runs on the same VM left checkpoints of trails that were since deleted. After the newest three records are deleted, it reports the truncation.

## Remote use over SSH: `ssh-remote-end-to-end.txt`

Captured on 2026-10-28 on a different VM: Ubuntu 24.04.4 LTS (kernel 6.8.0-100-generic, arm64) under Colima on macOS, with snapd 2.77.1, polkitd 124, OpenSSH server 9.6p1, and maat 0.1.0 installed from the `edge` channel (revision 2). The host was prepared by [scripts/ssh-host-setup.sh](../../scripts/ssh-host-setup.sh), and the output is from [scripts/demo-ssh.sh](../../scripts/demo-ssh.sh), run on the Mac. The SSH address is `127.0.0.1` because Colima forwards the VM's SSH port to the Mac. See [remote.md](../remote.md) and F38. In order:

1. The agent's key (`restrict,command="... maat mcp"`) asks for `id; cat /etc/shadow` and gets no output, because `maat mcp` runs instead. A terminal request and a port forward are both refused by `sshd`.
2. An MCP session over `ssh -T` lists the 13 tools, reads the overview and the failed units: the deliberately broken `uploader.service`, plus the VM's own `cloud-final.service`, which really did fail under Colima.
3. During a session, `ps` on the host shows `maat mcp` under `snap.maat.maat (enforce)`.
4. Over SSH, `propose_action` refuses `reboot.target` and records a restart of `uploader.service` that cites the log line.
5. Over the second, unrestricted key, the same account's `maatctl` lists, shows, applies and verifies the plan under a polkit rule scoped to that unit and verb. `maat trail verify` passes. Its warning is real: an earlier run of the same demo on this VM left journal checkpoints of a trail that was then deleted before this run.
6. `maat selftest` on the host passes 6 of 6 checks, including `network_blocked` and `home_write_blocked`.

## Benchmark: `bench-comparison.md`, `bench-baseline-summary.md`, `bench-oracle-summary.md`

The latest maat-bench runs over ten scenarios in LXD containers, with maat running as an unprivileged user and fixes applied by the real `maatctl`. Full per-attempt detail, run configs and transcripts are in [../../bench/results/](../../bench/results/).

- **Oracle (validation):** 10/10 diagnosis, 10/10 evidence, 2/2 fixes applied and healthy, 10/10 trails verified.
- **Baseline (no LLM):** 9/10 diagnosis, 7/10 evidence, 0/2 fixes (it never proposes), 10/10 trails verified. Its restraint and injection numbers hold by construction.

No real model has been run yet.

## Reproducing

See [../demo.md](../demo.md). In short: build the snap on Ubuntu, `snap install --dangerous`, connect the three interfaces, add your user to `systemd-journal`, and run `maat selftest`; then build `maatctl` and run `scripts/demo-e2e.sh` on a disposable VM.
