package core

import (
	"fmt"
	"strconv"
	"strings"

	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

// Snapshot owns the immutable SQL text. Lexemes retain this one pointer and a
// byte range instead of copying large literal payloads into structural nodes.
type Snapshot struct {
	id   tree.SourceID
	text string
}

func NewSnapshot(id tree.SourceID, text string) *Snapshot { return &Snapshot{id: id, text: text} }
func (s *Snapshot) ID() tree.SourceID                     { return s.id }
func (s *Snapshot) Text() string                          { return s.text }
func (s *Snapshot) Raw(span tree.Span) (string, error) {
	if span.Source != s.id || s.id == "" || span.Start < 0 || span.End < span.Start || span.End > len(s.text) {
		return "", fmt.Errorf("sql: range outside snapshot: %+v", span)
	}
	return s.text[span.Start:span.End], nil
}

// Lexeme is a reference-backed leaf. Raw preserves quoting and case; Value
// decodes doubled quotes and folds ASCII bare identifiers to lower case.
type Lexeme struct {
	info      tree.Envelope
	snapshot  *Snapshot
	rangeSpan tree.Span
	quoted    bool
	decode    func(string) string
}

func (n *Lexeme) Info() tree.Envelope       { return n.info }
func (*Lexeme) Child(int) (tree.Node, bool) { return nil, false }
func (n *Lexeme) Snapshot() *Snapshot       { return n.snapshot }

// Range excludes leading trivia in the grammar production span.
func (n *Lexeme) Range() tree.Span { return n.rangeSpan }
func (n *Lexeme) Raw() string      { text, _ := n.snapshot.Raw(n.rangeSpan); return text }
func (n *Lexeme) Quoted() bool     { return n.quoted }
func (n *Lexeme) Value() string {
	raw := n.Raw()
	if n.decode != nil {
		return n.decode(raw)
	}
	if n.quoted {
		q := raw[:1]
		return strings.ReplaceAll(raw[1:len(raw)-1], q+q, q)
	}
	switch n.info.Kind {
	case NameKind:
		return strings.ToLower(raw)
	case OperatorKind:
		if raw == "!=" {
			return "<>"
		}
	case DirectionKind:
		return strings.ToUpper(raw)
	}
	return raw
}

// structure is private so a published node's envelope, fields and children
// cannot be replaced. Field returns a copy of child references.
type structure struct {
	info     tree.Envelope
	children []tree.Node
	fields   map[string][]tree.Node
}

func (n *structure) Info() tree.Envelope { return n.info }
func (n *structure) Child(i int) (tree.Node, bool) {
	if i < 0 || i >= len(n.children) {
		return nil, false
	}
	return n.children[i], true
}
func (n *structure) Field(name string) []tree.Node {
	return append([]tree.Node(nil), n.fields[name]...)
}
func (n *structure) One(name string) tree.Node {
	if nodes := n.fields[name]; len(nodes) == 1 {
		return nodes[0]
	}
	return nil
}

// Structural types share the tree.Node contract and read-only named accessors.
type File struct{ *structure }
type Select struct{ *structure }
type Identifier struct{ *structure }
type Projection struct{ *structure }
type Table struct{ *structure }
type Join struct{ *structure }
type Comparison struct{ *structure }
type Order struct{ *structure }
type Distinct struct{ *structure }
type Star struct{ *structure }

func (n *Identifier) Parts() []*Lexeme {
	result := make([]*Lexeme, len(n.children))
	for i, child := range n.children {
		result[i], _ = child.(*Lexeme)
	}
	return result
}
func (n *Table) Name() *Identifier         { value, _ := n.One("name").(*Identifier); return value }
func (n *Table) Alias() *Lexeme            { value, _ := n.One("alias").(*Lexeme); return value }
func (n *Select) Projections() []tree.Node { return n.Field("projections") }
func (n *Select) From() *Table             { value, _ := n.One("from").(*Table); return value }

// TokenDecoder maps a namespaced dialect token to a shared scalar value.
// Decode must be pure. Quoted marks a delimited name or string, so the portable
// printer can emit ANSI quoting from the decoded value.
type TokenDecoder struct {
	Kind   p.TokenKind
	Quoted bool
	Decode func(string) string
}

// ExtensionBuilder handles only dialect-owned kinds. Core kinds always use
// the shared factory, ensuring unchanged syntax produces the same node types.
type ExtensionBuilder func(p.Build[tree.Node]) (tree.Node, error)

func newStructure(kind tree.Kind, span tree.Span, fields []p.Field[tree.Node]) *structure {
	s := &structure{info: tree.NewEnvelope(kind, span), fields: map[string][]tree.Node{}}
	for _, field := range fields {
		s.children = append(s.children, field.Nodes...)
		s.fields[field.Name] = append(s.fields[field.Name], field.Nodes...)
	}
	return s
}

// Builders binds lexemes to their snapshot. The callback inputs and all
// published data remain immutable. No scanner token text is retained.
func Builders(snapshot *Snapshot, extension ExtensionBuilder, decoders ...TokenDecoder) p.Builders[tree.Node] {
	decoding := make(map[p.TokenKind]TokenDecoder, len(decoders))
	for _, decoder := range decoders {
		decoding[decoder.Kind] = decoder
	}
	return p.Builders[tree.Node]{
		Node: func(b p.Build[tree.Node]) (tree.Node, error) {
			switch b.Kind {
			case NameKind, StringKind, NumberKind, OperatorKind, DirectionKind:
				if b.Tokens.Len() != 1 {
					return nil, fmt.Errorf("sql: %s requires one token", b.Kind)
				}
				token, _ := b.Tokens.At(0)
				if _, err := snapshot.Raw(token.Span); err != nil {
					return nil, err
				}
				quoted := token.Kind == QuotedNameToken || token.Kind == StringToken
				var decode func(string) string
				if decoder, ok := decoding[token.Kind]; ok {
					if decoder.Decode == nil {
						return nil, fmt.Errorf("sql: nil decoder for %q", token.Kind)
					}
					quoted, decode = decoder.Quoted, decoder.Decode
				} else if !strings.HasPrefix(string(token.Kind), "sql:") {
					return nil, fmt.Errorf("sql: no decoder for dialect token %q", token.Kind)
				}
				return &Lexeme{info: tree.NewEnvelope(b.Kind, b.Span), snapshot: snapshot, rangeSpan: token.Span, quoted: quoted, decode: decode}, nil
			}
			s := newStructure(b.Kind, b.Span, b.Fields)
			switch b.Kind {
			case SelectKind:
				return &Select{s}, nil
			case IdentifierKind:
				return &Identifier{s}, nil
			case ProjectionKind:
				return &Projection{s}, nil
			case TableKind:
				return &Table{s}, nil
			case JoinKind:
				return &Join{s}, nil
			case ComparisonKind:
				return &Comparison{s}, nil
			case OrderKind:
				return &Order{s}, nil
			case DistinctKind:
				return &Distinct{s}, nil
			case StarKind:
				return &Star{s}, nil
			}
			if extension != nil {
				return extension(b)
			}
			return nil, fmt.Errorf("sql: no builder for %q", b.Kind)
		},
		File: func(span tree.Span, children []tree.Node, _ []p.Trivia) (tree.Node, error) {
			return &File{newStructure(FileKind, span, []p.Field[tree.Node]{{Nodes: children}})}, nil
		},
	}
}

// Canonical compares structure and decoded scalar values, ignoring source IDs,
// byte ranges, node IDs, trivia, quote spelling and keyword case. It is a
// structural oracle, not SQL semantic equivalence (e.g. 1 and 1.0 differ).
// An extension leaf must implement SQLValue() string to participate.
func Canonical(root tree.Node) string {
	var out strings.Builder
	var visit func(tree.Node)
	visit = func(n tree.Node) {
		out.WriteByte('(')
		out.WriteString(string(n.Info().Kind))
		switch v := n.(type) {
		case *Lexeme:
			out.WriteByte(' ')
			out.WriteString(strconv.Quote(v.Value()))
		case interface{ SQLValue() string }:
			out.WriteByte(' ')
			out.WriteString(strconv.Quote(v.SQLValue()))
		}
		for i := 0; ; i++ {
			child, ok := n.Child(i)
			if !ok {
				break
			}
			out.WriteByte(' ')
			visit(child)
		}
		out.WriteByte(')')
	}
	visit(root)
	return out.String()
}
