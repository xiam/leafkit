package tests

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

type wantDiagnostic struct {
	slot      string
	start     int
	end       int
	found     int
	foundText string
	skipped   string
	expected  []p.Pattern
}

type recoveryCase struct {
	name, input, shape string
	diagnostics        []wantDiagnostic
}

func tokenBoundaries(consume bool, kinds ...p.TokenKind) p.Boundary {
	boundary := p.Boundary{Consume: consume}
	for _, kind := range kinds {
		boundary.At = append(boundary.At, p.Kind(kind))
	}
	return boundary
}

func recoveryJSON() p.Grammar {
	grammar := jsonGrammar()
	// Give the pair's value its own site. The object still owns its comma
	// and closing brace, so the pair and key survive a missing value.
	grammar.Rules["pair"] = p.Make("pair", "pair", p.Seq(
		p.Bind("key", p.Make("key", "string", tagged("string"))),
		skip(":"), p.Bind("value", p.Recover("value", p.Ref("value"), tokenBoundaries(false, ",", "}"))),
	))
	return grammar
}

func recoveryJSONTokens(input string) []p.Token {
	var tokens []p.Token
	for i := 0; i < len(input); {
		if input[i] == ' ' {
			i++
			continue
		}
		start := i
		var kind p.TokenKind
		if input[i] == '"' {
			if close := strings.IndexByte(input[i+1:], '"'); close >= 0 {
				i += close + 2 // The oracle uses simple quoted keys.
				kind = "string"
			} else {
				i, kind = len(input), p.Invalid
			}
		} else {
			i++
			kind = p.TokenKind(input[start:i])
			if input[start] >= '0' && input[start] <= '9' {
				kind = "number"
			}
		}
		tokens = append(tokens, fixtureToken(kind, input[start:i], start, i))
	}
	return tokens
}

func TestJSONRecoveryOracle(t *testing.T) {
	value := []p.Pattern{p.Kind("{"), p.Kind("["), p.Kind("string"), p.Kind("number"), p.Word("true"), p.Word("false"), p.Word("null")}
	checkRecoveryCases(t, recoveryJSON(), recoveryJSONTokens, []recoveryCase{
		{
			name: "separate faults and a surviving pair", input: `{"a":,"b":1,"c":?}`,
			shape: "(file (object (pair a (leafkit:error)) (pair b 1) (pair c (leafkit:error))))",
			diagnostics: []wantDiagnostic{
				{"value", 5, 5, 5, ",", "", value},
				{"value", 16, 17, 16, "?", "?", value},
			},
		},
		{
			name: "invalid token reaches recovery", input: `{"a":"`,
			shape: "(file (leafkit:error))",
			diagnostics: []wantDiagnostic{
				{"file", 0, 6, 6, "", `{"a":"`, []p.Pattern{p.Kind("}")}},
			},
		},
		{
			name: "discarded prefix at EOF", input: "{",
			shape: "(file (leafkit:error))",
			diagnostics: []wantDiagnostic{
				{"file", 0, 1, 1, "", "{", []p.Pattern{p.Kind("}")}},
			},
		},
		{
			name: "missing document", input: "",
			shape: "(file (leafkit:error))",
			diagnostics: []wantDiagnostic{
				{"file", 0, 0, 0, "", "", value},
			},
		},
		{
			name: "file fallback after a valid root", input: "1??",
			shape: "(file 1 (leafkit:error))",
			diagnostics: []wantDiagnostic{
				{"file", 1, 3, 1, "?", "??", []p.Pattern{p.EndOfFile()}},
			},
		},
	})
}

func recoveryLines() p.Grammar {
	grammar := lineGrammar(false)
	grammar.Rules["file"] = p.Many(p.Seq(p.Not(p.EndOfFile()),
		p.Recover("line", p.Ref("line"), tokenBoundaries(true, "newline"))))
	return grammar
}

func recoveryLineTokens(input string) []p.Token {
	var tokens []p.Token
	for i, char := range []byte(input) {
		kind := p.TokenKind(input[i : i+1])
		switch {
		case char == '\n':
			kind = "newline"
		case char >= 'a' && char <= 'z':
			kind = "word"
		case char >= '0' && char <= '9':
			kind = "value"
		}
		tokens = append(tokens, fixtureToken(kind, input[i:i+1], i, i+1))
	}
	return tokens
}

func TestLineRecoveryOracle(t *testing.T) {
	checkRecoveryCases(t, recoveryLines(), recoveryLineTokens, []recoveryCase{
		{
			name: "scan through newline and keep the middle entry", input: "a?!!\nb=2\n[c?\n",
			shape: "(file (leafkit:error) (entry b 2) (leafkit:error))",
			diagnostics: []wantDiagnostic{
				{"line", 0, 5, 1, "?", "a?!!\n", []p.Pattern{p.Kind("=")}},
				{"line", 9, 13, 11, "?", "[c?\n", []p.Pattern{p.Kind("]")}},
			},
		},
		{
			name: "missing final value", input: "a=",
			shape: "(file (leafkit:error))",
			diagnostics: []wantDiagnostic{
				{"line", 0, 2, 2, "", "a=", []p.Pattern{p.Kind("value")}},
			},
		},
		{name: "empty file", shape: "(file)"},
	})
}

func recoveryStatements() p.Grammar {
	grammar, _ := expressionGrammar()
	grammar.Start = "file"
	grammar.Rules["file"] = p.Many(p.Seq(p.Not(p.EndOfFile()),
		p.Recover("stmt", p.Ref("stmt"), tokenBoundaries(true, ";"))))
	grammar.Rules["stmt"] = p.Choose(p.ChoiceSpec{},
		p.Make("stmt", "let", p.Seq(p.Drop(p.Tok(p.Word("let"))), p.Bind("name", tagged("word")),
			skip("="), p.Bind("value", p.Ref("expr")), skip(";"))),
		p.Make("stmt", "expr-stmt", p.Seq(p.Bind("value", p.Ref("expr")), skip(";"))),
	)
	grammar.Rules["atom"] = p.Choose(p.ChoiceSpec{},
		p.Make("expr", "number", tagged("number")),
		p.Make("expr", "word", tagged("word")),
		p.Make("expr", "group", p.Seq(skip("("),
			p.Recover("expr", p.Ref("expr"), tokenBoundaries(false, ")")), skip(")"))),
	)
	return grammar
}

func TestStatementRecoveryOracle(t *testing.T) {
	atom := []p.Pattern{p.Kind("number"), p.Kind("word"), p.Kind("(")}
	checkRecoveryCases(t, recoveryStatements(), expressionTokens, []recoveryCase{
		{
			name: "bad statements around a valid expression", input: "let a=;let b=2+3;let c=;",
			shape: "(file (leafkit:error) (let (add 2 3)) (leafkit:error))",
			diagnostics: []wantDiagnostic{
				{"stmt", 0, 7, 6, ";", "leta=;", atom},
				{"stmt", 17, 24, 23, ";", "letc=;", atom},
			},
		},
		{
			name: "missing infix operand inside a retained group", input: "let x=(1+);2;",
			shape: "(file (let (group (leafkit:error))) (expr-stmt 2))",
			diagnostics: []wantDiagnostic{
				{"expr", 7, 9, 9, ")", "1+", atom},
			},
		},
		{
			name: "point repair leaves the closer", input: "()+1;",
			shape: "(file (expr-stmt (add (group (leafkit:error)) 1)))",
			diagnostics: []wantDiagnostic{
				{"expr", 1, 1, 1, ")", "", atom},
			},
		},
	})
}

func checkRecoveryCases(t *testing.T, grammar p.Grammar, tokenize func(string) []p.Token, cases []recoveryCase) {
	t.Helper()
	engine := configured(t, grammar, builders(), p.Limits{})
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			tokens := tokenize(row.input)
			end := len(row.input)
			available, final := end, true
			whole := done(t, engine.Start(sourceFor(tokens, &end, &available, &final), ""))
			{
				got := expressionShape(whole)
				require.Equalf(t, row.shape, got, "%q => %s, want %s", row.input, got, row.shape)
			}
			withProblems := recoveryProblems(t, whole)
			require.Lenf(t, withProblems, len(row.diagnostics), "diagnostics = %v, want %v", withProblems, row.diagnostics)
			for i, node := range withProblems {
				checkProblem(t, node, row.diagnostics[i])
			}

			available, final = 0, false
			session := engine.Start(sourceFor(tokens, &end, &available, &final), "")
			for ; available <= end; available++ {
				paused(t, session)
			}
			final = true
			chunked := done(t, session)
			equalRecords(t, chunked, records(whole))
			got := recoveryProblems(t, chunked)
			require.Lenf(t, got, len(withProblems), "chunked diagnostics = %v, want %v", got, withProblems)
			for i, node := range got {
				require.Equalf(t, withProblems[i].Slot(), node.Slot(), "chunked diagnostic %d", i)
				require.Equalf(t, withProblems[i].Problem(), node.Problem(), "chunked diagnostic %d", i)
			}

		})
	}
}

func recoveryProblems(t testing.TB, root tree.Node) []*p.ErrorNode {
	t.Helper()
	var nodes []*p.ErrorNode
	for node := range tree.Walk(root) {
		if node.Info().Kind == tree.KindError {
			diagnostic, ok := node.(*p.ErrorNode)
			require.Truef(t, ok, "default error node has type %T", node)
			nodes = append(nodes, diagnostic)
		}
	}
	return nodes
}

func checkProblem(t *testing.T, node *p.ErrorNode, want wantDiagnostic) {
	t.Helper()
	problem := node.Problem()
	var text strings.Builder
	for _, token := range problem.Skipped {
		text.WriteString(token.Text)
	}
	require.Equalf(t, want.slot, node.Slot(), "slot %q problem %+v (skipped %q), want %+v", node.Slot(), problem, text.String(), want)
	require.Equalf(t, span(want.start, want.end), problem.Span, "slot %q problem %+v (skipped %q), want %+v", node.Slot(), problem, text.String(), want)
	require.Equalf(t, span(want.found, want.found), problem.Found.Point, "slot %q problem %+v (skipped %q), want %+v", node.Slot(), problem, text.String(), want)
	require.Equalf(t, want.foundText, problem.Found.Token.Text, "slot %q problem %+v (skipped %q), want %+v", node.Slot(), problem, text.String(), want)
	require.Equalf(t, (want.foundText == ""), problem.Found.End, "slot %q problem %+v (skipped %q), want %+v", node.Slot(), problem, text.String(), want)
	require.Equalf(t, want.skipped, text.String(), "slot %q problem %+v (skipped %q), want %+v", node.Slot(), problem, text.String(), want)
	require.Truef(t, slices.Equal(problem.Expected, want.expected), "slot %q problem %+v (skipped %q), want %+v", node.Slot(), problem, text.String(), want)
}
