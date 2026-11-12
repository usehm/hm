// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"strings"
	"unicode"
)

// When is a parsed keybinding condition such as
// "editorFocus && !vimInsert || terminalFocus". Names are context flags the
// app sets; ! binds tightest, then &&, then ||, and parentheses group.
type When struct{ root whenNode }

type whenNode interface {
	eval(ctx map[string]bool) bool
}

type whenName string
type whenNot struct{ x whenNode }
type whenAnd struct{ a, b whenNode }
type whenOr struct{ a, b whenNode }

func (n whenName) eval(ctx map[string]bool) bool { return ctx[string(n)] }
func (n whenNot) eval(ctx map[string]bool) bool  { return !n.x.eval(ctx) }
func (n whenAnd) eval(ctx map[string]bool) bool  { return n.a.eval(ctx) && n.b.eval(ctx) }
func (n whenOr) eval(ctx map[string]bool) bool   { return n.a.eval(ctx) || n.b.eval(ctx) }

// Eval reports whether the condition holds; an empty condition always does.
func (w When) Eval(ctx map[string]bool) bool { return w.root == nil || w.root.eval(ctx) }

// ParseWhen parses a keybinding condition; "" is always true.
func ParseWhen(s string) (When, error) {
	p := &whenParser{toks: whenTokens(s)}
	if len(p.toks) == 0 {
		return When{}, nil
	}
	n, err := p.or()
	if err != nil {
		return When{}, err
	}
	if p.i < len(p.toks) {
		return When{}, fmt.Errorf("unexpected %q", p.toks[p.i])
	}
	return When{root: n}, nil
}

func whenTokens(s string) []string {
	var toks []string
	for i := 0; i < len(s); {
		c := rune(s[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case strings.HasPrefix(s[i:], "&&"), strings.HasPrefix(s[i:], "||"):
			toks = append(toks, s[i:i+2])
			i += 2
		case c == '!' || c == '(' || c == ')':
			toks = append(toks, string(c))
			i++
		default:
			j := i
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || s[j] == '.' || s[j] == '_') {
				j++
			}
			if j == i {
				j = i + 1 // an unknown character becomes its own token
			}
			toks = append(toks, s[i:j])
			i = j
		}
	}
	return toks
}

type whenParser struct {
	toks []string
	i    int
}

func (p *whenParser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *whenParser) or() (whenNode, error) {
	a, err := p.and()
	for err == nil && p.peek() == "||" {
		p.i++
		var b whenNode
		if b, err = p.and(); err == nil {
			a = whenOr{a, b}
		}
	}
	return a, err
}

func (p *whenParser) and() (whenNode, error) {
	a, err := p.unary()
	for err == nil && p.peek() == "&&" {
		p.i++
		var b whenNode
		if b, err = p.unary(); err == nil {
			a = whenAnd{a, b}
		}
	}
	return a, err
}

func (p *whenParser) unary() (whenNode, error) {
	switch t := p.peek(); {
	case t == "!":
		p.i++
		x, err := p.unary()
		return whenNot{x}, err
	case t == "(":
		p.i++
		x, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.peek() != ")" {
			return nil, fmt.Errorf("missing )")
		}
		p.i++
		return x, nil
	case t == "":
		return nil, fmt.Errorf("condition ends too soon")
	case unicode.IsLetter(rune(t[0])):
		p.i++
		return whenName(t), nil
	default:
		return nil, fmt.Errorf("unexpected %q", t)
	}
}
