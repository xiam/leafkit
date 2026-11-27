# Resolve an ambiguous statement

Run `go test ./tests/ambiguity`. The [grammar and tests](ambiguity_test.go)
read `T * x;` as a declaration or a multiplication. With `All`, the tree
has one ambiguity node with both readings.

The example uses the same symbol information in two places.

- A pure parser filter reads the immutable list of known type names.
  It keeps the declaration when `T` is a type.
- An `eval.Fold` rule reads a symbol table from its environment and
  visits the selected alternative. The caller can reuse the same
  ambiguous tree with a different table.

`TestAmbiguityExample` checks both outcomes and confirms that the later
resolution leaves both original readings available.
`TestBrokenAmbiguityInput` checks file diagnostics for a missing operator
or operand.
