<div align="center">
  <img src="./build/appicon.png" alt="Hm" width="160"/>

  <h1>Hm</h1>

  <p><strong>The AI harness &amp; editor for when you pause to think.</strong></p>

  <p><a href="https://usehm.dev">usehm.dev</a></p>
</div>

Hm is a desktop code editor with an AI coding agent built in. It is written in Go and draws its UI with [Yoga](https://github.com/mirzakhany/yoga), a GPU-rendered (WebGPU) UI toolkit, so it starts quickly, stays light, and runs natively on macOS, Linux and Windows without a browser engine. It is a sibling of [Chapar](https://github.com/chapar-rest/chapar) and shares its look and feel.

Everything is configured in plain YAML, with a settings page on top. The agent is bring-your-own-key: Anthropic, OpenAI or any OpenAI-compatible endpoint (OpenRouter, Ollama, LM Studio, ...), and nothing leaves your machine except the calls you configure.

## Why "Hm"?

*Hm* is the sound you make when you are evaluating a solution: the short pause after reading a diff, a stack trace or a suggestion, before you decide whether it is right.

That pause is what Hm is built around. The agent can write code quickly, but the decision about whether it belongs in your codebase stays with you. That is why Hm:

- shows every change in a real editor, with the language server, diagnostics and git diff right next to it;
- has modes that slow the agent down when you want them to: **rubber-duck** plans and pushes back before touching code, and **lazy** changes only what is necessary;
- by default asks before it runs shell commands or deletes files, and always follows your `guard.yaml` rules;
- lets you undo the agent's last turn.

Fast where it can be, and quiet when you need to think.

## Features

### Editor
- Tabs, split editor groups, back/forward navigation and session restore.
- File explorer with file-type icons, git status colors, drag and drop, filter, and collapse all.
- Syntax highlighting with Tree-sitter for Go, TypeScript/JavaScript, Python, Rust, Java, C, C++, C#, Zig, Lua, Ruby, PHP, Protobuf, YAML, TOML, JSON, Bash, Make, Dockerfile, CSS, HTML, Markdown, SQL and common config files.
- Multiple cursors and column selection, find and replace with regex, code folding, soft wrap, indent guides, auto-closing brackets and quotes.
- Auto indent (`none`, `keep` or `advanced`), tabs or spaces, and trimming trailing whitespace on save.
- Per-language settings: tab width, indentation, auto indent, soft wrap, format on save and trimming, set in YAML or from a language picker in Settings.
- Markdown preview.
- Optional Vim keymap.
- Whole-window zoom with `cmd+=`, `cmd+-` and `cmd+0`.
- Auto save on focus change or after a delay.

### Language tools
- Language servers (LSP): diagnostics, hover, completion, go to definition and implementation, find references, rename, document symbols and formatting.
- Problems panel listing every diagnostic.
- Formatters per language, run on save, on demand or after paste, piping the buffer or rewriting the file.
- Missing servers and formatters can be installed with one click (`go install`, `brew`, `npm`, ...).
- Adding a language, server or formatter is a few lines of YAML.

### Search
- Project search with ripgrep (or a built-in engine): regex, replace one or all, results on the left and an editable preview on the right.
- Quick open (`cmd+p`), go to symbol, go to tab and a command palette (`cmd+shift+p`).

### Git
- Git panel from the footer: stage, commit, discard, branches, fetch, pull and push.
- Merge branches; files with conflicts are marked until every one is resolved.
- Remotes over HTTPS with tokens kept in the OS keychain.

### Terminal
- Integrated terminal with multiple sessions, opened from the footer.

### AI agent
- Chat with the agent beside the editor or full screen; ask about a selection with `cmd+alt+l`.
- Sessions in tabs, several working at once: `cmd+t` opens one, `ctrl+tab` / `ctrl+shift+tab` switch, and a spinner or dot marks a tab that is working, waiting for you or was interrupted.
- Full screen, a sidebar replaces the explorer: New chat, Projects, Artifacts, a filterable history of every session (across all your folders, with sub-agents nested under the session that started them) and Agent Settings.
- Projects: a title, description and context that the agent is given before every task in the project's sessions. Pick one in the composer.
- Reads the instruction files other agents use: `AGENTS.md`, or `CLAUDE.md` when there is none, from `~/.config/hm` and from each folder between your home folder and the workspace, outer folders first. A `SYSTEM.md` (`.hm/SYSTEM.md`, else `~/.config/hm/SYSTEM.md`) replaces the base prompt, or adds to it when its first line is `<!-- append -->`. The book icon in the session header and `/context` list the files a session uses. A session reads them once when it starts, so its prompt stays cached.
- Prompt templates: each `.md` file in `~/.config/hm/prompts`, `.hm/prompts`, `.claude/commands` or `.pi/prompts` is a `/name` command in the message box, listed in its completions with its description. `$ARGUMENTS` is what you type after the command, `$1`, `$2` … its words (quotes group them), and a line `@path` inlines a workspace file (guard.yaml rules apply). Front matter can set `description`, `argument-hint`, `mode` and `model` (a provider's name or the model it runs). Claude Code commands work as they are. Built-in commands win over a template of the same name.
- Type while the agent works. Enter steers: the running tool call finishes, the rest of the batch is skipped and the model reads your message. Alt+Enter sends a follow-up that waits for the turn to end. Queued messages show above the message box with their kind, and can be edited or removed before they are sent.
- Sessions are saved after every step. If hm stops mid-task, the session reopens marked as interrupted, with a **Resume** button.
- Replay a session one message at a time, with when each one happened.
- Rate limits, an overloaded API and dropped connections don't end the turn: the call is retried with growing waits (or as long as the API asks), and the chat says so ("Rate limited, retrying in 8s (2/5)"). `Esc` stops the wait. A call is never retried once its answer has started to stream.
- A context meter in the composer shows how much of the model's window the conversation fills ("42k / 200k"), in the warning colour from 80%.
- Long sessions keep working: when the conversation nears 85% of the model's window, or the API says the prompt is too long, older messages are summarised (goal, decisions, files touched, open tasks, unsolved errors, next step) and the model sees the summary plus the latest messages. `/compact [focus]` does it by hand, keeping what you name. A divider in the chat ("Compacted 148k → 12k tokens") opens to show the summary; the saved session and replay keep every message.
- Sub-agents: the `task` tool hands work to an agent in a session of its own, in the foreground or the background, read-only unless allowed to write. Several run at once. Agents message each other with `send_message`, `wait_message` and `list_agents`; a message steers the receiver unless `follow_up` is set, and a background task's report arrives as a follow-up.
- Modes, switched with `/mode`:
  - **lazy** changes only what is necessary;
  - **rubber-duck** plans and pushes back before touching code;
  - **auto** does what is best;
  - **fullstack** builds apps from the ground up.
- Tools: read, write, edit and multi-edit files, grep, list, run shell commands, LSP diagnostics and rename, format, project search, todos, web search and fetch, and asking you questions.
- Extensible with MCP servers, your own command-line tools and skills (also read from `.cursor/skills` and `.claude/skills`).
- Approval per tool and mode, plus a `guard.yaml` of deny, read-only and ask rules. A project's rules can only tighten yours.
- Undo the agent's last turn.
- AI inline completions: ghost text at the caret, `Tab` to accept.
- API keys live in the OS keychain or come from environment variables.

### App
- Themes, including Tokyo Night, following the system light/dark setting if you like.
- Settings page backed by YAML: user config in `~/.config/hm/config.yaml`, project config in `.hm/config.yaml`, applied live on save, with errors shown at their line.
- Keyboard shortcuts in Settings or YAML, with two-chord sequences and VS Code-style `when` conditions.
- In-app updates and notifications.
- `hm .` shell command to open a folder from the terminal.

## Keyboard shortcuts

macOS keys; on Windows and Linux `cmd` is `ctrl`, except where a column says otherwise. Every key can be changed in **Settings → Keyboard Shortcuts** or in YAML.

### Find and replace

| Action | macOS | Windows/Linux |
|---|---|---|
| Find in file | `cmd+f` | `ctrl+f` |
| Replace in file | `cmd+r` | `ctrl+r` |
| Search in project | `cmd+shift+f` | `ctrl+shift+f` |
| Next / previous match | `enter` / `shift+enter` | `enter` / `shift+enter` |
| Replace (in the replace field) / replace all | `enter` / `cmd+enter` | `enter` / `ctrl+enter` |
| Match case, whole word, regex | `cmd+alt+c`, `cmd+alt+w`, `cmd+alt+r` | `alt+c`, `alt+w`, `alt+r` |
| Find in selection, preserve case | `cmd+alt+l`, `cmd+alt+p` | `alt+l`, `alt+p` |
| Close find | `escape` | `escape` |

### Cursor movement and selection

| Action | macOS | Windows/Linux |
|---|---|---|
| Move by word | `alt+left` / `alt+right` | `ctrl+left` / `ctrl+right` |
| Delete by word | `alt+backspace` / `alt+delete` | `ctrl+backspace` / `ctrl+delete` |
| Start / end of line (first press goes to the first non-blank) | `cmd+left` / `cmd+right` | `home` / `end` |
| Start / end of file | `cmd+up` / `cmd+down` | `ctrl+home` / `ctrl+end` |
| Extend the selection | add `shift` to any of these | add `shift` to any of these |
| Extend the selection to a click | `shift+click` | `shift+click` |
| Add a cursor | `alt+click` | `alt+click` |
| Column selection | `alt+shift+drag` | `alt+shift+drag` |
| Add cursor above / below | `cmd+alt+up` / `cmd+alt+down` | `ctrl+alt+up` / `ctrl+alt+down` |
| Add next occurrence | `cmd+d` | `ctrl+d` |
| Select all occurrences | `cmd+shift+l` | `ctrl+shift+l` |
| Back to one cursor | `escape` | `escape` |

### Lines

| Action | macOS | Windows/Linux |
|---|---|---|
| Duplicate lines down / up | `shift+alt+down` / `shift+alt+up` | `shift+alt+down` / `shift+alt+up` |
| Move lines down / up | `alt+down` / `alt+up` | `alt+down` / `alt+up` |
| Delete lines | `cmd+shift+k` | `ctrl+shift+k` |
| Toggle line comment | `cmd+/` | `ctrl+/` |

### Folding

| Action | macOS | Windows/Linux |
|---|---|---|
| Fold / unfold at the cursor | `cmd+alt+[` / `cmd+alt+]` | `ctrl+alt+[` / `ctrl+alt+]` |
| Fold all / unfold all | `cmd+alt+0` / `cmd+alt+shift+0` | `ctrl+alt+0` / `ctrl+alt+shift+0` |
| Fold or unfold a block | click its chevron in the gutter | click its chevron in the gutter |

### App

| Action | macOS |
|---|---|
| Command palette | `cmd+shift+p` |
| Go to file | `cmd+p` |
| Go to symbol in file | `cmd+shift+o` |
| Toggle sidebar | `cmd+b` |
| Toggle agent | `cmd+l` |
| Ask the agent about the selection | `cmd+alt+l` |
| While the agent works: steer / send after the turn | `enter` / `alt+enter` |
| Terminal | `` ctrl+` `` |
| Settings | `cmd+,` |
| Zoom in / out / reset | `cmd+=` / `cmd+-` / `cmd+0` |

## Build and install

Hm needs Go (see `go.mod` for the version).

```sh
make run            # run from source in the current folder
make test           # run the tests
make install_deps   # install the yoga CLI used for packaging
make app            # build Hm.app and a DMG into dist/darwin
make install        # build Hm.app, copy it to /Applications and install `hm`
```

## Configuration

Open **Settings** (`cmd+,`) for a form, or **Edit as YAML** for the file behind it. **View defaults** shows the built-in config with comments for every key. For example:

```yaml
editor:
  theme: tokyo-night
  font_size: 14
  tab_width: 4
  auto_indent: advanced
  zoom: 1

languages:
  yaml:
    editor: { tab_width: 2, insert_spaces: true }
  go:
    formatters:
      - { command: gofmt, args: ["-w", "{file}"], on: [save, manual] }

agent:
  default_provider: anthropic
  providers:
    openrouter: { type: openai, base_url: "https://openrouter.ai/api/v1", api_key: "${OPENROUTER_API_KEY}" }
    ollama: { type: openai, base_url: "http://localhost:11434/v1", model: qwen3, context_window: 32768 } # for models hm doesn't know
  retry: { max_attempts: 5 }                       # retries after a failed model call; 0 = off
  compaction:                                      # summarise older messages near the window's limit
    enabled: true                                  # by itself; /compact works either way
    threshold: 0.85                                # fraction of the window that starts it
    reserve_tokens: 16384                          # room kept for the answer
    keep_tokens: 20000                             # latest history kept as it is
    provider: ""                                   # agent.providers entry that summarises; "" = the session's
    model: ""                                      # e.g. a smaller, cheaper model; "" = the provider's
  subagents: { max_depth: 1, max_concurrent: 4 }   # max_depth: 0 turns the task tool off
  context:                                         # AGENTS.md and friends in the system prompt
    enabled: true
    files: [AGENTS.md, CLAUDE.md]                  # first one found per folder wins
    max_bytes: 65536                               # larger files are skipped with a warning
  prompts:                                         # /name prompt templates; a later folder wins
    paths: [~/.config/hm/prompts, .hm/prompts, .claude/commands, .pi/prompts]

keybindings:   # swap Enter and Alt+Enter while the agent works
  - { key: enter, command: agent.followUp, when: "agentFocus && agentRunning" }
  - { command: -agent.followUp }
  - { key: alt+enter, command: agent.steer, when: agentFocus }

artifacts:
  storage: none   # none | gcs | s3 | git (not wired up yet)
```

## Roadmap

### Known issues
- Vim insert mode inserts a plain newline and a literal tab, so it ignores `auto_indent` and `insert_spaces`.
- On macOS the window controls (traffic lights) keep their native position when the window is zoomed instead of centering in the taller title bar.
- **Reset** in the per-language settings removes your overrides but not the built-in per-language defaults (such as two-space YAML); those can only be overridden.

### Planned
- Artifact storage in a GCS or S3 bucket or a git repository (the settings exist; the Artifacts tab is empty for now).
- A diff option to ignore whitespace-only changes (`ignore_trim_whitespace`).
- Full-color file icons, once Yoga can draw multi-color SVG icons in the tree.
- Prebuilt, signed releases for macOS, Linux and Windows.
