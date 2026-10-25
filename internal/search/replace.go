package search

import (
	"regexp"
	"sort"
)

// Replacement turns a match into its replacement text. In regex mode the
// template expands $1, ${name} and $$; otherwise it is used literally.
func Replacement(re *regexp.Regexp, regex bool, text []byte, m Match, template string) (string, bool) {
	if !regex {
		return template, true
	}
	loc := re.FindSubmatchIndex(text[m.Start:])
	if loc == nil || loc[0] != 0 {
		// The match no longer starts here (the text changed).
		return "", false
	}
	// Expand against the text from the match on, so ^ and lookbehind-free
	// patterns see the same match as the search.
	return string(re.Expand(nil, []byte(template), text[m.Start:], loc)), true
}

// ReplaceAll replaces every match of re in text and returns the new text
// and the number of replacements.
func ReplaceAll(re *regexp.Regexp, regex bool, text []byte, template string) ([]byte, int) {
	n := len(re.FindAllIndex(text, -1))
	if regex {
		return re.ReplaceAll(text, []byte(template)), n
	}
	return re.ReplaceAllLiteral(text, []byte(template)), n
}

// ReplaceMatches replaces the given matches in text (from one search, so
// they do not overlap) and returns the new text.
func ReplaceMatches(re *regexp.Regexp, regex bool, text []byte, ms []Match, template string) ([]byte, int) {
	sorted := append([]Match(nil), ms...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	var out []byte
	pos, n := 0, 0
	for _, m := range sorted {
		if m.Start < pos || m.End > len(text) {
			continue
		}
		rep, ok := Replacement(re, regex, text, m, template)
		if !ok {
			continue
		}
		out = append(out, text[pos:m.Start]...)
		out = append(out, rep...)
		pos = m.End
		n++
	}
	out = append(out, text[pos:]...)
	return out, n
}
