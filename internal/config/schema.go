// Package config loads hm's YAML configuration: built-in defaults, then the
// user file, then the project file, later layers winning. It validates each
// layer with file positions, watches the files, and edits them through
// yaml.Node so comments and key order survive.
package config

import (
	"errors"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the merged configuration.
type Config struct {
	Editor Editor            `yaml:"editor"`
	Keys   map[string]string `yaml:"keys"`
	// Keybindings add shortcuts that apply in a context, VS Code style.
	Keybindings []Keybinding        `yaml:"keybindings"`
	Search      Search              `yaml:"search"`
	Languages   map[string]Language `yaml:"languages"`
	Terminal    Terminal            `yaml:"terminal"`
	Updates     Updates             `yaml:"updates"`
	Agent       Agent               `yaml:"agent"`
	Completion  Completion          `yaml:"completion"`
	Modes       map[string]Mode     `yaml:"modes"`
	Tools       Tools               `yaml:"tools"`
	Artifacts   Artifacts           `yaml:"artifacts"`
}

// Artifacts is where the agent's artifacts are kept.
type Artifacts struct {
	// Storage is none | gcs | s3 | git.
	Storage string `yaml:"storage"`
	// Bucket and Prefix place artifacts in a GCS or S3 bucket.
	Bucket string `yaml:"bucket"`
	Prefix string `yaml:"prefix"`
	// Repo and Branch place them in a git repository.
	Repo   string `yaml:"repo"`
	Branch string `yaml:"branch"`
}

// Keybinding runs Command when Key is pressed while When holds. A Command
// of "-id" removes id's shortcut from keys.
type Keybinding struct {
	Key     string `yaml:"key"`
	Command string `yaml:"command"`
	When    string `yaml:"when"`
}

type Editor struct {
	Theme string `yaml:"theme"`
	// Zoom scales the whole window, 1 being 100%.
	Zoom         float64 `yaml:"zoom"`
	FontSize     int     `yaml:"font_size"`
	FontFamily   string  `yaml:"font_family"`
	TabWidth     int     `yaml:"tab_width"`
	InsertSpaces bool    `yaml:"insert_spaces"`
	SoftWrap     bool    `yaml:"soft_wrap"`
	LineNumbers  bool    `yaml:"line_numbers"`
	// AutoIndent is what Enter carries to the new line: none, keep (the
	// line's indent) or advanced (one more level after an opening bracket).
	AutoIndent string `yaml:"auto_indent"`
	// TrimTrailingWhitespace strips blanks at line ends when saving.
	TrimTrailingWhitespace bool `yaml:"trim_trailing_whitespace"`
	// Keymap is default, or vim for Vim's modal editing.
	Keymap string `yaml:"keymap"`
	// ScrollPastEnd leaves room below the last line so it can scroll to the top.
	ScrollPastEnd bool   `yaml:"scroll_past_end"`
	FormatOnSave  bool   `yaml:"format_on_save"`
	AutoSave      string `yaml:"auto_save"` // off | focus_change | delay
	// AutoSaveDelay is the idle time before a delayed auto save.
	AutoSaveDelay Duration `yaml:"auto_save_delay"`
	ShowHidden    bool     `yaml:"show_hidden"`
}

// Auto indent modes.
const (
	AutoIndentNone     = "none"
	AutoIndentKeep     = "keep"
	AutoIndentAdvanced = "advanced"
)

// EditorOverride is a language's editor settings; a field left out keeps
// the editor section's value.
type EditorOverride struct {
	TabWidth               *int    `yaml:"tab_width"`
	InsertSpaces           *bool   `yaml:"insert_spaces"`
	AutoIndent             *string `yaml:"auto_indent"`
	SoftWrap               *bool   `yaml:"soft_wrap"`
	FormatOnSave           *bool   `yaml:"format_on_save"`
	TrimTrailingWhitespace *bool   `yaml:"trim_trailing_whitespace"`
}

// For returns e with o's set fields applied.
func (e Editor) For(o *EditorOverride) Editor {
	if o == nil {
		return e
	}
	set(&e.TabWidth, o.TabWidth)
	set(&e.InsertSpaces, o.InsertSpaces)
	set(&e.AutoIndent, o.AutoIndent)
	set(&e.SoftWrap, o.SoftWrap)
	set(&e.FormatOnSave, o.FormatOnSave)
	set(&e.TrimTrailingWhitespace, o.TrimTrailingWhitespace)
	return e
}

func set[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// Auto save modes.
const (
	AutoSaveOff         = "off"
	AutoSaveFocusChange = "focus_change"
	AutoSaveDelay       = "delay"
)

type Search struct {
	Engine     string   `yaml:"engine"` // auto | ripgrep | builtin
	Exclude    []string `yaml:"exclude"`
	MaxResults int      `yaml:"max_results"`
}

type Language struct {
	Extensions []string `yaml:"extensions"`
	// Filenames are whole file names, for files without an extension such
	// as Makefile; matched case-insensitively, before the extension.
	Filenames []string `yaml:"filenames"`
	// LanguageID is the LSP language id; it defaults to the language's key.
	LanguageID string `yaml:"language_id"`
	// LanguageIDs overrides it per extension (.tsx: typescriptreact).
	LanguageIDs map[string]string `yaml:"language_ids"`
	// Highlight names the built-in grammar; it defaults to the key.
	Highlight string `yaml:"highlight"`
	// HighlightQueries are .scm files of tree-sitter highlight patterns
	// read after the grammar's built-in query, so their patterns win. Paths
	// are absolute, start with ~, or are relative to the config folder.
	HighlightQueries []string    `yaml:"highlight_queries"`
	LSP              *LSP        `yaml:"lsp"`
	Formatters       []Formatter `yaml:"formatters"`
	// Editor overrides the editor section for this language's files.
	Editor *EditorOverride `yaml:"editor"`
}

type LSP struct {
	Command     string            `yaml:"command"`
	Args        []string          `yaml:"args"`
	RootMarkers []string          `yaml:"root_markers"`
	Env         map[string]string `yaml:"env"`
	Options     map[string]any    `yaml:"initialization_options"`
	// Settings answer the server's workspace/configuration requests.
	Settings map[string]any `yaml:"settings"`
	// Install lists commands that install the server, in order of
	// preference; the first whose tool is on PATH is offered.
	Install []Installer `yaml:"install"`
	// Disabled turns the server off without deleting its settings.
	Disabled bool `yaml:"disabled"`
}

type Formatter struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	Stdin   bool     `yaml:"stdin"`
	On      []string `yaml:"on"` // save | manual | paste
	// Install, as for language servers.
	Install []Installer `yaml:"install"`
}

// Installer is a command that installs a tool, such as npm install -g x.
type Installer struct {
	Tool string   `yaml:"tool"`
	Args []string `yaml:"args"`
}

// Formatter triggers.
var FormatTriggers = []string{"save", "manual", "paste"}

type Terminal struct {
	Shell    string `yaml:"shell"`
	FontSize int    `yaml:"font_size"`
}

type Updates struct {
	Channel string `yaml:"channel"` // stable | prerelease | off
	// Repo is the GitHub repository releases come from, as owner/name.
	Repo string `yaml:"repo"`
}

type Agent struct {
	DefaultProvider string `yaml:"default_provider"`
	DefaultMode     string `yaml:"default_mode"`
	// Layout is where the agent shows: fullscreen swaps it with the editor,
	// panel docks it beside the editor.
	Layout    string              `yaml:"layout"`
	Providers map[string]Provider `yaml:"providers"`
	// FavoriteModels are the models agent.cycleModel steps through and the
	// model picker lists first, each as provider/model: a name under
	// providers, then the model id.
	FavoriteModels []string        `yaml:"favorite_models"`
	Subagents      Subagents       `yaml:"subagents"`
	Context        AgentContext    `yaml:"context"`
	Prompts        AgentPrompts    `yaml:"prompts"`
	Retry          AgentRetry      `yaml:"retry"`
	Compaction     AgentCompaction `yaml:"compaction"`
	// BranchSummary describes a branch the user leaves, for /tree.
	BranchSummary AgentBranchSummary `yaml:"branch_summary"`
	// Share is where /share puts a session.
	Share AgentShare `yaml:"share"`
}

// AgentShare configures /share, which uploads a session's HTML export as
// a secret GitHub gist.
type AgentShare struct {
	// ViewerURL is the link copied after sharing, with {id}, {user} and
	// {file} filled in from the gist; empty copies the gist's own URL.
	ViewerURL string `yaml:"viewer_url"`
}

// AgentBranchSummary asks the model, when the user goes back to an earlier
// point of a session, for a few lines on what the branch left behind did.
// The compaction model writes them.
type AgentBranchSummary struct {
	Enabled bool `yaml:"enabled"`
	// MinMessages is how long the branch left must be to get a summary.
	MinMessages int `yaml:"min_messages"`
}

// AgentCompaction summarises a session's older messages when it nears the
// model's context window. The session file keeps every message.
type AgentCompaction struct {
	// Enabled compacts by itself; /compact works either way.
	Enabled bool `yaml:"enabled"`
	// Threshold is the fraction of the window that starts a compaction.
	Threshold float64 `yaml:"threshold"`
	// ReserveTokens are kept free for the model's answer.
	ReserveTokens int `yaml:"reserve_tokens"`
	// KeepTokens of the latest messages are kept as they are.
	KeepTokens int `yaml:"keep_tokens"`
	// Provider names an entry under agent.providers to write summaries;
	// empty uses the session's.
	Provider string `yaml:"provider"`
	// Model writes summaries; empty uses the provider's model.
	Model string `yaml:"model"`
}

// AgentRetry retries model calls that fail for a passing reason, such as
// a rate limit or an overloaded API.
type AgentRetry struct {
	// MaxAttempts is how many retries follow the first failure; 0 turns
	// retrying off.
	MaxAttempts int `yaml:"max_attempts"`
}

// AgentPrompts finds the prompt templates typed as /name in the message box.
type AgentPrompts struct {
	// Paths are folders of .md templates; a later folder wins on a name.
	Paths []string `yaml:"paths"`
}

// AgentContext picks the instruction files, such as AGENTS.md, that go
// into the agent's system prompt.
type AgentContext struct {
	// Enabled off skips the files and SYSTEM.md.
	Enabled bool `yaml:"enabled"`
	// Files are the names looked for in each folder; the first found wins.
	Files []string `yaml:"files"`
	// MaxBytes skips larger files.
	MaxBytes int `yaml:"max_bytes"`
}

// Subagents bounds the agents an agent may start with the task tool.
type Subagents struct {
	// MaxDepth is how deep sub-agents may nest; 0 turns the task tool off.
	MaxDepth int `yaml:"max_depth"`
	// MaxConcurrent is how many sub-agents of one agent may run at once.
	MaxConcurrent int `yaml:"max_concurrent"`
}

type Provider struct {
	Type      string `yaml:"type"` // anthropic | openai
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
	// Effort sets how hard the model thinks: off | low | medium | high |
	// xhigh | max; /think changes it for a session.
	Effort string `yaml:"effort"`
	// ContextWindow is the model's window in tokens, for models hm does
	// not know; 0 uses hm's table.
	ContextWindow int `yaml:"context_window"`
}

// Completion configures AI inline completions in the editor.
type Completion struct {
	Enabled bool `yaml:"enabled"`
	// Provider names an entry under agent.providers, whose endpoint and key
	// it uses; empty means agent.default_provider.
	Provider string `yaml:"provider"`
	// Model should be small and fast; empty turns completions off.
	Model     string   `yaml:"model"`
	Delay     Duration `yaml:"delay"` // pause in typing before asking
	MaxTokens int      `yaml:"max_tokens"`
}

type Mode struct {
	Prompt   string            `yaml:"prompt"`
	Tools    StringList        `yaml:"tools"`
	Skills   []string          `yaml:"skills"`
	Approval map[string]string `yaml:"approval"` // tool -> auto | ask | deny
}

type Tools struct {
	MCP      map[string]MCPServer   `yaml:"mcp"`
	Commands map[string]CommandTool `yaml:"commands"`
	Skills   Skills                 `yaml:"skills"`
	Web      Web                    `yaml:"web"`
}

type MCPServer struct {
	Transport string            `yaml:"transport"` // stdio | http
	Command   string            `yaml:"command"`
	Args      []string          `yaml:"args"`
	Env       map[string]string `yaml:"env"`
	URL       string            `yaml:"url"`
	Headers   map[string]string `yaml:"headers"`
	// Approval applies to all of the server's tools: auto | ask | deny.
	Approval string `yaml:"approval"`
}

type CommandTool struct {
	Description string           `yaml:"description"`
	Command     string           `yaml:"command"`
	Timeout     Duration         `yaml:"timeout"`
	Params      map[string]Param `yaml:"params"`
	Approval    string           `yaml:"approval"`
}

type Param struct {
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
	Default     any    `yaml:"default"`
	Required    bool   `yaml:"required"`
}

type Skills struct {
	Paths []string `yaml:"paths"`
}

type Web struct {
	Search WebSearch `yaml:"search"`
}

type WebSearch struct {
	Provider string `yaml:"provider"` // duckduckgo | searxng | brave | tavily | exa
	URL      string `yaml:"url"`
	APIKey   string `yaml:"api_key"`
}

// Duration is a time.Duration written as "300s", "1m".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// StringList accepts a list, or the single word "all" meaning every tool.
type StringList []string

func (l *StringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*l = StringList{n.Value}
		return nil
	}
	var many []string
	if err := n.Decode(&many); err != nil {
		return errors.New("expected a list of tool names or \"all\"")
	}
	*l = many
	return nil
}
