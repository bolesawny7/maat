# Remote use over SSH

The model doesn't have to run on the machine you're diagnosing, and neither does the MCP client. MCP's local transport is a process's standard input and output, and SSH carries exactly that. So the client and the model can run in one place, such as your laptop or a central "agent box" next to a hosted API or a single inference machine, and reach `maat mcp` on each host over SSH. Each host runs only maat, which uses nothing until a session starts (T8 in the [security model](security-model.md)). Facts (F) and assumptions (A) are defined in [references.md](references.md).

**Status: developer preview.** Tested end to end on one Ubuntu 24.04.4 VM with the `edge` snap (F38, [evidence](evidence/ssh-remote-end-to-end.txt)). Not yet tested with Claude Code or OpenCode as the client (A18), or with many hosts at once.

## What stays the same

- On the host, `maat mcp` still runs in the strict snap. A process started over SSH carries the label `snap.maat.maat (enforce)`, and the self-test on that host passes (F38).
- The server still has no network access of its own. SSH traffic is handled by `sshd`, outside the snap; maat only reads and writes its standard input and output.
- Plans and the trail stay on each host, and a person approves each plan there with `maatctl` and polkit, as in local use.

## Set up a host

1. Install maat and connect its interfaces, as in the [Quickstart](../README.md#quickstart).
2. Choose the account whose plans you'll review, usually your own, and make sure it's in the `systemd-journal` group. maat and `maatctl` keep plans and the trail in the home directory of the user they run as (`~/snap/maat/common`), so the agent has to start maat as the same account that will run `maatctl`. Otherwise its plans won't show up in `maatctl list`.
3. Add the agent's public key to that account's `~/.ssh/authorized_keys`, with a forced command and `restrict`:

   ```text
   restrict,command="env MAAT_CLIENT=remote-agent /snap/bin/maat mcp" ssh-ed25519 AAAA... maat-agent
   ```

   `command=` makes `sshd` run `maat mcp` whatever the client asks for, and `restrict` turns off port, agent and X11 forwarding, terminal allocation and `~/.ssh/rc` (F37). `MAAT_CLIENT` names the proposer: `maatctl show` then reports "Proposed: ... by remote-agent".
4. Keep your own key on its own line, without options, for your normal shell.

The confined server can't write the account's files (the self-test's `home_write_blocked` check), so it can't edit `authorized_keys` to remove its own restriction.

## Connect the agent

The MCP server command is `ssh` itself, one entry per host:

```bash
ssh -T -i ~/.ssh/maat-agent -o IdentitiesOnly=yes -o BatchMode=yes ops@web1
```

With Claude Code, for example, that would be registered with the documented syntax (F17), though this exact use hasn't been tested yet (A18):

```bash
claude mcp add maat-web1 -- ssh -T -i ~/.ssh/maat-agent -o IdentitiesOnly=yes -o BatchMode=yes ops@web1
```

Pin each host's key, with a managed `known_hosts` file or SSH host certificates. Without that, someone who can intercept the connection can answer as the host and feed the agent invented data. `scripts/demo-ssh.sh` accepts host keys on first use only because its target is a throwaway VM.

## Review and apply

On the host, as the same account, the same way as for local use:

```bash
maatctl list
maatctl show PLAN_ID
maatctl apply PLAN_ID
maat trail verify
```

## What the agent's key can and can't do

It can start `maat mcp` as that account, which means maat's read-only tools and `propose_action`. In the [evidence run](evidence/ssh-remote-end-to-end.txt), it couldn't run any other command, get a terminal or forward a port. The server it started couldn't open internet sockets, write the account's files or restart units.

If the machine holding the key is compromised, an attacker can read what maat's tools return on every host the key reaches, and file plans there. They can't change any host, because every plan still needs a person running `maatctl` and polkit on that host. See T11 in the [security model](security-model.md), including the remaining risks.

## Try it

On a disposable host, with maat installed and connected, as a user with `sudo`:

```bash
./scripts/ssh-host-setup.sh ops "$(cat agent_ed25519.pub)" "$(cat human_ed25519.pub)"
```

Then, from your machine:

```bash
./scripts/demo-ssh.sh ops@HOST ./agent_ed25519 ./human_ed25519 [PORT]
```

The setup script creates the `ops` account with both keys, installs the release `maatctl`, breaks the two units from bench scenario `service-transient-failure-001`, and adds a polkit rule that lets `ops` restart `uploader.service` only. The demo then works through the same steps as the evidence run: the key restrictions, an MCP session, a refused and an accepted proposal, review, apply, verification and the self-test.

## Limits

- Tested on one VM so far (F38).
- One MCP session reaches one host. An agent that should look at ten machines needs ten server entries. maat doesn't aggregate or orchestrate fleets.
- Approval happens on each host. There's no central place to approve plans.
- Tool results leave the host over SSH to the machine running the MCP client. That's the route the [privacy policy](../PRIVACY.md) describes ("through the MCP client you choose"), except that here the client is on another machine, so protect that machine and its key accordingly.
- With a hosted model, condensed and redacted log data also goes to that provider, exactly as in local use (T2).
