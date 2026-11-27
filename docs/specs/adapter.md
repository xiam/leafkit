# bytescan adapter

**Current contract.** Package `github.com/xiam/leafkit/adapter/bytescan`
implements [`parser.Source`](parser.md#tokens-layout-and-replay) over
[bytescan v0.7.0](https://github.com/xiam/bytescan/tree/v0.7.0).
`TestBytescanImportsStayInAdapter` checks that the engine, tree and other
production packages are independent of bytescan.

## Choose a source

Use `New` for one token stream. Create a bytescan cursor at byte zero of
the snapshot and build a `Scanner`, a `modes.Driver`, or a compatible filter
over it. Pass the source, its cursor and the single supported
`parser.Request` to `New`. The adapter owns calls to `Next` during that
parse. It retains completed reads for backtracking and rejects requests
different from the configured one. See `TestFixedReadRetainsTextAndTrivia`
and `TestBytescanInterpolationModes`.

Use `NewReplay` when parser requests can change token decisions. Supply an
opener for the available bytes at any retained absolute offset, an initial
state and a `Factory`. The adapter resets its text cursor at the mark and
passes the full request and saved state to the factory. The factory returns
a fresh bytescan source and can return a function that saves the state after
a token. The state must be independent of the cursor, scanner and driver,
which have mutable state. A factory that needs more bytes can return
`bytescan.ErrMoreInput` for the adapter to retry at the same mark.
The opener and factory must not commit side effects on a speculative read.
An opener can return a fresh view over caller-owned storage, such as a
section reader over one open file. The caller manages the storage lifetime.
See `TestBytescanParserSelectsOperatorOrLiteral` and
`TestReplayKeepsSiblingState`.

Grammar code can calculate the request with
[`WithLex` or `CallWithLex`](parser.md#computed-lexical-requests).
`NewReplay` passes that complete request to `Factory`. The parser
can keep its own delimiter queue in branch state while the adapter
keeps lexer checkpoints. `TestComputedQueuedBodies` checks both
responsibilities with captured delimiters, an embedded expression
and recovery from a wrong closer.

## Controls

**Current contract.** The adapter and caller divide the controls as follows.

| Control | Adapter | Caller |
| --- | --- | --- |
| Kinds, text and ranges | `consume` copies lexemes into immutable strings, preserves absolute half-open byte ranges, and maps `TokenTypeUnknown` to `parser.Invalid`. | Emit all physical tokens from the shared cursor, including trivia. Synthetic tokens have a positioned, empty span and no text. |
| Request, mode and parameters | `Source.Read` checks the layout value and keys replay by the complete mark and request. `New` checks its configured request. | Select the rules or driver mode from the goal, argument and layout. Reject unsupported requests. Save any mode stack, delimiter state and other lexer state in the checkpoint. |
| Trivia and physical breaks | `consume` attaches kinds in `Options.Trivia` to the next event or final EOF. A skipped LF counts as a break even inside a comment. A physical token in `Options.Newlines` also signals a break, including a caller-defined terminator such as a lone CR. | Supply trivia and physical line-break kinds. Keep trivia available to the adapter. |
| Logical layout | `consume` keeps the physical break across visible line tokens and synthetic layout tokens. A leading gap attaches once. `Source.successor` increments virtual progress when a synthetic marker leaves the byte unchanged. | Decide line joining, indentation, continuation and virtual-token policy in the factory or fixed profile. Save pending virtual tokens, indentation and any prior-token context. Emit a finite sequence of markers before the next physical token or EOF. |
| Contextual words | `consume` keeps word kinds and original text. | Use `parser.Word` in a keyword position and a kind match in an identifier position, as in `TestBytescanLayoutAndContextualWord`. |
| Pause and EOF | `consume` maps `bytescan.ErrMoreInput` to `NeedInput`. A fixed stream keeps pending trivia. A replay stream discards the incomplete attempt. Final EOF uses the shared cursor offset and attaches trailing trivia. Other errors propagate to the parser. | Expose more of the same input at a pause. Return `io.EOF` at a true end. Make token decisions stable across feeds. |

`NewReplay` uses distinct successor identities when two requests read the
same byte under different states. It makes no assumption about equivalence
of the supplied states. The same retained mark and request returns the same
completed read, including its successor. `Source.DiscardBefore` drops
cached reads and checkpoints before the supplied byte and leaves previously
returned text and trivia usable. The opener still needs access to input from
every live mark. `TestReplayKeepsSiblingState` and
`TestFixedReadRetainsTextAndTrivia` exercise these guarantees.

Treat successor keys as opaque within each source. Different feed
schedules can change their allocation order. `TestComputedCompetingRequests`
compares whole and streamed replay with a checkpoint bijection that
preserves each creating event and grammar use. Within one run, a
suspended read retries with the very same complete mark and request.

Keep the immutable snapshot in caller-owned storage. A program can reserve
a source ID at the start and expose its `tree.SourceLookup` after sealing.
`Positions` resolves UTF-8 spans to the bytescan convention of one-based
lines and zero-based rune columns. `tree.Positions` supplies one-based byte
columns. `TestBytescanOracleAndEveryInputSplit` compares node endpoints and
final EOF with bytescan and checks a hand-made source against both adapter
paths. The same test feeds every byte split, including one inside a rune.

The layout fixture shows all four parser layout requests and a final dedent
before EOF. Layout policy, token rules and tree factories belong to the
caller. The [layout reference grammar](../../tests/layout/README.md) applies
that policy to nested suites and interpolated expressions.
`TestLayoutGrammar` checks contextual word matching, layout and lexical
requests on sealed and streamed input.

## bytescan boundary findings

- `Source.Next` returns no position or trivia with EOF. `New` takes the shared cursor
  explicitly. `consume` checks for missing physical ranges and reads the
  exact final position from that cursor.
  `TestAdapterReportsInvalidAndSourceFailures` exercises an EOF after
  `bytescan.Skip` has hidden a trailing token. [bytescan source](https://github.com/xiam/bytescan/blob/v0.7.0/source.go)
- `modes.Driver` can switch scanners but cannot save a switch closure's state. A fixed
  stream can retain its outputs. Replay requires an immutable state value
  and a fresh driver. `TestBytescanInterpolationModes` uses
  `ModeSet.Driver` and `StackDriver.Stack` for replay. [bytescan mode driver](https://github.com/xiam/bytescan/blob/v0.7.0/modes/modes.go)
- `cursor.Reader.ResetStart` preserves start flags supplied by the caller and resets coordinates to
  the start of the new reader. `Source.readReplay` sets those flags and
  `consume` adds the mark's byte offset to each span.
  `TestBytescanReplayPreservesBeginningOfLine` checks the flag. [bytescan cursor restart](https://github.com/xiam/bytescan/blob/v0.7.0/cursor/start.go)
- `Token.Text` constructs a string from the token's runes. The adapter calls it before
  the next read can reuse a filter's buffer. A custom filter supplies
  positions with `NewTokenWithSpan` or `NewEmptyToken`. The constructors
  `NewToken` and `NewTokenFromString` lack positioned spans.
  `TestFixedReadRetainsTextAndTrivia` varies the contents of a reused
  buffer after each read. [bytescan token](https://github.com/xiam/bytescan/blob/v0.7.0/token.go)
