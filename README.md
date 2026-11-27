# leafkit

leafkit is a Go library in development for turning tokens into trees that
programs can inspect, change, and evaluate. The parser supports ordered
and ambiguous choices, expression operators, syntax recovery, and
[branch-local state](docs/specs/parser.md#branch-local-state). A grammar
can [compute a lexical request](docs/specs/parser.md#computed-lexical-requests)
from matched input. The
module requires Go 1.23 or later.

[bytescan](https://github.com/xiam/bytescan) reads text and turns it into
tokens. A leafkit parser uses a grammar supplied by the program and a
[token source](docs/specs/parser.md#tokens-layout-and-replay). The
[bytescan adapter](docs/specs/adapter.md) connects the two. Programs such as
[symbolic](https://github.com/xiam/symbolic) and
fnlang are the first intended users.
Programs can already build trees by hand with the
[tree package](docs/specs/tree.md#hand-built-examples).

See the [grammar guide](docs/writing-a-grammar.md) for runnable examples
and the [documentation index](docs/README.md) for the API contracts.

The [three-dialect SQL example](docs/sql-example.md) composes a shared SELECT
grammar, visits typed trees, and translates a bounded row limit.
