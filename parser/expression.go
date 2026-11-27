package parser

import "github.com/xiam/leafkit/tree"

// Fixity specifies where an operator appears relative to its operands.
type Fixity uint8

const (
	Prefix Fixity = iota
	Infix
	Postfix
)

// Assoc controls how an infix operator groups with infix operators of equal power.
type Assoc uint8

const (
	Left Assoc = iota
	Right
	NonAssoc
)

// Operator declares one expression reduction. Higher Power binds more
// tightly. Assoc applies only to Infix. Tail applies only to Postfix
// and starts after the matched opener. Goal overrides the request for
// matching the operator token alone.
type Operator struct {
	Form  Fixity
	Match Pattern
	Power int
	Assoc Assoc
	Kind  tree.Kind
	Tail  Rule
	Goal  *Request
}

type expressionOperator struct {
	Operator
	tailChild int
}

// Expr recognizes an atom with prefix, infix and postfix operators.
// Operators are tried in declaration order among eligible forms and powers.
// Reductions record slot and kind with named fields. A bare atom retains
// its original output.
func Expr(slot string, atom Rule, ops ...Operator) Rule {
	d := &definition{kind: expressionRule, name: slot, children: []Rule{atom}}
	d.operators = make([]expressionOperator, len(ops))
	for i, op := range ops {
		if op.Goal != nil {
			goal := *op.Goal
			op.Goal = &goal
		}
		var tailChild int
		if op.Tail.definition != nil {
			tailChild = len(d.children)
			d.children = append(d.children, op.Tail)
		}
		d.operators[i] = expressionOperator{Operator: op, tailChild: tailChild}
	}
	return Rule{d}
}

func checkOperators(ops []expressionOperator) error {
	for i, op := range ops {
		switch {
		case op.Form > Postfix:
			return grammarError("operator %d has unknown fixity %d", i, op.Form)
		case op.Match.end || op.Match.kind == "" && !op.Match.text:
			return grammarError("operator %d requires a token pattern", i)
		case op.Power < 0:
			return grammarError("operator %d has negative power", i)
		case op.Kind == "":
			return grammarError("operator %d has an empty kind", i)
		case op.Form == Infix && op.Assoc > NonAssoc:
			return grammarError("operator %d has unknown association %d", i, op.Assoc)
		case op.Form != Postfix && op.tailChild != 0:
			return grammarError("operator %d has a tail outside postfix form", i)
		case op.Goal != nil && op.Goal.Layout > Joined:
			return grammarError("operator %d has unknown layout %d", i, op.Goal.Layout)
		}
	}
	return nil
}

type expressionPhase uint8

const (
	expressionStart expressionPhase = iota
	expressionAtom
	expressionContinue
	expressionOperand
	expressionTail
)

type bindingPower struct {
	minimum int
	strict  bool
}

func (p bindingPower) allows(power int) bool {
	return power > p.minimum || power == p.minimum && !p.strict
}

type expressionFrame struct {
	power       bindingPower
	phase       expressionPhase
	probe       int
	operator    int
	opToken     *output
	barredPower int // -1 until a non-associative infix reduces at this level.
}

func (s *Session[N]) expression(f *frame, rule site) (bool, error) {
	if f.expr == nil {
		f.expr = &expressionFrame{barredPower: -1}
	}
	e := f.expr
	if f.childReturned {
		if f.child.miss != nil {
			s.complete(result{miss: f.child.miss, repair: f.child.repair})
			return false, nil
		}
		switch e.phase {
		case expressionAtom:
			if s.mark == f.entry && s.repairCount == f.entryRepairs {
				return false, grammarError("expression atom succeeded without consuming a token")
			}
			f.output = f.child.output
		case expressionOperand, expressionTail:
			s.reduceExpression(f, rule, f.child.output)
		}
		e.phase = expressionContinue
		f.child, f.childReturned = result{}, false
	}

	start := e.phase == expressionStart
	for e.probe < len(rule.operators) {
		op := rule.operators[e.probe]
		if start != (op.Form == Prefix) || !start && !e.power.allows(op.Power) {
			e.probe++
			continue
		}
		request := s.request
		if op.Goal != nil {
			request = *op.Goal
		}
		read, err := s.readWithRequest(request)
		if err != nil || read.Status == NeedInput {
			return read.Status == NeedInput, err
		}
		look := s.look(read)
		if !Matches(look, op.Match) {
			e.probe++
			continue
		}
		if op.Form == Infix && op.Power == e.barredPower {
			s.reject(look, request, nil)
			return false, nil
		}

		e.operator, e.probe = e.probe, 0
		e.opToken = s.take(read)
		if op.Form == Postfix {
			if op.tailChild != 0 {
				e.phase = expressionTail
				return false, s.push(rule.children[op.tailChild])
			}
			s.reduceExpression(f, rule, nil)
			return false, nil
		}
		e.phase = expressionOperand
		power := bindingPower{minimum: op.Power, strict: op.Form == Infix && op.Assoc != Right}
		if err := s.push(f.site); err != nil {
			return false, err
		}
		s.frames[len(s.frames)-1].expr = &expressionFrame{power: power, barredPower: -1}
		return false, nil
	}

	if start {
		e.phase, e.probe = expressionAtom, 0
		return false, s.push(rule.children[0])
	}
	s.succeed(f.output)
	return false, nil
}

func (s *Session[N]) reduceExpression(f *frame, rule site, child *output) {
	e := f.expr
	op := rule.operators[e.operator]
	interval := tokenInterval{f.entryTokens, len(s.tokens)}
	operator := captured("operator", e.opToken.tokens, e.opToken)
	following := tokenInterval{e.opToken.tokens.end, interval.end}

	var fields *output
	if op.Form == Prefix {
		fields = join(operator, captured("operand", following, child))
	} else {
		left := captured("left", tokenInterval{interval.start, e.opToken.tokens.start}, f.output)
		fields = join(left, operator)
		if op.Form == Infix {
			fields = join(fields, captured("right", following, child))
			e.barredPower = -1
			if op.Assoc == NonAssoc {
				e.barredPower = op.Power
			}
		} else if op.tailChild != 0 {
			fields = join(fields, captured("suffix", following, child))
		}
	}
	f.output = s.recordNode(rule.name, op.Kind, f.entry.Byte, interval, fields)
	e.opToken = nil
}
