package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ModelInfo is what hm knows about a model: its limits, its price and what
// it can do. Every field is optional; a zero field is filled from a less
// specific entry (see Registry.Lookup).
type ModelInfo struct {
	// ID is the model id as the API takes it, or the start of ids, so
	// "claude-opus-5" also matches "claude-opus-5-20261101".
	ID string `json:"id"`
	// Provider scopes the entry to one agent.providers name; empty matches
	// any provider serving the id.
	Provider string `json:"provider,omitempty"`
	// Name is shown instead of the id.
	Name          string   `json:"name,omitempty"`
	ContextWindow int      `json:"context_window,omitempty"` // tokens a request may hold, prompt and answer together
	MaxOutput     int      `json:"max_output,omitempty"`     // tokens one answer may have
	Price         Price    `json:"price,omitzero"`
	Supports      Supports `json:"supports,omitzero"`
	// Efforts are the effort levels the model takes; empty means any.
	Efforts []string `json:"efforts,omitempty"`
}

// Price is USD per million tokens. Input counts tokens outside the cache.
type Price struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

func (p Price) known() bool { return p != Price{} }

// Supports says what a model can do; nil is unknown.
type Supports struct {
	Tools    *bool `json:"tools,omitempty"`
	Images   *bool `json:"images,omitempty"`
	Thinking *bool `json:"thinking,omitempty"`
}

// vision marks the models that take pictures.
var vision = Supports{Images: new(true)}

// builtin are the models hm knows. Prices are Anthropic first-party rates;
// a 5-minute cache write costs 1.25x input. Override or add to them in
// models.json.
var builtin = []ModelInfo{
	{ID: "claude-fable-5-1", ContextWindow: 1_000_000, MaxOutput: 128_000, Price: Price{10, 50, 0.25, 12.5}, Supports: vision},
	{ID: "claude-mythos-5-1", ContextWindow: 1_000_000, MaxOutput: 128_000, Price: Price{10, 50, 0.25, 12.5}, Supports: vision},
	{ID: "claude-fable-5", ContextWindow: 1_000_000, MaxOutput: 128_000, Price: Price{10, 50, 1, 12.5}, Supports: vision},
	{ID: "claude-mythos-5", ContextWindow: 1_000_000, MaxOutput: 128_000, Supports: vision},
	{ID: "claude-opus-5-5", ContextWindow: 1_000_000, MaxOutput: 128_000, Price: Price{4, 20, 0.20, 5}, Supports: vision},
	{ID: "claude-opus-5", ContextWindow: 1_000_000, MaxOutput: 128_000, Price: Price{5, 25, 0.5, 6.25}, Supports: vision},
	{ID: "claude-sonnet-5-5", ContextWindow: 1_000_000, MaxOutput: 64_000, Price: Price{2, 10, 0.20, 2.5}, Supports: vision},
	{ID: "claude-sonnet-5", ContextWindow: 1_000_000, MaxOutput: 64_000, Price: Price{2, 10, 0.20, 2.5}, Supports: vision},
	{ID: "claude-haiku-5-5", ContextWindow: 1_000_000, MaxOutput: 64_000, Price: Price{0.10, 0.50, 0.01, 0.125}, Supports: vision},
	{ID: "claude-haiku-5", ContextWindow: 1_000_000, MaxOutput: 64_000, Supports: vision},
	{ID: "claude-opus-4-8", ContextWindow: 1_000_000, MaxOutput: 128_000, Price: Price{5, 25, 0.5, 6.25}, Supports: vision},
	{ID: "claude-opus-4-7", ContextWindow: 1_000_000, MaxOutput: 128_000, Price: Price{5, 25, 0.5, 6.25}, Supports: vision},
	{ID: "claude-opus-4-6", ContextWindow: 1_000_000, MaxOutput: 128_000, Price: Price{5, 25, 0.5, 6.25}, Supports: vision},
	{ID: "claude-sonnet-4-6", ContextWindow: 1_000_000, MaxOutput: 64_000, Price: Price{3, 15, 0.3, 3.75}, Supports: vision},
	{ID: "claude-haiku-4-5", ContextWindow: 200_000, MaxOutput: 64_000, Price: Price{1, 5, 0.1, 1.25}, Supports: vision},
	{ID: "gemini-3", ContextWindow: 1_048_576, MaxOutput: 65_536, Supports: vision},
	{ID: "gemini-2.5", ContextWindow: 1_048_576, MaxOutput: 65_536, Supports: vision},
	{ID: "gpt-4o", ContextWindow: 128_000, MaxOutput: 16_384, Supports: vision},
	{ID: "gpt-4.1", ContextWindow: 1_047_576, MaxOutput: 32_768, Supports: vision},
	{ID: "gpt-5", ContextWindow: 400_000, MaxOutput: 128_000, Supports: vision},
	{ID: "o3", ContextWindow: 200_000, MaxOutput: 100_000, Supports: vision},
	{ID: "o4-mini", ContextWindow: 200_000, MaxOutput: 100_000, Supports: vision},
}

// Registry is a set of model entries: hm's own, then models.json files on
// top.
type Registry struct {
	entries []ModelInfo // later entries win over earlier ones with the same key
}

var builtinRegistry = &Registry{entries: builtin}

// Builtin is hm's own table.
func Builtin() *Registry { return builtinRegistry }

// Merge returns r with the entries of each models.json file added, a later
// file winning. A missing file is skipped; a file that does not read is
// reported and skipped, the others still apply.
func (r *Registry) Merge(files ...string) (*Registry, error) {
	out := &Registry{entries: append([]ModelInfo(nil), r.entries...)}
	var errs []error
	for _, f := range files {
		data, err := os.ReadFile(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		var ms []ModelInfo
		if err == nil {
			err = json.Unmarshal(data, &ms)
		}
		for i, m := range ms {
			if err == nil && m.ID == "" {
				err = fmt.Errorf("model %d has no id", i+1)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f, err))
			continue
		}
		out.entries = append(out.entries, ms...)
	}
	return out, errors.Join(errs...)
}

// normalize drops what hosts put before a model id: Bedrock's region and
// vendor ("us.anthropic.") and gateways' "vendor/" (openai/gpt-5).
func normalize(model string) string {
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	if i := strings.Index(model, "anthropic."); i >= 0 {
		model = model[i+len("anthropic."):]
	}
	return model
}

// Lookup returns what is known of provider prov's model, and false when
// nothing is. Each field comes from the most specific entry that sets it:
// one for prov with the exact id, one for any provider with the exact id,
// then the same for the longest matching id prefix. Among entries of the
// same rank the later one wins.
func (r *Registry) Lookup(prov, model string) (ModelInfo, bool) {
	ids := []string{model}
	if n := normalize(model); n != model {
		ids = append(ids, n)
	}
	type ranked struct {
		m    ModelInfo
		rank int // lower is more specific
		len  int // matched id length, for prefixes
		idx  int
	}
	var hits []ranked
	for i, e := range r.entries {
		if e.Provider != "" && e.Provider != prov {
			continue
		}
		scoped := 0
		if e.Provider == "" {
			scoped = 1
		}
		for _, id := range ids {
			if id == e.ID {
				hits = append(hits, ranked{e, scoped, len(e.ID), i})
				break
			}
			if strings.HasPrefix(id, e.ID) {
				hits = append(hits, ranked{e, 2 + scoped, len(e.ID), i})
				break
			}
		}
	}
	if len(hits) == 0 {
		return ModelInfo{ID: model, Provider: prov}, false
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.len != b.len {
			return a.len > b.len
		}
		return a.idx > b.idx
	})
	out := ModelInfo{ID: model, Provider: prov}
	for _, h := range hits {
		m := h.m
		if out.Name == "" {
			out.Name = m.Name
		}
		if out.ContextWindow == 0 {
			out.ContextWindow = m.ContextWindow
		}
		if out.MaxOutput == 0 {
			out.MaxOutput = m.MaxOutput
		}
		if !out.Price.known() {
			out.Price = m.Price
		}
		if out.Supports.Tools == nil {
			out.Supports.Tools = m.Supports.Tools
		}
		if out.Supports.Images == nil {
			out.Supports.Images = m.Supports.Images
		}
		if out.Supports.Thinking == nil {
			out.Supports.Thinking = m.Supports.Thinking
		}
		if out.Efforts == nil {
			out.Efforts = m.Efforts
		}
	}
	return out, true
}

// Vision reports whether provider prov's model takes pictures. A model
// nothing is known of is trusted to; models.json says otherwise with
// "supports": {"images": false}.
func (r *Registry) Vision(prov, model string) bool {
	m, _ := r.Lookup(prov, model)
	return m.Supports.Images == nil || *m.Supports.Images
}

// Listed returns the models of entries scoped to provider prov, for a
// model picker: what an API's own list may not name, such as a local
// model. Entries for any provider only describe models.
func (r *Registry) Listed(prov string) []ModelInfo {
	seen := map[string]bool{}
	var out []ModelInfo
	for i := len(r.entries) - 1; i >= 0; i-- {
		e := r.entries[i]
		if e.Provider != prov || prov == "" || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		m, _ := r.Lookup(prov, e.ID)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Cost prices u, or 0 when the price is unknown. Input excludes cache
// reads and writes, as every adapter reports it.
func (m ModelInfo) Cost(u Usage) float64 {
	p := m.Price
	return (float64(u.Input)*p.Input + float64(u.Output)*p.Output +
		float64(u.CacheRead)*p.CacheRead + float64(u.CacheWrite)*p.CacheWrite) / 1e6
}
