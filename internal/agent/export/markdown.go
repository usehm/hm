package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/usehm/hm/internal/agent"
)

// markdown renders d as GitHub-flavoured Markdown. What the HTML page
// folds away goes in <details> blocks, which GitHub shows folded too.
func markdown(d doc) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", html.EscapeString(oneLine(d.Title, 200)))
	if d.Started != "" {
		fmt.Fprintf(&b, "- Started: %s\n", d.Started)
	}
	if d.Changed != "" {
		fmt.Fprintf(&b, "- Updated: %s\n", d.Changed)
	}
	if len(d.Models) > 0 {
		fmt.Fprintf(&b, "- Models: %s\n", strings.Join(d.Models, ", "))
	}
	fmt.Fprintf(&b, "- Messages: %d\n", d.Messages)
	fmt.Fprintf(&b, "- Tokens: %s\n", usageLine(d.Usage))
	fmt.Fprintf(&b, "- Session: `%s`\n", d.ID)
	mdItems(&b, d.Items)
	return []byte(b.String())
}

func mdItems(b *strings.Builder, items []item) {
	for _, it := range items {
		switch it.Kind {
		case itemUser:
			b.WriteString("\n## You" + mdHead("", it) + "\n\n")
			for _, p := range it.Parts {
				if p.Kind == partImage {
					b.WriteString(p.Image.mark() + "\n")
					continue
				}
				b.WriteString(strings.TrimSpace(p.Text) + "\n")
			}
		case itemAssistant:
			b.WriteString("\n## Assistant" + mdHead(it.Model, it) + "\n")
			for _, p := range it.Parts {
				switch p.Kind {
				case partText:
					b.WriteString("\n" + strings.TrimSpace(p.Text) + "\n")
				case partThinking:
					b.WriteString("\n<details><summary>Thinking</summary>\n\n" + strings.TrimSpace(p.Text) + "\n\n</details>\n")
				case partTool:
					mdTool(b, p.Tool)
				}
			}
		case itemCompaction:
			line := "Compacted"
			if c := it.Compaction; c != nil {
				line += fmt.Sprintf(" · %s → %s tokens", fmtTokens(c.TokensBefore), fmtTokens(c.TokensAfter))
			}
			b.WriteString("\n---\n\n*" + line + "*\n\n<details><summary>Summary of earlier messages</summary>\n\n" +
				strings.TrimSpace(it.Text) + "\n\n</details>\n")
		case itemMarker:
			b.WriteString("\n*" + it.Text + label(it.Label) + "*\n")
		case itemBranch:
			br := it.Branch
			s := "s"
			if br.Messages == 1 {
				s = ""
			}
			fmt.Fprintf(b, "\n<details><summary>Other branch · %d message%s</summary>\n", br.Messages, s)
			if br.Summary != "" {
				b.WriteString("\n> " + strings.ReplaceAll(strings.TrimSpace(br.Summary), "\n", "\n> ") + "\n")
			}
			mdItems(b, br.Items)
			b.WriteString("\n</details>\n")
		}
	}
}

// mdHead is the rest of a message's heading: model, cost, label, time.
func mdHead(model string, it item) string {
	var parts []string
	if model != "" {
		parts = append(parts, model)
	}
	if it.Usage != nil {
		parts = append(parts, usageLine(*it.Usage))
	}
	if it.When != "" {
		parts = append(parts, it.When)
	}
	s := ""
	if len(parts) > 0 {
		s = " · " + strings.Join(parts, " · ")
	}
	return s + label(it.Label)
}

func label(l string) string {
	if l == "" {
		return ""
	}
	return " 🔖 " + oneLine(l, 80)
}

func mdTool(b *strings.Builder, tc *toolCall) {
	sum := "<code>" + html.EscapeString(tc.Name) + "</code>"
	if tc.Arg != "" {
		sum += " " + html.EscapeString(tc.Arg)
	}
	switch {
	case tc.Error:
		sum += " (error)"
	case !tc.Done:
		sum += " (no result)"
	}
	b.WriteString("\n<details><summary>" + sum + "</summary>\n\n")
	for _, d := range tc.Diffs {
		var body strings.Builder
		for _, l := range d.Lines {
			body.WriteString(string(l.Op) + l.Text + "\n")
		}
		if d.Path != "" {
			b.WriteString("`" + strings.ReplaceAll(d.Path, "`", "'") + "`\n\n")
		}
		fenced(b, "diff", body.String())
		if d.More > 0 {
			fmt.Fprintf(b, "… %d more lines\n\n", d.More)
		}
	}
	b.WriteString("Input:\n\n")
	fenced(b, "json", tc.Input)
	if tc.Done {
		b.WriteString("Result:\n\n")
		fenced(b, "", tc.Result)
		for _, im := range tc.Images {
			b.WriteString(im.mark() + "\n\n")
		}
	}
	b.WriteString("</details>\n")
}

// fenced writes s as a code block whose fence is longer than any run of
// backticks inside it.
func fenced(b *strings.Builder, lang, s string) {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	b.WriteString(fence + lang + "\n" + strings.TrimRight(s, "\n") + "\n" + fence + "\n\n")
}

// jsonLines is the session's own file, every string redacted and the
// providers' native message copies left out: they repeat the text and
// carry signatures that mean nothing outside the session.
func jsonLines(s *agent.Session, t *tally) ([]byte, error) {
	data, err := s.Lines()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if err := enc.Encode(scrub(v, t)); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func scrub(v any, t *tally) any {
	switch v := v.(type) {
	case string:
		return t.str(v)
	case []any:
		for i := range v {
			v[i] = scrub(v[i], t)
		}
	case map[string]any:
		delete(v, "native")
		delete(v, "native_from")
		for k := range v {
			v[k] = scrub(v[k], t)
		}
	}
	return v
}

// mark stands for a picture in Markdown, where gists and most viewers
// would not show one inline.
func (p *picture) mark() string {
	if p.Width > 0 {
		return fmt.Sprintf("*[image %d×%d]*", p.Width, p.Height)
	}
	return "*[image]*"
}
