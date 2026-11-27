package tests

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

func chooseAll(slot string, alternatives ...p.Rule) p.Rule {
	return p.Choose(p.ChoiceSpec{Policy: p.All, Slot: slot}, alternatives...)
}

func ambiguityGrammar(rule p.Rule) p.Grammar {
	return p.Grammar{Start: "start", Rules: map[string]p.Rule{"start": rule}}
}

func parseAmbiguity(t *testing.T, engine *p.Engine[tree.Node], input string, chunked bool) tree.Node {
	t.Helper()
	size := len(input)
	cut, final := size, true
	if chunked {
		cut, final = 0, false
	}
	src := sourceFor(expressionTokens(input), &size, &cut, &final)
	session := engine.Start(src, "")
	if chunked {
		for ; cut <= size; cut++ {
			paused(t, session)
		}
		final = true
	}
	return done(t, session)
}

func childAt(t *testing.T, node tree.Node, index int) tree.Node {
	t.Helper()
	child, ok := node.Child(index)
	require.Truef(t, ok, "%q has no child %d", node.Info().Kind, index)
	return child
}

func TestAmbiguousDeclarationResolution(t *testing.T) {
	const input = "T * x;"
	tokenName := p.Kind("word")
	word := p.Make("word", "word", p.Tok(tokenName))
	arm := func(kind tree.Kind) p.Rule {
		return p.Make("stmt", kind, p.Seq(p.Bind("head", word), skip("*"), p.Bind("name", word), skip(";")))
	}
	makeGrammar := func(filter func(p.Candidate) bool) p.Grammar {
		return ambiguityGrammar(p.Choose(p.ChoiceSpec{Policy: p.All, Slot: "stmt", Filter: filter},
			arm("declaration"), arm("multiply-stmt")))
	}

	// The fixture sends the type name as an ordinary word token.
	tokens := expressionTokens(input)
	tokens[0].Kind = "word"
	end, cut, final := len(input), len(input), true
	parse := func(engine *p.Engine[tree.Node], state p.State) tree.Node {
		return done(t, engine.Start(sourceFor(tokens, &end, &cut, &final), state))
	}

	engine := configured(t, makeGrammar(nil), builders(), p.Limits{})
	whole := parse(engine, "")
	packed := childAt(t, whole, 0)
	require.Equalf(t, tree.KindAmbiguity, packed.Info().Kind, "statement ambiguity = %+v", packed.Info())
	require.Equalf(t, span(0, end), packed.Info().Span, "statement ambiguity = %+v", packed.Info())
	{
		got := expressionShape(packed)
		require.Equalf(t, "(leafkit:ambiguity (declaration T x) (multiply-stmt T x))", got, "all readings: %s", got)
	}
	for _, row := range []struct {
		state p.State
		kind  tree.Kind
	}{{"T", "declaration"}, {"Other", "multiply-stmt"}} {
		t.Run(string(row.kind), func(t *testing.T) {
			build := builders()
			node := build.Node
			calls := 0
			build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
				if in.Kind == "declaration" || in.Kind == "multiply-stmt" {
					calls++
					if in.Kind != row.kind {
						return nil, fmt.Errorf("discarded factory ran for %s", in.Kind)
					}
				}
				return node(in)
			}
			filter := func(c p.Candidate) bool {
				head := c.Parts.Tokens("head")
				require.Lenf(t, head, 1, "candidate lost recognition context: %+v %v", c, head)
				require.Equalf(t, "T", head[0].Text, "candidate lost recognition context: %+v %v", c, head)
				require.Equalf(t, row.state, c.Before, "candidate lost recognition context: %+v %v", c, head)
				require.Equalf(t, row.state, c.After, "candidate lost recognition context: %+v %v", c, head)
				return (c.Kind == "declaration") == (string(c.Before) == head[0].Text)
			}
			filtered := childAt(t, parse(configured(t, makeGrammar(filter), build, p.Limits{}), row.state), 0)
			var late tree.Node
			for i := 0; ; i++ {
				reading, ok := packed.Child(i)
				if !ok {
					break
				}
				head := childAt(t, reading, 0).(*tree.Value[string]).Value()
				if (reading.Info().Kind == "declaration") == (head == string(row.state)) {
					late = reading
					break
				}
			}
			require.Equalf(t, 1, calls, "filter and later resolution differ: %v %v (%d factories)", filtered, late, calls)
			require.Equalf(t, row.kind, filtered.Info().Kind, "filter and later resolution differ: %v %v (%d factories)", filtered, late, calls)
			require.Truef(t, late != nil, "filter and later resolution differ: %v %v (%d factories)", filtered, late, calls)
			require.Equalf(t, expressionShape(late), expressionShape(filtered), "filter and later resolution differ: %v %v (%d factories)", filtered, late, calls)
		})
	}
}

func TestDanglingElseKeepsBothFullReadings(t *testing.T) {
	word := p.Make("word", "word", tagged("word"))
	keyword := func(text string) p.Rule { return p.Drop(p.Tok(p.Word(text))) }
	stmt := p.Ref("stmt")
	production := func(kind tree.Kind, parts ...p.Rule) p.Rule { return p.Make("stmt", kind, p.Seq(parts...)) }
	grammar := p.Grammar{Start: "stmt", Rules: map[string]p.Rule{
		"stmt": chooseAll("stmt",
			production("if", keyword("if"), p.Bind("test", word), p.Bind("then", stmt)),
			production("if-else", keyword("if"), p.Bind("test", word), p.Bind("then", stmt), keyword("else"), p.Bind("else", stmt)),
			production("command", word, skip(";"))),
	}}
	engine := configured(t, grammar, builders(), p.Limits{})
	const input = "if a if b p; else q;"
	whole := parseAmbiguity(t, engine, input, false)
	chunked := parseAmbiguity(t, engine, input, true)
	const want = "(file (leafkit:ambiguity (if a (if-else b (command p) (command q))) (if-else a (if b (command p)) (command q))))"
	for _, root := range []tree.Node{whole, chunked} {
		{
			got := expressionShape(root)
			require.Equalf(t, want, got, "dangling else = %s, want %s", got, want)
		}
		choices := childAt(t, root, 0)
		inner := childAt(t, childAt(t, choices, 0), 1)
		outer := childAt(t, choices, 1)
		require.Samef(t, childAt(t, outer, 2), childAt(t, inner, 2), "complete readings did not share the else command")
	}
}

func packedExpressionGrammar() p.Grammar {
	expr := p.Ref("expr")
	return p.Grammar{Start: "expr", Rules: map[string]p.Rule{
		"expr": chooseAll("expr",
			p.Make("expr", "binary", p.Seq(p.Bind("left", expr), skip("+"), p.Bind("right", expr))),
			p.Make("expr", "number", tagged("number"))),
	}}
}

func TestPackedLeftRecursiveExpression(t *testing.T) {
	grammar := packedExpressionGrammar()
	const n = 24
	input := strings.Repeat("1+", n-1) + "1"
	build := builders()
	normal := build.Node
	reductions := 0
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		if in.Kind == "binary" {
			reductions++
		}
		return normal(in)
	}
	engine := configured(t, grammar, build, p.Limits{})
	root := parseAmbiguity(t, engine, input, false)
	choices := childAt(t, root, 0)
	require.Equalf(t, tree.KindAmbiguity, choices.Info().Kind, "binary ambiguity = %+v", choices.Info())
	var splits int
	for ; ; splits++ {
		alternative, ok := choices.Child(splits)
		if !ok {
			break
		}
		left := childAt(t, alternative, 0)
		{
			wantEnd := 2*splits + 1
			require.Equalf(t, span(0, wantEnd), left.Info().Span, "split %d left span is %+v, want end %d", splits, left.Info().Span, wantEnd)
		}
	}
	require.Equalf(t, n-1, splits, "root has %d splits, want %d", splits, n-1)
	// A binary reduction is identified by start, split and end. The
	// packed forest has one such reduction per triple, one leaf per
	// operand and at most one ambiguity wrapper per interval.
	wantReductions := (n + 1) * n * (n - 1) / 6
	require.Equalf(t, wantReductions, reductions, "built %d binary reductions, want %d", reductions, wantReductions)
	nodes, edges := 0, 0
	for node := range tree.WalkUnique(root) {
		nodes++
		for i := 0; ; i++ {
			if _, ok := node.Child(i); !ok {
				break
			}
			edges++
		}
	}
	require.LessOrEqualf(t, nodes, 1+n+n*(n+1)/2+wantReductions, "published forest exceeds cubic bound: %d nodes, %d edges", nodes, edges)
	require.LessOrEqualf(t, edges, 1+3*wantReductions, "published forest exceeds cubic bound: %d nodes, %d edges", nodes, edges)
}

func TestPackedMalformedExpressionKeepsWorkBound(t *testing.T) {
	// The final plus leaves every reading one operand short. Resolving the
	// failure should share its subproblems within the chart's work limit.
	const n = 48
	input := strings.Repeat("1+", n)
	engine := configured(t, packedExpressionGrammar(), builders(), p.Limits{Steps: 500_000})
	for _, chunked := range []bool{false, true} {
		root := parseAmbiguity(t, engine, input, chunked)
		{
			err := tree.Validate(root)
			require.NoErrorf(t, err, "chunked=%t: %v", chunked, err)
		}
		node, ok := childAt(t, root, 0).(*p.ErrorNode)
		require.Truef(t, ok, "chunked=%t: expected a file diagnostic", chunked)
		require.Equalf(t, "file", node.Slot(), "chunked=%t: expected a file diagnostic", chunked)
		{
			_, ok := root.Child(1)
			require.Falsef(t, ok, "chunked=%t: expected exactly one diagnostic", chunked)
		}
		problem := node.Problem()
		require.Equalf(t, span(0, len(input)), problem.Span, "chunked=%t: unexpected file diagnostic %+v", chunked, problem)
		require.Truef(t, problem.Found.End, "chunked=%t: unexpected file diagnostic %+v", chunked, problem)
		require.Equalf(t, span(len(input), len(input)), problem.Found.Point, "chunked=%t: unexpected file diagnostic %+v", chunked, problem)
		require.Truef(t, slices.Equal(problem.Expected, []p.Pattern{p.Kind("number")}), "chunked=%t: unexpected file diagnostic %+v", chunked, problem)
		require.Lenf(t, problem.Skipped, len(input), "chunked=%t: unexpected file diagnostic %+v", chunked, problem)
	}
}

func TestUnambiguousChoiceAgreesWithFirst(t *testing.T) {
	numeric := p.Make("atom", "number", tagged("number"))
	word := p.Make("atom", "word", tagged("word"))
	makeGrammar := func(spec p.ChoiceSpec) p.Grammar {
		atom := p.Choose(spec, numeric, word)
		return ambiguityGrammar(p.Make("pair", "pair", p.Seq(p.Bind("left", atom), skip(":"), p.Bind("right", atom))))
	}
	build := builders()
	node := build.Node
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		if in.Kind == "pair" {
			if len(in.Fields) != 2 || in.Fields[0].Name != "left" || in.Fields[1].Name != "right" ||
				in.Fields[0].Tokens.Len() != 1 || in.Fields[1].Tokens.Len() != 1 || in.Tokens.Len() != 1 {
				return nil, fmt.Errorf("changed token ownership: %+v", in)
			}
		}
		return node(in)
	}
	first := configured(t, makeGrammar(p.ChoiceSpec{}), build, p.Limits{})
	all := configured(t, makeGrammar(p.ChoiceSpec{Policy: p.All, Slot: "atom"}), build, p.Limits{})
	for _, input := range []string{"1:x", "a:2"} {
		a := parseAmbiguity(t, first, input, false)
		b := parseAmbiguity(t, all, input, true)
		equalRecords(t, b, records(a))
	}
}
