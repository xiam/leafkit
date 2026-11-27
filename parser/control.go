package parser

import "slices"

func (s *Session[N]) setState(next State) {
	if s.frameStates == nil {
		if next == s.state {
			return
		}
		// All open frames entered before any change, so they share the
		// current key. Later pushes save their own entry key.
		s.frameStates = make([]State, len(s.frames), cap(s.frames))
		for i := range s.frameStates {
			s.frameStates[i] = s.state
		}
	}
	s.state = next
}

func (s *Session[N]) stateRule(f *frame, rule site) (bool, error) {
	if !f.childReturned {
		return false, s.push(rule.children[0])
	}
	child := f.child
	if child.miss == nil {
		view := View{State: s.state, Parts: Parts{child.output, s.tokens}}
		switch {
		case rule.kind == updateRule:
			s.setState(rule.update(view))
		case !rule.guard(view):
			read, err := s.read()
			if err != nil || read.Status == NeedInput {
				return read.Status == NeedInput, err
			}
			s.reject(s.look(read), s.request, nil)
			return false, nil
		}
	}
	s.complete(child)
	return false, nil
}

func checkComputedRequest(request Request) error {
	if request.Layout > Joined {
		return grammarError("unknown computed layout %d", request.Layout)
	}
	return nil
}

func (s *Session[N]) withLex(f *frame, rule site) error {
	switch f.next {
	case 0:
		f.next = 1
		return s.push(rule.children[0])
	case 1:
		prefix := f.child
		if prefix.miss != nil {
			s.complete(prefix)
			return nil
		}
		view := View{State: s.state, Parts: Parts{prefix.output, s.tokens}}
		request := rule.compute(view)
		if err := checkComputedRequest(request); err != nil {
			return err
		}
		f.accept(prefix.output, len(s.tokens))
		f.child, f.childReturned = result{}, false
		f.next = 2
		s.request = request
		return s.push(rule.children[1])
	default:
		s.request = f.entryRequest
		s.complete(f.partial(f.child))
		return nil
	}
}

func (s *Session[N]) choose(f *frame, rule site) error {
	index := len(s.frames) - 1
	if !f.childReturned {
		f.previousPin, f.floor = s.pin, s.floor()
		s.pin = index
		f.next, f.repairArm = 1, -1
		return s.push(rule.children[0])
	}
	child := f.child
	if s.pin != index {
		// This choice already committed at its token budget or is replaying
		// the selected repair arm.
		s.complete(child)
		return nil
	}
	if child.miss == nil {
		s.pin = f.previousPin
		s.commitChoices()
		s.complete(child)
		return nil
	}
	if child.repair && f.repairArm < 0 {
		f.repairArm = f.next - 1
	}
	f.best = s.bestFailure(f.best, child.miss, f.entryTokens)
	s.mark, s.request, s.virtual = f.entry, f.entryRequest, f.entryVirtual
	s.trim(f.entryTokens, f.entryRecipes, f.entryTakes)

	f.child, f.childReturned = result{}, false
	if f.next < len(rule.children) {
		next := rule.children[f.next]
		f.next++
		return s.push(next)
	}

	s.pin = f.previousPin
	if f.repairArm >= 0 && s.pin < 0 {
		return s.push(rule.children[f.repairArm])
	}
	s.complete(result{miss: f.best, repair: f.repairArm >= 0})
	return nil
}

func (s *Session[N]) trim(tokens, recipes, takes int) {
	clear(s.tokens[tokens:])
	s.tokens = s.tokens[:tokens]
	first, _ := slices.BinarySearch(s.inserted, tokens)
	s.inserted = s.inserted[:first]
	s.trimRecipes(recipes)
	s.takes = takes
}

func (s *Session[N]) trimRecipes(recipes int) {
	clear(s.recipes[recipes:])
	s.recipes = s.recipes[:recipes]
}

// partial propagates only already completed top-level children. Wrappers
// that own a reduction discard partial children of a failed production.
func (f *frame) partial(child result) result {
	child.output = join(f.output, child.output)
	child.kept = max(f.kept, child.kept)
	return child
}

func (f *frame) accept(out *output, end int) {
	f.output = join(f.output, out)
	if out != nil && out.hasNode {
		f.kept = end
	}
}

// Only the innermost tentative choice can commit on a take. Once it commits,
// its accepted progress is available to its enclosing choices.
func (s *Session[N]) commitChoices() {
	for s.pin >= 0 {
		f := &s.frames[s.pin]
		budget := max(1, s.engine.sites[f.site].choice.Lookahead)
		if s.takes-f.entryTakes < budget {
			return
		}
		s.pin = f.previousPin
	}
}

func (s *Session[N]) sequence(f *frame, rule site) error {
	if f.childReturned {
		if f.child.miss != nil {
			s.complete(f.partial(f.child))
			return nil
		}
		f.accept(f.child.output, len(s.tokens))
		f.child, f.childReturned = result{}, false
	}
	if f.next == len(rule.children) {
		s.succeed(f.output)
		return nil
	}
	next := rule.children[f.next]
	f.next++
	return s.push(next)
}

const (
	firstItem = iota + 1
	separator
	laterItem
)

func (s *Session[N]) repeat(f *frame, rule site) error {
	if f.childReturned {
		child := f.child
		progress := s.mark != f.iterationMark
		repaired := s.repairCount != f.iterationRepairs
		if child.miss != nil {
			if progress || repaired || child.repair || f.next == laterItem && !rule.trailing {
				s.complete(f.partial(child))
			} else {
				s.trim(f.iteration, f.iterationRecipes, f.iterationTakes)
				s.succeed(f.output)
			}
			return nil
		}
		if !progress && !repaired {
			return grammarError("repetition succeeded without consuming a token")
		}
		f.accept(child.output, len(s.tokens))
		if !progress && (rule.kind == manyRule || f.next == separator) {
			// The delimiter is still available to the repetition's caller.
			s.succeed(f.output)
			return nil
		}
		f.child, f.childReturned = result{}, false
	}

	f.iteration, f.iterationMark = len(s.tokens), s.mark
	f.iterationRecipes, f.iterationTakes = len(s.recipes), s.takes
	f.iterationRepairs = s.repairCount
	switch {
	case f.next == 0 || rule.kind == manyRule:
		f.next = firstItem
	case f.next == separator:
		f.next = laterItem
	default:
		f.next = separator
	}
	if f.next == separator {
		return s.push(rule.children[1])
	}
	return s.push(rule.children[0])
}

func (s *Session[N]) wrapper(f *frame, rule site) error {
	if !f.childReturned {
		if rule.kind == refRule {
			return s.push(s.engine.named[rule.name])
		}
		if rule.kind == lexRule {
			s.request = rule.request
		}
		return s.push(rule.children[0])
	}

	child := f.child
	if rule.kind == lexRule {
		s.request = f.entryRequest
	}
	if child.miss != nil {
		if rule.kind == optionalRule && s.mark == f.entry && s.repairCount == f.entryRepairs && !child.repair {
			s.trim(f.entryTokens, f.entryRecipes, f.entryTakes)
			s.succeed(nil)
			return nil
		}
		switch rule.kind {
		case bindRule, dropRule, makeRule:
			child.output, child.kept = nil, 0
		}
		s.complete(child)
		return nil
	}

	interval := tokenInterval{f.entryTokens, len(s.tokens)}
	switch rule.kind {
	case bindRule:
		child.output = captured(rule.name, interval, child.output)
	case dropRule:
		child.output = dropped(child.output, interval)
	case makeRule:
		child.output = s.recordNode(rule.name, rule.nodeKind, f.entry.Byte, interval, child.output)
	case scopeRule:
		if s.frameStates != nil {
			s.state = s.frameStates[len(s.frames)-1]
		}
	}
	s.complete(child)
	return nil
}
