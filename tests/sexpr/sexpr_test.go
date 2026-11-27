package sexpr

import (
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
	lexer.MustAddRule("atom", core.RuneRun(core.UnicodeLetter.Or(core.ASCIIDigit).Or(core.RuneSet("+-*"))))
	for _, symbol := range []string{"(", ")"} {
		lexer.MustAddRule(scan.TokenType(symbol), core.Literal(symbol))
	}
	return lexer
}

func source(input *reference.Input) (p.Source, error) {
	cur := cursor.NewReader(input.Open(0, scan.ErrMoreInput))
	return adapter.New(input.ID(), scanner(cur), cur, p.Request{}, adapter.Options{Trivia: []scan.TokenType{"space"}})
}

func grammar() p.Grammar {
	form := p.Ref("form")
	list := p.Make("form", "list", p.Seq(reference.Skip("("), p.Many(form), reference.Skip(")")))
	atom := p.Make("form", "atom", reference.Token("atom"))
	return p.Grammar{Start: "file", Rules: map[string]p.Rule{
		"file": p.Many(p.Seq(p.Not(p.EndOfFile()),
			p.Recover("form", form, p.Boundary{At: []p.Pattern{p.Kind(")")}, Consume: true}))),
		"form": p.Choose(p.ChoiceSpec{}, list, atom),
	}}
}

func printForms(root tree.Node) (string, error) {
	return eval.Fold(root, nil, map[tree.Kind]eval.Rule[string, struct{}]{
		"file": forms("", ""), "list": forms("(", ")"),
		"atom": func(frame eval.Frame, _ *eval.Env[struct{}], _ eval.Descend[string, struct{}]) (string, error) {
			return frame.Node.(*tree.Value[string]).Value(), nil
		},
	})
}

func forms(open, close string) eval.Rule[string, struct{}] {
	return func(frame eval.Frame, env *eval.Env[struct{}], child eval.Descend[string, struct{}]) (string, error) {
		var parts []string
		for i := 0; ; i++ {
			if _, ok := frame.Node.Child(i); !ok {
				return open + strings.Join(parts, " ") + close, nil
			}
			part, err := child(i, env)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
	}
}

func TestSExpressionGrammar(t *testing.T) {
	engine := reference.Engine(t, grammar(), reference.Builders("atom"))
	cases := []struct {
		name, input, shape, printed string
		errors                      []reference.Diagnostic
	}{
		{"lists and atoms", "(f (g 2) ())  λ",
			`(file (list (atom "f") (list (atom "g") (atom "2")) (list)) (atom "λ"))`, "(f (g 2) ()) λ", nil},
		{"unmatched close", "a ) b", `(file (atom "a") (leafkit:error) (atom "b"))`, "",
			[]reference.Diagnostic{{Slot: "form", Start: 1, End: 3, At: 2, Found: ")", Skipped: ")", Expected: []p.Pattern{p.Kind("("), p.Kind("atom")}}}},
		{"unclosed list", "(a (b)", "(file (leafkit:error))", "",
			[]reference.Diagnostic{{Slot: "form", Start: 0, End: 6, At: 6, Found: "<eof>", Skipped: "(a(b)", Expected: []p.Pattern{p.Kind(")")}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, streamed := range []bool{false, true} {
				root := reference.Parse(t, engine, source, tc.input, streamed, "")
				reference.Check(t, root, tc.shape, tc.errors...)
				if len(tc.errors) == 0 {
					got, err := printForms(root)
					if assert.NoErrorf(t, err, "print = %q, %v, want %q", got, err, tc.printed) {
						assert.Equalf(t, tc.printed, got, "print = %q, %v, want %q", got, err, tc.printed)
					}
				}
			}
		})
	}
}
