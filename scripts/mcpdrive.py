#!/usr/bin/env python3
"""Drive an MCP stdio server with a fixed list of JSON-RPC requests.

Usage: mcpdrive.py REQUESTS.jsonl COMMAND [ARGS...]

Requests are sent one at a time, each waiting for its response, the way a real
client waits for a tool result before the model decides its next call. (The Go
MCP SDK handles concurrent requests in parallel, so pipelining them all at once
would let a later propose_action run before an earlier tool call finished.)
The responses are printed as a JSON list.
"""
import json
import subprocess
import sys


def main() -> None:
    reqs = [json.loads(line) for line in open(sys.argv[1]) if line.strip()]
    proc = subprocess.Popen(sys.argv[2:], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, bufsize=1)
    responses = []
    for req in reqs:
        proc.stdin.write(json.dumps(req) + "\n")
        proc.stdin.flush()
        if "id" not in req:
            continue  # a notification has no response
        while True:
            line = proc.stdout.readline()
            if not line:
                sys.exit("server exited before answering request %s" % req["id"])
            msg = json.loads(line)
            if msg.get("id") == req["id"]:
                responses.append(msg)
                break
    proc.stdin.close()
    proc.wait()
    json.dump(responses, sys.stdout)


if __name__ == "__main__":
    main()
