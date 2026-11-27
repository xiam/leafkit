package parser_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

func problemAt(t *testing.T, node tree.Node, index int, slot string) p.Problem {
	t.Helper()
	child, _ := node.Child(index)
	diagnostic, ok := child.(*p.ErrorNode)
	require.Truef(t, ok, "child %d = %v, want an error in slot %q", index, child, slot)
	require.Equalf(t, slot, diagnostic.Slot(), "child %d = %v, want an error in slot %q", index, child, slot)
	require.Equalf(t, tree.KindError, diagnostic.Info().Kind, "child %d = %v, want an error in slot %q", index, child, slot)
	return diagnostic.Problem()
}

func TestRecoverySelectsFurthestStream(t *testing.T) {
	left, right, sync := p.Request{Goal: "left"}, p.Request{Goal: "right"}, p.Request{Goal: "sync"}
	const budget = 12
	body := p.Choose(p.ChoiceSpec{Lookahead: budget},
		p.Seq(tagged("a"), p.Lex(right, tagged("early"))),
		p.Seq(tagged("a"), tagged("b"), p.Lex(left, tagged("first"))),
		p.Seq(tagged("a"), tagged("b"), p.Lex(right, tagged("foreign"))),
		p.Seq(tagged("a"), tagged("b"), p.Lex(left, tagged("last"))),
	)
	rule := p.Seq(p.Recover("item", body, p.Boundary{At: []p.Pattern{p.Kind(";")}, Goal: &sync}), tagged(";"))
	engine := configured(t, single(rule), builders(), p.Limits{})
	src := textSource("ab?;")
	original := src.read
	var reads []p.Mark
	src.read = func(mark p.Mark, req p.Request) (p.Read, error) {
		read, err := original(mark, req)
		if mark.Byte == 2 {
			switch req {
			case left:
				read.Token.Kind = p.Invalid
			case right:
				read.Token.Kind = "other-stream"
			case sync:
				reads = append(reads, mark)
				read.Token.Kind = "resync"
				read.Next.Key = "resynced"
			}
		}
		require.Falsef(t, mark.Byte == 3 && (req == sync || req == (p.Request{})) && mark.Key != "resynced", "lost the recovery successor mark: %+v", mark)
		return read, err
	}
	root := done(t, engine.Start(src, ""))
	got := problemAt(t, root, 0, "item")
	require.Equalf(t, span(0, 3), got.Span, "recovered diagnostic = %+v", got)
	require.Equalf(t, p.Invalid, got.Found.Token.Kind, "recovered diagnostic = %+v", got)
	require.Equalf(t, span(2, 2), got.Found.Point, "recovered diagnostic = %+v", got)
	require.Truef(t, slices.Equal(got.Expected, []p.Pattern{p.Kind("first"), p.Kind("last")}), "recovered diagnostic = %+v", got)
	require.Lenf(t, got.Skipped, 3, "replay lost or repeated discarded input: skipped=%+v reads=%+v", got.Skipped, reads)
	require.Equalf(t, "a", got.Skipped[0].Text, "replay lost or repeated discarded input: skipped=%+v reads=%+v", got.Skipped, reads)
	require.Equalf(t, "b", got.Skipped[1].Text, "replay lost or repeated discarded input: skipped=%+v reads=%+v", got.Skipped, reads)
	require.Equalf(t, p.TokenKind("resync"), got.Skipped[2].Kind, "replay lost or repeated discarded input: skipped=%+v reads=%+v", got.Skipped, reads)
	require.Lenf(t, reads, 1, "replay lost or repeated discarded input: skipped=%+v reads=%+v", got.Skipped, reads)
	require.Equalf(t, 2, reads[0].Byte, "replay lost or repeated discarded input: skipped=%+v reads=%+v", got.Skipped, reads)
}

func TestRecoveryChoiceDefersConstruction(t *testing.T) {
	sync := p.Request{Goal: "sync"}
	repair := p.Recover("item",
		p.Seq(p.Make("discard", "discard", tagged("a")), tagged("!")),
		p.Boundary{At: []p.Pattern{p.Kind(";")}, Consume: true, Goal: &sync},
	)
	choice := p.ChoiceSpec{Lookahead: 5}
	// The inner choice sees its repair before the outer choice tries its
	// clean arm. After both outer arms fail it may replay the repair arm.
	rule := p.Choose(choice, p.Choose(choice, repair, tagged("x")),
		p.Make("clean", "clean", p.Seq(tagged("a"), tagged(";"))))
	for _, row := range []struct {
		input string
		want  []string
	}{
		{"a;", []string{"clean", "file"}},
		{"a??;", []string{"item", "file"}},
	} {
		t.Run(row.input, func(t *testing.T) {
			build := builders()
			var calls []string
			copyBuild := build
			build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
				calls = append(calls, in.Slot)
				return copyBuild.Node(in)
			}
			build.Error = func(slot string, problem p.Problem) (tree.Node, error) {
				calls = append(calls, slot)
				require.Equalf(t, "?", problem.Found.Token.Text, "replayed repair lost its prefix: %+v", problem)
				require.Lenf(t, problem.Skipped, 4, "replayed repair lost its prefix: %+v", problem)
				return tree.NewError(problem.Span, "statement"), nil
			}
			build.File = func(full tree.Span, children []tree.Node, trailing []p.Trivia) (tree.Node, error) {
				calls = append(calls, "file")
				return copyBuild.File(full, children, trailing)
			}
			engine := configured(t, single(rule), build, p.Limits{})
			src := textSource(row.input)
			read := src.read
			final := false
			src.read = func(mark p.Mark, request p.Request) (p.Read, error) {
				require.Falsef(t, request == sync && row.input == "a;", "speculative recovery scanned before the clean arm")
				if !final && (mark.Byte >= len(row.input) || request == sync && mark.Byte >= 2) {
					return shortage(), nil
				}
				return read(mark, request)
			}
			session := engine.Start(src, "")
			for range 3 {
				paused(t, session)
				require.Lenf(t, calls, 0, "constructors ran before EOF: %v", calls)
			}
			final = true
			done(t, session)
			require.Truef(t, slices.Equal(calls, row.want), "factories = %v, want %v", calls, row.want)
		})
	}
}

func TestRecoveryProgressAndInsertion(t *testing.T) {
	t.Run("point repair exits many", func(t *testing.T) {
		item := p.Recover("item", tagged("x"), p.Boundary{At: []p.Pattern{p.Kind("}")}})
		rule := p.Seq(p.Many(item), tagged("}"))
		engine := configured(t, single(rule), builders(), p.Limits{Steps: 30})
		root := done(t, engine.Start(textSource("}"), ""))
		{
			got := problemAt(t, root, 0, "item")
			require.Equalf(t, span(0, 0), got.Span, "missing item = %+v", got)
			require.Lenf(t, got.Skipped, 0, "missing item = %+v", got)
		}
		{
			child, ok := root.Child(1)
			require.Falsef(t, ok, "unexpected second child: %v", child)
		}
	})

	t.Run("same site at a left boundary hands off to file", func(t *testing.T) {
		grammar := single(p.Program("start", map[string]p.ProgramState{
			"start": callStep("item", "again", ""),
			"again": callStep("item", "again", ""),
		}))
		grammar.Rules["item"] = p.Recover("item", tagged("x"), p.Boundary{At: []p.Pattern{p.Kind("}")}})
		// A statically declared call loop without token progress is legal
		// for a hand program, which can always reject or return.
		engine := configured(t, grammar, builders(), p.Limits{Steps: 50})
		root := done(t, engine.Start(textSource("}abc"), ""))
		problemAt(t, root, 0, "item")
		{
			got := problemAt(t, root, 1, "file")
			require.Equalf(t, span(0, 4), got.Span, "file fallback = %+v", got)
			require.Lenf(t, got.Skipped, 4, "file fallback = %+v", got)
		}
		{
			child, ok := root.Child(2)
			require.Falsef(t, ok, "repeated point recovery: %v", child)
		}
	})

	t.Run("insert neither commits nor creates an error", func(t *testing.T) {
		insert := p.Insert(p.Kind(";"), func(look p.Look, view p.View) bool {
			return (look.End || look.Token.Kind == "a") && view.State == "allow"
		})
		pick := p.Choose(p.ChoiceSpec{}, p.Seq(insert, tagged("b")), tagged("a"))
		engine := configured(t, single(pick), builders(), p.Limits{})
		root := done(t, engine.Start(textSource("a"), "allow"))
		{
			child, ok := root.Child(0)
			require.Falsef(t, ok, "speculative insertion spent the choice budget: %v", child)
		}

		build := builders()
		node := build.Node
		build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
			got, ok := in.Tokens.At(1)
			require.Truef(t, ok, "bad inserted token: %+v", got)
			require.Equalf(t, 2, in.Tokens.Len(), "bad inserted token: %+v", got)
			require.Equalf(t, p.TokenKind(";"), got.Kind, "bad inserted token: %+v", got)
			require.Truef(t, got.Synthetic, "bad inserted token: %+v", got)
			require.Equalf(t, span(1, 1), got.Span, "bad inserted token: %+v", got)
			return node(in)
		}
		engine = configured(t, single(p.Make("stmt", "stmt", p.Seq(tagged("a"), insert))), build, p.Limits{})
		root = done(t, engine.Start(textSource("a"), "allow"))
		child, _ := root.Child(0)
		require.Equalf(t, tree.Kind("stmt"), child.Info().Kind, "insert changed the production span: %v", child.Info())
		require.Equalf(t, span(0, 1), child.Info().Span, "insert changed the production span: %v", child.Info())
	})
}

func TestRecoveryFatalFailures(t *testing.T) {
	allow := func(p.Look, p.View) bool { return true }
	for _, rule := range []p.Rule{
		p.Recover("", tagged("a"), p.Boundary{}),
		p.Recover("a", tagged("a"), p.Boundary{At: []p.Pattern{{}}}),
		p.Recover("a", tagged("a"), p.Boundary{Goal: &p.Request{Layout: p.Joined + 1}}),
		p.Insert(p.Word("a"), allow),
		p.Insert(p.EndOfFile(), allow),
		p.Insert(p.Kind("a"), nil),
		p.Many(p.Insert(p.Kind("a"), allow)),
		p.Seq(p.Recover("a", tagged("a"), p.Boundary{}), p.Ref("start")),
	} {
		{
			_, err := p.Configure(single(rule), builders(), p.Limits{})
			assert.ErrorIsf(t, err, p.ErrGrammar, "invalid recovery declaration = %v", err)
		}
	}
	grammar := single(p.Recover("item", tagged("a"), p.Boundary{}))
	cause := errors.New("fixture failure")
	for _, row := range []struct {
		name    string
		want    error
		cause   error
		limits  p.Limits
		source  func(*fixture)
		factory func(*p.Builders[tree.Node])
	}{
		{name: "source while skipping", want: p.ErrSource, cause: cause, source: func(src *fixture) {
			read := src.read
			src.read = func(mark p.Mark, req p.Request) (p.Read, error) {
				if mark.Byte == 1 {
					return p.Read{}, cause
				}
				return read(mark, req)
			}
		}},
		{name: "step limit while skipping", want: p.ErrLimit, limits: p.Limits{Steps: 4}},
		{name: "error constructor cause", want: p.ErrBuild, cause: cause, factory: func(build *p.Builders[tree.Node]) {
			build.Error = func(string, p.Problem) (tree.Node, error) { return nil, cause }
		}},
		{name: "wrong error span", want: p.ErrBuild, factory: func(build *p.Builders[tree.Node]) {
			build.Error = func(string, p.Problem) (tree.Node, error) {
				return tree.NewError(span(0, 0), "wrong span"), nil
			}
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			source, build := textSource("???"), builders()
			if row.source != nil {
				row.source(source)
			}
			if row.factory != nil {
				row.factory(&build)
			}
			session := configured(t, grammar, build, row.limits).Start(source, "")
			root, status, err := session.Run()
			require.Truef(t, root == nil, "Run = %v, %v, %v", root, status, err)
			require.Equalf(t, p.Done, status, "Run = %v, %v, %v", root, status, err)
			require.ErrorIsf(t, err, row.want, "Run = %v, %v, %v", root, status, err)
			require.Falsef(t, row.cause != nil && !errors.Is(err, row.cause), "Run = %v, %v, %v", root, status, err)
		})
	}
}

func TestDropRetainsDiagnostics(t *testing.T) {
	body := p.Seq(skip("a"), p.Make("item", "item",
		p.Recover("value", tagged("b"), p.Boundary{At: []p.Pattern{p.Kind(";")}})), skip(";"))
	build := builders()
	node := build.Node
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		first, _ := in.Tokens.At(0)
		last, _ := in.Tokens.At(1)
		require.Equalf(t, "whole", in.Slot, "discarded nodes or diagnostic-owned token leaked into parent: %+v", in)
		require.Equalf(t, 2, in.Tokens.Len(), "discarded nodes or diagnostic-owned token leaked into parent: %+v", in)
		require.Equalf(t, "a", first.Text, "discarded nodes or diagnostic-owned token leaked into parent: %+v", in)
		require.Equalf(t, ";", last.Text, "discarded nodes or diagnostic-owned token leaked into parent: %+v", in)
		return node(in)
	}
	engine := configured(t, single(p.Make("whole", "whole", p.Drop(body))), build, p.Limits{})
	root := done(t, engine.Start(textSource("a?;"), ""))
	whole, _ := root.Child(0)
	got := problemAt(t, whole, 0, "value")
	require.Lenf(t, got.Skipped, 1, "problem = %+v", got)
	require.Equalf(t, "?", got.Skipped[0].Text, "problem = %+v", got)
}

func TestFileFallbackKeepsCompletedChildren(t *testing.T) {
	rule := p.Seq(
		p.Make("keep", "kept", tagged("a")),
		p.Make("incomplete", "incomplete", p.Seq(
			p.Make("discard", "discard", tagged("b")), tagged("c"))),
	)
	build := builders()
	node := build.Node
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		require.Equalf(t, "keep", in.Slot, "built an incomplete recipe: %q", in.Slot)
		return node(in)
	}
	root := done(t, configured(t, single(rule), build, p.Limits{}).Start(textSource("ab?d"), ""))
	kept, _ := root.Child(0)
	require.Truef(t, kept != nil, "lost first node: %v", kept)
	require.Equalf(t, tree.Kind("kept"), kept.Info().Kind, "lost first node: %v", kept)
	require.Equalf(t, span(0, 1), kept.Info().Span, "lost first node: %v", kept)
	problem := problemAt(t, root, 1, "file")
	require.Equalf(t, span(1, 4), problem.Span, "fallback after partial production = %+v", problem)
	require.Equalf(t, span(2, 2), problem.Found.Point, "fallback after partial production = %+v", problem)
	require.Lenf(t, problem.Skipped, 3, "fallback after partial production = %+v", problem)
	require.Equalf(t, "b", problem.Skipped[0].Text, "fallback after partial production = %+v", problem)
	require.Truef(t, slices.Equal(problem.Expected, []p.Pattern{p.Kind("c")}), "fallback after partial production = %+v", problem)
}

func TestRecoveryDiscardsRejectedInsertion(t *testing.T) {
	insert := p.Insert(p.Kind(";"), func(p.Look, p.View) bool { return true })
	for _, choice := range []bool{false, true} {
		body := p.Seq(tagged("a"), insert, tagged("b"))
		if choice {
			body = p.Choose(p.ChoiceSpec{Lookahead: 3}, body, tagged("x"))
		}
		rule := p.Seq(p.Recover("part", body, p.Boundary{At: []p.Pattern{p.Kind("}")}}), tagged("}"))
		root := done(t, configured(t, single(rule), builders(), p.Limits{}).Start(textSource("a}"), ""))
		got := problemAt(t, root, 0, "part")
		require.Equalf(t, span(0, 1), got.Span, "choice=%t: unexpected inserted marker in %+v", choice, got)
		require.Lenf(t, got.Skipped, 1, "choice=%t: unexpected inserted marker in %+v", choice, got)
		require.Equalf(t, "a", got.Skipped[0].Text, "choice=%t: unexpected inserted marker in %+v", choice, got)
	}
}
