# SQL Server example contract

## Reference and boundary

The reference is Microsoft SQL Server 2025 (17.x), using the versioned
[TOP documentation](https://learn.microsoft.com/en-us/sql/t-sql/queries/top-transact-sql?view=sql-server-ver17)
and [identifier documentation](https://learn.microsoft.com/en-us/sql/relational-databases/databases/database-identifiers?view=sql-server-ver17).
The example uses `QUOTED_IDENTIFIER ON`. It extends the
[core subset](../core/contract.md), rather than implementing the complete
Transact-SQL language. No engine execution or result ordering is verified here.

## Syntax and meaning

**Current contract.** `Grammar` accepts the shared SELECT syntax with these
local additions. `TestDialectFixtures`, `TestPortableFixtures`,
`TestRejectedSyntax` and `TestParseAndIsolation` verify the boundaries.

```ebnf
modifiers    = [DISTINCT] [TOP "(" integer ")" [WITH TIES]] ;
integer      = digit {digit} ;
bracket_name = "[" nonempty_bracket_body "]" ;
```

**Current contract.** The count is a nonnegative decimal bigint literal in the
range 0 through 9223372036854775807. Leading zeros are retained. TOP limits
rows after the shared ordering, if present. Plain TOP does not require ORDER
BY; without it, the chosen rows are unspecified. WITH TIES requires ORDER BY
and retains rows tied at the cutoff according to the complete ordering list.
It can produce more rows than the count. `Top.Count`, `Top.WithTies`,
`TestTypedNodesAndRanges` and `TestDialectFixtures` verify representation and
preservation through printing. These are syntax and roundtrip checks, not
proofs that a database executes the query as described by the reference.

**Current contract.** `Scanner` adds the namespaced `tsql:TOP`, `tsql:WITH`,
`tsql:TIES`, `tsql:PERCENT` and `tsql:bracket-name` token kinds. Keywords are
ASCII case insensitive and use the core ASCII word boundary. PERCENT is
reserved so an unsupported percentage modifier cannot become a projection.
Bracket identifiers decode doubled closing brackets (`]]`) to `]`; `[` needs
no escape. Names are nonempty and cannot contain CR or LF, matching the
example's bounded lexical mode. Shared quotes, comments and bare names retain
the core behavior. `TestDialectFixtures`, `TestRejectedSyntax` and
`TestParseAndIsolation` cover escaping, Unicode, empty names, newline rejection,
trivia and keyword boundaries.

**Current contract.** The example rejects unparenthesized TOP, signs, decimals,
parameters, arithmetic, PERCENT, missing ORDER BY with WITH TIES, misplaced or
repeated modifiers, LIMIT, OFFSET/FETCH and UNION. Other core exclusions still
apply. `TestRejectedSyntax` verifies diagnostic trees, equal whole/chunked
problems, and printing refusal. The SELECT guard checks token kinds, so an
ORDER spelling inside a comment, string or delimited name cannot enable ties.

## Composition and public trees

**Current contract.** `Grammar` starts with a fresh `core.Grammar`, replacing
only `name` and `modifiers` and guarding the shared `select` production.
`Builders` delegates every shared kind to `core.Builders`; a `TokenDecoder`
converts bracket names into shared `core.Lexeme` values. All identifiers,
projections, tables, joins and order items retain the shared node types.
`TestPortableFixtures` runs every valid core corpus entry unchanged, while
`TestTypedNodesAndRanges` verifies the shared types and `core.Tables` visitor.
`TestParseAndIsolation` verifies independent grammar maps and core rejection
of dialect syntax. No library implementation or core package is changed.

**Current contract.** A `Top` node has kind `tsql:top`, its count as child zero,
and an optional `tsql:with-ties` marker as child one. It lives in the shared
Select's `modifiers` field after any DISTINCT node. `Count` returns the shared
number lexeme; `WithTies` reports marker presence. Published fields are private.
`Child` rejects negative and out-of-range indexes. `core.Canonical` therefore
compares count and tie mode without losing dialect semantics.
`TestDialectFixtures` and `TestTypedNodesAndRanges` verify these properties.

**Current contract.** Lexemes retain their immutable source snapshot, original
spelling and exact token byte ranges, including bracket escapes. The TOP
production envelope includes the modifier and any leading trivia; its count
range excludes trivia. `TestTypedNodesAndRanges` checks these ranges, decoded
values and public `tree.Validate` invariants.

**Current contract.** `Engine`, `Scanner` and `Builders` compose public LeafKit,
Bytescan and adapter APIs. `Parse` accepts a complete snapshot and returns Go
errors for operational failures, with syntax failures in `core.Diagnostics`.
`Print` delegates shared syntax to `core.PrintWith` and prints TOP with its
original count and tie mode. Shared decoded bracket names print as ANSI quoted
names under the declared mode. The core printer rejects TOP rather than
silently dropping the row limit. `ExampleParse`, `TestDialectFixtures`,
`TestTypedNodesAndRanges` and `TestRejectedSyntax` check these behaviors.

**Current contract.** The caller tests feed each valid fixture and each rejected
query whole and through every byte boundary, including inside Unicode,
doubled brackets, numbers and comments. They finalize EOF through the public
source interface. `TestDialectFixtures` checks independent expected typed trees
and parse/print/parse equality. `TestPortableFixtures` reuses the valid shared
oracle. `TestRejectedSyntax` requires rejection and equal diagnostic details.
