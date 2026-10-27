// Package guard checks what the agent is about to do against rules the user
// writes in guard.yaml, before any approval is asked for. A deny rule blocks
// a call outright, whatever the mode or the user's earlier answers; an ask
// rule makes the user approve it; an allow rule lets a command run without
// asking.
//
// Rules come from the user's guard.yaml and the project's .hm/guard.yaml.
// A project can only tighten them: its deny and ask rules count, its allow
// rules and allowed hosts do not, since a cloned repository should not be
// able to loosen what the agent may do.
package guard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mirzakhany/hm/internal/agent/tools"
)

// Rules is one guard.yaml.
type Rules struct {
	Paths struct {
		// Deny: files the agent may not read, write or delete.
		Deny []string `yaml:"deny"`
		// ReadOnly: files the agent may read but not change.
		ReadOnly []string `yaml:"read_only"`
	} `yaml:"paths"`
	Commands struct {
		// Patterns match a whole command line and each part of it between
		// &&, ||, ;, | and newlines: a glob where * is any text, or a
		// regular expression after "re:".
		Deny  []string `yaml:"deny"`
		Ask   []string `yaml:"ask"`
		Allow []string `yaml:"allow"`
	} `yaml:"commands"`
	Network struct {
		// AllowHosts, when set, are the only hosts the agent may reach.
		AllowHosts []string `yaml:"allow_hosts"`
		DenyHosts  []string `yaml:"deny_hosts"`
	} `yaml:"network"`
	Tools struct {
		// Tool names, globs allowed (mcp_*_delete_*).
		Deny []string `yaml:"deny"`
		Ask  []string `yaml:"ask"`
	} `yaml:"tools"`
}

// Verdict is the guard's ruling on a call.
type Verdict int

const (
	Pass  Verdict = iota // no rule applies: the mode's policy decides
	Allow                // run without asking
	Ask                  // the user must approve
	Deny                 // blocked
)

// Guard is the rules in force for a workspace.
type Guard struct {
	root, home string

	pathDeny, pathRO          []*regexp.Regexp
	cmdDeny, cmdAsk, cmdAllow []matcher
	hostAllow, hostDeny       []matcher
	toolDeny, toolAsk         []matcher
	srcPathDeny, srcPathRO    []string
}

type matcher struct {
	src string
	re  *regexp.Regexp
}

// Load reads the user and project guard files (either may be missing) for a
// workspace at root.
func Load(userFile, projectFile, root string) (*Guard, error) {
	user, err := read(userFile)
	if err != nil {
		return nil, err
	}
	proj, err := read(projectFile)
	if err != nil {
		return nil, err
	}
	// The project tightens only.
	proj.Commands.Allow = nil
	proj.Network.AllowHosts = nil
	return New(root, user, proj)
}

func read(path string) (Rules, error) {
	var r Rules
	if path == "" {
		return r, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil && !errors.Is(err, io.EOF) {
		return r, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// New builds a guard from rule sets, which add up.
func New(root string, sets ...Rules) (*Guard, error) {
	home, _ := os.UserHomeDir()
	g := &Guard{root: root, home: home}
	var errs []error
	for _, r := range sets {
		for _, p := range r.Paths.Deny {
			g.pathDeny = append(g.pathDeny, g.pathRegexp(p))
			g.srcPathDeny = append(g.srcPathDeny, p)
		}
		for _, p := range r.Paths.ReadOnly {
			g.pathRO = append(g.pathRO, g.pathRegexp(p))
			g.srcPathRO = append(g.srcPathRO, p)
		}
		add := func(dst *[]matcher, pats []string) {
			for _, p := range pats {
				m, err := newMatcher(p)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				*dst = append(*dst, m)
			}
		}
		add(&g.cmdDeny, r.Commands.Deny)
		add(&g.cmdAsk, r.Commands.Ask)
		add(&g.cmdAllow, r.Commands.Allow)
		add(&g.hostAllow, r.Network.AllowHosts)
		add(&g.hostDeny, r.Network.DenyHosts)
		add(&g.toolDeny, r.Tools.Deny)
		add(&g.toolAsk, r.Tools.Ask)
	}
	return g, errors.Join(errs...)
}

// newMatcher compiles a glob (* is any text) or "re:" regular expression
// that must match the whole string.
func newMatcher(p string) (matcher, error) {
	if rest, ok := strings.CutPrefix(p, "re:"); ok {
		re, err := regexp.Compile("^(?:" + rest + ")$")
		if err != nil {
			return matcher{}, fmt.Errorf("guard pattern %q: %w", p, err)
		}
		return matcher{p, re}, nil
	}
	var b strings.Builder
	b.WriteString("^")
	for _, r := range p {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return matcher{p, regexp.MustCompile(b.String())}, nil
}

func (m matcher) match(s string) bool { return m.re.MatchString(s) }

func firstMatch(ms []matcher, s string) (string, bool) {
	for _, m := range ms {
		if m.match(s) {
			return m.src, true
		}
	}
	return "", false
}

// pathRegexp compiles a path glob: ** is any number of folders, * any name
// part. A relative pattern is under the workspace and matches at any depth
// when it has no slash (".env" is every .env); ~ is the home folder.
func (g *Guard) pathRegexp(p string) *regexp.Regexp {
	p = filepath.ToSlash(p)
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		p = filepath.ToSlash(g.home) + p[1:]
	case !strings.HasPrefix(p, "/"):
		if !strings.Contains(p, "/") {
			p = "**/" + p
		}
		p = filepath.ToSlash(g.root) + "/" + p
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(p); i++ {
		switch {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i++
		case p[i] == '*':
			b.WriteString("[^/]*")
		case p[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(p[i : i+1]))
		}
	}
	b.WriteString("(?:/.*)?$") // a folder covers what is in it
	return regexp.MustCompile(b.String())
}

func matchPath(res []*regexp.Regexp, src []string, path string) (string, bool) {
	p := filepath.ToSlash(path)
	for i, re := range res {
		if re.MatchString(p) {
			return src[i], true
		}
	}
	return "", false
}

// Check rules on one tool call and says why when the rules decide it.
func (g *Guard) Check(tool string, plan tools.Plan) (Verdict, string) {
	if g == nil {
		return Pass, ""
	}
	if pat, ok := firstMatch(g.toolDeny, tool); ok {
		return Deny, fmt.Sprintf("the tool %s is denied by %q", tool, pat)
	}
	// Files the call touches, edits included.
	paths := append([]string(nil), plan.Paths...)
	for _, e := range plan.Edits {
		paths = append(paths, e.Path)
	}
	changes := len(plan.Edits) > 0 || plan.Category == tools.CatEdit || plan.Category == tools.CatDelete
	for _, p := range paths {
		if pat, ok := matchPath(g.pathDeny, g.srcPathDeny, p); ok {
			return Deny, fmt.Sprintf("%s is denied by %q", g.rel(p), pat)
		}
		if changes {
			if pat, ok := matchPath(g.pathRO, g.srcPathRO, p); ok {
				return Deny, fmt.Sprintf("%s is read-only by %q", g.rel(p), pat)
			}
		}
	}
	for _, h := range plan.Hosts {
		h = strings.ToLower(h)
		if pat, ok := firstMatch(g.hostDeny, h); ok {
			return Deny, fmt.Sprintf("the host %s is denied by %q", h, pat)
		}
		if len(g.hostAllow) > 0 {
			if _, ok := firstMatch(g.hostAllow, h); !ok {
				return Deny, fmt.Sprintf("the host %s is not in network.allow_hosts", h)
			}
		}
	}
	verdict, why := Pass, ""
	if plan.Command != "" {
		parts := append([]string{strings.TrimSpace(plan.Command)}, Split(plan.Command)...)
		for _, part := range parts {
			if pat, ok := firstMatch(g.cmdDeny, part); ok {
				return Deny, fmt.Sprintf("%q is denied by %q", part, pat)
			}
		}
		for _, part := range parts {
			if pat, ok := firstMatch(g.cmdAsk, part); ok {
				verdict, why = Ask, fmt.Sprintf("%q matches %q in guard.yaml", part, pat)
				break
			}
		}
		// Allow needs every part allowed, so "go test && rm -rf x" is not.
		if verdict == Pass && len(g.cmdAllow) > 0 {
			all := true
			for _, part := range parts[1:] {
				if _, ok := firstMatch(g.cmdAllow, part); !ok {
					all = false
					break
				}
			}
			if all {
				verdict, why = Allow, "allowed in guard.yaml"
			}
		}
	}
	if verdict == Pass {
		if pat, ok := firstMatch(g.toolAsk, tool); ok {
			verdict, why = Ask, fmt.Sprintf("the tool %s matches %q in guard.yaml", tool, pat)
		}
	}
	return verdict, why
}

func (g *Guard) rel(p string) string {
	if r, err := filepath.Rel(g.root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

// Split cuts a shell command line into the commands it runs: at &&, ||,
// ;, |, & and newlines outside quotes, and into $(...) and backquoted
// substitutions.
func Split(cmd string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '\\' && i+1 < len(cmd):
			cur.WriteByte(c)
			cur.WriteByte(cmd[i+1])
			i++
		case c == '\'' || c == '"':
			quote = c
			cur.WriteByte(c)
		case c == ';' || c == '\n' || c == '|' || c == '&':
			flush()
			if i+1 < len(cmd) && (cmd[i+1] == c) {
				i++ // && ||
			}
		case c == '`':
			end := strings.IndexByte(cmd[i+1:], '`')
			if end >= 0 {
				out = append(out, Split(cmd[i+1:i+1+end])...)
				i += end + 1
			}
		case c == '$' && i+1 < len(cmd) && cmd[i+1] == '(':
			depth, j := 1, i+2
			for ; j < len(cmd) && depth > 0; j++ {
				switch cmd[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
			}
			inner := cmd[i+2 : j]
			if depth == 0 {
				inner = cmd[i+2 : j-1]
			}
			out = append(out, Split(inner)...)
			cur.WriteString("$(...)")
			i = j - 1
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}
