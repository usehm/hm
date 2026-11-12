// Package host wires hm's agent together from config: the provider, the
// guard, the tools of a mode, skills, MCP, instruction files and
// sub-agents. The editor and the headless runner (pkg/hm/agent) both build
// their agents here, so they behave the same. It does not import the UI
// toolkit.
package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usehm/hm/internal/agent"
	"github.com/usehm/hm/internal/agent/guard"
	"github.com/usehm/hm/internal/agent/mcp"
	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/agent/skills"
	"github.com/usehm/hm/internal/agent/tools"
	"github.com/usehm/hm/internal/config"
)

// Host is what agents of one workspace are built from. It is cheap to
// make; the editor makes one per use so it always sees the current config.
type Host struct {
	Root     string
	Config   config.Config
	Projects *agent.Projects // nil: sessions have no project
	MCP      *mcp.Manager    // nil: no MCP tools
	// Warn is told about things the user should fix that do not stop the
	// agent, such as a skill that does not read; nil drops them.
	Warn func(msg string)
	// Registry is the model registry; nil reads it with LoadModels the
	// first time it is needed.
	Registry *provider.Registry
}

func (h *Host) warn(msg string) {
	if h.Warn != nil {
		h.Warn(msg)
	}
}

// BuiltinSkillsDir is where the bundled skills are written for the agent.
func BuiltinSkillsDir() string { return filepath.Join(config.UserDir(), "skills-builtin") }

// MaterializeSkills writes the bundled skills to BuiltinSkillsDir.
func MaterializeSkills() error { return skills.Materialize(BuiltinSkillsDir()) }

// UserGuardPath is the user's guard.yaml.
func UserGuardPath() string { return filepath.Join(config.UserDir(), "guard.yaml") }

// ProjectGuardPath is a workspace's guard.yaml.
func ProjectGuardPath(root string) string { return filepath.Join(root, ".hm", "guard.yaml") }

// Guard reads the user's and the workspace's guard.yaml. A broken file is
// an error: the agent must not run unguarded.
func (h *Host) Guard() (*guard.Guard, error) {
	g, err := guard.Load(UserGuardPath(), ProjectGuardPath(h.Root), h.Root)
	if err != nil {
		return nil, errors.New("guard.yaml: " + err.Error())
	}
	return g, nil
}

// LoadModels reads hm's model table with the user's and the workspace's
// models.json on top. A file that does not read is left out and reported.
func LoadModels(root string) (*provider.Registry, error) {
	proj := ""
	if root != "" {
		proj = config.ProjectPath(root)
	}
	return provider.Builtin().Merge(config.ModelsPaths(config.UserPath(), proj)...)
}

// Models is the model registry.
func (h *Host) Models() *provider.Registry {
	if h.Registry == nil {
		var err error
		h.Registry, err = LoadModels(h.Root)
		if err != nil {
			h.warn(err.Error())
		}
	}
	return h.Registry
}

// Modes resolves the configured modes for the workspace.
func (h *Host) Modes() map[string]agent.Mode { return agent.Modes(h.Config.Modes, h.Root) }

// Provider builds the named provider, resolving its key.
func (h *Host) Provider(name string) (provider.Provider, config.Provider, error) {
	pc, ok := h.Config.Agent.Providers[name]
	if !ok {
		return nil, pc, fmt.Errorf("no provider named %q in agent.providers", name)
	}
	p, err := BuildProvider(name, pc)
	return p, pc, err
}

// BuildProvider resolves pc's key and builds the provider. Reading the
// keychain runs a process, so callers off the UI goroutine are welcome.
func BuildProvider(name string, pc config.Provider) (provider.Provider, error) {
	key := ""
	if pc.APIKey != "" {
		var err error
		key, err = config.Resolve(pc.APIKey)
		if err != nil && keyOptional(pc) && strings.HasPrefix(pc.APIKey, "keychain:") {
			key, err = "", nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s API key: %w; add it in Settings → Agent", name, err)
		}
	}
	project, err := config.Resolve(pc.Project) // "${GOOGLE_CLOUD_PROJECT}"
	if err != nil {
		return nil, fmt.Errorf("%s project: %w", name, err)
	}
	return provider.New(provider.Config{Type: pc.Type, BaseURL: pc.BaseURL, APIKey: key, Project: project, Location: pc.Location,
		APIVersion: pc.APIVersion})
}

// keyOptional reports whether a provider finds credentials of its own
// when it has no key: the Anthropic SDK (ANTHROPIC_API_KEY, an `ant auth
// login` profile) and Vertex AI (Google Cloud's default credentials).
func keyOptional(pc config.Provider) bool {
	return pc.Type == "anthropic" || pc.Type == "google" && pc.Project != ""
}

// ModelOf is the model a provider runs, with the Anthropic default filled
// in.
func (h *Host) ModelOf(name string) string {
	pc := h.Config.Agent.Providers[name]
	if pc.Model == "" && pc.Type == "anthropic" {
		return provider.DefaultAnthropicModel
	}
	return pc.Model
}

// ContextWindow is the window of the provider's model in tokens: the
// provider's context_window, else the model registry; 0 when unknown.
func (h *Host) ContextWindow(name string) int { return h.ContextWindowOf(name, "") }

// ContextWindowOf is ContextWindow for provider name running model, or
// its own model when model is empty. context_window is for the
// provider's own model only.
func (h *Host) ContextWindowOf(name, model string) int {
	if model == "" || model == h.ModelOf(name) {
		if w := h.Config.Agent.Providers[name].ContextWindow; w > 0 {
			return w
		}
		model = h.ModelOf(name)
	}
	m, _ := h.Models().Lookup(name, model)
	return m.ContextWindow
}

// Compaction is how agents of provider name, built as prov, compact: the
// summary is written by agent.compaction.provider and model when set.
func (h *Host) Compaction(name string, prov provider.Provider) (*agent.CompactConfig, error) {
	cc := h.Config.Agent.Compaction
	sumName, sumProv := name, prov
	if cc.Provider != "" && cc.Provider != name {
		p, _, err := h.Provider(cc.Provider)
		if err != nil {
			return nil, errors.New("agent.compaction.provider: " + err.Error())
		}
		sumName, sumProv = cc.Provider, p
	}
	model := cc.Model
	if model == "" {
		model = h.ModelOf(sumName)
	}
	return &agent.CompactConfig{
		Auto: cc.Enabled, Threshold: cc.Threshold, Reserve: cc.ReserveTokens, KeepTokens: cc.KeepTokens,
		Compactor: agent.ModelCompactor{Provider: h.wrap(sumName, sumProv), Model: model},
	}, nil
}

// wrap adds retries and pricing to provider name's calls.
func (h *Host) wrap(name string, p provider.Provider) provider.Provider {
	return provider.WithPricing(provider.WithRetry(p, h.Config.Agent.Retry.MaxAttempts), h.Models(), name)
}

// Context reads the instruction files (AGENTS.md, SYSTEM.md and the like)
// for a new session; empty when agent.context is off.
func (h *Host) Context() *agent.Context {
	c := &agent.Context{}
	if cfg := h.Config.Agent.Context; cfg.Enabled {
		home, _ := os.UserHomeDir()
		*c = agent.LoadContext(h.Root, home, config.UserDir(),
			agent.ContextOptions{Names: cfg.Files, MaxBytes: cfg.MaxBytes})
	}
	return c
}

// Resume loads a saved session and recovers it.
func Resume(st *agent.Store, id string) (*agent.Session, error) {
	s, err := st.Load(id)
	if err != nil {
		return nil, err
	}
	Recover(st, s)
	return s, nil
}

// Recover marks a session saved mid-turn by a run of hm that is gone as
// interrupted and repairs it, so it can go on.
func Recover(st *agent.Store, s *agent.Session) {
	if s.Status == agent.StatusRunning {
		s.Status = agent.StatusInterrupted
		s.Repair()
		_ = st.Save(s)
	}
}

// Turn is what one agent turn is built for. The host fills in the rest
// from config.
type Turn struct {
	Session  *agent.Session
	Mode     string
	Provider string
	// Model overrides the provider's configured model; empty keeps it.
	Model string
	// Effort overrides the provider's effort; empty keeps it.
	Effort string
	Grants *agent.Grants // "allow for this session" answers; nil: none
	Env    tools.Env
	// Context is the session's instruction files, read once with Context
	// so its system prompt stays the same and caches.
	Context *agent.Context
	Tools   ToolsetOptions
	Hooks   agent.Hooks
	// MaxSteps bounds the turn's model calls; 0 is the agent's default.
	MaxSteps int
}

// Build makes the agent for one turn: the provider, guard, tools, policy
// and system prompt as they are now. guard.yaml is read each time, so
// edits apply to the next message.
func (h *Host) Build(t Turn) (*agent.Agent, error) {
	mode, ok := h.Modes()[t.Mode]
	if !ok {
		return nil, errors.New("Unknown mode " + t.Mode + "; pick one with /mode")
	}
	prov, pc, err := h.Provider(t.Provider)
	if err != nil {
		return nil, err
	}
	grd, err := h.Guard()
	if err != nil {
		return nil, err
	}
	model, effort := pc.Model, pc.Effort
	if t.Model != "" {
		model = t.Model
	}
	if t.Effort != "" {
		effort = t.Effort
	}
	s := t.Session
	s.SetModel(t.Mode, t.Provider, model, effort)
	topts := t.Tools
	topts.Mode = mode
	ts := h.Toolset(topts)
	policy := agent.NewPolicy(h.Root, ts.Rules)
	policy.ReadRoots = ts.ReadRoots
	if t.Grants != nil {
		policy.Grants = t.Grants
	}
	var project *agent.Project
	if h.Projects != nil {
		if p, ok := h.Projects.Get(s.ProjectID); ok {
			project = &p
		}
	}
	extra := ts.Extra
	if topts.Subagent {
		extra = SubagentPrompt(topts.Write) + "\n" + extra
	}
	compaction, err := h.Compaction(t.Provider, prov)
	if err != nil {
		return nil, err
	}
	ctx := t.Context
	if ctx == nil {
		ctx = &agent.Context{}
	}
	return &agent.Agent{
		Provider: h.wrap(t.Provider, prov),
		Model:    model, Effort: effort, ContextWindow: h.ContextWindowOf(t.Provider, model),
		Compaction: compaction,
		System:     agent.System(mode, h.Root, s.Created, project, ctx, extra),
		Tools:      ts.Reg, Policy: policy, Guard: grd, Env: t.Env,
		Parallel: map[string]bool{"task": true},
		Hooks:    t.Hooks, MaxSteps: t.MaxSteps,
		NoVision: !h.Models().Vision(t.Provider, model),
	}, nil
}
