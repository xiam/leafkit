# Bounded SQL translation

Translate a shared SQL tree to PostgreSQL without changing its source.
[`ExampleToPostgres`](translate_test.go) replaces SQL Server `TOP (2)` with
`LIMIT 2`. `ToPostgres` shares portable subtrees; `PrintPostgres` renders
identifier components with target quotes. Native `*` projections use the
shared projection wrapper; unsupported custom projection shapes get a
source-range diagnostic (`TestComposedProjectionShape`).

```go
source, err := tsql.Parse(core.NewSnapshot("query",
    "SELECT TOP (2) [id] FROM [items] ORDER BY [id]"))
if err != nil {
    return err
}
target, diagnostics := translate.ToPostgres(source,
    &translate.UniqueIntegerKey{Table: []string{"items"}, Column: "id"})
if len(diagnostics) != 0 {
    return fmt.Errorf("%s", diagnostics[0].Message)
}
text, err := translate.PrintPostgres(target)
// text: SELECT "id" FROM "items" ORDER BY "id" LIMIT 2
```

The key argument asserts a unique, non-null integer key in the source
relation. The parser cannot verify database constraints. `WITH TIES` gets
a source-range diagnostic; the pass never drops it. See the
[contract](contract.md) for the supported subset and caller obligations.

Run parser, translation and source-preservation checks from the module root:

```sh
go test ./tests/sql/translate ./tests/sql/postgres
```

Parser round trips do not verify database behavior. The separate
[engine procedure](engines.md) runs both SQL Server and PostgreSQL on the
same fixed integer fixture and compares ordered rows with expected results.
It also demonstrates the extra row that a lossy boundary-tie rewrite drops.
