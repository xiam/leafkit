package parser

import (
	"fmt"
	"slices"

	"github.com/xiam/leafkit/tree"
)

// A cell shares recognition at one compiled site and replay context. Its
// subscribers are return continuations. Each continuation sees an exit once;
// growth of an already published pack does not rerun structural continuations.
type chartPoint struct {
	mark    Mark
	virtual int
}

type chartKey struct {
	site         int
	point        chartPoint
	request      Request
	state        State
	power        bindingPower
	observeTakes bool
}

type chartResult struct {
	end        chartPoint
	state      State
	out        *chartOutput
	tape       *chartTape
	takes      int
	cost       int
	firstTakes int
	first      chartPoint
	repairs    *chartRepairLog
	order      []int
}

type chartMiss struct {
	chartResult
	found     Look
	request   Request
	expected  []Pattern
	commit    bool
	needOffer bool
}

type chartCell struct {
	key           chartKey
	results       []chartResult
	listeners     []func(chartResult)
	failures      []func(*chartMissSet)
	deps          map[*chartCell]struct{}
	misses        map[chartMissKey]*chartMissSet
	settledMisses []*chartMiss
	missesSettled bool
	waiting       int
}

type chartBarrier struct {
	cell *chartCell
	deps []*chartCell
	run  func()
}

type chart[N tree.Node] struct {
	parser   *Session[N]
	cells    map[chartKey]*chartCell
	reads    map[chartReadKey]Read
	agenda   []func()
	paused   []func()
	barriers []chartBarrier
	root     *chartCell
	file     *chartScan
	edges    int
	work     int
	head     int
	err      error
}

type chartReadKey struct {
	point   chartPoint
	request Request
}

func (s *Session[N]) runChart() (N, Status, error) {
	c := s.chart
	if c.root == nil {
		c.root = c.call(chartKey{site: s.engine.start, point: chartPoint{mark: s.mark}, request: s.request, state: s.state})
	} else if len(c.paused) != 0 {
		waiting := c.paused
		c.paused = nil
		progress := false
		for _, retry := range waiting {
			count, work := len(c.paused), c.work
			retry()
			progress = progress || len(c.paused) == count || c.work != work
			if c.err != nil {
				break
			}
		}
		if c.err == nil && !progress && len(c.agenda) == 0 {
			return s.root, Paused, nil
		}
	}
	for c.err == nil {
		if c.head < len(c.agenda) {
			if !c.charge() {
				break
			}
			job := c.agenda[c.head]
			c.agenda[c.head] = nil
			c.head++
			job()
			continue
		}
		c.agenda, c.head = c.agenda[:0], 0
		if c.settle() {
			continue
		}
		if c.err != nil {
			break
		}
		if len(c.paused) != 0 {
			return s.root, Paused, nil
		}
		if len(c.barriers) != 0 {
			c.err = grammarError("cyclic dependency through an ordered or observing rule")
			break
		}
		status := c.finishFile()
		if status == Paused && c.err == nil {
			return s.root, Paused, nil
		}
		break
	}
	s.finish(c.err)
	return s.root, Done, s.err
}

func (c *chart[N]) charge() bool {
	if c.work >= c.parser.engine.limits.Steps {
		c.err = limitError("steps", c.parser.engine.limits.Steps)
		return false
	}
	c.work++
	return true
}

func (c *chart[N]) enqueue(job func()) {
	if c.err != nil {
		return
	}
	if c.work+len(c.agenda)-c.head >= c.parser.engine.limits.Steps {
		c.err = limitError("steps", c.parser.engine.limits.Steps)
		return
	}
	c.agenda = append(c.agenda, job)
}

func (c *chart[N]) edge() bool {
	if c.edges == c.parser.engine.limits.ForestEdges {
		c.err = limitError("forest edges", c.edges)
		return false
	}
	c.edges++
	return true
}

func (c *chart[N]) call(key chartKey) *chartCell {
	if cell := c.cells[key]; cell != nil {
		return cell
	}
	cell := &chartCell{key: key}
	if len(c.cells) == c.parser.engine.limits.Descriptors {
		c.err = limitError("descriptors", len(c.cells))
		return cell
	}
	c.cells[key] = cell
	c.enqueue(func() { c.open(cell) })
	return cell
}

func childKey(parent *chartCell, site int, point chartPoint) chartKey {
	return chartKey{site: site, point: point, request: parent.key.request, state: parent.key.state, observeTakes: parent.key.observeTakes}
}

func resumeKey(parent *chartCell, site int, prefix chartResult) chartKey {
	key := childKey(parent, site, prefix.end)
	key.state = prefix.state
	return key
}

func (cell *chartCell) entry() chartResult {
	return chartResult{end: cell.key.point, state: cell.key.state}
}

func (c *chart[N]) depend(parent, child *chartCell) {
	if parent.deps == nil {
		parent.deps = make(map[*chartCell]struct{})
	}
	parent.deps[child] = struct{}{}
}

func (c *chart[N]) follow(parent *chartCell, key chartKey, fn func(chartResult)) *chartCell {
	child := c.call(key)
	c.depend(parent, child)
	child.listeners = append(child.listeners, fn)
	for _, result := range child.results {
		result := result
		c.enqueue(func() { fn(result) })
	}
	return child
}

func (c *chart[N]) emit(cell *chartCell, result chartResult) {
	if !c.edge() {
		return
	}
	cell.results = append(cell.results, result)
	for _, listener := range cell.listeners {
		listener := listener
		c.enqueue(func() { listener(result) })
	}
}

func (c *chart[N]) await(cell *chartCell, deps []*chartCell, run func()) {
	cell.waiting++
	for _, dep := range deps {
		c.depend(cell, dep)
	}
	c.barriers = append(c.barriers, chartBarrier{cell, deps, run})
}

// At agenda quiescence an observing rule can run once everything it observes
// is stable. Cycles of structural cells share a fixed point. A pause or an
// unresolved observing rule anywhere below the request keeps it unsettled.
func (c *chart[N]) settle() bool {
	for i, barrier := range c.barriers {
		pending := slices.Clone(barrier.deps)
		seen := make(map[*chartCell]struct{})
		stable := true
		for len(pending) != 0 {
			last := len(pending) - 1
			cell := pending[last]
			pending = pending[:last]
			if _, ok := seen[cell]; ok {
				continue
			}
			if !c.charge() {
				return false
			}
			if cell.waiting != 0 {
				stable = false
				break
			}
			seen[cell] = struct{}{}
			for dep := range cell.deps {
				pending = append(pending, dep)
			}
		}
		if stable {
			c.barriers = slices.Delete(c.barriers, i, i+1)
			barrier.cell.waiting--
			c.enqueue(barrier.run)
			return true
		}
	}
	return false
}

func (c *chart[N]) suspend(cell *chartCell, resume func()) {
	cell.waiting++
	c.paused = append(c.paused, func() {
		cell.waiting--
		resume()
	})
}

func (c *chart[N]) read(point chartPoint, request Request) (Read, error) {
	s := c.parser
	key := chartReadKey{point, request}
	if cached, ok := c.reads[key]; ok {
		return cached, nil
	}
	read, err := s.source.Read(point.mark, request)
	if err != nil {
		return Read{}, fmt.Errorf("%w: %w", ErrSource, err)
	}
	if err := checkRead(read, point.mark, s.sourceID); err != nil {
		return Read{}, fmt.Errorf("%w: %w", ErrSource, err)
	}
	if read.Status == NeedInput {
		return read, nil
	}
	if read.Next.Byte-s.start > s.engine.limits.RetainedBytes {
		return Read{}, limitError("retained bytes", s.engine.limits.RetainedBytes)
	}
	if read.Status == TokenReady && read.Next.Byte == point.mark.Byte && point.virtual >= s.engine.limits.VirtualTokens {
		return Read{}, limitError("virtual tokens", s.engine.limits.VirtualTokens)
	}
	c.reads[key] = read
	return read, nil
}

func (c *chart[N]) offer(cell *chartCell, point chartPoint, request Request, use func(Read)) {
	read, err := c.read(point, request)
	switch {
	case err != nil:
		c.err = err
	case read.Status == NeedInput:
		c.suspend(cell, func() { c.offer(cell, point, request, use) })
	default:
		use(read)
	}
}

func chartAdvance(point chartPoint, read Read) chartPoint {
	next := chartPoint{mark: read.Next}
	if read.Next.Byte == point.mark.Byte {
		next.virtual = point.virtual + 1
	}
	return next
}

type chartRepairLog struct {
	keys []repairSite
}

func chartAppend(left, right chartResult) chartResult {
	result := chartResult{
		end: right.end, state: right.state, out: chartJoin(left.out, right.out), tape: chartConcat(left.tape, right.tape),
		takes: left.takes + right.takes, cost: left.cost + right.cost,
		first: left.first, firstTakes: left.firstTakes, repairs: combineChartRepairs(left.repairs, right.repairs),
	}
	if left.cost == 0 && right.cost != 0 {
		result.first, result.firstTakes = right.first, left.takes+right.firstTakes
	}
	return result
}

func combineChartRepairs(left, right *chartRepairLog) *chartRepairLog {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	keys := append(slices.Clone(left.keys), right.keys...)
	return &chartRepairLog{keys: keys}
}

func (c *chart[N]) extend(cell *chartCell, left, right chartResult, use func(chartResult)) {
	if left.repairs != nil && right.repairs != nil {
		for _, key := range right.repairs.keys {
			if slices.Contains(left.repairs.keys, key) {
				c.offer(cell, left.end, cell.key.request, func(read Read) {
					c.reject(cell, &chartMiss{
						chartResult: left, found: c.parser.look(read), request: cell.key.request, commit: true,
					})
				})
				return
			}
		}
	}
	use(chartAppend(left, right))
}

func chartBetterRepair(a, b chartResult) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	return a.cost != 0 && after(a.first.mark, b.first.mark)
}
