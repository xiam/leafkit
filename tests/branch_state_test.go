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

func stateIs(state p.State) p.Rule {
	return p.Guard(p.Seq(), func(view p.View) bool { return view.State == state })
}

func withState(state p.State) p.Rule {
	return p.Update(p.Seq(), func(p.View) p.State { return state })
}

func stateCheckpoint(state p.State) p.Rule {
	return p.Make("state", tree.Kind("state:"+state), stateIs(state))
}

type stateInput func(available *int, sealed *bool) p.Source

func stateFixture(input string) stateInput {
	size := len(input)
	tokens := expressionTokens(input)
	return func(available *int, sealed *bool) p.Source {
		return sourceFor(tokens, &size, available, sealed)
	}
}

type stateDiagnostic struct {
	slot    string
	problem p.Problem
}

func stateDiagnostics(t *testing.T, root tree.Node) []stateDiagnostic {
	t.Helper()
	var diagnostics []stateDiagnostic
	for _, errNode := range recoveryProblems(t, root) {
		diagnostics = append(diagnostics, stateDiagnostic{errNode.Slot(), errNode.Problem()})
	}
	return diagnostics
}

func stateSchedules(t *testing.T, engine *p.Engine[tree.Node], size int, source stateInput, initial p.State, check func(*testing.T, tree.Node)) {
	t.Helper()
	var wantTree []record
	var wantDiagnostics []stateDiagnostic
	for _, schedule := range []string{"whole", "byte-stepped", "repeated-polls"} {
		t.Run(schedule, func(t *testing.T) {
			available, sealed := size, true
			if schedule != "whole" {
				available, sealed = 0, false
			}
			session := engine.Start(source(&available, &sealed), initial)
			if !sealed {
				for available <= size {
					paused(t, session)
					if schedule == "repeated-polls" {
						paused(t, session)
						paused(t, session)
					}
					if available == size {
						break
					}
					available++
				}
				sealed = true
			}
			root := done(t, session)
			require.NoError(t, tree.Validate(root))
			check(t, root)
			diagnostics := stateDiagnostics(t, root)
			if schedule == "whole" {
				wantTree, wantDiagnostics = records(root), diagnostics
				return
			}
			equalRecords(t, root, wantTree)
			require.Equalf(t, wantDiagnostics, diagnostics, "diagnostics = %+v, want %+v", diagnostics, wantDiagnostics)
		})
	}
}

func stateShape(root tree.Node) string {
	var children []string
	for i := 0; ; i++ {
		child, ok := root.Child(i)
		if !ok {
			break
		}
		children = append(children, stateShape(child))
	}
	kind := string(root.Info().Kind)
	if len(children) == 0 {
		return kind
	}
	return kind + "(" + strings.Join(children, ",") + ")"
}

func checkStateShape(t *testing.T, root tree.Node, want string) {
	t.Helper()
	{
		got := stateShape(root)
		require.Equalf(t, want, got, "tree shape = %s, want %s", got, want)
	}
}

func noStateDiagnostics(t *testing.T, root tree.Node) {
	t.Helper()
	{
		got := stateDiagnostics(t, root)
		require.Lenf(t, got, 0, "diagnostics = %+v, want none", got)
	}
}

func declareName(view p.View) p.State {
	return view.State + p.State(view.Parts.Tokens("name")[0].Text) + ";"
}

func programDeclaration() p.Rule {
	return p.Program("let", map[string]p.ProgramState{
		"let": {Takes: []string{"name"}, Step: func(look p.Look, _ p.View) p.Move {
			if p.Matches(look, p.Word("let")) {
				return p.Take("name", "")
			}
			return p.Reject(p.Word("let"))
		}},
		"name": {Takes: []string{"declare"}, Step: func(look p.Look, _ p.View) p.Move {
			if p.Matches(look, p.Kind("word")) {
				return p.Take("declare", "name")
			}
			return p.Reject(p.Kind("word"))
		}},
		"declare": {Jumps: []string{"end"}, Control: func(view p.View) p.Move {
			return p.SetState("end", declareName(view))
		}},
		"end": {Control: func(p.View) p.Move { return p.Return() }},
	})
}

func useName(view p.View, name string) string {
	scopes := strings.Split(string(view.State), "/")
	for i := len(scopes) - 1; i >= 0; i-- {
		if strings.Contains(";"+scopes[i], ";"+name+";") {
			if i == len(scopes)-1 {
				return "local"
			}
			return "inherited"
		}
	}
	return "call"
}

func TestBranchStateDeclarations(t *testing.T) {
	input := "x;let x;{x;let x;x;{let y;y;};y;};x;y;let y;y;"
	want := "file(call-x,declare,inherited-x,declare,local-x,declare,local-y,call-y,local-x,call-y,declare,local-y,state:x;y;)"
	for _, hand := range []bool{false, true} {
		for _, chart := range []bool{false, true} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("hand=%v/chart=%v/reverse=%v", hand, chart, reverse), func(t *testing.T) {
					decl := p.Update(p.Seq(p.Tok(p.Word("let")), p.Bind("name", tagged("word"))), declareName)
					if hand {
						decl = programDeclaration()
					}
					var uses []p.Rule
					for _, name := range []string{"x", "y"} {
						for _, kind := range []string{"local", "inherited", "call"} {
							word := p.Tok(p.Word(name))
							if chart {
								word = p.Choose(p.ChoiceSpec{Policy: p.Longest}, word)
							}
							body := p.Guard(word, func(view p.View) bool { return useName(view, name) == kind })
							uses = append(uses, p.Make("use", tree.Kind(kind+"-"+name), p.Seq(
								// Reject before the token when another classified arm should run.
								p.Guard(p.Seq(), func(view p.View) bool { return useName(view, name) == kind }),
								body, skip(";"),
							)))
						}
					}
					if reverse {
						slices.Reverse(uses)
					}
					grammar := p.Grammar{Start: "file", Rules: map[string]p.Rule{
						"file":        p.Seq(p.Many(p.Ref("statement")), stateCheckpoint("x;y;")),
						"statement":   p.Choose(p.ChoiceSpec{}, p.Ref("block"), p.Ref("declaration"), p.Ref("use")),
						"declaration": p.Make("stmt", "declare", p.Seq(decl, skip(";"))),
						"block": p.Scope(p.Seq(skip("{"), p.Update(p.Seq(), func(view p.View) p.State {
							return view.State + "/"
						}), p.Many(p.Ref("statement")), skip("}"), skip(";"))),
						"use": p.Choose(p.ChoiceSpec{}, uses...),
					}}
					engine := configured(t, grammar, builders(), p.Limits{})
					stateSchedules(t, engine, len(input), stateFixture(input), "", func(t *testing.T, root tree.Node) {
						checkStateShape(t, root, want)
						noStateDiagnostics(t, root)
					})
				})
			}
		}
	}
}

func branchLexicalSource(twoMarks bool) stateInput {
	return func(available *int, sealed *bool) p.Source {
		return &fixture{read: func(mark p.Mark, request p.Request) (p.Read, error) {
			switch mark.Byte {
			case 0:
				if *available < 1 {
					return shortage(), nil
				}
				next := p.Mark{Byte: 1}
				if twoMarks {
					next.Key = request.Goal
				}
				return ready(fixtureToken("head", "x", 0, 1), next), nil
			case 1:
				if *available < 2 {
					return shortage(), nil
				}
				return ready(fixtureToken(p.TokenKind("tail"+mark.Key), "y", 1, 2), p.Mark{Byte: 2}), nil
			case 2:
				if *sealed {
					return eof(2), nil
				}
				return shortage(), nil
			default:
				return p.Read{}, fmt.Errorf("unexpected replay mark %v", mark)
			}
		}}
	}
}

func TestBranchStateChoices(t *testing.T) {
	for _, twoMarks := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			order := []string{"a", "b"}
			if reverse {
				slices.Reverse(order)
			}
			for _, tc := range []struct {
				name       string
				policy     p.Policy
				forceChart bool
				filter     bool
				want       []string
			}{
				{"first", p.First, false, false, order[:1]},
				{"chart-first", p.First, true, false, order[:1]},
				{"longest-tie", p.Longest, false, false, order[:1]},
				{"longest-filter", p.Longest, false, true, []string{"b"}},
				{"all", p.All, false, false, order},
				{"all-filter", p.All, false, true, []string{"b"}},
			} {
				t.Run(fmt.Sprintf("%s/two-marks=%v/reverse=%v", tc.name, twoMarks, reverse), func(t *testing.T) {
					var arms, suffixes []p.Rule
					for _, name := range order {
						head := tagged("head")
						if tc.forceChart {
							head = p.Choose(p.ChoiceSpec{Policy: p.Longest}, head)
						}
						arms = append(arms, p.Make("head", tree.Kind("arm-"+name),
							p.Update(p.Lex(p.Request{Goal: name}, p.Bind("offered", head)),
								func(view p.View) p.State {
									if view.State == "entry" && view.Parts.Tokens("offered")[0].Text == "x" {
										return p.State(name)
									}
									return "bad"
								})))
						kind := "tail"
						if twoMarks {
							kind += name
						}
						suffixes = append(suffixes, p.Seq(stateIs(p.State(name)), p.Make("tail", tree.Kind("suffix-"+name), tagged(kind)),
							stateCheckpoint(p.State(name))))
					}
					spec := p.ChoiceSpec{Policy: tc.policy}
					if tc.policy != p.First {
						spec.Slot = "head"
					}
					if tc.filter {
						spec.Filter = func(candidate p.Candidate) bool {
							tokens := candidate.Parts.Tokens("offered")
							return candidate.Before == "entry" && candidate.After == "b" && candidate.Kind == "arm-b" &&
								len(tokens) == 1 && tokens[0].Text == "x" && candidate.Span == span(0, 1)
						}
					}
					grammar := p.Grammar{Start: "file", Rules: map[string]p.Rule{
						"file": p.Seq(p.Choose(spec, arms...), p.Ref("exit")),
						"exit": p.Choose(p.ChoiceSpec{}, suffixes...),
					}}
					engine := configured(t, grammar, builders(), p.Limits{})
					stateSchedules(t, engine, 2, branchLexicalSource(twoMarks), "entry", func(t *testing.T, root tree.Node) {
						noStateDiagnostics(t, root)
						var wants []string
						for _, name := range tc.want {
							wants = append(wants, fmt.Sprintf("file(arm-%[1]s,suffix-%[1]s,state:%[1]s)", name))
						}
						want := wants[0]
						if len(wants) == 2 {
							want = "leafkit:ambiguity(" + strings.Join(wants, ",") + ")"
						}
						checkStateShape(t, root, want)
					})
				})
			}
		}
	}
}

func TestBranchStateRecovery(t *testing.T) {
	input := "?;z"
	for _, policy := range []p.Policy{p.First, p.Longest, p.All} {
		for _, reverse := range []bool{false, true} {
			order := []string{"left", "right"}
			if reverse {
				slices.Reverse(order)
			}
			t.Run(fmt.Sprintf("policy=%d/reverse=%v", policy, reverse), func(t *testing.T) {
				arm := func(name string) p.Rule {
					// Equal replay marks and lexical goals still describe distinct
					// rejected offers when the state that made them differs.
					return p.Make("arm", tree.Kind(name), p.Seq(withState(p.State(name)), tagged(name)))
				}
				spec := p.ChoiceSpec{Policy: policy}
				if policy != p.First {
					spec.Slot = "arm"
				}
				recoverable := p.Recover("damaged", p.Choose(spec, arm(order[0]), arm(order[1])), tokenBoundaries(true, ";"))
				file := p.Seq(withState("kept"), recoverable, p.Make("after", "after", p.Seq(stateIs("kept"), p.Tok(p.Word("z")))),
					stateCheckpoint("kept"))
				engine := configured(t, ambiguityGrammar(file), builders(), p.Limits{})
				stateSchedules(t, engine, len(input), stateFixture(input), "entry", func(t *testing.T, root tree.Node) {
					checkStateShape(t, root, "file(leafkit:error,after,state:kept)")
					found := fixtureToken("?", "?", 0, 1)
					want := []stateDiagnostic{{"damaged", p.Problem{
						Span: span(0, 2), Expected: []p.Pattern{p.Kind(p.TokenKind(order[0]))},
						Found:   p.Look{Token: found, Point: span(0, 0)},
						Skipped: []p.Token{fixtureToken("?", "?", 0, 1), fixtureToken(";", ";", 1, 2)},
					}}}
					{
						got := stateDiagnostics(t, root)
						require.Equalf(t, want, got, "diagnostics = %+v, want %+v", got, want)
					}
				})
			})
		}
	}
}

func TestBranchStateProgramAndExpression(t *testing.T) {
	input := "-x+y!?"
	for _, chart := range []bool{false, true} {
		t.Run(fmt.Sprintf("chart=%v", chart), func(t *testing.T) {
			atomToken := p.Bind("atom", tagged("word"))
			if chart {
				atomToken = p.Bind("atom", p.Choose(p.ChoiceSpec{Policy: p.Longest}, tagged("word")))
			}
			atom := p.Make("term", "word", p.Update(atomToken, func(view p.View) p.State {
				return view.State + p.State(view.Parts.Tokens("atom")[0].Text)
			}))
			expression := p.Expr("expr", atom,
				p.Operator{Form: p.Prefix, Match: p.Kind("-"), Power: 30, Kind: "neg"},
				p.Operator{Form: p.Infix, Match: p.Kind("+"), Power: 10, Assoc: p.Left, Kind: "add"},
				p.Operator{Form: p.Postfix, Match: p.Kind("!"), Power: 40, Kind: "tail", Tail: p.Seq(stateIs("begin-xy"), withState("tail"))},
				p.Operator{Form: p.Postfix, Match: p.Kind("?"), Power: 40, Kind: "postfix"},
			)
			program := p.Program("enter", map[string]p.ProgramState{
				"enter": {Jumps: []string{"call"}, Control: func(view p.View) p.Move {
					return p.SetState("call", view.State+"-")
				}},
				"call": {Calls: []string{"expr"}, ReturnsTo: []string{"check"}, Control: func(p.View) p.Move {
					return p.Call("expr", "check", "expression")
				}},
				"check": {Jumps: []string{"return"}, Control: func(view p.View) p.Move {
					if view.State == "tail" && slices.Equal(view.Parts.Kinds("expression"), []tree.Kind{"add"}) {
						return p.SetState("return", view.State+"-program")
					}
					return p.Reject(p.Kind("program-exit"))
				}},
				"return": {Control: func(p.View) p.Move { return p.Return() }},
			})
			engine := configured(t, p.Grammar{Start: "file", Rules: map[string]p.Rule{
				"file": p.Seq(program, stateCheckpoint("tail-program")),
				"expr": expression,
			}}, builders(), p.Limits{})
			stateSchedules(t, engine, len(input), stateFixture(input), "begin", func(t *testing.T, root tree.Node) {
				checkStateShape(t, root, "file(add(neg(word),postfix(tail(word))),state:tail-program)")
				noStateDiagnostics(t, root)
			})
		})
	}
}

func TestBranchStateAbandonedArm(t *testing.T) {
	input := "?;z"
	for _, chart := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("chart=%v/reverse=%v", chart, reverse), func(t *testing.T) {
				missing := tagged("bad")
				if chart {
					missing = p.Choose(p.ChoiceSpec{Policy: p.Longest}, missing)
				}
				abandoned := p.Seq(
					withState("abandoned"),
					p.Recover("trial", missing, tokenBoundaries(true, ";")),
					// This predicate checks the trial's own state and then
					// fails, even if recovery made it past the delimiter.
					p.Guard(p.Seq(), func(view p.View) bool { return view.State == "entry" }),
				)
				clean := p.Make("winner", "clean", p.Seq(stateIs("entry"), skip("?"), skip(";"), withState("winner")))
				arms := []p.Rule{abandoned, clean}
				if reverse {
					slices.Reverse(arms)
				}
				file := p.Seq(p.Choose(p.ChoiceSpec{Lookahead: 8}, arms...), stateCheckpoint("winner"), p.Tok(p.Word("z")))
				build := builders()
				build.Error = func(string, p.Problem) (tree.Node, error) {
					return nil, fmt.Errorf("built a diagnostic from the abandoned arm")
				}
				engine := configured(t, ambiguityGrammar(file), build, p.Limits{})
				stateSchedules(t, engine, len(input), stateFixture(input), "entry", func(t *testing.T, root tree.Node) {
					checkStateShape(t, root, "file(clean,state:winner)")
					noStateDiagnostics(t, root)
				})
			})
		}
	}
}

func TestBranchStateGuardRecovery(t *testing.T) {
	input := "x?;z"
	for _, chart := range []bool{false, true} {
		t.Run(fmt.Sprintf("chart=%v", chart), func(t *testing.T) {
			word := p.Tok(p.Word("x"))
			if chart {
				word = p.Choose(p.ChoiceSpec{Policy: p.Longest}, word)
			}
			body := p.Update(p.Make("unpublished", "unpublished", word), func(view p.View) p.State {
				return view.State + "-trial"
			})
			fail := p.Guard(body, func(view p.View) bool { return view.State != "entry-trial" })
			file := p.Seq(
				p.Recover("guard", fail, tokenBoundaries(true, ";")),
				p.Make("after", "after", p.Tok(p.Word("z"))),
				p.Insert(p.Kind(";"), func(look p.Look, view p.View) bool { return look.End && view.State == "entry" }),
				stateCheckpoint("entry"),
			)
			build := builders()
			node := build.Node
			build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
				if in.Kind == "unpublished" {
					return nil, fmt.Errorf("built the rejected guard body")
				}
				return node(in)
			}
			engine := configured(t, ambiguityGrammar(file), build, p.Limits{})
			stateSchedules(t, engine, len(input), stateFixture(input), "entry", func(t *testing.T, root tree.Node) {
				checkStateShape(t, root, "file(leafkit:error,after,state:entry)")
				found := fixtureToken("?", "?", 1, 2)
				want := []stateDiagnostic{{"guard", p.Problem{
					Span:    span(0, 3),
					Found:   p.Look{Token: found, Point: span(1, 1)},
					Skipped: []p.Token{fixtureToken("word", "x", 0, 1), fixtureToken("?", "?", 1, 2), fixtureToken(";", ";", 2, 3)},
				}}}
				{
					got := stateDiagnostics(t, root)
					require.Equalf(t, want, got, "diagnostics = %+v, want %+v", got, want)
				}
			})
		})
	}
}
