# Public API at the first milestone

This is an inventory of the library surface in the milestone snapshot.
It includes package declarations, exported fields, interface methods,
methods on concrete types and the promoted node methods callers can use.
The contracts give signatures, invariants and examples.

**Recorded** in the decision column identifies semantics the owner selected.
**Open** identifies a choice that lacks a separate owner decision. Open
choices can have working implementations. The owner selected the project
name **leafkit** and the lexer, parser and consumer separation. The exact
spellings of exported Go names remain open unless recorded individually.
The decision column applies to every name in its row. Merge records
establish what landed. The milestone does not declare an API freeze.

## tree

See the [tree contract](specs/tree.md).

| Exported names | Behavior and evidence | Decision |
| --- | --- | --- |
| `Shape`, `Shape.Info`, `Shape.Child`, `Node`, `Node.Info`, `Node.Child` | Metadata and ordered children form the common interface. `Node` embeds `Shape[Node]`. `tree.Example` exercises a consumer-defined node and `TestTypedRootConstraint` checks a narrower interface. | Recorded open typed nodes and basic common types. Open exact interface spellings and support for other self-typed child accessors in generic parser APIs. |
| `Kind`, `KindError`, `KindAmbiguity` | `Kind` is string-based. `validateEnvelope` enforces the reserved prefix. | Recorded plain strings and structural error and ambiguity types. Open exact reserved spellings. |
| `ID`, `Envelope`, `Envelope.Kind`, `Envelope.Span`, `Envelope.ID`, `NewEnvelope` | `NewEnvelope` assigns a fresh process-local identity. `TestRebuildSharesUnchangedNodes` verifies identity and snapshot preservation on shared objects. | Recorded a common envelope and read-only publication. Open cross-revision identity matching and exact identity API. |
| `SourceID`, `Span`, `Span.Source`, `Span.Start`, `Span.End`, `SourceLookup`, `Position`, `Position.Line`, `Position.Column`, `Positions` | Half-open bytes name an immutable external snapshot. `Positions` checks the supplied bytes and returns one-based lines and byte columns. `TestPositions` distinguishes an unknown span from a known point. | Recorded byte offsets and source snapshot ID. Open exact lookup names and the convenience position API. |
| `Value`, `NewValue`, `Value.Info`, `Value.Child`, `Value.Value` | The typed leaf keeps the supplied value according to Go assignment rules. `TestPayloadReferences` checks pointer and slice sharing. | Recorded typed common values and reference-backed large data. Open exact type and method spellings. |
| `Container`, `NewContainer`, `Container.Info`, `Container.Child`, `ErrorNode`, `NewError`, `ErrorNode.Info`, `ErrorNode.Child`, `ErrorNode.Message`, `Ambiguity`, `NewAmbiguity`, `Ambiguity.Info`, `Ambiguity.Child` | The common structural forms use the same envelope and child order. Constructors retain child references in owned slices. `TestChildLists` and `TestValidate` verify their shape. | Recorded the structural set and read-only publication. Open exact names and diagnostic message API. |
| `Path`, `Step`, `Step.Parent`, `Step.Index`, `Walk`, `WalkPath`, `WalkUnique`, `All` | Occurrence traversal visits each path, and unique traversal visits an ID once. `All` selects occurrences by type. `TestSharedWalks` and `TestWalkEarlyExit` verify ordering and stopping. | Recorded both occurrence and opt-in unique traversals. Open exact path and filter names. |
| `Validate`, `Fprint` | `TestValidate` checks reachable structure and IDs. `TestFprint` checks structural output, repeated edges and writer errors. | Open separate owner decisions on the exact validator and structural printer surface. |

## eval

See the [evaluation contract](specs/tree.md#fold-scope-and-annotations).

| Exported names | Behavior and evidence | Decision |
| --- | --- | --- |
| `Fold`, `Rule`, `Descend`, `Frame`, `Frame.Node`, `Frame.Path` | Rules choose child visits and their runtime environment. `TestArithmetic`, `TestConditionalAndShortCircuit` and `TestFoldPathAndPosition` exercise typed results and paths. | Recorded a consumer-directed interpreter toolset. Open exact fold signatures and callback names. |
| `Env`, `NewEnv`, `Env.Get`, `Env.Set`, `Table` | `TestEnv` checks lookup and current-scope writes. `TestLexicalLet` distinguishes per-ID annotations from evaluations in different scopes. | Open separate owner decisions on the scope and annotation APIs. |
| `Error`, `Error.Node`, `Error.Path`, `Error.Err`, `Error.Error`, `Error.Unwrap` | `TestFoldDispatchErrors` and `TestFoldPathAndPosition` check the failing occurrence, copied path and preserved cause. | Recorded the need for positions and meaning. Open exact evaluation error names and representation. |

## parser

See the [parser contract](specs/parser.md).

| Exported names | Behavior and evidence | Decision |
| --- | --- | --- |
| `TokenKind`, `Invalid`, `Trivia`, `Trivia.Kind`, `Trivia.Span`, `Token`, `Token.Kind`, `Token.Text`, `Token.Span`, `Token.Leading`, `Token.BreakBefore`, `Token.Synthetic` | Immutable records identify the offered token, trivia, physical line break and synthetic status. `TestBytescanOracleAndEveryInputSplit` and `TestBytescanLayoutAndContextualWord` exercise their transport. | Recorded independent lexer and tree boundaries. Open exact token and trivia representation names. |
| `Layout`, `TriviaOnly`, `Lines`, `Indentation`, `Joined`, `Request`, `Request.Goal`, `Request.Argument`, `Request.Layout` | A grammar selects a source profile. Source code supplies policy and meanings. `TestBytescanParserSelectsOperatorOrLiteral` and `TestLayoutGrammar` cover context and layout. | Recorded lexer steering and visible layout as foundation needs. Open exact profile names. |
| `Mark`, `Mark.Byte`, `Mark.Virtual`, `Mark.Key`, `ReadStatus`, `TokenReady`, `EndOfInput`, `NeedInput`, `Read`, `Read.Status`, `Read.Token`, `Read.Next`, `Read.Trailing`, `Source`, `Source.Begin`, `Source.Read`, `Source.DiscardBefore` | `checkRead` checks position and source data. `TestReplayAcrossFeeds` and `TestAllKeepsLexicalExitsAndCorrelatedFileRoots` cover replay and checkpoint isolation. | Recorded lexer independence and partial-input foundation. Open exact source and checkpoint spellings. |
| `Look`, `Look.Token`, `Look.End`, `Look.Point`, `Pattern`, `Kind`, `Word`, `EndOfFile`, `OnSameLine`, `Matches` | `Matches` tests look events. `TestBytescanLayoutAndContextualWord` and `TestExpressionPostfixLineBoundary` exercise contextual words and line restrictions. | Recorded contextual matching as a foundation need. Open exact matcher API and further normalization helpers. |
| `Grammar`, `Grammar.Start`, `Grammar.Rules`, `Grammar.Default`, `Rule`, `Configure`, `Engine`, `Engine.Start`, `Session`, `Session.Run`, `Status`, `Done`, `Paused` | The caller supplies rules and a token source. `TestProgramResumesPendingStep` and `TestJSONOracle` exercise recognition and resumption. | Recorded a consumer-defined grammar and configurable parser. Open the exact configuration and session API. |
| `Limits`, `Limits.Depth`, `Limits.ZeroMoves`, `Limits.VirtualTokens`, `Limits.Steps`, `Limits.RetainedBytes`, `Limits.Descriptors`, `Limits.ForestEdges`, `ErrGrammar`, `ErrSource`, `ErrLimit`, `ErrBuild` | `checkLimits`, `TestProgressAndFailures`, `TestChartConfigurationAndLimits` and `TestRecoveryFatalFailures` cover finite budgets and error categories. | Open precise budgets, error names and use of defaults. |
| `State`, `View`, `View.State`, `View.Parts`, `Step`, `ProgramState`, `ProgramState.Step`, `ProgramState.Control`, `ProgramState.Takes`, `ProgramState.Jumps`, `ProgramState.ReturnsTo`, `ProgramState.Calls`, `Move`, `Program`, `Take`, `Jump`, `SetState`, `Call`, `Return`, `Reject` | A program observes one look or a token-free control view and returns a declared move. `checkProgram` validates declarations. `TestProgramResumesPendingStep` and `TestDeepNamedCallsAndBuildUseBoundedGoStack` exercise the stack and resume. `Engine.Start` supplies the entry key. `SetState` changes the key through a declared jump, as checked by `TestBranchStateProgramAndExpression`. | Recorded Go functions in the bytescan style. Open exact move and field spellings. |
| `Tok`, `Ref`, `Seq`, `Opt`, `Many`, `Sep`, `Bind`, `Drop`, `Make`, `At`, `Not`, `Lex` | The production blocks record ordered results and scope requests. See `TestJSONOracle`, `TestLineOracleInBothStyles`, `TestReplayAcrossFeeds` and `TestDropRetainsDiagnostics`. | Recorded that consumers define their grammar. Open individual combinator names and further composition helpers. |
| `Update`, `Guard`, `Scope` | A body can update its exit key, check a predicate or restore its entry key. `TestBranchStateDeclarations` and `TestBranchStateAbandonedArm` check scoped changes and rollback. | Recorded branch-state capability in the foundation design. Open exact branch-state helper spellings. |
| `WithLex`, `CallWithLex` | A recognition callback scopes a complete request from a local view. `TestComputedQueuedBodies` checks both grammar styles, including capture-derived profiles and a branch-owned queue. `TestComputedCompetingRequests` checks distinct requests and replay checkpoints. | Recorded capture-dependent lexical steering in the foundation design. Open exact helper spellings. |
| `Parts`, `Parts.Tokens`, `Parts.Kinds`, `TokenRange`, `TokenRange.Len`, `TokenRange.At`, `Field`, `Field.Name`, `Field.Nodes`, `Field.Tokens`, `Build`, `Build.Slot`, `Build.Kind`, `Build.Span`, `Build.Fields`, `Build.Tokens`, `Builders`, `Builders.Node`, `Builders.Error`, `Builders.File`, `Builders.Ambiguity` | `TestExpressionBuildFields`, `TestPackedTokenLedgersStayLocalToReadings`, `TestTypedRootConstraint` and `TestTypedAmbiguityFactory` exercise captures and typed construction after recognition. | Recorded typed consumer nodes. Open exact builder, slot, capture and factory names. |
| `Policy`, `First`, `Longest`, `All`, `ChoiceSpec`, `ChoiceSpec.Policy`, `ChoiceSpec.Lookahead`, `ChoiceSpec.Slot`, `ChoiceSpec.Filter`, `Choose`, `Candidate`, `Candidate.Kind`, `Candidate.Span`, `Candidate.Parts`, `Candidate.Before`, `Candidate.After` | `TestNestedFirstCommitment`, `TestLongestKeepsOneReadingAndBreaksTiesByArm` and `TestAmbiguousDeclarationResolution` exercise the three policies and filtering. `chart.candidate` offers the choice entry and reading exit state keys. `TestBranchStateChoices` checks filters and distinct lexical and state exits. | Recorded real ambiguity and consumer interpretation. Open exact names, commitment and filter signatures. |
| `Expr`, `Fixity`, `Prefix`, `Infix`, `Postfix`, `Assoc`, `Left`, `Right`, `NonAssoc`, `Operator`, `Operator.Form`, `Operator.Match`, `Operator.Power`, `Operator.Assoc`, `Operator.Kind`, `Operator.Tail`, `Operator.Goal` | `TestExpressionOracles`, `TestExpressionBuildFields` and `TestExpressionOperatorRequestsResume` cover precedence, reductions, suffixes and scoped operator reads. | Recorded a general foundation beyond list syntax. Open exact expression API names and operator fields. |
| `Recover`, `Boundary`, `Boundary.At`, `Boundary.Consume`, `Boundary.Goal`, `Insert`, `Problem`, `Problem.Span`, `Problem.Expected`, `Problem.Found`, `Problem.Skipped`, `ErrorNode`, `ErrorNode.Slot`, `ErrorNode.Problem`, `ErrorNode.Info`, `ErrorNode.Child`, `ErrorNode.Message` | `TestRecoveryProgressAndInsertion`, `TestRecoverySelectsFurthestStream`, `TestChartRecoveryPreservesEqualCountLexicalMarks` and `TestStatementRecoveryOracle` cover repairs and original offers. The last three methods are promoted from the common tree error node. | Recorded error nodes among the common forms and a general parser foundation. Open exact recovery, insertion and diagnostic names. |

## adapter/bytescan

See the [adapter contract](specs/adapter.md).

| Exported names | Behavior and evidence | Decision |
| --- | --- | --- |
| `New`, `NewReplay`, `Factory`, `Source`, `Source.Begin`, `Source.Read`, `Source.DiscardBefore`, `Options`, `Options.Trivia`, `Options.Newlines` | The fixed stream and request-sensitive replay both satisfy `parser.Source`. `TestBytescanOracleAndEveryInputSplit` and `TestReplayKeepsSiblingState` cover their boundaries. | Recorded bytescan as a separate lexer. Open exact adapter constructors, factory and option names. |
| `Position`, `Position.Line`, `Position.Column`, `Positions` | `TestBytescanOracleAndEveryInputSplit` compares endpoints with bytescan's one-based lines and zero-based rune columns. | Recorded byte spans as the stored representation. Open the adapter's exact display conversion API. |
