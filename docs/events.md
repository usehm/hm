# hm agent JSON events, version 1

`hm agent -p --output json "prompt"` writes what the agent does to standard output, one JSON object per line. Nothing else goes there: warnings and errors for people go to standard error. `hm agent --rpc` writes the same events, with a `response` to each command; see [rpc.md](rpc.md).

```sh
hm agent -p --output json "list the packages" | jq -c 'select(.type == "tool_call")'
hm agent -p --output json "list the packages" | jq -r 'select(.type == "session_end").answer'
```

## Every line

```json
{"v":1,"type":"text_delta","session":"k3j9x2","time":"2026-11-06T10:00:00.123Z","text":"There are"}
```

| field | |
|---|---|
| `v` | The format version, `1`. |
| `type` | What the line reports; one of the types below. |
| `session` | The session it happened in: the one you started, or one of its sub-agents'. |
| `time` | When it was written, RFC 3339 in UTC. |

New types and new fields can appear without a new `v`. Ignore what you do not know. `v` changes only when a field changes meaning or goes away.

## Order

- `session_start` is the first line and `session_end` the last, whatever happened in between. A stream without `session_end` was cut off.
- A model reply is `message_start`, then `thinking_delta`, `text_delta` and `tool_start` lines as it streams, then `usage`, then `message_end` once it joined the session.
- A tool call is `tool_call`, then `approval_needed` and `approval` when it needs approval, or `blocked` when guard.yaml refuses it, then `tool_result`. The results of one message's calls are one `message_end` with role `tool`.
- Each turn ends with `turn_end`. One `hm agent` run can hold several turns: queued messages and background sub-agents' reports start more.
- Sub-agent lines carry the sub-agent's `session` and can interleave with the parent's. Each sub-agent's turn ends with `subagent_end`.

## Types

### `session_start`

| field | |
|---|---|
| `root` | The workspace folder. |
| `mode`, `provider`, `model` | What runs. `model` is empty for the provider's default. |
| `resumed` | Whether the session was saved before (`--session`, `--continue`). |
| `prompt` | The prompt, with piped input included. |

### `message_start`

`role`: `"assistant"`. A reply has started streaming.

### `text_delta`, `thinking_delta`

`text`: more of the answer, or of the model's reasoning summary.

### `tool_start`

`tool`: the model began a call; its input is still streaming.

### `tool_call`

| field | |
|---|---|
| `tool`, `call_id` | The call. `call_id` ties it to its approval and result. |
| `summary` | What it will do: the command, or what changes. |
| `input` | The call's input as the model wrote it, as JSON. Input that is not valid JSON is a string. |

### `approval_needed`

The mode asks about this call. `call_id`, `tool`, `category` (`read`, `edit`, `delete`, `shell`, `network`, `command`, …), `summary`, and when they apply `command`, `paths` and `reason`.

### `approval`

`call_id`, `tool`, `allow` (bool), and `note`, which the model reads when the call is refused.

### `question`

`call_id`, `text`: the agent asks you something (`ask_user`). Over RPC, send `answer`; with `-p` nobody can answer, and the agent is told so.

### `blocked`

`tool`, `reason`: guard.yaml refused the call. No approval was asked.

### `tool_result`

`tool`, `call_id`, `text` (what the model reads), `is_error` (bool).

### `message_end`

A message joined the session. `role` is `user` (your message), `assistant` (a reply) or `tool` (tool results), and `text` its text; empty for a reply that only made tool calls.

### `usage`

One model call's tokens: `input`, `output`, `cache_read`, `cache_write`, and `cost` in USD (0 when unknown).

### `retry`, `error`

`text`: a model call failed and is retried soon, or something went wrong (the turn may go on).

### `compaction_start`, `compaction_end`

Older messages are being summarised to fit the model's window. `compaction_end`'s `text` says what it saved; empty when it failed.

### `subagent_end`, `turn_end`

`error`: why the turn ended early; absent when it finished.

### `session_end`

| field | |
|---|---|
| `answer` | The final answer; empty when the run failed. |
| `exit_code` | The process's exit code: `0` done, `1` the agent failed, `3` a call was refused for want of approval, `130` interrupted. |
| `refused` | The tools refused for want of approval, in order; `[]` when none. |
| `usage` | The session's totals, as in `usage`. |

Bad usage (exit code `2`) is reported on standard error before any session opens, so it writes no lines.

### `response`

RPC only: the answer to a command, with its `id`, `command`, `ok`, `error` and `data`. See [rpc.md](rpc.md).
