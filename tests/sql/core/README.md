# Shared SQL SELECT example

Parse a small SELECT query, print it, and find its table references with
public LeafKit and Bytescan APIs. This is a caller example, not a SQL
conformance layer.

```go
snapshot := core.NewSnapshot("query-1", "SELECT id FROM users")
root, err := core.Parse(snapshot)
if err != nil {
    return err
}
if len(core.Diagnostics(root)) != 0 {
    return fmt.Errorf("invalid SQL")
}
text, err := core.Print(root)
// text: SELECT id FROM users
```

[`ExampleParse`](core_test.go) is the runnable version with joins, printing
and the shared table visitor. The portable printer renders shared values;
`Lexeme.Raw` preserves original spelling. Run the examples from the module root:

```sh
go test ./tests/sql/core
```

Read the [subset and composition contract](contract.md) to extend the
lexer and grammar. The [common corpus](testdata/common.json) pairs queries
with expected trees, diagnostics and portable output.
