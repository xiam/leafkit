package tests

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

func FuzzRecoveryTokenStream(f *testing.F) {
	const maxBytes = 512
	// The reference source emits at most one token per byte. It uses no
	// virtual tokens. The work budget leaves room for rejected productions
	// and replays of the bounded probes, including nested expressions.
	engine, err := p.Configure(recoveryStatements(), builders(), p.Limits{
		Depth: 4096, Steps: 250000, ZeroMoves: 16384, RetainedBytes: maxBytes,
	})
	require.NoError(f, err)
	for _, seed := range []string{
		"", "let a=;let b=2+3;let c=;", "f((1+));", "()+1;", "([[[", "!?\x00\xff", "2^3^2;",
		strings.Repeat("(", maxBytes), strings.Repeat("f(", maxBytes/2),
	} {
		f.Add([]byte(seed))
	}

	parse := func(t *testing.T, session *p.Session[tree.Node], end int) tree.Node {
		t.Helper()
		root := done(t, session)
		require.Equalf(t, span(0, end), root.Info().Span, "file span = %+v, want %+v", root.Info().Span, span(0, end))
		require.NoError(t, tree.Validate(root))
		return root
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxBytes {
			data = data[:maxBytes]
		}
		tokens := expressionTokens(string(data))
		end := len(data)
		available, final := end, true
		whole := parse(t, engine.Start(sourceFor(tokens, &end, &available, &final), ""), end)
		for _, node := range recoveryProblems(t, whole) {
			problem := node.Problem()
			require.Equalf(t, node.Info().Span, problem.Span, "unpositioned diagnostic: %+v", problem)
			require.Equalf(t, snapshot, problem.Span.Source, "unpositioned diagnostic: %+v", problem)
			require.LessOrEqualf(t, problem.Span.End, end, "unpositioned diagnostic: %+v", problem)
			require.Equalf(t, snapshot, problem.Found.Point.Source, "unpositioned diagnostic: %+v", problem)
			require.Equalf(t, problem.Found.Point.End, problem.Found.Point.Start, "unpositioned diagnostic: %+v", problem)
			require.LessOrEqualf(t, problem.Found.Point.End, end, "unpositioned diagnostic: %+v", problem)
			previous := problem.Span.Start
			for _, token := range problem.Skipped {
				require.Equalf(t, snapshot, token.Span.Source, "out of order or out of range skipped token: %+v in %+v", token, problem)
				require.GreaterOrEqualf(t, token.Span.Start, previous, "out of order or out of range skipped token: %+v in %+v", token, problem)
				require.LessOrEqualf(t, token.Span.End, problem.Span.End, "out of order or out of range skipped token: %+v in %+v", token, problem)
				previous = token.Span.End
			}
		}

		available, final = end/2, false
		session := engine.Start(sourceFor(tokens, &end, &available, &final), "")
		paused(t, session)
		available, final = end, true
		chunked := parse(t, session, end)
		equalRecords(t, chunked, records(whole))
		want, got := recoveryProblems(t, whole), recoveryProblems(t, chunked)
		require.Lenf(t, got, len(want), "chunked recovery has %d diagnostics, whole input has %d", len(got), len(want))
		for i, node := range got {
			require.Equalf(t, want[i].Slot(), node.Slot(), "chunked diagnostic %d is %+v, want %+v", i, node.Problem(), want[i].Problem())
			require.Equalf(t, want[i].Problem(), node.Problem(), "chunked diagnostic %d is %+v, want %+v", i, node.Problem(), want[i].Problem())
		}
	})
}
