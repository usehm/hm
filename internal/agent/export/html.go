package export

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/mirzakhany/hm/internal/agent/provider"
)

// Colors are one theme's colours for the page, as CSS colour values.
type Colors struct {
	Background, Surface, Text, Muted, Border, Accent string
	Added, Removed, Error                            string
}

// Palette is the page's light and dark colours; the browser picks one.
type Palette struct {
	Light, Dark Colors
}

// DefaultPalette is used when the caller has no theme to give.
var DefaultPalette = Palette{
	Light: Colors{Background: "#ffffff", Surface: "#f5f6f8", Text: "#1f2328", Muted: "#656d76", Border: "#d8dee4",
		Accent: "#0969da", Added: "#1a7f37", Removed: "#cf222e", Error: "#cf222e"},
	Dark: Colors{Background: "#0d1117", Surface: "#161b22", Text: "#e6edf3", Muted: "#8d96a0", Border: "#30363d",
		Accent: "#4493f8", Added: "#3fb950", Removed: "#f85149", Error: "#f85149"},
}

// md renders model and user Markdown. Raw HTML is escaped rather than
// passed through, unsafe link schemes are dropped, and images become links
// so the page makes no requests.
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(noImages{}, 100))),
)

type noImages struct{}

func (noImages) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	var imgs []*ast.Image
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if img, ok := n.(*ast.Image); ok && entering {
			imgs = append(imgs, img)
		}
		return ast.WalkContinue, nil
	})
	for _, img := range imgs {
		link := ast.NewLink()
		link.Destination, link.Title = img.Destination, img.Title
		for c := img.FirstChild(); c != nil; {
			next := c.NextSibling()
			link.AppendChild(link, c)
			c = next
		}
		img.Parent().ReplaceChild(img.Parent(), img, link)
	}
}

func renderMarkdown(src string) template.HTML {
	var b bytes.Buffer
	if err := md.Convert([]byte(src), &b); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(src) + "</pre>")
	}
	return template.HTML(b.String()) // goldmark escapes raw HTML: it is not built WithUnsafe
}

// page renders d as one HTML file with its styles inline.
func page(d doc, p Palette) ([]byte, error) {
	if p == (Palette{}) {
		p = DefaultPalette
	}
	var b bytes.Buffer
	err := pageTemplate.Execute(&b, struct {
		doc
		CSS template.CSS
	}{d, template.CSS(css(p))})
	return b.Bytes(), err
}

func css(p Palette) string {
	vars := func(c Colors) string {
		return fmt.Sprintf("--bg:%s;--surface:%s;--text:%s;--muted:%s;--border:%s;--accent:%s;--added:%s;--removed:%s;--error:%s;",
			c.Background, c.Surface, c.Text, c.Muted, c.Border, c.Accent, c.Added, c.Removed, c.Error)
	}
	return ":root{" + vars(p.Light) + "color-scheme:light}\n" +
		"@media (prefers-color-scheme:dark){:root{" + vars(p.Dark) + "color-scheme:dark}}\n" + baseCSS
}

func fmtCost(c float64) string {
	switch {
	case c == 0:
		return ""
	case c < 0.01:
		return fmt.Sprintf("$%.4f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

func fmtTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// usageLine says what one reply or the whole session cost.
func usageLine(u provider.Usage) string {
	parts := []string{fmtTokens(u.Input+u.CacheRead+u.CacheWrite) + " in", fmtTokens(u.Output) + " out"}
	if u.CacheRead > 0 {
		parts = append(parts, fmtTokens(u.CacheRead)+" cached")
	}
	if c := fmtCost(u.Cost); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, " · ")
}

func diffClass(op byte) string {
	switch op {
	case '+':
		return "add"
	case '-':
		return "del"
	}
	return "ctx"
}

var pageTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"md":     renderMarkdown,
	"cost":   fmtCost,
	"usage":  usageLine,
	"tokens": fmtTokens,
	"op":     func(b byte) string { return string(b) },
	"cls":    diffClass,
	"join":   strings.Join,
	"isUser": func(k itemKind) bool { return k == itemUser },
	"isAsst": func(k itemKind) bool { return k == itemAssistant },
	"isComp": func(k itemKind) bool { return k == itemCompaction },
	"isMark": func(k itemKind) bool { return k == itemMarker },
	"isBr":   func(k itemKind) bool { return k == itemBranch },
	"isText": func(k partKind) bool { return k == partText },
	"isThk":  func(k partKind) bool { return k == partThinking },
	"isImg":  func(k partKind) bool { return k == partImage },
}).Parse(pageHTML))

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">
<meta name="generator" content="hm">
<title>{{.Title}}</title>
<style>
{{.CSS}}
</style>
</head>
<body>
<main>
<header>
<h1>{{.Title}}</h1>
<dl class="meta">
{{- with .Started}}<div><dt>Started</dt><dd>{{.}}</dd></div>{{end}}
{{- with .Changed}}<div><dt>Updated</dt><dd>{{.}}</dd></div>{{end}}
{{- with .Models}}<div><dt>Models</dt><dd>{{join . ", "}}</dd></div>{{end}}
<div><dt>Messages</dt><dd>{{.Messages}}</dd></div>
<div><dt>Tokens</dt><dd>{{tokens .Usage.Input}} in · {{tokens .Usage.Output}} out{{if .Usage.CacheRead}} · {{tokens .Usage.CacheRead}} cache read{{end}}{{if .Usage.CacheWrite}} · {{tokens .Usage.CacheWrite}} cache write{{end}}</dd></div>
{{- with cost .Usage.Cost}}<div><dt>Cost</dt><dd>{{.}} <span class="muted">(all branches)</span></dd></div>{{end}}
</dl>
</header>
{{template "items" .Items}}
<footer>Exported from hm · session {{.ID}}</footer>
</main>
</body>
</html>
{{define "pic"}}<img class="pic" src="{{.URL}}" alt="image"{{with .Width}} width="{{.}}"{{end}}{{with .Height}} height="{{.}}"{{end}}>{{end}}
{{define "items"}}{{range .}}
{{- if isUser .Kind}}
<section class="msg user">
<div class="head"><span class="role">You</span>{{with .Label}}<span class="label">{{.}}</span>{{end}}<time>{{.When}}</time></div>
{{range .Parts}}{{if isImg .Kind}}{{template "pic" .Image}}{{else}}<div class="md">{{md .Text}}</div>{{end}}{{end}}
</section>
{{- else if isAsst .Kind}}
<section class="msg assistant">
<div class="head"><span class="role">Assistant</span>{{with .Model}}<span class="model">{{.}}</span>{{end}}{{with .Label}}<span class="label">{{.}}</span>{{end}}{{with .Usage}}<span class="usage">{{usage .}}</span>{{end}}<time>{{.When}}</time></div>
{{range .Parts}}
{{- if isText .Kind}}<div class="md">{{md .Text}}</div>
{{- else if isThk .Kind}}<details class="thinking"><summary>Thinking</summary><div class="md">{{md .Text}}</div></details>
{{- else}}{{with .Tool}}<details class="tool{{if .Error}} error{{end}}"{{if .Diffs}} open{{end}}><summary><span class="name">{{.Name}}</span>{{with .Arg}} <span class="arg">{{.}}</span>{{end}}{{if .Error}} <span class="badge">error</span>{{else if not .Done}} <span class="badge">no result</span>{{end}}</summary>
{{- range .Diffs}}<div class="diff"><div class="path">{{.Path}}</div><pre>{{range .Lines}}<span class="{{cls .Op}}">{{op .Op}} {{.Text}}</span>
{{end}}</pre>{{if .More}}<div class="muted">… {{.More}} more lines</div>{{end}}</div>{{end}}
<details class="io"><summary>Input</summary><pre>{{.Input}}</pre></details>
{{- if .Done}}<details class="io"{{if .Error}} open{{end}}><summary>Result</summary><pre>{{.Result}}</pre>{{range .Images}}{{template "pic" .}}{{end}}</details>{{end}}
</details>{{end}}
{{- end}}
{{- end}}
</section>
{{- else if isComp .Kind}}
<div class="divider"><span>Compacted{{with .Compaction}} · {{tokens .TokensBefore}} → {{tokens .TokensAfter}} tokens{{end}} · {{.When}}</span></div>
<details class="compaction"><summary>Summary of earlier messages</summary><div class="md">{{md .Text}}</div></details>
{{- else if isMark .Kind}}
<div class="marker">{{.Text}}{{with .Label}} <span class="label">{{.}}</span>{{end}}</div>
{{- else if isBr .Kind}}{{with .Branch}}
<details class="branch"><summary>Other branch · {{.Messages}} message{{if ne .Messages 1}}s{{end}}</summary>
{{with .Summary}}<p class="note">{{.}}</p>{{end}}
{{template "items" .Items}}
</details>{{end}}
{{- end}}
{{- end}}{{end}}`

const baseCSS = `*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);font:15px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",Helvetica,Arial,sans-serif}
main{max-width:880px;margin:0 auto;padding:32px 16px 64px}
h1{font-size:24px;margin:0 0 12px}
.meta{display:flex;flex-wrap:wrap;gap:8px 24px;margin:0 0 28px;padding:12px 16px;background:var(--surface);border:1px solid var(--border);border-radius:8px;font-size:13px}
.meta dt{color:var(--muted)}.meta dd{margin:0;font-variant-numeric:tabular-nums}
.muted{color:var(--muted)}
.msg{margin:0 0 20px;padding:12px 16px;border:1px solid var(--border);border-radius:8px}
.msg.user{background:var(--surface)}
.head{display:flex;flex-wrap:wrap;align-items:baseline;gap:4px 10px;margin-bottom:6px;font-size:13px;color:var(--muted)}
.role{font-weight:600;color:var(--text)}
.head time{margin-left:auto}
.model,.usage{font-variant-numeric:tabular-nums}
.label{padding:0 6px;border-radius:4px;border:1px solid var(--accent);color:var(--accent);font-size:12px}
.md>:first-child{margin-top:0}.md>:last-child{margin-bottom:0}
.md a{color:var(--accent)}
.md table{border-collapse:collapse}.md th,.md td{border:1px solid var(--border);padding:4px 8px}
.md blockquote{margin:0;padding-left:12px;border-left:3px solid var(--border);color:var(--muted)}
code,pre{font:13px/1.45 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
:not(pre)>code{padding:1px 4px;border-radius:4px;background:var(--surface)}
pre{margin:6px 0;padding:10px 12px;overflow:auto;background:var(--surface);border:1px solid var(--border);border-radius:6px;white-space:pre-wrap;word-break:break-word}
details{margin:8px 0}
summary{cursor:pointer;color:var(--muted);font-size:13px}
.pic{display:block;max-width:100%;height:auto;margin:6px 0;border:1px solid var(--border);border-radius:6px}
.tool,.thinking{border-left:3px solid var(--border);padding-left:10px}
.tool.error{border-left-color:var(--error)}
.tool .name{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:var(--text)}
.badge{color:var(--error)}
.io{margin-left:4px}
.diff .path{font-size:12px;color:var(--muted);margin-top:6px}
.diff pre{white-space:pre}
.diff span{display:block}
.add{color:var(--added);background:color-mix(in srgb,var(--added) 12%,transparent)}
.del{color:var(--removed);background:color-mix(in srgb,var(--removed) 12%,transparent)}
.divider{display:flex;align-items:center;gap:12px;margin:28px 0 4px;color:var(--muted);font-size:13px}
.divider::before,.divider::after{content:"";flex:1;border-top:1px dashed var(--border)}
.marker{margin:8px 0;color:var(--muted);font-size:12px}
.branch{margin:12px 0 20px;padding:8px 12px;border:1px dashed var(--border);border-radius:8px}
.branch>summary{font-weight:600}
.note{color:var(--muted);font-size:13px}
footer{margin-top:40px;color:var(--muted);font-size:12px;text-align:center}
`
