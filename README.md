# leafkit

LeafKit is a Go library for building and using syntax trees. You can represent
`x + 1` as an addition with two children, then walk the tree to see where
each part came from.

## Install

LeafKit requires Go 1.23 or later. In your Go module, run this command.

```sh
go get github.com/xiam/leafkit
```

## Build and walk a tree

Build the tree for `x + 1` by hand and print each node with its source text.

`tree.NewValue` makes a leaf with a typed value. Here the name holds a string
and the number holds an int. A `tree.Span` names the source and gives a byte
range that includes the start and excludes the end. For example, bytes
`[4, 5)` cover `1`.

```go
package main

import (
	"fmt"

	"github.com/xiam/leafkit/tree"
)

func main() {
	const input = "x + 1"
	const source tree.SourceID = "expression"
	span := func(start, end int) tree.Span {
		return tree.Span{Source: source, Start: start, End: end}
	}

	x := tree.NewValue("name", span(0, 1), "x")
	one := tree.NewValue("number", span(4, 5), 1)
	sum := tree.NewContainer("add", span(0, len(input)), x, one)

	for node := range tree.Walk(sum) {
		info := node.Info()
		fmt.Printf("%s %q\n", info.Kind, input[info.Span.Start:info.Span.End])
	}
}
```

`tree.Walk` visits the addition first, then its children in the order passed
to `tree.NewContainer`.

```text
add "x + 1"
name "x"
number "1"
```

See the [statement example](tree/example_test.go) for a node that uses a Go
struct with named fields.

## Build trees from tokens

The [grammar guide](docs/writing-a-grammar.md) shows how to build nodes from
parts that a grammar matches. The parser supports ordered and ambiguous
choices, expression operators, syntax recovery, and
[branch-local state](docs/specs/parser.md#branch-local-state). A grammar can
[compute a lexical request](docs/specs/parser.md#computed-lexical-requests)
from matched input.

[Bytescan](https://github.com/xiam/bytescan) reads text and turns it into tokens.
The [Bytescan adapter](docs/specs/adapter.md) connects it to the parser. You
can also supply your own [token source](docs/specs/parser.md#tokens-layout-and-replay).
Programs such as [symbolic](https://github.com/xiam/symbolic) and fnlang are the
first intended users.

The [three-dialect SQL example](docs/sql-example.md) composes a shared SELECT
grammar, visits typed trees, and translates a bounded row limit. See the
[documentation index](docs/README.md) for the API contracts.
