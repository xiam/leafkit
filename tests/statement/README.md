# Statement language

Run `go test ./tests/statement`. The [grammar and tests](statement_test.go)
read bytescan tokens and evaluate the resulting tree with `eval.Fold`.

```text
let x = 2;
if x { double(x + 1); } else { 0; }
```

This program evaluates to `6`. Blocks have their own scope. An `if` evaluates
its condition and only the chosen block. Expressions support names, integer
literals, parentheses, addition, multiplication and calls to `double` or
`sum`.

`TestStatementGrammar` checks operator precedence, lazy branches and binding
scope. A missing initializer produces a statement diagnostic and leaves the
next statement or the closing brace available to the surrounding rule.
