package parser

import (
	"fmt"
	"slices"

	"github.com/xiam/leafkit/tree"
)

// Grammar names the start rule, the rules it can call, and its default request.
type Grammar struct {
	Start   string
	Rules   map[string]Rule
	Default Request
}

// Limits bounds depth, work, token-free work, virtual runs and replay distance.
// Zero fields select finite defaults.
type Limits struct {
	Depth, ZeroMoves, VirtualTokens int
	Steps, RetainedBytes            int
	Descriptors, ForestEdges        int
}

type site struct {
	*definition
	children []int
}

// Engine is an immutable configured grammar. It can be shared across sessions
// when its callbacks and factories can be called concurrently.
type Engine[N tree.Node] struct {
	start       int
	named       map[string]int
	sites       []site
	request     Request
	build       Builders[N]
	limits      Limits
	generalized []bool
}

// Configure checks rule references, declarations and progress before accepting
// a grammar. The caller supplies pure callbacks and constructors.
func Configure[N tree.Node](g Grammar, build Builders[N], limits Limits) (*Engine[N], error) {
	if build.Node == nil || build.File == nil {
		return nil, grammarError("both node and file factories are required")
	}
	if build.Error == nil {
		if _, ok := any((*ErrorNode)(nil)).(N); !ok {
			return nil, grammarError("an error factory is required for this node type")
		}
		build.Error = func(slot string, problem Problem) (N, error) {
			return any(&ErrorNode{errorLeaf: tree.NewError(problem.Span, "unexpected input"), slot: slot, problem: problem}).(N), nil
		}
	}
	limits, err := checkLimits(limits)
	if err != nil {
		return nil, err
	}
	if g.Start == "" || g.Rules[g.Start].definition == nil {
		return nil, grammarError("missing start rule %q", g.Start)
	}
	if g.Default.Layout > Joined {
		return nil, grammarError("unknown default layout %d", g.Default.Layout)
	}

	c := compiler{named: make(map[string]int), compiled: make(map[*definition]int)}
	names := make([]string, 0, len(g.Rules))
	for name := range g.Rules {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if name == "" {
			return nil, grammarError("empty rule name")
		}
		index, err := c.compile(g.Rules[name], g.Rules)
		if err != nil {
			return nil, fmt.Errorf("rule %q: %w", name, err)
		}
		c.named[name] = index
	}
	generalized := c.generalizedSites()
	if err := c.checkProgress(generalized); err != nil {
		return nil, err
	}
	start := c.named[g.Start]
	if len(generalized) != 0 && generalized[start] && build.Ambiguity == nil {
		_, supportsDefault := any((*tree.Ambiguity)(nil)).(N)
		if !supportsDefault && c.reachesAll(start, generalized) {
			return nil, grammarError("an ambiguity factory is required for this node type")
		}
		if supportsDefault {
			build.Ambiguity = func(_ string, span tree.Span, choices []N) (N, error) {
				children := make([]tree.Node, 0, len(choices))
				for _, child := range choices {
					children = append(children, child)
				}
				return any(tree.NewAmbiguity(span, children...)).(N), nil
			}
		}
	}
	return &Engine[N]{
		start: start, named: c.named, sites: c.sites, request: g.Default,
		build: build, limits: limits, generalized: generalized,
	}, nil
}

func checkLimits(limits Limits) (Limits, error) {
	for _, limit := range []struct {
		value    *int
		fallback int
	}{
		{&limits.Depth, 65536},
		{&limits.ZeroMoves, 262144},
		{&limits.VirtualTokens, 1024},
		{&limits.Steps, 10000000},
		{&limits.RetainedBytes, 16 << 20},
		{&limits.Descriptors, 250000},
		{&limits.ForestEdges, 1000000},
	} {
		switch {
		case *limit.value < 0:
			return Limits{}, grammarError("negative limit")
		case *limit.value == 0:
			*limit.value = limit.fallback
		}
	}
	return limits, nil
}

type compiler struct {
	named    map[string]int
	sites    []site
	compiled map[*definition]int
}

func (c *compiler) compile(rule Rule, named map[string]Rule) (int, error) {
	definition := rule.definition
	if definition == nil {
		return 0, grammarError("zero rule")
	}
	if index, ok := c.compiled[definition]; ok {
		return index, nil
	}
	if err := checkDefinition(definition, named); err != nil {
		return 0, err
	}
	children := make([]int, len(definition.children))
	for i, child := range definition.children {
		index, err := c.compile(child, named)
		if err != nil {
			return 0, err
		}
		children[i] = index
	}
	index := len(c.sites)
	c.sites = append(c.sites, site{definition, children})
	c.compiled[definition] = index
	return index, nil
}

func checkDefinition(d *definition, named map[string]Rule) error {
	switch d.kind {
	case programRule:
		return checkProgram(d.name, d.states, named)
	case refRule:
		if named[d.name].definition == nil {
			return grammarError("undefined rule %q", d.name)
		}
	case makeRule:
		if d.nodeKind == "" {
			return grammarError("empty kind in slot %q", d.name)
		}
	case lexRule:
		if d.request.Layout > Joined {
			return grammarError("unknown layout %d", d.request.Layout)
		}
	case recoverRule:
		if d.name == "" {
			return grammarError("recovery requires a slot")
		}
		if d.boundary.Goal != nil && d.boundary.Goal.Layout > Joined {
			return grammarError("unknown recovery layout %d", d.boundary.Goal.Layout)
		}
		for _, pattern := range d.boundary.At {
			if pattern == (Pattern{}) {
				return grammarError("empty recovery boundary pattern")
			}
		}
	case insertRule:
		if d.pattern.kind == "" || d.pattern.text || d.pattern.end || d.pattern.sameLine || d.allowed == nil {
			return grammarError("insertion requires a fixed token kind and an allowance callback")
		}
	case updateRule:
		if d.update == nil {
			return grammarError("update requires a state callback")
		}
	case guardRule:
		if d.guard == nil {
			return grammarError("guard requires a predicate")
		}
	case withLexRule:
		if d.compute == nil {
			return grammarError("WithLex requires a request callback")
		}
	case expressionRule:
		return checkOperators(d.operators)
	case choiceRule:
		choice := d.choice
		switch {
		case choice.Policy > Longest || len(d.children) == 0:
			return grammarError("choice requires a known policy and alternatives")
		case choice.Policy == First && (choice.Lookahead < 0 || choice.Filter != nil || choice.Slot != ""):
			return grammarError("First requires nonnegative lookahead and has no slot or filter")
		case choice.Policy != First && choice.Lookahead != 0:
			return grammarError("lookahead applies only to First")
		case (choice.Policy == All || choice.Filter != nil) && choice.Slot == "":
			return grammarError("a packed or filtered choice requires a slot")
		}
		if choice.Slot != "" {
			for _, child := range d.children {
				if arm := child.definition; arm != nil && arm.kind == makeRule && arm.name != choice.Slot {
					return grammarError("choice slot %q has a direct alternative in slot %q", choice.Slot, arm.name)
				}
			}
		}
	case tokenRule, atRule, notRule:
		if d.pattern == (Pattern{}) {
			return grammarError("empty token pattern")
		}
	}
	return nil
}

func checkProgram(entry string, states map[string]ProgramState, named map[string]Rule) error {
	if _, ok := states[entry]; entry == "" || !ok {
		return grammarError("undefined program entry %q", entry)
	}
	for label, state := range states {
		if label == "" || (state.Step == nil) == (state.Control == nil) {
			return grammarError("state %q requires exactly one callback", label)
		}
		if state.Control != nil && len(state.Takes) > 0 {
			return grammarError("control %q declares a token take", label)
		}
		for _, targets := range [][]string{state.Takes, state.Jumps, state.ReturnsTo} {
			for _, target := range targets {
				if _, ok := states[target]; !ok {
					return grammarError("state %q transfers to undefined label %q", label, target)
				}
			}
		}
		for _, callee := range state.Calls {
			if named[callee].definition == nil {
				return grammarError("state %q calls undefined rule %q", label, callee)
			}
		}
		if (len(state.Calls) == 0) != (len(state.ReturnsTo) == 0) {
			return grammarError("state %q must declare calls and resume labels together", label)
		}
	}
	return nil
}

// Compute visible empty paths and possible empty paths independently. Program
// callbacks can return without a token. Static analysis cannot prove which
// callback paths they use, so repetitions also check progress at runtime.
func (c *compiler) nullable(possible bool) []bool {
	nullable := make([]bool, len(c.sites))
	for changed := true; changed; {
		changed = false
		for i, rule := range c.sites {
			if nullable[i] {
				continue
			}
			var empty bool
			switch rule.kind {
			case programRule:
				empty = possible
			case tokenRule:
				empty = rule.pattern.end && !rule.pattern.sameLine
			case atRule, notRule, optionalRule, manyRule, separatedRule, insertRule:
				empty = true
			case refRule:
				empty = nullable[c.named[rule.name]]
			case expressionRule:
				empty = possible && nullable[rule.children[0]]
			case recoverRule:
				empty = possible || nullable[rule.children[0]]
			case bindRule, dropRule, makeRule, lexRule, updateRule, guardRule, scopeRule:
				empty = nullable[rule.children[0]]
			case sequenceRule, withLexRule:
				empty = true
				for _, child := range rule.children {
					empty = empty && nullable[child]
				}
			case choiceRule:
				for _, child := range rule.children {
					empty = empty || nullable[child]
				}
			}
			nullable[i] = empty
			changed = changed || empty
		}
	}
	return nullable
}

func (c *compiler) checkProgress(generalized []bool) error {
	visible := c.nullable(false)
	possible := c.nullable(true)
	edges := make([][]int, len(c.sites))
	for i, rule := range c.sites {
		switch rule.kind {
		case expressionRule:
			if visible[rule.children[0]] {
				return grammarError("expression at site %d has an empty atom", i)
			}
		case manyRule, separatedRule:
			for _, child := range rule.children {
				if visible[child] {
					return grammarError("repetition at site %d has an empty operand", i)
				}
			}
		}
		edges[i] = c.leftCorners(rule, possible)
	}
	if len(generalized) == 0 {
		return checkCycles(edges)
	}
	return c.checkChartCycles(edges, possible, generalized)
}

func (c *compiler) leftCorners(rule site, nullable []bool) []int {
	switch rule.kind {
	case refRule:
		return []int{c.named[rule.name]}
	case programRule:
		return c.programLeftCorners(rule, nullable)
	case sequenceRule, withLexRule:
		for i, child := range rule.children {
			if !nullable[child] {
				return rule.children[:i+1]
			}
		}
		return rule.children
	case expressionRule:
		// Each tail starts after a consumed opener.
		return rule.children[:1]
	case separatedRule:
		if !nullable[rule.children[0]] {
			return rule.children[:1]
		}
	}
	return rule.children
}

func (c *compiler) programLeftCorners(rule site, nullable []bool) []int {
	var edges []int
	seen := make(map[string]bool)
	pending := []string{rule.name}
	for len(pending) > 0 {
		label := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[label] {
			continue
		}
		seen[label] = true
		state := rule.states[label]
		pending = append(pending, state.Jumps...)
		canResume := false
		for _, name := range state.Calls {
			callee := c.named[name]
			edges = append(edges, callee)
			canResume = canResume || nullable[callee]
		}
		if canResume {
			pending = append(pending, state.ReturnsTo...)
		}
	}
	return edges
}

func checkCycles(edges [][]int) error {
	indegree := make([]int, len(edges))
	for _, neighbors := range edges {
		for _, neighbor := range neighbors {
			indegree[neighbor]++
		}
	}
	pending := make([]int, 0, len(edges))
	for i, degree := range indegree {
		if degree == 0 {
			pending = append(pending, i)
		}
	}
	for i := 0; i < len(pending); i++ {
		for _, neighbor := range edges[pending[i]] {
			indegree[neighbor]--
			if indegree[neighbor] == 0 {
				pending = append(pending, neighbor)
			}
		}
	}
	if len(pending) != len(edges) {
		return grammarError("deterministic left-corner recursion")
	}
	return nil
}

func declaredMove(state ProgramState, move Move) bool {
	switch move.kind {
	case takeMove:
		return slices.Contains(state.Takes, move.next)
	case jumpMove, stateMove:
		return slices.Contains(state.Jumps, move.next)
	case callMove:
		return slices.Contains(state.Calls, move.rule) && slices.Contains(state.ReturnsTo, move.next)
	case returnMove, rejectMove:
		return true
	default:
		return false
	}
}
