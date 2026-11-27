# Write a grammar

Start with the [statement example](../tests/statement/README.md). It
parses bindings, conditionals, blocks and calls. Its runnable Go example
evaluates a program to `6` under `go test ./tests/statement`.

## Give the parser tokens

Create a scanner on the supplied cursor. The scanner recognizes names,
numbers and punctuation. The adapter attaches spaces and comments as
trivia so grammar rules can match the significant tokens directly.

<!-- example: tests/statement/statement_test.go#tokens -->
```go
var trivia = adapter.Options{Trivia: []scan.TokenType{"space", "comment"}}

func scanner(cur *cursor.Reader) *scan.Scanner {
	lexer := scan.NewWithCursor(cur)
	lexer.MustAddRule("space", base.Whitespace())
	lexer.MustAddRule("comment", base.LineComment("//"))
	lexer.MustAddRule("name", core.RuneRun(core.UnicodeLetter))
	lexer.MustAddRule("number", core.RuneRun(core.ASCIIDigit))
	for _, symbol := range []string{"+", "*", "=", ";", ",", "(", ")", "{", "}"} {
		lexer.MustAddRule(scan.TokenType(symbol), core.Literal(symbol))
	}
	return lexer
}
```

## Define the grammar

Set an entry rule and name the rules that call each other. `Make` names
the node kind and its construction slot. `Expr` sets operator precedence
with binding powers. `Recover` records a diagnostic and leaves the
semicolon or closing brace available to its owner. A word match checks
the token text. A kind match checks the scanner's token kind.

<!-- example: tests/statement/statement_test.go#grammar -->
```go
func grammar() p.Grammar {
	token, skip := reference.Token, reference.Skip
	name := p.Make("name", "name", token("name"))
	number := p.Make("number", "number", token("number"))
	expr, stmt, block := p.Ref("expr"), p.Ref("statement"), p.Ref("block")
	atom := p.Choose(p.ChoiceSpec{}, number, name, p.Seq(skip("("), expr, skip(")")))
	item := p.Seq(p.Not(p.EndOfFile()), p.Not(p.Kind("}")),
		p.Recover("statement", stmt, p.Boundary{At: []p.Pattern{p.Kind(";"), p.Kind("}")}}), p.Opt(skip(";")))
	return p.Grammar{Start: "file", Rules: map[string]p.Rule{
		"file":  p.Many(item),
		"block": p.Make("block", "block", p.Seq(skip("{"), p.Many(item), skip("}"))),
		"statement": p.Choose(p.ChoiceSpec{},
			p.Make("statement", "let", p.Seq(word("let"), name, skip("="), expr, skip(";"))),
			p.Make("statement", "if", p.Seq(word("if"), expr, block, p.Opt(p.Seq(word("else"), block)))),
			p.Make("statement", "expr-stmt", p.Seq(expr, skip(";")))),
		"expr": p.Expr("expr", atom,
			p.Operator{Form: p.Infix, Match: p.Kind("+"), Power: 10, Kind: "add"},
			p.Operator{Form: p.Infix, Match: p.Kind("*"), Power: 20, Kind: "multiply"},
			p.Operator{Form: p.Postfix, Match: p.Kind("("), Power: 30, Kind: "call",
				Tail: p.Seq(p.Sep(expr, skip(","), false), skip(")"))}),
	}}
}
```

## Configure and parse

Pass your grammar and builders to `parser.Configure`. This example's
[builders](../tests/internal/reference/reference.go) construct text leaves
and ordered containers. Give the input snapshot a source ID and pass the
same cursor to the scanner and the adapter. `Start` creates a fresh parse
session for the input.

This function reads sealed text. For streaming, call `Run` again after
exposing more bytes when it returns `Paused`. A `Done` tree can contain
`*parser.ErrorNode` diagnostics. The reference tests compare their spans,
found events, expected patterns and skipped text.

Use a replay source and [a computed lexical request](specs/parser.md#computed-lexical-requests)
when a matched opener determines how the lexer reads a body.
The [queued-body example](../tests/computed_queue_test.go) binds two
openers, computes each delimiter and restores the enclosing request
after the body.

<!-- example: tests/statement/statement_test.go#parse -->
```go
func parse(id tree.SourceID, text string) (tree.Node, error) {
	engine, err := p.Configure(grammar(), reference.Builders("name", "number"), p.Limits{})
	if err != nil {
		return nil, err
	}
	cur := cursor.NewReader(strings.NewReader(text))
	input, err := adapter.New(id, scanner(cur), cur, p.Request{}, trivia)
	if err != nil {
		return nil, err
	}
	root, status, err := engine.Start(input, "").Run()
	if err == nil && status != p.Done {
		return nil, fmt.Errorf("sealed input paused")
	}
	return root, err
}
```

## Evaluate the tree

Inspect diagnostics before evaluating a program. `eval.Fold` calls a
rule for each node kind it reaches. Each rule can choose which children
to visit and which scope to pass. Here a conditional evaluates only its
chosen block. A block creates a child scope for its bindings.

<!-- example: tests/statement/statement_test.go#evaluate -->
```go
func evaluate(root tree.Node) (int, error) {
	rules := map[tree.Kind]eval.Rule[int, int]{
		"file":  sequence,
		"block": blockScope,
		"let":   bind,
		"if":    conditional,
		"expr-stmt": func(_ eval.Frame, env *eval.Env[int], child eval.Descend[int, int]) (int, error) {
			return child(0, env)
		},
		"name": lookup,
		"number": func(f eval.Frame, _ *eval.Env[int], _ eval.Descend[int, int]) (int, error) {
			return strconv.Atoi(text(f.Node))
		},
		"add":      binary(func(a, b int) int { return a + b }),
		"multiply": binary(func(a, b int) int { return a * b }),
		"call":     call,
	}
	return eval.Fold(root, eval.NewEnv[int](nil), rules)
}
```

## More reference grammars

- [Three SQL dialects](sql-example.md) compose a shared SELECT grammar and
  typed trees, then translate a bounded row limit.
- [Statement language](../tests/statement/README.md) evaluates expressions,
  calls and scoped statements.
- [JSON](../tests/json/README.md) parses nested values and prints compact JSON.
- [Line format](../tests/line/README.md) reads sections and entries into a map.
- [Layout language](../tests/layout/README.md) tracks indentation and joins
  lines inside an embedded expression.
- [S-expressions](../tests/sexpr/README.md) parse and print lists and atoms.
- [Ambiguity](../tests/ambiguity/README.md) resolves a statement using the same
  symbol information during parsing or during evaluation.

Each reference test checks complete trees with both sealed and streamed
input. See the [parser contract](specs/parser.md) and
[bytescan adapter](specs/adapter.md) for the API details.
