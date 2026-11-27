package tests

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
)

func TestComputedRequestCaller(t *testing.T) {
	head := p.Bind("delimiter", tagged("head"))
	req := p.Request{Goal: "x", Argument: "start", Layout: p.Joined}
	rule := p.WithLex(head, func(view p.View) p.Request {
		return p.Request{Goal: view.Parts.Tokens("delimiter")[0].Text, Argument: string(view.State), Layout: p.Joined}
	}, p.Make("tail", "tail", tagged("tail")))
	engine := configured(t, ambiguityGrammar(rule), builders(), p.Limits{})
	open := func(_ *testing.T, feed *adapterInput) p.Source {
		return &fixture{read: func(mark p.Mark, request p.Request) (p.Read, error) {
			if mark.Byte == 2 && feed.sealed && request == (p.Request{}) {
				return eof(2), nil
			}
			if feed.available <= mark.Byte {
				return shortage(), nil
			}
			if mark.Byte == 0 && request == (p.Request{}) {
				return ready(fixtureToken("head", "x", 0, 1), p.Mark{Byte: 1, Key: "after-head"}), nil
			}
			if mark.Byte == 1 && request == req {
				return ready(fixtureToken("tail", "y", 1, 2), p.Mark{Byte: 2, Key: "after-tail"}), nil
			}
			return p.Read{}, fmt.Errorf("unexpected minimal offer at %v under %+v", mark, request)
		}}
	}
	plan := readingPlan{"minimal", []grammarUse{
		{"prefix", "start", p.Request{}, true},
		{"body", "start", req, true},
		{"file/final", "start", p.Request{}, false},
	}}
	whole, wholeLog := runRequestTrace(t, engine, "xy", "start", "whole", open)
	polled, polledLog := runRequestTrace(t, engine, "xy", "start", "polled", open)
	checkStateShape(t, whole, "file(tail)")
	equalRecords(t, polled, records(whole))
	noStateDiagnostics(t, whole)
	noStateDiagnostics(t, polled)
	checkRequestGraphs(t, wholeLog.graph(t, plan), polledLog.graph(t, plan))
	retries := 0
	for _, attempt := range polledLog.attempts {
		if attempt.offer.mark.Byte != 1 {
			continue
		}
		require.Equalf(t, (requestOffer{p.Mark{Byte: 1, Key: "after-head"}, req}), attempt.offer, "held body changed full request or mark: %+v", attempt)
		if attempt.observation.read.Status == p.NeedInput {
			retries++
		}
	}
	require.GreaterOrEqualf(t, retries, 6, "expected several extra held raw reads, got %d", retries)
	require.Greaterf(t, len(polledLog.attempts), len(wholeLog.attempts), "expected several extra held raw reads, got %d", retries)
}

func TestPaperHelperCompositions(t *testing.T) {
	t.Run("grammar-extension", func(t *testing.T) {
		atom := p.Make("atom", "base", p.Tok(p.Word("x")))
		base := p.Grammar{Start: "file", Rules: map[string]p.Rule{"file": p.Ref("atom"), "atom": atom}}
		derived := base
		derived.Rules = maps.Clone(base.Rules)
		derived.Rules["original"] = atom
		derived.Rules["atom"] = p.Make("extension", "derived", p.Ref("original"))
		for _, tc := range []struct {
			grammar p.Grammar
			want    string
		}{
			{base, "file(base)"}, {derived, "file(derived(base))"},
		} {
			engine := configured(t, tc.grammar, builders(), p.Limits{})
			available, sealed := 1, true
			root := done(t, engine.Start(stateFixture("x")(&available, &sealed), ""))
			checkStateShape(t, root, tc.want)
			noStateDiagnostics(t, root)
		}
	})
	t.Run("word-normalization", func(t *testing.T) {
		match := p.Program("word", map[string]p.ProgramState{
			"word": {Takes: []string{"end"}, Step: func(look p.Look, _ p.View) p.Move {
				if p.Matches(look, p.Kind("word")) && strings.EqualFold(look.Token.Text, "case") {
					return p.Take("end", "")
				}
				return p.Reject(p.Word("case"))
			}},
			"end": {Control: func(p.View) p.Move { return p.Return() }},
		})
		engine := configured(t, ambiguityGrammar(p.Make("keyword", "word", match)), builders(), p.Limits{})
		for _, kind := range []p.TokenKind{"word", "quoted"} {
			n, available, sealed := 4, 4, true
			root := done(t, engine.Start(sourceFor([]p.Token{fixtureToken(kind, "CaSe", 0, 4)}, &n, &available, &sealed), ""))
			if kind == "word" {
				equalRecords(t, root, []record{{"file", span(0, 4), "", 1}, {"word", span(0, 4), "CaSe", 0}})
				noStateDiagnostics(t, root)
			} else {
				checkStateShape(t, root, "file(leafkit:error)")
			}
		}
	})
	t.Run("physical-line-guard", func(t *testing.T) {
		guard := p.Program("guard", map[string]p.ProgramState{
			"guard": {Step: func(look p.Look, _ p.View) p.Move {
				if !look.End && !look.Token.BreakBefore {
					return p.Return()
				}
				return p.Reject()
			}},
		})
		rule := p.Seq(guard, p.At(p.OnSameLine(p.Kind("word"))), p.Make("word", "word", tagged("word")))
		engine := configured(t, ambiguityGrammar(rule), builders(), p.Limits{})
		for _, lineBreak := range []bool{false, true} {
			token := fixtureToken("word", "x", 0, 1)
			token.BreakBefore = lineBreak
			n, available, sealed := 1, 1, true
			root := done(t, engine.Start(sourceFor([]p.Token{token}, &n, &available, &sealed), ""))
			if lineBreak {
				checkStateShape(t, root, "file(leafkit:error)")
			} else {
				equalRecords(t, root, []record{{"file", span(0, 1), "", 1}, {"word", span(0, 1), "x", 0}})
				noStateDiagnostics(t, root)
			}
		}
	})
}
