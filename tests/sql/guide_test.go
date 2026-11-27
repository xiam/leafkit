package sql_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/internal/reference"
	"github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tests/sql/mysql"
	"github.com/xiam/leafkit/tests/sql/postgres"
	"github.com/xiam/leafkit/tests/sql/translate"
	"github.com/xiam/leafkit/tests/sql/tsql"
	"github.com/xiam/leafkit/tree"
)

// Example walks the same portable bytes through each independently composed
// dialect, then translates a native modifier and reports an unsupported one.
func Example() {
	const query = "SELECT u.id FROM users AS u INNER JOIN teams AS t ON u.team_id = t.id ORDER BY u.id"
	for _, dialect := range []struct {
		name  string
		parse func(*core.Snapshot) (tree.Node, error)
	}{{"postgres", postgres.Parse}, {"mysql", mysql.Parse}, {"tsql", tsql.Parse}} {
		root, err := dialect.parse(core.NewSnapshot(tree.SourceID(dialect.name), query))
		if err != nil {
			panic(err)
		}
		if len(core.Diagnostics(root)) != 0 {
			panic("invalid SQL")
		}
		text, err := core.Print(root)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s: %s; tables=%v\n", dialect.name, text, tableNames(root))
	}
	source, err := tsql.Parse(core.NewSnapshot("top", "SELECT TOP (2) [id] FROM [items] ORDER BY [id]"))
	if err != nil {
		panic(err)
	}
	if len(core.Diagnostics(source)) != 0 {
		panic("invalid SQL")
	}
	key := &translate.UniqueIntegerKey{Table: []string{"items"}, Column: "id"}
	target, diagnostics := translate.ToPostgres(source, key)
	if len(diagnostics) != 0 {
		panic(diagnostics[0].Message)
	}
	text, err := translate.PrintPostgres(target)
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	const tied = "SELECT TOP (2) WITH TIES [id] FROM [items] ORDER BY [id]"
	source, err = tsql.Parse(core.NewSnapshot("ties", tied))
	if err != nil {
		panic(err)
	}
	if len(core.Diagnostics(source)) != 0 {
		panic("invalid SQL")
	}
	_, diagnostics = translate.ToPostgres(source, key)
	for _, d := range diagnostics {
		fmt.Printf("%s: %s\n", d.Code, strings.TrimSpace(tied[d.Span.Start:d.Span.End]))
	}
	// Output:
	// postgres: SELECT u.id FROM users AS u INNER JOIN teams AS t ON u.team_id = t.id ORDER BY u.id; tables=[users teams]
	// mysql: SELECT u.id FROM users AS u INNER JOIN teams AS t ON u.team_id = t.id ORDER BY u.id; tables=[users teams]
	// tsql: SELECT u.id FROM users AS u INNER JOIN teams AS t ON u.team_id = t.id ORDER BY u.id; tables=[users teams]
	// SELECT "id" FROM "items" ORDER BY "id" LIMIT 2
	// with-ties: WITH TIES
}

func tableNames(root tree.Node) []string {
	var names []string
	for _, table := range core.Tables(root) {
		var parts []string
		for _, part := range table.Name().Parts() {
			parts = append(parts, part.Value())
		}
		names = append(names, strings.Join(parts, "."))
	}
	return names
}

type dialect struct {
	name    string
	engine  func(*core.Snapshot) (*p.Engine[tree.Node], error)
	scanner func(*cursor.Reader) *scan.Scanner
}

var dialects = []dialect{
	{"core", core.Engine, func(c *cursor.Reader) *scan.Scanner { return core.Scanner(c) }},
	{"postgres", postgres.Engine, postgres.Scanner},
	{"mysql", mysql.Engine, mysql.Scanner},
	{"tsql", tsql.Engine, tsql.Scanner},
}

func parse(t *testing.T, d dialect, text string, chunked bool) tree.Node {
	t.Helper()
	id := tree.SourceID(fmt.Sprintf("reference:%x", sha256.Sum256([]byte(text))))
	engine, err := d.engine(core.NewSnapshot(id, text))
	require.NoError(t, err)
	return reference.Parse(t, engine, func(in *reference.Input) (p.Source, error) {
		cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
		return adapter.New(in.ID(), d.scanner(cur), cur, p.Request{}, core.Trivia())
	}, text, chunked, "")
}

// TestPortableMatrix anchors all four parsers to the independent common-corpus
// oracle, rather than treating agreement between parsers as correctness.
func TestPortableMatrix(t *testing.T) {
	var fixtures []struct {
		Name, SQL, Tree, Printed string
		Tables                   []string
		Diagnostic               json.RawMessage
	}
	data, err := os.ReadFile("core/testdata/common.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &fixtures))
	for _, f := range fixtures {
		if len(f.Diagnostic) != 0 {
			continue
		} // Dialects add expected token alternatives.
		for _, d := range dialects {
			t.Run(f.Name+"/"+d.name, func(t *testing.T) {
				for _, chunked := range []bool{false, true} {
					root := parse(t, d, f.SQL, chunked)
					require.Len(t, core.Diagnostics(root), 0, "portable input rejected")
					got := core.Canonical(root)
					require.Equal(t, f.Tree, got, "tree=%s want %s", got, f.Tree)
					tables := tableNames(root)
					require.Equal(t, f.Tables, tables, "tables=%v want %v", tables, f.Tables)
					got, err := core.Print(root)
					require.NoError(t, err, "print=%q, %v", got, err)
					require.Equal(t, f.Printed, got, "print=%q, %v", got, err)
				}
			})
		}
	}
}

// TestNativeMatrix checks both acceptance and foreign rejection at every byte
// boundary. The examples use unambiguous extension syntax, not quote heuristics.
func TestNativeMatrix(t *testing.T) {
	for _, f := range []struct {
		owner, sql string
		kind       tree.Kind
		tables     []string
	}{
		{"postgres", "SELECT DISTINCT ON (id) id, $$λ$$ FROM users ORDER BY id", "postgres:distinct-on", []string{"users"}},
		{"mysql", "SELECT `u`.`id` FROM users AS u STRAIGHT_JOIN teams AS t ON u.team_id = t.id", "mysql:straight-join", []string{"users", "teams"}},
		{"tsql", "SELECT TOP (2) WITH TIES [id] FROM users ORDER BY [id]", "tsql:top", []string{"users"}},
	} {
		for _, d := range dialects {
			t.Run(f.owner+"/"+d.name, func(t *testing.T) {
				var wholeProblems []p.Problem
				for _, chunked := range []bool{false, true} {
					root := parse(t, d, f.sql, chunked)
					var problems []p.Problem
					for _, e := range core.Diagnostics(root) {
						problems = append(problems, e.Problem())
					}
					if d.name != f.owner {
						require.NotEmpty(t, problems, "foreign syntax accepted")
						if !chunked {
							wholeProblems = problems
						} else {
							require.Equal(t, wholeProblems, problems, "streaming changed diagnostic")
						}
						continue
					}
					require.Len(t, problems, 0, "unexpected diagnostics: %v", problems)
					require.Equal(t, f.tables, tableNames(root), "shared visitor changed")
					found := false
					for n := range tree.Walk(root) {
						found = found || n.Info().Kind == f.kind
					}
					require.True(t, found, "missing %s", f.kind)
				}
			})
		}
	}
}
