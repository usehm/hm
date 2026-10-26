package provider

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// DefaultAnthropicModel is used when a provider sets no model.
const DefaultAnthropicModel = "claude-opus-5-5"

// defaultMaxTokens leaves room for long edits; streaming avoids timeouts.
const defaultMaxTokens = 64000

// Anthropic is the native Messages API adapter.
type Anthropic struct {
	client  anthropic.Client
	custom  bool // BaseURL set: a proxy or gateway, so no first-party-only features
	hasKey  bool
	baseURL string
}

// NewAnthropic builds the adapter. An empty key lets the SDK find
// credentials itself (ANTHROPIC_API_KEY, an `ant auth login` profile).
func NewAnthropic(cfg Config) *Anthropic {
	var opts []option.RequestOption
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return &Anthropic{
		client: anthropic.NewClient(opts...),
		custom: cfg.BaseURL != "", hasKey: cfg.APIKey != "", baseURL: cfg.BaseURL,
	}
}

func (a *Anthropic) Name(model string) string { return "anthropic:" + modelOr(model) }

func modelOr(m string) string {
	if m == "" {
		return DefaultAnthropicModel
	}
	return m
}

// adaptive reports whether a model takes adaptive thinking.
func adaptive(model string) bool {
	for _, p := range []string{"claude-fable-5", "claude-mythos-5", "claude-opus-5", "claude-sonnet-5", "claude-haiku-5",
		"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-sonnet-4-6"} {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

// fallbackModels take the server-side refusal fallback on the Claude API.
func fallbackModel(model string) bool {
	for _, m := range []string{"claude-fable-5-1", "claude-opus-5-5", "claude-opus-5", "claude-sonnet-5-5"} {
		if model == m {
			return true
		}
	}
	return false
}

func (a *Anthropic) params(req Request) (anthropic.BetaMessageNewParams, error) {
	model := modelOr(req.Model)
	max := int64(req.MaxTokens)
	if max <= 0 {
		max = defaultMaxTokens
	}
	p := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: max,
		// Cache the conversation prefix automatically; tools and system come
		// first in the prefix and never change within a session.
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
	}
	if req.System != "" {
		p.System = []anthropic.BetaTextBlockParam{{Text: req.System}}
	}
	if adaptive(model) {
		th := anthropic.BetaThinkingConfigAdaptiveParam{Display: anthropic.BetaThinkingConfigAdaptiveDisplaySummarized}
		p.Thinking = anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &th}
	}
	if req.Effort != "" {
		p.OutputConfig = anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(req.Effort)}
	}
	if !a.custom && fallbackModel(model) {
		// A refused request is re-served by a fallback model in the same call.
		p.Betas = append(p.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
		p.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	}
	for _, t := range req.Tools {
		var schema struct {
			Properties any      `json:"properties"`
			Required   []string `json:"required"`
		}
		var extra map[string]any
		if err := json.Unmarshal(t.Schema, &schema); err != nil {
			return p, err
		}
		_ = json.Unmarshal(t.Schema, &extra)
		delete(extra, "properties")
		delete(extra, "required")
		delete(extra, "type")
		tool := anthropic.BetaToolParam{
			Name:        t.Name,
			Description: param.NewOpt(t.Description),
			InputSchema: anthropic.BetaToolInputSchemaParam{
				Properties: schema.Properties, Required: schema.Required, ExtraFields: extra,
			},
			// Large inputs (whole files) stream as they are written; the
			// loop validates the JSON before running the tool.
			EagerInputStreaming: param.NewOpt(true),
		}
		p.Tools = append(p.Tools, anthropic.BetaToolUnionParam{OfTool: &tool})
	}
	name := a.Name(model)
	for _, m := range req.Messages {
		mp, err := toAnthropic(m, name)
		if err != nil {
			return p, err
		}
		p.Messages = append(p.Messages, mp)
	}
	return p, nil
}

// toAnthropic converts a message. An assistant turn from this same model
// is replayed exactly as the API returned it, thinking blocks included.
func toAnthropic(m Message, name string) (anthropic.BetaMessageParam, error) {
	if m.Role == Assistant && m.NativeFrom == name && len(m.Native) > 0 {
		var mp anthropic.BetaMessageParam
		if err := json.Unmarshal(m.Native, &mp); err == nil {
			return mp, nil
		}
	}
	var blocks []anthropic.BetaContentBlockParamUnion
	for _, b := range m.Blocks {
		switch b.Type {
		case Text:
			if b.Text != "" {
				blocks = append(blocks, anthropic.NewBetaTextBlock(b.Text))
			}
		case ToolUse:
			input := b.Input
			if !json.Valid(input) {
				input = json.RawMessage("{}")
			}
			blocks = append(blocks, anthropic.BetaContentBlockParamUnion{OfToolUse: &anthropic.BetaToolUseBlockParam{
				ID: b.ToolID, Name: b.ToolName, Input: input,
			}})
		case ToolResult:
			blocks = append(blocks, anthropic.NewBetaToolResultBlock(b.ToolID, b.Text, b.IsError))
		}
	}
	role := anthropic.BetaMessageParamRoleUser
	if m.Role == Assistant {
		role = anthropic.BetaMessageParamRoleAssistant
	}
	return anthropic.BetaMessageParam{Role: role, Content: blocks}, nil
}

// Models lists the models the key can use, newest first.
func (a *Anthropic) Models(ctx context.Context) ([]string, error) {
	var out []string
	it := a.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{Limit: anthropic.Int(1000)})
	for it.Next() {
		out = append(out, it.Current().ID)
	}
	if err := it.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (a *Anthropic) Stream(ctx context.Context, req Request, onEvent func(Event)) (Response, error) {
	p, err := a.params(req)
	if err != nil {
		return Response{}, err
	}
	stream := a.client.Beta.Messages.NewStreaming(ctx, p)
	defer stream.Close()
	var acc anthropic.BetaMessage
	for stream.Next() {
		ev := stream.Current()
		if err := acc.Accumulate(ev); err != nil {
			return Response{}, err
		}
		if onEvent == nil {
			continue
		}
		switch e := ev.AsAny().(type) {
		case anthropic.BetaRawContentBlockStartEvent:
			if tu, ok := e.ContentBlock.AsAny().(anthropic.BetaToolUseBlock); ok {
				onEvent(Event{Kind: EventToolStart, ToolName: tu.Name})
			}
		case anthropic.BetaRawContentBlockDeltaEvent:
			switch d := e.Delta.AsAny().(type) {
			case anthropic.BetaTextDelta:
				onEvent(Event{Kind: EventText, Text: d.Text})
			case anthropic.BetaThinkingDelta:
				onEvent(Event{Kind: EventThinking, Text: d.Thinking})
			case anthropic.BetaInputJSONDelta:
				onEvent(Event{Kind: EventToolInput, Text: d.PartialJSON})
			}
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, err
	}
	return a.response(acc, modelOr(req.Model))
}

func (a *Anthropic) response(acc anthropic.BetaMessage, model string) (Response, error) {
	msg := Message{Role: Assistant, NativeFrom: a.Name(model)}
	if native, err := json.Marshal(acc.ToParam()); err == nil {
		msg.Native = native
	}
	for _, c := range acc.Content {
		switch b := c.AsAny().(type) {
		case anthropic.BetaTextBlock:
			msg.Blocks = append(msg.Blocks, Block{Type: Text, Text: b.Text})
		case anthropic.BetaThinkingBlock:
			msg.Blocks = append(msg.Blocks, Block{Type: Thinking, Text: b.Thinking})
		case anthropic.BetaToolUseBlock:
			msg.Blocks = append(msg.Blocks, Block{Type: ToolUse, ToolID: b.ID, ToolName: b.Name,
				Input: json.RawMessage(b.JSON.Input.Raw())})
		}
	}
	r := Response{Message: msg, StopReason: mapStop(string(acc.StopReason))}
	if r.StopReason == StopRefusal {
		r.StopDetail = string(acc.StopDetails.Category)
		if e := acc.StopDetails.Explanation; e != "" {
			r.StopDetail += ": " + e
		}
	}
	u := acc.Usage
	r.Usage = Usage{Input: u.InputTokens, Output: u.OutputTokens,
		CacheRead: u.CacheReadInputTokens, CacheWrite: u.CacheCreationInputTokens}
	r.Usage.Cost = Cost(string(acc.Model), r.Usage)
	if r.Usage.Cost == 0 {
		r.Usage.Cost = Cost(model, r.Usage)
	}
	return r, nil
}

func mapStop(s string) StopReason {
	switch s {
	case "end_turn", "stop_sequence", "stop":
		return StopEnd
	case "tool_use", "tool_calls":
		return StopToolUse
	case "max_tokens", "length":
		return StopMaxTokens
	case "refusal", "content_filter":
		return StopRefusal
	}
	return StopOther
}
