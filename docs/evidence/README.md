# Evidence

Captured output from running maat on a real Ubuntu 24.04.3 LTS VM (kernel 6.8, arm64), so the claims in the README and security model can be checked rather than taken on trust.

## `selftest-confined.txt` / `selftest-confined.json`

`maat selftest` run inside the strictly confined snap, with `log-observe`, `system-observe` and `mount-observe` connected and the user in the `systemd-journal` group. Result: **7 passed, 0 failed.** The load-bearing lines:

- `units_listable` — PASS: listed 501 systemd units over D-Bus.
- `journal_readable` — PASS: the journal is readable.
- `network_blocked` — PASS: an outbound TCP connect is refused with `socket: permission denied`.
- `filesystem_read_only` — PASS: writing to `/etc` is refused.
- `systemd_control_denied` — PASS: `RestartUnit` is refused with
  `An AppArmor policy prevents this sender from sending this message ... label="snap.maat.maat (enforce)" ... member="RestartUnit"`.
- `network_state_readable` — informational: fails closed, because maat does not plug `network-observe` (see below).

The two informational checks exist to document what the chosen interface set does and doesn't allow.

## Why `network-observe` is not used

An earlier build plugged `network-observe` to power the `network_state` tool. With it connected, the `network_blocked` check *failed*: the confined process completed an outbound TCP connection. That would hand a prompt-injected model an exfiltration channel, so `network-observe` was removed. The `network_state` collector now fails closed, which is the right trade: no egress beats a nice-to-have tool. The exclusion is enforced by the `TestOnlyApprovedPlugs` invariant test.

## Benchmark results

`bench-baseline-summary.md`, `bench-oracle-summary.md`, and the combined `bench-comparison.md` hold the latest maat-bench run over ten scenarios in LXD containers:

- **Oracle (validation):** 10/10 diagnosis, evidence resolved on all six unit-based scenarios, 2/2 fix scenarios recover. Proves the scenarios and the scoring/fix/health machinery are correct.
- **Baseline (no-LLM floor):** 8/10 diagnosis, 8/8 restraint, 2/2 injection resisted, 6/10 evidence, 0/2 fix (it proposes no fixes by design), averaging 3 tool calls per scenario.

Full per-scenario detail is in `../../bench/results/<run-id>/scores.jsonl`.

## Reproducing

See [../demo.md](../demo.md). In short: build the snap on Ubuntu, `snap install --dangerous`, connect the three interfaces, add your user to `systemd-journal`, and run `maat selftest`.
