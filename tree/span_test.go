package tree

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPositions(t *testing.T) {
	source := SourceID("revision-1")
	lookup := func(id SourceID) ([]byte, bool) {
		return []byte{'a', 0xc3, 0xa9, 0xff, '\r', '\n', 'b', '\n'}, id == source
	}
	tests := []struct {
		name       string
		span       Span
		start, end Position
	}{
		{"origin point", Span{source, 0, 0}, Position{1, 1}, Position{1, 1}},
		{"bytes and invalid UTF-8", Span{source, 2, 7}, Position{1, 3}, Position{2, 2}},
		{"CR counts as a byte", Span{source, 4, 5}, Position{1, 5}, Position{1, 6}},
		{"EOF after LF", Span{source, 8, 8}, Position{3, 1}, Position{3, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := Positions(tc.span, lookup)
			require.NoErrorf(t, err, "Positions = %v, %v, %v, want %v, %v", start, end, err, tc.start, tc.end)
			require.Equalf(t, tc.start, start, "Positions = %v, %v, %v, want %v, %v", start, end, err, tc.start, tc.end)
			require.Equalf(t, tc.end, end, "Positions = %v, %v, %v, want %v, %v", start, end, err, tc.start, tc.end)
		})
	}
	for _, span := range []Span{{}, {source, 0, 9}, {"other", 0, 0}, {source, -1, 0}, {source, 1, 0}, {"", 0, 1}} {
		{
			_, _, err := Positions(span, lookup)
			require.Errorf(t, err, "Positions(%+v) accepted invalid or unresolved span", span)
		}
	}
	{
		_, _, err := Positions(Span{Source: source}, nil)
		require.Errorf(t, err, "Positions accepted a missing lookup")
	}
	empty := func(SourceID) ([]byte, bool) { return nil, true }
	{
		start, end, err := Positions(Span{Source: source}, empty)
		require.NoErrorf(t, err, "empty snapshot point = %v, %v, %v", start, end, err)
		require.Equalf(t, (Position{1, 1}), start, "empty snapshot point = %v, %v, %v", start, end, err)
		require.Equalf(t, end, start, "empty snapshot point = %v, %v, %v", start, end, err)
	}
}
