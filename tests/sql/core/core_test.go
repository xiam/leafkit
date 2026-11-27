package core_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/internal/reference"
	sql "github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tree"
)

type fixture struct {
	Name       string      `json:"name"`
	SQL        string      `json:"sql"`
	Tree       string      `json:"tree"`
	Printed    string      `json:"printed"`
	Tables     []string    `json:"tables"`
	Diagnostic *diagnostic `json:"diagnostic,omitempty"`
}
type diagnostic struct {
	Start    int      `json:"start"`
	End      int      `json:"end"`
	At       int      `json:"at"`
	Found    string   `json:"found"`
	Expected []string `json:"expected"`
}

func fixtures(t testing.TB) []fixture {
	t.Helper()
	data, err := os.ReadFile("testdata/common.json")
	require.NoError(t, err)
	var result []fixture
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}
func snapshot(text string) *sql.Snapshot {
	return sql.NewSnapshot(tree.SourceID(fmt.Sprintf("reference:%x", sha256.Sum256([]byte(text)))), text)
}
func parse(t testing.TB, text string, streamed bool) tree.Node {
	t.Helper()
	snap := snapshot(text)
	engine, err := sql.Engine(snap)
	require.NoError(t, err)
	return reference.Parse(t, engine, func(in *reference.Input) (p.Source, error) {
		cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
		return adapter.New(in.ID(), sql.Scanner(cur), cur, p.Request{}, sql.Trivia())
	}, text, streamed, "")
}
func TestCommonFixtures(t *testing.T) {
	for _, tc := range fixtures(t) {
		t.Run(tc.Name, func(t *testing.T) {
			whole := parse(t, tc.SQL, false)
			chunked := parse(t, tc.SQL, true)
			got := sql.Canonical(whole)
			require.Equal(t, tc.Tree, got, "tree = %s\nwant   %s", got, tc.Tree)
			got = sql.Canonical(chunked)
			require.Equal(t, tc.Tree, got, "chunked tree = %s\nwant %s", got, tc.Tree)
			for _, root := range []tree.Node{whole, chunked} {
				problems := sql.Diagnostics(root)
				if tc.Diagnostic != nil {
					require.Len(t, problems, 1, "diagnostics=%v", problems)
					problem := problems[0].Problem()
					found := problem.Found.Token.Text
					if problem.Found.End {
						found = "<eof>"
					}
					var expected []p.Pattern
					for _, kind := range tc.Diagnostic.Expected {
						if kind == "<eof>" {
							expected = append(expected, p.EndOfFile())
						} else {
							expected = append(expected, p.Kind(p.TokenKind(kind)))
						}
					}
					assert.Equal(t, tc.Diagnostic.Start, problem.Span.Start, "diagnostic start")
					assert.Equal(t, tc.Diagnostic.End, problem.Span.End, "diagnostic end")
					assert.Equal(t, tc.Diagnostic.At, problem.Found.Point.Start, "diagnostic position")
					assert.Equal(t, tc.Diagnostic.Found, found, "diagnostic token")
					assert.Equal(t, expected, problem.Expected, "diagnostic expected tokens")
					_, err := sql.Print(root)
					assert.Error(t, err, "printer accepted diagnostics")
					continue
				}
				require.Len(t, problems, 0, "unexpected diagnostic: %+v", problems)
				printed, err := sql.Print(root)
				require.NoError(t, err, "Print=%q,%v want %q", printed, err, tc.Printed)
				require.Equal(t, tc.Printed, printed, "Print=%q,%v want %q", printed, err, tc.Printed)
				roundtrip := parse(t, printed, false)
				assert.Equal(t, tc.Tree, sql.Canonical(roundtrip), "roundtrip changed tree: %s", sql.Canonical(roundtrip))
				var tables []string
				for _, table := range sql.Tables(root) {
					var parts []string
					for _, part := range table.Name().Parts() {
						parts = append(parts, part.Value())
					}
					tables = append(tables, strings.Join(parts, "."))
				}
				assert.Equal(t, fmt.Sprint(tc.Tables), fmt.Sprint(tables), "tables=%v want %v", tables, tc.Tables)
			}
		})
	}
}

func TestLexemeReferencesAndReadOnlyAccess(t *testing.T) {
	text := `SELECT "c""ol", '` + strings.Repeat("λ", 8192) + `' FROM app.users AS u`
	snap := snapshot(text)
	root, err := sql.Parse(snap)
	require.NoError(t, err, "parse=%v,%v", root, err)
	require.Len(t, sql.Diagnostics(root), 0, "parse=%v,%v", root, err)
	require.NoError(t, tree.Validate(root))
	var values []*sql.Lexeme
	for node := range tree.Walk(root) {
		if value, ok := node.(*sql.Lexeme); ok {
			require.Same(t, snap, value.Snapshot(), "literal copied instead of retaining snapshot")
			raw, err := snap.Raw(value.Range())
			require.NoError(t, err, "raw range mismatch")
			require.Equal(t, value.Raw(), raw, "raw range mismatch")
			values = append(values, value)
		}
		child, ok := node.Child(-1)
		require.False(t, ok, "negative child")
		require.True(t, child == nil, "negative child")
		child, ok = node.Child(100)
		require.False(t, ok, "out of range child")
		require.True(t, child == nil, "out of range child")
	}
	require.GreaterOrEqual(t, len(values), 2, "decoded lexemes")
	require.Equal(t, `c"ol`, values[0].Value(), "decoding changed values")
	require.Equal(t, strings.Repeat("λ", 8192), values[1].Value(), "decoding changed values")
	file, ok := root.(*sql.File)
	require.True(t, ok, "file type=%T", root)
	require.NotNil(t, file)
	selNode, ok := file.Child(0)
	require.True(t, ok, "missing select")
	sel, ok := selNode.(*sql.Select)
	require.True(t, ok, "select type=%T", selNode)
	require.NotNil(t, sel)
	projections := sel.Projections()
	require.NotEmpty(t, projections)
	projections[0] = nil
	require.False(t, sel.Projections()[0] == nil, "mutable projection backing array")
	parts := sel.From().Name().Parts()
	require.NotEmpty(t, parts)
	parts[0] = nil
	require.False(t, sel.From().Name().Parts()[0] == nil, "mutable identifier backing array")
	require.Equal(t, "u", sel.From().Alias().Value(), "table alias missing")
	_, err = snap.Raw(tree.Span{Source: "other", End: 1})
	require.Error(t, err, "cross-snapshot range accepted")
	_, err = snap.Raw(tree.Span{Source: snap.ID(), End: len(text) + 1})
	require.Error(t, err, "out-of-bounds range accepted")
}

func TestCanonicalSourceIndependence(t *testing.T) {
	a, _ := sql.Parse(sql.NewSnapshot("a", `select U.ID, 'it''s' from Users AS U where U.ID != 2`))
	b, _ := sql.Parse(sql.NewSnapshot("b", `SELECT u.id,'it''s' FROM users AS u WHERE u.id <> 2;`))
	require.Zero(t, len(sql.Diagnostics(a))+len(sql.Diagnostics(b)), "normalization differs: %s / %s", sql.Canonical(a), sql.Canonical(b))
	require.Equal(t, sql.Canonical(b), sql.Canonical(a), "normalization differs: %s / %s", sql.Canonical(a), sql.Canonical(b))
	c, _ := sql.Parse(sql.NewSnapshot("c", `SELECT "U".id, 'it''s' FROM users AS u WHERE u.id <> 2`))
	require.NotEqual(t, sql.Canonical(a), sql.Canonical(c), "quoted case was folded")
}

func ExampleParse() {
	root, err := sql.Parse(sql.NewSnapshot("example", `select distinct u.id from users as u inner join teams as t on u.team_id = t.id where t.active = 1 order by u.id desc;`))
	if err != nil || len(sql.Diagnostics(root)) != 0 {
		panic("invalid example")
	}
	text, err := sql.Print(root)
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	for _, table := range sql.Tables(root) {
		fmt.Println(table.Name().Parts()[0].Value())
	}
	// Output:
	// SELECT DISTINCT u.id FROM users AS u INNER JOIN teams AS t ON u.team_id = t.id WHERE t.active = 1 ORDER BY u.id DESC
	// users
	// teams
}

func TestRejectedSubset(t *testing.T) {
	for _, text := range []string{
		"SELECT FROM users", "SELECT id, FROM users", "SELECT id FROM", "SELECT id FROM users ORDER BY", "SELECT id FROM users WHERE id =", "SELECT * AS x FROM users", "SELECT id FROM users u", "SELECT id FROM users JOIN teams ON id = id", "SELECT id FROM users INNER JOIN teams", "SELECT id FROM users LIMIT 2", "SELECT id FROM users; SELECT id FROM teams", "SELECT id FROM users WHERE id = 1 AND id = 2", "SELECT `id` FROM users", "SELECT [id] FROM users", "SELECT 'unclosed FROM users", "SELECT id FROM users /* unclosed", "SELECT id FROM users WHERE id == 1", "SELECT id FROM users ORDER BY id,", "SELECT DISTINCT DISTINCT id FROM users", "SELECT t.* FROM users AS t", "SELECT -1 FROM users", "SELECT count(*) FROM users", "SELECT id", "SELECT id FROM users LEFT JOIN teams ON id = id", `SELECT "" FROM users`,
	} {
		t.Run(text, func(t *testing.T) {
			whole := parse(t, text, false)
			chunked := parse(t, text, true)
			problems := sql.Diagnostics(whole)
			require.Len(t, problems, 1, "rejected subset accepted: %s", sql.Canonical(whole))
			chunkProblems := sql.Diagnostics(chunked)
			require.Len(t, chunkProblems, 1, "chunked diagnostics=%v", chunkProblems)
			a, b := problems[0].Problem(), chunkProblems[0].Problem()
			require.Equal(t, fmt.Sprint(b), fmt.Sprint(a), "streamed diagnostic differs: %+v / %+v", a, b)
			_, err := sql.Print(whole)
			assert.Error(t, err, "printer accepted diagnostic")
		})
	}
}
