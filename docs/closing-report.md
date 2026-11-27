# First milestone implementation report

The library separates tree structure, evaluation, grammar execution and
bytescan integration. The [public API inventory](api.md) records landed
names and distinguishes selected owner decisions from open choices.
The [foundation audit](specs/foundation.md) connects the accepted
six-language design traces to the code and tests that exist today.

The initial audit recorded a partial parser foundation. Subsequent
oracles cover the two outstanding mechanisms: scoped declarations and
computed lexical requests. `TestComputedQueuedBodies` exercises both
together. The milestone remains open for a separate close-out decision.
The [foundation audit](specs/foundation.md#the-paper-traces-against-the-current-api)
identifies the remaining consumer language work.

## What the repository contains

- The tree accepts consumer-defined pointer nodes and provides common
  containers, typed values, error leaves and ambiguity nodes.
  `tree.Example`, `TestHandBuiltGrammarShapes` and `TestPayloadReferences` exercise
  independent construction and ownership.
- The evaluation tools let each kind rule choose which children to evaluate
  and which environment to use. `TestArithmetic`, `TestLexicalLet` and
  `TestFoldPathAndPosition` exercise typed payloads, scope and diagnostics.
- The parser runs caller-defined grammars, resumes at partial input and
  builds retained reductions at final EOF. `TestJSONOracle`,
  `TestExpressionOracles`, `TestStatementRecoveryOracle` and
  `TestPackedLeftRecursiveExpression` cover deterministic, operator,
  recovery and ambiguity behavior.
- The adapter bridges bytescan's streams and restartable lexers. The
  [grammar guide](writing-a-grammar.md) leads to runnable examples under
  `tests/`. `TestGrammarGuideMatchesExampleSources` checks its Go snippets
  against their source. The six paper languages remain design cases.
  `parser.Grammar` takes caller rules and the examples stay in test packages.

## Independence checks

`TestHandBuiltGrammarShapes` constructs trees without scanning. The
JSON fixture in `TestJSONOracle` feeds a hand-made source to the parser.
`TestBytescanOracleAndEveryInputSplit` compares the hand-made source with
both adapter paths. `TestTreeImportsOnlyStandardLibrary` and
`TestBytescanImportsStayInAdapter` enforce the package boundary.
`TestPublishedFilesLinkNoInternalHost` checks the public export selection.

## Findings for the bytescan boundary

The [adapter findings](specs/adapter.md#bytescan-boundary-findings) record
the current dependency constraints and the tests that exercise them.

- EOF gives no final token span or trivia. Keep the cursor and preserve
  trivia until the adapter assigns it. A filter that drops physical bytes
  can violate the adapter's range check. See
  `TestAdapterReportsInvalidAndSourceFailures`.
- An opaque scanner switch closure has no replay state contract. Use a
  fixed stream when one request suffices. Supply a replay factory and
  independent saved state for grammar-directed requests. `TestBytescanInterpolationModes`
  demonstrates the mode stack path.
- Cursor restart uses relative positions. The adapter restores the
  beginning-of-line flag and converts offsets to the absolute mark.
  Display columns differ between `tree.Positions` and
  `adapter/bytescan.Positions`. See `TestBytescanReplayPreservesBeginningOfLine`
  and `TestBytescanOracleAndEveryInputSplit`.
- Text retained after scanner buffer reuse must be immutable. Custom virtual
  and physical tokens need positioned constructors. See
  `TestFixedReadRetainsTextAndTrivia`.

## Findings for leafkit

- Generic parser and evaluator callers use `tree.Node` for stable common
  child access. `tree.Shape` can describe another result type, and a
  self-typed child accessor of that shape does not satisfy `tree.Node`
  unless it returns `tree.Node`. See `TestTypedRootConstraint`.
- The generalized parser must keep continuation-visible token counts and
  full lexical marks and requests through misses and repairs.
  `TestRecoverPreservesPackedReadingTokenCounts`,
  `TestChartRecoveryPreservesEqualCountLexicalMarks` and
  `TestChartRecoveryMergesOnlyCompatibleFailureOffers` guard that behavior.
- A transparent packed forest depends on equivalent states and normalized
  production shapes. The binary expression oracle proves sharing for its
  grammar. The malformed binary oracle checks a work limit for failure
  settlement. See `TestPackedLeftRecursiveExpression` and
  `TestPackedMalformedExpressionKeepsWorkBound`.
- Parser callbacks can replace a branch key, check a predicate or restore
  an enclosing key. `TestBranchStateChoices` and `TestBranchStateRecovery`
  check state-bearing alternatives and diagnostic isolation.
  `TestComputedCompetingRequests` checks request and checkpoint independence.
  `TestPaperHelperCompositions` shows how to express the proposed extension,
  word and line helpers with the current API. See the [paper-trace audit](specs/foundation.md#the-paper-traces-against-the-current-api).

## Inputs for the consumer ports

### symbolic

The [tree evidence](specs/tree.md#evidence-from-existing-consumers)
records symbolic's existing node model, bracket forms, traversal and
printer. A port needs a projection from its concrete node kinds to the
common envelope and ordered child slots, with typed payloads for scalars
and an external source lookup. Keep reader compatibility fixtures for
expressions, lists, maps, literals and end positions. In particular,
decide how a malformed alternating key and value sequence appears in the
new tree. The [parser evidence](specs/parser.md#evidence) records the
existing EOF and column conventions and the calculator's precedence
work. A port should compare that behavior before replacing its parser or
printer. Exact source-preserving printing remains intended.

### fnlang

The [evaluation evidence](specs/tree.md#evaluation-evidence) traces the
existing evaluator requirements to the current fold. A port needs explicit
rules for callable values, arguments, closures, forcing, dynamic results and
collection lookup. The consumer decides which scope to share, when to create
a child scope and how to carry runtime state. Preserve the distinction
between node-ID annotations and occurrence-specific errors when a tree is
shared. `TestLexicalLet` and `TestFoldPathAndPosition` are starting oracles
for scope and error reporting. `TestArithmetic` shows how evaluation uses
a reference-backed number while keeping published payloads read-only.

Future port work belongs in the consumer repositories. Reference grammars
provide examples of the calling contract, including [a scoped statement
language](../tests/statement/README.md) and [later ambiguity
resolution](../tests/ambiguity/README.md).
