# SQL Server SELECT example

Compose SQL Server row limits and bracket names over the
[shared SELECT example](../core/README.md). Parse a query, check diagnostics,
then print it or visit its tables:

```go
root, err := tsql.Parse(core.NewSnapshot("query", "SELECT TOP (2) WITH TIES [id] FROM [users] ORDER BY [score] DESC"))
if err != nil {
    return err
}
if len(core.Diagnostics(root)) != 0 {
    return fmt.Errorf("invalid SQL")
}
text, err := tsql.Print(root)
// SELECT TOP (2) WITH TIES "id" FROM "users" ORDER BY "score" DESC
```

[`ExampleParse`](tsql_test.go) runs this example and the shared table visitor.
The printer uses ANSI quotes for bracket names, assuming SQL Server's
`QUOTED_IDENTIFIER ON` mode. It retains the count and WITH TIES modifier.
Run the caller example from the module root with `go test ./tests/sql/tsql`.

Read the [local contract](contract.md) for the bounded syntax, reference version,
and limits. The [dialect fixtures](testdata/dialect.json) record expected trees
and output. This example is a parser composition exercise; it does not claim
SQL Server conformance or engine execution verification.
