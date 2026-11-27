package parser

import (
	"slices"

	"github.com/xiam/leafkit/tree"
)

// Problem describes a committed syntax error. Found records the original
// offer, even if recovery scans under a different request. Expected and
// Skipped and all token data are read-only and may be retained.
type Problem struct {
	Span     tree.Span
	Expected []Pattern
	Found    Look
	Skipped  []Token
}

type errorLeaf = tree.ErrorNode

// ErrorNode is the default diagnostic leaf when the node type permits it.
type ErrorNode struct {
	*errorLeaf
	slot    string
	problem Problem
}

func (n *ErrorNode) Slot() string     { return n.slot }
func (n *ErrorNode) Problem() Problem { return n.problem }

// A tentative choice may rewind its token tape. Each winning failure keeps
// detached intervals from such rewinds, in input order.
type tokenFragment struct {
	tokens []Token
	next   *tokenFragment
}

type failure struct {
	mark     Mark
	request  Request
	state    State
	found    Look
	expected []Pattern
	tokenEnd int
	virtual  int
	detached *tokenFragment
}

func (s *Session[N]) reject(look Look, request Request, expected []Pattern) {
	s.complete(result{miss: s.failureAt(look, request, expected)})
}

func (s *Session[N]) failureAt(look Look, request Request, expected []Pattern) *failure {
	return &failure{
		mark: s.mark, request: request, state: s.state, found: look,
		expected: slices.Clone(expected), tokenEnd: len(s.tokens), virtual: s.virtual,
	}
}

func (s *Session[N]) bestFailure(best, next *failure, entry int) *failure {
	if best == nil || after(next.mark, best.mark) {
		if next.tokenEnd > entry {
			if tokens := s.withoutInsertions(nil, entry, next.tokenEnd); len(tokens) != 0 {
				next.detached = &tokenFragment{tokens, next.detached}
			}
			next.tokenEnd = entry
		}
		return next
	}
	// The full replay mark and request identify the exact stable offer. Keep
	// declaration order even when different streams reach the same position.
	if best.mark == next.mark && best.request == next.request && best.state == next.state {
		for _, pattern := range next.expected {
			if !slices.Contains(best.expected, pattern) {
				best.expected = append(best.expected, pattern)
			}
		}
	}
	return best
}

func after(a, b Mark) bool {
	return a.Byte > b.Byte || a.Byte == b.Byte && a.Virtual > b.Virtual
}

type repairSite struct {
	site int
	mark Mark
}

type recoveryScan struct {
	miss  *failure
	start int
}

// Parser-inserted markers never came from the source. A rejected
// production loses them, including markers before a rewound failure.
func (s *Session[N]) withoutInsertions(dst []Token, start, end int) []Token {
	first, _ := slices.BinarySearch(s.inserted, start)
	for _, index := range s.inserted[first:] {
		if index >= end {
			break
		}
		dst = append(dst, s.tokens[start:index]...)
		start = index + 1
	}
	return append(dst, s.tokens[start:end]...)
}

func (s *Session[N]) beginRecovery(miss *failure, start int) *recoveryScan {
	first, _ := slices.BinarySearch(s.inserted, start)
	if first < len(s.inserted) {
		tape := s.withoutInsertions(s.tokens[:s.inserted[first]], s.inserted[first], len(s.tokens))
		clear(s.tokens[len(tape):])
		s.tokens, s.inserted = tape, s.inserted[:first]
	}
	for part := miss.detached; part != nil; part = part.next {
		s.tokens = append(s.tokens, part.tokens...)
	}
	s.mark, s.virtual = miss.mark, miss.virtual
	return &recoveryScan{miss: miss, start: start}
}

// scanRecovery performs one resumable read. A left boundary remains at
// exactly the read's entry mark for the parent to read under its own goal.
func (s *Session[N]) scanRecovery(boundary Boundary, request Request) (Read, bool, error) {
	if boundary.Goal != nil {
		request = *boundary.Goal
	}
	read, err := s.readWithRequest(request)
	if err != nil || read.Status == NeedInput {
		return read, false, err
	}
	if read.Status == EndOfInput {
		return read, true, nil
	}
	if s.steps == s.engine.limits.Steps {
		return Read{}, false, limitError("steps", s.steps)
	}
	look := s.look(read)
	for _, pattern := range boundary.At {
		if Matches(look, pattern) {
			if boundary.Consume {
				s.consume(read)
			}
			return read, true, nil
		}
	}
	s.consume(read)
	return read, false, nil
}

func (s *Session[N]) recovered(slot string, scan *recoveryScan) *output {
	interval := tokenInterval{scan.start, len(s.tokens)}
	skipped := interval.from(s.tokens)
	span := scan.miss.found.Point
	if len(skipped) != 0 {
		start := skipped[0].Span.Start
		for _, trivia := range skipped[0].Leading {
			start = min(start, trivia.Span.Start)
		}
		span = tree.Span{Source: s.sourceID, Start: start, End: s.mark.Byte}
	}
	return s.recordError(slot, Problem{
		Span: span, Expected: scan.miss.expected, Found: scan.miss.found, Skipped: skipped,
	}, interval)
}

func (s *Session[N]) recover(f *frame, rule site) (bool, error) {
	if !f.childReturned {
		return false, s.push(rule.children[0])
	}
	if f.scan == nil {
		child := f.child
		if child.miss == nil {
			s.complete(child)
			return false, nil
		}
		if s.pin >= 0 {
			// Try the other clean arms first. The choice can replay this
			// arm after commitment without having scanned or built an error.
			s.complete(result{miss: child.miss, repair: true})
			return false, nil
		}
		key := repairSite{f.site, f.entry}
		if s.repairs[key] {
			s.complete(result{miss: child.miss})
			return false, nil
		}
		if s.repairs == nil {
			s.repairs = make(map[repairSite]bool)
		}
		s.repairs[key] = true
		s.repairCount++
		s.trimRecipes(f.entryRecipes)
		f.scan = s.beginRecovery(child.miss, f.entryTokens)
		f.child = result{}
	}
	before := s.mark
	read, complete, err := s.scanRecovery(rule.boundary, f.entryRequest)
	if err == nil && complete {
		if s.mark == before {
			// Let the delimiter owner advance. Reentering this site at
			// that mark must not emit a second diagnostic.
			s.repairs[repairSite{f.site, s.mark}] = true
		}
		s.succeed(s.recovered(rule.name, f.scan))
	}
	return read.Status == NeedInput, err
}

func (s *Session[N]) insert(rule site) (bool, error) {
	read, err := s.read()
	if err != nil || read.Status == NeedInput {
		return read.Status == NeedInput, err
	}
	look := s.look(read)
	if !rule.allowed(look, View{State: s.state}) {
		s.reject(look, s.request, []Pattern{rule.pattern})
		return false, nil
	}
	i := len(s.tokens)
	s.inserted = append(s.inserted, i)
	s.tokens = append(s.tokens, Token{Kind: rule.pattern.kind, Span: look.Point, Synthetic: true})
	s.succeed(&output{kind: tokenOutput, tokens: tokenInterval{i, i + 1}})
	return false, nil
}

func (s *Session[N]) file() (bool, error) {
	if s.fileScan == nil {
		miss := s.recognized.miss
		if miss == nil {
			read, err := s.readWithRequest(s.engine.request)
			if err != nil || read.Status == NeedInput {
				return read.Status == NeedInput, err
			}
			if read.Status == EndOfInput {
				s.root, err = s.construct(s.recognized.output, read)
				s.finish(err)
				return false, nil
			}
			miss = s.failureAt(s.look(read), s.engine.request, []Pattern{EndOfFile()})
			s.recognized.kept = len(s.tokens)
		}
		s.fileScan = s.beginRecovery(miss, s.recognized.kept)
	}
	read, complete, err := s.scanRecovery(Boundary{}, s.engine.request)
	if err == nil && complete {
		output := join(s.recognized.output, s.recovered("file", s.fileScan))
		s.root, err = s.construct(output, read)
		s.finish(err)
	}
	return read.Status == NeedInput, err
}
