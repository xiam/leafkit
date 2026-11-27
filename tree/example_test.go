package tree_test

import (
	"fmt"
	"math/big"

	"github.com/xiam/leafkit/tree"
)

type binding struct {
	name   string
	labels []string
}

type statement struct {
	info tree.Envelope
	bind *binding
	init *tree.Container
}

func (s *statement) Info() tree.Envelope { return s.info }

func (s *statement) Child(index int) (tree.Node, bool) {
	if index == 0 {
		return s.init, true
	}
	return nil, false
}

func Example() {
	const text = "let total = 123456789012345678901234567890 + 2;"
	const source tree.SourceID = "statement@1"
	span := func(start, end int) tree.Span {
		return tree.Span{Source: source, Start: start, End: end}
	}

	n, ok := new(big.Int).SetString(text[12:42], 10)
	if !ok {
		panic("invalid number")
	}
	left := tree.NewValue("stmt:int", span(12, 42), n)
	right := tree.NewValue("stmt:int", span(45, 46), big.NewInt(2))
	sum := tree.NewContainer("stmt:add", span(12, 46), left, right)
	original := &binding{name: "total", labels: []string{"local"}}
	root := &statement{
		info: tree.NewEnvelope("stmt:let", span(0, len(text))),
		bind: original,
		init: sum,
	}
	if err := tree.Validate(root); err != nil {
		panic(err)
	}
	fmt.Println(root.bind.name, root.bind.labels, root.bind == original)
	fmt.Println(left.Value(), left.Value() == n)
	for node := range tree.Walk(root) {
		fmt.Println(node.Info().Kind)
	}

	// Output:
	// total [local] true
	// 123456789012345678901234567890 true
	// stmt:let
	// stmt:add
	// stmt:int
	// stmt:int
}
