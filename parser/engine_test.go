package parser_test

import (
	"errors"
	"fmt"
	goparser "go/parser"
	gotoken "go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

func TestReplayAcrossFeeds(t *testing.T) {
	probe := p.Request{Goal: "probe"}
	m0, m1, m3 := p.Mark{}, p.Mark{Byte: 1, Key: "after-a"}, p.Mark{Byte: 3, Key: "after-c"}
	trailing := p.Trivia{Kind: "comment", Span: span(3, 5)}
	lex := p.Choose(p.ChoiceSpec{Lookahead: 2},
		p.Lex(probe, p.Seq(p.Make("left", "word", tagged("a")), tagged("b"))),
		p.Make("right", "winner", p.Seq(skip("a"), p.Bind("letter", p.Make("letter", "word", tagged("c"))))),
	)
	type call struct {
		mark p.Mark
		goal string
	}
	var calls []call
	var built []string
	var stable [1]byte
	build := builders()
	word := build.Node
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		built = append(built, in.Slot)
		if in.Slot == "right" {
			a, ok := in.Tokens.At(0)
			require.Truef(t, ok, "unexpected capture or direct ledger: %+v", in)
			require.Equalf(t, "a", a.Text, "unexpected capture or direct ledger: %+v", in)
			require.Equalf(t, 1, in.Tokens.Len(), "unexpected capture or direct ledger: %+v", in)
			require.Lenf(t, in.Fields, 1, "unexpected capture or direct ledger: %+v", in)
			require.Equalf(t, "letter", in.Fields[0].Name, "unexpected capture or direct ledger: %+v", in)
			require.Equalf(t, 1, in.Fields[0].Tokens.Len(), "unexpected capture or direct ledger: %+v", in)
		}
		return word(in)
	}
	file := build.File
	build.File = func(full tree.Span, children []tree.Node, trivia []p.Trivia) (tree.Node, error) {
		require.Equalf(t, span(0, 5), full, "file got %+v, trailing %+v", full, trivia)
		require.Equalf(t, []p.Trivia{trailing}, trivia, "file got %+v, trailing %+v", full, trivia)
		return file(full, children, trivia)
	}
	engine := configured(t, single(lex), build, p.Limits{})
	cut, final := 0, false
	src := &fixture{read: func(mark p.Mark, req p.Request) (p.Read, error) {
		calls = append(calls, call{mark, req.Goal})
		switch mark {
		case m0:
			return ready(token("a", "a", 0, 1), m1), nil
		case m1:
			if cut < 3 {
				return shortage(), nil
			}
			kind := p.TokenKind("c")
			if req == probe {
				kind = "probe-c"
			}
			stable[0] = 'c'
			return ready(token(kind, string(stable[:]), 2, 3), m3), nil
		case m3:
			if req != (p.Request{}) {
				return p.Read{}, fmt.Errorf("scoped request leaked to EOF")
			}
			stable[0] = 'x'
			if !final {
				return shortage(), nil
			}
			return eof(5, trailing), nil
		}
		return p.Read{}, fmt.Errorf("unknown mark %+v", mark)
	}}
	session := engine.Start(src, "")
	cut = 2
	paused(t, session)
	require.Lenf(t, built, 0, "probe lost priority or replay pin: reads %+v, factories %v, floor %d", calls, built, src.discarded)
	require.Equalf(t, 0, src.discarded, "probe lost priority or replay pin: reads %+v, factories %v, floor %d", calls, built, src.discarded)
	require.Equalf(t, []call{{m0, "probe"}, {m1, "probe"}}, calls, "probe lost priority or replay pin: reads %+v, factories %v, floor %d", calls, built, src.discarded)
	cut = 5
	paused(t, session)
	require.Lenf(t, built, 0, "committed prefix retained or built early: factories %v, floor %d", built, src.discarded)
	require.Equalf(t, 3, src.discarded, "committed prefix retained or built early: factories %v, floor %d", built, src.discarded)
	final = true
	root := done(t, session)
	equalRecords(t, root, []record{{"file", span(0, 5), "", 1}, {"winner", span(0, 3), "", 1}, {"word", span(1, 3), "c", 0}})
	wantReads := []call{{m0, "probe"}, {m1, "probe"}, {m1, "probe"}, {m0, ""}, {m1, ""}, {m3, ""}, {m3, ""}}
	require.Equalf(t, wantReads, calls, "reads %+v, built %v", calls, built)
	require.Equalf(t, []string{"letter", "right"}, built, "reads %+v, built %v", calls, built)
	// The source exposes the sealed snapshot only. Its bytes are independent
	// of the scratch buffer used to make token strings.
	start, end, err := tree.Positions(span(2, 3), func(id tree.SourceID) ([]byte, bool) {
		return []byte("a c #"), id == snapshot
	})
	require.NoErrorf(t, err, "positions = %+v %+v %v", start, end, err)
	require.Equalf(t, 3, start.Column, "positions = %+v %+v %v", start, end, err)
	require.Equalf(t, 4, end.Column, "positions = %+v %+v %v", start, end, err)
}

func textSource(text string) *fixture {
	return &fixture{read: func(mark p.Mark, _ p.Request) (p.Read, error) {
		if mark.Byte == len(text) {
			return eof(len(text)), nil
		}
		next := mark.Byte + 1
		tok := text[mark.Byte:next]
		return ready(token(p.TokenKind(tok), tok, mark.Byte, next), p.Mark{Byte: next}), nil
	}}
}

func TestNestedFirstCommitment(t *testing.T) {
	b := p.ChoiceSpec{Lookahead: 2}
	tests := []struct {
		name string
		rule p.Rule
		miss bool
	}{
		{"inner fallback keeps outer tentative", p.Choose(b,
			p.Seq(tagged("a"), p.Choose(b, p.Seq(tagged("b"), tagged("x")), tagged("c"))),
			p.Seq(tagged("a"), tagged("b"), tagged("z")),
		), false},
		{"inner success supplies outer commitment", p.Choose(b,
			p.Seq(tagged("a"), p.Choose(b, tagged("b"), tagged("c")), tagged("!")),
			p.Seq(tagged("a"), tagged("b"), tagged("z")),
		), true},
		{"inner commitment supplies outer commitment", p.Choose(b,
			p.Seq(tagged("a"), p.Choose(b, p.Seq(tagged("b"), tagged("z"), tagged("!")), tagged("c"))),
			p.Seq(tagged("a"), tagged("b"), tagged("z")),
		), true},
		{"optional propagates committed consumption", p.Seq(
			p.Opt(p.Seq(tagged("a"), tagged("x"))),
			tagged("a"), tagged("b"), tagged("z"),
		), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := configured(t, single(tt.rule), builders(), p.Limits{})
			session := engine.Start(textSource("abz"), "")
			if !tt.miss {
				done(t, session)
				return
			}
			root := done(t, session)
			child, _ := root.Child(0)
			diagnostic, ok := child.(*p.ErrorNode)
			require.Truef(t, ok, "file child = %v, want a positioned recovery error", child)
			require.Equalf(t, snapshot, diagnostic.Problem().Found.Point.Source, "file child = %v, want a positioned recovery error", child)
		})
	}
}

func TestProgramResumesPendingStep(t *testing.T) {
	steps := 0
	available, final := 1, false
	grammar := single(p.Program("first", map[string]p.ProgramState{
		"first": {
			Takes: []string{"second"},
			Step: func(look p.Look, view p.View) p.Move {
				steps++
				require.Equalf(t, "a", look.Token.Text, "first step got %+v and %q", look, view.State)
				require.Equalf(t, p.State("scope"), view.State, "first step got %+v and %q", look, view.State)
				return p.Take("second", "prefix")
			},
		},
		"second": {
			Takes: []string{"done"},
			Step: func(look p.Look, view p.View) p.Move {
				steps++
				tokens := view.Parts.Tokens("prefix")
				require.Lenf(t, tokens, 1, "lost capture: %+v, look %+v", tokens, look)
				require.Equalf(t, "a", tokens[0].Text, "lost capture: %+v, look %+v", tokens, look)
				require.Equalf(t, "b", look.Token.Text, "lost capture: %+v, look %+v", tokens, look)
				return p.Take("done", "")
			},
		},
		"done": returnStep(),
	}))
	src := textSource("ab")
	read := src.read
	src.read = func(mark p.Mark, req p.Request) (p.Read, error) {
		if mark.Byte == available && !final {
			return shortage(), nil
		}
		return read(mark, req)
	}
	session := configured(t, grammar, builders(), p.Limits{Steps: 3}).Start(src, "scope")
	for range 10 {
		paused(t, session)
	}
	require.Equalf(t, 1, steps, "step ran %d times before new input", steps)
	available = 2
	paused(t, session)
	final = true
	done(t, session)
	require.Equalf(t, 2, steps, "steps replayed during resume: %d", steps)
}

func TestProgressAndFailures(t *testing.T) {
	t.Run("configure", func(t *testing.T) {
		for _, grammar := range []p.Grammar{
			single(p.Ref("start")),
			single(p.Choose(p.ChoiceSpec{}, p.Tok(p.EndOfFile()), p.Seq(p.Opt(tagged("a")), p.Ref("start")))),
			single(p.Many(p.Opt(tagged("a")))),
			single(p.Sep(tagged("a"), p.Tok(p.EndOfFile()), false)),
			single(p.Program("run", map[string]p.ProgramState{"run": callStep("start", "run", "")})),
		} {
			{
				_, err := p.Configure(grammar, builders(), p.Limits{})
				require.ErrorIsf(t, err, p.ErrGrammar, "Configure = %v", err)
			}
		}
	})
	t.Run("runtime", func(t *testing.T) {
		zero := p.Program("return", map[string]p.ProgramState{"return": returnStep()})
		undeclared := p.Program("run", map[string]p.ProgramState{
			"run": {Control: func(p.View) p.Move { return p.Jump("run") }},
		})
		loop := p.Program("run", map[string]p.ProgramState{
			"run": {Jumps: []string{"run"}, Control: func(p.View) p.Move { return p.Jump("run") }},
		})
		takeEOF := p.Program("run", map[string]p.ProgramState{
			"run":  {Takes: []string{"done"}, Step: func(p.Look, p.View) p.Move { return p.Take("done", "") }},
			"done": returnStep(),
		})
		for _, tt := range []struct {
			rule p.Rule
			want error
		}{{p.Many(zero), p.ErrGrammar}, {undeclared, p.ErrGrammar}, {loop, p.ErrLimit}, {takeEOF, p.ErrGrammar}} {
			engine := configured(t, single(tt.rule), builders(), p.Limits{ZeroMoves: 4})
			session := engine.Start(textSource(""), "")
			root, status, err := session.Run()
			require.ErrorIsf(t, err, tt.want, "Run() = %v, %v, %v; want %v", root, status, err, tt.want)
			require.Truef(t, root == nil, "Run() = %v, %v, %v; want %v", root, status, err, tt.want)
			require.Equalf(t, p.Done, status, "Run() = %v, %v, %v; want %v", root, status, err, tt.want)
			{
				_, _, again := session.Run()
				require.Truef(t, again == err, "terminal error changed")
			}
		}
	})

	t.Run("virtual tokens and source errors", func(t *testing.T) {
		newSource := func(nonprogress bool) *fixture {
			return &fixture{read: func(mark p.Mark, req p.Request) (p.Read, error) {
				if mark.Virtual == 2 {
					return eof(0), nil
				}
				tok := token("indent", "", 0, 0)
				tok.Synthetic = true
				next := mark
				if !nonprogress {
					next.Virtual++
				}
				return ready(tok, next), nil
			}}
		}
		// One virtual token commits the default First arm, and two virtual
		// tokens are enough to demonstrate progress at a single byte.
		indent := tagged("indent")
		engine := configured(t, single(p.Many(indent)), builders(), p.Limits{VirtualTokens: 2})
		done(t, engine.Start(newSource(false), ""))
		engine = configured(t, single(p.Choose(p.ChoiceSpec{}, p.Seq(indent, tagged("x")), p.Many(indent))), builders(), p.Limits{})
		root := done(t, engine.Start(newSource(false), ""))
		child, _ := root.Child(0)
		{
			errorNode, ok := child.(*p.ErrorNode)
			require.Truef(t, ok, "the virtual take did not commit the first arm")
			require.Lenf(t, errorNode.Problem().Skipped, 2, "the virtual take did not commit the first arm")
		}
		engine = configured(t, single(p.Many(indent)), builders(), p.Limits{VirtualTokens: 1})
		{
			_, _, err := engine.Start(newSource(false), "").Run()
			require.ErrorIsf(t, err, p.ErrLimit, "expected virtual limit, got %v", err)
		}
		{
			_, _, err := engine.Start(newSource(true), "").Run()
			require.ErrorIsf(t, err, p.ErrSource, "expected source contract error, got %v", err)
		}
	})

	t.Run("deferred and finished failures", func(t *testing.T) {
		cause := errors.New("factory failure")
		build := builders()
		calls := 0
		build.Node = func(p.Build[tree.Node]) (tree.Node, error) {
			calls++
			return nil, cause
		}
		engine := configured(t, single(p.Make("a", "word", tagged("a"))), build, p.Limits{})
		{
			_, _, err := engine.Start(textSource("ab"), "").Run()
			require.ErrorIsf(t, err, cause, "fallback did not retain the completed recipe: %v, %d calls", err, calls)
			require.Equalf(t, 1, calls, "fallback did not retain the completed recipe: %v, %d calls", err, calls)
		}
		{
			root, status, err := engine.Start(textSource("a"), "").Run()
			require.ErrorIsf(t, err, p.ErrBuild, "failed build = %v, %v, %v, %d calls", root, status, err, calls)
			require.ErrorIsf(t, err, cause, "failed build = %v, %v, %v, %d calls", root, status, err, calls)
			require.Equalf(t, 2, calls, "failed build = %v, %v, %v, %d calls", root, status, err, calls)
			require.Truef(t, root == nil, "failed build = %v, %v, %v, %d calls", root, status, err, calls)
			require.Equalf(t, p.Done, status, "failed build = %v, %v, %v, %d calls", root, status, err, calls)
		}
	})
}

func TestDeepNamedCallsAndBuildUseBoundedGoStack(t *testing.T) {
	const depth = 5000
	checkStack := func() {
		t.Helper()
		var callers [32]uintptr
		require.NotEqualf(t, len(callers), runtime.Callers(0, callers[:]), "grammar or construction is running on a deeply recursive Go stack")
	}
	input := strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth)
	step := p.Program("leaf", map[string]p.ProgramState{
		"leaf": {
			Takes: []string{"done"},
			Step: func(look p.Look, _ p.View) p.Move {
				checkStack()
				if look.Token.Text == "1" {
					return p.Take("done", "")
				}
				return p.Reject(p.Word("1"))
			},
		},
		"done": returnStep(),
	})
	grammar := p.Grammar{Start: "value", Rules: map[string]p.Rule{
		"value": p.Choose(p.ChoiceSpec{},
			p.Make("value", "array", p.Seq(skip("["), p.Ref("value"), skip("]"))),
			p.Make("value", "number", step),
		),
	}}
	build := builders()
	baseNode := build.Node
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		checkStack()
		return baseNode(in)
	}
	root := done(t, configured(t, grammar, build, p.Limits{}).Start(textSource(input), ""))
	{
		got := len(records(root))
		require.Equalf(t, depth+2, got, "visited %d nodes, want %d", got, depth+2)
	}
}

type narrowNode interface {
	tree.Node
	fixtureNode()
}

type narrowFile struct{ *tree.Container }

func (*narrowFile) fixtureNode() {}

type narrowError struct{ *tree.ErrorNode }

func (*narrowError) fixtureNode() {}

func TestTypedRootConstraint(t *testing.T) {
	build := p.Builders[narrowNode]{
		Node: func(in p.Build[narrowNode]) (narrowNode, error) {
			return &narrowFile{tree.NewContainer(in.Kind, in.Span)}, nil
		},
		Error: func(_ string, problem p.Problem) (narrowNode, error) {
			return &narrowError{tree.NewError(problem.Span, "invalid input")}, nil
		},
		File: func(full tree.Span, parts []narrowNode, _ []p.Trivia) (narrowNode, error) {
			return &narrowFile{tree.NewContainer("file", full, parts[0])}, nil
		},
	}
	engine, err := p.Configure(single(p.Make("root", "root", tagged("x"))), build, p.Limits{})
	require.NoError(t, err)
	root, status, err := engine.Start(textSource("x"), "").Run()
	require.NoErrorf(t, err, "typed Run = %v, %v, %v", root, status, err)
	require.Equalf(t, p.Done, status, "typed Run = %v, %v, %v", root, status, err)
	var _ narrowNode = root
	{
		_, ok := root.Child(0)
		require.Truef(t, ok, "typed root has no child")
	}
	root, status, err = engine.Start(textSource("?"), "").Run()
	require.NoErrorf(t, err, "typed recovery = %v, %v, %v", root, status, err)
	require.Equalf(t, p.Done, status, "typed recovery = %v, %v, %v", root, status, err)
	{
		child, ok := root.Child(0)
		require.Truef(t, ok, "typed file missing its diagnostic: %v", child)
		require.Equalf(t, tree.KindError, child.Info().Kind, "typed file missing its diagnostic: %v", child)
	}

}

func TestParserImports(t *testing.T) {
	names, err := filepath.Glob("*.go")
	require.NoErrorf(t, err, "parser sources: %v, %v", names, err)
	require.NotEmptyf(t, names, "parser sources: %v, %v", names, err)
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(gotoken.NewFileSet(), name, nil, goparser.ImportsOnly)
		require.NoError(t, err)
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			prefix, _, _ := strings.Cut(path, "/")
			assert.Falsef(t, strings.Contains(prefix, ".") && path != "github.com/xiam/leafkit/tree", "%s imports %q", name, path)
		}
	}
}
