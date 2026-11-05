package provider

import "strings"

// Limits are a model's token limits.
type Limits struct {
	ContextWindow int // tokens a request may hold, prompt and answer together
	MaxOutput     int // tokens one answer may have
}

// limits are known models, matched exactly and then by the longest prefix,
// so dated ids and OpenRouter's "vendor/model" names find their family.
// Override them with agent.providers.<name>.context_window.
var limits = map[string]Limits{
	"claude-fable-5":    {1_000_000, 128_000},
	"claude-mythos-5":   {1_000_000, 128_000},
	"claude-opus-5":     {1_000_000, 128_000},
	"claude-sonnet-5":   {1_000_000, 64_000},
	"claude-haiku-5":    {1_000_000, 64_000},
	"claude-opus-4-8":   {1_000_000, 128_000},
	"claude-opus-4-7":   {1_000_000, 128_000},
	"claude-opus-4-6":   {1_000_000, 128_000},
	"claude-sonnet-4-6": {1_000_000, 64_000},
	"claude-haiku-4-5":  {200_000, 64_000},
	"gpt-4o":            {128_000, 16_384},
	"gpt-4.1":           {1_047_576, 32_768},
	"gpt-5":             {400_000, 128_000},
	"o3":                {200_000, 100_000},
	"o4-mini":           {200_000, 100_000},
}

// LimitsOf returns model's limits, and false when the model is unknown.
func LimitsOf(model string) (Limits, bool) {
	model = strings.TrimPrefix(model, "anthropic.")
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:] // openai/gpt-5, anthropic/claude-opus-5
	}
	if l, ok := limits[model]; ok {
		return l, true
	}
	best := ""
	for p := range limits {
		if strings.HasPrefix(model, p) && len(p) > len(best) {
			best = p
		}
	}
	if best == "" {
		return Limits{}, false
	}
	return limits[best], true
}
