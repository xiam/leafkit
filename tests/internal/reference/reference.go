// Package reference holds the fixture and oracle helpers used by the examples.
package reference

import (
	"crypto/sha256"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

// Input exposes immutable prefixes of one snapshot to independent readers.
type Input struct {
	text      string
	available int
	final     bool
}

func (in *Input) ID() tree.SourceID {
	return tree.SourceID(fmt.Sprintf("reference:%x", sha256.Sum256([]byte(in.text))))
}

func (in *Input) Open(offset int, moreInput error) io.Reader {
	if offset < 0 || offset > in.available {
		return nil
	}
	return &inputReader{input: in, offset: offset, moreInput: moreInput}
}

type inputReader struct {
	input     *Input
	offset    int
	moreInput error
}

func (r *inputReader) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	if r.offset == r.input.available {
		if r.input.final {
			return 0, io.EOF
		}
		return 0, r.moreInput
	}
	n := copy(buf, r.input.text[r.offset:r.input.available])
	r.offset += n
	return n, nil
}

func Token(kind p.TokenKind) p.Rule { return p.Tok(p.Kind(kind)) }
func Skip(kind p.TokenKind) p.Rule  { return p.Drop(Token(kind)) }

func Builders(textKinds ...tree.Kind) p.Builders[tree.Node] {
	return p.Builders[tree.Node]{
		Node: func(build p.Build[tree.Node]) (tree.Node, error) {
			if slices.Contains(textKinds, build.Kind) {
				token, ok := build.Tokens.At(0)
				if !ok || build.Tokens.Len() != 1 {
					return nil, fmt.Errorf("expected one token for %q", build.Kind)
				}
				return tree.NewValue(build.Kind, build.Span, token.Text), nil
			}
			var children []tree.Node
			for _, field := range build.Fields {
				children = append(children, field.Nodes...)
			}
			return tree.NewContainer(build.Kind, build.Span, children...), nil
		},
		File: func(full tree.Span, children []tree.Node, _ []p.Trivia) (tree.Node, error) {
			return tree.NewContainer("file", full, children...), nil
		},
	}
}

func Engine(t testing.TB, grammar p.Grammar, builders p.Builders[tree.Node]) *p.Engine[tree.Node] {
	t.Helper()
	engine, err := p.Configure(grammar, builders, p.Limits{})
	require.NoError(t, err)
	return engine
}

type Source func(*Input) (p.Source, error)

// Parse can feed every byte boundary before declaring final EOF.
func Parse(t testing.TB, engine *p.Engine[tree.Node], source Source, input string, streamed bool, state p.State) tree.Node {
	t.Helper()
	in := &Input{text: input}
	if !streamed {
		in.available, in.final = len(input), true
	}
	src, err := source(in)
	require.NoError(t, err)
	session := engine.Start(src, state)
	if streamed {
		for cut := 0; cut <= len(input); cut++ {
			in.available = cut
			root, status, err := session.Run()
			require.NoErrorf(t, err, "byte %d: Run() = %v, %v, %v, want a pause", cut, root, status, err)
			require.Equalf(t, p.Paused, status, "byte %d: Run() = %v, %v, %v, want a pause", cut, root, status, err)
			require.Truef(t, root == nil, "byte %d: Run() = %v, %v, %v, want a pause", cut, root, status, err)
		}
		in.final = true
	}
	root, status, err := session.Run()
	require.NoErrorf(t, err, "Run() = %v, %v, %v, want a tree", root, status, err)
	require.Equalf(t, p.Done, status, "Run() = %v, %v, %v, want a tree", root, status, err)
	require.Truef(t, root != nil, "Run() = %v, %v, %v, want a tree", root, status, err)
	require.Equalf(t, (tree.Span{Source: in.ID(), End: len(input)}), root.Info().Span, "file span = %+v", root.Info().Span)
	require.NoError(t, tree.Validate(root))
	return root
}

type Diagnostic struct {
	Slot           string
	Start, End, At int
	Found, Skipped string
	Expected       []p.Pattern
}

func Check(t testing.TB, root tree.Node, shape string, expected ...Diagnostic) {
	t.Helper()
	{
		got := Shape(root)
		assert.Equalf(t, shape, got, "tree = %s\nwant   %s", got, shape)
	}
	var diagnostics []Diagnostic
	for node := range tree.Walk(root) {
		leaf, ok := node.(*p.ErrorNode)
		if !ok {
			continue
		}
		problem := leaf.Problem()
		found := problem.Found.Token.Text
		if problem.Found.End {
			found = "<eof>"
		} else if problem.Found.Token.Synthetic {
			found = "<" + string(problem.Found.Token.Kind) + ">"
		}
		var skipped strings.Builder
		for _, token := range problem.Skipped {
			skipped.WriteString(token.Text)
		}
		diagnostics = append(diagnostics, Diagnostic{
			Slot: leaf.Slot(), Start: problem.Span.Start, End: problem.Span.End, At: problem.Found.Point.Start,
			Found: found, Skipped: skipped.String(), Expected: problem.Expected,
		})
	}
	assert.Equalf(t, expected, diagnostics, "diagnostics = %+v\nwant          %+v", diagnostics, expected)
}

func Shape(node tree.Node) string {
	var out strings.Builder
	writeShape(&out, node)
	return out.String()
}

func writeShape(out *strings.Builder, node tree.Node) {
	out.WriteByte('(')
	out.WriteString(string(node.Info().Kind))
	if value, ok := node.(*tree.Value[string]); ok {
		out.WriteByte(' ')
		out.WriteString(strconv.Quote(value.Value()))
	}
	for i := 0; ; i++ {
		child, ok := node.Child(i)
		if !ok {
			break
		}
		out.WriteByte(' ')
		writeShape(out, child)
	}
	out.WriteByte(')')
}
