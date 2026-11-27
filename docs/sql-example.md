# Compose three SQL dialects

Run one SELECT through PostgreSQL, MySQL and SQL Server grammars, then
translate a bounded SQL Server row limit to PostgreSQL. Start at the module
root with Go 1.23 or later:

```sh
go test -v ./tests/sql -run '^Example$'
```

The runnable [Example](../tests/sql/guide_test.go) parses these same bytes
with `postgres.Parse`, `mysql.Parse` and `tsql.Parse`:

```sql
SELECT u.id FROM users AS u INNER JOIN teams AS t ON u.team_id = t.id ORDER BY u.id
```

Each parser returns shared SELECT, projection, table, join and ordering
nodes. Check the Go error, then `core.Diagnostics(root)`, before using a
tree. `core.Print(root)` prints the query above. The same `core.Tables(root)`
visitor reports `[users teams]` for all three. See `Example` and
`TestPortableMatrix` for executable output and independent tree oracles.

These packages are caller examples under `tests/sql`, built from public
LeafKit, Bytescan and adapter APIs. The library supplies parser and tree
mechanisms; the caller owns the SQL types and grammar. For exact coverage,
versions and test evidence, read the [SQL example contract](specs/sql.md).

## Extend tokens and trees separately

Start each dialect with a fresh `core.Grammar()` map. Replace named rules
before `parser.Configure`; `parser.Ref` resolves nested uses. Pair the
configured engine with that dialect's scanner and snapshot-bound builders.
`core.TestComposition` in [composition_test.go](../tests/sql/core/composition_test.go)
shows the construction hooks without changing the library.

A token extension can keep a shared AST type. PostgreSQL dollar strings,
MySQL backtick names and SQL Server bracket names retain their raw snapshot
ranges but decode into shared `core.Lexeme` values. A syntax extension that
carries meaning needs a distinct tree node: `postgres.DistinctOn`,
`mysql.StraightJoin` or `tsql.Top`. Their children retain shared types, so
`core.Tables` traverses them without a dialect switch. The
[capability table](specs/sql.md#capability-to-test-evidence) links the tests
for decoding, typed nodes, source spans and visitors.

The [native matrix](../tests/sql/guide_test.go) accepts each extension in its
own dialect and rejects its unambiguous syntax in the other dialects and
core. A parser's acceptance alone does not establish engine semantics.

## Translate a row limit safely

The same runnable example parses:

```sql
SELECT TOP (2) [id] FROM [items] ORDER BY [id]
```

It calls `translate.ToPostgres(source, key)`, where `key` is:

```go
&translate.UniqueIntegerKey{Table: []string{"items"}, Column: "id"}
```

This is a caller assertion that `items.id` is a unique, non-null integer
key. The translator has no catalog with which to verify it. Check the
returned diagnostics before printing. `translate.PrintPostgres` produces:

```sql
SELECT "id" FROM "items" ORDER BY "id" LIMIT 2
```

`TestTopTranslation` checks source preservation, portable subtree reuse and
target reparse. Target identifier quoting uses decoded components. This
pass accepts only the [documented translation subset](../tests/sql/translate/contract.md),
including guards against output aliases shadowing the ordering key.

Adding `WITH TIES` produces a `with-ties` diagnostic whose range identifies
that source phrase; it returns no target. Even an asserted unique key does
not enable this rewrite. Other dialect constructs also produce diagnostics
instead of losing their meaning. `Example`, `TestTranslationDiagnostics`
and `TestBoundaryTieDiagnostic` check these paths.

## Verify the example

Run examples, portable and native matrices, per-dialect fixtures and
translation checks:

```sh
GOTOOLCHAIN=go1.23.12 go test -count=1 ./tests/sql/...
```

`TestPortableMatrix` compares all four grammars with the common corpus's
expected trees, printing and table lists. `TestNativeMatrix` compares native
acceptance and foreign rejection. Both run whole input and the same session
paused at every byte boundary before final EOF. The dialect suites add
independent tree, diagnostic and source-range oracles. Translation's
`TestStreamedTranslation` also checks target text and diagnostic ranges for
whole and bytewise input.

Run the optional [engine procedure](../tests/sql/translate/engines.md) in
its two disposable, memory-capped containers. It compares ordered integer
rows on PostgreSQL and SQL Server, with seven accepted controls, a tied
cutoff and an alias-shadowing counterexample. Ordinary tests skip this check
unless `LEAFKIT_ENGINE_PARITY=1`; report its actual opt-in result separately.
This does not verify MySQL execution or arbitrary SQL semantics.

Smoke-test the benchmarks:

```sh
GOTOOLCHAIN=go1.23.12 go test ./tests/sql/bench -run '^$' -bench . -benchtime=1x -benchmem -count=1 -cpu=1
```

Use the [benchmark method](../tests/sql/bench/method.md) for repeated private
measurements. It checks equal normalized trees and visitor results before
timing the matched frozen corpus. Grammar construction stays outside timed
loops; lexing, parsing and fresh input setup stay inside. Whole and 64-byte
stream schedules are separate, as are native and rejected inputs. There is
no speed guarantee or timing threshold.

Run `make fmt-check`, `make vet` and `make test` for the complete repository.
`TestPublishedFilesLinkNoInternalHost` checks tracked exportable files.
To test the actual public snapshot, use `git archive HEAD`, extract it to a
fresh directory and run the same checks there. That scan intentionally skips
outside a Git checkout; inspect the archive separately for excluded files
and internal links. On a systemd runner, put each Go or Make check inside
its own `systemd-run --user --scope -p MemoryMax=8G -p MemorySwapMax=0 -q --`.
