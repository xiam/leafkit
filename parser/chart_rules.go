package parser

import (
	"slices"

	"github.com/xiam/leafkit/tree"
)

func (c *chart[N]) open(cell *chartCell) {
	engine := c.parser.engine
	rule := engine.sites[cell.key.site]
	if !engine.generalized[cell.key.site] {
		c.runIsland(cell)
		return
	}
	switch rule.kind {
	case sequenceRule:
		c.sequence(cell, rule, 0, cell.entry())
	case refRule, lexRule, bindRule, dropRule, makeRule, scopeRule:
		c.wrapper(cell, rule)
	case updateRule, guardRule:
		c.stateRule(cell, rule)
	case withLexRule:
		c.withLex(cell, rule)
	case choiceRule:
		switch rule.choice.Policy {
		case First:
			c.first(cell, rule, 0, nil, nil)
		case Longest:
			c.longest(cell, rule)
		case All:
			c.all(cell, rule)
		}
	case optionalRule:
		child := c.follow(cell, childKey(cell, rule.children[0], cell.key.point), func(value chartResult) {
			c.emit(cell, value)
		})
		c.await(cell, []*chartCell{child}, func() {
			if len(child.results) == 0 {
				committed := false
				for _, miss := range c.settledMisses(child) {
					if miss.commit {
						committed = true
						c.reject(cell, miss)
					}
				}
				if !committed {
					c.emit(cell, cell.entry())
				}
			}
		})
	case manyRule, separatedRule:
		c.repetition(cell, rule, firstItem, cell.entry())
	case recoverRule:
		c.recover(cell, rule)
	case programRule:
		c.program(cell, rule, rule.name, cell.entry(), 0)
	case expressionRule:
		c.expressionStart(cell, rule, 0)
	default:
		c.err = grammarError("invalid chart rule %d", rule.kind)
	}
}

func chartProductMiss(entry chartPoint, prefix chartResult, miss chartMiss) chartMiss {
	order := chartOrder(prefix.order, miss.order, prefix.end.mark)
	miss.chartResult = chartAppend(prefix, miss.chartResult)
	miss.order = order
	miss.commit = miss.commit || prefix.end != entry || prefix.cost != 0
	return miss
}

func (c *chart[N]) sequence(cell *chartCell, rule site, next int, prefix chartResult) {
	if next == len(rule.children) {
		c.emit(cell, prefix)
		return
	}
	child := c.follow(cell, resumeKey(cell, rule.children[next], prefix), func(value chartResult) {
		c.extend(cell, prefix, value, func(result chartResult) {
			result.order = chartOrder(prefix.order, value.order, prefix.end.mark)
			c.sequence(cell, rule, next+1, result)
		})
	})
	c.onMiss(cell, child, func(miss chartMiss) chartMiss {
		return chartProductMiss(cell.key.point, prefix, miss)
	})
}

func (c *chart[N]) wrapper(cell *chartCell, rule site) {
	key := cell.key
	if rule.kind == refRule {
		key.site = c.parser.engine.named[rule.name]
	} else {
		key.site = rule.children[0]
	}
	if rule.kind == lexRule {
		key.request = rule.request
	}
	child := c.follow(cell, key, func(value chartResult) {
		switch rule.kind {
		case bindRule:
			value.out = chartCapture(rule.name, value)
		case makeRule:
			span := c.span(cell.key.point, value.end)
			node := &chartNode{slot: rule.name, kind: rule.nodeKind, span: span, body: value.out, hasError: value.cost != 0}
			value.out = chartNodeOutput(node, value.tape)
		case dropRule:
			value.out = dropChartOutput(value, cell.key.point.mark.Byte)
		case scopeRule:
			value.state = cell.key.state
		}
		c.emit(cell, value)
	})
	c.onMiss(cell, child, func(miss chartMiss) chartMiss {
		switch rule.kind {
		case bindRule, makeRule, dropRule:
			miss.out = nil
		}
		return miss
	})
}

func (c *chart[N]) stateRule(cell *chartCell, rule site) {
	child := c.call(childKey(cell, rule.children[0], cell.key.point))
	c.onMiss(cell, child, func(miss chartMiss) chartMiss { return miss })
	// A callback may inspect a packed child's local parts. Wait until
	// they settle, just as a program does after a named call.
	c.await(cell, []*chartCell{child}, func() {
		for _, value := range stableChartResults(child.results) {
			view := View{State: value.state, Parts: chartParts(value.out)}
			switch {
			case rule.kind == updateRule:
				value.state = rule.update(view)
			case !rule.guard(view):
				value.out = nil
				c.offer(cell, value.end, cell.key.request, func(read Read) {
					c.reject(cell, &chartMiss{
						chartResult: value, request: cell.key.request, found: c.parser.look(read),
						commit: value.end != cell.key.point || value.cost != 0,
					})
				})
				continue
			}
			c.emit(cell, value)
		}
	})
}

func (c *chart[N]) withLex(cell *chartCell, rule site) {
	prefix := c.call(childKey(cell, rule.children[0], cell.key.point))
	c.onMiss(cell, prefix, func(miss chartMiss) chartMiss { return miss })
	c.await(cell, []*chartCell{prefix}, func() {
		for _, reading := range stableChartResults(prefix.results) {
			view := View{State: reading.state, Parts: chartParts(reading.out)}
			key := resumeKey(cell, rule.children[1], reading)
			key.request = rule.compute(view)
			if err := checkComputedRequest(key.request); err != nil {
				c.err = err
				return
			}
			body := c.follow(cell, key, func(exit chartResult) {
				c.extend(cell, reading, exit, func(result chartResult) {
					result.order = chartOrder(reading.order, exit.order, reading.end.mark)
					c.emit(cell, result)
				})
			})
			c.onMiss(cell, body, func(miss chartMiss) chartMiss {
				return chartProductMiss(cell.key.point, reading, miss)
			})
			if c.err != nil {
				return
			}
		}
	})
}

func (c *chart[N]) span(from, to chartPoint) tree.Span {
	return tree.Span{Source: c.parser.sourceID, Start: from.mark.Byte, End: to.mark.Byte}
}

type chartPackKey struct {
	takes      int
	end        chartPoint
	state      State
	cost       int
	first      chartPoint
	firstTakes int
	repairs    *chartRepairLog
}

func (c *chart[N]) candidate(cell *chartCell, rule site, value chartResult) (*chartNode, bool) {
	node, ok := chartRoot(value.out)
	if rule.choice.Slot == "" {
		return node, true
	}
	if !ok || node.slot != rule.choice.Slot || node.span != c.span(cell.key.point, value.end) {
		c.err = grammarError("choice slot %q requires one full-span root of the same slot", rule.choice.Slot)
		return nil, false
	}
	if rule.choice.Filter == nil {
		return node, true
	}
	if rule.choice.Filter(Candidate{
		Kind: node.kind, Span: node.span, Parts: chartParts(node.body), Before: cell.key.state, After: value.state,
	}) {
		return node, true
	}
	miss := (chartMiss{chartResult: value, request: cell.key.request, needOffer: true}).rewind()
	c.reject(cell, &miss)
	return nil, false
}

// A filter can inspect each root alternative in a local pack. A packed
// descendant remains a shared child and is observed only through Parts.
func chartCandidates(value chartResult, expand bool, visit func(chartResult)) {
	root, ok := chartRoot(value.out)
	if !expand || !ok || root.arms == nil {
		visit(value)
		return
	}
	arms := slices.Clone(root.arms)
	sortChartAlternatives(arms)
	for _, arm := range arms {
		reading := value
		reading.out, reading.tape = chartNodeOutput(arm.node, arm.tape), arm.tape
		reading.order = append([]int{arm.arm}, arm.order...)
		chartCandidates(reading, true, visit)
	}
}

func (c *chart[N]) all(cell *chartCell, rule site) {
	packs := make(map[chartPackKey]*chartNode)
	add := func(arm int, value chartResult) {
		node, keep := c.candidate(cell, rule, value)
		if !keep {
			return
		}
		key := chartPackKey{
			end: value.end, state: value.state, cost: value.cost, first: value.first, firstTakes: value.firstTakes, repairs: value.repairs,
		}
		if cell.key.observeTakes {
			key.takes = value.takes
		}
		pack := packs[key]
		if !c.edge() {
			return
		}
		alternative := chartAlternative{arm: arm, order: value.order, node: node, tape: value.tape}
		if pack != nil {
			pack.arms = append(pack.arms, alternative)
			return
		}
		pack = &chartNode{
			kind: tree.KindAmbiguity, slot: rule.choice.Slot, span: node.span,
			arms: []chartAlternative{alternative}, hasError: value.cost != 0,
		}
		packs[key] = pack
		value.tape = &chartTape{pack: pack}
		value.out = chartNodeOutput(pack, value.tape)
		value.order = nil
		c.emit(cell, value)
	}
	for arm, site := range rule.children {
		key := childKey(cell, site, cell.key.point)
		var child *chartCell
		if rule.choice.Filter == nil {
			child = c.follow(cell, key, func(value chartResult) { add(arm, value) })
		} else {
			child = c.call(key)
			c.await(cell, []*chartCell{child}, func() {
				for _, value := range child.results {
					chartCandidates(value, true, func(reading chartResult) { add(arm, reading) })
				}
			})
		}
		c.onMiss(cell, child, func(miss chartMiss) chartMiss {
			miss = miss.rewind()
			miss.order = append([]int{arm}, miss.order...)
			return miss
		})
	}
}

func stableChartResults(results []chartResult) []chartResult {
	ordered := append([]chartResult(nil), results...)
	slices.SortStableFunc(ordered, compareChartResults)
	return ordered
}

func compareChartResults(a, b chartResult) int {
	if order := slices.Compare(a.order, b.order); order != 0 {
		return order
	}
	if a.out == b.out {
		return 0
	}
	return slices.Compare(chartRank(nil, a.out), chartRank(nil, b.out))
}

func (c *chart[N]) longest(cell *chartCell, rule site) {
	arms := make([]*chartCell, len(rule.children))
	for i, id := range rule.children {
		arms[i] = c.call(childKey(cell, id, cell.key.point))
	}
	c.await(cell, arms, func() {
		var best chartResult
		found := false
		for index, arm := range arms {
			for _, failure := range c.settledMisses(arm) {
				miss := failure.rewind()
				miss.order = append([]int{index}, miss.order...)
				c.reject(cell, &miss)
			}
			for _, value := range stableChartResults(arm.results) {
				chartCandidates(value, rule.choice.Filter != nil, func(reading chartResult) {
					if _, keep := c.candidate(cell, rule, reading); !keep {
						return
					}
					if !found || chartBetterRepair(reading, best) ||
						!chartBetterRepair(best, reading) && reading.end.mark.Byte > best.end.mark.Byte {
						best, found = reading, true
					}
				})
			}
		}
		if found {
			c.emit(cell, selectChartReading(best))
		}
	})
}

func (c *chart[N]) first(cell *chartCell, rule site, index int, fallback []chartResult, rejected []*chartMiss) {
	key := childKey(cell, rule.children[index], cell.key.point)
	key.observeTakes = true
	child := c.call(key)
	c.await(cell, []*chartCell{child}, func() {
		budget := max(1, rule.choice.Lookahead)
		selected := false
		for _, value := range child.results {
			if value.cost == 0 || value.firstTakes >= budget {
				c.emit(cell, value)
				selected = true
			}
		}
		if selected {
			return
		}
		committed := false
		var trial []*chartMiss
		for _, failure := range c.settledMisses(child) {
			miss := *failure
			if miss.budgetTakes() >= budget {
				miss.commit = true
				c.reject(cell, &miss)
				committed = true
			} else {
				miss = miss.rewind()
				trial = append(trial, &miss)
			}
		}
		if committed {
			return
		}
		rejected = preferFirstTrial(rejected, trial)
		if fallback == nil && len(child.results) != 0 {
			fallback = child.results
		}
		if index+1 < len(rule.children) {
			c.first(cell, rule, index+1, fallback, rejected)
			return
		}
		if len(fallback) == 0 {
			for _, miss := range rejected {
				c.reject(cell, miss)
			}
		}
		for _, value := range fallback {
			c.emit(cell, value)
		}
	})
}

func (c *chart[N]) repetition(cell *chartCell, rule site, phase int, prefix chartResult) {
	site := rule.children[0]
	if phase == separator {
		site = rule.children[1]
	}
	child := c.call(resumeKey(cell, site, prefix))
	c.await(cell, []*chartCell{child}, func() {
		if len(child.results) == 0 {
			rejected := false
			for _, miss := range c.settledMisses(child) {
				if miss.commit || phase == laterItem && !rule.trailing {
					rejected = true
					product := chartProductMiss(cell.key.point, prefix, *miss)
					c.reject(cell, &product)
				}
			}
			if !rejected && (phase != laterItem || rule.trailing) {
				c.emit(cell, prefix)
			}
			return
		}
		for _, value := range child.results {
			c.extend(cell, prefix, value, func(result chartResult) {
				if value.end == prefix.end {
					if value.cost == 0 {
						c.err = grammarError("repetition succeeded without consuming a token")
						return
					}
					if rule.kind == manyRule || phase == separator {
						c.emit(cell, result)
						return
					}
				}
				next := firstItem
				if rule.kind == separatedRule {
					next = separator
					if phase == separator {
						next = laterItem
					}
				}
				c.repetition(cell, rule, next, result)
			})
		}
	})
}
