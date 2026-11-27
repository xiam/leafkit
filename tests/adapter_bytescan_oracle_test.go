package tests

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

// adapterInput exposes successive prefixes of one logical snapshot. Its
// readers have independent offsets into the available prefix.
type adapterInput struct {
	text      string
	available int
	sealed    bool
}

func (in *adapterInput) open(offset int) io.Reader {
	if offset < 0 || offset > in.available {
		return nil
	}
	return &adapterReader{input: in, offset: offset}
}

type adapterReader struct {
	input  *adapterInput
	offset int
}

func (r *adapterReader) Read(buf []byte) (int, error) {
	if r.offset == r.input.available {
		if r.input.sealed {
			return 0, io.EOF
		}
		return 0, scan.ErrMoreInput
	}
	n := copy(buf, r.input.text[r.offset:r.input.available])
	r.offset += n
	return n, nil
}

func adapterOracleScanner(cur *cursor.Reader) *scan.Scanner {
	lexer := scan.NewWithCursor(cur)
	lexer.MustAddRule("word", core.RuneRun(core.UnicodeLetter))
	lexer.MustAddRule("=", core.Literal("="))
	lexer.MustAddRule("space", base.Whitespace())
	lexer.MustAddRule("newline", base.Newline())
	lexer.MustAddRule("comment", core.Literal("/*\nhi*/"))
	return lexer
}

func TestBytescanOracleAndEveryInputSplit(t *testing.T) {
	const text = "é = /*\nhi*/fox \n"
	leading := []p.Trivia{{Kind: "space", Span: span(4, 5)}, {Kind: "comment", Span: span(5, 12)}}
	trailing := []p.Trivia{{Kind: "space", Span: span(15, 16)}, {Kind: "newline", Span: span(16, 17)}}
	hand := &fixture{read: func(mark p.Mark, _ p.Request) (p.Read, error) {
		switch mark.Byte {
		case 0:
			return ready(fixtureToken("word", "é", 0, 2), p.Mark{Byte: 2}), nil
		case 2:
			token := fixtureToken("=", "=", 3, 4)
			token.Leading = []p.Trivia{{Kind: "space", Span: span(2, 3)}}
			return ready(token, p.Mark{Byte: 4}), nil
		case 4:
			token := fixtureToken("word", "fox", 12, 15)
			token.Leading, token.BreakBefore = leading, true
			return ready(token, p.Mark{Byte: 15}), nil
		case 15:
			return eof(len(text), trailing...), nil
		default:
			return p.Read{}, fmt.Errorf("unexpected mark %+v", mark)
		}
	}}
	word := p.Make("word", "word", tagged("word"))
	grammar := p.Grammar{Start: "main", Rules: map[string]p.Rule{"main": p.Seq(word, skip("="), word)}}
	build := p.Builders[tree.Node]{
		Node: func(in p.Build[tree.Node]) (tree.Node, error) {
			token, ok := in.Tokens.At(0)
			if !ok || in.Tokens.Len() != 1 {
				return nil, fmt.Errorf("expected one word token")
			}
			if token.Text == "fox" && (!token.BreakBefore || !reflect.DeepEqual(token.Leading, leading)) {
				return nil, fmt.Errorf("fox has wrong leading gap: %+v", token)
			}
			return tree.NewValue(in.Kind, token.Span, token.Text), nil
		},
		File: func(full tree.Span, children []tree.Node, trivia []p.Trivia) (tree.Node, error) {
			if !reflect.DeepEqual(trivia, trailing) {
				return nil, fmt.Errorf("wrong trailing trivia: %+v", trivia)
			}
			return tree.NewContainer("file", full, children...), nil
		},
	}
	engine := configured(t, grammar, build, p.Limits{})
	want := []record{{"file", span(0, 17), "", 2}, {"word", span(0, 2), "é", 0}, {"word", span(12, 15), "fox", 0}}
	equalRecords(t, done(t, engine.Start(hand, "")), want)

	options := adapter.Options{Trivia: []scan.TokenType{"space", "comment", "newline"}}
	for _, restart := range []bool{false, true} {
		for cut := 0; cut <= len(text); cut++ {
			t.Run(fmt.Sprintf("restart=%t/cut=%d", restart, cut), func(t *testing.T) {
				input := &adapterInput{text: text, available: cut}
				lookup := func(id tree.SourceID) ([]byte, bool) {
					if !input.sealed || id != snapshot {
						return nil, false
					}
					return []byte(text), true
				}
				var src p.Source
				var err error
				if restart {
					build := func(cur *cursor.Reader, _ struct{}, req p.Request) (scan.Source, func() struct{}, error) {
						if req != (p.Request{}) {
							return nil, nil, fmt.Errorf("unsupported request %+v", req)
						}
						return adapterOracleScanner(cur), nil, nil
					}
					src, err = adapter.NewReplay(snapshot, input.open, struct{}{}, build, options)
				} else {
					cur := cursor.NewReader(input.open(0))
					src, err = adapter.New(snapshot, adapterOracleScanner(cur), cur, p.Request{}, options)
				}
				require.NoError(t, err)
				session := engine.Start(src, "")
				paused(t, session)
				{
					_, _, err := adapter.Positions(span(0, 0), lookup)
					require.Errorf(t, err, "open snapshot was available for positions")
				}
				input.available = len(text)
				paused(t, session)
				input.sealed = true
				root := done(t, session)
				equalRecords(t, root, want)
				checkBytescanPositions(t, text, root, lookup)
			})
		}
	}
}

func checkBytescanPositions(t *testing.T, text string, root tree.Node, lookup tree.SourceLookup) {
	t.Helper()
	cur := cursor.NewReader(strings.NewReader(text))
	lexer := scan.Skip(adapterOracleScanner(cur), "space", "newline", "comment", "=")
	for i := 0; ; i++ {
		raw, err := lexer.Next()
		if err == io.EOF {
			_, end, posErr := adapter.Positions(root.Info().Span, lookup)
			pos := cur.Cursor()
			require.NoErrorf(t, posErr, "EOF position = %+v, %v; want %s", end, posErr, pos)
			require.Equalf(t, pos.Line(), end.Line, "EOF position = %+v, %v; want %s", end, posErr, pos)
			require.Equalf(t, pos.Column(), end.Column, "EOF position = %+v, %v; want %s", end, posErr, pos)
			break
		}
		child, ok := root.Child(i)
		require.NoErrorf(t, err, "token %d: bytescan error %v, child present %t", i, err, ok)
		require.Truef(t, ok, "token %d: bytescan error %v, child present %t", i, err, ok)
		start, end, err := adapter.Positions(child.Info().Span, lookup)
		wantStart, wantEnd := raw.Span().Start(), raw.Span().End()
		require.NoErrorf(t, err, "node %d positions = %+v, %+v, %v; want %s, %s", i, start, end, err, wantStart, wantEnd)
		require.Equalf(t, wantStart.Line(), start.Line, "node %d positions = %+v, %+v, %v; want %s, %s", i, start, end, err, wantStart, wantEnd)
		require.Equalf(t, wantStart.Column(), start.Column, "node %d positions = %+v, %+v, %v; want %s, %s", i, start, end, err, wantStart, wantEnd)
		require.Equalf(t, wantEnd.Line(), end.Line, "node %d positions = %+v, %+v, %v; want %s, %s", i, start, end, err, wantStart, wantEnd)
		require.Equalf(t, wantEnd.Column(), end.Column, "node %d positions = %+v, %+v, %v; want %s, %s", i, start, end, err, wantStart, wantEnd)
	}
}
