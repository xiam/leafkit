# MySQL example contract

## Reference and scope

This example pins the MySQL 8.4 Reference Manual:
[JOIN clause](https://dev.mysql.com/doc/refman/8.4/en/join.html),
[identifier quoting](https://dev.mysql.com/doc/refman/8.4/en/identifiers.html),
and [SQL modes](https://dev.mysql.com/doc/refman/8.4/en/sql-mode.html).
STRAIGHT_JOIN preserves the instruction to read the left input before the
right input. This example covers syntax and tree preservation. It does not
execute queries or verify a server's optimizer behavior.

**Current contract.** `Grammar` composes the [core subset](../core/contract.md)
by replacing only `name` and `join`. It adds a backtick name token and this
join production, where `table` and `comparison` remain shared references:

```ebnf
join = INNER JOIN table ON comparison
     | STRAIGHT_JOIN table [ON comparison] ;
```

`TestFixtures` checks the core corpus directly and adds independent expected
trees in `testdata/mysql.json`. `TestRejectedSyntax` checks malformed joins,
USING, SELECT-level STRAIGHT_JOIN, row limits and other excluded syntax.
This is a composition example, not a complete MySQL grammar. The exclusions
of the shared core still apply, including required FROM and explicit AS.

## Fixed lexical mode

**Current contract.** `Scanner` fixes `ANSI_QUOTES` and
`NO_BACKSLASH_ESCAPES`. Double quotes delimit identifiers; single quotes
delimit strings. Backslashes are ordinary characters. Backtick identifiers
are nonempty, allow Unicode and doubled backticks, and exclude CR/LF.
`mysql:backtick-name` decodes to a shared quoted `core.Lexeme`. Raw text and
byte ranges still refer to the original immutable snapshot. Shared rules
remain the core's bounded lexical subset; this does not extend its bare
identifier, number or comment rules to all MySQL forms. `TestLexicalMode`,
`TestStraightJoinAndRanges` and `TestRejectedSyntax` verify these boundaries.

**Current contract.** STRAIGHT_JOIN is case insensitive and reserved, using
`mysql:STRAIGHT_JOIN` with the core's ASCII word guard. Longer bare names
such as `straight_joined` remain names. Quoted STRAIGHT_JOIN remains a name.
`TestLexicalMode` and the lowercase native fixture verify this distinction.
Each `Grammar` and `Scanner` call is local; the dialect does not add rules
to core parses (`TestStraightJoinAndRanges`).

## Typed tree and printing

**Current contract.** `StraightJoin` has kind `mysql:straight-join` and
private, read-only fields. `Table()` returns its right table operand;
`Condition()` returns the optional ON comparison, or nil. Child indices
expose those operands in order. Its left input is the preceding FROM and
join sequence in the shared Select, just as for a core Join. This distinct
type retains join order semantics instead of converting it to an INNER
JOIN. `TestStraightJoinAndRanges` checks the type, accessors, absent and
invalid children, aliases and exact byte spans. `core.Tables` traverses
the shared Table children in source order, including mixed join chains.
`TestFixtures` checks independently specified relation lists and trees.

**Current contract.** `Print` delegates shared nodes to `core.PrintWith`
and renders STRAIGHT_JOIN with its optional ON. It emits ANSI double quotes
for decoded delimited names, including names originally in backticks.
These quotes are valid in the fixed mode. Keyword spelling and spacing
normalize; doubled quotes escape the output delimiter. `core.Print` rejects
the ordered join rather than dropping its instruction. Diagnostics also
prevent printing. `TestFixtures`, `TestLexicalMode`,
`TestStraightJoinAndRanges` and `ExampleParse` check these behaviors.

**Current contract.** `TestFixtures` checks parse/print/parse against the
expected canonical trees, not only against the first parse. Whole input and
input split at every byte boundary use the same public parser and adapter
APIs. `TestRejectedSyntax` compares whole/chunked diagnostics, and fixtures
pin diagnostic ranges, found tokens and expected kinds. Shared syntax keeps
its expected trees; name diagnostics add the dialect backtick alternative.
`TestStraightJoinAndRanges` checks reference-backed lexemes and source ranges
across multibyte text, doubled delimiters and comments. The README links
here and its runnable example is `ExampleParse`.
