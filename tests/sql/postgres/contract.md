# PostgreSQL example contract

## Reference and subset

**Current contract.** This example pins the PostgreSQL **18** reference for
[SELECT](https://www.postgresql.org/docs/18/sql-select.html#SQL-DISTINCT)
and [lexical structure](https://www.postgresql.org/docs/18/sql-syntax-lexical.html#SQL-SYNTAX-DOLLAR-QUOTING).
It implements the [core SELECT subset](../core/contract.md) plus the
productions below. `Grammar`, `Scanner`, `TestPortableFixtures` and
`TestDialectFixtures` verify the bounded syntax; the upstream documentation
is a dialect reference, not a claim that all PostgreSQL syntax is accepted.

```ebnf
modifier = DISTINCT [ON "(" expression {"," expression} ")"] ;
operand  = core_operand | dollar_string ;
suffix   = [LIMIT nonnegative_bigint_literal] ;
```

**Current contract.** `DISTINCT`, `ON`, punctuation and common SQL keep
shared `sql:` tokens. `Grammar` uses two-token lookahead to distinguish
ordinary DISTINCT from DISTINCT ON. The latter produces one
`*DistinctOn` child in the shared `*core.Select` modifiers field.
`DistinctOnKind` is `postgres:distinct-on`. Its expressions are shared
nodes in syntax order. `Expressions` copies the child references; `Info`
and `Child` implement the public read-only tree contract. Names,
projections, tables, joins, comparisons and ordering retain core types.
`TestTypedModifierAndSharedVisitor` checks typed children, source span,
read-only access and unchanged `core.Tables` traversal.

**Current contract.** This is a syntax example. It does not check types,
catalog names, DISTINCT ON versus leftmost ORDER BY expressions, or
whether ordering uniquely chooses a row. PostgreSQL retains the first row
of each DISTINCT ON group; a caller needs suitable ordering to select a
predictable row, including a tie breaker when relevant. The example does
not execute SQL or claim engine validation. `Grammar` has no semantic
validation stage. The [translation example](../translate/README.md) provides a separate
optional engine comparison; this grammar does not execute SQL.

**Current contract.** All other core exclusions apply, including required
FROM, explicit AS and INNER JOIN, no functions, general parenthesized
expressions, boolean combinations, grouping or subqueries. Row limits accept only `LIMIT n` for a decimal
integer in 0..9223372036854775807; ALL, OFFSET, expressions, signs and
fractional counts remain excluded. `LimitToken` is namespaced
`postgres:LIMIT`; `LimitKind` is `postgres:limit`. `Limit` retains one shared
count lexeme; `NewLimit` permits the separate translation pass to reuse a
source count. `TestLimit` checks typed accessors, bigint bounds, printing,
keyword boundaries and whole/chunked behavior.
`TestRejectedDialectSyntax` and `TestPortableFixtures` check rejected forms.
`Grammar` implements LIMIT through the suffix composition point.

## Lexical modes and storage

**Current contract.** `Scanner` uses the core ASCII bare-name mode, adding
`$` after the first name character. A dollar-bearing name uses
`DollarNameToken` (`postgres:dollar-name`) and builds a shared `core.Lexeme`
with `core.NameKind`. This prevents a dollar delimiter adjoining a bare
name or keyword from being split off as a string. Core keywords remain
case insensitive and reserved. `TestDollarNamesAndBoundaries` checks
these boundaries and preserves the decoded lower-case name.

**Current contract.** `DollarStringToken` is `postgres:dollar-string`.
`dollarString` recognizes `$$...$$` or `$tag$...$tag$`, ending at the first
identical, case-sensitive closing delimiter. Tags use ASCII
`[A-Za-z_][A-Za-z_0-9]*`; empty tags are allowed. Contents can contain
apostrophes, backslashes, newlines, Unicode and different dollar tags;
there is no escaping or nested interpretation. NUL and unterminated or
invalid delimiters are rejected. Non-ASCII tags are outside this fixed
mode. `TestDollarSpellingValueAndReuse`, `TestDialectFixtures` and
`TestRejectedDialectSyntax` verify these cases, including repeated tokens
and partial closing-delimiter matches.

**Current contract.** Dollar strings build shared `core.StringKind`
lexemes through the core decoder hook. `Raw`, `Range` and `Snapshot`
preserve original spelling and reference-backed storage; `Value` removes
only the matching delimiters. Decoding occurs on access. The lexer stores
only delimiter progress, not the literal payload. `TestDollarSpellingValueAndReuse`
checks source byte ranges, a large Unicode payload and shared snapshot
identity. `dollarString` retains immutable rule progress so scanner
restarts and partial input cannot mutate another token's state.

**Current contract.** Single-quoted strings allow literal newlines and
retain SQL doubled-apostrophe escaping, using `QuotedStringToken`
(`postgres:quoted-string`) with a shared string node. Backslashes remain
literal, matching `standard_conforming_strings = on`. Escape-prefixed and
Unicode-prefixed strings, adjacent-string concatenation, non-ASCII bare
names and general PostgreSQL lexical modes remain outside this example.
`Scanner`, `Engine` and `TestDollarSpellingValueAndReuse` verify this mode
and multiline output reparsing. Other quoting and comments retain the core
lexical restrictions.

## Printing and input

**Current contract.** `Print` delegates shared nodes to `core.PrintWith`
and adds rendering for typed `DistinctOn` and `Limit` nodes. Dollar strings normalize
to single-quoted decoded values with doubled apostrophes, including literal
newlines. Source spelling is available through lexeme accessors rather
than the printer. `core.Canonical` compares parse/print/parse trees while
ignoring source identity and quote spelling. The core printer rejects the
dialect modifier; the PostgreSQL printer rejects diagnostics and unknown
nodes. `TestDialectFixtures`, `TestDollarSpellingValueAndReuse` and
`TestTypedModifierAndSharedVisitor` verify these properties.

**Current contract.** `Parse` consumes one complete snapshot. As in core,
Go errors report operational failures while malformed SQL produces parser
diagnostic nodes. Callers must inspect `core.Diagnostics` before consuming
the tree. `Engine` and `Scanner` also support public adapter input for
streaming callers. `TestPortableFixtures`, `TestDialectFixtures` and
`TestRejectedDialectSyntax` compare whole input with the same session fed
at every byte boundary, including opening/closing tags, Unicode and EOF.
The [local fixtures](testdata/postgres.json) pin trees and diagnostic byte
ranges; the common fixtures retain their original trees and coordinates.
