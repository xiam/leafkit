package tree

import (
	"bytes"
	"errors"
	"fmt"
	"iter"
	"math/big"
	"slices"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

type testNode struct {
	info      Envelope
	children  []Node
	childCall int
}

func (n *testNode) Info() Envelope { return n.info }
func (n *testNode) Child(i int) (Node, bool) {
	n.childCall++
	return childAt(n.children, i)
}

type valueNode struct{}

func (valueNode) Info() Envelope         { return Envelope{} }
func (valueNode) Child(int) (Node, bool) { return nil, false }

func TestValidate(t *testing.T) {
	span := Span{Source: "snapshot", End: 4}
	leaf := NewValue("literal", span, 1)
	cycle := &testNode{info: NewEnvelope("cycle", Span{})}
	cycle.children = []Node{NewContainer("branch", Span{}, cycle)}
	var typedNil *Value[int]
	tests := []struct {
		name string
		node Node
		want string
	}{
		{"ordinary", NewContainer("root", Span{}, leaf, NewError(Span{}, "missing")), ""},
		{"shared", NewAmbiguity(span, leaf, NewContainer("other", span, leaf)), ""},
		{"source-free ambiguity", NewAmbiguity(Span{}, NewValue("a", Span{}, 1), NewValue("b", Span{}, 2)), ""},
		{"cycle", cycle, "cycle"},
		{"nil root", nil, "non-nil"},
		{"typed nil root", typedNil, "non-nil"},
		{"nil child", NewContainer("root", Span{}, nil), "non-nil"},
		{"typed nil child", NewContainer("root", Span{}, typedNil), "non-nil"},
		{"non-pointer", valueNode{}, "non-nil node pointer"},
		{"zero ID", &testNode{info: Envelope{Kind: "a"}}, "zero node ID"},
		{"copied ID", NewContainer("root", Span{}, leaf, &testNode{info: leaf.Info()}), "distinct nodes"},
		{"empty kind", NewValue("", Span{}, 0), "empty kind"},
		{"reserved kind", NewValue("leafkit:custom", Span{}, 0), "reserved kind"},
		{"source-free with range", NewValue("a", Span{End: 1}, 0), "invalid span"},
		{"negative span", NewValue("a", Span{Source: "s", Start: -1}, 0), "invalid span"},
		{"reversed span", NewValue("a", Span{Source: "s", Start: 1}, 0), "invalid span"},
		{"too few alternatives", NewAmbiguity(span, leaf), "at least two"},
		{"different snapshot", NewAmbiguity(span, leaf, NewValue("a", Span{Source: "other", End: 4}, 0)), "different span"},
		{"different range", NewAmbiguity(span, leaf, NewValue("a", Span{Source: "snapshot", End: 3}, 0)), "different span"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.node)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.Errorf(t, err, "Validate = %v, want error containing %q", err, tc.want)
				require.Containsf(t, err.Error(), tc.want, "Validate = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestChildLists(t *testing.T) {
	first := NewValue("literal", Span{}, 1)
	second := NewValue("literal", Span{}, 2)
	children := []Node{first, second}
	group := NewContainer("list", Span{}, children...)
	ambiguity := NewAmbiguity(Span{}, children...)
	children[0] = nil
	for _, root := range []Node{group, ambiguity} {
		for i, want := range []Node{first, second} {
			{
				child, ok := root.Child(i)
				require.Truef(t, ok, "%T.Child(%d) = %v, %v, want %v, true", root, i, child, ok, want)
				require.Samef(t, want, child, "%T.Child(%d) = %v, %v, want %v, true", root, i, child, ok, want)
			}
		}
		for _, i := range []int{-1, 2} {
			{
				child, ok := root.Child(i)
				require.Truef(t, child == nil, "%T.Child(%d) = %v, %v, want nil, false", root, i, child, ok)
				require.Falsef(t, ok, "%T.Child(%d) = %v, %v, want nil, false", root, i, child, ok)
			}
		}
	}
}

func TestRebuildSharesUnchangedNodes(t *testing.T) {
	leaf := NewValue("constant", Span{Source: "old", End: 1}, 7)
	oldPath := NewContainer("path", Span{}, leaf, NewValue("constant", Span{}, 1))
	oldRoot := NewContainer("root", Span{}, oldPath)
	newPath := NewContainer("path", Span{}, leaf, NewValue("constant", Span{}, 1))
	newRoot := NewContainer("root", Span{}, newPath)
	oldValue, _ := oldPath.Child(1)
	newValue, _ := newPath.Child(1)
	require.NotEqualf(t, newRoot.Info().ID, oldRoot.Info().ID, "replacement objects must get fresh IDs")
	require.NotEqualf(t, newPath.Info().ID, oldPath.Info().ID, "replacement objects must get fresh IDs")
	require.NotEqualf(t, newValue.Info().ID, oldValue.Info().ID, "replacement objects must get fresh IDs")
	for _, path := range []*Container{oldPath, newPath} {
		child, _ := path.Child(0)
		require.Samef(t, leaf, child, "rebuilding changed a reused node or its original snapshot")
		require.Equalf(t, leaf.Info(), child.Info(), "rebuilding changed a reused node or its original snapshot")
	}
}

func TestPayloadReferences(t *testing.T) {
	type largeRecord struct {
		label string
		data  [1 << 20]byte
	}
	array := new([8 << 20]byte)
	record := &largeRecord{label: "blob"}
	number, ok := new(big.Int).SetString("123456789012345678901234567890", 10)
	require.Truef(t, ok, "invalid test integer")
	buf := []byte("abc")
	a := NewValue("array", Span{}, array)
	r := NewValue("record", Span{}, record)
	n := NewValue("number", Span{}, number)
	b := NewValue("bytes", Span{}, buf)
	i := NewValue("number", Span{}, 7)

	require.Samef(t, array, a.Value(), "payload readback changed value or reference identity")
	require.Samef(t, record, r.Value(), "payload readback changed value or reference identity")
	require.Samef(t, number, n.Value(), "payload readback changed value or reference identity")
	require.Samef(t, &buf[0], &b.Value()[0], "payload readback changed value or reference identity")
	require.Equalf(t, 7, i.Value(), "payload readback changed value or reference identity")
	require.Equalf(t, unsafe.Sizeof(*n), unsafe.Sizeof(*a), "the reference-backed node size varies with the pointee type")
	require.Equalf(t, unsafe.Sizeof(*n), unsafe.Sizeof(*r), "the reference-backed node size varies with the pointee type")
}

func TestSharedWalks(t *testing.T) {
	leaf := NewValue("leaf", Span{}, 1)
	branch := &testNode{info: NewEnvelope("branch", Span{}), children: []Node{leaf}}
	root := NewContainer("root", Span{}, branch, branch)
	require.NoError(t, Validate(root))

	want := []Node{root, branch, leaf, branch, leaf}
	{
		got := slices.Collect(Walk(root))
		require.Truef(t, slices.Equal(got, want), "occurrences = %v, want %v", got, want)
	}
	var paths []Path
	for path, node := range WalkPath(root) {
		if node.Info().ID == leaf.Info().ID {
			paths = append(paths, slices.Clone(path))
		}
	}
	left := Path{{Parent: root, Index: 0}, {Parent: branch, Index: 0}}
	right := Path{{Parent: root, Index: 1}, {Parent: branch, Index: 0}}
	require.Lenf(t, paths, 2, "paths = %v, want %v and %v", paths, left, right)
	require.Truef(t, slices.Equal(paths[0], left), "paths = %v, want %v and %v", paths, left, right)
	require.Truef(t, slices.Equal(paths[1], right), "paths = %v, want %v and %v", paths, left, right)

	walk := WalkUnique(root)
	wantUnique := []Node{root, branch, leaf}
	for range 2 {
		branch.childCall = 0
		{
			got := slices.Collect(walk)
			require.Truef(t, slices.Equal(got, wantUnique), "unique = %v with %d branch lookups, want %v and 2 lookups", got, branch.childCall, wantUnique)
			require.Equalf(t, 2, branch.childCall, "unique = %v with %d branch lookups, want %v and 2 lookups", got, branch.childCall, wantUnique)
		}
	}
	{
		got := slices.Collect(All[*Value[int]](root))
		require.Truef(t, slices.Equal(got, []*Value[int]{leaf, leaf}), "All = %v, want two occurrences of %v", got, leaf)
	}
}

func TestWalkEarlyExit(t *testing.T) {
	root := &testNode{info: NewEnvelope("root", Span{})}
	for _, walk := range []iter.Seq[Node]{Walk(root), WalkUnique(root), All[Node](root)} {
		for node := range walk {
			require.Samef(t, root, node, "walk must first yield the root")
			break
		}
	}
	for path, node := range WalkPath(root) {
		require.Lenf(t, path, 0, "the root must have an empty path")
		require.Samef(t, root, node, "the root must have an empty path")
		break
	}
	require.Equalf(t, 0, root.childCall, "a walk looked up children after early exit")
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestFprint(t *testing.T) {
	point := Span{Source: "input"}
	errorNode := NewError(point, "missing item")
	root := NewContainer("root", Span{}, errorNode, errorNode)
	var buf bytes.Buffer
	require.NoError(t, Fprint(&buf, root))
	r, e := root.Info().ID.serial, errorNode.Info().ID.serial
	want := fmt.Sprintf("#%d \"root\" <unknown>\n#%d[0] -> #%d \"leafkit:error\" \"input\"[0,0)\n#%d[1] -> #%d (shared)\n", r, r, e, r, e)
	{
		got := buf.String()
		require.Equalf(t, want, got, "debug = %q, message = %q, want debug %q and recovery message", got, errorNode.Message(), want)
		require.Equalf(t, "missing item", errorNode.Message(), "debug = %q, message = %q, want debug %q and recovery message", got, errorNode.Message(), want)
	}
	sentinel := errors.New("failed writer")
	{
		err := Fprint(failingWriter{sentinel}, root)
		require.Truef(t, err == sentinel, "Fprint failure = %v, want %v", err, sentinel)
	}
}
