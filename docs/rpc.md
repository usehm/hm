# hm agent RPC, version 1

`hm agent --rpc` runs hm's agent for another program: an editor plugin, a bot, a test harness. The program writes JSON commands to hm's standard input, one per line, and reads JSON lines from its standard output: the [events](events.md) of `--output json`, and a `response` to each command. Warnings for people go to standard error. [examples/rpc_client.py](../examples/rpc_client.py) is a complete client in about a hundred lines of Python.

```sh
hm agent --rpc [--cwd DIR] [--mode NAME] [--provider NAME] [--model ID]
               [--session ID | --continue | --no-session] [--max-steps N]
               [--approve ask|edits|all]
```

The flags are those of `hm agent -p`, except that prompts come as commands, not arguments.

## Life of a session

1. hm writes `session_start` for the session it opened, then waits for commands.
2. You send `prompt`. hm answers with a `response` as soon as the turn starts; the turn's events follow, and `turn_end` says it is over.
3. While a turn runs you can `steer` it, queue a `follow_up`, `abort` it, and must `approve` the calls the mode asks about and `answer` the agent's questions. A second `prompt` is refused until `turn_end`.
4. When you close hm's standard input, hm waits for the running turn, writes `session_end` and exits `0`. To stop at once, send `abort` first. Bad flags exit `2` with no output; Ctrl-C (SIGINT or SIGTERM) aborts and exits `130`.

## Commands

```json
{"id":"7","type":"prompt","text":"fix the failing test"}
```

`type` names the command. `id` is optional and may be any JSON value; the response carries it back unchanged.

```json
{"v":1,"type":"response","session":"20261106-…","time":"…","id":"7","command":"prompt","ok":true}
{"v":1,"type":"response","session":"20261106-…","time":"…","id":"8","command":"prompt","ok":false,"error":"a turn is running; steer it or queue a follow-up"}
```

A response has `ok`, an `error` when it failed, and `data` for the commands that return something. A line that is not JSON, or names no known command, gets `ok: false` (with `id: null` when even that could not be read), and hm reads on.

| command | fields | answers |
|---|---|---|
| `prompt` | `text` | When the turn starts. Fails while another runs. |
| `steer` | `text` | At once. The message reaches the model after the tool call in progress; the calls of that batch not yet started are skipped. Sent while idle, it waits for the next prompt. |
| `follow_up` | `text` | At once. The message waits for the turn to end, then starts another step. |
| `abort` | | At once. Stops the turn and background sub-agents; calls waiting for approval are refused and questions go unanswered. Queued messages are dropped. |
| `approve` | `call_id`, `allow`, and optionally `for_session`, `note`, `edits` | At once. Fails unless that call is waiting for approval. |
| `answer` | `call_id`, `text` | At once. Fails unless that call is waiting for an answer. |
| `set_mode` | `mode` | At once; applies from the next turn. |
| `set_model` | `provider`, `model` and/or `effort` | At once; applies from the next turn. An empty provider keeps the current one, an empty model is the provider's configured one. `effort` (`off`, `low`, `medium`, `high`, `xhigh`, `max`, or `""` for the provider's) is kept when left out; with only `effort`, the model stays. |
| `compact` | `focus` (optional) | When it is done, after `compaction_start` and `compaction_end`. Fails while a turn runs. |
| `get_state` | | `data`: `session` (below), `running`, `pending_approvals` and `pending_questions` (call ids). |
| `list_sessions` | `all` (optional) | `data.sessions`: the saved sessions of the workspace, or every one with `all`, newest first: `id`, `title`, `mode`, `provider`, `root`, `parent_id`, `fork_of`, `status`, `updated`, `turns`, `cost`. |
| `fork` | `entry` (optional) | When it is idle only. The session goes on as a fork from `entry`, or from its latest message: a new saved session, which hm works in from then on and announces with a new `session_start`. `data` is the fork's `session`. |
| `export` | `format` (`html`, `md` or `jsonl`; default `html`), `path`, `all_branches` | Writes the session to a file, with the secrets hm knows of and anything shaped like a key taken out. `path` is relative to the workspace; a folder or nothing gets the default name (in Downloads). `data`: `path`, and `redacted`, such as `["2 anthropic key"]`. |

A `session` in `data` has `id`, `title`, `root`, `mode`, `provider`, `model`, `effort` (when set), `status`, `running`, `saved`, `context` (tokens the next model call about sends) and `usage` (`input`, `output`, `cache_read`, `cache_write`, `cost`).

## Approvals

The mode and guard.yaml decide which calls need approval, as in the app; guard.yaml deny rules refuse a call before anyone is asked. `--approve` says who decides:

- `ask` (the default): you do, for every call.
- `edits`: file edits are allowed without asking; you decide the rest.
- `all`: every call is allowed without asking.

A call that needs approval shows as `approval_needed`, after its `tool_call`:

```json
{"v":1,"type":"approval_needed","session":"…","time":"…","call_id":"toolu_01","tool":"shell","category":"shell","summary":"go test ./...","command":"go test ./...","paths":["/home/me/app"]}
```

The agent waits until you send `approve` with its `call_id`:

```json
{"type":"approve","call_id":"toolu_01","allow":true}
{"type":"approve","call_id":"toolu_01","allow":false,"note":"run only the store's tests"}
{"type":"approve","call_id":"toolu_02","allow":true,"for_session":true}
{"type":"approve","call_id":"toolu_03","allow":true,"edits":[{"path":"main.go","content":"package main\n"}]}
```

- `note` goes to the model; say why you refused.
- `for_session` allows calls like this one for the rest of the session, when the call allows it.
- `edits`, for an edit call, are the files to apply, each with its whole new content: leave a file out to skip it, or change its content to apply only part of the change. Paths are relative to the workspace. Files the call did not plan to change are ignored.

`approval` then says what was decided, and `tool_result` follows. With `edits` or `all`, `approval_needed` and `approval` still appear, with nothing to send.

## Questions

When the agent asks you something (the `ask_user` tool), hm writes `question` with the question and its call:

```json
{"v":1,"type":"question","session":"…","time":"…","call_id":"toolu_04","text":"Which database should the tests use?"}
```

Send `answer` with that `call_id` and your `text`. The agent waits for it; `abort` ends the wait.

## A session

What goes in (`>`) and comes out (`<`), with the header fields shortened:

```
< {"type":"session_start","session":"S1","root":"/home/me/app","mode":"auto","provider":"anthropic","model":"","resumed":false,"prompt":""}
> {"id":"1","type":"prompt","text":"run the tests"}
< {"type":"response","id":"1","command":"prompt","ok":true}
< {"type":"message_end","role":"user","text":"run the tests"}
< {"type":"message_start","role":"assistant"}
< {"type":"tool_start","tool":"shell"}
< {"type":"usage","input":2410,"output":38,"cache_read":0,"cache_write":0,"cost":0.0127}
< {"type":"message_end","role":"assistant","text":""}
< {"type":"tool_call","tool":"shell","call_id":"toolu_01","summary":"go test ./...","input":{"command":"go test ./..."}}
< {"type":"approval_needed","call_id":"toolu_01","tool":"shell","category":"shell","summary":"go test ./...","command":"go test ./..."}
> {"id":"2","type":"approve","call_id":"toolu_01","allow":true}
< {"type":"response","id":"2","command":"approve","ok":true}
< {"type":"approval","call_id":"toolu_01","tool":"shell","allow":true}
< {"type":"tool_result","tool":"shell","call_id":"toolu_01","text":"ok  \tapp\t0.21s\n","is_error":false}
< {"type":"message_end","role":"tool","text":""}
< {"type":"message_start","role":"assistant"}
< {"type":"text_delta","text":"All tests pass."}
< {"type":"usage","input":2502,"output":9,"cache_read":2400,"cache_write":0,"cost":0.0021}
< {"type":"message_end","role":"assistant","text":"All tests pass."}
< {"type":"turn_end"}
  (stdin closed)
< {"type":"session_end","answer":"All tests pass.","exit_code":0,"refused":[],"usage":{"input":4912,"output":47,"cache_read":2400,"cache_write":0,"cost":0.0148}}
```

## Order

Events keep the order of [events.md](events.md). A command's `response` is written when the command is done (or, for `prompt`, accepted), so it can land between the events of a running turn: match it by `id`, not by position. A `prompt`'s response comes before any event of its turn, and `fork`'s new `session_start` before its response.
