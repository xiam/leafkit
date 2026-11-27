package translate_test

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
	tr "github.com/xiam/leafkit/tests/sql/translate"
	"github.com/xiam/leafkit/tests/sql/tsql"
	"github.com/xiam/leafkit/tree"
)

type fixture struct {
	Name, SQL, Target string
	Rows              [][]int
}

func fixtures(t testing.TB) []fixture {
	t.Helper()
	data, err := os.ReadFile("testdata/top.json")
	require.NoError(t, err)
	var cases []fixture
	require.NoError(t, json.Unmarshal(data, &cases))
	return cases
}

var key = &tr.UniqueIntegerKey{Table: []string{"leafkit_translate"}, Column: "id"}

type parseFunc func(*core.Snapshot) (tree.Node, error)

func parse(t testing.TB, parser parseFunc, text string) tree.Node {
	t.Helper()
	root, err := parser(core.NewSnapshot("source", text))
	require.NoError(t, err)
	ds := core.Diagnostics(root)
	require.Len(t, ds, 0, "parse %q: %+v", text, ds)
	return root
}
func selectNode(t testing.TB, root tree.Node) *core.Select {
	t.Helper()
	require.NotNil(t, root)
	child, ok := root.Child(0)
	require.True(t, ok, "missing select")
	node, ok := child.(*core.Select)
	require.True(t, ok, "select type=%T", child)
	require.NotNil(t, node)
	return node
}
func target(t testing.TB, source tree.Node, k *tr.UniqueIntegerKey) (tree.Node, string) {
	t.Helper()
	before := core.Canonical(source)
	result, ds := tr.ToPostgres(source, k)
	require.Len(t, ds, 0, "translation: %+v", ds)
	require.NoError(t, tree.Validate(result))
	text, err := tr.PrintPostgres(result)
	require.NoError(t, err)
	reparsed := parse(t, postgres.Parse, text)
	require.Equal(t, core.Canonical(reparsed), core.Canonical(result), "reparse changed target:\n%s\n%s", core.Canonical(result), core.Canonical(reparsed))
	require.Equal(t, before, core.Canonical(source), "source changed")
	return result, text
}
func TestTopTranslation(t *testing.T) {
	for _, tc := range fixtures(t) {
		t.Run(tc.Name, func(t *testing.T) {
			source := parse(t, tsql.Parse, tc.SQL)
			sourceSQL, _ := tsql.Print(source)
			result, text := target(t, source, key)
			require.Equal(t, tc.Target, text, "SQL=%s want %s", text, tc.Target)
			from, to := selectNode(t, source), selectNode(t, result)
			if tc.Name == "native-star" {
				require.NotEmpty(t, from.Projections())
				projection, ok := from.Projections()[0].(*core.Projection)
				require.True(t, ok, "native star lacks Projection wrapper")
				_, ok = projection.One("expression").(*core.Star)
				require.True(t, ok, "native projection expression is not Star")
			}
			require.NotSame(t, source, result, "rewrite reused modified ancestors")
			require.NotSame(t, to, from, "rewrite reused modified ancestors")
			require.NotEqual(t, to.Info().ID, from.Info().ID, "rewrite reused modified ancestors")
			for _, field := range []string{"projections", "from", "joins", "where", "order"} {
				require.Equal(t, to.Field(field), from.Field(field), "portable %s changed", field)
			}
			limit, ok := to.One("suffix").(*postgres.Limit)
			require.True(t, ok, "missing LIMIT")
			require.NotNil(t, limit)
			top, ok := from.One("modifiers").(*tsql.Top)
			require.True(t, ok, "missing TOP")
			require.NotNil(t, top)
			require.Same(t, top.Count(), limit.Count(), "count copied")
			require.Same(t, top.Count().Snapshot(), limit.Count().Snapshot(), "count copied")
			afterSQL, _ := tsql.Print(source)
			require.Equal(t, afterSQL, sourceSQL, "source mutated")
			require.False(t, top.WithTies(), "source mutated")
		})
	}
}
func TestPortableDialectsAndIdentifierRendering(t *testing.T) {
	// Target-only reserved LIMIT, a double quote and Unicode must remain names.
	cases := []struct {
		parser parseFunc
		query  string
	}{
		{core.Parse, `SELECT LIMIT AS "a""λ" FROM t ORDER BY LIMIT`},
		{tsql.Parse, `SELECT [LIMIT] AS [a"λ] FROM [t] ORDER BY [LIMIT]`},
		{mysql.Parse, "SELECT `LIMIT` AS `a\"λ` FROM `t` ORDER BY `LIMIT`"},
	}
	for _, tc := range cases {
		source := parse(t, tc.parser, tc.query)
		result, text := target(t, source, nil)
		require.Same(t, source, result, "portable AST copied")
		// Source bare names fold to lower case; delimited names retain case.
		expectedName := selectNode(t, source).Projections()[0].(*core.Projection).One("expression").(*core.Identifier).Parts()[0].Value()
		require.False(t, !strings.Contains(text, `"`+expectedName+`" AS "a""λ"`), "identifier rendering: %s", text)
		require.Len(t, core.Tables(result), 1, "shared table visitor changed")
		require.Same(t, core.Tables(source)[0], core.Tables(result)[0], "shared table visitor changed")
	}
	// Shared joins, WHERE and DISTINCT remain portable without a TOP rewrite.
	for _, parser := range []parseFunc{core.Parse, postgres.Parse, mysql.Parse, tsql.Parse} {
		source := parse(t, parser, "SELECT DISTINCT a.id FROM a INNER JOIN b ON a.id = b.id WHERE a.id > 1 ORDER BY a.id DESC")
		result, _ := target(t, source, nil)
		require.Same(t, source, result, "shared visitor/subtrees changed")
		require.Len(t, core.Tables(result), 2, "shared visitor/subtrees changed")
	}
}
func TestNativeExtensionRejection(t *testing.T) {
	cases := []struct {
		parser parseFunc
		sql    string
	}{
		{postgres.Parse, "SELECT DISTINCT ON (id) id FROM t ORDER BY id"},
		{mysql.Parse, "SELECT t.id FROM t STRAIGHT_JOIN u ON t.id = u.id"},
		{tsql.Parse, "SELECT TOP (2) id FROM t ORDER BY id"},
	}
	parsers := []parseFunc{postgres.Parse, mysql.Parse, tsql.Parse}
	for i, tc := range cases {
		source := parse(t, tc.parser, tc.sql)
		require.NotEmpty(t, core.Tables(source), "shared visitor missed extension's table")
		for j, parser := range parsers {
			if i == j {
				continue
			}
			root, err := parser(core.NewSnapshot("foreign", tc.sql))
			require.NoError(t, err, "dialect%d accepted extension%d: %v", j, i, err)
			require.NotEmpty(t, core.Diagnostics(root), "dialect%d accepted extension%d: %v", j, i, err)
		}
	}
}
func TestTranslationDiagnostics(t *testing.T) {
	cases := []struct {
		sql, code, fragment string
		key                 *tr.UniqueIntegerKey
	}{
		{"SELECT TOP (3) WITH TIES id, score FROM leafkit_translate ORDER BY score", "with-ties", "WITH TIES", key},
		{"SELECT TOP (3) id FROM leafkit_translate ORDER BY id", "ordering-key", "TOP (3)", nil},
		{"SELECT TOP (3) id FROM leafkit_translate", "ordering-key", "TOP (3)", key},
		{"SELECT TOP (3) id FROM leafkit_translate ORDER BY score", "ordering-key", "score", key},
		{"SELECT TOP (3) id FROM leafkit_translate ORDER BY id, score", "ordering-key", "TOP (3)", key},
		{"SELECT TOP (3) id FROM other_table ORDER BY id", "ordering-key", "TOP (3)", key},
		{"SELECT TOP (3) id FROM leafkit_translate AS r ORDER BY leafkit_translate.id", "ordering-key", "leafkit_translate.id", key},
		{"SELECT TOP (2) id AS original_id, score AS id FROM leafkit_translate ORDER BY id", "ordering-key", "id", key},
		{"SELECT TOP (2) id AS original_id, score AS [ID] FROM leafkit_translate ORDER BY id", "ordering-key", "id", key},
		{"SELECT TOP (2) id AS id, score AS id FROM leafkit_translate ORDER BY id", "ordering-key", "id", key},
		{"SELECT TOP (2) 1 AS id FROM leafkit_translate ORDER BY id", "ordering-key", "id", key},
		{"SELECT DISTINCT TOP (3) id FROM leafkit_translate ORDER BY id", "unsupported-top", "TOP (3)", key},
		{"SELECT TOP (3) r.id FROM leafkit_translate AS r INNER JOIN b ON r.id = b.id ORDER BY r.id", "unsupported-top", "TOP (3)", key},
	}
	for _, tc := range cases {
		source := parse(t, tsql.Parse, tc.sql)
		before := core.Canonical(source)
		out, ds := tr.ToPostgres(source, tc.key)
		require.True(t, out == nil, "%s: result=%v diagnostics=%+v", tc.sql, out, ds)
		require.Len(t, ds, 1, "%s: result=%v diagnostics=%+v", tc.sql, out, ds)
		require.Equal(t, tc.code, ds[0].Code, "%s: result=%v diagnostics=%+v", tc.sql, out, ds)
		require.Equal(t, tree.SourceID("source"), ds[0].Span.Source, "%s: result=%v diagnostics=%+v", tc.sql, out, ds)
		require.Equal(t, tc.fragment, strings.TrimSpace(tc.sql[ds[0].Span.Start:ds[0].Span.End]), "%s: result=%v diagnostics=%+v", tc.sql, out, ds)
		require.Equal(t, before, core.Canonical(source), "failed translation mutated source")
	}
	for _, tc := range []struct {
		parser parseFunc
		sql    string
	}{
		{postgres.Parse, "SELECT DISTINCT ON (id) id FROM t"},
		{mysql.Parse, "SELECT t.id FROM t STRAIGHT_JOIN u ON t.id = u.id"},
	} {
		source := parse(t, tc.parser, tc.sql)
		out, ds := tr.ToPostgres(source, nil)
		require.True(t, out == nil, "lossy translation: %v %+v", out, ds)
		require.Len(t, ds, 1, "lossy translation: %v %+v", out, ds)
		require.Equal(t, "unsupported-node", ds[0].Code, "lossy translation: %v %+v", out, ds)
	}
	bad, err := tsql.Parse(core.NewSnapshot("bad", "SELECT TOP (1.5) id FROM t"))
	require.NoError(t, err)
	out, ds := tr.ToPostgres(bad, key)
	require.True(t, out == nil, "accepted parse error")
	require.Len(t, ds, 1, "accepted parse error")
	require.Equal(t, "invalid-sql", ds[0].Code, "accepted parse error")
	out, ds = tr.ToPostgres(nil, key)
	require.True(t, out == nil, "accepted nil")
	require.Len(t, ds, 1, "accepted nil")
}
func ExampleToPostgres() {
	source, _ := tsql.Parse(core.NewSnapshot("example", "SELECT TOP (2) [id] FROM [items] ORDER BY [id]"))
	result, diagnostics := tr.ToPostgres(source, &tr.UniqueIntegerKey{Table: []string{"items"}, Column: "id"})
	if len(diagnostics) != 0 {
		panic(diagnostics[0].Message)
	}
	text, _ := tr.PrintPostgres(result)
	fmt.Println(text)
	// Output:
	// SELECT "id" FROM "items" ORDER BY "id" LIMIT 2
}

func TestStreamedTranslation(t *testing.T) {
	cases := fixtures(t)
	cases = append(cases, fixture{Name: "boundary ties", SQL: ties(t).SQL}, fixture{Name: "shadowed ordering key", SQL: aliasCase(t).SQL})
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			var outputs []string
			var diagnostics [][]tr.Diagnostic
			for _, chunked := range []bool{false, true} {
				id := tree.SourceID(fmt.Sprintf("reference:%x", sha256.Sum256([]byte(tc.SQL))))
				engine, err := tsql.Engine(core.NewSnapshot(id, tc.SQL))
				require.NoError(t, err)
				source := reference.Parse(t, engine, func(in *reference.Input) (p.Source, error) {
					cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
					return adapter.New(in.ID(), tsql.Scanner(cur), cur, p.Request{}, core.Trivia())
				}, tc.SQL, chunked, "")
				out, ds := tr.ToPostgres(source, key)
				diagnostics = append(diagnostics, ds)
				if out != nil {
					text, err := tr.PrintPostgres(out)
					require.NoError(t, err)
					outputs = append(outputs, text)
				}
			}
			require.Len(t, diagnostics, 2)
			require.Equal(t, diagnostics[0], diagnostics[1], "streamed diagnostic ranges changed")
			require.False(t, len(outputs) == 2 && (outputs[0] != tc.Target || outputs[1] != tc.Target), "streamed translation: %v", outputs)
			require.False(t, tc.Target != "" && len(outputs) != 2, "missing streamed target")
		})
	}
}

func TestComposedProjectionShape(t *testing.T) {
	for _, query := range []string{
		"SELECT TOP (3) * FROM leafkit_translate ORDER BY id",
		"SELECT TOP (3) * FROM leafkit_translate ORDER BY leafkit_translate.id",
		"SELECT * FROM leafkit_translate ORDER BY id",
	} {
		for _, chunked := range []bool{false, true} {
			id := tree.SourceID(fmt.Sprintf("reference:%x", sha256.Sum256([]byte(query))))
			snapshot := core.NewSnapshot(id, query)
			grammar := tsql.Grammar()
			grammar.Rules["projection"] = p.Make("unwrapped-star", core.StarKind, core.Skip(core.Symbol("*")))
			engine, err := p.Configure(grammar, tsql.Builders(snapshot), p.Limits{})
			require.NoError(t, err)
			root := reference.Parse(t, engine, func(in *reference.Input) (p.Source, error) {
				cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
				return adapter.New(in.ID(), tsql.Scanner(cur), cur, p.Request{}, core.Trivia())
			}, query, chunked, "")
			require.NoError(t, tree.Validate(root))
			require.Len(t, core.Diagnostics(root), 0, "custom parse has diagnostics")
			require.NotEmpty(t, selectNode(t, root).Projections())
			_, ok := selectNode(t, root).Projections()[0].(*core.Star)
			require.True(t, ok, "probe did not create an unwrapped Star")
			before := core.Canonical(root)
			result, ds := tr.ToPostgres(root, key)
			require.True(t, result == nil, "custom projection result=%v diagnostic=%+v", result, ds)
			require.Len(t, ds, 1, "custom projection result=%v diagnostic=%+v", result, ds)
			require.Equal(t, "unsupported-projection", ds[0].Code, "custom projection result=%v diagnostic=%+v", result, ds)
			require.Equal(t, id, ds[0].Span.Source, "custom projection result=%v diagnostic=%+v", result, ds)
			require.Equal(t, "*", strings.TrimSpace(query[ds[0].Span.Start:ds[0].Span.End]), "custom projection result=%v diagnostic=%+v", result, ds)
			require.Equal(t, before, core.Canonical(root), "rejection changed source")
		}
	}
}
