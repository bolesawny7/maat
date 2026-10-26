#!/usr/bin/env bash
# Provision an Ubuntu VM to build and test maat: Go toolchain, build tools,
# snapd + snapcraft (for the strict snap), and LXD (for maat-bench).
#
# Idempotent: safe to re-run. Intended for a throwaway VM, not a real machine.
set -euo pipefail

GO_VERSION="${GO_VERSION:-1.26.10}"
ARCH="$(dpkg --print-architecture)" # amd64 or arm64

echo "== apt build tools =="
sudo DEBIAN_FRONTEND=noninteractive apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
  build-essential curl ca-certificates jq >/dev/null

echo "== Go ${GO_VERSION} (${ARCH}) =="
if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "go${GO_VERSION}"; then
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tgz
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf /tmp/go.tgz
fi
grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null || \
  echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' | sudo tee /etc/profile.d/go.sh >/dev/null
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
go version

echo "== snapd =="
sudo systemctl enable --now snapd.socket snapd.service >/dev/null 2>&1 || true
sudo systemctl start snapd.seeded.service 2>/dev/null || true
# Wait for snapd to be ready to accept commands.
sudo snap wait system seed.loaded 2>/dev/null || sleep 5
sudo snap version || true

echo "== journal access for the lima user =="
# So non-snap collector tests can read the journal during development.
sudo usermod -aG adm,systemd-journal "$USER" || true
# Ensure persistent journald storage so FSS and history tests work.
sudo mkdir -p /var/log/journal
sudo systemd-tmpfiles --create --prefix /var/log/journal 2>/dev/null || true
sudo systemctl restart systemd-journald 2>/dev/null || true

echo "== done =="
echo "Go:       $(go version)"
echo "snapcraft/LXD install is a separate, slower step: scripts/vm-setup-snap.sh"
