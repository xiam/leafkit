package parser

import (
	"slices"

	"github.com/xiam/leafkit/tree"
)

// State is the consumer's immutable equality key for recognition state.
// Equal keys must have the same meaning to all grammar callbacks.
type State string

// View describes the local results available when a step runs.
// Parts is valid for the duration of the callback.
type View struct {
	State State
	Parts Parts
}

// Step observes one offered event. It returns a move and must not perform
// external effects. A step can run on a branch that is later abandoned.
type Step func(Look, View) Move

// ProgramState has exactly one callback. The lists declare its permitted
// label transfers and named callees. Return and Reject are always permitted.
type ProgramState struct {
	Step                           Step
	Control                        func(View) Move
	Takes, Jumps, ReturnsTo, Calls []string
}

type moveKind uint8

const (
	noMove moveKind = iota
	takeMove
	jumpMove
	stateMove
	callMove
	returnMove
	rejectMove
)

// Move describes one transition in a hand-written program.
type Move struct {
	kind             moveKind
	next, rule, bind string
	expected         []Pattern
	state            State
	request          *Request
}

func Take(next, bind string) Move { return Move{kind: takeMove, next: next, bind: bind} }
func Jump(next string) Move       { return Move{kind: jumpMove, next: next} }

// SetState replaces the branch state and transfers to a declared Jumps
// label without consuming input.
func SetState(next string, state State) Move {
	return Move{kind: stateMove, next: next, state: state}
}

func Call(rule, resume, bind string) Move {
	return Move{kind: callMove, rule: rule, next: resume, bind: bind}
}

// CallWithLex scopes request over one declared named call. On return
// the caller continues under its previous request.
func CallWithLex(rule, resume, bind string, request Request) Move {
	move := Call(rule, resume, bind)
	move.request = &request
	return move
}

func Return() Move                    { return Move{kind: returnMove} }
func Reject(expected ...Pattern) Move { return Move{kind: rejectMove, expected: expected} }

type ruleKind uint8

const (
	programRule ruleKind = iota
	tokenRule
	refRule
	sequenceRule
	optionalRule
	manyRule
	separatedRule
	bindRule
	dropRule
	makeRule
	atRule
	notRule
	lexRule
	choiceRule
	expressionRule
	recoverRule
	insertRule
	updateRule
	guardRule
	scopeRule
	withLexRule
)

// Rule is an immutable grammar fragment. Its zero value is invalid.
type Rule struct{ definition *definition }

type definition struct {
	kind      ruleKind
	children  []Rule
	name      string
	nodeKind  tree.Kind
	pattern   Pattern
	request   Request
	choice    ChoiceSpec
	trailing  bool
	states    map[string]ProgramState
	operators []expressionOperator
	boundary  Boundary
	allowed   func(Look, View) bool
	update    func(View) State
	guard     func(View) bool
	compute   func(View) Request
}

// Program packages named Go steps as a rule. It copies the declarations.
func Program(entry string, states map[string]ProgramState) Rule {
	copied := make(map[string]ProgramState, len(states))
	for label, state := range states {
		state.Takes = append([]string(nil), state.Takes...)
		state.Jumps = append([]string(nil), state.Jumps...)
		state.ReturnsTo = append([]string(nil), state.ReturnsTo...)
		state.Calls = append([]string(nil), state.Calls...)
		copied[label] = state
	}
	return Rule{&definition{kind: programRule, name: entry, states: copied}}
}

func Tok(pattern Pattern) Rule { return Rule{&definition{kind: tokenRule, pattern: pattern}} }
func Ref(name string) Rule     { return Rule{&definition{kind: refRule, name: name}} }
func Seq(rules ...Rule) Rule {
	return Rule{&definition{kind: sequenceRule, children: append([]Rule(nil), rules...)}}
}
func Opt(body Rule) Rule  { return Rule{&definition{kind: optionalRule, children: []Rule{body}}} }
func Many(body Rule) Rule { return Rule{&definition{kind: manyRule, children: []Rule{body}}} }
func Sep(item, separator Rule, trailing bool) Rule {
	return Rule{&definition{kind: separatedRule, children: []Rule{item, separator}, trailing: trailing}}
}
func Bind(name string, body Rule) Rule {
	return Rule{&definition{kind: bindRule, name: name, children: []Rule{body}}}
}
func Drop(body Rule) Rule { return Rule{&definition{kind: dropRule, children: []Rule{body}}} }
func Make(slot string, kind tree.Kind, body Rule) Rule {
	return Rule{&definition{kind: makeRule, name: slot, nodeKind: kind, children: []Rule{body}}}
}
func At(pattern Pattern) Rule  { return Rule{&definition{kind: atRule, pattern: pattern}} }
func Not(pattern Pattern) Rule { return Rule{&definition{kind: notRule, pattern: pattern}} }
func Lex(request Request, body Rule) Rule {
	return Rule{&definition{kind: lexRule, request: request, children: []Rule{body}}}
}

// WithLex runs prefix under the enclosing request. It computes a request
// from the prefix's matched parts and exit state, then scopes it over body.
// Both rules' output and the body's exit mark and state pass to the caller.
// The callback is pure and its View is valid only during the call.
func WithLex(prefix Rule, compute func(View) Request, body Rule) Rule {
	return Rule{&definition{kind: withLexRule, children: []Rule{prefix, body}, compute: compute}}
}

// Update runs body and replaces its exit state with the pure callback's
// result. The view offers the body's exit state and matched parts. An
// empty Seq is a token-free body.
func Update(body Rule, update func(View) State) Rule {
	return Rule{&definition{kind: updateRule, children: []Rule{body}, update: update}}
}

// Guard keeps a successful body when the pure predicate accepts its exit
// state and matched parts. Rejection reports a miss at the body's exit.
func Guard(body Rule, accept func(View) bool) Rule {
	return Rule{&definition{kind: guardRule, children: []Rule{body}, guard: accept}}
}

// Scope runs body with the current state and restores its entry state
// when it returns. Its matched parts and consumed tokens still propagate.
func Scope(body Rule) Rule {
	return Rule{&definition{kind: scopeRule, children: []Rule{body}}}
}

// Boundary describes where recovery stops. Final EOF always stops the
// scan. Consume includes a matching token in the discarded region. Goal
// overrides the recovery scope's inherited request when set.
type Boundary struct {
	At      []Pattern
	Consume bool
	Goal    *Request
}

// Recover replaces a failed body with an error node at slot.
func Recover(slot string, body Rule, sync Boundary) Rule {
	sync.At = slices.Clone(sync.At)
	if sync.Goal != nil {
		goal := *sync.Goal
		sync.Goal = &goal
	}
	return Rule{&definition{kind: recoverRule, children: []Rule{body}, name: slot, boundary: sync}}
}

// Insert records a synthetic, zero-width token with a fixed kind when
// allowed accepts the offered event. It consumes no source token.
func Insert(expected Pattern, allowed func(Look, View) bool) Rule {
	return Rule{&definition{kind: insertRule, pattern: expected, allowed: allowed}}
}

// Policy controls how a choice commits to one of its alternatives.
type Policy uint8

const (
	// First tries alternatives in declaration order with bounded lookahead.
	First Policy = iota
	// All continues every successful reading and packs equivalent exits.
	All
	// Longest takes the longest successful local reading.
	Longest
)

// Candidate is a local reduction offered to a pure choice filter.
// Parts and its recognition views are valid during the callback.
type Candidate struct {
	Kind          tree.Kind
	Span          tree.Span
	Parts         Parts
	Before, After State
}

// ChoiceSpec sets the token budget before First commits. Zero selects one
// token. Success before the budget is exhausted also commits.
type ChoiceSpec struct {
	Policy    Policy
	Lookahead int
	Slot      string
	Filter    func(Candidate) bool
}

func Choose(spec ChoiceSpec, alternatives ...Rule) Rule {
	return Rule{&definition{kind: choiceRule, choice: spec, children: append([]Rule(nil), alternatives...)}}
}
