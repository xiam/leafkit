package bench_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xiam/leafkit/tests/sql/core"
	"github.com/xiam/leafkit/tree"
)

var result tree.Node

func measure(b *testing.B, r runner, streamed bool) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(len(r.snapshot.Text())))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root, err := r.parse(streamed)
		if err != nil {
			b.Fatal(err)
		}
		result = root
	}
}

func schedules(b *testing.B, r runner) {
	for _, schedule := range []struct {
		name     string
		streamed bool
	}{{"whole", false}, {"stream64", true}} {
		b.Run(schedule.name, func(b *testing.B) { measure(b, r, schedule.streamed) })
	}
}

func BenchmarkMatched(b *testing.B) {
	for _, f := range matchedFixtures(b) {
		runners := checkMatched(b, f)
		b.Run(f.Name, func(b *testing.B) {
			for i, d := range dialects {
				b.Run(d.name, func(b *testing.B) { schedules(b, runners[i]) })
			}
		})
	}
}

// These are separate workloads, never used to compare dialect parser costs.
var native = []struct {
	dialect int
	text    string
	kind    tree.Kind
}{
	{1, "SELECT DISTINCT ON (id) id, $$value$$ FROM users ORDER BY id;", "postgres:distinct-on"},
	{2, "SELECT `u`.`id` FROM `users` AS `u` STRAIGHT_JOIN `teams` AS `t` ON u.team_id = t.id;", "mysql:straight-join"},
	{3, "SELECT TOP (2) WITH TIES [id] FROM [users] ORDER BY [id];", "tsql:top"},
}

func checkNative(t testing.TB, index int) runner {
	t.Helper()
	f := native[index]
	r := prepare(t, dialects[f.dialect], f.text)
	tables := []string{"users"}
	if f.dialect == 2 {
		tables = append(tables, "teams")
	}
	a := checked(t, r, false, false, tables)
	b := checked(t, r, true, false, tables)
	require.Equal(t, core.Canonical(b), core.Canonical(a), "native streaming tree differs")
	found := false
	for node := range tree.Walk(a) {
		found = found || node.Info().Kind == f.kind
	}
	require.True(t, found, "missing native node %s", f.kind)
	return r
}

func BenchmarkDialectOnly(b *testing.B) {
	for i, f := range native {
		r := checkNative(b, i)
		b.Run(dialects[f.dialect].name, func(b *testing.B) { schedules(b, r) })
	}
}

const rejection = "SELECT id FROM users WHERE id = ;"

func checkRejection(t testing.TB) []runner {
	t.Helper()
	var runners []runner
	for _, d := range dialects {
		var want string
		r := prepare(t, d, rejection)
		for _, streamed := range []bool{false, true} {
			root := checked(t, r, streamed, true, nil)
			problem := core.Diagnostics(root)[0].Problem()
			// Include the expected/found token and ranges, not just the tree kind.
			got := fmt.Sprint(problem)
			if want == "" {
				want = got
			}
			require.Equal(t, want, got, "%s rejection differs: %s / %s", d.name, got, want)
			require.Equal(t, 32, problem.Found.Point.Start, "rejection at %d, want 32", problem.Found.Point.Start)
		}
		runners = append(runners, r)
	}
	return runners
}

func BenchmarkRejection(b *testing.B) {
	for i, r := range checkRejection(b) {
		b.Run(dialects[i].name, func(b *testing.B) { schedules(b, r) })
	}
}

func TestSeparateWorkloads(t *testing.T) {
	for i, f := range native {
		t.Run(dialects[f.dialect].name, func(t *testing.T) { checkNative(t, i) })
	}
	t.Run("rejection", func(t *testing.T) { checkRejection(t) })
}
