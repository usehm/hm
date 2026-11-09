package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// OpenAI speaks the chat completions API that OpenAI, Azure OpenAI,
// OpenRouter, Ollama, LM Studio, Groq, DeepSeek and others share.
type OpenAI struct {
	base string
	key  string
	http *http.Client
	// azure calls an Azure OpenAI resource: the key goes in api-key, and
	// with apiVersion the model is a deployment in the path.
	azure      bool
	apiVersion string
}

// NewOpenAI builds the adapter for cfg.BaseURL (ending in /v1).
func NewOpenAI(cfg Config) *OpenAI {
	return &OpenAI{
		base: strings.TrimRight(cfg.BaseURL, "/"), key: cfg.APIKey,
		http: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
	}
}

// NewAzure builds the adapter for an Azure OpenAI resource at cfg.BaseURL
// (https://<resource>.openai.azure.com). With cfg.APIVersion it calls a
// deployment, named by the request's model, at that API version; without,
// the v1 API, which takes the deployment as the model.
func NewAzure(cfg Config) *OpenAI {
	o := NewOpenAI(cfg)
	o.base = strings.TrimSuffix(strings.TrimSuffix(o.base, "/openai/v1"), "/openai")
	o.azure, o.apiVersion = true, cfg.APIVersion
	return o
}

func (o *OpenAI) Name(model string) string {
	if o.azure {
		return "azure:" + o.base + ":" + model
	}
	return "openai:" + o.base + ":" + model
}

// url is where a call for model goes.
func (o *OpenAI) url(model, path string) string {
	switch {
	case o.azure && o.apiVersion != "":
		return o.base + "/openai/deployments/" + url.PathEscape(model) + path + "?api-version=" + url.QueryEscape(o.apiVersion)
	case o.azure:
		return o.base + "/openai/v1" + path
	}
	return o.base + path
}

// auth adds the key the way the API takes it.
func (o *OpenAI) auth(hr *http.Request) {
	switch {
	case o.key == "":
	case o.azure:
		hr.Header.Set("api-key", o.key)
	default:
		hr.Header.Set("Authorization", "Bearer "+o.key)
	}
}

// reasoning reports whether the request goes to OpenAI's own models
// (on OpenAI or Azure) that reason: they take reasoning_effort and count
// their limit in max_completion_tokens.
func (o *OpenAI) reasoning(model string) bool {
	if !o.azure && !strings.Contains(o.base, "api.openai.com") {
		return false
	}
	m := normalize(model)
	for _, p := range []string{"o1", "o3", "o4", "gpt-5"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

// openAIEffort maps hm's effort words onto reasoning_effort: gpt-5 can
// think minimally, the o-series no less than low.
func openAIEffort(model, e string) string {
	switch e {
	case "xhigh", "max":
		return "high"
	case "off":
		if strings.HasPrefix(normalize(model), "gpt-5") {
			return "minimal"
		}
		return "low"
	}
	return e
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content"` // *string, or []oaPart with pictures
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// oaPart is one part of a message's content: text or a picture, which
// goes as a data URL.
type oaPart struct {
	Type     string      `json:"type"`
	Text     string      `json:"text,omitempty"`
	ImageURL *oaImageURL `json:"image_url,omitempty"`
}

type oaImageURL struct {
	URL string `json:"url"`
}

func imagePart(im ImageData) oaPart {
	return oaPart{Type: "image_url", ImageURL: &oaImageURL{"data:" + im.MediaType + ";base64," + im.Base64()}}
}

func str(s string) *string { return &s }

func (o *OpenAI) body(req Request) ([]byte, error) {
	msgs := []oaMessage{}
	if req.System != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: str(req.System)})
	}
	for _, m := range req.Messages {
		if m.Role == Assistant {
			om := oaMessage{Role: "assistant"}
			if t := m.TextOf(); t != "" {
				om.Content = str(t)
			}
			for _, b := range m.ToolUses() {
				tc := oaToolCall{ID: b.ToolID, Type: "function"}
				tc.Function.Name = b.ToolName
				tc.Function.Arguments = string(b.Input)
				if !json.Valid(b.Input) {
					tc.Function.Arguments = "{}"
				}
				om.ToolCalls = append(om.ToolCalls, tc)
			}
			msgs = append(msgs, om)
			continue
		}
		// A tool message takes only text, so the pictures of tool results
		// follow them in a user message of their own.
		var parts, shown []oaPart
		for _, b := range m.Blocks {
			switch b.Type {
			case ToolResult:
				content := b.Text
				if b.IsError {
					content = "Error: " + content
				}
				msgs = append(msgs, oaMessage{Role: "tool", ToolCallID: b.ToolID, Content: str(content)})
				if len(b.Images) > 0 {
					shown = append(shown, oaPart{Type: "text", Text: "Images from tool call " + b.ToolID + ":"})
					for _, im := range b.Images {
						shown = append(shown, imagePart(im))
					}
				}
			case Text:
				if n := len(parts); n > 0 && parts[n-1].Type == "text" {
					parts[n-1].Text += b.Text
				} else if b.Text != "" {
					parts = append(parts, oaPart{Type: "text", Text: b.Text})
				}
			case Image:
				if b.Image != nil {
					parts = append(parts, imagePart(*b.Image))
				}
			}
		}
		if len(shown) > 0 {
			msgs = append(msgs, oaMessage{Role: "user", Content: shown})
		}
		switch {
		case len(parts) == 1 && parts[0].Type == "text":
			msgs = append(msgs, oaMessage{Role: "user", Content: str(parts[0].Text)})
		case len(parts) > 0:
			msgs = append(msgs, oaMessage{Role: "user", Content: parts})
		}
	}
	body := map[string]any{
		"model":          req.Model,
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	reasoning := o.reasoning(req.Model)
	if req.MaxTokens > 0 {
		if reasoning {
			body["max_completion_tokens"] = req.MaxTokens
		} else {
			body["max_tokens"] = req.MaxTokens
		}
	}
	if reasoning && req.Effort != "" {
		body["reasoning_effort"] = openAIEffort(req.Model, req.Effort)
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": json.RawMessage(t.Schema),
			}})
		}
		body["tools"] = tools
	}
	if strings.Contains(o.base, "openrouter.ai") {
		body["usage"] = map[string]any{"include": true} // OpenRouter reports cost
		switch req.Effort {
		case "":
		case "off":
			body["reasoning"] = map[string]any{"enabled": false}
		default:
			body["reasoning"] = map[string]any{"effort": openRouterEffort(req.Effort)}
		}
	}
	return json.Marshal(body)
}

// openRouterEffort maps hm's effort words onto OpenRouter's three.
func openRouterEffort(e string) string {
	switch e {
	case "xhigh", "max":
		return "high"
	}
	return e
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64   `json:"prompt_tokens"`
		CompletionTokens    int64   `json:"completion_tokens"`
		Cost                float64 `json:"cost"`
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *oaError `json:"error"`
}

// oaError is the {"error": {...}} object, in a response body or a chunk.
type oaError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"` // a string, or a number on some servers
}

// kind is the error's code, else its type, for classify.
func (e oaError) kind() string {
	if c, ok := e.Code.(string); ok && c != "" {
		return c
	}
	return e.Type
}

// Models lists the endpoint's models from GET /models, sorted.
func (o *OpenAI) Models(ctx context.Context) ([]string, error) {
	if o.azure {
		return nil, errors.New("Azure OpenAI does not list your deployments; type the deployment name")
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/models", nil)
	if err != nil {
		return nil, err
	}
	o.auth(hr)
	resp, err := o.http.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, apiError(resp, b)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("reading the model list: %w", err)
	}
	out := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (o *OpenAI) Stream(ctx context.Context, req Request, onEvent func(Event)) (Response, error) {
	if req.Model == "" {
		return Response{}, errors.New("pick a model for this provider in Settings → Agent")
	}
	data, err := o.body(req)
	if err != nil {
		return Response{}, err
	}
	if o.azure && o.base == "" {
		return Response{}, errors.New("set base_url to your Azure OpenAI resource, https://<resource>.openai.azure.com")
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url(req.Model, "/chat/completions"), bytes.NewReader(data))
	if err != nil {
		return Response{}, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "text/event-stream")
	o.auth(hr)
	hr.Header.Set("HTTP-Referer", "https://github.com/mirzakhany/hm")
	hr.Header.Set("X-Title", "hm")
	resp, err := o.http.Do(hr)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return Response{}, apiError(resp, b)
	}

	type call struct{ id, name, args string }
	calls := map[int]*call{}
	var text, reasoning strings.Builder
	var out Response
	out.StopReason = StopEnd
	done := false // [DONE] or a finish reason arrived
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			done = true
			break
		}
		var ch oaChunk
		if json.Unmarshal([]byte(payload), &ch) != nil {
			continue
		}
		if ch.Error != nil {
			return Response{}, classify(0, nil, ch.Error.kind(), ch.Error.Message)
		}
		if u := ch.Usage; u != nil {
			cached := u.PromptTokensDetails.CachedTokens
			out.Usage = Usage{Input: u.PromptTokens - cached, Output: u.CompletionTokens, CacheRead: cached, Cost: u.Cost}
		}
		for _, c := range ch.Choices {
			d := c.Delta
			if d.Content != "" {
				text.WriteString(d.Content)
				emit(onEvent, Event{Kind: EventText, Text: d.Content})
			}
			if r := d.Reasoning + d.ReasoningContent; r != "" {
				reasoning.WriteString(r)
				emit(onEvent, Event{Kind: EventThinking, Text: r})
			}
			for _, tc := range d.ToolCalls {
				cl := calls[tc.Index]
				if cl == nil {
					cl = &call{}
					calls[tc.Index] = cl
				}
				if tc.ID != "" {
					cl.id = tc.ID
				}
				if tc.Function.Name != "" {
					cl.name += tc.Function.Name
					emit(onEvent, Event{Kind: EventToolStart, ToolName: cl.name})
				}
				if tc.Function.Arguments != "" {
					cl.args += tc.Function.Arguments
					emit(onEvent, Event{Kind: EventToolInput, Text: tc.Function.Arguments})
				}
			}
			if c.FinishReason != "" {
				done = true
				out.StopReason = mapStop(c.FinishReason)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, err
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if !done {
		return Response{}, errEarlyEnd
	}
	msg := Message{Role: Assistant}
	if reasoning.Len() > 0 {
		msg.Blocks = append(msg.Blocks, Block{Type: Thinking, Text: reasoning.String()})
	}
	if text.Len() > 0 {
		msg.Blocks = append(msg.Blocks, Block{Type: Text, Text: text.String()})
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for n, i := range idx {
		cl := calls[i]
		if cl.id == "" {
			cl.id = fmt.Sprintf("call_%d", n) // some local servers send none
		}
		args := cl.args
		if args == "" {
			args = "{}"
		}
		msg.Blocks = append(msg.Blocks, Block{Type: ToolUse, ToolID: cl.id, ToolName: cl.name, Input: json.RawMessage(args)})
	}
	if len(calls) > 0 {
		out.StopReason = StopToolUse
	}
	out.Message = msg
	return out, nil
}

func emit(fn func(Event), e Event) {
	if fn != nil {
		fn(e)
	}
}

// apiError reads an error body in the common {"error": {"message"}} shape.
func apiError(resp *http.Response, body []byte) error {
	var e struct {
		Error oaError `json:"error"`
	}
	msg := fmt.Sprintf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	if json.Unmarshal(body, &e) == nil && e.Error.Message != "" {
		msg = fmt.Sprintf("%s: %s", resp.Status, e.Error.Message)
	}
	return classify(resp.StatusCode, resp.Header, e.Error.kind(), msg)
}
