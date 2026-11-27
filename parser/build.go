package parser

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/xiam/leafkit/tree"
)

// Parts offers read-only access to named recognition results during a step.
type Parts struct {
	output *output
	tokens []Token
}

func (p Parts) Tokens(name string) []Token {
	var tokens []Token
	visit(p.output, func(piece *output) {
		if piece.name == name && (piece.kind == capturedOutput || piece.kind == nodeOutput) {
			tokens = append(tokens, piece.tokens.from(p.tokens)...)
		}
	})
	return tokens
}

func (p Parts) Kinds(name string) []tree.Kind {
	var kinds []tree.Kind
	visit(p.output, func(piece *output) {
		if piece.name != name {
			return
		}
		switch piece.kind {
		case nodeOutput:
			kinds = append(kinds, piece.recipe.kind)
		case capturedOutput:
			contents(piece.left, func(item *output) {
				if item.kind == nodeOutput {
					kinds = append(kinds, item.recipe.kind)
				}
			})
		}
	})
	return kinds
}

// TokenRange is an immutable sequence of matched token records.
type TokenRange struct{ tokens []Token }

func (r TokenRange) Len() int { return len(r.tokens) }
func (r TokenRange) At(index int) (Token, bool) {
	if index < 0 || index >= len(r.tokens) {
		return Token{}, false
	}
	return r.tokens[index], true
}

// Field is an ordered result. An empty Name denotes unnamed output.
type Field[N tree.Node] struct {
	Name   string
	Nodes  []N
	Tokens TokenRange
}

// Build describes one retained production. Tokens lists directly owned tokens,
// including dropped punctuation. Built children own their own token records.
type Build[N tree.Node] struct {
	Slot   string
	Kind   tree.Kind
	Span   tree.Span
	Fields []Field[N]
	Tokens TokenRange
}

// Builders construct read-only nodes on final success. Callbacks may retain
// their immutable inputs and must have no external effects.
type Builders[N tree.Node] struct {
	Node      func(Build[N]) (N, error)
	Error     func(slot string, problem Problem) (N, error)
	File      func(full tree.Span, children []N, trailing []Trivia) (N, error)
	Ambiguity func(slot string, span tree.Span, alternatives []N) (N, error)
}

type tokenInterval struct{ start, end int }

func (i tokenInterval) from(tokens []Token) []Token { return tokens[i.start:i.end:i.end] }

type outputKind uint8

const (
	joinedOutput outputKind = iota
	tokenOutput
	nodeOutput
	capturedOutput
)

// Output segments concatenate without copying. A wrapper can replace a whole
// segment with a recipe, a named capture, or just its dropped token interval.
type output struct {
	kind        outputKind
	hasNode     bool
	hasError    bool
	left, right *output
	tokens      tokenInterval
	name        string
	recipe      *recipe
}

type recipe struct {
	index   int
	slot    string
	kind    tree.Kind
	span    tree.Span
	output  *output
	problem *Problem
}

func join(left, right *output) *output {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return &output{kind: joinedOutput, hasNode: left.hasNode || right.hasNode, hasError: left.hasError || right.hasError, left: left, right: right}
}

func captured(name string, tokens tokenInterval, child *output) *output {
	return &output{kind: capturedOutput, hasNode: child != nil && child.hasNode, hasError: child != nil && child.hasError, name: name, tokens: tokens, left: child}
}

// visit unfolds only sequencing. Callers can also inspect the contents of a
// named capture without including the internals of a built child.
func visit(segment *output, yield func(*output)) {
	scanOutput(segment, false, yield)
}

func contents(segment *output, yield func(*output)) {
	scanOutput(segment, true, yield)
}

func scanOutput(segment *output, insideCaptures bool, yield func(*output)) {
	if segment == nil {
		return
	}
	pending := []*output{segment}
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		switch {
		case current.kind == joinedOutput:
			pending = append(pending, current.right, current.left)
		case insideCaptures && current.kind == capturedOutput:
			if current.left != nil {
				pending = append(pending, current.left)
			}
		default:
			yield(current)
		}
	}
}

// Dropping a production keeps its diagnostics and the tokens that would
// otherwise have been owned by its discarded nodes.
func dropped(body *output, tokens tokenInterval) *output {
	var out *output
	start := tokens.start
	pending := []*output{body}
	for len(pending) != 0 {
		last := len(pending) - 1
		piece := pending[last]
		pending = pending[:last]
		if piece == nil || !piece.hasError {
			continue
		}
		switch piece.kind {
		case joinedOutput, capturedOutput:
			pending = append(pending, piece.right, piece.left)
		case nodeOutput:
			if piece.recipe.problem == nil {
				pending = append(pending, piece.recipe.output)
				continue
			}
			if start < piece.tokens.start {
				out = join(out, &output{kind: tokenOutput, tokens: tokenInterval{start, piece.tokens.start}})
			}
			out = join(out, piece)
			start = piece.tokens.end
		}
	}
	if start < tokens.end || out == nil {
		out = join(out, &output{kind: tokenOutput, tokens: tokenInterval{start, tokens.end}})
	}
	return out
}

type fieldPlan struct {
	name   string
	nodes  []*recipe
	tokens tokenInterval
}

type buildPlan struct {
	fields []fieldPlan
	tokens []Token
}

func planBuild(segment *output, tape []Token) buildPlan {
	var plan buildPlan
	visit(segment, func(piece *output) {
		switch piece.kind {
		case tokenOutput:
			plan.tokens = append(plan.tokens, piece.tokens.from(tape)...)
		case nodeOutput:
			plan.fields = append(plan.fields, fieldPlan{nodes: []*recipe{piece.recipe}, tokens: piece.tokens})
		case capturedOutput:
			field := fieldPlan{name: piece.name, tokens: piece.tokens}
			contents(piece.left, func(item *output) {
				switch item.kind {
				case tokenOutput:
					plan.tokens = append(plan.tokens, item.tokens.from(tape)...)
				case nodeOutput:
					field.nodes = append(field.nodes, item.recipe)
				}
			})
			plan.fields = append(plan.fields, field)
		}
	})
	return plan
}

func (s *Session[N]) recordNode(slot string, kind tree.Kind, start int, tokens tokenInterval, body *output) *output {
	r := &recipe{
		index: len(s.recipes), slot: slot, kind: kind, output: body,
		span: tree.Span{Source: s.sourceID, Start: start, End: s.mark.Byte},
	}
	s.recipes = append(s.recipes, r)
	return &output{kind: nodeOutput, hasNode: true, hasError: body != nil && body.hasError, tokens: tokens, recipe: r}
}

func (s *Session[N]) recordError(slot string, problem Problem, tokens tokenInterval) *output {
	r := &recipe{index: len(s.recipes), slot: slot, kind: tree.KindError, span: problem.Span, problem: &problem}
	s.recipes = append(s.recipes, r)
	return &output{kind: nodeOutput, hasNode: true, hasError: true, tokens: tokens, recipe: r}
}

func checkConstructedNode(node tree.Node, span tree.Span) error {
	value := reflect.ValueOf(node)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("factory returned %T, expected a non-nil node pointer", node)
	}
	if node.Info().Span != span {
		return fmt.Errorf("node must have span %+v", span)
	}
	return nil
}

func checkAmbiguityNode[N tree.Node](node N, span tree.Span, choices []N) error {
	if err := checkConstructedNode(node, span); err != nil {
		return err
	}
	if node.Info().Kind != tree.KindAmbiguity {
		return fmt.Errorf("ambiguity node must have kind %q", tree.KindAmbiguity)
	}
	for index, want := range choices {
		if got, ok := node.Child(index); !ok || got != tree.Node(want) {
			return fmt.Errorf("ambiguity alternative %d must preserve the supplied child", index)
		}
	}
	if _, ok := node.Child(len(choices)); ok {
		return fmt.Errorf("ambiguity node has extra alternatives")
	}
	return nil
}

func checkErrorNode(node tree.Node, span tree.Span) error {
	value := reflect.ValueOf(node)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return fmt.Errorf("error factory returned %T, expected a non-nil node pointer", node)
	}
	if info := node.Info(); info.Kind != tree.KindError || info.Span != span {
		return fmt.Errorf("error node must have kind %q and diagnostic span %+v", tree.KindError, span)
	}
	if _, ok := node.Child(0); ok {
		return fmt.Errorf("error node must be a leaf")
	}
	return nil
}

func (s *Session[N]) construct(result *output, end Read) (N, error) {
	// Recipes are recorded after their descendants. Mark reachable recipes in
	// reverse order, then invoke factories in the recorded order.
	live := make([]bool, len(s.recipes))
	var fileChildren []*recipe
	contents(result, func(piece *output) {
		if piece.kind == nodeOutput {
			fileChildren = append(fileChildren, piece.recipe)
			live[piece.recipe.index] = true
		}
	})
	plans := make([]buildPlan, len(s.recipes))
	for i := len(s.recipes) - 1; i >= 0; i-- {
		if !live[i] {
			continue
		}
		plan := planBuild(s.recipes[i].output, s.tokens)
		for _, field := range plan.fields {
			for _, child := range field.nodes {
				live[child.index] = true
			}
		}
		plans[i] = plan
	}

	var zero N
	nodes := make([]N, len(s.recipes))
	for i, r := range s.recipes {
		if !live[i] {
			continue
		}
		if r.problem != nil {
			node, err := s.engine.build.Error(r.slot, *r.problem)
			if err == nil {
				err = checkErrorNode(node, r.span)
			}
			if err != nil {
				return zero, fmt.Errorf("%w in recovery slot %q: %w", ErrBuild, r.slot, err)
			}
			nodes[i] = node
			continue
		}
		plan := plans[i]
		fields := make([]Field[N], 0, len(plan.fields))
		for _, field := range plan.fields {
			fields = append(fields, Field[N]{
				Name: field.name, Nodes: builtNodes(nodes, field.nodes),
				Tokens: TokenRange{field.tokens.from(s.tokens)},
			})
		}
		node, err := s.engine.build.Node(Build[N]{
			Slot: r.slot, Kind: r.kind, Span: r.span, Fields: fields,
			Tokens: TokenRange{plan.tokens},
		})
		if err != nil {
			return zero, fmt.Errorf("%w in slot %q: %w", ErrBuild, r.slot, err)
		}
		nodes[i] = node
	}
	span := tree.Span{Source: s.sourceID, Start: s.start, End: end.Next.Byte}
	root, err := s.engine.build.File(span, builtNodes(nodes, fileChildren), slices.Clone(end.Trailing))
	if err != nil {
		return zero, fmt.Errorf("%w building file: %w", ErrBuild, err)
	}
	if err := tree.Validate(root); err != nil {
		return zero, fmt.Errorf("%w validating file: %w", ErrBuild, err)
	}
	return root, nil
}

func builtNodes[N tree.Node](nodes []N, recipes []*recipe) []N {
	result := make([]N, 0, len(recipes))
	for _, child := range recipes {
		result = append(result, nodes[child.index])
	}
	return result
}
