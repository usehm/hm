package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/usehm/hm/internal/agent/provider"
	"github.com/usehm/hm/internal/config"
)

// commandTool is a tool the user defined in the config: a command template
// with named parameters. Parameters are passed as argv entries, never
// through a shell, so a value can't inject another command.
type commandTool struct {
	name string
	cfg  config.CommandTool
	argv []string // the template, split into words
}

// CommandTools builds the config's command tools.
func CommandTools(cfg map[string]config.CommandTool) ([]Tool, []error) {
	names := make([]string, 0, len(cfg))
	for n := range cfg {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Tool
	var errs []error
	for _, n := range names {
		c := cfg[n]
		argv, err := SplitArgs(c.Command)
		if err == nil && len(argv) == 0 {
			err = errors.New("command is empty")
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("tools.commands.%s: %w", n, err))
			continue
		}
		out = append(out, &commandTool{name: n, cfg: c, argv: argv})
	}
	return out, errs
}

func (t *commandTool) Spec() provider.Tool {
	props := map[string]any{}
	var required []string
	for name, p := range t.cfg.Params {
		typ := p.Type
		if typ == "" {
			typ = "string"
		}
		prop := map[string]any{"type": typ}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		if p.Default != nil {
			prop["default"] = p.Default
		}
		props[name] = prop
		if p.Required && p.Default == nil {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	desc := t.cfg.Description + "\nRuns: " + t.cfg.Command
	return provider.Tool{Name: t.name, Description: desc, Schema: schema(props, required...)}
}

func (t *commandTool) Prepare(env Env, input json.RawMessage) (Plan, error) {
	var in map[string]any
	if !json.Valid(input) {
		return Plan{}, errors.New("INVALID_JSON: the tool input is not valid JSON; send the call again")
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return Plan{}, err
	}
	for k := range in {
		if _, ok := t.cfg.Params[k]; !ok {
			return Plan{}, fmt.Errorf("unknown parameter %q", k)
		}
	}
	values := map[string]string{}
	for name, p := range t.cfg.Params {
		v, ok := in[name]
		if !ok {
			v = p.Default
		}
		if v == nil {
			if p.Required {
				return Plan{}, fmt.Errorf("parameter %q is required", name)
			}
			v = ""
		}
		values[name] = fmt.Sprint(v)
	}
	argv := make([]string, 0, len(t.argv))
	for _, w := range t.argv {
		// Environment references come from the template only; a value can't
		// smuggle one in.
		resolved, err := config.Resolve(w)
		if err != nil {
			return Plan{}, err
		}
		for name, v := range values {
			resolved = strings.ReplaceAll(resolved, "{"+name+"}", v)
		}
		argv = append(argv, resolved)
	}
	timeout := t.cfg.Timeout.Duration
	if timeout <= 0 {
		timeout = defaultShellTimeout
	}
	summary := t.name + ": " + shownArgv(argv, t.argv)
	return Plan{Category: CatCommand, Summary: summary, Paths: []string{env.Root()}, Command: strings.Join(argv, " "), Run: func(ctx context.Context) Result {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, config.ExpandHome(argv[0]), argv[1:]...)
		cmd.Dir = env.Root()
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		text := clip(out.String(), shellOutputMax)
		var xe *exec.ExitError
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			return fail("%s\n[timed out after %v]", text, timeout)
		case errors.As(err, &xe):
			return fail("%s\n[exit status %d]", text, xe.ExitCode())
		case err != nil:
			return fail("%v", err)
		}
		return ok("%s\n[exit status 0]", text)
	}}, nil
}

// shownArgv is the command line for the approval card. Words that came from
// ${ENV} references show the reference, so secrets stay off the screen.
func shownArgv(argv, template []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if strings.Contains(template[i], "${") || strings.HasPrefix(template[i], "keychain:") {
			a = template[i]
		}
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			a = fmt.Sprintf("%q", a)
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// SplitArgs splits a command template into words like a POSIX shell does
// for quoting, without expanding anything.
func SplitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord := false
	var quote rune
	for i := 0; i < len(s); i++ {
		c := rune(s[i])
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) && strings.ContainsRune(`"\$`, rune(s[i+1])) {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, inWord = c, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unclosed quote")
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}
