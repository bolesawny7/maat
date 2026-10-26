#!/usr/bin/env bash
# Install snapcraft (to build the strict snap) and LXD (to run maat-bench).
# Separate from vm-setup.sh because snap installs are slow and sometimes need a
# second attempt on a freshly seeded snapd.
set -euo pipefail

echo "== snapcraft =="
sudo snap install snapcraft --classic

echo "== LXD =="
sudo snap install lxd || sudo snap refresh lxd || true
sudo usermod -aG lxd "$USER" || true
# Minimal non-interactive LXD init with a dir storage pool (no ZFS needed).
if ! sudo lxc storage show default >/dev/null 2>&1; then
  cat <<'YAML' | sudo lxd init --preseed
config: {}
networks:
  - name: lxdbr0
    type: bridge
    config:
      # Use an explicit subnet: auto-detection can fail inside a nested VM.
      ipv4.address: 10.201.0.1/24
      ipv4.nat: "true"
      ipv6.address: none
storage_pools:
  - name: default
    driver: dir
profiles:
  - name: default
    devices:
      eth0:
        name: eth0
        network: lxdbr0
        type: nic
      root:
        path: /
        pool: default
        type: disk
YAML
fi
sudo lxc version || true
echo "== done =="
