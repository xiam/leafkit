package jsonexample

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	"github.com/xiam/leafkit/eval"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/internal/reference"
	"github.com/xiam/leafkit/tree"
)

func scanner(cur *cursor.Reader) *scan.Scanner {
	lexer := scan.NewWithCursor(cur)
	lexer.MustAddRule("space", core.RuneRun(core.RuneSet(" \r\n\t")))
	lexer.MustAddRule("boolean", core.AnyLiteral("true", "false"))
	lexer.MustAddRule("null", core.Literal("null"))
	lexer.MustAddRule("string", stringRule())
	lexer.MustAddRule("number", numberRule())
	for _, symbol := range []string{"{", "}", "[", "]", ":", ","} {
		lexer.MustAddRule(scan.TokenType(symbol), core.Literal(symbol))
	}
	return lexer
}

func stringRule() scan.Rule {
	quote := core.Literal(`"`)
	character := core.Rune(func(r rune) bool { return r >= ' ' && r != '"' && r != '\\' })
	hexEscape := core.Sequence(core.Literal("u"), core.Repeat(core.Rune(core.ASCIIHexDigit), 4, 4))
	escape := core.Sequence(core.Literal(`\`), core.Choice(core.Rune(core.RuneSet(`"\/bfnrt`)), hexEscape))
	return core.Sequence(quote, core.ZeroOrMore(core.Choice(character, escape)), quote)
}

func numberRule() scan.Rule {
	digits := core.RuneRun(core.ASCIIDigit)
	integer := core.Choice(core.Literal("0"), core.Sequence(core.Rune(core.RuneRange('1', '9')), core.Optional(digits)))
	fraction := core.Sequence(core.Literal("."), digits)
	exponent := core.Sequence(core.Rune(core.RuneSet("eE")), core.Optional(core.Rune(core.RuneSet("+-"))), digits)
	return core.Sequence(core.Optional(core.Literal("-")), integer, core.Optional(fraction), core.Optional(exponent))
}

func source(input *reference.Input) (p.Source, error) {
	cur := cursor.NewReader(input.Open(0, scan.ErrMoreInput))
	return adapter.New(input.ID(), scanner(cur), cur, p.Request{}, adapter.Options{Trivia: []scan.TokenType{"space"}})
}

func grammar() p.Grammar {
	skip := reference.Skip
	value := p.Ref("value")
	leaf := func(kind p.TokenKind) p.Rule {
		return p.Make("value", tree.Kind(kind), reference.Token(kind))
	}
	pair := p.Make("pair", "pair", p.Seq(leaf("string"), skip(":"),
		p.Recover("value", value, p.Boundary{At: []p.Pattern{p.Kind(","), p.Kind("}")}})))
	return p.Grammar{Start: "value", Rules: map[string]p.Rule{
		"value": p.Choose(p.ChoiceSpec{},
			p.Make("value", "object", p.Seq(skip("{"), p.Sep(pair, skip(","), false), skip("}"))),
			p.Make("value", "array", p.Seq(skip("["), p.Sep(value, skip(","), false), skip("]"))),
			leaf("string"), leaf("number"), leaf("boolean"), leaf("null")),
	}}
}

func printJSON(root tree.Node) (string, error) {
	raw := func(frame eval.Frame, _ *eval.Env[struct{}], _ eval.Descend[string, struct{}]) (string, error) {
		return frame.Node.(*tree.Value[string]).Value(), nil
	}
	return eval.Fold(root, nil, map[tree.Kind]eval.Rule[string, struct{}]{
		"file": join("", "", ""), "object": join("{", ",", "}"), "array": join("[", ",", "]"), "pair": join("", ":", ""),
		"string": raw, "number": raw, "boolean": raw, "null": raw,
	})
}

func join(open, separator, close string) eval.Rule[string, struct{}] {
	return func(frame eval.Frame, env *eval.Env[struct{}], child eval.Descend[string, struct{}]) (string, error) {
		var parts []string
		for i := 0; ; i++ {
			if _, ok := frame.Node.Child(i); !ok {
				break
			}
			part, err := child(i, env)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		return open + strings.Join(parts, separator) + close, nil
	}
}

func TestJSONGrammar(t *testing.T) {
	engine := reference.Engine(t, grammar(), reference.Builders("string", "number", "boolean", "null"))
	valuePatterns := []p.Pattern{p.Kind("{"), p.Kind("["), p.Kind("string"), p.Kind("number"), p.Kind("boolean"), p.Kind("null")}
	cases := []struct {
		name, input, tree, printed string
		errors                     []reference.Diagnostic
	}{
		{"nested", `{ "n": [ -0.25e+2, true, null ], "e": {} }`,
			`(file (object (pair (string "\"n\"") (array (number "-0.25e+2") (boolean "true") (null "null"))) (pair (string "\"e\"") (object))))`,
			`{"n":[-0.25e+2,true,null],"e":{}}`, nil},
		{"strings and empty array", `["é\u03c0\n\"",false,[]]`,
			`(file (array (string "\"é\\u03c0\\n\\\"\"") (boolean "false") (array)))`,
			`["é\u03c0\n\"",false,[]]`, nil},
		{"missing object value", `{"a":,"b":1}`,
			`(file (object (pair (string "\"a\"") (leafkit:error)) (pair (string "\"b\"") (number "1"))))`, "",
			[]reference.Diagnostic{{Slot: "value", Start: 5, End: 5, At: 5, Found: ",", Expected: valuePatterns}}},
		{"trailing comma", "[1,]", "(file (leafkit:error))", "",
			[]reference.Diagnostic{{Slot: "file", Start: 0, End: 4, At: 3, Found: "]", Skipped: "[1,]", Expected: valuePatterns}}},
		{"leading zero", "01", `(file (number "0") (leafkit:error))`, "",
			[]reference.Diagnostic{{Slot: "file", Start: 1, End: 2, At: 1, Found: "1", Skipped: "1", Expected: []p.Pattern{p.EndOfFile()}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equalf(t, (len(tc.errors) == 0), json.Valid([]byte(tc.input)), "encoding/json disagrees with fixture %q", tc.input)
			for _, streamed := range []bool{false, true} {
				root := reference.Parse(t, engine, source, tc.input, streamed, "")
				reference.Check(t, root, tc.tree, tc.errors...)
				if len(tc.errors) == 0 {
					printed, err := printJSON(root)
					if assert.NoError(t, err) && assert.Equal(t, tc.printed, printed) {
						assert.Truef(t, json.Valid([]byte(printed)), "invalid JSON: %q", printed)
					}
				}
			}
		})
	}
}
