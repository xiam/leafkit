package core_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	lex "github.com/xiam/bytescan/rules/core"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/internal/reference"
	sql "github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tree"
)

// This artificial dialect verifies hooks without implementing any SQL dialect.
func composed(t testing.TB, text string, streamed bool) tree.Node {
	t.Helper()
	grammar := sql.Grammar()
	originalModifiers := grammar.Rules["modifiers"]
	originalJoin := grammar.Rules["join"]
	originalExpression := grammar.Rules["expression"]
	grammar.Rules["modifiers"] = p.Choose(p.ChoiceSpec{}, p.Make("demo", "demo:mark", sql.Skip("demo:mark")), originalModifiers)
	grammar.Rules["expression"] = p.Choose(p.ChoiceSpec{}, p.Make("demo", "demo:tag", p.Seq(sql.Skip("demo:@"), p.Ref("operand"))), originalExpression)
	grammar.Rules["join"] = p.Choose(p.ChoiceSpec{}, p.Make("demo", "demo:via", p.Seq(sql.Skip("demo:via"), sql.Skip(sql.Keyword("JOIN")), p.Ref("table"), sql.Skip(sql.Keyword("ON")), p.Ref("comparison"))), originalJoin)
	extension := func(build p.Build[tree.Node]) (tree.Node, error) {
		if !strings.HasPrefix(string(build.Kind), "demo:") {
			return nil, fmt.Errorf("unknown extension %q", build.Kind)
		}
		var children []tree.Node
		for _, field := range build.Fields {
			children = append(children, field.Nodes...)
		}
		return tree.NewContainer(build.Kind, build.Span, children...), nil
	}
	engine := reference.Engine(t, grammar, sql.Builders(snapshot(text), extension))
	return reference.Parse(t, engine, func(in *reference.Input) (p.Source, error) {
		cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
		lexer := sql.Scanner(cur,
			sql.LexRule{Kind: "demo:mark", Rule: lex.KeywordsIgnoreCase("MARK")},
			sql.LexRule{Kind: "demo:via", Rule: lex.KeywordsIgnoreCase("VIA")},
			sql.LexRule{Kind: "demo:@", Rule: lex.Literal("@")})
		return adapter.New(in.ID(), lexer, cur, p.Request{}, sql.Trivia())
	}, text, streamed, "")
}

func TestComposition(t *testing.T) {
	// Every valid common query must keep its exact typed tree through extension.
	for _, tc := range fixtures(t) {
		if tc.Diagnostic != nil {
			continue
		}
		for _, streamed := range []bool{false, true} {
			root := composed(t, tc.SQL, streamed)
			require.Equal(t, tc.Tree, sql.Canonical(root), "extension changed %s: %s", tc.Name, sql.Canonical(root))
		}
	}
	text := "SELECT MARK @id FROM users VIA JOIN teams ON users.id = teams.id WHERE @id = 2"
	extensionPrint := func(n tree.Node, recur func(tree.Node) (string, error)) (string, error) {
		child := func(i int) (string, error) {
			node, ok := n.Child(i)
			if !ok {
				return "", fmt.Errorf("missing child")
			}
			return recur(node)
		}
		switch n.Info().Kind {
		case "demo:mark":
			return "MARK", nil
		case "demo:tag":
			text, err := child(0)
			return "@" + text, err
		case "demo:via":
			table, err := child(0)
			if err != nil {
				return "", err
			}
			cond, err := child(1)
			return "VIA JOIN " + table + " ON " + cond, err
		}
		return "", fmt.Errorf("unknown %q", n.Info().Kind)
	}
	for _, streamed := range []bool{false, true} {
		root := composed(t, text, streamed)
		require.Len(t, sql.Diagnostics(root), 0, "extension failed: %s", sql.Canonical(root))
		require.Len(t, sql.Tables(root), 2, "shared visitor missed extension table")
		_, err := sql.Print(root)
		require.Error(t, err, "portable printer accepted extension")
		printed, err := sql.PrintWith(root, extensionPrint)
		require.NoError(t, err, "extension print=%q,%v", printed, err)
		require.Equal(t, text, printed, "extension print=%q,%v", printed, err)
		require.Equal(t, sql.Canonical(root), sql.Canonical(composed(t, printed, false)), "extension roundtrip differs")
	}
	// Rule-map changes are local to one grammar, never process-global.
	root := parse(t, text, false)
	require.NotEmpty(t, sql.Diagnostics(root), "core acquired dialect rules")
}

func TestNamespacedLexicalValue(t *testing.T) {
	text := `SELECT {a}}b} FROM users`
	grammar := sql.Grammar()
	grammar.Rules["name"] = p.Make("name", sql.NameKind, p.Choose(p.ChoiceSpec{}, sql.Token("demo:quoted"), sql.Token(sql.NameToken), sql.Token(sql.QuotedNameToken)))
	decoder := sql.TokenDecoder{Kind: "demo:quoted", Quoted: true, Decode: func(raw string) string { return strings.ReplaceAll(raw[1:len(raw)-1], "}}", "}") }}
	builders := sql.Builders(snapshot(text), nil, decoder)
	decoder.Decode = nil // Builders retained a value copy, not caller-owned configuration.
	engine := reference.Engine(t, grammar, builders)
	source := func(in *reference.Input) (p.Source, error) {
		cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
		rule := lex.Sequence(lex.Literal("{"), lex.OneOrMore(lex.Choice(lex.Literal("}}"), lex.Rune(lex.RuneSet("}\r\n").Not()))), lex.Literal("}"))
		return adapter.New(in.ID(), sql.Scanner(cur, sql.LexRule{Kind: "demo:quoted", Rule: rule}), cur, p.Request{}, sql.Trivia())
	}
	for _, decoders := range [][]sql.TokenDecoder{nil, {decoder}} {
		rejected := reference.Engine(t, grammar, sql.Builders(snapshot(text), nil, decoders...))
		inCur := cursor.NewReader(strings.NewReader(text))
		inLexer := sql.Scanner(inCur, sql.LexRule{Kind: "demo:quoted", Rule: lex.Literal("{a}}b}")})
		inSource, err := adapter.New(snapshot(text).ID(), inLexer, inCur, p.Request{}, sql.Trivia())
		require.NoError(t, err)
		_, _, err = rejected.Start(inSource, "").Run()
		require.ErrorIs(t, err, p.ErrBuild, "missing/nil decoder=%v", err)
	}

	for _, streamed := range []bool{false, true} {
		root := reference.Parse(t, engine, source, text, streamed, "")
		require.Len(t, sql.Diagnostics(root), 0, "namespaced quote rejected")
		printed, err := sql.Print(root)
		require.NoError(t, err, "portable quote=%q,%v", printed, err)
		require.Equal(t, `SELECT "a}b" FROM users`, printed, "portable quote=%q,%v", printed, err)
		require.Equal(t, sql.Canonical(parse(t, printed, false)), sql.Canonical(root), "decoder changed normalized name")
		for n := range tree.Walk(root) {
			leaf, ok := n.(*sql.Lexeme)
			require.False(t, ok && leaf.Value() == "a}b" && leaf.Raw() != `{a}}b}`, "raw dialect spelling lost")
		}
	}
}

func TestDecodedNumberPortablePrint(t *testing.T) {
	text := "SELECT 0x10 FROM users"
	snap := snapshot(text)
	grammar := sql.Grammar()
	grammar.Rules["operand"] = p.Choose(p.ChoiceSpec{},
		p.Make("hex", sql.NumberKind, sql.Token("demo:hex")), grammar.Rules["operand"])
	decoder := sql.TokenDecoder{Kind: "demo:hex", Decode: func(string) string { return "16" }}
	engine := reference.Engine(t, grammar, sql.Builders(snap, nil, decoder))
	source := func(in *reference.Input) (p.Source, error) {
		cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
		return adapter.New(in.ID(), sql.Scanner(cur, sql.LexRule{Kind: "demo:hex", Rule: lex.Literal("0x10")}), cur, p.Request{}, sql.Trivia())
	}
	for _, streamed := range []bool{false, true} {
		root := reference.Parse(t, engine, source, text, streamed, "")
		require.Len(t, sql.Diagnostics(root), 0, "numeric extension rejected")
		var number *sql.Lexeme
		for node := range tree.Walk(root) {
			if leaf, ok := node.(*sql.Lexeme); ok && leaf.Info().Kind == sql.NumberKind {
				number = leaf
			}
		}
		require.NotNil(t, number, "numeric spelling/value/range=%+v", number)
		require.Equal(t, "0x10", number.Raw(), "numeric spelling/value/range=%+v", number)
		require.Equal(t, "16", number.Value(), "numeric spelling/value/range=%+v", number)
		require.Equal(t, (tree.Span{Source: snap.ID(), Start: 7, End: 11}), number.Range(), "numeric spelling/value/range=%+v", number)
		raw, err := snap.Raw(number.Range())
		require.NoError(t, err, "snapshot raw=%q,%v", raw, err)
		require.Equal(t, "0x10", raw, "snapshot raw=%q,%v", raw, err)
		printed, err := sql.Print(root)
		assert.False(t, err != nil || printed != "SELECT 16 FROM users", "portable number=%q,%v", printed, err)
		portable := parse(t, printed, false)
		require.Len(t, sql.Diagnostics(portable), 0, "portable roundtrip differs: %s -> %s", sql.Canonical(root), sql.Canonical(portable))
		require.Equal(t, sql.Canonical(root), sql.Canonical(portable), "portable roundtrip differs: %s -> %s", sql.Canonical(root), sql.Canonical(portable))
	}
}
