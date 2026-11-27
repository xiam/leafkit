# Specifications

This directory holds the library's contracts and design notes. Use these
status labels on substantive statements.

- **Design principle** guides decisions.
- **Current contract** describes behavior and cites the test or source
  symbol that verifies it.
- **Intended capability** describes planned behavior.
- **Open design question** identifies a decision that is still pending.

## Library specifications

- [Tree](tree.md) specifies nodes, construction, spans, and traversal.
- [Evaluation](tree.md#fold-scope-and-annotations) specifies the fold,
  scope, and annotations.
- [Parser engine](parser.md) specifies the token source, grammar steps,
  deterministic parsing, expression operators, recovery, packed
  ambiguity, branch-local state, and computed lexical requests.
- [bytescan adapter](adapter.md) specifies scanner integration, replay,
  trivia, layout and partial input.
- [SQL composition example](sql.md) maps shared and dialect syntax, bounded
  translation and benchmark checks to caller tests.
- [As-built parser foundation](foundation.md) maps the paper check to
  tested behavior and intended capabilities.

The [grammar guide](../writing-a-grammar.md) links to runnable reference
grammars that exercise these contracts.

See the [writing guide](../writing.md) for how these documents fit with the
rest of the documentation.
