package parser

import "slices"

// Failed prefixes can share a continuation only at the same complete
// source offer. Keep commitment information visible to enclosing rules.
type chartMissKey struct {
	chartReadKey
	state State
	shape chartMissShape
}

type chartMissShape struct {
	takes, budget    int
	repaired, commit bool
}

func (m chartMiss) budgetTakes() int {
	if m.cost != 0 {
		return m.firstTakes
	}
	return m.takes
}

// An abandoned trial can supply a diagnostic at its furthest offer. Its
// recipes, prior repairs and consumed-token budget belong to the trial.
func (m chartMiss) rewind() chartMiss {
	m.chartResult = chartResult{end: m.end, state: m.state, tape: m.tape, order: m.order}
	m.commit = false
	return m
}

func missKey(cell *chartCell, miss chartMiss) chartMissKey {
	key := chartMissKey{
		chartReadKey: chartReadKey{miss.end, miss.request},
		state:        miss.state,
		shape:        chartMissShape{repaired: miss.cost != 0, commit: miss.commit},
	}
	if cell.key.observeTakes {
		key.shape.takes, key.shape.budget = miss.takes, miss.budgetTakes()
	}
	return key
}

// Each continuation receives a failure set once. Later derivations extend
// the set in place, like a successful pack. Resolve the derivation and
// diagnostic order only after the observing rule's dependencies settle.
type chartMissSet struct {
	entry  chartPoint
	sample chartMiss
	edges  []chartMissEdge
}

type chartMissEdge struct {
	literal *chartMiss
	child   *chartMissSet
	project func(chartMiss) chartMiss
}

func (c *chart[N]) reject(cell *chartCell, miss *chartMiss) {
	if miss != nil {
		c.addMiss(cell, *miss, chartMissEdge{literal: miss})
	}
}

func (c *chart[N]) onMiss(parent, child *chartCell, project func(chartMiss) chartMiss) {
	forward := func(set *chartMissSet) {
		c.addMiss(parent, project(set.sample), chartMissEdge{child: set, project: project})
	}
	child.failures = append(child.failures, forward)
	for _, set := range child.misses {
		c.enqueue(func() { forward(set) })
	}
}

func (c *chart[N]) addMiss(cell *chartCell, sample chartMiss, edge chartMissEdge) {
	if !c.edge() {
		return
	}
	if cell.misses == nil {
		cell.misses = make(map[chartMissKey]*chartMissSet)
	}
	key := missKey(cell, sample)
	if set := cell.misses[key]; set != nil {
		set.edges = append(set.edges, edge)
		return
	}
	set := &chartMissSet{entry: cell.key.point, sample: sample, edges: []chartMissEdge{edge}}
	cell.misses[key] = set
	for _, listener := range cell.failures {
		c.enqueue(func() { listener(set) })
	}
}

func (c *chart[N]) settledMisses(cell *chartCell) []*chartMiss {
	if cell.missesSettled {
		return cell.settledMisses
	}
	if len(cell.misses) == 0 {
		return nil
	}
	resolution := chartMissResolution{
		memo:    make(map[*chartMissSet]chartResolvedMiss),
		active:  make(map[*chartMissSet]bool),
		entries: make(map[chartPoint]int),
	}
	furthest := make(map[chartMissShape]Mark)
	for key := range cell.misses {
		mark := key.point.mark
		if old, ok := furthest[key.shape]; !ok || after(mark, old) {
			furthest[key.shape] = mark
		}
	}
	misses := make([]*chartMiss, 0, len(cell.misses))
	for key, set := range cell.misses {
		if after(furthest[key.shape], key.point.mark) {
			continue
		}
		miss, _ := c.resolveMiss(set, &resolution)
		if c.err != nil {
			return nil
		}
		if miss != nil {
			misses = append(misses, miss)
		}
	}
	slices.SortStableFunc(misses, compareChartMisses)
	if c.err == nil {
		cell.settledMisses, cell.missesSettled = misses, true
	}
	return misses
}

type chartMissResolution struct {
	memo    map[*chartMissSet]chartResolvedMiss
	active  map[*chartMissSet]bool
	entries map[chartPoint]int
}

type chartResolvedMiss struct {
	miss   *chartMiss
	cyclic bool
}

func (c *chart[N]) resolveMiss(set *chartMissSet, resolution *chartMissResolution) (*chartMiss, bool) {
	sharedEntry := resolution.entries[set.entry] != 0
	if cached, ok := resolution.memo[set]; ok && (!cached.cyclic || !sharedEntry) {
		return cached.miss, false
	}
	if resolution.active[set] {
		// A cyclic route supplies no new failure. Another edge must reach
		// a concrete failed offer for the bucket to exist.
		return nil, true
	}
	resolution.active[set] = true
	resolution.entries[set.entry]++
	defer func() {
		delete(resolution.active, set)
		resolution.entries[set.entry]--
	}()

	var choices []*chartMiss
	cyclic := false
	for _, edge := range set.edges {
		if !c.charge() {
			return nil, cyclic
		}
		if edge.literal != nil {
			choices = append(choices, edge.literal)
			continue
		}
		child, loop := c.resolveMiss(edge.child, resolution)
		cyclic = cyclic || loop
		if child != nil {
			projected := edge.project(*child)
			choices = append(choices, &projected)
		}
	}
	slices.SortStableFunc(choices, compareChartMisses)
	var best *chartMiss
	for _, choice := range choices {
		best = bestChartMiss(best, choice)
	}
	// A consuming edge cannot return to an earlier entry point. Resolve
	// same-point cycles in their local DFS and share the result as soon
	// as it no longer depends on a caller at that point.
	if !cyclic || !sharedEntry {
		resolution.memo[set] = chartResolvedMiss{miss: best, cyclic: cyclic}
		return best, false
	}
	return best, true
}

func compareChartMisses(a, b *chartMiss) int {
	if after(a.end.mark, b.end.mark) {
		return -1
	}
	if after(b.end.mark, a.end.mark) {
		return 1
	}
	if order := slices.Compare(a.order, b.order); order != 0 {
		return order
	}
	return slices.Compare(a.tape.sourceOrder(nil), b.tape.sourceOrder(nil))
}

// First reports the furthest failed trial, preferring the earlier arm at a
// tie. That trial may itself have distinct lexical continuations at the
// furthest point. Later trials only contribute compatible expectations.
func preferFirstTrial(best, trial []*chartMiss) []*chartMiss {
	if len(trial) == 0 || len(best) != 0 && after(best[0].end.mark, trial[0].end.mark) {
		return best
	}
	furthest := trial[0].end.mark
	replace := len(best) == 0 || after(furthest, best[0].end.mark)
	if replace {
		best = nil
	}
	for _, miss := range trial {
		if after(furthest, miss.end.mark) {
			break
		}
		index := slices.IndexFunc(best, func(previous *chartMiss) bool {
			return previous.end == miss.end && previous.request == miss.request && previous.state == miss.state
		})
		if index >= 0 {
			best[index] = bestChartMiss(best[index], miss)
		} else if replace {
			best = append(best, miss)
		}
	}
	return best
}

func bestChartMiss(best, next *chartMiss) *chartMiss {
	if best == nil || after(next.end.mark, best.end.mark) {
		copy := *next
		return &copy
	}
	if best.end != next.end || best.request != next.request || best.state != next.state {
		return best
	}
	selected := *best
	changed := false
	if next.commit && !selected.commit {
		selected.commit, changed = true, true
	}
	if selected.needOffer && !next.needOffer {
		selected.found, selected.needOffer, changed = next.found, false, true
	}
	for _, expected := range next.expected {
		if !slices.Contains(selected.expected, expected) {
			selected.expected = append(slices.Clip(selected.expected), expected)
			changed = true
		}
	}
	if !changed {
		return best
	}
	return &selected
}
