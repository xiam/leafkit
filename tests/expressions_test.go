package tests

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

func expressionGrammar() (p.Grammar, []p.Operator) {
	ops := []p.Operator{
		{Form: p.Prefix, Match: p.Kind("-"), Power: 30, Kind: "negate"},
		{Form: p.Infix, Match: p.Kind("+"), Power: 10, Kind: "add"},
		{Form: p.Infix, Match: p.Kind("-"), Power: 10, Kind: "subtract"},
		{Form: p.Infix, Match: p.Kind("*"), Power: 20, Kind: "multiply"},
		{Form: p.Infix, Match: p.Kind("^"), Power: 25, Assoc: p.Right, Kind: "power"},
		{Form: p.Postfix, Match: p.Kind("("), Power: 40, Kind: "call",
			Tail: p.Seq(p.Sep(p.Ref("expr"), skip(","), false), skip(")"))},
		{Form: p.Postfix, Match: p.Kind("["), Power: 40, Kind: "index",
			Tail: p.Seq(p.Ref("expr"), skip("]"))},
		{Form: p.Postfix, Match: p.Kind("."), Power: 40, Kind: "member",
			Tail: p.Make("member", "word", tagged("word"))},
	}
	return p.Grammar{Start: "expr", Rules: map[string]p.Rule{
		"expr": p.Expr("expr", p.Ref("atom"), ops...),
		"atom": p.Choose(p.ChoiceSpec{},
			p.Make("expr", "number", tagged("number")),
			p.Make("expr", "word", tagged("word")),
			p.Make("expr", "group", p.Seq(skip("("), p.Ref("expr"), skip(")"))),
		),
	}}, ops
}

// The oracle uses a small token fixture over ASCII source text.
func expressionTokens(input string) []p.Token {
	var tokens []p.Token
	for i := 0; i < len(input); {
		start := i
		if input[i] == ' ' {
			i++
			continue
		}
		kind := p.TokenKind(input[i : i+1])
		switch {
		case input[i] >= 'a' && input[i] <= 'z':
			kind = "word"
			for i < len(input) && input[i] >= 'a' && input[i] <= 'z' {
				i++
			}
		case input[i] >= '0' && input[i] <= '9':
			kind = "number"
			for i < len(input) && input[i] >= '0' && input[i] <= '9' {
				i++
			}
		default:
			i++
		}
		tokens = append(tokens, fixtureToken(kind, input[start:i], start, i))
	}
	return tokens
}

func expressionShape(node tree.Node) string {
	if value, ok := node.(*tree.Value[string]); ok {
		return value.Value()
	}
	var parts []string
	parts = append(parts, string(node.Info().Kind))
	for i := 0; ; i++ {
		child, ok := node.Child(i)
		if !ok {
			return "(" + strings.Join(parts, " ") + ")"
		}
		parts = append(parts, expressionShape(child))
	}
}

func TestExpressionOracles(t *testing.T) {
	rows := []struct{ name, input, want string }{
		{"multiplication before addition", "1 + 2 * 3", "(file (add 1 (multiply 2 3)))"},
		{"right associative power", "2 ^ 3 ^ 2", "(file (power 2 (power 3 2)))"},
		{"prefix before multiplication", "-a * b", "(file (multiply (negate a) b))"},
		{"left associative subtraction", "a - b - c", "(file (subtract (subtract a b) c))"},
		{"chained member and call", "f(x)(y).z", "(file (member (call (call f x) y) z))"},
		{"chained indexes and call", "a[i][j](k)[l]", "(file (index (call (index (index a i) j) k) l))"},
		{"empty and multiple arguments", "f()(a,b)", "(file (call (call f) a b))"},
		{"parenthesized override", "(1 + 2) * 3", "(file (multiply (group (add 1 2)) 3))"},
	}

	grammar, _ := expressionGrammar()
	factories := builders()
	buildNode, buildFile := factories.Node, factories.File
	calls := 0
	factories.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		calls++
		return buildNode(in)
	}
	factories.File = func(full tree.Span, children []tree.Node, trailing []p.Trivia) (tree.Node, error) {
		calls++
		return buildFile(full, children, trailing)
	}
	engine := configured(t, grammar, factories, p.Limits{})
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			end := len(row.input)
			tokens := expressionTokens(row.input)
			available, final := end, true
			whole := done(t, engine.Start(sourceFor(tokens, &end, &available, &final), ""))
			{
				got := expressionShape(whole)
				require.Equalf(t, row.want, got, "%s => %s, want %s", row.input, got, row.want)
			}
			if row.input == "f(x)(y).z" {
				equalRecords(t, whole, []record{
					{"file", span(0, 9), "", 1}, {"member", span(0, 9), "", 2},
					{"call", span(0, 7), "", 2}, {"call", span(0, 4), "", 2},
					{"word", span(0, 1), "f", 0}, {"word", span(2, 3), "x", 0},
					{"word", span(5, 6), "y", 0}, {"word", span(8, 9), "z", 0},
				})
			}
			calls = 0
			available, final = 0, false
			session := engine.Start(sourceFor(tokens, &end, &available, &final), "")
			for ; available <= end; available++ {
				paused(t, session)
				require.Equalf(t, 0, calls, "ran %d factories while paused at byte %d", calls, available)
			}
			final = true
			equalRecords(t, done(t, session), records(whole))
		})
	}
}

func TestExpressionOracleSensitivity(t *testing.T) {
	for _, row := range []struct {
		name, input, changed string
		operator             tree.Kind
		edit                 func(*p.Operator)
	}{
		{"multiplication before addition", "1 + 2 * 3", "(file (multiply (add 1 2) 3))",
			"multiply", func(op *p.Operator) { op.Power = 5 }},
		{"right associative power", "2 ^ 3 ^ 2", "(file (power (power 2 3) 2))",
			"power", func(op *p.Operator) { op.Assoc = p.Left }},
		{"prefix before multiplication", "-a * b", "(file (negate (multiply a b)))",
			"negate", func(op *p.Operator) { op.Power = 15 }},
		{"left associative subtraction", "a - b - c", "(file (subtract a (subtract b c)))",
			"subtract", func(op *p.Operator) { op.Assoc = p.Right }},
	} {
		t.Run(row.name, func(t *testing.T) {
			grammar, ops := expressionGrammar()
			engine := configured(t, grammar, builders(), p.Limits{})
			for i := range ops {
				if ops[i].Kind == row.operator {
					row.edit(&ops[i])
				}
			}
			grammar.Rules["expr"] = p.Expr("expr", p.Ref("atom"), ops...)
			changed := configured(t, grammar, builders(), p.Limits{})
			end, available, final := len(row.input), len(row.input), true
			tokens := expressionTokens(row.input)
			before := expressionShape(done(t, engine.Start(sourceFor(tokens, &end, &available, &final), "")))
			after := expressionShape(done(t, changed.Start(sourceFor(tokens, &end, &available, &final), "")))
			require.NotEqualf(t, before, after, "changed declaration produced %s (original %s), want %s", after, before, row.changed)
			require.Equalf(t, row.changed, after, "changed declaration produced %s (original %s), want %s", after, before, row.changed)
		})
	}
}

func TestExpressionStatementGrammar(t *testing.T) {
	grammar, _ := expressionGrammar()
	grammar.Start = "file"
	grammar.Rules["file"] = p.Many(p.Ref("stmt"))
	grammar.Rules["stmt"] = p.Choose(p.ChoiceSpec{}, p.Ref("let"), p.Ref("if"), p.Ref("expr-stmt"))
	grammar.Rules["let"] = p.Make("stmt", "let", p.Seq(
		p.Drop(p.Tok(p.Word("let"))), p.Bind("name", tagged("word")),
		skip("="), p.Bind("value", p.Ref("expr")), skip(";"),
	))
	grammar.Rules["if"] = p.Make("stmt", "if", p.Seq(
		p.Drop(p.Tok(p.Word("if"))), p.Bind("test", p.Ref("expr")), p.Bind("then", p.Ref("block")),
		p.Opt(p.Seq(p.Drop(p.Tok(p.Word("else"))), p.Bind("else", p.Ref("block")))),
	))
	grammar.Rules["expr-stmt"] = p.Make("stmt", "expr-stmt", p.Seq(p.Bind("value", p.Ref("expr")), skip(";")))
	grammar.Rules["block"] = p.Make("block", "block", p.Seq(
		skip("{"), p.Many(p.Ref("stmt")), skip("}"),
	))

	factories := builders()
	buildNode := factories.Node
	factories.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		if in.Kind == "let" {
			if len(in.Fields) != 2 || in.Fields[0].Name != "name" || in.Fields[1].Name != "value" {
				return nil, fmt.Errorf("binding fields = %v", in.Fields)
			}
			name, ok := in.Fields[0].Tokens.At(0)
			if !ok || name.Text != "x" {
				return nil, fmt.Errorf("binding name = %q", name.Text)
			}
		}
		if in.Kind == "if" {
			var names []string
			for _, field := range in.Fields {
				names = append(names, field.Name)
			}
			if strings.Join(names, ",") != "test,then,else" {
				return nil, fmt.Errorf("conditional fields = %v", names)
			}
		}
		return buildNode(in)
	}
	engine := configured(t, grammar, factories, p.Limits{})
	input := "let x = -(2 + 3 * 4); if x { f(x)(2); } else { g(0); }"
	want := "(file (let (negate (group (add 2 (multiply 3 4))))) " +
		"(if x (block (expr-stmt (call (call f x) 2))) (block (expr-stmt (call g 0)))))"
	end, available, final := len(input), 0, false
	session := engine.Start(sourceFor(expressionTokens(input), &end, &available, &final), "")
	for ; available <= end; available++ {
		paused(t, session)
	}
	final = true
	{
		got := expressionShape(done(t, session))
		require.Equalf(t, want, got, "%s => %s, want %s", input, got, want)
	}
}
