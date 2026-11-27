package tree

import "fmt"

// SourceID names an immutable source snapshot. A revision gets a new ID.
type SourceID string

// Span covers the half-open byte range [Start, End) of Source.
// The zero span is unknown. A nonempty Source can have Start == End == 0.
type Span struct {
	Source     SourceID
	Start, End int
}

func (s Span) valid() bool {
	if s.Source == "" {
		return s == (Span{})
	}
	return s.Start >= 0 && s.End >= s.Start
}

// SourceLookup returns the externally stored bytes for a source snapshot.
// The bytes must be immutable while available under that snapshot ID.
type SourceLookup func(SourceID) ([]byte, bool)

// Position records a one-based line and byte column. LF ends a line.
type Position struct {
	Line, Column int
}

// Positions resolves both endpoints, including a point at end of input.
// It returns an error for unknown spans, unavailable sources or invalid bounds.
func Positions(span Span, lookup SourceLookup) (start, end Position, err error) {
	switch {
	case !span.valid():
		return start, end, fmt.Errorf("tree: invalid span %+v", span)
	case span.Source == "":
		return start, end, fmt.Errorf("tree: unknown span")
	case lookup == nil:
		return start, end, fmt.Errorf("tree: no source lookup for %q", span.Source)
	}
	data, ok := lookup(span.Source)
	if !ok {
		return start, end, fmt.Errorf("tree: source %q unavailable", span.Source)
	}
	if span.End > len(data) {
		return start, end, fmt.Errorf("tree: span end %d exceeds source %q length %d", span.End, span.Source, len(data))
	}

	position := Position{Line: 1, Column: 1}
	for i, b := range data[:span.End] {
		if i == span.Start {
			start = position
		}
		if b == '\n' {
			position.Line++
			position.Column = 1
		} else {
			position.Column++
		}
	}
	if span.Start == span.End {
		start = position
	}
	return start, position, nil
}
