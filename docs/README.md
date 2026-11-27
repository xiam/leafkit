# Documentation

- [Implementation report](closing-report.md) records the foundation, findings
  and inputs for consumer ports.
- [Public API inventory](api.md) lists the exported surface and decisions.
- [Write a grammar](writing-a-grammar.md) shows how to parse and evaluate
  a statement language and links to the reference grammars.
- [Compose three SQL dialects](sql-example.md) walks through shared trees,
  bounded translation and reproducible checks.
- [Branch-local parser state](specs/parser.md#branch-local-state) explains
  scoped declarations and predicates during recognition.
- [Computed lexical requests](specs/parser.md#computed-lexical-requests)
  explains how to select a lexer profile from a captured opener.
- [Writing the documentation](writing.md) describes where to put information
  and how to support claims about current behavior.
- [Specifications](specs/README.md) describes how to record the exact
  contracts and design decisions.
- [bytescan adapter](specs/adapter.md) describes connecting a scanner or mode
  driver to the parser.

## Checking a change

From the module root, run `make fmt-check`, `make vet`, and `make test`.
Use Go 1.23 or later and a C compiler for the race detector. The
[Makefile](../Makefile) defines the checks. The test target runs the suite with
the race detector and disables cached test results.

<!-- prose-internal: CI configuration notes moved to agent guidance. No library contract changed. -->
