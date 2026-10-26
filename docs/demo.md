# Demo & reproduction

How to build maat, verify its central claim (the agent is caged by the kernel), and run it against a model. Everything here was developed and tested on an Ubuntu 24.04 VM.

## Prerequisites

- An Ubuntu 24.04 machine or VM (on macOS, `limactl start template:ubuntu-24.04` works).
- Go 1.26+, `snapcraft`, and `lxd` for the benchmark. The repo ships `scripts/vm-setup.sh` and `scripts/vm-setup-snap.sh` that install all of it.

## 1. Build and install the snap

```bash
snapcraft pack --destructive-mode
sudo snap install --dangerous maat_*.snap
```

## 2. Verify the confinement (the central claim)

Run the self-test **before** connecting interfaces, then after. Before, the server can't even observe; after, it observes but still cannot act or reach the network:

```bash
maat selftest                       # blocked from journal, D-Bus, network, filesystem
for i in log-observe system-observe mount-observe; do sudo snap connect maat:$i; done
sudo usermod -aG systemd-journal "$USER"   # start a new session afterwards
maat selftest                       # reads journal + 500+ units, STILL cannot restart units or reach the network
```

The load-bearing line is the kernel refusing a privileged action:

```
[PASS] systemd_control_denied
   observed: RestartUnit was refused: An AppArmor policy prevents this sender ...
             label="snap.maat.maat (enforce)" ... member="RestartUnit"
```

That is AppArmor, not the model's good behaviour, refusing the dangerous action. The captured output is committed under [evidence/](evidence/).

## 3. Drive it with an agent

Point an MCP client at the server:

```bash
claude mcp add maat -- maat mcp     # or: opencode mcp add maat -- maat mcp
```

Break something (`sudo systemctl start a-service-with-a-typo`, or fill a tmpfs), then ask the agent what's wrong. It calls the read-only tools, cites evidence IDs, and may call `propose_action`. Applying a fix is a separate, human step:

```bash
maatctl list
maatctl show <plan-id>
maatctl apply <plan-id>             # confirms on your terminal; polkit authorizes
maat trail verify
```

To keep everything local, serve the model from an inference snap and point the client at its OpenAI endpoint (`<snap> status`).

## 4. Run the benchmark

```bash
make build
./bin/harness -scenarios bench/scenarios -agent baseline -maat bin/maat   # no-LLM floor
./bin/harness -scenarios bench/scenarios -agent oracle   -maat bin/maat   # validates scenarios
```

Against a local inference snap or any OpenAI-compatible endpoint:

```bash
sudo snap install qwen3
qwen3 status                        # note the endpoint + model name
./bin/harness -agent openai -openai-url http://localhost:8338/v1 -model qwen3-8b -maat bin/maat
```

Combine runs into one table:

```bash
./bin/harness -compare bench/results/<run-a>,bench/results/<run-b>
```

Results land in `bench/results/<timestamp>/`.
