package parser_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

func chartChildren(t *testing.T, root tree.Node) []tree.Node {
	t.Helper()
	var children []tree.Node
	for i := 0; ; i++ {
		child, ok := root.Child(i)
		if !ok {
			return children
		}
		children = append(children, child)
	}
}

func chartKinds(t *testing.T, root tree.Node) []tree.Kind {
	t.Helper()
	var kinds []tree.Kind
	for _, child := range chartChildren(t, root) {
		kinds = append(kinds, child.Info().Kind)
	}
	return kinds
}

func allOf(slot string, arms ...p.Rule) p.Rule {
	return p.Choose(p.ChoiceSpec{Policy: p.All, Slot: slot}, arms...)
}

func TestLongestWaitsAndResumesWinningMark(t *testing.T) {
	wide := p.Request{Goal: "wide"}
	short := p.Make("stmt", "short", tagged("a"))
	long := p.Make("stmt", "long", p.Lex(wide, tagged("ab")))
	rule := p.Seq(p.Choose(p.ChoiceSpec{Policy: p.Longest}, short, long), tagged("!"))
	build := builders()
	original := build.Node
	factories := 0
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		factories++
		require.Equalf(t, tree.Kind("long"), in.Kind, "lost longest match to %q", in.Kind)
		return original(in)
	}
	engine := configured(t, single(rule), build, p.Limits{})
	open := true
	src := &fixture{read: func(mark p.Mark, req p.Request) (p.Read, error) {
		switch {
		case mark.Byte == 0 && req == wide:
			if open {
				return shortage(), nil
			}
			return ready(token("ab", "ab", 0, 2), p.Mark{Byte: 2, Key: "wide exit"}), nil
		case mark.Byte == 0:
			return ready(token("a", "a", 0, 1), p.Mark{Byte: 1}), nil
		case mark.Byte == 2 && mark.Key == "wide exit" && req == (p.Request{}):
			return ready(token("!", "!", 2, 3), p.Mark{Byte: 3}), nil
		case mark.Byte == 3:
			return eof(3), nil
		default:
			require.FailNowf(t, "read under the wrong continuation", "%+v %+v", mark, req)
			return p.Read{}, nil
		}
	}}
	session := engine.Start(src, "")
	paused(t, session)
	require.Equalf(t, 0, factories, "local longest match was built before final EOF")
	open = false
	root := done(t, session)
	{
		got := chartKinds(t, root)
		require.Truef(t, slices.Equal(got, []tree.Kind{"long"}), "root=%v factories=%d", got, factories)
		require.Equalf(t, 1, factories, "root=%v factories=%d", got, factories)
	}
}

func TestLongestKeepsOneReadingAndBreaksTiesByArm(t *testing.T) {
	a := p.Make("stmt", "first", tagged("a"))
	b := p.Make("stmt", "second", tagged("a"))
	for _, test := range []struct {
		name string
		rule p.Rule
		want tree.Kind
	}{
		{"same span", p.Choose(p.ChoiceSpec{Policy: p.Longest}, a, b), "first"},
		{"nested All pack", p.Choose(p.ChoiceSpec{Policy: p.Longest}, allOf("stmt", a, b)), "first"},
		{"filtered nested pack", p.Choose(p.ChoiceSpec{
			Policy: p.Longest, Slot: "stmt", Filter: func(c p.Candidate) bool { return c.Kind == "second" },
		}, allOf("stmt", a, b)), "second"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := done(t, configured(t, single(test.rule), builders(), p.Limits{}).Start(textSource("a"), ""))
			{
				got := chartKinds(t, root)
				require.Truef(t, slices.Equal(got, []tree.Kind{test.want}), "children=%v want %v", got, test.want)
			}
		})
	}
}

func TestAllKeepsLexicalExitsAndCorrelatedFileRoots(t *testing.T) {
	arm := func(goal string, tokenKind p.TokenKind) p.Rule {
		return p.Make("atom", tree.Kind(goal), p.Lex(p.Request{Goal: goal}, p.Tok(p.Kind(tokenKind))))
	}
	choice := allOf("atom", arm("wide", "ab"), p.Make("atom", "narrow",
		p.Lex(p.Request{Goal: "narrow"}, p.Seq(tagged("a"), tagged("b")))))
	end := allOf("end", p.Make("end", "wide-end", tagged("W")), p.Make("end", "narrow-end", tagged("N")))
	rootRule := p.Seq(choice, end)
	engine := configured(t, single(rootRule), builders(), p.Limits{})
	for _, chunked := range []bool{false, true} {
		open := chunked
		src := &fixture{read: func(mark p.Mark, req p.Request) (p.Read, error) {
			switch {
			case mark.Byte == 0 && req.Goal == "wide":
				if open {
					return shortage(), nil
				}
				return ready(token("ab", "ab", 0, 2), p.Mark{Byte: 2, Key: "wide"}), nil
			case mark.Byte == 0 && req.Goal == "narrow":
				return ready(token("a", "a", 0, 1), p.Mark{Byte: 1, Key: "narrow"}), nil
			case mark.Byte == 1 && req.Goal == "narrow":
				return ready(token("b", "b", 1, 2), p.Mark{Byte: 2, Key: "narrow"}), nil
			case mark.Byte == 2 && req == (p.Request{}):
				if mark.Key == "wide" {
					return ready(token("W", "!", 2, 3), p.Mark{Byte: 3, Key: "wide"}), nil
				}
				return ready(token("N", "!", 2, 3), p.Mark{Byte: 3, Key: "narrow"}), nil
			case mark.Byte == 3:
				return eof(3), nil
			default:
				require.FailNowf(t, "illegal read", "%+v %+v", mark, req)
				return p.Read{}, nil
			}
		}}
		session := engine.Start(src, "")
		if open {
			paused(t, session)
			open = false
		}
		root := done(t, session)
		roots := chartChildren(t, root)
		require.Equalf(t, tree.KindAmbiguity, root.Info().Kind, "wanted two full file readings, got %v", records(root))
		require.Equalf(t, span(0, 3), root.Info().Span, "wanted two full file readings, got %v", records(root))
		require.Lenf(t, roots, 2, "wanted two full file readings, got %v", records(root))
		{
			got := chartKinds(t, roots[0])
			require.Truef(t, slices.Equal(got, []tree.Kind{"wide", "wide-end"}), "first file alternative %#v", got)
		}
		{
			got := chartKinds(t, roots[1])
			require.Truef(t, slices.Equal(got, []tree.Kind{"narrow", "narrow-end"}), "second file alternative %#v", got)
		}
	}
}

func TestChartConfigurationAndLimits(t *testing.T) {
	base := p.Make("expr", "base", tagged("a"))
	for _, rule := range []p.Rule{
		p.Choose(p.ChoiceSpec{Policy: p.All}, base),
		p.Choose(p.ChoiceSpec{Policy: p.Longest, Lookahead: 2}, base),
		allOf("expr", p.Ref("start"), base),
		allOf("expr", p.Make("expr", "alias", p.Seq(p.Opt(tagged("a")), p.Ref("start"))), base),
	} {
		{
			_, err := p.Configure(single(rule), builders(), p.Limits{})
			assert.ErrorIsf(t, err, p.ErrGrammar, "invalid ambiguity grammar returned %v", err)
		}
	}
	for _, limits := range []p.Limits{{Descriptors: 1}, {ForestEdges: 1}, {Steps: 1}} {
		engine := configured(t, single(allOf("expr", base)), builders(), limits)
		root, status, err := engine.Start(textSource("a"), "").Run()
		require.Truef(t, root == nil, "limit=%+v: got %v, %v, %v", limits, root, status, err)
		require.Equalf(t, p.Done, status, "limit=%+v: got %v, %v, %v", limits, root, status, err)
		require.ErrorIsf(t, err, p.ErrLimit, "limit=%+v: got %v, %v, %v", limits, root, status, err)
	}
}

func TestChartRecoveryRanksCompleteReadings(t *testing.T) {
	// The first arm repairs at the end of the item. The second arm can
	// parse the same material cleanly. Only the clean reading is built.
	boundary := p.Boundary{At: []p.Pattern{p.Kind(";")}}
	bad := p.Make("stmt", "bad", p.Seq(tagged("a"), p.Recover("missing", tagged("b"), boundary), tagged(";")))
	good := p.Make("stmt", "good", p.Seq(tagged("a"), tagged(";")))
	build := builders()
	build.Error = func(string, p.Problem) (tree.Node, error) {
		require.FailNow(t, "discarded recovery factory ran")
		return nil, nil
	}
	engine := configured(t, single(allOf("stmt", bad, good)), build, p.Limits{})
	root := done(t, engine.Start(textSource("a;"), ""))
	{
		got := chartKinds(t, root)
		require.Truef(t, slices.Equal(got, []tree.Kind{"good"}), "complete readings=%v", got)
	}
}

func TestChartPrattAndProgramComposeWithPacks(t *testing.T) {
	atom := allOf("atom", p.Make("atom", "x", tagged("a")), p.Make("atom", "y", tagged("a")))
	expr := p.Expr("expr", atom, p.Operator{Form: p.Infix, Match: p.Kind("+"), Power: 1, Kind: "add"})
	grammar := single(p.Program("read", map[string]p.ProgramState{
		"read": callStep("expr", "done", "result"),
		"done": {Control: func(view p.View) p.Move {
			{
				got := view.Parts.Kinds("result")
				require.Truef(t, slices.Equal(got, []tree.Kind{"add"}), "program lost its result kind: %v", got)
			}
			{
				tokens := view.Parts.Tokens("result")
				require.Lenf(t, tokens, 3, "program lost recognition data: %v", tokens)
				require.Equalf(t, "+", tokens[1].Text, "program lost recognition data: %v", tokens)
			}
			return p.Return()
		}},
	}))
	grammar.Rules["expr"] = expr
	engine := configured(t, grammar, builders(), p.Limits{})
	root := done(t, engine.Start(textSource("a+a"), ""))
	add := chartChildren(t, root)[0]
	require.Equalf(t, tree.Kind("add"), add.Info().Kind, "operator did not retain both packed operands: %v", records(root))
	require.Truef(t, slices.Equal(chartKinds(t, add), []tree.Kind{tree.KindAmbiguity, tree.KindAmbiguity}), "operator did not retain both packed operands: %v", records(root))
}

func TestRepeatedChartRepairUnwindsToFile(t *testing.T) {
	atom := allOf("atom", p.Make("atom", "atom", tagged("a")))
	grammar := single(p.Program("loop", map[string]p.ProgramState{
		"loop": callStep("recover", "loop", ""),
	}))
	grammar.Rules["recover"] = p.Recover("item", atom, p.Boundary{At: []p.Pattern{p.Kind("}")}})
	engine := configured(t, grammar, builders(), p.Limits{Steps: 100})
	root := done(t, engine.Start(textSource("}tail"), ""))
	point := problemAt(t, root, 0, "item")
	file := problemAt(t, root, 1, "file")
	require.Lenf(t, chartChildren(t, root), 2, "repeated recovery escaped its guard: %v %v", point, file)
	require.Equalf(t, span(0, 0), point.Span, "repeated recovery escaped its guard: %v %v", point, file)
	require.Equalf(t, span(0, 5), file.Span, "repeated recovery escaped its guard: %v %v", point, file)
	require.Lenf(t, file.Skipped, 5, "repeated recovery escaped its guard: %v %v", point, file)
}

func TestChartConstructionFailuresStayFatal(t *testing.T) {
	arm := func(kind tree.Kind) p.Rule {
		return p.Make("atom", kind, tagged("a"))
	}
	grammar := single(allOf("atom", arm("left"), arm("right")))
	failure := errors.New("ambiguity factory failed")
	build := builders()
	build.Ambiguity = func(slot string, extent tree.Span, children []tree.Node) (tree.Node, error) {
		require.Equalf(t, "atom", slot, "invalid ambiguity factory inputs at slot %q: %v %v", slot, extent, children)
		require.Equalf(t, span(0, 1), extent, "invalid ambiguity factory inputs at slot %q: %v %v", slot, extent, children)
		require.Lenf(t, children, 2, "invalid ambiguity factory inputs at slot %q: %v %v", slot, extent, children)
		return nil, failure
	}
	for _, check := range []struct {
		build p.Builders[tree.Node]
		want  error
	}{
		{build, failure},
		{func() p.Builders[tree.Node] {
			copy := build
			copy.Ambiguity = func(_ string, extent tree.Span, alternatives []tree.Node) (tree.Node, error) {
				return tree.NewAmbiguity(extent, alternatives[1], alternatives[0]), nil
			}
			return copy
		}(), p.ErrBuild},
	} {
		engine := configured(t, grammar, check.build, p.Limits{})
		root, status, err := engine.Start(textSource("a"), "").Run()
		require.Truef(t, root == nil, "Run got %v %v %v, want construction error %v", root, status, err, check.want)
		require.Equalf(t, p.Done, status, "Run got %v %v %v, want construction error %v", root, status, err, check.want)
		require.ErrorIsf(t, err, p.ErrBuild, "Run got %v %v %v, want construction error %v", root, status, err, check.want)
		require.ErrorIsf(t, err, check.want, "Run got %v %v %v, want construction error %v", root, status, err, check.want)
	}
}

type narrowAmbiguity struct{ *tree.Ambiguity }

func (*narrowAmbiguity) fixtureNode() {}

func TestTypedAmbiguityFactory(t *testing.T) {
	a := p.Make("atom", "left", tagged("a"))
	b := p.Make("atom", "right", tagged("a"))
	build := p.Builders[narrowNode]{
		Node: func(in p.Build[narrowNode]) (narrowNode, error) {
			return &narrowFile{tree.NewContainer(in.Kind, in.Span)}, nil
		},
		Error: func(_ string, problem p.Problem) (narrowNode, error) {
			return &narrowError{tree.NewError(problem.Span, "invalid")}, nil
		},
		File: func(full tree.Span, parts []narrowNode, _ []p.Trivia) (narrowNode, error) {
			var children []tree.Node
			for _, part := range parts {
				children = append(children, part)
			}
			return &narrowFile{tree.NewContainer("file", full, children...)}, nil
		},
	}
	{
		_, err := p.Configure(single(p.Choose(p.ChoiceSpec{Policy: p.Longest}, a, b)), build, p.Limits{})
		require.NoErrorf(t, err, "Longest requires no ambiguity wrapper: %v", err)
	}
	grammar := single(allOf("atom", a, b))
	{
		_, err := p.Configure(grammar, build, p.Limits{})
		require.ErrorIsf(t, err, p.ErrGrammar, "missing typed ambiguity factory: %v", err)
	}
	build.Ambiguity = func(_ string, extent tree.Span, children []narrowNode) (narrowNode, error) {
		return &narrowAmbiguity{tree.NewAmbiguity(extent, children[0], children[1])}, nil
	}
	engine, err := p.Configure(grammar, build, p.Limits{})
	require.NoError(t, err)
	root, status, err := engine.Start(textSource("a"), "").Run()
	require.NoErrorf(t, err, "typed ambiguity = %v %v %v", root, status, err)
	require.Equalf(t, p.Done, status, "typed ambiguity = %v %v %v", root, status, err)
	child, _ := root.Child(0)
	{
		_, ok := child.(*narrowAmbiguity)
		require.Truef(t, ok, "typed ambiguity root = %v", child)
		require.Truef(t, slices.Equal(chartKinds(t, child), []tree.Kind{"left", "right"}), "typed ambiguity root = %v", child)
	}
}

func TestPackedTokenLedgersStayLocalToReadings(t *testing.T) {
	lex := p.Request{Goal: "whole"}
	wide := p.Make("atom", "wide", p.Lex(lex, tagged("ab")))
	narrow := p.Make("atom", "narrow", p.Seq(tagged("a"), tagged("b")))
	rule := p.Make("group", "group", p.Seq(p.Bind("inside", allOf("atom", wide, narrow)), skip("!")))
	build := builders()
	original := build.Node
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		switch in.Kind {
		case "wide":
			require.Equalf(t, 1, in.Tokens.Len(), "wide reading has %d tokens", in.Tokens.Len())
		case "narrow":
			require.Equalf(t, 2, in.Tokens.Len(), "narrow reading has %d tokens", in.Tokens.Len())
		case "group":
			require.Equalf(t, 1, in.Tokens.Len(), "parent got another reading's token records: %+v", in)
			require.Lenf(t, in.Fields, 1, "parent got another reading's token records: %+v", in)
			require.Equalf(t, 0, in.Fields[0].Tokens.Len(), "parent got another reading's token records: %+v", in)
		}
		return original(in)
	}
	src := textSource("ab!")
	fixed := src.read
	src.read = func(mark p.Mark, request p.Request) (p.Read, error) {
		if mark.Byte == 0 && request == lex {
			return ready(token("ab", "ab", 0, 2), p.Mark{Byte: 2}), nil
		}
		return fixed(mark, request)
	}
	engine := configured(t, single(rule), build, p.Limits{})
	root := done(t, engine.Start(src, ""))
	group := chartChildren(t, root)[0]
	packed := chartChildren(t, group)[0]
	{
		got := chartKinds(t, packed)
		require.Truef(t, slices.Equal(got, []tree.Kind{"wide", "narrow"}), "divergent tokenizations lost their pack: %v", got)
	}
}

func TestFilterNeverResumesRejectedLexicalExit(t *testing.T) {
	arm := func(goal string) p.Rule {
		return p.Make("atom", tree.Kind(goal), p.Lex(p.Request{Goal: goal}, tagged("a")))
	}
	for _, policy := range []p.Policy{p.All, p.Longest} {
		rule := p.Choose(p.ChoiceSpec{Policy: policy, Slot: "atom", Filter: func(c p.Candidate) bool {
			return c.Kind == "keep"
		}}, arm("discard"), arm("keep"))
		src := &fixture{read: func(mark p.Mark, request p.Request) (p.Read, error) {
			switch {
			case mark.Byte == 0 && request.Goal != "":
				return ready(token("a", "a", 0, 1), p.Mark{Byte: 1, Key: request.Goal}), nil
			case mark.Key == "keep":
				return eof(1), nil
			default:
				return p.Read{}, errors.New("source read from rejected lexical exit")
			}
		}}
		engine := configured(t, single(rule), builders(), p.Limits{})
		root := done(t, engine.Start(src, ""))
		{
			got := chartKinds(t, root)
			require.Truef(t, slices.Equal(got, []tree.Kind{"keep"}), "policy %v retained %v", policy, got)
		}
	}
}

func TestChartPollingForInputPreservesWorkBudget(t *testing.T) {
	rule := allOf("atom", p.Make("atom", "atom", tagged("a")))
	engine := configured(t, single(rule), builders(), p.Limits{Steps: 28})
	open := true
	source := textSource("a")
	read := source.read
	source.read = func(mark p.Mark, request p.Request) (p.Read, error) {
		if open {
			return shortage(), nil
		}
		return read(mark, request)
	}
	session := engine.Start(source, "")
	for range 100 {
		paused(t, session)
	}
	open = false
	{
		got := chartKinds(t, done(t, session))
		require.Truef(t, slices.Equal(got, []tree.Kind{"atom"}), "children=%v after resume", got)
	}
}

func TestFirstCommitmentUsesEachPackedReadingTokenCount(t *testing.T) {
	lex := p.Request{Goal: "whole"}
	wide := p.Make("atom", "wide", p.Lex(lex, tagged("ab")))
	narrow := p.Make("atom", "narrow", p.Seq(tagged("a"), tagged("b")))
	for _, arms := range [][]p.Rule{{wide, narrow}, {narrow, wide}} {
		rule := p.Choose(p.ChoiceSpec{Lookahead: 2},
			p.Seq(allOf("atom", arms...), tagged("x")),
			p.Make("fallback", "fallback", p.Seq(tagged("a"), tagged("b"), tagged("!"))),
		)
		engine := configured(t, single(rule), builders(), p.Limits{})
		for _, delayed := range []bool{false, true} {
			open := delayed
			source := textSource("ab!")
			read := source.read
			source.read = func(mark p.Mark, req p.Request) (p.Read, error) {
				if mark.Byte == 0 && req == lex {
					if open {
						return shortage(), nil
					}
					return ready(token("ab", "ab", 0, 2), p.Mark{Byte: 2}), nil
				}
				return read(mark, req)
			}
			session := engine.Start(source, "")
			if open {
				paused(t, session)
				open = false
			}
			root := done(t, session)
			want := []tree.Kind{"narrow", tree.KindError}
			if got := chartKinds(t, root); !assert.Truef(t, slices.Equal(got, want), "delayed=%t: got %v, want %v", delayed, got, want) {
				continue
			}
			got := problemAt(t, root, 1, "file")
			assert.Falsef(t, got.Span != span(2, 3) || got.Found.Token.Text != "!" ||
				!slices.Equal(got.Expected, []p.Pattern{p.Kind("x")}) ||
				len(got.Skipped) != 1 || got.Skipped[0].Text != "!", "delayed=%t: diagnostic %+v", delayed, got)
		}
	}
}

func TestRecoverPreservesPackedReadingTokenCounts(t *testing.T) {
	lex := p.Request{Goal: "whole"}
	wide := p.Make("atom", "wide", p.Lex(lex, tagged("ab")))
	narrow := p.Make("atom", "narrow", p.Seq(tagged("a"), tagged("b")))
	for _, arms := range []struct {
		name  string
		rules []p.Rule
	}{
		{"wide-first", []p.Rule{wide, narrow}},
		{"narrow-first", []p.Rule{narrow, wide}},
	} {
		t.Run(arms.name, func(t *testing.T) {
			recoverBody := p.Seq(allOf("atom", arms.rules...), tagged("x"))
			repaired := p.Make("stmt", "repaired", p.Seq(
				p.Recover("missing", recoverBody, p.Boundary{At: []p.Pattern{p.Kind("!")}}),
				tagged("!"),
			))
			fallback := p.Make("stmt", "fallback", p.Seq(tagged("a"), tagged("b"), tagged("!")))
			engine := configured(t, single(p.Choose(p.ChoiceSpec{Lookahead: 2}, repaired, fallback)), builders(), p.Limits{})

			for _, chunk := range []struct {
				name    string
				delayed bool
			}{{"whole", false}, {"paused-wide", true}} {
				t.Run(chunk.name, func(t *testing.T) {
					open := chunk.delayed
					source := textSource("ab!")
					read := source.read
					source.read = func(mark p.Mark, req p.Request) (p.Read, error) {
						if mark.Byte == 0 && req == lex {
							if open {
								return shortage(), nil
							}
							return ready(token("ab", "ab", 0, 2), p.Mark{Byte: 2}), nil
						}
						return read(mark, req)
					}

					session := engine.Start(source, "")
					if open {
						paused(t, session)
						open = false
					}
					root := done(t, session)
					{
						got := chartKinds(t, root)
						require.Truef(t, slices.Equal(got, []tree.Kind{"repaired"}), "file children = %v, want repaired", got)
					}
					stmt, _ := root.Child(0)
					assert.Equalf(t, span(0, 3), stmt.Info().Span, "repaired span = %+v", stmt.Info().Span)
					{
						got := chartKinds(t, stmt)
						require.Truef(t, slices.Equal(got, []tree.Kind{tree.KindError}), "repaired children = %v, want one error", got)
					}
					got := problemAt(t, stmt, 0, "missing")
					assert.Falsef(t, got.Span != span(0, 2) || got.Found.Token.Text != "!" ||
						!slices.Equal(got.Expected, []p.Pattern{p.Kind("x")}) ||
						len(got.Skipped) != 2 || got.Skipped[0].Text != "a" || got.Skipped[1].Text != "b", "diagnostic = %+v", got)
				})
			}
		})
	}
}

func TestFirstRanksTiedRepairsAcrossRecoveryPause(t *testing.T) {
	lex, sync := p.Request{Goal: "whole"}, p.Request{Goal: "sync"}
	wide := p.Make("atom", "wide", p.Lex(lex, tagged("ab")))
	narrow := p.Make("atom", "narrow", p.Seq(tagged("a"), tagged("b")))
	for _, arms := range []struct {
		name  string
		rules []p.Rule
		first string
	}{
		{"wide-first", []p.Rule{wide, narrow}, "ab"},
		{"narrow-first", []p.Rule{narrow, wide}, "a"},
	} {
		t.Run(arms.name, func(t *testing.T) {
			body := p.Seq(allOf("atom", arms.rules...), tagged("x"))
			repair := p.Recover("missing", body, p.Boundary{
				At: []p.Pattern{p.Kind("!")}, Goal: &sync, Consume: true,
			})
			engine := configured(t, single(p.Choose(p.ChoiceSpec{}, repair, tagged("?"))), builders(), p.Limits{})
			for _, delaySync := range []bool{false, true} {
				source := textSource("ab!")
				read := source.read
				source.read = func(mark p.Mark, req p.Request) (p.Read, error) {
					if mark.Byte == 0 && req == lex {
						return ready(token("ab", "ab", 0, 2), p.Mark{Byte: 2, Key: "wide"}), nil
					}
					if mark.Key == "wide" && req == sync && delaySync {
						return shortage(), nil
					}
					return read(mark, req)
				}

				session := engine.Start(source, "")
				if delaySync {
					paused(t, session)
					delaySync = false
				}
				root := done(t, session)
				{
					got := chartKinds(t, root)
					require.Truef(t, slices.Equal(got, []tree.Kind{tree.KindError}), "file children = %v, want one error", got)
				}
				got := problemAt(t, root, 0, "missing")
				assert.Falsef(t, got.Span != span(0, 3) || got.Found.Token.Text != "!" ||
					!slices.Equal(got.Expected, []p.Pattern{p.Kind("x")}) ||
					len(got.Skipped) < 2 || got.Skipped[0].Text != arms.first ||
					got.Skipped[len(got.Skipped)-1].Text != "!", "diagnostic = %+v, want first skipped token %q", got, arms.first)
			}
		})
	}
}

func TestChartRecoveryPreservesEqualCountLexicalMarks(t *testing.T) {
	for _, goalOrder := range [][2]string{{"left", "right"}, {"right", "left"}} {
		for _, first := range []string{"none", "around-repair", "inside-body"} {
			for _, consume := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-arm/first=%s/consume=%t", goalOrder[0], first, consume), func(t *testing.T) {
					arm := func(goal string) p.Rule {
						return p.Make("atom", tree.Kind(goal), p.Lex(p.Request{Goal: goal}, tagged(goal)))
					}
					body := p.Seq(allOf("atom", arm(goalOrder[0]), arm(goalOrder[1])), tagged("x"))
					winner := goalOrder[0]
					if first == "inside-body" {
						body = p.Choose(p.ChoiceSpec{Lookahead: 2}, body, tagged("?"))
						if !consume {
							winner = goalOrder[1]
						}
					}
					rule := p.Recover("missing", body, p.Boundary{At: []p.Pattern{p.Word("!")}, Consume: consume})
					if !consume {
						rule = p.Seq(rule, tagged(winner+"-end"))
					}
					rule = p.Make("stmt", "repaired", rule)
					if first == "around-repair" {
						rule = p.Choose(p.ChoiceSpec{}, rule, tagged("?"))
					}
					engine := configured(t, single(rule), builders(), p.Limits{})

					end := token(p.TokenKind(winner+"-end"), "!", 2, 3)
					want := p.Problem{
						Span: span(0, 2), Expected: []p.Pattern{p.Kind("x")},
						Found:   p.Look{Token: end, Point: span(2, 2)},
						Skipped: []p.Token{token(p.TokenKind(winner), "ab", 0, 2)},
					}
					if consume {
						want.Span = span(0, 3)
						want.Skipped = append(want.Skipped, end)
					}
					for _, delayed := range []string{"", goalOrder[0], goalOrder[1]} {
						blocked := delayed
						source := textSource("ab!")
						read := source.read
						source.read = func(mark p.Mark, req p.Request) (p.Read, error) {
							switch {
							case mark.Byte == 0 && req.Goal != "":
								if req.Goal == blocked {
									return shortage(), nil
								}
								return ready(token(p.TokenKind(req.Goal), "ab", 0, 2), p.Mark{Byte: 2, Key: req.Goal}), nil
							case mark.Byte == 2 && mark.Key != "":
								return ready(token(p.TokenKind(mark.Key+"-end"), "!", 2, 3), p.Mark{Byte: 3}), nil
							default:
								return read(mark, req)
							}
						}
						session := engine.Start(source, "")
						if blocked != "" {
							paused(t, session)
							blocked = ""
						}
						root := done(t, session)
						{
							got := chartKinds(t, root)
							require.Truef(t, slices.Equal(got, []tree.Kind{"repaired"}), "delayed=%q: file children = %v, want repaired", delayed, got)
						}
						stmt, _ := root.Child(0)
						{
							got := chartKinds(t, stmt)
							require.Truef(t, slices.Equal(got, []tree.Kind{tree.KindError}), "delayed=%q: repaired statement has span %+v and children %v", delayed, stmt.Info().Span, got)
							require.Equalf(t, span(0, 3), stmt.Info().Span, "delayed=%q: repaired statement has span %+v and children %v", delayed, stmt.Info().Span, got)
						}
						{
							got := problemAt(t, stmt, 0, "missing")
							assert.Equalf(t, want, got, "delayed=%q: diagnostic = %+v, want %+v", delayed, got, want)
						}
					}
				})
			}
		}
	}
}

func TestChartRecoveryMergesOnlyCompatibleFailureOffers(t *testing.T) {
	for _, goalOrder := range [][2]string{{"left", "right"}, {"right", "left"}} {
		for _, sharedOffer := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-first/shared=%t", goalOrder[0], sharedOffer), func(t *testing.T) {
				arm := func(goal string) p.Rule {
					req := p.Request{Goal: goal}
					start, mismatch := tagged(goal), tagged(goal+"-x")
					body := p.Lex(req, p.Seq(start, mismatch))
					if sharedOffer {
						body = p.Seq(p.Lex(req, start), mismatch)
					}
					return p.Make("atom", tree.Kind(goal), body)
				}
				body := allOf("atom", arm(goalOrder[0]), arm(goalOrder[1]))
				rule := p.Recover("missing", body, p.Boundary{At: []p.Pattern{p.Word("!")}, Consume: true})
				engine := configured(t, single(rule), builders(), p.Limits{})
				first := goalOrder[0]
				end := token(p.TokenKind(first+"-end"), "!", 1, 2)
				expected := []p.Pattern{p.Kind(p.TokenKind(first + "-x"))}
				if sharedOffer {
					end = token("!", "!", 1, 2)
					expected = append(expected, p.Kind(p.TokenKind(goalOrder[1]+"-x")))
				}
				want := p.Problem{
					Span: span(0, 2), Expected: expected,
					Found:   p.Look{Token: end, Point: span(1, 1)},
					Skipped: []p.Token{token(p.TokenKind(first), "a", 0, 1), token("!", "!", 1, 2)},
				}
				for _, delayed := range []string{"", goalOrder[0], goalOrder[1]} {
					blocked := delayed
					source := textSource("a!")
					read := source.read
					source.read = func(mark p.Mark, req p.Request) (p.Read, error) {
						switch {
						case mark.Byte == 0 && req.Goal != "":
							if req.Goal == blocked {
								return shortage(), nil
							}
							return ready(token(p.TokenKind(req.Goal), "a", 0, 1), p.Mark{Byte: 1}), nil
						case mark.Byte == 1 && req.Goal != "":
							return ready(token(p.TokenKind(req.Goal+"-end"), "!", 1, 2), p.Mark{Byte: 2}), nil
						default:
							return read(mark, req)
						}
					}
					session := engine.Start(source, "")
					if blocked != "" {
						paused(t, session)
						blocked = ""
					}
					root := done(t, session)
					{
						got := problemAt(t, root, 0, "missing")
						assert.Equalf(t, want, got, "delayed=%q: diagnostic = %+v, want %+v", delayed, got, want)
					}
				}
			})
		}
	}
}

func TestFirstDoesNotCommitAfterSpeculativeChartRepair(t *testing.T) {
	boundary := p.Boundary{At: []p.Pattern{p.Kind("!")}}
	atom := allOf("atom", p.Make("atom", "atom", tagged("a")))
	repair := p.Recover("inner", p.Seq(atom, tagged("b")), boundary)
	failed := p.Seq(repair, tagged("!"), tagged("z"), tagged("x"))
	clean := p.Make("stmt", "clean", p.Seq(tagged("a"), tagged("!"), tagged("z")))
	for _, body := range []p.Rule{failed, p.Recover("outer", failed, boundary)} {
		rule := p.Choose(p.ChoiceSpec{Lookahead: 2}, body, clean)
		build := builders()
		build.Error = func(string, p.Problem) (tree.Node, error) {
			require.FailNow(t, "speculative recovery built an error")
			return nil, nil
		}
		engine := configured(t, single(rule), build, p.Limits{})
		root := done(t, engine.Start(textSource("a!z"), ""))
		{
			got := chartKinds(t, root)
			assert.Truef(t, slices.Equal(got, []tree.Kind{"clean"}), "file children = %v, want the clean arm", got)
		}
	}
}

func TestChartFailurePollingPreservesWorkBudget(t *testing.T) {
	rule := p.Choose(p.ChoiceSpec{Policy: p.All, Slot: "atom", Filter: func(p.Candidate) bool {
		return false
	}}, p.Make("atom", "atom", tagged("a")))
	engine := configured(t, single(rule), builders(), p.Limits{Steps: 40})
	source := textSource("a")
	open := true
	read := source.read
	source.read = func(mark p.Mark, request p.Request) (p.Read, error) {
		if mark.Byte == 1 && open {
			return shortage(), nil
		}
		return read(mark, request)
	}
	session := engine.Start(source, "")
	for range 100 {
		paused(t, session)
	}
	open = false
	got := problemAt(t, done(t, session), 0, "file")
	assert.Falsef(t, got.Span != span(0, 1) || !got.Found.End || len(got.Skipped) != 1 || got.Skipped[0].Text != "a", "file diagnostic = %+v", got)
}
