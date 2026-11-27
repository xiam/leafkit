package tests

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

// Source observations belong to a complete replay mark and request. Grammar
// uses belong to a named continuation in a reading, even on cache hits.
type requestOffer struct {
	mark    p.Mark
	request p.Request
}

type requestObservation struct {
	read p.Read
	err  string
}

type requestAttempt struct {
	offer       requestOffer
	observation requestObservation
	available   int
}

type requestRecorder struct {
	p.Source
	input        *adapterInput
	snapshot     tree.SourceID
	begin        p.Mark
	observations map[requestOffer]requestObservation
	attempts     []requestAttempt
	disagreement error
}

func (r *requestRecorder) Begin() (tree.SourceID, p.Mark) {
	r.snapshot, r.begin = r.Source.Begin()
	return r.snapshot, r.begin
}

func (r *requestRecorder) Read(mark p.Mark, request p.Request) (p.Read, error) {
	read, err := r.Source.Read(mark, request)
	key := requestOffer{mark, request}
	event := requestObservation{read: read}
	if err != nil {
		event.err = err.Error()
	}
	r.attempts = append(r.attempts, requestAttempt{key, event, r.input.available})
	if err != nil || read.Status != p.NeedInput {
		if old, ok := r.observations[key]; ok && !reflect.DeepEqual(old, event) {
			r.disagreement = fmt.Errorf("offer %+v changed from %+v to %+v", key, old, event)
		}
		r.observations[key] = event
	}
	return read, err
}

// The fixture declares grammar-use paths independently of the source's order
// of calls. Each use is checked against an actual stable observation and the
// previous use's checkpoint. A cache hit still gets its own edge.
type grammarUse struct {
	site    string
	state   p.State
	request p.Request
	advance bool
}

type readingPlan struct {
	name string
	uses []grammarUse
}

type logicalUse struct {
	site, predecessor string
	state             p.State
	offer             requestOffer
	advance           bool
}

type logicalReading struct {
	name string
	uses []logicalUse
}

type requestGraph struct {
	role, input  string
	snapshot     tree.SourceID
	begin        p.Mark
	readings     []logicalReading
	observations map[requestOffer]requestObservation
}

func (r *requestRecorder) graph(t *testing.T, plans ...readingPlan) requestGraph {
	t.Helper()
	require.NoErrorf(t, r.disagreement, "%v", r.disagreement)
	graph := requestGraph{"file", r.input.text, r.snapshot, r.begin, nil, r.observations}
	used := make(map[requestOffer]bool)
	for _, plan := range plans {
		reading := logicalReading{name: plan.name}
		mark, predecessor := r.begin, ""
		for _, use := range plan.uses {
			key := requestOffer{mark, use.request}
			event, ok := r.observations[key]
			require.Truef(t, ok, "%s/%s lacks source observation at %+v. Attempts: %+v", plan.name, use.site, key, r.attempts)
			reading.uses = append(reading.uses, logicalUse{use.site, predecessor, use.state, key, use.advance})
			used[key] = true
			predecessor = use.site
			if use.advance {
				require.Equalf(t, "", event.err, "%s/%s cannot advance over %+v", plan.name, use.site, event)
				require.Equalf(t, p.TokenReady, event.read.Status, "%s/%s cannot advance over %+v", plan.name, use.site, event)
				mark = event.read.Next
			}
		}
		graph.readings = append(graph.readings, reading)
	}
	for key := range r.observations {
		require.Truef(t, used[key], "completed observation omitted from grammar graph: %+v", key)
	}
	sort.Slice(graph.readings, func(i, j int) bool { return graph.readings[i].name < graph.readings[j].name })
	return graph
}

type checkpointBijection struct {
	forward, backward map[string]string
}

func (b checkpointBijection) match(a, z p.Mark) bool {
	if a.Byte != z.Byte || a.Virtual != z.Virtual {
		return false
	}
	if key, ok := b.forward[a.Key]; ok {
		return key == z.Key
	}
	if _, ok := b.backward[z.Key]; ok {
		return false
	}
	b.forward[a.Key], b.backward[z.Key] = z.Key, a.Key
	return true
}

func compareRequestGraphs(a, b requestGraph) error {
	if a.role != b.role || a.input != b.input || a.snapshot != b.snapshot ||
		len(a.readings) != len(b.readings) || len(a.observations) != len(b.observations) {
		return fmt.Errorf("different source/snapshot, reading count, or observation count")
	}
	marks := checkpointBijection{make(map[string]string), make(map[string]string)}
	if !marks.match(a.begin, b.begin) {
		return fmt.Errorf("initial checkpoint differs")
	}
	for i, first := range a.readings {
		second := b.readings[i]
		if first.name != second.name || len(first.uses) != len(second.uses) {
			return fmt.Errorf("different reading %q", first.name)
		}
		for j, left := range first.uses {
			right := second.uses[j]
			if left.site != right.site || left.predecessor != right.predecessor || left.state != right.state ||
				left.advance != right.advance || left.offer.request != right.offer.request ||
				!marks.match(left.offer.mark, right.offer.mark) {
				return fmt.Errorf("different grammar use at %s/%s: %+v vs %+v", first.name, left.site, left, right)
			}
			x, xOK := a.observations[left.offer]
			y, yOK := b.observations[right.offer]
			if !xOK || !yOK || !marks.match(x.read.Next, y.read.Next) {
				return fmt.Errorf("different creating observation at %s/%s", first.name, left.site)
			}
			x.read.Next, y.read.Next = p.Mark{}, p.Mark{}
			if !reflect.DeepEqual(x, y) {
				return fmt.Errorf("different completed event at %s/%s: %+v vs %+v", first.name, left.site, x, y)
			}
		}
	}
	return nil
}

func checkRequestGraphs(t *testing.T, want, got requestGraph) {
	t.Helper()
	require.NoError(t, compareRequestGraphs(want, got))
}

func runRequestTrace(t *testing.T, engine *p.Engine[tree.Node], input string, initial p.State, schedule string, open func(*testing.T, *adapterInput) p.Source) (tree.Node, *requestRecorder) {
	t.Helper()
	feed := &adapterInput{text: input}
	if schedule == "whole" {
		feed.available, feed.sealed = len(input), true
	}
	record := &requestRecorder{Source: open(t, feed), input: feed, observations: make(map[requestOffer]requestObservation)}
	session := engine.Start(record, initial)
	if !feed.sealed {
		for feed.available = 0; feed.available <= len(input); feed.available++ {
			paused(t, session)
			if schedule == "polled" {
				for count := 0; count < 5; count++ {
					paused(t, session)
				}
			}
		}
		feed.available, feed.sealed = len(input), true
	}
	return done(t, session), record
}
