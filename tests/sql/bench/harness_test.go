package bench_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	scan "github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	adapter "github.com/xiam/leafkit/adapter/bytescan"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tests/sql/mysql"
	"github.com/xiam/leafkit/tests/sql/postgres"
	"github.com/xiam/leafkit/tests/sql/tsql"
	"github.com/xiam/leafkit/tree"
)

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

type fixture struct {
	Name   string   `json:"name"`
	SHA256 string   `json:"sha256"`
	Tables []string `json:"tables"`
	text   string
}

func matchedFixtures(t testing.TB) []fixture {
	t.Helper()
	data, err := os.ReadFile("testdata/matched.json")
	require.NoError(t, err)
	var fixtures []fixture
	require.NoError(t, json.Unmarshal(data, &fixtures))
	for i := range fixtures {
		f := &fixtures[i]
		data, err := os.ReadFile("testdata/" + f.Name + ".sql")
		require.NoError(t, err)
		got := fmt.Sprintf("%x", sha256.Sum256(data))
		require.Equal(t, f.SHA256, got, "%s fixture digest = %s, want %s", f.Name, got, f.SHA256)
		f.text = string(data)
	}
	return fixtures
}

// feed exposes the same immutable bytes with either final EOF or 64-byte
// increments. The final partial chunk and EOF arrive together.
type feed struct {
	text              string
	offset, available int
	final             bool
}

func (f *feed) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if f.offset == f.available {
		if f.final {
			return 0, io.EOF
		}
		return 0, scan.ErrMoreInput
	}
	n := copy(dst, f.text[f.offset:f.available])
	f.offset += n
	return n, nil
}

type runner struct {
	snapshot *core.Snapshot
	engine   *p.Engine[tree.Node]
	scanner  func(*cursor.Reader) *scan.Scanner
}

func prepare(t testing.TB, d dialect, text string) runner {
	t.Helper()
	snapshot := core.NewSnapshot("benchmark", text)
	engine, err := d.engine(snapshot)
	require.NoError(t, err)
	return runner{snapshot, engine, d.scanner}
}

func (r runner) parse(streamed bool) (tree.Node, error) {
	in := &feed{text: r.snapshot.Text()}
	if !streamed {
		in.available, in.final = len(in.text), true
	}
	cur := cursor.NewReader(in)
	source, err := adapter.New(r.snapshot.ID(), r.scanner(cur), cur, p.Request{}, core.Trivia())
	if err != nil {
		return nil, err
	}
	session := r.engine.Start(source, "")
	for {
		root, status, err := session.Run()
		if err != nil {
			return nil, err
		}
		if status == p.Done {
			if !in.final || root == nil || root.Info().Span != (tree.Span{Source: r.snapshot.ID(), End: len(in.text)}) {
				return nil, fmt.Errorf("incomplete consumption")
			}
			return root, nil
		}
		if !streamed || in.final || status != p.Paused || root != nil {
			return nil, fmt.Errorf("unexpected pause/status %v", status)
		}
		in.available = min(in.available+64, len(in.text))
		in.final = in.available == len(in.text)
	}
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

func checked(t testing.TB, r runner, streamed, rejected bool, tables []string) tree.Node {
	t.Helper()
	root, err := r.parse(streamed)
	require.NoError(t, err)
	require.NoError(t, tree.Validate(root))
	problems := core.Diagnostics(root)
	if rejected {
		require.Len(t, problems, 1, "want one diagnostic, got %v", problems)
	} else {
		require.Empty(t, problems, "unexpected diagnostics: %+v", problems)
	}
	require.Equal(t, tables, tableNames(root), "tables = %v, want %v", tableNames(root), tables)
	return root
}

// checkMatched is called by both the ordinary test and the benchmark, so
// -run '^$' cannot bypass equivalence validation before timing.
func checkMatched(t testing.TB, f fixture) []runner {
	t.Helper()
	var runners []runner
	var want string
	for _, d := range dialects {
		r := prepare(t, d, f.text)
		for _, streamed := range []bool{false, true} {
			root := checked(t, r, streamed, false, f.Tables)
			got := core.Canonical(root)
			if want == "" {
				want = got
			}
			require.Equal(t, want, got, "%s/%s streamed=%v differs from core whole tree", f.Name, d.name, streamed)
		}
		runners = append(runners, r)
	}
	return runners
}

func TestMatched(t *testing.T) {
	for _, f := range matchedFixtures(t) {
		t.Run(f.Name, func(t *testing.T) { checkMatched(t, f) })
	}
}
