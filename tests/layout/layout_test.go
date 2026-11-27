package layout

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	"github.com/xiam/leafkit/eval"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/internal/reference"
	"github.com/xiam/leafkit/tree"
)

var (
	code     = p.Request{Layout: p.Indentation}
	template = p.Request{Goal: "template-text", Layout: p.Joined}
	embedded = p.Request{Goal: "embedded-code", Layout: p.Joined}
)

// Each suite indents by two spaces. The checkpoint carries the current
// nesting depth and any indentation already read for the new line.
type checkpoint struct {
	level, width int
	lineStart    bool
}

type layoutStream struct {
	cur   *cursor.Reader
	lexer scan.Source
	state checkpoint
}

func (s *layoutStream) Next() (*scan.Token, error) {
	if s.state.lineStart || s.state.level != 0 {
		next, err := s.cur.PeekRune()
		switch {
		case err == io.EOF:
			if s.state.level != 0 {
				s.state.level--
				return scan.NewEmptyToken("dedent", s.cur.Cursor()), nil
			}
			return nil, io.EOF
		case err != nil:
			return nil, err
		case s.state.lineStart:
			switch next.Rune() {
			case ' ':
				token, err := s.lexer.Next()
				if err == nil {
					s.state.width += token.Len()
				}
				return token, err
			case '\r', '\n':
				// A blank line leaves the indentation depth alone.
			default:
				target := s.state.width / 2
				switch {
				case s.state.width%2 != 0 || target > s.state.level+1:
					s.state.lineStart = false
					return scan.NewEmptyToken("bad-indent", s.cur.Cursor()), nil
				case target < s.state.level:
					s.state.level--
					return scan.NewEmptyToken("dedent", s.cur.Cursor()), nil
				case target > s.state.level:
					s.state.level, s.state.lineStart = target, false
					return scan.NewEmptyToken("indent", s.cur.Cursor()), nil
				default:
					s.state.lineStart = false
				}
			}
		}
	}
	token, err := s.lexer.Next()
	if err == nil && token.Type() == "newline" {
		s.state.width, s.state.lineStart = 0, true
	}
	return token, err
}

func scanner(cur *cursor.Reader, saved checkpoint, request p.Request) (scan.Source, func() checkpoint, error) {
	lexer := scan.NewWithCursor(cur)
	switch request {
	case template:
		lexer.MustAddRule("text", core.RuneRun(core.RuneSet("$\"\r\n").Not()))
		lexer.MustAddRule("interpolate", core.Literal("${"))
		lexer.MustAddRule("close", core.Literal("\""))
		lexer.MustAddRule("newline", base.Newline())
		return lexer, nil, nil
	case code, embedded:
		lexer.MustAddRule("space", core.RuneRun(core.RuneSet(" ")))
		lexer.MustAddRule("name", core.RuneRun(core.UnicodeLetter))
		lexer.MustAddRule("number", core.RuneRun(core.ASCIIDigit))
		for _, symbol := range []string{"+", "=", ":", "\"", "}"} {
			lexer.MustAddRule(scan.TokenType(symbol), core.Literal(symbol))
		}
		if request == embedded {
			lexer.MustAddRule("joined-newline", base.Newline())
			return lexer, nil, nil
		}
		lexer.MustAddRule("newline", base.Newline())
		stream := &layoutStream{cur: cur, lexer: lexer, state: saved}
		return stream, func() checkpoint { return stream.state }, nil
	default:
		return nil, nil, fmt.Errorf("unsupported layout request %+v", request)
	}
}

func source(input *reference.Input) (p.Source, error) {
	options := adapter.Options{
		Trivia:   []scan.TokenType{"space", "joined-newline"},
		Newlines: []scan.TokenType{"newline", "joined-newline"},
	}
	open := func(offset int) io.Reader { return input.Open(offset, scan.ErrMoreInput) }
	return adapter.NewReplay(input.ID(), open, checkpoint{lineStart: true}, scanner, options)
}

func grammar() p.Grammar {
	token, skip := reference.Token, reference.Skip
	line, expr := p.Ref("line"), p.Ref("expr")
	name := p.Make("name", "name", token("name"))
	number := p.Make("number", "number", token("number"))
	text := p.Make("text", "text", token("text"))
	interpolation := p.Make("part", "interpolate", p.Seq(skip("interpolate"), p.Lex(embedded, p.Seq(expr, skip("}")))))
	piece := p.Choose(p.ChoiceSpec{}, text, interpolation)
	quoted := p.Make("value", "template", p.Seq(skip("\""), p.Lex(template, p.Seq(p.Many(piece), skip("close")))))
	value := p.Choose(p.ChoiceSpec{}, quoted, expr)

	// "match =" falls through to assignment. "match name" commits to the
	// header. Elsewhere the scanner still supplies the name token "match".
	header := p.Make("statement", "match", p.Seq(
		p.Drop(p.Tok(p.Word("match"))), name, skip(":"), skip("newline"),
		p.Many(skip("newline")), skip("indent"), line, p.Many(line), skip("dedent")))
	assignment := p.Make("statement", "set", p.Seq(name, skip("="), value, skip("newline")))
	statement := p.Choose(p.ChoiceSpec{Lookahead: 2}, header, assignment)
	boundary := p.Boundary{At: []p.Pattern{p.Kind("newline"), p.Kind("dedent")}}
	return p.Grammar{Start: "file", Default: code, Rules: map[string]p.Rule{
		"file": p.Many(line),
		"line": p.Choose(p.ChoiceSpec{}, skip("newline"), p.Seq(
			p.Not(p.EndOfFile()), p.Not(p.Kind("dedent")),
			p.Recover("line", statement, boundary), p.Opt(skip("newline")))),
		"expr": p.Expr("expr", p.Choose(p.ChoiceSpec{}, number, name),
			p.Operator{Form: p.Infix, Match: p.Kind("+"), Power: 1, Kind: "add"}),
	}}
}

func printLayout(root tree.Node) (string, error) {
	text := func(frame eval.Frame, _ *eval.Env[int], _ eval.Descend[string, int]) (string, error) {
		return frame.Node.(*tree.Value[string]).Value(), nil
	}
	return eval.Fold(root, eval.NewEnv[int](nil), map[tree.Kind]eval.Rule[string, int]{
		"file": combine("", "", ""), "add": combine("", " + ", ""), "template": combine("\"", "", "\""),
		"interpolate": combine("${", "", "}"), "match": printHeader, "set": printAssignment,
		"name": text, "number": text, "text": text,
	})
}

func combine(open, sep, close string) eval.Rule[string, int] {
	return func(frame eval.Frame, env *eval.Env[int], child eval.Descend[string, int]) (string, error) {
		content, err := children(frame, env, child, 0, sep)
		return open + content + close, err
	}
}

func children(frame eval.Frame, env *eval.Env[int], child eval.Descend[string, int], start int, sep string) (string, error) {
	var parts []string
	for i := start; ; i++ {
		if _, ok := frame.Node.Child(i); !ok {
			return strings.Join(parts, sep), nil
		}
		part, err := child(i, env)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
}

func printHeader(frame eval.Frame, env *eval.Env[int], child eval.Descend[string, int]) (string, error) {
	subject, err := child(0, env)
	if err != nil {
		return "", err
	}
	depth, _ := env.Get("depth")
	suite := eval.NewEnv(env)
	suite.Set("depth", depth+1)
	body, err := children(frame, suite, child, 1, "")
	return strings.Repeat("  ", depth) + "match " + subject + ":\n" + body, err
}

func printAssignment(frame eval.Frame, env *eval.Env[int], child eval.Descend[string, int]) (string, error) {
	line, err := children(frame, env, child, 0, " = ")
	depth, _ := env.Get("depth")
	return strings.Repeat("  ", depth) + line + "\n", err
}

func TestLayoutGrammar(t *testing.T) {
	engine := reference.Engine(t, grammar(), reference.Builders("name", "number", "text"))
	cases := []struct {
		name, input, shape, printed string
		errors                      []reference.Diagnostic
	}{
		{"nested suites and final dedents", "match main:\n  match part:\n    match = \"v=${1+2}\"\n",
			`(file (match (name "main") (match (name "part") (set (name "match") (template (text "v=") (interpolate (add (number "1") (number "2"))))))))`,
			"match main:\n  match part:\n    match = \"v=${1 + 2}\"\n", nil},
		{"joined embedded line and dedent to following statement", "match topic:\n  match = \"sum ${1 +\n    2}\"\nnext = 3\n",
			`(file (match (name "topic") (set (name "match") (template (text "sum ") (interpolate (add (number "1") (number "2")))))) (set (name "next") (number "3")))`,
			"match topic:\n  match = \"sum ${1 + 2}\"\nnext = 3\n", nil},
		{"contextual word outside header", "match = 4\r\n",
			`(file (set (name "match") (number "4")))`, "match = 4\n", nil},
		{"missing value in a suite", "match x:\n  match = \nnext = 3\n",
			`(file (match (name "x") (leafkit:error)) (set (name "next") (number "3")))`, "",
			[]reference.Diagnostic{{Slot: "line", Start: 11, End: 18, At: 19, Found: "\n", Skipped: "match=", Expected: []p.Pattern{p.Kind("\""), p.Kind("number"), p.Kind("name")}}}},
		{"bad indentation keeps the next line", "match x:\n   y = 1\nnext = 2\n",
			`(file (leafkit:error) (set (name "next") (number "2")))`, "",
			[]reference.Diagnostic{{Slot: "line", Start: 0, End: 17, At: 12, Found: "<bad-indent>", Skipped: "matchx:\ny=1", Expected: []p.Pattern{p.Kind("indent")}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, streamed := range []bool{false, true} {
				root := reference.Parse(t, engine, source, tc.input, streamed, "")
				reference.Check(t, root, tc.shape, tc.errors...)
				if len(tc.errors) == 0 {
					got, err := printLayout(root)
					if assert.NoErrorf(t, err, "print = %q, %v, want %q", got, err, tc.printed) {
						assert.Equalf(t, tc.printed, got, "print = %q, %v, want %q", got, err, tc.printed)
					}
				}
			}
		})
	}
}
