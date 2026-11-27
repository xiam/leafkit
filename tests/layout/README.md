# Layout language

Run `go test ./tests/layout`. The [grammar and tests](layout_test.go) read
assignments and `match name:` headers. Statements end at a line break.
Each suite indents by two spaces. A blank line leaves the indentation
depth unchanged.

```text
match topic:
  match = "sum ${1 +
    2}"
next = 3
```

The source checkpoint holds the indentation depth. The adapter exposes
indent and dedent tokens to the grammar, including dedents at final EOF.
The word `match` starts a header before a name and a colon. It remains
a name in the assignment shown above.

Inside quotes the grammar requests text tokens. Inside `${...}` it
requests expression tokens with joined lines, then returns to text tokens.
The printer uses `eval.Fold` to produce canonical indentation and prints
the embedded expression as `1 + 2` on one line.

`TestLayoutGrammar` covers nested suites, final dedents, partial input,
malformed indentation and a missing assignment value. The broken cases
retain the next statement.
