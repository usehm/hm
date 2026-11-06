#!/usr/bin/env python3
"""Drive hm's agent over RPC: send a prompt, show what it does, approve
its calls and answer its questions, then print the answer.

    python3 examples/rpc_client.py "fix the failing test"
    python3 examples/rpc_client.py --yes "list the packages" -- --cwd ~/src/app --mode lazy

Arguments after "--" go to `hm agent --rpc`. Without --yes, each call
that needs approval is put to you as y/n on the terminal. The exit code is
hm's: 0 done, 1 failed, 130 interrupted. Only the standard library is
used. docs/rpc.md describes the protocol.
"""

import argparse
import json
import subprocess
import sys


def main():
    args = sys.argv[1:]
    hm_args = []
    if "--" in args:
        i = args.index("--")
        args, hm_args = args[:i], args[i + 1:]
    p = argparse.ArgumentParser(description="Drive hm agent --rpc.")
    p.add_argument("prompt")
    p.add_argument("--yes", action="store_true", help="approve every call without asking")
    p.add_argument("--hm", default="hm", help="the hm command (default: hm)")
    opts = p.parse_args(args)

    proc = subprocess.Popen(
        [opts.hm, "agent", "--rpc", *hm_args],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, bufsize=1,
    )
    assert proc.stdin and proc.stdout
    next_id = 0

    def send(cmd):
        nonlocal next_id
        next_id += 1
        cmd["id"] = str(next_id)
        proc.stdin.write(json.dumps(cmd) + "\n")
        proc.stdin.flush()
        return cmd["id"]

    def ask(question):
        print(question, end=" ", file=sys.stderr, flush=True)
        line = sys.stdin.readline()
        return line.strip()

    top = None  # the session we prompted; sub-agents have their own
    exit_code = 1
    for line in proc.stdout:
        ev = json.loads(line)
        kind = ev["type"]
        if kind == "session_start" and top is None:
            top = ev["session"]
            send({"type": "prompt", "text": opts.prompt})
        elif kind == "response" and not ev["ok"]:
            print(f"! {ev['command']}: {ev['error']}", file=sys.stderr)
        elif kind == "text_delta" and ev["session"] == top:
            print(ev["text"], end="", flush=True)
        elif kind == "message_end" and ev["role"] == "assistant" and ev["session"] == top and ev["text"]:
            print()
        elif kind == "tool_call":
            print(f"→ {ev['tool']} {ev['summary']}", file=sys.stderr)
        elif kind == "approval_needed":
            allow = opts.yes or ask(f"Allow {ev['tool']}: {ev['summary']}? [y/N]").lower() in ("y", "yes")
            send({"type": "approve", "call_id": ev["call_id"], "allow": allow,
                  "note": "" if allow else "the user said no"})
        elif kind == "question":
            send({"type": "answer", "call_id": ev["call_id"], "text": ask(ev["text"]) or "no preference"})
        elif kind == "turn_end" and ev["session"] == top:
            if ev.get("error"):
                print(f"! {ev['error']}", file=sys.stderr)
            proc.stdin.close()  # no more prompts: hm ends the session
        elif kind == "session_end":
            exit_code = ev["exit_code"]
            u = ev["usage"]
            print(f"({u['input']} in, {u['output']} out, ${u['cost']:.4f})", file=sys.stderr)
    proc.wait()
    return exit_code if proc.returncode == 0 else proc.returncode


if __name__ == "__main__":
    sys.exit(main())
