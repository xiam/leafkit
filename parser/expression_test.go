package parser_test

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

func expressionSource(kinds ...p.TokenKind) *fixture {
	return &fixture{read: func(m p.Mark, _ p.Request) (p.Read, error) {
		return expressionRead(m, kinds), nil
	}}
}

func expressionRead(m p.Mark, kinds []p.TokenKind) p.Read {
	if m.Byte == len(kinds) {
		return eof(m.Byte)
	}
	kind := kinds[m.Byte]
	return ready(token(kind, string(kind), m.Byte, m.Byte+1), p.Mark{Byte: m.Byte + 1})
}

func expressionWord() p.Rule { return p.Make("atom", "word", tagged("word")) }

func TestExpressionConfiguration(t *testing.T) {
	op := p.Operator{Form: p.Infix, Match: p.Kind("+"), Kind: "add"}
	for _, row := range []struct {
		name string
		edit func(*p.Operator)
	}{
		{"fixity", func(op *p.Operator) { op.Form = 255 }},
		{"empty pattern", func(op *p.Operator) { op.Match = p.Pattern{} }},
		{"EOF pattern", func(op *p.Operator) { op.Match = p.EndOfFile() }},
		{"power", func(op *p.Operator) { op.Power = -1 }},
		{"kind", func(op *p.Operator) { op.Kind = "" }},
		{"association", func(op *p.Operator) { op.Assoc = 255 }},
		{"infix tail", func(op *p.Operator) { op.Tail = tagged("word") }},
		{"request", func(op *p.Operator) { op.Goal = &p.Request{Layout: 255} }},
		{"tail reference", func(op *p.Operator) { op.Form, op.Tail = p.Postfix, p.Ref("missing") }},
	} {
		t.Run(row.name, func(t *testing.T) {
			bad := op
			row.edit(&bad)
			_, err := p.Configure(single(p.Expr("expr", expressionWord(), bad)), builders(), p.Limits{})
			require.ErrorIsf(t, err, p.ErrGrammar, "Configure() error = %v", err)
		})
	}
	for _, atom := range []p.Rule{p.Opt(tagged("word")), p.Ref("start")} {
		{
			_, err := p.Configure(single(p.Expr("expr", atom, op)), builders(), p.Limits{})
			require.ErrorIsf(t, err, p.ErrGrammar, "Configure() accepted empty or left-recursive atom, error = %v", err)
		}
	}
	unknown := p.Program("empty", map[string]p.ProgramState{"empty": returnStep()})
	engine := configured(t, single(p.Expr("expr", unknown)), builders(), p.Limits{})
	{
		_, _, err := engine.Start(expressionSource(), "").Run()
		require.ErrorIsf(t, err, p.ErrGrammar, "Run() accepted empty atom callback, error = %v", err)
	}
}

func TestExpressionBuildFields(t *testing.T) {
	rule := p.Expr("expression", expressionWord(),
		p.Operator{Form: p.Prefix, Match: p.Kind("-"), Power: 2, Kind: "negate"},
		p.Operator{Form: p.Infix, Match: p.Kind("+"), Power: 1, Kind: "add"},
		p.Operator{Form: p.Postfix, Match: p.Kind("."), Power: 3, Kind: "member", Tail: expressionWord()},
		p.Operator{Form: p.Postfix, Match: p.Kind("!"), Power: 3, Kind: "bang"},
	)
	type field struct {
		name, tokens, nodeKind string
	}
	want := map[tree.Kind][]field{
		"negate": {{"operator", "-", ""}, {"operand", "word", "word"}},
		"add":    {{"left", "-,word", "negate"}, {"operator", "+", ""}, {"right", "word,.,word,!", "bang"}},
		"member": {{"left", "word", "word"}, {"operator", ".", ""}, {"suffix", "word", "word"}},
		"bang":   {{"left", "word,.,word", "member"}, {"operator", "!", ""}},
	}
	tape := func(r p.TokenRange) string {
		var text []string
		for i := 0; i < r.Len(); i++ {
			tok, _ := r.At(i)
			text = append(text, tok.Text)
		}
		return strings.Join(text, ",")
	}

	b := builders()
	buildNode := b.Node
	var built []tree.Kind
	b.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		built = append(built, in.Kind)
		if fields, ok := want[in.Kind]; ok {
			var got []field
			for _, f := range in.Fields {
				item := field{name: f.Name, tokens: tape(f.Tokens)}
				if len(f.Nodes) > 0 {
					item.nodeKind = string(f.Nodes[0].Info().Kind)
				}
				got = append(got, item)
			}
			assert.Falsef(t, in.Slot != "expression" || !reflect.DeepEqual(got, fields), "%s in slot %s fields = %v, want %v", in.Kind, in.Slot, got, fields)
			for _, f := range fields {
				assert.Falsef(t, f.name == "operator" && tape(in.Tokens) != f.tokens, "%s owns tokens %q, want %q", in.Kind, tape(in.Tokens), f.tokens)
			}
		}
		return buildNode(in)
	}
	engine := configured(t, single(rule), b, p.Limits{})
	done(t, engine.Start(expressionSource("-", "word", "+", "word", ".", "word", "!"), ""))
	{
		want := []tree.Kind{"word", "negate", "word", "word", "member", "bang", "add"}
		require.Equalf(t, want, built, "build order = %v, want %v", built, want)
	}
}

func TestExpressionOperatorRequestsResume(t *testing.T) {
	prefix := p.Request{Goal: "prefix"}
	miss := p.Request{Goal: "miss"}
	division := p.Request{Goal: "division"}
	ops := []p.Operator{
		{Form: p.Prefix, Match: p.Kind("-"), Power: 20, Kind: "negate", Goal: &prefix},
		{Form: p.Infix, Match: p.Kind("/"), Power: 10, Kind: "miss", Goal: &miss},
		{Form: p.Infix, Match: p.Kind("/"), Power: 10, Kind: "divide", Goal: &division},
		{Form: p.Postfix, Match: p.Kind("/"), Power: 10, Kind: "shadowed", Goal: &division},
	}
	rule := p.Expr("expr", expressionWord(), ops...)
	// The grammar owns its operator values and Goal requests.
	ops[0].Kind, prefix.Goal = "changed", "changed"
	var atSlash []string
	available := false
	src := &fixture{read: func(m p.Mark, req p.Request) (p.Read, error) {
		switch m.Byte {
		case 0:
			if req.Goal != "prefix" {
				return p.Read{}, fmt.Errorf("prefix request = %q", req.Goal)
			}
		case 3:
			if m.Key != "division-successor" {
				return p.Read{}, fmt.Errorf("restored request lost the successor mark: %+v", m)
			}
			fallthrough
		case 1:
			if req.Goal != "" && req.Goal != "prefix" {
				return p.Read{}, fmt.Errorf("request leaked into atom: %q", req.Goal)
			}
		case 2:
			atSlash = append(atSlash, req.Goal)
			switch req.Goal {
			case "miss":
				return expressionRead(m, []p.TokenKind{"-", "word", "other", "word"}), nil
			case "division":
				if !available {
					return shortage(), nil
				}
				return ready(token("/", "/", 2, 3), p.Mark{Byte: 3, Key: "division-successor"}), nil
			default:
				return p.Read{}, fmt.Errorf("unexpected operator request at slash: %q", req.Goal)
			}
		}
		return expressionRead(m, []p.TokenKind{"-", "word", "/", "word"}), nil
	}}
	engine := configured(t, single(rule), builders(), p.Limits{})
	session := engine.Start(src, "")
	paused(t, session)
	available = true
	root := done(t, session)
	equalRecords(t, root, []record{
		{"file", span(0, 4), "", 1}, {"divide", span(0, 4), "", 2},
		{"negate", span(0, 2), "", 1}, {"word", span(1, 2), "word", 0},
		{"word", span(3, 4), "word", 0},
	})
	{
		want := []string{"miss", "division", "division"}
		require.Equalf(t, want, atSlash, "requests at slash = %v, want %v", atSlash, want)
	}
}

func TestExpressionSyntaxFailures(t *testing.T) {
	grammar := p.Grammar{Start: "expr", Rules: map[string]p.Rule{
		"expr": p.Expr("expr", p.Ref("atom"),
			p.Operator{Form: p.Infix, Match: p.Kind("<"), Power: 1, Kind: "compare", Assoc: p.NonAssoc},
			p.Operator{Form: p.Infix, Match: p.Kind("="), Power: 1, Kind: "equal"},
			p.Operator{Form: p.Prefix, Match: p.Kind("-"), Power: 2, Kind: "negate"},
			p.Operator{Form: p.Postfix, Match: p.Kind("["), Power: 3, Kind: "index", Tail: p.Seq(p.Ref("expr"), skip("]"))},
		),
		"atom": p.Choose(p.ChoiceSpec{}, expressionWord(), p.Seq(skip("("), p.Ref("expr"), skip(")"))),
	}}
	b := builders()
	buildNode := b.Node
	calls := 0
	b.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		calls++
		return buildNode(in)
	}
	engine := configured(t, grammar, b, p.Limits{})
	for _, row := range []struct {
		name     string
		kinds    []p.TokenKind
		position int
		want     []p.Pattern
	}{
		{"non-associative chain", []p.TokenKind{"word", "<", "word", "<", "word"}, 3, nil},
		{"equal-power infix after non-associative", []p.TokenKind{"word", "<", "word", "=", "word"}, 3, nil},
		{"missing prefix operand", []p.TokenKind{"-"}, 1, []p.Pattern{p.Kind("word"), p.Kind("(")}},
		{"missing suffix closer", []p.TokenKind{"word", "[", "word"}, 3, []p.Pattern{p.Kind("]")}},
		{"missing infix operand", []p.TokenKind{"word", "<"}, 2, []p.Pattern{p.Kind("word"), p.Kind("(")}},
	} {
		t.Run(row.name, func(t *testing.T) {
			root := done(t, engine.Start(expressionSource(row.kinds...), ""))
			child, _ := root.Child(0)
			diagnostic, ok := child.(*p.ErrorNode)
			require.Truef(t, ok, "file child = %v, want an error node", child)
			problem := diagnostic.Problem()
			require.Equalf(t, span(row.position, row.position), problem.Found.Point, "problem = %#v, constructions = %d", problem, calls)
			require.Equalf(t, row.want, problem.Expected, "problem = %#v, constructions = %d", problem, calls)
			require.Equalf(t, 0, calls, "problem = %#v, constructions = %d", problem, calls)
		})
	}
	done(t, engine.Start(expressionSource("(", "word", "<", "word", ")", "<", "word"), ""))
}

func TestExpressionPostfixLineBoundary(t *testing.T) {
	rule := p.Expr("expr", expressionWord(),
		p.Operator{Form: p.Postfix, Match: p.OnSameLine(p.Kind("!")), Kind: "postfix"},
	)
	engine := configured(t, single(rule), builders(), p.Limits{})
	done(t, engine.Start(expressionSource("word", "!"), ""))
	src := expressionSource("word", "!")
	read := src.read
	src.read = func(m p.Mark, req p.Request) (p.Read, error) {
		out, err := read(m, req)
		out.Token.BreakBefore = m.Byte == 1
		return out, err
	}
	root := done(t, engine.Start(src, ""))
	child, _ := root.Child(1)
	diagnostic, ok := child.(*p.ErrorNode)
	require.Truef(t, ok, "postfix across a line boundary: %v", child)
	require.Equalf(t, span(1, 1), diagnostic.Problem().Found.Point, "postfix across a line boundary: %v", child)
}

func TestExpressionRollback(t *testing.T) {
	expr := p.Expr("expr", expressionWord(), p.Operator{Form: p.Infix, Match: p.Kind("+"), Kind: "add"})
	grammar := single(p.Choose(p.ChoiceSpec{Lookahead: 5},
		p.Seq(expr, tagged("!")),
		p.Make("fallback", "fallback", p.Seq(skip("word"), skip("+"), skip("word"), skip(";"))),
	))
	b := builders()
	buildNode := b.Node
	b.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		assert.Equalf(t, tree.Kind("fallback"), in.Kind, "abandoned expression built %s", in.Kind)
		return buildNode(in)
	}
	engine := configured(t, grammar, b, p.Limits{})
	root := done(t, engine.Start(expressionSource("word", "+", "word", ";"), ""))
	equalRecords(t, root, []record{{"file", span(0, 4), "", 1}, {"fallback", span(0, 4), "", 0}})
}

func TestExpressionLongChains(t *testing.T) {
	for _, form := range []struct {
		name  string
		op    p.Operator
		depth func(int) int
	}{
		{"left", p.Operator{Form: p.Infix, Match: p.Kind("+"), Power: math.MaxInt, Kind: "op"}, func(int) int { return 5 }},
		{"right", p.Operator{Form: p.Infix, Match: p.Kind("+"), Assoc: p.Right, Kind: "op"}, func(n int) int { return n + 3 }},
		{"prefix", p.Operator{Form: p.Prefix, Match: p.Kind("+"), Kind: "op"}, func(n int) int { return n + 3 }},
		{"postfix", p.Operator{Form: p.Postfix, Match: p.Kind("+"), Kind: "op", Tail: skip("]")}, func(int) int { return 3 }},
	} {
		t.Run(form.name, func(t *testing.T) {
			for _, n := range []int{128, 2048} {
				var kinds []p.TokenKind
				if form.op.Form != p.Prefix {
					kinds = append(kinds, "word")
				}
				for i := 0; i < n; i++ {
					kinds = append(kinds, "+")
					switch form.op.Form {
					case p.Infix:
						kinds = append(kinds, "word")
					case p.Postfix:
						kinds = append(kinds, "]")
					}
				}
				if form.op.Form == p.Prefix {
					kinds = append(kinds, "word")
				}

				b := builders()
				buildNode := b.Node
				constructions := 0
				b.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
					constructions++
					return buildNode(in)
				}
				src := expressionSource(kinds...)
				read := src.read
				reads := 0
				src.read = func(m p.Mark, req p.Request) (p.Read, error) {
					reads++
					return read(m, req)
				}
				engine := configured(t, single(p.Expr("expr", expressionWord(), form.op)), b,
					p.Limits{Steps: 6 * len(kinds), Depth: form.depth(n)})
				done(t, engine.Start(src, ""))
				wantBuilds := n + 1
				if form.op.Form == p.Infix {
					wantBuilds += n
				}
				require.Equalf(t, len(kinds)+1, reads, "%d operators took %d reads and %d node builds, want %d and %d", n, reads, constructions, len(kinds)+1, wantBuilds)
				require.Equalf(t, wantBuilds, constructions, "%d operators took %d reads and %d node builds, want %d and %d", n, reads, constructions, len(kinds)+1, wantBuilds)
			}
		})
	}
}
