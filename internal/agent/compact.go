package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/agent/tools"
)

// Compaction keeps long sessions inside the model's window: older messages
// are summarised, and the model sees the summary followed by the recent
// messages. The session file keeps every message; only what is sent
// changes.

// ErrNothingToCompact is returned when no older messages can be folded
// into a summary.
var ErrNothingToCompact = errors.New("nothing to compact yet")

// Limits on what the summariser reads, in characters.
const (
	maxResultChars     = 2000
	maxInputChars      = 500
	maxTranscriptChars = 400_000
	summaryMaxTokens   = 8192
)

// SummaryRequest is what a Compactor summarises.
type SummaryRequest struct {
	// Previous is the summary of an earlier compaction, which the new one
	// replaces; empty for the first.
	Previous string
	Messages []provider.Message
	Todos    []tools.Todo
	// Focus is what the user asked the summary to keep, from /compact.
	Focus string
}

// SummaryReply is a Compactor's answer.
type SummaryReply struct {
	Text  string
	Model string
	Usage provider.Usage
}

// Compactor writes the summary that stands in for older messages. The
// default asks a model; extensions may replace it.
type Compactor interface {
	Summarize(ctx context.Context, in SummaryRequest) (SummaryReply, error)
}

// CompactConfig sets how an agent compacts its session.
type CompactConfig struct {
	// Auto compacts before a request that would fill the window past
	// Threshold, and once after the API says the prompt is too long.
	// /compact works either way.
	Auto      bool
	Threshold float64 // fraction of the window
	Reserve   int     // tokens kept free for the answer
	// KeepTokens of the latest messages are kept as they are, at most a
	// quarter of the window.
	KeepTokens int
	Compactor  Compactor
}

// ModelCompactor summarises with a language model.
type ModelCompactor struct {
	Provider provider.Provider
	Model    string
}

const summarySystem = `You summarise a conversation between a user and a coding agent, so the
agent can continue the work from your summary and the latest messages alone.
The earlier messages will be gone, so keep everything the agent still needs
and nothing it does not. Be specific: exact file paths, names, commands and
error messages. Write in plain Markdown with these sections, leaving out any
that would be empty:

## Goal
What the user wants, in their words where it matters.
## Constraints and preferences
Requirements and rules the user set.
## Decisions
What was decided and why, including approaches tried and dropped.
## Files
Files read and files changed, with paths and what changed.
## Tasks
Done, in progress and still open.
## Unsolved errors
Failures not yet fixed, with their messages.
## Next step
What the agent was about to do.`

// Summarize asks the model for the summary.
func (m ModelCompactor) Summarize(ctx context.Context, in SummaryRequest) (SummaryReply, error) {
	resp, err := m.Provider.Stream(ctx, provider.Request{
		Model:     m.Model,
		System:    summarySystem,
		Messages:  []provider.Message{{Role: provider.User, Blocks: []provider.Block{{Type: provider.Text, Text: summaryPrompt(in)}}}},
		MaxTokens: summaryMaxTokens,
	}, nil)
	if err != nil {
		return SummaryReply{}, err
	}
	text := strings.TrimSpace(resp.Message.TextOf())
	if text == "" {
		return SummaryReply{}, errors.New("the model wrote an empty summary")
	}
	return SummaryReply{Text: text, Model: m.Model, Usage: resp.Usage}, nil
}

// summaryPrompt is the user message asking for the summary.
func summaryPrompt(in SummaryRequest) string {
	var b strings.Builder
	if in.Previous != "" {
		b.WriteString("Summary of the conversation before these messages:\n<previous-summary>\n")
		b.WriteString(in.Previous)
		b.WriteString("\n</previous-summary>\n\n")
	}
	b.WriteString("The messages to summarise:\n<conversation>\n")
	b.WriteString(transcript(in.Messages))
	b.WriteString("</conversation>\n")
	if len(in.Todos) > 0 {
		b.WriteString("\nThe agent's task list now:\n")
		for _, t := range in.Todos {
			fmt.Fprintf(&b, "- [%s] %s\n", t.Status, t.Text)
		}
	}
	b.WriteString("\nWrite the summary")
	if in.Previous != "" {
		b.WriteString(", folding in the previous summary")
	}
	b.WriteString(".")
	if f := strings.TrimSpace(in.Focus); f != "" {
		b.WriteString(" The user asks you to focus on: " + f)
	}
	return b.String()
}

// transcript writes messages as plain text: sending them as they are could
// overflow the window again, and thinking does not move between models.
// Long tool input and results are cut, and when the whole is still too
// long its middle is left out.
func transcript(msgs []provider.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, bl := range m.Blocks {
			switch bl.Type {
			case provider.Text:
				if t := strings.TrimSpace(bl.Text); t != "" {
					fmt.Fprintf(&b, "[%s]\n%s\n\n", m.Role, t)
				}
			case provider.ToolUse:
				fmt.Fprintf(&b, "[assistant called %s]\n%s\n\n", bl.ToolName, cut(string(bl.Input), maxInputChars))
			case provider.ToolResult:
				label := "result"
				if bl.IsError {
					label = "error"
				}
				fmt.Fprintf(&b, "[%s %s]\n%s\n\n", bl.ToolName, label, cut(strings.TrimSpace(bl.Text), maxResultChars))
			}
		}
	}
	s := b.String()
	if len(s) > maxTranscriptChars {
		head := maxTranscriptChars / 4
		tail := maxTranscriptChars - head
		s = s[:head] + fmt.Sprintf("\n[... %d characters left out ...]\n\n", len(s)-head-tail) + s[len(s)-tail:]
	}
	return s
}

// cut shortens s to about n bytes.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… [%d more characters]", len(s)-n)
}

// summaryIntro leads the message that carries the summary.
const summaryIntro = "Earlier parts of this conversation were summarised to save context. The summary:\n\n"

// withSummary puts the summary in front of the kept messages: in the
// first kept message when it is the user's, so two user messages never
// follow each other, and in a message of its own otherwise.
func withSummary(summary string, kept []provider.Message) []provider.Message {
	head := provider.Block{Type: provider.Text, Text: summaryIntro + "<summary>\n" + summary + "\n</summary>"}
	out := make([]provider.Message, 0, len(kept)+1)
	if len(kept) > 0 && kept[0].Role == provider.User {
		m := kept[0]
		m.Blocks = append([]provider.Block{head}, m.Blocks...)
		out = append(out, m)
		return append(out, kept[1:]...)
	}
	out = append(out, provider.Message{Role: provider.User, Blocks: []provider.Block{head}})
	return append(out, kept...)
}

// canCut reports whether the kept messages may start at m: an assistant
// message, or a user message holding no tool results, whose calls would
// be left behind in the summary.
func canCut(m provider.Message) bool {
	if m.Role == provider.Assistant {
		return true
	}
	for _, b := range m.Blocks {
		if b.Type == provider.ToolResult {
			return false
		}
	}
	return true
}

// cutPoint picks where the kept messages start: far enough back to keep
// about keep tokens, after from (the previous compaction's), and never
// between a tool call and its result. It returns -1 when nothing can be
// folded in.
func cutPoint(msgs []provider.Message, from, keep int) int {
	i := len(msgs)
	for sum := int64(0); i > from && sum < int64(keep); {
		i--
		sum += estimate(msgs[i])
	}
	for j := i; j > from; j-- {
		if canCut(msgs[j]) {
			return j
		}
	}
	// Even the newest messages hold more than keep: keep fewer.
	for j := max(i, from) + 1; j < len(msgs); j++ {
		if canCut(msgs[j]) {
			return j
		}
	}
	return -1
}

// estimate guesses a message's tokens at four characters each.
func estimate(m provider.Message) int64 {
	n := 4 // the message's own framing
	for _, b := range m.Blocks {
		n += 4 + len(b.Text) + len(b.Input)
	}
	return int64(n / 4)
}

func estimateAll(msgs []provider.Message) int64 {
	var n int64
	for _, m := range msgs {
		n += estimate(m)
	}
	return n
}

// Compact folds s's older messages into a summary written by c, keeping
// about keep tokens of the latest ones. focus is added to the request.
func Compact(ctx context.Context, s *Session, c Compactor, keep int, focus string) (Compaction, error) {
	s.mu.Lock()
	msgs := append([]provider.Message(nil), s.Messages...)
	var prev Compaction
	if n := len(s.Compactions); n > 0 {
		prev = s.Compactions[n-1]
	}
	todos := append([]tools.Todo(nil), s.Todos...)
	before := s.Context
	s.mu.Unlock()

	from := min(prev.At, len(msgs))
	at := cutPoint(msgs, from, keep)
	if at < 0 {
		return Compaction{}, ErrNothingToCompact
	}
	sum, err := c.Summarize(ctx, SummaryRequest{Previous: prev.Summary, Messages: msgs[from:at], Todos: todos, Focus: focus})
	if err != nil {
		return Compaction{}, err
	}
	s.addCost(sum.Usage)

	// The context shrinks by what was folded in and grows by the summary;
	// the system prompt and tools stay. The next call measures it.
	if before == 0 {
		before = estimateAll(msgs[from:])
		if prev.Summary != "" {
			before += int64(len(prev.Summary) / 4)
		}
	}
	kept := estimateAll(withSummary(sum.Text, msgs[at:]))
	dropped := estimateAll(msgs[from:at]) + int64(len(prev.Summary)/4)
	after := max(before-dropped+int64(len(sum.Text)/4), kept)
	comp := Compaction{At: at, Summary: sum.Text, TokensBefore: before, TokensAfter: after, Model: sum.Model, Time: now()}
	s.addCompaction(comp)
	return comp, nil
}

// Compact summarises s's older messages now, as /compact does.
func (a *Agent) Compact(ctx context.Context, s *Session, focus string) error {
	cc := a.Compaction
	if cc == nil || cc.Compactor == nil {
		return errors.New("compaction is not set up")
	}
	// On a small window, keeping KeepTokens could leave nothing to fold.
	keep := cc.KeepTokens
	if a.ContextWindow > 0 {
		keep = min(keep, a.ContextWindow/4)
	}
	a.emit(Event{Kind: EvCompacting})
	c, err := Compact(ctx, s, cc.Compactor, keep, focus)
	if err != nil {
		a.emit(Event{Kind: EvCompacted})
		return err
	}
	a.emit(Event{Kind: EvCompacted, Text: fmt.Sprintf("Compacted %s → %s tokens", Tokens(c.TokensBefore), Tokens(c.TokensAfter))})
	a.emit(Event{Kind: EvMessage}) // saves the session
	return nil
}

// autoCompact reports whether the agent compacts by itself.
func (a *Agent) autoCompact() bool {
	return a.Compaction != nil && a.Compaction.Auto && a.Compaction.Compactor != nil
}

// needsCompaction reports whether the next request would fill the window
// past the threshold.
func (a *Agent) needsCompaction(s *Session) bool {
	if !a.autoCompact() || a.ContextWindow <= 0 {
		return false
	}
	used := s.ContextUsed()
	return used > 0 && float64(used+int64(a.Compaction.Reserve)) > float64(a.ContextWindow)*a.Compaction.Threshold
}

// Tokens writes a token count briefly: 950, 42k, 1.2M.
func Tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e3), ".0") + "k"
	}
	return fmt.Sprint(n)
}
