// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package export

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Redaction says how many values of one kind were taken out.
type Redaction struct {
	Kind  string
	Count int
}

// minKnown is the shortest known secret worth looking for; shorter values
// would match ordinary text.
const minKnown = 8

// Redactor takes secrets out of text: values it was told about, such as
// the API keys hm holds, and anything shaped like a key or token.
type Redactor struct {
	known []string // longest first, so a key's prefix never splits it
}

// NewRedactor redacts the known values as well as the usual key shapes.
func NewRedactor(known ...string) *Redactor {
	seen := map[string]bool{}
	var k []string
	for _, v := range known {
		v = strings.TrimSpace(v)
		if len(v) < minKnown || seen[v] {
			continue
		}
		seen[v] = true
		k = append(k, v)
	}
	sort.SliceStable(k, func(i, j int) bool { return len(k[i]) > len(k[j]) })
	return &Redactor{known: k}
}

// Redact returns s with its secrets replaced, and what was replaced.
func (r *Redactor) Redact(s string) (string, []Redaction) {
	t := r.tally()
	out := t.str(s)
	return out, t.list()
}

// pattern is one shape of secret. With prefix, the first group is kept
// (such as "api_key=") and the second is the secret.
type pattern struct {
	kind   string
	re     *regexp.Regexp
	prefix bool
	ok     func(secret string) bool // nil takes every match
}

var patterns = []pattern{
	{kind: "private key", re: regexp.MustCompile(`-----BEGIN[A-Z ]*PRIVATE KEY-----[\s\S]*?-----END[A-Z ]*PRIVATE KEY-----`)},
	{kind: "anthropic key", re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{20,}`)},
	{kind: "openai key", re: regexp.MustCompile(`\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_\-]{20,}`), ok: mixed},
	{kind: "github token", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})`)},
	{kind: "aws key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{kind: "slack token", re: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{10,}`)},
	{kind: "google key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}`)},
	{kind: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`)},
	{kind: "bearer token", re: regexp.MustCompile(`(?i)(\bbearer\s+)([A-Za-z0-9._~+/\-]{16,}=*)`), prefix: true, ok: mixed},
	{kind: "secret value", re: regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|auth[_-]?token|refresh[_-]?token|` +
		`client[_-]?secret|secret[_-]?key|secret|password|passwd|token)["']?\s*[:=]\s*["']?)([A-Za-z0-9_\-./+=]{12,})`),
		prefix: true, ok: mixed},
}

// mixed reports whether s has both letters and digits, as generated keys
// do and words and names mostly do not.
func mixed(s string) bool {
	var letter, digit bool
	for _, r := range s {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsDigit(r)
	}
	return letter && digit
}

func mark(kind string) string { return "[REDACTED: " + kind + "]" }

// tally redacts the strings of one export and counts what it took out.
type tally struct {
	r *Redactor
	n map[string]int
}

func (r *Redactor) tally() *tally { return &tally{r: r, n: map[string]int{}} }

func (t *tally) str(s string) string {
	if s == "" {
		return s
	}
	for _, k := range t.r.known {
		if n := strings.Count(s, k); n > 0 {
			s = strings.ReplaceAll(s, k, mark("known secret"))
			t.n["known secret"] += n
		}
	}
	for _, p := range patterns {
		s = p.re.ReplaceAllStringFunc(s, func(m string) string {
			keep, secret := "", m
			if p.prefix {
				sub := p.re.FindStringSubmatch(m)
				keep, secret = sub[1], sub[2]
			}
			if p.ok != nil && !p.ok(secret) {
				return m
			}
			t.n[p.kind]++
			return keep + mark(p.kind)
		})
	}
	return s
}

// list is what was taken out, the most common kind first.
func (t *tally) list() []Redaction {
	out := make([]Redaction, 0, len(t.n))
	for k, n := range t.n {
		out = append(out, Redaction{Kind: k, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// Describe says in a line what was redacted: "3 values: 2 anthropic key,
// 1 known secret"; "" for nothing.
func Describe(rs []Redaction) string {
	total := 0
	parts := make([]string, len(rs))
	for i, r := range rs {
		total += r.Count
		parts[i] = strconv.Itoa(r.Count) + " " + r.Kind
	}
	if total == 0 {
		return ""
	}
	noun := "values"
	if total == 1 {
		noun = "value"
	}
	return strconv.Itoa(total) + " " + noun + ": " + strings.Join(parts, ", ")
}
