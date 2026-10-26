# Security policy

maat exists to let AI agents look at real machines safely, so security reports are especially welcome.

## Supported versions

None yet: maat hasn't been released. Once v0.1 ships, the latest release will be supported.

## Reporting a vulnerability

Please don't open a public issue. Use GitHub's private vulnerability reporting instead (the "Report a vulnerability" button on the repository's Security tab). You'll get an acknowledgement within 7 days.

## In scope

- Any way for the confined server to write to the host, open a network connection, or run code outside its confinement.
- Any way for `maatctl` to execute something that wasn't reviewed and confirmed, or to act on parameters other than the ones it showed.
- Tampering with the trail that `maat trail verify` doesn't detect.
- Secrets passing through redaction that a reasonable pattern should have caught.
- Prompt injection that leads to an action without a person approving it.

## Out of scope

- Attacks that require root or a kernel exploit.
- Vulnerabilities in snapd, AppArmor, polkit, systemd or an MCP client. Please report those to the upstream project. If maat's design makes one of them worse, that part is in scope.
- A person approving a harmful plan after being shown an accurate preview.
