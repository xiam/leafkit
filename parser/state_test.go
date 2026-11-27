package parser_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

func TestBranchStateProgressAndLimits(t *testing.T) {
	grow := p.Update(p.Seq(), func(view p.View) p.State { return view.State + "x" })
	build := builders()
	for _, rule := range []p.Rule{
		p.Many(grow),
		p.Expr("expr", p.Scope(grow)),
		p.Update(p.Seq(), nil),
		p.Guard(p.Seq(), nil),
	} {
		{
			_, err := p.Configure(single(rule), build, p.Limits{})
			require.ErrorIsf(t, err, p.ErrGrammar, "Configure = %v, want ErrGrammar", err)
		}
	}

	program := p.Program("loop", map[string]p.ProgramState{
		"loop": {Jumps: []string{"loop"}, Control: func(view p.View) p.Move {
			return p.SetState("loop", view.State+"x")
		}},
	})
	undeclared := p.Program("loop", map[string]p.ProgramState{
		"loop": {Control: func(p.View) p.Move { return p.SetState("loop", "x") }},
	})
	recursive := p.Grammar{Start: "start", Rules: map[string]p.Rule{
		"start": p.Choose(p.ChoiceSpec{Policy: p.All, Slot: "term"},
			p.Make("term", "more", p.Seq(grow, p.Ref("start"), tagged("+"))),
			p.Make("term", "atom", tagged("x")),
		),
	}}
	for _, tc := range []struct {
		name    string
		grammar p.Grammar
		limits  p.Limits
		want    error
	}{
		{"state-move-loop", single(program), p.Limits{ZeroMoves: 8}, p.ErrLimit},
		{"undeclared-transition", single(undeclared), p.Limits{}, p.ErrGrammar},
		{"state-growing-left-recursion", recursive, p.Limits{Descriptors: 40}, p.ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := p.Configure(tc.grammar, build, tc.limits)
			require.NoError(t, err)
			root, status, err := engine.Start(textSource("x"), "").Run()
			require.Truef(t, root == nil, "Run = %v, %v, %v, want fatal %v", root, status, err, tc.want)
			require.Equalf(t, p.Done, status, "Run = %v, %v, %v, want fatal %v", root, status, err, tc.want)
			require.ErrorIsf(t, err, tc.want, "Run = %v, %v, %v, want fatal %v", root, status, err, tc.want)
		})
	}
}

func TestBranchStateEquivalentExitsSharePack(t *testing.T) {
	var suffixCalls int
	build := builders()
	prior := build.Node
	build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
		if in.Kind == "suffix" {
			suffixCalls++
		}
		return prior(in)
	}
	arm := func(kind tree.Kind) p.Rule {
		change := p.Update(tagged("x"), func(p.View) p.State { return p.State(kind) })
		return p.Make("choice", kind, p.Scope(change))
	}
	suffix := p.Make("suffix", "suffix", p.Guard(tagged("y"), func(view p.View) bool {
		return view.State == "entry"
	}))
	choice := p.Choose(p.ChoiceSpec{Policy: p.All, Slot: "choice"}, arm("first"), arm("second"))
	engine, err := p.Configure(single(p.Seq(choice, suffix)), build, p.Limits{})
	require.NoError(t, err)
	root, status, err := engine.Start(textSource("xy"), "entry").Run()
	require.NoErrorf(t, err, "Run = %v, %v, %v", root, status, err)
	require.Equalf(t, p.Done, status, "Run = %v, %v, %v", root, status, err)
	require.Truef(t, root != nil, "Run = %v, %v, %v", root, status, err)
	pack, ok := root.Child(0)
	require.Truef(t, ok, "first child = %v, want a local pack", pack)
	require.Equalf(t, tree.KindAmbiguity, pack.Info().Kind, "first child = %v, want a local pack", pack)
	for i, want := range []tree.Kind{"first", "second"} {
		child, ok := pack.Child(i)
		require.Truef(t, ok, "alternative %d = %v, want %q", i, child, want)
		require.Equalf(t, want, child.Info().Kind, "alternative %d = %v, want %q", i, child, want)
	}
	{
		child, ok := root.Child(1)
		require.Truef(t, ok, "suffix = %v, built %d times, want one shared continuation", child, suffixCalls)
		require.Equalf(t, tree.Kind("suffix"), child.Info().Kind, "suffix = %v, built %d times, want one shared continuation", child, suffixCalls)
		require.Equalf(t, 1, suffixCalls, "suffix = %v, built %d times, want one shared continuation", child, suffixCalls)
	}
}
