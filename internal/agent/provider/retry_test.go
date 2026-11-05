package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// step is one scripted answer: an error status with a body, or a 200 SSE
// stream, cut short after cut bytes when drop is set.
type step struct {
	status int
	header map[string]string
	body   string
	cut    int
	drop   bool
}

// scripted serves steps in order, the last one again once they run out,
// and counts the requests.
func scripted(t *testing.T, steps ...step) (*httptest.Server, *int) {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := steps[min(n, len(steps)-1)]
		n++
		for k, v := range s.header {
			w.Header().Set(k, v)
		}
		if s.status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(s.status)
			_, _ = w.Write([]byte(s.body))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if !s.drop {
			_, _ = w.Write([]byte(s.body))
			return
		}
		// Send part of the stream, then drop the connection.
		_, _ = w.Write([]byte(s.body[:s.cut]))
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func sse(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cutBefore is the offset of the first line holding marker.
func cutBefore(t *testing.T, body, marker string) int {
	t.Helper()
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no %q in fixture", marker)
	}
	return strings.LastIndex(body[:i], "\n") + 1
}

type providerCase struct {
	name     string
	fixture  string
	newP     func(url string) Provider
	firstOut string // the fixture's first line that streams output
	textLine string // a line of the fixture that streams text
	// Error bodies in the API's shape.
	rateLimit, overloaded, overflow, auth string
	overloadStatus                        int
}

func providerCases() []providerCase {
	anthropicErr := func(typ, msg string) string {
		return `{"type":"error","error":{"type":"` + typ + `","message":"` + msg + `"}}`
	}
	openaiErr := func(code, msg string) string {
		return `{"error":{"message":"` + msg + `","type":"invalid_request_error","code":"` + code + `"}}`
	}
	return []providerCase{
		{
			name: "anthropic", fixture: "anthropic_tool_use.sse",
			newP:      func(url string) Provider { return NewAnthropic(Config{APIKey: "k", BaseURL: url}) },
			firstOut:  `"thinking_delta"`,
			textLine:  `"text_delta","text":"Reading "`,
			rateLimit: anthropicErr("rate_limit_error", "slow down"), overloaded: anthropicErr("overloaded_error", "Overloaded"),
			overflow: anthropicErr("invalid_request_error", "prompt is too long: 250000 tokens > 200000 maximum"),
			auth:     anthropicErr("authentication_error", "invalid x-api-key"), overloadStatus: 529,
		},
		{
			name: "openai", fixture: "openrouter_tool_use.sse",
			newP:      func(url string) Provider { return NewOpenAI(Config{APIKey: "k", BaseURL: url}) },
			firstOut:  `"content":"Reading "`,
			textLine:  `"content":"Reading "`,
			rateLimit: openaiErr("rate_limit_exceeded", "Rate limit reached"), overloaded: openaiErr("", "The server is overloaded"),
			overflow: openaiErr("context_length_exceeded",
				"This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens."),
			auth: openaiErr("invalid_api_key", "Incorrect API key provided"), overloadStatus: 503,
		},
	}
}

// retrying wraps p, recording the waits instead of sleeping.
func retrying(p Provider, max int) (*Retry, *[]time.Duration) {
	var waits []time.Duration
	r := WithRetry(p, max).(*Retry)
	r.sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	return r, &waits
}

var simpleReq = Request{Model: "claude-opus-5-5", Messages: []Message{{Role: User, Blocks: []Block{{Type: Text, Text: "hi"}}}}}

func TestRetry(t *testing.T) {
	for _, pc := range providerCases() {
		ok := sse(t, pc.fixture)
		cases := []struct {
			name     string
			steps    []step
			max      int
			wantErr  error // nil: the call succeeds
			wantReqs int
			check    func(t *testing.T, waits []time.Duration, retries []string)
		}{
			{
				name: "429 with Retry-After then 200",
				steps: []step{{status: 429, header: map[string]string{"Retry-After": "3"}, body: pc.rateLimit},
					{status: 200, body: ok}},
				max: 5, wantReqs: 2,
				check: func(t *testing.T, waits []time.Duration, retries []string) {
					if len(waits) != 1 || waits[0] != 3*time.Second {
						t.Fatalf("waits %v", waits)
					}
					if len(retries) != 1 || retries[0] != "Rate limited, retrying in 3s (1/5)" {
						t.Fatalf("retry events %q", retries)
					}
				},
			},
			{
				name:  "overloaded twice then 200",
				steps: []step{{status: pc.overloadStatus, body: pc.overloaded}, {status: pc.overloadStatus, body: pc.overloaded}, {status: 200, body: ok}},
				max:   5, wantReqs: 3,
				check: func(t *testing.T, waits []time.Duration, _ []string) {
					// 1s then 2s, a fifth either way.
					if len(waits) != 2 || waits[0] < 800*time.Millisecond || waits[0] > 1200*time.Millisecond ||
						waits[1] < 1600*time.Millisecond || waits[1] > 2400*time.Millisecond {
						t.Fatalf("waits %v", waits)
					}
				},
			},
			{
				name:  "gives up after max",
				steps: []step{{status: pc.overloadStatus, body: pc.overloaded}},
				max:   2, wantErr: ErrOverloaded, wantReqs: 3,
			},
			{
				name:  "disconnect before any output",
				steps: []step{{status: 200, body: ok, cut: cutBefore(t, ok, pc.firstOut), drop: true}, {status: 200, body: ok}},
				max:   5, wantReqs: 2,
			},
			{
				name:  "disconnect after text is not retried",
				steps: []step{{status: 200, body: ok, cut: cutBefore(t, ok, pc.textLine) + len(firstLine(ok[cutBefore(t, ok, pc.textLine):])) + 2, drop: true}},
				max:   5, wantErr: errAny, wantReqs: 1,
			},
			{
				name:  "overflow is not retried",
				steps: []step{{status: 400, body: pc.overflow}},
				max:   5, wantErr: ErrContextOverflow, wantReqs: 1,
			},
			{
				name:  "auth is not retried",
				steps: []step{{status: 401, body: pc.auth}},
				max:   5, wantErr: ErrAuth, wantReqs: 1,
			},
		}
		for _, tc := range cases {
			t.Run(pc.name+"/"+tc.name, func(t *testing.T) {
				srv, reqs := scripted(t, tc.steps...)
				url := srv.URL
				if pc.name == "openai" {
					url += "/v1"
				}
				r, waits := retrying(pc.newP(url), tc.max)
				var retries []string
				var text strings.Builder
				resp, err := r.Stream(context.Background(), simpleReq, func(e Event) {
					switch e.Kind {
					case EventRetry:
						retries = append(retries, e.Text)
					case EventText:
						text.WriteString(e.Text)
					}
				})
				switch {
				case tc.wantErr == nil && err != nil:
					t.Fatalf("err %v", err)
				case tc.wantErr == errAny && err == nil, tc.wantErr != nil && tc.wantErr != errAny && !errors.Is(err, tc.wantErr):
					t.Fatalf("err %v, want %v", err, tc.wantErr)
				}
				if *reqs != tc.wantReqs {
					t.Fatalf("%d requests, want %d", *reqs, tc.wantReqs)
				}
				if tc.wantErr == nil {
					checkToolTurn(t, resp, []Event{{Kind: EventText, Text: text.String()}})
				} else if strings.Count(text.String(), "Reading") > 1 {
					t.Fatalf("text streamed twice: %q", text.String())
				}
				if tc.check != nil {
					tc.check(t, *waits, retries)
				}
			})
		}
	}
}

// errAny accepts any error.
var errAny = errors.New("any error")

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// An overloaded_error event inside a stream that started fine is retried.
func TestRetryAnthropicStreamErrorEvent(t *testing.T) {
	ok := sse(t, "anthropic_tool_use.sse")
	bad := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	srv, reqs := scripted(t, step{status: 200, body: bad}, step{status: 200, body: ok})
	r, _ := retrying(NewAnthropic(Config{APIKey: "k", BaseURL: srv.URL}), 3)
	if _, err := r.Stream(context.Background(), simpleReq, nil); err != nil || *reqs != 2 {
		t.Fatalf("err %v after %d requests", err, *reqs)
	}
}

// Cancelling during the wait returns at once.
func TestRetryCancelWait(t *testing.T) {
	srv, reqs := scripted(t, step{status: 429, header: map[string]string{"Retry-After": "30"}, body: `{"error":{"message":"slow"}}`})
	p := WithRetry(NewOpenAI(Config{BaseURL: srv.URL + "/v1"}), 5)
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	_, err := p.Stream(ctx, Request{Model: "m"}, func(e Event) {
		if e.Kind == EventRetry {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second || *reqs != 1 {
		t.Fatalf("err %v after %v, %d requests", err, time.Since(start), *reqs)
	}
}

func TestWithRetryOff(t *testing.T) {
	p := NewOpenAI(Config{BaseURL: "http://x"})
	if WithRetry(p, 0) != Provider(p) {
		t.Fatal("max 0 should not wrap")
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		status  int
		header  map[string]string
		errType string
		msg     string
		kind    error
		after   time.Duration
	}{
		{429, map[string]string{"Retry-After": "8"}, "", "slow down", ErrRateLimited, 8 * time.Second},
		{429, map[string]string{"Retry-After-Ms": "1500"}, "", "slow down", ErrRateLimited, 1500 * time.Millisecond},
		{0, nil, "rate_limit_error", "slow", ErrRateLimited, 0},
		{529, nil, "", "Overloaded", ErrOverloaded, 0},
		{503, nil, "", "unavailable", ErrOverloaded, 0},
		{0, nil, "overloaded_error", "Overloaded", ErrOverloaded, 0},
		{401, nil, "", "bad key", ErrAuth, 0},
		{403, nil, "permission_error", "no", ErrAuth, 0},
		{400, nil, "invalid_request_error", "prompt is too long: 250000 tokens > 200000 maximum", ErrContextOverflow, 0},
		{400, nil, "context_length_exceeded", "too long", ErrContextOverflow, 0},
		{400, nil, "", "This model's maximum context length is 8192 tokens", ErrContextOverflow, 0},
		{400, nil, "", "input length exceeds the context length", ErrContextOverflow, 0}, // Ollama
		{413, nil, "", "Request too large: too many tokens", ErrContextOverflow, 0},
		{400, nil, "", "bad request", nil, 0},
		{500, nil, "", "boom", nil, 0},
	}
	for _, tc := range cases {
		h := http.Header{}
		for k, v := range tc.header {
			h.Set(k, v)
		}
		e := classify(tc.status, h, tc.errType, tc.msg)
		if e.Kind != tc.kind || e.RetryAfter != tc.after || e.Error() != tc.msg {
			t.Errorf("classify(%d, %q, %q) = %v %v %q", tc.status, tc.errType, tc.msg, e.Kind, e.RetryAfter, e.Error())
		}
	}
}

func TestLimitsOf(t *testing.T) {
	cases := []struct {
		model  string
		window int
		ok     bool
	}{
		{"claude-opus-5-5", 1_000_000, true},
		{"claude-haiku-4-5-20251001", 200_000, true},
		{"anthropic.claude-sonnet-4-6", 1_000_000, true},
		{"anthropic/claude-opus-5", 1_000_000, true},
		{"openai/gpt-4o-mini", 128_000, true},
		{"gpt-4.1-nano", 1_047_576, true},
		{"llama3.2", 0, false},
	}
	for _, tc := range cases {
		l, ok := LimitsOf(tc.model)
		if l.ContextWindow != tc.window || ok != tc.ok {
			t.Errorf("LimitsOf(%q) = %v %v", tc.model, l, ok)
		}
	}
}
