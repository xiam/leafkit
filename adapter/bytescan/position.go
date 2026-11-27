package bytescan

import (
	"bytes"
	"fmt"
	"unicode/utf8"

	"github.com/xiam/leafkit/tree"
)

// Position uses bytescan's one-based line and zero-based rune column.
type Position struct {
	Line, Column int
}

// Positions resolves a UTF-8 text span against its external snapshot. Its
// columns use bytescan's convention. tree.Positions reports byte columns.
func Positions(span tree.Span, lookup tree.SourceLookup) (start, end Position, err error) {
	if span.Source == "" || span.Start < 0 || span.End < span.Start || lookup == nil {
		return start, end, fmt.Errorf("bytescan adapter: invalid span or source lookup")
	}
	data, ok := lookup(span.Source)
	if !ok || span.End > len(data) {
		return start, end, fmt.Errorf("bytescan adapter: span %+v outside an available snapshot", span)
	}
	return positionAt(data[:span.Start]), positionAt(data[:span.End]), nil
}

func positionAt(prefix []byte) Position {
	return Position{
		Line:   1 + bytes.Count(prefix, []byte{'\n'}),
		Column: utf8.RuneCount(prefix[bytes.LastIndexByte(prefix, '\n')+1:]),
	}
}
