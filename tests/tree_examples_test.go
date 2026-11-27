package tests

import (
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/leafkit/tree"
)

func TestHandBuiltGrammarShapes(t *testing.T) {
	unknown := tree.Span{}
	jsonKey := tree.NewValue("json:key", unknown, "n")
	jsonNumber := tree.NewValue("json:number", unknown, 7)
	pair := tree.NewContainer("json:pair", unknown, jsonKey, jsonNumber)
	object := tree.NewContainer("json:object", unknown, pair)

	configKey := tree.NewValue("config:key", unknown, "room")
	configValue := tree.NewValue("config:value", unknown, "3")
	line := tree.NewContainer("config:line", unknown, configKey, configValue)

	op := tree.NewValue("sexpr:symbol", unknown, "+")
	left := tree.NewValue("sexpr:integer", unknown, 2)
	right := tree.NewValue("sexpr:integer", unknown, 3)
	form := tree.NewContainer("sexpr:form", unknown, op, left, right)

	for _, tc := range []struct {
		input string
		root  tree.Node
		want  []tree.Kind
	}{
		{`{"n": 7}`, object, []tree.Kind{"json:object", "json:pair", "json:key", "json:number"}},
		{"room=3\n", line, []tree.Kind{"config:line", "config:key", "config:value"}},
		{"(+ 2 3)", form, []tree.Kind{"sexpr:form", "sexpr:symbol", "sexpr:integer", "sexpr:integer"}},
	} {
		{
			err := tree.Validate(tc.root)
			require.NoErrorf(t, err, "%q: %v", tc.input, err)
		}
		var kinds []tree.Kind
		for n := range tree.Walk(tc.root) {
			kinds = append(kinds, n.Info().Kind)
			require.Equalf(t, unknown, n.Info().Span, "%q: source-free node has span %+v", tc.input, n.Info().Span)
		}
		require.Truef(t, slices.Equal(kinds, tc.want), "%q: got kinds %v, want %v", tc.input, kinds, tc.want)
	}
	require.Equalf(t, "n", jsonKey.Value(), "hand-built fixture values changed")
	require.Equalf(t, 7, jsonNumber.Value(), "hand-built fixture values changed")
	require.Equalf(t, "room", configKey.Value(), "hand-built fixture values changed")
	require.Equalf(t, "3", configValue.Value(), "hand-built fixture values changed")
	require.Equalf(t, "+", op.Value(), "hand-built fixture values changed")
	require.Equalf(t, 2, left.Value(), "hand-built fixture values changed")
	require.Equalf(t, 3, right.Value(), "hand-built fixture values changed")
}

func TestTreeImportsOnlyStandardLibrary(t *testing.T) {
	packages, err := parser.ParseDir(token.NewFileSet(), "../tree", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, parser.ImportsOnly)
	require.NoError(t, err)
	pkg := packages["tree"]
	require.Lenf(t, packages, 1, "expected production files for the tree package")
	require.Truef(t, pkg != nil, "expected production files for the tree package")
	require.NotEmptyf(t, pkg.Files, "expected production files for the tree package")
	for file, parsed := range pkg.Files {
		for _, spec := range parsed.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoErrorf(t, err, "%s: %v", file, err)
			dep, err := build.Default.Import(path, "", build.FindOnly)
			assert.Falsef(t, err != nil || !dep.Goroot, "%s: %q is not a standard library import", file, path)
		}
	}
}
