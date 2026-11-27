package ambiguity

import (
	"fmt"
	"slices"
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

func scanner(cur *cursor.Reader) *scan.Scanner {
	lexer := scan.NewWithCursor(cur)
	lexer.MustAddRule("space", base.Whitespace())
	lexer.MustAddRule("name", core.RuneRun(core.UnicodeLetter))
	for _, symbol := range []string{"*", ";"} {
		lexer.MustAddRule(scan.TokenType(symbol), core.Literal(symbol))
	}
	return lexer
}

func source(input *reference.Input) (p.Source, error) {
	cur := cursor.NewReader(input.Open(0, scan.ErrMoreInput))
	return adapter.New(input.ID(), scanner(cur), cur, p.Request{}, adapter.Options{Trivia: []scan.TokenType{"space"}})
}

func grammar(filter func(p.Candidate) bool) p.Grammar {
	name, skip := p.Ref("name"), reference.Skip
	arm := func(kind tree.Kind) p.Rule {
		return p.Make("statement", kind, p.Seq(p.Bind("head", name), skip("*"), name, skip(";")))
	}
	return p.Grammar{Start: "statement", Rules: map[string]p.Rule{
		"statement": p.Choose(p.ChoiceSpec{Policy: p.All, Slot: "statement", Filter: filter},
			arm("declaration"), arm("multiply")),
		"name": p.Make("name", "name", reference.Token("name")),
	}}
}

// The immutable state lists names known to be types before the parse.
func knownType(candidate p.Candidate) bool {
	head := candidate.Parts.Tokens("head")
	isType := len(head) == 1 && slices.Contains(strings.Fields(string(candidate.Before)), head[0].Text)
	return candidate.Kind == reading(isType)
}

func reading(isType bool) tree.Kind {
	if isType {
		return "declaration"
	}
	return "multiply"
}

func describe(root tree.Node, symbols *eval.Env[bool]) (string, error) {
	return eval.Fold(root, symbols, map[tree.Kind]eval.Rule[string, bool]{
		"file": func(_ eval.Frame, env *eval.Env[bool], child eval.Descend[string, bool]) (string, error) {
			return child(0, env)
		},
		tree.KindAmbiguity: selectReading,
		"declaration":      describePair("declare %s as a pointer to %s"),
		"multiply":         describePair("multiply %s by %s"),
	})
}

func selectReading(frame eval.Frame, env *eval.Env[bool], child eval.Descend[string, bool]) (string, error) {
	first, _ := frame.Node.Child(0)
	isType, _ := env.Get(nameAt(first, 0))
	for i := 0; ; i++ {
		alternative, ok := frame.Node.Child(i)
		if !ok {
			return "", fmt.Errorf("no reading for the symbol table")
		}
		if alternative.Info().Kind == reading(isType) {
			return child(i, env)
		}
	}
}

func describePair(format string) eval.Rule[string, bool] {
	return func(frame eval.Frame, _ *eval.Env[bool], _ eval.Descend[string, bool]) (string, error) {
		left, right := nameAt(frame.Node, 0), nameAt(frame.Node, 1)
		if frame.Node.Info().Kind == "declaration" {
			left, right = right, left
		}
		return fmt.Sprintf(format, left, right), nil
	}
}

func nameAt(node tree.Node, index int) string {
	child, _ := node.Child(index)
	return child.(*tree.Value[string]).Value()
}

func TestAmbiguityExample(t *testing.T) {
	build := reference.Builders("name")
	unfiltered := reference.Engine(t, grammar(nil), build)
	filtered := reference.Engine(t, grammar(knownType), build)
	const input = "T * x;"
	const packed = `(file (leafkit:ambiguity (declaration (name "T") (name "x")) (multiply (name "T") (name "x"))))`

	for _, streamed := range []bool{false, true} {
		root := reference.Parse(t, unfiltered, source, input, streamed, "")
		reference.Check(t, root, packed)
		for _, tc := range []struct {
			state         p.State
			isType        bool
			tree, meaning string
		}{
			{"Other T", true, `(file (declaration (name "T") (name "x")))`, "declare x as a pointer to T"},
			{"Other", false, `(file (multiply (name "T") (name "x")))`, "multiply T by x"},
		} {
			selected := reference.Parse(t, filtered, source, input, streamed, tc.state)
			reference.Check(t, selected, tc.tree)
			symbols := eval.NewEnv[bool](nil)
			symbols.Set("T", tc.isType)
			for _, reading := range []tree.Node{root, selected} {
				got, err := describe(reading, symbols)
				if assert.NoErrorf(t, err, "describe = %q, %v, want %q", got, err, tc.meaning) {
					assert.Equalf(t, tc.meaning, got, "describe = %q, %v, want %q", got, err, tc.meaning)
				}
			}
		}
		// Both lookups used the same unfiltered tree.
		reference.Check(t, root, packed)
	}
}

func TestBrokenAmbiguityInput(t *testing.T) {
	engine := reference.Engine(t, grammar(nil), reference.Builders("name"))
	for _, tc := range []struct {
		input string
		error reference.Diagnostic
	}{
		{"T * ;", reference.Diagnostic{Slot: "file", Start: 0, End: 5, At: 4, Found: ";", Skipped: "T*;", Expected: []p.Pattern{p.Kind("name")}}},
		{"T x;", reference.Diagnostic{Slot: "file", Start: 0, End: 4, At: 2, Found: "x", Skipped: "Tx;", Expected: []p.Pattern{p.Kind("*")}}},
	} {
		t.Run(tc.input, func(t *testing.T) {
			for _, streamed := range []bool{false, true} {
				root := reference.Parse(t, engine, source, tc.input, streamed, "")
				reference.Check(t, root, "(file (leafkit:error))", tc.error)
			}
		})
	}
}
