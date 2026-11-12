package agentcmd

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/usehm/hm/pkg/hm/agent"
)

// eventsVersion is the "v" of every line; docs/events.md describes it.
// New types and fields do not change it; consumers ignore what they do
// not know.
const eventsVersion = 1

// clock stamps the lines; tests stop it.
var clock = time.Now

// envelope starts every line.
type envelope struct {
	V       int    `json:"v"`
	Type    string `json:"type"`
	Session string `json:"session"`
	Time    string `json:"time"`
}

func (e *envelope) head() *envelope { return e }

// line is one of the types below.
type line interface{ head() *envelope }

type (
	sessionStart struct {
		envelope
		Root     string `json:"root"`
		Mode     string `json:"mode"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Resumed  bool   `json:"resumed"`
		Prompt   string `json:"prompt"`
	}
	roleLine struct {
		envelope
		Role string `json:"role"`
	}
	textLine struct {
		envelope
		Text string `json:"text"`
	}
	toolStart struct {
		envelope
		Tool string `json:"tool"`
	}
	toolCall struct {
		envelope
		Tool    string          `json:"tool"`
		CallID  string          `json:"call_id"`
		Summary string          `json:"summary"`
		Input   json.RawMessage `json:"input,omitempty"`
	}
	approvalNeeded struct {
		envelope
		CallID   string   `json:"call_id"`
		Tool     string   `json:"tool"`
		Category string   `json:"category"`
		Summary  string   `json:"summary"`
		Command  string   `json:"command,omitempty"`
		Paths    []string `json:"paths,omitempty"`
		Reason   string   `json:"reason,omitempty"`
	}
	approvalLine struct {
		envelope
		CallID string `json:"call_id"`
		Tool   string `json:"tool"`
		Allow  bool   `json:"allow"`
		Note   string `json:"note,omitempty"`
	}
	questionLine struct {
		envelope
		CallID string `json:"call_id"`
		Text   string `json:"text"`
	}
	blockedLine struct {
		envelope
		Tool   string `json:"tool"`
		Reason string `json:"reason"`
	}
	toolResult struct {
		envelope
		Tool    string      `json:"tool"`
		CallID  string      `json:"call_id"`
		Text    string      `json:"text"`
		IsError bool        `json:"is_error"`
		Images  []imageLine `json:"images,omitempty"`
	}
	messageEnd struct {
		envelope
		Role   string      `json:"role"`
		Text   string      `json:"text"`
		Images []imageLine `json:"images,omitempty"`
	}
	// imageLine is a picture, its data in base64.
	imageLine struct {
		MediaType string `json:"media_type"`
		Data      []byte `json:"data"`
		Width     int    `json:"width,omitempty"`
		Height    int    `json:"height,omitempty"`
	}
	usageFields struct {
		Input      int64   `json:"input"`
		Output     int64   `json:"output"`
		CacheRead  int64   `json:"cache_read"`
		CacheWrite int64   `json:"cache_write"`
		Cost       float64 `json:"cost"`
	}
	usageLine struct {
		envelope
		usageFields
	}
	errorLine struct {
		envelope
		Error string `json:"error,omitempty"`
	}
	bare       struct{ envelope }
	sessionEnd struct {
		envelope
		Answer   string      `json:"answer"`
		ExitCode int         `json:"exit_code"`
		Refused  []string    `json:"refused"`
		Usage    usageFields `json:"usage"`
	}
)

// jsonWriter writes events as JSON lines, one at a time.
type jsonWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
	// streaming holds the sessions whose assistant message has started
	// but not joined the session yet, for message_start.
	streaming map[string]bool
}

func newJSONWriter(w io.Writer) *jsonWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &jsonWriter{enc: enc, streaming: map[string]bool{}}
}

// write stamps l as typ of session and writes it.
func (w *jsonWriter) write(typ, session string, l line) {
	h := l.head()
	h.V, h.Type, h.Session = eventsVersion, typ, session
	h.Time = clock().UTC().Format(time.RFC3339Nano)
	_ = w.enc.Encode(l)
}

func (w *jsonWriter) sessionStart(info agent.SessionInfo, prompt string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.write("session_start", info.ID, &sessionStart{Root: info.Root, Mode: info.Mode, Provider: info.Provider,
		Model: info.Model, Resumed: info.Resumed, Prompt: prompt})
}

func (w *jsonWriter) sessionEnd(info agent.SessionInfo, answer string, code int, refused []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if refused == nil {
		refused = []string{}
	}
	w.write("session_end", info.ID, &sessionEnd{Answer: answer, ExitCode: code, Refused: refused, Usage: usageOf(info.Usage)})
}

func imageLines(ims []agent.Image) []imageLine {
	var out []imageLine
	for _, im := range ims {
		out = append(out, imageLine{MediaType: im.MediaType, Data: im.Data, Width: im.Width, Height: im.Height})
	}
	return out
}

func usageOf(u agent.Usage) usageFields {
	return usageFields{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Cost: u.Cost}
}

// event writes one of the Runner's events.
func (w *jsonWriter) event(e agent.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := e.Session
	switch e.Kind {
	case agent.EventText, agent.EventThinking, agent.EventToolStart:
		if !w.streaming[s] {
			w.streaming[s] = true
			w.write("message_start", s, &roleLine{Role: "assistant"})
		}
	}
	switch e.Kind {
	case agent.EventText, agent.EventThinking, agent.EventRetry, agent.EventError, agent.EventWarning:
		w.write(string(e.Kind), s, &textLine{Text: e.Text})
	case agent.EventToolStart:
		w.write("tool_start", s, &toolStart{Tool: e.Tool})
	case agent.EventToolCall:
		w.write("tool_call", s, &toolCall{Tool: e.Tool, CallID: e.CallID, Summary: e.Summary, Input: rawInput(e.Input)})
	case agent.EventApprovalNeeded:
		r := e.Request
		w.write("approval_needed", s, &approvalNeeded{CallID: r.CallID, Tool: r.Tool, Category: r.Category,
			Summary: r.Summary, Command: r.Command, Paths: r.Paths, Reason: r.Reason})
	case agent.EventApproval:
		w.write("approval", s, &approvalLine{CallID: e.CallID, Tool: e.Tool, Allow: e.Decision.Allow, Note: e.Decision.Note})
	case agent.EventQuestion:
		w.write("question", s, &questionLine{CallID: e.CallID, Text: e.Text})
	case agent.EventBlocked:
		w.write("blocked", s, &blockedLine{Tool: e.Tool, Reason: e.Text})
	case agent.EventToolResult:
		w.write("tool_result", s, &toolResult{Tool: e.Tool, CallID: e.CallID, Text: e.Result.Text, IsError: e.Result.IsError,
			Images: imageLines(e.Result.Images)})
	case agent.EventMessage:
		delete(w.streaming, s)
		if m := e.Message; m != nil {
			w.write("message_end", s, &messageEnd{Role: m.Role, Text: m.Text, Images: imageLines(m.Images)})
		}
	case agent.EventUsage:
		w.write("usage", s, &usageLine{usageFields: usageOf(*e.Usage)})
	case agent.EventCompacting:
		w.write("compaction_start", s, &bare{})
	case agent.EventCompacted:
		w.write("compaction_end", s, &textLine{Text: e.Text})
	case agent.EventSubagentEnd, agent.EventTurnEnd:
		delete(w.streaming, s)
		w.write(string(e.Kind), s, &errorLine{Error: e.Text})
	}
}

// rawInput is a tool call's input as JSON; input that is not valid JSON
// goes as a string, so the line still parses.
func rawInput(in json.RawMessage) json.RawMessage {
	if len(in) == 0 || json.Valid(in) {
		return in
	}
	quoted, _ := json.Marshal(string(in))
	return quoted
}
