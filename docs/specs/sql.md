# SQL composition example contract

## Coverage and versions

**Current contract.** The caller packages in `tests/sql` share one bounded
SELECT grammar and typed AST. `TestPortableMatrix` in
[guide_test.go](../../tests/sql/guide_test.go) checks the valid
[common corpus](../../tests/sql/core/testdata/common.json) against its
independent canonical-tree, printed-text and table-list oracles for core,
PostgreSQL, MySQL and SQL Server, both whole and at every byte boundary.
This is syntax composition, not SQL conformance or a library SQL API.
The [walkthrough](../sql-example.md) is the paired usage guide.

| Package | Reference or fixed mode | Additions to the shared subset |
| --- | --- | --- |
| [core](../../tests/sql/core/contract.md) | Fixed ASCII bare-name and doubled-quote mode | SELECT, DISTINCT, projections, required FROM, explicit AS, INNER JOIN ON, one comparison in WHERE, ORDER BY, optional semicolon |
| [postgres](../../tests/sql/postgres/contract.md) | PostgreSQL 18; standard_conforming_strings on | DISTINCT ON, dollar strings, dollar-bearing names, multiline single-quoted strings, bounded LIMIT n |
| [mysql](../../tests/sql/mysql/contract.md) | MySQL 8.4; ANSI_QUOTES and NO_BACKSLASH_ESCAPES | Backtick names, STRAIGHT_JOIN with optional ON |
| [tsql](../../tests/sql/tsql/contract.md) | SQL Server 2025 (17.x) reference; QUOTED_IDENTIFIER ON | Bracket names, TOP (n), optional WITH TIES with ORDER BY |

**Current contract.** The local contracts linked above define exact lexical
and grammar boundaries. Core has no functions, arithmetic, boolean
combinations, grouping, subqueries, NULL, outer joins, implicit aliases or
script parsing (`core.TestRejectedSubset`). Dialect additions do not lift
unrelated core exclusions (`TestRejectedDialectSyntax` in postgres and
`TestRejectedSyntax` in mysql and tsql). Reference versions identify the
syntax documentation used, not a promise of complete server compatibility.

## Composition and translation boundary

**Current contract.** Namespaced lexical rules and decoders can retain shared
lexeme and identifier types. Meaning-bearing syntax uses distinct dialect
node kinds with shared children. `core.Grammar`, `core.Builders`, each
dialect's `Grammar` and `Scanner`, and the tests below verify the split.
`core.Tables` visits relation references through extension children without
switching on dialects. `TestNativeMatrix` verifies native acceptance and
foreign rejection with stable whole/chunked diagnostics for three
unambiguous native queries, including core as a foreign parser.

**Current contract.** Translation is a separate read-only caller pass, bounded
by the [translation contract](../../tests/sql/translate/contract.md).
`ToPostgres` preserves supported portable subtrees and rewrites guarded
TOP counts to LIMIT. Its unique, non-null integer key is asserted by the
caller, not inferred. It diagnoses WITH TIES, unsupported dialect nodes,
unsupported projection shapes and unsafe ordering, including alias
shadowing. `TestTopTranslation`, `TestTranslationDiagnostics`,
`TestComposedProjectionShape` and `TestAliasShadowingDiagnostic` check those
boundaries. Printed target reparsing proves AST preservation, not database
execution equivalence. Source ranges still identify original input bytes.

**Current contract.** The opt-in `translate.TestEngineParity` uses the pinned
images in [engines.md](../../tests/sql/translate/engines.md), PostgreSQL 18
and SQL Server 2022 (16.x). The executed SQL Server version is distinct from
the grammar's 17.x documentation reference. Seven accepted integer controls
compare explicit ordered rows, including native star, qualified and proven
key aliases, zero and maximum bigint counts. A boundary-tie fixture contrasts
four source rows with three from a deliberately lossy LIMIT; the translator
rejects that source. An alias-shadowing counterexample also requires
rejection. The check skips unless opted in. No MySQL engine check, arbitrary
catalog/type proof, collation equivalence, optimizer validation or general
transpilation guarantee is provided (`TestEngineParity`, `testdata/setup.sql`
and the translation contract).

## Capability-to-test evidence

**Current contract.** These source tests verify the named capabilities.
Commands and expected example output are in the [guide](../sql-example.md).
Engine execution requires a separately reported opt-in run.

| Capability | Test or source oracle |
| --- | --- |
| One portable query across three dialects; visitor; TOP translation; WITH TIES diagnostic | [`sql.Example`](../../tests/sql/guide_test.go) |
| Four-parser whole/bytewise common corpus | [`sql.TestPortableMatrix`](../../tests/sql/guide_test.go), `core/testdata/common.json` |
| Native acceptance and foreign rejection, whole/bytewise | [`sql.TestNativeMatrix`](../../tests/sql/guide_test.go) |
| Fresh grammar rules, decoder hooks, source-backed lexemes | [`core.TestComposition`, `TestNamespacedLexicalValue`](../../tests/sql/core/composition_test.go), [`TestLexemeReferencesAndReadOnlyAccess`](../../tests/sql/core/core_test.go) |
| PostgreSQL token versus AST extension, shared visitor, bounds | [`TestDollarSpellingValueAndReuse`, `TestTypedModifierAndSharedVisitor`, `TestLimit`](../../tests/sql/postgres/postgres_test.go) |
| MySQL token versus AST extension, visitor and ranges | [`TestLexicalMode`, `TestStraightJoinAndRanges`](../../tests/sql/mysql/mysql_test.go) |
| SQL Server token versus AST extension, visitor and ranges | [`TestTypedNodesAndRanges`, `TestParseAndIsolation`](../../tests/sql/tsql/tsql_test.go) |
| Translation, source preservation, target reparse and streaming | [`TestTopTranslation`, `TestPortableDialectsAndIdentifierRendering`, `TestStreamedTranslation`](../../tests/sql/translate/translate_test.go) |
| Unsafe translation rejection and actual integer engine parity | [`TestTranslationDiagnostics`](../../tests/sql/translate/translate_test.go), [`TestEngineParity`, `TestBoundaryTieDiagnostic`, `TestAliasShadowingDiagnostic`](../../tests/sql/translate/engines_test.go) |
| Matched trees and visits before timing; separate native/rejected work | [`bench.TestMatched`](../../tests/sql/bench/harness_test.go), [`TestSeparateWorkloads`, `BenchmarkMatched`, `BenchmarkDialectOnly`, `BenchmarkRejection`](../../tests/sql/bench/bench_test.go) |
| Exported tracked files contain no internal host | [`TestPublishedFilesLinkNoInternalHost`](../../tests/published_files_test.go); `.gitattributes` defines archive exclusions |

**Design principle.** Keep measured results and internal tracker URLs out of
public documents. Archive verification and private execution evidence do not
publish or release the example. Benchmark smoke checks establish runnable
workloads; they make no performance claim.
