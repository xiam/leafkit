package eval_test

import (
	"fmt"
	"strings"

	"github.com/xiam/leafkit/eval"
	"github.com/xiam/leafkit/tree"
)

func ExampleFold() {
	root := tree.NewContainer("group", tree.Span{},
		tree.NewValue("word", tree.Span{}, "hello"),
		tree.NewContainer("group", tree.Span{},
			tree.NewValue("word", tree.Span{}, "world")),
	)
	rules := map[tree.Kind]eval.Rule[string, struct{}]{
		"word": func(frame eval.Frame, _ *eval.Env[struct{}], _ eval.Descend[string, struct{}]) (string, error) {
			return frame.Node.(*tree.Value[string]).Value(), nil
		},
		"group": func(frame eval.Frame, env *eval.Env[struct{}], child eval.Descend[string, struct{}]) (string, error) {
			var parts []string
			for index := 0; ; index++ {
				if _, ok := frame.Node.Child(index); !ok {
					break
				}
				part, err := child(index, env)
				if err != nil {
					return "", err
				}
				parts = append(parts, part)
			}
			return "(" + strings.Join(parts, " ") + ")", nil
		},
	}
	result, err := eval.Fold(root, nil, rules)
	if err != nil {
		panic(err)
	}
	fmt.Println(result)
	// Output: (hello (world))
}
