package mysql_test

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
	mysql "github.com/xiam/leafkit/tests/sql/mysql"
	"github.com/xiam/leafkit/tree"
)

type fixture struct {
	Name       string   `json:"name"`
	SQL        string   `json:"sql"`
	Tree       string   `json:"tree"`
	Printed    string   `json:"printed"`
	Tables     []string `json:"tables"`
	Diagnostic *struct {
		Start, End, At int
		Found          string
		Expected       []string
	} `json:"diagnostic"`
}

func fixtures(t testing.TB, path string) []fixture {
	t.Helper()
	data, err := os.ReadFile(path)
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
	engine, err := mysql.Engine(snapshot(text))
	require.NoError(t, err)
	return reference.Parse(t, engine, func(in *reference.Input) (p.Source, error) {
		cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
		return adapter.New(in.ID(), mysql.Scanner(cur), cur, p.Request{}, sql.Trivia())
	}, text, streamed, "")
}
func tableNames(root tree.Node) []string {
	result := []string{}
	for _, table := range sql.Tables(root) {
		var parts []string
		for _, part := range table.Name().Parts() {
			parts = append(parts, part.Value())
		}
		result = append(result, strings.Join(parts, "."))
	}
	return result
}
func TestFixtures(t *testing.T) {
	// Consume the core oracle directly; dialect work does not copy or edit it.
	for _, path := range []string{"../core/testdata/common.json", "testdata/mysql.json"} {
		for _, tc := range fixtures(t, path) {
			t.Run(tc.Name, func(t *testing.T) {
				for _, streamed := range []bool{false, true} {
					root := parse(t, tc.SQL, streamed)
					got := sql.Canonical(root)
					require.Equal(t, tc.Tree, got, "tree=%s\nwant %s", got, tc.Tree)
					problems := sql.Diagnostics(root)
					if tc.Diagnostic != nil {
						require.Len(t, problems, 1, "diagnostics=%v", problems)
						problem := problems[0].Problem()
						found := problem.Found.Token.Text
						if problem.Found.End {
							found = "<eof>"
						}
						expected := []p.Pattern{}
						for _, kind := range tc.Diagnostic.Expected {
							if kind == "<eof>" {
								expected = append(expected, p.EndOfFile())
							} else {
								expected = append(expected, p.Kind(p.TokenKind(kind)))
							}
							// MySQL adds one name token alternative after the core quoted name.
							if kind == string(sql.QuotedNameToken) {
								expected = append(expected, p.Kind(mysql.BacktickNameToken))
							}
						}
						require.Equal(t, tc.Diagnostic.Start, problem.Span.Start, "diagnostic=%+v want %+v expected=%v", problem, *tc.Diagnostic, expected)
						require.Equal(t, tc.Diagnostic.End, problem.Span.End, "diagnostic=%+v want %+v expected=%v", problem, *tc.Diagnostic, expected)
						require.Equal(t, tc.Diagnostic.At, problem.Found.Point.Start, "diagnostic=%+v want %+v expected=%v", problem, *tc.Diagnostic, expected)
						require.Equal(t, tc.Diagnostic.Found, found, "diagnostic=%+v want %+v expected=%v", problem, *tc.Diagnostic, expected)
						require.Equal(t, expected, problem.Expected, "diagnostic=%+v want %+v expected=%v", problem, *tc.Diagnostic, expected)
						_, err := mysql.Print(root)
						require.Error(t, err, "printed diagnostic")
						continue
					}
					require.Len(t, problems, 0, "unexpected diagnostic=%v", problems)
					require.Equal(t, tc.Tables, tableNames(root), "tables for %s", tc.Name)
					printed, err := mysql.Print(root)
					require.NoError(t, err, "print=%q,%v want %q", printed, err, tc.Printed)
					require.Equal(t, tc.Printed, printed, "print=%q,%v want %q", printed, err, tc.Printed)
					reparsed := parse(t, printed, false)
					require.Len(t, sql.Diagnostics(reparsed), 0, "roundtrip=%s", sql.Canonical(reparsed))
					require.Equal(t, tc.Tree, sql.Canonical(reparsed), "roundtrip=%s", sql.Canonical(reparsed))
					direct, err := mysql.Parse(snapshot(tc.SQL))
					require.NoError(t, err, "Parse=%v,%v", direct, err)
					require.Len(t, sql.Diagnostics(direct), 0, "Parse=%v,%v", direct, err)
					require.Equal(t, tc.Tree, sql.Canonical(direct), "Parse=%v,%v", direct, err)
				}
			})
		}
	}
}
func TestStraightJoinAndRanges(t *testing.T) {
	text := "SELECT `a``b`, `名` FROM `Left` AS l /* gap */ STRAIGHT_JOIN `Right` AS r ON l.id = r.id STRAIGHT_JOIN tail"
	for _, streamed := range []bool{false, true} {
		root := parse(t, text, streamed)
		require.Len(t, sql.Diagnostics(root), 0, sql.Canonical(root))
		var joins []*mysql.StraightJoin
		var escaped *sql.Lexeme
		for node := range tree.Walk(root) {
			if join, ok := node.(*mysql.StraightJoin); ok {
				joins = append(joins, join)
			}
			if leaf, ok := node.(*sql.Lexeme); ok {
				start := leaf.Range().Start
				require.Equal(t, snapshot(text).ID(), leaf.Snapshot().ID(), "snapshot range lost")
				require.Equal(t, leaf.Raw(), text[start:leaf.Range().End], "snapshot range lost")
				if leaf.Raw() == "`a``b`" {
					escaped = leaf
				}
			}
		}
		require.NotNil(t, escaped, "escaped name=%+v", escaped)
		require.Equal(t, "a`b", escaped.Value(), "escaped name=%+v", escaped)
		require.True(t, escaped.Quoted(), "escaped name=%+v", escaped)
		require.Equal(t, (tree.Span{Source: snapshot(text).ID(), Start: 7, End: 13}), escaped.Range(), "escaped name=%+v", escaped)
		require.Len(t, joins, 2, "joins=%v", joins)
		require.False(t, joins[0].Condition() == nil, "joins=%v", joins)
		require.True(t, joins[1].Condition() == nil, "joins=%v", joins)
		require.NotEmpty(t, joins[0].Table().Name().Parts())
		require.Equal(t, "Right", joins[0].Table().Name().Parts()[0].Value(), "right operand lost")
		require.Equal(t, "r", joins[0].Table().Alias().Value(), "right operand lost")
		require.Equal(t, (tree.Span{Source: snapshot(text).ID(), Start: strings.Index(text, " /* gap */"), End: strings.Index(text, " STRAIGHT_JOIN tail")}), joins[0].Info().Span, "join range=%+v", joins[0].Info().Span)
		got := tableNames(root)
		require.Equal(t, []string{"Left", "Right", "tail"}, got, "visitor=%v", got)
		for _, i := range []int{-1, 2, 99} {
			n, ok := joins[0].Child(i)
			require.True(t, n == nil, "invalid child")
			require.False(t, ok, "invalid child")
		}
		n, ok := joins[1].Child(1)
		require.True(t, n == nil, "absent condition child")
		require.False(t, ok, "absent condition child")
		_, err := sql.Print(root)
		require.Error(t, err, "portable printer erased ordered-join semantics")
		coreRoot, err := sql.Parse(snapshot(text))
		require.NoError(t, err, "core acquired dialect grammar: %v", err)
		require.NotEmpty(t, sql.Diagnostics(coreRoot), "core acquired dialect grammar: %v", err)
	}
}
func TestRejectedSyntax(t *testing.T) {
	for _, text := range []string{
		"SELECT id FROM users STRAIGHT_JOIN", "SELECT id FROM users STRAIGHT_JOIN teams ON", "SELECT id FROM users STRAIGHT_JOIN teams ON id =", "SELECT id FROM users STRAIGHT_JOIN JOIN teams ON id = id", "SELECT STRAIGHT_JOIN id FROM users", "SELECT id FROM users STRAIGHT_JOIN teams USING (id)", "SELECT id FROM users STRAIGHT_JOIN teams ON id = id AND id = id", "SELECT id FROM users LIMIT 2", "SELECT [id] FROM users", "SELECT `` FROM users", "SELECT `unclosed FROM users", "SELECT `a\nb` FROM users", "SELECT `a\rb` FROM users", "SELECT id FROM users; SELECT id FROM teams",
	} {
		t.Run(text, func(t *testing.T) {
			a, b := parse(t, text, false), parse(t, text, true)
			da, db := sql.Diagnostics(a), sql.Diagnostics(b)
			require.Len(t, da, 1, "diagnostics=%v / %v", da, db)
			require.Len(t, db, 1, "diagnostics=%v / %v", da, db)
			require.Equal(t, db[0].Problem(), da[0].Problem(), "split diagnostic differs")
			_, err := mysql.Print(a)
			require.Error(t, err, "printed rejected syntax")
		})
	}
}
func TestLexicalMode(t *testing.T) {
	text := "select straight_joined, `STRAIGHT_JOIN`, \"a\"\"b\", 'x\\y', 'it''s' from `a``b`"
	const expected = "(sql:file (sql:select (sql:projection (sql:identifier (sql:name \"straight_joined\"))) (sql:projection (sql:identifier (sql:name \"STRAIGHT_JOIN\"))) (sql:projection (sql:identifier (sql:name \"a\\\"b\"))) (sql:projection (sql:string \"x\\\\y\")) (sql:projection (sql:string \"it's\")) (sql:table (sql:identifier (sql:name \"a`b\")))))"
	for _, streamed := range []bool{false, true} {
		root := parse(t, text, streamed)
		require.Len(t, sql.Diagnostics(root), 0, "mode=%s", sql.Canonical(root))
		require.Equal(t, expected, sql.Canonical(root), "mode=%s", sql.Canonical(root))
		printed, err := mysql.Print(root)
		require.NoError(t, err, "mode roundtrip=%q,%v", printed, err)
		require.Equal(t, expected, sql.Canonical(parse(t, printed, streamed)), "mode roundtrip=%q,%v", printed, err)
	}
}
func ExampleParse() {
	root, err := mysql.Parse(sql.NewSnapshot("example", "SELECT `u`.`id` FROM `users` AS u STRAIGHT_JOIN teams AS t ON u.team_id = t.id"))
	if err != nil || len(sql.Diagnostics(root)) != 0 {
		panic("invalid example")
	}
	text, err := mysql.Print(root)
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(tableNames(root))
	// Output:
	// SELECT "u"."id" FROM "users" AS u STRAIGHT_JOIN teams AS t ON u.team_id = t.id
	// [users teams]
}
