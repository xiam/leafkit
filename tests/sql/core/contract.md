# SQL core contract

## Subset

**Current contract.** `Grammar` accepts one SELECT with a required FROM and
an optional final semicolon. It does not accept a script. Keywords are
case insensitive. These productions describe the accepted core syntax;
`TestCommonFixtures` and `TestRejectedSubset` check the boundaries.

```ebnf
file       = select [";"] EOF ;
select     = SELECT [DISTINCT] projection {"," projection}
             FROM table {join}
             [WHERE comparison] [ORDER BY order {"," order}] ;
projection = "*" | expression [AS name] ;
table      = identifier [AS name] ;
join       = INNER JOIN table ON comparison ;
comparison = expression ("=" | "<>" | "!=" | "<" | "<=" | ">" | ">=") expression ;
order      = expression [ASC | DESC] ;
expression = identifier | string | number ;
identifier = name {"." name} ;
name       = bare_name | quoted_name ;
```

**Current contract.** The example has no catalog lookup or type analysis.
It accepts syntactically valid comparisons even when their SQL types would
not compare. It does not implement ISO SQL, aggregate/function calls,
qualified wildcards, boolean combinations, arithmetic, parentheses,
implicit aliases, outer joins, grouping, subqueries, NULL or a row limit.
`TestRejectedSubset` covers unsupported forms. Bare JOIN is excluded;
INNER must appear explicitly. Only expression projections accept aliases.

## Fixed lexical modes

**Current contract.** `Scanner` recognizes ASCII bare names
`[A-Za-z_][A-Za-z_0-9]*`. Core keywords are reserved and use the same ASCII
word boundary. Double quotes delimit nonempty identifiers; single quotes
delimit strings, including an empty string. Doubling the delimiter escapes
it. Backslashes have no escape meaning. Quoted text can contain Unicode,
but cannot cross CR or LF. Numbers contain digits with an optional decimal
point followed by digits; signs and exponents are excluded. Whitespace is
space, tab, CR or LF. Comments are `--` through the line end and non-nested
`/* ... */`. `Scanner`, `TestCommonFixtures`, `TestRejectedSubset` and
`TestLexemeReferencesAndReadOnlyAccess` verify this mode. Unrecognized input
becomes a Bytescan unknown token and produces a syntax diagnostic.

**Current contract.** Shared token and node kinds use `sql:`. `NameToken`,
`QuotedNameToken`, `StringToken`, `NumberToken`, `Keyword` and `Symbol` name
the token contract. Dialect-only kinds must use a separate prefix, such as
`demo:` in `TestComposition`. Extra `LexRule` entries precede core rules;
Bytescan chooses the longest token, with registration order breaking equal
length ties. Callers use an explicit keyword boundary, not a bare literal,
when adding reserved words. `TestComposition` verifies keyword precedence
without changing the common corpus.

## Trees and source storage

**Current contract.** `Builders` publishes private, read-only structure as
`File`, `Select`, `Identifier`, `Projection`, `Table`, `Join`, `Comparison`,
`Order`, `Distinct` and `Star` pointers. Leaves are `Lexeme` pointers.
`Child` preserves syntax order; `Field` and `Identifier.Parts` copy child
references. `One` returns the sole child of a field, or nil. Accessors do not
expose writable backing slices. `TestLexemeReferencesAndReadOnlyAccess`
verifies these properties and the public `tree.Validate` contract.

| Node | Named fields in child order |
| --- | --- |
| Select | modifiers, projections, from, joins, where, order, suffix |
| Projection | expression, alias |
| Table | name, alias |
| Join | table, condition |
| Comparison | left, operator, right |
| Order | expression, direction |

**Current contract.** Optional fields have zero children. Lists preserve
all children in syntax order. An identifier has unnamed `Lexeme` children
for its name parts. A File has one unnamed Select child on success.
`Grammar` supplies these bindings, and `TestCommonFixtures` checks child
order against independently specified trees.

**Current contract.** `Snapshot` names immutable text with a nonempty
`tree.SourceID`. Reusing an ID for different text violates the caller
contract. Every lexeme holds a snapshot pointer and its token byte range,
not a copied payload. `Lexeme.Raw` slices this range; `Range` excludes
leading trivia, while `Info().Span` retains the grammar production span.
`Snapshot.Raw` rejects foreign or invalid ranges. `Value` decodes doubled
quotes, folds bare ASCII names to lower case and normalizes `!=` to `<>`.
It preserves quoted identifier case and the spelling of numbers.
`TestLexemeReferencesAndReadOnlyAccess` checks a large Unicode value, shared
snapshot references and token ranges.

**Current contract.** `Canonical` serializes kind, decoded scalar values
and ordered children of a successful tree. It ignores source identity,
byte offsets, node identity, trivia, quote spelling and keyword case.
It compares syntax, not semantic equivalence: `1` and `1.0`, for example,
remain different. Extension leaves with scalar values must implement
`SQLValue() string`; extension structural nodes expose children normally.
Diagnostics require a separate comparison of `parser.Problem`.
`TestCanonicalSourceIndependence` and `TestCommonFixtures` verify this rule.

## Composition

**Current contract.** Each `Grammar` call returns a fresh rule map.
Replace named entries before `parser.Configure`. Calls use `parser.Ref`,
so changes reach nested uses as well. The slots are `name`, `identifier`,
`operand`, `expression`, `comparison`, `modifiers`, `projection`, `table`,
`join`, `order`, `suffix`, `select` and `file`. `modifiers` runs immediately
after SELECT; `suffix` is empty by default and runs after ORDER BY. A
replacement modifier is responsible for allowing ordinary DISTINCT if its
dialect supports it. `TestComposition` replaces lexical, modifier, join
and expression rules and checks every valid common fixture, both whole
and chunked.

**Current contract.** Use `Scanner(cur, extra...)` with the same cursor
passed to the public Bytescan adapter. `Trivia` returns fresh adapter
options. `Builders(snapshot, extension, decoders...)` constructs all shared
node kinds. An `ExtensionBuilder` constructs dialect-owned node kinds;
its children can include shared nodes so visitors can descend through it.
A `TokenDecoder` maps a namespaced dialect token to a shared `Lexeme` value
and marks whether it is quoted. The decoder is a pure function of raw text;
it does not change snapshot storage. Builders copy decoder entries. Missing
or nil decoders for dialect tokens in shared leaves return construction
errors. `TestNamespacedLexicalValue` checks an artificial asymmetric quoted
name, its shared identifier node, raw spelling, decoded value, portable
quoting and split-token behavior. Extensions need no library API change.

## Printing, visiting and diagnostics

**Current contract.** `Print` emits uppercase keywords, explicit aliases
and comparison operators, with normalized spacing and no final semicolon.
It emits ANSI doubled-quote escaping from decoded delimited values. It
rejects diagnostic and dialect-owned nodes. `PrintWith` delegates only
unknown nodes to an extension printer, which can call the shared recursive
printer. `Tables` traverses shared Table nodes inside any extension node;
it returns FROM and joined relation references in syntax order. It does
not interpret aliases, expression qualifiers or catalog names.
`ExampleParse`, `TestCommonFixtures` and `TestComposition` verify these
contracts and parse/print/parse equality.

**Current contract.** A decoded scalar prints its shared value rather than
its dialect token spelling. `TestDecodedNumberPortablePrint` maps raw `0x10`
to the shared numeric value `16`, verifies the original snapshot range, and
requires diagnostic-free core reparsing with the same canonical tree. Raw
spelling remains available through `Lexeme.Raw` and `Lexeme.Range`.

**Current contract.** `Parse` distinguishes operational errors from SQL
diagnostics: a non-nil Go error reports a configuration, source, limit or
construction failure. On malformed SQL, LeafKit returns a diagnostic tree.
Callers must check `Diagnostics(root)` before consuming or printing it.
A valid statement prefix can precede a trailing diagnostic; the core does
not repair SQL. `TestCommonFixtures` checks matched expected trees,
diagnostic byte positions, found tokens and ordered expected token kinds.
`TestRejectedSubset` checks additional rejected syntax and whole/chunked
diagnostic equality. Both drive the same parser session through every byte
boundary, including multibyte text, comments and doubled delimiters, then
finalize EOF through public source APIs.
