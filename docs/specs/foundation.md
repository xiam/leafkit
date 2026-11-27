# As-built parser foundation

The [six-language paper check](parser.md#six-language-paper-check) describes
18 grammar traces for Go, Python, JavaScript, TypeScript, SQL and Ruby.
The design was reviewed before the library was built. The tables below
trace its shared capabilities to the implementation and show where a
paper trace still calls for future work.

## Current foundation

The delivery names correspond to the work on the tree, parser core,
expressions, recovery, ambiguity, adapter, reference grammars and
branch-local state and computed requests.

| Capability | Delivery | Code or test evidence |
| --- | --- | --- |
| Open typed nodes, source ranges and occurrence paths | Tree | `tree.Example` exercises a consumer-defined node and `TestHandBuiltGrammarShapes` tests three structures without a lexer. `TestPayloadReferences`, `TestPositions` and `TestSharedWalks` cover payload references, spans and shared occurrences. |
| Consumer-directed evaluation and scopes | Evaluation | `TestArithmetic`, `TestConditionalAndShortCircuit`, `TestLexicalLet` and `TestFoldPathAndPosition` exercise typed results, selective descent, lookup and located errors. |
| Consumer grammars, named calls, fields, progress and streaming on a hand-made source | Parser core | `TestJSONOracle`, `TestLineOracleInBothStyles`, `TestProgramResumesPendingStep`, `TestDeepNamedCallsAndBuildUseBoundedGoStack` and `TestProgressAndFailures`. |
| Parser-directed lexical profiles and immutable replay | Parser core, adapter, ambiguity | `TestReplayAcrossFeeds` and `TestBytescanParserSelectsOperatorOrLiteral` use different goals from one mark. `TestReplayKeepsSiblingState` checks source state isolation. `TestAllKeepsLexicalExitsAndCorrelatedFileRoots` retains simultaneous exits. |
| Nested lexical modes and requests | Parser core, adapter, reference grammars | `TestBytescanInterpolationModes` exercises scanner switches and stack restart. `TestLayoutGrammar` scopes template text and embedded expressions with `Lex`. |
| Line breaks, layout markers, joined input and trivia | Parser core, adapter, reference grammars | `TestBytescanLayoutAndContextualWord` checks the four request profiles. `TestBytescanLayoutFlushesVirtualTokenBeforeEOF` checks final layout. `TestLayoutGrammar` combines a nested suite with joined expression lines and `TestBytescanOracleAndEveryInputSplit` checks trivia and spans. |
| Contextual words and physical line restrictions | Parser core, expressions, adapter, reference grammars | `Word` and `Kind` match text and kind. `TestLayoutGrammar` distinguishes header and identifier uses of `match`. `TestExpressionPostfixLineBoundary` checks `OnSameLine` on an offered operator. |
| Grammar-owned state, scope and predicates | Branch-local state | `TestBranchStateDeclarations`, `TestBranchStateProgramAndExpression`, `TestBranchStateChoices` and `TestBranchStateRecovery` cover scoped declarations, state-bearing continuations, and rollback across streaming schedules. |
| Captured delimiters, computed lexical profiles and queued bodies | Computed requests | `TestComputedQueuedBodies` combines declaration scopes, captured delimiter state, quoted and indented bodies, a joined expression, insertion, a wrong closer and replay through both sources. `TestComputedCompetingRequests` verifies same-mark differences, filtering and stable logical graphs under checkpoint renaming. |
| Grammar extension, normalized words and physical line checks | Public compositions | `TestPaperHelperCompositions` checks a copied rule map with late-bound references, case folding that preserves text and excludes quoted tokens, and a non-consuming line check. |
| Operators, precedence and postfix continuation | Expressions | `TestExpressionOracles`, `TestExpressionBuildFields`, `TestExpressionLongChains` and `TestExpressionOperatorRequestsResume`. |
| Local repair and grammar-permitted insertion | Recovery, ambiguity | `TestRecoveryProgressAndInsertion`, `TestStatementRecoveryOracle`, `TestJSONRecoveryOracle` and `TestChartRecoveryPreservesEqualCountLexicalMarks` cover boundary ownership, diagnostics, retained output and replay. |
| Bounded ordered choices, packed readings and consumer resolution | Parser core, ambiguity, reference grammars | `TestNestedFirstCommitment`, `TestLongestKeepsOneReadingAndBreaksTiesByArm`, `TestPackedLeftRecursiveExpression`, `TestPackedMalformedExpressionKeepsWorkBound` and `TestAmbiguityExample`. The binary-grammar bounds concern the stated fixtures. |
| Whole and streamed examples through both source paths | Parser core, adapter, reference grammars | `TestBytescanOracleAndEveryInputSplit` compares the handmade source, fixed stream and replay adapter. The [reference grammars](../writing-a-grammar.md#more-reference-grammars) cover consumer parsing, printing and evaluation. |

## The paper traces against the current API

Each row identifies evidence for the reusable building blocks. The last
column describes the language policies and reference comparisons still
needed for a compatibility result.

| Paper trace | What the milestone demonstrates | Remaining language work or planned API |
| --- | --- | --- |
| Go inserted semicolons | The virtual layout tests establish positioned synthetic tokens before another token or EOF. `TestRecoveryProgressAndInsertion` establishes grammar-side insertion separately. | A Go source policy must decide when to emit a lexical semicolon and compare it with a Go scanner. |
| Go control-clause composite literal | `Grammar.Rules` can supply separate expression bodies. `TestExpressionStatementGrammar` and `TestStatementRecoveryOracle` cover expression and block ownership. | Provide the Go control-clause exclusions and compare them with the reference grammar. |
| Go index, slice and type arguments | `TestExpressionOracles` checks postfix tails. `TestAmbiguousDeclarationResolution` checks caller-controlled filtering of full-span candidates. | Define Go bracket alternatives and their semantic resolution. |
| Python indentation and bracket suppression | `TestBytescanLayoutAndContextualWord` checks visibility and suppression. `TestLayoutGrammar` checks nesting, final dedents and joined expression lines. | Supply Python blank-line, continuation, bracket and dedent rules. |
| Python soft keywords and patterns | `TestLayoutGrammar` uses `match` as a contextual header and a name. | Supply the pattern and guard grammar and validate its actual keyword positions. |
| Python nested f-strings | `TestBytescanInterpolationModes` and `TestLayoutGrammar` establish saved modes. `TestComputedQueuedBodies` derives a nested expression request from a captured opener. | Supply Python replacement fields, escaping and format-specifier rules. |
| JavaScript regexp versus division | `TestBytescanParserSelectsOperatorOrLiteral` and `TestExpressionOperatorRequestsResume` establish goal-sensitive reads at the same mark. | The lexical regexp body and JavaScript expression contexts belong in its lexer and grammar. |
| JavaScript template substitutions | `TestLayoutGrammar` scopes a quoted text profile around an embedded expression and resumes it after the closer. | Add JavaScript's template head, middle and tail tokens, nesting and brace rules. |
| JavaScript semicolons and restricted productions | `TestRecoveryProgressAndInsertion` checks permitted insertion. `TestExpressionPostfixLineBoundary` checks physical line rejection. | Supply JavaScript's terminator eligibility. `TestPaperHelperCompositions` verifies a non-consuming physical line check for the paper's `SameLine` use. |
| TypeScript generic call versus comparisons | `TestAmbiguousDeclarationResolution` checks a permissive grammar plus a consumer filter. `Grammar.Rules` accepts a caller-assembled set. | Supply TypeScript's syntax and discriminator. A copied rule map supports the paper's extension pattern, as checked by `TestPaperHelperCompositions`. |
| TypeScript arrows versus parentheses | `TestDanglingElseKeepsBothFullReadings` shows full alternatives surviving a shared prefix. `TestExpressionPostfixLineBoundary` checks the line guard on a token. | Supply parameter, follow-token and TSX rules. A public program step can express the standalone physical line check. |
| TypeScript split and shifted tokens | `TestAllKeepsLexicalExitsAndCorrelatedFileRoots` tests distinct replay exits for multiple lexical requests in one parse. | Exercise actual split `>` and shift tokens in a consumer grammar. |
| SQL non-reserved words | `TestLayoutGrammar` shows grammar-position-specific word matching. `Look.Token` gives a program step the offered token and its original `Token.Text`. | Supply dialect classification and validation. `Guard` can inspect the captured word. The caller can use the normalized-word composition checked by `TestPaperHelperCompositions`. |
| SQL case folding and CASE | `TestLineOracleInBothStyles` checks ordered clauses and bindings in both forms of grammar. `Word` compares text exactly. | Implement dialect-specific normalization and CASE productions. The normalized-word oracle keeps original text and rejects a quoted token. |
| SQL dialects and quoted bodies | `TestPaperHelperCompositions` checks the rule-map override and `TestComputedQueuedBodies` checks capture-derived delimiter arguments. | Supply dialect rules and matching sources for the actual quoted forms. |
| Ruby queued heredocs | `TestComputedQueuedBodies` checks grammar-owned queue advancement, captured delimiters, quoting and indentation profiles, embedded expressions and recovery over handmade and pinned replay sources. | Implement Ruby's exact delimiter, indentation, quoting and interpolation rules. |
| Ruby local names versus receiverless calls | `TestBranchStateDeclarations` uses `Update` or `SetState` for grammar-time declarations, `Scope` for restoration and `Guard` to classify local names. `TestBranchStateChoices` checks filters against entry and exit keys. | Supply Ruby-specific declaration points, scoping and name-resolution rules. |
| Ruby command and block attachment | `TestDanglingElseKeepsBothFullReadings` demonstrates nested attachment ambiguity, and `TestAmbiguityExample` shows how a caller can filter or resolve a reading. | Supply Ruby's attachment policy and command contexts. `Guard` can consult local state and the tested line-check composition can restrict the argument context. |

The [API inventory](../api.md#parser) lists the current declarations.
The original design hooks cover lexical steering, visible or joined
layout, and contextual words. The branch-state and computed-request
oracles add the two foundation mechanisms flagged in the initial audit.
The combined fixture demonstrates them together through both source paths.
The milestone remains open for a separate close-out decision.
Real-language parsers and compatibility comparisons are consumer work.

The milestone scope places the symbolic and fnlang ports, real-language
parsers, generated parsers, lossless write-back, incremental reparse and
a reference or control-flow graph overlay in later work. The paper helper
spellings remain open. Their tested compositions address the uses above.
