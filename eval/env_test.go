package eval_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xiam/leafkit/eval"
)

func TestEnv(t *testing.T) {
	var root eval.Env[int]
	inner := eval.NewEnv(&root)
	root.Set("updated", 4)
	root.Set("shadowed", 7)
	inner.Set("shadowed", 0)
	{
		got, ok := inner.Get("updated")
		require.Truef(t, ok, "inherited binding = %d, %v", got, ok)
		require.Equalf(t, 4, got, "inherited binding = %d, %v", got, ok)
	}
	{
		got, ok := inner.Get("shadowed")
		require.Truef(t, ok, "shadowing zero binding = %d, %v", got, ok)
		require.Equalf(t, 0, got, "shadowing zero binding = %d, %v", got, ok)
	}
	{
		got, ok := root.Get("shadowed")
		require.Truef(t, ok, "outer binding = %d, %v", got, ok)
		require.Equalf(t, 7, got, "outer binding = %d, %v", got, ok)
	}
	var absent *eval.Env[int]
	for _, env := range []*eval.Env[int]{inner, absent} {
		{
			got, ok := env.Get("missing")
			require.Falsef(t, ok, "missing binding = %d, %v", got, ok)
			require.Equalf(t, 0, got, "missing binding = %d, %v", got, ok)
		}
	}
}
