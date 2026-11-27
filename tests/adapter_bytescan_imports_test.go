package tests

import (
	goparser "go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBytescanImportsStayInAdapter(t *testing.T) {
	root := ".."
	adapter := filepath.Join(root, "adapter", "bytescan")
	// These reusable SQL callers are examples, not library implementations.
	sqlCallers := map[string]bool{}
	for _, name := range []string{"core", "postgres", "mysql", "tsql"} {
		sqlCallers[filepath.Join(root, "tests", "sql", name)] = true
	}
	adapterImports := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root && (strings.HasPrefix(entry.Name(), ".") ||
			entry.Name() == "testdata" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := goparser.ParseFile(token.NewFileSet(), path, nil, goparser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			name, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			const bytescan = "github.com/xiam/bytescan"
			if name == bytescan || strings.HasPrefix(name, bytescan+"/") {
				switch dir := filepath.Dir(path); {
				case dir == adapter:
					adapterImports++
				case sqlCallers[dir]:
					// Permit exact caller packages, never their subtrees.
				default:
					assert.Failf(t, "import outside the adapter package", "%s imports %q", path, name)
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEqualf(t, 0, adapterImports, "guard found no bytescan imports")
}
