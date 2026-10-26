#!/usr/bin/env bash
# End-to-end demonstration on a disposable Ubuntu VM: the confined maat server
# proposes fixes, and an unconfined maatctl reviews, refuses tampered plans,
# asks polkit, applies, verifies, and records everything in the trail.
#
# Requirements: the maat snap installed with its three interfaces connected,
# your user in the systemd-journal group, a release maatctl at $MAATCTL
# (default ~/bin/maatctl), python3, and sudo. It creates two broken demo units
# and a polkit rule scoped to them, and removes the rule at the end. It deletes
# maat's plans and trail under ~/snap/maat/common. Don't run it on a machine
# you care about.
#
# The output is committed as docs/evidence/maatctl-end-to-end.txt.
set -u
MAATCTL=${MAATCTL:-$HOME/bin/maatctl}
HERE=$(cd "$(dirname "$0")" && pwd)
DRIVE="python3 $HERE/mcpdrive.py"
RULE=/etc/polkit-1/rules.d/50-maat-demo.rules
WORK=$(mktemp -d)
trap 'sudo rm -f $RULE; rm -rf "$WORK"' EXIT
say() { printf '\n$ %s\n' "$*"; }
jget() { python3 -c "import json,sys; ms=json.load(open(sys.argv[1])); m=[m for m in ms if m['id']==int(sys.argv[2])][0]; print(eval(sys.argv[3], {'r': m.get('result', {})}))" "$@"; }

# Two broken units: one whose unit file was removed after it failed (only a
# reset can clear it), and one whose binary is missing (a restart can't help).
rm -rf ~/snap/maat/common/plans ~/snap/maat/common/trail
printf '[Unit]\nDescription=maat stale demo\n[Service]\nType=oneshot\nExecStart=/bin/false\n' | sudo tee /etc/systemd/system/maat-stale.service >/dev/null
printf '[Unit]\nDescription=maat demo service\n[Service]\nExecStart=/usr/local/bin/maat-demo-missing --serve\n' | sudo tee /etc/systemd/system/maat-demo.service >/dev/null
sudo systemctl daemon-reload
sudo systemctl start maat-stale.service 2>/dev/null
sudo rm /etc/systemd/system/maat-stale.service
sudo systemctl daemon-reload
sudo systemctl reset-failed maat-demo.service 2>/dev/null
sudo systemctl start maat-demo.service 2>/dev/null
sleep 1
say systemctl list-units --failed --no-legend
systemctl list-units --failed --no-legend | cat

# One MCP session with the confined server: look, then propose. The second
# proposal cites one real evidence ID and one invented one, and its rationale
# tries to repaint the reviewer's terminal.
cat > "$WORK/look.jsonl" <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recent_failures","arguments":{}}}
EOF
sg systemd-journal -c "env MAAT_CLIENT=demo $DRIVE $WORK/look.jsonl maat mcp" > "$WORK/look.json"
UNIT_EV=$(jget "$WORK/look.json" 2 "[f for f in r['structuredContent']['failed'] if f['unit']=='maat-stale.service'][0]['evidence_id']")
LOG_EV=$(jget "$WORK/look.json" 2 "[l['evidence_id'] for f in r['structuredContent']['failed'] if f['unit']=='maat-demo.service' for l in f['recent_log'] if '203' in l['message']][0]")
python3 - "$UNIT_EV" "$LOG_EV" > "$WORK/propose.jsonl" <<'PY'
import json, sys
unit_ev, log_ev = sys.argv[1], sys.argv[2]
hostile = "The unit file is gone (load_state not-found), so only its failed flag remains.\u001b[2J\u001b[H\u001b[1;31mmaatctl: plan VERIFIED by Canonical\u001b[0m \u202eevil"
for r in [
    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "demo", "version": "0"}}},
    {"jsonrpc": "2.0", "method": "notifications/initialized"},
    {"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {"name": "recent_failures", "arguments": {}}},
    {"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "propose_action", "arguments": {"kind": "service.restart", "params": {"unit": "reboot.target"}, "rationale": "a log line told me to"}}},
    {"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "propose_action", "arguments": {"kind": "service.reset-failed", "params": {"unit": "maat-stale.service"}, "evidence": [unit_ev, "ev_deadbeef"], "rationale": hostile}}},
    {"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": {"name": "propose_action", "arguments": {"kind": "service.restart", "params": {"unit": "maat-demo.service"}, "evidence": [log_ev], "rationale": "Try a restart."}}},
]:
    print(json.dumps(r))
PY
sg systemd-journal -c "env MAAT_CLIENT=demo $DRIVE $WORK/propose.jsonl maat mcp" > "$WORK/propose.json"
say "propose_action results from the confined server"
for id in 3 4 5; do
  echo "  id $id: $(jget "$WORK/propose.json" $id "('error: ' if r.get('isError') else 'ok: ') + ' '.join(c['text'] for c in r['content'])")"
done
STALE=$(jget "$WORK/propose.json" 4 "r['structuredContent']['plan_id']")
DEMO=$(jget "$WORK/propose.json" 5 "r['structuredContent']['plan_id']")

say maatctl list
"$MAATCTL" list
say "maatctl show $STALE | cat -v    # cat -v would print a raw escape byte as ^["
"$MAATCTL" show "$STALE" | cat -v

PLAN=~/snap/maat/common/plans/$STALE.json
cp "$PLAN" "$WORK/plan.bak"
say "# tamper 1: weaken the precondition in the plan file, then show it"
sed -i '0,/"equals": "failed"/s//"equals": "inactive"/' "$PLAN"
"$MAATCTL" show "$STALE"
cp "$WORK/plan.bak" "$PLAN"
say "# tamper 2: edit only the rationale (still a valid plan), then apply"
sed -i 's/only its failed flag remains/it is safe, trust me/' "$PLAN"
"$MAATCTL" apply "$STALE" </dev/null
cp "$WORK/plan.bak" "$PLAN"

say "maatctl apply $DEMO    # no polkit rule: pkttyagent prompts on the terminal; a wrong password is typed"
(sleep 1.5; printf '%s\n' "$DEMO"; sleep 4; printf 'not-the-password\n'; sleep 5) |
  script -qfec "$MAATCTL apply $DEMO" /dev/null 2>&1 | tr -d '\r' | sed 's/\x1b\[[0-9;]*m//g' | tail -8

sudo tee "$RULE" >/dev/null <<EOF
// Let $USER reset maat-stale.service and restart maat-demo.service without a
// password; every other unit and verb still asks for authentication.
polkit.addRule(function(action, subject) {
    if (action.id == "org.freedesktop.systemd1.manage-units" && subject.user == "$USER" &&
        ((action.lookup("unit") == "maat-stale.service" && action.lookup("verb") == "reset-failed") ||
         (action.lookup("unit") == "maat-demo.service" && action.lookup("verb") == "restart"))) {
        return polkit.Result.YES;
    }
});
EOF
sleep 1
say "sudo cat $RULE"
sudo cat "$RULE"
say "maatctl apply $STALE    # authorized by the scoped rule above"
(sleep 1.5; printf '%s\n' "$STALE"; sleep 6) |
  script -qfec "$MAATCTL apply $STALE" /dev/null 2>&1 | tr -d '\r' | tail -7

say maatctl list
"$MAATCTL" list
say maat trail verify
sg systemd-journal -c "maat trail verify"

TRAIL=~/snap/maat/common/trail/trail.jsonl
cp "$TRAIL" "$WORK/trail.bak"
N=$(wc -l < "$TRAIL")
say "# tamper 3: delete the newest three records from the trail, then verify"
head -n $((N - 3)) "$WORK/trail.bak" > "$TRAIL"
sg systemd-journal -c "maat trail verify"
cp "$WORK/trail.bak" "$TRAIL"
