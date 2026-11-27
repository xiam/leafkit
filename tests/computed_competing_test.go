package tests

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

// The handmade source allocates opaque successor keys only for completed
// token reads, independently of the adapter.
type opaqueRequestSource struct {
	begin p.Mark
	reads map[requestOffer]p.Read
	live  map[p.Mark]bool
	lex   func(p.Mark, p.Request) (p.Read, error)
	next  int
}

func newOpaqueRequestSource(lex func(p.Mark, p.Request) (p.Read, error)) *opaqueRequestSource {
	start := p.Mark{Key: "initial"}
	return &opaqueRequestSource{begin: start, reads: make(map[requestOffer]p.Read), live: map[p.Mark]bool{start: true}, lex: lex}
}

func (s *opaqueRequestSource) Begin() (tree.SourceID, p.Mark) { return snapshot, s.begin }

func (s *opaqueRequestSource) Read(mark p.Mark, request p.Request) (p.Read, error) {
	if !s.live[mark] {
		return p.Read{}, fmt.Errorf("unknown or retired mark %v", mark)
	}
	key := requestOffer{mark, request}
	if read, ok := s.reads[key]; ok {
		return read, nil
	}
	read, err := s.lex(mark, request)
	if err == nil && read.Status != p.NeedInput {
		if read.Status == p.TokenReady {
			s.next++
			read.Next.Key = "mark:" + strconv.Itoa(s.next)
			s.live[read.Next] = true
		}
		s.reads[key] = read
	}
	return read, err
}

func (s *opaqueRequestSource) DiscardBefore(offset int) {
	for mark := range s.live {
		if mark.Byte < offset {
			delete(s.live, mark)
		}
	}
	for key := range s.reads {
		if key.mark.Byte < offset {
			delete(s.reads, key)
		}
	}
}

func competingRequest(opener string, state p.State) p.Request {
	layout := p.Lines
	if state == "B" {
		layout = p.Joined
	}
	return p.Request{Goal: "literal", Argument: opener + ":" + string(state), Layout: layout}
}

var requestA = competingRequest("x", "A")
var requestB = competingRequest("x", "B")

func competingHandmade(_ *testing.T, input *adapterInput) p.Source {
	return newOpaqueRequestSource(func(mark p.Mark, req p.Request) (p.Read, error) {
		switch {
		case mark.Byte == 3 && req == (p.Request{}):
			if input.sealed {
				return eof(3), nil
			}
			return shortage(), nil
		case mark.Byte == 0 && req == (p.Request{}):
			if input.available < 1 {
				return shortage(), nil
			}
			return ready(fixtureToken("head", "x", 0, 1), p.Mark{Byte: 1}), nil
		case mark.Byte == 1 && req == (p.Request{}):
			if input.available < 2 {
				return shortage(), nil
			}
			return ready(fixtureToken("skipped", "a", 1, 2), p.Mark{Byte: 2}), nil
		case mark.Byte == 1 && (req == requestA || req == requestB):
			if input.available < 2 || req == requestA && input.available < 3 {
				return shortage(), nil
			}
			return ready(fixtureToken("data", "a", 1, 2), p.Mark{Byte: 2}), nil
		case mark.Byte == 2 && req == (p.Request{}):
			if input.available < 3 {
				return shortage(), nil
			}
			return ready(fixtureToken("tail", "b", 2, 3), p.Mark{Byte: 3}), nil
		default:
			return p.Read{}, fmt.Errorf("unexpected competing offer at %v under %+v", mark, req)
		}
	})
}

func competingReplay(t *testing.T, input *adapterInput) p.Source {
	t.Helper()
	source, err := adapter.NewReplay(snapshot, input.open, struct{}{}, func(cur *cursor.Reader, _ struct{}, req p.Request) (scan.Source, func() struct{}, error) {
		lexer := scan.NewWithCursor(cur)
		switch req {
		case p.Request{}:
			lexer.MustAddRule("head", core.Literal("x"))
			lexer.MustAddRule("skipped", core.Literal("a"))
			lexer.MustAddRule("tail", core.Literal("b"))
		case requestA:
			// Branch A cannot commit its one-byte token until a lookahead
			// disproves the forbidden suffix.
			lexer.MustAddRule("data", core.NotFollowedBy(core.Literal("a"), core.Literal("!")))
		case requestB:
			lexer.MustAddRule("data", core.Literal("a"))
		default:
			return nil, nil, fmt.Errorf("unexpected request %+v", req)
		}
		return lexer, nil, nil
	}, adapter.Options{})
	require.NoError(t, err)
	return source
}

func competingGrammar(order []p.State, policy p.Policy, filtered bool) p.Grammar {
	var arms []p.Rule
	for _, branch := range order {
		prefix := p.Seq(stateIs("initial"), p.Bind("opener", tagged("head")), withState(branch))
		body := p.Seq(
			p.At(p.Kind("data")), // The next use shares this source observation.
			tagged("data"), stateCheckpoint(branch),
		)
		arms = append(arms, p.Make("reading", tree.Kind("reading-"+branch), p.WithLex(prefix, func(view p.View) p.Request {
			return competingRequest(view.Parts.Tokens("opener")[0].Text, view.State)
		}, body)))
	}
	spec := p.ChoiceSpec{Policy: policy}
	if policy != p.First {
		spec.Slot = "reading"
	}
	if filtered {
		spec.Filter = func(candidate p.Candidate) bool {
			opener := candidate.Parts.Tokens("opener")
			return candidate.Before == "initial" && candidate.After == "B" && len(opener) == 1 && opener[0].Text == "x"
		}
	}
	return ambiguityGrammar(p.Seq(p.Choose(spec, arms...), p.Make("tail", "tail", tagged("tail"))))
}

func competingPlan(branch p.State, completes bool) readingPlan {
	req := competingRequest("x", branch)
	plan := readingPlan{string(branch), []grammarUse{
		{"opener/token", "initial", p.Request{}, true},
		{"literal/peek", branch, req, false},
		{"literal/consume", branch, req, true},
	}}
	if completes {
		plan.uses = append(plan.uses,
			grammarUse{"continuation/tail", branch, p.Request{}, true},
			grammarUse{"file/final", branch, p.Request{}, false},
		)
	}
	return plan
}

func completedBranchOrder(record *requestRecorder) []p.Request {
	var order []p.Request
	for _, attempt := range record.attempts {
		req := attempt.offer.request
		if (req == requestA || req == requestB) && attempt.observation.read.Status == p.TokenReady &&
			!slices.Contains(order, req) {
			order = append(order, req)
		}
	}
	return order
}

func heldBranch(t *testing.T, record *requestRecorder, want p.Read) {
	t.Helper()
	var held *requestOffer
	polls := 0
	for _, attempt := range record.attempts {
		if attempt.offer.request.Goal != requestA.Goal || attempt.offer.request.Argument != requestA.Argument {
			continue
		}
		if attempt.observation.read.Status == p.NeedInput {
			if held == nil {
				first := attempt.offer
				held = &first
			}
			require.Equalf(t, *held, attempt.offer, "suspended branch changed request or complete mark: %+v vs %+v", attempt.offer, held)
			require.Equalf(t, requestA, attempt.offer.request, "suspended branch changed request or complete mark: %+v vs %+v", attempt.offer, held)
			if attempt.available == 2 {
				polls++
			}
			continue
		}
		got := attempt.observation.read
		// This is the eventual full event within the run. The graph
		// separately compares its complete successor up to the bijection.
		require.Truef(t, held != nil, "completed held observation = %+v, expected %+v after stable retries", attempt, want)
		require.Equalf(t, *held, attempt.offer, "completed held observation = %+v, expected %+v after stable retries", attempt, want)
		require.Equalf(t, want.Next.Byte, got.Next.Byte, "completed held observation = %+v, expected %+v after stable retries", attempt, want)
		require.Equalf(t, want.Next.Virtual, got.Next.Virtual, "completed held observation = %+v, expected %+v after stable retries", attempt, want)
		require.Equalf(t, want.Token, got.Token, "completed held observation = %+v, expected %+v after stable retries", attempt, want)
		require.Equalf(t, want.Trailing, got.Trailing, "completed held observation = %+v, expected %+v after stable retries", attempt, want)
	}
	require.GreaterOrEqualf(t, polls, 6, "got %d held no-input attempts at byte 2, want at least 6", polls)
}

func TestComputedCompetingRequests(t *testing.T) {
	for _, backend := range []struct {
		name string
		open func(*testing.T, *adapterInput) p.Source
	}{{"handmade", competingHandmade}, {"bytescan", competingReplay}} {
		var allAB requestGraph
		for _, order := range [][]p.State{{"A", "B"}, {"B", "A"}} {
			for _, tc := range []struct {
				name   string
				policy p.Policy
				filter bool
			}{{"all", p.All, false}, {"all-filter", p.All, true}, {"longest", p.Longest, false},
				{"longest-filter", p.Longest, true}, {"first", p.First, false}} {
				t.Run(fmt.Sprintf("%s/%s/%s%s", backend.name, tc.name, order[0], order[1]), func(t *testing.T) {
					engine := configured(t, competingGrammar(order, tc.policy, tc.filter), builders(), p.Limits{})
					winner := order[0]
					if tc.filter {
						winner = "B"
					}
					shape := func(name p.State) string { return fmt.Sprintf("file(reading-%[1]s(state:%[1]s),tail)", name) }
					want := shape(winner)
					all := tc.policy == p.All && !tc.filter
					if all {
						want = "leafkit:ambiguity(" + shape(order[0]) + "," + shape(order[1]) + ")"
					}
					var plans []readingPlan
					for _, branch := range order {
						if tc.policy != p.First || branch == winner {
							plans = append(plans, competingPlan(branch, all || branch == winner))
						}
					}
					var baseline requestGraph
					var baseTree []record
					var baselineAttempts int
					for _, schedule := range []string{"whole", "byte-stepped", "polled"} {
						t.Run(schedule, func(t *testing.T) {
							root, log := runRequestTrace(t, engine, "xab", "initial", schedule, backend.open)
							checkStateShape(t, root, want)
							noStateDiagnostics(t, root)
							if all {
								checkStateShape(t, resolveComputedBranch(t, root, "B"), shape("B"))
							}
							graph := log.graph(t, plans...)
							if schedule == "whole" {
								baseline, baseTree, baselineAttempts = graph, records(root), len(log.attempts)
								if all && order[0] == "A" {
									allAB = graph
								} else if all {
									checkRequestGraphs(t, allAB, graph)
								}
							} else {
								checkRequestGraphs(t, baseline, graph)
								equalRecords(t, root, baseTree)
							}
							if all && order[0] == "A" {
								expected := []p.Request{requestB, requestA}
								if schedule == "whole" {
									expected = []p.Request{requestA, requestB}
								}
								{
									got := completedBranchOrder(log)
									require.Truef(t, slices.Equal(got, expected), "allocation order = %+v, want %+v", got, expected)
								}
								if schedule == "polled" {
									heldBranch(t, log, baseline.observations[baseline.readings[0].uses[1].offer].read)
									require.Greaterf(t, len(log.attempts), baselineAttempts, "polling did not create additional raw attempts")
									checkCheckpointTraceSensitivity(t, graph)
								}
							}
						})
					}
				})
			}
		}
	}
}

func resolveComputedBranch(t *testing.T, root tree.Node, state p.State) tree.Node {
	t.Helper()
	for index := 0; ; index++ {
		alternative, ok := root.Child(index)
		require.Truef(t, ok, "no retained reading in state %q", state)
		for node := range tree.Walk(alternative) {
			if node.Info().Kind == tree.Kind("state:"+state) {
				return alternative
			}
		}
	}
}

func copyRequestGraph(graph requestGraph) requestGraph {
	copy := graph
	copy.observations = maps.Clone(graph.observations)
	copy.readings = slices.Clone(graph.readings)
	for i := range copy.readings {
		copy.readings[i].uses = slices.Clone(copy.readings[i].uses)
	}
	return copy
}

func renameTraceMarks(graph requestGraph, rename func(string) string) requestGraph {
	copy := copyRequestGraph(graph)
	copy.begin.Key = rename(copy.begin.Key)
	copy.observations = make(map[requestOffer]requestObservation)
	for key, event := range graph.observations {
		key.mark.Key = rename(key.mark.Key)
		event.read.Next.Key = rename(event.read.Next.Key)
		copy.observations[key] = event
	}
	for _, reading := range copy.readings {
		for i := range reading.uses {
			use := &reading.uses[i]
			use.offer.mark.Key = rename(use.offer.mark.Key)
		}
	}
	return copy
}

func checkCheckpointTraceSensitivity(t *testing.T, graph requestGraph) {
	t.Helper()
	renamed := renameTraceMarks(graph, func(key string) string { return "renamed:" + key })
	checkRequestGraphs(t, graph, renamed)
	aTail, bTail := graph.readings[0].uses[3].offer, graph.readings[1].uses[3].offer
	require.Equalf(t, bTail.mark.Byte, aTail.mark.Byte, "fixture did not create distinct same-offset checkpoints")
	require.NotEqualf(t, bTail.mark.Key, aTail.mark.Key, "fixture did not create distinct same-offset checkpoints")
	for _, tc := range []struct {
		name   string
		change func(*requestGraph)
	}{
		{"redirect use", func(copy *requestGraph) { copy.readings[0].uses[3].offer = bTail }},
		{"change creating provenance", func(copy *requestGraph) {
			key := copy.readings[0].uses[1].offer
			event := copy.observations[key]
			event.read.Next = bTail.mark
			copy.observations[key] = event
		}},
		{"conflate checkpoints", func(copy *requestGraph) {
			*copy = renameTraceMarks(*copy, func(key string) string {
				if key == aTail.mark.Key {
					return bTail.mark.Key
				}
				return key
			})
		}},
		{"change delimiter", func(copy *requestGraph) { copy.readings[0].uses[1].offer.request.Argument = "wrong" }},
		{"change correlated state", func(copy *requestGraph) { copy.readings[0].uses[1].state = "wrong" }},
	} {
		altered := copyRequestGraph(graph)
		tc.change(&altered)
		assert.Truef(t, compareRequestGraphs(graph, altered) != nil, "comparator accepted %s", tc.name)
	}
}

func TestComputedRequestPollingWorkBudget(t *testing.T) {
	const input = "xab"
	grammar := competingGrammar([]p.State{"A", "B"}, p.All, false)
	// Find the first budget at which the whole-input fixture succeeds.
	budget := 1
	for {
		engine := configured(t, grammar, builders(), p.Limits{Steps: budget})
		feed := &adapterInput{text: input, available: len(input), sealed: true}
		root, status, err := engine.Start(competingHandmade(t, feed), "initial").Run()
		if err == nil && status == p.Done && root != nil {
			break
		}
		require.ErrorIsf(t, err, p.ErrLimit, "unexpected work-bound result at %d: %v", budget, err)
		require.NotEqualf(t, 1000, budget, "unexpected work-bound result at %d: %v", budget, err)
		budget++
	}
	engine := configured(t, grammar, builders(), p.Limits{Steps: budget})
	whole, rawWhole := runRequestTrace(t, engine, input, "initial", "whole", competingHandmade)
	polled, rawPolled := runRequestTrace(t, engine, input, "initial", "polled", competingHandmade)
	equalRecords(t, polled, records(whole))
	checkRequestGraphs(t, rawWhole.graph(t, competingPlan("A", true), competingPlan("B", true)),
		rawPolled.graph(t, competingPlan("A", true), competingPlan("B", true)))
}

func TestComputedRequestRepairOrder(t *testing.T) {
	for _, policy := range []p.Policy{p.First, p.Longest, p.All} {
		for _, order := range [][]p.State{{"A", "B"}, {"B", "A"}} {
			t.Run(fmt.Sprintf("%d/%s%s", policy, order[0], order[1]), func(t *testing.T) {
				var alternatives []p.Rule
				var plans []readingPlan
				for _, branch := range order {
					prefix := p.Seq(p.Bind("opener", tagged("head")), withState(branch))
					alternatives = append(alternatives, p.Make("choice", tree.Kind(branch), p.WithLex(prefix, func(view p.View) p.Request {
						return competingRequest(view.Parts.Tokens("opener")[0].Text, view.State)
					}, tagged("expected-"+string(branch)))))
					plans = append(plans, readingPlan{string(branch), []grammarUse{
						{"opener/token", "initial", p.Request{}, true},
						{"literal/rejected", branch, competingRequest("x", branch), false},
					}})
				}
				spec := p.ChoiceSpec{Policy: policy, Slot: "choice"}
				if policy == p.First {
					spec.Slot, spec.Lookahead = "", 2
				}
				file := p.Seq(
					p.Recover("choice", p.Choose(spec, alternatives...), tokenBoundaries(false, "tail")),
					p.Make("suffix", "suffix", p.Seq(stateIs("initial"), tagged("tail"))),
					stateCheckpoint("initial"),
				)
				plans[0].uses = append(plans[0].uses,
					grammarUse{"repair/skip", "initial", p.Request{}, true},
					grammarUse{"repair/leave-boundary", "initial", p.Request{}, false},
					grammarUse{"suffix/consume", "initial", p.Request{}, true},
					grammarUse{"file/final", "initial", p.Request{}, false},
				)
				engine := configured(t, ambiguityGrammar(file), builders(), p.Limits{})
				var reference requestGraph
				var wantRecords []record
				for _, schedule := range []string{"whole", "byte-stepped", "polled"} {
					t.Run(schedule, func(t *testing.T) {
						root, log := runRequestTrace(t, engine, "xab", "initial", schedule, competingReplay)
						checkStateShape(t, root, "file(leafkit:error,suffix,state:initial)")
						found := fixtureToken("data", "a", 1, 2)
						want := []stateDiagnostic{{"choice", p.Problem{
							Span: span(0, 2), Expected: []p.Pattern{p.Kind(p.TokenKind("expected-" + order[0]))},
							Found:   p.Look{Token: found, Point: span(1, 1)},
							Skipped: []p.Token{fixtureToken("head", "x", 0, 1), fixtureToken("skipped", "a", 1, 2)},
						}}}
						{
							got := stateDiagnostics(t, root)
							require.Equalf(t, want, got, "diagnostics = %+v, want %+v", got, want)
						}
						graph := log.graph(t, plans...)
						if schedule == "whole" {
							reference, wantRecords = graph, records(root)
						} else {
							checkRequestGraphs(t, reference, graph)
							equalRecords(t, root, wantRecords)
						}
					})
				}
			})
		}
	}
}

func TestComputedRequestAbandonedBranch(t *testing.T) {
	for _, policy := range []p.Policy{p.First, p.Longest, p.All} {
		for _, order := range [][]p.State{{"A", "B"}, {"B", "A"}} {
			t.Run(fmt.Sprintf("%d/%s%s", policy, order[0], order[1]), func(t *testing.T) {
				var arms []p.Rule
				for _, branch := range order {
					head := tagged("head")
					if branch == "A" {
						head = p.Make("abandoned", "abandoned", head)
					}
					prefix := p.Seq(stateIs("initial"), p.Bind("open", head), withState(branch))
					body := p.Seq(tagged("data"), stateCheckpoint("B"))
					if branch == "A" {
						body = tagged("unmatched")
					}
					arms = append(arms, p.Make("reading", tree.Kind("reading-"+branch), p.WithLex(prefix, func(view p.View) p.Request {
						return competingRequest(view.Parts.Tokens("open")[0].Text, view.State)
					}, body)))
				}
				spec := p.ChoiceSpec{Policy: policy, Slot: "reading"}
				if policy == p.First {
					spec.Slot, spec.Lookahead = "", 2
				}
				rule := p.Seq(p.Choose(spec, arms...), p.Make("tail", "tail", p.Seq(stateIs("B"), tagged("tail"))))
				build := builders()
				node := build.Node
				build.Node = func(in p.Build[tree.Node]) (tree.Node, error) {
					if in.Kind == "abandoned" {
						return nil, fmt.Errorf("constructed an abandoned request branch")
					}
					return node(in)
				}
				engine := configured(t, ambiguityGrammar(rule), build, p.Limits{})
				var baseline requestGraph
				var baselineTree []record
				plans := []readingPlan{{"B", []grammarUse{
					{"header", "initial", p.Request{}, true},
					{"body", "B", requestB, true},
					{"tail", "B", p.Request{}, true},
					{"file/final", "B", p.Request{}, false},
				}}}
				if policy != p.First || order[0] == "A" {
					plans = append(plans, readingPlan{"A", []grammarUse{
						{"header", "initial", p.Request{}, true},
						{"body/rejected", "A", requestA, false},
					}})
				}
				for _, schedule := range []string{"whole", "byte-stepped", "polled"} {
					t.Run(schedule, func(t *testing.T) {
						root, log := runRequestTrace(t, engine, "xab", "initial", schedule, competingReplay)
						checkStateShape(t, root, "file(reading-B(state:B),tail)")
						noStateDiagnostics(t, root)
						graph := log.graph(t, plans...)
						if schedule == "whole" {
							baseline, baselineTree = graph, records(root)
						} else {
							checkRequestGraphs(t, baseline, graph)
							equalRecords(t, root, baselineTree)
						}
					})
				}
			})
		}
	}
}

func TestComputedRequestObservesSettledPrefix(t *testing.T) {
	for _, equivalent := range []bool{false, true} {
		t.Run(fmt.Sprintf("equivalent=%v", equivalent), func(t *testing.T) {
			var arms []p.Rule
			for _, branch := range []p.State{"A", "B"} {
				exit := branch
				if equivalent {
					exit = "B"
				}
				arms = append(arms, p.Make("prefix", tree.Kind("prefix-"+branch), p.Seq(tagged("head"), withState(exit))))
			}
			prefix := p.Bind("open", p.Choose(p.ChoiceSpec{Policy: p.All, Slot: "prefix"}, arms...))
			body := p.Seq(p.At(p.Kind("data")), tagged("data"))
			scope := p.WithLex(prefix, func(view p.View) p.Request {
				opener := view.Parts.Tokens("open")
				kinds := view.Parts.Kinds("open")
				want := tree.Kind("prefix-" + view.State)
				if equivalent {
					want = tree.KindAmbiguity
				}
				if len(opener) != 1 || len(kinds) != 1 || kinds[0] != want {
					return p.Request{Goal: "unexpected-prefix-summary"}
				}
				return competingRequest(opener[0].Text, view.State)
			}, body)
			suffix := p.Choose(p.ChoiceSpec{}, p.Seq(stateCheckpoint("A"), p.Make("tail", "tail", tagged("tail"))),
				p.Seq(stateCheckpoint("B"), p.Make("tail", "tail", tagged("tail"))))
			engine := configured(t, ambiguityGrammar(p.Seq(scope, suffix)), builders(), p.Limits{})
			want := "leafkit:ambiguity(file(prefix-A,state:A,tail),file(prefix-B,state:B,tail))"
			plans := []readingPlan{competingPlan("A", true), competingPlan("B", true)}
			if equivalent {
				want = "file(leafkit:ambiguity(prefix-A,prefix-B),state:B,tail)"
				plans = plans[1:]
			}
			var baseline requestGraph
			var baseTree []record
			for _, schedule := range []string{"whole", "byte-stepped", "polled"} {
				t.Run(schedule, func(t *testing.T) {
					root, log := runRequestTrace(t, engine, "xab", "initial", schedule, competingReplay)
					checkStateShape(t, root, want)
					noStateDiagnostics(t, root)
					graph := log.graph(t, plans...)
					if schedule == "whole" {
						baseline, baseTree = graph, records(root)
					} else {
						checkRequestGraphs(t, baseline, graph)
						equalRecords(t, root, baseTree)
					}
				})
			}
		})
	}
}
