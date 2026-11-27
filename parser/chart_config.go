package parser

func (c *compiler) generalizedSites() []bool {
	var pending []int
	for id, site := range c.sites {
		if site.kind == choiceRule && site.choice.Policy != First {
			pending = append(pending, id)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	callers := make([][]int, len(c.sites))
	for id, site := range c.sites {
		for _, child := range site.children {
			callers[child] = append(callers[child], id)
		}
		switch site.kind {
		case refRule:
			child := c.named[site.name]
			callers[child] = append(callers[child], id)
		case programRule:
			for _, state := range site.states {
				for _, name := range state.Calls {
					child := c.named[name]
					callers[child] = append(callers[child], id)
				}
			}
		}
	}
	chart := make([]bool, len(c.sites))
	for len(pending) != 0 {
		last := len(pending) - 1
		id := pending[last]
		pending = pending[:last]
		if !chart[id] {
			chart[id] = true
			pending = append(pending, callers[id]...)
		}
	}
	return chart
}

func (c *compiler) reachesAll(start int, chart []bool) bool {
	pending := []int{start}
	seen := make([]bool, len(c.sites))
	for len(pending) != 0 {
		last := len(pending) - 1
		id := pending[last]
		pending = pending[:last]
		if !chart[id] || seen[id] {
			continue
		}
		seen[id] = true
		rule := c.sites[id]
		switch rule.kind {
		case choiceRule:
			if rule.choice.Policy == All {
				return true
			}
		case refRule:
			pending = append(pending, c.named[rule.name])
		case programRule:
			for _, state := range rule.states {
				for _, name := range state.Calls {
					pending = append(pending, c.named[name])
				}
			}
		}
		pending = append(pending, rule.children...)
	}
	return false
}

func (c *compiler) checkChartCycles(left [][]int, nullable, generalized []bool) error {
	// Ordered parsing still forbids left recursion. Cutting the unfiltered
	// All edges leaves the part that would recurse without a packed seed.
	ordered := append([][]int(nil), left...)
	zero := make([][]int, len(c.sites))
	for id, site := range c.sites {
		if !generalized[id] {
			continue
		}
		switch {
		case site.kind == choiceRule && site.choice.Policy == All && site.choice.Filter == nil:
			ordered[id] = nil
		case chartObserves(site):
			if reachesSite(left, id) {
				return grammarError("left recursion through an ordered or observing rule at site %d", id)
			}
		}
		if site.kind != sequenceRule {
			zero[id] = left[id]
			continue
		}
		nonempty := 0
		for _, child := range site.children {
			if !nullable[child] {
				nonempty++
			}
		}
		for _, child := range site.children {
			if nonempty == 0 || nonempty == 1 && !nullable[child] {
				zero[id] = append(zero[id], child)
			}
		}
	}
	if err := checkCycles(ordered); err != nil {
		return err
	}
	if err := checkCycles(zero); err != nil {
		return grammarError("zero-yield recursion in ambiguity grammar")
	}
	return nil
}

func chartObserves(site site) bool {
	switch site.kind {
	case choiceRule:
		return site.choice.Policy != All || site.choice.Filter != nil
	case optionalRule, manyRule, separatedRule, programRule, expressionRule, recoverRule, updateRule, guardRule, withLexRule:
		return true
	}
	return false
}

func reachesSite(edges [][]int, target int) bool {
	pending := append([]int(nil), edges[target]...)
	seen := make(map[int]bool)
	for len(pending) != 0 {
		last := len(pending) - 1
		id := pending[last]
		pending = pending[:last]
		if id == target {
			return true
		}
		if !seen[id] {
			seen[id] = true
			pending = append(pending, edges[id]...)
		}
	}
	return false
}
