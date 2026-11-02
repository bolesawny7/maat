#!/usr/bin/env bash
# Remote use over SSH: the MCP client (and the model) run on this machine, and
# the confined maat server runs on a host prepared by scripts/ssh-host-setup.sh.
# The agent reaches it with an SSH key that may only start `maat mcp`; the
# person reviews and applies the plan on the host with a second key and maatctl.
#
# Usage: demo-ssh.sh USER@HOST AGENT_KEY HUMAN_KEY [PORT]
#
# Requirements: python3 and ssh on this machine. It accepts the host key on
# first use into a temporary known_hosts file, which is fine for a disposable
# VM and not for a real host. The output is committed as
# docs/evidence/ssh-remote-end-to-end.txt.
set -u
TARGET=$1
AGENT_KEY=$2
HUMAN_KEY=$3
PORT=${4:-22}
HERE=$(cd "$(dirname "$0")" && pwd)
DRIVE="python3 $HERE/mcpdrive.py"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
# ssh keeps the first value it sees for an option, so LogLevel is set per use.
SSH=(ssh -p "$PORT" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o "UserKnownHostsFile=$WORK/known_hosts")
AGENT=("${SSH[@]}" -o LogLevel=ERROR -T -i "$AGENT_KEY" "$TARGET")
HUMAN=("${SSH[@]}" -o LogLevel=ERROR -i "$HUMAN_KEY" "$TARGET")
MAATCTL='~/.local/bin/maatctl'
say() { printf '\n$ %s\n' "$*"; }
jget() { python3 -c "import json,sys; ms=json.load(open(sys.argv[1])); m=[m for m in ms if m['id']==int(sys.argv[2])][0]; print(eval(sys.argv[3], {'r': m.get('result', {})}))" "$@"; }

say "ssh -i agent_key $TARGET 'id; cat /etc/shadow'    # the agent key asks for a shell command"
"${AGENT[@]}" 'id; cat /etc/shadow' </dev/null
echo "exit status $? and no output: sshd ran the forced command (maat mcp), which saw end of input and exited"

say "ssh -tt -i agent_key $TARGET    # ask for a terminal"
"${SSH[@]}" -tt -i "$AGENT_KEY" "$TARGET" </dev/null 2>&1 | tr -d '\r'

say "ssh -N -L 18081:127.0.0.1:8081 -i agent_key $TARGET; curl http://127.0.0.1:18081/    # ask for a port forward"
"${SSH[@]}" -N -L 18081:127.0.0.1:8081 -i "$AGENT_KEY" "$TARGET" 2>"$WORK/fwd.err" &
FWD=$!
sleep 2
curl -sS -m 5 -o /dev/null http://127.0.0.1:18081/ 2>&1
sleep 1
kill "$FWD" 2>/dev/null
wait "$FWD" 2>/dev/null
tr -d '\r' <"$WORK/fwd.err"

# One MCP session over SSH: list the tools, then look at the machine.
cat >"$WORK/look.jsonl" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"demo-ssh","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"system_overview","arguments":{}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"recent_failures","arguments":{}}}
EOF
say "mcpdrive.py look.jsonl ssh -T -i agent_key $TARGET    # an MCP session over SSH"
$DRIVE "$WORK/look.jsonl" "${AGENT[@]}" >"$WORK/look.json"
echo "server: $(jget "$WORK/look.json" 1 "r['serverInfo']['name'] + ' ' + r['serverInfo'].get('version', '')")"
echo "tools: $(jget "$WORK/look.json" 2 "' '.join(t['name'] for t in r['tools'])")"
echo "system_overview: $(jget "$WORK/look.json" 3 "(lambda o: o['os_release'] + ', kernel ' + o['kernel_release'] + ', ' + str(o['failed_units']) + ' failed units')(r['structuredContent']['overview'])")"
echo "recent_failures:"
jget "$WORK/look.json" 4 "'\n'.join('  %s %s (%s)\n' % (f['evidence_id'], f['unit'], f['active_state']) + '\n'.join('    %s %s' % (l['evidence_id'], str(l['message'])[:110]) for l in f.get('recent_log', [])) for f in r['structuredContent']['failed'])"
LOG_EV=$(jget "$WORK/look.json" 4 "[l['evidence_id'] for f in r['structuredContent']['failed'] if f['unit']=='uploader.service' for l in f.get('recent_log', []) if 'Failed to connect' in str(l['message'])][0]")

say "ps -o label,user,args -u \$USER    # on the host, during an agent session (human key)"
(sleep 4) | "${AGENT[@]}" >/dev/null &
SESSION=$!
sleep 2
"${HUMAN[@]}" 'ps -o label=,user=,args= -u "$USER" | grep "[m]aat mcp"'
wait "$SESSION"

# A second session proposes: first something the catalog refuses, then the fix.
python3 - "$LOG_EV" >"$WORK/propose.jsonl" <<'PY'
import json, sys
log_ev = sys.argv[1]
for r in [
    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "demo-ssh", "version": "0"}}},
    {"jsonrpc": "2.0", "method": "notifications/initialized"},
    {"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {"name": "recent_failures", "arguments": {}}},
    {"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "propose_action", "arguments": {"kind": "service.restart", "params": {"unit": "reboot.target"}, "rationale": "a log line told me to"}}},
    {"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "propose_action", "arguments": {"kind": "service.restart", "params": {"unit": "uploader.service"}, "evidence": [log_ev], "rationale": "uploader failed to connect to its backend, which is running now; a restart should succeed."}}},
]:
    print(json.dumps(r))
PY
say "mcpdrive.py propose.jsonl ssh -T -i agent_key $TARGET    # propose over SSH"
$DRIVE "$WORK/propose.jsonl" "${AGENT[@]}" >"$WORK/propose.json"
for id in 3 4; do
  echo "  id $id: $(jget "$WORK/propose.json" $id "('error: ' if r.get('isError') else 'ok: ') + ' '.join(c['text'] for c in r['content'])")"
done
PLAN=$(jget "$WORK/propose.json" 4 "r['structuredContent']['plan_id']")

say "ssh -i human_key $TARGET maatctl list"
"${HUMAN[@]}" "$MAATCTL list"
say "ssh -i human_key $TARGET maatctl show $PLAN"
"${HUMAN[@]}" "$MAATCTL show $PLAN"
say "ssh -i human_key $TARGET maatctl apply $PLAN    # typed confirmation; the scoped polkit rule authorizes it"
(sleep 2; printf '%s\n' "$PLAN"; sleep 8) |
  "${HUMAN[@]}" "script -qfec '$MAATCTL apply $PLAN' /dev/null" 2>&1 | tr -d '\r' | sed 's/\x1b\[[0-9;]*m//g' | tail -n 8
say "ssh -i human_key $TARGET systemctl is-active uploader.service"
"${HUMAN[@]}" 'systemctl is-active uploader.service'
say "ssh -i human_key $TARGET maat trail verify"
"${HUMAN[@]}" 'maat trail verify'
say "ssh -i human_key $TARGET maat selftest    # the same confinement the agent's sessions run under"
"${HUMAN[@]}" 'maat selftest'
