# S-expressions

Run `go test ./tests/sexpr`. The [grammar and tests](sexpr_test.go) read
atoms and parenthesized lists. A list may contain other lists and may
be empty.

`(f (g 2) ())  λ` prints as `(f (g 2) ()) λ` using `eval.Fold`.
`TestSExpressionGrammar` checks the exact tree and output. It also checks
an unmatched closing parenthesis between two atoms and an unclosed list
at EOF.
