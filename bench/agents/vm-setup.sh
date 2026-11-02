#!/usr/bin/env bash
# Prepare a disposable k3s VM for maat-bench/agents. Run it inside the VM as a
# user with sudo (for example Colima's "lima" user); see README.md for the host
# side. It:
#   - locks the k3s admin kubeconfig to root and gives this user a private copy,
#   - creates an unprivileged "agent" user whose kubeconfig can edit namespace
#     shop and view the rest of the cluster,
#   - installs k8sgpt, HolmesGPT and OpenCode for that user, pointed at an
#     Ollama server on 127.0.0.1:11434 (tunnelled in from the host).
set -euo pipefail
K8SGPT_VERSION=${K8SGPT_VERSION:-v0.4.41}
HOLMES_VERSION=${HOLMES_VERSION:-0.45.0}
MODELS=${MODELS:-"qwen3:8b gpt-oss:20b"}
arch=$(dpkg --print-architecture)

sudo DEBIAN_FRONTEND=noninteractive apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq jq unzip >/dev/null
id agent >/dev/null 2>&1 || sudo useradd -m -s /bin/bash agent
sudo install -d -m 1777 /tmp/agents-run

# k3s writes its admin kubeconfig with the mode it was started with (Colima uses
# 644), and the k3s kubectl falls back to that file when KUBECONFIG is unset, so
# any local user would be cluster-admin. The runner re-checks this every run.
sudo chmod 600 /etc/rancher/k3s/k3s.yaml
install -d -m 700 "$HOME/.kube"
sudo cat /etc/rancher/k3s/k3s.yaml >"$HOME/.kube/bench-admin.yaml"
chmod 600 "$HOME/.kube/bench-admin.yaml"
K="kubectl --kubeconfig $HOME/.kube/bench-admin.yaml"

$K create namespace shop --dry-run=client -o yaml | $K apply -f - >/dev/null
$K create namespace agents --dry-run=client -o yaml | $K apply -f - >/dev/null
$K -n agents create serviceaccount sre-agent --dry-run=client -o yaml | $K apply -f - >/dev/null
$K -n shop create rolebinding sre-agent-edit --clusterrole=edit --serviceaccount=agents:sre-agent --dry-run=client -o yaml | $K apply -f - >/dev/null
$K create clusterrolebinding sre-agent-view --clusterrole=view --serviceaccount=agents:sre-agent --dry-run=client -o yaml | $K apply -f - >/dev/null

SERVER=$(awk '/server:/ {print $2; exit}' "$HOME/.kube/bench-admin.yaml")
CA=$(awk '/certificate-authority-data/ {print $2; exit}' "$HOME/.kube/bench-admin.yaml")
TOKEN=$($K -n agents create token sre-agent --duration=72h)
sudo install -d -m 700 -o agent -g agent /home/agent/.kube
sudo tee /home/agent/.kube/config >/dev/null <<EOF
apiVersion: v1
kind: Config
clusters:
- name: bench
  cluster:
    server: $SERVER
    certificate-authority-data: $CA
users:
- name: sre-agent
  user:
    token: $TOKEN
contexts:
- name: bench
  context: {cluster: bench, user: sre-agent, namespace: shop}
current-context: bench
EOF
sudo chown agent:agent /home/agent/.kube/config
sudo chmod 600 /home/agent/.kube/config
grep -qs 'KUBECONFIG=' /home/agent/.bashrc || echo 'export KUBECONFIG=$HOME/.kube/config' | sudo tee -a /home/agent/.bashrc >/dev/null

curl -fsSL -o /tmp/k8sgpt.deb "https://github.com/k8sgpt-ai/k8sgpt/releases/download/$K8SGPT_VERSION/k8sgpt_$arch.deb"
sudo dpkg -i /tmp/k8sgpt.deb >/dev/null
sudo -u agent -H k8sgpt auth add -b ollama -m "${MODELS%% *}" -u http://127.0.0.1:11434 >/dev/null
sudo -u agent -H k8sgpt auth default -p ollama >/dev/null

sudo -iu agent bash -c 'curl -LsSf https://astral.sh/uv/install.sh | sh >/dev/null 2>&1'
sudo -iu agent bash -c "~/.local/bin/uv tool install --quiet --python 3.12 holmesgpt==$HOLMES_VERSION"
sudo -iu agent bash -c 'curl -fsSL https://opencode.ai/install | bash >/dev/null 2>&1'

models_json=$(for m in $MODELS; do printf '"%s": {"name": "%s"},' "$m" "$m"; done)
sudo -u agent mkdir -p /home/agent/.config/opencode /home/agent/work
sudo -u agent tee /home/agent/.config/opencode/opencode.json >/dev/null <<EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "provider": {
    "ollama": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Ollama (local)",
      "options": {"baseURL": "http://127.0.0.1:11434/v1"},
      "models": {${models_json%,}}
    }
  }
}
EOF

echo "== checks =="
if sudo -u agent test -r /etc/rancher/k3s/k3s.yaml; then echo "FAIL: agent can read the admin kubeconfig"; exit 1; fi
echo "ok: agent cannot read the admin kubeconfig"
A="sudo -u agent -H env KUBECONFIG=/home/agent/.kube/config"
$A kubectl auth whoami -o jsonpath='{.status.userInfo.username}{"\n"}'
for v in "delete deployments -n shop" "delete deployments -n kube-system"; do
  printf '%-40s %s\n' "can-i $v:" "$($A kubectl auth can-i $v)"
done
$A k8sgpt version
$A /home/agent/.local/bin/holmes version 2>&1 | tail -n 1
$A /home/agent/.opencode/bin/opencode --version
curl -fsS -m 3 http://127.0.0.1:11434/api/version || echo "warning: Ollama isn't reachable on 127.0.0.1:11434 yet"
