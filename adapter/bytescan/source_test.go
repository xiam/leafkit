package bytescan

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/leafkit/parser"
)

type sourceFunc func() (*scan.Token, error)

func (f sourceFunc) Next() (*scan.Token, error) { return f() }

func TestFixedReadRetainsTextAndTrivia(t *testing.T) {
	cur := cursor.NewReader(strings.NewReader(" a b\n"))
	reused := make([]rune, 1)
	lexer := sourceFunc(func() (*scan.Token, error) {
		r, err := cur.ReadLocatedRune()
		if err != nil {
			return nil, err
		}
		reused[0] = r.Rune()
		kind := scan.TokenType("word")
		if r.Rune() == ' ' || r.Rune() == '\n' {
			kind = "space"
		}
		return scan.NewTokenWithSpan(kind, reused, r.Span()), nil
	})
	src, err := New("buffer", lexer, cur, parser.Request{}, Options{Trivia: []scan.TokenType{"space"}})
	require.NoError(t, err)
	_, mark := src.Begin()
	first := requireRead(t, src, mark, parser.Request{}, parser.TokenReady)
	second := requireRead(t, src, first.Next, parser.Request{}, parser.TokenReady)
	end := requireRead(t, src, second.Next, parser.Request{}, parser.EndOfInput)
	replayed := requireRead(t, src, mark, parser.Request{}, parser.TokenReady)
	require.Equalf(t, first, replayed, "replay = %+v, want %+v", replayed, first)
	src.DiscardBefore(second.Next.Byte)
	{
		_, err := src.Read(mark, parser.Request{})
		require.Errorf(t, err, "discarded mark still reads")
	}
	require.Equalf(t, "a", first.Token.Text, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	require.Equalf(t, "b", second.Token.Text, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	require.Lenf(t, first.Token.Leading, 1, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	require.Equalf(t, 0, first.Token.Leading[0].Span.Start, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	require.Lenf(t, second.Token.Leading, 1, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	require.Equalf(t, 2, second.Token.Leading[0].Span.Start, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	require.Lenf(t, end.Trailing, 1, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	require.Equalf(t, 4, end.Trailing[0].Span.Start, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	require.Equalf(t, 5, end.Next.Byte, "after discard and buffer reuse: first %+v, second %+v, EOF %+v", first, second, end)
	{
		_, err := src.Read(second.Next, parser.Request{Goal: "other"})
		require.Errorf(t, err, "fixed stream accepted another request at EOF")
	}
}

func TestAdapterReportsInvalidAndSourceFailures(t *testing.T) {
	t.Run("unmatched", func(t *testing.T) {
		cur := cursor.NewReader(strings.NewReader("?"))
		lexer := scan.NewWithCursor(cur)
		lexer.MustAddRule("word", core.Literal("x"))
		src, err := New("input", lexer, cur, parser.Request{}, Options{Trivia: []scan.TokenType{scan.TokenTypeUnknown}})
		require.NoError(t, err)
		_, mark := src.Begin()
		read := requireRead(t, src, mark, parser.Request{}, parser.TokenReady)
		require.Equalf(t, parser.Invalid, read.Token.Kind, "unmatched character = %+v", read)
		require.Equalf(t, "?", read.Token.Text, "unmatched character = %+v", read)
		require.Equalf(t, 1, read.Token.Span.End, "unmatched character = %+v", read)
	})

	t.Run("hidden-trailing-token", func(t *testing.T) {
		cur := cursor.NewReader(strings.NewReader(" "))
		lexer := scan.NewWithCursor(cur)
		lexer.MustAddRule("space", core.Literal(" "))
		src, err := New("input", scan.Skip(lexer, "space"), cur, parser.Request{}, Options{})
		require.NoError(t, err)
		_, mark := src.Begin()
		{
			_, err := src.Read(mark, parser.Request{})
			require.Errorf(t, err, "accepted EOF after bytes hidden by a filter")
		}
	})

	t.Run("io-error", func(t *testing.T) {
		cur := cursor.NewReader(strings.NewReader(""))
		fault := errors.New("read failed")
		src, err := New("input", sourceFunc(func() (*scan.Token, error) { return nil, fault }), cur, parser.Request{}, Options{})
		require.NoError(t, err)
		_, mark := src.Begin()
		{
			_, err := src.Read(mark, parser.Request{})
			require.ErrorIsf(t, err, fault, "source error = %v, want %v", err, fault)
		}
	})
}

func TestReplayKeepsSiblingState(t *testing.T) {
	build := func(cur *cursor.Reader, state bool, req parser.Request) (scan.Source, func() bool, error) {
		if req.Goal == "change" {
			state = !state
		}
		lexer := scan.NewWithCursor(cur)
		lexer.MustAddRule("open", core.Literal("<"))
		kind := scan.TokenType("ordinary")
		if state {
			kind = "changed"
		}
		lexer.MustAddRule(kind, core.Literal("x"))
		return lexer, func() bool { return state }, nil
	}
	src, err := NewReplay("siblings", func(offset int) io.Reader {
		return strings.NewReader("<x"[offset:])
	}, false, build, Options{})
	require.NoError(t, err)
	_, start := src.Begin()
	ordinary := requireRead(t, src, start, parser.Request{}, parser.TokenReady)
	changed := requireRead(t, src, start, parser.Request{Goal: "change"}, parser.TokenReady)
	require.Equalf(t, changed.Next.Byte, ordinary.Next.Byte, "same byte requires separate state identities: %+v, %+v", ordinary.Next, changed.Next)
	require.NotEqualf(t, changed.Next.Key, ordinary.Next.Key, "same byte requires separate state identities: %+v, %+v", ordinary.Next, changed.Next)
	src.DiscardBefore(ordinary.Next.Byte)
	for _, tc := range []struct {
		mark parser.Mark
		want parser.TokenKind
	}{
		{ordinary.Next, "ordinary"}, {changed.Next, "changed"}, {ordinary.Next, "ordinary"},
	} {
		got := requireRead(t, src, tc.mark, parser.Request{}, parser.TokenReady)
		require.Equalf(t, tc.want, got.Token.Kind, "read from %+v = %q, want %q", tc.mark, got.Token.Kind, tc.want)
	}
}

func requireRead(t *testing.T, src parser.Source, mark parser.Mark, req parser.Request, status parser.ReadStatus) parser.Read {
	t.Helper()
	read, err := src.Read(mark, req)
	require.NoErrorf(t, err, "Read(%+v, %+v) = %+v, %v, want status %v", mark, req, read, err, status)
	require.Equalf(t, status, read.Status, "Read(%+v, %+v) = %+v, %v, want status %v", mark, req, read, err, status)
	return read
}

func TestReplayFactoryCanPause(t *testing.T) {
	input := ""
	build := func(cur *cursor.Reader, _ struct{}, _ parser.Request) (scan.Source, func() struct{}, error) {
		next, err := cur.PeekRune()
		if err != nil && err != io.EOF {
			return nil, nil, err
		}
		lexer := scan.NewWithCursor(cur)
		lexer.MustAddRule("word", core.Literal(string(next.Rune())))
		return lexer, nil, nil
	}
	src, err := NewReplay("factory", func(offset int) io.Reader {
		if input == "" {
			return readerFunc(func([]byte) (int, error) { return 0, scan.ErrMoreInput })
		}
		return strings.NewReader(input[offset:])
	}, struct{}{}, build, Options{})
	require.NoError(t, err)
	_, mark := src.Begin()
	requireRead(t, src, mark, parser.Request{}, parser.NeedInput)
	input = "a"
	got := requireRead(t, src, mark, parser.Request{}, parser.TokenReady)
	require.Equalf(t, "a", got.Token.Text, "resumed factory = %+v", got)
	require.Equalf(t, 1, got.Next.Byte, "resumed factory = %+v", got)
	requireRead(t, src, got.Next, parser.Request{}, parser.EndOfInput)
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(buf []byte) (int, error) { return f(buf) }
