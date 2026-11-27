package postgres_test

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
	pg "github.com/xiam/leafkit/tests/sql/postgres"
	"github.com/xiam/leafkit/tree"
)

func snapshot(text string) *core.Snapshot {
	return core.NewSnapshot(tree.SourceID(fmt.Sprintf("reference:%x", sha256.Sum256([]byte(text)))), text)
}
func parse(t testing.TB, text string, chunked bool) tree.Node {
	t.Helper()
	engine, err := pg.Engine(snapshot(text))
	require.NoError(t, err)
	return reference.Parse(t, engine, func(in *reference.Input) (p.Source, error) {
		cur := cursor.NewReader(in.Open(0, scan.ErrMoreInput))
		return adapter.New(in.ID(), pg.Scanner(cur), cur, p.Request{}, core.Trivia())
	}, text, chunked, "")
}
func roundtrip(t testing.TB, root tree.Node, want string) {
	t.Helper()
	require.Len(t, core.Diagnostics(root), 0, "unexpected diagnostics: %+v", core.Diagnostics(root))
	printed, err := pg.Print(root)
	require.NoError(t, err, "Print=%q,%v want %q", printed, err, want)
	require.Equal(t, want, printed, "Print=%q,%v want %q", printed, err, want)
	again, err := pg.Parse(core.NewSnapshot("roundtrip", printed))
	require.NoError(t, err, "roundtrip changed tree: %s -> %v (%v)", core.Canonical(root), again, err)
	require.Len(t, core.Diagnostics(again), 0, "roundtrip changed tree: %s -> %v (%v)", core.Canonical(root), again, err)
	require.Equal(t, core.Canonical(again), core.Canonical(root), "roundtrip changed tree: %s -> %v (%v)", core.Canonical(root), again, err)
}

type fixture struct {
	Name, SQL, Tree, Printed string
	Diagnostic               *struct {
		Start, End, At int
		Found          string
	}
}

func fixtures(t testing.TB, path string) []fixture {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var cases []fixture
	require.NoError(t, json.Unmarshal(data, &cases))
	return cases
}

func TestPortableFixtures(t *testing.T) {
	for _, tc := range fixtures(t, "../core/testdata/common.json") {
		// LIMIT is the one core-negative fixture accepted by this extension.
		// TestLimit pins its target tree, rendering and whole/chunk behavior.
		if tc.Name == "unsupported tail" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			for _, chunked := range []bool{false, true} {
				root := parse(t, tc.SQL, chunked)
				require.Equal(t, tc.Tree, core.Canonical(root), "changed portable tree: %s", core.Canonical(root))
				if tc.Diagnostic == nil {
					roundtrip(t, root, tc.Printed)
				} else {
					checkDiagnostic(t, root, tc)
				}
			}
		})
	}
}
func checkDiagnostic(t testing.TB, root tree.Node, tc fixture) {
	t.Helper()
	ds := core.Diagnostics(root)
	require.Len(t, ds, 1, "diagnostics=%v", ds)
	d := ds[0].Problem()
	found := d.Found.Token.Text
	if d.Found.End {
		found = "<eof>"
	}
	require.Equal(t, tc.Diagnostic.Start, d.Span.Start, "diagnostic=%+v want %+v", d, tc.Diagnostic)
	require.Equal(t, tc.Diagnostic.End, d.Span.End, "diagnostic=%+v want %+v", d, tc.Diagnostic)
	require.Equal(t, tc.Diagnostic.At, d.Found.Point.Start, "diagnostic=%+v want %+v", d, tc.Diagnostic)
	require.Equal(t, tc.Diagnostic.Found, found, "diagnostic=%+v want %+v", d, tc.Diagnostic)
	_, err := pg.Print(root)
	require.Error(t, err, "printer accepted diagnostic")
}
func TestDialectFixtures(t *testing.T) {
	for _, tc := range fixtures(t, "testdata/postgres.json") {
		t.Run(tc.Name, func(t *testing.T) {
			whole, chunked := parse(t, tc.SQL, false), parse(t, tc.SQL, true)
			require.Equal(t, tc.Tree, core.Canonical(whole), "tree=%s streamed=%s want %s", core.Canonical(whole), core.Canonical(chunked), tc.Tree)
			require.Equal(t, tc.Tree, core.Canonical(chunked), "tree=%s streamed=%s want %s", core.Canonical(whole), core.Canonical(chunked), tc.Tree)
			for _, root := range []tree.Node{whole, chunked} {
				if tc.Diagnostic == nil {
					roundtrip(t, root, tc.Printed)
				} else {
					checkDiagnostic(t, root, tc)
				}
			}
			if tc.Diagnostic != nil {
				require.Equal(t, core.Diagnostics(whole)[0].Problem(), core.Diagnostics(chunked)[0].Problem(), "streamed diagnostic differs")
			}
		})
	}
}

func TestTypedModifierAndSharedVisitor(t *testing.T) {
	text := "SELECT DISTINCT ON (u.id, t.name) u.id FROM app.users AS u INNER JOIN teams AS t ON u.team_id = t.id ORDER BY u.id, t.name, t.id DESC"
	for _, chunked := range []bool{false, true} {
		root := parse(t, text, chunked)
		roundtrip(t, root, text)
		selNode, exists := root.Child(0)
		require.True(t, exists, "missing select")
		sel, ok := selNode.(*core.Select)
		require.True(t, ok, "select type=%T", selNode)
		require.NotNil(t, sel)
		modifier, ok := sel.One("modifiers").(*pg.DistinctOn)
		require.True(t, ok, "modifier=%v", modifier)
		require.NotNil(t, modifier)
		require.Len(t, modifier.Expressions(), 2, "modifier=%v", modifier)
		require.Equal(t, pg.DistinctOnKind, modifier.Info().Kind, "modifier=%v", modifier)
		parts := modifier.Expressions()
		_, ok = parts[0].(*core.Identifier)
		require.True(t, ok, "expression type=%T", parts[0])
		parts[0] = nil
		require.False(t, modifier.Expressions()[0] == nil, "mutable children")
		for _, i := range []int{-1, 2} {
			n, ok := modifier.Child(i)
			require.False(t, ok, "out of bounds child")
			require.True(t, n == nil, "out of bounds child")
		}
		raw, err := snapshot(text).Raw(modifier.Info().Span)
		require.NoError(t, err, "modifier span=%q,%v", raw, err)
		require.Equal(t, "DISTINCT ON (u.id, t.name)", strings.TrimSpace(raw), "modifier span=%q,%v", raw, err)
		var tables []string
		for _, table := range core.Tables(root) {
			var names []string
			for _, part := range table.Name().Parts() {
				names = append(names, part.Value())
			}
			tables = append(tables, strings.Join(names, "."))
		}
		require.Equal(t, []string{"app.users", "teams"}, tables, "tables=%v", tables)
		_, err = core.Print(root)
		require.Error(t, err, "core printer dropped dialect modifier")
	}
	coreRoot, err := core.Parse(snapshot(text))
	require.NoError(t, err, "core acquired dialect grammar")
	require.NotEmpty(t, core.Diagnostics(coreRoot), "core acquired dialect grammar")
}

func TestDollarSpellingValueAndReuse(t *testing.T) {
	for _, raw := range []string{"$$$$", "$tag$it'$other$\\λ\n$tag$", "$Tag_2$a$tag_2$b$Tag_2$", "$a$a$ax$aa$a$"} {
		text := "SELECT " + raw + " FROM users"
		for _, chunked := range []bool{false, true} {
			root := parse(t, text, chunked)
			require.Len(t, core.Diagnostics(root), 0, "dollar literal rejected: %q", raw)
			var literal *core.Lexeme
			for n := range tree.Walk(root) {
				if leaf, ok := n.(*core.Lexeme); ok && leaf.Info().Kind == core.StringKind {
					literal = leaf
				}
			}
			width := strings.IndexByte(raw[1:], '$') + 2
			value := raw[width : len(raw)-width]
			require.NotNil(t, literal, "literal=%+v", literal)
			require.Equal(t, raw, literal.Raw(), "literal=%+v", literal)
			require.Equal(t, value, literal.Value(), "literal=%+v", literal)
			require.True(t, literal.Quoted(), "literal=%+v", literal)
			require.Equal(t, (tree.Span{Source: snapshot(text).ID(), Start: 7, End: 7 + len(raw)}), literal.Range(), "literal=%+v", literal)
			require.Equal(t, text, literal.Snapshot().Text(), "snapshot reference lost")
			roundtrip(t, root, "SELECT '"+strings.ReplaceAll(value, "'", "''")+"' FROM users")
		}
	}
	text := "SELECT $a$first$a$, $$second$$, $b$third$b$ FROM users"
	roundtrip(t, parse(t, text, true), "SELECT 'first', 'second', 'third' FROM users")
	large := strings.Repeat("λ '$x$ ", 4096)
	snap := core.NewSnapshot("large", "SELECT $payload$"+large+"$payload$ FROM users")
	root, err := pg.Parse(snap)
	require.NoError(t, err, "large literal parse=%v", err)
	require.Len(t, core.Diagnostics(root), 0, "large literal parse=%v", err)
	for n := range tree.Walk(root) {
		if leaf, ok := n.(*core.Lexeme); ok && leaf.Info().Kind == core.StringKind {
			require.Same(t, snap, leaf.Snapshot(), "large value or snapshot changed")
			require.Equal(t, large, leaf.Value(), "large value or snapshot changed")
		}
	}
}

func TestRejectedDialectSyntax(t *testing.T) {
	for _, text := range []string{
		"SELECT DISTINCT ON () id FROM users", "SELECT DISTINCT ON (id,) id FROM users", "SELECT DISTINCT ON id FROM users", "SELECT DISTINCT ON (id id FROM users", "SELECT DISTINCT ON (id) FROM users", "SELECT DISTINCT ON (*) id FROM users", "SELECT DISTINCT ON (id) DISTINCT id FROM users",
		"SELECT $x$unclosed FROM users", "SELECT $x$wrong$X$ FROM users", "SELECT $1$bad$1$ FROM users", "SELECT $x-y$bad$x-y$ FROM users", "SELECT$x$body$x$ FROM users", "SELECT $x$a\x00b$x$ FROM users", "SELECT $$a$$x FROM users", "SELECT ALL id FROM users",
	} {
		t.Run(text, func(t *testing.T) {
			whole, chunked := parse(t, text, false), parse(t, text, true)
			require.Len(t, core.Diagnostics(whole), 1, "invalid syntax accepted")
			require.Len(t, core.Diagnostics(chunked), 1, "invalid syntax accepted")
			require.Equal(t, core.Diagnostics(chunked)[0].Problem(), core.Diagnostics(whole)[0].Problem(), "diagnostics differ")
			_, err := pg.Print(whole)
			require.Error(t, err, "printer accepted diagnostic")
		})
	}
}

func ExampleParse() {
	root, err := pg.Parse(core.NewSnapshot("example", "SELECT DISTINCT ON (u.id) u.id, $tag$it's ready$tag$ FROM users AS u ORDER BY u.id, u.updated DESC"))
	if err != nil || len(core.Diagnostics(root)) != 0 {
		panic("invalid example")
	}
	text, err := pg.Print(root)
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	fmt.Println(core.Tables(root)[0].Name().Parts()[0].Value())
	// Output:
	// SELECT DISTINCT ON (u.id) u.id, 'it''s ready' FROM users AS u ORDER BY u.id, u.updated DESC
	// users
}

func TestDollarNamesAndBoundaries(t *testing.T) {
	text := "SELECT foo$Bar, selected$tag$ FROM order$By"
	root := parse(t, text, true)
	roundtrip(t, root, "SELECT foo$bar, selected$tag$ FROM order$by")
	var names []string
	for n := range tree.Walk(root) {
		if leaf, ok := n.(*core.Lexeme); ok {
			names = append(names, leaf.Value())
		}
	}
	require.Equal(t, []string{"foo$bar", "selected$tag$", "order$by"}, names, "names=%v", names)
}

func TestLimit(t *testing.T) {
	for _, count := range []string{"0", "0003", "3", "9223372036854775807"} {
		text := "SELECT id FROM users ORDER BY id LIMIT " + count
		for _, chunked := range []bool{false, true} {
			root := parse(t, text, chunked)
			roundtrip(t, root, text)
			selNode, ok := root.Child(0)
			require.True(t, ok, "missing select")
			sel, ok := selNode.(*core.Select)
			require.True(t, ok, "select type=%T", selNode)
			require.NotNil(t, sel)
			limit, ok := sel.One("suffix").(*pg.Limit)
			require.True(t, ok, "missing LIMIT")
			require.NotNil(t, limit)
			require.Equal(t, count, limit.Count().Value(), "lost LIMIT count")
			require.Equal(t, pg.LimitKind, limit.Info().Kind, "lost LIMIT count")
			for _, i := range []int{-1, 1} {
				_, ok := limit.Child(i)
				require.False(t, ok, "invalid LIMIT child")
			}
		}
	}
	for _, tail := range []string{"LIMIT", "LIMIT -1", "LIMIT 1.5", "LIMIT ALL", "LIMIT 9223372036854775808", "LIMIT 1 OFFSET 1", "LIMIT (1)", "LIMIT 1 LIMIT 2"} {
		whole, chunked := parse(t, "SELECT id FROM users "+tail, false), parse(t, "SELECT id FROM users "+tail, true)
		require.Len(t, core.Diagnostics(whole), 1, "accepted %s", tail)
		require.Len(t, core.Diagnostics(chunked), 1, "accepted %s", tail)
		require.Equal(t, core.Diagnostics(chunked)[0].Problem(), core.Diagnostics(whole)[0].Problem(), "LIMIT chunk diagnostic changed")
	}
	roundtrip(t, parse(t, "SELECT limit$tag$, limited FROM t", true), "SELECT limit$tag$, limited FROM t")
}
