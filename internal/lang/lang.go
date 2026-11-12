// Package lang turns the languages section of the config into language
// servers, highlighters and formatter chains, and reports what goes wrong
// with them.
package lang

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mirzakhany/yoga/highlight"
	"github.com/mirzakhany/yoga/lsp"
	"github.com/mirzakhany/yoga/ui"

	"github.com/usehm/hm/internal/config"
)

// Language is one configured language.
type Language struct {
	Name string
	config.Language
}

// Service owns the language table and the language-server manager.
type Service struct {
	mgr *lsp.Manager

	mu       sync.Mutex
	langs    map[string]config.Language
	byExt    map[string]string // extension or file name -> language name
	lspExts  map[string]bool   // extensions and file names registered with lsp
	hlExts   map[string]bool   // extensions and file names registered with highlight
	pending  []*lsp.ServerError
	notices  []Notice
	last     map[string]time.Time
	wake     func()
	pathDone bool
	missing  []*lsp.ServerError // not-found errors held until PATH is fixed
}

// Notice is a language problem for the notification center.
type Notice struct {
	Title, Message string
	Warning        bool
	// Toast is set for the first notice of its kind in a while; repeats go
	// to the notification history only.
	Toast bool
	// Missing names a tool that is not installed, with ways to install it.
	Missing *Missing
}

// Missing is a tool that could not be found.
type Missing struct {
	Language string
	Command  string
	Install  []config.Installer
}

// New returns a service using the editor's shared language-server manager.
func New(wake func()) *Service {
	s := &Service{
		mgr:     ui.LanguageServers(),
		langs:   map[string]config.Language{},
		byExt:   map[string]string{},
		lspExts: map[string]bool{},
		hlExts:  map[string]bool{},
		last:    map[string]time.Time{},
		wake:    wake,
	}
	s.mgr.OnError(s.onError)
	s.mgr.OnActivity(func() {
		if s.wake != nil {
			s.wake()
		}
	})
	return s
}

// Manager returns the language-server manager.
func (s *Service) Manager() *lsp.Manager { return s.mgr }

// FixPathAsync extends PATH from the login shell in the background, then
// retries servers that were reported missing before it finished.
func (s *Service) FixPathAsync() {
	go func() {
		FixPath()
		s.mu.Lock()
		s.pathDone = true
		missing := s.missing
		s.missing = nil
		s.mu.Unlock()
		for _, e := range missing {
			if _, err := exec.LookPath(e.Command); err == nil {
				s.mgr.Restart(e.LanguageID)
			} else {
				s.queue(e)
			}
		}
		if s.wake != nil {
			s.wake()
		}
	}()
}

// MarkPathReady is FixPathAsync for tests and headless runs, which keep PATH.
func (s *Service) MarkPathReady() {
	s.mu.Lock()
	s.pathDone = true
	s.mu.Unlock()
}

func (s *Service) onError(e *lsp.ServerError) {
	s.mu.Lock()
	if e.Kind == lsp.ErrNotFound && !s.pathDone {
		s.missing = append(s.missing, e)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.queue(e)
}

func (s *Service) queue(e *lsp.ServerError) {
	s.mu.Lock()
	s.pending = append(s.pending, e)
	s.mu.Unlock()
	if s.wake != nil {
		s.wake()
	}
}

// Apply installs the languages from cfg: language servers and highlighters
// by extension. Running servers restart when their settings changed. It
// returns what is wrong with the languages' highlight queries; those
// languages keep the built-in colors.
func (s *Service) Apply(langs map[string]config.Language) []error {
	var errs []error
	s.mu.Lock()
	old := s.langs
	s.langs = langs
	s.byExt = map[string]string{}
	for name, l := range langs {
		for _, ext := range keys(l) {
			s.byExt[ext] = name
		}
	}
	for ext := range s.lspExts {
		lsp.Unregister(ext)
	}
	for ext := range s.hlExts {
		highlight.Unregister(ext)
	}
	s.lspExts, s.hlExts = map[string]bool{}, map[string]bool{}
	for name, l := range langs {
		hl := l.Highlight
		if hl == "" {
			hl = name
		}
		queried := map[string]highlight.Factory{} // grammar -> factory with the language's queries
		for _, ext := range keys(l) {
			grammar := grammarFor(hl, ext)
			factory, ok := highlight.ByName(grammar)
			if len(l.HighlightQueries) > 0 {
				f, seen := queried[grammar]
				if !seen {
					var err error
					if f, err = queryFactory(grammar, l.HighlightQueries); err != nil {
						errs = append(errs, fmt.Errorf("languages.%s.highlight_queries: %w", name, err))
					}
					queried[grammar] = f
				}
				if f != nil {
					factory, ok = f, true
				}
			}
			if ok {
				highlight.Register(ext, factory)
				s.hlExts[ext] = true
			}
			if l.LSP == nil || l.LSP.Disabled || l.LSP.Command == "" {
				continue
			}
			lsp.Register(ext, serverConfig(name, ext, l))
			s.lspExts[ext] = true
		}
	}
	s.mu.Unlock()

	var restart []string
	for name, l := range langs {
		if o, ok := old[name]; ok && !reflect.DeepEqual(o.LSP, l.LSP) || !ok && l.LSP != nil {
			restart = append(restart, languageIDs(name, l)...)
		}
	}
	for name, l := range old {
		if _, ok := langs[name]; !ok {
			restart = append(restart, languageIDs(name, l)...)
		}
	}
	sort.Strings(restart)
	for i, id := range restart {
		if i == 0 || restart[i-1] != id {
			s.mgr.Restart(id)
		}
	}
	return errs
}

// keys returns the lowercase extensions and file names l is registered by.
func keys(l config.Language) []string {
	var out []string
	for _, k := range l.Extensions {
		out = append(out, strings.ToLower(k))
	}
	for _, k := range l.Filenames {
		out = append(out, strings.ToLower(k))
	}
	return out
}

// queryFactory colors grammar with its built-in query followed by the
// patterns in files, which win where they overlap.
func queryFactory(grammar string, files []string) (highlight.Factory, error) {
	var b strings.Builder
	b.WriteString(highlight.BuiltinQuery(grammar))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		b.WriteString("\n")
		b.Write(data)
	}
	return highlight.QueryFactory(grammar, b.String())
}

// grammarFor picks the grammar for one extension of a language: TypeScript
// files with JSX need the TSX grammar.
func grammarFor(name, ext string) string {
	if name == "typescript" && ext == ".tsx" {
		return "tsx"
	}
	return name
}

func serverConfig(name, ext string, l config.Language) lsp.ServerConfig {
	env := map[string]string{}
	for k, v := range l.LSP.Env {
		if r, err := config.Resolve(v); err == nil {
			env[k] = r
		}
	}
	return lsp.ServerConfig{
		LanguageID:            languageID(name, ext, l),
		Command:               config.ExpandHome(l.LSP.Command),
		Args:                  l.LSP.Args,
		RootMarkers:           l.LSP.RootMarkers,
		Env:                   env,
		InitializationOptions: l.LSP.Options,
		Settings:              l.LSP.Settings,
	}
}

func languageID(name, ext string, l config.Language) string {
	if id := l.LanguageIDs[ext]; id != "" {
		return id
	}
	if l.LanguageID != "" {
		return l.LanguageID
	}
	return name
}

func languageIDs(name string, l config.Language) []string {
	seen := map[string]bool{}
	var out []string
	for _, ext := range keys(l) {
		if id := languageID(name, ext, l); !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// For returns the language configured for path.
func (s *Service) For(path string) (Language, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := s.byExt[strings.ToLower(filepath.Base(path))]
	if !ok {
		name, ok = s.byExt[strings.ToLower(filepath.Ext(path))]
	}
	if !ok {
		return Language{}, false
	}
	return Language{Name: name, Language: s.langs[name]}, true
}

// Running reports whether path's language server is up.
func (s *Service) Running(path string) bool {
	cfg, ok := lsp.ForPath(path)
	if !ok {
		return false
	}
	for _, id := range s.mgr.Running() {
		if id == cfg.LanguageID {
			return true
		}
	}
	return false
}

// HasServer reports whether a language server is configured for path.
func (s *Service) HasServer(path string) bool {
	_, ok := lsp.ForPath(path)
	return ok
}

// Restart restarts every language server.
func (s *Service) Restart() { s.mgr.Restart("") }

// toastInterval spaces out repeated toasts for one problem.
const toastInterval = 2 * time.Minute

// Drain returns language problems since the last call; call it from Body.
func (s *Service) Drain() []Notice {
	s.mu.Lock()
	errs := s.pending
	s.pending = nil
	notices := s.notices
	s.notices = nil
	s.mu.Unlock()
	now := time.Now()
	for _, e := range errs {
		n := s.describe(e)
		key := e.LanguageID + "\x00" + e.Kind.String()
		s.mu.Lock()
		if now.Sub(s.last[key]) >= toastInterval {
			s.last[key] = now
			n.Toast = true
		}
		s.mu.Unlock()
		notices = append(notices, n)
	}
	return notices
}

// Notify queues a notice from elsewhere in the package, such as a formatter.
func (s *Service) Notify(n Notice) {
	s.mu.Lock()
	key := n.Title
	if time.Since(s.last[key]) >= toastInterval {
		s.last[key] = time.Now()
		n.Toast = true
	}
	s.notices = append(s.notices, n)
	s.mu.Unlock()
	if s.wake != nil {
		s.wake()
	}
}

func (s *Service) describe(e *lsp.ServerError) Notice {
	name := e.LanguageID
	var install []config.Installer
	s.mu.Lock()
	for n, l := range s.langs {
		if l.LSP != nil && l.LSP.Command == e.Command {
			name = n
			install = l.LSP.Install
			break
		}
	}
	s.mu.Unlock()
	title := strings.ToUpper(name[:1]) + name[1:] + " language server"
	n := Notice{}
	switch e.Kind {
	case lsp.ErrNotFound:
		n = Notice{Title: title + " not installed", Warning: true,
			Message: fmt.Sprintf("%s was not found on PATH.", e.Command),
			Missing: &Missing{Language: name, Command: e.Command, Install: install}}
	case lsp.ErrStart:
		n = Notice{Title: title + " failed to start", Message: e.Err.Error()}
	case lsp.ErrExited:
		n = Notice{Title: title + " stopped", Message: e.Err.Error() + ". Restart it from the command palette."}
	default:
		n = Notice{Title: title + " error", Message: e.Err.Error()}
	}
	if e.Stderr != "" {
		n.Message += "\n" + lastLines(e.Stderr, 6)
	}
	return n
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
