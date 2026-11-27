package tsql_test

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
	sql "github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tests/sql/tsql"
	"github.com/xiam/leafkit/tree"
)

func snapshot(text string) *sql.Snapshot {
	return sql.NewSnapshot(tree.SourceID(fmt.Sprintf("reference:%x", sha256.Sum256([]byte(text)))), text)
}

func parse(t testing.TB, text string, chunked bool) tree.Node {
	t.Helper()
	snap := snapshot(text)
	engine, err := tsql.Engine(snap)
	require.NoError(t, err)
	return reference.Parse(t, engine, func(in *reference.Input) (p.Source, error) {
		cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
		return adapter.New(in.ID(), tsql.Scanner(cur), cur, p.Request{}, sql.Trivia())
	}, text, chunked, "")
}

type fixture struct {
	Name    string `json:"name"`
	SQL     string `json:"sql"`
	Tree    string `json:"tree"`
	Printed string `json:"printed"`
}

func load(t testing.TB, path string) []fixture {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cases []fixture
	require.NoError(t, json.Unmarshal(data, &cases))
	return cases
}

func check(t *testing.T, tc fixture) {
	t.Helper()
	for _, chunked := range []bool{false, true} {
		root := parse(t, tc.SQL, chunked)
		errors := sql.Diagnostics(root)
		require.Len(t, errors, 0, "diagnostic: %+v", errors)
		got := sql.Canonical(root)
		require.Equal(t, tc.Tree, got, "tree=%s\nwant %s", got, tc.Tree)
		printed, err := tsql.Print(root)
		require.NoError(t, err, "Print=%q,%v want %q", printed, err, tc.Printed)
		require.Equal(t, tc.Printed, printed, "Print=%q,%v want %q", printed, err, tc.Printed)
		again := parse(t, printed, chunked)
		require.Len(t, sql.Diagnostics(again), 0, "roundtrip=%s", sql.Canonical(again))
		require.Equal(t, tc.Tree, sql.Canonical(again), "roundtrip=%s", sql.Canonical(again))
	}
}

func TestDialectFixtures(t *testing.T) {
	for _, tc := range load(t, "testdata/dialect.json") {
		t.Run(tc.Name, func(t *testing.T) { check(t, tc) })
	}
}

func TestPortableFixtures(t *testing.T) {
	for _, tc := range load(t, "../core/testdata/common.json") {
		if tc.Printed == "" {
			continue
		} // invalid core cases have no portable output
		t.Run(tc.Name, func(t *testing.T) { check(t, tc) })
	}
}

func TestRejectedSyntax(t *testing.T) {
	for _, text := range []string{
		"SELECT TOP 2 id FROM users", "SELECT TOP () id FROM users",
		"SELECT TOP (-1) id FROM users", "SELECT TOP (+1) id FROM users",
		"SELECT TOP (1.5) id FROM users", "SELECT TOP (1e2) id FROM users",
		"SELECT TOP (9223372036854775808) id FROM users",
		"SELECT TOP (1+1) id FROM users", "SELECT TOP (@n) id FROM users",
		"SELECT TOP (2) PERCENT id FROM users", "SELECT TOP (2) PERCENT FROM users",
		"SELECT TOP (2) WITH TIES id FROM users",
		"SELECT TOP (2) WITH TIES [ORDER] FROM users",
		"SELECT TOP (2) WITH TIES 'ORDER BY' FROM users /* ORDER BY id */",
		"SELECT TOP (2) WITH TIES id FROM users ORDER BY",
		"SELECT WITH TIES id FROM users ORDER BY id",
		"SELECT TOP (2) DISTINCT id FROM users",
		"SELECT TOP (2) TOP (3) id FROM users",
		"SELECT [] FROM users", "SELECT [unclosed FROM users",
		"SELECT [a\nb] FROM users", "SELECT [a]b] FROM users",
		"SELECT id FROM users LIMIT 2", "SELECT id FROM users ORDER BY id OFFSET 1 ROWS",
		"SELECT id FROM users UNION SELECT id FROM archived",
	} {
		t.Run(text, func(t *testing.T) {
			whole, chunked := parse(t, text, false), parse(t, text, true)
			a, b := sql.Diagnostics(whole), sql.Diagnostics(chunked)
			require.NotEmpty(t, a, "diagnostics=%d,%d", len(a), len(b))
			require.Len(t, a, len(b), "diagnostics=%d,%d", len(a), len(b))
			for i := range a {
				require.Equal(t, b[i].Problem(), a[i].Problem(), "chunking changed diagnostic")
			}
			for _, root := range []tree.Node{whole, chunked} {
				_, err := tsql.Print(root)
				require.Error(t, err, "printed invalid query")
			}
		})
	}
}

func TestTypedNodesAndRanges(t *testing.T) {
	text := "-- prefix\nSELECT DISTINCT TOP /* count */ (0002) WITH TIES [a]]b] AS [名] FROM [dbo].[User]]s] AS u INNER JOIN teams AS t ON u.id = t.id ORDER BY [a]]b] DESC, u.id ASC;"
	root := parse(t, text, true)
	require.Len(t, sql.Diagnostics(root), 0, "query rejected")
	var top *tsql.Top
	var selectNode *sql.Select
	var name *sql.Lexeme
	var projections, orders int
	for node := range tree.Walk(root) {
		switch n := node.(type) {
		case *tsql.Top:
			top = n
		case *sql.Select:
			selectNode = n
		case *sql.Projection:
			projections++
		case *sql.Order:
			orders++
		case *sql.Lexeme:
			if n.Value() == "a]b" {
				name = n
			}
		}
	}
	require.NotNil(t, top, "TOP semantics lost")
	require.True(t, top.WithTies(), "TOP semantics lost")
	require.Equal(t, "0002", top.Count().Value(), "TOP semantics lost")
	require.NotNil(t, selectNode, "shared projection/order types lost")
	require.Equal(t, 1, projections, "shared projection/order types lost")
	require.Equal(t, 2, orders, "shared projection/order types lost")
	require.Len(t, selectNode.Field("order"), 2, "shared projection/order types lost")
	got := sql.Tables(root)
	require.Len(t, got, 2, "shared table visitor differs")
	require.GreaterOrEqual(t, len(got[0].Name().Parts()), 2)
	require.NotEmpty(t, got[1].Name().Parts())
	require.Equal(t, "User]s", got[0].Name().Parts()[1].Value(), "shared table visitor differs")
	require.Equal(t, "teams", got[1].Name().Parts()[0].Value(), "shared table visitor differs")
	require.NotNil(t, name, "bracket decoding lost")
	require.Equal(t, "[a]]b]", name.Raw(), "bracket decoding lost")
	require.True(t, name.Quoted(), "bracket decoding lost")
	for _, leaf := range []*sql.Lexeme{name, top.Count()} {
		span := leaf.Range()
		raw, err := leaf.Snapshot().Raw(span)
		require.NoError(t, err, "range=%+v raw=%q,%v", span, raw, err)
		require.Equal(t, leaf.Raw(), raw, "range=%+v raw=%q,%v", span, raw, err)
		require.Equal(t, raw, text[span.Start:span.End], "range=%+v raw=%q,%v", span, raw, err)
	}
	require.Equal(t, strings.Index(text, "0002"), top.Count().Range().Start, "count range includes trivia")
	span := top.Info().Span
	topText := text[span.Start:span.End]
	require.True(t, strings.Contains(topText, "TOP"), "TOP span=%q", topText)
	require.True(t, strings.HasSuffix(topText, "TIES"), "TOP span=%q", topText)
	child, ok := top.Child(0)
	require.True(t, ok, "count child lost")
	require.Same(t, top.Count(), child, "count child lost")
	_, ok = top.Child(-1)
	require.False(t, ok, "negative child")
	_, ok = top.Child(2)
	require.False(t, ok, "out of range child")
	_, err := sql.Print(root)
	require.Error(t, err, "core silently dropped TOP")
	printed, err := tsql.Print(root)
	require.NoError(t, err, "Print=%q,%v", printed, err)
	require.True(t, strings.Contains(printed, "TOP (0002) WITH TIES"), "Print=%q,%v", printed, err)
	require.True(t, strings.Contains(printed, `"a]b"`), "Print=%q,%v", printed, err)
	again := parse(t, printed, true)
	require.Len(t, sql.Diagnostics(again), 0, "typed roundtrip changed semantics")
	require.Equal(t, sql.Canonical(root), sql.Canonical(again), "typed roundtrip changed semantics")
}

func TestParseAndIsolation(t *testing.T) {
	for _, text := range []string{"SELECT TOP (0) id FROM users", "SELECT TOP (9223372036854775807) id FROM users", "SELECT [a[b] FROM users", "SELECT top_name, with_ties, ties2, percent_value FROM users"} {
		root, err := tsql.Parse(snapshot(text))
		require.NoError(t, err, "Parse(%q)=%v,%v", text, root, err)
		require.Len(t, sql.Diagnostics(root), 0, "Parse(%q)=%v,%v", text, root, err)
		require.NoError(t, tree.Validate(root))
	}
	text := "SELECT TOP (2) id FROM users"
	root, err := sql.Parse(snapshot(text))
	require.NoError(t, err, "core acquired dialect syntax")
	require.NotEmpty(t, sql.Diagnostics(root), "core acquired dialect syntax")
	g := tsql.Grammar()
	g.Rules["select"] = p.Seq()
	root, err = tsql.Parse(snapshot(text))
	require.NoError(t, err, "caller mutated global grammar")
	require.Len(t, sql.Diagnostics(root), 0, "caller mutated global grammar")
}

func ExampleParse() {
	root, err := tsql.Parse(sql.NewSnapshot("query", "SELECT TOP (2) WITH TIES [id] FROM [users] ORDER BY [score] DESC"))
	if err != nil || len(sql.Diagnostics(root)) != 0 {
		panic("invalid query")
	}
	text, err := tsql.Print(root)
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println("tables:", len(sql.Tables(root)))
	// Output:
	// SELECT TOP (2) WITH TIES "id" FROM "users" ORDER BY "score" DESC
	// tables: 1
}
