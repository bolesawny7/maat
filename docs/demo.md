# Demo & reproduction

How to build maat, verify its central claim (the agent is caged by the kernel), run the human review loop, and run the benchmark. Everything here was run on an Ubuntu 24.04 VM; the captured output is in [evidence/](evidence/).

## Prerequisites

- An Ubuntu 24.04 machine or VM you don't mind breaking (on macOS, `limactl start template:ubuntu-24.04` works).
- Go 1.26+, `snapcraft`, and `lxd` for the benchmark. `scripts/vm-setup.sh` and `scripts/vm-setup-snap.sh` install all of it.

## 1. Build and install

```bash
snapcraft pack --destructive-mode          # or plain `snapcraft pack` to build in a managed VM
sudo snap install --dangerous maat_*.snap
go build -o ~/.local/bin/maatctl ./cmd/maatctl   # maatctl lives outside the snap by design
```

## 2. Verify the confinement (the central claim)

Run the self-test before connecting interfaces, then after. Before, the server can't even observe; after, it observes but still can't act, write your files, or reach the network:

```bash
maat selftest
for i in log-observe system-observe mount-observe; do sudo snap connect maat:$i; done
sudo usermod -aG systemd-journal "$USER"   # start a new session afterwards
maat selftest                              # expect: 6 passed, 0 failed, 3 informational
```

The load-bearing lines are refusals that only confinement can produce:

```
[PASS] network_blocked
       observed: refused by policy: tcp4: dial tcp4 127.0.0.1:9: socket: permission denied; ...
[PASS] home_write_blocked
       observed: write to /home/you was refused: open /home/you/.maat-selftest-probe-...: permission denied
[PASS] systemd_control_denied
       observed: refused by AppArmor: An AppArmor policy prevents this sender ... label="snap.maat.maat (enforce)" ... member="RestartUnit"
```

## 3. The review loop

`scripts/demo-e2e.sh` breaks two units, has the confined server propose fixes (one with a hostile rationale and an invented evidence ID), tampers with the plan files and the trail, and shows `maatctl` and `maat trail verify` catching each attack. It needs sudo, creates and removes a polkit rule, and deletes maat's plans and trail, so run it only on a disposable VM:

```bash
MAATCTL=~/.local/bin/maatctl scripts/demo-e2e.sh
```

## 4. Drive it with an agent

Point an MCP client at the server:

```bash
claude mcp add maat -- maat mcp     # or: opencode mcp add maat -- maat mcp
```

Break something (for example a unit whose `ExecStart` points at a missing file), then ask the agent what's wrong. It calls the read-only tools, cites evidence IDs, and may call `propose_action`. Applying a fix is a separate, human step, in your own terminal:

```bash
maatctl list
maatctl show <plan-id>
maatctl apply <plan-id>             # type the plan ID; polkit asks for admin authentication
maat trail verify
```

To keep everything local, serve the model from an inference snap and point the client at its OpenAI endpoint (`<snap> status`).

## 5. Run the benchmark

```bash
make bench-bin
./bin/harness -agent oracle      # validates the scenarios and the fix track
./bin/harness -agent baseline    # the no-LLM floor
./bin/harness -agent openai -openai-url http://localhost:8336/v1 -model <model> -runs 3
./bin/harness -compare bench/results/<run-a>,bench/results/<run-b>
```

Results land in `bench/results/<timestamp>/`, with a run config, per-attempt scores, a summary and transcripts. See [bench/README.md](../bench/README.md).
