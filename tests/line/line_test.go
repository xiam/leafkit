package line

import (
	"fmt"
	"io"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	"github.com/xiam/leafkit/eval"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/internal/reference"
	"github.com/xiam/leafkit/tree"
)

var (
	lines = p.Request{Layout: p.Lines}
	value = p.Request{Goal: "line-value", Layout: p.Lines}
)

func scanner(cur *cursor.Reader, _ struct{}, request p.Request) (scan.Source, func() struct{}, error) {
	lexer := scan.NewWithCursor(cur)
	horizontal := core.RuneSet(" \t")
	lexer.MustAddRule("space", core.RuneRun(horizontal))
	lexer.MustAddRule("comment", base.LineComment("#"))
	lexer.MustAddRule("newline", base.Newline())
	switch request {
	case lines:
		lexer.MustAddRule("word", core.RuneRun(core.UnicodeLetter.Or(core.RuneSet("_-"))))
		for _, symbol := range []string{"[", "]", "="} {
			lexer.MustAddRule(scan.TokenType(symbol), core.Literal(symbol))
		}
	case value:
		body := core.RuneSet("#\r\n").Not()
		lexer.MustAddRule("value", core.Sequence(core.Rune(body.And(horizontal.Not())), core.Optional(core.RuneRun(body))))
	default:
		return nil, nil, fmt.Errorf("unsupported line request %+v", request)
	}
	return lexer, nil, nil
}

func source(input *reference.Input) (p.Source, error) {
	options := adapter.Options{Trivia: []scan.TokenType{"space", "comment"}, Newlines: []scan.TokenType{"newline"}}
	open := func(offset int) io.Reader { return input.Open(offset, scan.ErrMoreInput) }
	return adapter.NewReplay(input.ID(), open, struct{}{}, scanner, options)
}

func grammar() p.Grammar {
	token, skip := reference.Token, reference.Skip
	lineEnd := p.Choose(p.ChoiceSpec{}, skip("newline"), p.Tok(p.EndOfFile()))
	lineBoundary := p.Boundary{At: []p.Pattern{p.Kind("newline")}}
	name := p.Make("name", "name", token("word"))
	key := p.Make("key", "key", token("word"))
	text := p.Make("value", "value", p.Lex(value, token("value")))
	entry := p.Make("entry", "entry", p.Seq(key, skip("="), p.Recover("value", text, lineBoundary), lineEnd))
	entryLine := p.Choose(p.ChoiceSpec{}, skip("newline"), p.Seq(
		p.Not(p.EndOfFile()), p.Not(p.Kind("[")),
		p.Recover("entry", entry, p.Boundary{At: lineBoundary.At, Consume: true})))
	section := p.Make("section", "section", p.Seq(skip("["), name, skip("]"), lineEnd, p.Many(entryLine)))
	return p.Grammar{Start: "file", Default: lines, Rules: map[string]p.Rule{
		"file": p.Seq(p.Many(skip("newline")), p.Many(section)),
	}}
}

type config map[string]map[string]string

func readConfig(root tree.Node) (config, error) {
	return eval.Fold(root, nil, map[tree.Kind]eval.Rule[config, map[string]string]{
		"file":    readFile,
		"section": readSection,
		"entry":   readEntry,
	})
}

func readFile(frame eval.Frame, env *eval.Env[map[string]string], child eval.Descend[config, map[string]string]) (config, error) {
	all := make(config)
	for i := 0; ; i++ {
		if _, ok := frame.Node.Child(i); !ok {
			return all, nil
		}
		section, err := child(i, env)
		if err != nil {
			return nil, err
		}
		maps.Copy(all, section)
	}
}

func readSection(frame eval.Frame, env *eval.Env[map[string]string], child eval.Descend[config, map[string]string]) (config, error) {
	entries := make(map[string]string)
	scope := eval.NewEnv(env)
	scope.Set("entries", entries)
	for i := 1; ; i++ {
		if _, ok := frame.Node.Child(i); !ok {
			return config{textAt(frame.Node, 0): entries}, nil
		}
		if _, err := child(i, scope); err != nil {
			return nil, err
		}
	}
}

func readEntry(frame eval.Frame, env *eval.Env[map[string]string], _ eval.Descend[config, map[string]string]) (config, error) {
	entries, ok := env.Get("entries")
	if !ok {
		return nil, fmt.Errorf("entry outside a section")
	}
	key := textAt(frame.Node, 0)
	node, _ := frame.Node.Child(1)
	value, ok := node.(*tree.Value[string])
	if !ok {
		return nil, fmt.Errorf("entry %q has no value", key)
	}
	entries[key] = strings.TrimSpace(value.Value())
	return nil, nil
}

func textAt(node tree.Node, index int) string {
	child, _ := node.Child(index)
	return child.(*tree.Value[string]).Value()
}

func TestLineGrammar(t *testing.T) {
	engine := reference.Engine(t, grammar(), reference.Builders("name", "key", "value"))
	cases := []struct {
		name, text, shape string
		values            config
		errors            []reference.Diagnostic
	}{
		{"sections and comments", "# settings\r\n[user]\r\nname = Ada Lovelace # note\r\n\r\n[place]\ncity=Zürich",
			`(file (section (name "user") (entry (key "name") (value "Ada Lovelace "))) (section (name "place") (entry (key "city") (value "Zürich"))))`,
			config{"user": {"name": "Ada Lovelace"}, "place": {"city": "Zürich"}}, nil},
		{"comment only", "# settings\n", "(file)", config{}, nil},
		{"missing value", "[user]\nname=\ncity=Oslo\n",
			`(file (section (name "user") (entry (key "name") (leafkit:error)) (entry (key "city") (value "Oslo"))))`,
			nil, []reference.Diagnostic{{Slot: "value", Start: 12, End: 12, At: 12, Found: "\n", Expected: []p.Pattern{p.Kind("value")}}}},
		{"bad entry followed by a good entry", "[user]\nname Ada\ncity=Oslo",
			`(file (section (name "user") (leafkit:error) (entry (key "city") (value "Oslo"))))`,
			nil, []reference.Diagnostic{{Slot: "entry", Start: 7, End: 16, At: 12, Found: "Ada", Skipped: "nameAda\n", Expected: []p.Pattern{p.Kind("=")}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, streamed := range []bool{false, true} {
				root := reference.Parse(t, engine, source, tc.text, streamed, "")
				reference.Check(t, root, tc.shape, tc.errors...)
				if len(tc.errors) == 0 {
					got, err := readConfig(root)
					if assert.NoErrorf(t, err, "readConfig = %v, %v, want %v", got, err, tc.values) {
						assert.Equalf(t, tc.values, got, "readConfig = %v, %v, want %v", got, err, tc.values)
					}
				} else {
					_, err := readConfig(root)
					assert.Errorf(t, err, "readConfig accepted a syntax error")
				}
			}
		})
	}
}
