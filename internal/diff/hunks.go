// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package diff

import "strings"

// Hunk is one block of changed lines: A[A0:A1] becomes B[B0:B1].
type Hunk struct {
	A0, A1 int
	B0, B1 int
}

// Hunks groups the changes from a to b into hunks; changes closer than
// 2*context equal lines apart share a hunk, as in a unified diff.
func Hunks(a, b []string, context int) []Hunk {
	var out []Hunk
	for _, op := range Compute(a, b) {
		if op.Kind == Equal {
			continue
		}
		if n := len(out); n > 0 && op.A0-out[n-1].A1 <= 2*context && op.B0-out[n-1].B1 <= 2*context {
			out[n-1].A1, out[n-1].B1 = op.A1, op.B1
			continue
		}
		out = append(out, Hunk{op.A0, op.A1, op.B0, op.B1})
	}
	return out
}

// Apply builds the text that keeps the accepted hunks of a -> b and the
// old lines of the rest. Hunks must come from Hunks(a, b, ...).
func Apply(a, b []string, hunks []Hunk, accepted []bool, trailingNewline bool) string {
	var out []string
	pos := 0
	for i, h := range hunks {
		out = append(out, a[pos:h.A0]...)
		if i < len(accepted) && accepted[i] {
			out = append(out, b[h.B0:h.B1]...)
		} else {
			out = append(out, a[h.A0:h.A1]...)
		}
		pos = h.A1
	}
	out = append(out, a[pos:]...)
	s := strings.Join(out, "\n")
	if trailingNewline && len(out) > 0 {
		s += "\n"
	}
	return s
}
