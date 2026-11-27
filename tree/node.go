package tree

import (
	"sync/atomic"
)

// Kind identifies a syntactic role. The prefix "leafkit:" is reserved.
type Kind string

const (
	KindError     Kind = "leafkit:error"
	KindAmbiguity Kind = "leafkit:ambiguity"
)

// ID identifies a node in the current process. The zero ID is invalid.
type ID struct{ serial uint64 }

// Envelope is the metadata shared by library and consumer nodes.
type Envelope struct {
	Kind Kind
	Span Span
	ID   ID
}

// Shape describes an ordered child accessor. Node uses Shape[Node].
type Shape[N any] interface {
	Info() Envelope
	Child(index int) (N, bool)
}

// Node is implemented by non-nil pointers with stable metadata and children.
// Children occupy contiguous indices from zero. Invalid indices return nil
// and false. Once published, nodes and their reachable data are read-only.
type Node interface {
	Shape[Node]
}

var lastID atomic.Uint64

// NewEnvelope assigns a fresh ID. Use a fresh envelope for each new node.
func NewEnvelope(kind Kind, span Span) Envelope {
	for {
		last := lastID.Load()
		if last == ^uint64(0) {
			panic("tree: exhausted node IDs")
		}
		if lastID.CompareAndSwap(last, last+1) {
			return Envelope{Kind: kind, Span: span, ID: ID{serial: last + 1}}
		}
	}
}

// Value is a leaf with a typed payload.
type Value[T any] struct {
	info  Envelope
	value T
}

// NewValue stores value as supplied. Pass a pointer for a large struct or
// array. A slice value shares its existing backing array.
func NewValue[T any](kind Kind, span Span, value T) *Value[T] {
	return &Value[T]{info: NewEnvelope(kind, span), value: value}
}

func (v *Value[T]) Info() Envelope       { return v.info }
func (*Value[T]) Child(int) (Node, bool) { return nil, false }
func (v *Value[T]) Value() T             { return v.value }

// Container holds an ordered list of child nodes.
type Container struct {
	info     Envelope
	children []Node
}

// NewContainer copies the child references in order.
func NewContainer(kind Kind, span Span, children ...Node) *Container {
	return &Container{info: NewEnvelope(kind, span), children: append([]Node(nil), children...)}
}

func (n *Container) Info() Envelope               { return n.info }
func (n *Container) Child(index int) (Node, bool) { return childAt(n.children, index) }

// ErrorNode is a recovery leaf carrying a diagnostic message.
type ErrorNode struct {
	info    Envelope
	message string
}

func NewError(span Span, message string) *ErrorNode {
	return &ErrorNode{info: NewEnvelope(KindError, span), message: message}
}

func (n *ErrorNode) Info() Envelope       { return n.info }
func (*ErrorNode) Child(int) (Node, bool) { return nil, false }
func (n *ErrorNode) Message() string      { return n.message }

// Ambiguity holds ordered alternative roots for one span.
type Ambiguity struct {
	info         Envelope
	alternatives []Node
}

// NewAmbiguity copies the alternative references in order. Validate checks
// that there are at least two alternatives and that each has the given span.
func NewAmbiguity(span Span, alternatives ...Node) *Ambiguity {
	return &Ambiguity{info: NewEnvelope(KindAmbiguity, span), alternatives: append([]Node(nil), alternatives...)}
}

func (n *Ambiguity) Info() Envelope               { return n.info }
func (n *Ambiguity) Child(index int) (Node, bool) { return childAt(n.alternatives, index) }

func childAt(children []Node, index int) (Node, bool) {
	if index < 0 || index >= len(children) {
		return nil, false
	}
	return children[index], true
}
