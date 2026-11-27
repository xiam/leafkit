# Parser engine

A consumer supplies a token source, a grammar of Go functions and tree
factories. The engine holds grammar continuations and returns a tree after
recognizing a complete input. `Grammar` and `Configure` require caller
rules. The runnable grammars live in the test directories linked from
the [grammar guide](../writing-a-grammar.md).

**Current contract.** The `parser` package provides deterministic grammar
steps, expression operators, syntax recovery, and packed ambiguity with
deferred construction into the [tree package](tree.md). See
`TestJSONOracle`, `TestExpressionOracles`, `TestStatementRecoveryOracle`
and `TestPackedLeftRecursiveExpression`. Consumers choose whether and
when to resolve ambiguity. See `TestAmbiguousDeclarationResolution`.

## Evidence

- The bytescan v0.7.0 [core contract][bs-core], [composition spec][bs-compose]
  and [resource contract][bs-resources] distinguish function steps, rule
  composition and bounded streaming. Leafkit keeps function steps and
  caller-driven execution. A lexer can compare competing matches for one
  token. A parser also needs resumable calls, reductions and branch-specific
  token boundaries.
- The bytescan v0.7.0 [`Source` and `NewEmptyToken`][bs-source] separate
  `io.EOF` from successful tokens and allow synthetic tokens in a layer
  above the scanner. [`modes.Driver`][bs-modes] can switch scanners after a
  token. [`ModeSet.Driver` and `StackDriver.Stack`][bs-stack] can restart
  with an explicit mode stack. Neither the pull interface nor a mode stack
  alone captures arbitrary consumer state. The [resumption spec][bs-resume]
  distinguishes cursor replay, lexer lookahead extent and `ErrMoreInput`.
- Symbolic v0.1.0 has a `parserState` function and one-token `peek` in
  [`parser/parser.go`][sym-parser]. Its `parserStateOpen` delegates nesting
  to Go calls. [`lexer.Lexer.Next` and `Token.Pos`][sym-lexer] expose a
  positioned EOF token and 1-based lines with 0-based rune columns. The
  [scanner port][sym-port] bridges scanner EOF and the old lexer API. The
  [span follow-up][sym-span] removes `spanFrom`'s use of the next token
  while keeping the integer/float lookahead in `expectNumberNode`.
  Successful matches carry their own end positions.
- Symbolic v0.1.0's [calculator][sym-calc] shows a more demanding case.
  `advance` skips layout and stores EOF position separately. `expr`,
  `term` and `binary` encode precedence and left associativity, `unary`
  recurses, and `primary` parses groups. The engine should express the
  same structure without requiring every consumer to write the token
  loop, precedence levels, or stack handling. The symbolic tag uses
  bytescan v0.6.0. The current adapter uses the version declared in
  [go.mod](../../go.mod).

[bs-core]: https://github.com/xiam/bytescan/blob/v0.7.0/docs/specs/core-contract.md
[bs-compose]: https://github.com/xiam/bytescan/blob/v0.7.0/docs/specs/composition.md
[bs-resources]: https://github.com/xiam/bytescan/blob/v0.7.0/docs/specs/resources-and-streams.md
[bs-resume]: https://github.com/xiam/bytescan/blob/v0.7.0/docs/specs/resumable-lexing.md
[bs-source]: https://github.com/xiam/bytescan/blob/v0.7.0/source.go
[bs-modes]: https://github.com/xiam/bytescan/blob/v0.7.0/modes/modes.go
[bs-stack]: https://github.com/xiam/bytescan/blob/v0.7.0/modes/stack.go
[sym-parser]: https://github.com/xiam/symbolic/blob/v0.1.0/parser/parser.go
[sym-lexer]: https://github.com/xiam/symbolic/tree/v0.1.0/lexer
[sym-port]: https://github.com/xiam/symbolic/commit/61577fd1001dad57e2ee669eb3b1a05990efb48a
[sym-span]: https://github.com/xiam/symbolic/commit/f94e63b81c87d2bccc2c515c25fe6a54fbf8082c
[sym-calc]: https://github.com/xiam/symbolic/blob/v0.1.0/_example/calc/parser.go

## Tokens, layout and replay

**Current contract.** `Source` and `Mark` address one immutable logical
input. A feed makes more bytes available. Previously supplied bytes and
positions stay fixed. An edit or replacement starts a new parse under a
new snapshot ID. A live source can reserve the ID at `Begin` and make its
external `tree.SourceLookup` return unavailable until the snapshot is
sealed. An already complete fixture can expose its snapshot and gate
availability separately. See `TestReplayAcrossFeeds` and `tree.Positions`.

```go
type TokenKind string
const Invalid TokenKind = "leafkit:invalid"

type Trivia struct {
    Kind TokenKind
    Span tree.Span
}
type Token struct {
    Kind        TokenKind
    Text        string
    Span        tree.Span
    Leading     []Trivia
    BreakBefore bool
    Synthetic   bool
}
type Layout uint8
const (
    TriviaOnly Layout = iota
    Lines
    Indentation
    Joined
)
type Request struct {
    Goal, Argument string
    Layout         Layout
}
type Mark struct {
    Byte, Virtual int
    Key            string
}
type ReadStatus uint8
const (
    TokenReady ReadStatus = iota
    EndOfInput
    NeedInput
)
type Read struct {
    Status   ReadStatus
    Token    Token
    Next     Mark
    Trailing []Trivia
}
type Source interface {
    Begin() (tree.SourceID, Mark)
    Read(Mark, Request) (Read, error)
    DiscardBefore(byteOffset int)
}
```

- **Request.** A grammar picks a goal and immutable argument before asking
  for a token. The source defines the goal vocabulary. A goal may choose
  a mode, select a different rule within a mode, or pass a delimiter or
  other argument to a rule. The same bytes can start different tokens
  under `division`, `regexp` and `template-tail`. Expectations about
  which token kinds match the grammar stay separate from the lexical goal.
- **Layout.** The request carries a layout policy selected by the grammar.
  A source defines its behavior. `TriviaOnly` exposes physical line breaks
  through trivia and `BreakBefore`. `Lines` also makes logical newlines
  consumable. `TestLineOracleInBothStyles` uses explicit newlines and a
  lexical goal for line values. More elaborate sources can use the remaining
  modes.
  `Indentation` adds indent and dedent tokens for the consumer's indentation
  policy. A grammar scopes `Joined` over bracket contents that suppress
  logical newlines and indentation changes. The source can also join
  explicit continuations, ignore blank and comment-only lines for block
  structure, and detect bad indentation. Its checkpoint includes physical
  and logical line state, prior-token summaries, indentation stack and any
  pending virtual tokens. A source can implement Go-style end-of-line
  semicolon insertion under its own goal. Leafkit assigns no language
  semantics to indentation or line ends. See `TestLayoutGrammar` and
  `TestBytescanLayoutAndContextualWord`.
- **Trivia.** A skipped item carries a kind and raw range. The source emits
  it once per reading, in `Leading` of the next token or `Trailing` at
  final EOF. Returned token text and trivia stay immutable and usable across
  reads, feeds and discards. A source with a reusable byte buffer can
  convert its token window to a Go string before returning. The consumer
  retains snapshot bytes independently when it needs raw trivia or line and
  column lookup. See `Token` and `TestReplayAcrossFeeds`. `BreakBefore`
  records a physical line terminator between significant tokens, including
  a newline inside an intervening comment. It still works when trivia is suppressed as a
  grammar symbol. With explicit layout tokens it refers to the same gap
  until the next significant token. A significant newline's own bytes
  appear in its token span. An unsuppressed synthetic layout marker owns
  zero bytes. Attach a gap once. A physical break can still be reflected in
  later tokens' `BreakBefore` flags. The chosen reading supplies its trivia
  and tokens to the factories in `Build` or the file's trailing trivia.
  See `TestBytescanOracleAndEveryInputSplit` and
  `TestPackedTokenLedgersStayLocalToReadings`.
- **Words.** A source can emit one word kind with original text. The
  grammar can use `Word("match")` in keyword positions and
  `Kind("word")` in identifier positions. `Word` tests the offered text.
  A custom step can check a kind before comparing or normalizing its
  spelling. Contextual keywords need no lexer-mode change. See `Matches`.
- **End of input.** Final EOF gives an exact end mark and trailing trivia.
  The engine exposes an end event to a step. It can be tested repeatedly,
  contributes no parse progress and cannot satisfy repetition. Pending
  virtual tokens come before EOF. Temporary exhaustion gives `NeedInput`
  with no committed token, trivia, mark or extra indentation transition.
  A source returns `Invalid` with a physical span for undecodable or
  unrecognized input it can advance over. I/O and broken source contracts
  are fatal errors. See `checkRead`, `TestProgressAndFailures` and
  `TestRecoveryFatalFailures`.

A mark identifies the next unread position and the lexer state at that
position. `Key` is a source-local canonical equality key for the complete
state or a unique identity when equivalence is unavailable. A snapshot ID
identifies bytes for source lookup. It gives no information about
checkpoint equality or future tokens. The key's namespace includes
mode stack, queued heredoc delimiters or bodies, indentation,
pending virtual tokens and source configuration. Equal marks in the same
parse promise the same token, next mark and trivia at any equal request
when enough bytes are available. Two marks can refer to the same byte and
still have different keys. Distinct keys conservatively inhibit merging.

Each successful token read strictly advances `(Byte, Virtual)` in
lexicographic order. The virtual counter is zero at a new byte position
and increases for zero-width tokens. A source must emit only finitely
many successive virtual tokens at one byte, including at EOF. The engine
checks monotonicity and a configured bound. A token's span can begin after
the old mark, for example after leading spaces. A token's recorded
span never controls the read cursor. `checkRead` checks the token and
trivia ranges against the read interval and snapshot. `EndOfInput`
supplies the final physical end, even when trailing trivia advanced past
the last token.

`Read` works like restoring a checkpoint, reading one token and saving a
new checkpoint. It has no logical effect on other marks and can be called
again at the same mark under another request. A successful token decision is stable as the input grows. If a suffix
could change the answer, return `NeedInput` while the input is open.
A source may internally
seek, reinstantiate a scanner, or cache results by full mark and
request. Its supported retention must include every live mark, including
ones retained by ordered lookahead, error recovery or a suspended parse.
`DiscardBefore` states that the engine will make no read with an earlier
byte mark. The deterministic VM pins the oldest tentative choice. The
retained byte distance runs from that floor to the physical end reported by
a successful read. Tokens and trivia already returned remain immutable and
available to the engine. A source that hits its retention
limit returns a resource error. A source with bounded retention may run
out of capacity on a long unresolved choice. A monotone lexer can be
wrapped in a token buffer for one request vocabulary with request-invariant
behavior. The adapter's `New` uses that fixed-profile model.
`TestReplayAcrossFeeds` and `TestBytescanParserSelectsOperatorOrLiteral`
check the request-sensitive alternative.

### Bytescan adapter boundary

**Current contract.** The [bytescan adapter](adapter.md) preserves token
ranges and trivia and maps final EOF, temporary exhaustion and unmatched
tokens to the parser source contract. Its `New` function retains a fixed
stream. `NewReplay` restores a byte cursor and independent state and
passes the full request to a caller factory. `Positions` gives bytescan
line and rune columns from the external snapshot. See
`TestBytescanOracleAndEveryInputSplit`,
`TestBytescanInterpolationModes` and `TestAllKeepsLexicalExitsAndCorrelatedFileRoots`.

## Grammars and execution

**Current contract.** The package is `github.com/xiam/leafkit/parser`.
A configured grammar is immutable. It can be shared when its callbacks
and factories permit concurrent calls. A session owns one source, its
recognition state and any pending output recipes. Drive a session from one caller at a
time. `Run` executes on that caller's goroutine. Feed the same source
between calls after `Paused`. The waiting request and its frames,
tentative choices and recipes survive. A generalized session also
retains pending branches and their shared results. A second call after completion
returns the same result. See `TestProgramResumesPendingStep` and
`TestJSONOracle`. The caller owns publication and evaluation.

```go
type Rule struct { /* immutable representation */ }
type Grammar struct {
    Start   string
    Rules   map[string]Rule
    Default Request
}
type Limits struct {
    Depth, ZeroMoves, VirtualTokens int
    Steps, RetainedBytes int
    Descriptors, ForestEdges int
}
type Engine[N tree.Node] struct { /* private */ }
type Session[N tree.Node] struct { /* private */ }
type Status uint8
const (
    Done Status = iota
    Paused
)
func Configure[N tree.Node](g Grammar, build Builders[N], limits Limits) (*Engine[N], error)
func (e *Engine[N]) Start(src Source, initial State) *Session[N]
func (s *Session[N]) Run() (N, Status, error)
```

`Configure` checks references, declared transfers and statically visible
progress. It requires node and file factories and a compatible error
factory. It installs the default error factory when the node type
accepts it. Zero-valued limits select finite defaults. The session checks
its own limits regardless of the source's retention policy. `Run` checks
final EOF under the default request after the start rule finishes. A
syntax miss can produce a diagnostic in the returned tree. On
`Paused` or a fatal error the root is the zero value of `N`.
`ErrGrammar`, `ErrSource`, `ErrLimit` and `ErrBuild` distinguish fatal
failures. A final error ends the session and is returned again on later
calls. See `TestProgressAndFailures` and `TestRecoveryFatalFailures`. The
generalized path also limits tabulated descriptors and forest edges.
See `TestChartConfigurationAndLimits`.

### One-token steps

```go
type State string
type Pattern struct { /* kind, text, or EOF */ }
func Kind(TokenKind) Pattern
func Word(string) Pattern
func EndOfFile() Pattern
func OnSameLine(Pattern) Pattern
func Matches(Look, Pattern) bool

type Look struct {
    Token Token
    End   bool
    Point tree.Span
}
type View struct {
    State State
    Parts Parts
}
type Step func(Look, View) Move
type ProgramState struct {
    Step                    Step
    Control                 func(View) Move
    Takes, Jumps, ReturnsTo []string
    Calls                   []string
}
func Program(entry string, states map[string]ProgramState) Rule
type Move struct { /* private */ }
func Take(next, bind string) Move
func Jump(next string) Move
func SetState(next string, state State) Move
func Call(rule, resume, bind string) Move
func CallWithLex(rule, resume, bind string, request Request) Move
func Return() Move
func Reject(expected ...Pattern) Move
```

A step inspects one look event and returns a move with the next program
label or a terminal outcome. `Take` consumes the event and records it
under the binding, if present. `Jump` changes label at the same mark.
`SetState` replaces the state at that mark and transfers to a declared
`Jumps` label. A `Control` callback can do this without asking for a token.
`Call` pushes a frame with a return label and optional binding for the
result. It calls a named rule at the same mark. `CallWithLex` applies
the same declarations and scopes a computed request over the callee.
`Return` pops a frame and produces its
recorded results. `Reject` reports expected patterns and leaves the
mark unchanged. A program state supplies a token-reading `Step` or
a token-free `Control` for pure calls, jumps and returns. The engine
asks the source for the current frame's request before invoking a
`Step`. If `Control` rejects, the VM obtains the event at its mark for
the diagnostic. See `Session.program`. `Look.Point` is the zero-width location of the offered
token or final EOF. An end event has no token. A call or a changed request can then
reuse that read only if both mark and request still match. Peeking does
not commit source movement. End-of-file is tested and returned from.
Taking it is a grammar-contract error.

**Current contract.** A program declares label transfers and callees.
`Program` copies the declarations. A program state supplies exactly one
callback. A control callback cannot take a token. `Return` and `Reject`
need no declaration. The engine checks returned moves against the lists.
See `checkProgram` and `TestProgressAndFailures`. Label,
compiled rule site and continuation identify control state. Function
pointer identity or hidden mutable closure fields cannot be used for
state equality. `View` offers the current branch state and locally
accumulated syntax. Use `Parts` during the callback. The engine owns the
source cursor and stack. See `TestBranchStateProgramAndExpression`.

### Building blocks

```go
func Tok(Pattern) Rule
func Ref(name string) Rule
func Seq(...Rule) Rule
func Opt(Rule) Rule
func Many(Rule) Rule
func Sep(item, separator Rule, trailing bool) Rule
func Drop(Rule) Rule
func Bind(name string, body Rule) Rule
func Make(slot string, kind tree.Kind, body Rule) Rule
func At(Pattern) Rule
func Not(Pattern) Rule
func Lex(Request, Rule) Rule
func WithLex(prefix Rule, compute func(View) Request, body Rule) Rule
func Update(body Rule, update func(View) State) Rule
func Guard(body Rule, accept func(View) bool) Rule
func Scope(body Rule) Rule
```

**Current contract.** `Tok` matches and consumes one token and records it. With
`EndOfFile()` it succeeds without consumption or output. `At` tests
without consumption. `Not` makes the same test and inverts the result.
`OnSameLine` requires a token without an intervening physical break before
matching its pattern. Use `At` with that pattern for a zero-width check.
`Seq` runs children in order. `Opt` and `Many` stop on a clean miss
at entry. A child failure after committed consumption propagates.
`Sep` permits zero items, separates items with the supplied rule, and
allows a final separator only when requested. It consumes a separator
once. After consuming one, the next item is required unless the trailing
form is enabled and the item fails without committing any input. A grammar
must name and match the enclosing end delimiter separately. `Ref`, both
named and inside a program, resolves in the configured grammar.

`Lex` scopes a request over its body. A nested `Lex` temporarily
replaces the request. On exit the engine restores the outer request and
keeps the lexer mark from successful reads. A failed tentative choice
restores its saved checkpoint. See `TestReplayAcrossFeeds`.

`Drop` discards structural output and retains the matched token ledger
for spans and trivia. It also forwards error descendants in their original
order, with their skipped tokens still owned by the diagnostic. See
`TestDropRetainsDiagnostics`. `Bind` gives a name to the structural output and
matched tokens of a rule, including absence or a list. `Make` wraps its
body in one reduction with a kind and output slot. A result carries
ordered output including named captures. Ordinary leaves can preserve a
single token. Groups can keep delimiters in the token ledger and emit
only their contents as structural children. `compiler.compile` lowers
programs and combinators to compiled rule sites. `Session.tick` runs the
deterministic sites and `chart.open` runs the generalized sites.
`TestLineOracleInBothStyles` checks equivalent named calls with hand
steps and combinators.

### Branch-local state

**Current contract.** `Engine.Start` supplies the initial `State` key.
`Update` runs its body and replaces the body's exit state with the
key returned by the callback. `Guard` keeps the same exit state and
output when its predicate accepts. A false predicate discards the
body's output and rejects at its exit. Recognition obtains the actual
offer there to report the miss. Each callback receives the body's exit
state and matched parts. Use `Seq()` as the body to run the action at
the current position. The action itself consumes no token. A failed
body propagates its miss without calling the wrapper's callback.
`Scope` keeps its body's output and restores the state that was
current on entry. See `Session.stateRule`, `chart.stateRule` and
`TestBranchStateDeclarations`.

A successful named call passes its exit key back to its caller. A
sequence passes the key to the next element. Each completed iteration
passes it to the next item or separator. `Expr` passes it through
atoms, operands and postfix tails in recognition order. A failed rule
rolls back its state to the entry key. A completed sibling or earlier
iteration supplies that key when it precedes the failed rule. For
example, a failed trailing item leaves the state from a kept separator.
Rejected trials and abandoned repairs supply no outgoing state.
See `TestBranchStateProgramAndExpression`,
`TestBranchStateAbandonedArm` and `TestBranchStateRecovery`.

Equal keys must have the same meaning to every grammar callback.
Keep any data reached through a key immutable for the parse. The engine
retains string values by reference to their immutable storage. It does
not clone lookup tables or payloads behind them. Callbacks may run
again when a trial is replayed or a diagnostic waits for input. Use
their `View` and `Parts` during the call and derive the key without
mutating external state. Generalized callbacks on a packed body wait
for the packed summary to settle. See `State`, `Session.setState`,
`chart.stateRule` and `TestBranchStateEquivalentExitsSharePack`.

### Computed lexical requests

**Current contract.** `WithLex` runs its prefix under the enclosing
request. After a successful prefix, the callback receives its matched
parts and exit state. Use `Seq()` for a request derived only from
state. The callback returns the complete `Request` by value. Its
`View` and `Parts` are valid during the call. A failed prefix
propagates its miss. A generalized prefix settles before the callback
runs. Distinct admissible exits supply distinct views, and a packed
exit supplies the common token and kind summary described under
[construction](#reductions-and-tree-construction). See
`TestComputedRequestCaller` and `TestComputedRequestObservesSettledPrefix`.

The computed request replaces the enclosing goal, argument and layout
only while recognizing the body. A successful body passes on its
exit state, full lexer mark and output in prefix-then-body order. The
caller resumes under its original request. If a body fails, the
engine keeps the actual offer and scoped request for failure selection.
Ordinary failure and recovery rules decide which preceding
outputs and state survive. A tentative choice restores its own entry
mark, request and state before trying the next arm. See
`TestComputedRequestAbandonedBranch` and `TestComputedRequestRepairOrder`.

`CallWithLex` lets a program calculate the request in a `Step` or
`Control` callback from its own current view and use it for a named
callee. Declare the callee and resume label in `Calls` and `ReturnsTo`.
The ordinary binding records the callee's result. A caller may use a
scoped call inside another scoped request. See `TestComputedQueuedBodies`.

A pause in the body retains the computed request and full mark.
Unchanged-input polling retries the held read. The computation may run
again if a tentative trial is replayed. Keep it pure. Recognition and
request computation use the usual work, depth, descriptor and edge limits.
The caller controls the cost of the callback and the size of the strings
it returns. `Configure` rejects a nil callback. A computed layout
outside the supported enum is `ErrGrammar`. A source that declines an
otherwise valid request returns its error through `ErrSource`.
Resource and factory failures retain `ErrLimit` and `ErrBuild`.
See `TestComputedRequestErrors` and `TestComputedRequestPollingWorkBudget`.

### Composing the paper helpers

**Current contract.** To extend a grammar, copy its rule map and replace
selected rules before calling `Configure`. A reused `Ref` resolves by name
in the configured grammar. Give the original rule a new name when an
override calls it. To normalize a word, a program step checks the token
kind and compares the original text with the consumer's normalization.
To check a physical line without taking a token, use `At(OnSameLine(pattern))`
or a program step that inspects `Look.Token.BreakBefore` and
`Look.End`. `TestPaperHelperCompositions` exercises these forms.
The proposed convenience names were `Extend`, `WordAs` and `SameLine`.

## Reductions and tree construction

**Current contract.** `Make` records a recipe, and the engine calls the
factories for retained recipes after final EOF. See `TestReplayAcrossFeeds`
and `TestDeepNamedCallsAndBuildUseBoundedGoStack`.

```go
type Parts struct { /* read-only recognition data */ }
func (p Parts) Tokens(name string) []Token
func (p Parts) Kinds(name string) []tree.Kind

type TokenRange struct { /* immutable ledger reference */ }
func (r TokenRange) Len() int
func (r TokenRange) At(index int) (Token, bool)

type Field[N tree.Node] struct {
    Name   string
    Nodes  []N
    Tokens TokenRange
}
type Build[N tree.Node] struct {
    Slot   string
    Kind   tree.Kind
    Span   tree.Span
    Fields []Field[N]
    Tokens TokenRange
}
type Builders[N tree.Node] struct {
    Node      func(Build[N]) (N, error)
    Error     func(slot string, problem Problem) (N, error)
    File      func(full tree.Span, children []N, trailing []Trivia) (N, error)
    Ambiguity func(slot string, span tree.Span, alternatives []N) (N, error)
}
```

A reduction records an immutable recipe. Its input order is grammar
order, including multiple results under the same binding. A named binding
records its tokens and direct nodes. An unnamed output is a
field with an empty name. A bare captured token is available to the
builder without imposing a node. `Build.Tokens` references the recipe's
directly owned tokens, including dropped punctuation, in order. Tokens
inside separately built children travel with those children. Use `Make` around
top-level tokens if a factory needs their records. The file factory
receives child nodes and trailing trivia. This lets a
parent with an ambiguous child keep its own tokens even when no single
tokenization describes the child's interior. For an individual reading,
source tools can combine these ledgers and use spans to identify trivia
and gaps. The engine can store intervals or persistent ledger references.
A consumer who visits every token in every descendant pays for those
visits. Recognition captures can inspect matched tokens through `Parts`
while their distinguishing ledger remains available. A zero name queries unnamed
captures. `Kinds` exposes immediate recipe kinds in order. A packed child with
multiple readings reports `tree.KindAmbiguity`. Its enclosing capture
exposes tokens only when all readings agree on the token records. Put a
filter at the choice that declares a candidate when it needs a specific
reading. See `TestChartPrattAndProgramComposeWithPacks` and
`TestAmbiguousDeclarationResolution`.

Ordinary reductions use the physical interval from the entry mark to
the last consumed mark that survives rollback inside the reduction.
It may include leading trivia. A zero-width reduction has a point span.
The file wrapper uses the full input span from the initial mark through
EOF, including final trivia. Speculative lookahead never extends a span.
See `TestReplayAcrossFeeds` and `TestJSONOracle`. For an expression
chain, each reduction keeps the expression entry mark and ends at the
last consumed mark. See `TestExpressionOracles`.
A grammar declaring a packed choice supplies one root of that slot per
alternative.
The engine checks the full site interval for each root and the factory
output. Build the full-span root in the grammar if a production normally
returns just a narrower child.

**Current contract.** The parser constrains `N` to `tree.Node` and calls
`tree.Validate` on the finished root. A grammar can use the common
`tree.Node` interface or a narrower interface that embeds it. A
self-typed child accessor with a different result type does not satisfy
`tree.Node`. See `TestTypedRootConstraint`.

**Current contract.** A typed consumer can choose `N = tree.Node`
with narrower Go interfaces for its own slots. For example an
`Expr` interface embeds `tree.Node` and a consumer marker method.
A parent whose condition field is `Expr` accepts a normal expression
or an expression error. Declare both with that marker and use the slot
to select the error constructor. Check field types in the ordinary
builder and return a construction error on mismatch. A consumer with a
narrower `N` supplies an error factory when `*parser.ErrorNode` cannot
satisfy its interface. See `TestTypedRootConstraint`.

**Current contract.** `Builders.Ambiguity` wraps two or more alternatives
in the order supplied. The wrapper has `tree.KindAmbiguity` and the
supplied span. Its children are the exact alternative nodes. A consumer
can implement private markers on its own ambiguity type. When
`*tree.Ambiguity` satisfies `N`, `Configure` supplies the factory if it
is omitted. A grammar that reaches `All` needs a compatible factory.
The factory runs only for packs with multiple alternatives. See
`TestTypedAmbiguityFactory` and `TestChartConstructionFailuresStayFatal`.

**Current contract.** A factory may retain immutable inputs and handles
the recipe shapes declared by its grammar. Factory failure is reported as
a construction error. The session
validates the resulting graph against the tree contract before handing it
out. The engine allocates no public nodes or tree IDs while exploring
alternatives. Factories run descendants first for retained recipes,
memoized per retained recipe identity and slot. They are pure
constructors apart from allocation and fresh tree identity. They never
update consumer scope, log events or publish partially built nodes.
User-visible work such as evaluating the selected tree or publishing
diagnostics runs after `Done` in the caller. Leafkit cannot undo an
arbitrary Go side effect. Mutating shared state, doing I/O, retaining and
changing callback inputs, or making results depend on call order violates
the callback contract. See `TestReplayAcrossFeeds`,
`TestRecoveryChoiceDefersConstruction` and `TestTypedAmbiguityFactory`.

## Choice and branch state

**Current contract.** Every choice starts its arms from a shared
entry checkpoint. A grammar that reaches only `First` uses the
deterministic VM. See `TestNestedFirstCommitment` and `TestReplayAcrossFeeds`.

```go
type Policy uint8
const (
    First Policy = iota
    All
    Longest
)
type Candidate struct {
    Kind          tree.Kind
    Span          tree.Span
    Parts         Parts
    Before, After State
}
type ChoiceSpec struct {
    Policy    Policy
    Lookahead int
    Slot      string
    Filter    func(Candidate) bool
}
func Choose(spec ChoiceSpec, alternatives ...Rule) Rule
```

### First

`First` tries arms in declaration order. A rejection before commitment
restores the entry mark, request, recipes and token records. Success
commits. An arm also commits when its accepted prefix meets the
`Lookahead` token budget. Zero selects one token. Source virtual tokens
count. Grammar insertions and EOF leave the count unchanged. Nested
choices spend the enclosing budget only when their prefix survives.
A temporary shortage in an earlier arm suspends the decision. See
`TestNestedFirstCommitment` and `TestRecoveryProgressAndInsertion`.

When a `First` arm itself calls a generalized choice, local results can
remain packed. The engine settles that arm before selecting it or
trying a later arm. While the budget is observable, exits with different
consumed-token counts stay distinct even at the same mark, including
when `Recover` repairs those prefixes. A surviving prefix that reached
the budget commits the arm. A failure or repair below the budget leaves
the later arms available. See `TestFirstCommitmentUsesEachPackedReadingTokenCount`
and `TestRecoverPreservesPackedReadingTokenCounts`.

### All and Longest

`All` continues each successful exit, including exits at different
lengths. A suffix can accept a shorter exit even when a longer one is
available. At final EOF the engine publishes the complete surviving
readings. See `TestDanglingElseKeepsBothFullReadings`.

`Longest` chooses a successful local result by physical end byte,
then declaration order and derivation order. A synthetic token can
keep the same byte and leaves that ordering unchanged. A clean
result takes priority over a local repair. The engine waits while a
pending arm could change the decision. The winner resumes its caller
with the full saved lexer mark and exit state under the caller's
request. When the winning result contains a pack, the engine selects its earliest
reading. See `TestLongestWaitsAndResumesWinningMark` and
`TestLongestKeepsOneReadingAndBreaksTiesByArm`.

`All` and filtered choices require `Slot`. Each eligible reduction
has one root in that slot whose physical span matches the choice.
A filter runs on local root candidates after their inputs settle
and before construction or continuation. It receives the kind, span,
read-only parts, the choice's entry key in `Candidate.Before` and the
reading's exit key in `Candidate.After`. A scoped update can affect
recognition within its scope while leaving the two keys equal.
A filter can reject a declaration after inspecting a bound name.
It can inspect a nested packed child's summary through `Parts`.
A consumer that needs a specific nested reading can filter at the
inner choice or resolve it later over the tree. See
`TestAmbiguousDeclarationResolution` and `TestBranchStateChoices`.

### Equality and sharing

**Current contract.** The generalized scheduler tabulates a compiled
rule site at its entry mark, consecutive virtual-token count, lexical
request, branch-state key, expression binding power and whether a caller
observes token count. A result resumes each subscribing
continuation. The continuation keeps its own prefix, capture bindings
and enclosing request. Equal physical positions can retain distinct
marks and lexical states. `TestAllKeepsLexicalExitsAndCorrelatedFileRoots`
checks that an exit resumes only its compatible suffix.

At an `All` site, equivalent exits add alternative roots to a common
slot and endpoint. Different exit keys keep separate readings even
at the same complete mark. The pack can grow while a structural
continuation uses its shared reference. A Go step or filter that observes the
children sees a stable summary after they settle. A repaired path
carries its outstanding repair history separately. See `chartCell`,
`chart.all`, `TestChartPrattAndProgramComposeWithPacks`,
`TestBranchStateChoices` and `TestBranchStateEquivalentExitsSharePack`.

The chart's recognition cells carry parser state and continuation
context. Source reads share a separate cache keyed by complete mark
and request within one source and session. A cached source event can
serve multiple grammar continuations and states while each keeps its
own use of that event. Distinct computed delimiter arguments remain
distinct cache requests. See `chartKey`, `chartReadKey` and
`TestComputedCompetingRequests`.

The engine shares complete derivation edges and their children.
For example, two arms supplying pairs `(A, B)` and `(C, D)` preserve
their pairings. Each arm can refer to a common nested child without
copying that subtree. Alternative order follows the grammar, then
split positions and child derivation order. Lexer and parser state keys
identify equivalent contexts. See
`TestPackedLeftRecursiveExpression` and
`TestAllKeepsLexicalExitsAndCorrelatedFileRoots`.

### Progress and resource bounds

**Current contract.** `Configure` checks references, declared moves,
choice declarations, visible empty repetition and recursive progress.
A grammar that permits left recursion uses an unfiltered `All` on
the cycle. The productive binary form `E → E "+" E | atom` seeds the
chart with atoms and grows results as new endpoints become available.
Zero-yield cycles such as `A → A | empty` are rejected. Keep ordered
choice, recovery and capture-observing callbacks out of same-mark
recursive components. A consuming recursive call can use them at a
later entry mark. Settling failed derivations skips cycles without a
new offer and shares the resolved failures at later entry marks.
See `TestChartConfigurationAndLimits`, `TestPackedLeftRecursiveExpression`
and `TestPackedMalformedExpressionKeepsWorkBound`.

The generalized session holds the input from the initial mark while
alternatives need replay. The retention limit applies to that extent.
`Descriptors` bounds tabulated calls and `ForestEdges` bounds result,
alternative and failure edges. Deterministic subrules run in bounded turns
using the original VM. A temporary shortage preserves waiting
continuations. Retrying with no additional input leaves the work
budget unchanged. Limits and callback contract errors are fatal.
See `TestChartPollingForInputPreservesWorkBudget`,
`TestChartFailurePollingPreservesWorkBudget` and `TestChartConfigurationAndLimits`.

A state-only update or jump spends work. Token consumption supplies
progress for repetition, recursive configuration and choice commitment.
The deterministic VM retains at most one entry key per open frame under `Depth` and a bounded
number of failure records under its work limits. On the chart, entry
keys occupy `Descriptors` and exit and failed keys occupy
`ForestEdges`, with pending work under `Steps`. Revisiting one byte
with a growing set of keys consumes these limits. `RetainedBytes`
limits replayable source extent. The caller controls key sizes and
any memory used by its callbacks or immutable backing data.
See `TestBranchStateProgressAndLimits`.

### Packed complexity and publication

**Current contract.** In the binary grammar in
`TestPackedLeftRecursiveExpression`, a fixed stream of `n` operands
has at most one symbol pack per interval. A binary reduction is
identified by the start, split and end. The oracle checks one recipe
per such triple and bounds published nodes and child edges by a
cubic polynomial. Each root split shares the packed children for its
left and right subintervals. It never enumerates full binary readings.

Here the cubic bound counts descriptors, joins and packed edges for
the fixed binary grammar. Extra lexer states, variable-length
tokenizations, higher-arity productions, recovery and callbacks have
their own work and storage costs. Token projection into a builder also
costs work in the size of the token ranges it receives. To preserve
factoring in a larger grammar, declare structural intermediate slots
and let parents accept packed children at those slots. See
`chart.sequence` and `forestBuilder.constructNode`.

At EOF the engine constructs each retained recipe once and builds
one ambiguity wrapper for each surviving pack with multiple
alternatives. A single surviving alternative supplies its own root.
If distinct complete exits cannot share one local root, the engine
builds full-span file alternatives and wraps them at slot `"file"`.
See `TestPackedLeftRecursiveExpression` and
`TestAllKeepsLexicalExitsAndCorrelatedFileRoots`.

## Expressions

**Current contract.** `Expr` recognizes one atom with optional prefix,
infix and postfix operators. An atom must consume at least one token.
Configuration rejects visibly empty atoms and left-corner cycles
through the atom. A successful atom callback that consumes nothing is
a grammar error. See `TestExpressionConfiguration`.

```go
type Fixity uint8
const (
    Prefix Fixity = iota
    Infix
    Postfix
)
type Assoc uint8
const (
    Left Assoc = iota
    Right
    NonAssoc
)
type Operator struct {
    Form       Fixity
    Match      Pattern
    Power      int
    Assoc      Assoc
    Kind       tree.Kind
    Tail       Rule
    Goal       *Request
}
func Expr(slot string, atom Rule, ops ...Operator) Rule
```

Higher power binds more tightly. An infix operator parses its right
operand with strictly higher power for `Left` or `NonAssoc`. `Right`
also admits operators of its own power in the right operand. After a
`NonAssoc` reduction, the next infix at the same binding level cannot
have that same power. A second such operator reports a syntax error at
its token. Parenthesized atoms can start a fresh expression. A prefix
parses its operand with its own power as the inclusive minimum.
A postfix consumes its opener and then runs its optional `Tail`.
A call tail owns the closing parenthesis. Prefix and postfix ignore
`Assoc`. Power must be nonnegative and `Match` must match a token.
See `TestExpressionOracles`, `TestExpressionSyntaxFailures` and
`TestExpressionLongChains`.

At the start of an operand, the engine probes prefix operators before
calling the atom. After an operand, it tries eligible infix and
postfix operators in declaration order. Eligibility depends on the
operand's minimum power. Each probe uses the ambient request unless
`Goal` supplies a complete replacement request for that operator.
`Expr` copies operator declarations and their requests. The atom and
tail keep the ambient request and can scope their own requests with
`Lex`. A probe that needs input suspends before later candidates are
tried. On resumption it retries that request at the same mark.
See `TestExpressionOperatorRequestsResume`.

A bare atom forwards its original output. Each operator reduction
records a recipe with the expression slot and the operator's kind.
Factories receive these ordered `Build.Fields`.

| Form | Fields in order |
| --- | --- |
| Prefix | `operator`, `operand` |
| Infix | `left`, `operator`, `right` |
| Postfix | `left`, `operator`, and `suffix` when a tail is declared |

The `operator` field holds the matched token. It is also directly owned
in `Build.Tokens`. The other fields hold their matched token ranges and
the ordered nodes produced by their rules. The `suffix` range starts
after the opener. To preserve separate named fields within a suffix,
build a child with `Make` in the tail. Each reduction spans the
expression entry mark through the last consumed mark. Factories run
only for retained recipes after complete recognition. See
`TestExpressionBuildFields` and `TestExpressionRollback`.

The engine folds repeated left-infix and postfix results in one
expression frame. Right operands, prefix operands and nested tails use
the engine's explicit stack and count toward its depth limit.
For a fixed table, bounded token probes, and linear atom and tail work
in total, expression recognition takes linear work in the number of
tokens and operator reductions. Each reduction adds a fixed number of
captures and stores token ranges by reference. Factories that rescan
the full token range of each operand in a chain can do more work.
`TestExpressionLongChains` checks move budgets, source reads and recipe
construction counts at two input sizes without using elapsed time.

## Errors and recovery

**Current contract.** `Recover` gives the grammar an error slot and a
resynchronization boundary. An empty boundary list scans to final EOF.
`TestJSONRecoveryOracle`, `TestLineRecoveryOracle` and
`TestStatementRecoveryOracle` check errors and surviving nodes when
input arrives whole or one byte at a time.

```go
type Problem struct {
    Span     tree.Span
    Expected []Pattern
    Found    Look
    Skipped  []Token
}
type Boundary struct {
    At         []Pattern
    Consume    bool
    Goal       *Request
}
func Recover(slot string, body Rule, sync Boundary) Rule
func Insert(expected Pattern, allowed func(Look, View) bool) Rule
```

`Insert` records a legal virtual marker such as an implicit terminator.
It offers a token or final EOF to a pure callback. The callback receives
the current branch state and has no local captures. A success records one
synthetic token of a fixed kind at the look point and leaves the
source mark unchanged. It is nullable and spends no choice lookahead.
Use it only where the language permits insertion. It creates no
diagnostic of its own. See `TestRecoveryProgressAndInsertion`.

A miss records the expected patterns, exact found event and furthest
consumed mark. An exhausted tentative choice selects the failure at the
furthest mark, ordered by byte and virtual position. Ties favor the
first arm. Expectations combine in grammar order at the same complete
replay mark, request and state at the failed offer. See
`TestRecoverySelectsFurthestStream` and `TestBranchStateRecovery`.

Recovery keeps completed siblings outside the failed body and drops the
body's recipes. A successful body forwards its exit key. A repair
resumes with the recovery body's entry key and leaves prior completed
siblings' state in effect. The VM retains the rejected body's consumed
source tokens.
The scan starts at the selected failure mark under the recovery request,
or under the scope's inherited request if `Boundary.Goal` is nil. It
stops at the first matching token or final EOF. When `Consume` is true,
the matching token joins the discarded region. Otherwise the parent
can read it from the same mark under its own request.

`Problem.Skipped` covers the consumed source tokens of the failed body
and tokens discarded by the scan. A rejected grammar insertion is omitted.
`Found` stays at the original offer. The error span covers the discarded
tokens, including leading trivia on the first skipped token, through the
last consumed mark. With no discarded tokens the span is the original
look point. In all cases the span carries the immutable source ID.
A scan suspended by `NeedInput` keeps its mark, diagnostic and skip
progress. The site-and-mark limit on repeated repairs also applies
when the branch key changes. See `Session.recover`,
`TestRecoveryChoiceDefersConstruction`, `TestBranchStateRecovery`
and `TestBranchStateGuardRecovery`.

A tentative `First` arm defers recovery while the choice tries its
remaining arms. When the clean trials miss, the VM replays the first
arm that requested a repair after the enclosing choice commits. That
replay uses the saved mark and pure callbacks. A failure after the
ordinary token budget commits needs no retry at that choice. Factories
run at final EOF for accepted recipes. An abandoned tentative arm
does neither. See
`TestNestedFirstCommitment` and `TestRecoveryChoiceDefersConstruction`.

A repair site can emit one error at its entry mark. If it leaves a
boundary, a repeat visit to that site at the boundary mark propagates a
miss. A point repair can return to the delimiter's owner. `Many` exits
on a point repair. `Sep` can try its separator after a point item.
A grammar can guard an optional item with a boundary check before
calling `Recover`. See `TestRecoveryProgressAndInsertion`.

The start rule describes the file's contents. The engine always
requires final EOF under the grammar's default request. If the start
rule fails or leaves a suffix, the engine keeps completed top-level
outputs and appends an error at slot `"file"`. The fallback scans to
EOF under that default request. `Builders.File` receives ordered roots
and final trivia. See `TestJSONRecoveryOracle`. A consumer can use
`(*parser.ErrorNode).Problem` and `Slot` to read the default error
leaf. A supplied `Builders.Error` may retain the read-only diagnostic
and returns a leaf with `tree.KindError` and the diagnostic span. See
`TestTypedRootConstraint` and `TestRecoveryFatalFailures`.

| Outcome | Caller observes |
| --- | --- |
| Invalid grammar or configuration | `Configure` returns `ErrGrammar`. An undeclared runtime move also fails the session with `ErrGrammar`. |
| Finite malformed input over a conforming source and callbacks within limits | `Done` with a valid error-bearing tree. |
| Temporary exhaustion | `Paused` with no published root. Feed the source and resume the same session. |
| Source, resource or construction failure | Fatal error with its category and available cause. The session still enforces movement, work, depth, virtual token and replay limits during recovery. |

**Current contract.** The chart keeps failed prefixes separate by complete
replay mark, lexical request, branch-state key and observable commitment.
It settles their shared derivations before combining compatible expectations
in grammar order.
Recovery can continue distinct lexical exits at the same furthest progress.
A boundary left to the parent uses the original replay mark for each exit.
See `TestChartRecoveryPreservesEqualCountLexicalMarks` and
`TestChartRecoveryMergesOnlyCompatibleFailureOffers`.

The chart keeps complete clean readings when any survive. If every complete
reading uses repairs, it selects one by fewer repairs, furthest clean progress
at the first repair and grammar order. Pausing a scan keeps its discarded
reading's grammar order. A speculative repair spends no additional
`First` budget, even if a suffix consumes more tokens before rejecting.
See `TestChartRecoveryRanksCompleteReadings`,
`TestFirstRanksTiedRepairsAcrossRecoveryPause`,
`TestFirstDoesNotCommitAfterSpeculativeChartRepair`, and
`TestRepeatedChartRepairUnwindsToFile`.

## Design sketches

The [grammar guide](../writing-a-grammar.md) links to the runnable bytescan
reference grammars. Their tests check complete trees, diagnostics and
evaluation or printing from sealed and streamed input.

These consumer fragments use current calls and proposed extensions.
The JSON and line-format oracles are in `tests/parser_oracles_test.go`.
The expression and statement oracles are in `tests/expressions_test.go`.
Broken-input oracles are in `tests/recovery_oracles_test.go`.
Kinds, names
and slot strings belong to the sample grammars. They use an ordinary
builder that projects captured child output in field order, retains
token text and trivia for leaves and named fields, and constructs the
reserved kinds with the corresponding factory. A builder can construct
its own node types with `tree.NewEnvelope` and ordered `Child` accessors,
or use the common tree nodes where they fit the slot. No grammar depends on a concrete tree representation.

For compactness the examples use a consumer helper for kind matches.

```go
func k(kind string) Rule { return Tok(Kind(TokenKind(kind))) }
func skip(kind string) Rule { return Drop(k(kind)) }

var ordered = ChoiceSpec{Policy: First, Lookahead: 1}
```

### Statement language with building blocks

**Current contract.** `TestExpressionStatementGrammar` exercises this
grammar with a fixture source. The sample has bindings, expression
statements, blocks and a conditional with a braced body and optional
`else` block. The source emits words,
numbers and punctuation. Quoting in examples denotes source text, while
the Go calls below match token kinds.

```go
var statement = Choose(ordered, Ref("let"), Ref("if"), Ref("expr-stmt"))

var rules = map[string]Rule{
    "file": Many(Ref("stmt")),
    "stmt": statement,
    "let": Make("stmt", "let", Seq(
        Drop(Tok(Word("let"))), Bind("name", k("word")), skip("="),
        Bind("value", Ref("expr")), skip(";"),
    )),
    "if": Make("stmt", "if", Seq(
        Drop(Tok(Word("if"))), Bind("test", Ref("expr")),
        Bind("then", Ref("block")),
        Opt(Seq(Drop(Tok(Word("else"))), Bind("else", Ref("block")))),
    )),
    "expr-stmt": Make("stmt", "expr-stmt", Seq(Bind("value", Ref("expr")), skip(";"))),
    "block": Make("block", "block", Seq(
        skip("{"), Many(Ref("stmt")), skip("}"),
    )),
    "expr": Expr("expr", Ref("atom"),
        Operator{Form: Prefix, Match: Kind("-"), Power: 30, Kind: "negate"},
        Operator{Form: Infix, Match: Kind("+"), Power: 10, Assoc: Left, Kind: "add"},
        Operator{Form: Infix, Match: Kind("-"), Power: 10, Assoc: Left, Kind: "subtract"},
        Operator{Form: Infix, Match: Kind("*"), Power: 20, Assoc: Left, Kind: "multiply"},
        Operator{Form: Infix, Match: Kind("^"), Power: 25, Assoc: Right, Kind: "power"},
        Operator{Form: Postfix, Match: Kind("("), Power: 40, Kind: "call",
            Tail: Seq(Sep(Ref("expr"), skip(","), false), skip(")"))},
        Operator{Form: Postfix, Match: Kind("["), Power: 40, Kind: "index",
            Tail: Seq(Ref("expr"), skip("]"))},
        Operator{Form: Postfix, Match: Kind("."), Power: 40, Kind: "member",
            Tail: Make("member", "word", k("word"))},
    ),
    "atom": Choose(ordered,
        Make("expr", "number", k("number")),
        Make("expr", "word", k("word")),
        Make("expr", "group", Seq(skip("("), Ref("expr"), skip(")"))),
    ),
}

var statementGrammar = Grammar{Start: "file", Rules: rules}
```

Each repetition stops at a statement rejection before any token is
consumed. The block's final rule owns the closing brace.

For `let x = -(2 + 3 * 4); if x { f(x)(2); } else { g(0); }`,
the top-level outputs are a binding and a conditional. Its named parts
select a test and two blocks. Multiplication nests inside addition
under the prefix. The second call in the then block wraps the first.

### The same statement language in step style

**Intended capability.** This variant sketches statement control with
hand-written steps and recovery around the same `expr` and `atom`
rules. Small helpers build move declarations for required-token steps
and named calls.

```go
func tokenStep(pattern Pattern, next, bind string) ProgramState {
    return ProgramState{
        Takes: []string{next},
        Step: func(look Look, _ View) Move {
            if Matches(look, pattern) {
                return Take(next, bind)
            }
            return Reject(pattern)
        },
    }
}
func callStep(rule, resume, bind string) ProgramState {
    return ProgramState{
        Calls: []string{rule}, ReturnsTo: []string{resume},
        Control: func(View) Move { return Call(rule, resume, bind) },
    }
}
var done = ProgramState{Control: func(View) Move { return Return() }}

var stmtBoundary = Boundary{
    At: []Pattern{Kind(";"), Kind("}")},
}

var statementSteps = Extend(statementGrammar, "file", map[string]Rule{
    "stmt": Program("dispatch", map[string]ProgramState{
        "dispatch": {
            Calls: []string{"let", "if", "expr-stmt"}, ReturnsTo: []string{"done"},
            Step: func(look Look, _ View) Move {
                switch {
                case Matches(look, Word("let")):
                    return Call("let", "done", "")
                case Matches(look, Word("if")):
                    return Call("if", "done", "")
                default:
                    return Call("expr-stmt", "done", "")
                }
            },
        },
        "done": done,
    }),
    "let": Make("stmt", "let", Program("keyword", map[string]ProgramState{
        "keyword": tokenStep(Word("let"), "name", ""),
        "name":    tokenStep(Kind("word"), "equals", "name"),
        "equals":  tokenStep(Kind("="), "value", ""),
        "value":   callStep("expr", "end", "value"),
        "end":     tokenStep(Kind(";"), "done", ""),
        "done":    done,
    })),
    "if": Make("stmt", "if", Program("keyword", map[string]ProgramState{
        "keyword": tokenStep(Word("if"), "test", ""),
        "test":    callStep("expr", "then", "test"),
        "then":    callStep("block", "maybe-else", "then"),
        "maybe-else": {
            Takes: []string{"else"},
            Step: func(look Look, _ View) Move {
                if Matches(look, Word("else")) {
                    return Take("else", "")
                }
                return Return()
            },
        },
        "else": callStep("block", "done", "else"),
        "done": done,
    })),
    "expr-stmt": Make("stmt", "expr-stmt", Program("value", map[string]ProgramState{
        "value": callStep("expr", "end", "value"),
        "end":   tokenStep(Kind(";"), "done", ""),
        "done":  done,
    })),
    "block": Make("block", "block", Program("open", map[string]ProgramState{
        "open": tokenStep(Kind("{"), "loop", ""),
        "loop": {
            Takes: []string{"done"},
            Calls: []string{"block-item"}, ReturnsTo: []string{"after-item"},
            Step: func(look Look, _ View) Move {
                if Matches(look, Kind("}")) {
                    return Take("done", "")
                }
                if look.End {
                    return Reject(Kind("}"))
                }
                return Call("block-item", "after-item", "")
            },
        },
        "after-item": {
            Takes: []string{"loop"}, Jumps: []string{"loop"},
            Step: func(look Look, _ View) Move {
                if Matches(look, Kind(";")) {
                    return Take("loop", "")
                }
                return Jump("loop")
            },
        },
        "done": done,
    })),
    "block-item": Recover("stmt", Ref("stmt"), stmtBoundary),
    "file": Program("loop", map[string]ProgramState{
        "loop": {
            Calls: []string{"file-item"}, ReturnsTo: []string{"loop"},
            Step: func(look Look, _ View) Move {
                if look.End {
                    return Return()
                }
                return Call("file-item", "loop", "")
            },
        },
    }),
    "file-item": Recover("stmt", Ref("stmt"), Boundary{
        At: []Pattern{Kind(";")}, Consume: true,
    }),
})
```

An empty binding on `Take` keeps the token only in the ledger.
On `Call` an empty binding forwards its normal fields and ordered
outputs. Thus `let`, `if` and block assembly supply the same
structural fields and owned-token ranges as the combined grammar.
The hand version makes branch direction and resume labels easy to see.
For repeated nesting and named results, the combinators are easier to
read and audit. The combinator version puts the source order near the named
fields and removes the duplicate move declarations. A custom step is
useful where a local dispatch would otherwise require several probes.

### JSON, including a handmade source

```go
var jsonGrammar = Grammar{Start: "value", Rules: map[string]Rule{
    "value": Choose(ordered,
        Make("json", "object", Seq(skip("{"), Sep(Ref("pair"), skip(","), false), skip("}"))),
        Make("json", "array", Seq(skip("["), Sep(Ref("value"), skip(","), false), skip("]"))),
        Make("json", "string", k("string")),
        Make("json", "number", k("number")),
        Make("json", "bool", Tok(Word("true"))),
        Make("json", "bool", Tok(Word("false"))),
        Make("json", "null", Tok(Word("null"))),
    ),
    "pair": Make("pair", "pair", Seq(
        Bind("key", k("string")), skip(":"), Bind("value", Ref("value")),
    )),
}}
```

The source owns JSON string and number token boundaries and exposes
invalid sequences as input errors. This handmade fixture reads the
tokens for `[1]` with no lexer dependency. Its byte mark is also its
array index because each fixture token spans one byte. All requests
used by this instance give the same tokens.

```go
type arrayFixture struct{}

func (arrayFixture) Begin() (tree.SourceID, Mark) { return "json-fixture", Mark{} }
func (arrayFixture) DiscardBefore(int) {}

func (arrayFixture) Read(mark Mark, _ Request) (Read, error) {
    if mark.Byte == 3 {
        return Read{Status: EndOfInput, Next: mark}, nil
    }
    kinds := [...]TokenKind{"[", "number", "]"}
    next := Mark{Byte: mark.Byte + 1}
    return Read{
        Status: TokenReady,
        Token: Token{
            Kind: kinds[mark.Byte], Text: "[1]"[mark.Byte:next.Byte],
            Span: tree.Span{Source: "json-fixture", Start: mark.Byte, End: next.Byte},
        },
        Next: next,
    }, nil
}

func parseFixture[N tree.Node](build Builders[N]) (N, Status, error) {
    engine, err := Configure(jsonGrammar, build, Limits{})
    if err != nil {
        var root N
        return root, Done, err
    }
    return engine.Start(arrayFixture{}, "").Run()
}
```

The result is a file with one array and one number child. For a stream
fixture, withhold the final bracket and return `NeedInput` at its
mark. After feeding it, the same session must return the same tree
shape, source positions and payloads as the whole input parse.
Fresh IDs can differ between sessions.

### Line format

The sketch uses `Recover` around the line grammar. `TestLineRecoveryOracle`
checks surviving entries and the skipped region through a newline.

```go
var lineEnd = Choose(ordered, skip("newline"), Tok(EndOfFile()))

var lineGrammar = Grammar{
    Start: "file",
    Default: Request{Layout: Lines},
    Rules: map[string]Rule{
        "file": Many(Seq(Not(EndOfFile()), Recover("line", Ref("line"), Boundary{
            At: []Pattern{Kind("newline")}, Consume: true,
        }))),
        "line": Choose(ordered,
            Make("line", "section", Seq(skip("["), Bind("name", k("word")), skip("]"), lineEnd)),
            Make("line", "entry", Seq(
                Bind("key", k("word")), skip("="),
                Bind("value", Lex(Request{Goal: "line-value", Layout: Lines}, k("value"))),
                lineEnd,
            )),
            skip("newline"),
        ),
    },
}
```

For `[user]\nname = Ada # display name\n`, the source emits logical
newlines and uses the `line-value` goal to delimit the raw value.
The trailing comment belongs to the gap before the next line boundary.
The source retains that range in trivia even though the separator is
dropped from structural output. The same `lineEnd` accepts a final
entry without a newline. Each successful iteration consumes at least
the start of its line.

### Consumer resolution

`TestAmbiguousDeclarationResolution` checks both choices before
construction and again over the completed ambiguity node.
`TestDanglingElseKeepsBothFullReadings` checks the two complete
attachments and their shared command subtree.

```go
func declarations(names func(State, string) bool) Rule {
    return Choose(ChoiceSpec{
        Policy: All, Slot: "stmt",
        Filter: func(c Candidate) bool {
            name := c.Parts.Tokens("head")[0].Text
            isType := names(c.Before, name)
            return (c.Kind == "declaration") == isType
        },
    },
        Make("stmt", "declaration", Seq(
            Bind("head", k("word")), skip("*"), Bind("name", k("word")), skip(";"),
        )),
        Make("stmt", "multiply-stmt", Seq(
            Bind("head", k("word")), skip("*"), Bind("right", k("word")), skip(";"),
        )),
    )
}
```

The two recipes for `T * x;` cover the entire statement. The example's
consumer knows whether `T` is a type. It rejects the other reading
before either factory runs. With an unknown `T`, omit or relax the
filter and keep both. The consumer can later use its name environment
to choose by child kind in an `eval.Fold` handler for
`tree.KindAmbiguity`, or rebuild that occurrence with the selected
child. No parser global state changes.

```go
var elseGrammar = Grammar{Start: "stmt", Rules: map[string]Rule{
    "stmt": Choose(ChoiceSpec{Policy: All, Slot: "stmt"},
        Make("stmt", "if", Seq(
            Drop(Tok(Word("if"))), Bind("test", k("word")), Bind("then", Ref("stmt")),
        )),
        Make("stmt", "if-else", Seq(
            Drop(Tok(Word("if"))), Bind("test", k("word")), Bind("then", Ref("stmt")),
            Drop(Tok(Word("else"))), Bind("else", Ref("stmt")),
        )),
        Make("stmt", "command", Seq(k("word"), skip(";"))),
    ),
}}
```

For `if a if b p; else q;`, the inner call can return at `p;` or
after `q;`. The outer production that binds `else` needs the shorter
inner exit. Both complete readings survive and share command recipes.
A consumer whose language attaches to the nearest `if` chooses the
reading whose outer node has kind `if` and then-child has kind
`if-else`. The consumer's typed field or named accessor supplies the
then-child, with the same ordered projection through `Child`.
With a retained whole-file ambiguity, the handler first descends
through each alternative file root. A different consumer can choose
the outer attachment using the same tree.

### S-expressions

```go
var sexpr = Grammar{Start: "file", Rules: map[string]Rule{
    "file": Many(Ref("item")),
    "item": Choose(ordered,
        Make("sexpr", "symbol", k("word")),
        Make("sexpr", "list", Seq(skip("("), Many(Ref("item")), skip(")"))),
    ),
}}
```

For `(a (b))`, the engine needs only word matches, delimiters and
the call stack. General grammars additionally need precedence and
left chains (`Expr`), separators and terminators (`Sep`, layout
tokens and `Insert`), contextual words (`Word`), named parts (`Bind`), trivia (`Token`),
scoped lexical requests (`Lex`, `WithLex`), statement resynchronization (`Recover`),
and indentation (the `Indentation` request). The
[reference grammars](../writing-a-grammar.md#more-reference-grammars)
exercise these facilities. The [foundation audit](foundation.md) links
the tests for computed requests and the paper helper compositions.

## Six-language paper check

**Design record.** These are the accepted paper traces. They combine
current primitives and intended helpers. The
[as-built foundation audit](foundation.md) identifies the implemented
evidence and which traces still depend on planned API. The traces make
no language compatibility claim.

These are grammar obligations checked on paper. The child numbers identify
the implementation rows in the v0.1 milestone. **5** supplies the common
tree, **7** the core production and token-source API, **8** the expression
operators, **9** recovery and legal terminator insertion, **10** generalized
choice and filters, and **11** the bytescan adapter or its consumer-hook
contract. Child **12** exercises the underlying capabilities in reference
grammars. Language-specific rules and compatibility tests belong to later
consumer work. Descriptions of real-language behavior below are derived
from the linked specifications and reference implementations.

| Language and construct | Proposed API and trace | Building children |
| --- | --- | --- |
| Go — inserted semicolons | `Request{Goal: "go", Layout: Lines}` selects a source policy that tests the last significant token at physical newline or EOF. It emits a zero-width semicolon before advancing to the next significant token. `Tok(Kind(";"))` takes explicit and inserted semicolons. `Opt(Tok(Kind(";")))` applies only where the grammar immediately requires the permitted `)` or `}` closer. | **7**, **11** |
| Go — `if x == T{}` and typed composite literals | Use a distinct `Ref("control-expr")` whose `Expr` atom excludes an unparenthesized named-type composite literal at the control-clause level. It parses `x == T` and leaves `{` to `Ref("block")`. A parenthesized atom calls `Ref("expr")`, which includes the literal arm, as in `if x == (T{}) {}`. Use `Recover` when the enclosing statement cannot be completed. | **7**, **8**, **9** |
| Go — indexing, slicing and type arguments | Use `Expr` with a postfix `Operator` for `[`, with `Tail` choosing the slice, index or type-argument form up through `]`. A neutral one-expression bracket form can leave the index-versus-instantiation question to semantic analysis, or a consumer can use `Choose` with `All` to keep distinct full-span bracket recipes and filter with type information. Bind the bracket result under the enclosing suffix. | **7**, **8**, **10** |
| Python — indentation, blank lines and bracket suppression | A file uses `Request{Layout: Indentation}`. `Seq(Tok(Kind("newline")), Tok(Kind("indent")), Many(Ref("stmt")), Tok(Kind("dedent")))` owns suite delimiters. Bracketed productions apply `Lex(Request{Layout: Joined}, Ref("bracket-contents"))` through their closer. The source restores logical layout from its saved indent stack, handles explicit continuation and blank lines, and emits `Invalid` for invalid dedent. `Recover` can stop at a dedent and leave it for the suite. | **7**, **9**, **11** |
| Python — soft keywords and pattern matching | `Tok(Word("match"))` and `Tok(Word("case"))` select keyword uses at suite sites while `Tok(Kind("word"))` still admits identifiers elsewhere. Distinguish an assignment such as `match = 1` from a compound header with `Choose` over full header productions and a deliberate commitment rule. With long shared prefixes use `All` and let the colon and suite constrain the branch. Pattern guards and alternation can use ordinary `Seq` and `Expr` over a separate pattern rule. | **7**, **8**, **10** |
| Python — nested f-string replacements | A source goal for f-string text recognizes literal chunks and replacement openers, including escaped braces. `Lex` changes to expression input within a replacement. Named calls handle nested f-strings, bracketed expressions and format-specifier fields. The format-tail request resumes only at the enclosing replacement boundary. `WithLex` can pass the quote and delimiter profile from the opener; saved source mode and parser frames survive `NeedInput`. | **7**, **8**, **11** |
| JavaScript — regular expression versus division | An atom uses `Lex(Request{Goal: "regexp"}, Tok(Kind("regexp")))` when the grammar expects an expression start. A division `Operator` at the continuation site has a `Goal` for `division`. The engine reads the same mark again if the goal changes. Slash recognition, character classes and escape rules belong to the lexer. | **7**, **8**, **11** |
| JavaScript — template strings with nested expressions | Scope `Lex` to read a template head in expression position. Call the expression grammar for the substitution. The enclosing template frame requests the template-tail goal when that expression returns, so a top-level substitution `}` can become part of the middle or tail token. Nested object braces stay with the expression grammar, and a nested template gets its own frame. Repeat only a middle followed by a substitution. | **7**, **8**, **11** |
| JavaScript — automatic semicolon insertion and restricted productions | Use a continuation grammar that consumes a legal continuation before considering insertion, as with `a\n(b)` as a call. At a grammar-owned terminator site use `Choose` with `First` over explicit `Tok(Kind(";"))` and `Insert(Kind(";"), allowed)`, where the consumer checks the rejected continuation's look event, `BreakBefore`, `}` or EOF. `SameLine` guards a return operand. A postfix update operator uses `OnSameLine(Kind("++"))` or `OnSameLine(Kind("--"))` so the line restriction is checked before consuming the operator. Separate productions cover the do-while terminator and exclude forbidden insertion in empty statements and `for` headers. The next statement can replay under its own regexp goal. | **7**, **8**, **9**, **11** |
| TypeScript — `a < b > (c)` | `Extend` the JavaScript grammar with an expression choice. One `All` arm uses a generic-call production with `Lex` on type arguments and requires the call suffix. The other uses relational infix `Operator` values. Each arm returns a full-span expression. Apply the consumer's TypeScript parse rule at an admissible follow token or use `ChoiceSpec.Filter` on the completed candidate. The reference syntax can select a single interpretation even when the composed grammar retains both for analysis. | **7**, **8**, **10**, **11** |
| TypeScript — arrow functions versus parentheses | In the derived assignment grammar use `Choose` with `All` for a parenthesized expression versus a parameter list followed by `SameLine()`, `Tok(Kind("=>"))` and a body. The `=>` suffix can reject the expression arm at the enclosing continuation. An annotation on a parameter belongs only to the arrow recipe. A TSX profile can supply a separate JSX goal and require its source-specific generic-arrow discriminator in the generic arm. | **7**, **8**, **10**, **11** |
| TypeScript — lexical steering with simultaneous readings | Keep both arms over `f<A<B>>(x)` while comparing the extended grammar with its JavaScript base. At the same source byte the generic arm's `Lex(Request{Goal: "type"}, Ref("type-arguments"))` reads two closing `>` tokens. The other arm's shift `Operator.Goal` reads one `>>` token. `Choose` with `All` continues both to the same physical end, covering a generic call and the relational expression `(f < A) < (B >> (x))`. The requests and checkpoint identities prevent premature merging. A TypeScript compatibility consumer uses its completed candidate or follow-context filter to retain the compiler's reading. | **7**, **8**, **10**, **11** |
| SQL — non-reserved words | A PostgreSQL-profile lexer yields unquoted word tokens. `Tok(WordAs("filter", fold))` tests the word in the aggregate suffix while `filter` can be a column name. A name rule uses `Tok(Kind("word"))` with `Not` or `Guard` for the dialect's forbidden words in that exact name position. Quoted identifiers have their own kind. Full clause arms allow lookahead through a distinguishing suffix when the first word can name a column. | **7**, **11** |
| SQL — case folding and CASE expressions | `WordAs` uses the dialect's normalization, retains original text, and never folds a quoted identifier through the unquoted word matcher. A `Make` with named `Bind` fields reads optional test, `Many` of `WHEN` arms, optional `ELSE`, then `END`. A nested arm can `Ref("expr")` and reuse the dialect's operator table. | **7**, **8** |
| SQL — dialects and quoted token forms | `Extend` replaces the query, clause or atom rule for a dialect while preserving shared expression and statement rules. Ship it with a matching source profile. A PostgreSQL string atom can issue `Lex` or `WithLex` for a dollar-quoted body with a captured delimiter. The body hook keeps the actual delimiter in the checkpoint and consumes internal punctuation as body text. A different dialect can leave that production absent. | **7**, **11** |
| Ruby — queued heredocs, indentation and interpolation | The consumer binds opener data on the command header and uses `Update` to add the delimiter, quoting and indentation policy to immutable queue state. After the header newline, `WithLex` passes the first pending delimiter and body profile. The next body rule handles chunks, interpolation by named call, and terminator. A body can advance to the next queued delimiter. Checkpoints include any source-owned pending delimiter or layout data. Bind header references and ordered bodies on the command or enclosing expression reduction, then let `Builders.Node` link them by ordinal. Each read advances physically or emits a finite virtual marker. Replay does not move the live cursor back over the command header. | **7**, **11** |
| Ruby — local name versus receiverless call | Carry immutable lexical scope in `State`. A chosen assignment production uses `Update` to record its local at the language's declaration point, and `Scope` restores the outer state at scope exit. A `Guard` on a bare name or an `All` filter consults the state visible at the occurrence. A later assignment cannot retroactively affect a branch's earlier occurrence. Consumers supply Ruby's exact scope and declaration rules. | **7**, **10** |
| Ruby — command calls and block binding | Use separate named command, argument and block rules. In `outer inner { ... }` and `outer inner do ... end`, the braces attach to the inner call and `do ... end` attaches to the outer call. `Choose` with `All` can first preserve attachment alternatives in a permissive grammar. A `Guard` or completed-candidate filter encoded by the consumer enforces the brace versus `do` attachment restriction. `SameLine` and a scoped lexical goal can distinguish applicable command-argument contexts. | **7**, **8**, **10**, **11** |

Language references by row group.

- Go language specification. [Semicolons][go-semi], [composite literals][go-lit],
  [index expressions][go-index] and [instantiations][go-inst].
- Python 3.14 language reference. [Lexical analysis][python].
- ECMAScript 2025. [Lexical grammar and semicolon insertion][ecma] and
  [restricted productions for return][ecma-return].
- TypeScript 5.9.3. [Parser source][ts-parser] and [scanner source][ts-scanner].
  Compare `parseTypeArgumentsInExpression`, `isParenthesizedArrowFunctionExpression`
  and `reScanGreaterToken`. The shifted and split readings in the paper
  check are a union grammar exercise.
- PostgreSQL 18. [Keyword categories][sql-keywords] and
  [lexical structure][sql-lex].
- Ruby 3.4. [Literals and heredocs][ruby-lit], [local assignment][ruby-assign]
  and [calling methods][ruby-call].

[go-semi]: https://go.dev/ref/spec#Semicolons
[go-lit]: https://go.dev/ref/spec#Composite_literals
[go-index]: https://go.dev/ref/spec#Index_expressions
[go-inst]: https://go.dev/ref/spec#Instantiations
[python]: https://docs.python.org/3.14/reference/lexical_analysis.html
[ecma]: https://tc39.es/ecma262/2025/multipage/ecmascript-language-lexical-grammar.html
[ecma-return]: https://tc39.es/ecma262/2025/multipage/ecmascript-language-statements-and-declarations.html#sec-return-statement
[ts-parser]: https://github.com/microsoft/TypeScript/blob/v5.9.3/src/compiler/parser.ts
[ts-scanner]: https://github.com/microsoft/TypeScript/blob/v5.9.3/src/compiler/scanner.ts
[sql-keywords]: https://www.postgresql.org/docs/18/sql-keywords-appendix.html
[sql-lex]: https://www.postgresql.org/docs/18/sql-syntax-lexical.html
[ruby-lit]: https://docs.ruby-lang.org/en/3.4/syntax/literals_rdoc.html
[ruby-assign]: https://docs.ruby-lang.org/en/3.4/syntax/assignment_rdoc.html
[ruby-call]: https://docs.ruby-lang.org/en/3.4/syntax/calling_methods_rdoc.html

### Compatibility checks

Pin the language version and comparison projection. For Go a consumer
can pass the same valid fixture to its configured engine and to
`go/parser.ParseFile`, normalize optional wrappers and compare kinds,
named parts, child order and byte offsets through `go/token.File`.
Compare inserted-token behavior with the reference scanner separately.
For invalid fixtures compare the diagnostic location, presence of error
regions and surviving valid constructs. Recovery tree shapes are
consumer policy. A compatibility claim for TypeScript compares the
filtered reading with the compiler parser, while a separate union
fixture asserts that two readings and two lexical requests were
actually retained. Similar fixtures can compare a Python tokenizer's
layout stream, a Ruby reference parser's local-versus-call distinction,
and dialect-specific SQL fixtures. Tests should cover identical trees
over the handmade source and bytescan adapter, chunk splits inside
tokens and layout transitions, same-byte different-goal replay, and
malformed inputs with valid statements between errors. Compare identity
sharing within each result, since independently parsed IDs differ.

## Owner decisions

The table preserves the original parser design alternatives and
recommendations. For current behavior and separately recorded owner
decisions, see the [API inventory](../api.md#parser) and
[foundation audit](foundation.md).

| Decision | Options and consequences | Recommendation |
| --- | --- | --- |
| Package, exported symbols and reserved strings | A. Separate `parser`, `tree` and adapter package, using the illustrative spellings here. B. Rename or move before APIs are implemented. A common package reduces import names but risks coupling handmade trees to parsing. Choices include the grammar start/configure names, source events and parser policy names, `file` slot and recovery terminology. | A. Review the exported names with the corresponding implementation. |
| Lexer checkpoint form | A. Use immutable replay marks and requests. Implement sources with restartable cursors, hooks or buffering. B. Expose mutable `Save`/`Restore` handles with explicit release, placing replay scheduling and ownership in the engine. C. Offer monotone sources alone and restrict ambiguity to one token stream. | A. It expresses conflicting lexical requests and preserves a fixed-stream wrapper for simpler consumers. C leaves the simultaneous-steering paper case uncovered. |
| Layout placement | A. Keep lexical layout policy in a replayable source, with grammar scopes and a physical break signal. B. Move indentation and statement insertion policies into the engine. | A. Keep `Insert` for grammar-specific legal omissions and source insertion for lexical policies such as Go's. Python suites and JavaScript restricted productions need layout in the first foundation. |
| Choice and commitment | A. Expose all three policies with local choice scope, declaration order and an explicit prefix cap for `First`. B. Require explicit cut for early commitment and make ordered choice retry longer. C. Use one generalized strategy for every choice. | A. This retains a separate deterministic path and makes the retry boundary visible. B gives more backtracking control and can retain source for longer. C increases the core path's cost and complexity. |
| Recursion and state | A. Reject structural zero-yield cycles, provide operator loops, and allow productive chart recursion in declarative `All` components. Require honest state keys and bounded hand steps. B. Limit all recursion to consuming prefixes plus operator loops. C. Accept arbitrary zero-progress recursion under a global cap. | A. B still handles deterministic expression chains but needs an alternate production normal form for the ambiguous binary oracle. C can reject valid finite input merely because an infinite family was explored first. |
| Filtering and effects | A. Pure parse-time filters over branch-local summaries, with full-tree selection after publication where needed. B. Only resolve after building the forest. C. Invoke arbitrary user actions with external transactions. | A. It supports context that determines future syntax, without requiring irreversible side effects to roll back. Keep the consumer's option to inspect a retained forest. |
| Published ambiguity and typed factories | A. Use ordered reserved nodes and slot-specific builder callbacks. Allow explicit grouping nodes in a highly ambiguous structural grammar. B. Expose a separate packed-forest API the consumer converts to trees. C. Enumerate all roots in the public tree. | A. It preserves the common tree contract and supports narrow slots. B would need another API and traversal model. C can grow exponentially even with cubic recognition. |
| Recovery fallback | A. Make structure-aware recovery explicit, with a deterministic file fallback and separate fatal errors. B. Return partial roots plus fatal errors or stop on the first syntax error. | A. It preserves a tree for finite malformed inputs within source and resource contracts and identifies explicit recovery ownership. |

**Merge gate.** The paper check proposes a path for every required
construct and section. It depends on owner approval of the choices
above and on the implementation children delivering their named
capabilities. If the owner selects an alternative that leaves a row
or required section unsupported, record the gap and its replacement
plan here. The owner must explicitly accept each such scope gap or
the design must cover it before merging. Language implementations,
lossless write-back, generated parsers and incremental reparse remain
outside this milestone.
