package tests

import (
	"fmt"
	"strconv"
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

func fixtureToken(kind p.TokenKind, text string, start, end int) p.Token {
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

func sourceFor(tokens []p.Token, end, available *int, final *bool) *fixture {
	byMark := make(map[int]p.Token)
	mark := 0
	for _, tok := range tokens {
		byMark[mark] = tok
		mark = tok.Span.End
	}
	return &fixture{read: func(m p.Mark, _ p.Request) (p.Read, error) {
		if tok, ok := byMark[m.Byte]; ok {
			if tok.Span.End > *available {
				return shortage(), nil
			}
			return ready(tok, p.Mark{Byte: tok.Span.End}), nil
		}
		if *final {
			return eof(*end), nil
		}
		return shortage(), nil
	}}
}

func builders() p.Builders[tree.Node] {
	return p.Builders[tree.Node]{
		Node: func(in p.Build[tree.Node]) (tree.Node, error) {
			var children []tree.Node
			for _, field := range in.Fields {
				children = append(children, field.Nodes...)
			}
			if in.Kind == "word" || in.Kind == "number" || in.Kind == "string" ||
				in.Kind == "bool" || in.Kind == "null" || in.Kind == "value" {
				tok, ok := in.Tokens.At(0)
				if !ok || in.Tokens.Len() != 1 {
					return nil, fmt.Errorf("leaf %q requires one owned token", in.Kind)
				}
				text := tok.Text
				if in.Kind == "string" {
					var err error
					text, err = strconv.Unquote(text)
					if err != nil {
						return nil, err
					}
				}
				return tree.NewValue(in.Kind, in.Span, text), nil
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

func jsonGrammar() p.Grammar {
	namedValue := p.Ref("value")
	return p.Grammar{Start: "value", Rules: map[string]p.Rule{
		"value": p.Choose(p.ChoiceSpec{},
			p.Make("json", "object", p.Seq(skip("{"), p.Sep(p.Ref("pair"), skip(","), false), skip("}"))),
			p.Make("json", "array", p.Seq(skip("["), p.Sep(namedValue, skip(","), false), skip("]"))),
			p.Make("json", "string", tagged("string")),
			p.Make("json", "number", tagged("number")),
			p.Make("json", "bool", p.Choose(p.ChoiceSpec{}, p.Tok(p.Word("true")), p.Tok(p.Word("false")))),
			p.Make("json", "null", p.Tok(p.Word("null"))),
		),
		"pair": p.Make("pair", "pair", p.Seq(
			p.Bind("key", p.Make("key", "string", tagged("string"))),
			skip(":"), p.Bind("value", namedValue),
		)),
	}}
}

func TestJSONOracle(t *testing.T) {
	// {"n": [1,true,null],"e":{}}
	tokens := []p.Token{
		fixtureToken("{", "{", 0, 1), fixtureToken("string", `"n"`, 1, 4), fixtureToken(":", ":", 4, 5),
		fixtureToken("[", "[", 6, 7), fixtureToken("number", "1", 7, 8), fixtureToken(",", ",", 8, 9),
		fixtureToken("word", "true", 9, 13), fixtureToken(",", ",", 13, 14), fixtureToken("word", "null", 14, 18),
		fixtureToken("]", "]", 18, 19), fixtureToken(",", ",", 19, 20), fixtureToken("string", `"e"`, 20, 23),
		fixtureToken(":", ":", 23, 24), fixtureToken("{", "{", 24, 25), fixtureToken("}", "}", 25, 26),
		fixtureToken("}", "}", 26, 27),
	}
	tokens[3].Leading = []p.Trivia{{Kind: "space", Span: span(5, 6)}}
	want := []record{
		{"file", span(0, 27), "", 1},
		{"object", span(0, 27), "", 2},
		{"pair", span(1, 19), "", 2}, {"string", span(1, 4), "n", 0},
		{"array", span(5, 19), "", 3}, {"number", span(7, 8), "1", 0},
		{"bool", span(9, 13), "true", 0}, {"null", span(14, 18), "null", 0},
		{"pair", span(20, 26), "", 2}, {"string", span(20, 23), "e", 0},
		{"object", span(24, 26), "", 0},
	}
	engine := configured(t, jsonGrammar(), builders(), p.Limits{})
	end := 27
	available, final := end, true
	whole := done(t, engine.Start(sourceFor(tokens, &end, &available, &final), ""))
	equalRecords(t, whole, want)
	for cut := 0; cut <= end; cut++ {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			available, final := cut, false
			session := engine.Start(sourceFor(tokens, &end, &available, &final), "")
			paused(t, session)
			available, final = end, true
			chunked := done(t, session)
			equalRecords(t, chunked, want)
			require.NotEqualf(t, whole.Info().ID, chunked.Info().ID, "independent sessions need fresh IDs and repeated Run needs the same root")
			require.Samef(t, chunked, done(t, session), "independent sessions need fresh IDs and repeated Run needs the same root")
		})
	}
}

var valueRequest = p.Request{Goal: "line-value", Layout: p.Lines}

func lineGrammar(hand bool) p.Grammar {
	word := p.Make("word", "word", tagged("word"))
	value := p.Make("value", "value", p.Lex(valueRequest, tagged("value")))
	lineEnd := p.Choose(p.ChoiceSpec{}, skip("newline"), p.Tok(p.EndOfFile()))
	section := p.Seq(skip("["), p.Bind("name", p.Ref("word")), skip("]"), p.Ref("end"))
	entry := p.Seq(p.Bind("key", p.Ref("word")), skip("="), p.Bind("value", p.Ref("value")), p.Ref("end"))
	if hand {
		section = p.Program("open", map[string]p.ProgramState{
			"open":  takeStep(p.Kind("["), "name"),
			"name":  callStep("word", "close", "name"),
			"close": takeStep(p.Kind("]"), "end"),
			"end":   callStep("end", "done", ""),
			"done":  returnStep(),
		})
		entry = p.Program("key", map[string]p.ProgramState{
			"key":    callStep("word", "equals", "key"),
			"equals": takeStep(p.Kind("="), "value"),
			"value":  callStep("value", "end", "value"),
			"end":    callStep("end", "done", ""),
			"done":   returnStep(),
		})
	}
	return p.Grammar{Start: "file", Default: p.Request{Layout: p.Lines}, Rules: map[string]p.Rule{
		"file": p.Many(p.Ref("line")),
		"line": p.Choose(p.ChoiceSpec{},
			p.Make("line", "section", section),
			p.Make("line", "entry", entry),
			skip("newline"),
		),
		"word": word, "value": value, "end": lineEnd,
	}}
}

func takeStep(pattern p.Pattern, next string) p.ProgramState {
	return p.ProgramState{Takes: []string{next}, Step: func(look p.Look, _ p.View) p.Move {
		if p.Matches(look, pattern) {
			return p.Take(next, "")
		}
		return p.Reject(pattern)
	}}
}

func callStep(name, next, bind string) p.ProgramState {
	return p.ProgramState{Calls: []string{name}, ReturnsTo: []string{next}, Control: func(p.View) p.Move {
		return p.Call(name, next, bind)
	}}
}

func returnStep() p.ProgramState {
	return p.ProgramState{Control: func(p.View) p.Move { return p.Return() }}
}

func lineSource(cut *int, final *bool) *fixture {
	// [user]\nname=Ada\ncity=Bern
	tokens := map[int]p.Token{
		0: fixtureToken("[", "[", 0, 1), 1: fixtureToken("word", "user", 1, 5), 5: fixtureToken("]", "]", 5, 6),
		6: fixtureToken("newline", "\n", 6, 7), 7: fixtureToken("word", "name", 7, 11), 11: fixtureToken("=", "=", 11, 12),
		12: fixtureToken("value", "Ada", 12, 15), 15: fixtureToken("newline", "\n", 15, 16),
		16: fixtureToken("word", "city", 16, 20), 20: fixtureToken("=", "=", 20, 21), 21: fixtureToken("value", "Bern", 21, 25),
	}
	return &fixture{read: func(mark p.Mark, req p.Request) (p.Read, error) {
		if req.Layout != p.Lines {
			return p.Read{}, fmt.Errorf("line source received layout %d", req.Layout)
		}
		if tok, ok := tokens[mark.Byte]; ok {
			if (tok.Kind == "value") != (req == valueRequest) {
				return p.Read{}, fmt.Errorf("unexpected goal %q at byte %d", req.Goal, mark.Byte)
			}
			if tok.Span.End > *cut || mark.Byte == 12 && *cut == 15 || mark.Byte == 21 && !*final {
				return shortage(), nil
			}
			return ready(tok, p.Mark{Byte: tok.Span.End}), nil
		}
		if *final {
			return eof(25), nil
		}
		return shortage(), nil
	}}
}

func TestLineOracleInBothStyles(t *testing.T) {
	want := []record{
		{"file", span(0, 25), "", 3},
		{"section", span(0, 7), "", 1}, {"word", span(1, 5), "user", 0},
		{"entry", span(7, 16), "", 2}, {"word", span(7, 11), "name", 0}, {"value", span(12, 15), "Ada", 0},
		{"entry", span(16, 25), "", 2}, {"word", span(16, 20), "city", 0}, {"value", span(21, 25), "Bern", 0},
	}
	for _, hand := range []bool{false, true} {
		t.Run(fmt.Sprintf("hand=%t", hand), func(t *testing.T) {
			engine := configured(t, lineGrammar(hand), builders(), p.Limits{})
			cut, final := 25, true
			equalRecords(t, done(t, engine.Start(lineSource(&cut, &final), "")), want)
			cut, final = 0, false
			session := engine.Start(lineSource(&cut, &final), "")
			for _, cut = range []int{0, 5, 7, 15, 16, 21, 25} {
				paused(t, session)
			}
			final = true
			equalRecords(t, done(t, session), want)
		})
	}
}
