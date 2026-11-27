package parser

// Deterministic subtrees execute through the original VM. Its retirement
// requests stay local because other chart branches may still need the input.
type heldChartSource struct {
	Source
	highest int
}

func (s *heldChartSource) Read(mark Mark, request Request) (Read, error) {
	read, err := s.Source.Read(mark, request)
	if err == nil && read.Status != NeedInput {
		s.highest = max(s.highest, read.Next.Byte)
	}
	return read, err
}

func (*heldChartSource) DiscardBefore(int) {}

func (c *chart[N]) runIsland(cell *chartCell) {
	s := c.parser
	held := &heldChartSource{Source: s.source, highest: s.start}
	vm := &Session[N]{
		engine: s.engine, source: held, sourceID: s.sourceID, pin: -1,
		state: cell.key.state, mark: cell.key.point.mark, virtual: cell.key.point.virtual,
		request: cell.key.request, discarded: cell.key.point.mark.Byte,
	}
	if err := vm.push(cell.key.site); err != nil {
		c.err = err
		return
	}
	c.advanceIsland(cell, vm, held)
}

func (c *chart[N]) advanceIsland(cell *chartCell, vm *Session[N], held *heldChartSource) {
	for moves := 0; moves < 64 && len(vm.frames) != 0; moves++ {
		s := c.parser
		if c.work >= s.engine.limits.Steps {
			c.err = limitError("steps", s.engine.limits.Steps)
			return
		}
		before := vm.takes
		paused, err := vm.tick()
		if err != nil {
			c.err = err
			return
		}
		if held.highest-s.start > s.engine.limits.RetainedBytes {
			c.err = limitError("retained bytes", s.engine.limits.RetainedBytes)
			return
		}
		if paused {
			c.suspend(cell, func() { c.advanceIsland(cell, vm, held) })
			return
		}
		c.work++
		if vm.takes > before {
			vm.zeros = 0
		} else {
			vm.zeros++
			if vm.zeros > s.engine.limits.ZeroMoves {
				c.err = limitError("zero moves", s.engine.limits.ZeroMoves)
				return
			}
		}
		vm.releaseInput()
	}
	if len(vm.frames) != 0 {
		// Give other live arms a turn before continuing a long island.
		c.enqueue(func() { c.advanceIsland(cell, vm, held) })
		return
	}
	recognized := vm.recognized
	out := convertChartOutput(recognized.output, vm.tokens, make(map[*recipe]*chartNode))
	tape := tapeOf(vm.tokens)
	if tape != nil {
		tape.inserted = vm.inserted
	}
	value := chartResult{
		end: chartPoint{vm.mark, vm.virtual}, state: vm.state, takes: vm.takes,
		out: out, tape: tape,
	}
	if vm.repairCount != 0 {
		cost, first := chartRepairRank(out)
		value.cost = cost
		if cost != 0 {
			value.first.mark.Byte = first
			for _, token := range vm.withoutInsertions(nil, 0, len(vm.tokens)) {
				if token.Span.End > first {
					break
				}
				value.firstTakes++
			}
		}
		var keys []repairSite
		for key := range vm.repairs {
			keys = append(keys, key)
		}
		value.repairs = &chartRepairLog{keys: keys}
	}
	if miss := recognized.miss; miss != nil {
		value.end = chartPoint{miss.mark, miss.virtual}
		value.state = miss.state
		tokens := vm.withoutInsertions(nil, 0, len(vm.tokens))
		for part := miss.detached; part != nil; part = part.next {
			tokens = append(tokens, part.tokens...)
		}
		value.tape = tapeOf(tokens)
		c.reject(cell, &chartMiss{
			chartResult: value, found: miss.found, expected: miss.expected, request: miss.request,
			commit: vm.mark != cell.key.point.mark || vm.repairCount != 0,
		})
		return
	}
	c.emit(cell, value)
}

func chartRepairRank(out *chartOutput) (cost, first int) {
	chartVisit(out, true, func(piece *chartOutput) {
		if piece.kind != nodeOutput {
			return
		}
		if problem := piece.node.problem; problem != nil {
			if cost == 0 {
				first = problem.Found.Point.Start
			}
			cost++
			return
		}
		count, point := chartRepairRank(piece.node.body)
		if cost == 0 && count != 0 {
			first = point
		}
		cost += count
	})
	return cost, first
}
