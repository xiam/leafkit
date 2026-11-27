# Translation example contract

**Current contract.** `ToPostgres` is a separate read-only AST pass over
one parsed `core.File` containing one `core.Select`. Portable syntax from
core, PostgreSQL, MySQL and SQL Server retains its original nodes. The
pass requires each SELECT projection to use the shared `core.Projection`
wrapper, including when its expression is `core.Star`. Native star queries
are accepted. A custom public grammar can replace the projection rule and
produce an unwrapped Star that passes generic `tree.Validate`; the pass
rejects that unsupported SQL AST shape with an `unsupported-projection`
source-range diagnostic. `TestComposedProjectionShape` verifies whole/chunked
rejection without mutation; the native-star fixture verifies normal TOP
translation and target reparse. The pass rejects parser diagnostics and
unsupported dialect nodes instead of partially translating a file. `TestPortableDialectsAndIdentifierRendering`,
`TestNativeExtensionRejection` and `TestTranslationDiagnostics` verify this
boundary. It is a caller example, not a library transpiler or execution engine.

**Current contract.** Ordinary SQL Server `TOP (n)` translates to one typed
`postgres.Limit` in the shared SELECT suffix. The source must have one FROM
table, no joins, no DISTINCT, no existing suffix, and exactly one ORDER BY
identifier. The caller supplies `UniqueIntegerKey` with the decoded table
components and column name, asserting that the ordering column is unique,
non-null and integer-valued. ORDER BY may use the bare column, the full table
qualification, or the active table alias. An alias replaces the table
qualification. A bare ORDER BY name that collides with a projection alias
is accepted only when exactly one such alias exists and its projection
expression itself names the asserted input key. Otherwise it returns an
`ordering-key` source-range diagnostic with no target. Potential
case-insensitive alias collisions are checked conservatively. Qualified
input-key ordering bypasses output aliases. `TestAliasShadowingDiagnostic`,
`TestStreamedTranslation` and the qualified/proven-alias fixtures verify this.
ASC and DESC remain shared. The parser has no catalog and
cannot validate that assertion. `TestTopTranslation` and
`TestTranslationDiagnostics` verify the syntactic guard and failure paths.

**Current contract.** This example establishes execution equivalence for
fixed integer-only fixtures, not arbitrary catalog types or session modes.
Callers must resolve database names and types consistently and use equivalent
integer comparison behavior. String collation, implicit casts, nullable ordering,
case-insensitive catalog lookup and arbitrary SQL expressions are outside its
semantic guarantee. The fixture relation has a primary integer key and integer
payload, with no NULL values; `testdata/setup.sql` defines it. `TestEngineParity`
compares generated queries and source queries with explicit ordered integer
oracles, including zero and maximum-bigint counts. That test skips unless
explicitly enabled. A normal test pass makes no claim that engines ran.

**Current contract.** `WITH TIES` always returns a `with-ties` diagnostic,
including when the asserted key would remove ties. Its Span is the original
WITH TIES production span, which can include leading trivia. No target is
returned. `TestBoundaryTieDiagnostic`, `TestTranslationDiagnostics` and the
optional engine boundary fixture verify this. SQL Server can return more
than n rows at a tied cutoff; ordinary PostgreSQL LIMIT cannot preserve that
behavior. The reference semantics are [SQL Server TOP](https://learn.microsoft.com/en-us/sql/t-sql/queries/top-transact-sql?view=sql-server-ver17)
and [PostgreSQL 18 SELECT](https://www.postgresql.org/docs/18/sql-select.html#SQL-LIMIT).

**Current contract.** A TOP rewrite creates new file and SELECT ancestors
through public core builders, retaining the source spans. Portable projection,
FROM, predicate and order subtrees keep their pointers and node IDs. The target
LIMIT shares the original count lexeme and snapshot. Its source span refers
to that count; it is not an offset in generated SQL. `TestTopTranslation`
checks pointer reuse, new ancestor IDs, `tree.Validate`, unchanged source
printing and canonical shape. Core.Table nodes remain visible to the shared
visitor. No source snapshot or published node is mutated.

**Current contract.** `PrintPostgres` emits each decoded name component in
ANSI double quotes, doubling embedded double quotes. It quotes bare names as
well, including words reserved only in the target. Source bare names follow
the existing core lower-case decoding convention; this does not emulate every
engine's catalog case rules. Strings and scalar values use the PostgreSQL
printer. `TestPortableDialectsAndIdentifierRendering` covers bracket,
backtick, Unicode, quote escapes and target-reserved names. `TestTopTranslation`
and `target` reparse output with the target grammar and compare canonical
ASTs. This proves structure and identifier values, not execution equivalence.
