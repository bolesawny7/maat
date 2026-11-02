#!/usr/bin/env bash
# Prepare a disposable Ubuntu host for scripts/demo-ssh.sh. Run it on the host,
# as a user with sudo, with the maat snap already installed and its three
# interfaces connected.
#
# Usage: ssh-host-setup.sh USER AGENT_PUBKEY HUMAN_PUBKEY
#
# It creates USER (in systemd-journal, not in sudo) with two SSH keys: the agent
# key may only run `maat mcp`, the human key gets a normal shell. It installs the
# release maatctl for USER, breaks the two units from bench scenario
# service-transient-failure-001, and adds a polkit rule that lets USER restart
# uploader.service only. Don't run it on a machine you care about.
set -euo pipefail
U=$1
AGENT_PUB=$2
HUMAN_PUB=$3

id "$U" >/dev/null 2>&1 || sudo useradd -m -s /bin/bash -G systemd-journal "$U"
sudo install -d -m 700 -o "$U" -g "$U" "/home/$U/.ssh"
printf 'restrict,command="env MAAT_CLIENT=remote-agent /snap/bin/maat mcp" %s\n%s\n' "$AGENT_PUB" "$HUMAN_PUB" |
  sudo tee "/home/$U/.ssh/authorized_keys" >/dev/null
sudo chown "$U:$U" "/home/$U/.ssh/authorized_keys"
sudo chmod 600 "/home/$U/.ssh/authorized_keys"

arch=$(dpkg --print-architecture)
sudo -u "$U" mkdir -p "/home/$U/.local/bin"
sudo -u "$U" curl -fsSL -o "/home/$U/.local/bin/maatctl" \
  "https://github.com/bolesawny7/maat/releases/latest/download/maatctl_linux_$arch"
sudo chmod +x "/home/$U/.local/bin/maatctl"

printf '[Unit]\nDescription=Report uploader\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/usr/bin/curl -sS -f -o /dev/null http://127.0.0.1:8081/\n' |
  sudo tee /etc/systemd/system/uploader.service >/dev/null
printf '[Unit]\nDescription=Report backend\n[Service]\nExecStart=/usr/bin/python3 -m http.server 8081 --bind 127.0.0.1\n' |
  sudo tee /etc/systemd/system/backend.service >/dev/null
sudo systemctl daemon-reload
sudo systemctl stop backend.service uploader.service 2>/dev/null || true
sudo systemctl reset-failed uploader.service 2>/dev/null || true
sudo systemctl start uploader.service || true
sudo systemctl start backend.service
sleep 2

sudo tee /etc/polkit-1/rules.d/50-maat-ssh-demo.rules >/dev/null <<EOF
// Let $U restart uploader.service without a password; every other unit and
// verb still asks for admin authentication.
polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units" && subject.user == "$U" &&
        action.lookup("unit") == "uploader.service" && action.lookup("verb") == "restart") {
        return polkit.Result.YES;
    }
});
EOF
systemctl list-units --failed --no-legend | cat
