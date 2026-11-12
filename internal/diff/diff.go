// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

// Package diff compares texts line by line, for the editor's change bars
// and the agent's edit review.
package diff

import "strings"

// OpKind is what a run of lines does.
type OpKind int

const (
	Equal OpKind = iota
	Insert
	Delete
)

// Op is a run of lines: A[A0:A1] in the old text, B[B0:B1] in the new.
type Op struct {
	Kind   OpKind
	A0, A1 int
	B0, B1 int
}

// Lines splits text into lines without their newlines. A trailing newline
// does not make an empty last line.
func Lines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	return strings.Split(text, "\n")
}

// Compute returns the edit script from a to b, using Myers' algorithm with
// a common prefix and suffix trimmed first. Runs come out in order.
func Compute(a, b []string) []Op {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var ops []Op
	if pre > 0 {
		ops = append(ops, Op{Equal, 0, pre, 0, pre})
	}
	mid := myers(a[pre:len(a)-suf], b[pre:len(b)-suf])
	for _, op := range mid {
		op.A0, op.A1, op.B0, op.B1 = op.A0+pre, op.A1+pre, op.B0+pre, op.B1+pre
		ops = append(ops, op)
	}
	if suf > 0 {
		ops = append(ops, Op{Equal, len(a) - suf, len(a), len(b) - suf, len(b)})
	}
	return merge(ops)
}

// maxEditCost bounds the search; beyond it the middle is one replace.
const maxEditCost = 4000

func myers(a, b []string) []Op {
	n, m := len(a), len(b)
	switch {
	case n == 0 && m == 0:
		return nil
	case n == 0:
		return []Op{{Insert, 0, 0, 0, m}}
	case m == 0:
		return []Op{{Delete, 0, n, 0, 0}}
	}
	max := n + m
	if max > maxEditCost {
		max = maxEditCost
	}
	off := max
	v := make([]int, 2*max+2)
	var trace [][]int
	for d := 0; d <= max; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, off, d)
			}
		}
	}
	// Too different: replace the whole middle.
	return []Op{{Delete, 0, n, 0, 0}, {Insert, n, n, 0, m}}
}

func backtrack(trace [][]int, a, b []string, off, d int) []Op {
	x, y := len(a), len(b)
	var rev []Op
	for ; d > 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v[off+pk]
		py := px - pk
		for x > px && y > py {
			rev = append(rev, Op{Equal, x - 1, x, y - 1, y})
			x--
			y--
		}
		if x == px {
			rev = append(rev, Op{Insert, x, x, y - 1, y})
		} else {
			rev = append(rev, Op{Delete, x - 1, x, y, y})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		rev = append(rev, Op{Equal, x - 1, x, y - 1, y})
		x--
		y--
	}
	ops := make([]Op, len(rev))
	for i := range rev {
		ops[i] = rev[len(rev)-1-i]
	}
	return ops
}

// merge joins adjacent runs of the same kind.
func merge(ops []Op) []Op {
	var out []Op
	for _, op := range ops {
		if n := len(out); n > 0 && out[n-1].Kind == op.Kind && out[n-1].A1 == op.A0 && out[n-1].B1 == op.B0 {
			out[n-1].A1, out[n-1].B1 = op.A1, op.B1
			continue
		}
		out = append(out, op)
	}
	return out
}

// Mark is how a line of the new text differs from the old.
type Mark int

const (
	Added Mark = iota + 1
	Modified
	Removed // lines were deleted just above this line
)

// Marks maps lines of b to their change against a, for gutter bars: a
// delete followed by an insert reads as modified lines.
func Marks(a, b []string) map[int]Mark {
	ops := Compute(a, b)
	out := map[int]Mark{}
	for i := 0; i < len(ops); i++ {
		op := ops[i]
		switch op.Kind {
		case Delete:
			if i+1 < len(ops) && ops[i+1].Kind == Insert {
				ins := ops[i+1]
				del := op.A1 - op.A0
				for l := ins.B0; l < ins.B1; l++ {
					if l-ins.B0 < del {
						out[l] = Modified
					} else {
						out[l] = Added
					}
				}
				if ins.B1-ins.B0 < del {
					out[ins.B1] = Removed
				}
				i++
				continue
			}
			if _, ok := out[op.B0]; !ok {
				out[op.B0] = Removed
			}
		case Insert:
			for l := op.B0; l < op.B1; l++ {
				out[l] = Added
			}
		}
	}
	return out
}
