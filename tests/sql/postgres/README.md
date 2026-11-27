# PostgreSQL SELECT composition

Add PostgreSQL syntax to the shared SQL caller example with public LeafKit
and Bytescan APIs. [`ExampleParse`](postgres_test.go) parses a `DISTINCT ON`
query and a dollar-quoted value, prints the query, and visits shared tables.
`TestLimit` also demonstrates a bounded `LIMIT n` suffix used by the
[translation example](../translate/README.md).

```go
snapshot := core.NewSnapshot("query-1",
    "SELECT DISTINCT ON (id) id, $$ready$$ FROM users ORDER BY id, stamp DESC")
root, err := postgres.Parse(snapshot)
if err != nil {
    return err
}
if len(core.Diagnostics(root)) != 0 {
    return fmt.Errorf("invalid SQL")
}
text, err := postgres.Print(root)
// text: SELECT DISTINCT ON (id) id, 'ready' FROM users ORDER BY id, stamp DESC
```

Run the fixtures from the module root:

```sh
go test ./tests/sql/postgres
```

Read the [bounded dialect contract](contract.md) for syntax, lexical modes,
source storage and limits. The example pins PostgreSQL 18 documentation;
it does not claim full PostgreSQL conformance.
