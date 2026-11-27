package translate_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiam/leafkit/tree"

	"github.com/stretchr/testify/require"

	"github.com/xiam/leafkit/tests/sql/core"
	tr "github.com/xiam/leafkit/tests/sql/translate"
	"github.com/xiam/leafkit/tests/sql/tsql"
)

type tiesFixture struct {
	SQL, Lossy string
	SourceRows [][]int `json:"source_rows"`
	LossyRows  [][]int `json:"lossy_rows"`
}

func ties(t testing.TB) tiesFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/ties.json")
	require.NoError(t, err)
	var tc tiesFixture
	require.NoError(t, json.Unmarshal(data, &tc))
	return tc
}
func TestBoundaryTieDiagnostic(t *testing.T) {
	tc := ties(t)
	source := parse(t, tsql.Parse, tc.SQL)
	result, ds := tr.ToPostgres(source, key)
	require.True(t, result == nil, "tie case lost: %+v", ds)
	require.Len(t, ds, 1, "tie case lost: %+v", ds)
	require.Equal(t, "with-ties", ds[0].Code, "tie case lost: %+v", ds)
	require.Equal(t, "WITH TIES", strings.TrimSpace(tc.SQL[ds[0].Span.Start:ds[0].Span.End]), "tie case lost: %+v", ds)
	require.Len(t, tc.SourceRows, 4, "boundary tie oracle")
	require.Len(t, tc.LossyRows, 3, "boundary tie oracle")
}

// TestEngineParity is opt-in. It uses two dedicated, caller-owned containers;
// setup.sql replaces only their leafkit_translate table. No host port is needed.
func TestEngineParity(t *testing.T) {
	if os.Getenv("LEAFKIT_ENGINE_PARITY") != "1" {
		t.Skip("set LEAFKIT_ENGINE_PARITY=1 and container variables; see engines.md")
	}
	pg, ms, password := os.Getenv("LEAFKIT_PG_CONTAINER"), os.Getenv("LEAFKIT_TSQL_CONTAINER"), os.Getenv("LEAFKIT_TSQL_PASSWORD")
	require.NotEqual(t, "", pg, "both dedicated containers and SQL Server password are required")
	require.NotEqual(t, "", ms, "both dedicated containers and SQL Server password are required")
	require.NotEqual(t, "", password, "both dedicated containers and SQL Server password are required")
	run := func(t testing.TB, engine, query string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var args []string
		if engine == "postgres" {
			args = []string{"exec", "-i", pg, "psql", "-X", "-qAt", "-F", ",", "-U", "postgres", "-v", "ON_ERROR_STOP=1"}
		} else {
			args = []string{"exec", "-i", "-e", "SQLCMDPASSWORD", ms, "/opt/mssql-tools18/bin/sqlcmd", "-S", "tcp:127.0.0.1,1433", "-U", "sa", "-C", "-b", "-h", "-1", "-W", "-s", ","}
			query = "SET NOCOUNT ON; SET QUOTED_IDENTIFIER ON;\n" + query
		}
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = append(os.Environ(), "SQLCMDPASSWORD="+password)
		cmd.Stdin = strings.NewReader(query + "\n")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s failed: %s", engine, out)
		return strings.TrimSpace(string(out))
	}
	rows := func(t testing.TB, output string) [][]int {
		t.Helper()
		result := [][]int{}
		for _, line := range strings.Split(output, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row []int
			for _, cell := range strings.Split(line, ",") {
				n, err := strconv.Atoi(strings.TrimSpace(cell))
				require.NoError(t, err, "non-integer engine row %q", line)
				row = append(row, n)
			}
			result = append(result, row)
		}
		return result
	}
	t.Log("PostgreSQL:", run(t, "postgres", "SHOW server_version;"))
	t.Log("SQL Server:", run(t, "tsql", "SELECT SERVERPROPERTY('ProductVersion');"))
	setup, err := os.ReadFile("testdata/setup.sql")
	require.NoError(t, err)
	run(t, "postgres", string(setup))
	run(t, "tsql", string(setup))
	for _, tc := range fixtures(t) {
		t.Run(tc.Name, func(t *testing.T) {
			source := parse(t, tsql.Parse, tc.SQL)
			_, translated := target(t, source, key)
			originalRows, targetRows := rows(t, run(t, "tsql", tc.SQL)), rows(t, run(t, "postgres", translated))
			require.Equal(t, tc.Rows, originalRows, "SQL Server=%v PostgreSQL=%v oracle=%v", originalRows, targetRows, tc.Rows)
			require.Equal(t, tc.Rows, targetRows, "SQL Server=%v PostgreSQL=%v oracle=%v", originalRows, targetRows, tc.Rows)
		})
	}
	tc := ties(t)
	source := rows(t, run(t, "tsql", tc.SQL))
	lossy := rows(t, run(t, "postgres", tc.Lossy))
	require.Equal(t, tc.SourceRows, source, "boundary ties: SQL Server=%v lossy LIMIT=%v", source, lossy)
	require.Equal(t, tc.LossyRows, lossy, "boundary ties: SQL Server=%v lossy LIMIT=%v", source, lossy)
	// Re-run the review counterexample explicitly. Its second row's identity
	// is unspecified within score=20, so do not bake an accidental engine
	// tie-breaking choice into a supposedly deterministic expected-row test.
	shadow := aliasCase(t)
	sourceShadow := rows(t, run(t, "tsql", shadow.SQL))
	unchecked := rows(t, run(t, "postgres", shadow.UncheckedTarget))
	t.Logf("alias-shadowing source rows=%v unchecked historical target rows=%v", sourceShadow, unchecked)
	for _, result := range [][][]int{sourceShadow, unchecked} {
		require.Len(t, result, 2, "unexpected alias counterexample rows=%v", result)
		require.Equal(t, []int{1, 10}, result[0], "unexpected alias counterexample rows=%v", result)
		require.Len(t, result[1], 2, "unexpected alias counterexample rows=%v", result)
		require.GreaterOrEqual(t, result[1][0], 2, "unexpected alias counterexample rows=%v", result)
		require.LessOrEqual(t, result[1][0], 4, "unexpected alias counterexample rows=%v", result)
		require.Equal(t, 20, result[1][1], "unexpected alias counterexample rows=%v", result)
	}
	TestAliasShadowingDiagnostic(t)
	// ToPostgres rejects the source before printing or executing a target.
	TestBoundaryTieDiagnostic(t)
}

type aliasFixture struct {
	SQL             string
	UncheckedTarget string `json:"unchecked_target"`
}

func aliasCase(t testing.TB) aliasFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/alias.json")
	require.NoError(t, err)
	var tc aliasFixture
	require.NoError(t, json.Unmarshal(data, &tc))
	return tc
}
func TestAliasShadowingDiagnostic(t *testing.T) {
	tc := aliasCase(t)
	source := parse(t, tsql.Parse, tc.SQL)
	before := core.Canonical(source)
	result, ds := tr.ToPostgres(source, key)
	require.True(t, result == nil, "shadowing result=%v diagnostic=%+v", result, ds)
	require.Len(t, ds, 1, "shadowing result=%v diagnostic=%+v", result, ds)
	require.Equal(t, "ordering-key", ds[0].Code, "shadowing result=%v diagnostic=%+v", result, ds)
	require.Equal(t, tree.SourceID("source"), ds[0].Span.Source, "shadowing result=%v diagnostic=%+v", result, ds)
	require.Equal(t, "id", strings.TrimSpace(tc.SQL[ds[0].Span.Start:ds[0].Span.End]), "shadowing result=%v diagnostic=%+v", result, ds)
	require.Equal(t, core.Canonical(source), before, "shadowing rejection changed source")
}
