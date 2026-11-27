package tests

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
)

// This fixture accepts two indentation widths and has one level. It keeps
// layout state in the checkpoint, including indentation read before a pause.
type layoutState struct {
	pending, indented bool
	width             int
}

type layoutSource struct {
	cur    *cursor.Reader
	lexer  scan.Source
	layout p.Layout
	state  layoutState
}

func (l *layoutSource) Next() (*scan.Token, error) {
	if l.layout == p.Indentation && l.state.pending {
		next, err := l.cur.PeekRune()
		if err != nil && err != io.EOF {
			return nil, err
		}
		switch next.Rune() {
		case ' ':
			token, err := l.lexer.Next()
			if err == nil {
				l.state.width += token.Len()
			}
			return token, err
		case '\r', '\n':
			// A blank line does not change the indentation.
		default:
			if l.state.width != 0 && l.state.width != 2 {
				return nil, fmt.Errorf("unsupported indentation width %d", l.state.width)
			}
			l.state.pending = false
			indented := l.state.width == 2 && err != io.EOF
			if indented != l.state.indented {
				l.state.indented = indented
				kind := scan.TokenType("dedent")
				if indented {
					kind = "indent"
				}
				return scan.NewEmptyToken(kind, l.cur.Cursor()), nil
			}
		}
	}
	token, err := l.lexer.Next()
	if err != nil || token.Type() != "newline" {
		return token, err
	}
	switch l.layout {
	case p.Indentation:
		l.state.pending, l.state.width = true, 0
	case p.Joined, p.TriviaOnly:
		return token.WithType("hidden-newline"), nil
	}
	return token, nil
}

func layoutFactory(cur *cursor.Reader, state layoutState, req p.Request) (scan.Source, func() layoutState, error) {
	if req.Goal != "" || req.Argument != "" {
		return nil, nil, fmt.Errorf("layout fixture does not support %+v", req)
	}
	lexer := scan.NewWithCursor(cur)
	lexer.MustAddRule("word", core.RuneRun(core.UnicodeLetter))
	lexer.MustAddRule("space", core.RuneRun(core.RuneSet(" ")))
	lexer.MustAddRule("newline", base.Newline())
	filter := &layoutSource{cur: cur, lexer: lexer, layout: req.Layout, state: state}
	return filter, func() layoutState { return filter.state }, nil
}

func TestBytescanLayoutAndContextualWord(t *testing.T) {
	const text = "match\r\n  match\nmatch"
	input := &adapterInput{text: text}
	options := adapter.Options{
		Trivia:   []scan.TokenType{"space", "hidden-newline"},
		Newlines: []scan.TokenType{"newline", "hidden-newline"},
	}
	src, err := adapter.NewReplay(snapshot, input.open, layoutState{}, layoutFactory, options)
	require.NoError(t, err)
	indentation := p.Request{Layout: p.Indentation}
	word := p.Make("word", "word", tagged("word"))
	rule := p.Seq(
		p.Drop(p.Tok(p.Word("match"))), skip("newline"), skip("indent"),
		p.Not(p.OnSameLine(p.Kind("word"))), word, skip("newline"),
		skip("dedent"), p.Not(p.OnSameLine(p.Kind("word"))), word,
	)
	engine := configured(t, p.Grammar{
		Start: "main", Default: indentation, Rules: map[string]p.Rule{"main": rule},
	}, builders(), p.Limits{})
	session := engine.Start(src, "")
	for cut := 0; cut <= len(text); cut++ {
		input.available = cut
		paused(t, session)
	}
	input.sealed = true
	equalRecords(t, done(t, session), []record{
		{"file", span(0, 20), "", 2}, {"word", span(9, 14), "match", 0}, {"word", span(15, 20), "match", 0},
	})

	// Read the same mark under all layout profiles. Visible markers own no
	// bytes and take the leading gap once. The physical break survives
	// suppression and survives the virtual indent.
	src, err = adapter.NewReplay(snapshot, input.open, layoutState{}, layoutFactory, options)
	require.NoError(t, err)
	_, start := src.Begin()
	first := readAdapterToken(t, src, start, indentation, "word")
	newline := readAdapterToken(t, src, first.Next, indentation, "newline")
	indent := readAdapterToken(t, src, newline.Next, indentation, "indent")
	nested := readAdapterToken(t, src, indent.Next, indentation, "word")
	nextLine := readAdapterToken(t, src, nested.Next, indentation, "newline")
	dedent := readAdapterToken(t, src, nextLine.Next, indentation, "dedent")
	require.Truef(t, indent.Token.Synthetic, "layout records: indent %+v, word %+v, dedent %+v", indent, nested, dedent)
	require.Equalf(t, span(9, 9), indent.Token.Span, "layout records: indent %+v, word %+v, dedent %+v", indent, nested, dedent)
	require.Equalf(t, []p.Trivia{{Kind: "space", Span: span(7, 9)}}, indent.Token.Leading, "layout records: indent %+v, word %+v, dedent %+v", indent, nested, dedent)
	require.Truef(t, nested.Token.BreakBefore, "layout records: indent %+v, word %+v, dedent %+v", indent, nested, dedent)
	require.Lenf(t, nested.Token.Leading, 0, "layout records: indent %+v, word %+v, dedent %+v", indent, nested, dedent)
	require.Truef(t, dedent.Token.Synthetic, "layout records: indent %+v, word %+v, dedent %+v", indent, nested, dedent)
	require.Equalf(t, nextLine.Next.Byte, dedent.Next.Byte, "layout records: indent %+v, word %+v, dedent %+v", indent, nested, dedent)
	require.Equalf(t, nextLine.Next.Virtual+1, dedent.Next.Virtual, "layout records: indent %+v, word %+v, dedent %+v", indent, nested, dedent)
	lines := p.Request{Layout: p.Lines}
	visible := readAdapterToken(t, src, first.Next, lines, "newline")
	readAdapterToken(t, src, visible.Next, lines, "word")
	for _, layout := range []p.Layout{p.Joined, p.TriviaOnly} {
		joined := readAdapterToken(t, src, first.Next, p.Request{Layout: layout}, "word")
		want := []p.Trivia{{Kind: "hidden-newline", Span: span(5, 7)}, {Kind: "space", Span: span(7, 9)}}
		require.Truef(t, joined.Token.BreakBefore, "suppressed break for layout %d = %+v", layout, joined)
		require.Equalf(t, want, joined.Token.Leading, "suppressed break for layout %d = %+v", layout, joined)
	}
}

func TestBytescanLayoutFlushesVirtualTokenBeforeEOF(t *testing.T) {
	const text = "match\n  match\n"
	req := p.Request{Layout: p.Indentation}
	src, err := adapter.NewReplay(snapshot, func(offset int) io.Reader {
		return strings.NewReader(text[offset:])
	}, layoutState{}, layoutFactory, adapter.Options{
		Trivia: []scan.TokenType{"space"}, Newlines: []scan.TokenType{"newline"},
	})
	require.NoError(t, err)
	_, mark := src.Begin()
	for _, kind := range []p.TokenKind{"word", "newline", "indent", "word", "newline"} {
		mark = readAdapterToken(t, src, mark, req, kind).Next
	}
	dedent := readAdapterToken(t, src, mark, req, "dedent")
	end, err := src.Read(dedent.Next, req)
	require.NoErrorf(t, err, "EOF after final dedent = %+v, %v", end, err)
	require.Equalf(t, p.EndOfInput, end.Status, "EOF after final dedent = %+v, %v", end, err)
	require.Equalf(t, len(text), end.Next.Byte, "EOF after final dedent = %+v, %v", end, err)
}

func readAdapterToken(t *testing.T, src p.Source, mark p.Mark, request p.Request, want p.TokenKind) p.Read {
	t.Helper()
	read, err := src.Read(mark, request)
	require.NoErrorf(t, err, "Read(%+v, %+v) = %+v, %v; want %q", mark, request, read, err, want)
	require.Equalf(t, p.TokenReady, read.Status, "Read(%+v, %+v) = %+v, %v; want %q", mark, request, read, err, want)
	require.Equalf(t, want, read.Token.Kind, "Read(%+v, %+v) = %+v, %v; want %q", mark, request, read, err, want)
	return read
}
