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

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// googleBase is the Gemini API.
const googleBase = "https://generativelanguage.googleapis.com/v1beta"

// Google speaks Gemini's generateContent API, on the Gemini API with a
// key or on Vertex AI with Google Cloud's application default
// credentials (gcloud auth application-default login).
type Google struct {
	base string // up to the models/ path, no trailing slash
	key  string
	// vertex holds the project and location of a Vertex AI endpoint;
	// tokens signs its calls.
	vertex  bool
	project string
	tokens  oauth2.TokenSource
	http    *http.Client
}

// NewGoogle builds the adapter. cfg.Project picks Vertex AI, in
// cfg.Location ("global" when empty).
func NewGoogle(cfg Config) *Google {
	g := &Google{key: cfg.APIKey, http: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}}
	switch {
	case cfg.Project != "":
		loc := cfg.Location
		if loc == "" {
			loc = "global"
		}
		host := "https://" + loc + "-aiplatform.googleapis.com"
		if loc == "global" {
			host = "https://aiplatform.googleapis.com"
		}
		if cfg.BaseURL != "" {
			host = strings.TrimRight(cfg.BaseURL, "/")
		}
		g.vertex, g.project = true, cfg.Project
		g.base = host + "/v1/projects/" + url.PathEscape(cfg.Project) + "/locations/" + url.PathEscape(loc) + "/publishers/google"
	case cfg.BaseURL != "":
		g.base = strings.TrimRight(cfg.BaseURL, "/")
	default:
		g.base = googleBase
	}
	return g
}

func (g *Google) Name(model string) string { return "google:" + g.base + ":" + model }

// auth signs a request: the key on the Gemini API, a token on Vertex AI.
func (g *Google) auth(ctx context.Context, hr *http.Request) error {
	if !g.vertex {
		if g.key == "" {
			return ErrNoKey
		}
		hr.Header.Set("x-goog-api-key", g.key)
		return nil
	}
	if g.key != "" { // an access token given as the key
		hr.Header.Set("Authorization", "Bearer "+g.key)
		return nil
	}
	if g.tokens == nil {
		creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return &APIError{Kind: ErrAuth, Msg: "Vertex AI needs Google Cloud credentials; run gcloud auth application-default login (" + err.Error() + ")"}
		}
		g.tokens = creds.TokenSource
	}
	tok, err := g.tokens.Token()
	if err != nil {
		return &APIError{Kind: ErrAuth, Msg: "Google Cloud credentials: " + err.Error()}
	}
	hr.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	return nil
}

type gContent struct {
	Role  string  `json:"role"`
	Parts []gPart `json:"parts"`
}

type gPart struct {
	Text             string     `json:"text,omitempty"`
	Thought          bool       `json:"thought,omitempty"`
	ThoughtSignature string     `json:"thoughtSignature,omitempty"`
	FunctionCall     *gCall     `json:"functionCall,omitempty"`
	FunctionResponse *gResponse `json:"functionResponse,omitempty"`
	InlineData       *gBlob     `json:"inlineData,omitempty"`
}

// gBlob is a picture sent inline.
type gBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

func geminiImage(im ImageData) gPart {
	return gPart{InlineData: &gBlob{MimeType: im.MediaType, Data: im.Base64()}}
}

type gCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type gResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// geminiCallPrefix marks the ids hm makes for calls Gemini sent without
// one; they are not sent back.
const geminiCallPrefix = "gcall_"

// sentID is the id a call or result carries to Gemini.
func sentID(id string) string {
	if strings.HasPrefix(id, geminiCallPrefix) {
		return ""
	}
	return id
}

func (g *Google) body(req Request) ([]byte, error) {
	var contents []gContent
	names := map[string]string{} // tool call id -> name, for the results
	for _, m := range req.Messages {
		for _, b := range m.ToolUses() {
			names[b.ToolID] = b.ToolName
		}
		if m.Role == Assistant {
			if m.Native != nil && m.NativeFrom == g.Name(req.Model) {
				var c gContent
				if err := json.Unmarshal(m.Native, &c); err == nil {
					contents = append(contents, c)
					continue
				}
			}
			c := gContent{Role: "model"}
			for _, b := range m.Blocks {
				switch b.Type {
				case Text:
					if b.Text != "" {
						c.Parts = append(c.Parts, gPart{Text: b.Text})
					}
				case ToolUse:
					args := b.Input
					if !json.Valid(args) {
						args = json.RawMessage("{}")
					}
					c.Parts = append(c.Parts, gPart{FunctionCall: &gCall{ID: sentID(b.ToolID), Name: b.ToolName, Args: args}})
				}
			}
			if len(c.Parts) > 0 {
				contents = append(contents, c)
			}
			continue
		}
		c := gContent{Role: "user"}
		for _, b := range m.Blocks {
			switch b.Type {
			case ToolResult:
				key := "result"
				if b.IsError {
					key = "error"
				}
				c.Parts = append(c.Parts, gPart{FunctionResponse: &gResponse{ID: sentID(b.ToolID),
					Name: names[b.ToolID], Response: map[string]any{key: b.Text}}})
				// The pictures of a result go next to it in the same turn.
				for _, im := range b.Images {
					c.Parts = append(c.Parts, geminiImage(im))
				}
			case Text:
				if b.Text != "" {
					c.Parts = append(c.Parts, gPart{Text: b.Text})
				}
			case Image:
				if b.Image != nil {
					c.Parts = append(c.Parts, geminiImage(*b.Image))
				}
			}
		}
		if len(c.Parts) > 0 {
			contents = append(contents, c)
		}
	}
	body := map[string]any{"contents": contents}
	if req.System != "" {
		body["systemInstruction"] = gContent{Role: "user", Parts: []gPart{{Text: req.System}}}
	}
	if len(req.Tools) > 0 {
		var decls []map[string]any
		for _, t := range req.Tools {
			var schema any
			if err := json.Unmarshal(t.Schema, &schema); err != nil {
				return nil, fmt.Errorf("tool %s: %w", t.Name, err)
			}
			decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": geminiSchema(schema)})
		}
		body["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	gen := map[string]any{}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	if tc := geminiThinking(req.Model, req.Effort); tc != nil {
		gen["thinkingConfig"] = tc
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	return json.Marshal(body)
}

// geminiSchema drops the JSON Schema keywords Gemini's schema subset
// refuses, at every level.
func geminiSchema(v any) any {
	switch s := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(s))
		for k, x := range s {
			switch k {
			case "$schema", "$id", "$comment", "$defs", "definitions", "additionalProperties",
				"patternProperties", "unevaluatedProperties", "examples":
				continue
			}
			out[k] = geminiSchema(x)
		}
		return out
	case []any:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = geminiSchema(x)
		}
		return out
	}
	return v
}

// geminiThinking maps hm's effort onto a model's thinking settings:
// Gemini 3 takes a level, 2.5 a token budget. Thoughts are always asked
// for, so they show as they stream.
func geminiThinking(model, effort string) map[string]any {
	m := normalize(model)
	if !strings.HasPrefix(m, "gemini-") || strings.HasPrefix(m, "gemini-1") || strings.HasPrefix(m, "gemini-2.0") {
		return nil
	}
	tc := map[string]any{"includeThoughts": true}
	flash := strings.Contains(m, "flash")
	if strings.HasPrefix(m, "gemini-2.5") {
		budget := map[string]int{"low": 1024, "medium": 8192, "high": 24576, "xhigh": 32768, "max": 32768}
		switch b, ok := budget[effort]; {
		case effort == "off" && flash:
			return map[string]any{"thinkingBudget": 0}
		case effort == "off":
			tc["thinkingBudget"] = 128 // Pro cannot turn thinking off
		case ok:
			if flash {
				b = min(b, 24576)
			}
			tc["thinkingBudget"] = b
		}
		return tc
	}
	switch effort {
	case "off":
		tc["thinkingLevel"] = "low"
		if flash {
			tc["thinkingLevel"] = "minimal"
		}
	case "low":
		tc["thinkingLevel"] = "low"
	case "medium", "high", "xhigh", "max":
		tc["thinkingLevel"] = "high"
	}
	return tc
}

type gChunk struct {
	Candidates []struct {
		Content      gContent `json:"content"`
		FinishReason string   `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount        int64 `json:"promptTokenCount"`
		CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
		ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
		CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	ModelVersion string  `json:"modelVersion"`
	Error        *gError `json:"error"`
}

// gError is Google's {"error": {...}} object.
type gError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// apiError builds the error for a failed call, mapping Google's status
// words onto the kinds classify knows.
func (e gError) apiError(status int, h http.Header) *APIError {
	kind := map[string]string{
		"RESOURCE_EXHAUSTED": "rate_limit_error",
		"UNAVAILABLE":        "overloaded_error",
		"UNAUTHENTICATED":    "authentication_error",
		"PERMISSION_DENIED":  "permission_error",
	}[e.Status]
	if strings.Contains(e.Message, "API key not valid") {
		kind = "authentication_error"
	}
	msg := e.Message
	if status != 0 {
		msg = fmt.Sprintf("%d %s: %s", status, e.Status, e.Message)
	}
	return classify(status, h, kind, msg)
}

func googleError(resp *http.Response, body []byte) error {
	var e struct {
		Error gError `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error.Message == "" {
		// Some proxies wrap it in a list.
		var list []struct {
			Error gError `json:"error"`
		}
		if json.Unmarshal(body, &list) == nil && len(list) > 0 {
			e.Error = list[0].Error
		}
	}
	if e.Error.Message == "" {
		e.Error.Message = strings.TrimSpace(string(body))
	}
	return e.Error.apiError(resp.StatusCode, resp.Header)
}

func (g *Google) Stream(ctx context.Context, req Request, onEvent func(Event)) (Response, error) {
	if req.Model == "" {
		return Response{}, errors.New("pick a model for this provider in Settings → Agent")
	}
	data, err := g.body(req)
	if err != nil {
		return Response{}, err
	}
	u := g.base + "/models/" + url.PathEscape(normalizeGoogleModel(req.Model)) + ":streamGenerateContent?alt=sse"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return Response{}, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "text/event-stream")
	if err := g.auth(ctx, hr); err != nil {
		return Response{}, err
	}
	resp, err := g.http.Do(hr)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return Response{}, googleError(resp, b)
	}

	var parts []gPart // as Gemini sent them, merged, for Native
	var out Response
	finish := ""
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ch gChunk
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ch) != nil {
			continue
		}
		if ch.Error != nil {
			return Response{}, ch.Error.apiError(0, nil)
		}
		if ch.ModelVersion != "" {
			out.Model = ch.ModelVersion
		}
		if u := ch.UsageMetadata; u != nil {
			out.Usage = Usage{Input: u.PromptTokenCount - u.CachedContentTokenCount, CacheRead: u.CachedContentTokenCount,
				Output: u.CandidatesTokenCount + u.ThoughtsTokenCount}
		}
		if f := ch.PromptFeedback; f != nil && f.BlockReason != "" {
			out.StopReason, out.StopDetail = StopRefusal, f.BlockReason
			finish = f.BlockReason
		}
		for _, c := range ch.Candidates {
			for _, p := range c.Content.Parts {
				parts = mergePart(parts, p)
				switch {
				case p.FunctionCall != nil:
					emit(onEvent, Event{Kind: EventToolStart, ToolName: p.FunctionCall.Name})
					emit(onEvent, Event{Kind: EventToolInput, Text: string(p.FunctionCall.Args)})
				case p.Thought && p.Text != "":
					emit(onEvent, Event{Kind: EventThinking, Text: p.Text})
				case p.Text != "":
					emit(onEvent, Event{Kind: EventText, Text: p.Text})
				}
			}
			if c.FinishReason != "" {
				finish = c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, err
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if finish == "" {
		return Response{}, errEarlyEnd
	}

	msg := Message{Role: Assistant}
	calls := 0
	for _, p := range parts {
		switch {
		case p.FunctionCall != nil:
			id := p.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("%s%d", geminiCallPrefix, calls)
			}
			args := p.FunctionCall.Args
			if len(args) == 0 || string(args) == "null" {
				args = json.RawMessage("{}")
			}
			msg.Blocks = append(msg.Blocks, Block{Type: ToolUse, ToolID: id, ToolName: p.FunctionCall.Name, Input: args})
			calls++
		case p.Thought:
			msg.Blocks = append(msg.Blocks, Block{Type: Thinking, Text: p.Text})
		case p.Text != "":
			msg.Blocks = append(msg.Blocks, Block{Type: Text, Text: p.Text})
		}
	}
	msg.Native, _ = json.Marshal(gContent{Role: "model", Parts: parts})
	msg.NativeFrom = g.Name(req.Model)
	out.Message = msg
	if out.StopReason == "" {
		out.StopReason = geminiStop(finish)
	}
	if calls > 0 && out.StopReason == StopEnd {
		out.StopReason = StopToolUse
	}
	return out, nil
}

// mergePart adds a streamed part: text continues the last part of its
// kind until a signature or a call comes between, so the parts sent back
// carry each signature where Gemini put it.
func mergePart(parts []gPart, p gPart) []gPart {
	if n := len(parts); n > 0 && p.FunctionCall == nil && p.FunctionResponse == nil {
		last := &parts[n-1]
		if last.FunctionCall == nil && last.Thought == p.Thought && last.ThoughtSignature == "" {
			last.Text += p.Text
			last.ThoughtSignature = p.ThoughtSignature
			return parts
		}
	}
	return append(parts, p)
}

func geminiStop(reason string) StopReason {
	switch reason {
	case "STOP":
		return StopEnd
	case "MAX_TOKENS":
		return StopMaxTokens
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "OTHER":
		return StopRefusal
	}
	return StopOther
}

// normalizeGoogleModel takes "models/gemini-2.5-pro" as well as the bare id.
func normalizeGoogleModel(m string) string { return strings.TrimPrefix(m, "models/") }

// Models lists the models that generate content. Vertex AI has no list
// of its publisher's models for a project, so there the id is typed.
func (g *Google) Models(ctx context.Context) ([]string, error) {
	if g.vertex {
		return nil, errors.New("Vertex AI does not list its models")
	}
	var out []string
	page := ""
	for range 20 {
		u := g.base + "/models?pageSize=1000"
		if page != "" {
			u += "&pageToken=" + url.QueryEscape(page)
		}
		hr, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		if err := g.auth(ctx, hr); err != nil {
			return nil, err
		}
		resp, err := g.http.Do(hr)
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, googleError(resp, b)
		}
		var body struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			Next string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			return nil, fmt.Errorf("reading the model list: %w", err)
		}
		for _, m := range body.Models {
			for _, meth := range m.Methods {
				if meth == "generateContent" {
					out = append(out, normalizeGoogleModel(m.Name))
					break
				}
			}
		}
		if page = body.Next; page == "" {
			break
		}
	}
	sort.Strings(out)
	return out, nil
}
