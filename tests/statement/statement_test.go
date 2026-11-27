package statement

import (
	"fmt"
	"strconv"
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

// guide:tokens
var trivia = adapter.Options{Trivia: []scan.TokenType{"space", "comment"}}

func scanner(cur *cursor.Reader) *scan.Scanner {
	lexer := scan.NewWithCursor(cur)
	lexer.MustAddRule("space", base.Whitespace())
	lexer.MustAddRule("comment", base.LineComment("//"))
	lexer.MustAddRule("name", core.RuneRun(core.UnicodeLetter))
	lexer.MustAddRule("number", core.RuneRun(core.ASCIIDigit))
	for _, symbol := range []string{"+", "*", "=", ";", ",", "(", ")", "{", "}"} {
		lexer.MustAddRule(scan.TokenType(symbol), core.Literal(symbol))
	}
	return lexer
}

// guide:end

func word(text string) p.Rule { return p.Drop(p.Tok(p.Word(text))) }

// guide:grammar
func grammar() p.Grammar {
	token, skip := reference.Token, reference.Skip
	name := p.Make("name", "name", token("name"))
	number := p.Make("number", "number", token("number"))
	expr, stmt, block := p.Ref("expr"), p.Ref("statement"), p.Ref("block")
	atom := p.Choose(p.ChoiceSpec{}, number, name, p.Seq(skip("("), expr, skip(")")))
	item := p.Seq(p.Not(p.EndOfFile()), p.Not(p.Kind("}")),
		p.Recover("statement", stmt, p.Boundary{At: []p.Pattern{p.Kind(";"), p.Kind("}")}}), p.Opt(skip(";")))
	return p.Grammar{Start: "file", Rules: map[string]p.Rule{
		"file":  p.Many(item),
		"block": p.Make("block", "block", p.Seq(skip("{"), p.Many(item), skip("}"))),
		"statement": p.Choose(p.ChoiceSpec{},
			p.Make("statement", "let", p.Seq(word("let"), name, skip("="), expr, skip(";"))),
			p.Make("statement", "if", p.Seq(word("if"), expr, block, p.Opt(p.Seq(word("else"), block)))),
			p.Make("statement", "expr-stmt", p.Seq(expr, skip(";")))),
		"expr": p.Expr("expr", atom,
			p.Operator{Form: p.Infix, Match: p.Kind("+"), Power: 10, Kind: "add"},
			p.Operator{Form: p.Infix, Match: p.Kind("*"), Power: 20, Kind: "multiply"},
			p.Operator{Form: p.Postfix, Match: p.Kind("("), Power: 30, Kind: "call",
				Tail: p.Seq(p.Sep(expr, skip(","), false), skip(")"))}),
	}}
}

// guide:end

func source(input *reference.Input) (p.Source, error) {
	cur := cursor.NewReader(input.Open(0, scan.ErrMoreInput))
	return adapter.New(input.ID(), scanner(cur), cur, p.Request{}, trivia)
}

// guide:parse
func parse(id tree.SourceID, text string) (tree.Node, error) {
	engine, err := p.Configure(grammar(), reference.Builders("name", "number"), p.Limits{})
	if err != nil {
		return nil, err
	}
	cur := cursor.NewReader(strings.NewReader(text))
	input, err := adapter.New(id, scanner(cur), cur, p.Request{}, trivia)
	if err != nil {
		return nil, err
	}
	root, status, err := engine.Start(input, "").Run()
	if err == nil && status != p.Done {
		return nil, fmt.Errorf("sealed input paused")
	}
	return root, err
}

// guide:end

// guide:evaluate
func evaluate(root tree.Node) (int, error) {
	rules := map[tree.Kind]eval.Rule[int, int]{
		"file":  sequence,
		"block": blockScope,
		"let":   bind,
		"if":    conditional,
		"expr-stmt": func(_ eval.Frame, env *eval.Env[int], child eval.Descend[int, int]) (int, error) {
			return child(0, env)
		},
		"name": lookup,
		"number": func(f eval.Frame, _ *eval.Env[int], _ eval.Descend[int, int]) (int, error) {
			return strconv.Atoi(text(f.Node))
		},
		"add":      binary(func(a, b int) int { return a + b }),
		"multiply": binary(func(a, b int) int { return a * b }),
		"call":     call,
	}
	return eval.Fold(root, eval.NewEnv[int](nil), rules)
}

// guide:end

func text(node tree.Node) string { return node.(*tree.Value[string]).Value() }

func sequence(f eval.Frame, env *eval.Env[int], child eval.Descend[int, int]) (int, error) {
	result := 0
	for i := 0; ; i++ {
		if _, ok := f.Node.Child(i); !ok {
			return result, nil
		}
		var err error
		result, err = child(i, env)
		if err != nil {
			return 0, err
		}
	}
}

func blockScope(f eval.Frame, env *eval.Env[int], child eval.Descend[int, int]) (int, error) {
	return sequence(f, eval.NewEnv(env), child)
}

func bind(f eval.Frame, env *eval.Env[int], child eval.Descend[int, int]) (int, error) {
	value, err := child(1, env)
	if err != nil {
		return 0, err
	}
	name, _ := f.Node.Child(0)
	env.Set(text(name), value)
	return value, nil
}

func conditional(f eval.Frame, env *eval.Env[int], child eval.Descend[int, int]) (int, error) {
	condition, err := child(0, env)
	if err != nil {
		return 0, err
	}
	if condition != 0 {
		return child(1, env)
	}
	if _, ok := f.Node.Child(2); ok {
		return child(2, env)
	}
	return 0, nil
}

func lookup(f eval.Frame, env *eval.Env[int], _ eval.Descend[int, int]) (int, error) {
	name := text(f.Node)
	if value, ok := env.Get(name); ok {
		return value, nil
	}
	return 0, fmt.Errorf("undefined name %q", name)
}

func binary(op func(int, int) int) eval.Rule[int, int] {
	return func(_ eval.Frame, env *eval.Env[int], child eval.Descend[int, int]) (int, error) {
		a, err := child(0, env)
		if err != nil {
			return 0, err
		}
		b, err := child(1, env)
		if err != nil {
			return 0, err
		}
		return op(a, b), nil
	}
}

func call(f eval.Frame, env *eval.Env[int], child eval.Descend[int, int]) (int, error) {
	callee, _ := f.Node.Child(0)
	if callee.Info().Kind != "name" {
		return 0, fmt.Errorf("call needs a function name")
	}
	name := text(callee)
	if name != "double" && name != "sum" {
		return 0, fmt.Errorf("unknown function %q", name)
	}
	var args []int
	for i := 1; ; i++ {
		if _, ok := f.Node.Child(i); !ok {
			break
		}
		value, err := child(i, env)
		if err != nil {
			return 0, err
		}
		args = append(args, value)
	}
	if name == "double" && len(args) == 1 {
		return 2 * args[0], nil
	}
	if name == "sum" && len(args) == 2 {
		return args[0] + args[1], nil
	}
	return 0, fmt.Errorf("wrong arity for %q", name)
}

func Example() {
	root, err := parse("statement-example", "let x = 2; if x { double(x + 1); } else { 0; }")
	if err != nil {
		panic(err)
	}
	value, err := evaluate(root)
	if err != nil {
		panic(err)
	}
	fmt.Println(value)
	// Output: 6
}

func TestStatementGrammar(t *testing.T) {
	engine := reference.Engine(t, grammar(), reference.Builders("name", "number"))
	cases := []struct {
		name, input, tree string
		value             int
		errors            []reference.Diagnostic
	}{
		{"binding and then block", "let x = 2; if x { double(x + 1); } else { 0; }",
			`(file (let (name "x") (number "2")) (if (name "x") (block (expr-stmt (call (name "double") (add (name "x") (number "1"))))) (block (expr-stmt (number "0")))))`, 6, nil},
		{"lazy else and precedence", "if 0 { missing(5); } else { sum(2, 3*4); }",
			`(file (if (number "0") (block (expr-stmt (call (name "missing") (number "5")))) (block (expr-stmt (call (name "sum") (number "2") (multiply (number "3") (number "4")))))))`, 14, nil},
		{"scope shadowing", "let x=2; if 1 { let x=9; x; } x;",
			`(file (let (name "x") (number "2")) (if (number "1") (block (let (name "x") (number "9")) (expr-stmt (name "x")))) (expr-stmt (name "x")))`, 2, nil},
		{"missing initializer", "let x = ; 7;",
			`(file (leafkit:error) (expr-stmt (number "7")))`, 0, []reference.Diagnostic{
				{Slot: "statement", Start: 0, End: 7, At: 8, Found: ";", Skipped: "letx=", Expected: []p.Pattern{p.Kind("number"), p.Kind("name"), p.Kind("(")}},
			}},
		{"broken block keeps its siblings", "if 1 { let x=; 2; } 3;",
			`(file (if (number "1") (block (leafkit:error) (expr-stmt (number "2")))) (expr-stmt (number "3")))`, 0, []reference.Diagnostic{
				{Slot: "statement", Start: 6, End: 13, At: 13, Found: ";", Skipped: "letx=", Expected: []p.Pattern{p.Kind("number"), p.Kind("name"), p.Kind("(")}},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, streamed := range []bool{false, true} {
				root := reference.Parse(t, engine, source, tc.input, streamed, "")
				reference.Check(t, root, tc.tree, tc.errors...)
				if len(tc.errors) == 0 {
					got, err := evaluate(root)
					if assert.NoErrorf(t, err, "evaluate = %d, %v, want %d", got, err, tc.value) {
						assert.Equalf(t, tc.value, got, "evaluate = %d, %v, want %d", got, err, tc.value)
					}
				}
			}
		})
	}
}
