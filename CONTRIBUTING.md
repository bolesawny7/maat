# Contributing to maat

Thanks for your interest. maat is an early but working preview. Some of the most useful contributions right now still aren't code:

- **Challenge the design.** Open an issue if a fact in [docs/references.md](docs/references.md) is wrong or out of date, or if [the security model](docs/security-model.md) misses a threat.
- **Test an assumption.** Each open assumption (A1, A2, and so on) in the references describes a test. Running one on a real Ubuntu machine and reporting the result is a genuine contribution.
- **Propose a benchmark scenario,** ideally with a link to a real bug report or forum thread showing the failure happens in practice.

## Before you start

For anything bigger than a typo, open an issue first so we can agree on the approach before you spend time on it.

## Ground rules

The rules in [AGENTS.md](AGENTS.md) apply to humans too. In short: cite sources, never fabricate results, keep the server read-only and offline, and never use a shell.

## Development setup

- Go 1.26 or 1.27. `make all race` runs the build, tests, vet and the security invariants; `make fuzz` runs short fuzz passes. The unit tests run on any OS.
- Snapcraft and an Ubuntu 24.04 or 26.04 machine or VM for anything that touches confinement. Claims about confinement must be checked inside the snap.
- LXD, for the benchmark (`make bench-bin`, then `./bin/harness`). `scripts/vm-setup.sh` and `scripts/vm-setup-snap.sh` prepare a VM.

## Pull requests

- Keep each pull request to one logical change.
- Add tests for any behavior change.
- Update the docs in the same pull request.
- Use [Conventional Commits](https://www.conventionalcommits.org/) and sign off every commit (`git commit -s`) to certify the [Developer Certificate of Origin](https://developercertificate.org/).
- AI-assisted work is welcome if you understand and have tested every line. Say which parts were assisted.

## Conduct

Be respectful and constructive. This project follows the [Contributor Covenant](https://www.contributor-covenant.org/version/2/1/code_of_conduct/).
