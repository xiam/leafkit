package tests

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
)

func interpolationDriver(cur *cursor.Reader) *modes.Driver {
	code := scan.NewWithCursor(cur)
	text := scan.NewWithCursor(cur)
	expression := scan.NewWithCursor(cur)
	code.MustAddRule("open", core.Literal("\""))
	text.MustAddRule("text", core.RuneRun(core.RuneSet("$\"").Not()))
	text.MustAddRule("interpolate", core.Literal("${"))
	text.MustAddRule("close", core.Literal("\""))
	expression.MustAddRule("word", core.RuneRun(core.UnicodeLetter))
	expression.MustAddRule("end-interpolation", core.Literal("}"))
	return modes.New(cur, code, modes.WithSwitch(func(_ *scan.Scanner, token *scan.Token) *scan.Scanner {
		switch token.Type() {
		case "open", "end-interpolation":
			return text
		case "interpolate":
			return expression
		case "close":
			return code
		default:
			return nil
		}
	}))
}

func TestBytescanInterpolationModes(t *testing.T) {
	const input = `"a${é}b"`
	content := core.RuneRun(core.RuneSet("$\"").Not())
	word := core.RuneRun(core.UnicodeLetter)
	modeSet := modes.NewModeSet("code")
	modeSet.Mode("code").Push("open", core.Literal("\""), "text")
	modeSet.Mode("text").Rule("text", content).Push("interpolate", core.Literal("${"), "expression").Pop("close", core.Literal("\""))
	modeSet.Mode("expression").Rule("word", word).Pop("end-interpolation", core.Literal("}"))
	build := func(cur *cursor.Reader, state modes.Stack, req p.Request) (scan.Source, func() modes.Stack, error) {
		if req != (p.Request{}) {
			return nil, nil, fmt.Errorf("unsupported request %+v", req)
		}
		driver, err := modeSet.Driver(cur, state)
		if err != nil {
			return nil, nil, err
		}
		return driver, driver.Stack, nil
	}
	textNode := p.Make("text", "value", tagged("text"))
	wordNode := p.Make("word", "word", tagged("word"))
	rule := p.Seq(skip("open"), textNode, skip("interpolate"), wordNode, skip("end-interpolation"), textNode, skip("close"))
	engine := configured(t, p.Grammar{Start: "main", Rules: map[string]p.Rule{"main": rule}}, builders(), p.Limits{})
	want := []record{{"file", span(0, 9), "", 3}, {"value", span(1, 2), "a", 0},
		{"word", span(4, 6), "é", 0}, {"value", span(7, 8), "b", 0}}

	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart=%t", restart), func(t *testing.T) {
			fed := &adapterInput{text: input}
			var src p.Source
			var err error
			if restart {
				src, err = adapter.NewReplay(snapshot, fed.open, modes.Stack{}, build, adapter.Options{})
			} else {
				cur := cursor.NewReader(fed.open(0))
				src, err = adapter.New(snapshot, interpolationDriver(cur), cur, p.Request{}, adapter.Options{})
			}
			require.NoError(t, err)
			session := engine.Start(src, "")
			for cut := 0; cut <= len(input); cut++ {
				fed.available = cut
				paused(t, session)
			}
			fed.sealed = true
			equalRecords(t, done(t, session), want)
		})
	}
}

func TestBytescanParserSelectsOperatorOrLiteral(t *testing.T) {
	operator := p.Request{Goal: "operator"}
	literal := p.Request{Goal: "literal", Argument: "/"}
	inside := core.RuneRun(core.UnicodeLetter)
	build := func(cur *cursor.Reader, _ struct{}, req p.Request) (scan.Source, func() struct{}, error) {
		lexer := scan.NewWithCursor(cur)
		switch req {
		case literal:
			delimiter := core.Literal(req.Argument)
			lexer.MustAddRule("literal", core.Sequence(delimiter, inside, delimiter))
		case operator, p.Request{}:
			lexer.MustAddRule("operator", core.Literal("/"))
			lexer.MustAddRule("word", inside)
		default:
			return nil, nil, fmt.Errorf("unsupported request %+v", req)
		}
		return lexer, nil, nil
	}
	options := adapter.Options{}
	input := &adapterInput{text: "/fox/"}
	src, err := adapter.NewReplay(snapshot, input.open, struct{}{}, build, options)
	require.NoError(t, err)
	_, mark := src.Begin()
	input.available = 2
	op, err := src.Read(mark, operator)
	require.NoErrorf(t, err, "operator at same mark = %+v, %v", op, err)
	require.Equalf(t, p.TokenReady, op.Status, "operator at same mark = %+v, %v", op, err)
	require.Equalf(t, "/", op.Token.Text, "operator at same mark = %+v, %v", op, err)
	require.Equalf(t, 1, op.Next.Byte, "operator at same mark = %+v, %v", op, err)
	before, err := src.Read(mark, literal)
	require.NoErrorf(t, err, "open literal = %+v, %v", before, err)
	require.Equalf(t, p.Read{Status: p.NeedInput}, before, "open literal = %+v, %v", before, err)
	input.available = len(input.text)
	input.sealed = true
	lit, err := src.Read(mark, literal)
	require.NoErrorf(t, err, "resumed literal at same mark = %+v, %v", lit, err)
	require.Equalf(t, p.TokenReady, lit.Status, "resumed literal at same mark = %+v, %v", lit, err)
	require.Equalf(t, "/fox/", lit.Token.Text, "resumed literal at same mark = %+v, %v", lit, err)
	require.Equalf(t, 5, lit.Next.Byte, "resumed literal at same mark = %+v, %v", lit, err)
	again, err := src.Read(mark, operator)
	require.NoErrorf(t, err, "operator changed after feed = %+v, %v", again, err)
	require.Equalf(t, op, again, "operator changed after feed = %+v, %v", again, err)
	{
		_, err := src.Read(mark, p.Request{Goal: "literal", Argument: "#"})
		require.Errorf(t, err, "unsupported argument accepted")
	}

	// The first arm consumes an operator and misses on the next token.
	// The second arm restarts at the original mark under another request.
	first := p.Seq(p.Lex(operator, skip("operator")), tagged("absent"))
	second := p.Make("literal", "value", p.Lex(literal, tagged("literal")))
	choice := p.Choose(p.ChoiceSpec{Lookahead: 2}, first, second)
	engine := configured(t, p.Grammar{Start: "main", Rules: map[string]p.Rule{"main": choice}}, builders(), p.Limits{})
	input.available, input.sealed = 2, false
	fresh, err := adapter.NewReplay(snapshot, input.open, struct{}{}, build, options)
	require.NoError(t, err)
	session := engine.Start(fresh, "")
	paused(t, session)
	input.available, input.sealed = len(input.text), true
	equalRecords(t, done(t, session), []record{{"file", span(0, 5), "", 1}, {"value", span(0, 5), "/fox/", 0}})
}

func TestBytescanReplayPreservesBeginningOfLine(t *testing.T) {
	build := func(cur *cursor.Reader, _ struct{}, _ p.Request) (scan.Source, func() struct{}, error) {
		lexer := scan.NewWithCursor(cur)
		lexer.MustAddRule("at-line-start", core.AtLineStart(core.Literal("x")))
		lexer.MustAddRule("ordinary", core.Literal("x"))
		lexer.MustAddRule("newline", base.Newline())
		return lexer, nil, nil
	}
	src, err := adapter.NewReplay(snapshot, func(offset int) io.Reader {
		return strings.NewReader("xx\nx"[offset:])
	}, struct{}{}, build, adapter.Options{Newlines: []scan.TokenType{"newline"}})
	require.NoError(t, err)
	_, mark := src.Begin()
	for _, want := range []p.TokenKind{"at-line-start", "ordinary", "newline", "at-line-start"} {
		read, err := src.Read(mark, p.Request{Layout: p.Lines})
		require.NoErrorf(t, err, "at %+v: %+v, %v; want %q", mark, read, err, want)
		require.Equalf(t, p.TokenReady, read.Status, "at %+v: %+v, %v; want %q", mark, read, err, want)
		require.Equalf(t, want, read.Token.Kind, "at %+v: %+v, %v; want %q", mark, read, err, want)
		mark = read.Next
	}
}
