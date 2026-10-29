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
- Multiple cursors, find and replace with regex, soft wrap, indent guides, auto-closing brackets and quotes.
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
- Chat with the agent beside the editor or full screen; ask about a selection with `cmd+shift+l`.
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
```

## Roadmap

### Known issues
- Vim insert mode inserts a plain newline and a literal tab, so it ignores `auto_indent` and `insert_spaces`.
- On macOS the window controls (traffic lights) keep their native position when the window is zoomed instead of centering in the taller title bar.
- **Reset** in the per-language settings removes your overrides but not the built-in per-language defaults (such as two-space YAML); those can only be overridden.

### Planned
- A diff option to ignore whitespace-only changes (`ignore_trim_whitespace`).
- Full-color file icons, once Yoga can draw multi-color SVG icons in the tree.
- Prebuilt, signed releases for macOS, Linux and Windows.
