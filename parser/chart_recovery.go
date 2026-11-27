package parser

import (
	"slices"

	"github.com/xiam/leafkit/tree"
)

func (c *chart[N]) recover(cell *chartCell, rule site) {
	child := c.call(childKey(cell, rule.children[0], cell.key.point))
	c.await(cell, []*chartCell{child}, func() {
		if len(child.results) != 0 {
			for _, value := range child.results {
				c.emit(cell, value)
			}
			return
		}
		misses := c.settledMisses(child)
		if c.err != nil {
			return
		}
		if len(misses) == 0 {
			c.offer(cell, cell.key.point, cell.key.request, func(read Read) {
				c.recoverMiss(cell, rule, &chartMiss{
					chartResult: cell.entry(),
					found:       c.parser.look(read), request: cell.key.request,
				})
			})
			return
		}
		// An enclosing First may commit only some failed prefixes.
		for _, miss := range misses {
			c.recoverMiss(cell, rule, miss)
		}
	})
}

func (c *chart[N]) recoverMiss(cell *chartCell, rule site, miss *chartMiss) {
	if miss.needOffer {
		c.offer(cell, miss.end, miss.request, func(read Read) {
			resolved := *miss
			resolved.found, resolved.needOffer = c.parser.look(read), false
			c.recoverMiss(cell, rule, &resolved)
		})
		return
	}
	request := cell.key.request
	if rule.boundary.Goal != nil {
		request = *rule.boundary.Goal
	}
	order := miss.tape.sourceOrder(slices.Clone(miss.order))
	scan := &chartScan{miss: miss, point: miss.end, skipped: miss.tape.sourceRecords(nil)}
	var advance func()
	advance = func() {
		c.offer(cell, scan.point, request, func(read Read) {
			boundary := read.Status == EndOfInput
			look := c.parser.look(read)
			for _, pattern := range rule.boundary.At {
				boundary = boundary || Matches(look, pattern)
			}
			left := boundary && (read.Status == EndOfInput || !rule.boundary.Consume)
			if read.Status == TokenReady && !left {
				scan.take(read)
			}
			if !boundary {
				c.enqueue(advance)
				return
			}
			keys := []repairSite{{cell.key.site, cell.key.point.mark}}
			if left && scan.point.mark != cell.key.point.mark {
				keys = append(keys, repairSite{cell.key.site, scan.point.mark})
			}
			result := chartResult{
				end: scan.point, state: cell.key.state, takes: miss.takes, cost: 1, first: miss.end, firstTakes: miss.budgetTakes(),
				tape: tapeOf(scan.skipped), repairs: &chartRepairLog{keys: keys}, order: order,
			}
			if miss.cost != 0 {
				result.first = miss.first
			}
			result.out = chartNodeOutput(c.problem(rule.name, scan), result.tape)
			c.emit(cell, result)
		})
	}
	advance()
}

type chartScan struct {
	miss    *chartMiss
	point   chartPoint
	out     *chartOutput
	skipped []Token
}

func (s *chartScan) take(read Read) {
	s.skipped = append(s.skipped, read.Token)
	s.point = chartAdvance(s.point, read)
}

func (c *chart[N]) problem(slot string, scan *chartScan) *chartNode {
	span := scan.miss.found.Point
	if len(scan.skipped) != 0 {
		first := scan.skipped[0]
		start := first.Span.Start
		for _, trivia := range first.Leading {
			start = min(start, trivia.Span.Start)
		}
		span = tree.Span{Source: c.parser.sourceID, Start: start, End: scan.point.mark.Byte}
	}
	return &chartNode{
		slot: slot, kind: tree.KindError, span: span, hasError: true,
		problem: &Problem{Span: span, Found: scan.miss.found, Expected: scan.miss.expected, Skipped: scan.skipped},
	}
}

func (c *chart[N]) finishFile() Status {
	if c.file != nil {
		return c.scanFile()
	}
	var best chartResult
	var files []chartFile
	var bestEnd Read
	var suffix *chartMiss
	for _, value := range stableChartResults(c.root.results) {
		read, err := c.read(value.end, c.parser.engine.request)
		if err != nil {
			c.err = err
			return Done
		}
		switch read.Status {
		case NeedInput:
			return Paused
		case TokenReady:
			if suffix == nil || after(value.end.mark, suffix.end.mark) ||
				value.end == suffix.end && chartBetterRepair(value, suffix.chartResult) {
				suffix = &chartMiss{
					chartResult: value, found: c.parser.look(read),
					expected: []Pattern{EndOfFile()}, request: c.parser.engine.request,
				}
			}
		case EndOfInput:
			if len(files) == 0 || chartBetterRepair(value, best) {
				best, bestEnd, files = value, read, files[:0]
			}
			if !chartBetterRepair(best, value) {
				files = append(files, chartFile{out: value.out, end: read})
			}
		}
	}
	if len(files) != 0 {
		if best.cost != 0 {
			reading := selectChartReading(best)
			files = []chartFile{{out: reading.out, end: bestEnd}}
		}
		c.parser.root, c.err = c.parser.constructForest(files)
		return Done
	}
	var miss *chartMiss
	if misses := c.settledMisses(c.root); len(misses) != 0 {
		miss = misses[0]
	}
	if c.err != nil {
		return Done
	}
	if suffix != nil && (miss == nil || !after(miss.end.mark, suffix.end.mark)) {
		miss = suffix
	}
	if miss == nil {
		point := c.root.key.point
		read, err := c.read(point, c.parser.engine.request)
		if err != nil {
			c.err = err
			return Done
		}
		if read.Status == NeedInput {
			return Paused
		}
		miss = &chartMiss{chartResult: chartResult{end: point}, found: c.parser.look(read), request: c.parser.engine.request}
	}
	if miss.needOffer {
		read, err := c.read(miss.end, miss.request)
		if err != nil {
			c.err = err
			return Done
		}
		if read.Status == NeedInput {
			return Paused
		}
		resolved := *miss
		resolved.found, resolved.needOffer = c.parser.look(read), false
		miss = &resolved
	}
	scan := &chartScan{miss: miss, point: miss.end, out: miss.out}
	if miss != suffix {
		keep := c.parser.start
		chartVisit(miss.out, true, func(piece *chartOutput) {
			if piece.kind == nodeOutput {
				keep = max(keep, piece.node.span.End)
			}
		})
		for _, token := range miss.tape.sourceRecords(nil) {
			if token.Span.Start >= keep {
				scan.skipped = append(scan.skipped, token)
			}
		}
	}
	c.file = scan
	return c.scanFile()
}

func (c *chart[N]) scanFile() Status {
	scan := c.file
	for {
		read, err := c.read(scan.point, c.parser.engine.request)
		if err != nil {
			c.err = err
			return Done
		}
		switch read.Status {
		case NeedInput:
			return Paused
		case EndOfInput:
			out := chartJoin(scan.out, chartNodeOutput(c.problem("file", scan), tapeOf(scan.skipped)))
			c.parser.root, c.err = c.parser.constructForest([]chartFile{{out: out, end: read}})
			return Done
		}
		if !c.charge() {
			return Done
		}
		scan.take(read)
	}
}

func dropChartOutput(value chartResult, start int) *chartOutput {
	if value.cost == 0 {
		return &chartOutput{kind: tokenOutput, tape: value.tape}
	}
	var out *chartOutput
	var descend func(*chartOutput)
	descend = func(node *chartOutput) {
		chartVisit(node, true, func(piece *chartOutput) {
			if piece.kind != nodeOutput || !piece.node.hasError {
				return
			}
			if piece.node.problem == nil && piece.node.arms == nil {
				descend(piece.node.body)
				return
			}
			tokens := tapeWindow(value.tape, start, piece.node.span.Start)
			if tokens != nil {
				out = chartJoin(out, &chartOutput{kind: tokenOutput, tape: tokens})
			}
			out = chartJoin(out, piece)
			start = piece.node.span.End
		})
	}
	descend(value.out)
	if tokens := tapeWindow(value.tape, start, value.end.mark.Byte); tokens != nil {
		out = chartJoin(out, &chartOutput{kind: tokenOutput, tape: tokens})
	}
	return out
}
