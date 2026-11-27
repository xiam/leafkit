# Tree

## Scope

**Design principle.** A consumer can build a tree by hand, inspect it, and
interpret it. A node has a kind, consumer data, and ordered children.
Leafkit reads a common envelope containing the kind, source span and node
identity, together with an ordered child accessor. Only consumer code
interprets the data. The tree has no lexer or parser dependency. A parser
using any token source can produce the same tree as a hand builder.

**Current contract.** A rooted tree can retain alternative readings of the
same text. Alternatives can share nodes. `NewAmbiguity` keeps the alternatives
in order, and `TestAmbiguityExample` resolves them during evaluation without
changing the original tree.

**Scope limits.** This specification covers the tree and evaluation.
The [parser contract](parser.md) covers grammar and recovery policies.
Lossless trees, incremental reparse, and an overlay for reference and
control-flow edges remain intended capabilities. `Node` exposes structural
children. The module requires Go 1.23 as declared in [go.mod](../../go.mod).

## Evidence from existing consumers

- **symbolic v0.1.0.** `ast.Node` exposes a span and an ordered child
  iterator. Its interface is sealed. `Int` and `Float` store `int64` and
  `float64`. `String`, `Symbol` and `Atom` represent literal and name
  forms. `Expr`, `List` and `Map` model the three bracket forms. `Map.Items`
  is an alternating list of keys and values and can have an unmatched last
  key. `File.Items` holds the top-level forms. Leafkit takes the envelope and
  ordered traversal pattern. Consumers choose the syntax shapes, scalar
  types and file root kind. Leafkit defines its own span contract.
  [symbolic v0.1.0, ast/node.go](https://github.com/xiam/symbolic/blob/v0.1.0/ast/node.go)
- **symbolic v0.1.0.** `ast.Walk` visits the root before its children.
  `WalkPath` passes the ancestors in a borrowed slice, and `All[T]` selects
  by dynamic type. Leafkit adds child indices to the path to distinguish two
  occurrences reached through the same ancestors. `Fprint` writes an
  S-expression with normalized spacing and omits source comments. Leafkit
  keeps span and payload separate so a consumer can recover source bytes
  independently of its value printer.
  [symbolic v0.1.0, ast/walk.go](https://github.com/xiam/symbolic/blob/v0.1.0/ast/walk.go),
  [symbolic v0.1.0, ast/print.go](https://github.com/xiam/symbolic/blob/v0.1.0/ast/print.go)

The evaluator requirements below use fnlang at commit
`297310ba1f9a7b6269d660c2d348eba91d54573f`.

| fnlang function and behavior | Need in leafkit |
| --- | --- |
| `Eval` creates a context and collects outputs. `evalContext` distinguishes values, lists, maps and expressions. `evalContextList` stops on the first returned error. | An entry point with a consumer-chosen result and one handler per kind. Handlers choose which children to visit and where to return errors. |
| `prepareFunc` defers applying the head value. `execExpr` dispatches on the value type and resolves names. `derefFunc` requires one result. `execFunc` supplies arguments while the context accepts them. `mapElement` and `mapListItem` interpret values as maps and lists. | On-demand child evaluation, typed results, and lexical name lookup. Callable values, argument passing, channels and collection lookup remain consumer policy. |
| `Defn` installs a function binding. `Context.Get` searches for a name, and `Set` writes to the context's symbol table. `New` creates a table with parent access and `NewClosure` shares the parent's table when one exists. `ExecArgument` resolves a symbol or executes a function when an argument is forced. | Pass a name environment explicitly. Let the consumer choose when to create or share scope and when to force an expression. Keep name lookup distinct from node annotations. |
| `RuntimeError` examines token position and encodes `n.Parent()` for context. | Carry the occurrence path and source span to report the site reached during evaluation. |

[fnlang eval.go](https://github.com/xiam/fnlang/blob/297310ba1f9a7b6269d660c2d348eba91d54573f/eval.go),
[fnlang context/context.go](https://github.com/xiam/fnlang/blob/297310ba1f9a7b6269d660c2d348eba91d54573f/context/context.go)

**Document form.** This page separates design principles, intended
capabilities, and open design questions, following bytescan's specification
status convention. A claim about existing behavior names the source.
[bytescan v0.7.0, docs/specs/core-contract.md](https://github.com/xiam/bytescan/blob/v0.7.0/docs/specs/core-contract.md#status-convention),
[bytescan v0.7.0, docs/writing.md](https://github.com/xiam/bytescan/blob/v0.7.0/docs/writing.md)

## Envelope and construction

**Current contract.** The package is `github.com/xiam/leafkit/tree`. Its
open interface lets a consumer define pointer types with typed payloads
and named children. The library reads only metadata and ordered child
references. See `Shape`, `Node` and `TestHandBuiltGrammarShapes`.

```go
type Shape[N any] interface {
    Info() Envelope
    Child(index int) (N, bool)
}
type Node interface { Shape[Node] }

type Kind string
type ID struct { serial uint64 }
type SourceID string
type Span struct {
    Source SourceID
    Start, End int
}
type Envelope struct {
    Kind Kind
    Span Span
    ID   ID
}
```

- **Kind.** Consumers can declare string constants of type `Kind`.
  The empty kind is invalid. The prefix `leafkit:` is reserved.
  `KindError` and `KindAmbiguity` are the recognized reserved kinds.
  See `Kind` and `validateEnvelope`.
- **Identity.** `NewEnvelope` assigns a nonzero ID unique within the
  process. Each new node needs a fresh envelope. Two child slots can
  hold the same pointer and observe the same ID. Two independently
  constructed nodes get different IDs even when their contents match.
  See `TestValidate` and `TestRebuildSharesUnchangedNodes`.
- **Children.** `Child` returns contiguous slots starting at zero.
  A negative or past-end index returns `nil, false`. A leaf has no
  children. Consumer fields project one stable child order. Optional
  absent fields contribute no edge. A required missing input part can
  be represented by an error node. See `Node`, `TestChildLists`
  and `TestFprint`.
- **Construction.** Build children before parents. `NewContainer`
  and `NewAmbiguity` copy the incoming list of child references.
  `NewValue[T]` and `NewError` build leaves. `NewValue[T]` stores its
  payload according to Go assignment rules. Use pointers for large
  structs, arrays, and arbitrary-precision values. A slice descriptor
  shares its backing array. Small scalars can stay inline. Retain a
  concrete node handle or use typed consumer fields to read payloads.
  See `TestChildLists`, `TestPayloadReferences` and `Example`.
- **Publication and edits.** Treat published nodes, their child
  structure, and all reachable payload data as read-only. Every
  consumer alias must respect that contract. To edit, rebuild changed
  paths and return a new root. Reuse unchanged objects where their
  meaning and source binding still apply. Reused objects retain their
  ID and snapshot. Replacements get fresh IDs. Matching nodes across
  reparses and projecting old coordinates onto new snapshots are
  future work. See `Node` and `TestRebuildSharesUnchangedNodes`.
- **Validation.** `Validate` checks the reachable structure for nil
  or non-pointer nodes, invalid metadata, copied IDs on distinct
  pointers, cycles, and malformed ambiguity. A node already on the
  current ancestor chain indicates a cycle. A completely checked
  node can be shared. Validation assumes finite, stable child
  accessors. It examines each distinct node's outgoing edges once.
  See `TestValidate` and `Validate`.

## Spans and source lookup

**Current contract.** A span holds half-open byte offsets into an
immutable source snapshot. The zero `Span{}` is unknown or source-free.
A known span has a nonempty source ID and `0 <= Start <= End`. The
known point at byte zero is distinct from the unknown span. A source
revision gets a new snapshot ID. See `Span`, `SourceLookup` and
`TestPositions`.

The consumer keeps the original bytes outside the tree. It can slice
those bytes by the offsets after checking bounds. `Positions` accepts
a `SourceLookup` callback, checks the bounds against the snapshot, and
returns one-based lines and byte columns for both endpoints. LF ends a
line. Column counting advances by byte even for invalid UTF-8. A point
can be at end of input. Unknown spans, unavailable snapshots, and
invalid bounds return errors. See `TestPositions`.

## Walk and occurrences

**Current contract.** Walks take a valid root. `Walk` visits the root
first and descends depth first in child order. `WalkPath` also yields
the edges taken from the root. Each `Step` records its parent and
zero-based child index. The root has an empty path. A path is borrowed
for the current iteration. To retain it, copy the slice.

Two paths can reach the same node, even through two slots of the same
parent. Each occurrence appears in `Walk` and `WalkPath`. In a shared
structure, the number of occurrences can grow exponentially with
depth. `WalkUnique` is an opt-in traversal for operations keyed by
node ID. It yields the first encounter in the same order and skips
descent at later encounters. Its seen set belongs to one iteration.
`All[T]` selects occurrences by Go type. Breaking out of any of these
iterators stops it immediately. See `TestSharedWalks` and
`TestWalkEarlyExit`.

`Fprint` writes a structural view of a valid root. It shows kind, ID
and span, and identifies each edge by parent ID and child index. Later
edges to an already printed node show a reference. The printer leaves
payload interpretation to the consumer and returns writer errors.
See `TestFprint`.

## Fold, scope and annotations

**Current contract.** The package is `github.com/xiam/leafkit/eval`. It
uses the standard library and the tree package. The result type `R` and
binding type `V` are independent. Rules accept `tree.Node` and can read
typed consumer nodes through that interface. The parent in each path
step is the original node. See `Fold` and `TestFoldPathAndPosition`.

```go
type Frame struct {
    Node tree.Node
    Path tree.Path
}

type Descend[R, V any] func(index int, env *Env[V]) (R, error)
type Rule[R, V any] func(
    frame Frame, env *Env[V], child Descend[R, V],
) (R, error)

func Fold[R, V any](
    root tree.Node, env *Env[V], rules map[tree.Kind]Rule[R, V],
) (R, error)
```

`Fold` takes a valid root and dispatches by `Info().Kind`. A rule
calls `child(index, env)` synchronously to evaluate a child with that
environment. Calls can skip, reorder or repeat children. Each call
appends an edge to the path and evaluates that occurrence afresh.
Keep the frame path read-only and copy it before retaining it. Use
`child` only during its rule invocation. See `Fold`,
`TestConditionalAndShortCircuit`, and `TestFoldPathAndPosition`.

`NewEnv[V](nil)` and the zero value of `Env[V]` create empty scopes.
`NewEnv(parent)` creates a child scope. `Set` writes to the current
scope. `Get` returns the nearest binding or the zero value and false
on a miss. A nil scope can be passed through the fold and looked up
as empty. A rule that writes bindings needs a non-nil scope. The
consumer decides when to create or share a scope. A closure can
capture its own scope and retain a body node for a later fold. See
`TestEnv` and `TestLexicalLet`.

`Table[V]` is an optional map keyed by `tree.ID`. Two paths to a
shared node use the same entry, even when evaluation gives different
results. For occurrence annotations, a consumer can key a separate
table by the root ID and a comparable encoding of the ordered child
indices. For example, slash-separated indices distinguish `0/0/1`
and `1/1/0`. The root uses the empty sequence. Occurrence entries are
scoped to a root and need rebuilding or remapping after edits. See
`Table` and `TestLexicalLet`.

`Fold` reports an absent rule or invalid child index as an `*eval.Error`
at the node where dispatch or descent failed. An ordinary error returned
by a rule gets the same location there. A returned error that already
wraps or contains an `*eval.Error` keeps its cause and location. Use
`errors.As` to retrieve the node and copied path. For a known span,
the message includes the source ID and byte range. Use `tree.Positions`
with the node span and a source lookup for line and column positions.
Error and ambiguity kinds need an explicit rule like any other kind.
See `TestFoldPathAndPosition` and `TestFoldDispatchErrors`.

### Evaluation evidence

The examples build trees by hand. They exercise the leafkit needs from
fnlang listed above.

| Need | Evidence |
| --- | --- |
| Entry point, one rule per kind, chosen result and early error return | `TestArithmetic`, `ExampleFold`, and `TestFoldDispatchErrors` cover integer and string results, kind dispatch, and stopping on an operand error. |
| On-demand child evaluation and typed results | `TestConditionalAndShortCircuit` covers both conditional branches and short-circuiting. `TestFoldPathAndPosition` covers reordering and repeated calls. |
| Lexical lookup, scope creation and reuse, and annotations by node ID | `TestEnv` and `TestLexicalLet` cover inheritance and shadowing. A shared name is evaluated in distinct scopes and has one annotation. |
| Error location with source span and occurrence path | `TestFoldPathAndPosition` resolves byte offsets to line and column and distinguishes two occurrences of a shared node. |
| Callable values, arguments, channels, and collection lookup | These are language features for consumers to implement with their own rule and value types. |

In `TestArithmetic`, each literal rule copies the `*big.Int` held by
the tree and addition writes to a fresh integer. Runtime bindings
live in the environment. The tree and its payloads remain read-only.

## Ambiguity, errors and future tooling

**Current contract.** A tree is ordered, rooted and acyclic. Sharing
is optional. With no sharing, each non-root node has one incoming
edge. `NewAmbiguity` takes two or more ordered, complete alternative
roots. The ambiguity and its roots have equal spans, including the
source ID. Source-free alternatives can all use the unknown span.
Order is stable and leaves the choice to the consumer. See
`TestValidate` and `TestChildLists`.

`NewError` builds a leaf with a diagnostic message and a span. Use
`Message` to read the diagnostic. A missing element can use a known
point span. See `TestFprint` and `TestPositions`.

**Current contract.** `parser.Builders.Error` and
`parser.Builders.Ambiguity` let a caller supply nodes of the reserved
kinds that also satisfy a narrower interface. See
`TestTypedRootConstraint`, `TestTypedAmbiguityFactory` and the
[factory contract](parser.md#reductions-and-tree-construction).

For example, a language with both juxtaposition and infix addition might
read `f x + y` as either `Add(Call(f, x), y)` or `Call(f, Add(x, y))`.
Create `f`, `x`, and `y` once. Attach the two different roots to one
ambiguity node covering all seven bytes. The routes `[0, 0, 1]` and
`[1, 1, 0]` both reach `x` with the same ID. A consumer can find a first
occurrence using `Walk` or collect distinct ambiguity nodes with
`WalkUnique`. It inspects the ordered children and uses `Child(index)` for
the chosen alternative. It can also install an
explicit fold rule for `KindAmbiguity` that calls `child(index, env)`
once, using analysis from that environment and path. Leave the original
tree inspectable. If two occurrences require distinct choices, annotate
the occurrences or rebuild the chosen paths. An ID-keyed table represents a
choice for all occurrences only if the consumer wants that scope.
`TestAmbiguityExample` shows the same consumer choice at parse time with
a pure filter and later with an evaluation rule.

Ordinary trees use the same envelope and child slots. They need no alternate
list, ambiguity bit, retained graph index, deduplication, or choice pass.
Only a tree that represents alternatives has ambiguity nodes and their extra
edges. See `Container` and `Ambiguity` for the stored fields.

**Current contract.** For a conforming finite input within its limits,
`parser.Session.Run` returns a complete file tree on `Done`. It may contain
`KindError` nodes for damaged regions or zero-width missing parts.
`TestJSONRecoveryOracle` checks missing values, and
`TestStatementRecoveryOracle` checks retained surrounding nodes.
`TestRecoveryFatalFailures` distinguishes source, resource and builder
failures from syntax diagnostics. See the
[recovery contract](parser.md#errors-and-recovery).

A tree is faithful to its text when a source-backed node's range refers to
the actual bytes consumed for that node. A semantic node may omit delimiters
or comments from its child list, so individual ranges can overlap or leave
gaps. The source snapshot retains all bytes, including bytes omitted from a
semantic tree. Exact write-back of edited trees additionally needs a lossless
representation.

| Future need | Design provision and deferred work |
| --- | --- |
| Comments, whitespace, delimiters and exact write-back | Preserve source references. A formatter can attach trivia ranges to a node ID when ownership is intrinsic, or to an occurrence or child edge when it differs by parent. A lossless builder must define ownership of leading, trailing, between-child, delimiter and trailing-file bytes, including malformed input. It must emit each byte once for a chosen reading even when paths overlap. This design leaves the lossless storage and printer to later work. |
| Trees for large files | Keep the common envelope bounded. Store source bytes and line indexes per snapshot. Allocate children and opt-in tables where used. Avoid per-node parent links and per-node maps for trivia or named parts. Compare memory per node, allocations and retained source bytes on representative large files after implementation. |
| Hundreds of consumer kinds | Consumer declarations need no central leafkit enumeration. Use kind dispatch or typed fields as appropriate. Avoid allocating a per-node kind registry. |
| Incremental reparse | Keep identity separate from source offset and path. A reparser needs an explicit map between source revisions before it can describe old coordinates in new text. Reuse and invalidation rules for reparsed nodes are deferred. |
| Graph overlay | Store cross-references, labels or control-flow edges separately, keyed by ID or occurrence as needed. These edges can cycle and do not take part in structural walks or folds. |

## Hand-built examples

The examples build their roots without a lexer or parser. The tree
stores the data a consumer chooses. Interpretation is separate.

### Infix statement

For `let total = 123456789012345678901234567890 + 2;`, build the
two `Value[*big.Int]` leaves and put them in an ordered addition
container. A consumer-defined statement stores a pointer to its
binding record and exposes the initializer in its first child slot.
It uses `NewEnvelope` to get its metadata and ID. See `Example`
in `tree/example_test.go` for the runnable example and output.

The example reads the record from the typed statement and the
integer from the typed leaf. Both retain caller pointer identity.
The numeral's span still identifies the original bytes. See also
`TestPayloadReferences` for large structs, arrays and slices.

### JSON object

For `{"n": 7}`, the object contains a pair with a key followed by a
value. Keep repeated keys in the tree if the input has them. A
consumer decides how repeated keys affect meaning.

```go
key := tree.NewValue("json:key", tree.Span{}, "n")
number := tree.NewValue("json:number", tree.Span{}, 7)
pair := tree.NewContainer("json:pair", tree.Span{}, key, number)
object := tree.NewContainer("json:object", tree.Span{}, pair)
```

### Line-oriented record

For `room=3`, a line contains the field name followed by the field
value. A source-backed version can include the terminator in the
line span.

```go
key := tree.NewValue("config:key", tree.Span{}, "room")
value := tree.NewValue("config:value", tree.Span{}, "3")
line := tree.NewContainer("config:line", tree.Span{}, key, value)
```

### S-expression

For `(+ 2 3)`, the operator is the first child of the form, followed
by its operands. The consumer assigns meaning to these slots.

```go
op := tree.NewValue("sexpr:symbol", tree.Span{}, "+")
left := tree.NewValue("sexpr:integer", tree.Span{}, 2)
right := tree.NewValue("sexpr:integer", tree.Span{}, 3)
form := tree.NewContainer("sexpr:form", tree.Span{}, op, left, right)
```

`TestHandBuiltGrammarShapes` checks the three source-free examples,
including their child order and typed values.

## Open design questions

**Open design question.** Exact public spellings remain open where the
owner has given no separate naming decision. The
[API inventory](../api.md#tree) distinguishes those choices from the
selected tree semantics. Reuse across reparsed snapshots and any future
in-place mutation model require separate contracts.
