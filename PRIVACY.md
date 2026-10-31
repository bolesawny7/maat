# Privacy Policy for maat

Effective date: October 26, 2026

## What maat does

maat is a strictly confined MCP server that reads system state from the local Ubuntu machine it runs on. It exposes read-only diagnostics such as systemd journal entries, unit states, process lists, filesystem usage, and AppArmor denials to an AI agent through the Model Context Protocol (MCP).

## What data maat collects

maat does **not** collect personal data, telemetry, or analytics. It reads only the system information needed to answer a diagnostic query. That information is:

- returned directly to the MCP client that launched maat,
- never sent to a remote server operated by the maat project, and
- never stored permanently by maat itself, except for the optional hash-chained audit trail written to the local machine.

## Where data goes

maat runs entirely on your machine. The confined server has **no network access**:

- the snap has no `network` plug,
- the snap deliberately does not use `network-observe`, because that interface permits outbound TCP,
- all reads are local to the host.

The only way data leaves the machine is through the MCP client you choose (for example, OpenCode or Claude Code). If you use a local inference snap, no diagnostic data leaves the machine at all. If you use a hosted model, the MCP client sends the data to that provider under the provider's terms, not maat's.

## Audit trail

When you approve and apply a plan with `maatctl`, the proposal, approval, execution, and verification are recorded in a tamper-evident audit trail on the local machine. This trail is under your control and is not uploaded anywhere.

## Third parties

maat does not integrate with third-party services, trackers, or advertising networks.

## Changes to this policy

If this policy changes, the update will be published in the maat repository and noted in the release notes.

## Contact

For privacy questions, open a GitHub issue or use the private vulnerability reporting process described in [SECURITY.md](SECURITY.md).
