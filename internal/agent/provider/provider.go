// Package provider talks to language model APIs: Anthropic's Messages API
// through its SDK, Google's Gemini (on the Gemini API or Vertex AI), and
// any OpenAI-compatible chat completions endpoint (OpenAI, OpenRouter,
// Ollama, LM Studio, Groq, DeepSeek, ...).
//
// Conversations are kept in a neutral form. An assistant turn also keeps
// the provider's own copy of the message, so it can be sent back unchanged
// (thinking blocks must be), and the history stays append-only.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Role is who wrote a message.
type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// BlockType tells content blocks apart.
type BlockType string

const (
	Text       BlockType = "text"
	Thinking   BlockType = "thinking"
	ToolUse    BlockType = "tool_use"
	ToolResult BlockType = "tool_result"
)

// Block is one piece of a message.
type Block struct {
	Type BlockType `json:"type"`
	Text string    `json:"text,omitempty"`
	// Tool use and result.
	ToolID   string          `json:"tool_id,omitempty"`
	ToolName string          `json:"tool_name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	IsError  bool            `json:"is_error,omitempty"`
}

// Message is one turn.
type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"blocks"`
	// Native is the provider's own encoding of an assistant message, sent
	// back as is when NativeFrom names the provider and model of the next
	// request; otherwise Blocks are converted and thinking is dropped.
	Native     json.RawMessage `json:"native,omitempty"`
	NativeFrom string          `json:"native_from,omitempty"`
}

// TextOf joins a message's text blocks.
func (m Message) TextOf() string {
	var b strings.Builder
	for _, bl := range m.Blocks {
		if bl.Type == Text {
			b.WriteString(bl.Text)
		}
	}
	return b.String()
}

// ToolUses returns the tool calls in a message.
func (m Message) ToolUses() []Block {
	var out []Block
	for _, b := range m.Blocks {
		if b.Type == ToolUse {
			out = append(out, b)
		}
	}
	return out
}

// Tool describes a tool the model may call.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema of the input object
}

// Request is one model call.
type Request struct {
	Model     string
	System    string // stable instructions; cached where the API supports it
	Messages  []Message
	Tools     []Tool
	MaxTokens int
	Effort    string // one of Efforts; "" keeps the model default
}

// Efforts are the effort levels a request may ask for, from none to the
// most: off turns thinking off where the API can.
var Efforts = []string{"off", "low", "medium", "high", "xhigh", "max"}

// StopReason is why the model stopped.
type StopReason string

const (
	StopEnd       StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
	StopOther     StopReason = "other"
)

// Usage counts tokens, and the cost when the API reports it.
type Usage struct {
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Cost       float64 `json:"cost"` // USD; 0 when unknown
}

// Add sums usages.
func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Cost += o.Cost
}

// Response is a finished model call.
type Response struct {
	Message    Message
	StopReason StopReason
	// StopDetail explains a refusal, when the API says why.
	StopDetail string
	Usage      Usage
	// Model is the model the API says answered, when it says; a fallback
	// may answer for the one asked for.
	Model string
}

// EventKind tags streaming events.
type EventKind int

const (
	EventText      EventKind = iota // Text holds new answer text
	EventThinking                   // Text holds new reasoning text
	EventToolStart                  // ToolName is being called
	EventToolInput                  // Text holds more of the tool input JSON
	EventRetry                      // Text says the call failed and when it is retried; not output
)

// Event is a piece of a streaming response.
type Event struct {
	Kind     EventKind
	Text     string
	ToolName string
}

// Provider streams model calls.
type Provider interface {
	// Name identifies the provider and model for Message.NativeFrom.
	Name(model string) string
	// Stream runs req, calling onEvent as output arrives, and returns the
	// whole response. It stops early when ctx ends.
	Stream(ctx context.Context, req Request, onEvent func(Event)) (Response, error)
	// Models lists the model ids the API offers.
	Models(ctx context.Context) ([]string, error)
}

// Config builds a provider.
type Config struct {
	Type    string // anthropic | openai | google | one of Presets
	BaseURL string
	APIKey  string
	// Project and Location pick Vertex AI for google.
	Project, Location string
}

// New returns the provider for cfg.
func New(cfg Config) (Provider, error) {
	switch cfg.Type {
	case "anthropic":
		return NewAnthropic(cfg), nil
	case "google":
		return NewGoogle(cfg), nil
	}
	if OpenAICompatible(cfg.Type) {
		cfg.BaseURL = BaseURL(cfg.Type, cfg.BaseURL)
		return NewOpenAI(cfg), nil
	}
	return nil, errors.New("unknown provider type " + cfg.Type)
}

// ErrNoKey is returned when a provider that needs a key has none.
var ErrNoKey = errors.New("no API key; add one in Settings → Agent")

// toolID makes a tool call id that another provider wrote one that APIs
// taking only letters, digits, _ and - accept, the same way for the call
// and its result, so they still pair.
func toolID(id string) string {
	bad := func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}
	if id != "" && strings.IndexFunc(id, bad) < 0 {
		return id
	}
	return "t_" + strings.Map(func(r rune) rune {
		if bad(r) {
			return '_'
		}
		return r
	}, id)
}
