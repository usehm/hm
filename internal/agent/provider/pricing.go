package provider

import "strings"

// price is USD per million tokens.
type price struct{ in, out, cacheRead, cacheWrite float64 }

// prices are Anthropic first-party rates; a 5-minute cache write costs
// 1.25x input. Models not listed (and every OpenAI-compatible model, whose
// providers report cost themselves or not at all) cost 0 here.
var prices = map[string]price{
	"claude-fable-5-1":  {10, 50, 0.25, 12.5},
	"claude-mythos-5-1": {10, 50, 0.25, 12.5},
	"claude-fable-5":    {10, 50, 1, 12.5},
	"claude-opus-5-5":   {4, 20, 0.20, 5},
	"claude-opus-5":     {5, 25, 0.5, 6.25},
	"claude-opus-4-8":   {5, 25, 0.5, 6.25},
	"claude-opus-4-7":   {5, 25, 0.5, 6.25},
	"claude-opus-4-6":   {5, 25, 0.5, 6.25},
	"claude-sonnet-5-5": {2, 10, 0.20, 2.5},
	"claude-sonnet-5":   {2, 10, 0.20, 2.5},
	"claude-sonnet-4-6": {3, 15, 0.3, 3.75},
	"claude-haiku-5-5":  {0.10, 0.50, 0.01, 0.125},
	"claude-haiku-4-5":  {1, 5, 0.1, 1.25},
}

// Cost prices u for model; 0 when the model's price is unknown.
func Cost(model string, u Usage) float64 {
	p, ok := prices[strings.TrimPrefix(model, "anthropic.")]
	if !ok {
		return 0
	}
	// Anthropic reports input tokens excluding cache reads and writes.
	return (float64(u.Input)*p.in + float64(u.Output)*p.out +
		float64(u.CacheRead)*p.cacheRead + float64(u.CacheWrite)*p.cacheWrite) / 1e6
}
