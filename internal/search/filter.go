package search

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// glob is a compiled path glob. A pattern without a slash matches a name at
// any depth, as in .gitignore and ripgrep.
type glob struct {
	raw string
	re  *regexp.Regexp
}

func compileGlob(pattern string) (glob, bool) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return glob{}, false
	}
	anchored := strings.Contains(strings.TrimSuffix(p, "/"), "/")
	p = strings.TrimPrefix(strings.TrimSuffix(p, "/"), "/")
	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '*' && i+1 < len(p) && p[i+1] == '*':
			i++
			if i+1 < len(p) && p[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := p[i+1 : i+j]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			b.WriteString("[" + class + "]")
			i += j
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	// A folder pattern also covers everything inside it.
	b.WriteString("(?:/.*)?$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return glob{}, false
	}
	return glob{raw: pattern, re: re}, true
}

func (g glob) match(rel string) bool { return g.re.MatchString(filepath.ToSlash(rel)) }

func compileGlobs(list string) []glob {
	var out []glob
	for _, p := range strings.Split(list, ",") {
		if g, ok := compileGlob(p); ok {
			out = append(out, g)
		}
	}
	return out
}

// filter decides which files a search reads.
type filter struct {
	include, exclude []glob
}

func newFilter(q Query, alwaysExclude []string) *filter {
	f := &filter{include: compileGlobs(q.Include), exclude: compileGlobs(q.Exclude)}
	for _, p := range alwaysExclude {
		if g, ok := compileGlob(p); ok {
			f.exclude = append(f.exclude, g)
		}
	}
	return f
}

func (f *filter) skipDir(rel string) bool {
	for _, g := range f.exclude {
		if g.match(rel) {
			return true
		}
	}
	return false
}

func (f *filter) wants(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	if f.skipDir(rel) {
		return false
	}
	if len(f.include) == 0 {
		return true
	}
	for _, g := range f.include {
		if g.match(rel) {
			return true
		}
	}
	return false
}

// ignoreRule is one .gitignore line.
type ignoreRule struct {
	base    string // folder of the .gitignore, relative to the root
	g       glob
	negate  bool
	dirOnly bool
}

// ignores is the stack of .gitignore rules seen on the way down.
type ignores []ignoreRule

// load adds the rules of dir/.gitignore (dir relative to root).
func (ig ignores) load(root, dir string) ignores {
	data, err := os.ReadFile(filepath.Join(root, dir, ".gitignore"))
	if err != nil {
		return ig
	}
	out := append(ignores(nil), ig...)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{base: dir}
		if strings.HasPrefix(line, "!") {
			r.negate, line = true, line[1:]
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
		}
		g, ok := compileGlob(line)
		if !ok {
			continue
		}
		r.g = g
		out = append(out, r)
	}
	return out
}

// ignored reports whether rel (relative to root) is ignored; the last
// matching rule wins.
func (ig ignores) ignored(rel string, isDir bool) bool {
	ignored := false
	for _, r := range ig {
		if r.dirOnly && !isDir {
			continue
		}
		sub := rel
		if r.base != "" && r.base != "." {
			s, err := filepath.Rel(r.base, rel)
			if err != nil || strings.HasPrefix(s, "..") {
				continue
			}
			sub = s
		}
		if r.g.re.MatchString(filepath.ToSlash(sub)) {
			// A folder rule matched through a parent ("build/x.go" against
			// "build/") is handled when the walk skips the folder itself.
			ignored = !r.negate
		}
	}
	return ignored
}

// MatchGlob reports whether the slash-separated relative path matches the
// glob, with the same rules as include and exclude filters.
func MatchGlob(pattern, rel string) bool {
	g, ok := compileGlob(pattern)
	return ok && g.match(rel)
}
