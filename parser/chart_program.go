package parser

import (
	"slices"
)

func (c *chart[N]) program(cell *chartCell, rule site, label string, value chartResult, zeros int) {
	if zeros > c.parser.engine.limits.ZeroMoves {
		c.err = limitError("zero moves", c.parser.engine.limits.ZeroMoves)
		return
	}
	state := rule.states[label]
	view := View{State: value.state, Parts: chartParts(value.out)}
	if state.Control != nil {
		c.programMove(cell, rule, label, value, state.Control(view), Read{}, zeros)
		return
	}
	c.offer(cell, value.end, cell.key.request, func(read Read) {
		c.programMove(cell, rule, label, value, state.Step(c.parser.look(read), view), read, zeros)
	})
}

func (c *chart[N]) programMove(cell *chartCell, rule site, label string, value chartResult, move Move, read Read, zeros int) {
	if !declaredMove(rule.states[label], move) {
		c.err = grammarError("undeclared move at program label %q", label)
		return
	}
	switch move.kind {
	case takeMove:
		if read.Status != TokenReady {
			c.err = grammarError("attempted take at EOF")
			return
		}
		taken := chartTaken(value, read)
		if move.bind != "" {
			taken.out = chartCapture(move.bind, taken)
		}
		result := chartAppend(value, taken)
		c.enqueue(func() { c.program(cell, rule, move.next, result, 0) })
	case jumpMove, stateMove:
		if move.kind == stateMove {
			value.state = move.state
		}
		c.enqueue(func() { c.program(cell, rule, move.next, value, zeros+1) })
	case callMove:
		callee := c.parser.engine.named[move.rule]
		key := resumeKey(cell, callee, value)
		if move.request != nil {
			key.request = *move.request
			if err := checkComputedRequest(key.request); err != nil {
				c.err = err
				return
			}
		}
		child := c.call(key)
		// A Go step can observe all captures. Wait for the packed child
		// to settle before offering a view to the resumption callback.
		c.await(cell, []*chartCell{child}, func() {
			if len(child.results) == 0 {
				for _, miss := range c.settledMisses(child) {
					product := chartProductMiss(cell.key.point, value, *miss)
					c.reject(cell, &product)
				}
				return
			}
			for _, result := range stableChartResults(child.results) {
				if move.bind != "" {
					result.out = chartCapture(move.bind, result)
				}
				c.extend(cell, value, result, func(combined chartResult) {
					nextZeros := 0
					if combined.end == value.end {
						nextZeros = zeros + 1
					}
					c.enqueue(func() { c.program(cell, rule, move.next, combined, nextZeros) })
				})
			}
		})
	case returnMove:
		c.emit(cell, value)
	case rejectMove:
		reject := func(read Read) {
			c.reject(cell, &chartMiss{
				chartResult: value, found: c.parser.look(read), expected: slices.Clone(move.expected),
				request: cell.key.request, commit: value.end != cell.key.point || value.cost != 0,
			})
		}
		if rule.states[label].Control == nil {
			reject(read)
		} else {
			c.offer(cell, value.end, cell.key.request, reject)
		}
	}
}

func chartTaken(prefix chartResult, read Read) chartResult {
	tape := tapeOf([]Token{read.Token})
	return chartResult{
		end: chartAdvance(prefix.end, read), state: prefix.state, takes: 1, tape: tape,
		out: &chartOutput{kind: tokenOutput, tape: tape},
	}
}
