// Package export turns a session into one file to read or send: a
// self-contained HTML page, Markdown, or the raw JSON Lines the session is
// stored as. Secrets are redacted on the way out.
package export

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/mirzakhany/hm/internal/agent"
	"github.com/mirzakhany/hm/internal/agent/provider"
	"github.com/mirzakhany/hm/internal/diff"
)

// Format is the kind of file an export writes.
type Format string

const (
	HTML     Format = "html"
	Markdown Format = "md"
	JSONL    Format = "jsonl"
)

// ParseFormat reads a format's name, as typed after --format.
func ParseFormat(s string) (Format, bool) {
	switch strings.ToLower(strings.TrimPrefix(s, ".")) {
	case "html", "htm":
		return HTML, true
	case "md", "markdown":
		return Markdown, true
	case "jsonl", "json":
		return JSONL, true
	}
	return "", false
}

// Options say what an export holds and how it looks.
type Options struct {
	Format Format
	// AllBranches adds the branches off the active path, folded away where
	// they leave it.
	AllBranches bool
	// Palette colours the HTML page; the zero value uses DefaultPalette.
	Palette Palette
	// Redactor takes secrets out; nil redacts the usual key shapes only.
	Redactor *Redactor
	// Location is the time zone of the times shown; nil is local time.
	Location *time.Location
}

// Result is an export's file and what was redacted from it.
type Result struct {
	Data     []byte
	Redacted []Redaction
}

// Render exports s.
func Render(s *agent.Session, o Options) (Result, error) {
	r := o.Redactor
	if r == nil {
		r = NewRedactor()
	}
	t := r.tally()
	var data []byte
	var err error
	switch o.Format {
	case JSONL:
		data, err = jsonLines(s, t)
	case Markdown:
		data = markdown(build(s, o, t))
	default:
		data, err = page(build(s, o, t), o.Palette)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Data: data, Redacted: t.list()}, nil
}

// FileName is where an export of a session titled title goes by default:
// hm-<title>-<date>.<format>.
func FileName(title string, t time.Time, f Format) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			if b.Len() >= 50 {
				break
			}
			continue
		}
		dash = true
	}
	slug := b.String()
	if slug == "" {
		slug = "session"
	}
	if f == "" {
		f = HTML
	}
	return "hm-" + slug + "-" + t.Format("2006-01-02") + "." + string(f)
}

// doc is a session laid out for rendering, with its secrets already out.
type doc struct {
	Title, ID        string
	Started, Changed string // when it was created and last updated
	Models           []string
	Usage            provider.Usage
	Messages         int  // prompts and replies shown
	Branches         bool // other branches are included
	Items            []item
}

type itemKind int

const (
	itemUser itemKind = iota
	itemAssistant
	itemCompaction
	itemMarker // a mode or model change
	itemBranch
)

// item is one thing the conversation shows.
type item struct {
	Kind  itemKind
	When  string
	Label string
	// Model is what wrote an assistant message; Usage what it cost.
	Model string
	Usage *provider.Usage
	Parts []part
	// Text is a marker's words or a compaction's summary.
	Text       string
	Compaction *agent.Compaction
	Branch     *branch
}

// branch is a branch off the path shown, folded away.
type branch struct {
	Messages int
	Summary  string
	Items    []item
}

type partKind int

const (
	partText partKind = iota
	partThinking
	partTool
)

type part struct {
	Kind partKind
	Text string
	Tool *toolCall
}

// toolCall is a tool the model called and what came back.
type toolCall struct {
	Name, Arg string
	Input     string // pretty-printed JSON
	Result    string
	Done      bool // a result came back
	Error     bool
	Diffs     []fileDiff
}

// fileDiff is the change an edit or write made to one file.
type fileDiff struct {
	Path  string
	Lines []diffLine
	More  int // lines left out of a long write
}

type diffLine struct {
	Op   byte // ' ', '+' or '-'
	Text string
}

// maxWriteLines is how much of a written file the export shows.
const maxWriteLines = 400

// builder lays out a session.
type builder struct {
	t       *tally
	loc     *time.Location
	entries []agent.Entry
	kids    map[string][]int // parent ID → positions in entries
	results map[string]provider.Block
	labels  map[string]string
	notes   map[string]agent.BranchSummary
}

func build(s *agent.Session, o Options, t *tally) doc {
	b := &builder{t: t, loc: o.Location, entries: s.Entries(), kids: map[string][]int{}, results: map[string]provider.Block{},
		labels: s.Labels(), notes: s.BranchSummaries()}
	for i, e := range b.entries {
		b.kids[e.ParentID] = append(b.kids[e.ParentID], i)
		if e.Kind != agent.KindMessage || e.Message == nil {
			continue
		}
		for _, bl := range e.Message.Blocks {
			if _, ok := b.results[bl.ToolID]; bl.Type == provider.ToolResult && !ok {
				b.results[bl.ToolID] = bl
			}
		}
	}
	if b.loc == nil {
		b.loc = time.Local
	}
	path := s.Path()
	title, created, updated := s.Meta()
	d := doc{Title: t.str(title), ID: s.ID, Started: b.when(created), Changed: b.when(updated),
		Usage: s.UsageNow(), Branches: o.AllBranches}
	if d.Title == "" {
		d.Title = "Untitled session"
	}
	seen := map[string]bool{}
	model := ""
	for _, e := range path {
		if o.AllBranches {
			parent, next := e.ParentID, e.ID
			for _, i := range b.kids[parent] {
				if id := b.entries[i].ID; id != next {
					d.Items = append(d.Items, b.branch(i, model))
				}
			}
		}
		if e.Kind == agent.KindModelChange {
			model = modelName(e)
			if !seen[model] {
				seen[model] = true
				d.Models = append(d.Models, model)
			}
		}
		if it, ok := b.item(e, model); ok {
			d.Items = append(d.Items, it)
			if it.Kind == itemUser || it.Kind == itemAssistant {
				d.Messages++
			}
		}
	}
	return d
}

// branch lays out the subtree from entries[i] as a folded branch; model
// is the one in effect where it leaves.
func (b *builder) branch(i int, model string) item {
	br := &branch{}
	start := b.when(b.entries[i].Time)
	for {
		e := b.entries[i]
		if e.Kind == agent.KindModelChange {
			model = modelName(e)
		}
		if n, ok := b.notes[e.ID]; ok && br.Summary == "" {
			br.Summary = b.t.str(n.Text)
		}
		if it, ok := b.item(e, model); ok {
			br.Items = append(br.Items, it)
			if it.Kind == itemUser || it.Kind == itemAssistant {
				br.Messages++
			}
		}
		kids := b.kids[e.ID]
		if len(kids) == 0 {
			break
		}
		for _, k := range kids[1:] {
			sub := b.branch(k, model)
			br.Messages += sub.Branch.Messages
			br.Items = append(br.Items, sub)
		}
		i = kids[0]
	}
	return item{Kind: itemBranch, When: start, Branch: br}
}

// item lays out one entry; ok is false for entries that show nothing,
// such as a message of tool results, which show with their calls.
func (b *builder) item(e agent.Entry, model string) (item, bool) {
	it := item{When: b.when(e.Time), Label: b.t.str(b.labels[e.ID])}
	switch e.Kind {
	case agent.KindMessage:
		if e.Message == nil {
			return it, false
		}
		if e.Message.Role == provider.User {
			text := agent.PromptText(*e.Message)
			if text == "" {
				return it, false
			}
			it.Kind = itemUser
			it.Parts = []part{{Kind: partText, Text: b.t.str(text)}}
			return it, true
		}
		it.Kind, it.Model = itemAssistant, model
		if e.Usage != nil && *e.Usage != (provider.Usage{}) {
			u := *e.Usage
			it.Usage = &u
		}
		for _, bl := range e.Message.Blocks {
			switch bl.Type {
			case provider.Text:
				if strings.TrimSpace(bl.Text) != "" {
					it.Parts = append(it.Parts, part{Kind: partText, Text: b.t.str(bl.Text)})
				}
			case provider.Thinking:
				if strings.TrimSpace(bl.Text) != "" {
					it.Parts = append(it.Parts, part{Kind: partThinking, Text: b.t.str(bl.Text)})
				}
			case provider.ToolUse:
				it.Parts = append(it.Parts, part{Kind: partTool, Tool: b.tool(bl)})
			}
		}
		return it, true
	case agent.KindCompaction:
		if e.Compaction == nil {
			return it, false
		}
		c := *e.Compaction
		it.Kind, it.Compaction, it.Text = itemCompaction, &c, b.t.str(c.Summary)
		return it, true
	case agent.KindModeChange:
		it.Kind, it.Text = itemMarker, "Mode: "+e.Mode
		return it, true
	case agent.KindModelChange:
		it.Kind, it.Text = itemMarker, "Model: "+modelName(e)
		return it, true
	}
	return it, false
}

// when shows a time in the export's zone; "" for an unknown one.
func (b *builder) when(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(b.loc).Format("2006-01-02 15:04")
}

func modelName(e agent.Entry) string {
	switch {
	case e.Model == "":
		return e.Provider
	case e.Provider == "":
		return e.Model
	}
	return e.Provider + " · " + e.Model
}

// argKeys are the inputs that say best what a call was about.
var argKeys = []string{"path", "command", "pattern", "query", "url", "description", "name"}

func (b *builder) tool(bl provider.Block) *toolCall {
	tc := &toolCall{Name: bl.ToolName}
	var in map[string]any
	_ = json.Unmarshal(bl.Input, &in)
	for _, k := range argKeys {
		if v, ok := in[k].(string); ok && v != "" {
			tc.Arg = b.t.str(oneLine(v, 80))
			break
		}
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, bl.Input, "", "  ") == nil {
		tc.Input = b.t.str(pretty.String())
	} else {
		tc.Input = b.t.str(string(bl.Input))
	}
	if r, ok := b.results[bl.ToolID]; ok {
		tc.Done, tc.Error, tc.Result = true, r.IsError, b.t.str(r.Text)
	}
	tc.Diffs = b.diffs(bl.ToolName, bl.Input)
	return tc
}

// diffs is what an edit, multi_edit or write call changed.
func (b *builder) diffs(name string, input json.RawMessage) []fileDiff {
	type edit struct {
		Old string `json:"old_string"`
		New string `json:"new_string"`
	}
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		edit
		Edits []edit `json:"edits"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	path := b.t.str(in.Path)
	switch name {
	case "edit":
		return []fileDiff{{Path: path, Lines: lineDiff(b.t.str(in.Old), b.t.str(in.New))}}
	case "multi_edit":
		out := make([]fileDiff, 0, len(in.Edits))
		for _, e := range in.Edits {
			out = append(out, fileDiff{Path: path, Lines: lineDiff(b.t.str(e.Old), b.t.str(e.New))})
		}
		return out
	case "write":
		d := fileDiff{Path: path}
		lines := diff.Lines(b.t.str(in.Content))
		if len(lines) > maxWriteLines {
			d.More, lines = len(lines)-maxWriteLines, lines[:maxWriteLines]
		}
		for _, l := range lines {
			d.Lines = append(d.Lines, diffLine{Op: '+', Text: l})
		}
		return []fileDiff{d}
	}
	return nil
}

func lineDiff(old, new string) []diffLine {
	a, b := diff.Lines(old), diff.Lines(new)
	var out []diffLine
	for _, op := range diff.Compute(a, b) {
		switch op.Kind {
		case diff.Equal:
			for _, l := range a[op.A0:op.A1] {
				out = append(out, diffLine{' ', l})
			}
		case diff.Delete:
			for _, l := range a[op.A0:op.A1] {
				out = append(out, diffLine{'-', l})
			}
		case diff.Insert:
			for _, l := range b[op.B0:op.B1] {
				out = append(out, diffLine{'+', l})
			}
		}
	}
	return out
}

func oneLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// Path is where an export goes: p as typed, relative to root, with
// the default file name when p is a folder; ~/Downloads (or home) when p
// is empty.
func Path(p string, f Format, title string, now time.Time, root, home string) string {
	name := FileName(title, now, f)
	if p == "" {
		dir := filepath.Join(home, "Downloads")
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			dir = home
		}
		return filepath.Join(dir, name)
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if fi, err := os.Stat(p); (err == nil && fi.IsDir()) || strings.HasSuffix(p, string(filepath.Separator)) {
		return filepath.Join(p, name)
	}
	if filepath.Ext(p) == "" {
		p += "." + string(f)
	}
	return p
}
