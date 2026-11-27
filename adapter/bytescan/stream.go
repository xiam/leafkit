package bytescan

import (
	"errors"
	"fmt"
	"io"

	scan "github.com/xiam/bytescan"
	"github.com/xiam/leafkit/parser"
	"github.com/xiam/leafkit/tree"
)

// stream retains incomplete trivia across a pause on a fixed lexer. A replay
// lexer instead rebuilds this state from its checkpoint on the next attempt.
type stream struct {
	lexer       scan.Source
	cur         scan.Cursor
	base        int
	offset      int
	lineStart   bool
	breakBefore bool
	leading     []parser.Trivia
}

func consume(raw *stream, id tree.SourceID, trivia, newlines map[scan.TokenType]bool) (parser.Read, error) {
	for {
		token, err := raw.lexer.Next()
		switch {
		case token != nil && err != nil, token == nil && err == nil:
			return parser.Read{}, fmt.Errorf("bytescan adapter: invalid Next outcome")
		case errors.Is(err, scan.ErrMoreInput):
			return parser.Read{Status: parser.NeedInput}, nil
		case err == io.EOF:
			end := raw.base + raw.cur.Cursor().ByteOffset()
			if end != raw.offset {
				return parser.Read{}, fmt.Errorf("bytescan adapter: unreported input before EOF at byte %d", end)
			}
			return parser.Read{
				Status: parser.EndOfInput, Next: parser.Mark{Byte: end}, Trailing: raw.leading,
			}, nil
		case err != nil:
			return parser.Read{}, err
		}

		kind := token.Type()
		span := token.Span()
		start := raw.base + span.Start().ByteOffset()
		end := raw.base + span.End().ByteOffset()
		synthetic := start == end
		if kind == "" || span.Start().Line() < 1 || span.End().Line() < 1 ||
			start != raw.offset || end < start || synthetic != (token.Len() == 0) ||
			synthetic && kind == scan.TokenTypeUnknown {
			return parser.Read{}, fmt.Errorf("bytescan adapter: invalid %q span [%d,%d) at byte %d", kind, start, end, raw.offset)
		}

		raw.offset = end
		lf := span.End().Line() > span.Start().Line()
		newline := kind != scan.TokenTypeUnknown && newlines[kind]
		lineBreak := lf || newline && !synthetic
		if !synthetic {
			raw.lineStart = lf && span.End().Column() == 0
		}
		at := tree.Span{Source: id, Start: start, End: end}
		if kind != scan.TokenTypeUnknown && trivia[kind] {
			if synthetic {
				return parser.Read{}, fmt.Errorf("bytescan adapter: zero-width trivia %q", kind)
			}
			raw.leading = append(raw.leading, parser.Trivia{Kind: parser.TokenKind(kind), Span: at})
			raw.breakBefore = raw.breakBefore || lineBreak
			continue
		}

		tokenKind := parser.TokenKind(kind)
		if kind == scan.TokenTypeUnknown {
			tokenKind = parser.Invalid
		}
		if newline {
			raw.breakBefore = raw.breakBefore || lineBreak
		}
		tok := parser.Token{
			Kind: tokenKind, Text: token.Text(), Span: at, Leading: raw.leading,
			BreakBefore: raw.breakBefore, Synthetic: synthetic,
		}
		read := parser.Read{Status: parser.TokenReady, Token: tok}
		if !synthetic && !newline {
			raw.breakBefore = false
		}
		return read, nil
	}
}
