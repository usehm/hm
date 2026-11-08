package provider

import "sort"

// Preset is an OpenAI-compatible service known by name, so a provider of
// that type needs only a key.
type Preset struct {
	Label   string
	BaseURL string
	// Local servers run on this machine and need no key.
	Local bool
}

// Presets are the provider types that speak the OpenAI API at a known
// address. agent.providers.<name>.base_url still wins when set.
var Presets = map[string]Preset{
	"openrouter":  {Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1"},
	"groq":        {Label: "Groq", BaseURL: "https://api.groq.com/openai/v1"},
	"cerebras":    {Label: "Cerebras", BaseURL: "https://api.cerebras.ai/v1"},
	"mistral":     {Label: "Mistral", BaseURL: "https://api.mistral.ai/v1"},
	"xai":         {Label: "xAI", BaseURL: "https://api.x.ai/v1"},
	"deepseek":    {Label: "DeepSeek", BaseURL: "https://api.deepseek.com/v1"},
	"together":    {Label: "Together AI", BaseURL: "https://api.together.xyz/v1"},
	"fireworks":   {Label: "Fireworks AI", BaseURL: "https://api.fireworks.ai/inference/v1"},
	"huggingface": {Label: "Hugging Face", BaseURL: "https://router.huggingface.co/v1"},
	"ollama":      {Label: "Ollama", BaseURL: "http://localhost:11434/v1", Local: true},
	"lmstudio":    {Label: "LM Studio", BaseURL: "http://localhost:1234/v1", Local: true},
}

// PresetNames lists the presets, sorted.
func PresetNames() []string {
	out := make([]string, 0, len(Presets))
	for n := range Presets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// OpenAICompatible reports whether a provider type speaks the OpenAI API:
// openai itself or a preset.
func OpenAICompatible(typ string) bool {
	_, ok := Presets[typ]
	return ok || typ == "openai" || typ == ""
}

// BaseURL is where a provider of type typ is reached: baseURL when set,
// else the preset's or OpenAI's own address.
func BaseURL(typ, baseURL string) string {
	if baseURL != "" {
		return baseURL
	}
	if p, ok := Presets[typ]; ok {
		return p.BaseURL
	}
	if typ == "openai" || typ == "" {
		return "https://api.openai.com/v1"
	}
	return ""
}
