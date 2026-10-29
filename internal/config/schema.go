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
}

// Keybinding runs Command when Key is pressed while When holds. A Command
// of "-id" removes id's shortcut from keys.
type Keybinding struct {
	Key     string `yaml:"key"`
	Command string `yaml:"command"`
	When    string `yaml:"when"`
}

type Editor struct {
	Theme        string `yaml:"theme"`
	FontSize     int    `yaml:"font_size"`
	FontFamily   string `yaml:"font_family"`
	TabWidth     int    `yaml:"tab_width"`
	InsertSpaces bool   `yaml:"insert_spaces"`
	SoftWrap     bool   `yaml:"soft_wrap"`
	LineNumbers  bool   `yaml:"line_numbers"`
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
}

type Provider struct {
	Type      string `yaml:"type"` // anthropic | openai
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
	// Effort sets how hard the model thinks: low | medium | high | xhigh | max.
	Effort string `yaml:"effort"`
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
