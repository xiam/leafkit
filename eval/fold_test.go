package eval_test

import (
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xiam/leafkit/eval"
	"github.com/xiam/leafkit/tree"
)

const (
	number tree.Kind = "number"
	add    tree.Kind = "add"
	ifKind tree.Kind = "if"
	and    tree.Kind = "and"
	name   tree.Kind = "name"
	let    tree.Kind = "let"
)

var unknown tree.Span

type intScope = eval.Env[*big.Int]
type intChild = eval.Descend[*big.Int, *big.Int]
type intRule = eval.Rule[*big.Int, *big.Int]

func arithmeticRules() map[tree.Kind]intRule {
	return map[tree.Kind]intRule{
		number: func(frame eval.Frame, _ *intScope, _ intChild) (*big.Int, error) {
			return new(big.Int).Set(frame.Node.(*tree.Value[*big.Int]).Value()), nil
		},
		add: func(_ eval.Frame, env *intScope, child intChild) (*big.Int, error) {
			left, err := child(0, env)
			if err != nil {
				return nil, err
			}
			right, err := child(1, env)
			if err != nil {
				return nil, err
			}
			return new(big.Int).Add(left, right), nil
		},
		ifKind: func(_ eval.Frame, env *intScope, child intChild) (*big.Int, error) {
			test, err := child(0, env)
			if err != nil {
				return nil, err
			}
			if test.Sign() != 0 {
				return child(1, env)
			}
			return child(2, env)
		},
		and: func(_ eval.Frame, env *intScope, child intChild) (*big.Int, error) {
			left, err := child(0, env)
			if err != nil || left.Sign() == 0 {
				return left, err
			}
			return child(1, env)
		},
		name: func(frame eval.Frame, env *intScope, _ intChild) (*big.Int, error) {
			symbol := frame.Node.(*tree.Value[string]).Value()
			if value, ok := env.Get(symbol); ok {
				return value, nil
			}
			return nil, fmt.Errorf("unbound name %q", symbol)
		},
		let: func(frame eval.Frame, env *intScope, child intChild) (*big.Int, error) {
			init, err := child(1, env)
			if err != nil {
				return nil, err
			}
			binder, _ := frame.Node.Child(0)
			scope := eval.NewEnv(env)
			scope.Set(binder.(*tree.Value[string]).Value(), init)
			return child(2, scope)
		},
	}
}

func intNode(value int64) tree.Node {
	return tree.NewValue(number, unknown, big.NewInt(value))
}

func TestArithmetic(t *testing.T) {
	left, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	right := big.NewInt(2)
	root := tree.NewContainer(add, unknown,
		tree.NewValue(number, unknown, left), tree.NewValue(number, unknown, right))
	got, err := eval.Fold(root, nil, arithmeticRules())
	require.NoErrorf(t, err, "Fold = %v, %v", got, err)
	require.Equalf(t, "123456789012345678901234567892", got.String(), "Fold = %v, %v", got, err)
	got.SetInt64(0)
	require.Equalf(t, "123456789012345678901234567890", left.String(), "AST operands changed to %s and %s", left, right)
	require.Equalf(t, int64(2), right.Int64(), "AST operands changed to %s and %s", left, right)
}

func TestConditionalAndShortCircuit(t *testing.T) {
	bad := tree.NewError(unknown, "must stay unevaluated")
	for _, tc := range []struct {
		label string
		root  tree.Node
		want  int64
	}{
		{"else", tree.NewContainer(ifKind, unknown, intNode(0), bad, intNode(42)), 42},
		{"then", tree.NewContainer(ifKind, unknown, intNode(1), intNode(7), bad), 7},
		{"and stops", tree.NewContainer(and, unknown, intNode(0), bad), 0},
		{"and continues", tree.NewContainer(and, unknown, intNode(1), intNode(9)), 9},
	} {
		t.Run(tc.label, func(t *testing.T) {
			got, err := eval.Fold(tc.root, nil, arithmeticRules())
			require.NoErrorf(t, err, "Fold = %v, %v, want %d", got, err, tc.want)
			require.Equalf(t, tc.want, got.Int64(), "Fold = %v, %v, want %d", got, err, tc.want)
		})
	}
}

func TestLexicalLet(t *testing.T) {
	env := eval.NewEnv[*big.Int](nil)
	env.Set("x", big.NewInt(10))
	env.Set("y", big.NewInt(100))
	x := tree.NewValue(name, unknown, "x")
	y := tree.NewValue(name, unknown, "y")
	readInside := tree.NewContainer(add, unknown, x, y)
	inner := tree.NewContainer(let, unknown, x, tree.NewContainer(add, unknown, x, intNode(1)), readInside)
	root := tree.NewContainer(add, unknown, inner, x)
	got, err := eval.Fold(root, env, arithmeticRules())
	require.NoErrorf(t, err, "Fold = %v, %v, want 121", got, err)
	require.Equalf(t, int64(121), got.Int64(), "Fold = %v, %v, want 121", got, err)
	// The same node can have one annotation and several evaluation results.
	table := eval.Table[string]{x.Info().ID: "reference"}
	for _, path := range [][]int{{0, 2, 0}, {1}} {
		var node tree.Node = root
		for _, index := range path {
			node, _ = node.Child(index)
		}
		require.Samef(t, x, node, "path %v did not find the shared node and annotation", path)
		require.Equalf(t, "reference", table[node.Info().ID], "path %v did not find the shared node and annotation", path)
	}
}

func TestFoldPathAndPosition(t *testing.T) {
	cause := errors.New("invalid operand")
	span := tree.Span{Source: "example", Start: 3, End: 4}
	shared := tree.NewValue("failure", span, 0)
	parent := tree.NewContainer("parent", unknown, shared, shared)
	inner := tree.NewContainer("wrapper", unknown, parent)
	outer := tree.NewContainer("wrapper", unknown, inner)
	root := tree.NewContainer("wrapper", unknown, outer)
	var visits []int
	rules := map[tree.Kind]eval.Rule[int, struct{}]{
		"wrapper": func(_ eval.Frame, env *eval.Env[struct{}], child eval.Descend[int, struct{}]) (int, error) {
			return child(0, env)
		},
		"parent": func(_ eval.Frame, env *eval.Env[struct{}], child eval.Descend[int, struct{}]) (int, error) {
			_, first := child(1, env)
			_, _ = child(0, env)
			_, _ = child(0, env)
			return 0, fmt.Errorf("while evaluating operands: %w", first)
		},
		"failure": func(frame eval.Frame, _ *eval.Env[struct{}], _ eval.Descend[int, struct{}]) (int, error) {
			visits = append(visits, frame.Path[len(frame.Path)-1].Index)
			return 0, cause
		},
	}
	_, err := eval.Fold(root, nil, rules)
	var located *eval.Error
	require.ErrorAsf(t, err, &located, "Fold error = %v", err)
	require.ErrorIsf(t, err, cause, "Fold error = %v", err)
	wantPath := tree.Path{{Parent: root}, {Parent: outer}, {Parent: inner}, {Parent: parent, Index: 1}}
	require.Samef(t, shared, located.Node, "error node = %v, path = %v", located.Node, located.Path)
	require.Equalf(t, wantPath, located.Path, "error node = %v, path = %v", located.Node, located.Path)
	require.Equalf(t, []int{1, 0, 0}, visits, "visited %v, want [1 0 0]", visits)
	start, end, err := tree.Positions(located.Node.Info().Span, func(source tree.SourceID) ([]byte, bool) {
		return []byte("a\n b"), source == "example"
	})
	require.NoErrorf(t, err, "positions = %v, %v, %v", start, end, err)
	require.Equalf(t, (tree.Position{Line: 2, Column: 2}), start, "positions = %v, %v, %v", start, end, err)
	require.Equalf(t, (tree.Position{Line: 2, Column: 3}), end, "positions = %v, %v, %v", start, end, err)
}

func TestFoldDispatchErrors(t *testing.T) {
	bad := tree.NewError(unknown, "damaged")
	ambiguity := tree.NewAmbiguity(unknown, intNode(1), intNode(2))
	for _, node := range []tree.Node{bad, ambiguity} {
		_, err := eval.Fold(node, nil, arithmeticRules())
		assertFoldError(t, err, node, "no rule")
	}

	rules := arithmeticRules()
	rules[tree.KindAmbiguity] = func(_ eval.Frame, env *intScope, child intChild) (*big.Int, error) {
		return child(1, env)
	}
	got, err := eval.Fold(ambiguity, nil, rules)
	require.NoErrorf(t, err, "explicit ambiguity rule = %v, %v, want 2", got, err)
	require.Equalf(t, int64(2), got.Int64(), "explicit ambiguity rule = %v, %v, want 2", got, err)

	root := tree.NewContainer(add, unknown, intNode(1))
	_, err = eval.Fold(root, nil, rules)
	assertFoldError(t, err, root, "child index 1")

	rightCalled := false
	rules[number] = func(eval.Frame, *intScope, intChild) (*big.Int, error) {
		rightCalled = true
		return nil, errors.New("unexpected operand")
	}
	_, err = eval.Fold(tree.NewContainer(add, unknown, bad, intNode(2)), nil, rules)
	var located *eval.Error
	require.ErrorAsf(t, err, &located, "expected only the left operand's failure, got %v with rightCalled = %v", err, rightCalled)
	require.Samef(t, bad, located.Node, "expected only the left operand's failure, got %v with rightCalled = %v", err, rightCalled)
	require.Lenf(t, located.Path, 1, "expected only the left operand's failure, got %v with rightCalled = %v", err, rightCalled)
	require.Falsef(t, rightCalled, "expected only the left operand's failure, got %v with rightCalled = %v", err, rightCalled)
}

func assertFoldError(t *testing.T, err error, node tree.Node, message string) {
	t.Helper()
	var located *eval.Error
	require.ErrorAsf(t, err, &located, "Fold error = %v, want %q at the root", err, message)
	require.Samef(t, node, located.Node, "Fold error = %v, want %q at the root", err, message)
	require.Lenf(t, located.Path, 0, "Fold error = %v, want %q at the root", err, message)
	require.Containsf(t, located.Error(), message, "Fold error = %v, want %q at the root", err, message)
}
