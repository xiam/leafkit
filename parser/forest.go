package parser

import (
	"fmt"
	"slices"

	"github.com/xiam/leafkit/tree"
)

// A tape is a shared sequence of source records. A pack exposes its tape
// only when all of its readings agree on those records.
type chartTape struct {
	left, right *chartTape
	records     []Token
	inserted    []int
	pack        *chartNode
	window      *chartTapeWindow
	cached      bool
	common      bool
}

// A window projects directly owned tokens around retained diagnostics.
type chartTapeWindow struct {
	tape       *chartTape
	start, end int
}

func tapeWindow(tape *chartTape, start, end int) *chartTape {
	if tape == nil || start >= end {
		return nil
	}
	return &chartTape{window: &chartTapeWindow{tape: tape, start: start, end: end}}
}

func chartConcat(left, right *chartTape) *chartTape {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return &chartTape{left: left, right: right}
}

func tapeOf(tokens []Token) *chartTape {
	if len(tokens) == 0 {
		return nil
	}
	return &chartTape{records: tokens, cached: true, common: true}
}

func (t *chartTape) flatten() ([]Token, bool) {
	if t == nil {
		return nil, true
	}
	if t.cached {
		return t.records, t.common
	}
	t.cached = true
	if t.window != nil {
		records, common := t.window.tape.flatten()
		if !common {
			return nil, false
		}
		start := 0
		for start < len(records) && records[start].Span.Start < t.window.start {
			start++
		}
		end := start
		for end < len(records) && records[end].Span.End <= t.window.end {
			end++
		}
		t.records = records[start:end:end]
	} else if t.pack != nil {
		for i, arm := range t.pack.arms {
			records, common := arm.tape.flatten()
			if !common || i > 0 && !slices.EqualFunc(t.records, records, equalTokens) {
				t.records = nil
				return nil, false
			}
			t.records = records
		}
	} else {
		left, leftCommon := t.left.flatten()
		right, rightCommon := t.right.flatten()
		if !leftCommon || !rightCommon {
			return nil, false
		}
		t.records = make([]Token, 0, len(left)+len(right))
		t.records = append(t.records, left...)
		t.records = append(t.records, right...)
	}
	t.common = true
	return t.records, true
}

// Recovery diagnostics follow one stable reading. Virtual tokens emitted
// by Source remain in the diagnostic. Grammar insertions are omitted.
func (t *chartTape) sourceRecords(dst []Token) []Token {
	if t == nil {
		return dst
	}
	switch {
	case t.pack != nil:
		return firstChartAlternative(t.pack.arms).tape.sourceRecords(dst)
	case t.left != nil || t.right != nil:
		dst = t.left.sourceRecords(dst)
		return t.right.sourceRecords(dst)
	default:
		start := 0
		for _, index := range t.inserted {
			dst = append(dst, t.records[start:index]...)
			start = index + 1
		}
		return append(dst, t.records[start:]...)
	}
}

func (t *chartTape) sourceOrder(order []int) []int {
	if t == nil {
		return order
	}
	if t.pack != nil {
		arm := firstChartAlternative(t.pack.arms)
		order = append(order, arm.arm)
		order = append(order, arm.order...)
		return arm.tape.sourceOrder(order)
	}
	order = t.left.sourceOrder(order)
	return t.right.sourceOrder(order)
}

func equalTokens(a, b Token) bool {
	return a.Kind == b.Kind && a.Text == b.Text && a.Span == b.Span &&
		a.BreakBefore == b.BreakBefore && a.Synthetic == b.Synthetic && slices.Equal(a.Leading, b.Leading)
}

type chartOutput struct {
	kind        outputKind
	left, right *chartOutput
	name        string
	tape        *chartTape
	node        *chartNode
}

type chartNode struct {
	slot     string
	kind     tree.Kind
	span     tree.Span
	body     *chartOutput
	problem  *Problem
	hasError bool
	arms     []chartAlternative
}

type chartAlternative struct {
	arm   int
	order []int
	node  *chartNode
	tape  *chartTape
}

func chartJoin(left, right *chartOutput) *chartOutput {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return &chartOutput{kind: joinedOutput, left: left, right: right}
}

func chartCapture(name string, result chartResult) *chartOutput {
	return &chartOutput{kind: capturedOutput, name: name, left: result.out, tape: result.tape}
}

func chartNodeOutput(node *chartNode, tape *chartTape) *chartOutput {
	return &chartOutput{kind: nodeOutput, node: node, tape: tape}
}

func chartVisit(root *chartOutput, captures bool, visit func(*chartOutput)) {
	pending := []*chartOutput{root}
	for len(pending) != 0 {
		last := len(pending) - 1
		out := pending[last]
		pending = pending[:last]
		if out == nil {
			continue
		}
		if out.kind == joinedOutput || captures && out.kind == capturedOutput {
			pending = append(pending, out.right, out.left)
		} else {
			visit(out)
		}
	}
}

func chartRoot(out *chartOutput) (*chartNode, bool) {
	var root *chartNode
	valid := true
	chartVisit(out, true, func(piece *chartOutput) {
		if piece.kind == nodeOutput {
			valid = valid && root == nil
			root = piece.node
		}
	})
	return root, valid && root != nil
}

func chartOrder(left, right []int, boundary Mark) []int {
	out := make([]int, 0, len(left)+len(right)+2)
	out = append(out, left...)
	out = append(out, boundary.Byte, boundary.Virtual)
	return append(out, right...)
}

func sortChartAlternatives(arms []chartAlternative) {
	slices.SortStableFunc(arms, compareChartAlternatives)
}

// Build a transient deterministic Parts view. Packed children expose only
// their root kind and any token records on which all readings agree.
func chartParts(out *chartOutput) Parts {
	var parts Parts
	var convert func(*chartOutput) *output
	convert = func(piece *chartOutput) *output {
		if piece == nil {
			return nil
		}
		translated := &output{kind: piece.kind, name: piece.name}
		switch piece.kind {
		case joinedOutput:
			translated.left, translated.right = convert(piece.left), convert(piece.right)
		case capturedOutput:
			translated.left = convert(piece.left)
		case nodeOutput:
			root := piece.node
			for len(root.arms) == 1 {
				root = root.arms[0].node
			}
			translated.recipe = &recipe{kind: root.kind}
		}
		if piece.kind != joinedOutput {
			tokens, _ := piece.tape.flatten()
			translated.tokens = tokenInterval{len(parts.tokens), len(parts.tokens) + len(tokens)}
			parts.tokens = append(parts.tokens, tokens...)
		}
		return translated
	}
	parts.output = convert(out)
	return parts
}

func convertChartOutput(out *output, tokens []Token, nodes map[*recipe]*chartNode) *chartOutput {
	if out == nil {
		return nil
	}
	if out.kind == joinedOutput {
		return chartJoin(convertChartOutput(out.left, tokens, nodes), convertChartOutput(out.right, tokens, nodes))
	}
	converted := &chartOutput{kind: out.kind, name: out.name, tape: tapeOf(out.tokens.from(tokens))}
	switch out.kind {
	case capturedOutput:
		converted.left = convertChartOutput(out.left, tokens, nodes)
	case nodeOutput:
		node := nodes[out.recipe]
		if node == nil {
			r := out.recipe
			node = &chartNode{slot: r.slot, kind: r.kind, span: r.span, problem: r.problem, hasError: out.hasError}
			node.body = convertChartOutput(r.output, tokens, nodes)
			nodes[r] = node
		}
		converted.node = node
	}
	return converted
}

type chartFile struct {
	out *chartOutput
	end Read
}

type forestBuilder[N tree.Node] struct {
	engine *Engine[N]
	nodes  map[*chartNode]N
	active map[*chartNode]bool
	depth  int
}

func (b *forestBuilder[N]) node(r *chartNode) (N, error) {
	if node, ok := b.nodes[r]; ok {
		return node, nil
	}
	var zero N
	if b.active[r] {
		return zero, grammarError("zero-yield cycle in ambiguity forest")
	}
	if b.depth == b.engine.limits.Depth {
		return zero, limitError("depth", b.engine.limits.Depth)
	}
	b.depth++
	b.active[r] = true
	node, err := b.constructNode(r)
	delete(b.active, r)
	b.depth--
	if err == nil {
		b.nodes[r] = node
	}
	return node, err
}

func (b *forestBuilder[N]) constructNode(r *chartNode) (N, error) {
	var zero N
	switch {
	case r.arms != nil:
		if len(r.arms) == 1 {
			return b.node(r.arms[0].node)
		}
		sortChartAlternatives(r.arms)
		arms := make([]N, 0, len(r.arms))
		for _, arm := range r.arms {
			node, err := b.node(arm.node)
			if err != nil {
				return zero, err
			}
			arms = append(arms, node)
		}
		return b.ambiguity(r.slot, r.span, arms)
	case r.problem != nil:
		node, err := b.engine.build.Error(r.slot, *r.problem)
		if err == nil {
			err = checkErrorNode(node, r.span)
		}
		if err != nil {
			return zero, fmt.Errorf("%w in recovery slot %q: %w", ErrBuild, r.slot, err)
		}
		return node, nil
	}

	var err error
	in := Build[N]{Slot: r.slot, Kind: r.kind, Span: r.span}
	add := func(field *Field[N], piece *chartOutput) {
		if err != nil {
			return
		}
		switch piece.kind {
		case nodeOutput:
			var node N
			node, err = b.node(piece.node)
			field.Nodes = append(field.Nodes, node)
		case tokenOutput:
			tokens, _ := piece.tape.flatten()
			in.Tokens.tokens = append(in.Tokens.tokens, tokens...)
		}
	}
	chartVisit(r.body, false, func(piece *chartOutput) {
		if piece.kind == tokenOutput {
			add(nil, piece)
			return
		}
		tokens, _ := piece.tape.flatten()
		field := Field[N]{Name: piece.name, Tokens: TokenRange{tokens}}
		if piece.kind == capturedOutput {
			chartVisit(piece.left, true, func(child *chartOutput) { add(&field, child) })
		} else {
			add(&field, piece)
		}
		in.Fields = append(in.Fields, field)
	})
	if err != nil {
		return zero, err
	}
	node, err := b.engine.build.Node(in)
	if err != nil {
		return zero, fmt.Errorf("%w in slot %q: %w", ErrBuild, r.slot, err)
	}
	return node, nil
}

func (b *forestBuilder[N]) ambiguity(slot string, span tree.Span, choices []N) (N, error) {
	var zero N
	if b.engine.build.Ambiguity == nil {
		return zero, fmt.Errorf("%w: missing ambiguity factory for slot %q", ErrBuild, slot)
	}
	for _, choice := range choices {
		if err := checkConstructedNode(choice, span); err != nil {
			return zero, fmt.Errorf("%w in ambiguity alternative for slot %q: %w", ErrBuild, slot, err)
		}
	}
	node, err := b.engine.build.Ambiguity(slot, span, choices)
	if err == nil {
		err = checkAmbiguityNode(node, span, choices)
	}
	if err != nil {
		return zero, fmt.Errorf("%w in ambiguity slot %q: %w", ErrBuild, slot, err)
	}
	return node, nil
}

func (b *forestBuilder[N]) file(out *chartOutput, span tree.Span, end Read) (N, error) {
	var err error
	var children []N
	chartVisit(out, true, func(piece *chartOutput) {
		if piece.kind == nodeOutput && err == nil {
			var node N
			node, err = b.node(piece.node)
			children = append(children, node)
		}
	})
	var zero N
	if err != nil {
		return zero, err
	}
	root, err := b.engine.build.File(span, children, slices.Clone(end.Trailing))
	if err != nil {
		return zero, fmt.Errorf("%w building file: %w", ErrBuild, err)
	}
	return root, nil
}

func (s *Session[N]) constructForest(files []chartFile) (N, error) {
	b := forestBuilder[N]{
		engine: s.engine, nodes: make(map[*chartNode]N), active: make(map[*chartNode]bool),
	}
	var zero N
	span := tree.Span{Source: s.sourceID, Start: s.start, End: files[0].end.Next.Byte}
	roots := make([]N, 0, len(files))
	for _, file := range files {
		if file.end.Next.Byte != span.End {
			return zero, fmt.Errorf("%w: final EOF byte differs across lexical streams", ErrSource)
		}
		root, err := b.file(file.out, span, file.end)
		if err != nil {
			return zero, err
		}
		if len(files) > 1 {
			if err := checkConstructedNode(root, span); err != nil {
				return zero, fmt.Errorf("%w building file alternative: %w", ErrBuild, err)
			}
		}
		roots = append(roots, root)
	}
	root := roots[0]
	if len(roots) > 1 {
		var err error
		root, err = b.ambiguity("file", span, roots)
		if err != nil {
			return zero, err
		}
	}
	if err := tree.Validate(root); err != nil {
		return zero, fmt.Errorf("%w validating file: %w", ErrBuild, err)
	}
	return root, nil
}

// An ordered policy selects a concrete local reading. Rebuild just the
// selected path through packs and preserve sharing along that reading.
type chartSelection struct {
	nodes map[*chartNode]*chartNode
	tapes map[*chartTape]*chartTape
}

func (s *chartSelection) tape(tape *chartTape) *chartTape {
	if tape == nil || tape.left == nil && tape.right == nil && tape.pack == nil && tape.window == nil {
		return tape
	}
	if selected, ok := s.tapes[tape]; ok {
		return selected
	}
	var selected *chartTape
	if tape.window != nil {
		selected = tapeWindow(s.tape(tape.window.tape), tape.window.start, tape.window.end)
	} else if tape.pack != nil {
		arm := firstChartAlternative(tape.pack.arms)
		selected = s.tape(arm.tape)
	} else {
		selected = chartConcat(s.tape(tape.left), s.tape(tape.right))
	}
	s.tapes[tape] = selected
	return selected
}

func (s *chartSelection) node(node *chartNode) *chartNode {
	if found := s.nodes[node]; found != nil {
		return found
	}
	var selected *chartNode
	if node.arms != nil {
		selected = s.node(firstChartAlternative(node.arms).node)
	} else {
		copy := *node
		copy.body = s.output(node.body)
		selected = node
		if copy.body != node.body {
			selected = &copy
		}
	}
	s.nodes[node] = selected
	return selected
}

func (s *chartSelection) output(out *chartOutput) *chartOutput {
	if out == nil {
		return nil
	}
	copy := *out
	copy.left, copy.right = s.output(out.left), s.output(out.right)
	copy.tape = s.tape(out.tape)
	if out.node != nil {
		copy.node = s.node(out.node)
	}
	if copy == *out {
		return out
	}
	return &copy
}

func selectChartReading(result chartResult) chartResult {
	selection := chartSelection{
		nodes: make(map[*chartNode]*chartNode), tapes: make(map[*chartTape]*chartTape),
	}
	result.out = selection.output(result.out)
	result.tape = selection.tape(result.tape)
	return result
}

func firstChartAlternative(arms []chartAlternative) chartAlternative {
	best := arms[0]
	for _, arm := range arms[1:] {
		if compareChartAlternatives(arm, best) < 0 {
			best = arm
		}
	}
	return best
}

func compareChartAlternatives(a, b chartAlternative) int {
	if a.arm != b.arm {
		return a.arm - b.arm
	}
	if order := slices.Compare(a.order, b.order); order != 0 {
		return order
	}
	return slices.Compare(chartNodeRank(nil, a.node), chartNodeRank(nil, b.node))
}

// Rank is needed only for otherwise equal local splits or for results from
// different lexical states at the same position. It uses production order,
// never a source key or the order in which reads became available.
func chartRank(rank []int, out *chartOutput) []int {
	chartVisit(out, true, func(piece *chartOutput) {
		if piece.kind == nodeOutput {
			rank = chartNodeRank(rank, piece.node)
		}
	})
	return rank
}

func chartNodeRank(rank []int, node *chartNode) []int {
	if len(node.arms) != 0 {
		arm := firstChartAlternative(node.arms)
		rank = append(rank, arm.arm)
		rank = append(rank, arm.order...)
		return chartNodeRank(rank, arm.node)
	}
	return chartRank(rank, node.body)
}
