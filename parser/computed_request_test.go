package parser_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

type requestFaultSource struct {
	p.Source
	request p.Request
	err     error
}

func (s requestFaultSource) Read(mark p.Mark, request p.Request) (p.Read, error) {
	if request == s.request {
		return p.Read{}, s.err
	}
	return s.Source.Read(mark, request)
}

func computedProbeGrammar(hand, chart bool, req p.Request) p.Grammar {
	body := p.Make("x", "computed", tagged("x"))
	if chart {
		body = p.Choose(p.ChoiceSpec{Policy: p.Longest}, body)
	}
	file := p.WithLex(p.Seq(), func(p.View) p.Request { return req }, p.Ref("body"))
	if hand {
		file = p.Program("call", map[string]p.ProgramState{
			"call": {Calls: []string{"body"}, ReturnsTo: []string{"done"}, Control: func(p.View) p.Move {
				return p.CallWithLex("body", "done", "", req)
			}},
			"done": {Control: func(p.View) p.Move { return p.Return() }},
		})
	}
	return p.Grammar{Start: "file", Rules: map[string]p.Rule{"file": file, "body": body}}
}

func TestComputedRequestErrors(t *testing.T) {
	build := builders()
	{
		_, err := p.Configure(single(p.WithLex(p.Seq(), nil, tagged("x"))), build, p.Limits{})
		require.ErrorIsf(t, err, p.ErrGrammar, "missing computation = %v, want ErrGrammar", err)
	}
	left := p.WithLex(p.Ref("left"), func(p.View) p.Request { return p.Request{} }, tagged("x"))
	{
		_, err := p.Configure(p.Grammar{Start: "left", Rules: map[string]p.Rule{"left": left}}, build, p.Limits{})
		require.ErrorIsf(t, err, p.ErrGrammar, "left-corner request computation = %v, want ErrGrammar", err)
	}
	sourceErr, buildErr := errors.New("unsupported lexical goal"), errors.New("factory failure")
	request := p.Request{Goal: "body", Argument: "X", Layout: p.Lines}
	for _, hand := range []bool{false, true} {
		for _, chart := range []bool{false, true} {
			for _, tc := range []struct {
				name            string
				req             p.Request
				limits          p.Limits
				fault           p.Source
				failBuild       bool
				category, cause error
			}{
				{"layout", p.Request{Layout: p.Layout(255)}, p.Limits{}, textSource("x"), false, p.ErrGrammar, nil},
				{"source", request, p.Limits{}, requestFaultSource{textSource("x"), request, sourceErr}, false, p.ErrSource, sourceErr},
				{"work", request, p.Limits{Steps: 2}, textSource("x"), false, p.ErrLimit, nil},
				{"builder", request, p.Limits{}, textSource("x"), true, p.ErrBuild, buildErr},
			} {
				t.Run(fmt.Sprintf("%s/hand=%v/chart=%v", tc.name, hand, chart), func(t *testing.T) {
					construct := build
					if tc.failBuild {
						construct.Node = func(p.Build[tree.Node]) (tree.Node, error) { return nil, buildErr }
					}
					engine := configured(t, computedProbeGrammar(hand, chart, tc.req), construct, tc.limits)
					root, status, err := engine.Start(tc.fault, "").Run()
					require.Truef(t, root == nil, "result = %v/%v/%v, want terminal %v with cause %v", root, status, err, tc.category, tc.cause)
					require.Equalf(t, p.Done, status, "result = %v/%v/%v, want terminal %v with cause %v", root, status, err, tc.category, tc.cause)
					require.ErrorIsf(t, err, tc.category, "result = %v/%v/%v, want terminal %v with cause %v", root, status, err, tc.category, tc.cause)
					require.Falsef(t, tc.cause != nil && !errors.Is(err, tc.cause), "result = %v/%v/%v, want terminal %v with cause %v", root, status, err, tc.category, tc.cause)
				})
			}
		}
	}
}
