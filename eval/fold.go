package eval

import (
	"errors"
	"fmt"
	"slices"

	"github.com/xiam/leafkit/tree"
)

// Frame describes the current occurrence. Path is borrowed and read-only.
// Copy it before retaining it beyond the rule invocation.
type Frame struct {
	Node tree.Node
	Path tree.Path
}

// Descend evaluates a child with the supplied scope. Use it synchronously
// during the rule invocation. Each call evaluates the child again.
type Descend[R, V any] func(index int, env *Env[V]) (R, error)

// Rule computes a result for one occurrence and controls child evaluation.
type Rule[R, V any] func(frame Frame, env *Env[V], child Descend[R, V]) (R, error)

// Fold evaluates a valid root by dispatching on node kind. It visits only the
// children requested by rules and passes env through as supplied, including nil.
// The rules map must stay unchanged during the fold.
func Fold[R, V any](root tree.Node, env *Env[V], rules map[tree.Kind]Rule[R, V]) (R, error) {
	var visit func(tree.Node, tree.Path, *Env[V]) (R, error)
	visit = func(node tree.Node, path tree.Path, env *Env[V]) (R, error) {
		kind := node.Info().Kind
		rule := rules[kind]
		if rule == nil {
			var zero R
			return zero, at(node, path, fmt.Errorf("no rule for kind %q", kind))
		}

		child := func(index int, scope *Env[V]) (R, error) {
			next, ok := node.Child(index)
			if !ok {
				var zero R
				return zero, at(node, path, fmt.Errorf("child index %d out of range", index))
			}
			return visit(next, append(path, tree.Step{Parent: node, Index: index}), scope)
		}

		result, err := rule(Frame{Node: node, Path: slices.Clip(path)}, env, child)
		if err != nil {
			var located *Error
			if !errors.As(err, &located) {
				err = at(node, path, err)
			}
		}
		return result, err
	}

	return visit(root, nil, env)
}

// Error records the failing occurrence and its cause. Node.Info provides the
// node ID and span. Path is an owned copy of the edges to Node.
type Error struct {
	Node tree.Node
	Path tree.Path
	Err  error
}

func at(node tree.Node, path tree.Path, err error) *Error {
	return &Error{Node: node, Path: slices.Clone(path), Err: err}
}

func (e *Error) Error() string {
	info := e.Node.Info()
	span := info.Span
	if span.Source == "" {
		return fmt.Sprintf("eval: %q node %v: %v", info.Kind, info.ID, e.Err)
	}
	return fmt.Sprintf("eval: %q node %v at %q bytes [%d,%d): %v",
		info.Kind, info.ID, span.Source, span.Start, span.End, e.Err)
}

// Unwrap returns the original cause.
func (e *Error) Unwrap() error { return e.Err }
