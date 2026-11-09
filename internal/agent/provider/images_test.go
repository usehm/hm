package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// imageChat is a turn with a pasted picture, then a read that gave one
// back.
func imageChat() []Message {
	pic := ImageData{MediaType: "image/png", Data: []byte("PNGDATA"), Width: 2, Height: 1}
	shot := ImageData{MediaType: "image/jpeg", Data: []byte("JPGDATA")}
	return []Message{
		{Role: User, Blocks: []Block{{Type: Text, Text: "what is this?"}, {Type: Image, Image: &pic}}},
		{Role: Assistant, Blocks: []Block{{Type: ToolUse, ToolID: "call_1", ToolName: "read", Input: json.RawMessage(`{"path":"b.jpg"}`)}}},
		{Role: User, Blocks: []Block{{Type: ToolResult, ToolID: "call_1", ToolName: "read", Text: "Image b.jpg", Images: []ImageData{shot}}}},
	}
}

const (
	pngB64 = "UE5HREFUQQ==" // PNGDATA
	jpgB64 = "SlBHREFUQQ==" // JPGDATA
)

// js is v as compact JSON, for comparing request parts.
func js(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestImageEncoding(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		google  bool
		new     func(url string) Provider
		model   string
		// messages picks the conversation out of the request body.
		messages func(body map[string]any) any
		want     string
	}{
		{
			name: "anthropic", fixture: "anthropic_tool_use.sse", model: "claude-opus-5-5",
			new:      func(url string) Provider { return NewAnthropic(Config{APIKey: "k", BaseURL: url}) },
			messages: func(b map[string]any) any { return b["messages"] },
			want: `[{"content":[{"text":"what is this?","type":"text"},` +
				`{"source":{"data":"` + pngB64 + `","media_type":"image/png","type":"base64"},"type":"image"}],"role":"user"},` +
				`{"content":[{"id":"call_1","input":{"path":"b.jpg"},"name":"read","type":"tool_use"}],"role":"assistant"},` +
				`{"content":[{"content":[{"text":"Image b.jpg","type":"text"},` +
				`{"source":{"data":"` + jpgB64 + `","media_type":"image/jpeg","type":"base64"},"type":"image"}],` +
				`"is_error":false,"tool_use_id":"call_1","type":"tool_result"}],"role":"user"}]`,
		},
		{
			// A tool message takes only text: the picture follows in a
			// user message of its own.
			name: "openai", fixture: "openrouter_tool_use.sse", model: "gpt-5",
			new:      func(url string) Provider { return NewOpenAI(Config{APIKey: "k", BaseURL: url}) },
			messages: func(b map[string]any) any { return b["messages"] },
			want: `[{"content":[{"text":"what is this?","type":"text"},` +
				`{"image_url":{"url":"data:image/png;base64,` + pngB64 + `"},"type":"image_url"}],"role":"user"},` +
				`{"content":null,"role":"assistant","tool_calls":[{"function":{"arguments":"{\"path\":\"b.jpg\"}","name":"read"},"id":"call_1","type":"function"}]},` +
				`{"content":"Image b.jpg","role":"tool","tool_call_id":"call_1"},` +
				`{"content":[{"text":"Images from tool call call_1:","type":"text"},` +
				`{"image_url":{"url":"data:image/jpeg;base64,` + jpgB64 + `"},"type":"image_url"}],"role":"user"}]`,
		},
		{
			name: "google", fixture: "gemini_tool_use.sse", model: "gemini-2.5-pro", google: true,
			new:      func(url string) Provider { return NewGoogle(Config{APIKey: "k", BaseURL: url}) },
			messages: func(b map[string]any) any { return b["contents"] },
			want: `[{"parts":[{"text":"what is this?"},{"inlineData":{"data":"` + pngB64 + `","mimeType":"image/png"}}],"role":"user"},` +
				`{"parts":[{"functionCall":{"args":{"path":"b.jpg"},"id":"call_1","name":"read"}}],"role":"model"},` +
				`{"parts":[{"functionResponse":{"id":"call_1","name":"read","response":{"result":"Image b.jpg"}}},` +
				`{"inlineData":{"data":"` + jpgB64 + `","mimeType":"image/jpeg"}}],"role":"user"}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			if tt.google {
				srv, reqs := googleServer(t, 200, tt.fixture)
				p := tt.new(srv.URL)
				if _, err := p.Stream(context.Background(), Request{Model: tt.model, Messages: imageChat()}, func(Event) {}); err != nil {
					t.Fatal(err)
				}
				body = (*reqs)[0].body
			} else {
				srv, bodies := fixture(t, tt.fixture)
				p := tt.new(srv.URL)
				if _, err := p.Stream(context.Background(), Request{Model: tt.model, Messages: imageChat()}, func(Event) {}); err != nil {
					t.Fatal(err)
				}
				body = (*bodies)[0]
			}
			if got := js(t, tt.messages(body)); got != tt.want {
				t.Fatalf("messages\n got %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestWithoutImages(t *testing.T) {
	msgs := imageChat()
	before := js(t, msgs)
	out, found := WithoutImages(msgs)
	if !found {
		t.Fatal("found no images")
	}
	if js(t, msgs) != before {
		t.Fatal("WithoutImages changed its input")
	}
	if b := out[0].Blocks[1]; b.Type != Text || b.Text != NoVision || b.Image != nil {
		t.Fatalf("user picture became %+v", b)
	}
	if b := out[2].Blocks[0]; b.Type != ToolResult || len(b.Images) != 0 || b.Text != "Image b.jpg\n"+NoVision {
		t.Fatalf("tool result became %+v", b)
	}
	if strings.Contains(js(t, out), "data") {
		t.Fatal("picture data left in")
	}
	plain := []Message{{Role: User, Blocks: []Block{{Type: Text, Text: "hi"}}}}
	if got, found := WithoutImages(plain); found || &got[0] != &plain[0] {
		t.Fatal("a conversation without pictures was copied")
	}
}

func TestVision(t *testing.T) {
	no := false
	reg := &Registry{entries: append(append([]ModelInfo(nil), builtin...),
		ModelInfo{ID: "llama3", Supports: Supports{Images: &no}},
		ModelInfo{ID: "claude-haiku-4-5", Provider: "cheap", Supports: Supports{Images: &no}})}
	tests := []struct {
		prov, model string
		want        bool
	}{
		{"anthropic", "claude-opus-5-5", true},
		{"openrouter", "anthropic/claude-sonnet-5-5", true},
		{"ollama", "llama3", false},
		{"ollama", "llama3:8b", false},          // prefix of the entry's id
		{"ollama", "some-local-model", true},    // nothing known: trusted
		{"cheap", "claude-haiku-4-5", false},    // scoped entry wins
		{"anthropic", "claude-haiku-4-5", true}, // the scoped entry is another provider's
	}
	for _, tt := range tests {
		if got := reg.Vision(tt.prov, tt.model); got != tt.want {
			t.Errorf("Vision(%s, %s) = %v, want %v", tt.prov, tt.model, got, tt.want)
		}
	}
}
