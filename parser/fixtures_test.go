package parser_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

const snapshot tree.SourceID = "fixture"

func span(start, end int) tree.Span {
	return tree.Span{Source: snapshot, Start: start, End: end}
}

type fixtureRead func(p.Mark, p.Request) (p.Read, error)

type fixture struct {
	read      fixtureRead
	discarded int
}

func (f *fixture) Begin() (tree.SourceID, p.Mark) { return snapshot, p.Mark{} }
func (f *fixture) Read(m p.Mark, req p.Request) (p.Read, error) {
	if m.Byte < f.discarded {
		return p.Read{}, fmt.Errorf("read at retired mark %+v (floor %d)", m, f.discarded)
	}
	return f.read(m, req)
}

func (f *fixture) DiscardBefore(offset int) { f.discarded = offset }

func token(kind p.TokenKind, text string, start, end int) p.Token {
	return p.Token{Kind: kind, Text: text, Span: span(start, end)}
}

func ready(tok p.Token, next p.Mark) p.Read {
	return p.Read{Status: p.TokenReady, Token: tok, Next: next}
}

func eof(end int, trailing ...p.Trivia) p.Read {
	return p.Read{Status: p.EndOfInput, Next: p.Mark{Byte: end}, Trailing: trailing}
}

func shortage() p.Read { return p.Read{Status: p.NeedInput} }

func tagged(kind string) p.Rule { return p.Tok(p.Kind(p.TokenKind(kind))) }
func skip(kind string) p.Rule   { return p.Drop(tagged(kind)) }

func builders() p.Builders[tree.Node] {
	return p.Builders[tree.Node]{
		Node: func(in p.Build[tree.Node]) (tree.Node, error) {
			var children []tree.Node
			for _, field := range in.Fields {
				children = append(children, field.Nodes...)
			}
			if in.Kind == "word" || in.Kind == "number" {
				tok, ok := in.Tokens.At(0)
				if !ok || in.Tokens.Len() != 1 {
					return nil, fmt.Errorf("leaf %q requires one owned token", in.Kind)
				}
				return tree.NewValue(in.Kind, in.Span, tok.Text), nil
			}
			return tree.NewContainer(in.Kind, in.Span, children...), nil
		},
		File: func(full tree.Span, children []tree.Node, _ []p.Trivia) (tree.Node, error) {
			return tree.NewContainer("file", full, children...), nil
		},
	}
}

func configured(t *testing.T, grammar p.Grammar, build p.Builders[tree.Node], limits p.Limits) *p.Engine[tree.Node] {
	t.Helper()
	engine, err := p.Configure(grammar, build, limits)
	require.NoError(t, err)
	return engine
}

func single(rule p.Rule) p.Grammar {
	return p.Grammar{Start: "start", Rules: map[string]p.Rule{"start": rule}}
}

func done(t *testing.T, session *p.Session[tree.Node]) tree.Node {
	t.Helper()
	root, status, err := session.Run()
	require.NoErrorf(t, err, "Run() = %v, %v, %v", root, status, err)
	require.Equalf(t, p.Done, status, "Run() = %v, %v, %v", root, status, err)
	require.Truef(t, root != nil, "Run() = %v, %v, %v", root, status, err)
	return root
}

func paused(t *testing.T, session *p.Session[tree.Node]) {
	t.Helper()
	root, status, err := session.Run()
	require.NoErrorf(t, err, "Run() while open = %v, %v, %v", root, status, err)
	require.Equalf(t, p.Paused, status, "Run() while open = %v, %v, %v", root, status, err)
	require.Truef(t, root == nil, "Run() while open = %v, %v, %v", root, status, err)
}

type record struct {
	Kind     tree.Kind
	Span     tree.Span
	Text     string
	Children int
}

func records(root tree.Node) []record {
	var result []record
	for node := range tree.Walk(root) {
		info := node.Info()
		item := record{Kind: info.Kind, Span: info.Span}
		if leaf, ok := node.(*tree.Value[string]); ok {
			item.Text = leaf.Value()
		}
		for {
			if _, ok := node.Child(item.Children); !ok {
				break
			}
			item.Children++
		}
		result = append(result, item)
	}
	return result
}

func equalRecords(t *testing.T, root tree.Node, want []record) {
	t.Helper()
	{
		got := records(root)
		require.Equalf(t, want, got, "tree = %+v\nwant   %+v", got, want)
	}
}

func callStep(name, next, bind string) p.ProgramState {
	return p.ProgramState{Calls: []string{name}, ReturnsTo: []string{next}, Control: func(p.View) p.Move {
		return p.Call(name, next, bind)
	}}
}

func returnStep() p.ProgramState {
	return p.ProgramState{Control: func(p.View) p.Move { return p.Return() }}
}
