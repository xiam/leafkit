package parser

import (
	"errors"
	"fmt"

	"github.com/xiam/leafkit/tree"
)

var (
	ErrGrammar = errors.New("parser grammar")
	ErrSource  = errors.New("parser source")
	ErrLimit   = errors.New("parser limit")
	ErrBuild   = errors.New("parser construction")
)

func grammarError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrGrammar, fmt.Sprintf(format, args...))
}

func limitError(name string, limit int) error {
	return fmt.Errorf("%w: exceeded %s limit of %d", ErrLimit, name, limit)
}

// Status describes whether Run is terminal or suspended at a token request.
type Status uint8

const (
	Done Status = iota
	Paused
)

type result struct {
	output *output
	miss   *failure
	kept   int  // Token prefix owned by completed outputs on a miss.
	repair bool // A tentative arm could replay with recovery enabled.
}

type frame struct {
	site             int
	label, bind      string
	next             int
	entry            Mark
	entryTokens      int
	entryTakes       int
	entryRepairs     int
	entryRecipes     int
	entryVirtual     int
	entryRequest     Request
	iteration        int
	iterationMark    Mark
	iterationTakes   int
	iterationRecipes int
	iterationRepairs int
	kept             int
	best             *failure
	repairArm        int
	scan             *recoveryScan
	previousPin      int
	floor            int
	output           *output
	child            result
	childReturned    bool
	expr             *expressionFrame
}

// The most recent stable event can also serve a non-consuming step or call.
type peek struct {
	valid   bool
	mark    Mark
	request Request
	read    Read
}

// Session holds one parse. Feed its source between calls to Run. Sessions
// execute on the caller's goroutine and must not be driven concurrently.
type Session[N tree.Node] struct {
	engine       *Engine[N]
	source       Source
	sourceID     tree.SourceID
	start        int
	state        State
	request      Request
	mark         Mark
	peek         peek
	discarded    int
	pin          int
	virtual      int
	takes        int
	repairs      map[repairSite]bool
	repairCount  int
	fileScan     *recoveryScan
	steps, zeros int
	frames       []frame
	frameStates  []State // Allocated on the first state change, parallel to frames.
	tokens       []Token
	inserted     []int // Sorted token indices of grammar insertions.
	recipes      []*recipe
	recognized   result
	root         N
	err          error
	done         bool
	chart        *chart[N]
}

// Start opens a fresh session on src with an immutable consumer state.
func (e *Engine[N]) Start(src Source, initial State) *Session[N] {
	s := &Session[N]{engine: e, source: src, state: initial, request: e.request, pin: -1}
	if src == nil {
		s.finish(fmt.Errorf("%w: nil source", ErrSource))
		return s
	}
	s.sourceID, s.mark = src.Begin()
	if s.sourceID == "" || s.mark.Byte < 0 || s.mark.Virtual < 0 {
		s.finish(fmt.Errorf("%w: invalid initial snapshot or mark", ErrSource))
		return s
	}
	s.start = s.mark.Byte
	s.discarded = s.start
	if e.generalized != nil && e.generalized[e.start] {
		s.chart = &chart[N]{parser: s, cells: make(map[chartKey]*chartCell), reads: make(map[chartReadKey]Read)}
		return s
	}
	if err := s.push(e.start); err != nil {
		s.finish(err)
	}
	return s
}

// Run recognizes input until it completes, fails or needs input. It returns
// the zero root on pause or failure. Terminal results are returned unchanged.
func (s *Session[N]) Run() (N, Status, error) {
	if s.chart != nil {
		return s.runChart()
	}
	for !s.done {
		if len(s.frames) > 0 && s.steps == s.engine.limits.Steps {
			s.finish(limitError("steps", s.steps))
			break
		}
		before := s.takes
		var paused bool
		var err error
		if len(s.frames) == 0 {
			paused, err = s.file()
		} else {
			paused, err = s.tick()
		}
		if err != nil {
			s.finish(err)
			break
		}
		if paused {
			return s.root, Paused, nil
		}
		if s.done {
			break
		}
		s.steps++
		if s.takes > before {
			s.zeros = 0
		} else {
			s.zeros++
			if s.zeros > s.engine.limits.ZeroMoves {
				s.finish(limitError("zero moves", s.engine.limits.ZeroMoves))
				break
			}
		}
		s.releaseInput()
	}
	return s.root, Done, s.err
}

func (s *Session[N]) finish(err error) {
	s.done, s.err = true, err
	s.source = nil
	s.frames = nil
	s.frameStates = nil
	s.tokens = nil
	s.inserted = nil
	s.recipes = nil
	s.recognized = result{}
	s.state = ""
	s.repairs = nil
	s.fileScan = nil
	s.chart = nil
	s.peek = peek{}
}

func (s *Session[N]) floor() int {
	if s.pin >= 0 {
		return s.frames[s.pin].floor
	}
	return s.mark.Byte
}

func (s *Session[N]) releaseInput() {
	floor := s.floor()
	if floor > s.discarded {
		s.source.DiscardBefore(floor)
		s.discarded = floor
	}
}

func (s *Session[N]) read() (Read, error) {
	return s.readWithRequest(s.request)
}

func (s *Session[N]) readWithRequest(request Request) (Read, error) {
	read := s.peek.read
	if !s.peek.valid || s.peek.mark != s.mark || s.peek.request != request {
		var err error
		read, err = s.source.Read(s.mark, request)
		if err != nil {
			return Read{}, fmt.Errorf("%w: %w", ErrSource, err)
		}
		if err := checkRead(read, s.mark, s.sourceID); err != nil {
			return Read{}, fmt.Errorf("%w: %w", ErrSource, err)
		}
		if read.Status == NeedInput {
			return read, nil
		}
		s.peek = peek{true, s.mark, request, read}
	}
	if read.Next.Byte-s.floor() > s.engine.limits.RetainedBytes {
		return Read{}, limitError("retained bytes", s.engine.limits.RetainedBytes)
	}
	if read.Status == TokenReady && read.Next.Byte == s.mark.Byte && s.virtual >= s.engine.limits.VirtualTokens {
		return Read{}, limitError("virtual tokens", s.engine.limits.VirtualTokens)
	}
	return read, nil
}

func (s *Session[N]) look(read Read) Look {
	if read.Status == EndOfInput {
		return Look{End: true, Point: point(s.sourceID, read.Next.Byte)}
	}
	tok := read.Token
	return Look{Token: tok, Point: point(s.sourceID, tok.Span.Start)}
}

func (s *Session[N]) take(read Read) *output {
	start := len(s.tokens)
	s.consume(read)
	return &output{kind: tokenOutput, tokens: tokenInterval{start, start + 1}}
}

func (s *Session[N]) consume(read Read) {
	s.tokens = append(s.tokens, read.Token)
	s.takes++
	if read.Next.Byte == s.mark.Byte {
		s.virtual++
	} else {
		s.virtual = 0
	}
	s.mark = read.Next
	s.commitChoices()
}

func (s *Session[N]) push(site int) error {
	if len(s.frames) == s.engine.limits.Depth {
		return limitError("depth", s.engine.limits.Depth)
	}
	s.frames = append(s.frames, frame{
		site: site, label: s.engine.sites[site].name, entry: s.mark,
		entryTokens: len(s.tokens), entryRecipes: len(s.recipes), entryRequest: s.request,
		entryTakes: s.takes, entryRepairs: s.repairCount, entryVirtual: s.virtual,
	})
	if s.frameStates != nil {
		s.frameStates = append(s.frameStates, s.state)
	}
	return nil
}

func (s *Session[N]) complete(out result) {
	last := len(s.frames) - 1
	if s.frameStates != nil {
		if out.miss != nil {
			s.state = s.frameStates[last]
		}
		s.frameStates[last] = ""
		s.frameStates = s.frameStates[:last]
	}
	s.frames[last] = frame{}
	s.frames = s.frames[:last]
	if last == 0 {
		s.recognized = out
		return
	}
	parent := &s.frames[last-1]
	parent.child = out
	parent.childReturned = true
}

func (s *Session[N]) succeed(out *output) { s.complete(result{output: out}) }

func (s *Session[N]) tick() (bool, error) {
	f := &s.frames[len(s.frames)-1]
	rule := s.engine.sites[f.site]
	switch rule.kind {
	case recoverRule:
		return s.recover(f, rule)
	case insertRule:
		return s.insert(rule)
	case expressionRule:
		return s.expression(f, rule)
	case programRule:
		return s.program(f, rule)
	case tokenRule, atRule, notRule:
		return s.matchToken(rule)
	case choiceRule:
		return false, s.choose(f, rule)
	case manyRule, separatedRule:
		return false, s.repeat(f, rule)
	case sequenceRule:
		return false, s.sequence(f, rule)
	case updateRule, guardRule:
		return s.stateRule(f, rule)
	case withLexRule:
		return false, s.withLex(f, rule)
	default:
		return false, s.wrapper(f, rule)
	}
}

func (s *Session[N]) matchToken(rule site) (bool, error) {
	read, err := s.read()
	if err != nil || read.Status == NeedInput {
		return read.Status == NeedInput, err
	}
	look := s.look(read)
	matches := Matches(look, rule.pattern)
	if matches == (rule.kind == notRule) {
		var expected []Pattern
		if rule.kind != notRule {
			expected = []Pattern{rule.pattern}
		}
		s.reject(look, s.request, expected)
	} else if rule.kind == tokenRule && !look.End {
		s.succeed(s.take(read))
	} else {
		s.succeed(nil)
	}
	return false, nil
}

func (s *Session[N]) program(f *frame, rule site) (bool, error) {
	if f.childReturned {
		s.request = f.entryRequest
		child := f.child
		if child.miss != nil {
			s.complete(f.partial(child))
			return false, nil
		}
		if f.bind != "" {
			child.output = captured(f.bind, tokenInterval{f.iteration, len(s.tokens)}, child.output)
		}
		f.accept(child.output, len(s.tokens))
		f.child, f.childReturned = result{}, false
	}
	state := rule.states[f.label]
	view := View{State: s.state, Parts: Parts{f.output, s.tokens}}
	var move Move
	var read Read
	var look Look
	if state.Control != nil {
		move = state.Control(view)
	} else {
		var err error
		read, err = s.read()
		if err != nil || read.Status == NeedInput {
			return read.Status == NeedInput, err
		}
		look = s.look(read)
		move = state.Step(look, view)
	}
	if !declaredMove(state, move) {
		return false, grammarError("undeclared move at program label %q", f.label)
	}
	switch move.kind {
	case takeMove:
		if read.Status != TokenReady {
			return false, grammarError("attempted take at EOF")
		}
		out := s.take(read)
		if move.bind != "" {
			out = captured(move.bind, out.tokens, out)
		}
		f.output = join(f.output, out)
		f.label = move.next
	case jumpMove:
		f.label = move.next
	case stateMove:
		s.setState(move.state)
		f.label = move.next
	case callMove:
		if move.request != nil {
			if err := checkComputedRequest(*move.request); err != nil {
				return false, err
			}
			s.request = *move.request
		}
		f.label, f.bind, f.iteration = move.next, move.bind, len(s.tokens)
		return false, s.push(s.engine.named[move.rule])
	case returnMove:
		s.succeed(f.output)
	case rejectMove:
		if state.Control != nil {
			var err error
			read, err = s.read()
			if err != nil || read.Status == NeedInput {
				return read.Status == NeedInput, err
			}
			look = s.look(read)
		}
		s.complete(f.partial(result{miss: s.failureAt(look, s.request, move.expected)}))
	}
	return false, nil
}
